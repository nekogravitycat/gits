package app

import (
	"context"

	"github.com/nekogravitycat/gits/internal/domain"
)

// ListEntry is one row of `gits list`, with every inherited value already resolved here (not in
// the renderer) so a JSON caller never reimplements the defaults chain.
type ListEntry struct {
	Name        string
	Path        string
	URL         string
	Branch      string
	Remote      string
	Groups      []string
	NoWrite     bool
	Description string
}

// ListResult is one list run.
type ListResult struct {
	Repos []ListEntry
}

// List reports what the manifest declares (spec §7.10). Reads the manifest only -- no directories,
// git, or network -- so it is the cheapest way for an agent to answer "what is in this workspace".
func List(_ context.Context, env *Env, g Global) (*ListResult, error) {
	m, err := env.LoadManifest()
	if err != nil {
		return nil, err
	}
	selected, _, err := Select(m, g, domain.SelectOpts{})
	if err != nil {
		return nil, err
	}

	res := &ListResult{Repos: make([]ListEntry, 0, len(selected))}
	for _, r := range selected {
		res.Repos = append(res.Repos, ListEntry{
			Name:        r.Name,
			Path:        r.EffectivePath(),
			URL:         r.URL,
			Branch:      m.EffectiveBranch(r),
			Remote:      m.EffectiveRemote(r),
			Groups:      r.Groups,
			NoWrite:     r.NoWrite,
			Description: r.Description,
		})
	}
	return res, nil
}
