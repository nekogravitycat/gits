package app

import (
	"context"

	"github.com/nekogravitycat/gits/internal/domain"
)

// AddOptions are the flags specific to `gits add` (spec §7.9).
type AddOptions struct {
	Repo domain.Repo

	// Update permits overwriting an existing entry; without it a conflicting add is an error.
	Update bool
}

// AddResult is one add run.
type AddResult struct {
	Repo    ListEntry
	Added   bool
	Updated bool
	NoOp    bool
}

// Add registers a single repo in the manifest (spec §7.9). Manifest-only; the checkout comes
// later from `gits clone -r <name>`.
//
// Why not edit gits.yaml directly: a YAML round-trip erases the comments that record why a repo is
// no-write (spec §5.1).
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

	// Re-read so the reported entry shows resolved defaults, not what was typed.
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

// sameEntry reports whether an existing entry already matches the requested add, making an
// identical add a no-op with exit 0 so setup steps stay safe to re-run (spec §6.11).
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
