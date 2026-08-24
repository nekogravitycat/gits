package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nekogravitycat/gits/internal/app"
	"github.com/nekogravitycat/gits/internal/domain"
)

// write drops a manifest into a fresh workspace and returns its directory and path.
func write(t *testing.T, content string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "gits.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

// writeLocal adds a gits.local.yaml beside an existing manifest.
func writeLocal(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, app.LocalManifestName)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func read(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

// formatShared runs Format and returns the gits.yaml result, asserting that it is the only file
// reported -- a workspace with no local overrides must not invent one.
func formatShared(t *testing.T, dir string, apply bool) app.Formatted {
	t.Helper()
	res, err := (&Store{}).Format(dir, apply)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if len(res) != 1 || res[0].File != app.ManifestName {
		t.Fatalf("Format reported %+v, want just %s", res, app.ManifestName)
	}
	return res[0]
}

// The whole contract in one case: entries in name order, a blank line between them, fields in a
// fixed order, top-level sections in order, and every comment still attached to what it described.
func TestFormat_CanonicalLayout(t *testing.T) {
	dir, path := write(t, `version: 1
repos:
  - url: https://example.com/zed.git
    name: zed
    description: last alphabetically
    groups:
      - tools
  # the root repo, holding the manifest itself
  - name: workspace
    path: "."
    url: https://example.com/workspace.git
  - name: alpha
    no-write: true   # someone else owns it
    url: https://example.com/alpha.git
defaults:
  branch: main
  remote: origin
`)

	res := formatShared(t, dir, true)
	if !res.Changed {
		t.Error("Changed = false, want true: the input was not canonical")
	}
	if res.Entries != 3 {
		t.Errorf("Entries = %d, want 3", res.Entries)
	}
	// workspace is absent from the list on purpose: it was already in the middle and did not
	// move. Naming it would tell the reader an entry changed places when it did not.
	if want := "alpha, zed"; strings.Join(res.Reordered, ", ") != want {
		t.Errorf("Reordered = %v, want [%s]", res.Reordered, want)
	}

	want := `version: 1

defaults:
  remote: origin
  branch: main

repos:
  - name: alpha
    url: https://example.com/alpha.git
    no-write: true # someone else owns it

  # the root repo, holding the manifest itself
  - name: workspace
    path: "."
    url: https://example.com/workspace.git

  - name: zed
    url: https://example.com/zed.git
    groups: [tools]
    description: last alphabetically
`
	if got := read(t, path); got != want {
		t.Errorf("formatted manifest:\n%s\nwant:\n%s", got, want)
	}
}

// An uppercase-led name must not jump ahead of every lowercase one: sorting is case-insensitive,
// not byte order, so "FantasyBaccaratSynthesizer" belongs after "amethyst-stack".
func TestFormat_SortIsCaseInsensitive(t *testing.T) {
	dir, path := write(t, `version: 1
repos:
  - name: FantasyBaccaratSynthesizer
    url: https://example.com/fbs.git
  - name: amethyst-stack
    url: https://example.com/amethyst-stack.git
defaults:
  branch: main
  remote: origin
`)

	res := formatShared(t, dir, true)
	if want := "amethyst-stack, FantasyBaccaratSynthesizer"; strings.Join(res.Reordered, ", ") != want {
		t.Errorf("Reordered = %v, want [%s]", res.Reordered, want)
	}

	got := read(t, path)
	if strings.Index(got, "name: amethyst-stack") > strings.Index(got, "name: FantasyBaccaratSynthesizer") {
		t.Errorf("amethyst-stack should sort before FantasyBaccaratSynthesizer:\n%s", got)
	}
}

// gits.local.yaml gets the same treatment: same sort, same spacing, its own field order. Two files
// sitting side by side should not need two conventions.
func TestFormat_CanonicalLayoutOfTheLocalFile(t *testing.T) {
	dir, _ := write(t, `version: 1
repos:
  - name: alpha
    url: https://example.com/alpha.git

  - name: legacy
    url: https://example.com/legacy.git
`)
	local := writeLocal(t, dir, `overrides:
  - disabled: true   # not on this laptop
    name: legacy
  - no-write: true
    path: ../shared/alpha
    name: alpha
version: 1
`)

	res, err := (&Store{}).Format(dir, true)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("Format reported %+v, want both manifest files", res)
	}
	if res[1].File != app.LocalManifestName || !res[1].Changed || res[1].Entries != 2 {
		t.Errorf("local result = %+v, want a changed %s with 2 entries", res[1], app.LocalManifestName)
	}
	if want := "alpha, legacy"; strings.Join(res[1].Reordered, ", ") != want {
		t.Errorf("Reordered = %v, want [%s]", res[1].Reordered, want)
	}

	want := `version: 1

overrides:
  - name: alpha
    path: ../shared/alpha
    no-write: true

  - name: legacy
    disabled: true # not on this laptop
`
	if got := read(t, local); got != want {
		t.Errorf("formatted overrides:\n%s\nwant:\n%s", got, want)
	}
}

// A workspace with no local overrides is the normal case, not a missing file to complain about.
func TestFormat_IgnoresAnAbsentLocalFile(t *testing.T) {
	dir, _ := write(t, `version: 1

repos:
  - name: alpha
    url: https://example.com/alpha.git
`)

	if res := formatShared(t, dir, true); res.Changed {
		t.Error("Changed = true, want false")
	}
}

// An empty gits.local.yaml is legal and means "no overrides on this machine". There is nothing to
// lay out, so it is left exactly as it is rather than being reshaped on a guess.
func TestFormat_LeavesAnEmptyLocalFileAlone(t *testing.T) {
	dir, _ := write(t, `version: 1

repos:
  - name: alpha
    url: https://example.com/alpha.git
`)
	local := writeLocal(t, dir, "# nothing overridden here yet\n")

	res, err := (&Store{}).Format(dir, true)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if len(res) != 2 || res[1].Changed {
		t.Errorf("Format reported %+v, want the local file reported and unchanged", res)
	}
	if got := read(t, local); got != "# nothing overridden here yet\n" {
		t.Errorf("the empty local file was rewritten:\n%s", got)
	}
}

// Running fmt twice must be a no-op, or it cannot sit in a pre-commit hook (spec §6.11).
func TestFormat_IsIdempotent(t *testing.T) {
	dir, path := write(t, `version: 1
defaults:
  remote: origin
  branch: main
repos:
  - name: beta
    url: https://example.com/beta.git
  - name: alpha
    url: https://example.com/alpha.git
`)
	local := writeLocal(t, dir, `version: 1
overrides:
  - name: beta
    disabled: true
`)

	if _, err := (&Store{}).Format(dir, true); err != nil {
		t.Fatalf("first Format: %v", err)
	}
	first, firstLocal := read(t, path), read(t, local)

	res, err := (&Store{}).Format(dir, true)
	if err != nil {
		t.Fatalf("second Format: %v", err)
	}
	for _, f := range res {
		if f.Changed {
			t.Errorf("%s: Changed = true on the second run, want false", f.File)
		}
		if len(f.Reordered) != 0 {
			t.Errorf("%s: Reordered = %v on the second run, want none", f.File, f.Reordered)
		}
	}
	if got := read(t, path); got != first {
		t.Errorf("second run rewrote the manifest:\n%s\nwant:\n%s", got, first)
	}
	if got := read(t, local); got != firstLocal {
		t.Errorf("second run rewrote the overrides:\n%s\nwant:\n%s", got, firstLocal)
	}
}

// --dry-run reports what would change and writes nothing, to either file.
func TestFormat_DryRunLeavesTheFilesAlone(t *testing.T) {
	original := `version: 1
repos:
  - name: beta
    url: https://example.com/beta.git
  - name: alpha
    url: https://example.com/alpha.git
`
	originalLocal := `overrides:
  - name: beta
    disabled: true
version: 1
`
	dir, path := write(t, original)
	local := writeLocal(t, dir, originalLocal)

	res, err := (&Store{}).Format(dir, false)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	for _, f := range res {
		if !f.Changed {
			t.Errorf("%s: Changed = false, want true", f.File)
		}
	}
	if got := read(t, path); got != original {
		t.Errorf("dry run rewrote the manifest:\n%s", got)
	}
	if got := read(t, local); got != originalLocal {
		t.Errorf("dry run rewrote the overrides:\n%s", got)
	}
}

// A key this build does not know about is kept, after the ones it does. A formatter that deletes
// what it cannot explain is one nobody can leave running in a hook.
func TestFormat_KeepsUnknownKeys(t *testing.T) {
	dir, path := write(t, `version: 1
repos:
  - future-field: someday
    name: alpha
    url: https://example.com/alpha.git
`)

	formatShared(t, dir, true)

	want := `version: 1

repos:
  - name: alpha
    url: https://example.com/alpha.git
    future-field: someday
`
	if got := read(t, path); got != want {
		t.Errorf("formatted manifest:\n%s\nwant:\n%s", got, want)
	}
}

// A groups list carrying a comment stays in block form: flow style has nowhere to put one, so
// inlining it would move the comment somewhere surprising or lose it outright.
func TestFormat_KeepsCommentedGroupsInBlockForm(t *testing.T) {
	dir, path := write(t, `version: 1
repos:
  - name: alpha
    url: https://example.com/alpha.git
    groups:
      - game # the one that ships
      - tools
`)

	formatShared(t, dir, true)
	if got := read(t, path); !strings.Contains(got, "- game # the one that ships") {
		t.Errorf("comment on a group was not preserved:\n%s", got)
	}
}

// The writers must agree on the layout: a manifest gits created, then added to, must already be
// canonical. Without this, every `gits add` would leave the file wanting a fmt.
func TestFormat_IsNoOpOnWhatGitsWrites(t *testing.T) {
	dir := t.TempDir()
	store := &Store{}

	err := store.Create(dir, &domain.Manifest{
		Version:  domain.SchemaVersion,
		Defaults: domain.Defaults{Remote: "origin", Branch: "main"},
		Repos: []domain.Repo{
			{Name: "alpha", URL: "https://example.com/alpha.git", Groups: []string{"game"}},
			{Name: "workspace", Path: domain.RootPath, URL: "https://example.com/workspace.git"},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if res := formatShared(t, dir, true); res.Changed {
		t.Errorf("Create wrote a manifest fmt wants to change:\n%s",
			read(t, filepath.Join(dir, app.ManifestName)))
	}

	added := domain.Repo{
		Name: "beta", URL: "https://example.com/beta.git",
		Groups: []string{"platform"}, NoWrite: true, Description: "owned elsewhere",
	}
	if _, err := store.AddRepo(dir, added, false); err != nil {
		t.Fatalf("AddRepo: %v", err)
	}

	if res := formatShared(t, dir, true); res.Changed {
		t.Errorf("AddRepo wrote a manifest fmt wants to change:\n%s",
			read(t, filepath.Join(dir, app.ManifestName)))
	}
}

// An entry appended by hand is the case fmt exists for: it comes back in name order, with the
// hand-written comments still on the entry they were written about.
func TestFormat_SortsAHandEditedAppend(t *testing.T) {
	dir, path := write(t, `# gits workspace manifest.
version: 1

# Defaults for every repo below.
defaults:
  remote: origin
  branch: main

# Entries are kept sorted by name.
repos:
  - name: beta
    url: https://example.com/beta.git

  # added in a hurry on the laptop
  - name: alpha
    url: https://example.com/alpha.git
`)

	formatShared(t, dir, true)

	want := `# gits workspace manifest.
version: 1

# Defaults for every repo below.
defaults:
  remote: origin
  branch: main

# Entries are kept sorted by name.
repos:
  # added in a hurry on the laptop
  - name: alpha
    url: https://example.com/alpha.git

  - name: beta
    url: https://example.com/beta.git
`
	if got := read(t, path); got != want {
		t.Errorf("formatted manifest:\n%s\nwant:\n%s", got, want)
	}
}
