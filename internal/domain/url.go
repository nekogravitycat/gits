package domain

import "strings"

// NormalizeURL reduces a git remote URL to a comparable identity: lowercase host plus repo path,
// with scheme, credentials, port and trailing ".git" removed.
//
// CRITICAL: this is required for canonical resolution (spec §7.11), not defensive -- the same
// submodule is routinely referenced as ssh/https/with-or-without-.git and must resolve to one
// repository; matching on submodule *path* is not an option (many dependents reuse one path name).
// An unrecognisable URL is trimmed and folded, not rejected, so two identical unparseable strings
// still match.
func NormalizeURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}

	// CRITICAL: check Windows local paths first -- "C:/repos/proto.git" otherwise parses as scp
	// syntax (host "C") and produces an identity that matches nothing.
	if isWindowsPath(s) {
		p := strings.ReplaceAll(s, `\`, "/")
		p = strings.ToLower(p[:2]) + strings.TrimSuffix(strings.TrimRight(p[2:], "/"), ".git")
		return strings.TrimRight(p, "/")
	}

	if idx := strings.Index(s, "://"); idx >= 0 {
		s = s[idx+3:]
		s = stripCredentials(s)
	} else {
		// scp-like syntax [user@]host:path has no scheme; handled separately so "git@host:a/b.git"
		// is not read as scheme "git@host".
		s = stripCredentials(s)
		if colon := strings.Index(s, ":"); colon >= 0 {
			s = s[:colon] + "/" + s[colon+1:]
		}
	}

	host, repoPath, _ := strings.Cut(s, "/")
	// Strip the port: same repo on :24 and on the default port.
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

// stripCredentials removes a leading "user@" or "user:password@" from an authority. An "@" after
// the first "/" belongs to the path and is left alone.
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

// isWindowsPath reports whether s looks like a drive-letter path ("C:/repos/x" or `C:\repos\x`).
// The required separator after the colon distinguishes it from scp syntax ("host:repos/x").
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

// DisplayName derives a short label from a repo URL: its last path segment without ".git". Used
// only where no manifest entry supplies a real name; the full URL is reported alongside since a
// basename is not unique.
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
