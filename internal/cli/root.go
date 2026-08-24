// Package cli wires the use cases to a cobra command tree and maps their results onto the spec's
// exit codes.
//
// Nothing here decides what a command does; it parses flags, builds the adapters, calls one use
// case and hands the result to a renderer.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/nekogravitycat/gits/internal/adapter/fsys"
	adaptergit "github.com/nekogravitycat/gits/internal/adapter/git"
	"github.com/nekogravitycat/gits/internal/adapter/manifest"
	"github.com/nekogravitycat/gits/internal/adapter/output"
	"github.com/nekogravitycat/gits/internal/adapter/ui"
	"github.com/nekogravitycat/gits/internal/app"
	"github.com/nekogravitycat/gits/internal/domain"
)

// Runtime carries everything a command needs, assembled once per invocation from the global flags.
type Runtime struct {
	Global app.Global
	Env    *app.Env

	JSON  *output.JSON
	Human *output.Human

	Stdout io.Writer
	Stderr io.Writer
}

// globalFlags holds the raw flag values before they are resolved into app.Global.
type globalFlags struct {
	workspace string

	groups   []string
	repos    []string
	excludes []string

	yes      bool
	dryRun   bool
	jsonOut  bool
	verbose  bool
	plain    bool
	exitCode bool

	maxRepos int
	timeout  time.Duration
	jobs     int
}

// version is stamped at build time with -ldflags.
var version = "dev"

// Execute builds the command tree and runs it, returning the process exit code.
//
// Errors are never allowed to escape as a panic or a bare message: every path ends in one of the
// spec's exit codes, with a stable error code attached (spec §6.10).
func Execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var flags globalFlags

	root := &cobra.Command{
		Use:   "gits",
		Short: "Keep a workspace of many git repos in step",
		Long: "gits is a scheduling and reporting layer over git for a directory holding many\n" +
			"related repos. Every command works the same for a person at a terminal and for a\n" +
			"program parsing --json.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}

	// Flags shared by every command (spec §6.2, §6.12).
	pf := root.PersistentFlags()
	pf.StringVarP(&flags.workspace, "workspace", "w", "", "workspace directory (default: search upward for "+app.ManifestName+")")
	pf.StringArrayVarP(&flags.groups, "group", "g", nil, "only repos in this group (repeatable, unioned)")
	pf.StringArrayVarP(&flags.repos, "repo", "r", nil, "only this repo (repeatable)")
	pf.StringArrayVar(&flags.excludes, "exclude", nil, "exclude this repo (repeatable)")
	pf.BoolVarP(&flags.yes, "yes", "y", false, "skip confirmation prompts")
	pf.BoolVarP(&flags.dryRun, "dry-run", "n", false, "report what would happen, change nothing")
	pf.BoolVar(&flags.jsonOut, "json", false, "machine-readable output; one JSON object on stdout")
	pf.BoolVarP(&flags.verbose, "verbose", "v", false, "show the git commands being run (on stderr)")
	pf.BoolVar(&flags.plain, "plain", false, "plain ASCII output with no colour")
	pf.BoolVar(&flags.exitCode, "exit-code", false, "exit 3 when something needs attention")
	pf.IntVar(&flags.maxRepos, "max-repos", 0, "refuse to act on more than this many repos")
	pf.DurationVar(&flags.timeout, "timeout", app.DefaultTimeout, "timeout for a single git subprocess")
	pf.IntVarP(&flags.jobs, "jobs", "j", 0, "parallelism (default: min(8, CPUs))")

	exit := app.ExitOK
	// setExit keeps the highest-severity outcome: a later "nothing to report" must not overwrite
	// an earlier failure.
	setExit := func(code app.ExitCode) {
		if code > exit {
			exit = code
		}
	}

	newRuntime := func(needManifest bool) (*Runtime, error) {
		return buildRuntime(&flags, stdout, stderr, needManifest)
	}

	root.AddCommand(
		newUpCommand(ctx, newRuntime, setExit),
		newStatusCommand(ctx, newRuntime, setExit),
		newSyncCommand(ctx, newRuntime, setExit),
		newPushCommand(ctx, newRuntime, setExit),
		newCommitCommand(ctx, newRuntime, setExit),
		newCloneCommand(ctx, newRuntime, setExit),
		newInitCommand(ctx, newRuntime, setExit),
		newAdoptCommand(ctx, newRuntime, setExit),
		newAddCommand(ctx, newRuntime, setExit),
		newListCommand(ctx, newRuntime, setExit),
		newFmtCommand(ctx, newRuntime, setExit),
		newDepsCommand(ctx, newRuntime, setExit),
		newForeachCommand(ctx, newRuntime, setExit),
	)

	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	if err := root.Execute(); err != nil {
		// A cobra-level failure is an argument problem, which is exit 2 by definition.
		fmt.Fprintf(stderr, "gits: %v\n", err)
		return int(app.ExitUsage)
	}
	return int(exit)
}

// buildRuntime resolves the global flags into ports and renderers.
func buildRuntime(flags *globalFlags, stdout, stderr io.Writer, needManifest bool) (*Runtime, error) {
	var workspace string
	var err error
	if needManifest {
		workspace, err = FindWorkspace(flags.workspace)
	} else {
		workspace, err = WorkspaceForInit(flags.workspace)
	}
	if err != nil {
		return nil, err
	}

	// --json implies --plain: decorative text in a machine payload is noise at best (spec §6.4).
	plain := flags.plain || flags.jsonOut

	global := app.Global{
		Filter: domain.Filter{
			Groups:   flags.groups,
			Repos:    flags.repos,
			Excludes: flags.excludes,
		},
		// GITS_YES lets a whole CI or agent environment opt in once rather than threading -y
		// through every call (spec §6.7 rule 4).
		Yes:      flags.yes || os.Getenv(YesEnv) == "1",
		DryRun:   flags.dryRun,
		JSON:     flags.jsonOut,
		Verbose:  flags.verbose,
		Plain:    plain,
		ExitCode: flags.exitCode,
		MaxRepos: flags.maxRepos,
		Timeout:  flags.timeout,
		Jobs:     flags.jobs,
	}

	// In-place progress redraw needs a real terminal on the receiving end -- piping stderr to a
	// file or another process, or downgrading with --plain/--json, all fall back to one plain
	// line per event (spec §6.4's ASCII/no-colour fallback, extended to progress).
	live := IsTerminal(os.Stderr) && !plain
	logger := ui.NewLogger(stderr, flags.verbose, live)

	// Prompts are only ever possible on a terminal. Everything downstream checks IsInteractive
	// before asking anything, and fails with E_NEEDS_YES instead of waiting (spec §6.7).
	interactive := IsTerminal(os.Stdin) && IsTerminal(os.Stderr)
	prompter := ui.NewPrompter(os.Stdin, stderr, interactive)

	env := &app.Env{
		Workspace: workspace,
		Git:       adaptergit.New(flags.timeout, logger),
		FS:        fsys.New(),
		Store:     manifest.New(),
		Prompt:    prompter,
		Log:       logger,
	}

	style := output.NewStyle(IsTerminal(os.Stdout), plain)
	return &Runtime{
		Global: global,
		Env:    env,
		JSON:   output.NewJSON(stdout, workspace),
		Human:  output.NewHuman(stdout, style, workspace),
		Stdout: stdout,
		Stderr: stderr,
	}, nil
}

// runtimeFactory builds a Runtime for one command invocation.
type runtimeFactory func(needManifest bool) (*Runtime, error)

// exitSetter records a command's outcome for the process exit code.
type exitSetter func(app.ExitCode)

// reportError renders a fatal error in whichever mode the caller asked for and returns its exit
// code.
//
// A failure still produces JSON in --json mode. A caller that gets prose on failure and JSON on
// success has to write two parsers, and in practice writes one (spec §6.4).
func reportError(rt *Runtime, command string, err error, jsonMode bool, stdout, stderr io.Writer) app.ExitCode {
	code := app.ExitFailure
	var ae *app.Error
	if errorsAs(err, &ae) && ae.Exit != 0 {
		code = ae.Exit
	}

	if jsonMode {
		j := output.NewJSON(stdout, workspaceOf(rt))
		if werr := j.Error(command, err); werr != nil {
			fmt.Fprintf(stderr, "gits: %v\n", err)
		}
		return code
	}
	if rt != nil {
		rt.Human.Error(err)
		return code
	}
	fmt.Fprintf(stderr, "gits: %v (%s)\n", app.MessageOf(err), app.CodeOf(err))
	if ae != nil && ae.Hint != "" {
		fmt.Fprintf(stderr, "  -> %s\n", ae.Hint)
	}
	return code
}

func workspaceOf(rt *Runtime) string {
	if rt == nil {
		return ""
	}
	return rt.Env.Workspace
}
