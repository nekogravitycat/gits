package domain

import "sort"

// Submodule is one entry from a repo's .gitmodules, paired with the gitlink SHA recorded in HEAD's
// tree -- that is, the commit this repo currently pins its dependency to.
type Submodule struct {
	// Name is the .gitmodules section name.
	Name string
	// Path is where the submodule is checked out inside the dependent repo.
	Path string
	// URL is the dependency's remote as the dependent declares it.
	URL string
	// Branch is submodule.<name>.branch, if declared. It is the dependent's own statement of
	// intent about which line of development it tracks.
	Branch string
	// SHA is the gitlink commit recorded in HEAD's tree; empty when it could not be read.
	SHA string
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

// BaselineSource records which of the §7.11 rules chose the comparison branch. It is reported so a
// surprising verdict can be traced back to the reason its baseline was picked.
type BaselineSource string

const (
	// BaselineDeclared: submodule.<name>.branch in the dependent's own .gitmodules.
	BaselineDeclared BaselineSource = "declared"
	// BaselineManifest: the canonical repo's branch field in gits.yaml.
	BaselineManifest BaselineSource = "manifest"
	// BaselineDefaults: defaults.branch.
	BaselineDefaults BaselineSource = "defaults"
)

// Pin is one dependent repo's dependency on one canonical repo.
type Pin struct {
	Dependent     string
	SubmodulePath string
	SHA           string

	// BaselineRef is the remote-tracking ref compared against, e.g. "origin/main".
	//
	// Never the canonical checkout's HEAD: the canonical repo is very often sitting on a feature
	// branch, and using HEAD would make one workspace report different answers on two machines.
	BaselineRef    string
	BaselineSource BaselineSource

	Verdict PinVerdict
	Ahead   int
	Behind  int

	// ContainingBranches names branches that contain a diverged SHA, as a hint about which line of
	// development it came from.
	ContainingBranches []string

	Code    ErrCode
	Message string
	Hint    string
}

// DepGroup collects every repo that depends on one dependency, grouped by the dependency rather
// than by the dependent -- the question being answered is "who is behind on X", not "what does Y
// pin".
type DepGroup struct {
	// Name is the canonical repo's manifest name, or the normalised URL when no canonical checkout
	// exists in the workspace.
	Name string
	URL  string

	// CanonicalPath is the workspace-relative path of the canonical checkout. Empty means the
	// workspace does not contain the dependency, so the determination is incomplete.
	CanonicalPath string

	BaselineRef string
	BaselineSHA string

	Pins []Pin

	// Code is E_NO_CANONICAL when no canonical checkout was found.
	//
	// Reporting an answer as incomplete matters more than looking complete: a caller acting on a
	// partial verdict as though it were whole is the failure this field exists to prevent.
	Code    ErrCode
	Message string
	Hint    string
}

// HasCanonical reports whether a canonical checkout backed this group's verdicts.
func (g DepGroup) HasCanonical() bool { return g.CanonicalPath != "" }

// DistinctSHAs counts how many different commits the dependents pin. Without a canonical checkout
// to compare against, this disagreement count is the only signal left.
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
				// Neither is a problem: "ahead" means the dependent pins work that has not
				// reached the baseline branch yet, which is normal mid-development.
			}
		}
	}
	return s
}

// DeriveVerdict turns a two-way commit count into a verdict.
//
// Both numbers are required, which is why the ancestry check cannot be skipped. A one-way count
// returns a plausible number even for a commit that is not an ancestor at all: a SHA that is
// really "ahead 3, behind 3" reports a bare "3". That reads as "behind 3" and sends the user
// toward a plain submodule update, which cannot work -- the pin sits on a fork of the line, not
// behind it (spec §7.11).
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

// SortGroups orders dependency groups by name so output is deterministic across runs and machines.
func SortGroups(groups []DepGroup) {
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
}
