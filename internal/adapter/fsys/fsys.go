// Package fsys implements the app layer's filesystem port against the real filesystem.
package fsys

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/nekogravitycat/gits/internal/app"
)

// OS implements app.FS.
type OS struct{}

// New builds a filesystem adapter.
func New() *OS { return &OS{} }

var _ app.FS = (*OS)(nil)

// DirExists reports whether path is an existing directory.
//
// A path that exists but is a file counts as "not a directory" rather than an error: the caller's
// next question is "can a repo live here", and a file in the way answers that just as clearly.
func (*OS) DirExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return info.IsDir(), nil
}

// FileExists reports whether path is an existing regular file.
func (*OS) FileExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return info.Mode().IsRegular(), nil
}

// ListDirs returns the immediate subdirectory names of path, sorted.
//
// Sorting is not cosmetic: directory order from the OS varies between filesystems and machines,
// and an unsorted scan would make `gits init` write the same workspace differently on two
// computers (spec §6.5 rule 2).
func (*OS) ListDirs(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	var dirs []string
	for _, e := range entries {
		name := e.Name()
		// .git, .github, node_modules and friends are never managed repos, and descending into
		// the workspace's own .git would be actively wrong.
		if name != "" && name[0] == '.' {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, name)
			continue
		}
		// A symlink to a directory is reported as a symlink by ReadDir, so it needs a second look
		// -- some workspaces keep a repo elsewhere and link it in.
		if e.Type()&os.ModeSymlink != 0 {
			if info, serr := os.Stat(filepath.Join(path, name)); serr == nil && info.IsDir() {
				dirs = append(dirs, name)
			}
		}
	}
	sort.Strings(dirs)
	return dirs, nil
}

// ReadFile reads a file, returning empty content when it does not exist.
func (*OS) ReadFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}

// WriteFile writes a file, creating parent directories as needed.
func (*OS) WriteFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
