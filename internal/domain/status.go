package domain

// Dirty counts uncommitted changes, split the way the spec requires reporting them (§7.2).
//
// The split is not cosmetic: tracked modifications are what make an operation unsafe, while
// untracked files are merely worth mentioning. `commit` defaults to tracked-only and `sync` skips
// only on tracked changes, so conflating the two would either block safe operations or silently
// commit build output.
type Dirty struct {
	Tracked   int
	Untracked int
}

// Any reports whether there is anything uncommitted at all.
func (d Dirty) Any() bool { return d.Tracked > 0 || d.Untracked > 0 }

// RepoStatus is everything gits observed about one repo, plus the single derived State.
//
// The raw fields are always populated even when State collapses them to one verdict, so no
// information is lost by the collapse (spec §6.5 rule 1).
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

	// SubmodulesClean reports whether the submodule worktrees match their gitlinks. Nil when the
	// repo declares no submodules, so the JSON can omit the field rather than assert a fact about
	// something that does not exist.
	SubmodulesClean *bool

	// Fetched records that live remote refs were pulled before these numbers were computed. When
	// false the ahead/behind pair may be stale (spec §6.9).
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

// DeriveState collapses the raw observations into the single state an agent branches on, applying
// the fixed §6.5 priority:
//
//	error > not-a-repo > missing > detached > no-upstream > diverged > dirty > behind > ahead > clean
//
// Note that only *tracked* changes make a repo dirty. The spec's own example carries
// `"state": "behind"` alongside `"dirty": {"tracked": 0, "untracked": 1}`: an untracked scratch
// file is reported but must not outrank a real "you are two commits behind".
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

// SummaryState is the bucket this repo contributes to in the run summary.
//
// It differs from State for no-write repos only: local experiments in a repo you never commit to
// are expected and permanent, so counting them as "dirty" every single run trains the user to
// ignore the number entirely (spec §7.2). The `●` marker still shows on the repo's own line -- the
// fact is reported, it just does not inflate the tally.
//
// Recomputing (rather than mapping dirty->clean) preserves anything the dirty state was masking:
// a no-write repo that is both dirty and behind still counts as behind.
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

	// Attention counts repos that are neither healthy nor a failure of gits: a detached HEAD, a
	// branch with no upstream, a divergence.
	//
	// The spec's summary list does not name a bucket for these, but without one the counts do not
	// add up to Total -- a repo simply vanishes from the tally. A report whose numbers cannot be
	// reconciled is one nobody trusts, so they are counted here and named in the summary line.
	Attention int
}

// NeedsAttention reports whether anything in the run warrants the --exit-code value 3
// (spec §6.10): nothing failed, but something is not up to date.
func (s Summary) NeedsAttention() bool {
	return s.Dirty > 0 || s.Ahead > 0 || s.Behind > 0 || s.Missing > 0 || s.Attention > 0
}

// Summarize tallies statuses into the run summary, honouring the no-write downgrade.
//
// excluded counts repos a boundary kept out of the run. They count toward the total because the
// user selected them, so that the buckets always reconcile with it.
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
