package main

import (
	"context"
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
	err := executeGraph(context.Background(), "test-run", g, rec, commonRunFlags{inputs: inputFlag{}}, nil)
	if err != nil {
		t.Fatalf("executeGraph returned error: %v", err)
	}
	if got := rec.invocationFor("only").DisallowedTools; len(got) != 0 {
		t.Errorf("hand-written node ran with deny list %v, want none", got)
	}
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
