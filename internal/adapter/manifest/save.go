package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/nekogravitycat/gits/internal/app"
	"github.com/nekogravitycat/gits/internal/domain"
)

// yamlIndent matches the two-space style of the manifest gits itself writes.
const yamlIndent = 2

// YAML resolved tags for the node kinds this file constructs.
const (
	tagStr  = "!!str"
	tagSeq  = "!!seq"
	tagMap  = "!!map"
	tagBool = "!!bool"
)

// str builds a plain string scalar node.
func str(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: value}
}

// Create writes a fresh manifest, refusing to overwrite an existing one.
//
// The file is written as text rather than marshalled from a struct so that the explanatory
// comments are exactly what a first-time reader needs. Everything after this goes through the Node
// API, which preserves whatever the user then writes in here.
func (s *Store) Create(workspace string, m *domain.Manifest) error {
	path := filepath.Join(workspace, app.ManifestName)
	if _, err := os.Stat(path); err == nil {
		return (&app.Error{
			Code: domain.ErrManifest,
			Msg:  app.ManifestName + " already exists",
			Exit: app.ExitUsage,
		}).WithHint("gits adopt")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return &app.Error{Code: domain.ErrManifest, Msg: "cannot check " + path, Exit: app.ExitUsage, Err: err}
	}

	var b bytes.Buffer
	b.WriteString("# gits workspace manifest.\n")
	b.WriteString("# Managed by gits: use `gits add` / `gits adopt` rather than editing by hand,\n")
	b.WriteString("# but comments you add here are preserved across those writes.\n")
	fmt.Fprintf(&b, "version: %d\n\n", domain.SchemaVersion)

	b.WriteString("# Defaults for every repo below; individual entries may override them.\n")
	b.WriteString("defaults:\n")
	fmt.Fprintf(&b, "  remote: %s\n", valueOr(m.Defaults.Remote, domain.DefaultRemote))
	fmt.Fprintf(&b, "  branch: %s\n\n", valueOr(m.Defaults.Branch, domain.DefaultBranch))

	b.WriteString("# Entries are kept sorted by name. gits inserts new ones in order rather than\n")
	b.WriteString("# appending, so that two machines adding repos independently touch different\n")
	b.WriteString("# parts of this file and git can merge them without help.\n")
	b.WriteString("repos:\n")

	if len(m.Repos) == 0 {
		b.WriteString("  # No repos yet. Register one with:\n")
		b.WriteString("  #   gits add <name> --url <url>\n")
	}
	for i, r := range m.Repos {
		if i > 0 {
			// One blank line between entries, matching what encodeManifest re-imposes on every
			// later write. A fresh manifest is therefore already formatted.
			b.WriteString("\n")
		}
		writeEntryText(&b, r)
	}

	return writeFileAtomic(path, b.Bytes())
}

// writeEntryText renders one entry as text, in entryKeyOrder. Keep the two in step: `gits fmt` is
// a no-op on a manifest gits wrote, and that promise is what makes it safe in a pre-commit hook.
func writeEntryText(b *bytes.Buffer, r domain.Repo) {
	fmt.Fprintf(b, "  - name: %s\n", r.Name)
	if r.Path != "" {
		fmt.Fprintf(b, "    path: %q\n", r.Path)
	}
	if r.URL != "" {
		fmt.Fprintf(b, "    url: %s\n", r.URL)
	} else {
		// Written anyway, with the gap marked. Dropping the entry would leave the repo invisible
		// to the manifest, which is the drift this file exists to prevent (spec §7.7).
		b.WriteString("    url: \"\"   # TODO: no origin remote found; fill this in\n")
	}
	if r.Branch != "" {
		fmt.Fprintf(b, "    branch: %s\n", r.Branch)
	}
	if r.Remote != "" {
		fmt.Fprintf(b, "    remote: %s\n", r.Remote)
	}
	if len(r.Groups) > 0 {
		fmt.Fprintf(b, "    groups: [%s]\n", joinQuoted(r.Groups))
	}
	if r.NoWrite {
		b.WriteString("    no-write: true\n")
	}
	if r.Description != "" {
		fmt.Fprintf(b, "    description: %q\n", r.Description)
	}
}

// AddRepo inserts or updates one entry, keeping the rest of the file byte-for-byte intact.
//
// Insertion is in name order, never an append. Two machines that each add a repo would otherwise
// produce two insertions at the same spot in the file: a guaranteed conflict, and the most
// tedious kind of YAML conflict to resolve by hand. Sorted insertion usually puts them in
// different places, and git merges them without anyone noticing (spec §5.2, §10.1).
func (s *Store) AddRepo(workspace string, repo domain.Repo, update bool) (app.Written, error) {
	path := filepath.Join(workspace, app.ManifestName)

	content, err := os.ReadFile(path)
	if err != nil {
		return app.Written{}, &app.Error{
			Code: domain.ErrNoWorkspace,
			Msg:  "cannot read " + path,
			Exit: app.ExitUsage,
			Err:  err,
		}
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return app.Written{}, &app.Error{
			Code: domain.ErrManifest,
			Msg:  fmt.Sprintf("%s: %v", path, err),
			Exit: app.ExitUsage,
		}
	}
	root := documentRoot(&doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return app.Written{}, &app.Error{
			Code: domain.ErrManifest,
			Msg:  path + ": manifest is not a mapping",
			Exit: app.ExitUsage,
		}
	}

	repos := field(root, "repos")
	if repos == nil {
		repos = &yaml.Node{Kind: yaml.SequenceNode, Tag: tagSeq}
		root.Content = append(root.Content, str("repos"), repos)
	}
	if repos.Kind != yaml.SequenceNode {
		return app.Written{}, fieldError(path, repos, "repos must be a list")
	}

	entry := buildEntryNode(repo)

	if idx := indexOfName(repos, repo.Name); idx >= 0 {
		if !update {
			// Already present with the same contents: a no-op with exit 0, which is what lets a
			// setup script re-run without special-casing "already done" (spec §6.11).
			return app.Written{NoOp: true}, nil
		}
		// Carry the existing entry's comments onto the replacement: they explain decisions about
		// this repo, and rewriting a field is no reason to lose them.
		entry.HeadComment = repos.Content[idx].HeadComment
		entry.FootComment = repos.Content[idx].FootComment
		repos.Content[idx] = entry
		if err := encodeAndWrite(path, &doc); err != nil {
			return app.Written{}, err
		}
		return app.Written{Updated: true}, nil
	}

	pos := insertionPoint(repos, repo.Name)
	repos.Content = append(repos.Content, nil)
	copy(repos.Content[pos+1:], repos.Content[pos:])
	repos.Content[pos] = entry

	if err := encodeAndWrite(path, &doc); err != nil {
		return app.Written{}, err
	}
	return app.Written{Added: true}, nil
}

// insertionPoint returns the index where name belongs, keeping the sequence ordered by
// domain.NameLess.
func insertionPoint(repos *yaml.Node, name string) int {
	for i, entry := range repos.Content {
		if domain.NameLess(name, scalar(field(entry, "name"))) {
			return i
		}
	}
	return len(repos.Content)
}

func indexOfName(repos *yaml.Node, name string) int {
	for i, entry := range repos.Content {
		if scalar(field(entry, "name")) == name {
			return i
		}
	}
	return -1
}

// buildEntryNode renders one repo as a YAML mapping node.
//
// Only fields the user actually set are emitted. Materialising an inherited default would turn
// "follows defaults.branch" into "pinned to main", quietly changing what the entry means.
func buildEntryNode(r domain.Repo) *yaml.Node {
	entry := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
	put := func(key, value string) {
		entry.Content = append(entry.Content, str(key), str(value))
	}

	put("name", r.Name)
	if r.Path != "" {
		put("path", r.Path)
	}
	put("url", r.URL)
	if r.Branch != "" {
		put("branch", r.Branch)
	}
	if r.Remote != "" {
		put("remote", r.Remote)
	}
	if len(r.Groups) > 0 {
		groups := &yaml.Node{Kind: yaml.SequenceNode, Tag: tagSeq, Style: yaml.FlowStyle}
		for _, g := range r.Groups {
			groups.Content = append(groups.Content, str(g))
		}
		entry.Content = append(entry.Content, str("groups"), groups)
	}
	if r.NoWrite {
		entry.Content = append(entry.Content, str("no-write"),
			&yaml.Node{Kind: yaml.ScalarNode, Tag: tagBool, Value: "true"})
	}
	if r.Description != "" {
		put("description", r.Description)
	}
	return entry
}

func encodeAndWrite(path string, doc *yaml.Node) error {
	data, err := encodeManifest(path, doc, manifestLayout.listKey)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

// writeFileAtomic writes through a temporary file in the same directory, then renames.
//
// The manifest is the one file whose loss would leave the workspace unusable, and a crash or a
// full disk partway through a direct write would truncate it. Renaming within a directory is
// atomic, so a reader sees either the old file or the new one.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return &app.Error{Code: domain.ErrManifest, Msg: "cannot write " + path, Exit: app.ExitUsage, Err: err}
	}
	tmpName := tmp.Name()

	fail := func(err error) error {
		tmp.Close()
		os.Remove(tmpName)
		return &app.Error{Code: domain.ErrManifest, Msg: "cannot write " + path, Exit: app.ExitUsage, Err: err}
	}

	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	// Flush to disk before the rename: a rename that beats the data to disk can leave an empty
	// file behind after a power loss.
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		return fail(err)
	}

	// Windows will not rename onto an existing file, so the old one goes first. The temp file is
	// already safely on disk at this point.
	if err := os.Rename(tmpName, path); err != nil {
		if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			return fail(err)
		}
		if err := os.Rename(tmpName, path); err != nil {
			return fail(err)
		}
	}
	return nil
}

func valueOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func joinQuoted(items []string) string {
	var b bytes.Buffer
	for i, s := range items {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(s)
	}
	return b.String()
}
