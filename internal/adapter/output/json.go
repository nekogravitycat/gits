// Package output renders command results for the two audiences the spec treats as equals: a human
// reading a terminal, and a program parsing stdout.
//
// Both renderers consume the same result values from internal/app, which is what makes the
// equal-audiences rule enforceable rather than aspirational: there is no fact one can show that
// the other cannot (spec §3.7).
package output

import (
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"

	"github.com/nekogravitycat/gits/internal/app"
	"github.com/nekogravitycat/gits/internal/domain"
)

// SchemaVersion identifies the JSON contract. It increments on any incompatible change, so a
// caller can refuse output it was not written against (spec §6.5 rule 4).
const SchemaVersion = 1

// JSON renders results as a single object on stdout.
//
// Exactly one object, and nothing else: every progress line, warning and prompt goes to stderr, or
// piping into jq breaks on the first stray byte (spec §6.4).
type JSON struct {
	out       io.Writer
	workspace string
}

// NewJSON builds a JSON renderer.
func NewJSON(out io.Writer, workspace string) *JSON {
	return &JSON{out: out, workspace: workspace}
}

// header is the envelope every command shares. Declaration order is emission order, and the order
// is part of the contract: two runs are routinely diffed against each other (spec §6.5 rule 2).
//
// There is deliberately no timestamp and no elapsed time anywhere in this file. Either would make
// every diff of two runs show a spurious change.
type header struct {
	SchemaVersion int    `json:"schemaVersion"`
	Command       string `json:"command"`
	Workspace     string `json:"workspace"`
	ManifestPath  string `json:"manifestPath"`
	DryRun        bool   `json:"dryRun,omitempty"`
}

func (j *JSON) header(command string, dryRun bool) header {
	ws := toSlash(j.workspace)
	return header{
		SchemaVersion: SchemaVersion,
		Command:       command,
		Workspace:     ws,
		ManifestPath:  ws + "/" + app.ManifestName,
		DryRun:        dryRun,
	}
}

// repoDoc is one repo in a status payload.
//
// The pointer fields are the ones that only mean something for a repo that was actually inspected.
// A missing directory has no ahead count, and emitting 0 would state a fact gits does not have.
type repoDoc struct {
	Name            string         `json:"name"`
	Path            string         `json:"path"`
	Groups          []string       `json:"groups,omitempty"`
	State           string         `json:"state"`
	Exists          bool           `json:"exists"`
	Branch          string         `json:"branch,omitempty"`
	DefaultBranch   string         `json:"defaultBranch,omitempty"`
	OnDefaultBranch *bool          `json:"onDefaultBranch,omitempty"`
	Upstream        string         `json:"upstream,omitempty"`
	Ahead           *int           `json:"ahead,omitempty"`
	Behind          *int           `json:"behind,omitempty"`
	Dirty           *dirtyDoc      `json:"dirty,omitempty"`
	SubmodulesClean *bool          `json:"submodulesClean,omitempty"`
	NoWrite         bool           `json:"noWrite,omitempty"`
	Code            domain.ErrCode `json:"code,omitempty"`
	Message         string         `json:"message,omitempty"`
	Hint            string         `json:"hint,omitempty"`
}

type dirtyDoc struct {
	Tracked   int `json:"tracked"`
	Untracked int `json:"untracked"`
}

type summaryDoc struct {
	Total   int `json:"total"`
	Clean   int `json:"clean"`
	Dirty   int `json:"dirty"`
	Ahead   int `json:"ahead"`
	Behind  int `json:"behind"`
	Missing int `json:"missing"`
	// Attention covers detached, no-upstream and diverged repos: not healthy, not a gits failure.
	// Without it the buckets do not sum to total.
	Attention int `json:"attention"`
	Failed    int `json:"failed"`
	Skipped   int `json:"skipped"`
}

type depSummaryDoc struct {
	Outdated    int `json:"outdated"`
	Diverged    int `json:"diverged"`
	Unknown     int `json:"unknown,omitempty"`
	NoCanonical int `json:"noCanonical,omitempty"`
}

type statusDoc struct {
	header
	Network bool           `json:"network"`
	Stale   bool           `json:"stale,omitempty"`
	Repos   []repoDoc      `json:"repos"`
	Summary summaryDoc     `json:"summary"`
	Deps    *depSummaryDoc `json:"deps,omitempty"`
}

// Status writes a status payload.
func (j *JSON) Status(res *app.StatusResult) error {
	doc := statusDoc{
		header:  j.header("status", false),
		Network: res.Network,
		Stale:   res.Stale,
		Repos:   repoDocs(res.Repos),
		Summary: summaryDocOf(res.Summary),
	}
	if res.Deps != nil {
		doc.Deps = &depSummaryDoc{
			Outdated:    res.Deps.Outdated,
			Diverged:    res.Deps.Diverged,
			Unknown:     res.Deps.Unknown,
			NoCanonical: res.Deps.NoCanonical,
		}
	}
	return j.write(doc)
}

func repoDocs(statuses []domain.RepoStatus) []repoDoc {
	docs := make([]repoDoc, 0, len(statuses))
	for _, s := range statuses {
		d := repoDoc{
			Name:            s.Name,
			Path:            s.Path,
			Groups:          s.Groups,
			State:           string(s.State),
			Exists:          s.Exists,
			Branch:          s.Branch,
			DefaultBranch:   s.DefaultBranch,
			Upstream:        s.Upstream,
			SubmodulesClean: s.SubmodulesClean,
			NoWrite:         s.NoWrite,
			Code:            s.Code,
			Message:         s.Message,
			Hint:            s.Hint,
		}
		if s.IsRepo {
			ahead, behind := s.Ahead, s.Behind
			onDefault := s.OnDefaultBranch
			d.Ahead, d.Behind, d.OnDefaultBranch = &ahead, &behind, &onDefault
			d.Dirty = &dirtyDoc{Tracked: s.Dirty.Tracked, Untracked: s.Dirty.Untracked}
		}
		docs = append(docs, d)
	}
	return docs
}

func summaryDocOf(s domain.Summary) summaryDoc {
	return summaryDoc{
		Total: s.Total, Clean: s.Clean, Dirty: s.Dirty, Ahead: s.Ahead,
		Behind: s.Behind, Missing: s.Missing, Attention: s.Attention,
		Failed: s.Failed, Skipped: s.Skipped,
	}
}

// resultDoc is one repo in a write command's payload.
type resultDoc struct {
	Name      string         `json:"name"`
	Path      string         `json:"path"`
	Action    string         `json:"action"`
	Branch    string         `json:"branch,omitempty"`
	Upstream  string         `json:"upstream,omitempty"`
	Ahead     int            `json:"ahead,omitempty"`
	Behind    int            `json:"behind,omitempty"`
	Commits   int            `json:"commits,omitempty"`
	SHA       string         `json:"sha,omitempty"`
	Files     int            `json:"files,omitempty"`
	Untracked int            `json:"untracked,omitempty"`
	URL       string         `json:"url,omitempty"`
	NoWrite   bool           `json:"noWrite,omitempty"`
	Code      domain.ErrCode `json:"code,omitempty"`
	Message   string         `json:"message,omitempty"`
	Hint      string         `json:"hint,omitempty"`
}

func resultDocs(results []app.RepoResult) []resultDoc {
	docs := make([]resultDoc, 0, len(results))
	for _, r := range results {
		docs = append(docs, resultDoc{
			Name: r.Name, Path: r.Path, Action: string(r.Action),
			Branch: r.Branch, Upstream: r.Upstream,
			Ahead: r.Ahead, Behind: r.Behind, Commits: r.Commits,
			SHA: r.SHA, Files: r.Files, Untracked: r.Untracked, URL: r.URL,
			NoWrite: r.NoWrite, Code: r.Code, Message: r.Message, Hint: r.Hint,
		})
	}
	return docs
}

// excludedDocs renders repos left out by a boundary rather than by the user's filter.
//
// They are reported rather than silently dropped: a caller has to be able to see that the scope
// was narrowed and why (spec §6.6).
func excludedDocs(excluded []domain.Excluded) []resultDoc {
	docs := make([]resultDoc, 0, len(excluded))
	for _, e := range excluded {
		docs = append(docs, resultDoc{
			Name: e.Repo.Name, Path: e.Repo.EffectivePath(),
			Action: string(app.ActionSkipped), NoWrite: e.Repo.NoWrite,
			Code: e.Code, Message: "excluded from write commands",
		})
	}
	return docs
}

type writeDoc struct {
	header
	// ManifestStale marks a run whose repo list could not be refreshed from the root repo.
	ManifestStale bool        `json:"manifestStale,omitempty"`
	Root          *resultDoc  `json:"root,omitempty"`
	Repos         []resultDoc `json:"repos"`
	Skipped       []resultDoc `json:"skipped,omitempty"`
	Summary       summaryDoc  `json:"summary"`
}

// Sync writes a sync payload.
func (j *JSON) Sync(res *app.SyncResult, dryRun bool) error {
	doc := writeDoc{
		header:        j.header("sync", dryRun),
		ManifestStale: res.ManifestStale,
		Repos:         resultDocs(res.Repos),
		Skipped:       excludedDocs(res.Skipped),
		Summary:       summaryDocOf(res.Summary),
	}
	if res.Root != nil {
		root := resultDocs([]app.RepoResult{*res.Root})[0]
		doc.Root = &root
	}
	return j.write(doc)
}

// Push writes a push payload.
func (j *JSON) Push(res *app.PushResult) error {
	return j.write(writeDoc{
		header:  j.header("push", res.DryRun),
		Repos:   resultDocs(res.Repos),
		Skipped: excludedDocs(res.Skipped),
		Summary: summaryDocOf(res.Summary),
	})
}

// Commit writes a commit payload.
func (j *JSON) Commit(res *app.CommitResult) error {
	return j.write(writeDoc{
		header:  j.header("commit", res.DryRun),
		Repos:   resultDocs(res.Repos),
		Skipped: excludedDocs(res.Skipped),
		Summary: summaryDocOf(res.Summary),
	})
}

// Clone writes a clone payload.
func (j *JSON) Clone(res *app.CloneResult) error {
	return j.write(writeDoc{
		header:  j.header("clone", res.DryRun),
		Repos:   resultDocs(res.Repos),
		Skipped: excludedDocs(res.Skipped),
		Summary: summaryDocOf(res.Summary),
	})
}

type upDoc struct {
	header
	ManifestStale bool       `json:"manifestStale,omitempty"`
	Root          *resultDoc `json:"root,omitempty"`
	// Each stage is reported separately: "the clone failed" and "the sync failed" call for
	// different responses (spec §7.1).
	Clone   []resultDoc    `json:"clone,omitempty"`
	Sync    []resultDoc    `json:"sync,omitempty"`
	Repos   []repoDoc      `json:"repos"`
	Summary summaryDoc     `json:"summary"`
	Deps    *depSummaryDoc `json:"deps,omitempty"`
	Stale   bool           `json:"stale,omitempty"`
}

// Up writes an up payload.
func (j *JSON) Up(res *app.UpResult, dryRun bool) error {
	doc := upDoc{
		header:        j.header("up", dryRun),
		ManifestStale: res.ManifestStale,
	}
	if res.Root != nil {
		root := resultDocs([]app.RepoResult{*res.Root})[0]
		doc.Root = &root
	}
	if res.Clone != nil {
		doc.Clone = resultDocs(res.Clone.Repos)
	}
	if res.Sync != nil {
		doc.Sync = resultDocs(res.Sync.Repos)
	}
	if res.Status != nil {
		doc.Repos = repoDocs(res.Status.Repos)
		doc.Summary = summaryDocOf(res.Status.Summary)
		doc.Stale = res.Status.Stale
		if res.Status.Deps != nil {
			doc.Deps = &depSummaryDoc{
				Outdated:    res.Status.Deps.Outdated,
				Diverged:    res.Status.Deps.Diverged,
				Unknown:     res.Status.Deps.Unknown,
				NoCanonical: res.Status.Deps.NoCanonical,
			}
		}
	}
	return j.write(doc)
}

type listEntryDoc struct {
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	URL         string   `json:"url"`
	Branch      string   `json:"branch"`
	Remote      string   `json:"remote"`
	Groups      []string `json:"groups,omitempty"`
	NoWrite     bool     `json:"noWrite,omitempty"`
	Description string   `json:"description,omitempty"`
}

type listDoc struct {
	header
	Repos []listEntryDoc `json:"repos"`
}

// List writes a list payload.
func (j *JSON) List(res *app.ListResult) error {
	return j.write(listDoc{header: j.header("list", false), Repos: listEntryDocs(res.Repos)})
}

func listEntryDocs(entries []app.ListEntry) []listEntryDoc {
	docs := make([]listEntryDoc, 0, len(entries))
	for _, e := range entries {
		docs = append(docs, listEntryDoc{
			Name: e.Name, Path: e.Path, URL: e.URL, Branch: e.Branch, Remote: e.Remote,
			Groups: e.Groups, NoWrite: e.NoWrite, Description: e.Description,
		})
	}
	return docs
}

type pinDoc struct {
	Dependent          string         `json:"dependent"`
	SubmodulePath      string         `json:"submodulePath"`
	SHA                string         `json:"sha,omitempty"`
	Baseline           string         `json:"baseline,omitempty"`
	BaselineSource     string         `json:"baselineSource,omitempty"`
	Verdict            string         `json:"verdict"`
	Ahead              int            `json:"ahead,omitempty"`
	Behind             int            `json:"behind,omitempty"`
	ContainingBranches []string       `json:"containingBranches,omitempty"`
	Code               domain.ErrCode `json:"code,omitempty"`
	Message            string         `json:"message,omitempty"`
	Hint               string         `json:"hint,omitempty"`
}

type depGroupDoc struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
	// Canonical is null, not omitted, when the workspace has no checkout of this dependency.
	// An explicit null is what tells a caller the determination is incomplete rather than clean
	// (spec §7.11).
	Canonical    *string  `json:"canonical"`
	Baseline     string   `json:"baseline,omitempty"`
	BaselineSHA  string   `json:"baselineSha,omitempty"`
	DistinctSHAs int      `json:"distinctShas,omitempty"`
	Pins         []pinDoc `json:"pins"`

	Code    domain.ErrCode `json:"code,omitempty"`
	Message string         `json:"message,omitempty"`
	Hint    string         `json:"hint,omitempty"`
}

type depsDoc struct {
	header
	Network bool          `json:"network"`
	Deps    []depGroupDoc `json:"deps"`
	Summary depSummaryDoc `json:"summary"`
}

// Deps writes a dependency payload.
func (j *JSON) Deps(groups []domain.DepGroup, network bool) error {
	doc := depsDoc{header: j.header("deps", false), Network: network}
	for _, g := range groups {
		gd := depGroupDoc{
			Name: g.Name, URL: g.URL,
			Baseline: g.BaselineRef, BaselineSHA: g.BaselineSHA,
			DistinctSHAs: g.DistinctSHAs(),
			Code:         g.Code, Message: g.Message, Hint: g.Hint,
		}
		if g.CanonicalPath != "" {
			p := g.CanonicalPath
			gd.Canonical = &p
		}
		for _, p := range g.Pins {
			gd.Pins = append(gd.Pins, pinDoc{
				Dependent: p.Dependent, SubmodulePath: p.SubmodulePath, SHA: p.SHA,
				Baseline: p.BaselineRef, BaselineSource: string(p.BaselineSource),
				Verdict: string(p.Verdict), Ahead: p.Ahead, Behind: p.Behind,
				ContainingBranches: p.ContainingBranches,
				Code:               p.Code, Message: p.Message, Hint: p.Hint,
			})
		}
		doc.Deps = append(doc.Deps, gd)
	}
	sum := domain.SummarizeDeps(groups)
	doc.Summary = depSummaryDoc{
		Outdated:    sum.Outdated,
		Diverged:    sum.Diverged,
		Unknown:     sum.Unknown,
		NoCanonical: sum.NoCanonical,
	}
	if doc.Deps == nil {
		doc.Deps = []depGroupDoc{}
	}
	return j.write(doc)
}

type foreachOutputDoc struct {
	Name      string         `json:"name"`
	Path      string         `json:"path"`
	ExitCode  int            `json:"exitCode"`
	Stdout    string         `json:"stdout,omitempty"`
	Stderr    string         `json:"stderr,omitempty"`
	Truncated bool           `json:"truncated,omitempty"`
	Code      domain.ErrCode `json:"code,omitempty"`
	Message   string         `json:"message,omitempty"`
}

type foreachDoc struct {
	header
	CommandArgs []string           `json:"commandArgs"`
	Repos       []foreachOutputDoc `json:"repos"`
	Skipped     []resultDoc        `json:"skipped,omitempty"`
}

// Foreach writes a foreach payload.
func (j *JSON) Foreach(res *app.ForeachResult) error {
	doc := foreachDoc{
		header:      j.header("foreach", res.DryRun),
		CommandArgs: res.Command,
		Skipped:     excludedDocs(res.Skipped),
	}
	for _, o := range res.Repos {
		doc.Repos = append(doc.Repos, foreachOutputDoc{
			Name: o.Name, Path: o.Path, ExitCode: o.ExitCode,
			Stdout: o.Stdout, Stderr: o.Stderr, Truncated: o.Truncated,
			Code: o.Code, Message: o.Message,
		})
	}
	if doc.Repos == nil {
		doc.Repos = []foreachOutputDoc{}
	}
	return j.write(doc)
}

type initDoc struct {
	header
	ManifestPathWritten string         `json:"manifestPathWritten"`
	Repos               []listEntryDoc `json:"repos"`
	MissingURL          []string       `json:"missingUrl,omitempty"`
	ManifestIgnored     bool           `json:"manifestIgnored,omitempty"`
	GitignoreUpdated    bool           `json:"gitignoreUpdated,omitempty"`
}

// Init writes an init payload.
func (j *JSON) Init(res *app.InitResult, dryRun bool) error {
	return j.write(initDoc{
		header:              j.header("init", dryRun),
		ManifestPathWritten: toSlash(res.ManifestPath),
		Repos:               listEntryDocs(res.Repos),
		MissingURL:          res.MissingURL,
		ManifestIgnored:     res.ManifestIgnored,
		GitignoreUpdated:    res.GitignoreUpdated,
	})
}

type mismatchDoc struct {
	Name        string `json:"name"`
	ManifestURL string `json:"manifestUrl"`
	ActualURL   string `json:"actualUrl"`
}

type adoptDoc struct {
	header
	Adopted     []listEntryDoc `json:"adopted"`
	Skipped     []string       `json:"skipped,omitempty"`
	Missing     []string       `json:"missing,omitempty"`
	URLMismatch []mismatchDoc  `json:"urlMismatch,omitempty"`
}

// Adopt writes an adopt payload.
func (j *JSON) Adopt(res *app.AdoptResult) error {
	doc := adoptDoc{
		header:  j.header("adopt", res.DryRun),
		Adopted: listEntryDocs(res.Adopted),
		Skipped: res.Skipped,
		Missing: res.Missing,
	}
	for _, m := range res.URLMismatch {
		doc.URLMismatch = append(doc.URLMismatch, mismatchDoc{
			Name: m.Name, ManifestURL: m.ManifestURL, ActualURL: m.ActualURL,
		})
	}
	return j.write(doc)
}

type addDoc struct {
	header
	Repo    listEntryDoc `json:"repo"`
	Added   bool         `json:"added,omitempty"`
	Updated bool         `json:"updated,omitempty"`
	NoOp    bool         `json:"noop,omitempty"`
}

// Add writes an add payload.
func (j *JSON) Add(res *app.AddResult) error {
	return j.write(addDoc{
		header:  j.header("add", false),
		Repo:    listEntryDocs([]app.ListEntry{res.Repo})[0],
		Added:   res.Added,
		Updated: res.Updated,
		NoOp:    res.NoOp,
	})
}

type formattedFileDoc struct {
	File      string   `json:"file"`
	Entries   int      `json:"entries"`
	Changed   bool     `json:"changed"`
	Reordered []string `json:"reordered,omitempty"`
}

type formatDoc struct {
	header
	Changed bool               `json:"changed"`
	Files   []formattedFileDoc `json:"files"`
}

// Format writes a fmt payload.
//
// The top-level changed is the field a hook branches on -- false means every file was already
// canonical and nothing was written, true under dryRun means something would have been. The
// per-file breakdown is there for a caller that cares which one it was.
func (j *JSON) Format(res *app.FormatResult) error {
	doc := formatDoc{
		header:  j.header("fmt", res.DryRun),
		Changed: res.Changed(),
		Files:   make([]formattedFileDoc, 0, len(res.Files)),
	}
	for _, f := range res.Files {
		doc.Files = append(doc.Files, formattedFileDoc{
			File:      f.File,
			Entries:   f.Entries,
			Changed:   f.Changed,
			Reordered: f.Reordered,
		})
	}
	return j.write(doc)
}

type errorDoc struct {
	header
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code    domain.ErrCode `json:"code"`
	Message string         `json:"message"`
	Hint    string         `json:"hint,omitempty"`
}

// Error writes a failure as JSON.
//
// A command that fails must still put a parseable object on stdout: a caller that gets prose on a
// failure and JSON on success has to write two parsers, and will usually only write one.
func (j *JSON) Error(command string, err error) error {
	hint := ""
	var ae *app.Error
	if asAppError(err, &ae) {
		hint = ae.Hint
	}
	return j.write(errorDoc{
		header: j.header(command, false),
		Error: errorPayload{
			Code:    app.CodeOf(err),
			Message: app.MessageOf(err),
			Hint:    hint,
		},
	})
}

func (j *JSON) write(doc any) error {
	enc := json.NewEncoder(j.out)
	enc.SetIndent("", "  ")
	// Repo names and URLs are not HTML; escaping them would corrupt output for no benefit.
	enc.SetEscapeHTML(false)
	return enc.Encode(doc)
}

// toSlash normalises a path to forward slashes.
//
// Windows and Linux would otherwise emit different strings for the same workspace, so a caller
// comparing output across machines would see a difference that is not one.
func toSlash(p string) string {
	return strings.TrimRight(filepath.ToSlash(p), "/")
}

// asAppError is a thin errors.As wrapper keeping the call site above readable.
func asAppError(err error, target **app.Error) bool { return errors.As(err, target) }
