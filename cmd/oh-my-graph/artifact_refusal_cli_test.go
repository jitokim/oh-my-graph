package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// nonAncestorArtifactAutoSpec is the corpus's own shape (docs/measurements/
// 0244-auto-path-sweeps.md, run 20260820-162555): "writeup" quotes
// "corpus"'s artifact without depending on it. The graph is otherwise valid —
// it is exactly the reply the planner emitted, and it planned, ran and died at
// `cannot resolve {{ artifacts.corpus }}: artifact not available`.
//
// This is the class validatePlannedArtifactReferences (coordinator.go:1358)
// escalates to a plan REFUSAL rather than the plan screen's advisory: the
// token is one of the three shapes handoff.Interpolate always fails on, so
// the refusal cannot be wrong about the outcome. It never reaches
// printPlanForRuntime's warnAdvisories line — the run never gets that far.
const nonAncestorArtifactAutoSpec = `{"name":"corpus-writeup","version":"1","nodes":[` +
	`{"id":"corpus","prompt":"count the runs","allowed_tools":["Read"]},` +
	`{"id":"writeup","prompt":"write it up from {{ artifacts.corpus }}","allowed_tools":["Edit"]}]}`

// ancestorArtifactAutoSpec is the same graph with the one correction the
// refusal itself offers: "writeup" now depends_on "corpus", so the token
// resolves and the plan is not the planner's fault.
const ancestorArtifactAutoSpec = `{"name":"corpus-writeup","version":"1","nodes":[` +
	`{"id":"corpus","prompt":"count the runs","allowed_tools":["Read"]},` +
	`{"id":"writeup","depends_on":["corpus"],"prompt":"write it up from {{ artifacts.corpus }}","allowed_tools":["Edit"]}]}`

// TestPlanScreenWarnsUnresolvablePlaceholder is #244's third escalation,
// driven at the CLI layer through internal/runner.FakeRunner exactly as
// replan_test.go's own refusal tests do (newCycleFake, runAutoWith,
// captureStdout) — no real claude or codex spawn.
//
// The instruction that produced this test asked for the plan screen's
// `warning:` line. For THIS class it does not print one: the previous node's
// report and internal/coordinator/artifact_reference_test.go both pin the
// same fact — a non-ancestor artifact reference is refused before
// printPlanForRuntime ever runs, so the auto path fails the plan outright and
// the refusal text (quoting handoff's own advisory sentence) is what a reader
// sees instead. That is what this test asserts is present.
func TestPlanScreenWarnsUnresolvablePlaceholder(t *testing.T) {
	isolateRunHome(t)
	// The refusal is repairable (validatePlannedArtifactReferences is one of
	// the three graph-level families that buys the one re-plan), and the
	// planner's correction here repeats the same defect, so plan-2 is scripted
	// too — the loop is bounded at maxPlanRepairAttempts (1) and must not ask
	// for a third.
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: nonAncestorArtifactAutoSpec, TotalCostUSD: 0.02},
		"plan-2": {Result: nonAncestorArtifactAutoSpec, TotalCostUSD: 0.02},
	})

	var err error
	captureStdout(t, func() {
		err = runAutoWith([]string{"measure the corpus and write it up", "--plan-only", "--no-agent-mapping", "--no-skill-mapping"},
			fake, browser.NewFakeOpener(), os.Stdout)
	})

	var rejection *coordinator.PlanRejection
	if !errors.As(err, &rejection) {
		t.Fatalf("a node quoting an artifact of a node it does not depend on must be refused, got %T: %v", err, err)
	}
	if got := len(fake.Invocations()); got != 2 {
		t.Fatalf("made %d planner call(s), want exactly 2 — the refusal and its one bought correction", got)
	}
	for _, want := range []string{
		`"writeup"`,
		"{{ artifacts.corpus }}",
		"not an ancestor of this node",
		"depends_on",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not carry %q:\n%s", want, err.Error())
		}
	}
}

// TestPlanScreenWarnsUnresolvablePlaceholder's negative half: the same graph
// with the token's own correction applied — "writeup" now depends on
// "corpus" — plans clean in one call and carries none of the refusal's
// wording. A test that only ever sees the refusal cannot tell it is wired to
// the predicate rather than to every planned graph.
func TestPlanScreenSilentWhenPlaceholderResolves(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: ancestorArtifactAutoSpec, TotalCostUSD: 0.02},
	})

	var err error
	out := captureStdout(t, func() {
		err = runAutoWith([]string{"measure the corpus and write it up", "--plan-only", "--no-agent-mapping", "--no-skill-mapping"},
			fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("a token naming an ancestor is the wiring the prompt asks for and must plan, got: %v", err)
	}
	if got := len(fake.Invocations()); got != 1 {
		t.Errorf("made %d planner call(s), want exactly 1 — nothing to repair", got)
	}
	if strings.Contains(out, "not an ancestor of this node") {
		t.Errorf("a resolvable artifact reference must not carry the refusal's wording:\n%s", out)
	}
}
