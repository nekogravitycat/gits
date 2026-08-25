package app

import "github.com/nekogravitycat/gits/internal/domain"

// Action is what a write command did to one repo. It is the write-side counterpart of
// domain.RepoState: one stable enum a caller can branch on instead of inferring intent from prose.
type Action string

const (
	// ActionUpdated: the repo moved (fast-forwarded, committed, pushed, cloned).
	ActionUpdated Action = "updated"
	// ActionUpToDate: nothing needed doing. Re-running a command lands here rather than repeating
	// work, which makes every write command safe for an agent to retry (§6.11).
	ActionUpToDate Action = "up-to-date"
	// ActionSkipped: deliberately not touched, always with a Code saying why.
	ActionSkipped Action = "skipped"
	// ActionFailed: attempted and failed.
	ActionFailed Action = "failed"
	// ActionPlanned: what --dry-run would have done.
	ActionPlanned Action = "planned"
)

// RepoResult is the outcome of one write operation on one repo. The fields beyond the verdict let
// a report say "advanced 3 commits" instead of just "updated".
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
	// than on the machine where they turn out missing (§7.5).
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

// cancelledRepo builds a skipped result for a repo mapRepos never started because the run was
// interrupted (SIGINT) while it was still waiting for a concurrency slot. Skipped rather than
// Failed: AnyFailed must stay false so an interrupted run reports exit 130, not a exit 1 that
// masks it (spec §6.10).
func cancelledRepo(r domain.Repo) RepoResult {
	return skip(r, domain.ErrInterrupted, "interrupted before starting", "")
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

// retryHint turns a code into advice that respects whether retrying can work.
//
// CRITICAL: never suggest retrying an auth failure -- that is the exact loop where an agent
// retries a missing credential until its budget is gone (spec §6.6).
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
// excluded counts repos the write boundary kept out entirely (no-write); they are part of the
// total because the user selected them, and land in the skipped bucket.
//
// CRITICAL: every repo lands in exactly one bucket (clean + missing + skipped + failed == total).
// A repo counted twice or not at all produces a summary that does not reconcile, which nobody
// trusts.
func SummarizeResults(results []RepoResult, excluded int) domain.Summary {
	sum := domain.Summary{Total: len(results) + excluded, Skipped: excluded}
	for _, r := range results {
		switch r.Action {
		case ActionFailed:
			sum.Failed++
		case ActionSkipped:
			// "Missing" is more specific, so it replaces the generic skip rather than adding.
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

// inRepo renders a shell command scoped to a repo. The root repo has path ".", so its hint omits
// the "cd . &&" prefix that would otherwise read as boilerplate.
func inRepo(path, command string) string {
	if path == "" || path == domain.RootPath {
		return command
	}
	return "cd " + path + " && " + command
}

// sortByManifest reorders items into the manifest order given by order, via a stable insertion
// sort (the slice is small and equal ranks keep their original order).
func sortByManifest[T any](items []T, order []domain.Repo, name func(T) string) {
	rank := make(map[string]int, len(order))
	for i, r := range order {
		rank[r.Name] = i
	}
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && rank[name(items[j])] < rank[name(items[j-1])]; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}
