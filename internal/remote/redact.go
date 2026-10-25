package remote

import (
	"regexp"
	"strings"
)

// maxReasonBytes bounds an unavailable part's reason. git's and gh's
// diagnoses are one or two lines; the bound only keeps a chatty failure from
// bloating a snapshot that is rewritten after every node.
const maxReasonBytes = 500

var (
	// httpUserinfo is the whole userinfo of an http(s) URL: there the user
	// name alone is often a token (https://<token>@github.com/...).
	httpUserinfo = regexp.MustCompile(`(?i)\b(https?://)[^/\s@]+@`)
	// urlPassword is the password of any other scheme's userinfo; the user
	// name (git@ in ssh://git@host) is kept.
	urlPassword = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://[^/\s:@]*):[^/\s@]+@`)
)

// RedactURLCredentials replaces the credentials a URL may carry, wherever
// one appears in s.
func RedactURLCredentials(s string) string {
	s = httpUserinfo.ReplaceAllString(s, "${1}***@")
	return urlPassword.ReplaceAllString(s, "${1}:***@")
}

// SummarizeReason is the remote snapshot's reason hygiene (ADR 0046 §2.1),
// shared by every part that records a reason — the git parts
// worktree.TakeRemoteSnapshot reads and the open-PR list read here — so they
// cannot disagree on what is redacted: s becomes one bounded line with any
// credential a URL carries replaced.
func SummarizeReason(s string) string {
	s = RedactURLCredentials(strings.Join(strings.Fields(s), " "))
	if len(s) <= maxReasonBytes {
		return s
	}
	cut := maxReasonBytes
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + " …"
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
