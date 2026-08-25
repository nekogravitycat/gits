package cli

import (
	"os"
	"path/filepath"

	"github.com/nekogravitycat/gits/internal/app"
	"github.com/nekogravitycat/gits/internal/domain"
)

// WorkspaceEnv names a workspace (spec §6.1).
const WorkspaceEnv = "GITS_WORKSPACE"

// YesEnv makes --yes the default for a whole environment (spec §6.7 rule 4).
const YesEnv = "GITS_YES"

// FindWorkspace resolves the workspace root in fixed precedence (spec §6.1): --workspace flag,
// GITS_WORKSPACE, then the nearest gits.yaml at or above the working directory.
func FindWorkspace(flagValue string) (string, error) {
	if flagValue != "" {
		return validateWorkspace(flagValue, true)
	}
	if env := os.Getenv(WorkspaceEnv); env != "" {
		return validateWorkspace(env, true)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", &app.Error{
			Code: domain.ErrNoWorkspace,
			Msg:  "cannot determine the current directory",
			Exit: app.ExitUsage,
			Err:  err,
		}
	}

	dir, err := filepath.Abs(cwd)
	if err != nil {
		dir = cwd
	}
	for {
		if isWorkspace(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", (&app.Error{
		Code: domain.ErrNoWorkspace,
		Msg:  "cannot find " + app.ManifestName + " in this directory or any parent",
		Exit: app.ExitUsage,
	}).WithHint("gits init")
}

// validateWorkspace checks an explicitly named workspace.
//
// CRITICAL: an explicit path with no manifest is reported, never searched upward from -- doing so
// would act on a different workspace than the caller named.
func validateWorkspace(path string, mustHaveManifest bool) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if mustHaveManifest && !isWorkspace(abs) {
		return "", (&app.Error{
			Code: domain.ErrNoWorkspace,
			Msg:  "no " + app.ManifestName + " in " + abs,
			Exit: app.ExitUsage,
		}).WithHint("gits init -w %s", abs)
	}
	return abs, nil
}

// WorkspaceForInit resolves where a new manifest should be created; unlike FindWorkspace it does
// not require an existing manifest.
func WorkspaceForInit(flagValue string) (string, error) {
	if flagValue != "" {
		return validateWorkspace(flagValue, false)
	}
	if env := os.Getenv(WorkspaceEnv); env != "" {
		return validateWorkspace(env, false)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", &app.Error{
			Code: domain.ErrNoWorkspace,
			Msg:  "cannot determine the current directory",
			Exit: app.ExitUsage,
			Err:  err,
		}
	}
	return validateWorkspace(cwd, false)
}

func isWorkspace(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, app.ManifestName))
	return err == nil && info.Mode().IsRegular()
}

// IsTerminal reports whether f is an interactive terminal.
//
// CRITICAL: every prompt is gated on this; a wrong "yes" turns a piped run into a process that
// waits forever for input that never arrives (spec §6.7). The char-device bit needs no dependency
// and behaves the same on Windows and Unix.
func IsTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
