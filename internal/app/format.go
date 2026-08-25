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

// Changed reports whether any file was not in canonical form -- the one bit a hook or CI check
// branches on.
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
// Why name order matters: the shared manifest is edited by two machines independently, and entries
// in name order land in different parts of the file so git can merge additions without help
// (§10.1). The local file gets the same layout for consistency. Idempotent: a second run reports
// no change and touches nothing, so a hook can run it unconditionally (spec §6.11).
func Format(_ context.Context, env *Env, g Global) (*FormatResult, error) {
	// CRITICAL: load first -- it validates both files, so a duplicate name or an override naming a
	// missing repo is reported with its line number rather than silently reformatted (which is how
	// a formatter loses data).
	if _, err := env.LoadManifest(); err != nil {
		return nil, err
	}

	written, err := env.Store.Format(env.Workspace, !g.DryRun)
	if err != nil {
		return nil, err
	}

	return &FormatResult{Files: written, DryRun: g.DryRun}, nil
}
