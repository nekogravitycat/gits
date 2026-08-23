package app

import (
	"context"

	"github.com/nekogravitycat/gits/internal/domain"
)

// AddOptions are the flags specific to `gits add` (spec §7.9).
type AddOptions struct {
	Repo domain.Repo

	// Update permits changing an entry that already exists with different contents. Without it, a
	// conflicting add is an error rather than a silent overwrite.
	Update bool
}

// AddResult is one add run.
type AddResult struct {
	Repo    ListEntry
	Added   bool
	Updated bool
	NoOp    bool
}

// Add registers a single repo in the manifest (spec §7.9).
//
// This is the supported way for a script or an agent to extend the repo list. Editing gits.yaml
// directly is not: an ordinary YAML round-trip erases comments, and the comments are where
// "why is this repo no-write" is written down (spec §5.1).
//
// It writes the manifest and nothing else. The checkout comes later, from `gits clone -r <name>`,
// which keeps a list edit cheap and reversible.
func Add(_ context.Context, env *Env, _ Global, opts AddOptions) (*AddResult, error) {
	if opts.Repo.Name == "" {
		return nil, Usagef(domain.ErrManifest, "add requires a repo name")
	}
	if opts.Repo.URL == "" {
		return nil, Usagef(domain.ErrManifest, "add requires --url").
			WithHint("gits add %s --url <url>", opts.Repo.Name)
	}

	m, err := env.LoadManifest()
	if err != nil {
		return nil, err
	}

	if existing, found := m.Find(opts.Repo.Name); found && !opts.Update {
		if !sameEntry(existing, opts.Repo) {
			return nil, Usagef(domain.ErrManifest,
				"repo %q already exists with different settings", opts.Repo.Name).
				WithHint("gits add %s --update ...", opts.Repo.Name)
		}
	}

	written, err := env.Store.AddRepo(env.Workspace, opts.Repo, opts.Update)
	if err != nil {
		return nil, err
	}

	// Re-read so the reported entry shows resolved defaults rather than what was typed.
	m, err = env.LoadManifest()
	if err != nil {
		return nil, err
	}
	stored, _ := m.Find(opts.Repo.Name)

	return &AddResult{
		Repo: ListEntry{
			Name:        stored.Name,
			Path:        stored.EffectivePath(),
			URL:         stored.URL,
			Branch:      m.EffectiveBranch(stored),
			Remote:      m.EffectiveRemote(stored),
			Groups:      stored.Groups,
			NoWrite:     stored.NoWrite,
			Description: stored.Description,
		},
		Added:   written.Added,
		Updated: written.Updated,
		NoOp:    written.NoOp,
	}, nil
}

// sameEntry reports whether an existing entry already says exactly what the caller is asking for.
//
// An identical add is a no-op with exit code 0, which is what lets an agent re-run its setup
// steps without special-casing "already done" (spec §6.11).
func sameEntry(a, b domain.Repo) bool {
	if a.URL != b.URL || a.EffectivePath() != b.EffectivePath() || a.NoWrite != b.NoWrite {
		return false
	}
	if a.Branch != b.Branch || a.Remote != b.Remote || a.Description != b.Description {
		return false
	}
	if len(a.Groups) != len(b.Groups) {
		return false
	}
	for i := range a.Groups {
		if a.Groups[i] != b.Groups[i] {
			return false
		}
	}
	return true
}
