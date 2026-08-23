package domain_test

import (
	"testing"

	"github.com/nekogravitycat/gits/internal/domain"
)

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"https with .git", "https://git.example.com/game/shared-proto.git", "git.example.com/game/shared-proto"},
		{"https without .git", "https://git.example.com/game/shared-proto", "git.example.com/game/shared-proto"},
		{"ssh scheme with port", "ssh://git@git.example.com:24/game/shared-proto.git", "git.example.com/game/shared-proto"},
		{"scp-like", "git@git.example.com:game/shared-proto.git", "git.example.com/game/shared-proto"},
		{"scp-like without user", "git.example.com:game/shared-proto", "git.example.com/game/shared-proto"},
		{"host case folded", "https://GIT.Example.COM/a/b.git", "git.example.com/a/b"},
		{"path case preserved", "https://host/Game/SharedProto.git", "host/Game/SharedProto"},
		{"trailing slash", "https://host/a/b/", "host/a/b"},
		{"credentials in url", "https://user:pass@host/a/b.git", "host/a/b"},
		{"nested path", "https://host/game/tools/drawer-tool.git", "host/game/tools/drawer-tool"},
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"host only", "https://host", "host"},
		{"git protocol", "git://host/a/b.git", "host/a/b"},
		{"at sign inside path is not credentials", "https://host/a/b@c", "host/a/b@c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := domain.NormalizeURL(tt.in); got != tt.want {
				t.Errorf("NormalizeURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// The three spellings below are the ones actually observed for a single dependency across one
// workspace. Canonical resolution is impossible unless they collapse to the same identity.
func TestNormalizeURL_ThreeSpellingsOfOneRepo(t *testing.T) {
	spellings := []string{
		"ssh://git@git.example.com:24/game/shared-proto.git",
		"https://git.example.com/game/shared-proto.git",
		"https://git.example.com/game/shared-proto",
	}
	first := domain.NormalizeURL(spellings[0])
	for _, s := range spellings[1:] {
		if got := domain.NormalizeURL(s); got != first {
			t.Errorf("NormalizeURL(%q) = %q, want %q", s, got, first)
		}
	}
}

func TestSameRepoURL(t *testing.T) {
	if !domain.SameRepoURL("git@host:a/b.git", "https://host/a/b") {
		t.Error("expected the two spellings to match")
	}
	if domain.SameRepoURL("https://host/a/b", "https://host/a/c") {
		t.Error("different repos must not match")
	}
	// Two empty URLs are unknown, not equal: treating them as the same repo would collapse every
	// URL-less entry into one bogus dependency group.
	if domain.SameRepoURL("", "") {
		t.Error("empty URLs must not be reported as the same repo")
	}
}

// A local path on Windows must not be read as scp syntax. "C:/repos/proto.git" parsed that way
// becomes host "C" with path "/repos/proto.git", which matches nothing and reads as nonsense.
func TestNormalizeURL_WindowsLocalPath(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{`C:\repos\proto.git`, "c:/repos/proto"},
		{"C:/repos/proto.git", "c:/repos/proto"},
		{"c:/repos/proto", "c:/repos/proto"},
		{`D:\work\a\b.git`, "d:/work/a/b"},
	}
	for _, tt := range tests {
		if got := domain.NormalizeURL(tt.in); got != tt.want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// The two spellings of one local path are the same repository.
func TestNormalizeURL_WindowsSeparatorsAgree(t *testing.T) {
	if !domain.SameRepoURL(`C:\repos\proto.git`, "C:/repos/proto") {
		t.Error("backslash and forward-slash spellings of one path should match")
	}
}

// A host:path remote still parses as a remote, not as a drive letter.
func TestNormalizeURL_ScpSyntaxIsNotMistakenForADrive(t *testing.T) {
	if got := domain.NormalizeURL("host:repos/proto.git"); got != "host/repos/proto" {
		t.Errorf("NormalizeURL() = %q, want host/repos/proto", got)
	}
}

func TestDisplayName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"https://git.example.com/game/shared-proto.git", "shared-proto"},
		{"ssh://git@host:24/a/b.git", "b"},
		{`C:\repos\proto.git`, "proto"},
		{"https://host", "host"},
	}
	for _, tt := range tests {
		if got := domain.DisplayName(tt.in); got != tt.want {
			t.Errorf("DisplayName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
