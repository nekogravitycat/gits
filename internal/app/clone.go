package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/nekogravitycat/gits/internal/domain"
)

// CloneOptions are the flags specific to `gits clone` (spec §7.6).
type CloneOptions struct {
	// NoSubmodules skips submodule initialisation after a successful clone.
	NoSubmodules bool
}

// CloneResult is one clone run.
type CloneResult struct {
	Repos   []RepoResult
	Skipped []domain.Excluded
	Summary domain.Summary
	DryRun  bool
}

// Clone fills in the repos the manifest lists but this machine does not have (spec §7.6).
//
// This is the command that makes moving between machines work: the manifest travels in the root
// repo, so whatever was added on the other machine shows up here as a missing directory and gets
// created.
//
// no-write repos are included -- cloning a repo you will never write to is exactly why it is in
// the manifest.
func Clone(ctx context.Context, env *Env, g Global, opts CloneOptions) (*CloneResult, error) {
	m, err := env.LoadManifest()
	if err != nil {
		return nil, err
	}
	return CloneOf(ctx, env, g, opts, m)
}

// CloneOf clones against an already-loaded manifest, so `up` can use the list it just refreshed.
func CloneOf(ctx context.Context, env *Env, g Global, opts CloneOptions, m *domain.Manifest) (*CloneResult, error) {
	selected, skipped, err := Select(m, g, domain.SelectOpts{})
	if err != nil {
		return nil, err
	}

	res := &CloneResult{Skipped: skipped, DryRun: g.DryRun}
	var missing []domain.Repo

	for _, r := range selected {
		dir := env.Dir(r)
		exists, derr := env.FS.DirExists(dir)
		if derr != nil {
			res.Repos = append(res.Repos, fail(r, derr))
			continue
		}
		if !exists {
			if r.URL == "" {
				// `gits init` writes an entry with a blank url when a repo has no origin, marked
				// as a to-do. Say so plainly instead of handing git an empty argument.
				res.Repos = append(res.Repos, skip(r, domain.ErrManifest,
					"manifest entry has no url",
					"gits add "+r.Name+" --url <url> --update"))
				continue
			}
			missing = append(missing, r)
			continue
		}

		isRepo, rerr := env.Git.IsRepo(ctx, dir)
		if rerr != nil {
			res.Repos = append(res.Repos, fail(r, rerr))
			continue
		}
		if !isRepo {
			// Something is already sitting at that path. Refusing is the only safe move: the
			// alternative is destroying whatever is there (spec §7.6).
			res.Repos = append(res.Repos, skip(r, domain.ErrNotARepo,
				"directory exists but is not a git repository",
				"move "+r.EffectivePath()+" aside, then: gits clone -r "+r.Name))
			continue
		}
		// Already present: a no-op, which is what makes clone safe to re-run (spec §6.11).
		out := base(r, ActionUpToDate)
		out.Message = "already present"
		res.Repos = append(res.Repos, out)
	}

	if err := CheckMaxRepos(g, len(missing)); err != nil {
		return nil, err
	}
	if len(missing) == 0 {
		res.Summary = SummarizeResults(res.Repos, len(skipped))
		return res, nil
	}

	if err := env.Confirm(g, "clone", cloneQuestion(missing)); err != nil {
		return nil, err
	}

	env.Log.Progress("cloning", 0, len(missing), "")
	cloned := mapRepos(ctx, g.Concurrency(), missing, withProgress(env.Log, "cloning", len(missing), func(ctx context.Context, r domain.Repo) RepoResult {
		return cloneRepo(ctx, env, g, opts, m, r)
	}))
	env.Log.ProgressDone()
	res.Repos = append(res.Repos, cloned...)
	sortResultsByManifest(res.Repos, selected)
	res.Summary = SummarizeResults(res.Repos, len(skipped))
	return res, nil
}

func cloneRepo(ctx context.Context, env *Env, g Global, opts CloneOptions, m *domain.Manifest, r domain.Repo) RepoResult {
	out := base(r, ActionUpdated)
	out.URL = r.URL
	out.Branch = m.EffectiveBranch(r)

	if g.DryRun {
		out.Action = ActionPlanned
		out.Message = "would clone " + r.URL
		return out
	}

	if err := env.Git.Clone(ctx, r.URL, env.Dir(r), out.Branch, !opts.NoSubmodules); err != nil {
		// One repo failing must not stop the rest; the run ends with a list of what went wrong,
		// each with a code saying whether a retry has any chance (spec §3 principle 2, §7.6).
		return fail(r, err)
	}
	out.Message = "cloned"
	return out
}

// sortResultsByManifest restores manifest order after concurrent work, so two runs of the same
// command produce byte-identical reports (spec §6.5 rule 2).
func sortResultsByManifest(results []RepoResult, order []domain.Repo) {
	rank := make(map[string]int, len(order))
	for i, r := range order {
		rank[r.Name] = i
	}
	// Insertion sort: the slice is small and this keeps equal ranks in their original order.
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && rank[results[j].Name] < rank[results[j-1].Name]; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
}

func cloneQuestion(repos []domain.Repo) string {
	names := make([]string, len(repos))
	for i, r := range repos {
		names[i] = r.Name
	}
	return fmt.Sprintf("Clone %d missing repo(s): %s. Continue?", len(repos), strings.Join(names, ", "))
}
