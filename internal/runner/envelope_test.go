package runner

import (
	"context"
	"errors"
	"testing"
)

func TestParseEnvelope_Valid(t *testing.T) {
	outcome, err := parseEnvelope([]byte(`{"session_id":"s-1","result":"PASS done","total_cost_usd":0.0123}`))
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
	_, err := parseEnvelope([]byte("this is not json"))
	var outErr *NodeOutputError
	if !errors.As(err, &outErr) {
		t.Fatalf("expected *NodeOutputError, got %T: %v", err, err)
	}
}

func TestParseEnvelope_Empty(t *testing.T) {
	_, err := parseEnvelope([]byte("   \n  "))
	var outErr *NodeOutputError
	if !errors.As(err, &outErr) {
		t.Fatalf("expected *NodeOutputError for empty output, got %T: %v", err, err)
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
