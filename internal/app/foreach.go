package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/nekogravitycat/gits/internal/domain"
)

// ForeachOutputLimit caps each captured stream at 8KB (spec §7.12).
//
// The cap exists for the agent caller: eighteen repos each returning an unbounded log can exhaust
// a context window in a single call. Truncation is always flagged, so a reader knows the output
// was cut rather than silently assuming it was short.
const ForeachOutputLimit = 8 << 10

// ForeachOptions are the flags specific to `gits foreach` (spec §7.12).
type ForeachOptions struct {
	// Args is the command to run in each repo.
	Args []string

	// IncludeNoWrite opts a run into touching no-write repos.
	//
	// Required because the command is opaque to gits: there is no way to tell `git log` from
	// `git reset --hard`, so every foreach is treated as a write (spec §7.12).
	IncludeNoWrite bool
}

// ForeachOutput is one repo's captured result.
type ForeachOutput struct {
	Name string
	Path string

	ExitCode int
	Stdout   string
	Stderr   string

	// Truncated marks that Stdout or Stderr was cut at ForeachOutputLimit.
	Truncated bool

	Code    domain.ErrCode
	Message string
}

// ForeachResult is one foreach run.
type ForeachResult struct {
	Command []string
	Repos   []ForeachOutput
	Skipped []domain.Excluded
	DryRun  bool
}

// Failed reports whether any repo exited non-zero, which drives exit code 1.
func (r *ForeachResult) Failed() bool {
	for _, o := range r.Repos {
		if o.ExitCode != 0 {
			return true
		}
	}
	return false
}

// Foreach runs an arbitrary command across the workspace (spec §7.12).
//
// This is the escape hatch for everything gits does not wrap. It matters most to agents: without
// it, an agent has to assemble its own `cd X && git ...` for every repo, losing the filtering,
// the concurrency, the timeouts and the error classification in one step.
func Foreach(ctx context.Context, env *Env, g Global, opts ForeachOptions) (*ForeachResult, error) {
	if len(opts.Args) == 0 {
		return nil, Usagef(domain.ErrManifest, "foreach needs a command to run").
			WithHint("gits foreach -- git log --oneline -1")
	}

	m, err := env.LoadManifest()
	if err != nil {
		return nil, err
	}
	selected, skipped, err := Select(m, g, domain.SelectOpts{
		Write:          true,
		IncludeNoWrite: opts.IncludeNoWrite,
	})
	if err != nil {
		return nil, err
	}

	// Only repos that actually exist can run a command.
	var runnable []domain.Repo
	res := &ForeachResult{Command: opts.Args, Skipped: skipped, DryRun: g.DryRun}
	for _, r := range selected {
		exists, derr := env.FS.DirExists(env.Dir(r))
		if derr != nil || !exists {
			res.Repos = append(res.Repos, ForeachOutput{
				Name: r.Name, Path: r.EffectivePath(), ExitCode: -1,
				Code: domain.ErrMissingDir, Message: "directory does not exist",
			})
			continue
		}
		runnable = append(runnable, r)
	}

	if err := CheckMaxRepos(g, len(runnable)); err != nil {
		return nil, err
	}
	if len(runnable) == 0 {
		return res, nil
	}

	if err := env.Confirm(g, "foreach", foreachQuestion(opts.Args, runnable)); err != nil {
		return nil, err
	}
	if g.DryRun {
		for _, r := range runnable {
			res.Repos = append(res.Repos, ForeachOutput{
				Name: r.Name, Path: r.EffectivePath(),
				Message: "would run: " + strings.Join(opts.Args, " "),
			})
		}
		sortForeach(res.Repos, selected)
		return res, nil
	}

	ran := mapRepos(ctx, g.Concurrency(), runnable, func(ctx context.Context, r domain.Repo) ForeachOutput {
		return runOne(ctx, env, r, opts.Args)
	})
	res.Repos = append(res.Repos, ran...)
	sortForeach(res.Repos, selected)
	return res, nil
}

func runOne(ctx context.Context, env *Env, r domain.Repo, args []string) ForeachOutput {
	out := ForeachOutput{Name: r.Name, Path: r.EffectivePath()}

	code, stdout, stderr, err := env.Git.Run(ctx, env.Dir(r), args)
	if err != nil {
		// gits could not run the command at all, which is a different failure from the command
		// running and reporting an error.
		out.ExitCode = -1
		out.Code = CodeOf(err)
		out.Message = MessageOf(err)
		return out
	}
	out.ExitCode = code
	out.Stdout, out.Truncated = truncate(stdout)
	var stderrTruncated bool
	out.Stderr, stderrTruncated = truncate(stderr)
	out.Truncated = out.Truncated || stderrTruncated
	return out
}

// truncate caps a captured stream, reporting whether anything was cut.
func truncate(s string) (string, bool) {
	if len(s) <= ForeachOutputLimit {
		return s, false
	}
	return s[:ForeachOutputLimit], true
}

func sortForeach(outputs []ForeachOutput, order []domain.Repo) {
	rank := make(map[string]int, len(order))
	for i, r := range order {
		rank[r.Name] = i
	}
	for i := 1; i < len(outputs); i++ {
		for j := i; j > 0 && rank[outputs[j].Name] < rank[outputs[j-1].Name]; j-- {
			outputs[j], outputs[j-1] = outputs[j-1], outputs[j]
		}
	}
}

func foreachQuestion(args []string, repos []domain.Repo) string {
	return fmt.Sprintf("Run %q in %d repo(s). Continue?", strings.Join(args, " "), len(repos))
}
