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
	outcome := runner.NodeOutcome{SessionID: "s-" + spec.Prompt, Result: "PASS", ExitCode: 0}
	if spec.SessionStarted != nil {
		spec.SessionStarted(outcome.SessionID)
	}
	return outcome, nil
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
	err := executePlan(context.Background(), "test-run", plan, rec, commonRunFlags{inputs: inputFlag{}}, "graph.json", nil, nil, nil)
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
	err := executeGraph(context.Background(), "test-run", g, rec, commonRunFlags{inputs: inputFlag{}}, nil, 0, "handwritten.yaml", []byte("name: handwritten\n"), false, nil, nil, nil)
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

func TestRunGraphWithRuntime_CodexDisclosesAllowedToolsAreDeclarations(t *testing.T) {
	isolateRunHome(t)
	path := writeGraphFile(t, `name: handwritten
nodes:
  - id: only
    prompt: only
    allowed_tools: [Read, Edit]
`)
	rec := &capturingRunner{}

	var runErr error
	out := captureStdout(t, func() {
		runErr = runGraphWithRuntime(runner.RuntimeCodex, []string{path, "--no-web"}, rec, nil, os.Stdout)
	})
	if runErr != nil {
		t.Fatalf("Codex handwritten run returned error: %v", runErr)
	}
	// The `run` path prints the same disclosure `auto` does, minus the isolation
	// line, so it pins the same differences. Same reason as in planonly_test.go:
	// docs/LIMITATIONS.md points at this print as the mitigation.
	for _, want := range []string{
		"allowed_tools declarations do not become granular Codex permissions",
		"No network: a sandboxed node cannot reach it",
		"First node: apply-flags",
		"Every node: merge-shepherd",
		"Cost is unknown for every Codex node",
		`approval_policy="never" is passed on every node`,
		// Reversed by ADR 0009's 2026-09-02 amendment (#222): this line used to
		// read "No session-limit pause". Both halves are pinned — that the pause
		// applies, and that detection is still prose — because a disclosure that
		// promised the pause without the brittleness would be the new lie.
		"ADR 0009's resumable pause covers this runtime too",
		"exits 2 with a `resume --retry-failed` hint",
		"Detection is prose on both runtimes, not a typed signal",
		"hit your usage limit",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Codex handwritten run hid %q:\n%s", want, out)
		}
	}
	// The claim this replaced, kept as a negative so it cannot silently revert:
	// it was printed in the very same run that then printed "⏸ … pausing run".
	for _, wrong := range []string{"No session-limit pause", "resumable pause is Claude-only"} {
		if strings.Contains(out, wrong) {
			t.Errorf("Codex handwritten run still denies the pause it performs, %q:\n%s", wrong, out)
		}
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
	if spec.SessionStarted != nil {
		spec.SessionStarted("s-" + spec.Prompt)
	}
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
		nil, 0, "fan-out.yaml", []byte("name: fan-out\n"), false, nil, nil, nil)
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
		if err := executePlan(context.Background(), "issue-15", plan, fake, commonRunFlags{inputs: inputFlag{}}, "graph.json", nil, nil, nil); err != nil {
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
	noteCeiling(&out, false)
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
	// A plan with no mapped node must not carry the exception either: it is a
	// statement about specific nodes, and printing it where there are none
	// would teach a reader to skip the line that matters when there are.
	if strings.Contains(got, "EXCEPT any node marked") {
		t.Errorf("nothing was agent-mapped, so nothing may claim an exception:\n%s", got)
	}
}

// The other half, and the one ADR 0022 reversed. Between v0.6.0 and ADR 0022
// this test asserted the OPPOSITE: that the summary carried an EXCEPTION for
// agent-mapped nodes, because ADR 0004's E1 claim was measured FALSE for them.
// The code under it changed — the agent definition now arrives from a staged
// --plugin-dir, so layer 1 stays "" — and the same ceiling arm that breached
// 2 of 2 was denied 3 of 3
// (docs/measurements/0017-staged-agent-restores-layer-1.md).
//
// So this test now guards the OTHER direction, which is the one that is easy to
// get wrong: a warning kept past its cause is still a false disclosure, and it
// teaches readers to discount the next one. The negative assertions below are
// the load-bearing half.
func TestNoteCeiling_TheMappedNodeIsNoLongerAnException(t *testing.T) {
	var out strings.Builder
	noteCeiling(&out, true)
	got := out.String()

	for _, want := range []string{
		"That INCLUDES any node marked [agent: ...]",
		"copied into this run's directory",
		"0017-staged-agent-restores-layer-1.md",
		// What is STILL different about a mapped node has to survive the
		// reversal: it runs under someone else's system prompt and holds no
		// Skill tool. Deleting the exception must not delete the note.
		"runs under one of YOUR agents' system prompts",
		"holds no",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the ceiling summary does not carry %q:\n%s", want, got)
		}
	}
	// The retired exception must not come back. Each of these was true of the
	// shipped argv until 2026-08-12 and is false of it now, and re-asserting
	// one would understate the ceiling — a conservative lie is still a lie,
	// and this one tells a user their repository can configure an unattended
	// node when it cannot.
	for _, gone := range []string{
		"EXCEPT any node marked",
		"DO load your settings",
		"CLAUDE.md and hooks ARE available to them",
		"0017-lifting-the-agent-mapped-exclusion.md",
	} {
		if strings.Contains(got, gone) {
			t.Errorf("the ceiling summary still carries the retired exception %q:\n%s", gone, got)
		}
	}
}

// TestPrintPlan_ShowsAgentMappingsAndSkips pins the disclosure contract of
// subagent auto-mapping: every mapping made appears on its node's line, every
// refused candidate prints its reason, and an applied mapping states its cost
// PER NODE plus both opt-outs — because a mapping the human never saw before
// execution would defeat the reason the mapping lives in trusted code.
//
// The per-node half is what measurement (j) added, and ADR 0022 kept the shape
// while inverting what it says: THIS node, by name, runs under the same ceiling
// as any other planned node and holds no Skill tool. What it used to say — that
// the node loads the user's settings and has a scope enforced only as far as
// those settings enforce it — was true of the argv v0.6.0 shipped and is false
// of this one, so the negative assertions at the end of this test are as
// load-bearing as the positive ones.
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
		// Which node got what, by name — not a paragraph about "nodes".
		`review runs as your "code-reviewer"`,
		"same ceiling as any other planned node",
		"holds NO Skill tool",
		// The ceiling claim is a measurement, and the record travels with it —
		// including the counterfactual, because "denied 3 of 3" on its own is
		// consistent with a machine that stopped breaching.
		"denied 3 of 3",
		"minus the staging breaching",
		"0017-staged-agent-restores-layer-1.md",
		// What the fix COSTS the node has to print beside what it buys. A
		// mapped node was the one planned node that saw the user's
		// environment, and anyone whose agents leaned on that is who this
		// release changes behaviour for.
		"your CLAUDE.md, your hooks, your MCP servers",
		"was the one exception",
		// The definition is pinned, which is what makes the ceiling check
		// non-vacuous, and a resumed leg maps nothing — both are behaviour a
		// user finds surprising if they meet it without being told.
		"pinned by hash",
		"A resumed",
		// Both ways out, with their prices distinguished. The decline is per
		// AGENT: it frees every node that agent would have taken, and calling
		// that "a single node" understates it by however many nodes matched.
		"--no-agent-mapping turns all of it off",
		"--no-agent <name> declines one agent",
		"every node that agent would have taken",
		// The scan's scope, on the same screen: a user whose project agent
		// stopped mapping on 2026-08-12 has no other way to find out why, and
		// the reason it stopped is the one thing on this screen a repository
		// could otherwise have written (measurement (l)).
		"Not scanned: ./.claude/agents",
		"0022-repo-planted-agent-and-the-agents-only-dir.md",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("plan printout is missing %q:\n%s", want, got)
		}
	}
	// Neither retired claim may come back, and they were retired in opposite
	// directions: the first was refuted by measurement (j), the second by the
	// code change measurement (k) recommended. Both sat on the screen a human
	// reads before letting an unattended run start.
	for _, gone := range []string{
		"declared tool list still binds",
		"enforced only as far as YOUR settings enforce it",
		"plugins its own .claude/settings.json enables",
	} {
		if strings.Contains(got, gone) {
			t.Errorf("the printout still carries the retired claim %q:\n%s", gone, got)
		}
	}
}

// A candidate refused by --no-agent prints like any other skip, and says which
// flag refused it. The two skip reasons mean opposite things — the tool
// ceiling refusing an agent is oh-my-graph protecting the node, the user
// refusing one is the user spending a mapping to keep the ceiling — and a line
// that did not distinguish them would leave the user unable to tell their own
// opt-out from a mapping that was never available.
func TestPrintPlan_NamesAnAgentDeclinedByFlag(t *testing.T) {
	g, err := graph.Parse([]byte(`{"name":"r","version":"1","nodes":[{"id":"design","prompt":"p"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	printPlan(&out, coordinator.Plan{Graph: g, AgentMappings: []coordinator.AgentMapping{
		{NodeID: "design", Agent: "architect", SkippedReason: "declined by --no-agent"},
	}}, "/tmp/graph.json")
	got := out.String()

	if !strings.Contains(got, `! agent "architect" not applied to design: declined by --no-agent`) {
		t.Errorf("a declined agent must be named with the flag that declined it:\n%s", got)
	}
	// Nothing was mapped, so none of the mapped-node cost may print — and the
	// ceiling summary keeps its unqualified claim, which for this plan is true.
	if strings.Contains(got, "holds NO Skill tool") || strings.Contains(got, "EXCEPT any node marked") {
		t.Errorf("no mapping applied, so nothing may describe one:\n%s", got)
	}
}

// TestPrintPlan_WarnsAboutUnisolatedCheckouts pins the plan-time warning for
// the reported scenario (#103): a goal spanning two local repositories gets
// told, in the printout `auto --plan-only` also renders, that the second one
// is outside everything oh-my-graph isolates. It must name the checkout, name
// where the plan said it, give the actionable instruction (the node has to
// make its own worktree), and — because detection is a heuristic read of
// prompt text — state its own limits rather than reading as a clean bill of
// health.
func TestPrintPlan_WarnsAboutUnisolatedCheckouts(t *testing.T) {
	g, err := graph.Parse([]byte(`{"name":"r","version":"1","nodes":[{"id":"index-impl","prompt":"p"}]}`))
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	printPlan(&out, coordinator.Plan{Graph: g, Unisolated: &coordinator.UnisolatedScan{
		Root:   "/home/u/IdeaProjects/deploy-config",
		IsRepo: true,
		Paths: []coordinator.UnisolatedPath{{
			Repo:    "/home/u/IdeaProjects/search-index",
			Mention: "/home/u/IdeaProjects/search-index",
			InGoal:  true,
			NodeIDs: []string{"index-impl"},
		}},
	}}, "/tmp/graph.json")
	got := out.String()

	for _, want := range []string{
		"! not isolated: /home/u/IdeaProjects/search-index — a local git checkout\n",
		`    named by the goal and node "index-impl"`,
		"isolates no checkout at all",
		"not even the one it was invoked\n  from (/home/u/IdeaProjects/deploy-config)",
		"create its own git worktree there first",
		"heuristic read of the plan's text",
		"warning, not a refusal",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the unisolated-path warning is missing %q:\n%s", want, got)
		}
	}

	// And it may not claim an isolation auto does not have. `auto` rejects
	// cwd: and worktree: at plan time, so it provisions no managed worktree
	// anywhere — including in the repository it was invoked from. A sentence
	// contrasting "the checkouts above" with a protected invocation repository
	// would teach the opposite of SECURITY.md, "Isolation stops at the
	// invocation repository", to the only readers who will never open it.
	if strings.Contains(got, "isolation stops at the repository") {
		t.Errorf("the warning presents the boundary as one of protection; it is one of ownership:\n%s", got)
	}

	// And it stays a signal: a plan naming nothing outside the boundary
	// prints not one word of it.
	var clean strings.Builder
	printPlan(&clean, coordinator.Plan{Graph: g}, "/tmp/graph.json")
	if strings.Contains(clean.String(), "not isolated") {
		t.Errorf("nothing was outside the boundary, so nothing may say so:\n%s", clean.String())
	}
}

// A mention deeper than the checkout root is filed under the root — that is
// what the "make your own worktree" advice applies to — but the printed line
// keeps the path as written, so the user can find the text that caused it. And
// with no repository around the invocation directory, the message may not
// claim one.
func TestPrintPlan_UnisolatedWarningNamesTheMentionAndTheMissingRepository(t *testing.T) {
	g, err := graph.Parse([]byte(`{"name":"r","version":"1","nodes":[{"id":"impl","prompt":"p"}]}`))
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	printPlan(&out, coordinator.Plan{Graph: g, Unisolated: &coordinator.UnisolatedScan{
		Root: "/home/u/scratch",
		Paths: []coordinator.UnisolatedPath{{
			Repo:    "/home/u/IdeaProjects/other",
			Mention: "/home/u/IdeaProjects/other/pkg/server/handler.go",
			NodeIDs: []string{"impl"},
		}},
	}}, "/tmp/graph.json")
	got := out.String()

	if !strings.Contains(got, "! not isolated: /home/u/IdeaProjects/other — a local git checkout\n") {
		t.Errorf("the headline must be the checkout the advice applies to:\n%s", got)
	}
	if !strings.Contains(got, `    named by node "impl", written as /home/u/IdeaProjects/other/pkg/server/handler.go`) {
		t.Errorf("the detail line must keep the path as it was written:\n%s", got)
	}
	if !strings.Contains(got, "was not invoked from a git\n  repository (/home/u/scratch)") {
		t.Errorf("outside a repository, the message must say so rather than name one:\n%s", got)
	}
}

// TestPrintPlan_ShowsTheStagedCorpusAndWhatItCosts pins skill activation's
// disclosure contract (ADR 0017 §7). Two halves, and the second is the one
// that is easy to lose: the corpus is named skill by skill with size and hash,
// AND the printout says out loud that WHICH skill a node uses is not knowable
// here. A plan that listed a corpus and stopped would read exactly like a plan
// that activates nothing, and silent absence is this mechanism's signature
// failure mode.
func TestPrintPlan_ShowsTheStagedCorpusAndWhatItCosts(t *testing.T) {
	g, err := graph.Parse([]byte(`{"name":"r","version":"1","nodes":[` +
		`{"id":"impl","prompt":"p"},` +
		`{"id":"verify","prompt":"p"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	plan := coordinator.Plan{Graph: g,
		SkillScan: &coordinator.SkillScan{Dirs: []string{"/home/u/.claude/skills"}, Found: 2},
		SkillActivation: &coordinator.SkillActivation{
			Enabled: true,
			Skills: []coordinator.StagedSkill{
				{Name: "coding-rules", Description: "team coding rules", Bytes: 6861, SHA256: strings.Repeat("ab12", 16)},
				{Name: "pre-commit-checklist", Description: "the checklist", Bytes: 88678, SHA256: strings.Repeat("cd34", 16)},
			},
			NodeIDs:               []string{"impl"},
			ExcludedNodeIDs:       []string{"verify"},
			PluginDir:             "/runs/r1/skills-plugin",
			EstimatedPromptTokens: 344,
		}}

	var out strings.Builder
	printPlan(&out, plan, "/tmp/graph.json")
	got := out.String()

	for _, want := range []string{
		"skill activation: ENABLED on 1 of 2 planned node(s)",
		`coding-rules (6.7 KiB, sha256:ab12ab12ab12…) — "team coding rules"`,
		`pre-commit-checklist (86.6 KiB, sha256:cd34cd34cd34…)`,
		"344 tokens",
		"excluded: verify is agent-mapped",
		// The exclusion's COST, not a reassurance about it. This printout used
		// to close that line with "it already sees your real skills", which is
		// a capability claim and is measured false (2026-08-09, 10 spawns): an
		// agent-mapped node's argv carries no Skill in --tools, so it invokes
		// nothing, and the corpus its settings load is unreachable. Both halves
		// are pinned — that it holds NO Skill tool, and the switch that gets a
		// node out of the exclusion — because a disclosure naming the exclusion
		// without naming its cost reads as a trade the user made knowingly.
		"holds NO Skill tool",
		"not your own installed skills",
		// ADR 0022 removed the GROUND the refusal to lift stood on, and the
		// line has to say so or it becomes the same half-truth pointing the
		// other way: a user told "measured and refused" would not think to ask
		// whether the reason still applies.
		"That ground is GONE",
		"a decision nobody has re-taken",
		"--no-agent-mapping is what gets a node",
		"the WHOLE plan",
		"NOT knowable here",
		"session transcript",
		"re-materialized and verified before every node spawn",
		// The measured yield travels with the price. A disclosure that charges
		// a user ~6,000 tokens per invocation while the ADR records FAIL, and
		// says only what the mechanism WOULD do, is the asymmetry ADR 0017
		// convicts ADR 0012 of. Both halves of the number are pinned, because
		// the yield and the value are separate measurements and quoting the
		// first without the second is that same asymmetry in a smaller font:
		// 0-of-9 → 8-of-9 is what the sentence bought, and "NOT measured" is
		// what it is still not known to buy.
		"MEASURED YIELD",
		"0 of 9 times unaided, and 8 of 9",
		"Whether the WORK is better is NOT measured",
		"--no-skill-activation",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("plan printout is missing %q:\n%s", want, got)
		}
	}
	// The ceiling claim is the other half of the contract: this printout must
	// not let a reader think activation relaxed the isolation.
	if !strings.Contains(got, "ceiling: UNCHANGED") {
		t.Errorf("the printout must state that the ceiling did not move:\n%s", got)
	}
}

// An activation that is OFF despite a scan having run must say why. "Scanned,
// staged nothing" and "never scanned" are the same silence otherwise.
func TestPrintPlan_SaysWhyActivationIsOff(t *testing.T) {
	g, err := graph.Parse([]byte(`{"name":"r","version":"1","nodes":[{"id":"impl","prompt":"p"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	printPlan(&out, coordinator.Plan{Graph: g,
		SkillScan:       &coordinator.SkillScan{Dirs: []string{"/home/u/.claude/skills"}, Found: 0},
		SkillActivation: &coordinator.SkillActivation{DisabledReason: "the scan found no usable skill definition"},
	}, "/tmp/graph.json")
	if !strings.Contains(out.String(), "skill activation: OFF — the scan found no usable skill definition") {
		t.Errorf("an OFF activation must name its reason:\n%s", out.String())
	}
	if strings.Contains(out.String(), "ENABLED") {
		t.Errorf("nothing was staged, so nothing may claim otherwise:\n%s", out.String())
	}
	// Nothing was excluded here — nobody is agent-mapped — so the exclusion's
	// cost may not print. It is a statement about specific nodes, not a
	// disclaimer to attach to every OFF.
	if strings.Contains(out.String(), "holds NO Skill tool") {
		t.Errorf("no node was excluded, so nothing may describe an exclusion:\n%s", out.String())
	}
}

// The OFF branch that matters most: every planned node is agent-mapped, so
// applySkillActivation activates nobody and disables itself
// (internal/coordinator/skillstage.go). The exclusion is then TOTAL — not one
// node out of several, but a plan in which nothing can invoke a skill — and
// this used to be the one branch that printed the reason and stopped. A user
// reading "OFF — every planned node is agent-mapped" alone learns that a
// corpus went unused, which is the smaller half of what happened.
func TestPrintPlan_SaysWhatTheExclusionCostsWhenItTakesEveryNode(t *testing.T) {
	g, err := graph.Parse([]byte(`{"name":"r","version":"1","nodes":[` +
		`{"id":"design","prompt":"p","agent":"architect"},` +
		`{"id":"review","prompt":"p","agent":"code-reviewer"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	printPlan(&out, coordinator.Plan{Graph: g,
		SkillScan: &coordinator.SkillScan{Dirs: []string{"/home/u/.claude/skills"}, Found: 2},
		SkillActivation: &coordinator.SkillActivation{
			DisabledReason:  "every planned node is agent-mapped, and an agent-mapped node is excluded",
			ExcludedNodeIDs: []string{"design", "review"},
		},
	}, "/tmp/graph.json")
	got := out.String()

	for _, want := range []string{
		"skill activation: OFF — every planned node is agent-mapped",
		"holds NO Skill tool",
		"not your own installed skills",
		"That ground is GONE",
		"--no-agent-mapping is what gets a node",
		"the WHOLE plan",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the all-excluded printout is missing %q:\n%s", want, got)
		}
	}
}

// rule matches nothing for most node ids, so "no lines" is the majority
// outcome and used to be indistinguishable from "skill mapping never ran" —
// and from the case a user actually hits, a corpus sitting somewhere this scan
// does not go. A scan that happened must therefore say where it looked, how
// much it found, and which skill locations are deliberately out of scope
// (ADR 0012's cuts, held by ADR 0017: plugin-provided and project skills).
func TestPrintPlan_ShowsSkillScanAndItsLimits(t *testing.T) {
	g, err := graph.Parse([]byte(`{"name":"r","version":"1","nodes":[{"id":"impl","prompt":"p"}]}`))
	if err != nil {
		t.Fatal(err)
	}

	// The silent case: scanned, nothing found, nothing mapped.
	var scanned strings.Builder
	printPlan(&scanned, coordinator.Plan{Graph: g,
		SkillScan: &coordinator.SkillScan{Dirs: []string{"/home/u/.claude/skills"}, Found: 0},
	}, "/tmp/graph.json")
	for _, want := range []string{
		"skill scan: 0 skill(s) from /home/u/.claude/skills",
		"plugin-provided skills",
		".claude/plugins",
		"project skills",
	} {
		if !strings.Contains(scanned.String(), want) {
			t.Errorf("a scan that mapped nothing must still disclose %q:\n%s", want, scanned.String())
		}
	}

	// The opted-out case: no scan happened, so there is nothing to report and
	// the user who typed --no-skill-activation is not told about it twice.
	var off strings.Builder
	printPlan(&off, coordinator.Plan{Graph: g}, "/tmp/graph.json")
	if strings.Contains(off.String(), "skill scan") {
		t.Errorf("no scan ran, so the printout must not claim one did:\n%s", off.String())
	}
}

// TestPrintPlan_NamesAShadowedSkill: the count above the decisions is the size
// of the deduped set, so two definitions sharing a name print as one and take
// the count down with them. That subtraction has no explanation anywhere in the
// output unless the file that lost is named — "35 skill(s)" against 36 skill
// directories otherwise just looks wrong.
func TestPrintPlan_NamesAShadowedSkill(t *testing.T) {
	g, err := graph.Parse([]byte(`{"name":"r","version":"1","nodes":[{"id":"impl","prompt":"p"}]}`))
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	printPlan(&out, coordinator.Plan{Graph: g, SkillScan: &coordinator.SkillScan{
		Dirs:     []string{"/home/u/.claude/skills"},
		Found:    35,
		Shadowed: []string{"/home/u/.claude/skills/old-babysit/SKILL.md"},
	}}, "/tmp/graph.json")
	if !strings.Contains(out.String(), "skill shadowed: /home/u/.claude/skills/old-babysit/SKILL.md") {
		t.Errorf("a shadowed definition must be named:\n%s", out.String())
	}

	// And the line stays a signal: no collision, no line.
	var clean strings.Builder
	printPlan(&clean, coordinator.Plan{Graph: g, SkillScan: &coordinator.SkillScan{
		Dirs: []string{"/home/u/.claude/skills"}, Found: 35,
	}}, "/tmp/graph.json")
	if strings.Contains(clean.String(), "shadowed") {
		t.Errorf("nothing was shadowed, so nothing may say so:\n%s", clean.String())
	}
}

// TestNoteSkillMappings_NotScannedNoteMatchesWhatIsActuallyScanned holds the
// disclosure to the code it describes. The "Not scanned" note is prose: if
// ADR 0012's conditions are ever met and plugin directories join
// DefaultSkillDirs, every existing test still passes while the printout claims
// they are out of scope — a disclosure that lies, on the one code path whose
// entire purpose is that the disclosure is true. So each claim is paired here
// with the absolute path it means, its presence in the note asserted first
// (rewording the note fails here rather than quietly detaching this check),
// and no scanned directory may fall inside any of them.
func TestNoteSkillActivation_NotScannedNoteMatchesWhatIsActuallyScanned(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	notScanned := []struct{ printed, resolved string }{
		{"~/.claude/plugins", filepath.Join(home, ".claude", "plugins")},
		{"./.claude/skills", filepath.Join(cwd, ".claude", "skills")},
	}

	scanned := coordinator.DefaultSkillDirs()
	var out strings.Builder
	noteSkillActivation(&out, &coordinator.SkillScan{Dirs: scanned}, nil)
	note := out.String()

	for _, loc := range notScanned {
		if !strings.Contains(note, loc.printed) {
			t.Fatalf("this test encodes the note's claims, and %q is no longer one of them:\n%s", loc.printed, note)
		}
		for _, dir := range scanned {
			if dir == loc.resolved || strings.HasPrefix(dir, loc.resolved+string(os.PathSeparator)) {
				t.Errorf("the printout calls %s out of scope, but DefaultSkillDirs scans %s", loc.printed, dir)
			}
		}
	}
}

// TestMappingOptions_UnresolvableHomeIsNotSilent covers the one case where
// skill mapping is ON, maps nothing, and the plan printout says nothing at all:
// DefaultSkillDirs returns empty when the home directory cannot be resolved, a
// coordinator with zero skill directories never scans, and a scan that never
// happened records no SkillScan to print. Nobody chose that, so it cannot be
// left to the silence the opt-out earns.
func TestMappingOptions_UnresolvableHomeIsNotSilent(t *testing.T) {
	t.Setenv("HOME", "")
	if dirs := coordinator.DefaultSkillDirs(); len(dirs) != 0 {
		t.Fatalf("DefaultSkillDirs = %v with no home, want empty — this test's premise is gone", dirs)
	}

	var on strings.Builder
	mappingOptions(&on, false, nil, false, false)
	if !strings.Contains(on.String(), "no skill directory") || !strings.Contains(on.String(), "--no-skill-activation") {
		t.Errorf("activation is on and can never stage: say so, and name the way to mean it:\n%s", on.String())
	}

	// Turning it off is a choice, and a choice is not a warning.
	var off strings.Builder
	mappingOptions(&off, false, nil, true, false)
	if off.Len() != 0 {
		t.Errorf("--no-skill-activation must stay silent:\n%s", off.String())
	}

	// Neither is the normal case a warning.
	t.Setenv("HOME", t.TempDir())
	var quiet strings.Builder
	mappingOptions(&quiet, false, nil, false, false)
	if quiet.Len() != 0 {
		t.Errorf("a resolvable home has nothing to report:\n%s", quiet.String())
	}
}
