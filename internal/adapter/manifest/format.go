package manifest

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/nekogravitycat/gits/internal/app"
	"github.com/nekogravitycat/gits/internal/domain"
)

// Architecture Note (formatting):
//   - One canonical key order, declared once, is applied by every writer: `gits add`
//     (buildEntryNode), `gits init` (writeEntryText), and `gits fmt`. A gits-only file is thus
//     already canonical and fmt is a no-op on it -- which is what makes fmt safe in a pre-commit hook.
//   - Entry order is identity (name, path, url), then git knobs, then human labels; description
//     last as the one long field. Overrides follow the same shape.
//   - CRITICAL: unknown keys are preserved, never dropped (reorderMapping) -- a manifest may carry
//     a field from a newer gits, and a lossy formatter can't live in a hook.
//   - Sort/insert order is domain.NameLess; for gits.yaml this enables conflict-free merges across
//     machines (spec §5.2, §10.1), not cosmetics.
var (
	rootKeyOrder     = []string{"version", "defaults", "repos"}
	defaultsKeyOrder = []string{"remote", "branch"}
	entryKeyOrder    = []string{"name", "path", "url", "branch", "remote", "groups", "no-write", "description"}

	localRootKeyOrder = []string{"version", "overrides"}
	overrideKeyOrder  = []string{"name", "path", "disabled", "no-write"}
)

// layout is the canonical shape of one manifest file: top-level key order, which key holds the
// entry list, the entry field order, and which entry fields render inline. Both files share the
// treatment so a reader learns one convention.
type layout struct {
	rootKeys []string

	// nested orders the keys of a top-level mapping, e.g. defaults.
	nested map[string][]string

	// listKey names the top-level sequence whose items are entries; entryKeys orders their fields.
	listKey   string
	entryKeys []string

	// inline names entry fields rendered as a one-line list, e.g. groups.
	inline []string
}

var (
	manifestLayout = layout{
		rootKeys:  rootKeyOrder,
		nested:    map[string][]string{"defaults": defaultsKeyOrder},
		listKey:   "repos",
		entryKeys: entryKeyOrder,
		inline:    []string{"groups"},
	}

	localLayout = layout{
		rootKeys:  localRootKeyOrder,
		listKey:   "overrides",
		entryKeys: overrideKeyOrder,
	}
)

// Format rewrites the workspace's manifest files in canonical form (spec §7.13).
//
// Always gits.yaml; gits.local.yaml too when present (same shape, missing is normal). Parsed and
// re-emitted via the node API so comments survive and travel with their entry when the sort moves
// it. With apply=false nothing is written but results still report whether each file was canonical
// (--dry-run).
func (s *Store) Format(workspace string, apply bool) ([]app.Formatted, error) {
	content, err := os.ReadFile(filepath.Join(workspace, app.ManifestName))
	if err != nil {
		return nil, &app.Error{
			Code: domain.ErrNoWorkspace,
			Msg:  "cannot read " + filepath.Join(workspace, app.ManifestName),
			Exit: app.ExitUsage,
			Err:  err,
		}
	}

	res, err := formatFile(workspace, app.ManifestName, content, manifestLayout, apply)
	if err != nil {
		return nil, err
	}
	out := []app.Formatted{res}

	local, err := os.ReadFile(filepath.Join(workspace, app.LocalManifestName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return out, nil
		}
		return nil, &app.Error{
			Code: domain.ErrManifest,
			Msg:  "cannot read " + filepath.Join(workspace, app.LocalManifestName),
			Exit: app.ExitUsage,
			Err:  err,
		}
	}
	res, err = formatFile(workspace, app.LocalManifestName, local, localLayout, apply)
	if err != nil {
		return nil, err
	}
	return append(out, res), nil
}

// formatFile canonicalises one file's contents and writes it back when it differs.
func formatFile(workspace, name string, content []byte, lay layout, apply bool) (app.Formatted, error) {
	path := filepath.Join(workspace, name)
	res := app.Formatted{File: name}

	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return app.Formatted{}, &app.Error{
			Code: domain.ErrManifest,
			Msg:  path + ": " + err.Error(),
			Exit: app.ExitUsage,
		}
	}
	root := documentRoot(&doc)
	if root == nil || root.Kind != yaml.MappingNode {
		// Nothing to act on. An empty gits.local.yaml is legal ("no overrides"); anything else
		// failed validation before fmt, so leave it untouched rather than reshape on a guess.
		return res, nil
	}

	reorderMapping(root, lay.rootKeys)
	for key, order := range lay.nested {
		reorderMapping(field(root, key), order)
	}

	if list := field(root, lay.listKey); list != nil && list.Kind == yaml.SequenceNode {
		res.Entries = len(list.Content)
		res.Reordered = sortEntries(list)
		for _, entry := range list.Content {
			reorderMapping(entry, lay.entryKeys)
			for _, key := range lay.inline {
				inlineList(entry, key)
			}
		}
	}

	formatted, err := encodeManifest(path, &doc, lay.listKey)
	if err != nil {
		return app.Formatted{}, err
	}
	if bytes.Equal(formatted, content) {
		// Already canonical: touch nothing. Rewriting an identical file bumps mtime and can
		// trigger a watcher or rebuild (spec §6.11).
		res.Reordered = nil
		return res, nil
	}

	res.Changed = true
	if !apply {
		return res, nil
	}
	if err := writeFileAtomic(path, formatted); err != nil {
		return app.Formatted{}, err
	}
	return res, nil
}

// sortEntries puts an entry list in name order (domain.NameLess, same as AddRepo) and reports
// which entries moved, in their new order. See Architecture Note for why order matters.
func sortEntries(repos *yaml.Node) []string {
	was := make([]*yaml.Node, len(repos.Content))
	copy(was, repos.Content)

	// NOTE: stable sort so duplicate-named entries (rejected by validation but fmt may still tidy)
	// keep relative order instead of reshuffling each run.
	sort.SliceStable(repos.Content, func(i, j int) bool {
		return domain.NameLess(nameOf(repos.Content[i]), nameOf(repos.Content[j]))
	})

	var moved []string
	for i, entry := range repos.Content {
		if entry != was[i] {
			moved = append(moved, nameOf(entry))
		}
	}
	return moved
}

func nameOf(entry *yaml.Node) string { return scalar(field(entry, "name")) }

// reorderMapping rearranges a mapping's keys into the given order.
//
// CRITICAL: an unknown key is kept (placed after known ones, preserving its relative order);
// dropping it would make fmt lossy on manifests this build doesn't fully understand.
func reorderMapping(n *yaml.Node, order []string) {
	if n == nil || n.Kind != yaml.MappingNode {
		return
	}

	rank := make(map[string]int, len(order))
	for i, key := range order {
		rank[key] = i
	}
	rankOf := func(key string) int {
		if r, known := rank[key]; known {
			return r
		}
		return len(order)
	}

	type pair struct{ key, value *yaml.Node }
	pairs := make([]pair, 0, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		pairs = append(pairs, pair{n.Content[i], n.Content[i+1]})
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		return rankOf(pairs[i].key.Value) < rankOf(pairs[j].key.Value)
	})

	content := n.Content[:0]
	for _, p := range pairs {
		content = append(content, p.key, p.value)
	}
	n.Content = content
}

// inlineList renders a short list on one line as `[game, platform]`, the form gits writes.
//
// NOTE: a list carrying a comment stays block-style; flow style has nowhere to hold a comment, so
// the encoder would move or lose it.
func inlineList(entry *yaml.Node, key string) {
	list := field(entry, key)
	if list == nil || list.Kind != yaml.SequenceNode {
		return
	}
	for _, item := range list.Content {
		if item.Kind != yaml.ScalarNode || hasComment(item) {
			return
		}
	}
	list.Style = yaml.FlowStyle
}

func hasComment(n *yaml.Node) bool {
	return n.HeadComment != "" || n.LineComment != "" || n.FootComment != ""
}

// encodeManifest renders the document with the manifest's layout applied. All gits writes (fmt,
// add, adopt) go through here so the file looks the same whichever command last touched it.
func encodeManifest(path string, doc *yaml.Node, listKey string) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(yamlIndent)
	if err := enc.Encode(doc); err != nil {
		return nil, &app.Error{Code: domain.ErrManifest, Msg: "cannot encode " + path, Exit: app.ExitUsage, Err: err}
	}
	if err := enc.Close(); err != nil {
		return nil, &app.Error{Code: domain.ErrManifest, Msg: "cannot encode " + path, Exit: app.ExitUsage, Err: err}
	}
	return spaceSections(buf.Bytes(), listKey), nil
}

// spaceSections re-inserts blank lines between top-level sections and between entries of listKey.
//
// CRITICAL: yaml.v3 has no representation for blank lines; a round trip collapses the file into a
// dense block. Comments survive, but §5.1 asks for formatting to survive too. This re-imposes the
// layout gits writes: blank lines above leading comment blocks (a comment belongs to the thing
// below it).
func spaceSections(data []byte, listKey string) []byte {
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines)+len(lines)/4)

	inList := false
	seenEntry := false

	for i, line := range lines {
		switch {
		case startsSection(line):
			// A top-level comment block introducing the list isn't itself "inside" it, else the
			// first entry gets a stray blank line above it.
			inList = topLevelKey(line) == listKey
			seenEntry = false
			if i > 0 && needsBlankBefore(lines[i-1]) {
				out = append(out, "")
			}
		case inList && startsEntry(line):
			if seenEntry && needsBlankBefore(lines[i-1]) {
				out = append(out, "")
			}
			seenEntry = true
		}
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n"))
}

// startsSection reports whether a line begins a new top-level section: an unindented key, or an
// unindented comment introducing one.
func startsSection(line string) bool {
	if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
		return false
	}
	return true
}

// topLevelKey names the key a section line declares, or "" for a comment line.
func topLevelKey(line string) string {
	key, _, found := strings.Cut(line, ":")
	if !found {
		return ""
	}
	return key
}

// startsEntry reports whether a line begins a repo entry: the `- name:` line, or a comment block
// at the same indent (where yaml.v3 puts an entry's head comment).
func startsEntry(line string) bool {
	rest, ok := strings.CutPrefix(line, "  ")
	if !ok || strings.HasPrefix(rest, " ") {
		return false
	}
	return strings.HasPrefix(rest, "- ") || strings.HasPrefix(rest, "#")
}

// needsBlankBefore reports whether prev should be followed by a blank line. A comment directly
// above a key documents it, so they stay together and the blank goes above the comment.
func needsBlankBefore(prev string) bool {
	trimmed := strings.TrimSpace(prev)
	return trimmed != "" && !strings.HasPrefix(trimmed, "#")
}
