package domain

// RepoState is the single enum an agent can branch on without re-deriving a verdict from five
// booleans (spec §6.5 rule 1).
//
// The raw fields (ahead/behind/dirty) are always emitted alongside it, so collapsing to one state
// never loses information.
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

// statePriority orders states from "most needs a human" to "nothing to do", exactly as fixed by
// spec §6.5:
//
//	error > not-a-repo > missing > detached > no-upstream > diverged > dirty > behind > ahead > clean
//
// A repo is routinely both dirty and behind; the single state must pick deterministically, or two
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

// Priority reports the state's rank in the §6.5 ordering. Unknown states rank above everything so
// a state added later can never be silently swallowed by an existing one.
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

// IsAttention reports whether the state is something other than a fully clean, up-to-date repo.
// It backs `--exit-code` (spec §6.10 code 3) and the highlighting in human output.
func (s RepoState) IsAttention() bool { return s != StateClean }
