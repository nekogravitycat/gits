package domain

import "sort"

// Submodule is one .gitmodules entry paired with the gitlink SHA in HEAD's tree.
type Submodule struct {
	Name   string // .gitmodules section name.
	Path   string // checkout path inside the dependent repo.
	URL    string // dependency remote as the dependent declares it.
	Branch string // submodule.<name>.branch if declared.
	SHA    string // gitlink commit from HEAD's tree; empty when unreadable.
}

// PinVerdict is the comparison outcome for one pinned SHA against its baseline.
type PinVerdict string

const (
	PinUpToDate PinVerdict = "up-to-date"
	PinBehind   PinVerdict = "behind"
	PinAhead    PinVerdict = "ahead"
	PinDiverged PinVerdict = "diverged"
	PinUnknown  PinVerdict = "unknown"
)

// BaselineSource records which §7.11 rule chose the comparison branch, so a surprising verdict
// traces to its baseline.
type BaselineSource string

const (
	BaselineDeclared BaselineSource = "declared" // submodule.<name>.branch in .gitmodules.
	BaselineManifest BaselineSource = "manifest" // canonical repo's branch in gits.yaml.
	BaselineDefaults BaselineSource = "defaults" // defaults.branch.
)

// Pin is one dependent repo's dependency on one canonical repo.
type Pin struct {
	Dependent     string
	SubmodulePath string
	SHA           string

	// BaselineRef is the remote-tracking ref compared against, e.g. "origin/main".
	// CRITICAL: never the canonical checkout's HEAD -- it is often on a feature branch, which would
	// make one workspace report different answers on two machines.
	BaselineRef    string
	BaselineSource BaselineSource

	Verdict PinVerdict
	Ahead   int
	Behind  int

	// ContainingBranches names branches that contain a diverged SHA.
	ContainingBranches []string

	Code    ErrCode
	Message string
	Hint    string
}

// DepGroup collects every repo depending on one dependency, grouped by dependency: the question is
// "who is behind on X", not "what does Y pin".
type DepGroup struct {
	// Name is the canonical repo's manifest name, or the normalised URL when no canonical checkout
	// exists in the workspace.
	Name string
	URL  string

	// CanonicalPath is the workspace-relative path of the canonical checkout. Empty means the
	// workspace lacks the dependency, so the determination is incomplete.
	CanonicalPath string

	BaselineRef string
	BaselineSHA string

	Pins []Pin

	// Code is E_NO_CANONICAL when no canonical checkout was found.
	// CRITICAL: exists to stop a caller acting on a partial verdict as if it were whole.
	Code    ErrCode
	Message string
	Hint    string
}

// HasCanonical reports whether a canonical checkout backed this group's verdicts.
func (g DepGroup) HasCanonical() bool { return g.CanonicalPath != "" }

// DistinctSHAs counts how many different commits the dependents pin. Without a canonical checkout,
// this disagreement count is the only signal left.
func (g DepGroup) DistinctSHAs() int {
	seen := map[string]bool{}
	for _, p := range g.Pins {
		if p.SHA != "" {
			seen[p.SHA] = true
		}
	}
	return len(seen)
}

// DepSummary is the one-line tally appended to status (spec §7.2) and emitted as the "deps" object
// in JSON.
type DepSummary struct {
	Outdated    int
	Diverged    int
	Unknown     int
	NoCanonical int
}

// Any reports whether the dependency scan found anything worth mentioning.
func (d DepSummary) Any() bool {
	return d.Outdated > 0 || d.Diverged > 0 || d.Unknown > 0 || d.NoCanonical > 0
}

// SummarizeDeps tallies dependency groups for the headline summary.
func SummarizeDeps(groups []DepGroup) DepSummary {
	var s DepSummary
	for _, g := range groups {
		if !g.HasCanonical() {
			s.NoCanonical++
		}
		for _, p := range g.Pins {
			switch p.Verdict {
			case PinBehind:
				s.Outdated++
			case PinDiverged:
				s.Diverged++
			case PinUnknown:
				s.Unknown++
			case PinUpToDate, PinAhead:
				// Neither is a problem: "ahead" is normal mid-development.
			}
		}
	}
	return s
}

// DeriveVerdict turns a two-way commit count into a verdict.
//
// CRITICAL: both numbers are required -- the ancestry check cannot be skipped. A one-way count
// reports an "ahead 3, behind 3" fork as a bare "3", which reads as "behind 3" and sends the user
// to a plain submodule update that cannot work (spec §7.11).
func DeriveVerdict(ahead, behind int) PinVerdict {
	switch {
	case ahead > 0 && behind > 0:
		return PinDiverged
	case behind > 0:
		return PinBehind
	case ahead > 0:
		return PinAhead
	default:
		return PinUpToDate
	}
}

// SortGroups orders dependency groups deterministically across runs and machines.
// NOTE: URL breaks name ties (no-canonical groups share URL-basename names); without it order
// depends on map iteration and two runs would not diff cleanly (spec §6.5 rule 2).
func SortGroups(groups []DepGroup) {
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Name != groups[j].Name {
			return groups[i].Name < groups[j].Name
		}
		return groups[i].URL < groups[j].URL
	})
}
