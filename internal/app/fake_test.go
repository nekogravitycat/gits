package app_test

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/nekogravitycat/gits/internal/app"
	"github.com/nekogravitycat/gits/internal/domain"
)

// The fakes here are the point of the port layering: every use case can be driven through its
// decision tables in milliseconds, with no git binary, no temp directories and no network. The
// real adapter's job -- parsing git's output -- is covered separately by the integration tests.

// fakeRepo is one repo's state as the fake git sees it.
type fakeRepo struct {
	exists bool
	isRepo bool

	branch   string
	detached bool
	upstream string
	ahead    int
	behind   int
	dirty    domain.Dirty

	hasSubmodules   bool
	submodulesClean bool
	submodules      []domain.Submodule

	// refs maps a ref name to a commit SHA, standing in for the object store.
	refs map[string]string
	// commits is the set of commit SHAs present locally.
	commits map[string]bool
	// counts maps "left...right" to the two-way commit count.
	counts map[string][2]int

	// fetchErr, mergeErr, pushErr and commitErr inject failures.
	fetchErr  error
	mergeErr  error
	pushErr   error
	commitErr error
}

// fakeGit implements app.Git over an in-memory map of directories.
type fakeGit struct {
	mu    sync.Mutex
	repos map[string]*fakeRepo

	// calls records every mutating operation, so a test can assert that a repo was left alone.
	calls []string
}

func newFakeGit() *fakeGit {
	return &fakeGit{repos: map[string]*fakeRepo{}}
}

func (f *fakeGit) set(dir string, r *fakeRepo) {
	if r.refs == nil {
		r.refs = map[string]string{}
	}
	if r.commits == nil {
		r.commits = map[string]bool{}
	}
	if r.counts == nil {
		r.counts = map[string][2]int{}
	}
	f.repos[dir] = r
}

func (f *fakeGit) get(dir string) *fakeRepo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.repos[dir]
}

func (f *fakeGit) record(op string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, op)
}

func (f *fakeGit) didCall(op string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == op {
			return true
		}
	}
	return false
}

var errNoSuchRepo = errors.New("no such repo in the fake")

func (f *fakeGit) IsRepo(_ context.Context, dir string) (bool, error) {
	r := f.get(dir)
	if r == nil {
		return false, nil
	}
	return r.isRepo, nil
}

func (f *fakeGit) Status(_ context.Context, dir string) (app.RepoObservation, error) {
	r := f.get(dir)
	if r == nil {
		return app.RepoObservation{}, errNoSuchRepo
	}
	return app.RepoObservation{
		Branch:          r.branch,
		Detached:        r.detached,
		Upstream:        r.upstream,
		Ahead:           r.ahead,
		Behind:          r.behind,
		Dirty:           r.dirty,
		HasSubmodules:   r.hasSubmodules,
		SubmodulesClean: r.submodulesClean,
	}, nil
}

func (f *fakeGit) RemoteURL(_ context.Context, dir, _ string) (string, error) {
	if r := f.get(dir); r != nil {
		return r.refs["origin-url"], nil
	}
	return "", nil
}

func (f *fakeGit) ListSubmodules(_ context.Context, dir string) ([]domain.Submodule, error) {
	if r := f.get(dir); r != nil {
		return r.submodules, nil
	}
	return nil, nil
}

func (f *fakeGit) ResolveRef(_ context.Context, dir, ref string) (string, bool, error) {
	r := f.get(dir)
	if r == nil {
		return "", false, errNoSuchRepo
	}
	sha, ok := r.refs[ref]
	return sha, ok, nil
}

func (f *fakeGit) CommitExists(_ context.Context, dir, sha string) (bool, error) {
	r := f.get(dir)
	if r == nil {
		return false, errNoSuchRepo
	}
	return r.commits[sha], nil
}

func (f *fakeGit) CountAheadBehind(_ context.Context, dir, left, right string) (int, int, error) {
	r := f.get(dir)
	if r == nil {
		return 0, 0, errNoSuchRepo
	}
	c := r.counts[left+"..."+right]
	return c[0], c[1], nil
}

func (f *fakeGit) BranchContains(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}

func (f *fakeGit) DiffStat(_ context.Context, _ string) (string, error) { return "", nil }
func (f *fakeGit) Diff(_ context.Context, _ string) (string, error)     { return "", nil }

func (f *fakeGit) IsIgnored(_ context.Context, _, _ string) (bool, error) { return false, nil }

func (f *fakeGit) Fetch(_ context.Context, dir, _ string) error {
	f.record("fetch:" + dir)
	if r := f.get(dir); r != nil {
		return r.fetchErr
	}
	return nil
}

func (f *fakeGit) MergeFFOnly(_ context.Context, dir, _ string) error {
	f.record("merge:" + dir)
	r := f.get(dir)
	if r == nil {
		return errNoSuchRepo
	}
	if r.mergeErr != nil {
		return r.mergeErr
	}
	f.mu.Lock()
	r.behind = 0
	f.mu.Unlock()
	return nil
}

func (f *fakeGit) SubmoduleUpdate(_ context.Context, dir string) error {
	f.record("submodule-update:" + dir)
	return nil
}

func (f *fakeGit) Clone(_ context.Context, _, dir, _ string, _ bool) error {
	f.record("clone:" + dir)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repos[dir] = &fakeRepo{
		exists: true, isRepo: true, branch: "main", upstream: "origin/main",
		refs: map[string]string{}, commits: map[string]bool{}, counts: map[string][2]int{},
	}
	return nil
}

func (f *fakeGit) Commit(_ context.Context, dir, _ string, _ bool) (string, error) {
	f.record("commit:" + dir)
	r := f.get(dir)
	if r == nil {
		return "", errNoSuchRepo
	}
	if r.commitErr != nil {
		return "", r.commitErr
	}
	f.mu.Lock()
	r.dirty = domain.Dirty{}
	r.ahead++
	f.mu.Unlock()
	return "abc1234", nil
}

func (f *fakeGit) Push(_ context.Context, dir, _, _ string, _ bool) error {
	f.record("push:" + dir)
	r := f.get(dir)
	if r == nil {
		return errNoSuchRepo
	}
	if r.pushErr != nil {
		return r.pushErr
	}
	f.mu.Lock()
	r.ahead = 0
	f.mu.Unlock()
	return nil
}

func (f *fakeGit) Run(_ context.Context, dir string, args []string) (int, string, string, error) {
	f.record("run:" + dir + ":" + strings.Join(args, " "))
	return 0, "ok\n", "", nil
}

// fakeFS implements app.FS over a set of known directories.
type fakeFS struct {
	dirs  map[string]bool
	files map[string][]byte
}

func newFakeFS() *fakeFS {
	return &fakeFS{dirs: map[string]bool{}, files: map[string][]byte{}}
}

func (f *fakeFS) DirExists(path string) (bool, error) { return f.dirs[path], nil }

func (f *fakeFS) FileExists(path string) (bool, error) {
	_, ok := f.files[path]
	return ok, nil
}

func (f *fakeFS) ListDirs(string) ([]string, error) {
	var names []string
	for d := range f.dirs {
		names = append(names, d)
	}
	sort.Strings(names)
	return names, nil
}

func (f *fakeFS) ReadFile(path string) ([]byte, error) { return f.files[path], nil }

func (f *fakeFS) WriteFile(path string, data []byte) error {
	f.files[path] = data
	return nil
}

// fakeStore implements app.ManifestStore over an in-memory manifest.
type fakeStore struct {
	manifest *domain.Manifest
	// reloaded counts Load calls, so a test can prove the manifest was re-read after the root repo
	// was synced.
	reloaded int
	// onReload swaps in a different manifest, standing in for a root repo that just pulled a
	// changed repo list.
	onReload func(n int) *domain.Manifest

	added   []domain.Repo
	created *domain.Manifest
	loadErr error

	// unformatted stands in for a manifest that is not in canonical form; formatted counts the
	// Format calls, so a test can prove --dry-run still left the file alone.
	unformatted bool
	formatted   int

	// hasLocal stands in for a machine that carries a gits.local.yaml.
	hasLocal bool
}

func (s *fakeStore) Load(string) (*domain.Manifest, error) {
	s.reloaded++
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	if s.onReload != nil {
		if m := s.onReload(s.reloaded); m != nil {
			s.manifest = m
		}
	}
	// A copy, so a use case mutating its manifest cannot corrupt the store's.
	clone := *s.manifest
	clone.Repos = append([]domain.Repo(nil), s.manifest.Repos...)
	return &clone, nil
}

func (s *fakeStore) AddRepo(_ string, repo domain.Repo, _ bool) (app.Written, error) {
	s.added = append(s.added, repo)
	s.manifest.Repos = append(s.manifest.Repos, repo)
	sort.Slice(s.manifest.Repos, func(i, j int) bool {
		return s.manifest.Repos[i].Name < s.manifest.Repos[j].Name
	})
	return app.Written{Added: true}, nil
}

func (s *fakeStore) Create(_ string, m *domain.Manifest) error {
	s.created = m
	return nil
}

func (s *fakeStore) Format(_ string, apply bool) ([]app.Formatted, error) {
	s.formatted++
	out := []app.Formatted{{
		File:    app.ManifestName,
		Entries: len(s.manifest.Repos),
		Changed: s.unformatted,
	}}
	if s.hasLocal {
		out = append(out, app.Formatted{File: app.LocalManifestName, Entries: 1})
	}
	if apply {
		s.unformatted = false
	}
	return out, nil
}

// fakePrompt implements app.Prompter with scripted answers.
type fakePrompt struct {
	interactive bool
	confirm     bool
	lines       []string
	asked       []string
}

func (p *fakePrompt) IsInteractive() bool { return p.interactive }

func (p *fakePrompt) Confirm(question string) (bool, error) {
	p.asked = append(p.asked, question)
	return p.confirm, nil
}

func (p *fakePrompt) Line(prompt string) (string, error) {
	p.asked = append(p.asked, prompt)
	if len(p.lines) == 0 {
		return "", nil
	}
	line := p.lines[0]
	p.lines = p.lines[1:]
	return line, nil
}

func (p *fakePrompt) Editor(string) (string, error) { return "", nil }

// silentLog implements app.Logger and discards everything.
type silentLog struct{ warnings []string }

func (l *silentLog) Verbosef(string, ...any) {}

func (l *silentLog) Warnf(format string, args ...any) {
	l.warnings = append(l.warnings, format)
	_ = args
}

func (l *silentLog) Progress(string, int, int, string) {}

func (l *silentLog) ProgressDone() {}

// harness bundles a fully faked environment for one test.
type harness struct {
	env    *app.Env
	git    *fakeGit
	fs     *fakeFS
	store  *fakeStore
	prompt *fakePrompt
	log    *silentLog
}

const workspaceDir = "/ws"

// newHarness builds an environment where every named repo exists, is a git repo, and is clean.
func newHarness(repos ...domain.Repo) *harness {
	git := newFakeGit()
	fs := newFakeFS()
	store := &fakeStore{manifest: &domain.Manifest{
		Version:  domain.SchemaVersion,
		Defaults: domain.Defaults{Remote: "origin", Branch: "main"},
		Repos:    repos,
	}}
	prompt := &fakePrompt{interactive: false}
	log := &silentLog{}

	h := &harness{
		git: git, fs: fs, store: store, prompt: prompt, log: log,
		env: &app.Env{
			Workspace: workspaceDir,
			Git:       git, FS: fs, Store: store, Prompt: prompt, Log: log,
		},
	}
	for _, r := range repos {
		h.setRepo(r.Name, &fakeRepo{
			exists: true, isRepo: true, branch: "main", upstream: "origin/main",
			submodulesClean: true,
		})
	}
	return h
}

// dirOf resolves a repo's directory exactly as app.Env.Dir does.
//
// It must go through filepath, not string concatenation: on Windows Env.Dir yields "\ws\alpha"
// while a hand-built "/ws/alpha" would never match, and the fake would silently report every repo
// as missing.
func (h *harness) dirOf(name string) string {
	for _, r := range h.store.manifest.Repos {
		if r.Name == name {
			return h.env.Dir(r)
		}
	}
	return filepath.Join(workspaceDir, name)
}

func (h *harness) setRepo(name string, r *fakeRepo) {
	dir := h.dirOf(name)
	h.fs.dirs[dir] = r.exists
	h.git.set(dir, r)
}

// remove makes a repo absent from disk.
func (h *harness) remove(name string) {
	dir := h.dirOf(name)
	delete(h.fs.dirs, dir)
	delete(h.git.repos, dir)
}

func repo(name string, opts ...func(*domain.Repo)) domain.Repo {
	r := domain.Repo{Name: name, URL: "https://host/" + name + ".git", URLDeclared: true}
	for _, o := range opts {
		o(&r)
	}
	return r
}

func noWrite(r *domain.Repo) { r.NoWrite = true }
func atRoot(r *domain.Repo)  { r.Path = domain.RootPath }

// resultFor finds one repo's result in a write command's output.
func resultFor(results []app.RepoResult, name string) (app.RepoResult, bool) {
	for _, r := range results {
		if r.Name == name {
			return r, true
		}
	}
	return app.RepoResult{}, false
}
