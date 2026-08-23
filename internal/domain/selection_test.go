package domain_test

import (
	"strings"
	"testing"

	"github.com/nekogravitycat/gits/internal/domain"
)

func workspace() *domain.Manifest {
	return manifest(
		domain.Repo{Name: "workspace", Path: ".", URL: "https://host/w.git", Groups: []string{"workspace"}},
		domain.Repo{Name: "vendor-sdk", URL: "https://host/dls.git", Groups: []string{"platform"}, NoWrite: true},
		domain.Repo{Name: "shared-proto", URL: "https://host/dp.git", Groups: []string{"platform", "proto"}},
		domain.Repo{Name: "drawer-tool", URL: "https://host/rd.git", Groups: []string{"game"}},
	)
}

func names(repos []domain.Repo) []string {
	out := make([]string, len(repos))
	for i, r := range repos {
		out[i] = r.Name
	}
	return out
}

func assertNames(t *testing.T, got []domain.Repo, want ...string) {
	t.Helper()
	gotNames := names(got)
	if strings.Join(gotNames, ",") != strings.Join(want, ",") {
		t.Errorf("selected %v, want %v", gotNames, want)
	}
}

func TestSelect_NoFilterSelectsEverything(t *testing.T) {
	got, skipped := workspace().Select(domain.Filter{}, domain.SelectOpts{})
	assertNames(t, got, "workspace", "vendor-sdk", "shared-proto", "drawer-tool")
	if len(skipped) != 0 {
		t.Errorf("read-only command skipped %v; no-write repos take part in read-only commands", skipped)
	}
}

func TestSelect_WriteCommandExcludesNoWrite(t *testing.T) {
	got, skipped := workspace().Select(domain.Filter{}, domain.SelectOpts{Write: true})
	assertNames(t, got, "workspace", "shared-proto", "drawer-tool")

	if len(skipped) != 1 || skipped[0].Repo.Name != "vendor-sdk" {
		t.Fatalf("skipped = %+v, want the no-write repo", skipped)
	}
	// Reported, not silently dropped: the user must be able to see why the scope shrank.
	if skipped[0].Code != domain.ErrNoWrite {
		t.Errorf("skip code = %q, want %q", skipped[0].Code, domain.ErrNoWrite)
	}
}

func TestSelect_IncludeNoWriteOverridesTheBoundary(t *testing.T) {
	got, skipped := workspace().Select(domain.Filter{}, domain.SelectOpts{Write: true, IncludeNoWrite: true})
	assertNames(t, got, "workspace", "vendor-sdk", "shared-proto", "drawer-tool")
	if len(skipped) != 0 {
		t.Errorf("skipped = %+v, want none", skipped)
	}
}

func TestSelect_GroupsAreUnioned(t *testing.T) {
	got, _ := workspace().Select(domain.Filter{Groups: []string{"game", "proto"}}, domain.SelectOpts{})
	assertNames(t, got, "shared-proto", "drawer-tool")
}

func TestSelect_RepoAndGroupAreUnioned(t *testing.T) {
	got, _ := workspace().Select(
		domain.Filter{Groups: []string{"game"}, Repos: []string{"workspace"}},
		domain.SelectOpts{},
	)
	assertNames(t, got, "workspace", "drawer-tool")
}

func TestSelect_ExcludeWinsOverSelection(t *testing.T) {
	got, _ := workspace().Select(
		domain.Filter{Groups: []string{"platform"}, Excludes: []string{"shared-proto"}},
		domain.SelectOpts{},
	)
	assertNames(t, got, "vendor-sdk")
}

// A disabled repo is simply not part of this machine's workspace: it is neither selected nor
// reported as skipped, and above all never reported as missing (spec §5.5).
func TestSelect_DisabledIsInvisible(t *testing.T) {
	m := workspace()
	m.Repos[3].Disabled = true

	got, skipped := m.Select(domain.Filter{}, domain.SelectOpts{Write: true})
	assertNames(t, got, "workspace", "shared-proto")
	for _, s := range skipped {
		if s.Repo.Name == "drawer-tool" {
			t.Error("a disabled repo must not appear in the skipped list either")
		}
	}
}

// Explicitly naming a disabled repo must not resurrect it.
func TestSelect_DisabledStaysOutEvenWhenNamed(t *testing.T) {
	m := workspace()
	m.Repos[3].Disabled = true
	got, _ := m.Select(domain.Filter{Repos: []string{"drawer-tool"}}, domain.SelectOpts{})
	if len(got) != 0 {
		t.Errorf("selected %v, want none", names(got))
	}
}

func TestSelect_PreservesManifestOrder(t *testing.T) {
	// Selectors listed in reverse: output order must follow the manifest regardless, or diffing
	// two runs stops being meaningful (spec §6.5 rule 2).
	got, _ := workspace().Select(
		domain.Filter{Repos: []string{"drawer-tool", "workspace"}},
		domain.SelectOpts{},
	)
	assertNames(t, got, "workspace", "drawer-tool")
}

func TestUnknownSelectors(t *testing.T) {
	m := workspace()
	got := m.UnknownSelectors(domain.Filter{
		Repos:    []string{"drawer-tool", "roulete-drawer"},
		Excludes: []string{"nope", "nope"},
	})
	want := []string{"nope", "roulete-drawer"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("UnknownSelectors() = %v, want %v", got, want)
	}
}

func TestUnknownGroups(t *testing.T) {
	got := workspace().UnknownGroups(domain.Filter{Groups: []string{"game", "arcade"}})
	if len(got) != 1 || got[0] != "arcade" {
		t.Errorf("UnknownGroups() = %v, want [arcade]", got)
	}
}

func TestFilter_IsEmpty(t *testing.T) {
	if !(domain.Filter{}).IsEmpty() {
		t.Error("a zero filter is empty")
	}
	if (domain.Filter{Excludes: []string{"a"}}).IsEmpty() {
		t.Error("an exclude-only filter is not empty")
	}
}

func TestApplyOverrides(t *testing.T) {
	m := workspace()
	err := m.Apply(&domain.LocalOverrides{
		Version: 1,
		Overrides: []domain.Override{
			{Name: "drawer-tool", Disabled: true, Line: 4},
			{Name: "shared-proto", Path: "vendor/shared-proto", Line: 6},
			{Name: "workspace", NoWrite: true, Line: 8},
		},
	}, "gits.local.yaml")
	if err != nil {
		t.Fatalf("Apply() = %v, want nil", err)
	}

	drawer, _ := m.Find("drawer-tool")
	if !drawer.Disabled {
		t.Error("drawer-tool should be disabled")
	}
	proto, _ := m.Find("shared-proto")
	if got := proto.EffectivePath(); got != "vendor/shared-proto" {
		t.Errorf("path = %q, want vendor/shared-proto", got)
	}
	wp, _ := m.Find("workspace")
	if !wp.NoWrite {
		t.Error("workspace should have been tightened to no-write")
	}
}

// The boundary may only be tightened. A local file cannot hand back write access that the shared
// manifest withheld (spec §5.5).
func TestApplyOverrides_CannotLoosenNoWrite(t *testing.T) {
	m := workspace()
	if err := m.Apply(&domain.LocalOverrides{
		Version:   1,
		Overrides: []domain.Override{{Name: "vendor-sdk", NoWrite: false}},
	}, "gits.local.yaml"); err != nil {
		t.Fatalf("Apply() = %v, want nil", err)
	}
	r, _ := m.Find("vendor-sdk")
	if !r.NoWrite {
		t.Error("no-write must remain set; overrides can only tighten it")
	}
}

func TestApplyOverrides_UnknownNameIsAnError(t *testing.T) {
	m := workspace()
	err := m.Apply(&domain.LocalOverrides{
		Version:   1,
		Overrides: []domain.Override{{Name: "typo-repo", Disabled: true, Line: 4}},
	}, "gits.local.yaml")
	if err == nil {
		t.Fatal("Apply() = nil, want an error naming the unknown entry")
	}
	if !strings.Contains(err.Error(), "typo-repo") {
		t.Errorf("error = %v, want it to name typo-repo", err)
	}
}

// An override that relocates a repo onto another entry's path has to fail the same way the
// manifest itself would.
func TestApplyOverrides_RevalidatesPaths(t *testing.T) {
	m := workspace()
	err := m.Apply(&domain.LocalOverrides{
		Version:   1,
		Overrides: []domain.Override{{Name: "shared-proto", Path: "drawer-tool", Line: 4}},
	}, "gits.local.yaml")
	if err == nil {
		t.Fatal("Apply() = nil, want a path collision error")
	}
}

func TestApplyOverrides_NilIsANoop(t *testing.T) {
	m := workspace()
	if err := m.Apply(nil, "gits.local.yaml"); err != nil {
		t.Errorf("Apply(nil) = %v, want nil", err)
	}
}

func TestApplyOverrides_DuplicateNameIsAnError(t *testing.T) {
	m := workspace()
	err := m.Apply(&domain.LocalOverrides{
		Version: 1,
		Overrides: []domain.Override{
			{Name: "shared-proto", Disabled: true, Line: 4},
			{Name: "shared-proto", Path: "x", Line: 7},
		},
	}, "gits.local.yaml")
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("Apply() = %v, want a duplicate-override error", err)
	}
}
