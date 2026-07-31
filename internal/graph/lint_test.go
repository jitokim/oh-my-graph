package graph

import (
	"strings"
	"testing"
)

// TestLint_ValidGraphHasNoIssues pins the exit-0 half of the contract: a graph
// Parse accepts must lint clean.
func TestLint_ValidGraphHasNoIssues(t *testing.T) {
	issues := Lint([]byte(`
name: clean
nodes:
  - { id: a, prompt: a }
  - { id: b, prompt: b, depends_on: [a], handoff: session }
`))
	if len(issues) != 0 {
		t.Fatalf("valid graph reported issues: %v", issues)
	}
}

// TestLint_CollectsEveryViolation is what distinguishes Lint from Parse: a
// graph broken in three independent ways — an unknown depends_on id, a
// dependency cycle, and a session-handoff root — reports all three at once,
// rather than one per fix-and-retry round trip.
func TestLint_CollectsEveryViolation(t *testing.T) {
	issues := Lint([]byte(`
name: broken
nodes:
  - { id: a, prompt: a, depends_on: [b] }
  - { id: b, prompt: b, depends_on: [a] }
  - { id: c, prompt: c, depends_on: [ghost] }
  - { id: d, prompt: d, handoff: session }
`))
	if len(issues) != 3 {
		t.Fatalf("got %d issues, want 3: %v", len(issues), issues)
	}
	for _, want := range []string{"ghost", "cycle", "session"} {
		if !containsIssue(issues, want) {
			t.Errorf("no issue mentions %q: %v", want, issues)
		}
	}
}

// TestLint_EveryIssueIsAValidationError pins the reporting contract: each
// structural issue is a *GraphValidationError, so a renderer can name the
// offending node without string matching.
func TestLint_EveryIssueIsAValidationError(t *testing.T) {
	issues := Lint([]byte(`
name: broken
nodes:
  - { id: a, prompt: a, type: wizardry, handoff: telepathy }
`))
	if len(issues) != 2 {
		t.Fatalf("got %d issues, want 2 (bad type + bad handoff): %v", len(issues), issues)
	}
	for _, issue := range issues {
		vErr := asValidationError(t, issue)
		if vErr.NodeID != "a" {
			t.Errorf("issue named node %q, want a: %v", vErr.NodeID, issue)
		}
	}
}

// TestLint_FirstIssueIsWhatParseReturns pins Validate as Issues' fail-fast
// view: lint and run can never disagree about which graphs are valid, nor
// about which violation is reported first.
func TestLint_FirstIssueIsWhatParseReturns(t *testing.T) {
	spec := []byte(`
name: broken
nodes:
  - { id: a, prompt: a, depends_on: [ghost], handoff: session }
`)
	issues := Lint(spec)
	if len(issues) == 0 {
		t.Fatal("expected issues for a broken graph")
	}
	_, err := Parse(spec)
	if err == nil {
		t.Fatal("Parse accepted a graph Lint rejects")
	}
	if issues[0].Error() != err.Error() {
		t.Errorf("Lint's first issue %q differs from Parse's error %q", issues[0], err)
	}
}

// TestLint_YAMLSyntaxErrorIsTheWholeReport: nothing structural can be judged
// about a document that did not decode, so the decode error stands alone.
func TestLint_YAMLSyntaxErrorIsTheWholeReport(t *testing.T) {
	issues := Lint([]byte("name: [unterminated"))
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want exactly the decode error: %v", len(issues), issues)
	}
	if !strings.Contains(issues[0].Error(), "parse graph YAML") {
		t.Errorf("issue should be the decode error: %v", issues[0])
	}
}

// containsIssue reports whether any issue's message mentions substr.
func containsIssue(issues []error, substr string) bool {
	for _, issue := range issues {
		if strings.Contains(issue.Error(), substr) {
			return true
		}
	}
	return false
}
