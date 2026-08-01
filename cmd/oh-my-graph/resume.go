package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/jitokim/oh-my-graph/internal/gate"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/handoff"
	"github.com/jitokim/oh-my-graph/internal/ledger"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
	"github.com/jitokim/oh-my-graph/internal/verify"
)

// stateFileName and lockFileName are the two files a run directory holds
// beyond node artifacts: the resumable snapshot and the concurrent-resume
// guard.
const (
	stateFileName = "state.json"
	lockFileName  = "resume.lock"
)

// runResume is the `resume` subcommand: parse argv and wire the production
// ClaudeCLIRunner. Split from executeResume the same way runGraph/runAuto are
// split from executeGraph/executePlan, so a test can inject a FakeRunner
// instead of spawning a real claude subprocess.
func runResume(args []string) error {
	flags := newResumeFlags()
	if err := flags.parse(args); err != nil {
		return err
	}
	return executeResume(flags, runner.NewClaudeCLIRunner())
}

// executeResume loads a run's snapshot and continues it in one of two modes:
// the default gate mode applies exactly one new gate decision to a paused run,
// while --retry-failed clears a halted run's FAILED records and re-executes
// only the non-passed nodes (see resumeRetryLeg).
func executeResume(flags *resumeFlags, nodeRunner runner.NodeRunner) error {
	// A pure flag contradiction fails before any state is touched: retrying
	// failures and deciding a gate are separate resumes — a retry leg replays
	// prior gate decisions unchanged and must never sneak a new one in.
	if flags.retryFailed && (flags.approveGate != "" || flags.rejectGate != "") {
		return fmt.Errorf("resume: --retry-failed cannot be combined with --approve or --reject (decide the gate in its own resume)")
	}

	runID := flags.runID
	runDir := runDirFor(runID)
	statePath := filepath.Join(runDir, stateFileName)
	lockPath := filepath.Join(runDir, lockFileName)

	// The lock guards the whole resume, not just the scheduler run: two
	// concurrent resumes racing to load and rewrite the same snapshot would
	// double-run nodes even before either scheduler starts (DESIGN.md,
	// "resume.lock ... guards against two concurrent resumes of the same run
	// id double-running nodes").
	release, err := runstate.AcquireLock(lockPath)
	if err != nil {
		return err
	}
	defer release()

	snap, err := runstate.Load(statePath)
	if err != nil {
		return fmt.Errorf("load run %q: %w", runID, err)
	}
	warnIfGraphSourceChanged(snap)

	if flags.retryFailed {
		return resumeRetryLeg(flags, snap, nodeRunner)
	}
	return resumeGateLeg(flags, snap, nodeRunner)
}

// resumeGateLeg is the gate mode: apply exactly one new gate decision to a
// paused run and continue the graph from where the earlier leg left off. It
// carries every snapshot record forward unchanged — passed AND failed — so a
// settled node never re-runs and the resumed ledger stays honest about the
// whole run.
func resumeGateLeg(flags *resumeFlags, snap runstate.Snapshot, nodeRunner runner.NodeRunner) error {
	if snap.Gate.PausedAt == "" {
		return fmt.Errorf("run %q is not paused (nothing to resume; a failed run is retried with --retry-failed)", flags.runID)
	}
	gateID, decision, err := resumeDecision(flags, snap.Gate.PausedAt)
	if err != nil {
		return err
	}
	decisions := mergedGateDecisions(snap.Gate.Decisions, gateID, runstate.GateDecision(decision))
	banner := fmt.Sprintf("Resuming run %q (gate %q %s)", flags.runID, gateID, decisionVerb(decision))
	return continueRun(flags, snap, snap.Nodes, decisions, banner, nodeRunner)
}

// resumeRetryLeg is the --retry-failed mode: keep every PASSED node's record
// and artifact as-is, clear the FAILED records, and run the graph again so
// only the cleared nodes — plus any node an earlier leg cancelled or never
// reached, which has no record to clear — execute. Prior gate decisions
// replay unchanged through the same RecordedController a gate resume uses.
//
// A run with nothing to clear may still be unfinished: a session-limit pause
// (ADR 0009) records the limited nodes NOWHERE — they never really ran — so
// such a run holds only PASS records plus launchable, un-recorded nodes, and
// --retry-failed is exactly the command its exit hint promised would finish
// it. Only when there is genuinely nothing to launch (all passed, or a
// rejected gate's standing decision blocks the rest) is there no leg at all:
// the run directory is left untouched (no new run_started bracket, no spawn)
// and the command exits 0. A gate-paused run is redirected to its own resume
// mode rather than run — the pending gate needs a human decision, which a
// retry leg must never sneak past.
func resumeRetryLeg(flags *resumeFlags, snap runstate.Snapshot, nodeRunner runner.NodeRunner) error {
	retained, cleared := partitionForRetry(snap)
	banner := fmt.Sprintf("Resuming run %q (retrying failed nodes: %s)", flags.runID, strings.Join(cleared, ", "))
	if len(cleared) == 0 {
		if snap.Gate.PausedAt != "" {
			fmt.Fprintf(os.Stdout, "run %q has no failed nodes to retry.\n", flags.runID)
			fmt.Fprintf(os.Stdout, "It is paused at gate %q — decide it with --approve %s or --reject %s instead.\n",
				snap.Gate.PausedAt, snap.Gate.PausedAt, snap.Gate.PausedAt)
			return nil
		}
		unfinished, err := hasUnfinishedWork(snap.Graph, retained)
		if err != nil {
			return fmt.Errorf("reconstruct graph for run %q: %w", flags.runID, err)
		}
		if !unfinished {
			fmt.Fprintf(os.Stdout, "run %q has no failed nodes to retry.\n", flags.runID)
			return nil
		}
		banner = fmt.Sprintf("Resuming run %q (running unfinished nodes)", flags.runID)
	}
	return continueRun(flags, snap, retained, snap.Gate.Decisions, banner, nodeRunner)
}

// hasUnfinishedWork reports whether a retry leg carrying exactly the retained
// records would launch anything at all: some node with no record whose
// dependencies are all retained PASSes. This is the scheduler's own initial
// launch condition (ReadyGiven(completed) minus settled), applied ahead of
// time — a node buried behind a retained FAIL (a rejected gate's pruned
// subtree) is unreachable and does not count, so a retry of such a run stays
// a clean no-op instead of opening an empty leg.
func hasUnfinishedWork(graphJSON []byte, retained map[string]runstate.NodeRecord) (bool, error) {
	g, err := graph.Parse(graphJSON)
	if err != nil {
		return false, err
	}
	carried := runstate.Snapshot{Nodes: retained}
	settled := carried.SettledNodes()
	for _, id := range g.ReadyGiven(carried.CompletedNodes()) {
		if !settled[id] {
			return true, nil
		}
	}
	return false, nil
}

// partitionForRetry splits a snapshot's node records for --retry-failed:
// retained records carry forward as-is — every PASS (dependents interpolate
// their artifacts exactly as after a gate resume), plus a rejected gate's
// FAIL, because a rejection is a standing human decision and not a failure to
// salvage (retrying it would only replay the recorded reject) — and cleared
// lists, sorted for a stable banner, the FAILED node ids whose records are
// dropped so the retry leg re-executes them. Cancelled/never-settled nodes
// have no record and need no clearing: they become runnable again once their
// parents settle.
func partitionForRetry(snap runstate.Snapshot) (retained map[string]runstate.NodeRecord, cleared []string) {
	retained = make(map[string]runstate.NodeRecord, len(snap.Nodes))
	for id, rec := range snap.Nodes {
		if rec.Verdict == runstate.VerdictPass || snap.Gate.Decisions[id] == runstate.GateReject {
			retained[id] = rec
			continue
		}
		cleared = append(cleared, id)
	}
	sort.Strings(cleared)
	return retained, cleared
}

// continueRun is the shared back half of both resume modes: rebuild the run's
// collaborators from the snapshot (graph, handoff, ledger, recorder, event
// stream, worktrees), seed the scheduler so exactly the carried records never
// re-run, and execute the leg. records is the set of node records this leg
// carries forward — all of snap.Nodes for a gate resume, the retained subset
// for a retry — and decisions is the gate-decision map the leg replays.
func continueRun(flags *resumeFlags, snap runstate.Snapshot, records map[string]runstate.NodeRecord, decisions map[string]runstate.GateDecision, banner string, nodeRunner runner.NodeRunner) error {
	runID := flags.runID
	runDir := runDirFor(runID)

	g, err := graph.Parse(snap.Graph)
	if err != nil {
		return fmt.Errorf("reconstruct graph for run %q: %w", runID, err)
	}
	// A resumed leg re-warns exactly as `run` did at load: the warning is
	// promised to be loud and never silent (DESIGN.md), and a resume may be
	// far from the terminal session that saw the first one.
	warnBypassPermissions(g)

	h := handoff.New(runDir, snap.Inputs)
	for nodeID, rec := range records {
		h.Seed(nodeID, rec.ArtifactPath, rec.SessionID)
	}

	led := ledger.New(runID)
	for nodeID, rec := range records {
		led.Record(ledger.Record{
			NodeID:    nodeID,
			SessionID: rec.SessionID,
			CostUSD:   rec.CostUSD,
			BudgetUSD: rec.BudgetUSD,
			Verdict:   ledger.Verdict(rec.Verdict),
			Duration:  rec.Duration,
			Detail:    rec.Detail,
		})
	}

	recorder := runstate.NewSnapshotRecorder(filepath.Join(runDir, stateFileName), runstate.Snapshot{
		RunID:           runID,
		GraphSourcePath: snap.GraphSourcePath,
		GraphSHA256:     snap.GraphSHA256,
		Graph:           snap.Graph,
		Inputs:          snap.Inputs,
		ContinueOnFail:  snap.ContinueOnFail,
		ToolPolicies:    snap.ToolPolicies,
		Nodes:           records,
		// PausedAt starts empty: the run is actively continuing, not paused,
		// until (if at all) this leg pauses again at a later gate.
		Gate: runstate.GateState{Decisions: decisions},
	})

	// Reopened in append mode, so the resumed leg continues the same
	// events.jsonl the first leg started — the stream records the whole run's
	// history across legs, each bracketed by its own run_started/run_finished
	// (docs/RUN-FEED.md).
	feed, err := runfeed.NewStreamWriter(filepath.Join(runDir, runfeed.FileName), runID)
	if err != nil {
		return fmt.Errorf("prepare run event stream: %w", err)
	}
	defer feed.Close()

	// The resumed leg manages worktrees in the same per-run location the
	// first leg did. Its checkouts are fresh, though — a retry leg included,
	// which provisions exactly as a fresh run would rather than reattaching
	// anything: a branch an earlier leg retained with commits still exists,
	// so a resumed node re-declaring that worktree name fails loudly on the
	// ref collision rather than silently resetting retained work.
	worktrees := worktreeManagerFor(runID)

	// carried re-derives the two scheduler seed sets from exactly the records
	// this leg carries forward, through the same Snapshot methods a fresh
	// load uses — a record cleared by --retry-failed is in neither set, which
	// is precisely what makes its node run again.
	carried := runstate.Snapshot{Nodes: records}
	scheduler := schedule.NewScheduler(nodeRunner, schedule.Options{
		Concurrency:    flags.concurrency,
		ContinueOnFail: snap.ContinueOnFail,
		Gate:           gate.NewRecordedController(toGateDecisions(decisions)),
		Verifier:       verify.NewShellVerifier(),
		Worktrees:      worktrees,
		ToolPolicies:   toRunnerToolPolicies(snap.ToolPolicies),
		Recorder:       recorder,
		EventSink:      feed,
		// CompletedNodes seeds the resumed leg's ready set from
		// graph.ReadyGiven(completed) instead of graph.Roots(), so a node the
		// first leg already finished is never re-run (and re-paid for).
		CompletedNodes: carried.CompletedNodes(),
		// SettledNodes additionally covers a node that FAILED on an earlier
		// leg (absent from CompletedNodes by design, so it does not wrongly
		// unblock its dependents), so THAT node itself still never re-runs
		// either — otherwise --continue-on-fail's "this failure is final"
		// would silently be undone by a later resume.
		SettledNodes: carried.SettledNodes(),
	})

	fmt.Fprintf(os.Stdout, "%s\n\n", banner)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runErr := scheduler.Run(ctx, g, h, led)
	reportWorktreeCleanup(os.Stderr, worktrees.Cleanup(context.Background()))

	fmt.Fprintln(os.Stdout)
	led.Print(os.Stdout)
	printPauseHint(os.Stdout, runID, runErr)

	return runErr
}

// resumeDecision turns the flags this invocation was given, plus the gate the
// snapshot says the run is actually paused at, into the one gate id/decision
// pair to apply. It enforces the CLI contract from DESIGN.md:
//
//   - exactly one of --approve/--reject is required;
//   - a bare `resume <run-id>` names the pending gate in its error rather
//     than silently approving anything;
//   - the named gate must match where the run is actually paused, so
//     resuming an old run can never approve a gate the user was not looking
//     at.
func resumeDecision(flags *resumeFlags, pausedAt string) (gateID string, decision gate.Decision, err error) {
	switch {
	case flags.approveGate != "" && flags.rejectGate != "":
		return "", "", fmt.Errorf("resume: --approve and --reject are mutually exclusive")
	case flags.approveGate != "":
		gateID, decision = flags.approveGate, gate.DecisionApprove
	case flags.rejectGate != "":
		gateID, decision = flags.rejectGate, gate.DecisionReject
	default:
		return "", "", fmt.Errorf(
			"run is paused at gate %q; resume with --approve %s or --reject %s",
			pausedAt, pausedAt, pausedAt,
		)
	}
	if gateID != pausedAt {
		return "", "", fmt.Errorf("resume named gate %q but the run is paused at %q", gateID, pausedAt)
	}
	return gateID, decision, nil
}

// mergedGateDecisions copies prior into a new map with gateID's new decision
// applied, so neither the caller's prior map nor the snapshot on disk is
// mutated out from under this leg before it has actually run anything.
func mergedGateDecisions(prior map[string]runstate.GateDecision, gateID string, decision runstate.GateDecision) map[string]runstate.GateDecision {
	merged := make(map[string]runstate.GateDecision, len(prior)+1)
	for id, d := range prior {
		merged[id] = d
	}
	merged[gateID] = decision
	return merged
}

// toGateDecisions converts a runstate.GateDecision map (the persisted
// snapshot's shape) into a gate.Decision map (what gate.RecordedController
// takes) at the CLI boundary — the one place DESIGN.md assigns this
// conversion, so neither package depends on the other's type. The underlying
// string values are defined identically in both packages by construction
// (approve/reject/pause), so the conversion cannot fail.
func toGateDecisions(decisions map[string]runstate.GateDecision) map[string]gate.Decision {
	out := make(map[string]gate.Decision, len(decisions))
	for id, d := range decisions {
		out[id] = gate.Decision(d)
	}
	return out
}

// toRunnerToolPolicies is toNodeToolPolicies' inverse: it converts the
// snapshot's runstate.NodeToolPolicy map back into the runner.ToolPolicy map
// the Scheduler takes, preserving nilness (see toNodeToolPolicies) so a
// hand-written `run`'s resumed leg still imposes no ceiling at all.
func toRunnerToolPolicies(policies map[string]runstate.NodeToolPolicy) map[string]runner.ToolPolicy {
	if policies == nil {
		return nil
	}
	out := make(map[string]runner.ToolPolicy, len(policies))
	for id, p := range policies {
		out[id] = runner.ToolPolicy{
			AllowedTools:    p.AllowedTools,
			DisallowedTools: p.DisallowedTools,
			Tools:           p.Tools,
			SettingSources:  p.SettingSources,
			StrictMCPConfig: p.StrictMCPConfig,
		}
	}
	return out
}

// decisionVerb renders a gate.Decision as the past-tense word the resume
// banner reports — "approved" or "rejected". resumeDecision only ever
// produces one of these two for a decision actually applied by `resume`
// (DecisionPause is never a CLI-supplied decision), so there is no third case.
func decisionVerb(d gate.Decision) string {
	if d == gate.DecisionApprove {
		return "approved"
	}
	return "rejected"
}

// warnIfGraphSourceChanged prints a stderr warning when snap.GraphSourcePath
// exists on disk but no longer hashes to snap.GraphSHA256 — DESIGN.md,
// "GraphSHA256 ... exists so a resume can warn out loud when GraphSourcePath
// has changed on disk since the snapshot was taken, rather than silently
// ignoring the edit." A resume always re-runs the graph exactly as it was
// snapshotted (graph.Parse(snap.Graph), never a re-read of the path), so this
// is advisory only: it can never fail the resume, and a missing/unreadable
// source file (auto mode's graph.json may have been cleaned up, or the path
// may be relative to a different cwd) is silently skipped rather than treated
// as evidence of a change.
func warnIfGraphSourceChanged(snap runstate.Snapshot) {
	if snap.GraphSourcePath == "" || snap.GraphSHA256 == "" {
		return
	}
	data, err := os.ReadFile(snap.GraphSourcePath)
	if err != nil {
		return
	}
	if sha256Hex(data) != snap.GraphSHA256 {
		fmt.Fprintf(os.Stderr,
			"WARNING: %q has changed on disk since this run started; resume continues the graph exactly as it was snapshotted, not as the file now reads.\n",
			snap.GraphSourcePath,
		)
	}
}
