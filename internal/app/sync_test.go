package app_test

import (
	"context"
	"testing"

	"github.com/nekogravitycat/gits/internal/app"
	"github.com/nekogravitycat/gits/internal/domain"
)

// The §7.3 decision table, case by case. Each row is a state sync must recognise and a promise
// about what it does -- most importantly, what it refuses to do.
func TestSync_DecisionTable(t *testing.T) {
	tests := []struct {
		name       string
		state      *fakeRepo
		wantAction app.Action
		wantCode   domain.ErrCode
		wantMerge  bool
	}{
		{
			name:       "clean and up to date is left alone",
			state:      &fakeRepo{exists: true, isRepo: true, branch: "main", upstream: "origin/main"},
			wantAction: app.ActionUpToDate,
		},
		{
			name:       "pure behind fast-forwards",
			state:      &fakeRepo{exists: true, isRepo: true, branch: "main", upstream: "origin/main", behind: 3},
			wantAction: app.ActionUpdated,
			wantMerge:  true,
		},
		{
			name:       "pure ahead is left alone",
			state:      &fakeRepo{exists: true, isRepo: true, branch: "main", upstream: "origin/main", ahead: 2},
			wantAction: app.ActionUpToDate,
		},
		{
			name: "uncommitted tracked changes are never touched",
			state: &fakeRepo{
				exists: true, isRepo: true, branch: "main", upstream: "origin/main",
				behind: 3, dirty: domain.Dirty{Tracked: 1},
			},
			wantAction: app.ActionSkipped,
			wantCode:   domain.ErrDirty,
		},
		{
			name: "detached HEAD is skipped",
			state: &fakeRepo{
				exists: true, isRepo: true, detached: true, upstream: "origin/main", behind: 1,
			},
			wantAction: app.ActionSkipped,
			wantCode:   domain.ErrDetached,
		},
		{
			name:       "no upstream is skipped",
			state:      &fakeRepo{exists: true, isRepo: true, branch: "feature/x"},
			wantAction: app.ActionSkipped,
			wantCode:   domain.ErrNoUpstream,
		},
		{
			name: "diverged is skipped rather than merged",
			state: &fakeRepo{
				exists: true, isRepo: true, branch: "main", upstream: "origin/main", ahead: 1, behind: 2,
			},
			wantAction: app.ActionSkipped,
			wantCode:   domain.ErrDiverged,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(repo("alpha"))
			h.setRepo("alpha", tt.state)

			res, err := app.Sync(context.Background(), h.env, app.Global{}, app.SyncOptions{})
			if err != nil {
				t.Fatalf("Sync() = %v", err)
			}

			got, ok := resultFor(res.Repos, "alpha")
			if !ok {
				t.Fatalf("no result for alpha in %+v", res.Repos)
			}
			if got.Action != tt.wantAction {
				t.Errorf("Action = %q, want %q (%s)", got.Action, tt.wantAction, got.Message)
			}
			if got.Code != tt.wantCode {
				t.Errorf("Code = %q, want %q", got.Code, tt.wantCode)
			}
			if merged := h.git.didCall("merge:" + h.dirOf("alpha")); merged != tt.wantMerge {
				t.Errorf("merged = %v, want %v", merged, tt.wantMerge)
			}
		})
	}
}

// Untracked files must not block a fast-forward: the merge does not touch them, and refusing over
// a stray scratch file would make sync useless in everyday work.
func TestSync_UntrackedFilesDoNotBlockFastForward(t *testing.T) {
	h := newHarness(repo("alpha"))
	h.setRepo("alpha", &fakeRepo{
		exists: true, isRepo: true, branch: "main", upstream: "origin/main",
		behind: 2, dirty: domain.Dirty{Untracked: 3},
	})

	res, err := app.Sync(context.Background(), h.env, app.Global{}, app.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync() = %v", err)
	}
	got, _ := resultFor(res.Repos, "alpha")
	if got.Action != app.ActionUpdated {
		t.Errorf("Action = %q, want %q", got.Action, app.ActionUpdated)
	}
}

// Every skipped repo carries a command the reader can run, not just a reason.
func TestSync_SkippedReposCarryARunnableNextStep(t *testing.T) {
	h := newHarness(repo("alpha"))
	h.setRepo("alpha", &fakeRepo{
		exists: true, isRepo: true, branch: "main", upstream: "origin/main", ahead: 1, behind: 2,
	})

	res, _ := app.Sync(context.Background(), h.env, app.Global{}, app.SyncOptions{})
	got, _ := resultFor(res.Repos, "alpha")
	if got.Hint == "" {
		t.Fatal("a diverged repo must come with a next step, not just a reason")
	}
	if want := "cd alpha && git rebase origin/main"; got.Hint != want {
		t.Errorf("Hint = %q, want %q", got.Hint, want)
	}
}

// --dry-run reports the plan and changes nothing.
func TestSync_DryRunDoesNotMerge(t *testing.T) {
	h := newHarness(repo("alpha"))
	h.setRepo("alpha", &fakeRepo{
		exists: true, isRepo: true, branch: "main", upstream: "origin/main", behind: 4,
	})

	res, err := app.Sync(context.Background(), h.env, app.Global{DryRun: true}, app.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync() = %v", err)
	}
	got, _ := resultFor(res.Repos, "alpha")
	if got.Action != app.ActionPlanned {
		t.Errorf("Action = %q, want %q", got.Action, app.ActionPlanned)
	}
	if h.git.didCall("merge:" + h.dirOf("alpha")) {
		t.Error("--dry-run merged; it must change nothing")
	}
	// Fetching is still expected: the plan has to be computed against live refs.
	if !h.git.didCall("fetch:" + h.dirOf("alpha")) {
		t.Error("--dry-run should still fetch, so the plan reflects reality")
	}
}

// A no-write repo still gets pulled. Fetching is read-only as far as the remote is concerned, and
// leaving it stale would defeat the reason it is in the workspace.
func TestSync_IncludesNoWriteRepos(t *testing.T) {
	h := newHarness(repo("alpha"), repo("vendor", noWrite))
	h.setRepo("vendor", &fakeRepo{
		exists: true, isRepo: true, branch: "main", upstream: "origin/main", behind: 1,
	})

	res, err := app.Sync(context.Background(), h.env, app.Global{}, app.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync() = %v", err)
	}
	got, ok := resultFor(res.Repos, "vendor")
	if !ok {
		t.Fatal("a no-write repo must still be synced")
	}
	if got.Action != app.ActionUpdated {
		t.Errorf("Action = %q, want %q", got.Action, app.ActionUpdated)
	}
}

// sync does not clone. Keeping the two apart is what lets sync be the precise tool and `up` the
// one that does everything.
func TestSync_DoesNotCloneMissingRepos(t *testing.T) {
	h := newHarness(repo("alpha"))
	h.remove("alpha")

	res, _ := app.Sync(context.Background(), h.env, app.Global{}, app.SyncOptions{})
	got, _ := resultFor(res.Repos, "alpha")
	if got.Code != domain.ErrMissingDir {
		t.Errorf("Code = %q, want %q", got.Code, domain.ErrMissingDir)
	}
	if h.git.didCall("clone:" + h.dirOf("alpha")) {
		t.Error("sync cloned a missing repo; that is `gits clone`'s job")
	}
	if got.Hint != "gits clone -r alpha" {
		t.Errorf("Hint = %q, want the clone command", got.Hint)
	}
}

// After a successful fast-forward, submodule worktrees are brought in line with their gitlinks --
// otherwise the build no longer matches what was just pulled.
func TestSync_UpdatesSubmodulesAfterFastForward(t *testing.T) {
	h := newHarness(repo("alpha"))
	h.setRepo("alpha", &fakeRepo{
		exists: true, isRepo: true, branch: "main", upstream: "origin/main",
		behind: 1, hasSubmodules: true, submodulesClean: true,
	})

	if _, err := app.Sync(context.Background(), h.env, app.Global{}, app.SyncOptions{}); err != nil {
		t.Fatalf("Sync() = %v", err)
	}
	if !h.git.didCall("submodule-update:" + h.dirOf("alpha")) {
		t.Error("submodules were not updated after a fast-forward")
	}
}

func TestSync_NoSubmodulesSkipsTheUpdate(t *testing.T) {
	h := newHarness(repo("alpha"))
	h.setRepo("alpha", &fakeRepo{
		exists: true, isRepo: true, branch: "main", upstream: "origin/main",
		behind: 1, hasSubmodules: true,
	})

	_, err := app.Sync(context.Background(), h.env, app.Global{}, app.SyncOptions{NoSubmodules: true})
	if err != nil {
		t.Fatalf("Sync() = %v", err)
	}
	if h.git.didCall("submodule-update:" + h.dirOf("alpha")) {
		t.Error("--no-submodules still updated submodules")
	}
}

// The root repo is synced before anything else and the manifest is re-read from it, because the
// repo list lives inside that repo. Without this, a repo added on another machine never appears.
func TestSync_SyncsRootFirstThenReloadsTheManifest(t *testing.T) {
	h := newHarness(repo("workspace", atRoot), repo("alpha"))
	h.setRepo("workspace", &fakeRepo{
		exists: true, isRepo: true, branch: "main", upstream: "origin/main", behind: 1,
	})

	// After the root repo advances, the manifest names a repo it did not before.
	h.store.onReload = func(n int) *domain.Manifest {
		if n < 2 {
			return nil
		}
		return &domain.Manifest{
			Version:  domain.SchemaVersion,
			Defaults: domain.Defaults{Remote: "origin", Branch: "main"},
			Repos:    []domain.Repo{repo("workspace", atRoot), repo("alpha"), repo("newcomer")},
		}
	}

	res, err := app.Sync(context.Background(), h.env, app.Global{}, app.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync() = %v", err)
	}

	if res.Root == nil || res.Root.Name != "workspace" {
		t.Fatalf("Root = %+v, want the workspace root repo", res.Root)
	}
	if res.Root.Action != app.ActionUpdated {
		t.Errorf("root Action = %q, want %q", res.Root.Action, app.ActionUpdated)
	}
	if _, ok := resultFor(res.Repos, "newcomer"); !ok {
		t.Error("the repo added by the reloaded manifest was not synced; the list was not re-read")
	}
	// The root must not be synced twice.
	count := 0
	for _, r := range res.Repos {
		if r.Name == "workspace" {
			count++
		}
	}
	if count != 0 {
		t.Errorf("the root repo appears %d times in Repos as well as in Root", count)
	}
}

// When the root repo cannot advance, the run continues on the old list but says so. Carrying on
// quietly is the one thing the spec forbids here.
func TestSync_WarnsWhenTheRepoListMayBeStale(t *testing.T) {
	h := newHarness(repo("workspace", atRoot), repo("alpha"))
	h.setRepo("workspace", &fakeRepo{
		exists: true, isRepo: true, branch: "main", upstream: "origin/main",
		behind: 2, dirty: domain.Dirty{Tracked: 1},
	})

	res, err := app.Sync(context.Background(), h.env, app.Global{}, app.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync() = %v", err)
	}
	if !res.ManifestStale {
		t.Error("ManifestStale = false; a root repo that could not update leaves the list uncertain")
	}
	if res.Root.Code != domain.ErrDirty {
		t.Errorf("root Code = %q, want %q", res.Root.Code, domain.ErrDirty)
	}
	// The rest of the workspace is still synced: one stuck repo must not stop everything.
	if _, ok := resultFor(res.Repos, "alpha"); !ok {
		t.Error("alpha was not synced; a stale list is a warning, not a halt")
	}
}

// A root repo that fails outright invalidates the list for the whole run, so it has to drive the
// failure exit code even though it is not part of Repos.
func TestSyncResult_FailedIncludesTheRootRepo(t *testing.T) {
	h := newHarness(repo("workspace", atRoot))
	h.setRepo("workspace", &fakeRepo{
		exists: true, isRepo: true, branch: "main", upstream: "origin/main",
		behind: 1, fetchErr: &app.GitError{Code: domain.ErrNetwork, Stderr: "could not resolve host"},
	})

	res, err := app.Sync(context.Background(), h.env, app.Global{}, app.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync() = %v", err)
	}
	if !res.Failed() {
		t.Error("Failed() = false, but the root repo's sync failed")
	}
	if res.Root.Code != domain.ErrNetwork {
		t.Errorf("root Code = %q, want %q", res.Root.Code, domain.ErrNetwork)
	}
}

// One repo failing must never stop the others (spec §3 principle 2).
func TestSync_OneFailureDoesNotStopTheRest(t *testing.T) {
	h := newHarness(repo("alpha"), repo("beta"), repo("gamma"))
	h.setRepo("beta", &fakeRepo{
		exists: true, isRepo: true, branch: "main", upstream: "origin/main",
		fetchErr: &app.GitError{Code: domain.ErrAuth, Stderr: "Authentication failed"},
	})

	res, err := app.Sync(context.Background(), h.env, app.Global{}, app.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync() = %v", err)
	}
	if len(res.Repos) != 3 {
		t.Fatalf("got %d results, want 3: every repo must be reported", len(res.Repos))
	}
	failed, _ := resultFor(res.Repos, "beta")
	if failed.Action != app.ActionFailed || failed.Code != domain.ErrAuth {
		t.Errorf("beta = %+v, want a failed result coded E_AUTH", failed)
	}
	// And the advice must not be "try again", which for an auth failure can never work.
	if failed.Code.Retryable() {
		t.Error("E_AUTH must not be reported as retryable")
	}
}

// Output order follows the manifest, not whichever repo finished first.
func TestSync_ResultsFollowManifestOrder(t *testing.T) {
	h := newHarness(repo("zulu"), repo("alpha"), repo("mike"))

	res, err := app.Sync(context.Background(), h.env,
		app.Global{Jobs: 4}, app.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync() = %v", err)
	}
	want := []string{"zulu", "alpha", "mike"}
	for i, name := range want {
		if res.Repos[i].Name != name {
			t.Fatalf("result %d = %q, want %q (order must follow the manifest)", i, res.Repos[i].Name, name)
		}
	}
}
