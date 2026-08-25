package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nekogravitycat/gits/internal/domain"
)

// CommitOptions are the flags specific to `gits commit` (spec §7.5).
type CommitOptions struct {
	// Message drives the fast path: one message applied to every selected repo that has changes.
	// Empty means interactive review, which requires a TTY.
	Message string

	// All stages untracked files too. Off by default so local config and build output are not
	// swept into an unreviewed commit (spec §7.5).
	All bool
}

// CommitResult is one commit run.
type CommitResult struct {
	Repos   []RepoResult
	Skipped []domain.Excluded
	Summary domain.Summary
	DryRun  bool
}

// errCommitAborted ends an interactive session early at the user's request, keeping whatever was
// already committed.
var errCommitAborted = errors.New("aborted by user")

// Commit records pending work across the workspace (spec §7.5).
//
// no-write repos are excluded automatically. gits does not touch signing, hooks or the editor, and
// commit never pushes.
func Commit(ctx context.Context, env *Env, g Global, opts CommitOptions) (*CommitResult, error) {
	m, err := env.LoadManifest()
	if err != nil {
		return nil, err
	}
	selected, skipped, err := Select(m, g, domain.SelectOpts{Write: true})
	if err != nil {
		return nil, err
	}

	statuses := Observe(ctx, env, g, m, selected, false)

	var dirty []domain.Repo
	var dirtyStatus []domain.RepoStatus
	res := &CommitResult{Skipped: skipped, DryRun: g.DryRun}

	for i, st := range statuses {
		r := selected[i]
		if st.State == domain.StateMissing || st.State == domain.StateNotARepo || st.State == domain.StateError {
			res.Repos = append(res.Repos, skip(r, st.Code, st.Message, st.Hint))
			continue
		}
		if !hasCommittableChanges(st, opts.All) {
			continue
		}
		dirty = append(dirty, r)
		dirtyStatus = append(dirtyStatus, st)
	}

	if err := CheckMaxRepos(g, len(dirty)); err != nil {
		return nil, err
	}
	if len(dirty) == 0 {
		// No changes is a clean no-op, so commit is safe to retry and never produces an empty
		// commit (spec §6.11).
		res.Summary = SummarizeResults(res.Repos, len(skipped))
		return res, nil
	}

	if opts.Message == "" {
		if !env.Prompt.IsInteractive() {
			return nil, (&Error{
				Code: domain.ErrNeedsYes,
				Msg:  "interactive commit requires a terminal",
				Exit: ExitUsage,
			}).WithHint(`gits commit -m "<message>" -y`)
		}
		if err := commitInteractive(ctx, env, g, dirty, dirtyStatus, opts, res); err != nil {
			return nil, err
		}
	} else {
		if err := commitWithMessage(ctx, env, g, dirty, dirtyStatus, opts, res); err != nil {
			return nil, err
		}
	}

	res.Summary = SummarizeResults(res.Repos, len(skipped))
	return res, nil
}

// hasCommittableChanges reports whether a repo has anything this invocation would commit. Without
// -A, untracked files alone are not committable.
func hasCommittableChanges(st domain.RepoStatus, all bool) bool {
	if st.Dirty.Tracked > 0 {
		return true
	}
	return all && st.Dirty.Untracked > 0
}

// commitWithMessage applies one message to every dirty repo (spec §7.5, fast path).
func commitWithMessage(ctx context.Context, env *Env, g Global,
	repos []domain.Repo, statuses []domain.RepoStatus, opts CommitOptions, res *CommitResult,
) error {
	if err := env.Confirm(g, "commit", commitQuestion(repos, statuses)); err != nil {
		return err
	}
	for i, r := range repos {
		res.Repos = append(res.Repos, commitRepo(ctx, env, g, r, statuses[i], opts.Message, opts.All))
	}
	return nil
}

// commitInteractive walks the dirty repos one at a time (spec §7.5, interactive mode).
//
// NOTE: sequential on purpose -- the user reads a diff and types a message, and interleaved output
// would make the review unreadable (spec §6.3).
func commitInteractive(ctx context.Context, env *Env, g Global,
	repos []domain.Repo, statuses []domain.RepoStatus, opts CommitOptions, res *CommitResult,
) error {
	for i, r := range repos {
		st := statuses[i]
		env.Log.Warnf("[%d/%d] %s  (%s)", i+1, len(repos), r.Name, st.Branch)
		env.Log.Warnf("  %d tracked change(s), %d untracked", st.Dirty.Tracked, st.Dirty.Untracked)
		if st.Dirty.Untracked > 0 && !opts.All {
			env.Log.Warnf("  untracked files will not be committed (use -A to include them)")
		}

		msg, err := promptForMessage(ctx, env, r)
		switch {
		case errors.Is(err, errCommitAborted):
			// Everything already committed stays committed; the rest is not attempted.
			return nil
		case err != nil:
			return err
		case msg == "":
			res.Repos = append(res.Repos, skip(r, "", "skipped at the prompt", ""))
			continue
		}
		res.Repos = append(res.Repos, commitRepo(ctx, env, g, r, st, msg, opts.All))
	}
	return nil
}

// promptForMessage runs the review loop for one repo, returning the message, or "" to skip.
func promptForMessage(ctx context.Context, env *Env, r domain.Repo) (string, error) {
	dir := env.Dir(r)
	for {
		line, err := env.Prompt.Line("message > ")
		if err != nil {
			return "", err
		}
		switch strings.TrimSpace(line) {
		case "":
			return "", nil
		case "q":
			return "", errCommitAborted
		case "d":
			out, derr := env.Git.DiffStat(ctx, dir)
			if derr != nil {
				return "", derr
			}
			env.Log.Warnf("%s", out)
		case "dd":
			out, derr := env.Git.Diff(ctx, dir)
			if derr != nil {
				return "", derr
			}
			env.Log.Warnf("%s", out)
		case "e":
			msg, eerr := env.Prompt.Editor("")
			if eerr != nil {
				return "", eerr
			}
			if strings.TrimSpace(msg) == "" {
				return "", nil
			}
			return msg, nil
		default:
			return line, nil
		}
	}
}

func commitRepo(ctx context.Context, env *Env, g Global, r domain.Repo, st domain.RepoStatus, msg string, all bool) RepoResult {
	out := base(r, ActionUpdated)
	out.Branch = st.Branch
	out.Files = st.Dirty.Tracked
	if all {
		out.Files += st.Dirty.Untracked
	} else {
		// Reported, not committed: the user learns about them here rather than on the machine
		// where they turn out to be missing (spec §7.5).
		out.Untracked = st.Dirty.Untracked
	}

	if g.DryRun {
		out.Action = ActionPlanned
		out.Message = fmt.Sprintf("would commit %d file(s)", out.Files)
		return out
	}

	sha, err := env.Git.Commit(ctx, env.Dir(r), msg, all)
	if err != nil {
		res := fail(r, err)
		res.Branch = st.Branch
		return res
	}
	out.SHA = sha
	out.Message = fmt.Sprintf("committed %d file(s)", out.Files)
	return out
}

func commitQuestion(repos []domain.Repo, statuses []domain.RepoStatus) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Commit in %d repo(s): ", len(repos))
	for i, r := range repos {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s (%d file(s))", r.Name, statuses[i].Dirty.Tracked)
	}
	b.WriteString(". Continue?")
	return b.String()
}
