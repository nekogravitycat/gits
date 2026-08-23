package cli

import (
	"os"
	"path/filepath"

	"github.com/nekogravitycat/gits/internal/app"
	"github.com/nekogravitycat/gits/internal/domain"
)

// WorkspaceEnv is the environment variable that names a workspace (spec §6.1).
const WorkspaceEnv = "GITS_WORKSPACE"

// YesEnv makes --yes the default for a whole environment, which is how CI and agent runners avoid
// threading the flag through every call site (spec §6.7 rule 4).
const YesEnv = "GITS_YES"

// FindWorkspace resolves the workspace root, in the fixed precedence of spec §6.1:
// the --workspace flag, then GITS_WORKSPACE, then the nearest gits.yaml at or above the working
// directory.
//
// The upward search mirrors how git locates .git, so the tool works from anywhere inside a
// workspace with nothing to configure.
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
			// Reached the filesystem root without finding one.
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
// An explicit path that turns out to hold no manifest is reported rather than searched upward
// from: the caller said where to look, and quietly looking somewhere else would act on a different
// workspace than the one they named.
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

// WorkspaceForInit resolves where a new manifest should be created. Unlike FindWorkspace it does
// not require an existing manifest, since creating one is the point.
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
// Getting this right is what keeps gits from ever hanging: every prompt is gated on it, and a
// wrong "yes" here turns a piped run into a process that waits forever for input that will never
// arrive (spec §6.7). Checking the character-device bit needs no dependency and behaves the same
// on Windows and Unix.
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
