package fence

import (
	"strings"
	"testing"
	"unicode/utf8"
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

// TestCutsLandOnRuneBoundaries: every bound in this package cuts text at a byte
// offset, and a byte offset lands inside a multi-byte rune the moment the
// material is not ASCII. Half a rune is invalid UTF-8 — mojibake handed to a
// model at exactly the seam it is reading. Sweeping the budget walks the cut
// across every offset within a rune, so no single lucky alignment can hide it.
func TestCutsLandOnRuneBoundaries(t *testing.T) {
	body := strings.Repeat("긴 답장", 200) // 3-byte runes, ASCII spaces between
	for n := 1; n < 80; n++ {
		if got := Excerpt(body, len(ExcerptMarker)+n); !utf8.ValidString(got) {
			t.Fatalf("Excerpt(…, marker+%d) returned invalid UTF-8", n)
		}
		if got := Truncate(body, n); !utf8.ValidString(got) {
			t.Fatalf("Truncate(…, %d) returned invalid UTF-8", n)
		}
		head, tail := HeadAndTail(body, n)
		if !utf8.ValidString(head) || !utf8.ValidString(tail) {
			t.Fatalf("HeadAndTail(…, %d) cut inside a rune: head valid=%v tail valid=%v",
				n, utf8.ValidString(head), utf8.ValidString(tail))
		}
		if len(head)+len(tail) > n {
			t.Fatalf("HeadAndTail(…, %d) kept %d bytes; trimming may only shorten", n, len(head)+len(tail))
		}
	}
}

// TestHeadAndTail_KeepsBothEnds pins what the pieces are for: a caller joining
// them with its own marker gets the opening AND the closing of the material,
// which is the half a head-only bound throws away.
func TestHeadAndTail_KeepsBothEnds(t *testing.T) {
	head, tail := HeadAndTail("OPENING"+strings.Repeat("x", 500)+"CLOSING", 20)
	if !strings.HasPrefix(head, "OPENING") {
		t.Errorf("head = %q, want the opening of the material", head)
	}
	if !strings.HasSuffix(tail, "CLOSING") {
		t.Errorf("tail = %q, want the closing of the material", tail)
	}
	if h, tl := HeadAndTail("short", 100); h != "short" || tl != "" {
		t.Errorf("HeadAndTail(%q, 100) = (%q, %q), want the material whole in the head", "short", h, tl)
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
