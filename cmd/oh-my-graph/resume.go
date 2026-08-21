package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/gate"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/handoff"
	"github.com/jitokim/oh-my-graph/internal/ledger"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/runstatus"
	"github.com/jitokim/oh-my-graph/internal/schedule"
	"github.com/jitokim/oh-my-graph/internal/verify"
)

// stateFileName and lockFileName are the two files a run directory holds
// beyond node artifacts: the resumable snapshot and the concurrent-resume
// guard.
const (
	// Both names are runstate's, for the same reason: the package that writes
	// the snapshot and takes the lock owns what they are called, and a
	// hand-spelled copy here is one more place the contract can drift.
	stateFileName = runstate.SnapshotFileName
	// lockFileName is runstate's, not this package's: since ADR 0015 §3 the
	// lock file is contract surface, and the package that takes and probes it
	// owns its name.
	lockFileName = runstate.LockFileName
)

// hasSnapshot reports whether a run directory has a state.json at all. It is
// the one fact the recovery hint splits on (runstatus.Recovery): a run killed
// before its first node settled has no snapshot, so there is nothing to resume
// FROM and the honest advice is to run the graph again rather than to resume
// (ADR 0015 §5). Anything other than a clean stat reads as "no snapshot", which
// is the conservative half — it advises re-running rather than promising a
// resume that would then fail.
func hasSnapshot(runDir string) bool {
	info, err := os.Stat(filepath.Join(runDir, stateFileName))
	return err == nil && info.Mode().IsRegular()
}

// runResume is the `resume` subcommand: parse argv and wire the production
// CLIRunner. Split from executeResume the same way runGraph/runAuto are
// split from executeGraph/executePlan, so a test can inject a FakeRunner
// instead of spawning a real claude subprocess.
func runResume(args []string) error {
	return runResumeRuntime(runner.RuntimeClaude, false, args)
}

func runResumeRuntime(requested runner.Runtime, requestedSet bool, args []string) error {
	flags := newResumeFlags()
	if err := flags.parse(args); err != nil {
		return err
	}
	// A resumed leg gets the live view on exactly the terms a first leg does:
	// the same webOpener gate (an interactive stdout, no --no-web) over the
	// same real launcher behind the fourth exec seam (ADR 0006). Watching the
	// rest of a run is worth as much as watching its beginning, and the leg a
	// human just decided a gate on is precisely one they are sitting at.
	nodeRunner, err := persistedNodeRunner(flags.runID, requested, requestedSet)
	if err != nil {
		return err
	}
	return executeResume(flags, nodeRunner, webOpener(flags.noWeb, os.Stdout, browser.NewExecOpener()))
}

// persistedNodeRunner reconstructs the run-wide CLI from state.json. The
// runtime never comes from a fresh default on resume; an explicit global flag
// may only confirm, not change, the persisted choice.
func persistedNodeRunner(runID string, requested runner.Runtime, requestedSet bool) (runner.NodeRunner, error) {
	snap, err := runstate.Load(filepath.Join(runDirFor(runID), stateFileName))
	if err != nil {
		return nil, fmt.Errorf("load run %q runtime: %w", runID, err)
	}
	runtime, err := runner.ParseRuntime(snap.Runtime)
	if err != nil {
		return nil, fmt.Errorf("load run %q runtime: %w", runID, err)
	}
	if requestedSet && requested != runtime {
		return nil, fmt.Errorf("run %q uses runtime %q; cannot resume it with --runtime %s", runID, runtime, requested)
	}
	return runner.NewCLIRunner(runtime), nil
}

// executeResume loads a run's snapshot and continues it in one of two modes:
// the default gate mode applies exactly one new gate decision to a paused run,
// while --retry-failed clears a halted run's FAILED records and re-executes
// only the non-passed nodes (see resumeRetryLeg). web is the leg's live-view
// Opener or nil for none, following executeGraph's convention exactly: the
// gate is the caller's decision, and nil means no server, no browser and
// byte-identical output.
func executeResume(flags *resumeFlags, nodeRunner runner.NodeRunner, web browser.Opener) error {
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

	// Derived BEFORE the lock is taken, because taking it is what makes the
	// answer "held": this leg would otherwise read its own lock and never see
	// the corpse it is about to resume. The warning is the whole mitigation ADR
	// 0015 accepts for its largest cost — the engine's children are in their own
	// process groups, so a death that took the engine alone (SIGHUP, kill -9, a
	// panic, an OOM kill) leaves a `claude` still spending, and this leg is
	// about to run that node again beside it.
	if status, statusErr := runstatus.Of(runDir); statusErr == nil && status == runstatus.Abandoned {
		fmt.Fprintf(os.Stderr, "WARNING: run %q reads as abandoned — a leg started and never reported an end; %s.\n", runID, runstatus.OrphanWarning)
	}

	// The lock guards the whole resume, not just the scheduler run: two
	// concurrent legs racing to load and rewrite the same snapshot would
	// double-run nodes even before either scheduler starts (DESIGN.md,
	// "resume.lock ... guards against two concurrent legs of the same run
	// id double-running nodes"). The first `run`/`auto` leg holds the same
	// lock (executeGraph), so a resume against a still-in-flight run fails
	// here too.
	release, err := acquireRunLock(lockPath)
	if err != nil {
		return err
	}
	defer release()

	snap, err := runstate.Load(statePath)
	if err != nil {
		// A run killed before its first node settled has no snapshot at all,
		// and `resume` loads it before it branches — --retry-failed included.
		// That is not a regression this ADR opens, but it IS the death shape
		// most likely to need recovery, so it says why rather than surfacing a
		// bare "no such file" (ADR 0015 §5).
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("cannot resume run %q: %s (%s)", runID, runstatus.Recovery(runID, false), statePath)
		}
		return fmt.Errorf("load run %q: %w", runID, err)
	}
	warnIfGraphSourceChanged(snap)

	// Before either mode, because both have exits that never reach continueRun:
	// a --retry-failed with nothing to retry returns early and would otherwise
	// accept a flag that is an error in every other state, answering as if it had
	// applied. A flag whose whole subject is which graph this run holds can be
	// answered the moment that graph is known.
	if err := checkVerifyCommandApplies(runID, snap, flags.verifyCommand()); err != nil {
		return err
	}

	if flags.retryFailed {
		return resumeRetryLeg(flags, snap, nodeRunner, web)
	}
	return resumeGateLeg(flags, snap, nodeRunner, web)
}

// resumeGateLeg is the gate mode: apply exactly one new gate decision to a
// paused run and continue the graph from where the earlier leg left off. It
// carries every snapshot record forward unchanged — passed AND failed — so a
// settled node never re-runs and the resumed ledger stays honest about the
// whole run.
func resumeGateLeg(flags *resumeFlags, snap runstate.Snapshot, nodeRunner runner.NodeRunner, web browser.Opener) error {
	if snap.Gate.PausedAt == "" {
		return fmt.Errorf("run %q is not paused (nothing to resume; a failed run is retried with --retry-failed)", flags.runID)
	}
	gateID, decision, err := resumeDecision(flags, snap.Gate.PausedAt)
	if err != nil {
		return err
	}
	decisions := mergedGateDecisions(snap.Gate.Decisions, gateID, runstate.GateDecision(decision))
	banner := fmt.Sprintf("Resuming run %q (gate %q %s)", flags.runID, gateID, decisionVerb(decision))
	// nil: a gate resume clears no failed node, so there is no attempt for
	// anything in this leg to be repeating.
	return continueRun(flags, snap, snap.Nodes, decisions, nil, banner, nodeRunner, web)
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
func resumeRetryLeg(flags *resumeFlags, snap runstate.Snapshot, nodeRunner runner.NodeRunner, web browser.Opener) error {
	g, err := graph.Parse(snap.Graph)
	if err != nil {
		return fmt.Errorf("reconstruct graph for run %q: %w", flags.runID, err)
	}
	retained, cleared := partitionForRetry(g, snap)
	banner := fmt.Sprintf("Resuming run %q (retrying failed nodes: %s)", flags.runID, strings.Join(cleared, ", "))
	if len(cleared) == 0 {
		if snap.Gate.PausedAt != "" {
			fmt.Fprintf(os.Stdout, "run %q has no failed nodes to retry.\n", flags.runID)
			fmt.Fprintf(os.Stdout, "It is paused at gate %q — decide it with --approve %s or --reject %s instead.\n",
				snap.Gate.PausedAt, snap.Gate.PausedAt, snap.Gate.PausedAt)
			return nil
		}
		if !hasUnfinishedWork(g, retained) {
			fmt.Fprintf(os.Stdout, "run %q has no failed nodes to retry.\n", flags.runID)
			return nil
		}
		banner = fmt.Sprintf("Resuming run %q (running unfinished nodes)", flags.runID)
	}
	return continueRun(flags, snap, retained, snap.Gate.Decisions, cleared, banner, nodeRunner, web)
}

// hasUnfinishedWork reports whether a retry leg carrying exactly the retained
// records would launch anything at all: some node with no record whose
// dependencies are all retained PASSes. This is the scheduler's own initial
// launch condition (ReadyGiven(completed) minus settled), applied ahead of
// time — a node buried behind a retained FAIL (a rejected gate's pruned
// subtree) is unreachable and does not count, so a retry of such a run stays
// a clean no-op instead of opening an empty leg. A feedback declarer's
// retained mid-loop marker (no verdict — see partitionForRetry) counts as
// unfinished through the same computation: the marker is neither completed
// nor settled, so the declarer itself reads as launchable.
func hasUnfinishedWork(g *graph.Graph, retained map[string]runstate.NodeRecord) bool {
	carried := runstate.Snapshot{Nodes: retained}
	settled := carried.SettledNodes()
	for _, id := range g.ReadyGiven(carried.CompletedNodes()) {
		if !settled[id] {
			return true
		}
	}
	return false
}

// partitionForRetry splits a snapshot's node records for --retry-failed:
// retained records carry forward as-is — every PASS (dependents interpolate
// their artifacts exactly as after a gate resume), plus a rejected gate's
// FAIL, because a rejection is a standing human decision and not a failure to
// salvage (retrying it would only replay the recorded reject), plus a
// feedback declarer's non-terminal mid-loop MARKER (no verdict — ADR 0010),
// because a marker is not a failure either: retaining it is what lets
// continueRun resume INTO the loop at the recorded round — and cleared
// lists, sorted for a stable banner, the node ids whose records are dropped
// so the retry leg re-executes them. Cancelled/never-settled nodes have no
// record and need no clearing: they become runnable again once their parents
// settle.
//
// A cleared FAILED feedback declarer whose arc fired at least once (its
// record carries Round > 0) takes its whole loop body with it (ADR 0010,
// "salvage means re-arming the loop, not re-running the declarer alone"):
// retaining the body's PASSes would relaunch the declarer against artifacts
// its rounds already judged insufficient — exactly the target-only shape the
// ADR rejects — so the body's records are cleared too, and with no record
// left to seed a round from, the re-run starts at round 0 with a fresh
// rounds budget: explicit human intervention buys a fresh set of rounds. A
// declarer that FAILED at round 0 never fired — only a non-judgment fault (a
// verify infrastructure fault, a blown budget) fails a declarer before its
// first fire — so its body's PASSes were never judged and are retained, and
// the retry leg re-runs the declarer alone, like any other failed node.
func partitionForRetry(g *graph.Graph, snap runstate.Snapshot) (retained map[string]runstate.NodeRecord, cleared []string) {
	clearedSet := make(map[string]bool)
	retained = make(map[string]runstate.NodeRecord, len(snap.Nodes))
	for id, rec := range snap.Nodes {
		if rec.Verdict == runstate.VerdictFail && snap.Gate.Decisions[id] != runstate.GateReject {
			clearedSet[id] = true
			continue
		}
		retained[id] = rec
	}
	for _, n := range g.Nodes {
		if n.Feedback == nil || !clearedSet[n.ID] || snap.Nodes[n.ID].Round == 0 {
			continue
		}
		for _, member := range g.FeedbackBody(n.ID) {
			if _, carried := retained[member]; carried {
				clearedSet[member] = true
				delete(retained, member)
			}
		}
	}
	for id := range clearedSet {
		cleared = append(cleared, id)
	}
	sort.Strings(cleared)
	return retained, cleared
}

// checkVerifyCommandApplies refuses a --verify-cmd this run's graph has no use
// for, and this is the check that keeps the fix from being a hole: `run` has no
// --verify-cmd, so attaching one to a hand-written graph here would be a
// capability only `resume` has. A hand-written graph says `verify:` on whichever
// node it means, and that field round-trips untouched — there is nothing for a
// flag to re-supply.
//
// The discriminator is the snapshot's ToolPolicies, non-empty exactly for a
// planned graph, the same one continueRun uses to decide what to reattach.
func checkVerifyCommandApplies(runID string, snap runstate.Snapshot, v coordinator.VerifyCommand) error {
	if !v.Supplied() || len(snap.ToolPolicies) > 0 {
		return nil
	}
	return fmt.Errorf("resume run %q: --verify-cmd applies to an auto run's planned graph, and this run's graph is hand-written; "+
		"its own success_check.verify is your reviewed artifact and resumes exactly as written. "+
		"(`run` has no --verify-cmd either: a resumed leg must not be able to attach a check a fresh run could not.)", runID)
}

// continueRun is the shared back half of both resume modes: rebuild the run's
// collaborators from the snapshot (graph, handoff, ledger, recorder, event
// stream, worktrees), seed the scheduler so exactly the carried records never
// re-run, and execute the leg. records is the set of node records this leg
// carries forward — all of snap.Nodes for a gate resume, the retained subset
// for a retry — and decisions is the gate-decision map the leg replays.
// cleared is the node ids whose records this leg dropped so they re-execute
// (nil for a gate resume): those are the nodes whose previous attempt this leg
// is repeating, and the only ones whose failed reply is re-read from disk. web,
// when non-nil, is the Opener this leg's embedded live view hands its URL to;
// nil is no live view at all (see executeResume).
func continueRun(flags *resumeFlags, snap runstate.Snapshot, records map[string]runstate.NodeRecord, decisions map[string]runstate.GateDecision, cleared []string, banner string, nodeRunner runner.NodeRunner, web browser.Opener) error {
	runID := flags.runID
	runDir := runDirFor(runID)

	g, err := graph.Parse(snap.Graph)
	if err != nil {
		return fmt.Errorf("reconstruct graph for run %q: %w", runID, err)
	}
	// A resumed leg does not take an auto graph's success_check.verify from
	// disk (ADR 0016 §4). graph.Parse re-parses whatever the run directory
	// holds, and a verification is engine-run shell outside every ceiling
	// layer — precisely what validatePlannedNodeVerify refuses at plan time —
	// so a snapshot-borne one is dropped here rather than replayed. The
	// discriminator is the snapshot's ToolPolicies, non-empty exactly for a
	// planned graph: a hand-written graph's `verify:` is the user's own
	// reviewed artifact and must keep round-tripping untouched.
	//
	// What replaces it is the command on THIS command line (#198). The refusal
	// was terminal until `resume` learned --verify-cmd, which meant a
	// session-limit pause — ADR 0009's whole claim that a limit is a PAUSE, with
	// the work banked for a later leg — was not kept for any auto run carrying
	// build evidence. The flag closes that without widening anything: the
	// command still comes from the human and never from disk, it is validated by
	// the same value object under the same ceiling, it attaches through the same
	// trusted-code path at the same sinks, and the engine still judges the exit
	// code itself.
	//
	// A planned graph's `agent:` is dropped here for a neighbouring reason
	// (ADR 0022 §5): a mapped node's ceiling now depends on a definition this
	// run staged, and a second process has no in-memory manifest to re-stage
	// from — only a record in the run directory the previous leg's nodes could
	// write. So a resumed leg maps nothing and runs those nodes as ordinary
	// planned nodes: isolated, under their own unchanged tool ceiling, without
	// the user's agent persona. The discriminator is the same as above, and it
	// matters as much: a HAND-WRITTEN graph's `agent:` is the user's own
	// reviewed artifact, its node loads the user's settings by design, and it
	// must keep round-tripping untouched.
	verifyCmd := flags.verifyCommand()
	// Re-asserted here rather than assumed from executeResume's earlier call:
	// this is the function that would otherwise ATTACH the command, so the
	// refusal belongs where the attachment is, and one implementation serving
	// both call sites is what stops the two from drifting (#198's own shape).
	if err := checkVerifyCommandApplies(runID, snap, verifyCmd); err != nil {
		return err
	}
	var unmapped []string
	// serializedVerify is what makes this leg's injected checks run one at a
	// time, exactly as a fresh leg's do (executeGraph). It is derived from the
	// REATTACHED graph — a resumed leg's checks are this invocation's, so the
	// set is empty unless the flag supplied one.
	var serializedVerify map[string]bool
	if len(snap.ToolPolicies) > 0 {
		reattached, attachments, err := coordinator.ReattachVerifyCommand(g, verifyCmd)
		if err != nil {
			return fmt.Errorf("resume run %q: %w", runID, err)
		}
		g = reattached
		// Disclosed for the same reason a plan's attachments are: trusted code
		// quietly adding an engine-run shell command to a graph is what the
		// disclosure exists against, and a resumed leg is the case where the
		// user is furthest from the screen that first showed it.
		noteVerifyAttachments(os.Stdout, attachments)
		serializedVerify = coordinator.InjectedVerifyNodes(g)

		dropped, droppedIDs, err := coordinator.DropAgentMapping(g)
		if err != nil {
			return fmt.Errorf("resume run %q: %w", runID, err)
		}
		g, unmapped = dropped, droppedIDs
	}
	runtime, err := runner.ParseRuntime(snap.Runtime)
	if err != nil {
		return fmt.Errorf("resume run %q: %w", runID, err)
	}
	runtimeWarnings, err := runner.ValidateGraphForRuntime(runtime, g)
	// A resumed leg re-surfaces these for the same reason it re-warns about
	// bypassPermissions below: the terminal that saw the first leg's copy may
	// be long gone, and a cap that cannot apply is a fact about the nodes this
	// leg is about to spend on (ADR 0026).
	warnRuntimePreflight(os.Stderr, snap.GraphSourcePath, runtimeWarnings)
	if err != nil {
		return fmt.Errorf("resume run %q: %w", runID, err)
	}
	// A resumed leg re-warns exactly as `run` did at load: the warning is
	// promised to be loud and never silent (DESIGN.md), and a resume may be
	// far from the terminal session that saw the first one.
	warnBypassPermissions(g)

	h := handoff.New(runDir, snap.Inputs)
	for nodeID, rec := range records {
		h.Seed(nodeID, rec.ArtifactPath, rec.SessionID)
	}

	// A retry leg re-reads a cleared node's failed reply from
	// failed/<node-id>.out, so the re-execution is handed the attempt it is
	// repeating (ADR 0020). This is the whole reason that file is written by a
	// process other than the one that reads it: --retry-failed drops the FAIL
	// record — with it the ledger row and the 240-rune detail — and the reply on
	// disk is the only account of the attempt that survives into this leg.
	//
	// Only nodes whose recorded failure was JUDGED are seeded, which is exactly
	// the gate the in-leg retry applies (isJudgmentFailure). It is read from the
	// UNPARTITIONED snapshot, because the record carrying it is one of the ones
	// this leg dropped. Without it the two halves of one feature would disagree
	// about their own trigger — a budget-killed node, whose reply no check ever
	// faulted, would be told across a process boundary that a check rejected it.
	//
	// A failure to re-read is a WARNING, never fatal, matching the write side's
	// policy exactly (Scheduler.keepFailedReply): the leg's job is to re-run the
	// node, and losing the quote costs it context, not correctness. Contrast
	// SeedFeedback above, whose failure IS fatal — a feedback re-run without its
	// payload is a paid lie about what the body was told.
	//
	// An ABSENT file is warned about too, and only here. It is a clean no-op to
	// handoff, which cannot know whether a reply was owed; this loop can, because
	// it has already gated on Judged — a judged failure is one the previous leg
	// rendered a verdict on, so its reply was written, and a missing one means
	// something removed it or the write did not survive. Both retries are legal
	// and both re-run the node; the difference is that one carries the attempt it
	// repeats and the other quietly does not, and a degraded retry that looks
	// exactly like a whole one is the one nobody ever notices paying for.
	for _, nodeID := range cleared {
		if !snap.Nodes[nodeID].Judged {
			continue
		}
		switch seeded, err := h.SeedPriorReply(nodeID); {
		case err != nil:
			fmt.Fprintf(os.Stderr, "warning: %v; %s will be retried without it\n", err, nodeID)
		case !seeded:
			fmt.Fprintf(os.Stderr, "warning: no persisted reply for %s, whose failure was judged; "+
				"it will be retried without the attempt it is repeating\n", nodeID)
		}
	}

	// A feedback declarer carrying a non-terminal MARKER record (round k, no
	// verdict — ADR 0010) means the earlier leg stopped mid-loop, and this leg
	// must resume INTO the loop, not out of it. The marker plus the graph is
	// the whole resume state: body records written before round k are
	// superseded — dropped below from the completed and settled seeds so the
	// remainder of round k re-executes — every body node resumes at round k
	// (so max − k rounds remain and events/snapshot records stamp the right
	// round), and the declarer's persisted feedback payload is re-read so the
	// re-run cannot silently run without it (a failure to re-read is fatal
	// for the same reason).
	nodeRounds := make(map[string]int)
	superseded := make(map[string]bool)
	for _, n := range g.Nodes {
		if n.Feedback == nil {
			continue
		}
		marker, recorded := records[n.ID]
		if !recorded || marker.Verdict != "" || marker.Round == 0 {
			continue
		}
		if err := h.SeedFeedback(n.ID); err != nil {
			return fmt.Errorf("resume run %q mid-loop: %w", runID, err)
		}
		for _, member := range g.FeedbackBody(n.ID) {
			nodeRounds[member] = marker.Round
			if rec, carried := records[member]; carried && rec.Round < marker.Round {
				superseded[member] = true
			}
		}
	}

	led := ledger.New(runID)
	led.RecordPlanning(snap.PlanningCostUSD, snap.PlanningCostUnknown, ledger.TokenUsage{
		InputTokens: snap.PlanningUsage.InputTokens, CachedInputTokens: snap.PlanningUsage.CachedInputTokens,
		OutputTokens: snap.PlanningUsage.OutputTokens, ReasoningOutputTokens: snap.PlanningUsage.ReasoningOutputTokens,
	})
	for nodeID, rec := range records {
		led.Record(ledger.Record{
			NodeID:      nodeID,
			SessionID:   rec.SessionID,
			CostUSD:     rec.CostUSD,
			CostUnknown: rec.CostUnknown,
			Usage: ledger.TokenUsage{
				InputTokens: rec.Usage.InputTokens, CachedInputTokens: rec.Usage.CachedInputTokens,
				OutputTokens: rec.Usage.OutputTokens, ReasoningOutputTokens: rec.Usage.ReasoningOutputTokens,
			},
			BudgetUSD:  rec.BudgetUSD,
			Verdict:    ledger.Verdict(rec.Verdict),
			Duration:   rec.Duration,
			Detail:     rec.Detail,
			Provenance: rec.Provenance,
		})
	}

	// ADR 0017 §6, amended 2026-08-07: a resumed leg does not activate skills.
	// toRunnerToolPolicies deliberately does NOT carry PluginDirs across, and
	// nothing here puts them back, so there is no staged directory to guard.
	// The resolved policies are what BOTH the scheduler and the recorder below
	// get, so the de-escalation persists into the snapshot instead of being
	// re-decided by every later leg.
	policies := toRunnerToolPolicies(snap.ToolPolicies)
	noteAgentDeescalation(os.Stdout, unmapped)
	dropSkillActivation(os.Stdout, snap.ToolPolicies, policies, flags.noSkillActivation, unmapped)
	// The one thing on this screen that is not a de-escalation, and the reason
	// it has to be here at all (ADR 0032 §2.7): a leg continuing an opted-in
	// run spawns nodes carrying the operator's own configuration, and the
	// terminal that disclosed it the first time is long gone. `resume`
	// registers no flag to make that choice — it reads the choice out of the
	// policies it just rehydrated, which is the same value the argv is built
	// from — so this is inheritance being SAID, not a second process widening
	// anything. Before the banner, with the leg's other differences.
	//
	// On Codex the sandbox line comes first, because the sentence below POINTS
	// at it ("the filesystem sandbox above") and this leg prints none of the
	// rest of noteCodexRuntimePolicy. A disclosure whose load-bearing clause
	// refers to text the screen never emitted reads as a missing paragraph, and
	// the reader is left unable to check the one thing the sentence claims still
	// binds. Nothing else on a resumed screen is deictic.
	if plannedNodesLoadUserConfig(policies) {
		if runtime == runner.RuntimeCodex {
			fmt.Fprint(os.Stdout, codexSandboxLine)
		}
		noteLoadedUserConfig(os.Stdout, runtime)
	}

	recorder := runstate.NewSnapshotRecorder(filepath.Join(runDir, stateFileName), runstate.Snapshot{
		RunID:               runID,
		Runtime:             snap.Runtime,
		PlanningCostUSD:     snap.PlanningCostUSD,
		PlanningCostUnknown: snap.PlanningCostUnknown,
		PlanningUsage:       snap.PlanningUsage,
		GraphSourcePath:     snap.GraphSourcePath,
		GraphSHA256:         snap.GraphSHA256,
		Graph:               snap.Graph,
		Inputs:              snap.Inputs,
		ContinueOnFail:      snap.ContinueOnFail,
		ToolPolicies:        toNodeToolPolicies(policies),
		// Goal lineage carries across legs: a resumed cycle of a goal loop
		// (a session-limit pause mid-loop, ADR 0011 §2) must not lose its
		// group membership just because a second process finished it.
		Goal: snap.Goal,
		// So does the build-evidence answer, for a sharper reason: this
		// recorder writes the WHOLE snapshot on every RecordNode, so a field
		// omitted here is a field the first settling node ERASES. ADR 0030 §2.6
		// declines to re-gate a resume precisely because "the snapshot the
		// resume loads records it" — that sentence is only true while this line
		// exists, and without it a gate-paused or limit-paused `auto`, which is
		// exactly the interactive class a human declared over, finishes looking
		// like an accident and drops out of all four strata of §8(a).
		// Carrying it forward adds no trust surface: the block is inert by
		// construction (marker filenames, nothing executable, nothing reads it
		// to decide behaviour — ADR 0030 §2.5a), so this is transcription, not
		// a run directory deciding anything.
		BuildEvidence: snap.BuildEvidence,
		Nodes:         records,
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
	// is precisely what makes its node run again. A body record a mid-loop
	// marker superseded is then removed from BOTH sets: dropping it from
	// completed is what re-runs it, and dropping it from settled is what lets
	// the scheduler relaunch a node an earlier leg's PASS would otherwise
	// have pinned (the record itself stays in `records`, so its spend still
	// seeds the ledger and accumulates into the round that replaces it).
	carried := runstate.Snapshot{Nodes: records}
	completed := carried.CompletedNodes()
	settled := carried.SettledNodes()
	for id := range superseded {
		delete(completed, id)
		delete(settled, id)
	}
	scheduler := schedule.NewScheduler(nodeRunner, schedule.Options{
		Concurrency:    flags.concurrency,
		ContinueOnFail: snap.ContinueOnFail,
		Gate:           gate.NewRecordedController(toGateDecisions(decisions)),
		Verifier:       verify.NewShellVerifier(),
		Worktrees:      worktrees,
		ToolPolicies:   policies,
		// An injected evidence command runs one at a time on a resumed leg for
		// the same load-bearing reason it does on a fresh one (ADR 0016 §2): two
		// concurrent builds of one directory can each fail on the other's
		// half-written output, and neither result then describes the tree the
		// user has. Nil unless this invocation supplied a command.
		SerializedVerifyNodes: serializedVerify,
		Recorder:              recorder,
		EventSink:             feed,
		// CompletedNodes seeds the resumed leg's ready set from
		// graph.ReadyGiven(completed) instead of graph.Roots(), so a node the
		// first leg already finished is never re-run (and re-paid for).
		CompletedNodes: completed,
		// SettledNodes additionally covers a node that FAILED on an earlier
		// leg (absent from CompletedNodes by design, so it does not wrongly
		// unblock its dependents), so THAT node itself still never re-runs
		// either — otherwise --continue-on-fail's "this failure is final"
		// would silently be undone by a later resume.
		SettledNodes: settled,
		// NodeRounds re-enters a mid-loop feedback leg at the marker's round:
		// re-executed body nodes stamp round k on their events and records,
		// and the declarer's remaining budget is max − k (ADR 0010).
		NodeRounds: nodeRounds,
	})

	fmt.Fprintf(os.Stdout, "%s\n\n", banner)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The embedded live view lives exactly as long as this leg, on the same
	// terms as a first leg's (executeGraph): serve's own listener, handler and
	// lifecycle on an ephemeral port, one browser open, and a deferred stop
	// that waits for the server to exit after the ledger print below. It reads
	// the same run directory the first leg's view did, so the URL shows the
	// whole run's history, not just this leg's.
	if web != nil {
		defer startLiveView(ctx, web, runID)()
	}

	runErr := scheduler.Run(ctx, g, h, led)
	reportWorktreeCleanup(os.Stderr, worktrees.Cleanup(context.Background()))

	fmt.Fprintln(os.Stdout)
	led.Print(os.Stdout)
	// The next leg needs the pair re-supplied exactly as this one did: the
	// snapshot this leg rewrites carries snap.Graph forward verbatim — the
	// verification stays on disk and stays untrusted — so the hint repeats the
	// flags rather than promising a bare resume that would be refused.
	printPauseHint(os.Stdout, runID, runErr, verifyCmd)

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
//
// It carries the five CEILING fields and deliberately NOT PluginDirs, which is
// the one field where "rehydrate what the first leg recorded" is the wrong
// behaviour (ADR 0017 §6). The five bound capability, so re-imposing them
// verbatim is exactly right and a dropped one would silently WIDEN a resumed
// leg. PluginDirs names a directory a node of the previous leg could have
// written, and a --plugin-dir pointing at nothing is accepted by the CLI with
// exit 0 and no warning — so a leg that trusted the path would either read a
// corpus that leg's nodes chose, or run with no skills and be
// indistinguishable from one whose model chose none. Since 2026-08-07 no path
// puts the field back: dropSkillActivation completes the de-escalation by
// taking `Skill` out of Tools too, and says so.
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

// noteAgentDeescalation says which planned nodes lost their `agent:` to the
// resume, and why. coordinator.DropAgentMapping has already done it to the
// graph; this is the half that must not be silent.
//
// It is a cost and not a reassurance, so it names both directions: the node
// keeps its whole ceiling — it never had less of one, since ADR 0022 — and
// loses the system prompt the first leg's node ran under. A leg that behaves
// differently from the one it continues and does not say so is the same
// unexplained-absence shape ADR 0017 §6's own de-escalation prints against.
func noteAgentDeescalation(w io.Writer, unmapped []string) {
	if len(unmapped) == 0 {
		return
	}
	sorted := append([]string(nil), unmapped...)
	sort.Strings(sorted)
	fmt.Fprintf(w, "agent mapping is off for this leg: re-staging an agent definition could only read a record inside the run directory, which this run's own nodes could have rewritten, and there is no integrity anchor outside it yet — %s run(s) as an ordinary planned node, isolated and under the same tool ceiling, without your agent's system prompt (ADR 0022 §5).\n", strings.Join(sorted, ", "))
}

// dropSkillActivation takes skill activation OFF a resumed leg, in place on
// policies, and says so. It is unconditional (ADR 0017 §6, amended
// 2026-08-07), and the reason is a hazard the first leg does not have:
//
// Within a leg the manifest is trusted Go code's and lives in memory.
// Materialize runs from it before EVERY spawn and deletes every path it does
// not name, so a node that plants a skill has it wiped before the next node
// reads — and no file on disk is ever consulted to decide what the corpus is.
// A resumed leg has none of that. It is a second process with no in-memory
// manifest, so the only thing it could re-stage from is the sidecar in the run
// directory — and that directory is writable by the very nodes the previous
// leg ran, which hold unscoped `Write` and run as the same uid. Its per-file
// SHA-256 is no defence: one actor authoring both `source` and `sha256`
// satisfies its own check, and the load-time `rel` guard only bounds a forged
// row to inside the staged directory, which is exactly where the nodes read.
// So a resumed leg that re-staged would hand its nodes a corpus the previous
// leg's nodes could have chosen — including by REPLACING the user's skills,
// since re-materialization prunes whatever the manifest does not name.
//
// Closing that needs an integrity anchor outside the run directory: somewhere
// to record what this run staged that a node cannot reach, plus a settled
// answer for what `resume` does when the anchor and the directory disagree.
// That is a design, and ADR 0017 §6 defers it rather than guessing at it. Until
// it exists, a resumed leg runs with no `Skill` and no `--plugin-dir`. What
// that costs is no longer "approximately nothing": ADR 0017's 44-spawn
// measurement (docs/measurements/0017-skill-activation-yield.md) moved the
// yield from 0 of 9 unaided to 8 of 9 once an activated prompt carries the
// notice, so a resumed leg gives up a capability its first leg measurably had.
// It is still the right trade, because what the alternative buys is a corpus
// re-staged from a record the previous leg's own nodes could have rewritten —
// and re-staging PRUNES what the record does not name, so a forged one
// replaces the user's skills rather than merely adding to them. The value of
// activation is unmeasured (arms A and B were indistinguishable on the one
// mechanically checkable task); the cost of trusting that record is not
// bounded at all.
//
// The de-escalation is printed rather than silent, because a resumed leg that
// behaves differently from its first leg and does not say why is the same
// unexplained-absence shape this whole mechanism is most exposed to.
//
// snapPolicies is read rather than policies because PluginDirs is the only
// durable record that a run was activation-enabled at all — the grant is
// invisible in graph.json by design (ADR 0017 §2) — and toRunnerToolPolicies
// deliberately does not rehydrate it.
func dropSkillActivation(w io.Writer, snapPolicies map[string]runstate.NodeToolPolicy, policies map[string]runner.ToolPolicy, off bool, unmapped []string) {
	// A mapped node's snapshot carries a PluginDirs too — the staged AGENT
	// directory, not the corpus (ADR 0022) — and noteAgentDeescalation has
	// already said what happens to it. Counting those nodes here would report
	// them as losing a Skill tool they never had.
	wasMapped := make(map[string]bool, len(unmapped))
	for _, id := range unmapped {
		wasMapped[id] = true
	}
	activated := make([]string, 0, len(snapPolicies))
	for id, p := range snapPolicies {
		if len(p.PluginDirs) == 0 || wasMapped[id] {
			continue
		}
		activated = append(activated, id)
	}
	if len(activated) == 0 {
		return
	}
	sort.Strings(activated)

	for _, id := range activated {
		p := policies[id]
		p.Tools = withoutSkillTool(p.Tools)
		p.PluginDirs = nil
		policies[id] = p
	}
	if off {
		fmt.Fprintf(w, "skill activation is off for this leg (--no-skill-activation; a resumed leg does not activate skills in any case): %d node(s) run with no Skill tool and no --plugin-dir.\n", len(activated))
		return
	}
	fmt.Fprintf(w, "skill activation is off for this leg: re-staging could only read the manifest inside the run directory, which this run's own nodes could have rewritten, and there is no integrity anchor outside it yet — %d node(s) run with no Skill tool and no --plugin-dir (ADR 0017 §6, 2026-08-07).\n", len(activated))
}

// withoutSkillTool is de-escalation's whole edit: layer 3 minus the one name
// ADR 0017 added to it. It copies rather than filtering in place, because the
// slice it is handed is shared with the snapshot map it was rehydrated from.
func withoutSkillTool(tools []string) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		if t == coordinator.SkillToolName {
			continue
		}
		out = append(out, t)
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
