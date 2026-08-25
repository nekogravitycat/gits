package app

import (
	"context"
	"path/filepath"

	"github.com/nekogravitycat/gits/internal/domain"
)

// Discovered is a git repo found on disk during a workspace scan.
type Discovered struct {
	// Name is the directory name, which also becomes the manifest entry's name.
	Name string
	// Path is workspace-relative, "." for the workspace root repo itself.
	Path string
	// URL is the origin remote's URL, empty when the repo has no origin.
	URL string
	// Branch is the branch currently checked out.
	Branch string
	// IsRoot marks the workspace directory itself.
	IsRoot bool
}

// ScanWorkspace finds the git repos in a workspace: the root directory when it is a repo, plus
// every immediate subdirectory that is one. init and adopt share this one implementation so they
// can never disagree about what counts as a repo (spec §7.7).
//
// NOTE: the scan is one level deep on purpose -- recursing would sweep up submodules and vendored
// checkouts, which are dependencies of repos, not members of the workspace.
func ScanWorkspace(ctx context.Context, env *Env) ([]Discovered, error) {
	var found []Discovered

	// The workspace root is checked first and separately: it is not one of its own subdirectories,
	// so scanning children alone would miss it and a second pass would double-count it (spec §5.4).
	if isRepo, err := env.Git.IsRepo(ctx, env.Workspace); err == nil && isRepo {
		d := Discovered{Name: filepath.Base(env.Workspace), Path: domain.RootPath, IsRoot: true}
		d.URL, d.Branch = describeRepo(ctx, env, env.Workspace)
		found = append(found, d)
	}

	names, err := env.FS.ListDirs(env.Workspace)
	if err != nil {
		return nil, &Error{Code: domain.ErrGit, Msg: "cannot scan workspace", Exit: ExitUsage, Err: err}
	}

	for _, name := range names {
		dir := filepath.Join(env.Workspace, name)
		isRepo, rerr := env.Git.IsRepo(ctx, dir)
		if rerr != nil || !isRepo {
			continue
		}
		d := Discovered{Name: name, Path: name}
		d.URL, d.Branch = describeRepo(ctx, env, dir)
		found = append(found, d)
	}
	return found, nil
}

// describeRepo reads the origin URL and current branch, both optional. A repo with no origin is
// still recorded with a blank URL: dropping it would leave exactly the drift the manifest exists
// to stop.
func describeRepo(ctx context.Context, env *Env, dir string) (url, branch string) {
	if u, err := env.Git.RemoteURL(ctx, dir, domain.DefaultRemote); err == nil {
		url = u
	}
	if obs, err := env.Git.Status(ctx, dir); err == nil {
		branch = obs.Branch
	}
	return url, branch
}
