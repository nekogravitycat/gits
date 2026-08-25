package app

import (
	"context"
	"fmt"

	"github.com/nekogravitycat/gits/internal/domain"
)

// SyncOptions are the flags specific to `gits sync` (spec §7.3).
type SyncOptions struct {
	// NoSubmodules skips the submodule update that follows a successful fast-forward.
	NoSubmodules bool
}

// SyncResult is one sync run.
type SyncResult struct {
	// Root reports the outcome for the workspace root repo, which is synced before everything
	// else. Nil when the manifest declares no root repo.
	Root *RepoResult

	// ManifestStale marks that the repo list may be out of date because the root repo could not
	// be fast-forwarded.
	ManifestStale bool

	Repos   []RepoResult
	Skipped []domain.Excluded
	Summary domain.Summary
}

// Failed reports whether any repo operation failed, including the root repo's own sync.
//
// CRITICAL: the root is checked explicitly because it is not part of Repos; leaving it out would
// let the one repo whose failure invalidates the whole run exit 0.
func (r *SyncResult) Failed() bool {
	if r.Root != nil && r.Root.Failed() {
		return true
	}
	return AnyFailed(r.Repos)
}

// Sync brings every selected repo up to date (spec §7.3).
//
// Timid by design: never touches uncommitted work, never creates a conflict. Anything not
// advanceable by a clean fast-forward is skipped and reported with its resolving command. no-write
// repos are included, since pulling is read-only to the remote.
func Sync(ctx context.Context, env *Env, g Global, opts SyncOptions) (*SyncResult, error) {
	m, err := env.LoadManifest()
	if err != nil {
		return nil, err
	}
	res := &SyncResult{}
	m = syncRootFirst(ctx, env, g, opts, m, res)
	return syncRest(ctx, env, g, opts, m, res)
}

// syncRootFirst syncs the workspace root repo and reloads the manifest from it.
//
// CRITICAL: the manifest lives inside the root repo, so a repo added elsewhere is only visible
// after the root is pulled -- sync the others first and the list used was the old one (§7.1, §10.1).
// Returns the reloaded manifest when the root advanced, the original otherwise.
func syncRootFirst(ctx context.Context, env *Env, g Global, opts SyncOptions, m *domain.Manifest, res *SyncResult) *domain.Manifest {
	root, ok := m.Root()
	if !ok || root.Disabled {
		return m
	}
	// An explicit filter excluding the root repo is the user narrowing scope on purpose.
	if selected, _, err := Select(m, g, domain.SelectOpts{}); err == nil && !containsRepo(selected, root.Name) {
		return m
	}

	out := syncRepo(ctx, env, g, opts, m, root)
	res.Root = &out

	if out.Action != ActionUpdated {
		if out.Action == ActionSkipped || out.Action == ActionFailed {
			// CRITICAL: never carry on silently with a possibly-stale list -- a repo added on the
			// other machine may be absent from this run (spec §7.1 step 2). Flagged so each renderer
			// surfaces it once.
			res.ManifestStale = true
		}
		return m
	}

	reloaded, err := env.LoadManifest()
	if err != nil {
		res.ManifestStale = true
		env.Log.Verbosef("reloading %s after syncing the root repo failed: %v", ManifestName, err)
		return m
	}
	return reloaded
}

func syncRest(ctx context.Context, env *Env, g Global, opts SyncOptions, m *domain.Manifest, res *SyncResult) (*SyncResult, error) {
	selected, skipped, err := Select(m, g, domain.SelectOpts{})
	if err != nil {
		return nil, err
	}
	// The root repo is already done; syncing it twice would double-report it.
	rest := make([]domain.Repo, 0, len(selected))
	for _, r := range selected {
		if res.Root != nil && r.Name == res.Root.Name {
			continue
		}
		rest = append(rest, r)
	}

	if err := CheckMaxRepos(g, len(rest)); err != nil {
		return nil, err
	}

	env.Log.Progress("syncing", 0, len(rest), "")
	res.Repos = mapRepos(ctx, g.Concurrency(), rest, withProgress(env.Log, "syncing", len(rest), func(ctx context.Context, r domain.Repo) RepoResult {
		return syncRepo(ctx, env, g, opts, m, r)
	}))
	env.Log.ProgressDone()
	res.Skipped = skipped
	res.Summary = SummarizeResults(res.Repos, len(skipped))
	return res, nil
}

// syncRepo applies the §7.3 decision table to one repo.
func syncRepo(ctx context.Context, env *Env, g Global, opts SyncOptions, m *domain.Manifest, r domain.Repo) RepoResult {
	dir := env.Dir(r)

	exists, err := env.FS.DirExists(dir)
	if err != nil {
		return fail(r, err)
	}
	if !exists {
		// sync deliberately does not clone; keeping the two separate lets `sync` be precise and
		// `up` do both (spec §7.3).
		return skip(r, domain.ErrMissingDir, "directory does not exist", "gits clone -r "+r.Name)
	}
	if isRepo, rerr := env.Git.IsRepo(ctx, dir); rerr != nil {
		return fail(r, rerr)
	} else if !isRepo {
		return skip(r, domain.ErrNotARepo, "directory exists but is not a git repository", "")
	}

	if err := env.Git.Fetch(ctx, dir, m.EffectiveRemote(r)); err != nil {
		return fail(r, err)
	}

	obs, err := env.Git.Status(ctx, dir)
	if err != nil {
		return fail(r, err)
	}

	out := base(r, ActionUpToDate)
	out.Branch = obs.Branch
	out.Upstream = obs.Upstream
	out.Ahead, out.Behind = obs.Ahead, obs.Behind

	switch {
	case obs.Dirty.Tracked > 0:
		// Untracked files are not a reason to skip: a fast-forward does not touch them.
		out.Action, out.Code = ActionSkipped, domain.ErrDirty
		out.Message = fmt.Sprintf("uncommitted changes in %d tracked file(s)", obs.Dirty.Tracked)
		out.Hint = "gits commit -r " + r.Name
		return out

	case obs.Detached:
		out.Action, out.Code = ActionSkipped, domain.ErrDetached
		out.Message = "HEAD is detached"
		out.Hint = inRepo(out.Path, "git switch "+m.EffectiveBranch(r))
		return out

	case obs.Upstream == "":
		out.Action, out.Code = ActionSkipped, domain.ErrNoUpstream
		out.Message = "branch has no upstream"
		out.Hint = inRepo(out.Path, "git push -u "+m.EffectiveRemote(r)+" "+obs.Branch)
		return out

	case obs.Ahead > 0 && obs.Behind > 0:
		out.Action, out.Code = ActionSkipped, domain.ErrDiverged
		out.Message = fmt.Sprintf("diverged: ahead %d, behind %d", obs.Ahead, obs.Behind)
		// A concrete rebase command, not "needs manual attention" (spec §7.3 step 4).
		out.Hint = inRepo(out.Path, "git rebase "+obs.Upstream)
		return out

	case obs.Behind == 0:
		if obs.Ahead > 0 {
			out.Message = fmt.Sprintf("ahead %d", obs.Ahead)
			out.Hint = "gits push -r " + r.Name
		}
		return out
	}

	// Pure behind: a clean fast-forward is possible.
	if g.DryRun {
		out.Action = ActionPlanned
		out.Commits = obs.Behind
		out.Message = fmt.Sprintf("would fast-forward %d commit(s) to %s", obs.Behind, obs.Upstream)
		return out
	}

	if err := env.Git.MergeFFOnly(ctx, dir, obs.Upstream); err != nil {
		return fail(r, err)
	}
	out.Action = ActionUpdated
	out.Commits = obs.Behind
	out.Message = fmt.Sprintf("fast-forwarded %d commit(s)", obs.Behind)

	if !opts.NoSubmodules && obs.HasSubmodules {
		// CRITICAL: not optional by default -- skipping leaves submodule worktrees on the old SHA,
		// so the build no longer matches the gitlinks just updated (spec §7.3 step 3).
		if err := env.Git.SubmoduleUpdate(ctx, dir); err != nil {
			out.Action = ActionFailed
			out.Code = CodeOf(err)
			out.Message = "fast-forwarded, but submodule update failed: " + MessageOf(err)
			out.Hint = inRepo(out.Path, "git submodule update --init --recursive")
		}
	}
	return out
}

func containsRepo(repos []domain.Repo, name string) bool {
	for _, r := range repos {
		if r.Name == name {
			return true
		}
	}
	return false
}
