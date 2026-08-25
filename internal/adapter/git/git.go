// Package git implements the app layer's git ports by driving the real git executable.
//
// Architecture Note
//
//   - This layer runs commands, parses output and classifies failures; it makes no policy
//     decisions. Meaning lives in internal/domain and internal/app, which are testable without
//     git installed.
//   - Submodule pins are read from the committed tree/gitlink, never the checked-out worktree:
//     the worktree may be on something else, but the pin is what other machines get (spec §7.11).
//   - A gitQuiet non-zero exit is a legitimate answer (missing ref, no origin, no HEAD tree), not
//     an error to propagate.
package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nekogravitycat/gits/internal/app"
	"github.com/nekogravitycat/gits/internal/domain"
)

// Adapter implements app.Git.
type Adapter struct {
	runner *Runner
}

// New builds an adapter bounded by the given per-subprocess timeout.
func New(timeout time.Duration, log app.Logger) *Adapter {
	return &Adapter{runner: &Runner{Timeout: timeout, Log: log}}
}

var _ app.Git = (*Adapter)(nil)

// IsRepo reports whether dir is the root of a git worktree.
//
// CRITICAL: the .git check must come first; git alone reports true for any subdirectory of a repo,
// so every folder under a workspace-root-that-is-itself-a-repo would be mistaken for a repo.
func (a *Adapter) IsRepo(ctx context.Context, dir string) (bool, error) {
	// Worktrees/submodules store .git as a file, so test existence only, not type.
	if !pathExists(filepath.Join(dir, ".git")) {
		return false, nil
	}
	_, code, err := a.runner.gitQuiet(ctx, dir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return false, err
	}
	return code == 0, nil
}

// Status reports a repo's branch, upstream, divergence and pending changes in one call.
func (a *Adapter) Status(ctx context.Context, dir string) (app.RepoObservation, error) {
	out, err := a.runner.git(ctx, dir, "status", "--porcelain=v2", "--branch")
	if err != nil {
		return app.RepoObservation{}, err
	}
	obs := parseStatus(out)

	// A submodule matching its gitlink emits no status record, so .gitmodules is the only reliable
	// answer to "does this repo have submodules at all".
	if pathExists(filepath.Join(dir, ".gitmodules")) {
		obs.HasSubmodules = true
	}
	return obs, nil
}

// RemoteURL returns a remote's URL, or "" when the remote is not configured.
func (a *Adapter) RemoteURL(ctx context.Context, dir, remote string) (string, error) {
	out, code, err := a.runner.gitQuiet(ctx, dir, "remote", "get-url", remote)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", nil // no origin is normal, not an error
	}
	return strings.TrimSpace(out), nil
}

// ListSubmodules reads .gitmodules and pairs each entry with its gitlink SHA from HEAD's tree.
func (a *Adapter) ListSubmodules(ctx context.Context, dir string) ([]domain.Submodule, error) {
	content, err := os.ReadFile(filepath.Join(dir, ".gitmodules"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	subs := parseGitmodules(string(content), func(msg string) {
		if a.runner.Log != nil {
			a.runner.Log.Verbosef("%s: %s", dir, msg)
		}
	})
	if len(subs) == 0 {
		return nil, nil
	}

	// One ls-tree limited by pathspec rather than one call per submodule; gitlink read from the
	// committed tree (see Architecture Note).
	args := []string{"ls-tree", "-r", "HEAD", "--"}
	for _, s := range subs {
		args = append(args, s.Path)
	}
	out, code, err := a.runner.gitQuiet(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		// No commits yet means no HEAD tree; the declarations are still worth returning.
		return subs, nil
	}

	pins := parseLsTree(out)
	for i := range subs {
		subs[i].SHA = pins[subs[i].Path]
	}
	return subs, nil
}

// ResolveRef resolves a ref to a commit SHA, reporting ok=false when it does not exist.
func (a *Adapter) ResolveRef(ctx context.Context, dir, ref string) (string, bool, error) {
	// CRITICAL: the ^{commit} peel forces annotated tags to their commit, keeping downstream
	// comparisons commit-to-commit rather than tag-object-to-commit.
	out, code, err := a.runner.gitQuiet(ctx, dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", false, err
	}
	if code != 0 {
		return "", false, nil // an unfetched remote-tracking branch is missing, not broken
	}
	return strings.TrimSpace(out), true, nil
}

// CommitExists reports whether a commit object is present locally.
func (a *Adapter) CommitExists(ctx context.Context, dir, sha string) (bool, error) {
	_, code, err := a.runner.gitQuiet(ctx, dir, "cat-file", "-e", sha+"^{commit}")
	if err != nil {
		return false, err
	}
	return code == 0, nil
}

// CountAheadBehind returns the two-way commit count between a and b.
//
// CRITICAL: the three-dot form with --left-right is required; one-way `a..b` returns a bare count
// even when a is not an ancestor of b, so "ahead 3, behind 3" reads as "behind 3" and points the
// user at an impossible fast-forward (spec §7.11).
func (a *Adapter) CountAheadBehind(ctx context.Context, dir, left, right string) (int, int, error) {
	out, err := a.runner.git(ctx, dir, "rev-list", "--left-right", "--count", left+"..."+right)
	if err != nil {
		return 0, 0, err
	}
	ahead, behind := parseAheadBehind(out)
	return ahead, behind, nil
}

// BranchContains names the branches containing a commit.
func (a *Adapter) BranchContains(ctx context.Context, dir, sha string) ([]string, error) {
	out, code, err := a.runner.gitQuiet(ctx, dir,
		"branch", "--all", "--contains", sha, "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, nil
	}
	var branches []string
	for _, line := range strings.Split(out, "\n") {
		if b := strings.TrimSpace(line); b != "" {
			branches = append(branches, b)
		}
	}
	return branches, nil
}

// DiffStat renders pending changes as a summary.
func (a *Adapter) DiffStat(ctx context.Context, dir string) (string, error) {
	return a.runner.git(ctx, dir, "diff", "--stat")
}

// Diff renders pending changes in full.
func (a *Adapter) Diff(ctx context.Context, dir string) (string, error) {
	return a.runner.git(ctx, dir, "diff")
}

// IsIgnored reports whether git would ignore a workspace-relative path.
func (a *Adapter) IsIgnored(ctx context.Context, dir, relPath string) (bool, error) {
	// check-ignore: exit 0 = ignored, 1 = not, 128 = real error. Exit status is the answer here.
	_, code, err := a.runner.gitQuiet(ctx, dir, "check-ignore", "-q", relPath)
	if err != nil {
		return false, err
	}
	return code == 0, nil
}

// Fetch updates remote-tracking refs and prunes deleted ones.
func (a *Adapter) Fetch(ctx context.Context, dir, remote string) error {
	// --prune stops deleted upstream branches from being reported as real remote-tracking ones.
	_, err := a.runner.git(ctx, dir, "fetch", "--prune", remote)
	return err
}

// MergeFFOnly fast-forwards the current branch, refusing anything else.
func (a *Adapter) MergeFFOnly(ctx context.Context, dir, ref string) error {
	// CRITICAL: --ff-only is the whole safety story for sync; it refuses rather than creating a
	// merge commit or conflicted worktree across many repos (spec §7.3).
	_, err := a.runner.git(ctx, dir, "merge", "--ff-only", ref)
	return err
}

// SubmoduleUpdate brings submodule worktrees in line with their gitlinks.
func (a *Adapter) SubmoduleUpdate(ctx context.Context, dir string) error {
	_, err := a.runner.git(ctx, dir, "submodule", "update", "--init", "--recursive")
	return err
}

// Clone creates a checkout at dir.
func (a *Adapter) Clone(ctx context.Context, url, dir, branch string, withSubmodules bool) error {
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return &app.GitError{Code: domain.ErrGit, Args: []string{"clone", url}, Err: err}
	}

	args := []string{"clone"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	if withSubmodules {
		// One round trip, and no window where the checkout exists with empty submodule dirs.
		args = append(args, "--recurse-submodules")
	}
	args = append(args, url, dir)

	// CRITICAL: run from parent (just created) -- a subprocess's working directory must exist.
	_, err := a.runner.git(ctx, parent, args...)
	return err
}

// Commit records pending changes and returns the new short SHA.
func (a *Adapter) Commit(ctx context.Context, dir, message string, addUntracked bool) (string, error) {
	if addUntracked {
		if _, err := a.runner.git(ctx, dir, "add", "-A"); err != nil {
			return "", err
		}
		if _, err := a.runner.git(ctx, dir, "commit", "-m", message); err != nil {
			return "", err
		}
	} else {
		// -a stages tracked modifications only, keeping local config and build output out of the
		// commit (spec §7.5). No --no-verify: hooks/signing/editor stay as configured; a rejecting
		// hook surfaces as E_HOOK_FAILED.
		if _, err := a.runner.git(ctx, dir, "commit", "-a", "-m", message); err != nil {
			return "", err
		}
	}

	out, err := a.runner.git(ctx, dir, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Push publishes a branch.
func (a *Adapter) Push(ctx context.Context, dir, remote, branch string, setUpstream bool) error {
	args := []string{"push"}
	if setUpstream {
		args = append(args, "-u")
	}
	// CRITICAL: never --force in any spelling; v1 does not offer it, so an accidental history
	// rewrite cannot originate here (spec §7.4).
	args = append(args, remote, branch)
	_, err := a.runner.git(ctx, dir, args...)
	return err
}

// Run executes an arbitrary command for foreach.
func (a *Adapter) Run(ctx context.Context, dir string, args []string) (int, string, string, error) {
	if len(args) == 0 {
		return -1, "", "", &app.GitError{Code: domain.ErrGit, Err: os.ErrInvalid}
	}
	res, err := a.runner.exec(ctx, dir, args[0], args[1:])
	if err != nil {
		return -1, res.stdout, res.stderr, err
	}
	return res.exitCode, res.stdout, res.stderr, nil
}

// pathExists reports whether a filesystem entry is present, without following symlinks.
func pathExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}
