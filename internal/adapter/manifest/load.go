// Package manifest reads and writes the workspace manifest.
//
// Architecture Note:
//   - CRITICAL: all reads/writes go through yaml.v3's Node API, never struct marshalling; a
//     marshal round trip erases comments, which carry design intent like "why no-write" (spec §5.1).
//   - gits is the sole writer of gits.yaml so that guarantee holds.
//   - Unknown keys are preserved on round-trip, never dropped (see reorderMapping in format.go).
//   - Entries stay sorted by domain.NameLess and are inserted in order, never appended, so
//     independent machines touch different parts of the file and git auto-merges (spec §5.2, §10.1).
package manifest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/nekogravitycat/gits/internal/app"
	"github.com/nekogravitycat/gits/internal/domain"
)

// Store implements app.ManifestStore.
type Store struct{}

// New builds a manifest store.
func New() *Store { return &Store{} }

var _ app.ManifestStore = (*Store)(nil)

// Load reads gits.yaml, folds in gits.local.yaml when present, and validates the result.
func (s *Store) Load(workspace string) (*domain.Manifest, error) {
	path := filepath.Join(workspace, app.ManifestName)

	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, (&app.Error{
				Code: domain.ErrNoWorkspace,
				Msg:  "cannot find " + app.ManifestName,
				Exit: app.ExitUsage,
			}).WithHint("gits init")
		}
		return nil, &app.Error{Code: domain.ErrManifest, Msg: "cannot read " + path, Exit: app.ExitUsage, Err: err}
	}

	m, err := parseManifest(content, path)
	if err != nil {
		return nil, err
	}
	if err := m.Validate(path); err != nil {
		return nil, asAppError(err)
	}

	localPath := filepath.Join(workspace, app.LocalManifestName)
	local, err := loadOverrides(localPath)
	if err != nil {
		return nil, err
	}
	if err := m.Apply(local, localPath); err != nil {
		return nil, asAppError(err)
	}
	return m, nil
}

// parseManifest decodes the document, keeping each entry's line number so validation can point at
// the offending entry (spec §5.6).
func parseManifest(content []byte, path string) (*domain.Manifest, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, &app.Error{
			Code: domain.ErrManifest,
			Msg:  fmt.Sprintf("%s: %v", path, err),
			Exit: app.ExitUsage,
		}
	}

	root := documentRoot(&doc)
	if root == nil {
		return nil, &app.Error{
			Code: domain.ErrManifest,
			Msg:  path + ": manifest is empty",
			Exit: app.ExitUsage,
		}
	}
	if root.Kind != yaml.MappingNode {
		return nil, &app.Error{
			Code: domain.ErrManifest,
			Msg:  fmt.Sprintf("%s:%d: manifest must be a mapping", path, root.Line),
			Exit: app.ExitUsage,
		}
	}

	m := &domain.Manifest{}
	if v := field(root, "version"); v != nil {
		if err := v.Decode(&m.Version); err != nil {
			return nil, fieldError(path, v, "version must be an integer")
		}
	}
	if d := field(root, "defaults"); d != nil {
		m.Defaults.Remote = scalar(field(d, "remote"))
		m.Defaults.Branch = scalar(field(d, "branch"))
	}

	repos := field(root, "repos")
	if repos == nil {
		return m, nil
	}
	if repos.Kind != yaml.SequenceNode {
		return nil, fieldError(path, repos, "repos must be a list")
	}

	for _, entry := range repos.Content {
		if entry.Kind != yaml.MappingNode {
			return nil, fieldError(path, entry, "each repo entry must be a mapping")
		}
		urlNode := field(entry, "url")
		r := domain.Repo{
			Line:        entry.Line,
			Name:        scalar(field(entry, "name")),
			URL:         scalar(urlNode),
			URLDeclared: urlNode != nil,
			Path:        scalar(field(entry, "path")),
			Branch:      scalar(field(entry, "branch")),
			Remote:      scalar(field(entry, "remote")),
			Description: scalar(field(entry, "description")),
		}
		if g := field(entry, "groups"); g != nil {
			if err := g.Decode(&r.Groups); err != nil {
				return nil, fieldError(path, g, "groups must be a list of strings")
			}
		}
		if nw := field(entry, "no-write"); nw != nil {
			if err := nw.Decode(&r.NoWrite); err != nil {
				return nil, fieldError(path, nw, "no-write must be true or false")
			}
		}
		m.Repos = append(m.Repos, r)
	}
	return m, nil
}

// loadOverrides reads gits.local.yaml. A missing file is the normal case, not an error.
func loadOverrides(path string) (*domain.LocalOverrides, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, &app.Error{Code: domain.ErrManifest, Msg: "cannot read " + path, Exit: app.ExitUsage, Err: err}
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, &app.Error{
			Code: domain.ErrManifest,
			Msg:  fmt.Sprintf("%s: %v", path, err),
			Exit: app.ExitUsage,
		}
	}
	root := documentRoot(&doc)
	if root == nil {
		return nil, nil
	}

	ov := &domain.LocalOverrides{}
	if v := field(root, "version"); v != nil {
		_ = v.Decode(&ov.Version)
	}
	list := field(root, "overrides")
	if list == nil || list.Kind != yaml.SequenceNode {
		return ov, nil
	}

	for _, entry := range list.Content {
		if entry.Kind != yaml.MappingNode {
			return nil, fieldError(path, entry, "each override must be a mapping")
		}
		o := domain.Override{
			Line: entry.Line,
			Name: scalar(field(entry, "name")),
			Path: scalar(field(entry, "path")),
		}
		if d := field(entry, "disabled"); d != nil {
			_ = d.Decode(&o.Disabled)
		}
		if nw := field(entry, "no-write"); nw != nil {
			_ = nw.Decode(&o.NoWrite)
		}
		ov.Overrides = append(ov.Overrides, o)
	}
	return ov, nil
}

// documentRoot unwraps the document node that yaml.v3 always produces at the top level.
func documentRoot(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			return nil
		}
		return doc.Content[0]
	}
	if doc.Kind == 0 {
		return nil
	}
	return doc
}

// field looks up a key in a mapping node (content alternates key, value).
func field(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func scalar(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode {
		return ""
	}
	return n.Value
}

func fieldError(path string, n *yaml.Node, msg string) error {
	line := 0
	if n != nil {
		line = n.Line
	}
	return &app.Error{
		Code: domain.ErrManifest,
		Msg:  fmt.Sprintf("%s:%d: %s", path, line, msg),
		Exit: app.ExitUsage,
	}
}

// asAppError wraps a domain validation failure with the CLI exit status, keeping the original
// reachable via errors.As.
func asAppError(err error) error {
	var me *domain.ManifestError
	if errors.As(err, &me) {
		return &app.Error{Code: domain.ErrManifest, Msg: me.Error(), Exit: app.ExitUsage, Err: err}
	}
	return &app.Error{Code: domain.ErrManifest, Msg: err.Error(), Exit: app.ExitUsage, Err: err}
}
