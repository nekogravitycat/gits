package app

import (
	"bytes"
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nekogravitycat/gits/internal/domain"
)

// InitResult is one init run.
type InitResult struct {
	ManifestPath string
	Repos        []ListEntry

	// MissingURL names entries written without a URL because the repo had no origin. They need a
	// human to fill them in before clone can work.
	MissingURL []string

	// ManifestIgnored reports that .gitignore would exclude gits.yaml from version control.
	ManifestIgnored bool
	// GitignoreUpdated reports that gits added the allowlist and ignore lines itself.
	GitignoreUpdated bool
}

// Init creates a manifest for the current directory (spec §7.7).
//
// Groups and no-write are deliberately left blank on every entry. Ownership is not something a
// scan can infer, and a wrong guess about "you may not write here" is worse than no guess: the
// user would either trust it and be wrong, or learn to ignore the field entirely.
func Init(ctx context.Context, env *Env, g Global) (*InitResult, error) {
	manifestPath := env.ManifestPath()
	if exists, err := env.FS.FileExists(manifestPath); err != nil {
		return nil, err
	} else if exists {
		// Never overwrite: the manifest is hand-annotated, and re-running init must not be a way
		// to lose those annotations (spec §6.11).
		return nil, Usagef(domain.ErrManifest, "%s already exists", ManifestName).
			WithHint("gits adopt   # register repos that are not listed yet")
	}

	found, err := ScanWorkspace(ctx, env)
	if err != nil {
		return nil, err
	}

	m := &domain.Manifest{
		Version:  domain.SchemaVersion,
		Defaults: domain.Defaults{Remote: domain.DefaultRemote, Branch: domain.DefaultBranch},
	}
	res := &InitResult{ManifestPath: manifestPath}

	for _, d := range found {
		entry := domain.Repo{Name: d.Name, URL: d.URL}
		if d.IsRoot {
			entry.Path = domain.RootPath
		}
		if d.Branch != "" && d.Branch != domain.DefaultBranch {
			entry.Branch = d.Branch
		}
		if d.URL == "" {
			res.MissingURL = append(res.MissingURL, d.Name)
		}
		m.Repos = append(m.Repos, entry)
	}

	// domain.NameLess, so that two machines adding entries independently mostly touch different
	// parts of the file and git can merge them without help (spec §5.2, §10.1).
	sort.Slice(m.Repos, func(i, j int) bool { return domain.NameLess(m.Repos[i].Name, m.Repos[j].Name) })

	if g.DryRun {
		res.Repos = entriesOf(m)
		return res, nil
	}

	if err := env.Store.Create(env.Workspace, m); err != nil {
		return nil, err
	}
	res.Repos = entriesOf(m)

	if err := ensureManifestTracked(ctx, env, res); err != nil {
		// A .gitignore problem must not undo a manifest that was written successfully; warn and
		// let the caller act on the flags in the result.
		env.Log.Warnf("could not check .gitignore: %v", err)
	}
	return res, nil
}

// ensureManifestTracked makes sure gits.yaml can actually be committed, and that the local
// override file cannot be.
//
// This check earns its place. A workspace whose .gitignore is "ignore everything, then allow a
// list" swallows gits.yaml silently: `git add` reports no error, the file never enters version
// control, and the user only finds out on the second machine when nothing has synced across
// (spec §7.7, §10.1 trap 1).
func ensureManifestTracked(ctx context.Context, env *Env, res *InitResult) error {
	ignored, err := env.Git.IsIgnored(ctx, env.Workspace, ManifestName)
	if err != nil {
		return err
	}
	res.ManifestIgnored = ignored

	gitignore := filepath.Join(env.Workspace, ".gitignore")
	existing, _ := env.FS.ReadFile(gitignore)

	var add []string
	if ignored && !hasLine(existing, "!"+ManifestName) {
		add = append(add, "!"+ManifestName)
	}
	// The local override file describes this machine only and must never be shared (spec §5.5).
	if !hasLine(existing, LocalManifestName) {
		add = append(add, LocalManifestName)
	}
	if len(add) == 0 {
		return nil
	}

	var buf bytes.Buffer
	buf.Write(existing)
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		buf.WriteString("\n")
	}
	buf.WriteString("\n# gits: keep the shared manifest tracked, keep local overrides out\n")
	for _, line := range add {
		buf.WriteString(line + "\n")
	}
	if err := env.FS.WriteFile(gitignore, buf.Bytes()); err != nil {
		return err
	}
	res.GitignoreUpdated = true
	return nil
}

func hasLine(content []byte, want string) bool {
	for _, line := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

func entriesOf(m *domain.Manifest) []ListEntry {
	out := make([]ListEntry, 0, len(m.Repos))
	for _, r := range m.Repos {
		out = append(out, ListEntry{
			Name:    r.Name,
			Path:    r.EffectivePath(),
			URL:     r.URL,
			Branch:  m.EffectiveBranch(r),
			Remote:  m.EffectiveRemote(r),
			Groups:  r.Groups,
			NoWrite: r.NoWrite,
		})
	}
	return out
}
