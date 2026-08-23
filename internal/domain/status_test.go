package domain_test

import (
	"testing"

	"github.com/nekogravitycat/gits/internal/domain"
)

func TestDeriveState_Priority(t *testing.T) {
	tests := []struct {
		name  string
		facts domain.StatusFacts
		want  domain.RepoState
	}{
		{
			name:  "clean",
			facts: domain.StatusFacts{Exists: true, IsRepo: true, HasUpstream: true},
			want:  domain.StateClean,
		},
		{
			name:  "missing outranks everything observable",
			facts: domain.StatusFacts{Exists: false},
			want:  domain.StateMissing,
		},
		{
			name:  "failure outranks missing",
			facts: domain.StatusFacts{Failed: true, Exists: false},
			want:  domain.StateError,
		},
		{
			name:  "exists but not a repo",
			facts: domain.StatusFacts{Exists: true, IsRepo: false},
			want:  domain.StateNotARepo,
		},
		{
			name:  "detached outranks dirty",
			facts: domain.StatusFacts{Exists: true, IsRepo: true, Detached: true, Dirty: domain.Dirty{Tracked: 3}},
			want:  domain.StateDetached,
		},
		{
			name:  "no upstream outranks diverged",
			facts: domain.StatusFacts{Exists: true, IsRepo: true, HasUpstream: false, Ahead: 1, Behind: 1},
			want:  domain.StateNoUpstream,
		},
		{
			name:  "diverged outranks dirty",
			facts: domain.StatusFacts{Exists: true, IsRepo: true, HasUpstream: true, Ahead: 1, Behind: 2, Dirty: domain.Dirty{Tracked: 1}},
			want:  domain.StateDiverged,
		},
		{
			name:  "dirty outranks behind",
			facts: domain.StatusFacts{Exists: true, IsRepo: true, HasUpstream: true, Behind: 2, Dirty: domain.Dirty{Tracked: 1}},
			want:  domain.StateDirty,
		},
		{
			name:  "behind outranks ahead",
			facts: domain.StatusFacts{Exists: true, IsRepo: true, HasUpstream: true, Behind: 2},
			want:  domain.StateBehind,
		},
		{
			name:  "ahead",
			facts: domain.StatusFacts{Exists: true, IsRepo: true, HasUpstream: true, Ahead: 3},
			want:  domain.StateAhead,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := domain.DeriveState(tt.facts); got != tt.want {
				t.Errorf("DeriveState() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Straight from the spec's own JSON example (§6.5): a repo carrying one untracked file alongside
// "behind 2" is reported as behind, not dirty. Untracked scratch files must never outrank a real
// "you are two commits behind".
func TestDeriveState_UntrackedDoesNotOutrankBehind(t *testing.T) {
	got := domain.DeriveState(domain.StatusFacts{
		Exists:      true,
		IsRepo:      true,
		HasUpstream: true,
		Behind:      2,
		Dirty:       domain.Dirty{Tracked: 0, Untracked: 1},
	})
	if got != domain.StateBehind {
		t.Errorf("DeriveState() = %q, want %q", got, domain.StateBehind)
	}
}

func TestRepoState_PriorityOrdering(t *testing.T) {
	// The full §6.5 chain, most urgent first.
	order := []domain.RepoState{
		domain.StateError,
		domain.StateNotARepo,
		domain.StateMissing,
		domain.StateDetached,
		domain.StateNoUpstream,
		domain.StateDiverged,
		domain.StateDirty,
		domain.StateBehind,
		domain.StateAhead,
		domain.StateClean,
	}
	for i := 0; i+1 < len(order); i++ {
		if !order[i].MoreUrgentThan(order[i+1]) {
			t.Errorf("%q should outrank %q", order[i], order[i+1])
		}
	}
}

func TestSummaryState_NoWriteDirtyIsNotCountedAsDirty(t *testing.T) {
	s := domain.RepoStatus{
		NoWrite:  true,
		Exists:   true,
		IsRepo:   true,
		Upstream: "origin/main",
		Dirty:    domain.Dirty{Tracked: 2},
		State:    domain.StateDirty,
	}
	if got := s.SummaryState(); got != domain.StateClean {
		t.Errorf("SummaryState() = %q, want %q", got, domain.StateClean)
	}
	// The repo's own line still reports the truth; only the tally is downgraded.
	if s.State != domain.StateDirty {
		t.Errorf("State was mutated to %q; it must stay %q", s.State, domain.StateDirty)
	}
}

// Downgrading must not swallow a second problem the dirty state was masking.
func TestSummaryState_NoWriteDirtyAndBehindStaysBehind(t *testing.T) {
	s := domain.RepoStatus{
		NoWrite:  true,
		Exists:   true,
		IsRepo:   true,
		Upstream: "origin/main",
		Behind:   4,
		Dirty:    domain.Dirty{Tracked: 2},
		State:    domain.StateDirty,
	}
	if got := s.SummaryState(); got != domain.StateBehind {
		t.Errorf("SummaryState() = %q, want %q", got, domain.StateBehind)
	}
}

func TestSummaryState_WritableDirtyIsUnchanged(t *testing.T) {
	s := domain.RepoStatus{Exists: true, IsRepo: true, Upstream: "origin/main", State: domain.StateDirty}
	if got := s.SummaryState(); got != domain.StateDirty {
		t.Errorf("SummaryState() = %q, want %q", got, domain.StateDirty)
	}
}

func TestSummarize(t *testing.T) {
	statuses := []domain.RepoStatus{
		{State: domain.StateClean},
		{State: domain.StateClean},
		{State: domain.StateDirty},
		{State: domain.StateBehind},
		{State: domain.StateMissing},
		{State: domain.StateError},
		{State: domain.StateNotARepo},
		{State: domain.StateAhead},
		{State: domain.StateDiverged},
	}
	got := domain.Summarize(statuses, 2)
	want := domain.Summary{
		Total: 9, Clean: 2, Dirty: 1, Ahead: 1, Behind: 1, Missing: 1,
		Attention: 1, Failed: 2, Skipped: 2,
	}
	if got != want {
		t.Errorf("Summarize() = %+v, want %+v", got, want)
	}
}

// Every repo has to land in exactly one bucket. A state that falls through the tally disappears
// from the report, and a summary whose parts do not add up to its total is one nobody trusts.
func TestSummarize_EveryStateIsCounted(t *testing.T) {
	all := []domain.RepoState{
		domain.StateClean, domain.StateDirty, domain.StateAhead, domain.StateBehind,
		domain.StateDiverged, domain.StateDetached, domain.StateNoUpstream,
		domain.StateMissing, domain.StateNotARepo, domain.StateError,
	}
	statuses := make([]domain.RepoStatus, 0, len(all))
	for _, st := range all {
		statuses = append(statuses, domain.RepoStatus{State: st})
	}

	s := domain.Summarize(statuses, 0)
	counted := s.Clean + s.Dirty + s.Ahead + s.Behind + s.Missing + s.Attention + s.Failed
	if counted != s.Total {
		t.Errorf("buckets sum to %d but total is %d: %+v", counted, s.Total, s)
	}
}

func TestSummary_Attention(t *testing.T) {
	if (domain.Summary{Total: 3, Clean: 3}).NeedsAttention() {
		t.Error("an all-clean run must not report attention")
	}
	if !(domain.Summary{Total: 3, Clean: 2, Behind: 1}).NeedsAttention() {
		t.Error("a behind repo must report attention")
	}
}

func TestErrCode_Retryable(t *testing.T) {
	// The whole reason codes exist: retrying a network blip can work, retrying an auth failure
	// only burns an agent's budget.
	retryable := []domain.ErrCode{domain.ErrNetwork, domain.ErrTimeout}
	notRetryable := []domain.ErrCode{
		domain.ErrAuth, domain.ErrDirty, domain.ErrDiverged, domain.ErrDetached,
		domain.ErrNoUpstream, domain.ErrMissingDir, domain.ErrNotARepo, domain.ErrNoWrite,
		domain.ErrHookFailed, domain.ErrNoCanonical, domain.ErrManifest, domain.ErrNoWorkspace,
		domain.ErrNeedsYes, domain.ErrMaxRepos,
	}
	for _, c := range retryable {
		if !c.Retryable() {
			t.Errorf("%s should be retryable", c)
		}
	}
	for _, c := range notRetryable {
		if c.Retryable() {
			t.Errorf("%s must not be retryable", c)
		}
	}
}
