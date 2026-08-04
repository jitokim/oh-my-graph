// Data fences: the one mechanism this package uses whenever trusted code has
// to quote untrusted text into a prompt. A fence made of fixed markers is
// forgeable by the very text it fences — the fenced material can predict the
// marker and emit it, placing its own words outside the fence as far as the
// reading model can tell. Carrying a per-call random nonce in BOTH markers is
// what removes that prediction: the nonce is minted after the text is already
// fixed, so no material can contain it.
//
// Three call sites share this: skillmap.go's inlined SKILL.md body (ADR 0012)
// and assess.go's engine-recorded material — node details, artifact excerpts
// and the previous cycle's `remaining` — which is raw model output by design
// (ADR 0011 §2), plus coordinator.go's continuation quote of that same
// `remaining` into the next cycle's planner prompt.

package coordinator

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// fenceNonceBytes sizes a fence nonce: 3 random bytes render as 6 hex
// characters — entropy the fenced text cannot predict, which is all the fence
// needs.
const fenceNonceBytes = 3

// fenceNonce mints one fence nonce, purpose naming the caller for the error.
// Failure is returned rather than swallowed: an unfenceable quote is not a
// zero-config degradation — it would silently weaken the fence's one property.
func fenceNonce(purpose string) (string, error) {
	buf := make([]byte, fenceNonceBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mint %s fence nonce: %w", purpose, err)
	}
	return hex.EncodeToString(buf), nil
}
