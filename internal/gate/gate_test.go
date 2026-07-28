package gate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

func TestStubController_RefusesAndNamesNode(t *testing.T) {
	err := NewStubController().Evaluate(context.Background(), graph.Node{ID: "approve"})
	if err == nil {
		t.Fatal("v0.1 gate stub must refuse execution")
	}
	if !errors.Is(err, ErrGateNotImplemented) {
		t.Fatalf("error should unwrap to ErrGateNotImplemented, got %v", err)
	}
	if !strings.Contains(err.Error(), "approve") {
		t.Fatalf("error should name the gate node: %v", err)
	}
}
