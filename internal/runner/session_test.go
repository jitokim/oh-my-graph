package runner

import (
	"regexp"
	"testing"
)

// uuidV4Pattern is the canonical lowercase-hex form claude's --session-id
// requires ("must be a valid UUID"), with the version nibble pinned to 4 and
// the variant nibble to RFC 4122 (8/9/a/b).
var uuidV4Pattern = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// TestNewSessionID_IsCanonicalUUIDv4 pins the format contract: every minted id
// must be one claude will accept, so a malformed id can never turn a node
// start into a CLI usage error.
func TestNewSessionID_IsCanonicalUUIDv4(t *testing.T) {
	for i := 0; i < 64; i++ {
		id := NewSessionID()
		if !uuidV4Pattern.MatchString(id) {
			t.Fatalf("NewSessionID() = %q, not a canonical v4 UUID", id)
		}
	}
}

// TestNewSessionID_IsUnique proves consecutive draws differ — the property a
// retried attempt depends on, since its failed predecessor's id already names
// a real session.
func TestNewSessionID_IsUnique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1024; i++ {
		id := NewSessionID()
		if seen[id] {
			t.Fatalf("NewSessionID() repeated %q", id)
		}
		seen[id] = true
	}
}
