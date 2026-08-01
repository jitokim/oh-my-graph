package runner

import (
	"crypto/rand"
	"fmt"
)

// NewSessionID mints the pre-assigned session id for one claude invocation: a
// random (version 4, variant 1) UUID in the canonical lowercase-hex form,
// because `--session-id` "must be a valid UUID" (claude --help, verified
// 2026-08-02). It lives in this package — not in the scheduler that calls it —
// because the required FORMAT is this seam's knowledge: it is claude's
// contract, not a scheduling policy.
//
// Random rather than derived (from the run id or node id) on purpose: a
// retried attempt needs an id distinct from the failed attempt's, whose id
// already names a real session on disk, and a derived scheme would have to
// re-invent uniqueness that v4 randomness simply is.
func NewSessionID() string {
	var b [16]byte
	// crypto/rand.Read never returns an error (guaranteed since Go 1.24; it
	// panics internally on an irrecoverable platform failure instead).
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 1 (RFC 4122)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
