package app

import (
	"context"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/nekogravitycat/gits/internal/domain"
)

// ManifestName and LocalManifestName are the fixed file names a workspace is recognised by
// (spec §5.1, §5.5).
const (
	ManifestName      = "gits.yaml"
	LocalManifestName = "gits.local.yaml"
)

// DefaultTimeout bounds every git subprocess (spec §6.8). A stuck pre-commit hook is routine, and
// without a ceiling there is no ceiling at all.
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

	// MaxRepos refuses a plan that would touch more than this many repos. Zero means no ceiling.
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

// Env bundles the workspace location and the ports a use case needs. It is built once in main()
// and passed down; nothing below constructs an adapter for itself.
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

// Dir resolves a manifest entry to an absolute directory.
//
// The root repo (path ".") resolves to the workspace directory itself, which is why this is not
// just a join (spec §5.4).
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

// Select resolves the caller's filter against the manifest, rejecting selectors that match nothing.
//
// An unknown name is a usage error rather than an empty selection: `gits push -r roulete-drawer`
// must not report "nothing to push, all good" when the user simply mistyped a repo name.
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

// Confirm gates a write behind the user's approval.
//
// --dry-run never needs approval: it changes nothing, so requiring --yes for it would only push
// callers toward passing --yes habitually (spec §6.7 rule 3).
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

// mapRepos runs fn over repos concurrently and returns the results in manifest order.
//
// The reordering is the point. Results are written into a pre-sized slice by index and only read
// after every worker finishes, so output never depends on which repo happened to finish first --
// which is what makes diffing two runs of the same command meaningful (spec §6.3, §6.5).
func mapRepos[T any](ctx context.Context, jobs int, repos []domain.Repo, fn func(context.Context, domain.Repo) T) []T {
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
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			results[i] = fn(ctx, r)
		}()
	}
	wg.Wait()
	return results
}
