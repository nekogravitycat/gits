package app

import "context"

// FormatResult is one fmt run: one entry per file that was looked at.
type FormatResult struct {
	// Files carries gits.yaml first and gits.local.yaml second when this machine has one. A
	// missing local file is the normal case and is simply absent from the list.
	Files []Formatted

	// DryRun reports that the changes below were not written.
	DryRun bool
}

// Changed reports whether any file was not in canonical form. This is the one bit a hook or a CI
// check branches on.
func (r *FormatResult) Changed() bool {
	for _, f := range r.Files {
		if f.Changed {
			return true
		}
	}
	return false
}

// Format rewrites gits.yaml, and gits.local.yaml when present, in canonical form (spec §7.13).
//
// The shared manifest is a file two machines edit independently, so its layout is not only a
// matter of taste: entries in name order are what keeps their additions in different parts of the
// file and lets git merge them without help (§10.1). The local file never leaves the machine, but
// it has the same shape and is read the same way, so it gets the same treatment rather than making
// the reader learn two conventions for two files sitting side by side.
//
// Every gits write already produces that layout; fmt is for the file a human edited by hand, and
// for the pre-commit hook that keeps it that way. It is idempotent: a second run reports no change
// and does not touch either file, so a hook can run it unconditionally (spec §6.11).
func Format(_ context.Context, env *Env, g Global) (*FormatResult, error) {
	// Load first. It validates both files, so a manifest with a duplicate name, or an override
	// naming a repo that does not exist, is reported with its line number rather than silently
	// tidied -- reformatting a file gits cannot make sense of is how a formatter loses data.
	if _, err := env.LoadManifest(); err != nil {
		return nil, err
	}

	written, err := env.Store.Format(env.Workspace, !g.DryRun)
	if err != nil {
		return nil, err
	}

	return &FormatResult{Files: written, DryRun: g.DryRun}, nil
}
