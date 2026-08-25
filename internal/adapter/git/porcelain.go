package git

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nekogravitycat/gits/internal/app"
	"github.com/nekogravitycat/gits/internal/domain"
)

// parseStatus reads `git status --porcelain=v2 --branch` output.
//
// porcelain=v2 is git's documented stable machine interface, yielding branch, upstream,
// ahead/behind and every changed file in one call (spec §10). Record format, one per line:
//
//	# branch.oid <sha> | (initial)
//	# branch.head <branch> | (detached)
//	# branch.upstream <upstream>
//	# branch.ab +<ahead> -<behind>
//	1 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <path>                       ordinary change
//	2 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <X><score> <path><TAB><orig> rename or copy
//	u <XY> <sub> <m1> <m2> <m3> <mW> <h1> <h2> <h3> <path>             unmerged
//	? <path>                                                            untracked
func parseStatus(out string) app.RepoObservation {
	// Clean until a record proves otherwise: a submodule matching its gitlink emits no record.
	obs := app.RepoObservation{SubmodulesClean: true}

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "# "):
			parseHeader(line, &obs)
		case strings.HasPrefix(line, "? "):
			obs.Dirty.Untracked++
		case strings.HasPrefix(line, "! "):
			// Ignored files are not a change.
		case strings.HasPrefix(line, "1 "), strings.HasPrefix(line, "2 "), strings.HasPrefix(line, "u "):
			obs.Dirty.Tracked++
			noteSubmodule(line, &obs)
		}
	}
	return obs
}

func parseHeader(line string, obs *app.RepoObservation) {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return
	}
	switch fields[1] {
	case "branch.oid":
		if fields[2] != "(initial)" {
			obs.Head = fields[2]
		}
	case "branch.head":
		// A branch named "(detached)" is not possible, so this sentinel is unambiguous.
		if fields[2] == "(detached)" {
			obs.Detached = true
			return
		}
		obs.Branch = fields[2]
	case "branch.upstream":
		obs.Upstream = fields[2]
	case "branch.ab":
		// "+12 -3": ahead of upstream by 12, behind by 3.
		if len(fields) < 4 {
			return
		}
		obs.Ahead = parseSigned(fields[2])
		obs.Behind = parseSigned(fields[3])
	}
}

// noteSubmodule records whether a changed entry is a submodule that has drifted from its gitlink.
//
// The <sub> field is 4 chars: "N..." for a plain path, or "S<c><m><u>" for a submodule -- c=commit
// differs from gitlink, m=modified content, u=untracked content. Any of the three means the
// worktree disagrees with committed gitlinks (spec §7.2, §7.3).
func noteSubmodule(line string, obs *app.RepoObservation) {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return
	}
	sub := fields[2]
	if len(sub) != 4 || sub[0] != 'S' {
		return
	}
	obs.HasSubmodules = true
	if sub[1] != '.' || sub[2] != '.' || sub[3] != '.' {
		obs.SubmodulesClean = false
	}
}

// parseSigned reads git's "+12" / "-3" counts as a magnitude.
func parseSigned(s string) int {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "+"), "-")
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// parseGitmodules reads a .gitmodules file into submodule entries.
//
// Parsed directly (not via `git config -f`): .gitmodules is a plain committed file needing no
// subprocess. The URL identifies the dependency; the path deliberately does not, since most
// dependents name the same submodule "proto" (spec §7.11).
//
// A section with no path is malformed (real after a bad merge) and is dropped rather than
// reported as a dependency with an empty path; warn, if non-nil, is called so the drop is not
// silent -- deps is described as gits' original value, and a vanished dependency deserves a
// diagnostic (spec §6 "報告誠實").
func parseGitmodules(content string, warn func(msg string)) []domain.Submodule {
	var subs []domain.Submodule
	var current *domain.Submodule

	flush := func() {
		if current == nil {
			return
		}
		if current.Path == "" {
			if warn != nil {
				warn(fmt.Sprintf("gitmodules: [submodule %q] has no path, skipping", current.Name))
			}
		} else {
			subs = append(subs, *current)
		}
		current = nil
	}

	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		if strings.HasPrefix(line, "[") {
			flush()
			if name, ok := sectionName(line); ok {
				current = &domain.Submodule{Name: name}
			}
			continue
		}
		if current == nil {
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "path":
			current.Path = value
		case "url":
			current.URL = value
		case "branch":
			// The dependent's declared tracking line; outranks other baseline rules, since a repo
			// pinned to the branch it declared is correct, not outdated (spec §7.11).
			current.Branch = value
		}
	}
	flush()
	return subs
}

// sectionName extracts "name" from a `[submodule "name"]` header.
func sectionName(line string) (string, bool) {
	inner := strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
	kind, rest, found := strings.Cut(inner, " ")
	if !found || strings.ToLower(strings.TrimSpace(kind)) != "submodule" {
		return "", false
	}
	return strings.Trim(strings.TrimSpace(rest), `"`), true
}

// parseLsTree extracts gitlink SHAs from `git ls-tree -r HEAD`, keyed by path.
//
// The gitlink is the committed pin, read from the tree not the worktree (see git.go Architecture
// Note).
func parseLsTree(out string) map[string]string {
	pins := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		// "<mode> <type> <object>\t<path>"
		meta, path, found := strings.Cut(line, "\t")
		if !found {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) < 3 || fields[1] != "commit" {
			continue
		}
		pins[strings.TrimSpace(path)] = fields[2]
	}
	return pins
}

// parseAheadBehind reads `git rev-list --left-right --count a...b`, which prints "<left>\t<right>".
func parseAheadBehind(out string) (ahead, behind int) {
	fields := strings.Fields(out)
	if len(fields) < 2 {
		return 0, 0
	}
	ahead, _ = strconv.Atoi(fields[0])
	behind, _ = strconv.Atoi(fields[1])
	return ahead, behind
}
