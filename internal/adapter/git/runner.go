package git

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/nekogravitycat/gits/internal/app"
	"github.com/nekogravitycat/gits/internal/domain"
)

// Runner executes git subprocesses under a hardened, non-interactive environment.
//
// gits shells out to the real git binary rather than reimplementing git in Go. That is a
// deliberate choice (spec §10): credential helpers, GPG signing programs, hooks and includeIf
// rules then behave exactly as they do when the user runs git themselves, with no second
// implementation to drift.
type Runner struct {
	// GitPath is the git executable; empty means "git" from PATH.
	GitPath string
	// Timeout bounds a single subprocess.
	Timeout time.Duration
	// Log receives the command line and output under -v.
	Log app.Logger
}

// waitDelay is how long to wait, after a kill, for inherited pipes to close.
//
// Without it a killed git whose grandchild still holds the output pipe leaves gits blocked in
// Wait -- turning a timeout into precisely the hang the timeout existed to prevent.
const waitDelay = 2 * time.Second

// result is one completed subprocess.
type result struct {
	stdout   string
	stderr   string
	exitCode int
}

// hardenedEnv builds the environment every git subprocess runs under (spec §6.8).
//
// Bounding gits' own prompts is not enough: git will happily open its own. On a machine with no
// credential helper -- a container, CI, a freshly installed laptop -- `git fetch` stops at
// "Username for 'https://...':" and waits forever. These variables convert that wait into an
// immediate, classifiable failure.
func hardenedEnv() []string {
	env := os.Environ()

	// Fail instead of prompting on the terminal.
	env = append(env, "GIT_TERMINAL_PROMPT=0")
	// Empty rather than unset: an empty askpass suppresses the GUI credential dialog, which would
	// otherwise block a "non-interactive" run behind a window nobody is looking at.
	env = append(env, "GIT_ASKPASS=", "SSH_ASKPASS=")
	// Read-only work must not contend for index.lock with an editor or a language server.
	env = append(env, "GIT_OPTIONAL_LOCKS=0")

	// Only supply a batch-mode ssh when the user has not configured their own command; overriding
	// a deliberate GIT_SSH_COMMAND would break setups that rely on it.
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		env = append(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	}
	return env
}

// baseArgs are prepended to every invocation so output is parseable regardless of user config.
func baseArgs() []string {
	return []string{"-c", "core.pager=cat", "-c", "color.ui=false"}
}

// exec runs git (or, for foreach, an arbitrary command) and captures its output.
//
// A non-zero exit is reported in the result, not as an error: err is reserved for gits failing to
// run the command at all. The two need different handling, and conflating them would make a failing
// `git log` indistinguishable from a missing git binary.
func (r *Runner) exec(ctx context.Context, dir string, name string, args []string) (result, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = app.DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = hardenedEnv()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Nothing may read from the terminal: a subprocess that inherits stdin can block on it even
	// with prompting disabled.
	cmd.Stdin = nil

	// Kill the whole process tree, not just git. git delegates to ssh, credential helpers and
	// hooks; killing only the parent leaves those running and holding the output pipes.
	configureProcessGroup(cmd)
	cmd.Cancel = func() error { return killProcessTree(cmd) }
	cmd.WaitDelay = waitDelay

	if r.Log != nil {
		r.Log.Verbosef("%s $ %s %s", dir, name, strings.Join(args, " "))
	}

	err := cmd.Run()
	res := result{stdout: stdout.String(), stderr: stderr.String()}

	if r.Log != nil && res.stderr != "" {
		r.Log.Verbosef("%s", strings.TrimRight(res.stderr, "\n"))
	}

	switch {
	case err == nil:
		return res, nil

	case ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
		// A stuck pre-commit hook is the everyday case here. Without a ceiling there is none at
		// all, and the caller waits indefinitely for a process that will never finish (§6.8).
		return res, &app.GitError{
			Code:   domain.ErrTimeout,
			Args:   args,
			Stderr: res.stderr,
			Err:    err,
		}

	case ctx.Err() != nil:
		return res, &app.GitError{Code: domain.ErrGit, Args: args, Stderr: res.stderr, Err: ctx.Err()}
	}

	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.exitCode = ee.ExitCode()
		return res, nil
	}
	// Could not start at all: git missing, directory gone, permission denied.
	return res, &app.GitError{Code: domain.ErrGit, Args: args, Stderr: res.stderr, Err: err}
}

// git runs a git command, treating a non-zero exit as a classified error.
func (r *Runner) git(ctx context.Context, dir string, args ...string) (string, error) {
	full := append(baseArgs(), args...)
	res, err := r.exec(ctx, dir, r.gitPath(), full)
	if err != nil {
		return res.stdout, err
	}
	if res.exitCode != 0 {
		return res.stdout, &app.GitError{
			Code:   classify(res.stderr),
			Args:   args,
			Stderr: res.stderr,
		}
	}
	return res.stdout, nil
}

// gitQuiet runs a git command whose non-zero exit is a legitimate answer rather than a failure
// (`check-ignore`, `cat-file -e`, `rev-parse` on a ref that may not exist).
func (r *Runner) gitQuiet(ctx context.Context, dir string, args ...string) (stdout string, exitCode int, err error) {
	full := append(baseArgs(), args...)
	res, err := r.exec(ctx, dir, r.gitPath(), full)
	return res.stdout, res.exitCode, err
}

func (r *Runner) gitPath() string {
	if r.GitPath != "" {
		return r.GitPath
	}
	return "git"
}
