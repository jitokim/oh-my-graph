package coordinator

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/runner"
)

// scriptedRunner answers a fixed sequence and counts how many times it was
// asked. FakeRunner keys on the prompt and so cannot express "fail, then
// succeed" for one caller; this can, and counting is the whole assertion here.
//
// Every assertion below states the call count explicitly rather than checking
// that some record is absent — an absence would be satisfied by the assessor
// never running at all, which is the bug's own failure mode.
type scriptedRunner struct {
	replies []struct {
		outcome runner.NodeOutcome
		err     error
	}
	calls int
}

func (s *scriptedRunner) Run(context.Context, runner.NodeInvocation) (runner.NodeOutcome, error) {
	i := s.calls
	s.calls++
	if i >= len(s.replies) {
		i = len(s.replies) - 1
	}
	return s.replies[i].outcome, s.replies[i].err
}

func spawnFailure() error {
	return &runner.NodeSpawnError{Runtime: "claude", Err: exec.ErrNotFound}
}

func metReply() runner.NodeOutcome {
	return runner.NodeOutcome{Result: assessMetReply, TotalCostUSD: 0.01}
}

func scripted(t *testing.T, replies ...struct {
	outcome runner.NodeOutcome
	err     error
}) *scriptedRunner {
	t.Helper()
	if len(replies) == 0 {
		t.Fatal("a scripted runner with no replies would answer nothing")
	}
	return &scriptedRunner{replies: replies}
}

type reply = struct {
	outcome runner.NodeOutcome
	err     error
}

// A spawn failure means the CLI never started, so no verdict was formed and
// nothing was billed. Asking again asks for the first time — the case that
// named this: an npm update replaced `claude` on PATH mid-run and threw away a
// cycle whose five nodes had already finished (#214).
func TestAssess_RetriesASpawnFailureAndSucceeds(t *testing.T) {
	restore := shortenSpawnRetryDelay(t)
	defer restore()

	r := scripted(t,
		reply{err: spawnFailure()},
		reply{err: spawnFailure()},
		reply{outcome: metReply()},
	)

	got, err := New(r).Assess(context.Background(), "ship it", CycleEvidence{RunID: "r1", RunPassed: true})
	if err != nil {
		t.Fatalf("Assess after two recoverable spawn failures: %v", err)
	}
	if !got.GoalMet {
		t.Errorf("GoalMet = false, want true — the third attempt's reply said the goal was met")
	}
	if r.calls != 3 {
		t.Errorf("assessor was launched %d time(s), want exactly 3 (two failures then the answer)", r.calls)
	}
}

// The bound is a bound. A machine with no CLI installed fails every attempt and
// must report that, not spin.
func TestAssess_SpawnFailureIsBounded(t *testing.T) {
	restore := shortenSpawnRetryDelay(t)
	defer restore()

	r := scripted(t, reply{err: spawnFailure()})

	_, err := New(r).Assess(context.Background(), "ship it", CycleEvidence{RunID: "r1", RunPassed: true})
	if err == nil {
		t.Fatal("Assess returned nil error when every spawn failed")
	}
	var spawnErr *runner.NodeSpawnError
	if !errors.As(err, &spawnErr) {
		t.Errorf("err = %v, want it to still carry *runner.NodeSpawnError", err)
	}
	if r.calls != assessorSpawnAttempts {
		t.Errorf("assessor was launched %d time(s), want exactly assessorSpawnAttempts (%d)", r.calls, assessorSpawnAttempts)
	}
}

// THE load-bearing test. A reply failure is not a spawn failure: the model was
// reached and it answered. Retrying that would be re-rolling a verdict until
// the loop liked one, which is exactly the quiet-spend behaviour ADR 0011
// exists to make unrepresentable.
func TestAssess_DoesNotRetryAnythingButASpawnFailure(t *testing.T) {
	restore := shortenSpawnRetryDelay(t)
	defer restore()

	for _, tc := range []struct {
		name  string
		first reply
	}{
		{"an unparseable reply", reply{outcome: runner.NodeOutcome{Result: "I think it went well?"}}},
		{"a non-zero exit", reply{outcome: runner.NodeOutcome{ExitCode: 1, Result: "boom"}}},
		{"a timeout", reply{err: &runner.NodeTimeoutError{Runtime: "claude", Timeout: time.Second}}},
		{"an ordinary run error", reply{err: errors.New("something else went wrong")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A second reply that WOULD succeed: if the code retried, the test
			// would pass with goal_met and one extra call, so the count below
			// is what catches it.
			r := scripted(t, tc.first, reply{outcome: metReply()})

			_, err := New(r).Assess(context.Background(), "ship it", CycleEvidence{RunID: "r1", RunPassed: true})
			if err == nil {
				t.Fatalf("Assess returned nil error for %s — it must stop the loop", tc.name)
			}
			if r.calls != 1 {
				t.Errorf("assessor was launched %d time(s) for %s, want exactly 1: the model answered, so there is nothing to ask again", r.calls, tc.name)
			}
		})
	}
}

// A caller that gave up must not wait out the remaining attempts.
func TestAssess_CancelledContextOutranksTheSpawnBound(t *testing.T) {
	r := scripted(t, reply{err: spawnFailure()})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		_, _ = New(r).Assess(ctx, "ship it", CycleEvidence{RunID: "r1", RunPassed: true})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Assess did not return promptly on a cancelled context — it appears to be sleeping between spawn attempts")
	}
	if r.calls < 1 {
		t.Errorf("assessor was launched %d time(s), want at least 1: cancellation stops the RETRY, not the first attempt", r.calls)
	}
	if r.calls >= assessorSpawnAttempts {
		t.Errorf("assessor was launched %d time(s) on a cancelled context, want fewer than the full bound (%d)", r.calls, assessorSpawnAttempts)
	}
}

// shortenSpawnRetryDelay keeps these tests fast without making the delay a
// parameter of the production call. The double's sync is scripted, never a
// wall-clock arm — this only shrinks a real sleep that has nothing to
// synchronize with.
func shortenSpawnRetryDelay(t *testing.T) func() {
	t.Helper()
	previous := assessorSpawnRetryDelay
	assessorSpawnRetryDelay = time.Millisecond
	return func() { assessorSpawnRetryDelay = previous }
}

// The planner shares the assessor's spawn retry since 2026-08-21. #214's
// scoping note said not to generalise across the three coordinator call classes
// without evidence; the evidence is a second occurrence, in the planner, that
// killed a whole lane before any node ran.
//
// The chat router deliberately does NOT share it: a router call is interactive,
// and a human watching it can ask again.
func TestPlan_RetriesASpawnFailureAndSucceeds(t *testing.T) {
	restore := shortenSpawnRetryDelay(t)
	defer restore()

	r := scripted(t,
		reply{err: spawnFailure()},
		reply{outcome: runner.NodeOutcome{Result: validSpec, TotalCostUSD: 0.01}},
	)

	_, err := New(r).Plan(context.Background(), "ship it", []string{"repo"})
	if err != nil {
		t.Fatalf("Plan after one recoverable spawn failure: %v", err)
	}
	if r.calls != 2 {
		t.Errorf("planner was launched %d time(s), want exactly 2 (one failure then the plan)", r.calls)
	}
}
