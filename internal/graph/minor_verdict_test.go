package graph

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The verdict replies ADR 0043's patterns are judged against, each dressed the
// way a real reply arrives: bare, with emphasis on either side of the colon,
// in a code span, and after leading blank lines.
var (
	cleanReplies = []string{
		"CLEAN",
		"CLEAN — nothing worth changing.\n",
		"**CLEAN**\n",
		"\n\n  CLEAN\n",
	}
	minorReplies = []string{
		"MINOR: - the helper name says nothing about what it returns\n",
		"**MINOR:** - the helper name says nothing about what it returns\n",
		"**MINOR**: - the helper name says nothing about what it returns\n",
		"`MINOR:` - the helper name says nothing about what it returns\n",
		"\n\n  MINOR:\n- the helper name says nothing about what it returns\n",
	}
	findingsReplies = []string{
		"FINDINGS: - the persisting branch returns without refreshing the projection\n",
		"**FINDINGS:** - the persisting branch returns without refreshing the projection\n",
		"**FINDINGS**: - the persisting branch returns without refreshing the projection\n",
		"\n\nFINDINGS:\n- the persisting branch returns without refreshing the projection\nMinor:\n- a name\n",
	}
	// Malformed: none of the three tokens as the reply's first characters.
	// Every one fails BOTH patterns — the colon is part of MINOR:, the match
	// is case-sensitive, a heading is block structure the class does not
	// absorb, and the anchor holds the token to the start of the reply.
	malformedReplies = []string{
		"",
		"Still reviewing; MINOR: so far.\n",
		"MINOR - the helper name says nothing\n",
		"Minor: - the helper name says nothing\n",
		"minor: - the helper name says nothing\n",
		"MINORITY: report\n",
		"## MINOR: - the helper name says nothing\n",
		"Context first.\nMINOR: - the helper name says nothing\n",
		"Context first.\nFINDINGS: - a defect\n",
	}
)

// loadShipped loads graphs/<name> through the same path-aware seam `run` uses,
// so a fragment's pattern is judged as it resolves, never as YAML text.
func loadShipped(t *testing.T, name string) *Graph {
	t.Helper()
	loaded, err := LoadFile(filepath.Join("..", "..", "graphs", name))
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return loaded.Graph
}

func shippedNode(t *testing.T, g *Graph, file, id string) Node {
	t.Helper()
	n, ok := g.byID[id]
	if !ok {
		t.Fatalf("%s has no node %q", file, id)
	}
	return n
}

// assertPartition judges pattern against every reply class and names the
// reply it got wrong.
func assertPartition(t *testing.T, where, pattern string, wantClean, wantMinor, wantFindings bool) {
	t.Helper()
	re := regexp.MustCompile(pattern)
	for _, c := range []struct {
		class   string
		replies []string
		want    bool
	}{
		{"CLEAN", cleanReplies, wantClean},
		{"MINOR:", minorReplies, wantMinor},
		{"FINDINGS:", findingsReplies, wantFindings},
		{"malformed", malformedReplies, false},
	} {
		for _, reply := range c.replies {
			if got := re.MatchString(reply); got != c.want {
				t.Errorf("%s: pattern %q on %s reply %q: matched=%v, want %v", where, pattern, c.class, reply, got, c.want)
			}
		}
	}
}

// TestReviewFragmentsAdvisoryPatternIsThreeValued pins ADR 0043 §2.3's
// advisory row: a fragment's own check passes every verdict and fails anything
// that is not one. Judged on dev-review-pr's reviews, which keep the default.
func TestReviewFragmentsAdvisoryPatternIsThreeValued(t *testing.T) {
	g := loadShipped(t, "dev-review-pr.yaml")
	for _, id := range []string{"review-style", "review-security"} {
		n := shippedNode(t, g, "dev-review-pr.yaml", id)
		if n.Feedback != nil {
			t.Fatalf("dev-review-pr's %s declares a feedback arc — it is meant to keep the advisory default", id)
		}
		assertPartition(t, "dev-review-pr/"+id, n.SuccessCheck.ResultMatches, true, true, true)
	}
}

// TestGatedLaneReviewGatesOnlyOnFindings pins §2.3's gating row on the one
// fragment that ships the gate pair: CLEAN and MINOR: pass, FINDINGS: fails
// and fires the arc, malformed fails.
func TestGatedLaneReviewGatesOnlyOnFindings(t *testing.T) {
	g := loadShipped(t, "backlog-batch.yaml")
	n := shippedNode(t, g, "backlog-batch.yaml", "lane-a/review")
	if n.Feedback == nil {
		t.Fatal("gated-lane's review declares no feedback arc — it no longer gates")
	}
	assertPartition(t, "backlog-batch/lane-a/review", n.SuccessCheck.ResultMatches, true, true, false)
}

// TestSelfDevVerdictPatternGatesOnlyOnFindings is the same row for
// self-dev's fan-in `review-verdict`, against the whole reply set.
func TestSelfDevVerdictPatternGatesOnlyOnFindings(t *testing.T) {
	g := loadShipped(t, "self-dev.yaml")
	n := shippedNode(t, g, "self-dev.yaml", "review-verdict")
	assertPartition(t, "self-dev/review-verdict", n.SuccessCheck.ResultMatches, true, true, false)
}

// normalize collapses a prompt's line wrapping, so a phrase is found however
// the YAML block happens to break it.
func normalize(s string) string { return strings.Join(strings.Fields(s), " ") }

// TestReviewPromptsCarryTheGradingRuleAndTheRatchet pins §2.1 and §2.2 where a
// reviewer reads them — in the resolved prompt of every node that splices a
// review fragment: the three tokens, the mergeability rule, and the
// minor-stays-minor ratchet, all ahead of the verdict-format rule's token list.
// And it pins gated-lane.yaml's standing rule against this change: the
// reviewer is told nothing about a gate, an arc or a feedback round.
func TestReviewPromptsCarryTheGradingRuleAndTheRatchet(t *testing.T) {
	const verdictFormatRule = "START the reply with exactly one of these three bare tokens"
	required := []string{
		"whether you would merge with it unfixed, not by how small the fix is",
		"An item you rated minor last round stays minor unless the rework changed the code it is about",
		"a blocking item stays blocking while it is open",
		"the verdict is CLEAN, or MINOR: if only minor items remain open",
		"- CLEAN —",
		"- MINOR: —",
		"- FINDINGS: —",
		"under a `Minor:` line",
	}
	gateTalk := regexp.MustCompile(`(?i)\b(gate|gates|gated|gating|arc|feedback)\b`)
	seen := 0
	for _, name := range shippedTemplateNames(t) {
		loaded, err := LoadFile(filepath.Join("..", "..", "graphs", name))
		if err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		for _, res := range loaded.Resolutions {
			if !strings.HasPrefix(res.Fragment, "review-") || len(res.Spliced) > 0 {
				continue
			}
			n, ok := loaded.Graph.byID[res.NodeID]
			if !ok {
				continue
			}
			seen++
			prompt := normalize(n.Prompt)
			format := strings.Index(prompt, verdictFormatRule)
			if format < 0 {
				t.Errorf("%s: review %q never offers the three tokens (%q missing)", name, res.NodeID, verdictFormatRule)
				continue
			}
			for _, want := range required {
				if !strings.Contains(prompt, want) {
					t.Errorf("%s: review %q's prompt lacks %q", name, res.NodeID, want)
				}
			}
			if ratchet := strings.Index(prompt, "stays minor unless"); ratchet > format {
				t.Errorf("%s: review %q states the ratchet after the verdict-format rule — the rule must be the last thing the reviewer reads", name, res.NodeID)
			}
			if rule := strings.Index(prompt, "whether you would merge"); rule > format {
				t.Errorf("%s: review %q states the grading rule after the token list it grades into", name, res.NodeID)
			}
			if m := gateTalk.FindString(prompt); m != "" {
				t.Errorf("%s: review %q's prompt says %q — a reviewer told what the gate does is biased toward the passing tokens (gated-lane.yaml)", name, res.NodeID, m)
			}
		}
	}
	if seen == 0 {
		t.Error("no shipped graph splices a review fragment — this test asserts nothing")
	}
}

// TestGatingDevPromptsRepairOnlyWhatBlocked pins §2.4: both gating places'
// implementers fix the blocking findings and leave the `Minor:` items, so a
// repair round repairs what blocked it and nothing else.
func TestGatingDevPromptsRepairOnlyWhatBlocked(t *testing.T) {
	for _, c := range []struct{ file, id string }{
		{"backlog-batch.yaml", "lane-a/dev"},
		{"self-dev.yaml", "dev"},
	} {
		prompt := normalize(shippedNode(t, loadShipped(t, c.file), c.file, c.id).Prompt)
		for _, want := range []string{
			"fix every blocking finding before anything else",
			"Items under a `Minor:` line are not this round's to fix",
		} {
			if !strings.Contains(prompt, want) {
				t.Errorf("%s/%s: prompt lacks %q", c.file, c.id, want)
			}
		}
		if strings.Contains(prompt, "fix every one") {
			t.Errorf("%s/%s: prompt still says \"fix every one\" — the round would repair the minor items too, and touching their code releases the reviewer's ratchet", c.file, c.id)
		}
	}
}

// TestTwoValuedReviewsStayTwoValued pins §2.4's exclusion: the reviews no gate
// reads keep CLEAN / FINDINGS:, and a MINOR: reply there is an unoffered token
// that fails loudly. A sweep that "makes it uniform" changes this test and
// says why.
func TestTwoValuedReviewsStayTwoValued(t *testing.T) {
	g := loadShipped(t, "adr-driven-dev.yaml")
	for _, id := range []string{"adr-review", "round1/review", "round2/review", "round3"} {
		n := shippedNode(t, g, "adr-driven-dev.yaml", id)
		assertPartition(t, "adr-driven-dev/"+id, n.SuccessCheck.ResultMatches, true, false, true)
		if strings.Contains(n.Prompt, "MINOR") {
			t.Errorf("adr-driven-dev/%s offers MINOR — its consumer addresses every item, so a severity label there changes nothing (ADR 0043 §2.4)", id)
		}
	}
}
