package handoff

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// TestInterpolateAs_SelfTimeoutRendersTheSuppliedBound (#292): the token
// renders the node's effective per-attempt timeout in time.Duration's String
// form, unfenced — the engine wrote it, not a model.
func TestInterpolateAs_SelfTimeoutRendersTheSuppliedBound(t *testing.T) {
	h := New(t.TempDir(), nil)
	for timeout, want := range map[time.Duration]string{
		7 * time.Minute:              "7m0s",
		90 * time.Minute:             "1h30m0s",
		45 * time.Second:             "45s",
		20*time.Minute + time.Second: "20m1s",
	} {
		got, err := h.InterpolateAs(Self{ID: "impl", Timeout: timeout}, "you have {{ self.timeout }} per attempt")
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", timeout, err)
		}
		if got != "you have "+want+" per attempt" {
			t.Errorf("%s: got %q, want %q inlined", timeout, got, want)
		}
	}
}

// TestInterpolate_SelfTimeoutRefusals (#292): a filtered or unknown self token
// is refused, and each refusal names BOTH self references; a self.timeout with
// no node, or with no timeout supplied, is refused rather than rendered as 0s.
func TestInterpolate_SelfTimeoutRefusals(t *testing.T) {
	h := New(t.TempDir(), nil)
	self := Self{ID: "impl", Timeout: 7 * time.Minute}
	for _, tc := range []struct {
		name, tmpl string
	}{
		{"filtered timeout", "{{ self.timeout | inline }}"},
		{"unknown reference", "{{ self.nope }}"},
	} {
		_, err := h.InterpolateAs(self, tc.tmpl)
		var interp *InterpolationError
		if !errors.As(err, &interp) || interp.Kind != "self" {
			t.Fatalf("%s: error = %v, want a self *InterpolationError", tc.name, err)
		}
		for _, ref := range []string{"{{ self.previous }}", "{{ self.timeout }}"} {
			if !strings.Contains(err.Error(), ref) {
				t.Errorf("%s: refusal %q does not name %s", tc.name, err, ref)
			}
		}
	}

	if _, err := h.Interpolate("{{ self.timeout }}"); err == nil {
		t.Error("Interpolate (no node) resolved self.timeout; want an error")
	}
	if got, err := h.InterpolateFor("impl", "{{ self.timeout }}"); err == nil {
		t.Errorf("self.timeout with no timeout supplied resolved to %q; want an error", got)
	}
}
