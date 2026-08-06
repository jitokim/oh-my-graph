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
// The BOUND half has one caller that is not quoting into a prompt at all:
// internal/handoff cuts a failed node's reply down to what a run directory may
// hold. It shares the cut (HeadAndTail) rather than hand-rolling a second one,
// because the discipline is the same wherever text is cut — keep both ends,
// land the seams on whole runes — while the marker that announces the cut
// belongs to the caller, which is the one that knows what to say about it.
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
	"unicode/utf8"
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

// Excerpt bounds s to at most n bytes keeping head and tail — quoted material's
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
	head, tail := HeadAndTail(s, n-len(ExcerptMarker))
	return head + ExcerptMarker + tail
}

// HeadAndTail is the cut itself: the first and last pieces of s, keep bytes of
// it between them at most, for a caller to join with whatever marker it wants
// in the gap. Excerpt is that caller with a fixed marker; a caller whose marker
// STATES how much was dropped is why the pieces are returned instead of a
// joined string — only what was actually cut can say how much that was, and a
// figure derived from the budget instead of from the cut is an under-report in
// exactly the sentence whose job is to prevent one.
//
// The seams land on whole runes. An arbitrary byte offset lands inside a
// multi-byte rune the moment the material is not ASCII, and half a rune is
// invalid UTF-8 — mojibake at precisely the seam a reader is looking at, in
// text this package exists to hand to a model. Trimming only ever shortens, so
// the keep bound still holds. Callers pass keep < len(s); a keep that is not
// smaller is returned whole in the head.
func HeadAndTail(s string, keep int) (head, tail string) {
	if keep >= len(s) {
		return s, ""
	}
	if keep <= 0 {
		return "", ""
	}
	n := keep / 2
	return trimPartialRuneSuffix(s[:n]), trimPartialRunePrefix(s[len(s)-(keep-n):])
}

// TruncateMarker announces the cut Truncate makes at the end of over-long
// material. Like ExcerptMarker it is exported so a test can assert that a cut
// was ANNOUNCED without transcribing the sentence that announces it.
const TruncateMarker = "… (truncated)"

// Truncate shortens s to at most n bytes, marking the cut. It is the head-only
// bound, for material whose tail carries nothing (an error's echo of a reply)
// and for Excerpt's own fallback when the budget is smaller than its marker.
//
// The marker is spent OUT OF n rather than added to it. n is a budget callers
// subtract the result's length from — assessMaterial's per-run detail cap does
// exactly that — so a return that overspends by the marker's length is an
// overspend the caller cannot see, on every cut, and it is the same n both
// Excerpt and this function promise to stay inside. A budget too small to hold
// the marker buys material only: a cut that cannot afford to announce itself is
// still a cut that has to fit.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 0 {
		return ""
	}
	if n < len(TruncateMarker) {
		return trimPartialRuneSuffix(s[:n])
	}
	return trimPartialRuneSuffix(s[:n-len(TruncateMarker)]) + TruncateMarker
}

// trimPartialRuneSuffix drops the fragment of a multi-byte rune a cut leaves at
// the end of a piece. At most utf8.UTFMax-1 bytes can be such a fragment, which
// is also the bound on what material that genuinely ends in invalid bytes can
// lose here.
func trimPartialRuneSuffix(s string) string {
	for i := 0; i < utf8.UTFMax-1 && len(s) > 0; i++ {
		if r, size := utf8.DecodeLastRuneInString(s); r != utf8.RuneError || size > 1 {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}

// trimPartialRunePrefix is the mirror: a continuation byte at the start of a
// piece is the remainder of a rune whose leading byte the cut dropped.
func trimPartialRunePrefix(s string) string {
	for i := 0; i < utf8.UTFMax-1 && len(s) > 0 && !utf8.RuneStart(s[0]); i++ {
		s = s[1:]
	}
	return s
}
