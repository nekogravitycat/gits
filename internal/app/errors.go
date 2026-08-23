package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/nekogravitycat/gits/internal/domain"
)

// ExitCode is the process exit status (spec §6.10).
type ExitCode int

const (
	// ExitOK: everything succeeded.
	ExitOK ExitCode = 0
	// ExitFailure: at least one repo operation failed.
	ExitFailure ExitCode = 1
	// ExitUsage: the command could not be attempted -- bad arguments, missing or invalid
	// manifest, or a confirmation required in a non-interactive environment.
	ExitUsage ExitCode = 2
	// ExitAttention: nothing failed, but something needs looking at. Only ever returned when the
	// caller asked for it with --exit-code.
	//
	// Kept distinct from ExitFailure on purpose: "a push failed" and "everything worked, but you
	// are two commits behind" call for completely different handling by the caller, and collapsing
	// them (as `git diff --exit-code` does) would lose that (spec §6.10).
	ExitAttention ExitCode = 3
	// ExitInterrupted: the user interrupted the run.
	ExitInterrupted ExitCode = 130
)

// Error is a failure that ends the command before any repo work, carrying the stable code and the
// exit status the CLI should use.
//
// Per-repo failures are not represented this way: they are collected into the command's result so
// that one bad repo never stops the rest (spec §3 principle 2).
type Error struct {
	Code domain.ErrCode
	Msg  string
	Hint string
	Exit ExitCode
	Err  error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Err)
	}
	return e.Msg
}

func (e *Error) Unwrap() error { return e.Err }

// GitError is a failed git invocation, already classified into a stable code by the adapter.
//
// Classification belongs next to the raw stderr, which only the adapter sees; the use cases above
// need the code, not the prose. Keeping E_AUTH and E_NETWORK apart is the whole point: one is
// worth retrying, the other never is, and an agent that cannot tell them apart burns its budget
// retrying a missing credential (spec §6.6).
type GitError struct {
	Code   domain.ErrCode
	Args   []string
	Stderr string
	Err    error
}

func (e *GitError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("git %v: %s", e.Args, e.Stderr)
	}
	if e.Err != nil {
		return fmt.Sprintf("git %v: %v", e.Args, e.Err)
	}
	return fmt.Sprintf("git %v failed", e.Args)
}

func (e *GitError) Unwrap() error { return e.Err }

// CodeOf extracts the stable code from an error, falling back to E_GIT for anything unclassified.
func CodeOf(err error) domain.ErrCode {
	var ge *GitError
	if errorsAs(err, &ge) {
		return ge.Code
	}
	var ae *Error
	if errorsAs(err, &ae) {
		return ae.Code
	}
	return domain.ErrGit
}

// MessageOf extracts a one-line, human-readable reason from an error.
//
// git's own stderr is preferred over Go's wrapping: "Permission denied (publickey)" tells the user
// what to do, while "exit status 128" does not.
func MessageOf(err error) string {
	if err == nil {
		return ""
	}
	var ge *GitError
	if errorsAs(err, &ge) && ge.Stderr != "" {
		return firstLine(ge.Stderr)
	}
	return firstLine(err.Error())
}

// Usagef builds a usage error (exit 2) with the given code.
func Usagef(code domain.ErrCode, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...), Exit: ExitUsage}
}

// WithHint attaches a next step the caller can run verbatim. Hints are cheap to produce and useful
// to both audiences: a human pastes it, an agent executes it (spec §6.6).
func (e *Error) WithHint(format string, args ...any) *Error {
	e.Hint = fmt.Sprintf(format, args...)
	return e
}

// ErrNeedsYes is the canonical non-interactive refusal.
//
// Failing immediately is the entire point. Waiting for input that can never arrive leaves a silent
// process that never exits, which a caller only discovers as a timeout with no diagnostics
// (spec §6.7).
func ErrNeedsYes(command string) *Error {
	return (&Error{
		Code: domain.ErrNeedsYes,
		Msg:  "non-interactive environment requires --yes",
		Exit: ExitUsage,
	}).WithHint("gits %s --yes", command)
}

// ErrMaxRepos reports that the planned scope exceeded the caller's --max-repos ceiling.
//
// The check guards against the failure mode that actually happens in automation: not one wrong
// operation, but the right operation applied to far too many repos (spec §6.12).
func ErrMaxRepos(planned, maxRepos int) *Error {
	return &Error{
		Code: domain.ErrMaxRepos,
		Msg: fmt.Sprintf("plan affects %d repos, which exceeds --max-repos %d",
			planned, maxRepos),
		Exit: ExitUsage,
	}
}

// errorsAs is a thin wrapper so the call sites above stay readable.
func errorsAs[T error](err error, target *T) bool { return errors.As(err, target) }

// firstLine trims an error down to one line: multi-line git output belongs in -v, not in a
// summary row that has to stay scannable.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
