package schedule

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/handoff"
	"github.com/jitokim/oh-my-graph/internal/ledger"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// feedbackState is one Run's view of every feedback arc (ADR 0010): the
// current round per node, and each declarer's precomputed body — membership,
// order, and in-body in-degrees — so the launch loop's re-arming step is a
// lookup, never a graph walk under the scheduling mutex. Built once per
// execute() from graph.FeedbackBody, the same computation validation used,
// so the runtime and the validator cannot disagree about what "the loop" is.
//
// The read helpers (roundOf, roundNote) are nil-safe on the receiver
// (round 0, no note), so paths that run before execute() built the state
// degrade to today's behaviour. fire and bodyInDegrees are NOT: both run
// only after judgeFeedback consulted a real arc, which requires a built
// state — firing an arc without one is a bug to surface, not degrade.
type feedbackState struct {
	mu sync.Mutex
	// rounds is each node's current feedback round: 0 on the initial pass,
	// k while the body is re-executing round k. Seeded from
	// Options.NodeRounds so a resumed mid-loop leg stamps the same round
	// the marker recorded.
	rounds map[string]int

	// arcs maps every body node (declarer included) to its owning arc.
	// Bodies are disjoint (validated), so the membership is single-valued.
	arcs map[string]feedbackArc
	// bodies maps declarer id -> body ids in declared graph order.
	bodies map[string][]string
	// bodyInDegree maps declarer id -> (body node id -> count of IN-BODY
	// parents): the in-degrees the launch loop re-seeds when the arc fires.
	// Out-of-body parents are deliberately absent — they ran once, their
	// artifacts are stable, and they stay satisfied across rounds.
	bodyInDegree map[string]map[string]int
}

// feedbackArc is one declared arc, denormalized for the runtime.
type feedbackArc struct {
	declarer string
	target   string
	max      int
}

// newFeedbackState precomputes every arc's body from the validated graph.
// initialRounds carries a resumed leg's marker positions (nil for a fresh
// run).
func newFeedbackState(g *graph.Graph, initialRounds map[string]int) *feedbackState {
	st := &feedbackState{
		rounds:       make(map[string]int, len(initialRounds)),
		arcs:         make(map[string]feedbackArc),
		bodies:       make(map[string][]string),
		bodyInDegree: make(map[string]map[string]int),
	}
	for id, round := range initialRounds {
		st.rounds[id] = round
	}
	for _, n := range g.Nodes {
		if n.Feedback == nil {
			continue
		}
		arc := feedbackArc{declarer: n.ID, target: n.Feedback.Rerun, max: n.Feedback.Max}
		body := g.FeedbackBody(n.ID)
		inBody := make(map[string]bool, len(body))
		for _, id := range body {
			inBody[id] = true
		}
		degrees := make(map[string]int, len(body))
		for _, id := range body {
			member, _ := g.NodeByID(id)
			count := 0
			for _, parent := range member.DependsOn {
				if inBody[parent] {
					count++
				}
			}
			degrees[id] = count
			st.arcs[id] = arc
		}
		st.bodies[n.ID] = body
		st.bodyInDegree[n.ID] = degrees
	}
	return st
}

// roundOf returns id's current feedback round (0 outside any loop or on the
// initial pass) — the value every event and snapshot record for id is
// stamped with.
func (st *feedbackState) roundOf(id string) int {
	if st == nil {
		return 0
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.rounds[id]
}

// roundNote renders the "feedback round k/N" ledger note for a body node's
// terminal row mid-loop, or "" outside a round. The declarer's own rows are
// excluded: its fire row and exhaustion detail carry their own, richer
// wording, and doubling it would be noise.
func (st *feedbackState) roundNote(id string) string {
	if st == nil {
		return ""
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	arc, ok := st.arcs[id]
	round := st.rounds[id]
	if !ok || round == 0 || arc.declarer == id {
		return ""
	}
	return fmt.Sprintf("feedback round %d/%d", round, arc.max)
}

// fire advances declarer's arc one round: every body node's round becomes
// spent+1, and the new round is returned. The caller has already decided the
// arc may fire (judgment cause, rounds remaining).
func (st *feedbackState) fire(declarer string) int {
	st.mu.Lock()
	defer st.mu.Unlock()
	round := st.rounds[declarer] + 1
	for _, id := range st.bodies[declarer] {
		st.rounds[id] = round
	}
	return round
}

// bodyInDegrees returns declarer's re-arming in-degrees — the precomputed
// map, so callers must not mutate it.
func (st *feedbackState) bodyInDegrees(declarer string) map[string]int {
	return st.bodyInDegree[declarer]
}

// judgmentCauses is the fixed built-in trigger set: the causes that mean
// "the node ran and judged the work insufficient", which re-running the body
// can plausibly repair. It is not configuration — it is the definition of
// what a feedback edge is for (ADR 0010). Everything else that reaches
// recordFail (an interpolation error, a spawn error, budget_exceeded) is an
// infrastructure or spend fault a re-run cannot fix, and fails final
// immediately, arc or no arc.
func isJudgmentCause(cause string) bool {
	switch cause {
	case graph.CauseVerifyFailed, graph.CauseResultMismatch, graph.CauseNonzeroExit:
		return true
	}
	return false
}

// isJudgmentFailure lifts isJudgmentCause from tokens to the failure itself,
// because the verify_failed token alone is not enough: a verification that
// could not be completed — its command's interpolation failed, its process
// could not spawn, it timed out — fails the node under that same token (for
// retry) but rendered no verdict on the work, and firing the arc on it would
// spend a whole body re-run on a fault the re-run cannot repair. Those
// failures are marked NodeCheckError.Infrastructure at the verify seam
// (verifyFault) and excluded here.
func isJudgmentFailure(cause error) bool {
	var checkErr *NodeCheckError
	if asErr(cause, &checkErr) && checkErr.Infrastructure {
		return false
	}
	return isJudgmentCause(causeFromCheck(cause))
}

// judgeFeedback is consulted exactly where recordFail would otherwise be
// reached with a verdict failure — after the declarer's own retries are
// spent — and decides what the failure means for the node's feedback arc:
//
//   - no arc, or a non-judgment cause: (nil, cause) — the failure proceeds
//     unchanged;
//   - rounds remaining: the arc FIRES — persist the payload, advance the
//     round, write the non-final trio (progress line, ledger row, marker
//     record), emit node_retried — and the *feedbackSignal comes back for
//     Run's launch loop to intercept;
//   - rounds exhausted: (nil, wrapped cause) naming the spend, so the
//     terminal FAIL's detail reads "feedback exhausted after k rounds of
//     target → declarer: <cause>".
//
// A payload-persist failure is an infrastructure fault: the re-run would
// silently miss its feedback, so the node fails final instead, like any
// PersistOutput failure.
func (s *Scheduler) judgeFeedback(node graph.Node, outcome runner.NodeOutcome, h *handoff.Handoff, led *ledger.RunLedger, duration time.Duration, cause error) (*feedbackSignal, error) {
	f := node.Feedback
	if f == nil || !isJudgmentFailure(cause) {
		return nil, cause
	}

	spent := s.feedback.roundOf(node.ID)
	if spent >= f.Max {
		roundWord := "rounds"
		if spent == 1 {
			roundWord = "round"
		}
		return nil, fmt.Errorf("feedback exhausted after %d %s of %s → %s: %w",
			spent, roundWord, f.Rerun, node.ID, cause)
	}

	// The payload is the declarer's own account of what is wrong: its result
	// text when the failing execution produced one, else its failure detail.
	payload := outcome.Result
	if strings.TrimSpace(payload) == "" {
		payload = cause.Error()
	}
	if err := h.SetFeedback(node.ID, payload); err != nil {
		return nil, fmt.Errorf("feedback arc could not fire: %w", err)
	}

	round := s.feedback.fire(node.ID)
	s.logProgress("↻ %s  feedback round %d/%d — re-running %s → %s\n", node.ID, round, f.Max, f.Rerun, node.ID)

	// The non-final trio (ADR 0010): a ledger row pricing this failing
	// execution, a non-terminal MARKER in the snapshot (round k, no verdict
	// — a runstate FAIL here would settle the declarer and collapse the
	// loop on resume), and node_retried on the stream — never node_failed,
	// which stays terminal.
	rec := failRecord(node, outcome.SessionID, outcome.TotalCostUSD, duration, cause)
	rec.Detail = capDetail(fmt.Sprintf("feedback round %d/%d: %v", round, f.Max, cause))
	led.Record(rec)

	artifactPath, _ := h.ArtifactPath(node.ID)
	marker := runstate.NodeRecord{
		SessionID:    rec.SessionID,
		CostUSD:      rec.CostUSD,
		BudgetUSD:    rec.BudgetUSD,
		Duration:     rec.Duration,
		ArtifactPath: artifactPath,
		Detail:       rec.Detail,
		Round:        round,
	}
	if err := s.recorder.RecordNode(node.ID, marker); err != nil {
		s.logProgress("⚠ %s  snapshot write failed: %v\n", node.ID, err)
	}

	s.emitEvent(runfeed.Event{
		Type:   runfeed.EventNodeRetried,
		NodeID: node.ID,
		Round:  round,
		Detail: capDetail(fmt.Sprintf("feedback round %d/%d: re-running %s → %s", round, f.Max, f.Rerun, node.ID)),
	})

	return &feedbackSignal{NodeID: node.ID, Target: f.Rerun, Round: round}, nil
}
