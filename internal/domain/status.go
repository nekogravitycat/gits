package domain

// Dirty counts uncommitted changes, split as the spec requires (§7.2).
//
// CRITICAL: the split is load-bearing -- tracked modifications make an operation unsafe (commit
// defaults to tracked-only, sync skips only on tracked); conflating with untracked would block
// safe operations or silently commit build output.
type Dirty struct {
	Tracked   int
	Untracked int
}

// Any reports whether there is anything uncommitted at all.
func (d Dirty) Any() bool { return d.Tracked > 0 || d.Untracked > 0 }

// RepoStatus is everything gits observed about one repo plus the single derived State. Raw fields
// stay populated even when State collapses them, so no information is lost (spec §6.5 rule 1).
type RepoStatus struct {
	Name        string
	Path        string
	Groups      []string
	Description string
	NoWrite     bool

	Exists bool
	IsRepo bool

	Branch          string
	DefaultBranch   string
	OnDefaultBranch bool
	Detached        bool

	Upstream string
	Ahead    int
	Behind   int

	Dirty Dirty

	// SubmodulesClean reports whether submodule worktrees match their gitlinks. Nil when the repo
	// declares no submodules, so JSON omits the field rather than asserting a fact about nothing.
	SubmodulesClean *bool

	// Fetched records that live remote refs were pulled before these numbers were computed.
	// NOTE: when false the ahead/behind pair may be stale (spec §6.9).
	Fetched bool

	State   RepoState
	Code    ErrCode
	Message string
	Hint    string
}

// StatusFacts are the raw observations a status derivation consumes.
type StatusFacts struct {
	Exists      bool
	IsRepo      bool
	Detached    bool
	HasUpstream bool
	Ahead       int
	Behind      int
	Dirty       Dirty
	Failed      bool
}

// DeriveState collapses raw observations into the single state an agent branches on, in the fixed
// §6.5 priority (see statePriority).
//
// CRITICAL: only *tracked* changes make a repo dirty -- an untracked scratch file is reported
// (spec example pairs state "behind" with untracked:1) but must not outrank "behind".
func DeriveState(f StatusFacts) RepoState {
	switch {
	case f.Failed:
		return StateError
	case !f.Exists:
		return StateMissing
	case !f.IsRepo:
		return StateNotARepo
	case f.Detached:
		return StateDetached
	case !f.HasUpstream:
		return StateNoUpstream
	case f.Ahead > 0 && f.Behind > 0:
		return StateDiverged
	case f.Dirty.Tracked > 0:
		return StateDirty
	case f.Behind > 0:
		return StateBehind
	case f.Ahead > 0:
		return StateAhead
	default:
		return StateClean
	}
}

// SummaryState is the bucket this repo contributes to in the run summary. Differs from State only
// for no-write repos: permanent local experiments counted as "dirty" every run train the user to
// ignore the tally (spec §7.2); the ● marker still shows on the repo's line.
//
// NOTE: recompute (don't map dirty->clean) so a no-write repo that is both dirty and behind still
// counts as behind.
func (s RepoStatus) SummaryState() RepoState {
	if !s.NoWrite || s.State != StateDirty {
		return s.State
	}
	facts := StatusFacts{
		Exists:      s.Exists,
		IsRepo:      s.IsRepo,
		Detached:    s.Detached,
		HasUpstream: s.Upstream != "",
		Ahead:       s.Ahead,
		Behind:      s.Behind,
		Dirty:       Dirty{Untracked: s.Dirty.Untracked},
	}
	return DeriveState(facts)
}

// Summary is the tally printed at the end of every multi-repo command (spec §6.5).
type Summary struct {
	Total   int
	Clean   int
	Dirty   int
	Ahead   int
	Behind  int
	Missing int
	Failed  int
	Skipped int

	// Attention counts repos neither healthy nor a gits failure: detached HEAD, no upstream,
	// divergence. CRITICAL: without this bucket the counts don't reconcile with Total and a repo
	// vanishes from the tally (spec has no named bucket for these).
	Attention int
}

// NeedsAttention reports whether the run warrants --exit-code value 3 (spec §6.10): nothing
// failed, but something is not up to date.
func (s Summary) NeedsAttention() bool {
	return s.Dirty > 0 || s.Ahead > 0 || s.Behind > 0 || s.Missing > 0 || s.Attention > 0
}

// Summarize tallies statuses into the run summary, honouring the no-write downgrade. excluded
// repos count toward Total (the user selected them) so the buckets always reconcile.
func Summarize(statuses []RepoStatus, excluded int) Summary {
	sum := Summary{Total: len(statuses) + excluded, Skipped: excluded}
	for _, st := range statuses {
		switch st.SummaryState() {
		case StateClean:
			sum.Clean++
		case StateDirty:
			sum.Dirty++
		case StateAhead:
			sum.Ahead++
		case StateBehind:
			sum.Behind++
		case StateMissing:
			sum.Missing++
		case StateError, StateNotARepo:
			sum.Failed++
		case StateDetached, StateNoUpstream, StateDiverged:
			sum.Attention++
		}
	}
	return sum
}
