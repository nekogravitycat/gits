package app

import (
	"context"

	"github.com/nekogravitycat/gits/internal/domain"
)

// StatusOptions are the flags specific to `gits status` (spec §7.2).
type StatusOptions struct {
	// Fetch updates remote-tracking refs first, trading milliseconds for accuracy.
	Fetch bool
	// NoDeps suppresses the dependency summary line appended to the report.
	NoDeps bool
}

// StatusResult is one status run, ready to be rendered by either output mode.
//
// Both renderers consume this same value, which is what guarantees the spec's equal-audiences
// rule: there is no fact a human can see on screen that --json does not also carry (spec §3.7).
type StatusResult struct {
	Repos   []domain.RepoStatus
	Skipped []domain.Excluded
	Summary domain.Summary

	// Network records whether this run talked to a remote.
	Network bool

	// Stale marks ahead/behind numbers computed from possibly-outdated local refs.
	//
	// gits does not guess at the real numbers when offline; it says so and moves on. Silently
	// presenting stale counts as live ones is the one thing the spec rules out (spec §6.9).
	Stale bool

	// Deps is the dependency tally appended to the report, nil when --no-deps was given.
	Deps *domain.DepSummary
}

// Status collects the state of every selected repo (spec §7.2).
//
// no-write repos are included: reading is not writing, and leaving them out would make the report
// answer a different question than "what is in this workspace".
func Status(ctx context.Context, env *Env, g Global, opts StatusOptions) (*StatusResult, error) {
	m, err := env.LoadManifest()
	if err != nil {
		return nil, err
	}
	return StatusOf(ctx, env, g, opts, m)
}

// StatusOf runs a status pass against an already-loaded manifest.
//
// `up` needs this: it re-reads the manifest after syncing the root repo, and must report against
// that reloaded list rather than load a third copy (spec §7.1).
func StatusOf(ctx context.Context, env *Env, g Global, opts StatusOptions, m *domain.Manifest) (*StatusResult, error) {
	selected, skipped, err := Select(m, g, domain.SelectOpts{})
	if err != nil {
		return nil, err
	}

	statuses := observeStage(ctx, env, g, m, selected, opts.Fetch, "checking")

	res := &StatusResult{
		Repos:   statuses,
		Skipped: skipped,
		Summary: domain.Summarize(statuses, len(skipped)),
		Network: opts.Fetch,
		Stale:   !opts.Fetch,
	}

	if !opts.NoDeps {
		// The dependency tally rides along with the command people actually run every day.
		// Pain point 4 is that cross-repo drift is invisible, and nobody goes looking for a
		// command they have forgotten exists (spec §7.2).
		groups, derr := DepsOf(ctx, env, g, DepsOptions{}, m)
		if derr == nil {
			sum := domain.SummarizeDeps(groups)
			res.Deps = &sum
		} else {
			// A dependency scan failing must not fail `status`; the primary answer is still
			// worth delivering.
			env.Log.Warnf("dependency summary unavailable: %v", derr)
		}
	}
	return res, nil
}

// Observe gathers the state of each repo concurrently, returning results in manifest order.
func Observe(ctx context.Context, env *Env, g Global, m *domain.Manifest, repos []domain.Repo, fetch bool) []domain.RepoStatus {
	return observeStage(ctx, env, g, m, repos, fetch, "")
}

// observeStage is Observe with an optional progress stage name.
//
// commit and push call Observe for a quick local status check and stay silent; only status's own
// pass -- the one that can involve a real fetch across every repo -- announces itself, so a stage
// name is opt-in rather than baked into Observe itself.
func observeStage(ctx context.Context, env *Env, g Global, m *domain.Manifest, repos []domain.Repo, fetch bool, stage string) []domain.RepoStatus {
	fn := func(ctx context.Context, r domain.Repo) domain.RepoStatus {
		return observeRepo(ctx, env, m, r, fetch)
	}
	if stage == "" {
		return mapRepos(ctx, g.Concurrency(), repos, fn)
	}
	env.Log.Progress(stage, 0, len(repos), "")
	defer env.Log.ProgressDone()
	return mapRepos(ctx, g.Concurrency(), repos, withProgress(env.Log, stage, len(repos), fn))
}

// observeRepo inspects a single repo and derives its state.
func observeRepo(ctx context.Context, env *Env, m *domain.Manifest, r domain.Repo, fetch bool) domain.RepoStatus {
	st := domain.RepoStatus{
		Name:          r.Name,
		Path:          r.EffectivePath(),
		Groups:        r.Groups,
		Description:   r.Description,
		NoWrite:       r.NoWrite,
		DefaultBranch: m.EffectiveBranch(r),
	}
	dir := env.Dir(r)

	exists, err := env.FS.DirExists(dir)
	if err != nil {
		return failStatus(st, domain.ErrGit, MessageOf(err), "")
	}
	if !exists {
		st.State = domain.StateMissing
		st.Code = domain.ErrMissingDir
		st.Message = "directory does not exist"
		st.Hint = "gits clone -r " + r.Name
		return st
	}
	st.Exists = true

	isRepo, err := env.Git.IsRepo(ctx, dir)
	if err != nil {
		return failStatus(st, CodeOf(err), MessageOf(err), "")
	}
	if !isRepo {
		st.State = domain.StateNotARepo
		st.Code = domain.ErrNotARepo
		st.Message = "directory exists but is not a git repository"
		st.Hint = "move " + st.Path + " aside, then: gits clone -r " + r.Name
		return st
	}
	st.IsRepo = true

	if fetch {
		if err := env.Git.Fetch(ctx, dir, m.EffectiveRemote(r)); err != nil {
			// The caller asked for live data and could not have it. Reporting the stale numbers
			// as though they were fresh would be exactly the dishonesty §3.6 rules out, so the
			// repo is marked failed -- with every local fact still populated below, and a code
			// that says whether a retry is worth attempting.
			obs, oerr := env.Git.Status(ctx, dir)
			if oerr == nil {
				applyObservation(&st, m, r, obs)
			}
			return failStatus(st, CodeOf(err), MessageOf(err), fetchHint(CodeOf(err), r.Name))
		}
		st.Fetched = true
	}

	obs, err := env.Git.Status(ctx, dir)
	if err != nil {
		return failStatus(st, CodeOf(err), MessageOf(err), "")
	}
	applyObservation(&st, m, r, obs)

	st.State = domain.DeriveState(domain.StatusFacts{
		Exists:      true,
		IsRepo:      true,
		Detached:    st.Detached,
		HasUpstream: st.Upstream != "",
		Ahead:       st.Ahead,
		Behind:      st.Behind,
		Dirty:       st.Dirty,
	})
	annotate(&st)
	return st
}

// applyObservation copies the raw git observation onto the status value.
func applyObservation(st *domain.RepoStatus, m *domain.Manifest, r domain.Repo, obs RepoObservation) {
	st.Branch = obs.Branch
	st.Detached = obs.Detached
	st.Upstream = obs.Upstream
	st.Ahead = obs.Ahead
	st.Behind = obs.Behind
	st.Dirty = obs.Dirty
	st.OnDefaultBranch = obs.Branch != "" && obs.Branch == m.EffectiveBranch(r)
	if obs.HasSubmodules {
		clean := obs.SubmodulesClean
		st.SubmodulesClean = &clean
	}
}

// annotate attaches the code and the next step for states that need one.
//
// Every skipped or unusual repo gets a command the reader can run verbatim. It costs almost
// nothing to produce and serves both audiences: a human pastes it, an agent executes it (§6.6).
func annotate(st *domain.RepoStatus) {
	switch st.State {
	case domain.StateDetached:
		st.Code = domain.ErrDetached
		st.Message = "HEAD is detached"
		st.Hint = inRepo(st.Path, "git switch "+st.DefaultBranch)
	case domain.StateNoUpstream:
		st.Code = domain.ErrNoUpstream
		st.Message = "branch has no upstream"
		st.Hint = inRepo(st.Path, "git push -u origin "+st.Branch)
	case domain.StateDiverged:
		st.Code = domain.ErrDiverged
		st.Message = "diverged from upstream"
		st.Hint = inRepo(st.Path, "git rebase "+st.Upstream)
	case domain.StateClean, domain.StateDirty, domain.StateAhead, domain.StateBehind,
		domain.StateMissing, domain.StateNotARepo, domain.StateError:
		// Either healthy, or already annotated where the condition was detected.
	}
}

func failStatus(st domain.RepoStatus, code domain.ErrCode, msg, hint string) domain.RepoStatus {
	st.State = domain.StateError
	st.Code = code
	st.Message = msg
	st.Hint = hint
	return st
}

// fetchHint suggests a next step matched to why the fetch failed. Telling someone to retry an
// auth failure would be worse than saying nothing.
func fetchHint(code domain.ErrCode, name string) string {
	switch code {
	case domain.ErrAuth:
		return "check credentials for " + name + " (retrying will not help)"
	case domain.ErrNetwork, domain.ErrTimeout:
		return "gits status --fetch -r " + name
	default:
		return ""
	}
}
