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

// yamlIndent matches the two-space style gits itself writes.
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
// Written as text (not marshalled) so the seeded explanatory comments are exactly what a
// first-time reader needs; every later write goes through the Node API, which preserves them.
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
			// Blank line between entries, matching encodeManifest's re-imposed layout, so a fresh
			// manifest is already canonical.
			b.WriteString("\n")
		}
		writeEntryText(&b, r)
	}

	return writeFileAtomic(path, b.Bytes())
}

// writeEntryText renders one entry as text in entryKeyOrder. CRITICAL: keep in step with
// buildEntryNode/encodeManifest so `gits fmt` stays a no-op on a gits-written manifest, which is
// what makes it safe in a pre-commit hook.
func writeEntryText(b *bytes.Buffer, r domain.Repo) {
	fmt.Fprintf(b, "  - name: %s\n", r.Name)
	if r.Path != "" {
		fmt.Fprintf(b, "    path: %q\n", r.Path)
	}
	if r.URL != "" {
		fmt.Fprintf(b, "    url: %s\n", r.URL)
	} else {
		// CRITICAL: emit url anyway with the gap marked; dropping the entry hides the repo from
		// the manifest, the drift this file exists to prevent (spec §7.7).
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
// CRITICAL: insertion is in name order, never an append (spec §5.2, §10.1); appending makes two
// machines' adds collide at the same spot every time.
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
			// Identical contents: no-op with exit 0 so a setup script can re-run safely (spec §6.11).
			return app.Written{NoOp: true}, nil
		}
		// Carry existing comments onto the replacement; they explain per-repo decisions and a
		// field rewrite is no reason to lose them.
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
// Only fields the user set are emitted; materialising an inherited default would silently turn
// "follows defaults.branch" into a pin.
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

// writeFileAtomic writes to a temp file in the same directory, then renames.
//
// CRITICAL: the manifest's loss leaves the workspace unusable; a direct write truncated by a crash
// or full disk would destroy it. Rename within a directory is atomic, so a reader sees either the
// old or the new file whole.
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
	// CRITICAL: fsync before rename; a rename that beats data to disk can leave an empty file
	// after power loss.
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		return fail(err)
	}

	// NOTE: Windows won't rename onto an existing file, so remove the old one first; the temp is
	// already safely on disk.
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
