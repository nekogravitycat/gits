package domain

import "fmt"

// LocalOverrides is the parsed gits.local.yaml (spec §5.5): this machine's exceptions to the
// shared list. It deliberately cannot add repos -- a new repo belongs in the shared manifest, or
// list drift (pain point 3) returns from the other direction.
type LocalOverrides struct {
	Version   int
	Overrides []Override
}

// Override adjusts one existing manifest entry for this machine only.
type Override struct {
	Name string
	Line int

	// Disabled removes the repo from every command on this machine.
	// CRITICAL: it also suppresses the "missing" report -- otherwise a partial checkout shows a
	// false E_MISSING_DIR every run and an agent keeps trying to "fix" it (spec §5.5).
	Disabled bool

	// Path, when non-empty, relocates the checkout.
	Path string

	// NoWrite may only tighten the boundary: a local file cannot grant write access the shared
	// manifest withheld, so false means "no opinion", not "allow writes".
	NoWrite bool
}

// Apply folds the overrides into the manifest in place.
// CRITICAL: every override must name an existing entry (spec §5.6); a typo that silently did
// nothing would leave the user believing a repo was disabled when it was not.
func (m *Manifest) Apply(ov *LocalOverrides, file string) error {
	if ov == nil {
		return nil
	}
	fail := func(line int, format string, args ...any) error {
		return &ManifestError{File: file, Line: line, Msg: fmt.Sprintf(format, args...)}
	}

	if ov.Version != 0 && ov.Version != SchemaVersion {
		return fail(0, "unsupported version %d (expected %d)", ov.Version, SchemaVersion)
	}

	seen := map[string]int{}
	for _, o := range ov.Overrides {
		if o.Name == "" {
			return fail(o.Line, "override entry is missing required field 'name'")
		}
		if prev, dup := seen[o.Name]; dup {
			return fail(o.Line, "duplicate override for %q (first at line %d)", o.Name, prev)
		}
		seen[o.Name] = o.Line

		idx := -1
		for i := range m.Repos {
			if m.Repos[i].Name == o.Name {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fail(o.Line, "override names %q, which is not in the manifest; "+
				"local overrides may only adjust existing entries, not add repos", o.Name)
		}

		if o.Disabled {
			m.Repos[idx].Disabled = true
		}
		if o.Path != "" {
			m.Repos[idx].Path = o.Path
		}
		if o.NoWrite {
			m.Repos[idx].NoWrite = true
		}
	}

	// Re-validate: an override's path can collide or escape the workspace just like a manifest one.
	return m.Validate(file)
}
