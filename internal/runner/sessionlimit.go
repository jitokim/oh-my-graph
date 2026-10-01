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
// (error_max_budget_usd). Claude has a SECOND limit condition with its own
// sentence: one model's allowance runs out while the account's session is
// fine, and the same is_error envelope then reads "You've reached your Fable
// limit. Switch to another model, or manage usage credits at …, to continue."
// (observed 2026-09-21 on run 20260921-071606.434336000-1, issue #283;
// realModelLimitMessage in sessionlimit_test.go carries it byte for byte).
// Both are a pause for the same reason — the CLI refused the work and will
// take it again later, or on another model. String matching is brittle by
// nature: a wording change in the CLI silently downgrades the limit back to an
// ordinary node failure. That degradation is deliberate and safe — a missed
// match means FAILED-not-paused, which `resume --retry-failed` still salvages
// — and keeping the patterns here, pinned by sessionlimit_test.go against the
// real messages, is what makes a wording change a one-line fix instead of a
// hunt.
//
// Codex reports its own limit as prose too (observed 2026-09-02 on run
// 20260901-171816.016378000-1; the message is in
// testdata/codex-usage-limit.jsonl byte for byte, recovered from that run's own
// records — codexLimitRecords in sessionlimit_test.go states the provenance):
// `codex exec --json` writes an
// `{"type":"error","message":"You've hit your usage limit. …"}` record the
// parser does not decode — abbreviated on THIS line for shape only; the fixture
// carries the sentence whole, which is what "byte for byte" above is about —
// then a `turn.failed` whose error.message repeats the whole
// sentence, reset clause included, and THAT is the only copy the engine sees
// (codex_protocol.go: turn.failed fills FailureCause). The typed shape cannot
// do the deciding here, because
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

// isLimitCause answers, for the claude protocol's OWN output, whether the
// captured cause is that runtime's subscription limit — either of Claude's two
// conditions, the account-wide session limit or one model's allowance. It
// is one half of the cliProtocol method CLIRunner.Run calls exactly once (ADR
// 0009: one matcher, one call site) — the classification asks the protocol
// rather than switching on a Runtime, so no code outside this file has to know
// which prose belongs to which CLI, or that Claude has two, and the scheduler
// downstream sees only NodeOutcome.SessionLimited.
func (claudeProtocol) isLimitCause(cause string) bool {
	return isSessionLimitCause(cause) || isClaudeModelLimitCause(cause)
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

// claudeModelLimitPattern recognizes Claude's OTHER limit sentence: one model's
// allowance is spent ("You've reached your Fable limit. Switch to another
// model, …"). Deliberately NOT folded into sessionLimitPattern either, and for
// the same reason the Codex pattern above gives, applied within one
// runtime: the account-wide session limit and one model's allowance are different
// sentences for different conditions, each pinned by its own narrow contract
// test (TestSessionLimitCause_DoesNotMatchOtherFailures,
// TestClaudeModelLimitCause_DoesNotMatchOtherFailures), so a rewording of one
// must not silently widen or narrow the other. isSessionLimitCause stays
// exactly as narrow as it was; only claudeProtocol.isLimitCause knows there
// are two.
//
// The model name (Fable, Opus, Sonnet, a versioned family name with a space or
// a dot in it) is a bounded wildcard, left out on purpose like Codex's plan
// name: it varies per invocation, and the bound keeps it from spanning a whole
// flattened report. The trailing "Switch to another model" clause is KEPT,
// against the every-extra-word rule, because it is what makes the sentence
// this CLI's and not anyone's: "reached your X limit" is ordinary English that
// a node's own tool output produces (`gh`'s quota, a rate limiter, a doc that
// quotes the wording), and on a non-zero exit that stderr tail becomes the
// FailureCause this pattern is asked about (cli.go). A lazier
// `reached your .{1,40}? limit` would pause a healthy run on a node that merely
// mentioned the limit and then failed for its own reason. The URL and the
// credits clause are left out: the URL carries a tracking parameter that will
// churn, and the credits wording is billing prose.
var claudeModelLimitPattern = regexp.MustCompile(`(?i)reached your .{1,40}? limit\W+switch to another model`)

// isClaudeModelLimitCause reports whether a NodeOutcome.FailureCause is one
// Claude model's allowance running out. An empty cause never matches.
func isClaudeModelLimitCause(cause string) bool {
	return claudeModelLimitPattern.MatchString(cause)
}

// isLimitCause is the codex half of the same method. A protocol answers only
// for the stream it decodes: codex's pattern is never asked about claude's
// output and vice versa, so a rewording on one runtime cannot widen or narrow
// the other. Adding a third runtime means implementing this method — a
// protocol that has no limit wording to match returns false and degrades the
// way ADR 0009 specifies (the node FAILs carrying the message, and
// `resume --retry-failed` salvages the run), which is that ADR's Scope section
// stated in code.
func (codexProtocol) isLimitCause(cause string) bool {
	return codexUsageLimitPattern.MatchString(cause)
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
