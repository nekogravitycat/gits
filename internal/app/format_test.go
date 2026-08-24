package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nekogravitycat/gits/internal/app"
	"github.com/nekogravitycat/gits/internal/domain"
)

func TestFormat_ReportsAnAlreadyCanonicalManifest(t *testing.T) {
	h := newHarness(repo("alpha"), repo("beta"))

	res, err := app.Format(context.Background(), h.env, app.Global{})
	if err != nil {
		t.Fatalf("Format() = %v", err)
	}
	if res.Changed() {
		t.Error("Changed() = true, want false")
	}
	if len(res.Files) != 1 || res.Files[0].File != app.ManifestName {
		t.Fatalf("Files = %+v, want just %s", res.Files, app.ManifestName)
	}
	if res.Files[0].Entries != 2 {
		t.Errorf("Entries = %d, want 2", res.Files[0].Entries)
	}
}

// A machine with local overrides gets both files reported, so the reader can see the local one was
// considered rather than skipped.
func TestFormat_ReportsTheLocalOverrideFileToo(t *testing.T) {
	h := newHarness(repo("alpha"))
	h.store.hasLocal = true

	res, err := app.Format(context.Background(), h.env, app.Global{})
	if err != nil {
		t.Fatalf("Format() = %v", err)
	}
	if len(res.Files) != 2 {
		t.Fatalf("Files = %+v, want both manifest files", res.Files)
	}
	if got := res.Files[1].File; got != app.LocalManifestName {
		t.Errorf("Files[1].File = %q, want %q", got, app.LocalManifestName)
	}
}

// --dry-run reports the change and leaves the files alone, which is what makes
// `gits fmt --dry-run --exit-code` usable as a check in CI.
func TestFormat_DryRunWritesNothing(t *testing.T) {
	h := newHarness(repo("alpha"))
	h.store.unformatted = true

	res, err := app.Format(context.Background(), h.env, app.Global{DryRun: true})
	if err != nil {
		t.Fatalf("Format() = %v", err)
	}
	if !res.Changed() {
		t.Error("Changed() = false, want true")
	}
	if !res.DryRun {
		t.Error("DryRun = false, want true")
	}
	if !h.store.unformatted {
		t.Error("the manifest was rewritten under --dry-run")
	}
}

// A manifest that does not load is not one to rewrite. Reformatting a file gits cannot make sense
// of is how a formatter loses data, so the validation error comes back untouched instead.
func TestFormat_RefusesAManifestThatDoesNotLoad(t *testing.T) {
	h := newHarness(repo("alpha"))
	h.store.loadErr = app.Usagef(domain.ErrManifest, "gits.yaml:7: duplicate repo name")

	_, err := app.Format(context.Background(), h.env, app.Global{})
	if err == nil {
		t.Fatal("Format() = nil, want the load error")
	}
	var ae *app.Error
	if !errors.As(err, &ae) || ae.Code != domain.ErrManifest {
		t.Errorf("Format() = %v, want %s", err, domain.ErrManifest)
	}
	if h.store.formatted != 0 {
		t.Error("the manifest was formatted despite failing to load")
	}
}
