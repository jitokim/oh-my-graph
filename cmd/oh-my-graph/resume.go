package main

import (
	"context"
	"errors"
	"fmt"
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
	stateFileName = "state.json"
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
// ClaudeCLIRunner. Split from executeResume the same way runGraph/runAuto are
// split from executeGraph/executePlan, so a test can inject a FakeRunner
// instead of spawning a real claude subprocess.
func runResume(args []string) error {
	flags := newResumeFlags()
	if err := flags.parse(args); err != nil {
		return err
	}
	// A resumed leg gets the live view on exactly the terms a first leg does:
	// the same webOpener gate (an interactive stdout, no --no-web) over the
	// same real launcher behind the fourth exec seam (ADR 0006). Watching the
	// rest of a run is worth as much as watching its beginning, and the leg a
	// human just decided a gate on is precisely one they are sitting at.
	return executeResume(flags, runner.NewClaudeCLIRunner(), webOpener(flags.noWeb, os.Stdout, browser.NewExecOpener()))
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
	return continueRun(flags, snap, snap.Nodes, decisions, banner, nodeRunner, web)
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
	return continueRun(flags, snap, retained, snap.Gate.Decisions, banner, nodeRunner, web)
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

// continueRun is the shared back half of both resume modes: rebuild the run's
// collaborators from the snapshot (graph, handoff, ledger, recorder, event
// stream, worktrees), seed the scheduler so exactly the carried records never
// re-run, and execute the leg. records is the set of node records this leg
// carries forward — all of snap.Nodes for a gate resume, the retained subset
// for a retry — and decisions is the gate-decision map the leg replays. web,
// when non-nil, is the Opener this leg's embedded live view hands its URL to;
// nil is no live view at all (see executeResume).
func continueRun(flags *resumeFlags, snap runstate.Snapshot, records map[string]runstate.NodeRecord, decisions map[string]runstate.GateDecision, banner string, nodeRunner runner.NodeRunner, web browser.Opener) error {
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
	// so a snapshot-borne one is refused here rather than replayed. The
	// discriminator is the snapshot's ToolPolicies, non-empty exactly for a
	// planned graph: a hand-written graph's `verify:` is the user's own
	// reviewed artifact and must keep round-tripping untouched.
	//
	// The zero VerifyCommand is not a placeholder for a flag: `resume` has no
	// --verify-cmd yet, so re-supplying is not possible and the refusal is
	// terminal. It also means no injected check can reach this leg at all,
	// which is why the scheduler below is handed no SerializedVerifyNodes —
	// the set would be empty by construction.
	if len(snap.ToolPolicies) > 0 {
		reattached, _, err := coordinator.ReattachVerifyCommand(g, coordinator.VerifyCommand{})
		if err != nil {
			return fmt.Errorf("resume run %q: %w", runID, err)
		}
		g = reattached
	}
	// A resumed leg re-warns exactly as `run` did at load: the warning is
	// promised to be loud and never silent (DESIGN.md), and a resume may be
	// far from the terminal session that saw the first one.
	warnBypassPermissions(g)

	h := handoff.New(runDir, snap.Inputs)
	for nodeID, rec := range records {
		h.Seed(nodeID, rec.ArtifactPath, rec.SessionID)
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
	for nodeID, rec := range records {
		led.Record(ledger.Record{
			NodeID:     nodeID,
			SessionID:  rec.SessionID,
			CostUSD:    rec.CostUSD,
			BudgetUSD:  rec.BudgetUSD,
			Verdict:    ledger.Verdict(rec.Verdict),
			Duration:   rec.Duration,
			Detail:     rec.Detail,
			Provenance: rec.Provenance,
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
		// Goal lineage carries across legs: a resumed cycle of a goal loop
		// (a session-limit pause mid-loop, ADR 0011 §2) must not lose its
		// group membership just because a second process finished it.
		Goal:  snap.Goal,
		Nodes: records,
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
		ToolPolicies:   toRunnerToolPolicies(snap.ToolPolicies),
		Recorder:       recorder,
		EventSink:      feed,
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
