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

// Clone fills in the repos the manifest lists but this machine lacks (spec §7.6).
//
// This is what makes moving between machines work: the manifest travels in the root repo, so a
// repo added elsewhere shows up here as a missing directory. no-write repos are included.
func Clone(ctx context.Context, env *Env, g Global, opts CloneOptions) (*CloneResult, error) {
	m, err := env.LoadManifest()
	if err != nil {
		return nil, err
	}
	return CloneOf(ctx, env, g, opts, m)
}

// CloneOf clones against an already-loaded manifest, so `up` can reuse the list it just refreshed.
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
				// `gits init` writes a blank url for a repo with no origin; report it plainly
				// rather than hand git an empty argument.
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
			// CRITICAL: something else occupies that path; cloning would destroy it, so refuse
			// (spec §7.6).
			res.Repos = append(res.Repos, skip(r, domain.ErrNotARepo,
				"directory exists but is not a git repository",
				"move "+r.EffectivePath()+" aside, then: gits clone -r "+r.Name))
			continue
		}
		// Already present: a no-op, which makes clone safe to re-run (spec §6.11).
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
		// CRITICAL: one repo failing must not stop the rest; collect the error with its code
		// (spec §3 principle 2, §7.6).
		return fail(r, err)
	}
	out.Message = "cloned"
	return out
}

// sortResultsByManifest restores manifest order after concurrent work, so two runs of the same
// command produce byte-identical reports (spec §6.5 rule 2).
func sortResultsByManifest(results []RepoResult, order []domain.Repo) {
	sortByManifest(results, order, func(r RepoResult) string { return r.Name })
}

func cloneQuestion(repos []domain.Repo) string {
	names := make([]string, len(repos))
	for i, r := range repos {
		names[i] = r.Name
	}
	return fmt.Sprintf("Clone %d missing repo(s): %s. Continue?", len(repos), strings.Join(names, ", "))
}
