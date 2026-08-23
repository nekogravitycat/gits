package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/nekogravitycat/gits/internal/domain"
)

// PushOptions are the flags specific to `gits push` (spec §7.4).
type PushOptions struct {
	// SetUpstream pushes with -u for a branch that has no upstream yet.
	SetUpstream bool
}

// PushResult is one push run.
type PushResult struct {
	// Planned lists what will be, or would have been, pushed. It is populated before anything is
	// sent so the confirmation prompt and --dry-run describe the same plan that then executes.
	Planned []RepoResult
	Repos   []RepoResult
	Skipped []domain.Excluded
	Summary domain.Summary
	DryRun  bool
}

// Push publishes every repo that is ahead of its upstream (spec §7.4).
//
// no-write repos are excluded automatically, and there is deliberately no force push in v1: a
// caller who needs one can cd into the repo, where the consequences are at least local and visible.
func Push(ctx context.Context, env *Env, g Global, opts PushOptions) (*PushResult, error) {
	m, err := env.LoadManifest()
	if err != nil {
		return nil, err
	}
	selected, skipped, err := Select(m, g, domain.SelectOpts{Write: true})
	if err != nil {
		return nil, err
	}

	// Every repo is inspected first, so the plan shown to the user is the plan that runs.
	statuses := Observe(ctx, env, g, m, selected, false)

	res := &PushResult{Skipped: skipped, DryRun: g.DryRun}
	var toPush []domain.Repo
	for i, st := range statuses {
		r := selected[i]
		decision := planPush(m, r, st, opts)
		if decision.Action == ActionPlanned {
			toPush = append(toPush, r)
		}
		res.Planned = append(res.Planned, decision)
	}

	if err := CheckMaxRepos(g, len(toPush)); err != nil {
		return nil, err
	}

	if len(toPush) == 0 {
		// Nothing to do is a success, not an error: it is exactly what a second run of push
		// should report (spec §6.11).
		res.Repos = res.Planned
		res.Summary = SummarizeResults(res.Repos, len(skipped))
		return res, nil
	}

	// Pushing is outward-facing and visible to everyone else, so it is confirmed by default.
	if err := env.Confirm(g, "push", pushQuestion(toPush)); err != nil {
		return nil, err
	}
	if g.DryRun {
		res.Repos = res.Planned
		res.Summary = SummarizeResults(res.Repos, len(skipped))
		return res, nil
	}

	executed := mapRepos(ctx, g.Concurrency(), toPush, func(ctx context.Context, r domain.Repo) RepoResult {
		return pushRepo(ctx, env, m, r, plannedFor(res.Planned, r.Name), opts)
	})

	// Splice the executed outcomes back into the planned list so that skipped repos stay visible
	// in the report rather than vanishing from it.
	byName := map[string]RepoResult{}
	for _, e := range executed {
		byName[e.Name] = e
	}
	res.Repos = make([]RepoResult, 0, len(res.Planned))
	for _, p := range res.Planned {
		if e, ok := byName[p.Name]; ok {
			res.Repos = append(res.Repos, e)
			continue
		}
		res.Repos = append(res.Repos, p)
	}
	res.Summary = SummarizeResults(res.Repos, len(skipped))
	return res, nil
}

// planPush decides what will happen to one repo, without touching the network.
func planPush(m *domain.Manifest, r domain.Repo, st domain.RepoStatus, opts PushOptions) RepoResult {
	out := base(r, ActionUpToDate)
	out.Branch = st.Branch
	out.Upstream = st.Upstream
	out.Ahead, out.Behind = st.Ahead, st.Behind

	switch st.State {
	case domain.StateMissing, domain.StateNotARepo, domain.StateError:
		out.Action, out.Code = ActionSkipped, st.Code
		out.Message, out.Hint = st.Message, st.Hint
		return out

	case domain.StateDetached:
		out.Action, out.Code = ActionSkipped, domain.ErrDetached
		out.Message = "HEAD is detached"
		out.Hint = "cd " + out.Path + " && git switch " + m.EffectiveBranch(r)
		return out

	case domain.StateDiverged:
		out.Action, out.Code = ActionSkipped, domain.ErrDiverged
		out.Message = fmt.Sprintf("diverged (ahead %d, behind %d)", st.Ahead, st.Behind)
		out.Hint = "cd " + out.Path + " && git rebase " + st.Upstream
		return out

	case domain.StateNoUpstream:
		// Only push a branch with no upstream when the user has actually asked for it; creating a
		// remote branch is not something to do on a guess.
		if !opts.SetUpstream {
			out.Action, out.Code = ActionSkipped, domain.ErrNoUpstream
			out.Message = "branch has no upstream"
			out.Hint = "gits push --set-upstream -r " + r.Name
			return out
		}
		out.Action = ActionPlanned
		out.Message = "would create " + m.EffectiveRemote(r) + "/" + st.Branch
		return out

	case domain.StateClean, domain.StateDirty, domain.StateBehind:
		// Uncommitted work is irrelevant to pushing, and being behind only matters if we are also
		// ahead -- which is the diverged case, already handled.
		return out

	case domain.StateAhead:
		out.Action = ActionPlanned
		out.Commits = st.Ahead
		out.Message = fmt.Sprintf("%d commit(s) to %s", st.Ahead, st.Upstream)
		return out
	}
	return out
}

func pushRepo(ctx context.Context, env *Env, m *domain.Manifest, r domain.Repo, planned RepoResult, opts PushOptions) RepoResult {
	setUpstream := opts.SetUpstream && planned.Upstream == ""
	if err := env.Git.Push(ctx, env.Dir(r), m.EffectiveRemote(r), planned.Branch, setUpstream); err != nil {
		out := fail(r, err)
		out.Branch, out.Upstream = planned.Branch, planned.Upstream
		out.Ahead, out.Behind = planned.Ahead, planned.Behind
		return out
	}
	out := planned
	out.Action = ActionUpdated
	out.Message = fmt.Sprintf("pushed %d commit(s)", planned.Ahead)
	return out
}

func plannedFor(planned []RepoResult, name string) RepoResult {
	for _, p := range planned {
		if p.Name == name {
			return p
		}
	}
	return RepoResult{Name: name}
}

func pushQuestion(repos []domain.Repo) string {
	names := make([]string, len(repos))
	for i, r := range repos {
		names[i] = r.Name
	}
	return fmt.Sprintf("Push %d repo(s): %s. Continue?", len(repos), strings.Join(names, ", "))
}
