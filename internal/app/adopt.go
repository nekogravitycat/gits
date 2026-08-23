package app

import (
	"context"
	"strings"

	"github.com/nekogravitycat/gits/internal/domain"
)

// AdoptOptions are the flags specific to `gits adopt` (spec §7.8).
type AdoptOptions struct {
	// Groups is applied to every newly adopted entry.
	Groups []string
	// NoWrite marks every newly adopted entry as no-write.
	NoWrite bool
}

// AdoptResult is one adopt run.
type AdoptResult struct {
	Adopted []ListEntry
	Skipped []string

	// Missing names manifest entries with no directory on disk. Reported only -- adopt registers
	// repos, it does not fetch them.
	Missing []string

	// URLMismatch names repos whose origin disagrees with what the manifest records, which usually
	// means the checkout points at a fork or an old host.
	URLMismatch []URLMismatch

	DryRun bool
}

// URLMismatch is one manifest-vs-disk disagreement about a repo's origin.
type URLMismatch struct {
	Name        string
	ManifestURL string
	ActualURL   string
}

// Adopt registers repos that exist on disk but are absent from the manifest (spec §7.8).
//
// It is the mirror image of clone: clone materialises what the list knows about, adopt teaches the
// list about what is already here. Together they close both directions of list drift.
func Adopt(ctx context.Context, env *Env, g Global, opts AdoptOptions) (*AdoptResult, error) {
	m, err := env.LoadManifest()
	if err != nil {
		return nil, err
	}

	found, err := ScanWorkspace(ctx, env)
	if err != nil {
		return nil, err
	}

	res := &AdoptResult{DryRun: g.DryRun}
	byPath := map[string]domain.Repo{}
	for _, r := range m.Repos {
		byPath[r.EffectivePath()] = r
	}

	var candidates []Discovered
	for _, d := range found {
		if known, listed := byPath[d.Path]; listed {
			// Already registered: check it still points where the manifest says it does.
			if d.URL != "" && known.URL != "" && !domain.SameRepoURL(d.URL, known.URL) {
				res.URLMismatch = append(res.URLMismatch, URLMismatch{
					Name: known.Name, ManifestURL: known.URL, ActualURL: d.URL,
				})
			}
			continue
		}
		candidates = append(candidates, d)
	}

	// The other direction of drift: listed, but not here. Reported and never acted on -- adopting
	// is a manifest edit, and silently cloning would be a much larger action than the user asked
	// for (spec §7.8).
	for _, r := range m.Repos {
		if r.Disabled {
			continue
		}
		if exists, derr := env.FS.DirExists(env.Dir(r)); derr == nil && !exists {
			res.Missing = append(res.Missing, r.Name)
		}
	}

	if err := CheckMaxRepos(g, len(candidates)); err != nil {
		return nil, err
	}

	for _, d := range candidates {
		entry, include, err := adoptEntry(env, g, m, d, opts)
		if err != nil {
			return nil, err
		}
		if !include {
			res.Skipped = append(res.Skipped, d.Name)
			continue
		}
		if !g.DryRun {
			if _, werr := env.Store.AddRepo(env.Workspace, entry, false); werr != nil {
				return nil, werr
			}
		}
		res.Adopted = append(res.Adopted, ListEntry{
			Name:    entry.Name,
			Path:    entry.EffectivePath(),
			URL:     entry.URL,
			Branch:  m.EffectiveBranch(entry),
			Remote:  m.EffectiveRemote(entry),
			Groups:  entry.Groups,
			NoWrite: entry.NoWrite,
		})
	}
	return res, nil
}

// adoptEntry builds the manifest entry for one discovered repo, asking the user about it when
// there is a terminal to ask on.
//
// With -y (or no terminal) it takes everything and applies the flags, which is what makes adopt
// usable unattended rather than only from a keyboard (spec §7.8).
func adoptEntry(env *Env, g Global, m *domain.Manifest, d Discovered, opts AdoptOptions) (domain.Repo, bool, error) {
	entry := domain.Repo{
		Name:    d.Name,
		URL:     d.URL,
		Groups:  opts.Groups,
		NoWrite: opts.NoWrite,
	}
	if d.Path != d.Name {
		entry.Path = d.Path
	}
	// A branch equal to the manifest default is left unwritten, so the entry says only what is
	// actually specific to this repo (spec §7.8).
	if d.Branch != "" && d.Branch != m.EffectiveBranch(entry) {
		entry.Branch = d.Branch
	}

	if g.Yes || g.DryRun || !env.Prompt.IsInteractive() {
		return entry, true, nil
	}

	include, err := env.Prompt.Confirm("Add " + d.Name + " (" + displayURL(d.URL) + ") to the manifest?")
	if err != nil || !include {
		return entry, false, err
	}

	groups, err := env.Prompt.Line("  groups (comma separated, blank for none) > ")
	if err != nil {
		return entry, false, err
	}
	if g := splitGroups(groups); len(g) > 0 {
		entry.Groups = g
	}

	noWrite, err := env.Prompt.Confirm("  mark as no-write (never commit, push or foreach here)?")
	if err != nil {
		return entry, false, err
	}
	entry.NoWrite = entry.NoWrite || noWrite

	return entry, true, nil
}

func splitGroups(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func displayURL(url string) string {
	if url == "" {
		return "no origin"
	}
	return url
}
