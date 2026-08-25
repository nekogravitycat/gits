package git

import (
	"strings"

	"github.com/nekogravitycat/gits/internal/domain"
)

// authPatterns match git/transport auth failures.
//
// CRITICAL: matched before networkPatterns; several arrive over a healthy connection, and
// misclassifying auth as network sends the caller into a retry loop that can never succeed.
var authPatterns = []string{
	"authentication failed",
	"could not read username",
	"could not read password",
	"terminal prompts disabled",
	"permission denied (publickey",
	"permission denied, please try again",
	"access denied",
	"403 forbidden",
	"401 unauthorized",
	"invalid username or password",
	"support for password authentication was removed",
	"host key verification failed",
	"no supported authentication methods",
	"repository not found", // forges return this for a private repo you cannot see
}

// networkPatterns are transport-level failures where the operation might well work next time.
var networkPatterns = []string{
	"could not resolve host",
	"could not resolve hostname",
	"connection timed out",
	"connection refused",
	"connection reset",
	"network is unreachable",
	"temporary failure in name resolution",
	"operation timed out",
	"failed to connect",
	"unable to access",
	"ssl certificate problem",
	"gnutls_handshake() failed",
	"early eof",
	"rpc failed",
	"the remote end hung up unexpectedly",
	"502 bad gateway",
	"503 service unavailable",
	"504 gateway",
}

// hookPatterns identify a local hook rejecting the operation.
var hookPatterns = []string{
	"pre-commit hook",
	"commit-msg hook",
	"pre-push hook",
	"pre-receive hook",
	"hook declined",
	"hook exited with",
}

// classify turns git's stderr into a stable code (spec §6.6).
//
// CRITICAL: the E_AUTH/E_NETWORK split drives retry policy -- network is retried, auth never is,
// and a caller that cannot tell them apart retries a missing credential until its budget is gone.
func classify(stderr string) domain.ErrCode {
	s := strings.ToLower(stderr)

	if containsAny(s, hookPatterns) {
		return domain.ErrHookFailed
	}
	// CRITICAL: auth before network -- "unable to access ... 403" matches both, and the real
	// reason (auth) must win.
	if containsAny(s, authPatterns) {
		return domain.ErrAuth
	}
	if containsAny(s, networkPatterns) {
		return domain.ErrNetwork
	}

	switch {
	case strings.Contains(s, "not a git repository"):
		return domain.ErrNotARepo
	case strings.Contains(s, "you have unstaged changes"),
		strings.Contains(s, "local changes to the following files would be overwritten"),
		strings.Contains(s, "your local changes would be overwritten"):
		return domain.ErrDirty
	case strings.Contains(s, "not possible to fast-forward"),
		strings.Contains(s, "non-fast-forward"),
		strings.Contains(s, "fetch first"):
		return domain.ErrDiverged
	case strings.Contains(s, "no upstream"),
		strings.Contains(s, "has no upstream branch"):
		return domain.ErrNoUpstream
	}

	// git's exit codes are not classifiable: 128 covers everything from a bad ref to a dead
	// network, so the text has to be read.
	return domain.ErrGit
}

func containsAny(s string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}
