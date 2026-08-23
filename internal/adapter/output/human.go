package output

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/nekogravitycat/gits/internal/app"
	"github.com/nekogravitycat/gits/internal/domain"
)

// Human renders results for someone reading a terminal.
type Human struct {
	out       io.Writer
	style     Style
	workspace string
}

// NewHuman builds a human renderer.
func NewHuman(out io.Writer, style Style, workspace string) *Human {
	return &Human{out: out, style: style, workspace: workspace}
}

func (h *Human) printf(format string, args ...any) {
	fmt.Fprintf(h.out, format, args...)
}

// Status renders the workspace report (spec §7.2).
//
// Every repo is listed, healthy ones dimmed rather than hidden. Seeing all eighteen go by is what
// tells the reader the scan really covered everything, instead of leaving them wondering whether
// a repo was quietly skipped.
func (h *Human) Status(res *app.StatusResult) {
	h.printf("workspace: %s  (%d repos)\n\n", h.workspace, res.Summary.Total)

	width := nameWidth(res.Repos)
	for _, s := range res.Repos {
		h.repoLine(s, width)
	}

	if len(res.Skipped) > 0 {
		h.printf("\n")
		for _, e := range res.Skipped {
			h.printf("  %s %s  (%s)\n", h.style.Dim("-"), e.Repo.Name, e.Code)
		}
	}

	h.printf("\n")
	h.summary(res.Summary)

	if res.Deps != nil && res.Deps.Any() {
		h.printf("%s\n", h.depsLine(*res.Deps))
	}
	if res.Stale {
		// Said plainly rather than guessed around. The offline numbers come from local refs and
		// may lag; presenting them as live would be the one dishonesty the spec rules out (§6.9).
		h.printf("%s\n", h.style.Dim("data may be stale (offline); add --fetch for live status"))
	}
}

func (h *Human) repoLine(s domain.RepoStatus, width int) {
	body := fmt.Sprintf("%s  %s", pad(s.Name, width), pad(orDash(s.Branch), 12))
	if detail := statusDetail(s); detail != "" {
		body += "  " + detail
	}
	body = strings.TrimRight(body, " ")

	if s.State == domain.StateClean {
		// Dimmed rather than omitted: present and accounted for, without competing for attention
		// with the repos that actually need something.
		body = h.style.Dim(body)
	}
	if s.NoWrite {
		body += "  " + h.style.Dim("[no-write]")
	}
	h.printf("  %s %s\n", h.style.Symbol(s.State), body)

	if s.Hint != "" {
		h.printf("      %s %s\n", h.style.Dim("->"), s.Hint)
	}
}

// statusDetail is the right-hand explanation on a repo's line.
func statusDetail(s domain.RepoStatus) string {
	var parts []string

	if s.Message != "" {
		msg := s.Message
		if s.Code != "" {
			msg += " (" + string(s.Code) + ")"
		}
		parts = append(parts, msg)
	}
	if s.Dirty.Tracked > 0 {
		parts = append(parts, fmt.Sprintf("uncommitted %d", s.Dirty.Tracked))
	}
	if s.Dirty.Untracked > 0 {
		parts = append(parts, fmt.Sprintf("untracked %d", s.Dirty.Untracked))
	}
	if s.Behind > 0 {
		parts = append(parts, fmt.Sprintf("behind %d", s.Behind))
	}
	if s.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("ahead %d", s.Ahead))
	}
	// A feature branch is normal work, not a fault: it is flagged so the reader knows the
	// comparison baseline differs, and deliberately not treated as an error (spec §7.2).
	if s.IsRepo && s.Branch != "" && !s.OnDefaultBranch && s.DefaultBranch != "" {
		parts = append(parts, "(default is "+s.DefaultBranch+")")
	}
	if s.SubmodulesClean != nil && !*s.SubmodulesClean {
		parts = append(parts, "submodules differ from gitlinks")
	}
	return strings.Join(parts, ", ")
}

func (h *Human) summary(s domain.Summary) {
	parts := []string{fmt.Sprintf("%d repos", s.Total)}
	add := func(n int, label string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, label))
		}
	}
	add(s.Clean, "clean")
	add(s.Dirty, "dirty")
	add(s.Ahead, "ahead")
	add(s.Behind, "behind")
	add(s.Missing, "missing")
	// Named explicitly rather than folded into another bucket: otherwise these repos disappear
	// from the tally and the parts stop adding up to the total.
	add(s.Attention, "need attention")
	add(s.Failed, "failed")
	add(s.Skipped, "skipped")
	h.printf("%s %s\n", h.style.Bold("summary:"), strings.Join(parts, " - "))
}

func (h *Human) depsLine(d domain.DepSummary) string {
	var parts []string
	if d.Outdated > 0 {
		parts = append(parts, fmt.Sprintf("%d repos pinned to an outdated dependency", d.Outdated))
	}
	if d.Diverged > 0 {
		parts = append(parts, fmt.Sprintf("%d diverged", d.Diverged))
	}
	if d.Unknown > 0 {
		parts = append(parts, fmt.Sprintf("%d undetermined", d.Unknown))
	}
	if d.NoCanonical > 0 {
		parts = append(parts, fmt.Sprintf("%d without a canonical checkout", d.NoCanonical))
	}
	return "deps: " + strings.Join(parts, ", ") + " (see gits deps for details)"
}

// StatusByGroup renders the report grouped by group label (spec §7.2, --by-group).
//
// A repo in several groups is listed under each of them. Picking one group arbitrarily would be a
// silent lie about the workspace, and deduplicating would hide the membership the reader asked to
// see -- which is exactly why grouping is not the default.
func (h *Human) StatusByGroup(res *app.StatusResult) {
	h.printf("workspace: %s  (%d repos)\n", h.workspace, res.Summary.Total)

	byGroup := map[string][]domain.RepoStatus{}
	var ungrouped []domain.RepoStatus
	for _, s := range res.Repos {
		if len(s.Groups) == 0 {
			ungrouped = append(ungrouped, s)
			continue
		}
		for _, g := range s.Groups {
			byGroup[g] = append(byGroup[g], s)
		}
	}

	names := make([]string, 0, len(byGroup))
	for g := range byGroup {
		names = append(names, g)
	}
	sort.Strings(names)

	for _, g := range names {
		h.printf("\n%s\n", h.style.Bold(g))
		width := nameWidth(byGroup[g])
		for _, s := range byGroup[g] {
			h.repoLine(s, width)
		}
	}
	if len(ungrouped) > 0 {
		h.printf("\n%s\n", h.style.Bold("(no group)"))
		width := nameWidth(ungrouped)
		for _, s := range ungrouped {
			h.repoLine(s, width)
		}
	}

	h.printf("\n")
	h.summary(res.Summary)
	if res.Deps != nil && res.Deps.Any() {
		h.printf("%s\n", h.depsLine(*res.Deps))
	}
	if res.Stale {
		h.printf("%s\n", h.style.Dim("data may be stale (offline); add --fetch for live status"))
	}
}

// Results renders a write command's per-repo outcomes.
func (h *Human) Results(title string, results []app.RepoResult, skipped []domain.Excluded, summary domain.Summary, dryRun bool) {
	if dryRun {
		title += "  " + h.style.Dim("(dry run: nothing was changed)")
	}
	h.printf("%s\n\n", h.style.Bold(title))

	width := resultNameWidth(results)
	for _, r := range results {
		line := fmt.Sprintf("  %s %s  %s", h.style.ActionSymbol(r.Action), pad(r.Name, width), r.Message)
		if r.Code != "" {
			line += " (" + string(r.Code) + ")"
		}
		h.printf("%s\n", strings.TrimRight(line, " "))
		if r.Hint != "" {
			// Always a command that can be pasted and run, never "needs manual attention": the
			// reader should not have to work out the rebase target themselves (spec §7.3).
			h.printf("      %s %s\n", h.style.Dim("->"), r.Hint)
		}
	}

	if len(skipped) > 0 {
		h.printf("\n%s\n", h.style.Bold("Skipped:"))
		for _, e := range skipped {
			h.printf("  %s %s  (%s)\n", h.style.Dim("-"), e.Repo.Name, e.Code)
		}
	}

	h.printf("\n")
	h.summary(summary)
}

// Sync renders a sync run, including the root-repo stage.
func (h *Human) Sync(res *app.SyncResult, dryRun bool) {
	h.rootStage(res.Root, res.ManifestStale)
	h.Results("Sync", res.Repos, res.Skipped, res.Summary, dryRun)
}

// rootStage renders the workspace root repo's own outcome and the stale-list warning.
func (h *Human) rootStage(root *app.RepoResult, stale bool) {
	if root == nil {
		return
	}
	h.printf("%s %s %s\n", h.style.Bold("workspace root:"), h.style.ActionSymbol(root.Action), root.Message)
	if stale {
		// Never continue quietly on an old list. A repo added on the other machine may simply be
		// absent from this run, and the user has to know that before trusting the report (§7.1).
		h.printf("  %s repo list may be stale: %s could not be updated\n",
			h.style.Symbol(domain.StateDiverged), app.ManifestName)
	}
	h.printf("\n")
}

// Up renders the full up run, stage by stage.
func (h *Human) Up(res *app.UpResult, dryRun bool) {
	h.rootStage(res.Root, res.ManifestStale)

	if res.Clone != nil {
		cloned := filterActed(res.Clone.Repos)
		if len(cloned) > 0 {
			h.Results("Cloned", cloned, nil, res.Clone.Summary, dryRun)
			h.printf("\n")
		}
	}
	if res.Sync != nil {
		synced := filterActed(res.Sync.Repos)
		if len(synced) > 0 {
			h.Results("Synced", synced, res.Sync.Skipped, res.Sync.Summary, dryRun)
			h.printf("\n")
		}
	}
	if res.Status != nil {
		h.Status(res.Status)
	}
}

// filterActed keeps the rows worth showing after a bulk stage: anything that moved, was skipped or
// failed. Listing a dozen "already up to date" lines would bury the two that matter.
func filterActed(results []app.RepoResult) []app.RepoResult {
	var out []app.RepoResult
	for _, r := range results {
		if r.Action != app.ActionUpToDate {
			out = append(out, r)
		}
	}
	return out
}

// Deps renders the dependency report, grouped by the repo being depended on (spec §7.11).
func (h *Human) Deps(groups []domain.DepGroup) {
	if len(groups) == 0 {
		h.printf("no submodule dependencies found in this workspace\n")
		return
	}

	for i, g := range groups {
		if i > 0 {
			h.printf("\n")
		}
		h.depGroupHeader(g)

		width := 0
		for _, p := range g.Pins {
			width = max(width, len(p.Dependent))
		}
		for _, p := range g.Pins {
			h.pinLine(p, g, width)
		}

		h.printf("\n%d repos depend on %s, pinned to %d different commits\n",
			len(g.Pins), g.Name, g.DistinctSHAs())
		if g.Hint != "" {
			h.printf("  %s %s\n", h.style.Dim("->"), g.Hint)
		}
	}

	// The report states facts -- behind, diverged, inconsistent -- and stops there. Whether being
	// three commits behind actually breaks anything takes reading those three commits, and
	// claiming otherwise would make the whole report untrustworthy (spec §7.11).
	h.printf("\n%s\n", h.style.Dim(
		"deps reports drift, not breakage; check what the missing commits changed"))
}

func (h *Human) depGroupHeader(g domain.DepGroup) {
	if g.HasCanonical() {
		h.printf("%s  (canonical: ./%s, baseline %s",
			h.style.Bold(g.Name), g.CanonicalPath, g.BaselineRef)
		if g.BaselineSHA != "" {
			h.printf(" @ %s", shortSHA(g.BaselineSHA))
		}
		h.printf(")\n\n")
		return
	}
	h.printf("%s  (%s)\n", h.style.Bold(g.Name), h.style.Dim("no canonical checkout in this workspace"))
	if g.Message != "" {
		h.printf("  %s\n", g.Message)
	}
	h.printf("\n")
}

func (h *Human) pinLine(p domain.Pin, g domain.DepGroup, width int) {
	detail := p.Message
	if detail == "" && p.Verdict == domain.PinUpToDate {
		detail = "up to date"
	}
	h.printf("  %s %s  %s  %s\n",
		h.style.VerdictSymbol(p.Verdict), pad(p.Dependent, width), shortSHA(p.SHA), detail)

	// When a dependent tracks a branch of its own, say so on the line. Otherwise its permanent
	// "behind" reads as a fault rather than as the deliberate choice it is (spec §7.11).
	if p.BaselineSource == domain.BaselineDeclared && p.BaselineRef != g.BaselineRef {
		h.printf("      %s\n", h.style.Dim("baseline is its declared "+p.BaselineRef))
	}
	if len(p.ContainingBranches) > 0 {
		h.printf("      %s\n", h.style.Dim("contained in: "+strings.Join(p.ContainingBranches, ", ")))
	}
	if p.Hint != "" {
		h.printf("      %s %s\n", h.style.Dim("->"), p.Hint)
	}
}

// List renders the manifest as a table.
func (h *Human) List(res *app.ListResult) {
	width := 0
	for _, e := range res.Repos {
		width = max(width, len(e.Name))
	}
	for _, e := range res.Repos {
		line := fmt.Sprintf("  %s  %s", pad(e.Name, width), e.Branch)
		if len(e.Groups) > 0 {
			line += "  [" + strings.Join(e.Groups, ", ") + "]"
		}
		if e.NoWrite {
			line += "  " + h.style.Dim("[no-write]")
		}
		h.printf("%s\n", line)
	}
}

// ListNames writes one name per line, for shell loops.
func (h *Human) ListNames(res *app.ListResult) {
	for _, e := range res.Repos {
		h.printf("%s\n", e.Name)
	}
}

// ListMarkdown writes a Markdown table (spec §7.10).
//
// This is the direct answer to agents reading a hand-maintained repo table in CLAUDE.md and
// walking into repos that no longer exist. A generated table cannot drift from the manifest.
func (h *Human) ListMarkdown(res *app.ListResult) {
	h.printf("| Repo | Path | Branch | Groups | Write | Description |\n")
	h.printf("| --- | --- | --- | --- | --- | --- |\n")
	for _, e := range res.Repos {
		write := "yes"
		if e.NoWrite {
			write = "no-write"
		}
		h.printf("| %s | `%s` | %s | %s | %s | %s |\n",
			e.Name, e.Path, e.Branch, strings.Join(e.Groups, ", "), write, e.Description)
	}
}

// Foreach renders each repo's captured output.
func (h *Human) Foreach(res *app.ForeachResult) {
	for i, o := range res.Repos {
		if i > 0 {
			h.printf("\n")
		}
		status := h.style.paint(ansiGreen, "exit 0")
		if o.ExitCode != 0 {
			status = h.style.paint(ansiRed, fmt.Sprintf("exit %d", o.ExitCode))
		}
		h.printf("%s  %s\n", h.style.Bold(o.Name), status)
		if o.Message != "" {
			h.printf("  %s\n", o.Message)
		}
		if out := strings.TrimRight(o.Stdout, "\n"); out != "" {
			h.printf("%s\n", indent(out, "  "))
		}
		if errOut := strings.TrimRight(o.Stderr, "\n"); errOut != "" {
			h.printf("%s\n", h.style.Dim(indent(errOut, "  ")))
		}
		if o.Truncated {
			h.printf("  %s\n", h.style.Dim("(output truncated at 8KB)"))
		}
	}
}

// Init renders the outcome of creating a manifest.
func (h *Human) Init(res *app.InitResult) {
	h.printf("wrote %s with %d entries\n", app.ManifestName, len(res.Repos))
	for _, e := range res.Repos {
		h.printf("  %s\n", e.Name)
	}
	if len(res.MissingURL) > 0 {
		h.printf("\n%s no origin remote found for: %s\n",
			h.style.Symbol(domain.StateDiverged), strings.Join(res.MissingURL, ", "))
		h.printf("  fill in their url before running gits clone\n")
	}
	if res.ManifestIgnored {
		h.printf("\n%s .gitignore was excluding %s\n", h.style.Symbol(domain.StateDiverged), app.ManifestName)
		if res.GitignoreUpdated {
			h.printf("  added an allowlist entry so it can be committed\n")
		}
	}
	h.printf("\nnext: fill in groups and no-write, then commit %s\n", app.ManifestName)
	h.printf("  git add %s && git commit -m \"add gits manifest\"\n", app.ManifestName)
}

// Adopt renders the outcome of registering existing repos.
func (h *Human) Adopt(res *app.AdoptResult) {
	if len(res.Adopted) == 0 {
		h.printf("nothing to adopt: every repo on disk is already in %s\n", app.ManifestName)
	} else {
		verb := "adopted"
		if res.DryRun {
			verb = "would adopt"
		}
		h.printf("%s %d repo(s)\n", verb, len(res.Adopted))
		for _, e := range res.Adopted {
			h.printf("  %s  %s\n", e.Name, e.URL)
		}
	}

	if len(res.Skipped) > 0 {
		h.printf("\nskipped: %s\n", strings.Join(res.Skipped, ", "))
	}

	// Both directions of drift are reported and neither is acted on. Cloning or rewriting a URL
	// would be a far larger action than the user asked for (spec §7.8).
	if len(res.Missing) > 0 {
		h.printf("\n%s in the manifest but not on disk: %s\n",
			h.style.Symbol(domain.StateMissing), strings.Join(res.Missing, ", "))
		h.printf("  %s gits clone\n", h.style.Dim("->"))
	}
	for _, m := range res.URLMismatch {
		h.printf("\n%s %s points at a different origin than the manifest records\n",
			h.style.Symbol(domain.StateDiverged), m.Name)
		h.printf("  manifest: %s\n  actual:   %s\n", m.ManifestURL, m.ActualURL)
	}
}

// Add renders the outcome of registering one repo.
func (h *Human) Add(res *app.AddResult) {
	switch {
	case res.NoOp:
		h.printf("%s is already in %s with these settings\n", res.Repo.Name, app.ManifestName)
	case res.Updated:
		h.printf("updated %s in %s\n", res.Repo.Name, app.ManifestName)
	default:
		h.printf("added %s to %s\n", res.Repo.Name, app.ManifestName)
	}
	h.printf("  url:    %s\n  path:   %s\n  branch: %s\n", res.Repo.URL, res.Repo.Path, res.Repo.Branch)
	if !res.NoOp {
		// The manifest is only a list; the checkout is a separate, explicit step (spec §7.9).
		h.printf("\nnext: gits clone -r %s\n", res.Repo.Name)
	}
}

// Error renders a fatal error with its code and next step.
func (h *Human) Error(err error) {
	code := app.CodeOf(err)
	h.printf("%s %s (%s)\n", h.style.Symbol(domain.StateError), app.MessageOf(err), code)

	var ae *app.Error
	if asAppError(err, &ae) && ae.Hint != "" {
		h.printf("  %s %s\n", h.style.Dim("->"), ae.Hint)
	}
}

func nameWidth(statuses []domain.RepoStatus) int {
	w := 0
	for _, s := range statuses {
		w = max(w, len(s.Name))
	}
	return w
}

func resultNameWidth(results []app.RepoResult) int {
	w := 0
	for _, r := range results {
		w = max(w, len(r.Name))
	}
	return w
}

func pad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	if sha == "" {
		return "-------"
	}
	return sha
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
