package app

import (
	"context"
	"fmt"
	"sort"

	"github.com/nekogravitycat/gits/internal/domain"
)

// DepsOptions are the flags specific to `gits deps` (spec §7.11).
type DepsOptions struct {
	// Fetch refreshes the canonical repos' remote refs before comparing, so that a baseline that
	// has moved on another machine is seen.
	Fetch bool
}

// Deps reports cross-repo submodule dependencies (spec §7.11).
//
// Everything is derived from existing git metadata (.gitmodules plus the gitlink SHA in HEAD's
// tree): no new format to declare, nothing for a human to keep in sync.
func Deps(ctx context.Context, env *Env, g Global, opts DepsOptions) ([]domain.DepGroup, error) {
	m, err := env.LoadManifest()
	if err != nil {
		return nil, err
	}
	return DepsOf(ctx, env, g, opts, m)
}

// dependentSubmodules pairs a repo with the submodules it declares.
type dependentSubmodules struct {
	repo domain.Repo
	subs []domain.Submodule
}

// DepsOf runs the dependency scan against an already-loaded manifest.
func DepsOf(ctx context.Context, env *Env, g Global, opts DepsOptions, m *domain.Manifest) ([]domain.DepGroup, error) {
	selected, _, err := Select(m, g, domain.SelectOpts{})
	if err != nil {
		return nil, err
	}

	found := mapRepos(ctx, g.Concurrency(), selected, func(ctx context.Context, r domain.Repo) dependentSubmodules {
		dir := env.Dir(r)
		if exists, derr := env.FS.DirExists(dir); derr != nil || !exists {
			return dependentSubmodules{repo: r}
		}
		subs, serr := env.Git.ListSubmodules(ctx, dir)
		if serr != nil {
			// CRITICAL: one unreadable repo must not sink the whole dependency report.
			env.Log.Verbosef("deps: %s: %v", r.Name, serr)
			return dependentSubmodules{repo: r}
		}
		return dependentSubmodules{repo: r, subs: subs}
	})

	groups := groupByDependency(found)
	resolveCanonicals(groups, m)

	if opts.Fetch {
		fetchCanonicals(ctx, env, g, m, groups)
	}
	for _, grp := range groups {
		compareGroup(ctx, env, m, grp)
	}

	out := make([]domain.DepGroup, 0, len(groups))
	for _, grp := range groups {
		grp.finish()
		out = append(out, *grp.group)
	}
	domain.SortGroups(out)
	return out, nil
}

// depGroup is the mutable working form of a dependency group during the scan.
type depGroup struct {
	group *domain.DepGroup

	// canonical is the workspace repo that *is* this dependency, when one exists.
	canonical    domain.Repo
	hasCanonical bool

	// declaredBranch records the branch each dependent declared for the submodule, keyed by
	// dependent name. Consulted per pin, not per group, since dependents legitimately track
	// different lines (spec §7.11).
	declaredBranch map[string]string
}

// groupByDependency buckets every submodule by the identity of the repo it points at.
//
// NOTE: grouping is by normalised URL, never by submodule path -- different dependents give the
// same submodule different paths, so paths identify nothing (spec §7.11).
func groupByDependency(found []dependentSubmodules) map[string]*depGroup {
	groups := map[string]*depGroup{}
	for _, f := range found {
		for _, s := range f.subs {
			key := domain.NormalizeURL(s.URL)
			if key == "" {
				continue
			}
			grp, ok := groups[key]
			if !ok {
				grp = &depGroup{
					group:          &domain.DepGroup{Name: key, URL: s.URL},
					declaredBranch: map[string]string{},
				}
				groups[key] = grp
			}
			grp.group.Pins = append(grp.group.Pins, domain.Pin{
				Dependent:     f.repo.Name,
				SubmodulePath: s.Path,
				SHA:           s.SHA,
			})
			grp.declaredBranch[f.repo.Name] = s.Branch
		}
	}
	return groups
}

// resolveCanonicals matches each dependency group against the workspace's own repos.
func resolveCanonicals(groups map[string]*depGroup, m *domain.Manifest) {
	for key, grp := range groups {
		for _, r := range m.Repos {
			if r.Disabled {
				continue
			}
			if domain.NormalizeURL(r.URL) == key {
				grp.canonical = r
				grp.hasCanonical = true
				grp.group.Name = r.Name
				grp.group.CanonicalPath = r.EffectivePath()
				break
			}
		}
	}
}

func fetchCanonicals(ctx context.Context, env *Env, g Global, m *domain.Manifest, groups map[string]*depGroup) {
	var repos []domain.Repo
	var owners []*depGroup
	for _, grp := range groups {
		if grp.hasCanonical {
			repos = append(repos, grp.canonical)
			owners = append(owners, grp)
		}
	}
	// Map order is random; sort so -v output and failure ordering stay deterministic.
	sort.Slice(owners, func(i, j int) bool { return owners[i].canonical.Name < owners[j].canonical.Name })
	sort.Slice(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })

	mapRepos(ctx, g.Concurrency(), repos, func(ctx context.Context, r domain.Repo) struct{} {
		if err := env.Git.Fetch(ctx, env.Dir(r), m.EffectiveRemote(r)); err != nil {
			env.Log.Warnf("deps --fetch: %s: %v", r.Name, MessageOf(err))
		}
		return struct{}{}
	})
}

// compareGroup fills in a verdict for every pin in one group.
func compareGroup(ctx context.Context, env *Env, m *domain.Manifest, grp *depGroup) {
	if !grp.hasCanonical {
		return
	}
	dir := env.Dir(grp.canonical)
	remote := m.EffectiveRemote(grp.canonical)

	// The group-level baseline is what the canonical repo's manifest entry declares; a pin may
	// override it below.
	grp.group.BaselineRef = remote + "/" + m.EffectiveBranch(grp.canonical)
	if sha, ok, err := env.Git.ResolveRef(ctx, dir, grp.group.BaselineRef); err == nil && ok {
		grp.group.BaselineSHA = sha
	}

	for i := range grp.group.Pins {
		pin := &grp.group.Pins[i]
		branch, source := baselineFor(m, grp, pin.Dependent)
		pin.BaselineRef = remote + "/" + branch
		pin.BaselineSource = source
		comparePin(ctx, env, dir, pin)
	}
}

// baselineFor picks the branch a pin is judged against, in the fixed §7.11 priority: the
// dependent's declared branch, then the canonical repo's manifest branch, then defaults.
//
// The declared-branch rule keeps the report worth reading: a dependent on its own declared branch
// must not carry a permanent warning, or users learn to ignore all warnings.
func baselineFor(m *domain.Manifest, grp *depGroup, dependent string) (string, domain.BaselineSource) {
	if b := grp.declaredBranch[dependent]; b != "" {
		return b, domain.BaselineDeclared
	}
	if grp.canonical.Branch != "" {
		return grp.canonical.Branch, domain.BaselineManifest
	}
	if m.Defaults.Branch != "" {
		return m.Defaults.Branch, domain.BaselineDefaults
	}
	return domain.DefaultBranch, domain.BaselineDefaults
}

// shortSHA abbreviates a commit for display; 7 chars is git's default and stays unambiguous in
// repos of ordinary size.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// comparePin judges one pinned SHA against its baseline, entirely offline.
func comparePin(ctx context.Context, env *Env, dir string, pin *domain.Pin) {
	if pin.SHA == "" {
		pin.Verdict = domain.PinUnknown
		pin.Message = "gitlink commit could not be read"
		return
	}

	baseSHA, ok, err := env.Git.ResolveRef(ctx, dir, pin.BaselineRef)
	if err != nil || !ok {
		pin.Verdict = domain.PinUnknown
		pin.Message = "baseline " + pin.BaselineRef + " does not exist locally"
		pin.Hint = "gits deps --fetch"
		return
	}

	// A pin absent from the canonical object store is "fetch and ask again", not "you are behind".
	if exists, cerr := env.Git.CommitExists(ctx, dir, pin.SHA); cerr != nil || !exists {
		pin.Verdict = domain.PinUnknown
		pin.Message = "commit not found in canonical"
		pin.Hint = "gits deps --fetch"
		return
	}

	if pin.SHA == baseSHA {
		pin.Verdict = domain.PinUpToDate
		return
	}

	ahead, behind, err := env.Git.CountAheadBehind(ctx, dir, pin.SHA, pin.BaselineRef)
	if err != nil {
		pin.Verdict = domain.PinUnknown
		pin.Message = MessageOf(err)
		return
	}
	pin.Ahead, pin.Behind = ahead, behind
	pin.Verdict = domain.DeriveVerdict(ahead, behind)

	switch pin.Verdict {
	case domain.PinDiverged:
		pin.Code = domain.ErrDiverged
		pin.Message = fmt.Sprintf("diverged: ahead %d, behind %d", ahead, behind)
		if branches, berr := env.Git.BranchContains(ctx, dir, pin.SHA); berr == nil {
			pin.ContainingBranches = branches
		}
		// Short SHA keeps the hint pasteable on one line; git resolves it just as well.
		pin.Hint = "cd " + pin.SubmodulePath + " && git log --oneline " +
			pin.BaselineRef + ".." + shortSHA(pin.SHA)
	case domain.PinBehind:
		pin.Message = fmt.Sprintf("behind %d", behind)
	case domain.PinAhead:
		pin.Message = fmt.Sprintf("ahead %d", ahead)
	case domain.PinUpToDate, domain.PinUnknown:
		// Nothing to add.
	}
}

// finish sorts a group's pins and records the incompleteness of a canonical-less determination.
func (grp *depGroup) finish() {
	sort.Slice(grp.group.Pins, func(i, j int) bool {
		return grp.group.Pins[i].Dependent < grp.group.Pins[j].Dependent
	})
	if grp.hasCanonical {
		return
	}

	// Without a canonical checkout there is no authoritative timeline; the only honest statement is
	// how much the dependents disagree. Saying so explicitly prevents a caller acting on a partial
	// verdict as if whole (§7.11).
	g := grp.group

	// Keyed by normalised URL (right identity, poor label); with no manifest entry for a real name,
	// use the URL's last segment and carry the full URL alongside for uniqueness.
	g.Name = domain.DisplayName(g.URL)

	g.Code = domain.ErrNoCanonical
	g.Message = fmt.Sprintf("%s pinned to %s; no canonical checkout in this workspace",
		countOf(len(g.Pins), "repo", "repos"),
		countOf(g.DistinctSHAs(), "commit", "different commits"))
	g.Hint = "gits add " + g.Name + " --url " + g.URL + "   # then: gits clone -r " + g.Name

	for i := range g.Pins {
		if g.Pins[i].Verdict == "" {
			g.Pins[i].Verdict = domain.PinUnknown
		}
		if g.Pins[i].Message == "" {
			g.Pins[i].Message = "no canonical checkout to compare against"
		}
	}
}

// countOf renders a count with the right noun form ("1 commit" vs "N commits") so the message does
// not read as a tool defect.
func countOf(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
