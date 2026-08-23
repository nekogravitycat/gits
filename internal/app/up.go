package app

import "context"

// UpOptions are the flags specific to `gits up` (spec §7.1).
type UpOptions struct {
	// NoClone leaves missing repos missing instead of filling them in.
	NoClone bool
	// NoSubmodules skips submodule updates in both the clone and sync stages.
	NoSubmodules bool
	// NoDeps suppresses the dependency summary at the end.
	NoDeps bool
}

// UpResult is one `up` run, stage by stage.
type UpResult struct {
	// Root is the outcome of syncing the workspace root repo, which happens before anything else.
	Root *RepoResult
	// ManifestStale marks that the repo list may be out of date because the root repo could not
	// be fast-forwarded.
	ManifestStale bool

	Clone  *CloneResult
	Sync   *SyncResult
	Status *StatusResult
}

// Up is the everyday verb: bring the whole workspace up to date, then say where things stand
// (spec §7.1).
//
// The stage order is part of the contract, not an implementation detail:
//
//  1. sync the root repo, because the manifest lives inside it;
//  2. reload the manifest, since the repo list may have just changed;
//  3. clone whatever the refreshed list mentions and this machine lacks;
//  4. sync everything else;
//  5. report.
//
// Steps 1 and 2 are what make pain point 3 actually solvable. Without them, a repo added on
// another machine last night never appears today -- the file that records it has not been pulled
// yet.
func Up(ctx context.Context, env *Env, g Global, opts UpOptions) (*UpResult, error) {
	m, err := env.LoadManifest()
	if err != nil {
		return nil, err
	}

	res := &UpResult{}
	syncRes := &SyncResult{}
	m = syncRootFirst(ctx, env, g, SyncOptions{NoSubmodules: opts.NoSubmodules}, m, syncRes)
	res.Root = syncRes.Root
	res.ManifestStale = syncRes.ManifestStale

	if !opts.NoClone {
		cloneRes, cerr := CloneOf(ctx, env, g, CloneOptions{NoSubmodules: opts.NoSubmodules}, m)
		if cerr != nil {
			return nil, cerr
		}
		res.Clone = cloneRes
	}

	syncRes, err = syncRest(ctx, env, g, SyncOptions{NoSubmodules: opts.NoSubmodules}, m, syncRes)
	if err != nil {
		return nil, err
	}
	res.Sync = syncRes

	// The closing status pass is not redundant with the sync report: it answers "where does the
	// workspace stand now", including the repos sync deliberately left alone.
	statusRes, err := StatusOf(ctx, env, g, StatusOptions{NoDeps: opts.NoDeps}, m)
	if err != nil {
		return nil, err
	}
	res.Status = statusRes
	return res, nil
}

// Failed reports whether any stage had a repo-level failure, which drives exit code 1.
func (r *UpResult) Failed() bool {
	if r.Root != nil && r.Root.Failed() {
		return true
	}
	if r.Clone != nil && AnyFailed(r.Clone.Repos) {
		return true
	}
	if r.Sync != nil && AnyFailed(r.Sync.Repos) {
		return true
	}
	return false
}

// Attention reports whether the closing status found anything the user should look at.
func (r *UpResult) Attention() bool {
	if r.ManifestStale {
		return true
	}
	if r.Status == nil {
		return false
	}
	if r.Status.Deps != nil && r.Status.Deps.Any() {
		return true
	}
	return r.Status.Summary.NeedsAttention()
}
