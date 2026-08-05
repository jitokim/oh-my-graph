// Package fence is the one mechanism this codebase uses whenever trusted code
// has to quote untrusted text into a prompt. It is two disciplines, not one: a
// marker the quoted text cannot forge, and a bound on how much of that text the
// prompt carries.
//
// A fence made of fixed markers is forgeable by the very text it fences — the
// fenced material can predict the marker and emit it, placing its own words
// outside the fence as far as the reading model can tell. Carrying a per-call
// random nonce in BOTH markers is what removes that prediction: the nonce is
// minted after the text is already fixed, so no material can contain it.
//
// Five call sites share Nonce: skillmap.go's inlined SKILL.md body (ADR 0012)
// and assess.go's engine-recorded material — node details, artifact excerpts
// and the previous cycle's `remaining` — which is raw model output by design
// (ADR 0011 §2), plus coordinator.go's continuation quote of that same
// `remaining` into the next cycle's planner prompt, repair.go's quote of the
// validator's refusals into a re-plan prompt, and retryfeedback.go's quote of a
// node's own rejected attempt into the prompt that retries it (ADR 0016). The
// refusals one is the least obvious and no less necessary: a refusal is an
// engine-authored sentence, but it interpolates model-authored fragments — a
// placeholder token, a node id — and at least one validator does so without
// escaping them, so the planner can place newlines and forged marker lines
// inside the text being quoted back.
//
// This is its own package rather than a file inside internal/coordinator
// because the newest caller is not a coordinator: internal/schedule quotes a
// failed attempt back to the node that produced it, and a second hand-rolled
// nonce minter is precisely the divergence a shared fence exists to prevent.
package fence

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// NonceBytes sizes a fence nonce: 3 random bytes render as 6 hex characters —
// entropy the fenced text cannot predict, which is all the fence needs.
const NonceBytes = 3

// Nonce mints one fence nonce, purpose naming the caller for the error.
// Failure is returned rather than swallowed: an unfenceable quote is not a
// zero-config degradation — it would silently weaken the fence's one property.
// What a caller does with the failure is the caller's call: the coordinator
// abandons the call it was building, because there is nothing to fall back on;
// the scheduler drops the quote and retries without it, because the unquoted
// prompt is a complete and valid retry. Neither may fence with fixed markers.
func Nonce(purpose string) (string, error) {
	buf := make([]byte, NonceBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mint %s fence nonce: %w", purpose, err)
	}
	return hex.EncodeToString(buf), nil
}

// ExcerptMarker marks the cut Excerpt makes in the middle of over-long
// material. It is exported so a test can assert that a cut was ANNOUNCED
// without transcribing the sentence that announces it.
const ExcerptMarker = "\n… (middle excerpted) …\n"

// Excerpt bounds s to roughly n bytes keeping head and tail — quoted material's
// opening context and its final result usually carry the content worth quoting,
// and a head-only cut would hide exactly the "PASS"/"FAIL" tail a check node
// prints last.
func Excerpt(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= len(ExcerptMarker) {
		return Truncate(s, n)
	}
	keep := n - len(ExcerptMarker)
	head := keep / 2
	tail := keep - head
	return s[:head] + ExcerptMarker + s[len(s)-tail:]
}

// Truncate shortens s to at most n bytes, marking the cut. It is the head-only
// bound, for material whose tail carries nothing (an error's echo of a reply)
// and for Excerpt's own fallback when the budget is smaller than its marker.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "… (truncated)"
}
