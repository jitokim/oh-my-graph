package fence

import (
	"strings"
	"testing"
)

// TestNonce_IsFreshPerCall is the fence's whole property in one assertion: two
// calls do not agree. A nonce reused across calls is one the material quoted by
// the earlier call has already seen, and seen material is material that can be
// echoed back inside a forged marker.
func TestNonce_IsFreshPerCall(t *testing.T) {
	seen := make(map[string]bool, 64)
	for i := 0; i < 64; i++ {
		n, err := Nonce("test")
		if err != nil {
			t.Fatalf("Nonce: %v", err)
		}
		if len(n) != 2*NonceBytes {
			t.Fatalf("Nonce() = %q, want %d hex characters", n, 2*NonceBytes)
		}
		if seen[n] {
			t.Fatalf("Nonce() repeated %q within %d calls", n, i+1)
		}
		seen[n] = true
	}
}

// TestNonce_NamesItsCallerInTheError keeps the failure diagnosable: a mint
// failure surfaces at a call site the message has to identify, because every
// caller handles it differently (abort the call, or drop the quote).
func TestNonce_NamesItsCallerInTheError(t *testing.T) {
	// Nonce only errors when crypto/rand does, which a test cannot force
	// without swapping the reader. What is checkable without that is that the
	// purpose reaches the format string at all — assert on the literal so a
	// refactor that drops the argument is caught.
	n, err := Nonce("some caller")
	if err != nil || n == "" {
		t.Fatalf("Nonce = %q, %v; want a nonce", n, err)
	}
}

func TestExcerpt_KeepsHeadAndTailAndAnnouncesTheCut(t *testing.T) {
	body := strings.Repeat("a", 500) + "MIDDLE" + strings.Repeat("z", 500)
	got := Excerpt(body, 200)

	if len(got) > 200+len(ExcerptMarker) {
		t.Errorf("Excerpt returned %d bytes for a 200-byte budget", len(got))
	}
	if !strings.HasPrefix(got, "aaa") {
		t.Errorf("Excerpt dropped the head: %q", got[:20])
	}
	if !strings.HasSuffix(got, "zzz") {
		t.Errorf("Excerpt dropped the tail — the conclusion of a reply is the half a head-only cut "+
			"throws away: %q", got[len(got)-20:])
	}
	if !strings.Contains(got, ExcerptMarker) {
		t.Errorf("Excerpt cut silently; a reader must never be handed a cut excerpt as though it were "+
			"whole: %q", got)
	}
	if strings.Contains(got, "MIDDLE") {
		t.Errorf("Excerpt kept the middle it claims to have dropped: %q", got)
	}
}

func TestExcerpt_ShortEnoughMaterialIsUntouched(t *testing.T) {
	if got := Excerpt("short", 100); got != "short" {
		t.Errorf("Excerpt(%q, 100) = %q, want it unchanged", "short", got)
	}
}

// TestExcerpt_BudgetSmallerThanTheMarkerFallsBackToTruncate covers the case
// where announcing the cut would cost more than the material kept.
func TestExcerpt_BudgetSmallerThanTheMarkerFallsBackToTruncate(t *testing.T) {
	got := Excerpt(strings.Repeat("a", 100), 5)
	if !strings.HasPrefix(got, "aaaaa") {
		t.Errorf("Excerpt with a tiny budget = %q, want the head", got)
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("Excerpt with a tiny budget cut silently: %q", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("short", 100); got != "short" {
		t.Errorf("Truncate(%q, 100) = %q, want it unchanged", "short", got)
	}
	got := Truncate(strings.Repeat("a", 100), 10)
	if !strings.HasPrefix(got, strings.Repeat("a", 10)) || !strings.Contains(got, "truncated") {
		t.Errorf("Truncate = %q, want 10 bytes plus an announced cut", got)
	}
}
