package runner

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseEnvelope_Valid(t *testing.T) {
	outcome, err := parseEnvelope([]byte(`{"session_id":"s-1","result":"PASS done","total_cost_usd":0.0123}`), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.SessionID != "s-1" || outcome.Result != "PASS done" {
		t.Fatalf("parsed wrong fields: %+v", outcome)
	}
	if outcome.TotalCostUSD != 0.0123 {
		t.Fatalf("cost = %v, want 0.0123", outcome.TotalCostUSD)
	}
}

func TestParseEnvelope_NotJSON(t *testing.T) {
	_, err := parseEnvelope([]byte("this is not json"), nil)
	var outErr *NodeOutputError
	if !errors.As(err, &outErr) {
		t.Fatalf("expected *NodeOutputError, got %T: %v", err, err)
	}
}

func TestParseEnvelope_Empty(t *testing.T) {
	_, err := parseEnvelope([]byte("   \n  "), nil)
	var outErr *NodeOutputError
	if !errors.As(err, &outErr) {
		t.Fatalf("expected *NodeOutputError for empty output, got %T: %v", err, err)
	}
}

// TestParseEnvelope_UnresolvableAgentIsDiagnosable is the failure this project
// deliberately does NOT paper over. An `agent:` naming a subagent the machine
// does not have makes claude exit non-zero having written NOTHING to stdout and
// its complaint to stderr (measured on 2.1.220). oh-my-graph does not retry as
// plain claude — silently running a review node as something other than the
// reviewer the graph asked for would be a worse outcome than failing — so the
// node fails, and the only thing that makes that failure actionable is carrying
// the CLI's own message, which names every agent that IS available.
func TestParseEnvelope_UnresolvableAgentIsDiagnosable(t *testing.T) {
	stderr := []byte("--agent 'reviewr' not found. Available agents: code-reviewer, developer\n")

	_, err := parseEnvelope(nil, stderr)

	var outErr *NodeOutputError
	if !errors.As(err, &outErr) {
		t.Fatalf("expected *NodeOutputError, got %T: %v", err, err)
	}
	if !strings.Contains(outErr.Stderr, "not found") {
		t.Errorf("NodeOutputError.Stderr lost the CLI's complaint: %q", outErr.Stderr)
	}
	// The message a user actually reads must name the alternatives, or the
	// failure is a dead end.
	if !strings.Contains(err.Error(), "Available agents: code-reviewer, developer") {
		t.Errorf("error message must surface the CLI's stderr, got: %q", err.Error())
	}
}

// TestParseEnvelope_StderrTailSurvivesTruncation proves a long stderr keeps its
// TAIL. The CLI prints startup warnings first and its real complaint last, so
// head-truncating would reliably discard the only useful line.
func TestParseEnvelope_StderrTailSurvivesTruncation(t *testing.T) {
	noise := strings.Repeat("warning: something chatty\n", 200)
	stderr := []byte(noise + "--agent 'reviewr' not found.")

	_, err := parseEnvelope(nil, stderr)

	var outErr *NodeOutputError
	if !errors.As(err, &outErr) {
		t.Fatalf("expected *NodeOutputError, got %T: %v", err, err)
	}
	if !strings.Contains(outErr.Stderr, "not found") {
		t.Errorf("truncation dropped the tail, which is where the complaint is: %q", outErr.Stderr)
	}
	if len(outErr.Stderr) > maxStderrInError+len("…(truncated) ") {
		t.Errorf("stderr was not truncated: %d bytes", len(outErr.Stderr))
	}
}

// TestNodeOutputError_ErrorIsSingleLine guards the blast radius of carrying
// stderr at all. Error() is rendered straight into the scheduler's live "✗ node
// FAILED: …" progress line and into ledger.Record.Detail, which is the last
// column of a fixed-width table. A newline in that string turns the end-of-run
// summary into ragged garbage — and the case that produces stderr is the
// unresolvable-agent failure this feature was built for, so it would be hit by
// the very users the feature is for.
func TestNodeOutputError_ErrorIsSingleLine(t *testing.T) {
	err := &NodeOutputError{
		Reason: "claude produced no output",
		Stderr: "Warning: something chatty\n--agent 'reviewr' not found. Available agents: code-reviewer\n",
	}

	msg := err.Error()
	if strings.Contains(msg, "\n") {
		t.Errorf("Error() must stay on one line for the ledger table and progress feed, got:\n%q", msg)
	}
	// Flattening must not silently drop the part worth reading.
	if !strings.Contains(msg, "not found") {
		t.Errorf("flattening lost the CLI's complaint: %q", msg)
	}
	if !strings.Contains(msg, "Warning: something chatty") {
		t.Errorf("flattening dropped a stderr line entirely: %q", msg)
	}
}

// TestParseEnvelope_SuccessCarriesNoStderr proves stderr never leaks into a
// successful outcome — a node that warned on stderr but printed a good
// envelope is a pass, not a diagnosis.
func TestParseEnvelope_SuccessCarriesNoStderr(t *testing.T) {
	outcome, err := parseEnvelope(
		[]byte(`{"session_id":"s-1","result":"PASS","total_cost_usd":0.01}`),
		[]byte("warning: noisy but harmless"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Result != "PASS" {
		t.Fatalf("outcome mangled by stderr handling: %+v", outcome)
	}
}

// TestParseEnvelope_FailureCause pins where the cause comes from inside the
// envelope itself: the errors array when present, the result text of an
// is_error envelope otherwise, always flattened to one line — and never
// anything on a clean success envelope.
func TestParseEnvelope_FailureCause(t *testing.T) {
	cases := []struct {
		name     string
		envelope string
		want     string
	}{
		{
			name:     "errors array wins",
			envelope: `{"session_id":"s1","total_cost_usd":0.02,"is_error":true,"errors":["You've hit your session limit","try again later"]}`,
			want:     "You've hit your session limit / try again later",
		},
		{
			name:     "is_error falls back to result",
			envelope: `{"session_id":"s1","result":"You've hit your session limit","total_cost_usd":0.02,"is_error":true}`,
			want:     "You've hit your session limit",
		},
		{
			name:     "success carries no cause",
			envelope: `{"session_id":"s1","result":"PASS","total_cost_usd":0.02,"subtype":"success"}`,
			want:     "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outcome, err := parseEnvelope([]byte(tc.envelope), nil)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if outcome.FailureCause != tc.want {
				t.Errorf("FailureCause = %q, want %q", outcome.FailureCause, tc.want)
			}
		})
	}
}

func TestFakeRunner_UnscriptedNodeErrors(t *testing.T) {
	f := NewFakeRunner(map[string]NodeOutcome{"a": {Result: "PASS"}})
	_, err := f.Run(context.Background(), NodeInvocation{Prompt: "unscripted"})
	if err == nil {
		t.Fatal("expected an error for an unscripted node")
	}
}

func TestFakeRunner_InjectedError(t *testing.T) {
	f := NewFakeRunner(map[string]NodeOutcome{})
	boom := errors.New("spawn failed")
	f.InjectError("a", boom)
	_, err := f.Run(context.Background(), NodeInvocation{Prompt: "a"})
	if !errors.Is(err, boom) {
		t.Fatalf("expected injected error, got %v", err)
	}
}
