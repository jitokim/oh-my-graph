package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runner"
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
// with its deny list. The coordinator building a ceiling and the scheduler
// forwarding one are each tested in their own package, but neither notices if
// this hop drops it — the suite would stay green while every planned node
// silently ran under the user's own standing grants. This pins the hop.
func TestExecutePlan_CarriesTheCeilingIntoEveryNode(t *testing.T) {
	g := mustParse(t, `{"name":"planned","nodes":[
		{"id":"scan","prompt":"scan","allowed_tools":["Read"]},
		{"id":"edit","prompt":"edit","depends_on":["scan"],"allowed_tools":["Edit"]}]}`)
	ceiling := map[string][]string{
		"scan": {"Bash", "Write"},
		"edit": {"Bash", "WebFetch"},
	}
	rec := &capturingRunner{}
	plan := coordinator.Plan{Graph: g, DisallowedTools: ceiling}

	// executeGraph resolves its run directory relative to the process cwd, so
	// run from a temp dir instead of littering the repo with artifacts.
	t.Chdir(t.TempDir())
	err := executePlan(context.Background(), "test-run", plan, rec, commonRunFlags{inputs: inputFlag{}})
	if err != nil {
		t.Fatalf("executePlan returned error: %v", err)
	}

	for _, node := range []string{"scan", "edit"} {
		got := rec.invocationFor(node).DisallowedTools
		if strings.Join(got, ",") != strings.Join(ceiling[node], ",") {
			t.Errorf("node %q ran with deny list %v, want %v", node, got, ceiling[node])
		}
	}
}

// TestExecuteGraph_HandWrittenPathImposesNoCeiling is the other half: the `run`
// subcommand passes nil, and that must reach the runner as "no ceiling" so a
// hand-written graph keeps running under the user's own permissions exactly as
// it did before this guard existed.
func TestExecuteGraph_HandWrittenPathImposesNoCeiling(t *testing.T) {
	g := mustParse(t, `{"name":"handwritten","nodes":[{"id":"only","prompt":"only","allowed_tools":["Read"]}]}`)
	rec := &capturingRunner{}

	t.Chdir(t.TempDir())
	err := executeGraph(context.Background(), "test-run", g, rec, commonRunFlags{inputs: inputFlag{}}, nil, 0)
	if err != nil {
		t.Fatalf("executeGraph returned error: %v", err)
	}
	if got := rec.invocationFor("only").DisallowedTools; len(got) != 0 {
		t.Errorf("hand-written node ran with deny list %v, want none", got)
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

	t.Chdir(t.TempDir())
	out := captureStdout(t, func() {
		if err := executePlan(context.Background(), "issue-15", plan, fake, commonRunFlags{inputs: inputFlag{}}); err != nil {
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

// TestPrintPlan_FlagsScopedBashAsUnenforced pins the honesty of the pre-run
// summary. A scoped pattern like Bash(git *) is what the plan asked for, not a
// limit the node actually runs under — this is the screen a user reads before
// letting an unattended run start, so it must not imply a tighter sandbox than
// exists.
func TestPrintPlan_FlagsScopedBashAsUnenforced(t *testing.T) {
	var out strings.Builder
	noteBashScope(&out, mustParse(t, `{"name":"p","nodes":[
		{"id":"build","prompt":"build","allowed_tools":["Bash(go *)"]},
		{"id":"read","prompt":"read","allowed_tools":["Read"]}]}`))

	got := out.String()
	if !strings.Contains(got, "build") {
		t.Errorf("note does not name the Bash-declaring node: %q", got)
	}
	if strings.Contains(got, "read") {
		t.Errorf("note wrongly names a node that really is denied Bash: %q", got)
	}
}

// TestPrintPlan_SilentWhenNoNodeDeclaresBash keeps the caveat truthful in the
// other direction: when every node is denied Bash outright, there is nothing to
// warn about and a standing warning would just teach users to ignore it.
func TestPrintPlan_SilentWhenNoNodeDeclaresBash(t *testing.T) {
	var out strings.Builder
	noteBashScope(&out, mustParse(t, `{"name":"p","nodes":[{"id":"read","prompt":"read","allowed_tools":["Read"]}]}`))

	if out.String() != "" {
		t.Errorf("expected no note, got %q", out.String())
	}
}
