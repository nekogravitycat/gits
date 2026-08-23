package domain

import "strings"

// NormalizeURL reduces a git remote URL to a comparable identity: lowercase host plus repository
// path, with scheme, credentials, port and a trailing ".git" removed.
//
// This is a hard requirement rather than defensive coding (spec §7.11). The same submodule is
// routinely referenced three different ways across a workspace --
// "ssh://git@host:24/a/b.git", "https://host/a/b.git" and "https://host/a/b" -- and canonical
// resolution has to see them as one repository. Matching on the submodule's *path* is not an
// option either: eight of nine dependents call the same submodule "proto".
//
// An unrecognisable URL is returned trimmed and folded rather than rejected: two identical
// unparseable strings should still match each other.
func NormalizeURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}

	// A local path on Windows is checked first, because "C:/repos/proto.git" otherwise looks
	// exactly like scp syntax and would be read as host "C" with path "/repos/proto.git" --
	// producing an identity that matches nothing and reads as nonsense in a report.
	if isWindowsPath(s) {
		p := strings.ReplaceAll(s, `\`, "/")
		p = strings.ToLower(p[:2]) + strings.TrimSuffix(strings.TrimRight(p[2:], "/"), ".git")
		return strings.TrimRight(p, "/")
	}

	if idx := strings.Index(s, "://"); idx >= 0 {
		s = s[idx+3:]
		s = stripCredentials(s)
	} else {
		// scp-like syntax has no scheme: [user@]host:path. It is handled separately so that
		// "git@host:a/b.git" is not mistaken for a URL whose scheme is "git@host".
		s = stripCredentials(s)
		if colon := strings.Index(s, ":"); colon >= 0 {
			s = s[:colon] + "/" + s[colon+1:]
		}
	}

	host, repoPath, _ := strings.Cut(s, "/")
	// Strip the port: a repo served on :24 and on the default port is the same repo.
	if colon := strings.LastIndex(host, ":"); colon >= 0 {
		host = host[:colon]
	}

	repoPath = strings.Trim(repoPath, "/")
	repoPath = strings.TrimSuffix(repoPath, ".git")
	repoPath = strings.Trim(repoPath, "/")

	// Paths are case-sensitive on most forges; only the host is safe to fold.
	host = strings.ToLower(host)
	if repoPath == "" {
		return host
	}
	return host + "/" + repoPath
}

// stripCredentials removes a leading "user@" or "user:password@" from an authority.
// An "@" that appears after the first "/" belongs to the path and is left alone.
func stripCredentials(s string) string {
	slash := strings.Index(s, "/")
	at := strings.Index(s, "@")
	if at < 0 {
		return s
	}
	if slash >= 0 && at > slash {
		return s
	}
	return s[at+1:]
}

// SameRepoURL reports whether two remote URLs identify the same repository.
func SameRepoURL(a, b string) bool {
	na, nb := NormalizeURL(a), NormalizeURL(b)
	return na != "" && na == nb
}

// isWindowsPath reports whether s looks like a drive-letter path such as "C:/repos/x" or
// `C:\repos\x`.
//
// The check requires a separator after the colon, which is what distinguishes it from scp syntax:
// "C:/repos/x" is a path, while "host:repos/x" is a remote.
func isWindowsPath(s string) bool {
	if len(s) < 3 || s[1] != ':' {
		return false
	}
	c := s[0]
	if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
		return false
	}
	return s[2] == '/' || s[2] == '\\'
}

// DisplayName derives a short, human-readable label for a repository URL: its last path segment
// without the ".git" suffix.
//
// It is used only where no manifest entry exists to supply a real name -- a dependency the
// workspace does not contain. The full URL is always reported alongside, since a basename alone is
// not unique.
func DisplayName(rawURL string) string {
	normalized := NormalizeURL(rawURL)
	if normalized == "" {
		return rawURL
	}
	if idx := strings.LastIndex(normalized, "/"); idx >= 0 && idx+1 < len(normalized) {
		return normalized[idx+1:]
	}
	return normalized
}
