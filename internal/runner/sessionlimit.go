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
//
// Codex reports its own limit as prose too (observed 2026-09-02, recorded byte
// for byte in testdata/codex-usage-limit.jsonl): `codex exec --json` writes an
// `{"type":"error","message":"You've hit your usage limit…"}` record the parser
// does not decode, then a `turn.failed` whose error.message repeats it, and
// THAT is the only copy the engine sees (codex_protocol.go: turn.failed fills
// FailureCause). The typed shape cannot do the deciding here, because
// turn.failed is Codex's one terminal-failure record — "model unavailable"
// (codex_protocol_test.go) and a stubbed refusal
// (cmd/oh-my-graph/loadeduserconfig_cli_test.go) arrive in exactly that shape,
// carrying nothing but a different sentence. So both runtimes are matched the
// same way, for the same reason, in this one file.

// sessionLimitPattern recognizes the limit message inside a captured failure
// cause. Substring, not anchored: the cause may carry the CLI's prefix or a
// flattened multi-line report around the message itself.
var sessionLimitPattern = regexp.MustCompile(`(?i)hit your session limit`)

// isSessionLimitCause reports whether a NodeOutcome.FailureCause is the
// subscription session limit. An empty cause (a clean run) never matches.
func isSessionLimitCause(cause string) bool {
	return sessionLimitPattern.MatchString(cause)
}

// codexUsageLimitPattern recognizes Codex's wording for the same condition.
// Deliberately NOT folded into an alternation with sessionLimitPattern: that
// pattern is a narrow contract of its own
// (TestSessionLimitCause_DoesNotMatchOtherFailures), and a rewording on one
// runtime must not silently widen what the other matches. The plan name
// ("Upgrade to Plus") and the reset date are left out on purpose — both vary
// per account, and every extra word is another way a reworded message stops
// matching.
var codexUsageLimitPattern = regexp.MustCompile(`(?i)hit your usage limit`)

// isLimitCause reports whether a NodeOutcome.FailureCause is the selected
// runtime's subscription limit. This is the ONE runtime branch in the
// classification, and it lives here rather than at the call site so a reader of
// this file sees the whole policy without hunting cli.go for a gate —
// CLIRunner.Run still calls it exactly once (ADR 0009: one matcher, one call
// site). A runtime this switch does not name owes no limit signal and gets
// none, which is ADR 0009's Scope section stated in code.
func isLimitCause(rt Runtime, cause string) bool {
	switch rt {
	case RuntimeClaude:
		return isSessionLimitCause(cause)
	case RuntimeCodex:
		return codexUsageLimitPattern.MatchString(cause)
	default:
		return false
	}
}

// sessionLimitResetPattern captures the human-readable reset time the limit
// message carries ("resets 5:20pm", "will reset at 4pm", and Codex's "try again
// at Sep 13th, 2026 10:04 PM"), stopping at the separators the CLI decorates
// with. Best-effort by design — see SessionLimitReset.
var sessionLimitResetPattern = regexp.MustCompile(`(?i)\b(?:resets?|try again at)\s+(?:at\s+)?([^·∙(\n]+)`)

// SessionLimitReset extracts the reset time from a session-limit cause, as the
// prose the CLI printed ("5:20pm") — never parsed into a real clock time,
// because the CLI's wording, timezone and format are its own. Codex's
// "Sep 13th, 2026 10:04 PM" looks more machine-readable and is not: it names no
// timezone, and a wrongly parsed instant is worse than none (ADR 0009 refused
// to sleep on "5:20pm" for the weaker version of this reason). "" means the
// cause carried no recognizable reset hint; the caller prints its resume hint
// without a time rather than inventing one.
func SessionLimitReset(cause string) string {
	m := sessionLimitResetPattern.FindStringSubmatch(cause)
	if m == nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(m[1]), ".")
}
