package schedule

import (
	"context"
	"errors"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/ledger"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/verify"
	"github.com/jitokim/oh-my-graph/internal/worktree"
)

// cwdByNode indexes the invocations the FakeRunner recorded by node id (tests
// here set each node's prompt to its id, per the suite convention).
func cwdByNode(fake *runner.FakeRunner) map[string]string {
	byNode := make(map[string]string)
	for _, inv := range fake.Invocations() {
		byNode[inv.Prompt] = inv.Cwd
	}
	return byNode
}

// TestScheduler_SameWorktreeNameSharesOneCheckout proves the lane contract: a
// dev -> e2e chain declaring the same worktree name runs both nodes in the
// SAME provisioned path, so the second node sees the first one's edits.
func TestScheduler_SameWorktreeNameSharesOneCheckout(t *testing.T) {
	g := mustGraph(t, `
name: one-lane
nodes:
  - { id: dev, prompt: dev, worktree: lane }
  - { id: e2e, prompt: e2e, depends_on: [dev], worktree: lane }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"dev": pass("s-dev", 0), "e2e": pass("s-e2e", 0),
	})
	worktrees := worktree.NewFakeManager()
	s, h, led := newHarness(t, fake, Options{Worktrees: worktrees})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	cwds := cwdByNode(fake)
	want := worktrees.PathFor("lane")
	if cwds["dev"] != want || cwds["e2e"] != want {
		t.Fatalf("same-name nodes must share one worktree path %q, got dev=%q e2e=%q", want, cwds["dev"], cwds["e2e"])
	}
}

// TestScheduler_DifferentWorktreeNamesGetDifferentCheckouts proves the
// parallel-lane contract: sibling nodes with different worktree names run in
// different paths, so their edits cannot race in a shared tree.
func TestScheduler_DifferentWorktreeNamesGetDifferentCheckouts(t *testing.T) {
	g := mustGraph(t, `
name: two-lanes
nodes:
  - { id: dev-a, prompt: dev-a, worktree: lane-a }
  - { id: dev-b, prompt: dev-b, worktree: lane-b }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"dev-a": pass("s-a", 0), "dev-b": pass("s-b", 0),
	})
	worktrees := worktree.NewFakeManager()
	s, h, led := newHarness(t, fake, Options{Worktrees: worktrees})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	cwds := cwdByNode(fake)
	if cwds["dev-a"] == cwds["dev-b"] {
		t.Fatalf("different worktree names must resolve to different paths, both got %q", cwds["dev-a"])
	}
	if cwds["dev-a"] != worktrees.PathFor("lane-a") || cwds["dev-b"] != worktrees.PathFor("lane-b") {
		t.Fatalf("nodes did not land in their own lanes: a=%q b=%q", cwds["dev-a"], cwds["dev-b"])
	}
}

// TestScheduler_NoWorktreeFieldKeepsPlainCwd pins backward compatibility both
// ways: a node without the field keeps its declared cwd exactly as before,
// and the provider is never consulted for it — so a graph with no worktree
// fields runs fine under the default RefusingProvider (no injection needed),
// exactly like a graph with no verify never touches the Verifier.
func TestScheduler_NoWorktreeFieldKeepsPlainCwd(t *testing.T) {
	g := mustGraph(t, `
name: plain
nodes:
  - { id: only, prompt: only, cwd: /work/app }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"only": pass("s", 0)})
	s, h, led := newHarness(t, fake, Options{}) // default RefusingProvider

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("a no-worktree graph must not need a worktree Provider: %v", err)
	}
	if got := cwdByNode(fake)["only"]; got != "/work/app" {
		t.Fatalf("plain node's cwd changed: got %q, want /work/app", got)
	}
}

// TestScheduler_MixedGraphOnlyAcquiresForWorktreeNodes proves the provider is
// asked exactly for the nodes that declared a worktree, never for the rest.
func TestScheduler_MixedGraphOnlyAcquiresForWorktreeNodes(t *testing.T) {
	g := mustGraph(t, `
name: mixed
nodes:
  - { id: plain, prompt: plain }
  - { id: isolated, prompt: isolated, depends_on: [plain], worktree: lane }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"plain": pass("s", 0), "isolated": pass("s", 0),
	})
	worktrees := worktree.NewFakeManager()
	s, h, led := newHarness(t, fake, Options{Worktrees: worktrees})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := worktrees.Calls(); len(got) != 1 || got[0] != "lane" {
		t.Fatalf("provider must be asked once, for the worktree node only, got %v", got)
	}
	if got := cwdByNode(fake)["plain"]; got != "" {
		t.Fatalf("plain node's cwd must stay untouched, got %q", got)
	}
}

// TestScheduler_WorktreeAcquireFailureFailsTheNode proves a provisioning
// failure (not a repo, branch collision on resume) is a loud node failure —
// recorded FAIL in the ledger, halting the run by default — never a silent
// fallback into the shared invocation tree, which would reintroduce the exact
// race the field exists to remove.
func TestScheduler_WorktreeAcquireFailureFailsTheNode(t *testing.T) {
	g := mustGraph(t, `
name: broken-lane
nodes:
  - { id: dev, prompt: dev, worktree: lane }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"dev": pass("s", 0)})
	worktrees := worktree.NewFakeManager()
	worktrees.InjectError("lane", errors.New("fatal: not a git repository"))
	s, h, led := newHarness(t, fake, Options{Worktrees: worktrees})

	err := s.Run(context.Background(), g, h, led)
	var halt *HaltError
	if !errors.As(err, &halt) || halt.NodeID != "dev" {
		t.Fatalf("expected a HaltError naming dev, got %v", err)
	}
	if fake.InvocationCount("dev") != 0 {
		t.Fatal("the node must never run outside its worktree after a failed provisioning")
	}
	if rec, ok := findRecord(led, "dev"); !ok || rec.Verdict != ledger.VerdictFail {
		t.Fatalf("expected a FAIL ledger row for dev, got %+v (present=%v)", rec, ok)
	}
}

// TestScheduler_VerifyRunsInTheWorktree proves the evidence command inherits
// the worktree as its default cwd — a lane's verification observes the
// checkout the work happened in, not the untouched invocation tree.
func TestScheduler_VerifyRunsInTheWorktree(t *testing.T) {
	g := mustGraph(t, `
name: verified-lane
nodes:
  - id: dev
    prompt: dev
    worktree: lane
    success_check:
      verify: { command: "go test ./..." }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"dev": pass("s", 0)})
	worktrees := worktree.NewFakeManager()
	verifier := verify.NewFakeVerifier(map[string]verify.Result{
		"go test ./...": {ExitCode: 0, Output: "ok"},
	})
	s, h, led := newHarness(t, fake, Options{Worktrees: worktrees, Verifier: verifier})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	calls := verifier.Calls()
	if len(calls) != 1 || calls[0].Cwd != worktrees.PathFor("lane") {
		t.Fatalf("verification must run in the worktree %q, got %+v", worktrees.PathFor("lane"), calls)
	}
}
