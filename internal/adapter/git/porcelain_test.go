package git

import (
	"strings"
	"testing"

	"github.com/nekogravitycat/gits/internal/domain"
)

func TestParseStatus_CleanTrackingBranch(t *testing.T) {
	out := `# branch.oid a1b2c3d4e5f6
# branch.head main
# branch.upstream origin/main
# branch.ab +0 -0
`
	obs := parseStatus(out)
	if obs.Branch != "main" {
		t.Errorf("Branch = %q, want main", obs.Branch)
	}
	if obs.Upstream != "origin/main" {
		t.Errorf("Upstream = %q, want origin/main", obs.Upstream)
	}
	if obs.Ahead != 0 || obs.Behind != 0 {
		t.Errorf("ahead/behind = %d/%d, want 0/0", obs.Ahead, obs.Behind)
	}
	if obs.Dirty.Any() {
		t.Errorf("Dirty = %+v, want empty", obs.Dirty)
	}
	if obs.Detached {
		t.Error("Detached = true, want false")
	}
}

// "+12 -3" means ahead 12 and behind 3. Swapping them would invert every sync and push decision.
func TestParseStatus_AheadBehind(t *testing.T) {
	obs := parseStatus("# branch.head main\n# branch.upstream origin/main\n# branch.ab +12 -3\n")
	if obs.Ahead != 12 {
		t.Errorf("Ahead = %d, want 12", obs.Ahead)
	}
	if obs.Behind != 3 {
		t.Errorf("Behind = %d, want 3", obs.Behind)
	}
}

func TestParseStatus_Detached(t *testing.T) {
	obs := parseStatus("# branch.oid a1b2c3d\n# branch.head (detached)\n")
	if !obs.Detached {
		t.Error("Detached = false, want true")
	}
	if obs.Branch != "" {
		t.Errorf("Branch = %q, want empty for a detached HEAD", obs.Branch)
	}
}

// A branch with no upstream emits no branch.upstream line at all, and no branch.ab either.
func TestParseStatus_NoUpstream(t *testing.T) {
	obs := parseStatus("# branch.oid a1b2c3d\n# branch.head feature/x\n")
	if obs.Upstream != "" {
		t.Errorf("Upstream = %q, want empty", obs.Upstream)
	}
	if obs.Branch != "feature/x" {
		t.Errorf("Branch = %q, want feature/x", obs.Branch)
	}
}

func TestParseStatus_InitialCommit(t *testing.T) {
	obs := parseStatus("# branch.oid (initial)\n# branch.head main\n")
	if obs.Head != "" {
		t.Errorf("Head = %q, want empty before the first commit", obs.Head)
	}
}

// The tracked/untracked split drives which repos sync skips and what commit includes, so the two
// counts must never be conflated.
func TestParseStatus_TrackedAndUntracked(t *testing.T) {
	out := `# branch.oid a1b2c3d
# branch.head main
# branch.upstream origin/main
# branch.ab +0 -2
1 .M N... 100644 100644 100644 aaa bbb internal/round/loop.go
1 M. N... 100644 100644 100644 ccc ddd internal/round/timing.go
2 R. N... 100644 100644 100644 eee fff R100 new/path.go	old/path.go
u UU N... 100644 100644 100644 100644 ggg hhh iii conflicted.go
? notes.txt
? build/output.bin
! ignored.log
`
	obs := parseStatus(out)
	if obs.Dirty.Tracked != 4 {
		t.Errorf("Tracked = %d, want 4 (one ordinary x2, one rename, one unmerged)", obs.Dirty.Tracked)
	}
	if obs.Dirty.Untracked != 2 {
		t.Errorf("Untracked = %d, want 2 (ignored files do not count)", obs.Dirty.Untracked)
	}
	if obs.Behind != 2 {
		t.Errorf("Behind = %d, want 2", obs.Behind)
	}
}

func TestParseStatus_SubmoduleClean(t *testing.T) {
	// A submodule matching its gitlink produces no record at all.
	obs := parseStatus("# branch.head main\n# branch.upstream origin/main\n")
	if !obs.SubmodulesClean {
		t.Error("SubmodulesClean = false; a repo with no submodule records is clean")
	}
}

func TestParseStatus_SubmoduleDrift(t *testing.T) {
	tests := []struct {
		name string
		sub  string
	}{
		{"checked-out commit differs from the gitlink", "SC.."},
		{"modified content inside the submodule", "S.M."},
		{"untracked content inside the submodule", "S..U"},
		{"all three", "SCMU"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := "# branch.head main\n" +
				"1 .M " + tt.sub + " 160000 160000 160000 aaa bbb proto\n"
			obs := parseStatus(out)
			if !obs.HasSubmodules {
				t.Error("HasSubmodules = false, want true")
			}
			if obs.SubmodulesClean {
				t.Errorf("SubmodulesClean = true for sub field %q, want false", tt.sub)
			}
		})
	}
}

// An ordinary file must never be mistaken for a submodule: "N..." is the not-a-submodule marker.
func TestParseStatus_OrdinaryFileIsNotASubmodule(t *testing.T) {
	obs := parseStatus("# branch.head main\n1 .M N... 100644 100644 100644 aaa bbb main.go\n")
	if obs.HasSubmodules {
		t.Error("HasSubmodules = true for an ordinary changed file")
	}
}

func TestParseStatus_CRLF(t *testing.T) {
	obs := parseStatus("# branch.head main\r\n# branch.upstream origin/main\r\n# branch.ab +1 -0\r\n")
	if obs.Branch != "main" || obs.Upstream != "origin/main" || obs.Ahead != 1 {
		t.Errorf("CRLF output parsed as %+v", obs)
	}
}

func TestParseStatus_PathsWithSpaces(t *testing.T) {
	obs := parseStatus("# branch.head main\n1 .M N... 100644 100644 100644 aaa bbb docs/my notes.md\n")
	if obs.Dirty.Tracked != 1 {
		t.Errorf("Tracked = %d, want 1", obs.Dirty.Tracked)
	}
}

func TestParseGitmodules(t *testing.T) {
	content := `[submodule "proto"]
	path = proto
	url = ssh://git@git.example.com:24/game/shared-proto.git

# a comment
[submodule "vendor/tools"]
	path = vendor/tools
	url = https://host/a/tools.git
	branch = feature/arcade-proto
`
	subs := parseGitmodules(content, nil)
	if len(subs) != 2 {
		t.Fatalf("parsed %d submodules, want 2: %+v", len(subs), subs)
	}

	if subs[0].Name != "proto" || subs[0].Path != "proto" {
		t.Errorf("first submodule = %+v", subs[0])
	}
	if subs[0].Branch != "" {
		t.Errorf("Branch = %q, want empty when undeclared", subs[0].Branch)
	}
	// The declared branch is the dependent's own statement of intent and outranks every other
	// baseline rule, so it has to survive parsing.
	if subs[1].Branch != "feature/arcade-proto" {
		t.Errorf("Branch = %q, want the declared feature branch", subs[1].Branch)
	}
	if subs[1].Path != "vendor/tools" {
		t.Errorf("Path = %q, want vendor/tools", subs[1].Path)
	}
}

func TestParseGitmodules_Empty(t *testing.T) {
	if subs := parseGitmodules("", nil); len(subs) != 0 {
		t.Errorf("parsed %d submodules from empty content", len(subs))
	}
}

// Entries with no path are incomplete and must not become phantom dependencies. The drop must not
// be silent, though: a warning is the only way a caller learns a dependency vanished.
func TestParseGitmodules_SkipsEntryWithoutPath(t *testing.T) {
	var warnings []string
	subs := parseGitmodules("[submodule \"broken\"]\n\turl = https://host/a/b.git\n", func(msg string) {
		warnings = append(warnings, msg)
	})
	if len(subs) != 0 {
		t.Errorf("parsed %+v, want nothing for an entry with no path", subs)
	}
	if len(warnings) != 1 {
		t.Fatalf("got %d warnings, want 1: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "broken") {
		t.Errorf("warning %q does not name the malformed section", warnings[0])
	}
}

// A well-formed .gitmodules must not warn about anything.
func TestParseGitmodules_NoWarningsWhenWellFormed(t *testing.T) {
	warned := false
	parseGitmodules("[submodule \"proto\"]\n\tpath = proto\n\turl = https://host/a/b.git\n", func(string) {
		warned = true
	})
	if warned {
		t.Errorf("well-formed .gitmodules should not produce a warning")
	}
}

// Every dependent in a real workspace names the submodule differently while pointing at the same
// repo. Only the URL identifies it, so the URL must round-trip exactly.
func TestParseGitmodules_URLIdentifiesTheDependency(t *testing.T) {
	a := parseGitmodules("[submodule \"proto\"]\n\tpath = proto\n\turl = ssh://git@host:24/a/b.git\n", nil)
	b := parseGitmodules("[submodule \"shared-proto\"]\n\tpath = shared-proto\n\turl = https://host/a/b\n", nil)
	if !domain.SameRepoURL(a[0].URL, b[0].URL) {
		t.Errorf("%q and %q should resolve to the same dependency", a[0].URL, b[0].URL)
	}
}

func TestParseLsTree(t *testing.T) {
	out := "160000 commit ca3426c1234567890abcdef1234567890abcdef12\tproto\n" +
		"100644 blob  aaaaaaa1234567890abcdef1234567890abcdef12\tREADME.md\n" +
		"160000 commit d2b1fb21234567890abcdef1234567890abcdef12\tvendor/tools\n"
	pins := parseLsTree(out)

	if len(pins) != 2 {
		t.Fatalf("parsed %d gitlinks, want 2 (blobs are not pins): %+v", len(pins), pins)
	}
	if pins["proto"] != "ca3426c1234567890abcdef1234567890abcdef12" {
		t.Errorf("proto pin = %q", pins["proto"])
	}
	if pins["vendor/tools"] != "d2b1fb21234567890abcdef1234567890abcdef12" {
		t.Errorf("vendor/tools pin = %q", pins["vendor/tools"])
	}
}

func TestParseAheadBehind(t *testing.T) {
	tests := []struct {
		out           string
		ahead, behind int
	}{
		{"3\t3\n", 3, 3},
		{"0\t18\n", 0, 18},
		{"2\t0\n", 2, 0},
		{"0\t0\n", 0, 0},
		{"", 0, 0},
	}
	for _, tt := range tests {
		ahead, behind := parseAheadBehind(tt.out)
		if ahead != tt.ahead || behind != tt.behind {
			t.Errorf("parseAheadBehind(%q) = %d/%d, want %d/%d", tt.out, ahead, behind, tt.ahead, tt.behind)
		}
	}
}

// The exact case the spec warns about: this pin is ahead 3 and behind 3, and a one-way count would
// report a bare "3" that reads as a simple "behind 3".
func TestParseAheadBehind_DivergedPin(t *testing.T) {
	ahead, behind := parseAheadBehind("3\t3\n")
	if got := domain.DeriveVerdict(ahead, behind); got != domain.PinDiverged {
		t.Errorf("verdict = %q, want %q", got, domain.PinDiverged)
	}
}
