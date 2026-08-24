package app

import (
	"context"

	"github.com/nekogravitycat/gits/internal/domain"
)

// The interfaces in this file are declared by the use cases that consume them, not by the adapters
// that implement them. That is what keeps the dependency arrows pointing inward: app knows nothing
// about subprocesses or YAML, and every use case can be exercised against an in-memory fake with
// no git binary present.
//
// They are deliberately narrow. A use case takes the smallest interface that covers what it does,
// so its test fake only has to implement what it actually calls.

// Inspector covers every read-only question gits asks of a repository. Nothing here mutates a
// worktree or touches the network.
type Inspector interface {
	// IsRepo reports whether dir is a git worktree. dir is an absolute path.
	IsRepo(ctx context.Context, dir string) (bool, error)

	// Status returns the raw observations behind a repo's state, parsed from
	// `git status --porcelain=v2 --branch`.
	Status(ctx context.Context, dir string) (RepoObservation, error)

	// RemoteURL returns the configured URL for a remote, or "" when the remote does not exist.
	RemoteURL(ctx context.Context, dir, remote string) (string, error)

	// ListSubmodules returns the repo's .gitmodules entries with the gitlink SHA recorded in
	// HEAD's tree. A repo without .gitmodules yields no entries and no error.
	ListSubmodules(ctx context.Context, dir string) ([]domain.Submodule, error)

	// ResolveRef resolves a ref to a full commit SHA. Reports ok=false when the ref does not
	// exist, which is a normal answer rather than an error -- an un-fetched remote-tracking branch
	// is missing, not broken.
	ResolveRef(ctx context.Context, dir, ref string) (sha string, ok bool, err error)

	// CommitExists reports whether a commit object is present in this repo. Used to tell "the pin
	// is on a different line of development" apart from "the pin is not in our object store yet",
	// which need very different advice.
	CommitExists(ctx context.Context, dir, sha string) (bool, error)

	// CountAheadBehind returns the two-way commit count between a and b, as
	// `git rev-list --left-right --count a...b` reports it: ahead counts commits reachable from a
	// but not b, behind the reverse.
	//
	// Both numbers are always required. A one-way count returns a plausible-looking number for a
	// commit that is not an ancestor at all, which silently turns a divergence into what reads as
	// a simple "behind N" (spec §7.11).
	CountAheadBehind(ctx context.Context, dir, a, b string) (ahead, behind int, err error)

	// BranchContains names the branches containing a commit, as a hint about where a diverged pin
	// came from.
	BranchContains(ctx context.Context, dir, sha string) ([]string, error)

	// DiffStat and Diff render pending changes for the interactive commit review.
	DiffStat(ctx context.Context, dir string) (string, error)
	Diff(ctx context.Context, dir string) (string, error)

	// IsIgnored reports whether git would ignore the given workspace-relative path.
	//
	// A workspace using a "* plus allowlist" .gitignore silently swallows gits.yaml: `git add`
	// reports no error, the file never enters version control, and the problem only surfaces on
	// the second machine when nothing has synced (spec §7.7).
	IsIgnored(ctx context.Context, dir, relPath string) (bool, error)
}

// Mutator covers the operations that change a worktree or talk to a remote.
type Mutator interface {
	// Fetch runs `git fetch --prune <remote>`.
	Fetch(ctx context.Context, dir, remote string) error

	// MergeFFOnly fast-forwards the current branch to ref, refusing anything that would create a
	// merge commit. gits never resolves conflicts on the user's behalf.
	MergeFFOnly(ctx context.Context, dir, ref string) error

	// SubmoduleUpdate runs `git submodule update --init --recursive`, so that submodule worktrees
	// match the gitlinks that were just fast-forwarded.
	SubmoduleUpdate(ctx context.Context, dir string) error

	// Clone clones url into dir at the given branch, then optionally initialises submodules.
	Clone(ctx context.Context, url, dir, branch string, withSubmodules bool) error

	// Commit commits pending changes and returns the new commit's short SHA. When addUntracked is
	// false only tracked modifications are committed, which is the default: sweeping up untracked
	// files silently commits local config and build output (spec §7.5).
	Commit(ctx context.Context, dir, message string, addUntracked bool) (sha string, err error)

	// Push pushes branch to remote. setUpstream adds -u for a branch that has none.
	Push(ctx context.Context, dir, remote, branch string, setUpstream bool) error
}

// CommandRunner backs `foreach`, the escape hatch for everything gits does not wrap.
type CommandRunner interface {
	// Run executes a command in dir and returns its captured output. A non-zero exit is reported
	// through exitCode, not through err: err is reserved for gits failing to run the command at
	// all (timeout, binary missing), which is a different thing from the command itself failing.
	Run(ctx context.Context, dir string, args []string) (exitCode int, stdout, stderr string, err error)
}

// Git is the full port, implemented by the git adapter. Use cases should depend on the narrow
// interfaces above; this exists so main() has one thing to wire.
type Git interface {
	Inspector
	Mutator
	CommandRunner
}

// RepoObservation is what a single `git status --porcelain=v2 --branch` call reveals.
//
// It is raw observation only: turning it into a domain.RepoState is the domain's job, so the
// priority ordering lives in one place and is testable without git.
type RepoObservation struct {
	// Branch is the current branch name, empty when HEAD is detached.
	Branch   string
	Detached bool

	// Head is the current commit SHA.
	Head string

	// Upstream is the tracking ref (e.g. "origin/main"), empty when the branch has none.
	Upstream string
	Ahead    int
	Behind   int

	Dirty domain.Dirty

	// HasSubmodules reports whether the repo declares any submodules; SubmodulesClean is only
	// meaningful when it is true.
	HasSubmodules   bool
	SubmodulesClean bool
}

// FS is the minimal filesystem access the use cases need, kept behind a port so that a use case
// test does not have to materialise a directory tree just to answer "does this exist".
type FS interface {
	// DirExists reports whether path is an existing directory.
	//
	// Distinguishing this from "exists but is not a repo" is what separates E_MISSING_DIR from
	// E_NOT_A_REPO, and the two call for different advice: clone it, versus look at what is
	// sitting in the way.
	DirExists(path string) (bool, error)

	// FileExists reports whether path is an existing regular file.
	FileExists(path string) (bool, error)

	// ListDirs returns the names of the immediate subdirectories of path, sorted. It is how init
	// and adopt discover candidate repos.
	ListDirs(path string) ([]string, error)

	// ReadFile and WriteFile handle the auxiliary files gits touches (.gitignore, editor
	// scratch files). The manifest itself goes through ManifestStore instead.
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte) error
}

// ManifestStore reads and writes the workspace manifest.
//
// Writing goes through the yaml.v3 node API so that comments and formatting survive a round trip:
// the comments are where "why is this repo no-write" is recorded, and a plain marshal would erase
// them (spec §5.1).
type ManifestStore interface {
	// Load reads gits.yaml, applies gits.local.yaml if present, and validates the result.
	Load(workspace string) (*domain.Manifest, error)

	// AddRepo inserts an entry, preserving surrounding comments and formatting.
	//
	// Entries are inserted in name order rather than appended. Two machines that each add a repo
	// otherwise produce two insertions at the same spot -- a guaranteed conflict, and the most
	// annoying kind of YAML conflict to resolve by hand (spec §10.1).
	AddRepo(workspace string, repo domain.Repo, update bool) (Written, error)

	// Create writes a new manifest. It refuses to overwrite an existing one.
	Create(workspace string, m *domain.Manifest) error

	// Format rewrites the workspace's manifest files in canonical form: entries in name order,
	// one blank line between them, fields in a fixed order, comments preserved.
	//
	// It returns one result per file it looked at -- gits.yaml always, gits.local.yaml when this
	// machine has one. With apply false nothing is written; the results still report whether each
	// file was already canonical, which is what --dry-run needs and what makes a check-only run
	// possible.
	Format(workspace string, apply bool) ([]Formatted, error)
}

// Formatted reports what formatting one manifest file did, or would do.
type Formatted struct {
	// File is the file's name, gits.yaml or gits.local.yaml. Not a path: the workspace is already
	// in the JSON header, and a bare name reads the same on every OS.
	File string

	// Entries is how many entries the file's list holds.
	Entries int

	// Changed reports that the file on disk differed from its canonical form.
	Changed bool

	// Reordered names the entries the sort moved, in their new order. Empty when nothing moved,
	// which is the normal case for a file only gits has written.
	Reordered []string
}

// Written reports what a manifest write actually did, so callers can distinguish a real change
// from an idempotent no-op (spec §6.11).
type Written struct {
	Added   bool
	Updated bool
	NoOp    bool
}

// Prompter is the human interaction port. Every method must be unreachable when stdin is not a
// terminal: the caller checks IsInteractive first and fails with E_NEEDS_YES instead.
//
// This is the single most important non-blocking rule in the tool. A prompt written to a
// non-terminal produces a process with no output that never exits, which the caller usually only
// notices as a timeout with nothing to debug (spec §6.7).
type Prompter interface {
	IsInteractive() bool

	// Confirm asks a yes/no question, defaulting to no.
	Confirm(question string) (bool, error)

	// Line reads one line of input.
	Line(prompt string) (string, error)

	// Editor opens the user's configured editor and returns what they wrote, for multi-line
	// commit messages.
	Editor(initial string) (string, error)
}

// Logger writes progress and diagnostics.
//
// Everything it emits goes to stderr, always. In --json mode stdout must carry exactly one JSON
// object and nothing else, or piping into jq breaks (spec §6.4).
type Logger interface {
	// Verbosef writes only under -v: the git commands being run and their output.
	Verbosef(format string, args ...any)
	// Warnf writes a warning the user should see regardless of verbosity.
	Warnf(format string, args ...any)
}
