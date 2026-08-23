package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/nekogravitycat/gits/internal/domain"
)

func manifest(repos ...domain.Repo) *domain.Manifest {
	return &domain.Manifest{
		Version:  domain.SchemaVersion,
		Defaults: domain.Defaults{Remote: "origin", Branch: "main"},
		Repos:    repos,
	}
}

func TestManifest_Validate_Accepts(t *testing.T) {
	m := manifest(
		domain.Repo{Name: "workspace", Path: ".", URL: "https://host/w.git", Line: 5},
		domain.Repo{Name: "drawer", URL: "https://host/d.git", Line: 9},
		domain.Repo{Name: "nested", Path: "tools/stack", URL: "https://host/s.git", Line: 13},
	)
	if err := m.Validate("gits.yaml"); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestManifest_Validate_Rejects(t *testing.T) {
	tests := []struct {
		name     string
		m        *domain.Manifest
		wantLine int
		wantMsg  string
	}{
		{
			name:    "missing version",
			m:       &domain.Manifest{Repos: []domain.Repo{{Name: "a", URL: "u"}}},
			wantMsg: "version",
		},
		{
			name:    "future version is refused, not guessed at",
			m:       &domain.Manifest{Version: domain.SchemaVersion + 1},
			wantMsg: "upgrade gits",
		},
		{
			name:     "missing name",
			m:        manifest(domain.Repo{URL: "https://host/a.git", Line: 4}),
			wantLine: 4,
			wantMsg:  "name",
		},
		{
			name:     "missing url",
			m:        manifest(domain.Repo{Name: "a", Line: 7}),
			wantLine: 7,
			wantMsg:  "url",
		},
		{
			name: "duplicate name",
			m: manifest(
				domain.Repo{Name: "a", URL: "https://host/a.git", Line: 3},
				domain.Repo{Name: "a", URL: "https://host/b.git", Path: "b", Line: 8},
			),
			wantLine: 8,
			wantMsg:  "duplicate repo name",
		},
		{
			name: "duplicate resolved path",
			m: manifest(
				domain.Repo{Name: "a", Path: "shared", URL: "https://host/a.git", Line: 3},
				domain.Repo{Name: "b", Path: "shared", URL: "https://host/b.git", Line: 8},
			),
			wantLine: 8,
			wantMsg:  "already used",
		},
		{
			name:     "path escapes the workspace",
			m:        manifest(domain.Repo{Name: "a", Path: "../outside", URL: "https://host/a.git", Line: 6}),
			wantLine: 6,
			wantMsg:  "escapes the workspace",
		},
		{
			name:     "path climbs out through a subdirectory",
			m:        manifest(domain.Repo{Name: "a", Path: "sub/../../outside", URL: "https://host/a.git", Line: 6}),
			wantLine: 6,
			wantMsg:  "escapes the workspace",
		},
		{
			name:     "absolute posix path",
			m:        manifest(domain.Repo{Name: "a", Path: "/etc/thing", URL: "https://host/a.git", Line: 6}),
			wantLine: 6,
			wantMsg:  "must be relative",
		},
		{
			name:     "absolute windows path is rejected on every OS",
			m:        manifest(domain.Repo{Name: "a", Path: `C:\repos\thing`, URL: "https://host/a.git", Line: 6}),
			wantLine: 6,
			wantMsg:  "must be relative",
		},
		{
			name: "two root repos",
			m: manifest(
				domain.Repo{Name: "a", Path: ".", URL: "https://host/a.git", Line: 3},
				domain.Repo{Name: "b", Path: ".", URL: "https://host/b.git", Line: 9},
			),
			wantLine: 9,
			wantMsg:  "at most one root repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.m.Validate("gits.yaml")
			if err == nil {
				t.Fatal("Validate() = nil, want an error")
			}
			var me *domain.ManifestError
			if !errors.As(err, &me) {
				t.Fatalf("Validate() = %T, want *domain.ManifestError", err)
			}
			if me.Code() != domain.ErrManifest {
				t.Errorf("Code() = %q, want %q", me.Code(), domain.ErrManifest)
			}
			if tt.wantLine != 0 && me.Line != tt.wantLine {
				t.Errorf("Line = %d, want %d", me.Line, tt.wantLine)
			}
			if !strings.Contains(me.Msg, tt.wantMsg) {
				t.Errorf("Msg = %q, want it to contain %q", me.Msg, tt.wantMsg)
			}
		})
	}
}

func TestRepo_EffectivePath(t *testing.T) {
	tests := []struct {
		repo domain.Repo
		want string
	}{
		{domain.Repo{Name: "drawer"}, "drawer"},
		{domain.Repo{Name: "root", Path: "."}, "."},
		{domain.Repo{Name: "s", Path: "tools/stack"}, "tools/stack"},
		{domain.Repo{Name: "s", Path: `tools\stack`}, "tools/stack"}, // written on Windows, read on Linux
		{domain.Repo{Name: "s", Path: "tools/./stack/"}, "tools/stack"},
	}
	for _, tt := range tests {
		if got := tt.repo.EffectivePath(); got != tt.want {
			t.Errorf("EffectivePath(%+v) = %q, want %q", tt.repo, got, tt.want)
		}
	}
}

func TestManifest_EffectiveBranchAndRemote(t *testing.T) {
	m := manifest(
		domain.Repo{Name: "a", URL: "u"},
		domain.Repo{Name: "b", URL: "u", Path: "b", Branch: "develop", Remote: "upstream"},
	)
	a, _ := m.Find("a")
	b, _ := m.Find("b")

	if got := m.EffectiveBranch(a); got != "main" {
		t.Errorf("inherited branch = %q, want main", got)
	}
	if got := m.EffectiveBranch(b); got != "develop" {
		t.Errorf("overridden branch = %q, want develop", got)
	}
	if got := m.EffectiveRemote(a); got != "origin" {
		t.Errorf("inherited remote = %q, want origin", got)
	}
	if got := m.EffectiveRemote(b); got != "upstream" {
		t.Errorf("overridden remote = %q, want upstream", got)
	}
}

func TestManifest_EffectiveBranch_FallsBackWhenDefaultsAreEmpty(t *testing.T) {
	m := &domain.Manifest{Version: 1, Repos: []domain.Repo{{Name: "a", URL: "u"}}}
	a, _ := m.Find("a")
	if got := m.EffectiveBranch(a); got != domain.DefaultBranch {
		t.Errorf("branch = %q, want %q", got, domain.DefaultBranch)
	}
	if got := m.EffectiveRemote(a); got != domain.DefaultRemote {
		t.Errorf("remote = %q, want %q", got, domain.DefaultRemote)
	}
}

func TestManifest_Root(t *testing.T) {
	m := manifest(
		domain.Repo{Name: "drawer", URL: "u"},
		domain.Repo{Name: "workspace", Path: ".", URL: "u2"},
	)
	root, ok := m.Root()
	if !ok || root.Name != "workspace" {
		t.Errorf("Root() = %+v, %v; want the workspace entry", root, ok)
	}

	noRoot := manifest(domain.Repo{Name: "drawer", URL: "u"})
	if _, ok := noRoot.Root(); ok {
		t.Error("Root() found a root repo where the manifest declares none")
	}
}

func TestManifest_Groups(t *testing.T) {
	m := manifest(
		domain.Repo{Name: "a", URL: "u", Groups: []string{"platform", "proto"}},
		domain.Repo{Name: "b", URL: "u", Path: "b", Groups: []string{"game", "platform"}},
	)
	got := m.Groups()
	want := []string{"platform", "proto", "game"}
	if len(got) != len(want) {
		t.Fatalf("Groups() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Groups() = %v, want %v", got, want)
		}
	}
}
