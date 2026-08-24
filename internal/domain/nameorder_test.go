package domain_test

import (
	"sort"
	"testing"

	"github.com/nekogravitycat/gits/internal/domain"
)

func TestNameLess_CaseInsensitive(t *testing.T) {
	names := []string{"FantasyBaccaratSynthesizer", "amethyst-stack", "Zebra", "alpha"}
	sort.Slice(names, func(i, j int) bool { return domain.NameLess(names[i], names[j]) })

	want := []string{"alpha", "amethyst-stack", "FantasyBaccaratSynthesizer", "Zebra"}
	for i, name := range names {
		if name != want[i] {
			t.Fatalf("sorted = %v, want %v", names, want)
		}
	}
}

func TestNameLess_DigitsCompareNumerically(t *testing.T) {
	names := []string{"repo10", "repo2", "repo1"}
	sort.Slice(names, func(i, j int) bool { return domain.NameLess(names[i], names[j]) })

	want := []string{"repo1", "repo2", "repo10"}
	for i, name := range names {
		if name != want[i] {
			t.Fatalf("sorted = %v, want %v", names, want)
		}
	}
}

func TestNameLess_LeadingZerosDoNotChangeNumericValue(t *testing.T) {
	if domain.NameLess("repo002", "repo2") || domain.NameLess("repo2", "repo002") {
		t.Fatalf("repo002 and repo2 should compare equal, neither less than the other")
	}
}

func TestNameLess_IsAntisymmetric(t *testing.T) {
	cases := [][2]string{
		{"alpha", "beta"},
		{"Alpha", "alpha"},
		{"repo9", "repo10"},
	}
	for _, c := range cases {
		if domain.NameLess(c[0], c[1]) && domain.NameLess(c[1], c[0]) {
			t.Fatalf("NameLess(%q, %q) and NameLess(%q, %q) both true", c[0], c[1], c[1], c[0])
		}
	}
}
