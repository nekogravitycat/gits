package domain

import "sort"

// Filter is the set of -g/-r/--exclude selectors from the command line (spec §6.2).
type Filter struct {
	Groups   []string
	Repos    []string
	Excludes []string
}

// IsEmpty reports whether the filter selects everything.
func (f Filter) IsEmpty() bool {
	return len(f.Groups) == 0 && len(f.Repos) == 0 && len(f.Excludes) == 0
}

// SelectOpts controls the boundaries applied on top of the user's filter.
type SelectOpts struct {
	// Write marks a command that mutates a repo or pushes to a remote; such commands automatically
	// exclude no-write entries (spec §4).
	Write bool

	// IncludeNoWrite overrides the automatic exclusion. Only `foreach --include-no-write` sets it,
	// where the user has explicitly taken responsibility (spec §7.12).
	IncludeNoWrite bool
}

// Excluded records a repo that was filtered out by a boundary rather than by the user's selectors,
// so the command can still report it instead of silently shrinking its scope.
type Excluded struct {
	Repo Repo
	Code ErrCode
}

// Select resolves the manifest, the user's filter and the command's write boundary into the repos
// to act on.
//
// NOTE: ordering follows the manifest, never the filter, so diffing two runs is meaningful
// (spec §6.5 rule 2). Disabled repos vanish entirely -- not returned, not reported skipped -- as
// on this machine they are not part of the workspace (spec §5.5).
func (m *Manifest) Select(f Filter, opts SelectOpts) (selected []Repo, skipped []Excluded) {
	groups := toSet(f.Groups)
	names := toSet(f.Repos)
	excl := toSet(f.Excludes)

	for _, r := range m.Repos {
		if r.Disabled {
			continue
		}
		if excl[r.Name] {
			continue
		}
		if len(groups) > 0 || len(names) > 0 {
			if !names[r.Name] && !matchesAnyGroup(r, groups) {
				continue
			}
		}
		if r.NoWrite && opts.Write && !opts.IncludeNoWrite {
			skipped = append(skipped, Excluded{Repo: r, Code: ErrNoWrite})
			continue
		}
		selected = append(selected, r)
	}
	return selected, skipped
}

// UnknownSelectors returns the -r/--exclude names that match no manifest entry.
// CRITICAL: a typo like `gits push -r roulete-drawer` must not be read as a silent no-op where the
// user expected an action.
func (m *Manifest) UnknownSelectors(f Filter) []string {
	known := map[string]bool{}
	for _, r := range m.Repos {
		known[r.Name] = true
	}
	var unknown []string
	for _, n := range append(append([]string{}, f.Repos...), f.Excludes...) {
		if !known[n] {
			unknown = append(unknown, n)
		}
	}
	sort.Strings(unknown)
	return dedupe(unknown)
}

// UnknownGroups returns the -g names that no manifest entry carries.
func (m *Manifest) UnknownGroups(f Filter) []string {
	known := map[string]bool{}
	for _, r := range m.Repos {
		for _, g := range r.Groups {
			known[g] = true
		}
	}
	var unknown []string
	for _, g := range f.Groups {
		if !known[g] {
			unknown = append(unknown, g)
		}
	}
	sort.Strings(unknown)
	return dedupe(unknown)
}

func matchesAnyGroup(r Repo, groups map[string]bool) bool {
	for _, g := range r.Groups {
		if groups[g] {
			return true
		}
	}
	return false
}

func toSet(items []string) map[string]bool {
	if len(items) == 0 {
		return nil
	}
	s := make(map[string]bool, len(items))
	for _, i := range items {
		s[i] = true
	}
	return s
}

func dedupe(sorted []string) []string {
	if len(sorted) < 2 {
		return sorted
	}
	out := sorted[:1]
	for _, s := range sorted[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}
