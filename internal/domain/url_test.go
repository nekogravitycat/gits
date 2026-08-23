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
