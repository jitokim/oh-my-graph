package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// capturingRunner records the invocation each node was launched with, so a
// wiring test can assert on what actually reached the runner seam. It never
// spawns anything.
type capturingRunner struct {
	mu      sync.Mutex
	invoked map[string]runner.NodeInvocation
}

func (r *capturingRunner) Run(_ context.Context, spec runner.NodeInvocation) (runner.NodeOutcome, error) {
	r.mu.Lock()
	if r.invoked == nil {
		r.invoked = make(map[string]runner.NodeInvocation)
	}
	r.invoked[spec.Prompt] = spec
	r.mu.Unlock()
	return runner.NodeOutcome{SessionID: "s-" + spec.Prompt, Result: "PASS", ExitCode: 0}, nil
}

func (r *capturingRunner) invocationFor(prompt string) runner.NodeInvocation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.invoked[prompt]
}

func mustParse(t *testing.T, spec string) *graph.Graph {
	t.Helper()
	g, err := graph.Parse([]byte(spec))
	if err != nil {
		t.Fatalf("parse graph: %v", err)
	}
	return g
}

// TestExecutePlan_CarriesTheCeilingIntoEveryNode covers the hop that makes auto
// mode's tool ceiling real: the planned graph reaching the scheduler together
// with its policies. The coordinator building a ceiling and the scheduler
// forwarding one are each tested in their own package, but neither notices if
// this hop drops it — the suite would stay green while every planned node
// silently ran under the user's own standing grants. This pins the hop, and it
// checks the ISOLATION layer specifically, because that is the one whose
// absence looks like nothing at all in an argv.
func TestExecutePlan_CarriesTheCeilingIntoEveryNode(t *testing.T) {
	g := mustParse(t, `{"name":"planned","nodes":[
		{"id":"scan","prompt":"scan","allowed_tools":["Read"]},
		{"id":"edit","prompt":"edit","depends_on":["scan"],"allowed_tools":["Edit"]}]}`)
	none := ""
	ceiling := map[string]runner.ToolPolicy{
		"scan": {AllowedTools: []string{"Read"}, DisallowedTools: []string{"Bash", "Write"}, Tools: []string{"Read"}, SettingSources: &none, StrictMCPConfig: true},
		"edit": {AllowedTools: []string{"Edit"}, DisallowedTools: []string{"Bash", "WebFetch"}, Tools: []string{"Edit"}, SettingSources: &none, StrictMCPConfig: true},
	}
	rec := &capturingRunner{}
	plan := coordinator.Plan{Graph: g, ToolPolicies: ceiling}

	// executeGraph writes its run directory under $OMG_HOME, so isolate it
	// instead of littering the real home with artifacts.
	isolateRunHome(t)
	err := executePlan(context.Background(), "test-run", plan, rec, commonRunFlags{inputs: inputFlag{}}, "graph.json", nil, nil)
	if err != nil {
		t.Fatalf("executePlan returned error: %v", err)
	}

	for _, node := range []string{"scan", "edit"} {
		policy := rec.invocationFor(node).Policy
		want := ceiling[node]
		if strings.Join(policy.DisallowedTools, ",") != strings.Join(want.DisallowedTools, ",") {
			t.Errorf("node %q ran with deny list %v, want %v", node, policy.DisallowedTools, want.DisallowedTools)
		}
		if policy.SettingSources == nil || *policy.SettingSources != "" {
			t.Errorf("node %q lost settings isolation on the way to the runner", node)
		}
		if !policy.StrictMCPConfig || len(policy.Tools) == 0 {
			t.Errorf("node %q lost a ceiling layer on the way to the runner: %+v", node, policy)
		}
	}
}

// TestExecuteGraph_HandWrittenPathImposesNoCeiling is the other half: the `run`
// subcommand passes nil, and that must reach the runner as "no ceiling" so a
// hand-written graph keeps running under the user's own settings, hooks and MCP
// servers exactly as it did before this guard existed.
func TestExecuteGraph_HandWrittenPathImposesNoCeiling(t *testing.T) {
	g := mustParse(t, `{"name":"handwritten","nodes":[{"id":"only","prompt":"only","allowed_tools":["Read"]}]}`)
	rec := &capturingRunner{}

	isolateRunHome(t)
	err := executeGraph(context.Background(), "test-run", g, rec, commonRunFlags{inputs: inputFlag{}}, nil, 0, "handwritten.yaml", []byte("name: handwritten\n"), nil, nil)
	if err != nil {
		t.Fatalf("executeGraph returned error: %v", err)
	}
	policy := rec.invocationFor("only").Policy
	if len(policy.DisallowedTools) != 0 {
		t.Errorf("hand-written node ran with deny list %v, want none", policy.DisallowedTools)
	}
	if policy.SettingSources != nil {
		t.Errorf("the `run` path must never disable the user's own settings, got %q", *policy.SettingSources)
	}
	if policy.StrictMCPConfig || policy.Tools != nil {
		t.Errorf("the `run` path must impose no narrowing, got %+v", policy)
	}
}

// barrierRunner holds every node's Run at a rendezvous until width nodes have
// arrived, then releases them all at once — so the scheduler goroutines'
// subsequent event-stream emits and snapshot writes genuinely overlap instead
// of trickling through one at a time. The wait blocks on the rendezvous
// channel or ctx.Done() only, deliberately with no wall-clock fallback
// (CONTRIBUTING, "Test doubles"): if the scheduler ever ran the lanes
// narrower than width, the rendezvous could not complete, and that genuine
// deadlock is go test's own timeout's job to report, naming the stuck line.
type barrierRunner struct {
	width   int
	release chan struct{}

	mu      sync.Mutex
	arrived int
}

func (r *barrierRunner) Run(ctx context.Context, spec runner.NodeInvocation) (runner.NodeOutcome, error) {
	r.mu.Lock()
	r.arrived++
	if r.arrived == r.width {
		close(r.release)
	}
	r.mu.Unlock()

	select {
	case <-r.release:
	case <-ctx.Done():
		return runner.NodeOutcome{}, ctx.Err()
	}
	return runner.NodeOutcome{SessionID: "s-" + spec.Prompt, Result: "PASS", ExitCode: 0}, nil
}

// countEvents counts the events of type eventType carrying nodeID — a count,
// not a bool, so the exactly-once assertions below cannot be satisfied by a
// node's events simply being absent.
func countEvents(events []runfeed.Event, eventType runfeed.EventType, nodeID string) int {
	n := 0
	for _, e := range events {
		if e.Type == eventType && e.NodeID == nodeID {
			n++
		}
	}
	return n
}

// TestExecuteGraph_ParallelFanOutWritesEachNodeOnceToBothWriters exists to
// give -race a window onto the two real writers' mutexes: executeGraph wires
// the scheduler to a real runfeed.StreamWriter and a real
// runstate.SnapshotRecorder, each of which serializes concurrent scheduler
// goroutines behind its own internal mutex — but every other test in this
// package that reaches both writers runs at most two nodes, in dependency
// order, so under -race those mutexes only ever see one writer at a time and
// the exclusion they exist for goes unexercised. Here a four-way fan-out is
// held at a rendezvous until all four nodes are in flight and then released
// together, so their terminal writes genuinely contend; the run's artifacts
// must still record every node exactly once in events.jsonl and exactly once
// in state.json.
func TestExecuteGraph_ParallelFanOutWritesEachNodeOnceToBothWriters(t *testing.T) {
	nodes := []string{"n1", "n2", "n3", "n4"}
	g := mustParse(t, `{"name":"fan-out","nodes":[
		{"id":"n1","prompt":"n1"},
		{"id":"n2","prompt":"n2"},
		{"id":"n3","prompt":"n3"},
		{"id":"n4","prompt":"n4"}]}`)
	rec := &barrierRunner{width: len(nodes), release: make(chan struct{})}

	isolateRunHome(t)
	// Concurrency is pinned to the fan-out width: the rendezvous only opens
	// once all four Runs are in flight, so a narrower ready set could never
	// reach it.
	err := executeGraph(context.Background(), "fan-out-run", g, rec,
		commonRunFlags{inputs: inputFlag{}, concurrency: len(nodes)},
		nil, 0, "fan-out.yaml", []byte("name: fan-out\n"), nil, nil)
	if err != nil {
		t.Fatalf("executeGraph returned error: %v", err)
	}

	events := readRunEvents(t, "fan-out-run")
	for _, node := range nodes {
		if got := countEvents(events, runfeed.EventNodeStarted, node); got != 1 {
			t.Errorf("node %q has %d node_started events, want exactly 1", node, got)
		}
		if got := countEvents(events, runfeed.EventNodePassed, node); got != 1 {
			t.Errorf("node %q has %d node_passed events, want exactly 1", node, got)
		}
	}

	snap, err := runstate.Load(filepath.Join(runDirFor("fan-out-run"), "state.json"))
	if err != nil {
		t.Fatalf("load state.json: %v", err)
	}
	if len(snap.Nodes) != len(nodes) {
		t.Errorf("state.json records %d nodes, want %d: %+v", len(snap.Nodes), len(nodes), snap.Nodes)
	}
	for _, node := range nodes {
		record, ok := snap.Nodes[node]
		if !ok {
			t.Errorf("node %q missing from state.json", node)
			continue
		}
		if record.Verdict != runstate.VerdictPass {
			t.Errorf("node %q recorded verdict %q in state.json, want %q", node, record.Verdict, runstate.VerdictPass)
		}
	}
}

// TestExecutePlan_TotalIncludesPlanningCost pins the end-to-end accounting the
// original bug (issue #15) slipped through: the coordinator computes the
// planning cost and printPlan shows it once up front, but executeGraph's ledger
// never received it, so the end-of-run TOTAL COST summed only the per-node
// costs. Numbers are the exact live-run figures from the issue — planning
// $0.6069 plus nodes $0.7977 and $0.5327 — so an honest total is $1.9373, not
// the $1.3304 node-only sum the pre-fix code printed. This is the hop no
// coordinator- or ledger-only test exercises; the suite would stay green while
// every auto run undercounted its real spend.
func TestExecutePlan_TotalIncludesPlanningCost(t *testing.T) {
	g := mustParse(t, `{"name":"haiku","nodes":[
		{"id":"write-haiku","prompt":"write-haiku","allowed_tools":["Read"]},
		{"id":"critique-haiku","prompt":"critique-haiku","allowed_tools":["Read"]}]}`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"write-haiku":    {SessionID: "s-write", Result: "PASS", TotalCostUSD: 0.7977, ExitCode: 0},
		"critique-haiku": {SessionID: "s-critique", Result: "PASS", TotalCostUSD: 0.5327, ExitCode: 0},
	})
	plan := coordinator.Plan{Graph: g, CostUSD: 0.6069}

	isolateRunHome(t)
	out := captureStdout(t, func() {
		if err := executePlan(context.Background(), "issue-15", plan, fake, commonRunFlags{inputs: inputFlag{}}, "graph.json", nil, nil); err != nil {
			t.Fatalf("executePlan returned error: %v", err)
		}
	})

	if !strings.Contains(out, "PLANNING COST: $0.6069") {
		t.Errorf("auto run must show the planning cost line:\n%s", out)
	}
	if !strings.Contains(out, "TOTAL COST: $1.9373") {
		t.Errorf("auto run's total must include planning cost (want $1.9373):\n%s", out)
	}
	if strings.Contains(out, "1.3304") {
		t.Errorf("total still shows the node-only sum $1.3304 — planning cost was dropped:\n%s", out)
	}
}

// captureStdout redirects os.Stdout for the duration of fn and returns what was
// written. executeGraph prints the ledger table straight to os.Stdout, so this
// is how a wiring test reads the total the user actually sees. (Scheduler
// progress goes to os.Stderr, so it does not pollute the capture.)
// It mutates the process-global os.Stdout, which is why cmd tests must never
// call t.Parallel.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	os.Stdout = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// TestNoteCeiling_StatesIsolationAndItsCost pins the honesty of the pre-run
// summary in BOTH directions, because it has now been wrong in one of them.
// It used to disclaim that a declared Bash scope was not enforced; measurement
// (DESIGN.md, E1) made that disclaimer false, and an out-of-date warning is
// how users learn to ignore warnings. The replacement has to keep stating the
// cost of the thing that made it true — planned nodes no longer see the user's
// CLAUDE.md, hooks or MCP servers.
func TestNoteCeiling_StatesIsolationAndItsCost(t *testing.T) {
	var out strings.Builder
	noteCeiling(&out)
	got := out.String()

	for _, want := range []string{"settings", "enforced", "hooks", "MCP"} {
		if !strings.Contains(got, want) {
			t.Errorf("pre-run note does not mention %q, so a user cannot know what running this plan does:\n%s", want, got)
		}
	}
	// The retired claim must not come back: it is measurably false for planned
	// nodes now, and re-asserting it would understate the ceiling rather than
	// overstate it — still a lie, just a conservative one.
	if strings.Contains(got, "not an enforced") {
		t.Errorf("pre-run note still claims declared scopes are unenforced:\n%s", got)
	}
}

// TestPrintPlan_ShowsAgentMappingsAndSkips pins the disclosure contract of
// subagent auto-mapping: every mapping made appears on its node's line, every
// refused candidate prints its reason, and an applied mapping's isolation
// trade (the node loads your settings so --agent can resolve) plus the
// opt-out flag are stated — because a mapping the human never saw before
// execution would defeat the reason the mapping lives in trusted code.
func TestPrintPlan_ShowsAgentMappingsAndSkips(t *testing.T) {
	g, err := graph.Parse([]byte(`{"name":"r","version":"1","nodes":[` +
		`{"id":"review","prompt":"p","agent":"code-reviewer"},` +
		`{"id":"scan","prompt":"p"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	plan := coordinator.Plan{Graph: g, AgentMappings: []coordinator.AgentMapping{
		{NodeID: "review", Agent: "code-reviewer"},
		{NodeID: "scan", Agent: "scanner", SkippedReason: "tools exceed ceiling: Bash"},
	}}

	var out strings.Builder
	printPlan(&out, plan, "/tmp/graph.json")
	got := out.String()

	for _, want := range []string{
		"[agent: code-reviewer]",
		`agent "scanner" not applied to scan: tools exceed ceiling: Bash`,
		"--no-agent-mapping",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("plan printout is missing %q:\n%s", want, got)
		}
	}
}
