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

// The canonical key order of a manifest, top level and per entry.
//
// One order, declared once, is the point: `gits add` builds entries in it (buildEntryNode),
// `gits init` writes them in it (writeEntryText), and `gits fmt` restores it. A file that has only
// ever been touched by gits is therefore already formatted, and running fmt on it is a no-op --
// which is what makes fmt safe to put in a pre-commit hook.
//
// Identity first (name, path, url), then the git knobs, then the human labels. description sits
// last because it is the one field that runs long. The override order follows the same idea:
// which repo, where it lives, then the two switches that take it out of gits' reach.
var (
	rootKeyOrder     = []string{"version", "defaults", "repos"}
	defaultsKeyOrder = []string{"remote", "branch"}
	entryKeyOrder    = []string{"name", "path", "url", "branch", "remote", "groups", "no-write", "description"}

	localRootKeyOrder = []string{"version", "overrides"}
	overrideKeyOrder  = []string{"name", "path", "disabled", "no-write"}
)

// layout describes the canonical shape of one of the two manifest files: the order of its
// top-level keys, which of them holds the entry list, how an entry's own fields are ordered, and
// which keys are rendered inline.
//
// Both files get the same treatment because both are read by a person looking for one entry among
// many, and a reader should not have to learn two conventions for two files sitting side by side.
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
// gits.yaml always; gits.local.yaml too when this machine has one, since it has the same shape and
// is read the same way. A missing local file is the normal case, not an error.
//
// Each file is parsed and re-emitted through the node API, so comments survive; an entry's
// comments travel with the entry when the sort moves it, since that is where they were written to
// apply. Unrecognised keys are kept, not dropped -- a manifest may carry a field written by a
// newer gits, and a formatter that silently deletes what it does not understand is one nobody can
// leave in a hook.
//
// With apply false nothing is written and the results still report whether each file was already
// canonical, which is what --dry-run needs.
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
		// Nothing a formatter can act on. An empty gits.local.yaml is legal and means "no
		// overrides"; anything else here failed validation before fmt was reached, so the file is
		// left exactly as it is rather than being reshaped on a guess.
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
		// Already canonical: report it and touch nothing. Rewriting an identical file would still
		// bump its mtime, which is enough to make a watcher or a build rerun (spec §6.11).
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

// sortEntries puts an entry list in name order and reports which entries moved, in their new
// order.
//
// Byte order by name, the same order AddRepo inserts into. For gits.yaml it is not cosmetic: two
// machines that each add a repo land in different parts of the file and git merges them without
// help, whereas entries appended at the end collide every time (spec §5.2, §10.1). gits.local.yaml
// never leaves the machine, so there the reason is the plainer one -- an entry is findable in a
// list of twenty only if the list is ordered.
func sortEntries(repos *yaml.Node) []string {
	was := make([]*yaml.Node, len(repos.Content))
	copy(was, repos.Content)

	// Stable, so that entries sharing a name -- which validation rejects, but fmt may still be
	// asked to tidy -- keep their relative order instead of shuffling on every run.
	sort.SliceStable(repos.Content, func(i, j int) bool {
		return nameOf(repos.Content[i]) < nameOf(repos.Content[j])
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
// A key the order does not mention keeps its place relative to the other unknown keys and lands
// after every known one. Dropping it instead would make fmt a lossy operation on any manifest this
// build does not fully understand.
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

// inlineList renders a short list on one line, as `[game, platform]` -- the form gits itself
// writes.
//
// A list carrying a comment stays in block form: flow style has nowhere to put one, and the
// encoder would either move it somewhere surprising or lose it.
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

// encodeManifest renders the document with the manifest's own layout applied.
//
// Everything gits writes goes through here -- fmt, add, adopt -- so one description of the layout
// serves all of them and the file looks the same whichever command last touched it.
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

// spaceSections puts the blank lines back: one before each top-level section, one between the
// entries of the listKey section.
//
// yaml.v3 keeps comments on its nodes but has nowhere to record blank lines, so a round trip
// collapses the file into one dense block. The comments survive -- which is the guarantee that
// matters -- but a manifest listing eighteen repos becomes a wall of text, and §5.1 asks for the
// formatting to survive too.
//
// Rather than trying to remember where the blank lines were, this re-imposes the layout gits
// writes in the first place. Blank lines are inserted above a leading comment block rather than
// between it and what it documents, since a comment belongs to the thing below it.
func spaceSections(data []byte, listKey string) []byte {
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines)+len(lines)/4)

	inList := false
	seenEntry := false

	for i, line := range lines {
		switch {
		case startsSection(line):
			// A top-level comment block introducing the list must not itself count as being
			// inside it, or the first entry would get a blank line above it.
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

// startsEntry reports whether a line begins a repo entry: the `- name:` line itself, or a comment
// block sitting at the same indentation, which is where yaml.v3 puts an entry's head comment.
func startsEntry(line string) bool {
	rest, ok := strings.CutPrefix(line, "  ")
	if !ok || strings.HasPrefix(rest, " ") {
		return false
	}
	return strings.HasPrefix(rest, "- ") || strings.HasPrefix(rest, "#")
}

// needsBlankBefore reports whether the previous line should be followed by a blank one.
//
// A comment immediately above a key documents that key, so the two stay together; the blank line
// goes above the comment instead.
func needsBlankBefore(prev string) bool {
	trimmed := strings.TrimSpace(prev)
	return trimmed != "" && !strings.HasPrefix(trimmed, "#")
}
