package graph

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The reuse:/bind: backstop (#338, ADR 0038). Those keys are legal only in a
// planner reply, which the coordinator judges against its menu and splices;
// every other way a graph reaches the engine must refuse them, or a node with
// no prompt and no tools of its own would run.

const reuseCitingYAML = `
name: hand-written
nodes:
  - id: report
    reuse: read-and-report
    bind: { target: README.md, question: what is this }
`

// #338: a hand-written YAML graph carrying reuse: is refused, through Parse and
// through the path-aware file loader `run` uses.
func TestReuseBackstop_HandWrittenGraphIsRefused(t *testing.T) {
	assertUnspliced := func(t *testing.T, err error) {
		t.Helper()
		var unspliced *UnsplicedReuseError
		if !errors.As(err, &unspliced) {
			t.Fatalf("err = %v, want *UnsplicedReuseError", err)
		}
		if unspliced.NodeID != "report" || !strings.Contains(unspliced.Reason, "reuse:") {
			t.Errorf("refusal does not name the node and the key: %v", err)
		}
		var general *GraphValidationError
		if !errors.As(err, &general) {
			t.Error("the backstop does not answer the general GraphValidationError question")
		}
	}

	t.Run("Parse", func(t *testing.T) {
		_, err := Parse([]byte(reuseCitingYAML))
		assertUnspliced(t, err)
	})
	t.Run("LoadFile", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "graph.yaml")
		if err := os.WriteFile(path, []byte(reuseCitingYAML), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := LoadFile(path)
		assertUnspliced(t, err)
	})
	t.Run("LintFile", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "graph.yaml")
		if err := os.WriteFile(path, []byte(reuseCitingYAML), 0o644); err != nil {
			t.Fatal(err)
		}
		issues, _, err := LintFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(issues) != 1 {
			t.Fatalf("issues = %v, want exactly the backstop", issues)
		}
		assertUnspliced(t, issues[0])
	})
}

// #338: a saved graph.json or a resumed snapshot — JSON handed to Parse — is
// refused the same way, for reuse: alone and for a stray bind: alone,
// including the empty `bind: {}` a length test would wave through.
func TestReuseBackstop_SavedSpecIsRefused(t *testing.T) {
	for name, node := range map[string]string{
		"reuse":      `{"id":"report","reuse":"read-and-report"}`,
		"bind":       `{"id":"report","prompt":"p","bind":{"target":"x"}}`,
		"empty bind": `{"id":"report","prompt":"p","bind":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(`{"name":"saved","nodes":[` + node + `]}`))
			var unspliced *UnsplicedReuseError
			if !errors.As(err, &unspliced) {
				t.Fatalf("err = %v, want *UnsplicedReuseError", err)
			}
		})
	}
}

// #338: ParsePlannerReply lets the citation through for the coordinator to
// judge, decodes both keys, and still refuses every other structural issue —
// including the use:/with: backstop beside it, which a planner may never pass.
func TestParsePlannerReply_PassesOnlyTheReuseBackstop(t *testing.T) {
	g, err := ParsePlannerReply([]byte(`{"name":"plan","nodes":[{"id":"report","reuse":"read-and-report","bind":{"target":"README.md"}}]}`))
	if err != nil {
		t.Fatalf("a citing planner reply must parse: %v", err)
	}
	node, _ := g.NodeByID("report")
	if node.Reuse != "read-and-report" || node.Bind["target"] != "README.md" {
		t.Errorf("reuse/bind did not decode: %+v", node)
	}
	if err := g.Validate(); err == nil {
		t.Error("the graph ParsePlannerReply returned validates with an unspliced citation in it")
	}

	_, err = ParsePlannerReply([]byte(`{"name":"plan","nodes":[{"id":"a","reuse":"x"},{"id":"a","reuse":"x"}]}`))
	var invalid *GraphValidationError
	if !errors.As(err, &invalid) || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("a duplicate id beside a citation was not refused: %v", err)
	}

	_, err = ParsePlannerReply([]byte(`{"name":"plan","nodes":[{"id":"a","use":"read-and-report"}]}`))
	var unresolved *UnresolvedFragmentError
	if !errors.As(err, &unresolved) {
		t.Errorf("use: was not refused by ParsePlannerReply: %v", err)
	}
}
