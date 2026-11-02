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

// TestParse_ResultMinBytesRejectsNonInteger pins the decode half of #446's QA
// finding: yaml.v3 truncates a float into an int field, so 2.5 loaded as 2
// and 200.5 as 200. Each must instead be a load error naming the field, and
// the quoted string — already refused — now says the same.
func TestParse_ResultMinBytesRejectsNonInteger(t *testing.T) {
	for _, value := range []string{"2.5", "200.5", `"200"`} {
		t.Run(value, func(t *testing.T) {
			_, err := Parse([]byte(`
name: min-bytes
nodes:
  - id: plan
    prompt: p
    success_check: { exit_zero: true, result_min_bytes: ` + value + ` }
`))
			if err == nil {
				t.Fatalf("result_min_bytes: %s loaded, want a load error", value)
			}
			msg := err.Error()
			if !strings.HasPrefix(msg, "parse graph YAML: ") {
				t.Errorf("error = %q, want it wrapped like every other decode error", msg)
			}
			if !strings.Contains(msg, "result_min_bytes must be a whole number of bytes") {
				t.Errorf("error = %q, want it to name result_min_bytes and ask for a whole number", msg)
			}
			if strings.Count(msg, "result_min_bytes") != 1 || strings.Contains(msg, "cannot unmarshal") {
				t.Errorf("error = %q, want the field reported once, in one message", msg)
			}
		})
	}
}

// TestParse_ResultMinBytesStrictnessLeavesOtherKeysAlone proves the strict
// decode touches one field only: the other success_check keys still decode,
// a wrong type elsewhere still draws yaml.v3's own error beside the new one,
// and an int field outside success_check still truncates exactly as before.
func TestParse_ResultMinBytesStrictnessLeavesOtherKeysAlone(t *testing.T) {
	g, err := Parse([]byte(`
name: min-bytes
concurrency: 2
nodes:
  - id: plan
    prompt: p
    success_check:
      exit_zero: true
      result_matches: "^DONE"
      result_min_bytes: 200
      verify: { command: "true" }
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plan, _ := g.NodeByID("plan")
	check := plan.SuccessCheck
	if !check.ExitZero || check.ResultMatches != "^DONE" || check.ResultMinBytes != 200 || check.Verify == nil || check.Verify.Command != "true" {
		t.Errorf("success_check decoded as %+v", check)
	}
	if g.Concurrency != 2 {
		t.Errorf("concurrency = %d, want 2", g.Concurrency)
	}

	_, err = Parse([]byte(`
name: min-bytes
nodes:
  - id: plan
    prompt: p
    success_check: { exit_zero: maybe, result_min_bytes: 2.5 }
`))
	if err == nil {
		t.Fatal("expected both wrong types to be rejected")
	}
	for _, want := range []string{"cannot unmarshal !!str `maybe` into bool", "result_min_bytes must be a whole number"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to carry %q", err, want)
		}
	}

	g, err = Parse([]byte(`
name: min-bytes
concurrency: 2.5
nodes:
  - { id: plan, prompt: p }
`))
	if err != nil {
		t.Fatalf("concurrency: 2.5 is outside this change and must decode as before: %v", err)
	}
	if g.Concurrency != 2 {
		t.Errorf("concurrency = %d, want yaml.v3's truncated 2", g.Concurrency)
	}
}
