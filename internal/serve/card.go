package serve

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// Run states as the dashboard names them — deliberately the SAME vocabulary
// the single-run page paints a node with (style.css's --running, --passed,
// --failed, --gate-paused, --pending), so a card's colour and a node's colour
// mean the same thing and the dashboard's CSS reuses the very same tokens.
// `runs list` says RUNNING/PASS/FAIL for the same runs; this is that
// vocabulary plus the two states a table has no colour for — the pause a run
// is sitting at, and a run that has not produced anything yet.
const (
	stateRunning    = "running"
	statePassed     = "passed"
	stateFailed     = "failed"
	stateGatePaused = "gate-paused"
	statePending    = "pending"
	// stateUnknown is the honest state of a run directory this binary cannot
	// read: a corrupt snapshot, or one written by a schema it refuses. The
	// card renders with its Error set rather than being dropped — a dashboard
	// that silently omits a run is worse than one that shows a broken one.
	stateUnknown = "unknown"
)

// runCard is one run's tile on the dashboard: everything a mini-DAG card
// renders, derived entirely from the run directory's two contract files
// (docs/RUN-FEED.md) and nothing else. It is a read model, rebuilt from disk
// on every change — the server holds no per-run state between polls.
type runCard struct {
	RunID string `json:"run_id"`
	// Name and Nodes come from the snapshot's own graph bytes; both are absent
	// during the window where a run exists but its first node has not
	// completed, which is exactly when Available is false.
	Name      string `json:"name,omitempty"`
	Available bool   `json:"available"`
	// State is the run's overall state in the vocabulary above.
	State string `json:"state"`
	// StartedAt / EndedAt are the leg boundaries as RFC 3339 strings, taken
	// verbatim from the stream's first run_started and last run_finished. They
	// are handed over as strings rather than an elapsed number on purpose: the
	// card ticks a live run's elapsed in the browser, so the server never has
	// to be re-polled just to advance a clock.
	StartedAt string `json:"started_at,omitempty"`
	EndedAt   string `json:"ended_at,omitempty"`
	// CostUSD is the sum of the snapshot's per-node reported spend — the same
	// accounting `runs list` prints, deliberately, so the two never disagree.
	// A run whose first node has not completed costs 0 here, which is honest
	// rather than unknown: nothing has been billed to the snapshot yet.
	CostUSD float64    `json:"cost_usd"`
	Counts  cardCounts `json:"counts"`
	Nodes   []cardNode `json:"nodes,omitempty"`
	// Goal is the goal-lineage block when this run is one cycle of an iterated
	// auto goal (ADR 0011), same shape /api/graph serves.
	Goal *goalPayload `json:"goal,omitempty"`
	// Error is why this run reads as stateUnknown, shown on the card.
	Error string `json:"error,omitempty"`
}

// cardCounts is the card's node tally. Total comes from the graph, the rest
// from the event stream, so Pending is whatever the stream has not spoken for.
type cardCounts struct {
	Total   int `json:"total"`
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Running int `json:"running"`
	Pending int `json:"pending"`
}

// cardNode is one node of the mini-DAG: identity, edges and current state.
// The same three facts /api/graph serves, plus the state the dashboard has
// already derived — a card is a picture, not a second event subscription.
type cardNode struct {
	ID        string   `json:"id"`
	Type      string   `json:"type,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
	State     string   `json:"state"`
}

// buildCard derives one run's card from its directory. It reads through the
// existing readers only — ONE runfeed.Walk for per-node state, the leg
// boundaries and (by runfeed.InFlight's own rule) whether a leg is open, plus
// runstate.Load and graph.Parse for the structure and the cost — so a card can
// never disagree with `runs list`, `watch` or the single-run view about the
// same run. One walk, not two: a card is rebuilt on every tick for every run
// that changed, so reading the stream twice per card was a doubling the
// dashboard pays on its hot path.
//
// It never returns an error: a run directory this binary cannot read becomes
// a stateUnknown card carrying the reason. The dashboard's job is to show
// every run, and a run that is broken is a thing the operator most needs to
// see.
func buildCard(runsRoot, runID string) runCard {
	runDir := filepath.Join(runsRoot, runID)
	card := runCard{RunID: runID, State: statePending}

	states, started, ended, err := walkNodeStates(filepath.Join(runDir, runfeed.FileName))
	if err != nil {
		return brokenCard(runID, err)
	}
	card.StartedAt, card.EndedAt = started, ended
	// runfeed.InFlight's rule — the last leg is still open — read off the walk
	// above instead of walking the stream a second time. walkNodeStates already
	// carries the leg state (it clears ended on every run_started and sets it on
	// every run_finished), and the dashboard rebuilds a card for every changed
	// run on every tick, so the second read was doubling the I/O on the hot
	// path. The two must not drift: TestBuildCard_InFlightAgreesWithRunfeed
	// judges this against runfeed.InFlight itself.
	inFlight := started != "" && ended == ""

	snap, err := runstate.Load(filepath.Join(runDir, stateFileName))
	switch {
	case err == nil:
		g, parseErr := graph.Parse(snap.Graph)
		if parseErr != nil {
			return brokenCard(runID, fmt.Errorf("reconstruct graph: %w", parseErr))
		}
		card.Available = true
		card.Name = g.Name
		for _, node := range g.Nodes {
			card.Nodes = append(card.Nodes, cardNode{
				ID: node.ID, Type: node.Type, DependsOn: node.DependsOn,
				State: nodeState(states, node.ID),
			})
		}
		for _, rec := range snap.Nodes {
			card.CostUSD += rec.CostUSD
		}
		card.State = runState(inFlight, snap.Gate.PausedAt != "", len(snap.CompletedNodes()) == len(g.Nodes))
		if snap.Goal != nil {
			card.Goal = &goalPayload{
				Text: snap.Goal.Text, Cycle: snap.Goal.Cycle,
				MaxCycles: snap.Goal.MaxCycles, FirstRunID: snap.Goal.FirstRunID,
			}
		}
	case errors.Is(err, fs.ErrNotExist):
		// The one legitimate snapshot-less run: state.json is written only
		// after a node's terminal verdict, so a fresh run has no structure to
		// draw yet. The stream still knows which nodes have started, so the
		// card renders those as an edgeless mini-DAG that gains its shape the
		// moment the first node completes — the same honesty `runs list` shows
		// with its "-" placeholders, in a form that is live.
		for _, id := range sortedKeys(states) {
			card.Nodes = append(card.Nodes, cardNode{ID: id, State: states[id]})
		}
		// A directory whose stream has said NOTHING — no leg, no node — keeps
		// the pending it started as. That is a real window, not a corner case:
		// the run lock creates the directory (and an auto run saves its
		// graph.json there) before the first event is emitted, and a pre-runfeed
		// directory has no stream at all. runState's default arm means "settled
		// and not all done", and a run that has not spoken is neither, so
		// letting it fall through would paint every healthy run's first moments
		// red.
		if inFlight || len(states) > 0 {
			card.State = runState(inFlight, false, false)
		}
	default:
		return brokenCard(runID, err)
	}

	card.Counts = tally(card.Nodes)
	return card
}

// brokenCard is the card for a run directory this binary refuses to read.
func brokenCard(runID string, err error) runCard {
	return runCard{RunID: runID, State: stateUnknown, Error: err.Error()}
}

// runState maps the three facts that decide a run's overall colour. An open
// leg wins over everything: mid-run the snapshot holds only the nodes
// completed so far, so the completed==all test would read a healthy run as
// failed (the same trap `runs list` documents). A pause is next, because a
// paused run is settled-but-waiting, which is neither passed nor failed.
func runState(inFlight, paused, allCompleted bool) string {
	switch {
	case inFlight:
		return stateRunning
	case paused:
		return stateGatePaused
	case allCompleted:
		return statePassed
	default:
		return stateFailed
	}
}

// nodeState is what the stream last said about one node, or pending when it
// has said nothing.
func nodeState(states map[string]string, id string) string {
	if state, ok := states[id]; ok {
		return state
	}
	return statePending
}

// walkNodeStates reads a run's stream once and returns what it says: the
// latest state per node, and the leg boundaries (the FIRST run_started, so a
// resumed run's elapsed still counts from when the work began, and the LAST
// run_finished, which is meaningful only once no leg is open).
//
// Latest-terminal-event-wins is the run-feed contract's own rule, feedback
// rounds included, so a node re-run by a feedback arc reads as running again
// exactly as it does in the single-run view. A missing stream is not an
// error: it is a run that has not emitted anything yet (or a pre-runfeed
// directory), which reads as no states and no boundaries.
func walkNodeStates(feedPath string) (states map[string]string, started, ended string, err error) {
	states = map[string]string{}
	err = runfeed.Walk(feedPath, func(event runfeed.Event) error {
		switch event.Type {
		case runfeed.EventRunStarted:
			if started == "" {
				started = event.Timestamp
			}
			ended = ""
		case runfeed.EventRunFinished:
			ended = event.Timestamp
		case runfeed.EventNodeStarted, runfeed.EventNodeRetried:
			states[event.NodeID] = stateRunning
		case runfeed.EventNodePassed:
			states[event.NodeID] = statePassed
		case runfeed.EventNodeFailed:
			states[event.NodeID] = stateFailed
		case runfeed.EventGatePaused:
			states[event.NodeID] = stateGatePaused
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return states, "", "", nil
		}
		return nil, "", "", err
	}
	return states, started, ended, nil
}

// sortedKeys gives a map's keys in a stable order, so a snapshot-less card's
// node list does not reshuffle between polls (which would make the SSE change
// detection fire on nothing).
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// tally counts the card's nodes by state. Anything the stream has not spoken
// for (pending) plus anything in a state with no column of its own (a paused
// gate) counts as pending — "not done yet", which is what the count means.
func tally(nodes []cardNode) cardCounts {
	counts := cardCounts{Total: len(nodes)}
	for _, node := range nodes {
		switch node.State {
		case statePassed:
			counts.Passed++
		case stateFailed:
			counts.Failed++
		case stateRunning:
			counts.Running++
		default:
			counts.Pending++
		}
	}
	return counts
}
