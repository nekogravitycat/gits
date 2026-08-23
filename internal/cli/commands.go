package cli

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	"github.com/nekogravitycat/gits/internal/app"
	"github.com/nekogravitycat/gits/internal/domain"
)

// errorsAs is a thin wrapper keeping the call sites readable.
func errorsAs[T error](err error, target *T) bool { return errors.As(err, target) }

// finish maps a command's outcome onto the spec's exit codes (spec §6.10).
//
// The distinction that matters: exit 1 means a repo operation failed, exit 3 means nothing failed
// but something wants attention. They are kept apart because a caller responds to them completely
// differently, and 3 is only ever returned when the caller asked for it with --exit-code.
func finish(rt *Runtime, setExit exitSetter, failed, attention bool) {
	switch {
	case failed:
		setExit(app.ExitFailure)
	case attention && rt.Global.ExitCode:
		setExit(app.ExitAttention)
	}
}

// run is the shared shape of every command: build the runtime, call one use case, render, exit.
func run(
	ctx context.Context, newRuntime runtimeFactory, setExit exitSetter,
	cmd *cobra.Command, name string, needManifest bool,
	fn func(context.Context, *Runtime) error,
) {
	rt, err := newRuntime(needManifest)
	if err != nil {
		setExit(reportError(nil, name, err, jsonRequested(cmd), cmd.OutOrStdout(), cmd.ErrOrStderr()))
		return
	}
	if err := fn(ctx, rt); err != nil {
		setExit(reportError(rt, name, err, rt.Global.JSON, rt.Stdout, rt.Stderr))
	}
}

// jsonRequested reads --json straight from the flag set, for the window before a Runtime exists.
func jsonRequested(cmd *cobra.Command) bool {
	v, err := cmd.Flags().GetBool("json")
	return err == nil && v
}

func newUpCommand(ctx context.Context, newRuntime runtimeFactory, setExit exitSetter) *cobra.Command {
	var opts app.UpOptions

	cmd := &cobra.Command{
		Use:   "up",
		Short: "Bring the whole workspace up to date, then report",
		Long: "up is the everyday verb. It syncs the workspace root repo first and reloads the\n" +
			"manifest from it, clones whatever the refreshed list mentions and this machine\n" +
			"lacks, syncs the rest, and finishes with a status and dependency summary.\n\n" +
			"The root repo goes first because the manifest lives inside it: a repo added on\n" +
			"another machine only becomes visible once that repo has been pulled.",
		Run: func(cmd *cobra.Command, _ []string) {
			run(ctx, newRuntime, setExit, cmd, "up", true, func(ctx context.Context, rt *Runtime) error {
				res, err := app.Up(ctx, rt.Env, rt.Global, opts)
				if err != nil {
					return err
				}
				if rt.Global.JSON {
					if err := rt.JSON.Up(res, rt.Global.DryRun); err != nil {
						return err
					}
				} else {
					rt.Human.Up(res, rt.Global.DryRun)
				}
				finish(rt, setExit, res.Failed(), res.Attention())
				return nil
			})
		},
	}

	cmd.Flags().BoolVar(&opts.NoClone, "no-clone", false, "do not clone missing repos")
	cmd.Flags().BoolVar(&opts.NoSubmodules, "no-submodules", false, "skip submodule updates")
	cmd.Flags().BoolVar(&opts.NoDeps, "no-deps", false, "skip the dependency summary")
	return cmd
}

func newStatusCommand(ctx context.Context, newRuntime runtimeFactory, setExit exitSetter) *cobra.Command {
	var opts app.StatusOptions
	var byGroup bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the state of every repo",
		Long: "status reports every repo in the manifest: current branch, uncommitted changes,\n" +
			"how far ahead or behind its upstream it is, and whether it exists at all.\n\n" +
			"It does not touch the network by default, so it runs in milliseconds; pass --fetch\n" +
			"for live numbers. Offline results are labelled as possibly stale rather than\n" +
			"presented as current.",
		Run: func(cmd *cobra.Command, _ []string) {
			run(ctx, newRuntime, setExit, cmd, "status", true, func(ctx context.Context, rt *Runtime) error {
				res, err := app.Status(ctx, rt.Env, rt.Global, opts)
				if err != nil {
					return err
				}
				switch {
				case rt.Global.JSON:
					if err := rt.JSON.Status(res); err != nil {
						return err
					}
				case byGroup:
					rt.Human.StatusByGroup(res)
				default:
					rt.Human.Status(res)
				}
				attention := res.Summary.NeedsAttention() || (res.Deps != nil && res.Deps.Any())
				finish(rt, setExit, res.Summary.Failed > 0, attention)
				return nil
			})
		},
	}

	cmd.Flags().BoolVar(&opts.Fetch, "fetch", false, "fetch first, for live ahead/behind numbers")
	cmd.Flags().BoolVar(&opts.NoDeps, "no-deps", false, "skip the dependency summary")
	// Not the default: a repo may belong to several groups, so grouping either duplicates rows or
	// picks one arbitrarily. Manifest order avoids both (spec §7.2).
	cmd.Flags().BoolVar(&byGroup, "by-group", false, "group the report by group (repos may repeat)")
	return cmd
}

func newSyncCommand(ctx context.Context, newRuntime runtimeFactory, setExit exitSetter) *cobra.Command {
	var opts app.SyncOptions

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Fast-forward every repo that can be",
		Long: "sync fetches and fast-forwards. It never touches uncommitted work and never\n" +
			"creates a conflict: a repo that is dirty, detached, without an upstream, or\n" +
			"diverged is skipped and reported with the command that would resolve it.\n\n" +
			"It does not clone missing repos and does not push. Use `gits up` for the whole cycle.",
		Run: func(cmd *cobra.Command, _ []string) {
			run(ctx, newRuntime, setExit, cmd, "sync", true, func(ctx context.Context, rt *Runtime) error {
				res, err := app.Sync(ctx, rt.Env, rt.Global, opts)
				if err != nil {
					return err
				}
				if rt.Global.JSON {
					if err := rt.JSON.Sync(res, rt.Global.DryRun); err != nil {
						return err
					}
				} else {
					rt.Human.Sync(res, rt.Global.DryRun)
				}
				finish(rt, setExit, res.Failed(), res.Summary.NeedsAttention() || res.ManifestStale)
				return nil
			})
		},
	}

	cmd.Flags().BoolVar(&opts.NoSubmodules, "no-submodules", false, "skip submodule updates after a fast-forward")
	return cmd
}

func newPushCommand(ctx context.Context, newRuntime runtimeFactory, setExit exitSetter) *cobra.Command {
	var opts app.PushOptions

	cmd := &cobra.Command{
		Use:   "push",
		Short: "Push every repo that is ahead",
		Long: "push publishes repos whose branch is ahead of its upstream, after showing the\n" +
			"plan and asking for confirmation. no-write repos are excluded automatically.\n\n" +
			"There is no force push in any form. If you need one, cd into the repo, where the\n" +
			"consequences are local and visible.",
		Run: func(cmd *cobra.Command, _ []string) {
			run(ctx, newRuntime, setExit, cmd, "push", true, func(ctx context.Context, rt *Runtime) error {
				res, err := app.Push(ctx, rt.Env, rt.Global, opts)
				if err != nil {
					return err
				}
				if rt.Global.JSON {
					if err := rt.JSON.Push(res); err != nil {
						return err
					}
				} else {
					rt.Human.Results("Push", res.Repos, res.Skipped, res.Summary, res.DryRun)
				}
				finish(rt, setExit, app.AnyFailed(res.Repos), res.Summary.NeedsAttention())
				return nil
			})
		},
	}

	cmd.Flags().BoolVarP(&opts.SetUpstream, "set-upstream", "u", false, "push branches that have no upstream, creating it")
	return cmd
}

func newCommitCommand(ctx context.Context, newRuntime runtimeFactory, setExit exitSetter) *cobra.Command {
	var opts app.CommitOptions

	cmd := &cobra.Command{
		Use:   "commit",
		Short: "Commit pending changes across the workspace",
		Long: "commit walks the repos with uncommitted work. With -m it applies one message to\n" +
			"all of them; without -m it reviews them one at a time, which needs a terminal.\n\n" +
			"Only tracked changes are committed unless -A is given, so local config and build\n" +
			"output are never swept in. Signing, hooks and the editor stay exactly as each repo\n" +
			"configures them, and commit never pushes.",
		Run: func(cmd *cobra.Command, _ []string) {
			run(ctx, newRuntime, setExit, cmd, "commit", true, func(ctx context.Context, rt *Runtime) error {
				res, err := app.Commit(ctx, rt.Env, rt.Global, opts)
				if err != nil {
					return err
				}
				if rt.Global.JSON {
					if err := rt.JSON.Commit(res); err != nil {
						return err
					}
				} else {
					rt.Human.Results("Commit", res.Repos, res.Skipped, res.Summary, res.DryRun)
				}
				finish(rt, setExit, app.AnyFailed(res.Repos), false)
				return nil
			})
		},
	}

	cmd.Flags().StringVarP(&opts.Message, "message", "m", "", "commit message applied to every selected repo")
	cmd.Flags().BoolVarP(&opts.All, "all", "A", false, "include untracked files")
	return cmd
}

func newCloneCommand(ctx context.Context, newRuntime runtimeFactory, setExit exitSetter) *cobra.Command {
	var opts app.CloneOptions

	cmd := &cobra.Command{
		Use:   "clone",
		Short: "Clone the repos the manifest lists but this machine lacks",
		Long: "clone materialises whatever the manifest knows about and this machine does not\n" +
			"have. It is what makes picking up work on a second computer a single step.\n\n" +
			"A path that already holds something that is not a git repo is left untouched.",
		Run: func(cmd *cobra.Command, _ []string) {
			run(ctx, newRuntime, setExit, cmd, "clone", true, func(ctx context.Context, rt *Runtime) error {
				res, err := app.Clone(ctx, rt.Env, rt.Global, opts)
				if err != nil {
					return err
				}
				if rt.Global.JSON {
					if err := rt.JSON.Clone(res); err != nil {
						return err
					}
				} else {
					rt.Human.Results("Clone", res.Repos, res.Skipped, res.Summary, res.DryRun)
				}
				finish(rt, setExit, app.AnyFailed(res.Repos), res.Summary.NeedsAttention())
				return nil
			})
		},
	}

	cmd.Flags().BoolVar(&opts.NoSubmodules, "no-submodules", false, "do not initialise submodules")
	return cmd
}

func newInitCommand(ctx context.Context, newRuntime runtimeFactory, setExit exitSetter) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create a manifest for the current directory",
		Long: "init scans this directory for git repos and writes a gits.yaml describing them,\n" +
			"including the directory itself when it is a repo.\n\n" +
			"Groups and no-write are left blank: ownership is not something a scan can infer,\n" +
			"and a confident wrong guess is worse than none. init also checks that .gitignore\n" +
			"is not quietly excluding the manifest, which would leave nothing to sync across.",
		Run: func(cmd *cobra.Command, _ []string) {
			// needManifest is false: creating the manifest is the point.
			run(ctx, newRuntime, setExit, cmd, "init", false, func(ctx context.Context, rt *Runtime) error {
				res, err := app.Init(ctx, rt.Env, rt.Global)
				if err != nil {
					return err
				}
				if rt.Global.JSON {
					return rt.JSON.Init(res, rt.Global.DryRun)
				}
				rt.Human.Init(res)
				return nil
			})
		},
	}
}

func newAdoptCommand(ctx context.Context, newRuntime runtimeFactory, setExit exitSetter) *cobra.Command {
	var opts app.AdoptOptions

	cmd := &cobra.Command{
		Use:   "adopt",
		Short: "Register repos that exist on disk but are not in the manifest",
		Long: "adopt is the mirror image of clone: clone materialises what the list knows about,\n" +
			"adopt teaches the list about what is already here.\n\n" +
			"It also reports both kinds of drift it notices -- entries with no directory, and\n" +
			"checkouts whose origin disagrees with the manifest -- without acting on either.",
		Run: func(cmd *cobra.Command, _ []string) {
			run(ctx, newRuntime, setExit, cmd, "adopt", true, func(ctx context.Context, rt *Runtime) error {
				res, err := app.Adopt(ctx, rt.Env, rt.Global, opts)
				if err != nil {
					return err
				}
				if rt.Global.JSON {
					return rt.JSON.Adopt(res)
				}
				rt.Human.Adopt(res)
				return nil
			})
		},
	}

	cmd.Flags().StringArrayVar(&opts.Groups, "group-tag", nil, "group to apply to every adopted entry (repeatable)")
	cmd.Flags().BoolVar(&opts.NoWrite, "no-write", false, "mark every adopted entry as no-write")
	return cmd
}

func newAddCommand(ctx context.Context, newRuntime runtimeFactory, setExit exitSetter) *cobra.Command {
	var (
		url         string
		path        string
		branch      string
		remote      string
		groups      []string
		noWrite     bool
		description string
		update      bool
	)

	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Register a single repo in the manifest",
		Long: "add registers one repo. It is the supported way for a script or an agent to extend\n" +
			"the list: editing gits.yaml directly loses the comments, which are where the\n" +
			"reasoning behind entries lives.\n\n" +
			"It writes the manifest only. Run `gits clone -r <name>` when you want the checkout.",
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			run(ctx, newRuntime, setExit, cmd, "add", true, func(ctx context.Context, rt *Runtime) error {
				res, err := app.Add(ctx, rt.Env, rt.Global, app.AddOptions{
					Repo: domain.Repo{
						Name:        args[0],
						URL:         url,
						Path:        path,
						Branch:      branch,
						Remote:      remote,
						Groups:      groups,
						NoWrite:     noWrite,
						Description: description,
					},
					Update: update,
				})
				if err != nil {
					return err
				}
				if rt.Global.JSON {
					return rt.JSON.Add(res)
				}
				rt.Human.Add(res)
				return nil
			})
		},
	}

	cmd.Flags().StringVar(&url, "url", "", "clone URL (required)")
	cmd.Flags().StringVar(&path, "path", "", "workspace-relative path (default: the name)")
	cmd.Flags().StringVar(&branch, "branch", "", "default branch (default: defaults.branch)")
	cmd.Flags().StringVar(&remote, "remote", "", "remote name (default: defaults.remote)")
	cmd.Flags().StringArrayVar(&groups, "group-tag", nil, "group label (repeatable)")
	cmd.Flags().BoolVar(&noWrite, "no-write", false, "exclude from every write command")
	cmd.Flags().StringVar(&description, "description", "", "one-line description")
	cmd.Flags().BoolVar(&update, "update", false, "overwrite an existing entry")
	return cmd
}

func newListCommand(ctx context.Context, newRuntime runtimeFactory, setExit exitSetter) *cobra.Command {
	var (
		names  bool
		format string
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List what the manifest declares",
		Long: "list reads the manifest and nothing else -- no directories, no git, no network --\n" +
			"so it answers \"what is in this workspace, where, and what may I write to\" in\n" +
			"milliseconds.\n\n" +
			"--format=markdown emits a table you can paste into CLAUDE.md or a README, so the\n" +
			"repo table in your docs is generated rather than hand-maintained.",
		Run: func(cmd *cobra.Command, _ []string) {
			run(ctx, newRuntime, setExit, cmd, "list", true, func(ctx context.Context, rt *Runtime) error {
				res, err := app.List(ctx, rt.Env, rt.Global)
				if err != nil {
					return err
				}
				switch {
				case rt.Global.JSON:
					return rt.JSON.List(res)
				case names:
					rt.Human.ListNames(res)
				case format == "markdown":
					rt.Human.ListMarkdown(res)
				default:
					rt.Human.List(res)
				}
				return nil
			})
		},
	}

	cmd.Flags().BoolVar(&names, "names", false, "print one name per line, for shell loops")
	cmd.Flags().StringVar(&format, "format", "", "output format: markdown")
	return cmd
}

func newDepsCommand(ctx context.Context, newRuntime runtimeFactory, setExit exitSetter) *cobra.Command {
	var opts app.DepsOptions

	cmd := &cobra.Command{
		Use:   "deps",
		Short: "Report cross-repo submodule dependencies",
		Long: "deps answers \"who in this workspace is pinned to an old version of what\". It is\n" +
			"derived entirely from git metadata that already exists -- .gitmodules plus the\n" +
			"gitlink SHA -- so there is nothing to declare and nothing to keep in sync.\n\n" +
			"Each pin is judged against the branch its own repo declared, falling back to the\n" +
			"dependency's default branch, and always compared against a remote-tracking ref\n" +
			"rather than whatever the canonical checkout happens to have checked out.\n\n" +
			"It reports drift -- behind, diverged, inconsistent -- and does not claim breakage.",
		Run: func(cmd *cobra.Command, _ []string) {
			run(ctx, newRuntime, setExit, cmd, "deps", true, func(ctx context.Context, rt *Runtime) error {
				groups, err := app.Deps(ctx, rt.Env, rt.Global, opts)
				if err != nil {
					return err
				}
				if rt.Global.JSON {
					if err := rt.JSON.Deps(groups, opts.Fetch); err != nil {
						return err
					}
				} else {
					rt.Human.Deps(groups)
				}
				finish(rt, setExit, false, domain.SummarizeDeps(groups).Any())
				return nil
			})
		},
	}

	cmd.Flags().BoolVar(&opts.Fetch, "fetch", false, "refresh the canonical repos' remote refs first")
	return cmd
}

func newForeachCommand(ctx context.Context, newRuntime runtimeFactory, setExit exitSetter) *cobra.Command {
	var includeNoWrite bool

	cmd := &cobra.Command{
		Use:   "foreach -- <command> [args...]",
		Short: "Run a command in every selected repo",
		Long: "foreach is the escape hatch for everything gits does not wrap.\n\n" +
			"The command is opaque to gits -- there is no way to tell `git log` from\n" +
			"`git reset --hard` -- so every run is treated as a write and no-write repos are\n" +
			"excluded unless you pass --include-no-write.\n\n" +
			"Each repo's output is captured separately and capped at 8KB, with truncation\n" +
			"flagged, so one noisy repo cannot swamp the caller.",
		Example: "  gits foreach -- git log --oneline -1\n" +
			"  gits foreach -g game --json -- git submodule update --remote proto",
		Args: cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			run(ctx, newRuntime, setExit, cmd, "foreach", true, func(ctx context.Context, rt *Runtime) error {
				res, err := app.Foreach(ctx, rt.Env, rt.Global, app.ForeachOptions{
					Args:           args,
					IncludeNoWrite: includeNoWrite,
				})
				if err != nil {
					return err
				}
				if rt.Global.JSON {
					if err := rt.JSON.Foreach(res); err != nil {
						return err
					}
				} else {
					rt.Human.Foreach(res)
				}
				finish(rt, setExit, res.Failed(), false)
				return nil
			})
		},
	}

	cmd.Flags().BoolVar(&includeNoWrite, "include-no-write", false, "also run in no-write repos")
	return cmd
}
