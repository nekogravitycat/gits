package app

// Architecture Note:
// - Env is built once in main() and passed down; use cases never construct their own adapters.
// - mapRepos runs work concurrently but returns results in manifest order, so two runs of the same
//   command produce byte-identical reports (spec §6.3, §6.5).
// - Ports (see ports.go) are declared by the use cases, keeping dependency arrows pointing inward.

import (
	"context"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nekogravitycat/gits/internal/domain"
)

// ManifestName and LocalManifestName are the fixed file names a workspace is recognised by
// (spec §5.1, §5.5).
const (
	ManifestName      = "gits.yaml"
	LocalManifestName = "gits.local.yaml"
)

// DefaultTimeout bounds every git subprocess (spec §6.8).
// CRITICAL: without a ceiling a stuck pre-commit hook hangs the whole run indefinitely.
const DefaultTimeout = 120 * time.Second

// Global holds the flags every command accepts (spec §6.12).
type Global struct {
	Filter domain.Filter

	Yes      bool
	DryRun   bool
	JSON     bool
	Verbose  bool
	Plain    bool
	ExitCode bool

	// MaxRepos refuses a plan touching more than this many repos. Zero means no ceiling.
	MaxRepos int

	Timeout time.Duration
	Jobs    int
}

// Concurrency resolves the effective parallelism (spec §6.3).
func (g Global) Concurrency() int {
	if g.Jobs > 0 {
		return g.Jobs
	}
	if n := runtime.NumCPU(); n < 8 {
		return max(n, 1)
	}
	return 8
}

// Env bundles the workspace location and the ports a use case needs.
type Env struct {
	// Workspace is the absolute path to the workspace root.
	Workspace string

	Git    Git
	FS     FS
	Store  ManifestStore
	Prompt Prompter
	Log    Logger
}

// ManifestPath returns the absolute path to gits.yaml.
func (e *Env) ManifestPath() string { return filepath.Join(e.Workspace, ManifestName) }

// Dir resolves a manifest entry to an absolute directory. The root repo (path ".") resolves to the
// workspace directory itself, not a join (spec §5.4).
func (e *Env) Dir(r domain.Repo) string {
	p := r.EffectivePath()
	if p == domain.RootPath {
		return e.Workspace
	}
	return filepath.Join(e.Workspace, filepath.FromSlash(p))
}

// LoadManifest reads and validates the workspace manifest.
func (e *Env) LoadManifest() (*domain.Manifest, error) {
	return e.Store.Load(e.Workspace)
}

// Select resolves the caller's filter against the manifest.
//
// An unknown name/group is a usage error, not an empty selection: a mistyped `-r` must not report
// "nothing to do, all good".
func Select(m *domain.Manifest, g Global, opts domain.SelectOpts) ([]domain.Repo, []domain.Excluded, error) {
	if unknown := m.UnknownSelectors(g.Filter); len(unknown) > 0 {
		return nil, nil, Usagef(domain.ErrManifest, "no repo named %q in the manifest", unknown[0]).
			WithHint("gits list --names")
	}
	if unknown := m.UnknownGroups(g.Filter); len(unknown) > 0 {
		return nil, nil, Usagef(domain.ErrManifest, "no repo belongs to group %q", unknown[0]).
			WithHint("gits list --json")
	}
	selected, skipped := m.Select(g.Filter, opts)
	return selected, skipped, nil
}

// CheckMaxRepos enforces the --max-repos ceiling before any work begins.
func CheckMaxRepos(g Global, planned int) error {
	if g.MaxRepos > 0 && planned > g.MaxRepos {
		return ErrMaxRepos(planned, g.MaxRepos)
	}
	return nil
}

// Confirm gates a write behind the user's approval. --dry-run never needs approval since it
// changes nothing; requiring --yes for it would only train callers to pass --yes habitually
// (spec §6.7 rule 3).
func (e *Env) Confirm(g Global, command, question string) error {
	if g.Yes || g.DryRun {
		return nil
	}
	if !e.Prompt.IsInteractive() {
		return ErrNeedsYes(command)
	}
	ok, err := e.Prompt.Confirm(question)
	if err != nil {
		return &Error{Code: domain.ErrNeedsYes, Msg: "could not read confirmation", Exit: ExitUsage, Err: err}
	}
	if !ok {
		return &Error{Code: domain.ErrNeedsYes, Msg: "aborted", Exit: ExitInterrupted}
	}
	return nil
}

// mapRepos runs fn over repos concurrently and returns results in manifest order.
//
// NOTE: results are written by index into a pre-sized slice and read only after all workers finish,
// so output never depends on completion order (spec §6.3, §6.5).
//
// The semaphore is acquired before a goroutine is even spawned, so --jobs is a real concurrency
// bound rather than just a cap on how many run at once. A repo that is still waiting for a slot
// when ctx is cancelled (SIGINT) never runs fn at all; cancelled supplies its result instead of
// leaving the zero value, which would otherwise vanish from every bucket a caller tallies against
// (spec's report-must-reconcile invariant).
func mapRepos[T any](ctx context.Context, jobs int, repos []domain.Repo, fn func(context.Context, domain.Repo) T, cancelled func(domain.Repo) T) []T {
	results := make([]T, len(repos))
	if len(repos) == 0 {
		return results
	}
	if jobs < 1 {
		jobs = 1
	}

	sem := make(chan struct{}, jobs)
	var wg sync.WaitGroup
	for i, r := range repos {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			results[i] = cancelled(r)
			continue
		}
		wg.Add(1)
		go func(i int, r domain.Repo) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = fn(ctx, r)
		}(i, r)
	}
	wg.Wait()
	return results
}

// withProgress wraps fn so each repo in a mapRepos batch reports as it finishes. Call
// log.Progress(stage, 0, total, "") before the batch and log.ProgressDone() after.
//
// NOTE: the counter is atomic because completions arrive from worker goroutines out of order.
func withProgress[T any](log Logger, stage string, total int, fn func(context.Context, domain.Repo) T) func(context.Context, domain.Repo) T {
	var done int32
	return func(ctx context.Context, r domain.Repo) T {
		out := fn(ctx, r)
		n := atomic.AddInt32(&done, 1)
		log.Progress(stage, int(n), total, r.Name)
		return out
	}
}
