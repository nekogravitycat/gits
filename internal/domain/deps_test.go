package domain_test

import (
	"testing"

	"github.com/nekogravitycat/gits/internal/domain"
)

func TestDeriveVerdict(t *testing.T) {
	tests := []struct {
		ahead, behind int
		want          domain.PinVerdict
	}{
		{0, 0, domain.PinUpToDate},
		{0, 3, domain.PinBehind},
		{2, 0, domain.PinAhead},
		{3, 3, domain.PinDiverged},
	}
	for _, tt := range tests {
		if got := domain.DeriveVerdict(tt.ahead, tt.behind); got != tt.want {
			t.Errorf("DeriveVerdict(%d, %d) = %q, want %q", tt.ahead, tt.behind, got, tt.want)
		}
	}
}

// The trap the spec calls out (§7.11): a one-way count reports a bare "3" for a SHA that is really
// ahead 3 and behind 3. Read as "behind 3", it points the user at a plain submodule update that
// cannot work. Only the two-way count distinguishes the two, and they must not be confused.
func TestDeriveVerdict_DivergedIsNotMistakenForBehind(t *testing.T) {
	if got := domain.DeriveVerdict(3, 3); got != domain.PinDiverged {
		t.Fatalf("DeriveVerdict(3, 3) = %q, want %q", got, domain.PinDiverged)
	}
	if got := domain.DeriveVerdict(0, 3); got != domain.PinBehind {
		t.Fatalf("DeriveVerdict(0, 3) = %q, want %q", got, domain.PinBehind)
	}
}

func TestDepGroup_DistinctSHAs(t *testing.T) {
	g := domain.DepGroup{Pins: []domain.Pin{
		{SHA: "a1b2c3d"},
		{SHA: "ca3426c"},
		{SHA: "ca3426c"},
		{SHA: ""}, // unreadable gitlink: not a distinct pin, just an unknown
	}}
	if got := g.DistinctSHAs(); got != 2 {
		t.Errorf("DistinctSHAs() = %d, want 2", got)
	}
}

func TestDepGroup_HasCanonical(t *testing.T) {
	if (domain.DepGroup{CanonicalPath: "shared-proto"}).HasCanonical() != true {
		t.Error("a group with a canonical path has a canonical checkout")
	}
	if (domain.DepGroup{Code: domain.ErrNoCanonical}).HasCanonical() {
		t.Error("a group with no canonical path must report the determination as incomplete")
	}
}

func TestSummarizeDeps(t *testing.T) {
	groups := []domain.DepGroup{
		{
			Name:          "shared-proto",
			CanonicalPath: "shared-proto",
			Pins: []domain.Pin{
				{Verdict: domain.PinUpToDate},
				{Verdict: domain.PinBehind, Behind: 3},
				{Verdict: domain.PinBehind, Behind: 18},
				{Verdict: domain.PinDiverged, Ahead: 3, Behind: 3},
				{Verdict: domain.PinUnknown},
				{Verdict: domain.PinAhead, Ahead: 1},
			},
		},
		{
			Name: "host/game/stack-tools",
			Code: domain.ErrNoCanonical,
			Pins: []domain.Pin{{Verdict: domain.PinUnknown}},
		},
	}
	got := domain.SummarizeDeps(groups)
	want := domain.DepSummary{Outdated: 2, Diverged: 1, Unknown: 2, NoCanonical: 1}
	if got != want {
		t.Errorf("SummarizeDeps() = %+v, want %+v", got, want)
	}
	if !got.Any() {
		t.Error("Any() = false, want true")
	}
}

func TestDepSummary_AnyIsFalseWhenEverythingIsCurrent(t *testing.T) {
	groups := []domain.DepGroup{{
		Name:          "shared-proto",
		CanonicalPath: "shared-proto",
		Pins:          []domain.Pin{{Verdict: domain.PinUpToDate}, {Verdict: domain.PinAhead}},
	}}
	if domain.SummarizeDeps(groups).Any() {
		t.Error("Any() = true, want false when nothing is behind, diverged or unknown")
	}
}

func TestSortGroups(t *testing.T) {
	groups := []domain.DepGroup{{Name: "zeta"}, {Name: "alpha"}, {Name: "mid"}}
	domain.SortGroups(groups)
	for i, want := range []string{"alpha", "mid", "zeta"} {
		if groups[i].Name != want {
			t.Fatalf("groups[%d] = %q, want %q", i, groups[i].Name, want)
		}
	}
}
