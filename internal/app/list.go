package app

import (
	"context"

	"github.com/nekogravitycat/gits/internal/domain"
)

// ListEntry is one row of `gits list`, with every inherited value already resolved.
//
// Resolution happens here rather than in the renderer so that a caller reading the JSON never has
// to reimplement the defaults chain to find out which branch a repo actually tracks.
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

// List reports what the manifest declares (spec §7.10).
//
// It reads the manifest and nothing else -- no directories, no git, no network -- which makes it
// the cheapest way for an agent to answer "what is in this workspace, where, and what may I write
// to". Cheaper than status, and far more reliable than having the agent parse the YAML itself.
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
