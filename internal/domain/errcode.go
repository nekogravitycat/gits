package domain

// ErrCode is the stable, machine-readable reason a repo was skipped or failed.
//
// Codes are part of the public contract (spec §6.6): they appear verbatim in `--json` output and
// in parentheses in human output. Agents branch on them, so a code's meaning must never change --
// add a new code instead.
type ErrCode string

const (
	ErrDirty       ErrCode = "E_DIRTY"
	ErrDiverged    ErrCode = "E_DIVERGED"
	ErrDetached    ErrCode = "E_DETACHED"
	ErrNoUpstream  ErrCode = "E_NO_UPSTREAM"
	ErrMissingDir  ErrCode = "E_MISSING_DIR"
	ErrNotARepo    ErrCode = "E_NOT_A_REPO"
	ErrNoWrite     ErrCode = "E_NO_WRITE"
	ErrAuth        ErrCode = "E_AUTH"
	ErrNetwork     ErrCode = "E_NETWORK"
	ErrTimeout     ErrCode = "E_TIMEOUT"
	ErrHookFailed  ErrCode = "E_HOOK_FAILED"
	ErrNoCanonical ErrCode = "E_NO_CANONICAL"
	ErrManifest    ErrCode = "E_MANIFEST"
	ErrNoWorkspace ErrCode = "E_NO_WORKSPACE"
	ErrNeedsYes    ErrCode = "E_NEEDS_YES"
	ErrMaxRepos    ErrCode = "E_MAX_REPOS"
	ErrGit         ErrCode = "E_GIT"
)

// Retryable reports whether a caller that retries this operation has any chance of a different
// outcome.
//
// This distinction is the whole point of having codes (spec §6.6): a network blip deserves a
// retry, while an auth failure retried a hundred times only burns an agent's budget.
func (c ErrCode) Retryable() bool {
	switch c {
	case ErrNetwork, ErrTimeout:
		return true
	default:
		return false
	}
}

// String satisfies fmt.Stringer so codes format correctly in %s and %v.
func (c ErrCode) String() string { return string(c) }
