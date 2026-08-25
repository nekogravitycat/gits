package domain

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// SchemaVersion is the manifest structure version this build understands (spec §5.3).
// CRITICAL: a manifest declaring a higher version is refused, not guessed at.
const SchemaVersion = 1

// Default values applied when the manifest omits them (spec §5.2).
const (
	DefaultRemote = "origin"
	DefaultBranch = "main"
)

// RootPath marks the entry whose checkout is the workspace directory itself (spec §5.4) -- the only
// path allowed to resolve outside the "subdirectory of the root" rule.
const RootPath = "."

// Repo is one entry in the manifest.
//
// NOTE: optional fields are kept as written -- empty means "inherit from defaults", not the default
// value. Resolution happens in the Effective* accessors so a round-trip through the writer never
// materialises an inherited value into the file.
type Repo struct {
	Name        string
	URL         string
	Path        string
	Branch      string
	Remote      string
	Groups      []string
	NoWrite     bool
	Description string

	// URLDeclared records that the entry carried a url key, even an empty one, so `gits init` can
	// write a URL-less to-do entry (§7.7): a missing key is a structural error, a blank value an
	// unfinished entry only URL-needing commands flag.
	URLDeclared bool

	// Line is the 1-based line the entry starts on, for E_MANIFEST diagnostics (spec §5.6). Zero
	// when the entry did not come from a file.
	Line int

	// Disabled comes from gits.local.yaml (spec §5.5), not the manifest. A disabled repo takes part
	// in no command and is never reported as missing.
	Disabled bool
}

// Manifest is the parsed, override-applied repo list for a workspace.
type Manifest struct {
	Version  int
	Defaults Defaults
	Repos    []Repo
}

// Defaults holds the manifest-wide fallbacks for per-repo fields.
type Defaults struct {
	Remote string
	Branch string
}

// EffectiveRemote returns the remote name to use for this entry.
func (m *Manifest) EffectiveRemote(r Repo) string {
	if r.Remote != "" {
		return r.Remote
	}
	if m.Defaults.Remote != "" {
		return m.Defaults.Remote
	}
	return DefaultRemote
}

// EffectiveBranch returns the entry's default branch.
// NOTE: "default branch" is only a comparison baseline (spec §5.3) -- it selects the clone target
// and tells status which branch is "not the usual one"; gits never switches branches.
func (m *Manifest) EffectiveBranch(r Repo) string {
	if r.Branch != "" {
		return r.Branch
	}
	if m.Defaults.Branch != "" {
		return m.Defaults.Branch
	}
	return DefaultBranch
}

// EffectivePath returns the entry's workspace-relative path in slash form.
func (r Repo) EffectivePath() string {
	if r.Path != "" {
		return path.Clean(filepathToSlash(r.Path))
	}
	return r.Name
}

// IsRoot reports whether this entry is the workspace root repo (spec §5.4).
func (r Repo) IsRoot() bool { return r.EffectivePath() == RootPath }

// HasGroup reports whether the entry carries the given group label.
func (r Repo) HasGroup(g string) bool {
	for _, have := range r.Groups {
		if have == g {
			return true
		}
	}
	return false
}

// Find returns the entry with the given name.
func (m *Manifest) Find(name string) (Repo, bool) {
	for _, r := range m.Repos {
		if r.Name == name {
			return r, true
		}
	}
	return Repo{}, false
}

// Root returns the workspace root entry, if the manifest declares one.
func (m *Manifest) Root() (Repo, bool) {
	for _, r := range m.Repos {
		if r.IsRoot() {
			return r, true
		}
	}
	return Repo{}, false
}

// Groups returns every group label used in the manifest, sorted, without duplicates.
func (m *Manifest) Groups() []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range m.Repos {
		for _, g := range r.Groups {
			if !seen[g] {
				seen[g] = true
				out = append(out, g)
			}
		}
	}
	sort.Strings(out)
	return out
}

// ManifestError is a validation failure carrying its line, so the CLI can point at the offending
// entry rather than just name the file (spec §5.6).
type ManifestError struct {
	File string
	Line int
	Msg  string
}

func (e *ManifestError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", e.File, e.Line, e.Msg)
	}
	return fmt.Sprintf("%s: %s", e.File, e.Msg)
}

// Code reports the stable error code for manifest problems.
func (e *ManifestError) Code() ErrCode { return ErrManifest }

// Validate applies the §5.6 rules, returning the first failure: a manifest failing any of them is
// unsafe to act on, and one clear location beats a wall of cascading errors.
func (m *Manifest) Validate(file string) error {
	fail := func(line int, format string, args ...any) error {
		return &ManifestError{File: file, Line: line, Msg: fmt.Sprintf(format, args...)}
	}

	if m.Version == 0 {
		return fail(0, "missing required field 'version' (expected %d)", SchemaVersion)
	}
	if m.Version > SchemaVersion {
		return fail(0, "manifest version %d is newer than this build supports (%d); upgrade gits",
			m.Version, SchemaVersion)
	}
	if m.Version < SchemaVersion {
		return fail(0, "unsupported manifest version %d (expected %d)", m.Version, SchemaVersion)
	}

	names := map[string]int{}
	paths := map[string]int{}
	rootLine := 0

	for _, r := range m.Repos {
		if r.Name == "" {
			return fail(r.Line, "repo entry is missing required field 'name'")
		}
		if !r.URLDeclared && r.URL == "" {
			return fail(r.Line, "repo %q is missing required field 'url'", r.Name)
		}
		if prev, dup := names[r.Name]; dup {
			return fail(r.Line, "duplicate repo name %q (first declared at line %d)", r.Name, prev)
		}
		names[r.Name] = r.Line

		p := r.EffectivePath()
		if err := validatePath(r.Path, p); err != nil {
			return fail(r.Line, "repo %q: %v", r.Name, err)
		}
		if p == RootPath {
			if rootLine != 0 {
				return fail(r.Line, "repo %q: a second entry declares path %q (first at line %d); "+
					"a workspace has at most one root repo", r.Name, RootPath, rootLine)
			}
			rootLine = r.Line
		}
		if prev, dup := paths[p]; dup {
			return fail(r.Line, "repo %q resolves to path %q, already used at line %d", r.Name, p, prev)
		}
		paths[p] = r.Line
	}
	return nil
}

// validatePath enforces that an entry stays inside the workspace (spec §5.6). "." is the sole
// exception: it means the root repo, not an escape.
func validatePath(raw, resolved string) error {
	if resolved == RootPath {
		return nil
	}
	if raw != "" && isAbsPath(raw) {
		return fmt.Errorf("path %q must be relative to the workspace root", raw)
	}
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return fmt.Errorf("path %q escapes the workspace root", raw)
	}
	return nil
}

// isAbsPath detects absolute paths in both POSIX and Windows spellings regardless of host OS.
// CRITICAL: a manifest is shared between machines, so "C:\..." must be rejected on Linux too, or
// the error surfaces only on the machine that cannot use it.
func isAbsPath(p string) bool {
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return true
	}
	// Drive-letter form: C:\foo or C:/foo.
	if len(p) >= 2 && p[1] == ':' {
		c := p[0]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return true
		}
	}
	return false
}

// filepathToSlash normalises separators without importing path/filepath, so behaviour does not
// depend on which OS reads a manifest written on the other.
func filepathToSlash(p string) string { return strings.ReplaceAll(p, `\`, "/") }
