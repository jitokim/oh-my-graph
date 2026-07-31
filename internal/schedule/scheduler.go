// Package schedule drives the DAG: it walks the graph in dependency order,
// runs the ready set concurrently under a cap, and enforces the run policy
// (success checks, flat retry, halt-on-fail). It owns coordination only — it
// asks the injected NodeRunner to execute a node, the injected Verifier to run
// a node's evidence check, the injected worktree.Provider to resolve a node's
// managed checkout, the Handoff to resolve inputs and persist outputs, and the
// RunLedger to record results. It never spawns a process itself and never
// learns whether a real claude ran, what actually ran a verification, or
// whether a real git created a worktree — which is exactly what lets the
// whole engine be tested against a FakeRunner, a FakeVerifier and a
// worktree.FakeManager.
package schedule

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/jitokim/oh-my-graph/internal/gate"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/handoff"
	"github.com/jitokim/oh-my-graph/internal/ledger"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/verify"
	"github.com/jitokim/oh-my-graph/internal/worktree"
)

// Concurrency bounds: the default ready-set width and the hard ceiling no graph
// may exceed. The default permission mode is unattended.
const (
	defaultConcurrency    = 4
	globalConcurrencyCap  = 10
	defaultPermissionMode = "dontAsk"
)

// predicateVerify is the success_check predicate name carried by a failed
// evidence check — the one string the error, the ledger detail and the
// verify_failed retry cause must all agree on.
const predicateVerify = "verify"

// maxDetailOutputRunes bounds how much of a verification command's output the
// ledger's one-line DETAIL column carries. Enough to see the failing assertion,
// short enough not to swamp the end-of-run table.
const maxDetailOutputRunes = 240

// Recorder persists the run's resumable snapshot (internal/runstate) as the
// run progresses. The Scheduler talks to this narrow interface and never
// imports runstate's concrete SnapshotRecorder itself, so a scheduler test can
// inject a fake with zero filesystem I/O — exactly the seam gate.GateController
// and verify.Verifier already are (DESIGN.md, "The Scheduler talks to a
// Recorder interface and defaults to a no-op, so nothing about persistence
// leaks into the engine's tests").
type Recorder interface {
	// RecordNode is called once a node reaches a terminal verdict — pass, fail,
	// gate-approved, or gate-rejected — so the snapshot stays current after
	// EVERY node, not only at a gate pause (DESIGN.md, "Snapshot writes happen
	// after every node"). A non-nil error here is non-fatal to this leg (the
	// ledger still renders this run's own summary correctly), but it is not a
	// cosmetic gap: the node stays absent from the persisted state, so a later
	// resume built from this file would not know it ran and would re-execute
	// it — a real cost, not just a blank in the printed table — which is why
	// it is surfaced on the progress feed rather than silently swallowed.
	RecordNode(nodeID string, rec runstate.NodeRecord) error
	// RecordGateDecision records an approve/reject/pause decision against a
	// gate node, independent of RecordNode (see runstate.SnapshotRecorder for
	// why a reject needs its own durable slot). Also non-fatal on error.
	RecordGateDecision(gateNodeID string, decision runstate.GateDecision) error
	// RecordPause is called once per Run(), after in-flight siblings have
	// drained, when the run stops launching new work at gateNodeID. Unlike the
	// other two methods, a failure here MUST be treated as fatal by the caller:
	// a pause whose state was not persisted is an unrecoverable stop and must
	// not be reported as a clean one (DESIGN.md, "a snapshot write failure at a
	// gate pause is fatal").
	RecordPause(gateNodeID string) error
}

// noopRecorder is the Recorder default: a scheduler test (or any caller that
// doesn't care about resume) that never injects one just doesn't persist,
// rather than needing a temp directory it never inspects.
type noopRecorder struct{}

func (noopRecorder) RecordNode(string, runstate.NodeRecord) error           { return nil }
func (noopRecorder) RecordGateDecision(string, runstate.GateDecision) error { return nil }
func (noopRecorder) RecordPause(string) error                               { return nil }

// EventSink receives one structured runfeed.Event per lifecycle transition —
// the third destination fed from the same hook points as the ProgressWriter
// (the human line) and the Recorder (the resumable snapshot), so the three can
// never disagree about what happened. The concrete implementation is
// runfeed.StreamWriter (events.jsonl — the consumer contract, docs/RUN-FEED.md),
// matched structurally so the Scheduler never imports it, exactly like
// Recorder and runstate.SnapshotRecorder. An Emit error is non-fatal: the
// event stream exists for an external consumer, and a consumer's feed must
// never fail the run it is watching — it is warned about on the progress feed
// instead (see emitEvent).
type EventSink interface {
	Emit(e runfeed.Event) error
}

// noopEventSink is the EventSink default: a caller that doesn't care about the
// consumer feed (scheduler tests) just doesn't emit one.
type noopEventSink struct{}

func (noopEventSink) Emit(runfeed.Event) error { return nil }

// Options configures a Scheduler at construction. Zero values are the safe
// defaults: use the graph's own concurrency, halt on the first failure.
type Options struct {
	// Concurrency overrides the graph's declared cap. 0 means "use the graph's".
	// The effective value is always clamped to globalConcurrencyCap.
	Concurrency int
	// ContinueOnFail prunes only a failed node's subtree instead of halting the
	// whole run (the --continue-on-fail flag).
	ContinueOnFail bool
	// Gate resolves gate nodes. Defaults to gate.PauseController: a gate node
	// pauses the run unless a caller (resume) injects a RecordedController.
	Gate gate.GateController
	// Recorder persists the resumable snapshot. Defaults to a no-op.
	Recorder Recorder
	// EventSink receives one structured event per lifecycle transition — the
	// events.jsonl consumer feed (docs/RUN-FEED.md). Defaults to a no-op.
	EventSink EventSink
	// ProgressWriter receives one line per node lifecycle event (start, pass,
	// fail, retry) as the run executes, so a long-running graph doesn't leave
	// the terminal looking dead. Defaults to os.Stderr; pass io.Discard to
	// silence it (tests do this).
	ProgressWriter io.Writer
	// Verifier runs the command a node's success_check.verify declares — the
	// second exec seam, separate from the NodeRunner because a verification is
	// not a claude invocation (docs/adr/0002). Defaults to
	// verify.RefusingVerifier, so a caller that forgot to inject one fails
	// loudly instead of quietly spawning a real process. A graph whose nodes
	// declare no verify never touches it.
	Verifier verify.Verifier
	// Worktrees provisions the managed git worktree a node declaring
	// `worktree: <name>` runs in — one checkout per unique name per run,
	// shared by every node naming it — the third exec seam, separate from
	// NodeRunner and Verifier because provisioning a checkout is neither a
	// claude invocation nor an evidence command (docs/adr/0005). Defaults to
	// worktree.RefusingProvider, so a caller that forgot to inject one fails
	// loudly instead of quietly spawning git. A graph whose nodes declare no
	// worktree never touches it.
	Worktrees worktree.Provider
	// ToolPolicies is the per-node tool ceiling keyed by node id. A node with
	// an entry runs under that COMPLETE policy — the entry is the whole story
	// for that node, not extra restrictions merged onto its declaration, so
	// there is no place for a half-applied ceiling to hide.
	//
	// A nil map means "no ceiling was imposed": every node then runs under its
	// own allowed_tools alone. That is the hand-written `run` path, whose argv
	// is exactly what it always was. A NON-nil map is auto mode's, and it must
	// carry an entry for every node — a partial map fails the missing node
	// loudly rather than running it uncapped (see policyFor). The Scheduler
	// only forwards policies; deciding what belongs in one is the
	// coordinator's job.
	ToolPolicies map[string]runner.ToolPolicy
	// CompletedNodes is the set of node ids an earlier leg already completed
	// successfully — nil (the zero value) for a fresh `run`/`auto`, where
	// nothing has completed yet. `resume` passes runstate.Snapshot.CompletedNodes()
	// so Run seeds its initial ready set from graph.ReadyGiven(CompletedNodes)
	// instead of graph.Roots(), and seeds each node's in-degree accounting for
	// parents already done — the mechanism that keeps a resumed leg from
	// re-running (and re-paying for) work the first leg already finished
	// (DESIGN.md, "resume recomputes them: Graph.ReadyGiven(done) ...").
	// graph.Roots() is defined as ReadyGiven(nil), so a nil CompletedNodes
	// reproduces the exact fresh-run behaviour this field didn't change.
	//
	// CompletedNodes is deliberately pass-only (see runstate.Snapshot.
	// CompletedNodes): a FAILED node must NOT count as done here, or its
	// dependents would read as unblocked and a --continue-on-fail subtree that
	// was pruned in an earlier leg would wrongly un-prune on resume. Use
	// SettledNodes, not this field, to stop a failed node itself from
	// relaunching.
	CompletedNodes map[string]bool
	// SettledNodes is the set of node ids that reached ANY terminal verdict —
	// pass or fail — in an earlier leg, and so must never be launched again on
	// resume. nil (the zero value) for a fresh `run`/`auto`.
	//
	// This is separate from CompletedNodes on purpose. CompletedNodes (pass
	// only) answers "whose dependents may proceed"; SettledNodes (pass or
	// fail) answers "what must not run again". Collapsing them into one set
	// would break one property or the other: seeding ReadyGiven's `done` with
	// a FAILED node would wrongly satisfy its dependents' dependency (un-
	// pruning a --continue-on-fail subtree); seeding it with pass-only alone
	// lets `resume` recompute a failed node as still-ready, since its own
	// dependencies were satisfied — the bug this field fixes: a node that
	// failed on an earlier leg would otherwise re-run (and re-add a second
	// ledger row for) a failure --continue-on-fail already decided was final.
	// `resume` passes runstate.Snapshot.SettledNodes(); CompletedNodes keeps
	// deciding topology, SettledNodes only gates re-launch.
	SettledNodes map[string]bool
}

// Scheduler executes graphs. Construct it with NewScheduler (constructor
// injection — no globals); its collaborators are supplied per Run.
type Scheduler struct {
	runner         runner.NodeRunner
	continueOnFail bool
	concurrency    int
	gate           gate.GateController
	verifier       verify.Verifier
	worktrees      worktree.Provider
	progress       io.Writer
	recorder       Recorder
	events         EventSink
	// toolPolicies is the per-node tool ceiling keyed by node id (nil for
	// hand-written graphs). A missing key means "no ceiling was imposed" — see
	// buildInvocation.
	toolPolicies map[string]runner.ToolPolicy
	// completedNodes is the set of node ids an earlier leg already finished —
	// see Options.CompletedNodes.
	completedNodes map[string]bool
	// settledNodes is the set of node ids that reached ANY terminal verdict in
	// an earlier leg (pass or fail) — see Options.SettledNodes.
	settledNodes map[string]bool
	// progressMu serializes writes to progress: parallel nodes emit events from
	// separate goroutines, and io.Writer (e.g. a *bytes.Buffer) is not safe for
	// concurrent use without one.
	progressMu sync.Mutex
}

// NewScheduler builds a Scheduler bound to a NodeRunner. The runner is the seam:
// production injects ClaudeCLIRunner, tests inject FakeRunner.
func NewScheduler(nodeRunner runner.NodeRunner, opts Options) *Scheduler {
	gateController := opts.Gate
	if gateController == nil {
		gateController = gate.NewPauseController()
	}
	verifier := opts.Verifier
	if verifier == nil {
		verifier = verify.NewRefusingVerifier()
	}
	worktrees := opts.Worktrees
	if worktrees == nil {
		worktrees = worktree.NewRefusingProvider()
	}
	progressWriter := opts.ProgressWriter
	if progressWriter == nil {
		progressWriter = os.Stderr
	}
	recorder := opts.Recorder
	if recorder == nil {
		recorder = noopRecorder{}
	}
	eventSink := opts.EventSink
	if eventSink == nil {
		eventSink = noopEventSink{}
	}
	return &Scheduler{
		runner:         nodeRunner,
		continueOnFail: opts.ContinueOnFail,
		concurrency:    opts.Concurrency,
		gate:           gateController,
		verifier:       verifier,
		worktrees:      worktrees,
		progress:       progressWriter,
		recorder:       recorder,
		events:         eventSink,
		toolPolicies:   opts.ToolPolicies,
		completedNodes: opts.CompletedNodes,
		settledNodes:   opts.SettledNodes,
	}
}

// Run executes g to completion, coordinating through h (inputs/outputs) and
// recording into led. It returns nil only when every node passed. On failure it
// returns a *HaltError (default: the first failure cancelled the run and killed
// in-flight siblings) or a *RunFailedError (continue-on-fail, or a rejected
// gate: some subtrees were pruned but independent branches finished) or a
// *PausedError (a gate decision paused the run — see the pause handling
// below, ADR 0003).
//
// Run also brackets the leg on the event stream: run_started before any node
// launches, run_finished (with the leg's outcome) after everything has
// settled — the run-level halves of the same per-transition feed the node
// helpers emit (see EventSink). Bracketing here rather than at the CLI keeps
// every lifecycle emission behind one seam.
func (s *Scheduler) Run(ctx context.Context, g *graph.Graph, h *handoff.Handoff, led *ledger.RunLedger) error {
	s.emitEvent(runfeed.Event{Type: runfeed.EventRunStarted})
	err := s.execute(ctx, g, h, led)
	s.emitEvent(runfeed.Event{Type: runfeed.EventRunFinished, Outcome: runOutcome(err)})
	return err
}

// execute is Run's body — everything between the run_started/run_finished pair.
func (s *Scheduler) execute(ctx context.Context, g *graph.Graph, h *handoff.Handoff, led *ledger.RunLedger) error {
	sem := make(chan struct{}, effectiveConcurrency(s.concurrency, g.Concurrency))
	grp, ctx := errgroup.WithContext(ctx)

	var mu sync.Mutex
	// inDegree seeds each node's remaining-parent count against s.completedNodes,
	// not just len(DependsOn): a fresh run has none completed, so this reduces to
	// the plain count exactly as before; a resumed leg's parents already marked
	// complete no longer count against it, so a node whose only pending parent
	// was finished in an earlier leg becomes ready immediately instead of waiting
	// on a re-run of it (DESIGN.md, "the scheduler seeds each node's in-degree as
	// len(DependsOn) - (parents already completed)").
	inDegree := make(map[string]int, len(g.Nodes))
	for _, n := range g.Nodes {
		remaining := 0
		for _, parent := range n.DependsOn {
			if !s.completedNodes[parent] {
				remaining++
			}
		}
		inDegree[n.ID] = remaining
	}

	var failMu sync.Mutex
	var prunedFailures []string

	// pauseMu guards pausedAt: the id of the gate whose DecisionPause first
	// stopped the run, or "" while no gate has paused it. It is the mechanism
	// behind "stop launching new work but drain what's in flight" — unlike
	// halt-on-fail, a pause deliberately does NOT cancel ctx (ADR 0003, "this
	// is the one place the halt path does not cancel the context"), so an
	// in-flight sibling runs to completion normally. What stops is new
	// launches: every successful node checks pausedAt before enqueuing its own
	// dependents, so the moment any gate pauses, the wavefront of new work
	// stops advancing while whatever already started keeps running.
	var pauseMu sync.Mutex
	var pausedAt string

	var launch func(id string)
	launch = func(id string) {
		if s.settledNodes[id] {
			// This node already reached a terminal verdict (pass or fail) in
			// an earlier leg. A PASS never reaches here at all — it is
			// excluded from the initial ReadyGiven(completedNodes) set and
			// its in-degree never re-zeroes it — but a FAILED node's own
			// dependencies were satisfied for it to have run at all, so
			// without this guard resume would hand it right back as ready
			// and re-run (and re-pay for, and double-record in the ledger) a
			// failure --continue-on-fail already decided was final for this
			// run (see Options.SettledNodes).
			return
		}
		node, _ := g.NodeByID(id)
		grp.Go(func() error {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return ctx.Err()
			}
			defer func() { <-sem }()

			if err := s.runNode(ctx, node, h, led); err != nil {
				var pause *pauseSignal
				if errors.As(err, &pause) {
					pauseMu.Lock()
					if pausedAt == "" {
						pausedAt = pause.NodeID
					}
					pauseMu.Unlock()
					return nil
				}
				var reject *rejectSignal
				if errors.As(err, &reject) {
					// A gate rejection always prunes its own subtree, regardless
					// of --continue-on-fail: approving/rejecting is the gate's
					// own decision, not a run-wide failure policy (DESIGN.md,
					// "--reject <gate-id> ... It is not a crash").
					failMu.Lock()
					prunedFailures = append(prunedFailures, reject.NodeID)
					failMu.Unlock()
					return nil
				}
				if s.continueOnFail {
					failMu.Lock()
					prunedFailures = append(prunedFailures, id)
					failMu.Unlock()
					// Prune the subtree: do not enqueue dependents, but do not
					// cancel the shared context — independent branches continue.
					return nil
				}
				// Halt-on-fail: returning a non-nil error cancels ctx, which
				// propagates to every in-flight child (killing wedged claude
				// subprocesses) and stops new nodes from starting.
				return &HaltError{NodeID: id, Err: err}
			}

			pauseMu.Lock()
			paused := pausedAt != ""
			pauseMu.Unlock()
			if paused {
				// This node's own success still counts — it is drained, not
				// cancelled — but a pause elsewhere already means the whole run
				// stops, not just this branch, so its dependents must not launch.
				return nil
			}

			// Success: every dependent loses one in-degree; any that reach zero
			// join the ready set right now.
			for _, dependent := range g.DependentsOf(id) {
				mu.Lock()
				inDegree[dependent]--
				ready := inDegree[dependent] == 0
				mu.Unlock()
				if ready {
					launch(dependent)
				}
			}
			return nil
		})
	}

	// The initial ready set is graph.ReadyGiven(s.completedNodes), not
	// graph.Roots(): Roots() is defined as ReadyGiven(nil) (see graph.go), so a
	// fresh run (nil s.completedNodes) launches exactly the roots as before,
	// while a resumed leg launches whatever the completed set has newly
	// unblocked instead of re-running finished work.
	for _, id := range g.ReadyGiven(s.completedNodes) {
		launch(id)
	}

	if err := grp.Wait(); err != nil {
		return err
	}

	pauseMu.Lock()
	gateID := pausedAt
	pauseMu.Unlock()
	if gateID != "" {
		// The pause snapshot write happens here — once, after every in-flight
		// sibling has drained — never from inside the gate's own evaluation,
		// which runs concurrently with them. A failure here is fatal: a pause
		// whose state was not persisted is an unrecoverable stop, and reporting
		// it as a clean pause would lie (DESIGN.md).
		if err := s.recorder.RecordPause(gateID); err != nil {
			return fmt.Errorf("persist pause snapshot for gate %q: %w", gateID, err)
		}
		return &PausedError{GateID: gateID}
	}

	failMu.Lock()
	defer failMu.Unlock()
	if len(prunedFailures) > 0 {
		sort.Strings(prunedFailures)
		return &RunFailedError{FailedNodes: prunedFailures}
	}
	return nil
}

// runNode executes one node under the run policy and records exactly one ledger
// row for it. The lifecycle is fixed:
//
//	NodeRunner.Run → exit_zero → result_matches → Verifier.Verify →
//	Handoff.PersistOutput → budget_usd → RunLedger.Record
//
// It returns nil on success, or the failure (an interpolation error, a runner
// error, a *NodeCheckError — including a failed verification — or a
// *NodeBudgetError) after retries are exhausted. A gate node never reaches
// this general path at all; see evaluateGate for its three outcomes,
// including the internal *pauseSignal/*rejectSignal Run() intercepts before
// they could be mistaken for one of these.
func (s *Scheduler) runNode(ctx context.Context, node graph.Node, h *handoff.Handoff, led *ledger.RunLedger) error {
	start := time.Now()
	s.logProgress("▶ %s  running…\n", node.ID)
	s.emitEvent(runfeed.Event{Type: runfeed.EventNodeStarted, NodeID: node.ID})

	if node.Type == graph.TypeGate {
		return s.evaluateGate(ctx, node, h, led, start)
	}

	invocation, err := s.buildInvocation(ctx, node, h)
	if err != nil {
		return s.recordFail(led, h, node, "", 0, time.Since(start), 0, err)
	}

	attempts := 1
	if node.Retry != nil && node.Retry.Max > 0 {
		attempts += node.Retry.Max
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			// A retry never resumes a failed session — start fresh.
			invocation.ResumeSession = ""
		}

		outcome, runErr := s.runner.Run(ctx, invocation)
		if runErr != nil {
			lastErr = runErr
			if s.shouldRetry(node, attempt, attempts, causeFromRunError(runErr)) {
				s.recordRetry(node, attempt+1)
				continue
			}
			return s.recordFail(led, h, node, "", 0, time.Since(start), attempt, runErr)
		}

		var verdictErr error
		if outcome.BudgetExhausted {
			// claude's own --max-budget-usd killed this node the moment its
			// running spend crossed budget_usd — a real mid-run cost kill. It
			// is a budget failure, so it takes the exact same *NodeBudgetError
			// path (and budget_exceeded retry token) as the post-hoc check
			// below, not the generic nonzero_exit its exit code would produce.
			// There is nothing to persist and nothing to verify: the run was
			// interrupted before a result existed (unlike a post-hoc overspend,
			// which completed and keeps its artifact).
			verdictErr = &NodeBudgetError{NodeID: node.ID, BudgetUSD: node.BudgetUSD, ActualUSD: outcome.TotalCostUSD}
		} else {
			// Cheapest predicates first: exit_zero and result_matches are in-memory,
			// the verification spawns a process. A node that already failed one of
			// them never pays for a command run against the wreckage.
			verdictErr = evaluateSuccessCheck(node, outcome)
			if verdictErr == nil {
				verdictErr = s.verifyEvidence(ctx, node, h, invocation.Cwd)
			}
		}
		if verdictErr == nil {
			if persistErr := h.PersistOutput(node.ID, outcome.Result, outcome.SessionID); persistErr != nil {
				return s.recordFail(led, h, node, outcome.SessionID, outcome.TotalCostUSD, time.Since(start), attempt, persistErr)
			}
			// Budget is judged only after the output has been persisted, so a
			// node that did useful work before blowing its budget still leaves
			// its artifact on disk for dependents and for inspection. Handoff
			// semantics are untouched here — only the pass/fail verdict is.
			verdictErr = evaluateBudget(node, outcome)
			if verdictErr == nil {
				return s.recordPass(led, h, node, outcome, time.Since(start), attempt)
			}
		}

		lastErr = verdictErr
		if s.shouldRetry(node, attempt, attempts, causeFromCheck(verdictErr)) {
			s.recordRetry(node, attempt+1)
			continue
		}
		return s.recordFail(led, h, node, outcome.SessionID, outcome.TotalCostUSD, time.Since(start), attempt, verdictErr)
	}

	// Unreachable in practice — the final attempt always records and returns
	// above — but the compiler cannot prove the loop terminates with a return.
	return lastErr
}

// evaluateGate asks the injected GateController for node's decision and turns
// it into exactly one of four outcomes:
//
//   - an evaluation error: an ordinary node failure (recordFail);
//   - DecisionApprove: a ledger PASS with no cost/session (a gate spawns no
//     subprocess) so its dependents may proceed;
//   - DecisionReject: a ledger FAIL wrapping a *rejectSignal, which Run()
//     recognizes and prunes the subtree with regardless of --continue-on-fail;
//   - DecisionPause: a *pauseSignal, which Run() recognizes and turns into the
//     whole-run pause — no ledger/snapshot write happens here, because that
//     happens once at the Run level after in-flight siblings have drained.
//
// Each decision also emits its gate_* event (gate_approved / gate_rejected /
// gate_paused) at the moment it is applied, from the same hook point as the
// matching progress line, so a stream consumer sees gate state without
// reading state.json. gate_paused is emitted per pausing gate: gates
// evaluating concurrently may each pause, and each emits its own event, even
// though only the first becomes the run's resumable pause point.
func (s *Scheduler) evaluateGate(ctx context.Context, node graph.Node, h *handoff.Handoff, led *ledger.RunLedger, start time.Time) error {
	decision, err := s.gate.Evaluate(ctx, node)
	if err != nil {
		return s.recordFail(led, h, node, "", 0, time.Since(start), 0, fmt.Errorf("gate %q: %w", node.ID, err))
	}

	switch decision {
	case gate.DecisionApprove:
		return s.recordGateApprove(led, h, node, time.Since(start))
	case gate.DecisionReject:
		s.recordGateDecision(node, runstate.GateReject)
		s.emitEvent(runfeed.Event{Type: runfeed.EventGateRejected, NodeID: node.ID})
		return s.recordFail(led, h, node, "", 0, time.Since(start), 0, &rejectSignal{NodeID: node.ID})
	case gate.DecisionPause:
		s.logProgress("⏸ %s  gate paused\n", node.ID)
		s.emitEvent(runfeed.Event{Type: runfeed.EventGatePaused, NodeID: node.ID})
		return &pauseSignal{NodeID: node.ID}
	default:
		return s.recordFail(led, h, node, "", 0, time.Since(start), 0,
			fmt.Errorf("node %q: gate controller returned unknown decision %q", node.ID, decision))
	}
}

// recordFail writes the node's live "✗ FAILED" progress line, its ledger row,
// the same record into the resumable snapshot, and the matching node_failed
// event, then returns cause so callers can `return s.recordFail(...)` directly.
// attempt is the 0-based index of the terminal attempt — i.e. how many retries
// preceded it (0 for a path that never retries, such as a gate).
func (s *Scheduler) recordFail(led *ledger.RunLedger, h *handoff.Handoff, node graph.Node, sessionID string, cost float64, duration time.Duration, attempt int, cause error) error {
	s.logProgress("✗ %s  FAILED: %s\n", node.ID, cause.Error())
	rec := failRecord(node, sessionID, cost, duration, cause)
	led.Record(rec)
	s.recordSnapshot(node, rec, h)
	s.emitEvent(terminalEvent(runfeed.EventNodeFailed, rec, attempt))
	return cause
}

// recordPass writes the node's live "✓ PASS" progress line, its ledger row,
// the same record into the resumable snapshot, and the matching node_passed
// event, then returns nil so callers can `return s.recordPass(...)` directly.
func (s *Scheduler) recordPass(led *ledger.RunLedger, h *handoff.Handoff, node graph.Node, outcome runner.NodeOutcome, duration time.Duration, attempt int) error {
	s.logProgress("✓ %s  %s  $%.4f  %s\n", node.ID, ledger.VerdictPass, outcome.TotalCostUSD, duration.Round(time.Millisecond))
	rec := passRecord(node, outcome, duration, attempt)
	led.Record(rec)
	s.recordSnapshot(node, rec, h)
	s.emitEvent(terminalEvent(runfeed.EventNodePassed, rec, attempt))
	return nil
}

// recordGateApprove writes the gate's live "approved" progress line and its
// ledger PASS row — zero cost/session, since a gate spawns no subprocess —
// then records the same into the snapshot so the gate counts as completed
// (CompletedNodes) and its dependents may proceed, this leg or after a later
// resume. The gate's terminal event is a node_passed like any other node's,
// preceded by the gate_approved decision event.
func (s *Scheduler) recordGateApprove(led *ledger.RunLedger, h *handoff.Handoff, node graph.Node, duration time.Duration) error {
	s.logProgress("✓ %s  %s  gate approved\n", node.ID, ledger.VerdictPass)
	s.emitEvent(runfeed.Event{Type: runfeed.EventGateApproved, NodeID: node.ID})
	rec := ledger.Record{NodeID: node.ID, Verdict: ledger.VerdictPass, Duration: duration, Detail: "gate approved"}
	led.Record(rec)
	s.recordSnapshot(node, rec, h)
	s.recordGateDecision(node, runstate.GateApprove)
	s.emitEvent(terminalEvent(runfeed.EventNodePassed, rec, 0))
	return nil
}

// recordRetry writes the node's live "↻ retry" progress line and the matching
// node_retried event. retryNumber is 1-based: the first retry after the
// initial attempt is 1.
func (s *Scheduler) recordRetry(node graph.Node, retryNumber int) {
	s.logProgress("↻ %s  retry\n", node.ID)
	s.emitEvent(runfeed.Event{Type: runfeed.EventNodeRetried, NodeID: node.ID, Retries: retryNumber})
}

// recordSnapshot converts rec into a runstate.NodeRecord — filling in
// whatever artifact path h has for node.ID, if any — and hands it to the
// injected Recorder. A write failure here is deliberately non-fatal to THIS
// leg: the ledger still renders this run's own summary correctly either way.
// But the failure is not cosmetic — a dropped write leaves node.ID absent
// from the persisted state, so a later `resume` built from this file would
// not know it ran and would re-execute it, a real cost — which is why it is
// surfaced on the progress feed rather than silently swallowed (DESIGN.md,
// "Gate nodes and resume").
func (s *Scheduler) recordSnapshot(node graph.Node, rec ledger.Record, h *handoff.Handoff) {
	artifactPath, _ := h.ArtifactPath(node.ID)
	if err := s.recorder.RecordNode(node.ID, toNodeRecord(rec, artifactPath)); err != nil {
		s.logProgress("⚠ %s  snapshot write failed: %v\n", node.ID, err)
	}
}

// recordGateDecision hands a gate's approve/reject/pause decision to the
// injected Recorder, warning (non-fatally) on the progress feed if it fails.
func (s *Scheduler) recordGateDecision(node graph.Node, decision runstate.GateDecision) {
	if err := s.recorder.RecordGateDecision(node.ID, decision); err != nil {
		s.logProgress("⚠ %s  gate decision snapshot write failed: %v\n", node.ID, err)
	}
}

// toNodeRecord derives a runstate.NodeRecord from the ledger.Record the
// Scheduler already built for the same node, so the two never carry different
// cost/verdict/detail for the same event — one accounting computation, two
// destinations (the ledger for this leg's table, the snapshot for a resume).
func toNodeRecord(rec ledger.Record, artifactPath string) runstate.NodeRecord {
	verdict := runstate.VerdictFail
	if rec.Verdict == ledger.VerdictPass {
		verdict = runstate.VerdictPass
	}
	return runstate.NodeRecord{
		Verdict:      verdict,
		SessionID:    rec.SessionID,
		CostUSD:      rec.CostUSD,
		BudgetUSD:    rec.BudgetUSD,
		Duration:     rec.Duration,
		ArtifactPath: artifactPath,
		Detail:       rec.Detail,
	}
}

// terminalEvent derives a node's terminal runfeed.Event from the ledger.Record
// the Scheduler already built for the same transition — one accounting
// computation, three destinations (ledger, snapshot, event stream) — so the
// stream can never carry a different cost/verdict/detail than the other two.
// retries is the number of retries that preceded the terminal attempt.
func terminalEvent(eventType runfeed.EventType, rec ledger.Record, retries int) runfeed.Event {
	return runfeed.Event{
		Type:      eventType,
		NodeID:    rec.NodeID,
		Verdict:   string(rec.Verdict),
		CostUSD:   rec.CostUSD,
		SessionID: rec.SessionID,
		Retries:   retries,
		Detail:    rec.Detail,
	}
}

// emitEvent hands one event to the injected EventSink. A write failure is
// non-fatal — the stream exists for an external consumer, and a consumer's
// feed must never fail the run it is watching — but it is surfaced on the
// progress feed rather than silently swallowed, exactly like a snapshot write
// failure: a dropped event leaves a tailing consumer's picture of the run
// incomplete, which the operator should know.
func (s *Scheduler) emitEvent(e runfeed.Event) {
	if err := s.events.Emit(e); err != nil {
		subject := e.NodeID
		if subject == "" {
			subject = string(e.Type)
		}
		s.logProgress("⚠ %s  event stream write failed: %v\n", subject, err)
	}
}

// runOutcome maps Run's return into the run_finished outcome token: nil is
// passed, a *PausedError is paused (a pause is not a failure — ADR 0003), and
// anything else (halt, pruned failures, a failed pause persist) is failed.
func runOutcome(err error) string {
	if err == nil {
		return runfeed.OutcomePassed
	}
	var paused *PausedError
	if errors.As(err, &paused) {
		return runfeed.OutcomePaused
	}
	return runfeed.OutcomeFailed
}

// logProgress writes one progress line, serialized by progressMu: parallel
// nodes call this from separate goroutines, and the injected io.Writer (e.g. a
// *bytes.Buffer in tests) is not safe for concurrent writes on its own.
func (s *Scheduler) logProgress(format string, args ...any) {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	fmt.Fprintf(s.progress, format, args...)
}

// buildInvocation renders a node into a runner.NodeInvocation: interpolate its
// prompt and cwd, swap in the managed worktree for a node that declares one,
// resolve the session it resumes (if any), default the permission mode, and
// attach the node's tool policy. It takes the run context because acquiring a
// worktree may spawn git (behind the injected worktree.Provider).
func (s *Scheduler) buildInvocation(ctx context.Context, node graph.Node, h *handoff.Handoff) (runner.NodeInvocation, error) {
	prompt, err := h.Interpolate(node.Prompt)
	if err != nil {
		return runner.NodeInvocation{}, err
	}
	cwd, err := h.Interpolate(node.Cwd)
	if err != nil {
		return runner.NodeInvocation{}, err
	}
	if node.Worktree != "" {
		// The managed checkout replaces the plain cwd wholesale (validation
		// rejects a node declaring both). Acquire is idempotent per name, so
		// every node sharing this worktree name lands in the same checkout —
		// and because verifyEvidence inherits the invocation's cwd, the node's
		// success_check.verify gathers its evidence in the worktree too.
		if cwd, err = s.worktrees.Acquire(ctx, node.Worktree); err != nil {
			return runner.NodeInvocation{}, fmt.Errorf("provision worktree %q: %w", node.Worktree, err)
		}
	}
	resume, err := h.ResumeSessionFor(node)
	if err != nil {
		return runner.NodeInvocation{}, err
	}

	permissionMode := node.PermissionMode
	if permissionMode == "" {
		permissionMode = defaultPermissionMode
	}

	policy, err := s.policyFor(node)
	if err != nil {
		return runner.NodeInvocation{}, err
	}

	return runner.NodeInvocation{
		Prompt:         prompt,
		Cwd:            cwd,
		PermissionMode: permissionMode,
		ResumeSession:  resume,
		Agent:          node.Agent,
		BudgetUSD:      node.BudgetUSD,
		Policy:         policy,
	}, nil
}

// policyFor returns the node's complete tool policy.
//
// A nil policy map means no ceiling was imposed at all — the hand-written `run`
// path — so the node runs under its own declaration and nothing else. A
// non-nil map is auto mode's, and it owns the whole policy for that node,
// including its --allowedTools.
//
// Deliberately NOT a merge of the two. Merging would mean a policy could be
// half-supplied and still look applied, which is the exact failure this type
// exists to make unrepresentable: the coordinator builds AllowedTools into the
// policy it hands over, and a coordinator test asserts it did.
//
// A non-nil map MISSING this node is an error rather than a fallback, because
// the fallback would be the one outcome the ceiling exists to prevent: an
// unreviewed planned node running uncapped, silently, while every other node in
// the run is capped. Failing the node names the bug; falling back hides it.
func (s *Scheduler) policyFor(node graph.Node) (runner.ToolPolicy, error) {
	if s.toolPolicies == nil {
		return runner.ToolPolicy{AllowedTools: node.AllowedTools}, nil
	}
	policy, imposed := s.toolPolicies[node.ID]
	if !imposed {
		return runner.ToolPolicy{}, &MissingToolPolicyError{NodeID: node.ID}
	}
	return policy, nil
}

// verifyEvidence runs the node's success_check.verify — the only predicate that
// judges something outside the node's own account of itself — and returns nil
// when the evidence holds, or a *NodeCheckError naming the "verify" predicate.
// A node that declared no verification passes trivially and nothing is spawned.
//
// nodeCwd is the node's own already-interpolated working directory: a
// verification runs where its node ran unless it says otherwise, so the common
// case declares no cwd at all.
//
// Every failure on this path — an unresolvable interpolation, a timeout, a
// command that could not spawn, the wrong exit code, output that did not match —
// becomes the same *NodeCheckError. That is deliberate: they all mean "this
// node's success was not demonstrated", they all carry the same retry cause
// (verify_failed), and a verification that could not be completed must never
// read as a pass.
func (s *Scheduler) verifyEvidence(ctx context.Context, node graph.Node, h *handoff.Handoff, nodeCwd string) error {
	verification := node.SuccessCheck.Verify
	if verification == nil {
		return nil
	}

	request, err := resolveVerification(*verification, h, nodeCwd)
	if err != nil {
		return verifyFailure(node.ID, err.Error())
	}

	s.logProgress("… %s  verifying: %s\n", node.ID, request.Command)
	result, err := s.verifier.Verify(ctx, request)
	if err != nil {
		return verifyFailure(node.ID, err.Error())
	}
	return judgeVerification(node.ID, *verification, request.Command, result)
}

// resolveVerification turns a declared verification into a runnable request:
// command and cwd interpolate like a prompt, and an undeclared cwd inherits the
// node's own.
func resolveVerification(v graph.Verification, h *handoff.Handoff, nodeCwd string) (verify.Request, error) {
	command, err := h.Interpolate(v.Command)
	if err != nil {
		return verify.Request{}, fmt.Errorf("could not resolve command %q: %w", v.Command, err)
	}
	cwd := nodeCwd
	if v.Cwd != "" {
		if cwd, err = h.Interpolate(v.Cwd); err != nil {
			return verify.Request{}, fmt.Errorf("could not resolve cwd %q: %w", v.Cwd, err)
		}
	}
	return verify.Request{Command: command, Cwd: cwd, Timeout: v.TimeoutDuration()}, nil
}

// judgeVerification applies the node's declared expectations to what the command
// actually did. This is the whole point of the predicate: the judgement is made
// here, from observed facts, not by the node reporting on itself.
func judgeVerification(nodeID string, v graph.Verification, command string, result verify.Result) error {
	if expected := v.ExpectedExitCode(); result.ExitCode != expected {
		return verifyFailure(nodeID, fmt.Sprintf("`%s` exited %d, want %d%s",
			command, result.ExitCode, expected, outputTail(result.Output)))
	}
	if v.OutputMatches == "" {
		return nil
	}

	// Already compiled once by graph.Validate at load time, so this cannot fail
	// for a graph that came through Load/Parse; it is still handled rather than
	// ignored, because a caller that hand-built a Node bypasses that guarantee.
	pattern, err := regexp.Compile(v.OutputMatches)
	if err != nil {
		return verifyFailure(nodeID, fmt.Sprintf("invalid output_matches regex %q: %v", v.OutputMatches, err))
	}
	if !pattern.MatchString(result.Output) {
		return verifyFailure(nodeID, fmt.Sprintf("`%s` output did not match /%s/%s",
			command, v.OutputMatches, outputTail(result.Output)))
	}
	return nil
}

// verifyFailure builds the one error shape every verification failure takes.
func verifyFailure(nodeID, detail string) error {
	return &NodeCheckError{NodeID: nodeID, Predicate: predicateVerify, Detail: detail}
}

// outputTail renders the end of a verification command's output for the ledger's
// DETAIL column: the last maxDetailOutputRunes, whitespace-collapsed onto one
// line so a multi-line test failure cannot break the end-of-run table. The tail
// is what matters — a failing command explains itself last. Empty output yields
// an empty string rather than a dangling separator.
func outputTail(output string) string {
	condensed := strings.Join(strings.Fields(output), " ")
	if condensed == "" {
		return ""
	}
	if runes := []rune(condensed); len(runes) > maxDetailOutputRunes {
		condensed = "…" + string(runes[len(runes)-maxDetailOutputRunes:])
	}
	return "; output: " + condensed
}

// shouldRetry reports whether a failed attempt should be retried: there must be
// an attempt left and the failure cause must be listed in the node's retry.on.
func (s *Scheduler) shouldRetry(node graph.Node, attempt, attempts int, cause string) bool {
	if attempt >= attempts-1 {
		return false
	}
	if node.Retry == nil {
		return false
	}
	for _, allowed := range node.Retry.On {
		if allowed == cause {
			return true
		}
	}
	return false
}

// effectiveConcurrency resolves the ready-set width: the CLI override if given,
// else the graph's own value, else the default — clamped to the global ceiling.
func effectiveConcurrency(override, graphConcurrency int) int {
	width := override
	if width <= 0 {
		width = graphConcurrency
	}
	if width <= 0 {
		width = defaultConcurrency
	}
	if width > globalConcurrencyCap {
		width = globalConcurrencyCap
	}
	return width
}

// evaluateSuccessCheck applies a node's success_check to its outcome, returning
// nil on pass or a *NodeCheckError naming the predicate that failed. An empty
// check means "exit zero is enough".
func evaluateSuccessCheck(node graph.Node, outcome runner.NodeOutcome) error {
	check := node.SuccessCheck

	if check.IsZero() {
		if outcome.ExitCode != 0 {
			return &NodeCheckError{NodeID: node.ID, Predicate: "exit_zero", Detail: fmt.Sprintf("exit code %d", outcome.ExitCode)}
		}
		return nil
	}

	if check.ExitZero && outcome.ExitCode != 0 {
		return &NodeCheckError{NodeID: node.ID, Predicate: "exit_zero", Detail: fmt.Sprintf("exit code %d", outcome.ExitCode)}
	}

	if check.ResultMatches != "" {
		re, err := regexp.Compile(check.ResultMatches)
		if err != nil {
			return &NodeCheckError{NodeID: node.ID, Predicate: "result_matches", Detail: fmt.Sprintf("invalid regex %q: %v", check.ResultMatches, err)}
		}
		if !re.MatchString(outcome.Result) {
			return &NodeCheckError{NodeID: node.ID, Predicate: "result_matches", Detail: fmt.Sprintf("result did not match /%s/", check.ResultMatches)}
		}
	}
	return nil
}

// evaluateBudget applies a node's budget_usd to the cost it actually reported,
// returning nil when the node stayed within budget (or declared none) and a
// *NodeBudgetError when it overspent. "Exceeds" is strictly greater than, so a
// node landing exactly on its budget passes; no tolerance is invented on top.
// A non-positive budget_usd means "no budget declared" and is never enforced.
//
// This check is the post-hoc backstop behind claude's own --max-budget-usd
// mid-run kill (surfaced as NodeOutcome.BudgetExhausted and handled upstream):
// it catches the one in-flight call that can overshoot before the native
// abort lands.
func evaluateBudget(node graph.Node, outcome runner.NodeOutcome) error {
	if node.BudgetUSD <= 0 || outcome.TotalCostUSD <= node.BudgetUSD {
		return nil
	}
	return &NodeBudgetError{
		NodeID:    node.ID,
		BudgetUSD: node.BudgetUSD,
		ActualUSD: outcome.TotalCostUSD,
	}
}

// causeFromRunError maps a runner error to a retry cause token. An unparseable
// output is "output_error"; anything else (spawn failure, context) is
// "run_error". Non-zero exits never reach here — the runner returns those inside
// the outcome, and evaluateSuccessCheck classifies them.
func causeFromRunError(err error) string {
	var outputErr *runner.NodeOutputError
	if asErr(err, &outputErr) {
		return "output_error"
	}
	return "run_error"
}

// causeFromCheck maps a failed verdict to a retry cause token: a blown
// budget_usd is "budget_exceeded"; a failed verification is "verify_failed"; a
// failed result_matches is "result_mismatch"; a failed exit_zero predicate is
// "nonzero_exit".
//
// budget_exceeded is deliberately its own token rather than folding into the
// "nonzero_exit" fallback: retrying a node that has already overspent spends
// that money again, so it must be an explicit, informed opt-in
// (retry: { on: [budget_exceeded] }) that no pre-existing graph can trip into.
// verify_failed is its own token for the same reason in reverse: re-running a
// node because its evidence check failed is a reasonable thing to want, but it
// is a different decision from re-running one that exited non-zero, and a graph
// should be able to opt into either without the other.
func causeFromCheck(err error) string {
	var budgetErr *NodeBudgetError
	if asErr(err, &budgetErr) {
		return "budget_exceeded"
	}
	var checkErr *NodeCheckError
	if asErr(err, &checkErr) {
		switch checkErr.Predicate {
		case predicateVerify:
			return "verify_failed"
		case "result_matches":
			return "result_mismatch"
		}
	}
	return "nonzero_exit"
}

// passRecord / failRecord build the single ledger row for a node's terminal
// result, keeping the record shape in one place.
func passRecord(node graph.Node, outcome runner.NodeOutcome, duration time.Duration, attempt int) ledger.Record {
	rec := ledger.Record{
		NodeID:    node.ID,
		SessionID: outcome.SessionID,
		CostUSD:   outcome.TotalCostUSD,
		BudgetUSD: node.BudgetUSD,
		Verdict:   ledger.VerdictPass,
		Duration:  duration,
	}

	var notes []string
	if attempt > 0 {
		notes = append(notes, fmt.Sprintf("passed after %d retr%s", attempt, plural(attempt)))
	}
	// A passing node is by definition within budget, so the delta is <= 0 here;
	// reporting the headroom is what makes "ran at 99% of budget" visible in the
	// table instead of only showing up once a node has already blown it.
	if delta, declared := rec.BudgetDeltaUSD(); declared {
		notes = append(notes, fmt.Sprintf("under budget by $%.4f", -delta))
	}
	rec.Detail = strings.Join(notes, "; ")
	return rec
}

func failRecord(node graph.Node, sessionID string, cost float64, duration time.Duration, cause error) ledger.Record {
	return ledger.Record{
		NodeID:    node.ID,
		SessionID: sessionID,
		CostUSD:   cost,
		BudgetUSD: node.BudgetUSD,
		Verdict:   ledger.VerdictFail,
		Duration:  duration,
		// A *NodeBudgetError already spells out budgeted-vs-actual and the
		// overage, so the failing path needs no separate budget note.
		Detail: cause.Error(),
	}
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
