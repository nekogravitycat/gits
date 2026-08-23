package git

import (
	"strings"
	"testing"

	"github.com/nekogravitycat/gits/internal/domain"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   domain.ErrCode
	}{
		{
			name:   "https credentials rejected",
			stderr: "remote: HTTP Basic: Access denied\nfatal: Authentication failed for 'https://host/a/b.git/'",
			want:   domain.ErrAuth,
		},
		{
			name:   "no credential helper, prompting disabled",
			stderr: "fatal: could not read Username for 'https://host': terminal prompts disabled",
			want:   domain.ErrAuth,
		},
		{
			name:   "ssh key rejected",
			stderr: "git@host: Permission denied (publickey).\nfatal: Could not read from remote repository.",
			want:   domain.ErrAuth,
		},
		{
			name:   "private repo looks like a missing one",
			stderr: "remote: Repository not found.\nfatal: repository 'https://host/a/b.git/' not found",
			want:   domain.ErrAuth,
		},
		{
			name:   "dns failure",
			stderr: "fatal: unable to access 'https://host/a/b.git/': Could not resolve host: host",
			want:   domain.ErrNetwork,
		},
		{
			name:   "connection refused",
			stderr: "ssh: connect to host host port 22: Connection refused",
			want:   domain.ErrNetwork,
		},
		{
			name:   "server error",
			stderr: "error: RPC failed; HTTP 503 Service Unavailable",
			want:   domain.ErrNetwork,
		},
		{
			name:   "pre-commit hook rejected the commit",
			stderr: "pre-commit hook failed (add --no-verify to bypass)",
			want:   domain.ErrHookFailed,
		},
		{
			name:   "not a repository",
			stderr: "fatal: not a git repository (or any of the parent directories): .git",
			want:   domain.ErrNotARepo,
		},
		{
			name:   "merge would clobber local work",
			stderr: "error: Your local changes to the following files would be overwritten by merge:",
			want:   domain.ErrDirty,
		},
		{
			name:   "fast-forward impossible",
			stderr: "fatal: Not possible to fast-forward, aborting.",
			want:   domain.ErrDiverged,
		},
		{
			name:   "push rejected as non-fast-forward",
			stderr: "! [rejected] main -> main (non-fast-forward)",
			want:   domain.ErrDiverged,
		},
		{
			name:   "branch has no upstream",
			stderr: "fatal: The current branch feature/x has no upstream branch.",
			want:   domain.ErrNoUpstream,
		},
		{
			name:   "unrecognised failure",
			stderr: "fatal: something nobody has seen before",
			want:   domain.ErrGit,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classify(tt.stderr); got != tt.want {
				t.Errorf("classify(%q) = %q, want %q", tt.stderr, got, tt.want)
			}
		})
	}
}

// The one classification that must not be wrong. git wraps auth failures in network-shaped
// prose ("unable to access ..."), and reading one as the other sends a caller into a retry loop
// that can never succeed (spec §6.6).
func TestClassify_AuthInsideNetworkShapedProse(t *testing.T) {
	stderr := "fatal: unable to access 'https://host/a/b.git/': The requested URL returned error: 403 Forbidden"
	got := classify(stderr)
	if got != domain.ErrAuth {
		t.Fatalf("classify() = %q, want %q", got, domain.ErrAuth)
	}
	if got.Retryable() {
		t.Error("an auth failure must never be reported as retryable")
	}
}

func TestClassify_NetworkIsRetryableAndAuthIsNot(t *testing.T) {
	network := classify("fatal: unable to access 'https://host/a/b.git/': Could not resolve host: host")
	if !network.Retryable() {
		t.Errorf("%s should be retryable", network)
	}
	auth := classify("fatal: Authentication failed for 'https://host/a/b.git/'")
	if auth.Retryable() {
		t.Errorf("%s must not be retryable", auth)
	}
}

func TestHardenedEnv(t *testing.T) {
	// Isolate from whatever the ambient environment has: some git clients (e.g. GitKraken) set
	// GIT_SSH_COMMAND themselves before spawning hooks, which would otherwise make this test's
	// outcome depend on which tool ran it rather than on hardenedEnv() itself.
	t.Setenv("GIT_SSH_COMMAND", "")

	env := hardenedEnv()
	// Each of these is what stops git opening its own prompt and hanging a non-interactive run.
	want := []string{
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		"GIT_OPTIONAL_LOCKS=0",
	}
	for _, w := range want {
		if !contains(env, w) {
			t.Errorf("hardened environment is missing %q", w)
		}
	}
	if !containsPrefix(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes") {
		t.Error("hardened environment is missing a batch-mode GIT_SSH_COMMAND")
	}
}

func TestHardenedEnv_RespectsUserSSHCommand(t *testing.T) {
	t.Setenv("GIT_SSH_COMMAND", "ssh -i /custom/key")
	env := hardenedEnv()
	// Overriding a deliberate GIT_SSH_COMMAND would break setups that depend on it.
	if containsPrefix(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes") {
		t.Error("a user-configured GIT_SSH_COMMAND must not be overridden")
	}
}

func TestBaseArgs(t *testing.T) {
	joined := strings.Join(baseArgs(), " ")
	for _, want := range []string{"core.pager=cat", "color.ui=false"} {
		if !strings.Contains(joined, want) {
			t.Errorf("base args are missing %q; output would not be parseable", want)
		}
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func containsPrefix(list []string, prefix string) bool {
	for _, s := range list {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}
