// An external test package, like gated_dev_test.go: the full `lint` view
// includes the handoff sweeps, and package handoff imports package graph.
package graph_test

import (
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/handoff"
)

// TestDriftReviewLintsClean_386 holds drift-review to what `lint` says about
// it: no issue, no gate-description refusal, no fragment advisory, no
// feedback-reach advisory and no handoff warning. Both fragments it cites are
// new with it, so no warning can be one a fragment already carries elsewhere.
func TestDriftReviewLintsClean_386(t *testing.T) {
	const name = "drift-review.yaml"
	issues, fragmentAdvisories, loaded, err := graph.LintLoadFile(shippedGraphPath(name))
	if err != nil {
		t.Fatalf("lint %s: %v", name, err)
	}
	if len(issues) != 0 {
		t.Fatalf("%s has lint issues: %v", name, issues)
	}
	if refused := handoff.GateDescriptionIssues(loaded.Graph); len(refused) != 0 {
		t.Errorf("%s: lint refuses a gate description: %v", name, refused)
	}
	if len(fragmentAdvisories) != 0 {
		t.Errorf("%s: fragment advisories: %v", name, fragmentAdvisories)
	}
	if reach := loaded.Graph.LintFeedbackReach(); len(reach) != 0 {
		t.Errorf("%s: feedback-reach advisories: %v", name, reach)
	}
	for _, w := range lintWarnings(loaded.Graph) {
		t.Errorf("%s: warning %q", name, w)
	}
}
