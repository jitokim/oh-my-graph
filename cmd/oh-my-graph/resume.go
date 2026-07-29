package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
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

// executeResume loads a paused run's snapshot, applies exactly one new gate
// decision to it, and continues the graph from where the first leg left off.
func executeResume(flags *resumeFlags, nodeRunner runner.NodeRunner) error {
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
	if snap.Gate.PausedAt == "" {
		return fmt.Errorf("run %q is not paused (nothing to resume)", runID)
	}
	warnIfGraphSourceChanged(snap)

	gateID, decision, err := resumeDecision(flags, snap.Gate.PausedAt)
	if err != nil {
		return err
	}

	g, err := graph.Parse(snap.Graph)
	if err != nil {
		return fmt.Errorf("reconstruct graph for run %q: %w", runID, err)
	}

	h := handoff.New(runDir, snap.Inputs)
	for nodeID, rec := range snap.Nodes {
		h.Seed(nodeID, rec.ArtifactPath, rec.SessionID)
	}

	led := ledger.New(runID)
	for nodeID, rec := range snap.Nodes {
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

	decisions := mergedGateDecisions(snap.Gate.Decisions, gateID, runstate.GateDecision(decision))
	recorder := runstate.NewSnapshotRecorder(statePath, runstate.Snapshot{
		RunID:           runID,
		GraphSourcePath: snap.GraphSourcePath,
		GraphSHA256:     snap.GraphSHA256,
		Graph:           snap.Graph,
		Inputs:          snap.Inputs,
		ContinueOnFail:  snap.ContinueOnFail,
		ToolPolicies:    snap.ToolPolicies,
		Nodes:           snap.Nodes,
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

	scheduler := schedule.NewScheduler(nodeRunner, schedule.Options{
		Concurrency:    flags.concurrency,
		ContinueOnFail: snap.ContinueOnFail,
		Gate:           gate.NewRecordedController(toGateDecisions(decisions)),
		Verifier:       verify.NewShellVerifier(),
		ToolPolicies:   toRunnerToolPolicies(snap.ToolPolicies),
		Recorder:       recorder,
		EventSink:      feed,
		// CompletedNodes seeds the resumed leg's ready set from
		// graph.ReadyGiven(completed) instead of graph.Roots(), so a node the
		// first leg already finished is never re-run (and re-paid for).
		CompletedNodes: snap.CompletedNodes(),
		// SettledNodes additionally covers a node that FAILED on an earlier
		// leg (absent from CompletedNodes by design, so it does not wrongly
		// unblock its dependents), so THAT node itself still never re-runs
		// either — otherwise --continue-on-fail's "this failure is final"
		// would silently be undone by a later resume.
		SettledNodes: snap.SettledNodes(),
	})

	fmt.Fprintf(os.Stdout, "Resuming run %q (gate %q %s)\n\n", runID, gateID, decisionVerb(decision))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runErr := scheduler.Run(ctx, g, h, led)

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
