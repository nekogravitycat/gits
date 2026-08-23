package app

import "github.com/nekogravitycat/gits/internal/domain"

// Action is what a write command did to one repo. It is the write-side counterpart of
// domain.RepoState: one stable enum a caller can branch on instead of inferring intent from prose.
type Action string

const (
	// ActionUpdated: the repo moved (fast-forwarded, committed, pushed, cloned).
	ActionUpdated Action = "updated"
	// ActionUpToDate: nothing needed doing. Re-running a command must land here rather than
	// repeat work, which is what makes every write command safe for an agent to retry (§6.11).
	ActionUpToDate Action = "up-to-date"
	// ActionSkipped: deliberately not touched, always with a Code saying why.
	ActionSkipped Action = "skipped"
	// ActionFailed: attempted and failed.
	ActionFailed Action = "failed"
	// ActionPlanned: what --dry-run would have done.
	ActionPlanned Action = "planned"
)

// RepoResult is the outcome of one write operation on one repo.
//
// The fields beyond the verdict are what let a report say "advanced 3 commits" instead of just
// "updated" -- the difference between a summary a user trusts and one they have to verify by hand.
type RepoResult struct {
	Name    string
	Path    string
	Groups  []string
	NoWrite bool

	Action  Action
	Code    domain.ErrCode
	Message string
	Hint    string

	Branch   string
	Upstream string
	Ahead    int
	Behind   int

	// Commits is how many commits the operation moved: fast-forwarded, or pushed.
	Commits int
	// SHA is the resulting commit, for commit and clone.
	SHA string
	// Files counts what a commit included.
	Files int
	// Untracked counts files deliberately left out of a commit, so the user finds out now rather
	// than on the machine where they turn out to be missing (§7.5).
	Untracked int

	// URL is the source for a clone.
	URL string
}

// Failed reports whether this result should drive exit code 1.
func (r RepoResult) Failed() bool { return r.Action == ActionFailed }

// skip builds a skipped result carrying the reason and a runnable next step.
func skip(r domain.Repo, code domain.ErrCode, msg, hint string) RepoResult {
	return RepoResult{
		Name: r.Name, Path: r.EffectivePath(), Groups: r.Groups, NoWrite: r.NoWrite,
		Action: ActionSkipped, Code: code, Message: msg, Hint: hint,
	}
}

// fail builds a failed result from an error the adapter already classified.
func fail(r domain.Repo, err error) RepoResult {
	return RepoResult{
		Name: r.Name, Path: r.EffectivePath(), Groups: r.Groups, NoWrite: r.NoWrite,
		Action: ActionFailed, Code: CodeOf(err), Message: MessageOf(err), Hint: retryHint(CodeOf(err), r.Name),
	}
}

// base builds a result pre-filled with the repo's identity.
func base(r domain.Repo, action Action) RepoResult {
	return RepoResult{
		Name: r.Name, Path: r.EffectivePath(), Groups: r.Groups, NoWrite: r.NoWrite, Action: action,
	}
}

// retryHint turns a code into advice that respects whether retrying can possibly work.
//
// Suggesting a retry for an auth failure would be worse than offering nothing: it is the exact
// loop the spec calls out, where an agent retries a missing credential until its budget is gone.
func retryHint(code domain.ErrCode, name string) string {
	switch code {
	case domain.ErrAuth:
		return "check your credentials for " + name + "; retrying will not help"
	case domain.ErrNetwork:
		return "retry when the network is available"
	case domain.ErrTimeout:
		return "retry with a longer --timeout"
	case domain.ErrHookFailed:
		return "cd " + name + " and fix what the hook reported"
	default:
		return ""
	}
}

// SummarizeResults tallies write results for the run summary.
//
// excluded counts repos the write boundary kept out of the run entirely (no-write). They are part
// of the total because the user selected them, and they land in the skipped bucket.
//
// Every repo lands in exactly one bucket: clean + missing + skipped + failed == total. A repo
// counted twice, or not at all, produces a summary whose parts do not reconcile with its own
// total, and a report that cannot be reconciled is one nobody trusts.
func SummarizeResults(results []RepoResult, excluded int) domain.Summary {
	sum := domain.Summary{Total: len(results) + excluded, Skipped: excluded}
	for _, r := range results {
		switch r.Action {
		case ActionFailed:
			sum.Failed++
		case ActionSkipped:
			// "Missing" is the more specific answer, so it replaces the generic skip rather than
			// adding to it.
			if r.Code == domain.ErrMissingDir {
				sum.Missing++
			} else {
				sum.Skipped++
			}
		case ActionUpdated, ActionPlanned, ActionUpToDate:
			sum.Clean++
		}
	}
	return sum
}

// AnyFailed reports whether any repo operation failed, which drives exit code 1.
func AnyFailed(results []RepoResult) bool {
	for _, r := range results {
		if r.Failed() {
			return true
		}
	}
	return false
}

// inRepo renders a shell command scoped to a repo.
//
// The workspace root repo has path ".", and "cd . && git ..." is noise that makes a hint read like
// boilerplate rather than something to paste. For the root, the command alone is already correct.
func inRepo(path, command string) string {
	if path == "" || path == domain.RootPath {
		return command
	}
	return "cd " + path + " && " + command
}
