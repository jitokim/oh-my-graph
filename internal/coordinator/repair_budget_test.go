package coordinator

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/fence"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// twoBrokenLanesSpec is the shape neither feedback rule was tested against: TWO
// declarers, each of them BOTH mis-aimed (its loop cannot reach `qa-*`, so
// validatePlannedFeedbackReach fires with a covering target) AND blind (no body
// node quotes `{{ feedback.review-* }}`, so validatePlannedFeedbackQuoting
// fires). ADR 0028 §Failure modes says the two rules can fire on one arc and are
// not deduplicated; this is that sentence as a fixture.
//
// It matters because the two refusal families are the longest sentences the
// validator emits, they both lead the issue list, and until this test nothing
// held them to a budget they share. It is also the fixture every byte figure in
// maxIssuesInPrompt's comment is measured on, which
// TestGraphLevelRefusalFamiliesRenderTheirMeasuredSize is what keeps true.
const twoBrokenLanesSpec = `{"name":"two-broken-lanes","version":"1","nodes":[` +
	`{"id":"scope-a","prompt":"scope lane a","allowed_tools":["Read"]},` +
	`{"id":"qa-a","depends_on":["scope-a"],"prompt":"write QA-PLAN-a.md","allowed_tools":["Write"]},` +
	`{"id":"load-a","depends_on":["scope-a"],"prompt":"write load-a.js","allowed_tools":["Write"]},` +
	`{"id":"review-a","depends_on":["qa-a","load-a"],"prompt":"judge lane a",` +
	`"allowed_tools":["Read"],"success_check":{"result_matches":"^PASS$"},"feedback":{"rerun":"load-a","max":2}},` +
	`{"id":"scope-b","prompt":"scope lane b","allowed_tools":["Read"]},` +
	`{"id":"qa-b","depends_on":["scope-b"],"prompt":"write QA-PLAN-b.md","allowed_tools":["Write"]},` +
	`{"id":"load-b","depends_on":["scope-b"],"prompt":"write load-b.js","allowed_tools":["Write"]},` +
	`{"id":"review-b","depends_on":["qa-b","load-b"],"prompt":"judge lane b",` +
	`"allowed_tools":["Read"],"success_check":{"result_matches":"^PASS$"},"feedback":{"rerun":"load-b","max":2}}]}`

// TestValidatePlannedNodes_ReachLeadsQuotingLeadsPerNode pins the ORDER the
// comment in validatePlannedNodes now calls load-bearing. It was asserted for
// the reach family alone (TestPlan_ReachRefusalSurvivesACrowdedRepairPrompt);
// the claim that reach also leads QUOTING — because a mis-aimed arc makes the
// quote moot, the token would be pasted into a node the loop never re-runs —
// had no probe on either side of the boundary.
//
// Asserted on the refusal list itself rather than through a rendered prompt, so
// a reordering fails here precisely instead of showing up as a truncation
// somewhere downstream.
func TestValidatePlannedNodes_ReachLeadsQuotingLeadsPerNode(t *testing.T) {
	// One per-node fault too (an empty prompt), so all three tiers are present.
	spec := strings.Replace(twoBrokenLanesSpec, `"prompt":"scope lane b"`, `"prompt":" "`, 1)
	g, err := graph.Parse([]byte(spec))
	if err != nil {
		t.Fatalf("the fixture must LOAD — every ADR 0010 rule holds, which is the point: %v", err)
	}

	issues := validatePlannedNodes(g, spec)
	if len(issues) != 4 {
		t.Fatalf("want 2 reach + 1 compacted quoting + 1 per-node refusal, got %d: %v", len(issues), reasons(issues))
	}
	for i, want := range []string{"whose loop body excludes", "whose loop body excludes", "nothing in their loop bodies quotes", "empty prompt"} {
		if !strings.Contains(issues[i].Reason, want) {
			t.Errorf("issue %d does not carry %q — the order graph-level-first, reach-before-quoting, per-node-last is broken:\n%s",
				i, want, strings.Join(reasons(issues), "\n"))
		}
	}
}

// TestRepairPromptHoldsBothGraphLevelRefusalFamilies is the byte-budget probe.
//
// The repair prompt is the ONE chance a refused plan gets (maxPlanRepairAttempts
// is 1), and a fault it never states is a fault the corrected reply re-commits —
// the plan is then refused a second time and the run the user paid for is gone.
// Leading the list is not enough on its own: both families lead it, both scale
// with the number of declarers, and so they can crowd EACH OTHER out. Two
// declarers that are each mis-aimed and blind rendered 2541 bytes into what was
// a 2000-byte budget, and the head-only cut left one family half-written and the
// rest silently missing.
//
// The five padded per-node refusals are what make this a real crowd rather
// than a hair's breadth: compacted, the two families alone render 1998 bytes,
// which fitted the old 2000-byte budget with two bytes to spare — one
// per-node slip beside them and a family was cut. (Uncompacted, which is what
// this branch replaced, the same two lanes rendered 2541 and a family was cut
// with no company at all.) A plan wrong about its arcs is rarely right about
// everything else, so the fixture is the two lanes plus ordinary company.
//
// What is asserted is the property, not the arithmetic: every id the planner
// must act on survives into the prompt, and nothing was cut.
func TestRepairPromptHoldsBothGraphLevelRefusalFamilies(t *testing.T) {
	crowded := strings.TrimSuffix(twoBrokenLanesSpec, "]}")
	for i := 0; i < 5; i++ {
		crowded += fmt.Sprintf(`,{"id":"pad-%02d","prompt":"pad","allowed_tools":["Read"],"permission_mode":"bypassPermissions"}`, i)
	}
	crowded += "]}"

	fake, repairPrompt := newRepairFake(
		runner.NodeOutcome{Result: crowded, TotalCostUSD: 0.02},
		runner.NodeOutcome{Result: crowded, TotalCostUSD: 0.03},
	)

	// The second reply repeats the graph, so Plan still fails — the assertion is
	// on what the one repair call was told.
	if _, err := New(fake).Plan(context.Background(), "canary two staging lanes", nil); err == nil {
		t.Fatal("the repaired reply repeats both arcs, so the plan must still fail")
	}

	// The reach half for BOTH lanes: the unreachable producer and the covering
	// target the planner is told to aim at.
	// The quoting half for BOTH lanes: the exact token to paste.
	for _, want := range []string{
		"qa-a", "scope-a", "qa-b", "scope-b",
		"{{ feedback.review-a }}", "{{ feedback.review-b }}",
		"pad-00", "pad-04",
	} {
		if !strings.Contains(repairPrompt.Prompt, want) {
			t.Errorf("the repair prompt lost %q, so that half of the fault is never stated:\n%s", want, repairPrompt.Prompt)
		}
	}
	if strings.Contains(repairPrompt.Prompt, fence.TruncateMarker) {
		t.Errorf("the repair prompt was cut mid-refusal — the budget no longer holds two declarers faulty both ways:\n%s", repairPrompt.Prompt)
	}
	if strings.Contains(repairPrompt.Prompt, "could not fit in this prompt") {
		t.Errorf("a refusal was dropped for want of budget on a two-declarer graph:\n%s", repairPrompt.Prompt)
	}
}

// TestPlan_TwoBlindArcsCostOneRefusal pins the compaction that makes the budget
// above hold. Every blind arc in a graph is one refusal naming every pair, not
// one refusal per declarer repeating a ~530-byte paragraph — and the compacted
// sentence must still name BOTH ends of BOTH pairs, or the saving was bought by
// making the correction unactionable.
func TestPlan_TwoBlindArcsCostOneRefusal(t *testing.T) {
	g, err := graph.Parse([]byte(twoBrokenLanesSpec))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}

	issues := validatePlannedFeedbackQuoting(g)
	if len(issues) != 1 {
		t.Fatalf("two blind arcs must render ONE refusal, got %d: %v", len(issues), reasons(issues))
	}
	for _, want := range []string{
		`"review-a"`, `"review-b"`, "{{ feedback.review-a }}", "{{ feedback.review-b }}",
		`"load-a"'s prompt`, `"load-b"'s prompt`, "empty on the first pass",
	} {
		if !strings.Contains(issues[0].Reason, want) {
			t.Errorf("the compacted refusal does not carry %q:\n%s", want, issues[0].Reason)
		}
	}
	// English, not a template: this sentence is a PROMPT the one repair call has
	// to understand, so the plural path is held to reading correctly. Every
	// clause referring back to the list is checked, not only the first two — a
	// leftover singular possessive in the PLACEMENT instruction ("after that
	// node's verdict contract", beside a plural "each of those prompts") names a
	// node the planner has to guess at, which is the half of the sentence it
	// actually has to act on.
	for _, singular := range []string{"declares a feedback arc", "its loop body", "its payload", "that node's", "that prompt"} {
		if strings.Contains(issues[0].Reason, singular) {
			t.Errorf("the two-arc refusal reads in the singular at %q:\n%s", singular, issues[0].Reason)
		}
	}
	if !strings.Contains(issues[0].Reason, "those nodes' verdict contract") {
		t.Errorf("the plural placement instruction does not name whose verdict contract to paste after:\n%s", issues[0].Reason)
	}
}

// brokenLanesSpec is twoBrokenLanesSpec widened to n lanes, each broken both
// ways exactly as lanes a and b are. The extra ids are the same length as
// theirs, so the per-lane byte costs the comments quote scale without a caveat.
func brokenLanesSpec(n int) string {
	spec := twoBrokenLanesSpec
	for i := 2; i < n; i++ {
		lane := string(rune('a' + i))
		spec = strings.TrimSuffix(spec, "]}") + fmt.Sprintf(
			`,{"id":"scope-%[1]s","prompt":"scope lane %[1]s","allowed_tools":["Read"]}`+
				`,{"id":"qa-%[1]s","depends_on":["scope-%[1]s"],"prompt":"write QA-PLAN-%[1]s.md","allowed_tools":["Write"]}`+
				`,{"id":"load-%[1]s","depends_on":["scope-%[1]s"],"prompt":"write load-%[1]s.js","allowed_tools":["Write"]}`+
				`,{"id":"review-%[1]s","depends_on":["qa-%[1]s","load-%[1]s"],"prompt":"judge lane %[1]s",`+
				`"allowed_tools":["Read"],"success_check":{"result_matches":"^PASS$"},"feedback":{"rerun":"load-%[1]s","max":2}}`,
			lane) + "]}"
	}
	return spec
}

// TestGraphLevelRefusalFamiliesRenderTheirMeasuredSize is the ADDRESS for every
// byte figure the comments around this budget quote — maxIssuesInPrompt's
// sizing paragraph, the ordering comment in validatePlannedNodes, the
// compaction note at validatePlannedFeedbackQuoting, ADR 0028 §Failure modes
// and the CHANGELOG entry. Those numbers are the only argument for
// maxIssuesInPrompt being 3000 rather than a round guess, and while they were
// estimates this one rendering was described at three different sizes in three
// files at once, one of which said a family had been cut where the measurement
// says it fitted.
//
// It is deliberately exact, and a reworded refusal is EXPECTED to fail it: the
// fix is to read the new numbers off this failure and carry them to the
// comments above, which is the entire reason they are pinned rather than
// re-estimated. carryTheNumbers says that IN the failure, because an
// instruction that lives only in a comment above the test is not read by the
// author who is looking at a red line in a terminal. Nothing here asserts
// behaviour — the behaviour is
// TestRepairPromptHoldsBothGraphLevelRefusalFamilies' subject.
const carryTheNumbers = "This test is a MEASUREMENT, not a contract: if you reworded a refusal on purpose, " +
	"the fix is to copy the measured numbers above into the four places that quote them — " +
	"maxIssuesInPrompt's sizing paragraph in repair.go, the validatePlannedNodes and " +
	"validatePlannedFeedbackQuoting comments in coordinator.go, " +
	"docs/adr/0028-a-feedback-arc-and-its-quote-are-one-mechanism.md and CHANGELOG.md — " +
	"and to update the table in this test."

func TestGraphLevelRefusalFamiliesRenderTheirMeasuredSize(t *testing.T) {
	for _, tc := range []struct{ lanes, reachEach, quoting, joined int }{
		{lanes: 2, reachEach: 677, quoting: 642, joined: 1998},
		{lanes: 3, reachEach: 677, quoting: 702, joined: 2736},
		{lanes: 4, reachEach: 677, quoting: 762, joined: 3474},
	} {
		g, err := graph.Parse([]byte(brokenLanesSpec(tc.lanes)))
		if err != nil {
			t.Fatalf("%d lanes: the fixture must LOAD: %v", tc.lanes, err)
		}
		reach := validatePlannedFeedbackReach(g)
		if len(reach) != tc.lanes {
			t.Fatalf("%d lanes: want one reach refusal per declarer, got %d", tc.lanes, len(reach))
		}
		for _, issue := range reach {
			if got := len(issue.Reason); got != tc.reachEach {
				t.Errorf("%d lanes: a reach refusal renders %d bytes, the comments say %d.\n%s", tc.lanes, got, tc.reachEach, carryTheNumbers)
			}
		}
		quoting := validatePlannedFeedbackQuoting(g)
		if len(quoting) != 1 {
			t.Fatalf("%d lanes: the quoting family is compacted to ONE refusal, got %d", tc.lanes, len(quoting))
		}
		if got := len(quoting[0].Reason); got != tc.quoting {
			t.Errorf("%d lanes: the compacted quoting refusal renders %d bytes, the comments say %d.\n%s", tc.lanes, got, tc.quoting, carryTheNumbers)
		}
		if got := len(strings.Join(reasons(validatePlannedNodes(g, "")), "\n")); got != tc.joined {
			t.Errorf("%d lanes: the two families joined render %d bytes, the comments say %d.\n%s", tc.lanes, got, tc.joined, carryTheNumbers)
		}
		// The claim the budget itself rests on: three such declarers fit, with
		// room left for the per-node refusals that travel with them.
		if tc.lanes == 3 && tc.joined >= maxIssuesInPrompt {
			t.Errorf("maxIssuesInPrompt (%d) no longer holds three declarers faulty both ways (%d bytes), which is what it was sized for",
				maxIssuesInPrompt, tc.joined)
		}
	}
}

// TestIssuesForPrompt_KeepsWholeRefusalsAndDisclosesTheRest is the bound past
// the budget. A list that does not fit is cut somewhere; where it is cut is the
// whole question.
//
// Head-only truncation cut at a BYTE, which left the last kept refusal ending
// mid-sentence — an instruction to guess — and dropped every later one with no
// trace, so the prompt read as if it were the complete list. Whole refusals and
// a stated count are what make the loss legible to the reader that has to act
// on it.
// The sizes are derived from maxIssuesInPrompt rather than written down, so
// this stays a test of the packing rule and not of today's budget: eight
// quarter-budget refusals overrun it whatever it is.
func TestIssuesForPrompt_KeepsWholeRefusalsAndDisclosesTheRest(t *testing.T) {
	const count = 8
	issues := make([]string, 0, count)
	for i := 0; i < count; i++ {
		issues = append(issues, fmt.Sprintf("refusal %d: %s.", i, strings.Repeat("x", maxIssuesInPrompt/4)))
	}

	rendered := issuesForPrompt(issues)
	if len(rendered) > maxIssuesInPrompt {
		t.Fatalf("rendered %d bytes, over the %d budget", len(rendered), maxIssuesInPrompt)
	}
	if strings.Contains(rendered, fence.TruncateMarker) {
		t.Errorf("a refusal was cut mid-sentence rather than dropped whole:\n%s", rendered)
	}
	kept := 0
	for _, line := range strings.Split(rendered, "\n") {
		if !strings.HasPrefix(line, "refusal ") {
			continue
		}
		kept++
		if !strings.HasSuffix(line, ".") {
			t.Errorf("kept a partial refusal, which names a node and stops before the correction: %.40s", line)
		}
	}
	if kept == 0 || kept == count {
		t.Fatalf("the fixture must overrun the budget and keep some of it, kept %d of %d", kept, count)
	}
	// The disclosure must AGREE with what was dropped — a count that drifts from
	// the list is the same defect as no count at all.
	if want := fmt.Sprintf("%d further refusals could not fit", count-kept); !strings.Contains(rendered, want) {
		t.Errorf("the drop is not disclosed as %q, so the prompt reads as the complete list:\n%s", want, rendered)
	}
	if !strings.HasPrefix(rendered, "refusal 0:") {
		t.Error("the list was not kept from the FRONT, where validatePlannedNodes puts the graph-level refusals")
	}
}

// TestIssuesForPrompt_KeepsAListThatFitsVerbatim is the negative control: the
// packer must be invisible on every list that fits, which is every list the
// corpus has produced.
func TestIssuesForPrompt_KeepsAListThatFitsVerbatim(t *testing.T) {
	issues := []string{"first refusal.", "second refusal."}
	if got, want := issuesForPrompt(issues), "first refusal.\nsecond refusal."; got != want {
		t.Errorf("issuesForPrompt() = %q, want %q", got, want)
	}
}

// TestIssuesForPrompt_BoundsASingleOverlongRefusal covers the case no packing
// can help: one refusal alone larger than the whole budget. There is no honest
// shorter rendering, so it is cut the old way — and the marker is what says so.
func TestIssuesForPrompt_BoundsASingleOverlongRefusal(t *testing.T) {
	rendered := issuesForPrompt([]string{strings.Repeat("y", maxIssuesInPrompt+500)})
	if len(rendered) > maxIssuesInPrompt {
		t.Fatalf("rendered %d bytes, over the %d budget", len(rendered), maxIssuesInPrompt)
	}
	if !strings.Contains(rendered, fence.TruncateMarker) {
		t.Error("an unavoidable cut must announce itself")
	}
}

func reasons(issues []*PlanError) []string {
	out := make([]string, 0, len(issues))
	for _, issue := range issues {
		out = append(out, issue.Reason)
	}
	return out
}

// TestIssuesForPrompt_FirstRefusalTooLongStillDisclosesTheRest is the edge the
// kept-count loop reaches when the FIRST refusal fits alone but not alongside
// the omission note.
//
// The earlier code truncated the JOINED list there, so the later refusals
// disappeared and their count disappeared with them: the planner was told less
// than it had been refused for, and nothing said so. That is the exact failure
// issuesForPrompt exists to prevent, reached through its own boundary — which
// is why this case is pinned rather than left to the general test above.
//
// The second refusal is written in the validator's own shape — a diagnosis, an
// em dash, then the correction — because the disclosure now names the class of
// what it dropped: the diagnosis half travels in the note, the correction half
// does not. Its two halves are asserted separately for that reason, where this
// test once asserted only that the whole sentence was absent.
func TestIssuesForPrompt_FirstRefusalTooLongStillDisclosesTheRest(t *testing.T) {
	// Fits inside the budget alone; cannot fit once the note is reserved.
	first := "first: " + strings.Repeat("x", maxIssuesInPrompt-10)
	issues := []string{first, "second: a refusal the planner must know exists — aim the arc at the other node."}

	got := issuesForPrompt(issues)

	if len(got) > maxIssuesInPrompt {
		t.Fatalf("rendered %d bytes, over the %d budget", len(got), maxIssuesInPrompt)
	}
	if !strings.Contains(got, "1 further refusal") {
		t.Errorf("the omitted refusal is not disclosed:\n%s", got[max(0, len(got)-200):])
	}
	if !strings.Contains(got, "second: a refusal the planner must know exists") {
		t.Errorf("the dropped refusal is disclosed as a bare count, so nothing says what it was about:\n%s", got[max(0, len(got)-300):])
	}
	if strings.Contains(got, "aim the arc at the other node") {
		t.Error("the second refusal was kept whole; this case is about disclosing its class, not keeping its correction")
	}
	if !strings.HasPrefix(got, "first: ") {
		t.Error("the first refusal's head was lost")
	}
}

// strandedArtifactSpec is n nodes each quoting an artifact of a node they do
// not depend on — #244's corpus shape (docs/measurements/0244-auto-path-sweeps.md),
// widened so the per-fault byte cost the comments quote can be read off a
// series rather than estimated. Every id is the same length, so the series
// scales without a caveat.
func strandedArtifactSpec(n int) string {
	spec := `{"name":"stranded","version":"1","nodes":[` +
		`{"id":"corpus","prompt":"count the runs","allowed_tools":["Read"]}`
	for i := 0; i < n; i++ {
		spec += fmt.Sprintf(`,{"id":"writeup-%02d","prompt":"write it up from {{ artifacts.corpus }}","allowed_tools":["Edit"]}`, i)
	}
	return spec + "]}"
}

// TestArtifactRefusalRendersItsMeasuredSize is the third family's entry in the
// measurement maxIssuesInPrompt is sized against — the "a third family fails
// here rather than in a paid run" case its comment named before one existed.
// Read it exactly as TestGraphLevelRefusalFamiliesRenderTheirMeasuredSize is
// read: a reworded refusal is EXPECTED to fail, and the fix is to carry the new
// numbers to the comment that quotes them.
//
// The shape of the series is the claim: one shared paragraph (~754 bytes) plus
// ~166 per fault, which is what makes six faults — the worst graph in the
// corpus — cost 1762 rather than the ~5000 a per-token refusal would have spent
// in a prompt capped at maxIssuesInPrompt.
func TestArtifactRefusalRendersItsMeasuredSize(t *testing.T) {
	for _, tc := range []struct{ faults, bytes int }{
		{faults: 1, bytes: 920},
		{faults: 2, bytes: 1098},
		{faults: 3, bytes: 1264},
		{faults: 6, bytes: 1762},
	} {
		g, err := graph.Parse([]byte(strandedArtifactSpec(tc.faults)))
		if err != nil {
			t.Fatalf("%d faults: the fixture must LOAD — a stranded artifact token is VALID, which is the point: %v", tc.faults, err)
		}
		issues := validatePlannedArtifactReferences(g)
		if len(issues) != 1 {
			t.Fatalf("%d faults: the family is compacted to ONE refusal, got %d", tc.faults, len(issues))
		}
		if got := len(issues[0].Reason); got != tc.bytes {
			t.Errorf("%d faults: the artifact refusal renders %d bytes, the comments say %d.\n%s", tc.faults, got, tc.bytes, carryTheNumbers)
		}
	}
}

// TestArtifactRefusalIsTheOneDroppedWhenAllThreeFamiliesFire pins the tie-break
// the ordering in validatePlannedNodes makes, on the one graph where the three
// graph-level families together overrun the budget: three lanes each mis-aimed
// AND blind, plus three stranded artifact tokens, renders past
// maxIssuesInPrompt, and something has to go.
//
// It is the artifact refusal, deliberately, because a dropped feedback refusal
// buys silence — a loop that runs every round it was given, produces the same
// output each time, and passes the money through — while this one's graph is
// stopped at the plan either way.
//
// The reason this comment used to give was that the ENGINE states the artifact
// fault for itself at run time. It does not: validatePlannedArtifactReferences
// refuses the plan, so that graph has no run time, and under `auto` there is
// nobody reading the screen either (docs/measurements/0244-auto-path-sweeps.md).
// The tie-break survived the correction; the argument for it did not, which is
// why the drop now leaves the class behind — see
// TestDroppedArtifactRefusalStillNamesItsClassInTheRepairPrompt.
//
// What is asserted here is which refusal takes the cut and that the loss is
// legible: the kept refusals are whole, the count is stated, and the dropped
// refusal's CORRECTION — the half a planner acts on — is what is gone.
func TestArtifactRefusalIsTheOneDroppedWhenAllThreeFamiliesFire(t *testing.T) {
	g, err := graph.Parse([]byte(threeFamiliesSpec()))
	if err != nil {
		t.Fatalf("the fixture must LOAD: %v", err)
	}

	issues := reasons(validatePlannedNodes(g, ""))
	if got := len(strings.Join(issues, "\n")); got <= maxIssuesInPrompt {
		t.Fatalf("the fixture no longer overruns the budget (%d bytes of %d) — it exists to be cut", got, maxIssuesInPrompt)
	}

	rendered := issuesForPrompt(issues)
	for _, gone := range []string{"break the two braces apart", "put it in this node's depends_on"} {
		if strings.Contains(rendered, gone) {
			t.Errorf("the artifact refusal survived a cut the ordering says it takes (%q is its correction); a feedback refusal was dropped instead:\n%s", gone, rendered)
		}
	}
	for _, want := range []string{"whose loop body excludes", "nothing in their loop bodies quotes", "1 further refusal could not fit"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the repair prompt lost %q, so the cut was not the one the ordering describes:\n%s", want, rendered)
		}
	}
}

// threeFamiliesSpec is brokenLanesSpec(3) — three lanes each mis-aimed AND
// blind — plus the three stranded `{{ artifacts.corpus }}` tokens that make the
// #244 family fire beside them. It is the graph maxIssuesInPrompt's sizing
// paragraph describes, and the only shape the corpus and the validator together
// produce where all three graph-level families overrun the budget at once.
//
// It is shared by the two tests that read that overrun from opposite ends —
// which refusal takes the cut, and what the planner is left holding — so that a
// change to the shape cannot make one of them true and the other vacuous.
func threeFamiliesSpec() string {
	return strings.TrimSuffix(brokenLanesSpec(3), "]}") +
		`,{"id":"corpus","prompt":"count","allowed_tools":["Read"]}` +
		`,{"id":"w-1","prompt":"use {{ artifacts.corpus }}","allowed_tools":["Read"]}` +
		`,{"id":"w-2","prompt":"use {{ artifacts.corpus }}","allowed_tools":["Read"]}` +
		`,{"id":"w-3","prompt":"use {{ artifacts.corpus }}","allowed_tools":["Read"]}]}`
}

// TestDroppedArtifactRefusalStillNamesItsClassInTheRepairPrompt is the
// guarantee in its name: the refusal the budget drops is still named, by class,
// in the prompt the one repair call (maxPlanRepairAttempts) is written against.
//
// The test above pins WHICH refusal is dropped. This one pins what that costs,
// end to end through a rendered prompt rather than through issuesForPrompt,
// because the prompt is the only model-facing route the content has: the plan
// is refused at validation, so it never runs, and nothing else carries a plan
// refusal to a planner. A count alone left the planner correcting the two
// feedback families and re-emitting the artifact fault, which is refused a
// second time — two planner calls, nothing executed.
//
// The over-budget case is CONSTRUCTED, not hoped for, and its size is asserted
// first: 3980 bytes of rendered refusal text against a 3000-byte budget. That
// figure is quoted in maxIssuesInPrompt's comment and had no test address
// before this one.
func TestDroppedArtifactRefusalStillNamesItsClassInTheRepairPrompt(t *testing.T) {
	spec := threeFamiliesSpec()
	g, err := graph.Parse([]byte(spec))
	if err != nil {
		t.Fatalf("the fixture must LOAD: %v", err)
	}
	if got := len(strings.Join(reasons(validatePlannedNodes(g, "")), "\n")); got != 3980 {
		t.Fatalf("the three families render %d bytes of refusal text, the comments say 3980.\n%s", got, carryTheNumbers)
	}

	fake, repairPrompt := newRepairFake(
		runner.NodeOutcome{Result: spec, TotalCostUSD: 0.02},
		runner.NodeOutcome{Result: spec, TotalCostUSD: 0.03},
	)

	// The second reply repeats the graph, so Plan still fails — the assertion is
	// on what the one repair call was told.
	if _, err := New(fake).Plan(context.Background(), "three staging lanes reading one corpus", nil); err == nil {
		t.Fatal("the repaired reply repeats every fault, so the plan must still fail")
	}
	if repairPrompt.Prompt == "" {
		t.Fatal("no repair call was made, so there is no prompt to assert on")
	}

	// The drop is still a drop: the two feedback families are quoted whole, and
	// the artifact refusal is not.
	for _, want := range []string{"whose loop body excludes", "nothing in their loop bodies quotes", "1 further refusal could not fit"} {
		if !strings.Contains(repairPrompt.Prompt, want) {
			t.Errorf("the repair prompt lost %q, so the cut was not the one the ordering describes:\n%s", want, repairPrompt.Prompt)
		}
	}
	if strings.Contains(repairPrompt.Prompt, "break the two braces apart") {
		t.Error("the artifact refusal was quoted whole; this test is about what survives when it is NOT:\n" + repairPrompt.Prompt)
	}

	// The guarantee: what was dropped is named, so the planner knows the graph
	// has an artifact fault in it even though it cannot read the correction.
	if !strings.Contains(repairPrompt.Prompt, "not guaranteed to exist when they run") {
		t.Errorf("the dropped refusal is disclosed as a bare count, so the one corrected reply is written by a planner that was never told this class exists:\n%s", repairPrompt.Prompt)
	}
	if strings.Contains(repairPrompt.Prompt, fence.TruncateMarker) {
		t.Errorf("a kept refusal was cut mid-sentence; the disclosure is meant to cost the budget a line, not a refusal:\n%s", repairPrompt.Prompt)
	}
}
