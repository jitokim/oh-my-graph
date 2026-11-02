package graph

import (
	"strings"
	"testing"
)

// TestParse_ResultMinBytesAcceptsZeroAndPositive pins the load half of #446:
// the key decodes onto SuccessCheck.ResultMinBytes, and 0 (no minimum) and a
// positive floor both validate.
func TestParse_ResultMinBytesAcceptsZeroAndPositive(t *testing.T) {
	g, err := Parse([]byte(`
name: min-bytes
nodes:
  - id: zero
    prompt: p
    success_check: { exit_zero: true, result_min_bytes: 0 }
  - id: floor
    prompt: p
    success_check: { result_min_bytes: 200 }
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	zero, _ := g.NodeByID("zero")
	if got := zero.SuccessCheck.ResultMinBytes; got != 0 {
		t.Errorf("zero: result_min_bytes = %d, want 0", got)
	}
	floor, _ := g.NodeByID("floor")
	if got := floor.SuccessCheck.ResultMinBytes; got != 200 {
		t.Errorf("floor: result_min_bytes = %d, want 200", got)
	}
}

// TestParse_ResultMinBytesRejectsNegative proves a negative floor is a load
// error naming the node and the field, not a silent "no minimum".
func TestParse_ResultMinBytesRejectsNegative(t *testing.T) {
	_, err := Parse([]byte(`
name: min-bytes
nodes:
  - id: plan
    prompt: p
    success_check: { result_min_bytes: -1 }
`))
	if err == nil {
		t.Fatal("expected a negative result_min_bytes to be rejected")
	}
	vErr := asValidationError(t, err)
	if vErr.NodeID != "plan" {
		t.Errorf("NodeID = %q, want plan", vErr.NodeID)
	}
	if !strings.Contains(vErr.Reason, "result_min_bytes") || !strings.Contains(vErr.Reason, "-1") {
		t.Errorf("Reason = %q, want it to name result_min_bytes and the value", vErr.Reason)
	}
}

// TestSuccessCheck_OnlyResultMinBytesIsNotZero guards the emptiness helper: a
// check carrying only the length floor read as empty would fall back to
// exit-zero-only and never measure the result.
func TestSuccessCheck_OnlyResultMinBytesIsNotZero(t *testing.T) {
	if (SuccessCheck{ResultMinBytes: 200}).IsZero() {
		t.Error("a check with only result_min_bytes must not be empty")
	}
	if !(SuccessCheck{ResultMinBytes: 0}).IsZero() {
		t.Error("result_min_bytes: 0 alone is no predicate and must stay empty")
	}
}
