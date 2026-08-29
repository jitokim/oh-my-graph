package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/gate"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/runstatus"
)

// newTestDashboard builds a Dashboard over root with the short test poll, so
// its runs-root sweep ticks in milliseconds rather than at the production
// cadence.
func newTestDashboard(root string) *Dashboard {
	d := NewDashboard(root)
	d.poll = testPoll
	return d
}

func TestRunCard_OmitsEmptyUsage(t *testing.T) {
	encoded, err := json.Marshal(runCard{RunID: "run-1", State: statePending})
	if err != nil {
		t.Fatalf("marshal run card: %v", err)
	}
	if strings.Contains(string(encoded), `"usage"`) {
		t.Fatalf("zero token usage must be absent, got %s", encoded)
	}
}

// runsRootWith creates a runs root holding one directory per named run and
// returns the root. The per-run fixtures are written by the callers through
// the real writers (writeSnapshot / writeEvents), as everywhere else here.
func runsRootWith(t *testing.T, runIDs ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, runID := range runIDs {
		if err := os.MkdirAll(filepath.Join(root, runID), 0o755); err != nil {
			t.Fatalf("create run dir %q: %v", runID, err)
		}
	}
	return root
}

// seedInFlightRun writes a run whose leg is still open: one node passed, one
// running, no run_finished. Its snapshot exists (the first node completed), so
// the card has a graph to draw.
func seedInFlightRun(t *testing.T, root, runID string) {
	t.Helper()
	dir := filepath.Join(root, runID)
	writeSnapshot(t, dir, runstate.Snapshot{
		RunID: runID,
		Graph: json.RawMessage(twoNodeGraph),
		Nodes: map[string]runstate.NodeRecord{
			"a": {Verdict: runstate.VerdictPass, CostUSD: 0.25},
		},
	})
	writeEvents(t, dir, runID,
		runfeed.Event{Type: runfeed.EventRunStarted},
		runfeed.Event{Type: runfeed.EventNodeStarted, NodeID: "a"},
		runfeed.Event{Type: runfeed.EventNodePassed, NodeID: "a", Verdict: runfeed.VerdictPass, CostUSD: 0.25},
		runfeed.Event{Type: runfeed.EventNodeStarted, NodeID: "b"},
	)
}

// seedSettledRun writes a run whose every node passed and whose leg is closed.
func seedSettledRun(t *testing.T, root, runID string) {
	t.Helper()
	dir := filepath.Join(root, runID)
	writeSnapshot(t, dir, runstate.Snapshot{
		RunID: runID,
		Graph: json.RawMessage(twoNodeGraph),
		Nodes: map[string]runstate.NodeRecord{
			"a": {Verdict: runstate.VerdictPass, CostUSD: 0.25},
			"b": {Verdict: runstate.VerdictPass, CostUSD: 0.75},
		},
	})
	writeEvents(t, dir, runID,
		runfeed.Event{Type: runfeed.EventRunStarted},
		runfeed.Event{Type: runfeed.EventNodePassed, NodeID: "a", Verdict: runfeed.VerdictPass, CostUSD: 0.25},
		runfeed.Event{Type: runfeed.EventNodePassed, NodeID: "b", Verdict: runfeed.VerdictPass, CostUSD: 0.75},
		runfeed.Event{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePassed},
	)
}

// getJSON performs one GET against the dashboard and decodes the body into v.
func getJSON(t *testing.T, d *Dashboard, path string, v any) {
	t.Helper()
	rec := get(t, d, path)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200 (%s)", path, rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("GET %s body is not the expected JSON: %v (%s)", path, err, rec.Body.String())
	}
}

// get performs one GET against the dashboard with a loopback Host, which every
// route requires.
func get(t *testing.T, d *Dashboard, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), "GET", "http://127.0.0.1:8642"+path, nil)
	rec := httptest.NewRecorder()
	d.Handler().ServeHTTP(rec, req)
	return rec
}

// cardByID finds one run's card in a decoded /api/cards response.
func cardByID(t *testing.T, cards []runCard, runID string) runCard {
	t.Helper()
	for _, card := range cards {
		if card.RunID == runID {
			return card
		}
	}
	t.Fatalf("no card for run %q (have %d cards)", runID, len(cards))
	return runCard{}
}

// --- the cards: one per run, in flight and settled ---------------------------

func TestDashboard_CardsForInFlightAndSettledRuns(t *testing.T) {
	// The whole premise: four concurrent runs are four cards on one page, and
	// a card says at a glance what state its run is in, how far along, and
	// what it has cost — without opening it.
	root := runsRootWith(t, "run-live", "run-done")
	seedInFlightRun(t, root, "run-live")
	seedSettledRun(t, root, "run-done")

	var cards []runCard
	getJSON(t, newTestDashboard(root), "/api/cards", &cards)
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want one per run directory", len(cards))
	}
	// Newest first, the same ordering `runs list` and ResolveRun use.
	if cards[0].RunID != "run-live" {
		t.Errorf("cards[0] = %q, want the newest run id first", cards[0].RunID)
	}

	live := cardByID(t, cards, "run-live")
	if live.State != stateRunning {
		t.Errorf("in-flight card state = %q, want %q", live.State, stateRunning)
	}
	if live.Name != "demo" || !live.Available {
		t.Errorf("in-flight card = (name %q, available %v), want the snapshot's graph name", live.Name, live.Available)
	}
	if live.CostUSD != 0.25 {
		t.Errorf("in-flight card cost = %v, want the snapshot's per-node total 0.25", live.CostUSD)
	}
	if live.StartedAt == "" || live.EndedAt != "" {
		t.Errorf("in-flight card boundaries = (%q, %q), want a start and no end", live.StartedAt, live.EndedAt)
	}
	wantCounts := cardCounts{Total: 2, Passed: 1, Running: 1}
	if live.Counts != wantCounts {
		t.Errorf("in-flight card counts = %+v, want %+v", live.Counts, wantCounts)
	}
	// The mini-DAG: the run's structure, with each node's live state on it.
	if len(live.Nodes) != 2 {
		t.Fatalf("in-flight card has %d nodes, want the graph's 2", len(live.Nodes))
	}
	if live.Nodes[0].State != statePassed || live.Nodes[1].State != stateRunning {
		t.Errorf("node states = (%q, %q), want (passed, running)", live.Nodes[0].State, live.Nodes[1].State)
	}
	if len(live.Nodes[1].DependsOn) != 1 || live.Nodes[1].DependsOn[0] != "a" {
		t.Errorf("node b depends_on = %v, want the graph's edge to a", live.Nodes[1].DependsOn)
	}

	done := cardByID(t, cards, "run-done")
	if done.State != statePassed {
		t.Errorf("settled card state = %q, want %q", done.State, statePassed)
	}
	if done.CostUSD != 1.0 {
		t.Errorf("settled card cost = %v, want 1.0", done.CostUSD)
	}
	if done.EndedAt == "" {
		t.Errorf("settled card has no end timestamp; its elapsed cannot be rendered")
	}
	if got := (cardCounts{Total: 2, Passed: 2}); done.Counts != got {
		t.Errorf("settled card counts = %+v, want %+v", done.Counts, got)
	}
}

func TestDashboard_CardCarriesUnknownCostAndTokens(t *testing.T) {
	root := runsRootWith(t, "codex-run")
	dir := filepath.Join(root, "codex-run")
	writeSnapshot(t, dir, runstate.Snapshot{
		RunID: "codex-run", Graph: json.RawMessage(twoNodeGraph),
		PlanningCostUSD: 0.4, PlanningCostUnknown: true,
		PlanningUsage: runstate.TokenUsage{InputTokens: 7, CachedInputTokens: 1, OutputTokens: 2, ReasoningOutputTokens: 1},
		Nodes: map[string]runstate.NodeRecord{
			"a": {Verdict: runstate.VerdictPass, CostUSD: 0.1,
				Usage: runstate.TokenUsage{InputTokens: 11, CachedInputTokens: 2, OutputTokens: 5, ReasoningOutputTokens: 3}},
		},
	})
	writeEvents(t, dir, "codex-run",
		runfeed.Event{Type: runfeed.EventRunStarted},
		runfeed.Event{Type: runfeed.EventNodePassed, NodeID: "a", Verdict: runfeed.VerdictPass, CostUnknown: true},
		runfeed.Event{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePassed},
	)

	card := buildCard(root, "codex-run")
	if !card.CostUnknown || card.CostUSD != 0.5 {
		t.Errorf("card cost = %v unknown %v, want unknown with $0.50 known subtotal", card.CostUSD, card.CostUnknown)
	}
	if card.Usage.InputTokens != 18 || card.Usage.CachedInputTokens != 3 || card.Usage.OutputTokens != 7 || card.Usage.ReasoningOutputTokens != 4 {
		t.Errorf("card usage = %+v", card.Usage)
	}
}

func TestDashboard_APausedRunIsItsOwnState(t *testing.T) {
	// A run paused at a gate is neither passed nor failed, and it is the one
	// state an operator most needs to spot on a wall of cards: it is waiting
	// for them.
	//
	// The RUN's token is `paused` since ADR 0023, while the gate NODE keeps
	// `gate-paused`. They are no longer the same word because they are no
	// longer the same claim: the node really is sitting at a gate, but the run
	// might instead have stopped on the subscription's session limit, which has
	// no gate at all — and the old shared token could only be reached through
	// the snapshot's gate block, which is why that pause painted red.
	root := runsRootWith(t, "run-gate")
	dir := filepath.Join(root, "run-gate")
	writeSnapshot(t, dir, runstate.Snapshot{
		RunID: "run-gate",
		Graph: json.RawMessage(gateGraph),
		Nodes: map[string]runstate.NodeRecord{"a": {Verdict: runstate.VerdictPass}},
		Gate:  runstate.GateState{PausedAt: "approve"},
	})
	writeEvents(t, dir, "run-gate",
		runfeed.Event{Type: runfeed.EventRunStarted},
		runfeed.Event{Type: runfeed.EventNodePassed, NodeID: "a", Verdict: runfeed.VerdictPass},
		runfeed.Event{Type: runfeed.EventGatePaused, NodeID: "approve"},
		runfeed.Event{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePaused},
	)

	var cards []runCard
	getJSON(t, newTestDashboard(root), "/api/cards", &cards)
	card := cardByID(t, cards, "run-gate")
	if card.State != statePaused {
		t.Errorf("card state = %q, want %q", card.State, statePaused)
	}
	for _, node := range card.Nodes {
		if node.ID == "approve" && node.State != stateGatePaused {
			t.Errorf("gate node state = %q, want %q", node.State, stateGatePaused)
		}
	}
}

func TestDashboard_ARunWithNoSnapshotYetStillGetsALiveCard(t *testing.T) {
	// state.json is written only after a node's terminal verdict, so a run
	// that just started has no structure to draw. It must still appear — that
	// window is exactly when someone opens the dashboard to watch.
	root := runsRootWith(t, "run-fresh")
	writeEvents(t, filepath.Join(root, "run-fresh"), "run-fresh",
		runfeed.Event{Type: runfeed.EventRunStarted},
		runfeed.Event{Type: runfeed.EventNodeStarted, NodeID: "a"},
	)

	var cards []runCard
	getJSON(t, newTestDashboard(root), "/api/cards", &cards)
	card := cardByID(t, cards, "run-fresh")
	if card.State != stateRunning || card.Available {
		t.Errorf("card = (state %q, available %v), want a running card with no snapshot", card.State, card.Available)
	}
	if len(card.Nodes) != 1 || card.Nodes[0].ID != "a" || card.Nodes[0].State != stateRunning {
		t.Errorf("card nodes = %+v, want the one node the stream named, running", card.Nodes)
	}
}

func TestDashboard_ARunDirectoryThatHasSaidNothingIsPendingNotFailed(t *testing.T) {
	// A run directory exists before either contract file does: the run lock is
	// taken (and an auto run's graph.json saved) before the first event lands
	// and long before the first snapshot. Nothing on disk has spoken yet, which
	// is pending — the state the card starts in. Calling it failed would put a
	// red card on the dashboard for every healthy run, for as long as it takes
	// that run to emit its first event.
	root := runsRootWith(t, "run-brand-new")

	var cards []runCard
	getJSON(t, newTestDashboard(root), "/api/cards", &cards)
	card := cardByID(t, cards, "run-brand-new")
	if card.State != statePending {
		t.Errorf("card state = %q, want %q", card.State, statePending)
	}
	if card.Available || len(card.Nodes) != 0 || card.Error != "" {
		t.Errorf("card = %+v, want an empty pending card with no error", card)
	}
}

func TestDashboard_AFailedRunIsAFailedCard(t *testing.T) {
	// The settled-and-not-all-passed case: the leg is closed and one node
	// failed. It is runState's default arm — the one with no positive fact of
	// its own — so it is pinned here rather than left to be reached only by
	// accident.
	root := runsRootWith(t, "run-fail")
	dir := filepath.Join(root, "run-fail")
	writeSnapshot(t, dir, runstate.Snapshot{
		RunID: "run-fail",
		Graph: json.RawMessage(twoNodeGraph),
		Nodes: map[string]runstate.NodeRecord{
			"a": {Verdict: runstate.VerdictPass, CostUSD: 0.25},
			"b": {Verdict: runstate.VerdictFail, CostUSD: 0.75},
		},
	})
	writeEvents(t, dir, "run-fail",
		runfeed.Event{Type: runfeed.EventRunStarted},
		runfeed.Event{Type: runfeed.EventNodePassed, NodeID: "a", Verdict: runfeed.VerdictPass, CostUSD: 0.25},
		runfeed.Event{Type: runfeed.EventNodeFailed, NodeID: "b", Verdict: runfeed.VerdictFail, CostUSD: 0.75},
		runfeed.Event{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomeFailed},
	)

	var cards []runCard
	getJSON(t, newTestDashboard(root), "/api/cards", &cards)
	card := cardByID(t, cards, "run-fail")
	if card.State != stateFailed {
		t.Errorf("card state = %q, want %q", card.State, stateFailed)
	}
	if got := (cardCounts{Total: 2, Passed: 1, Failed: 1}); card.Counts != got {
		t.Errorf("card counts = %+v, want %+v", card.Counts, got)
	}
	if card.EndedAt == "" {
		t.Errorf("a settled card has no end timestamp; its elapsed cannot be rendered")
	}
}

func TestDashboard_AnUnreadableRunIsShownNotDropped(t *testing.T) {
	// `runs list` skips a broken run with a warning, because a table can. A
	// dashboard that silently omitted one would be lying about what is on the
	// machine, so the card renders in an unknown state carrying the reason.
	root := runsRootWith(t, "run-broken")
	if err := os.WriteFile(filepath.Join(root, "run-broken", stateFileName), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt snapshot: %v", err)
	}

	var cards []runCard
	getJSON(t, newTestDashboard(root), "/api/cards", &cards)
	card := cardByID(t, cards, "run-broken")
	if card.State != stateUnknown || card.Error == "" {
		t.Errorf("card = (state %q, error %q), want an unknown card carrying the reason", card.State, card.Error)
	}

	// The reason is the SHARED sentence (ADR 0015), not the raw reader error
	// this page used to show: the same wording, with the same classification,
	// that `runs list --show-skipped`, `show` and `watch` say about the same
	// directory. The expected framing is taken from runstatus.Unreadable itself
	// rather than written out again here — a copy in this package's tests would
	// be the very fork the one-sentence rule exists to prevent, and would keep
	// passing after the shared wording changed.
	frame := strings.TrimSuffix(runstatus.Unreadable("run-broken", errors.New(sentinelReaderError)), sentinelReaderError)
	if !strings.HasPrefix(card.Error, frame) {
		t.Errorf("card error = %q, want it composed by runstatus.Unreadable (prefix %q)", card.Error, frame)
	}
	// ...and the reader's own error still survives inside it, quoted whole.
	if !strings.Contains(card.Error, "invalid character") {
		t.Errorf("card error = %q, want the reader's own error quoted whole", card.Error)
	}
}

// sentinelReaderError is a reader error no reader produces, used to read the
// framing runstatus.Unreadable puts AROUND an error without restating it.
const sentinelReaderError = "sentinel-reader-error"

func TestBuildCard_AgreesWithTheSharedRule(t *testing.T) {
	// The cross-surface agreement test, and the reason ADR 0015 §2 puts the
	// derivation in one place — judging ALL SIX values since ADR 0023, not just
	// the liveness half. Four things are judged against each other on every
	// fixture, each with a real lock in each of its three conditions:
	//
	//  1. runfeed.InFlight's own rule (the last leg is open) against the leg
	//     state buildCard derives inline off the walk it already does — it reads
	//     the stream ONCE on the dashboard's hot path, which is only safe while
	//     the two are the same rule;
	//  2. runstatus.Of (every fact, read from a path) against runstatus.Probe
	//     (the same rule for a caller that already walked and loaded);
	//  3. the card's own state word against that shared answer, through the
	//     complete six-value mapping — a new status must be given a colour here
	//     rather than inheriting one;
	//  4. ResolveRun's preference against it too — it must prefer an in-flight
	//     run (PLANNING included) over a newer settled one, and must NOT prefer
	//     an abandoned one.
	cases := map[string][]runfeed.Event{
		"no events at all": {},
		"open leg": {
			{Type: runfeed.EventRunStarted},
			{Type: runfeed.EventNodeStarted, NodeID: "a"},
		},
		"closed leg": {
			{Type: runfeed.EventRunStarted},
			{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePassed},
		},
		"resumed: a second leg reopens it": {
			{Type: runfeed.EventRunStarted},
			{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePaused},
			{Type: runfeed.EventRunStarted},
			{Type: runfeed.EventNodeStarted, NodeID: "b"},
		},
		"resumed and settled": {
			{Type: runfeed.EventRunStarted},
			{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePaused},
			{Type: runfeed.EventRunStarted},
			{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePassed},
		},
		"a close with no open before it": {
			{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePassed},
		},
		"node events only": {
			{Type: runfeed.EventNodeStarted, NodeID: "a"},
		},
		// The three shapes ADR 0023 introduces.
		"a planner call in progress": {
			{Type: runfeed.EventRunStarted, Phase: runfeed.PhasePlanning},
		},
		"a planner call whose plan committed": {
			{Type: runfeed.EventRunStarted, Phase: runfeed.PhasePlanning},
			{Type: runfeed.EventRunStarted},
		},
		"a refused plan: a failed leg with zero node events": {
			{Type: runfeed.EventRunStarted, Phase: runfeed.PhasePlanning},
			{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomeFailed},
		},
		"a paused leg": {
			{Type: runfeed.EventRunStarted},
			{Type: runfeed.EventNodeStarted, NodeID: "a"},
			{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePaused},
		},
	}
	wantState := map[runstatus.Status]string{
		runstatus.Planning:  statePlanning,
		runstatus.Running:   stateRunning,
		runstatus.Abandoned: stateAbandoned,
		runstatus.Paused:    statePaused,
		runstatus.Pass:      statePassed,
		runstatus.Fail:      stateFailed,
	}
	for name, events := range cases {
		for _, lock := range []string{"no lock file", "lock held", "lock free"} {
			t.Run(name+", "+lock, func(t *testing.T) {
				// Two runs, so ResolveRun's preference is observable: run-1
				// carries the fixture, run-2 is newer (ids sort lexically) and
				// always settled, so it is what an id-less resolve falls back to.
				root := runsRootWith(t, "run-1", "run-2")
				seedSettledRun(t, root, "run-2")
				dir := filepath.Join(root, "run-1")
				if len(events) > 0 {
					writeEvents(t, dir, "run-1", events...)
				}
				switch lock {
				case "lock held":
					holdLock(t, dir)
				case "lock free":
					freeLock(t, dir)
				}
				feedPath := filepath.Join(dir, runfeed.FileName)

				openLeg, err := runfeed.InFlight(feedPath)
				if err != nil {
					t.Fatalf("runfeed.InFlight returned error: %v", err)
				}
				walked, err := walkNodeStates(feedPath)
				if err != nil {
					t.Fatalf("walkNodeStates returned error: %v", err)
				}
				if walked.open != openLeg {
					t.Errorf("derived open leg = %v, want runfeed.InFlight's %v", walked.open, openLeg)
				}

				shared, err := runstatus.Of(dir)
				if err != nil {
					t.Fatalf("runstatus.Of returned error: %v", err)
				}
				facts := runstatus.Facts{OpenLeg: walked.open, AnyLeg: walked.anyLeg, Phase: walked.phase, LastOutcome: walked.lastOutcome}
				if probed := runstatus.Probe(dir, facts); probed != shared {
					t.Errorf("the card's composition = %v, want the shared rule's %v", probed, shared)
				}

				card := buildCard(root, "run-1")
				want := wantState[shared]
				// Spelled out here rather than borrowed from runstatus.Spoken:
				// this test is what judges the card against the shared rule, so
				// it states the rule independently.
				spoken := walked.anyLeg || len(walked.states) > 0
				if !spoken {
					// The one affirmative exception: a directory whose stream
					// has said NOTHING — no run_started, no node event — has no
					// status to paint yet and keeps `pending` (ADR 0023 §2.1.1).
					// A lone run_finished with no open before it is such a
					// stream: it is damage, and it is not a leg.
					want = statePending
				}
				if card.State != want {
					t.Errorf("card state = %q, want %q for a %v run", card.State, want, shared)
				}
				if (card.Hint != "") != (shared == runstatus.Abandoned && spoken) {
					t.Errorf("card hint = %q for a %v run — only an abandoned one carries the recovery sentence", card.Hint, shared)
				}

				resolved, err := ResolveRun(root, "")
				if err != nil {
					t.Fatalf("ResolveRun returned error: %v", err)
				}
				wantResolved := "run-2" // the newest, the fallback
				if shared.InFlight() {
					wantResolved = "run-1" // preferred: it is happening right now
				}
				if resolved != wantResolved {
					t.Errorf("ResolveRun = %q, want %q (run-1 is %v)", resolved, wantResolved, shared)
				}
			})
		}
	}
}

// TestBuildCard_ARefusedPlanRendersAsFailedNotPending is ADR 0023 §2.1.1's
// regression, on the dashboard's side. A refused plan's directory holds
// resume.lock, a two-line events.jsonl and rejected.json — no graph.json and no
// state.json, forever. The card's `pending` guard used to key on "has not
// settled", and under six values FAIL is settled, so a mechanical rewrite would
// have left every refused plan reading `pending` for good: a card promising a
// run is about to start, about a run that ended before it began.
func TestBuildCard_ARefusedPlanRendersAsFailedNotPending(t *testing.T) {
	root := runsRootWith(t, "run-refused")
	dir := filepath.Join(root, "run-refused")
	writeEvents(t, dir, "run-refused",
		runfeed.Event{Type: runfeed.EventRunStarted, Phase: runfeed.PhasePlanning},
		runfeed.Event{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomeFailed,
			CostUSD: 0.3, CostUnknown: true,
			Usage: runfeed.TokenUsage{InputTokens: 9, CachedInputTokens: 2, OutputTokens: 4, ReasoningOutputTokens: 1}},
	)
	// buildCard never reads this file; it is here so the fixture is the whole
	// directory shape ADR 0023 §3 describes, and so a future card that DOES
	// notice a rejected spec is tested against a real one rather than an absence.
	if err := os.WriteFile(filepath.Join(dir, "rejected.json"), []byte(`{"name":"x"}`), 0o600); err != nil {
		t.Fatalf("write rejected spec: %v", err)
	}
	freeLock(t, dir)

	card := buildCard(root, "run-refused")
	if card.State != stateFailed {
		t.Errorf("card state = %q, want %q", card.State, stateFailed)
	}
	if card.Error != "" {
		t.Errorf("a refused plan is not a broken directory, got error %q", card.Error)
	}
	if card.Available {
		t.Error("a refused plan has no graph, so the card must not claim a structure")
	}
	if card.CostUSD != 0.3 || !card.CostUnknown {
		t.Errorf("refused plan card cost = %v unknown %v, want unknown with $0.30 known subtotal", card.CostUSD, card.CostUnknown)
	}
	if card.Usage != (runstate.TokenUsage{InputTokens: 9, CachedInputTokens: 2, OutputTokens: 4, ReasoningOutputTokens: 1}) {
		t.Errorf("refused plan card usage = %+v", card.Usage)
	}
}

// TestBuildCard_APlanningRunHasACardBeforeItHasAGraph is #163 itself, on the
// surface the report names. Through the planner call the directory holds
// resume.lock and a one-line events.jsonl and NOTHING else — no graph.json, no
// state.json — and it must render as a live card rather than as `pending`,
// `unknown`, or (as before ADR 0023) no card at all, because no directory
// existed.
func TestBuildCard_APlanningRunHasACardBeforeItHasAGraph(t *testing.T) {
	root := runsRootWith(t, "run-planning")
	dir := filepath.Join(root, "run-planning")
	writeEvents(t, dir, "run-planning", runfeed.Event{Type: runfeed.EventRunStarted, Phase: runfeed.PhasePlanning})
	holdLock(t, dir)

	card := buildCard(root, "run-planning")
	if card.State != statePlanning {
		t.Errorf("card state = %q, want %q", card.State, statePlanning)
	}
	if card.Available || len(card.Nodes) != 0 {
		t.Errorf("a planning run has no graph yet: Available=%v, %d node(s)", card.Available, len(card.Nodes))
	}
	if card.StartedAt == "" {
		t.Error("a planning card must carry its started_at — the elapsed clock is the whole point of showing it")
	}
	if card.Error != "" {
		t.Errorf("a planning run is not a broken directory, got error %q", card.Error)
	}
}

// TestBuildCard_ASessionLimitPauseIsNotRed is the dashboard's half of the defect
// ADR 0023 §1.2 measures. The card used to ask the snapshot's gate.paused_at
// whether a run was paused; ADR 0009's session-limit pause has no gate, so it
// fell through the default arm and painted red, as failed. PAUSED is read off
// the stream's outcome instead, which covers both pause shapes — this fixture
// carries no gate anywhere.
func TestBuildCard_ASessionLimitPauseIsNotRed(t *testing.T) {
	root := runsRootWith(t, "run-limited")
	dir := filepath.Join(root, "run-limited")
	writeSnapshot(t, dir, runstate.Snapshot{
		RunID: "run-limited",
		Graph: json.RawMessage(twoNodeGraph),
		Nodes: map[string]runstate.NodeRecord{
			"a": {Verdict: runstate.VerdictPass},
		},
	})
	writeEvents(t, dir, "run-limited",
		runfeed.Event{Type: runfeed.EventRunStarted},
		runfeed.Event{Type: runfeed.EventNodePassed, NodeID: "a", Verdict: runfeed.VerdictPass},
		runfeed.Event{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePaused},
	)
	freeLock(t, dir)

	card := buildCard(root, "run-limited")
	if card.State != statePaused {
		t.Errorf("card state = %q, want %q — a session-limited run stopped as designed and is resumable, "+
			"and above all must not paint %q", card.State, statePaused, stateFailed)
	}
}

// holdLock takes run dir's real lock and holds it for the test, as a live leg
// does. It skips where the probe cannot answer — no flock(2), or a filesystem
// outside runstate's known-local allowlist — because there the derivation is
// deliberately unknown and asserting otherwise would assert against ADR 0015's
// own safety gate.
func holdLock(t *testing.T, runDir string) {
	t.Helper()
	path := filepath.Join(runDir, runstate.LockFileName)
	release, err := runstate.AcquireLock(path)
	if err != nil {
		t.Fatalf("acquire fixture lock: %v", err)
	}
	t.Cleanup(func() { release() })
	if got := runstate.ProbeLock(path); got != runstate.LivenessHeld {
		t.Skipf("the lock probe cannot answer here (%v)", got)
	}
}

// freeLock leaves exactly what a leg that died leaves: a marked lock file,
// written by the real AcquireLock, that nothing holds.
func freeLock(t *testing.T, runDir string) {
	t.Helper()
	path := filepath.Join(runDir, runstate.LockFileName)
	release, err := runstate.AcquireLock(path)
	if err != nil {
		t.Fatalf("acquire fixture lock: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("release fixture lock: %v", err)
	}
	if got := runstate.ProbeLock(path); got != runstate.LivenessFree {
		t.Skipf("the lock probe cannot answer here (%v)", got)
	}
}

// --- an abandoned run's card ---------------------------------------------------

func TestBuildCard_AnAbandonedRunStopsSpinning(t *testing.T) {
	// The two zombie runs, as a fixture: a leg that started a node and then
	// died. The node must not keep spinning, the card must not claim the run is
	// running, and the tally must not claim work is in progress.
	root := runsRootWith(t, "run-dead")
	seedInFlightRun(t, root, "run-dead")
	freeLock(t, filepath.Join(root, "run-dead"))

	var cards []runCard
	getJSON(t, newTestDashboard(root), "/api/cards", &cards)
	card := cardByID(t, cards, "run-dead")

	if card.State != stateAbandoned {
		t.Errorf("card state = %q, want %q", card.State, stateAbandoned)
	}
	for _, node := range card.Nodes {
		if node.State == stateRunning {
			t.Errorf("node %q still reads as running in an abandoned run", node.ID)
		}
	}
	if card.Counts.Running != 0 {
		t.Errorf("counts.running = %d in an abandoned run, want 0", card.Counts.Running)
	}
	// tally's existing default arm: an abandoned node is "not done yet".
	if card.Counts.Pending != 1 {
		t.Errorf("counts.pending = %d, want the abandoned node to tally as pending", card.Counts.Pending)
	}
	if !strings.Contains(card.Hint, "run-dead") {
		t.Errorf("card hint = %q, want the recovery hint for this run", card.Hint)
	}
}

// TestBuildCard_ALaterLegClosesTheDeadLegsNodes is the per-node half of the same
// bug, at the run level: a node left running by a leg that died stays running
// in the card's reducer across every later leg that does not re-run it, because
// the reducer only ever saw node events. The regression is a run that has since
// been resumed and FINISHED — its lock is free and its leg is closed, so the
// run-level derivation says nothing at all, and only the leg boundary can.
func TestBuildCard_ALaterLegClosesTheDeadLegsNodes(t *testing.T) {
	root := runsRootWith(t, "run-resumed")
	dir := filepath.Join(root, "run-resumed")
	writeSnapshot(t, dir, runstate.Snapshot{
		RunID: "run-resumed",
		Graph: json.RawMessage(twoNodeGraph),
		Nodes: map[string]runstate.NodeRecord{
			"a": {Verdict: runstate.VerdictPass, CostUSD: 0.25},
			"b": {Verdict: runstate.VerdictPass, CostUSD: 0.75},
		},
	})
	writeEvents(t, dir, "run-resumed",
		// Leg 1: `a` starts, then the process dies — no terminal, no
		// run_finished.
		runfeed.Event{Type: runfeed.EventRunStarted},
		runfeed.Event{Type: runfeed.EventNodeStarted, NodeID: "a"},
		// Leg 2: a resume that re-ran nothing but `b`, and finished.
		runfeed.Event{Type: runfeed.EventRunStarted},
		runfeed.Event{Type: runfeed.EventNodeStarted, NodeID: "b"},
		runfeed.Event{Type: runfeed.EventNodePassed, NodeID: "b", Verdict: runfeed.VerdictPass, CostUSD: 0.75},
		runfeed.Event{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePassed},
	)

	var cards []runCard
	getJSON(t, newTestDashboard(root), "/api/cards", &cards)
	card := cardByID(t, cards, "run-resumed")

	for _, node := range card.Nodes {
		if node.ID == "a" && node.State == stateRunning {
			t.Error("node `a` was left open by a leg that died; a later leg is a boundary, so it must not still read as running")
		}
	}
	if card.Counts.Running != 0 {
		t.Errorf("counts.running = %d in a finished run, want 0", card.Counts.Running)
	}
	if card.State == stateRunning || card.State == stateAbandoned {
		t.Errorf("the run itself finished; card state = %q", card.State)
	}
}

func TestDashboard_AnEmptyRunsRootIsAnEmptyDashboard(t *testing.T) {
	// `serve` on a machine that has never run anything is a page saying so,
	// not an error: the dashboard subscribes to the ROOT, so the first run
	// ever started appears on it without a restart.
	for _, root := range []string{t.TempDir(), filepath.Join(t.TempDir(), "never-created")} {
		var cards []runCard
		getJSON(t, newTestDashboard(root), "/api/cards", &cards)
		if len(cards) != 0 {
			t.Errorf("root %q: got %d cards, want none", root, len(cards))
		}
	}
}

// --- /run/<id>: the single-run view, mounted --------------------------------

func TestDashboard_MountsTheUnchangedSingleRunViewPerRun(t *testing.T) {
	// A card click must land on exactly the page `serve <id>` serves — same
	// handlers, same payloads, just behind /run/<id>/. Compared against the
	// standalone Server's own answer so the two cannot drift.
	root := runsRootWith(t, "run-live")
	seedInFlightRun(t, root, "run-live")
	d := newTestDashboard(root)

	var mounted, standalone graphPayload
	getJSON(t, d, "/run/run-live/api/graph", &mounted)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "GET", "http://127.0.0.1:8642/api/graph", nil)
	newTestServer(filepath.Join(root, "run-live"), "run-live").Handler().ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &standalone); err != nil {
		t.Fatalf("standalone /api/graph body: %v", err)
	}
	if mounted.RunID != standalone.RunID || mounted.Name != standalone.Name || len(mounted.Nodes) != len(standalone.Nodes) {
		t.Errorf("mounted /api/graph = %+v, want the standalone view's %+v", mounted, standalone)
	}

	// The page and its assets resolve under the prefix too — which is the
	// whole reason the mount is path-scoped: every URL the page fetches is
	// document-relative, so nothing in the UI had to learn about the mount.
	for _, path := range []string{"/run/run-live/", "/run/run-live/app.js", "/run/run-live/style.css"} {
		if code := get(t, d, path).Code; code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", path, code)
		}
	}

	// And a node artifact, the route that takes a node id from the URL: its
	// own membership guard still runs, one level below this one.
	if code := get(t, d, "/run/run-live/api/result?node=nope").Code; code != http.StatusNotFound {
		t.Errorf("an unknown node id under a mounted run = %d, want 404", code)
	}
}

func TestDashboard_RunWithoutATrailingSlashReachesTheView(t *testing.T) {
	// The page's URLs are relative, so they only resolve correctly under the
	// trailing slash. ServeMux's subtree redirect is what gets a hand-typed
	// /run/<id> there.
	root := runsRootWith(t, "run-live")
	seedInFlightRun(t, root, "run-live")

	rec := get(t, newTestDashboard(root), "/run/run-live")
	if rec.Code < 300 || rec.Code > 399 {
		t.Fatalf("GET /run/run-live status = %d, want a redirect to the trailing slash", rec.Code)
	}
	if got := rec.Header().Get("Location"); !strings.HasSuffix(got, "/run/run-live/") {
		t.Errorf("Location = %q, want the same path with a trailing slash", got)
	}
}

func TestDashboard_MembershipGuardMatchesTheRunsRootListing(t *testing.T) {
	// SECURITY: the run id is URL input. It is matched against the names of
	// the directories under the runs root BEFORE any path is built from it,
	// so nothing that is not literally a run directory can reach the
	// filesystem — a typo and a traversal probe are the same 404.
	root := runsRootWith(t, "run-live")
	seedInFlightRun(t, root, "run-live")
	d := newTestDashboard(root)

	for _, path := range []string{
		"/run/no-such-run/api/graph",
		"/run/no-such-run/",
		"/run/..%2f..%2fetc/api/graph",
		"/run/.../api/graph",
	} {
		rec := get(t, d, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404 — only a listed run directory may be served", path, rec.Code)
		}
	}

	// A file (not a directory) under the runs root is not a run either.
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write stray file: %v", err)
	}
	if code := get(t, d, "/run/notes.txt/api/graph").Code; code != http.StatusNotFound {
		t.Errorf("GET a stray file as a run = %d, want 404", code)
	}
}

func TestDashboard_RejectsNonLoopbackHostEverywhereIncludingMountedRuns(t *testing.T) {
	// The DNS-rebinding guard wraps the whole mux, so mounting runs under it
	// cannot open a hole around it.
	root := runsRootWith(t, "run-live")
	seedInFlightRun(t, root, "run-live")
	handler := newTestDashboard(root).Handler()

	for _, path := range []string{"/", "/api/cards", "/run/run-live/", "/run/run-live/api/graph"} {
		req := httptest.NewRequestWithContext(context.Background(), "GET", path, nil)
		req.Host = "evil.example.com"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("GET %s with a rebound Host = %d, want 403", path, rec.Code)
		}
	}
}

func TestDashboard_ServesItsPageAndOnlyItsOwnAssetsAtTheRoot(t *testing.T) {
	d := newTestDashboard(runsRootWith(t))

	page := get(t, d, "/")
	if page.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", page.Code)
	}
	if !strings.Contains(page.Body.String(), `src="dashboard.js"`) {
		t.Errorf("the dashboard page does not load dashboard.js:\n%s", page.Body.String())
	}
	for _, path := range []string{"/dashboard.js", "/dashboard.css", "/style.css"} {
		if code := get(t, d, path).Code; code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200 — the dashboard page loads it", path, code)
		}
	}
	// The single-run app is NOT served at the root: there is no run at "/",
	// so app.js there would poll a run that does not exist.
	if code := get(t, d, "/app.js").Code; code != http.StatusNotFound {
		t.Errorf("GET /app.js at the dashboard root = %d, want 404", code)
	}
}

// --- the gate token: one per process, shared by every mounted run ------------

func TestDashboard_MountedRunsShareTheProcessGateToken(t *testing.T) {
	// The token identifies the serving PROCESS, not the run. The dashboard
	// page and every run view it mounts are served by one process, so they
	// carry one token — and it is the one the gate routes accept.
	root := runsRootWith(t, "run-gate")
	dir := filepath.Join(root, "run-gate")
	writeSnapshot(t, dir, runstate.Snapshot{
		RunID: "run-gate",
		Graph: json.RawMessage(gateGraph),
		Nodes: map[string]runstate.NodeRecord{"a": {Verdict: runstate.VerdictPass}},
		Gate:  runstate.GateState{PausedAt: "approve"},
	})
	resumer := newFakeResumer()
	d := newTestDashboard(root).WithGateResumer(resumer)

	token := tokenFromPage(t, get(t, d, "/").Body.String())
	if mounted := tokenFromPage(t, get(t, d, "/run/run-gate/").Body.String()); mounted != token {
		t.Errorf("the mounted run's page carries token %q, want the dashboard's %q", mounted, token)
	}

	req := httptest.NewRequestWithContext(context.Background(), "POST",
		"http://127.0.0.1:8642/run/run-gate/api/gate/approve", strings.NewReader(`{"node":"approve"}`))
	req.Header.Set("X-OMG-Token", token)
	rec := httptest.NewRecorder()
	d.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("gate approve under the dashboard = %d, want 202 (%s)", rec.Code, rec.Body.String())
	}
	call := resumer.awaitCall(t)
	if call.runID != "run-gate" || call.gateID != "approve" || call.decision != gate.DecisionApprove {
		t.Errorf("resumed %+v, want (run-gate, approve, approve)", call)
	}
}

// --- /api/cards/events: cards appear and settle live ------------------------

// readCard reads one SSE frame and asserts it is a `card` frame, returning the
// decoded card.
func readCard(t *testing.T, stream *sseStream) runCard {
	t.Helper()
	name, data := stream.readFrame(t)
	if name != "card" {
		t.Fatalf("frame = (%q, %s), want a card frame", name, data)
	}
	var card runCard
	if err := json.Unmarshal([]byte(data), &card); err != nil {
		t.Fatalf("card frame is not a card: %v (%s)", err, data)
	}
	return card
}

// awaitCard reads card frames until runID arrives in state, or fails.
//
// It tolerates intermediate frames on purpose: a run settles by writing TWO
// files, and a sweep landing between them legitimately sends a card built from
// the half-updated pair. The client keys by run id and replaces, so an
// intermediate card is corrected by the next one — that is the contract this
// waits on, not a race being papered over.
func awaitCard(t *testing.T, stream *sseStream, runID, state string) runCard {
	t.Helper()
	for i := 0; i < 20; i++ {
		card := readCard(t, stream)
		if card.RunID == runID && card.State == state {
			return card
		}
	}
	t.Fatalf("run %q never reached state %q on the card stream", runID, state)
	return runCard{}
}

func TestDashboardEvents_ReplaysEveryRunThenStreamsChanges(t *testing.T) {
	// The dashboard subscribes to the runs root the way a run view subscribes
	// to its events.jsonl: everything already there arrives first, then
	// changes stream as they land.
	root := runsRootWith(t, "run-live")
	seedInFlightRun(t, root, "run-live")

	stream, cancel := sseClientAt(t, newTestDashboard(root).Handler(), "/api/cards/events")
	defer cancel()

	if card := readCard(t, stream); card.RunID != "run-live" || card.State != stateRunning {
		t.Fatalf("first frame = (%q, %q), want the in-flight run's card", card.RunID, card.State)
	}
	if name, data := stream.readFrame(t); name != "cards_ready" {
		t.Fatalf("frame = (%q, %s), want cards_ready after the first sweep", name, data)
	}

	// The run settles: its stream and snapshot change, so its card is resent
	// — the card the operator is looking at flips from running to passed
	// without a reload.
	writeEvents(t, filepath.Join(root, "run-live"), "run-live",
		runfeed.Event{Type: runfeed.EventNodePassed, NodeID: "b", Verdict: runfeed.VerdictPass, CostUSD: 0.75},
		runfeed.Event{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePassed},
	)
	writeSnapshot(t, filepath.Join(root, "run-live"), runstate.Snapshot{
		RunID: "run-live",
		Graph: json.RawMessage(twoNodeGraph),
		Nodes: map[string]runstate.NodeRecord{
			"a": {Verdict: runstate.VerdictPass, CostUSD: 0.25},
			"b": {Verdict: runstate.VerdictPass, CostUSD: 0.75},
		},
	})

	card := awaitCard(t, stream, "run-live", statePassed)
	if card.CostUSD != 1.0 || card.EndedAt == "" {
		t.Errorf("updated card = (cost %v, ended %q), want the settled totals", card.CostUSD, card.EndedAt)
	}
}

func TestDashboardEvents_ARunStartedAfterTheSubscriptionAppears(t *testing.T) {
	// The card that was not there when the page loaded: this is what one port
	// for every run buys over one `serve` per run.
	root := runsRootWith(t)

	stream, cancel := sseClientAt(t, newTestDashboard(root).Handler(), "/api/cards/events")
	defer cancel()

	if name, data := stream.readFrame(t); name != "cards_ready" {
		t.Fatalf("frame = (%q, %s), want cards_ready on an empty root", name, data)
	}

	addRunDir(t, root, "run-new")
	seedInFlightRun(t, root, "run-new")

	if card := awaitCard(t, stream, "run-new", stateRunning); len(card.Nodes) == 0 {
		t.Errorf("the new run's card carries no nodes: %+v", card)
	}
}

func TestDashboardEvents_AQuietRunIsNotResent(t *testing.T) {
	// The sweep is stamp-based: an idle dashboard with settled runs on it
	// costs two stats per run per tick and re-sends nothing. Without this a
	// wall of forty finished runs would re-serialize five times a second.
	root := runsRootWith(t, "run-done")
	seedSettledRun(t, root, "run-done")

	stream, cancel := sseClientAt(t, newTestDashboard(root).Handler(), "/api/cards/events")
	defer cancel()

	if card := readCard(t, stream); card.RunID != "run-done" {
		t.Fatalf("first frame is for %q, want run-done", card.RunID)
	}
	if name, _ := stream.readFrame(t); name != "cards_ready" {
		t.Fatalf("frame name = %q, want cards_ready", name)
	}

	// run-done does not change; run-live2 appears. Several sweeps run in
	// between, and the next frame must be the new run's — not a re-send of
	// the quiet one.
	addRunDir(t, root, "run-live2")
	seedInFlightRun(t, root, "run-live2")
	if card := readCard(t, stream); card.RunID != "run-live2" {
		t.Errorf("next frame is for %q, want only the changed run (run-live2)", card.RunID)
	}
}

// addRunDir adds another run directory to an existing root, so a test can grow
// the root mid-subscription.
func addRunDir(t *testing.T, root, runID string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, runID), 0o755); err != nil {
		t.Fatalf("create run dir %q: %v", runID, err)
	}
}

func TestDashboardEvents_ADeletedRunIsAnnounced(t *testing.T) {
	// A run directory removed under the dashboard's feet must take its card
	// with it, or the page keeps offering a link to a 404.
	root := runsRootWith(t, "run-done")
	seedSettledRun(t, root, "run-done")

	stream, cancel := sseClientAt(t, newTestDashboard(root).Handler(), "/api/cards/events")
	defer cancel()

	readCard(t, stream)
	if name, _ := stream.readFrame(t); name != "cards_ready" {
		t.Fatalf("frame name = %q, want cards_ready", name)
	}

	// The run goes away in ONE step — a rename out of the root, not
	// os.RemoveAll — because "the very next frame is card_removed" is only a
	// promise the sweep can keep for a removal it cannot observe half-done.
	// os.RemoveAll unlinks the contract files first and the directory second,
	// and a sweep landing in between sees a directory that is real and empty,
	// which is byte-for-byte a healthy young run (handleCardEvents names the two
	// tests that pin those shapes). It draws the card it must draw for those,
	// and card_removed follows a tick later. Racing that window is what made
	// this test flaky; it is not what this test is about. A rename within one
	// filesystem is atomic, so every sweep either sees the run whole or does not
	// see it at all, and the next frame is decided by the assertion rather than
	// by the clock.
	graveyard := t.TempDir() // a sibling of root, so the rename stays on one fs
	if err := os.Rename(filepath.Join(root, "run-done"), filepath.Join(graveyard, "run-done")); err != nil {
		t.Fatalf("remove run dir: %v", err)
	}
	name, data := stream.readFrame(t)
	if name != "card_removed" {
		t.Fatalf("frame = (%q, %s), want card_removed", name, data)
	}
	var removed struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal([]byte(data), &removed); err != nil || removed.RunID != "run-done" {
		t.Errorf("card_removed payload = %s (%v), want run-done", data, err)
	}
}

// TestDashboardEvents_ARunGoneFromDiskButStillListedIsRemovedNotRedrawn is the
// sweep's mid-sweep-deletion check, with the timing taken out of it.
//
// The case is a listing that names a run whose directory is already gone —
// which in production is the instant between the sweep's listing and that run's
// stamp, a window no test can win by racing (that race is what made
// TestDashboardEvents_ADeletedRunIsAnnounced flaky). A lister that keeps naming
// a removed run is the same observation, decided by the assertion rather than
// by the clock, and it makes the check load-bearing in both directions: without
// it the empty stamp is announced as a CHANGED card — a tile linking to a 404 —
// and the run is never removed at all, because the stale listing keeps it
// present forever.
func TestDashboardEvents_ARunGoneFromDiskButStillListedIsRemovedNotRedrawn(t *testing.T) {
	root := runsRootWith(t, "run-done")
	seedSettledRun(t, root, "run-done")

	d := newTestDashboard(root)
	d.lister = func(string) ([]string, error) { return []string{"run-done"}, nil }

	stream, cancel := sseClientAt(t, d.Handler(), "/api/cards/events")
	defer cancel()

	if card := readCard(t, stream); card.RunID != "run-done" {
		t.Fatalf("first card = %q, want run-done", card.RunID)
	}
	if name, data := stream.readFrame(t); name != "cards_ready" {
		t.Fatalf("frame = (%q, %s), want cards_ready", name, data)
	}

	// Atomic, so no sweep can see the run half-removed — the listing is the only
	// thing left saying it exists.
	graveyard := t.TempDir()
	if err := os.Rename(filepath.Join(root, "run-done"), filepath.Join(graveyard, "run-done")); err != nil {
		t.Fatalf("remove run dir: %v", err)
	}

	name, data := stream.readFrame(t)
	if name != "card_removed" {
		t.Fatalf("frame = (%q, %s), want card_removed — a listed-but-gone run must not be redrawn as a card", name, data)
	}
	var removed struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal([]byte(data), &removed); err != nil || removed.RunID != "run-done" {
		t.Errorf("card_removed payload = %s (%v), want run-done", data, err)
	}
}

// TestDashboardEvents_APreRunfeedRunGoneFromDiskIsStillRemoved is the same
// observation for the one run whose stamp deletion does not move: a directory
// that has written neither its feed nor its snapshot yet stamps as empty, and
// so does a directory that is gone. The two are told apart by the directory
// check, which is why that check has to sit in FRONT of the unchanged-stamp
// fast path — behind it, the sweep compares empty to empty, takes the fast
// path, and the run stays present with no card_removed ever sent.
//
// Deliberately a separate test from the settled-run case above: a settled run's
// stamp changes when its files go, so it reaches the check either way, and one
// test cannot pin both orderings.
func TestDashboardEvents_APreRunfeedRunGoneFromDiskIsStillRemoved(t *testing.T) {
	root := runsRootWith(t, "run-young")

	d := newTestDashboard(root)
	d.lister = func(string) ([]string, error) { return []string{"run-young"}, nil }

	stream, cancel := sseClientAt(t, d.Handler(), "/api/cards/events")
	defer cancel()

	// The card a directory that has said nothing yet must still get — the empty
	// stamp the sweep now holds for it is the one the deletion will not change.
	if card := readCard(t, stream); card.RunID != "run-young" {
		t.Fatalf("first card = %q, want run-young", card.RunID)
	}
	if name, data := stream.readFrame(t); name != "cards_ready" {
		t.Fatalf("frame = (%q, %s), want cards_ready", name, data)
	}

	graveyard := t.TempDir()
	if err := os.Rename(filepath.Join(root, "run-young"), filepath.Join(graveyard, "run-young")); err != nil {
		t.Fatalf("remove run dir: %v", err)
	}

	name, data := stream.readFrame(t)
	if name != "card_removed" {
		t.Fatalf("frame = (%q, %s), want card_removed — a gone run whose stamp never moved must still be removed", name, data)
	}
	var removed struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal([]byte(data), &removed); err != nil || removed.RunID != "run-young" {
		t.Errorf("card_removed payload = %s (%v), want run-young", data, err)
	}
}

// --- security: the dashboard and its mounted runs refuse to be framed --------

func TestDashboard_RefusesToBeFramed(t *testing.T) {
	// The dashboard is the easier half of the clickjack to aim: "/" needs no
	// run id at all, so a hostile page can frame it without knowing anything
	// about this machine, and a card click there navigates the frame to the
	// run view that carries the gate buttons. Both front-ends must refuse.
	root := runsRootWith(t, "run-1")
	seedInFlightRun(t, root, "run-1")
	handler := newTestDashboard(root).Handler()

	paths := []string{
		"/",             // the dashboard page
		"/api/cards",    // an API route of the dashboard's own mux
		"/dashboard.js", // a static asset
		"/run/run-1/",   // the mounted run view: the page with the gate buttons
		"/run/run-1/api/graph",
	}
	for _, path := range paths {
		req := httptest.NewRequestWithContext(context.Background(), "GET", path, nil)
		req.Host = "127.0.0.1:8642"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, want 200", path, rec.Code)
		}
		assertRefusesFraming(t, rec, "GET "+path)
	}
}
