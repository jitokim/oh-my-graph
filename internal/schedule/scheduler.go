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

// maxDetailRunes is the one shared bound on a detail string — applied at every
// point a message becomes a ledger/snapshot/event Detail: a verification
// command's output tail (outputTail), every cause in failRecord, a
// session-limit pause's node list (runFinishedEvent), and both halves of a
// feedback round's narration (judgeFeedback). Enough to see the
// failing assertion, short enough not to swamp the end-of-run table or an
// events.jsonl line (which must stay tailable even when an error is
// arbitrarily long).
const maxDetailRunes = 240

// capDetail bounds a detail string at maxDetailRunes, keeping the TAIL and
// marking the cut — the same choice tailOf and outputTail make, because this
// codebase's long strings put the payload last (a stderr complaint, a failing
// command's output) and the head they lose (the node id, the predicate) is
// carried separately by every surface that shows a Detail.
func capDetail(s string) string {
	if runes := []rune(s); len(runes) > maxDetailRunes {
		return "…" + string(runes[len(runes)-maxDetailRunes:])
	}
	return s
}

// Recorder persists the run's resumable snapshot (internal/runstate) as the
// run progresses. The Scheduler talks to this narrow interface and never
// imports runstate's concrete SnapshotRecorder itself, so a scheduler test can
// inject a fake with zero filesystem I/O — exactly the seam gate.GateController
// and verify.Verifier already are (DESIGN.md, "The Scheduler talks to a
// Recorder interface and defaults to a no-op, so nothing about persistence
// leaks into the engine's tests").
type Recorder interface {
	// RecordNode is called when a node reaches a terminal verdict — pass, fail,
	// gate-approved, or gate-rejected — so the snapshot stays current after
	// EVERY node, not only at a gate pause (DESIGN.md, "Snapshot writes happen
	// after every node"). It is NOT called exactly once per node: a feedback
	// declarer (ADR 0010) also records a verdict-LESS marker for every round it
	// fires (judgeFeedback), so a node inside a feedback loop arrives
	// here several times before its terminal record lands. An implementation
	// must treat a later call as SUPERSEDING the earlier one rather than as a
	// second node — runstate.SnapshotRecorder carries the superseded round's
	// cost forward for exactly this reason.
	// A non-nil error here is non-fatal to this leg (the
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
	// whole run (the --continue-on-fail flag). It ORs with the graph's own
	// `on_fail: continue` declaration — either saying continue means continue
	// (see effectiveContinueOnFail) — so a false here still continues when the
	// graph itself opted in.
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
	// pass or fail — in an earlier leg, and so are not launched again on resume
	// by the ordinary ready-set path. nil (the zero value) for a fresh
	// `run`/`auto`.
	//
	// "Not again" is a default, not an absolute: a feedback arc (ADR 0010) that
	// fires re-arms its whole loop body, and a re-armed node launches even
	// though it is settled (see launch's `rearmed` check). That is the point of
	// a feedback edge — the body's earlier verdict is exactly what the arc is
	// reacting to — and the re-arm is deliberately scoped to the loop body, so
	// nothing outside it loses this guard.
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
	// NodeRounds seeds each node's current feedback round for a resumed leg
	// (ADR 0010): the resume path derives it from a declarer's non-terminal
	// marker record (round k → every body node resumes at round k), so the
	// leg's events and snapshot records carry the right round and the
	// declarer's remaining budget is max − k. nil for a fresh run — every
	// node starts at round 0.
	NodeRounds map[string]int
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
	// nodeRounds seeds the feedback state per Run — see Options.NodeRounds.
	nodeRounds map[string]int
	// feedback is this Run's view of every feedback arc: rounds, bodies,
	// re-arming in-degrees (ADR 0010). Built by execute() from the graph it
	// is handed — the one piece of per-run state the Scheduler holds, which
	// is safe because every caller constructs a Scheduler per run; a second
	// sequential Run simply rebuilds it.
	feedback *feedbackState
	// sessionIDs mints the pre-assigned session id for each fresh-session
	// claude attempt (runner.NewSessionID in production; tests script it for
	// determinism). Assigning the id BEFORE the child spawns is what lets
	// node_started/node_retried publish it, so a live view can locate a
	// RUNNING node's transcript instead of waiting for the terminal envelope.
	// Parallel nodes mint from separate goroutines, so the function must be
	// safe for concurrent use (crypto/rand is).
	sessionIDs func() string
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
		nodeRounds:     opts.NodeRounds,
		sessionIDs:     runner.NewSessionID,
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
	s.emitEvent(runFinishedEvent(err))
	return err
}

// haltCause records which node's failure halted the run, so a sibling the halt
// cancelled can be recorded with the causal story — `cancelled: run halted
// after node "X" failed` — instead of the Go plumbing string ("claude run:
// context canceled") its cancellation surfaces as. It is written strictly
// BEFORE the shared context is cancelled (the errgroup cancels only after the
// failing node's goroutine returns, and note runs before that return), so any
// sibling that observes the cancellation is guaranteed to see the cause set.
type haltCause struct {
	mu     sync.Mutex
	nodeID string
}

// note records the failing node, first writer wins: concurrent failures may
// race to halt, but the run tells one story.
func (c *haltCause) note(nodeID string) {
	c.mu.Lock()
	if c.nodeID == "" {
		c.nodeID = nodeID
	}
	c.mu.Unlock()
}

// explain rewrites a context-cancellation error into the halt's causal story
// when a halt is on record. Every other error passes through untouched — in
// particular a cancellation with no halt recorded (the user's own Ctrl-C
// cancels the same context) and a per-node timeout (DeadlineExceeded), which
// is that node's own failure, not collateral of someone else's.
func (c *haltCause) explain(err error) error {
	if !errors.Is(err, context.Canceled) {
		return err
	}
	c.mu.Lock()
	nodeID := c.nodeID
	c.mu.Unlock()
	if nodeID == "" {
		return err
	}
	return fmt.Errorf("cancelled: run halted after node %q failed", nodeID)
}

// execute is Run's body — everything between the run_started/run_finished pair.
func (s *Scheduler) execute(ctx context.Context, g *graph.Graph, h *handoff.Handoff, led *ledger.RunLedger) error {
	sem := make(chan struct{}, effectiveConcurrency(s.concurrency, g.Concurrency))
	continueOnFail := effectiveContinueOnFail(s.continueOnFail, g)
	s.feedback = newFeedbackState(g, s.nodeRounds)
	grp, ctx := errgroup.WithContext(ctx)
	halt := &haltCause{}

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

	// rearmed (guarded by mu, like inDegree) is the set of node ids a fired
	// feedback arc has re-armed this leg. It exists for the resumed-leg case:
	// a body node that PASSED in an earlier leg is in settledNodes, and
	// without this override the launch guard below would refuse to re-run it
	// — but a fired arc supersedes that verdict for exactly the body's nodes
	// (the marker made that durable), so membership here bypasses the guard.
	// settledNodes itself stays untouched: it is the caller's map.
	rearmed := make(map[string]bool)

	// pauseMu guards the run's pause state: pausedAt (the id of the gate whose
	// DecisionPause first stopped the run, or "" while no gate has paused it)
	// and limitedNodes/limitCause (the nodes whose subprocess hit the
	// subscription session limit, and the first one's captured cause — ADR
	// 0009). Both are the mechanism behind "stop launching new work but drain
	// what's in flight" — unlike halt-on-fail, a pause deliberately does NOT
	// cancel ctx (ADR 0003, "this is the one place the halt path does not
	// cancel the context"), so an in-flight sibling runs to completion
	// normally — and a draining sibling may itself hit the limit and join
	// limitedNodes. What stops is new launches: every successful node checks
	// this state before enqueuing its own dependents, so the moment anything
	// pauses, the wavefront of new work stops advancing while whatever already
	// started keeps running.
	var pauseMu sync.Mutex
	var pausedAt string
	var limitedNodes []string
	var limitCause string

	var launch func(id string)
	launch = func(id string) {
		mu.Lock()
		wasRearmed := rearmed[id]
		mu.Unlock()
		if s.settledNodes[id] && !wasRearmed {
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

			if err := s.runNode(ctx, node, h, led, halt); err != nil {
				var pause *pauseSignal
				if errors.As(err, &pause) {
					pauseMu.Lock()
					if pausedAt == "" {
						pausedAt = pause.NodeID
					}
					pauseMu.Unlock()
					return nil
				}
				var limit *limitSignal
				if errors.As(err, &limit) {
					// A session limit pauses the run BEFORE the failure
					// policies below can see it: it is not the node's failure
					// (continue-on-fail must not prune its subtree as final)
					// and not a reason to kill in-flight siblings (halt must
					// not cancel them) — they drain, exactly as at a gate
					// pause (ADR 0009).
					pauseMu.Lock()
					limitedNodes = append(limitedNodes, limit.NodeID)
					if limitCause == "" {
						limitCause = limit.Cause
					}
					pauseMu.Unlock()
					return nil
				}
				var feedback *feedbackSignal
				if errors.As(err, &feedback) {
					// A fired feedback arc (ADR 0010) — the fourth intercepted
					// signal. The non-final record trio is already durable
					// (judgeFeedback wrote it); what happens here is pure
					// scheduling. A pause elsewhere suppresses the relaunch
					// exactly as it suppresses a successful node's dependents
					// — the marker means a later resume re-enters the loop.
					pauseMu.Lock()
					paused := pausedAt != "" || len(limitedNodes) > 0
					pauseMu.Unlock()
					if paused {
						return nil
					}
					// Re-arm the body: clear each member's completed status by
					// re-seeding its in-degree counting only IN-BODY parents
					// (out-of-body parents ran once and stay satisfied), mark
					// the members re-armed so a cross-leg settled verdict
					// cannot block their relaunch, and launch the target —
					// the one body node whose in-body in-degree is zero.
					// Independent branches are untouched. All body nodes had
					// settled before the declarer could fail, so no member is
					// in flight while this rewires it.
					mu.Lock()
					for member, degree := range s.feedback.bodyInDegrees(feedback.NodeID) {
						inDegree[member] = degree
						rearmed[member] = true
					}
					mu.Unlock()
					launch(feedback.Target)
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
				if continueOnFail {
					failMu.Lock()
					prunedFailures = append(prunedFailures, id)
					failMu.Unlock()
					// Prune the subtree: do not enqueue dependents, but do not
					// cancel the shared context — independent branches continue.
					return nil
				}
				// Halt-on-fail: returning a non-nil error cancels ctx, which
				// propagates to every in-flight child (killing wedged claude
				// subprocesses) and stops new nodes from starting. The cause
				// is noted first, so a cancelled sibling can name it (see
				// haltCause — the ordering is what makes that race-free).
				halt.note(id)
				return &HaltError{NodeID: id, Err: err}
			}

			pauseMu.Lock()
			paused := pausedAt != "" || len(limitedNodes) > 0
			pauseMu.Unlock()
			if paused {
				// This node's own success still counts — it is drained, not
				// cancelled — but a pause elsewhere (a gate decision or a
				// session limit) already means the whole run stops, not just
				// this branch, so its dependents must not launch.
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
	limited := append([]string(nil), limitedNodes...)
	cause := limitCause
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

	if len(limited) > 0 {
		// A session-limit pause needs no RecordPause: the limited nodes are
		// deliberately absent from the snapshot (they never really ran), and
		// every settled node was already persisted per node, so the state on
		// disk is exactly what `resume --retry-failed` reads. A gate pause
		// above wins when both happened — the gate needs its human decision
		// first, and the gate leg re-runs the un-recorded limited nodes
		// anyway. The limit in turn wins over pruned continue-on-fail
		// failures below, mirroring the gate-pause ordering: the run is
		// unfinished-and-resumable, not finished-and-failed, and the retained
		// FAIL records still tell the failures' story (a --retry-failed
		// clears and re-runs them together with the limited nodes).
		sort.Strings(limited)
		return &LimitPausedError{NodeIDs: limited, Cause: cause}
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
// *NodeBudgetError) after retries are exhausted. A feedback declarer's
// judgment failure with rounds remaining returns a *feedbackSignal instead
// (see judgeFeedback), which Run() intercepts and turns into the body's
// re-arm rather than a failure. A gate node never reaches this general path
// at all; see evaluateGate for its three outcomes, including the internal
// *pauseSignal/*rejectSignal Run() intercepts before they could be mistaken
// for one of these.
func (s *Scheduler) runNode(ctx context.Context, node graph.Node, h *handoff.Handoff, led *ledger.RunLedger, halt *haltCause) error {
	start := time.Now()
	s.logProgress("▶ %s  running…\n", node.ID)

	// A fresh-session claude node has its session id minted HERE, before
	// anything spawns, so node_started can publish it and a live view can
	// locate the transcript of a node that is still RUNNING — the terminal
	// events used to be the id's first appearance. A gate spawns no subprocess
	// and a session-handoff node continues its parent's session (whose id the
	// parent's own terminal event already published), so neither carries one.
	sessionID := ""
	if node.Type != graph.TypeGate && node.Handoff != graph.HandoffSession {
		sessionID = s.sessionIDs()
	}
	s.emitEvent(runfeed.Event{Type: runfeed.EventNodeStarted, NodeID: node.ID, SessionID: sessionID, Round: s.feedback.roundOf(node.ID)})

	if node.Type == graph.TypeGate {
		return s.evaluateGate(ctx, node, h, led, start)
	}

	invocation, err := s.buildInvocation(ctx, node, h, sessionID)
	if err != nil {
		return s.recordFail(led, h, node, "", 0, time.Since(start), 0, err)
	}

	attempts := 1
	if node.Retry != nil && node.Retry.Max > 0 {
		attempts += node.Retry.Max
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		outcome, runErr := s.runner.Run(ctx, invocation)
		if runErr != nil {
			// A context kill (the node's own timeout, a halt's cancellation,
			// Ctrl-C) ends the subprocess before it prints the JSON envelope
			// that carries total_cost_usd — judged here, before halt.explain
			// rewrites the error and drops the sentinel from the chain.
			killedBeforeReporting := errors.Is(runErr, context.Canceled) ||
				errors.Is(runErr, context.DeadlineExceeded)
			// A sibling this run's halt cancelled surfaces here as a bare
			// context cancellation; rewrite it into the causal story before
			// it is recorded anywhere.
			runErr = halt.explain(runErr)
			lastErr = runErr
			if s.shouldRetry(node, attempt, attempts, causeFromRunError(runErr)) {
				s.recordRetry(node, attempt+1, s.prepareRetry(&invocation))
				continue
			}
			if killedBeforeReporting {
				// Honest accounting (run 20260802-062446): the row's $0.0000
				// is "unreported", not "free". Recovering the true figure
				// would mean re-deriving cost from the session transcript
				// against model pricing tables only the CLI owns — fragile
				// coupling for a diagnostic — so the detail says so instead.
				runErr = fmt.Errorf("%w; cost unknown (killed before reporting)", runErr)
			}
			return s.recordFail(led, h, node, "", 0, time.Since(start), attempt, runErr)
		}

		if outcome.SessionLimited {
			// The subscription's session limit killed this subprocess — the
			// account's state, not the node's failure, and not final (the
			// limit resets). No ledger row, no snapshot record, no terminal
			// event, no retry (an immediate re-spawn meets the same limit):
			// the node stays un-run, and Run() turns this signal into the
			// whole-run pause (ADR 0009).
			s.logProgress("⏸ %s  session limit reached — pausing run\n", node.ID)
			return &limitSignal{NodeID: node.ID, Cause: outcome.FailureCause}
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
			s.recordRetry(node, attempt+1, s.prepareRetry(&invocation))
			continue
		}
		// The feedback arc is consulted exactly where the failure would
		// otherwise become final — after retries, before recordFail. A fired
		// arc returns the signal Run's launch loop intercepts; an exhausted
		// (or absent, or non-judgment) one falls through to the terminal
		// FAIL, its cause wrapped with the spend when rounds were used up.
		signal, finalErr := s.judgeFeedback(node, outcome, h, led, time.Since(start), verdictErr)
		if signal != nil {
			return signal
		}
		return s.recordFail(led, h, node, outcome.SessionID, outcome.TotalCostUSD, time.Since(start), attempt, finalErr)
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
	appendRoundNote(&rec, s.feedback.roundNote(node.ID))
	led.Record(rec)
	s.recordSnapshot(node, rec, h)
	s.emitEvent(terminalEvent(runfeed.EventNodeFailed, rec, attempt, s.feedback.roundOf(node.ID)))
	return cause
}

// recordPass writes the node's live "✓ PASS" progress line, its ledger row,
// the same record into the resumable snapshot, and the matching node_passed
// event, then returns nil so callers can `return s.recordPass(...)` directly.
func (s *Scheduler) recordPass(led *ledger.RunLedger, h *handoff.Handoff, node graph.Node, outcome runner.NodeOutcome, duration time.Duration, attempt int) error {
	s.logProgress("✓ %s  %s  $%.4f  %s\n", node.ID, ledger.VerdictPass, outcome.TotalCostUSD, duration.Round(time.Millisecond))
	rec := passRecord(node, outcome, duration, attempt)
	appendRoundNote(&rec, s.feedback.roundNote(node.ID))
	led.Record(rec)
	s.recordSnapshot(node, rec, h)
	s.emitEvent(terminalEvent(runfeed.EventNodePassed, rec, attempt, s.feedback.roundOf(node.ID)))
	return nil
}

// appendRoundNote joins a body node's "feedback round k/N" note onto its
// ledger detail, so every execution's row says which round it priced
// (ADR 0010, "the ledger prices every execution"). A no-op outside a round.
func appendRoundNote(rec *ledger.Record, note string) {
	if note == "" {
		return
	}
	if rec.Detail != "" {
		rec.Detail += "; "
	}
	rec.Detail += note
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
	// A gate is never in a feedback body (validated), so its round is 0.
	s.emitEvent(terminalEvent(runfeed.EventNodePassed, rec, 0, 0))
	return nil
}

// prepareRetry rebinds invocation for the attempt after a failed one and
// returns the fresh session id that attempt will run under. A retry never
// resumes a session — not the failed attempt's own, and not a session-handoff
// parent's either: the retried attempt starts fresh, which passRecord's
// detail states outright for a session node so the ledger, snapshot and
// events all say it. Nor can it reuse the failed attempt's pre-assigned id —
// that id already names a real session the failed child wrote — so it gets a
// new one, which recordRetry publishes on node_retried for the same
// live-view reason node_started publishes the first.
func (s *Scheduler) prepareRetry(invocation *runner.NodeInvocation) string {
	invocation.ResumeSession = ""
	invocation.SessionID = s.sessionIDs()
	return invocation.SessionID
}

// recordRetry writes the node's live "↻ retry" progress line and the matching
// node_retried event, carrying the retried attempt's pre-assigned session id.
// retryNumber is 1-based: the first retry after the initial attempt is 1.
func (s *Scheduler) recordRetry(node graph.Node, retryNumber int, sessionID string) {
	s.logProgress("↻ %s  retry\n", node.ID)
	s.emitEvent(runfeed.Event{Type: runfeed.EventNodeRetried, NodeID: node.ID, Retries: retryNumber, SessionID: sessionID, Round: s.feedback.roundOf(node.ID)})
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
	if err := s.recorder.RecordNode(node.ID, toNodeRecord(rec, artifactPath, s.feedback.roundOf(node.ID))); err != nil {
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
func toNodeRecord(rec ledger.Record, artifactPath string, round int) runstate.NodeRecord {
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
		Round:        round,
	}
}

// terminalEvent derives a node's terminal runfeed.Event from the ledger.Record
// the Scheduler already built for the same transition — one accounting
// computation, three destinations (ledger, snapshot, event stream) — so the
// stream can never carry a different cost/verdict/detail than the other two.
// retries is the number of retries that preceded the terminal attempt;
// round is the feedback round the execution belonged to (0 outside a loop).
func terminalEvent(eventType runfeed.EventType, rec ledger.Record, retries, round int) runfeed.Event {
	return runfeed.Event{
		Type:      eventType,
		NodeID:    rec.NodeID,
		Verdict:   string(rec.Verdict),
		CostUSD:   rec.CostUSD,
		SessionID: rec.SessionID,
		Retries:   retries,
		Detail:    rec.Detail,
		Round:     round,
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

// runFinishedEvent builds the leg's closing run_finished event: the outcome
// token, plus — for a session-limit pause only — a detail naming the limited
// nodes and the captured cause, so a stream consumer can tell a "waiting for
// a human" pause from a "wait for the limit to reset" one without a new event
// type or outcome token (docs/RUN-FEED.md, the additive-field rule).
func runFinishedEvent(err error) runfeed.Event {
	e := runfeed.Event{Type: runfeed.EventRunFinished, Outcome: runOutcome(err)}
	var limit *LimitPausedError
	if errors.As(err, &limit) {
		e.Detail = capDetail(fmt.Sprintf("session limit reached at %s: %s", strings.Join(limit.NodeIDs, ", "), limit.Cause))
	}
	return e
}

// runOutcome maps Run's return into the run_finished outcome token: nil is
// passed, a *PausedError or *LimitPausedError is paused (a pause is not a
// failure — ADR 0003, ADR 0009), and anything else (halt, pruned failures, a
// failed pause persist) is failed.
func runOutcome(err error) string {
	if err == nil {
		return runfeed.OutcomePassed
	}
	var paused *PausedError
	if errors.As(err, &paused) {
		return runfeed.OutcomePaused
	}
	var limited *LimitPausedError
	if errors.As(err, &limited) {
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
// worktree may spawn git (behind the injected worktree.Provider). sessionID
// is the pre-assigned session id runNode already published on node_started —
// empty exactly when the node resumes a parent's session instead, which keeps
// SessionID and ResumeSession mutually exclusive as the runner documents.
func (s *Scheduler) buildInvocation(ctx context.Context, node graph.Node, h *handoff.Handoff, sessionID string) (runner.NodeInvocation, error) {
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
		SessionID:      sessionID,
		Agent:          node.Agent,
		BudgetUSD:      node.BudgetUSD,
		Timeout:        node.TimeoutDuration(),
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
// becomes the same *NodeCheckError with the same retry cause (verify_failed):
// they all mean "this node's success was not demonstrated", and a verification
// that could not be completed must never read as a pass. But the error keeps
// the two ways of failing apart: evidence that was gathered and judged
// insufficient is a judgment, while a verification that never reached a
// verdict — the interpolation failed, the command could not spawn or timed
// out — is an Infrastructure fault (verifyFault), so a feedback arc does not
// spend a whole body re-run on a fault no re-run can repair (ADR 0010).
func (s *Scheduler) verifyEvidence(ctx context.Context, node graph.Node, h *handoff.Handoff, nodeCwd string) error {
	verification := node.SuccessCheck.Verify
	if verification == nil {
		return nil
	}

	request, err := resolveVerification(*verification, h, nodeCwd)
	if err != nil {
		return verifyFault(node.ID, err.Error())
	}

	s.logProgress("… %s  verifying: %s\n", node.ID, request.Command)
	result, err := s.verifier.Verify(ctx, request)
	if err != nil {
		return verifyFault(node.ID, err.Error())
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
	// Already compiled once by graph.Validate at load time, so this cannot fail
	// for a graph that came through Load/Parse; it is still handled rather than
	// ignored, because a caller that hand-built a Node bypasses that guarantee.
	// It is a fault, not a failure, and it is judged before the exit code: a
	// malformed pattern rendered no verdict on the work even when the exit code
	// also mismatches, and no re-run can repair the declaration.
	var pattern *regexp.Regexp
	if v.OutputMatches != "" {
		var err error
		if pattern, err = regexp.Compile(v.OutputMatches); err != nil {
			return verifyFault(nodeID, fmt.Sprintf("invalid output_matches regex %q: %v", v.OutputMatches, err))
		}
	}

	if expected := v.ExpectedExitCode(); result.ExitCode != expected {
		return verifyFailure(nodeID, fmt.Sprintf("`%s` exited %d, want %d%s",
			command, result.ExitCode, expected, outputTail(result.Output)))
	}
	if pattern == nil {
		return nil
	}
	if !pattern.MatchString(result.Output) {
		return verifyFailure(nodeID, fmt.Sprintf("`%s` output did not match /%s/%s",
			command, v.OutputMatches, outputTail(result.Output)))
	}
	return nil
}

// verifyFailure builds the error shape for evidence that was gathered and
// judged insufficient — a verdict on the work.
func verifyFailure(nodeID, detail string) error {
	return &NodeCheckError{NodeID: nodeID, Predicate: predicateVerify, Detail: detail}
}

// verifyFault builds the error shape for a verification that could not be
// completed at all — no verdict on the work exists. Same node failure, same
// verify_failed retry cause, but marked Infrastructure so a feedback arc
// never fires on it (see isJudgmentFailure).
func verifyFault(nodeID, detail string) error {
	return &NodeCheckError{NodeID: nodeID, Predicate: predicateVerify, Detail: detail, Infrastructure: true}
}

// outputTail renders the end of a verification command's output for the ledger's
// DETAIL column: the last maxDetailRunes, whitespace-collapsed onto one
// line so a multi-line test failure cannot break the end-of-run table. The tail
// is what matters — a failing command explains itself last. Empty output yields
// an empty string rather than a dangling separator.
func outputTail(output string) string {
	condensed := strings.Join(strings.Fields(output), " ")
	if condensed == "" {
		return ""
	}
	return "; output: " + capDetail(condensed)
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

// effectiveContinueOnFail resolves the run's failure policy: the CLI
// --continue-on-fail flag ORs with the graph's own `on_fail: continue` —
// either saying continue means continue. Unlike effectiveConcurrency there is
// no override direction: the flag cannot force a halt onto a graph that
// declared continue, and the graph cannot cancel a flag the operator passed —
// both are requests to keep independent branches alive, and honouring either
// is strictly what its author asked for. Resolved here, per execute, rather
// than at the CLI so every caller (`run`, `auto`, `resume` — whose snapshot
// round-trips on_fail inside the graph JSON) gets the same rule for free.
func effectiveContinueOnFail(flag bool, g *graph.Graph) bool {
	return flag || g.ContinuesOnFail()
}

// evaluateSuccessCheck applies a node's success_check to its outcome, returning
// nil on pass or a *NodeCheckError naming the predicate that failed. An empty
// check means "exit zero is enough".
func evaluateSuccessCheck(node graph.Node, outcome runner.NodeOutcome) error {
	check := node.SuccessCheck

	if check.IsZero() {
		if outcome.ExitCode != 0 {
			return &NodeCheckError{NodeID: node.ID, Predicate: "exit_zero", Detail: exitDetail(outcome)}
		}
		return nil
	}

	if check.ExitZero && outcome.ExitCode != 0 {
		return &NodeCheckError{NodeID: node.ID, Predicate: "exit_zero", Detail: exitDetail(outcome)}
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

// exitDetail is the exit_zero failure detail: the exit code, plus the WHY when
// the runner captured one (the CLI's own error report, or its stderr tail).
// "exit code 1" alone answers what happened, not why — a subprocess killed by
// a subscription session limit read exactly like any other non-zero exit until
// the cause travelled with the outcome.
func exitDetail(outcome runner.NodeOutcome) string {
	detail := fmt.Sprintf("exit code %d", outcome.ExitCode)
	if outcome.FailureCause != "" {
		detail += ": " + outcome.FailureCause
	}
	return detail
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
		return graph.CauseOutputError
	}
	return graph.CauseRunError
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
		return graph.CauseBudgetExceeded
	}
	var checkErr *NodeCheckError
	if asErr(err, &checkErr) {
		switch checkErr.Predicate {
		case predicateVerify:
			return graph.CauseVerifyFailed
		case "result_matches":
			return graph.CauseResultMismatch
		}
	}
	return graph.CauseNonzeroExit
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
		// runNode clears ResumeSession on every retry, so a session-handoff
		// node's passing attempt ran without its parent's conversation — a
		// fact the surfaces reading this Detail would otherwise never show.
		if node.Handoff == graph.HandoffSession {
			notes = append(notes, "retry started fresh — parent session not resumed")
		}
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
		// overage, so the failing path needs no separate budget note. This is
		// the point where a cause becomes a Detail, so the shared bound is
		// applied here — an arbitrarily long error must not swamp the ledger
		// table, the snapshot, or an events.jsonl line.
		Detail: capDetail(cause.Error()),
	}
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
