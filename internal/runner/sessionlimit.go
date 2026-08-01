package runner

import (
	"regexp"
	"strings"
)

// This file is the ONE place oh-my-graph knows what a subscription session
// limit looks like (ADR 0009). The claude CLI reports the limit only as prose
// — an error envelope whose result reads "You've hit your session limit ·
// resets 5:20pm" (HTTP 429 underneath; observed on claude 2.1.220) — with no
// structured subtype the way a --max-budget-usd abort has
// (error_max_budget_usd). String matching is brittle by nature: a wording
// change in the CLI silently downgrades the limit back to an ordinary node
// failure. That degradation is deliberate and safe — a missed match means
// FAILED-not-paused, which `resume --retry-failed` still salvages — and
// keeping the pattern here, pinned by sessionlimit_test.go against the real
// message, is what makes a wording change a one-line fix instead of a hunt.

// sessionLimitPattern recognizes the limit message inside a captured failure
// cause. Substring, not anchored: the cause may carry the CLI's prefix or a
// flattened multi-line report around the message itself.
var sessionLimitPattern = regexp.MustCompile(`(?i)hit your session limit`)

// isSessionLimitCause reports whether a NodeOutcome.FailureCause is the
// subscription session limit. An empty cause (a clean run) never matches.
func isSessionLimitCause(cause string) bool {
	return sessionLimitPattern.MatchString(cause)
}

// sessionLimitResetPattern captures the human-readable reset time the limit
// message carries ("resets 5:20pm", "will reset at 4pm"), stopping at the
// separators the CLI decorates with. Best-effort by design — see
// SessionLimitReset.
var sessionLimitResetPattern = regexp.MustCompile(`(?i)\bresets?\s+(?:at\s+)?([^·∙(\n]+)`)

// SessionLimitReset extracts the reset time from a session-limit cause, as the
// prose the CLI printed ("5:20pm") — never parsed into a real clock time,
// because the CLI's wording, timezone and format are its own. "" means the
// cause carried no recognizable reset hint; the caller prints its resume hint
// without a time rather than inventing one.
func SessionLimitReset(cause string) string {
	m := sessionLimitResetPattern.FindStringSubmatch(cause)
	if m == nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(m[1]), ".")
}
