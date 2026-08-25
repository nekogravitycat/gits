package domain

// RepoState is the single enum an agent branches on instead of re-deriving a verdict from five
// booleans (spec §6.5 rule 1). Raw fields (ahead/behind/dirty) are always emitted alongside it,
// so collapsing to one state loses no information.
type RepoState string

const (
	StateClean      RepoState = "clean"
	StateDirty      RepoState = "dirty"
	StateAhead      RepoState = "ahead"
	StateBehind     RepoState = "behind"
	StateDiverged   RepoState = "diverged"
	StateDetached   RepoState = "detached"
	StateNoUpstream RepoState = "no-upstream"
	StateMissing    RepoState = "missing"
	StateNotARepo   RepoState = "not-a-repo"
	StateError      RepoState = "error"
)

// statePriority orders states most-urgent to least, per the fixed §6.5 ordering:
//
//	error > not-a-repo > missing > detached > no-upstream > diverged > dirty > behind > ahead > clean
//
// CRITICAL: a repo is routinely both dirty and behind; this must pick deterministically or two
// machines report the same repo differently.
var statePriority = map[RepoState]int{
	StateError:      100,
	StateNotARepo:   90,
	StateMissing:    80,
	StateDetached:   70,
	StateNoUpstream: 60,
	StateDiverged:   50,
	StateDirty:      40,
	StateBehind:     30,
	StateAhead:      20,
	StateClean:      10,
}

// Priority reports the state's rank in the §6.5 ordering. CRITICAL: unknown states rank above
// everything so a state added later is never silently swallowed by an existing one.
func (s RepoState) Priority() int {
	if p, ok := statePriority[s]; ok {
		return p
	}
	return 1000
}

// MoreUrgentThan reports whether s outranks other in the §6.5 priority ordering.
func (s RepoState) MoreUrgentThan(other RepoState) bool {
	return s.Priority() > other.Priority()
}

// String satisfies fmt.Stringer.
func (s RepoState) String() string { return string(s) }

// IsAttention reports whether the state is anything other than clean+up-to-date. Backs
// --exit-code (spec §6.10 code 3) and human-output highlighting.
func (s RepoState) IsAttention() bool { return s != StateClean }
