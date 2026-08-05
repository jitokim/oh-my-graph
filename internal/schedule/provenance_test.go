package schedule

import (
	"context"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/gate"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/verify"
)

// Provenance (ADR 0016 §6) is the half of the #119 fix that needs no new input
// from the user and no cooperation from the model: nothing can force a planned
// node to build, but the engine always knows which predicates it evaluated, so
// it can stop recording a self-report and a measurement as the same word.
//
// Had it shipped before v0.4.1, the reporter's feed would have said
// `self-reported` on the check node that certified a branch that did not
// compile, next to `self-reported` on the $11.01 node it was checking — not a
// fixed build, but the difference between a tool that was wrong and one that
// lied.

// passProvenanceOf runs a one-node graph and returns the provenance its
// node_passed carried.
func passProvenanceOf(t *testing.T, yaml string, opts Options, outcome runner.NodeOutcome) string {
	t.Helper()
	g := mustGraph(t, yaml)
	feed, path := newEventStream(t, "run-provenance")
	opts.EventSink = feed
	s, h, led := newHarness(t, runner.NewFakeRunner(map[string]runner.NodeOutcome{"work": outcome}), opts)

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if err := feed.Close(); err != nil {
		t.Fatalf("close feed: %v", err)
	}

	for _, e := range readEventStream(t, path) {
		if e.Type == runfeed.EventNodePassed {
			return e.Provenance
		}
	}
	t.Fatal("the run emitted no node_passed event")
	return ""
}

// TestScheduler_PassProvenanceNamesTheStrongestPredicate walks the closed set.
// Each case is a real run through the scheduler, so a provenance that stopped
// matching what actually ran would fail here rather than in a unit test of the
// derivation alone.
func TestScheduler_PassProvenanceNamesTheStrongestPredicate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		yaml    string
		opts    Options
		outcome runner.NodeOutcome
		want    string
	}{
		{
			// The engine ran a command and judged its exit code. This is what
			// --verify-cmd buys, and the only qualifier that means anything
			// outside the model's narration was consulted.
			name: "verified",
			yaml: "name: p\nnodes:\n  - { id: work, prompt: work, success_check: { verify: { command: build } } }\n",
			opts: Options{Verifier: verify.NewFakeVerifier(map[string]verify.Result{
				"build": {ExitCode: 0, Output: "ok\n"},
			})},
			outcome: pass("s1", 0.5),
			want:    runfeed.ProvenanceVerified,
		},
		{
			// #119's shape exactly: the node passed by emitting the right
			// word. A verification and a result_matches together are
			// `verified` — the check that judged observed facts is the
			// stronger one — so this case must carry result_matches ALONE for
			// the ordering to mean anything.
			name:    "self-reported",
			yaml:    "name: p\nnodes:\n  - { id: work, prompt: work, success_check: { result_matches: \"^PASS$\" } }\n",
			outcome: pass("s1", 11.01),
			want:    runfeed.ProvenanceSelfReported,
		},
		{
			name:    "exit-only",
			yaml:    "name: p\nnodes:\n  - { id: work, prompt: work }\n",
			outcome: pass("s1", 0.5),
			want:    runfeed.ProvenanceExitOnly,
		},
		{
			// The fourth member is what closes the set. A gate spawns no
			// subprocess and evaluates no predicate, so under a three-member
			// set it would land on `exit-only` — wrong twice over: nothing
			// ran, and a person deciding is the strongest provenance the
			// system has, not the weakest.
			name:    "approved",
			yaml:    "name: p\nnodes:\n  - { id: work, prompt: work, type: gate }\n",
			opts:    Options{Gate: gate.NewRecordedController(map[string]gate.Decision{"work": gate.DecisionApprove})},
			outcome: pass("s1", 0),
			want:    runfeed.ProvenanceApproved,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := passProvenanceOf(t, tc.yaml, tc.opts, tc.outcome); got != tc.want {
				t.Errorf("provenance = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestScheduler_VerifiedBeatsSelfReported is the ordering, asserted where it
// matters: the auto-mode shape after ADR 0016 is a check node that already had
// a result_matches and now ALSO carries the injected command. If
// result_matches won, every #119-shaped node would keep reading
// `self-reported` even once the engine started gathering evidence, and the
// qualifier would say nothing about the feature that fixed it.
func TestScheduler_VerifiedBeatsSelfReported(t *testing.T) {
	yaml := "name: p\nnodes:\n" +
		"  - { id: work, prompt: work, success_check: { result_matches: \"^PASS$\", verify: { command: build } } }\n"
	opts := Options{Verifier: verify.NewFakeVerifier(map[string]verify.Result{"build": {ExitCode: 0}})}

	if got := passProvenanceOf(t, yaml, opts, pass("s1", 0.5)); got != runfeed.ProvenanceVerified {
		t.Errorf("provenance = %q, want %q: the predicate that judged observed facts is the stronger one",
			got, runfeed.ProvenanceVerified)
	}
}

// TestScheduler_FailedNodesCarryNoProvenance — the qualifier qualifies a PASS.
// A FAIL already carries its cause in Detail, and stamping a strength word on
// a failure would invite reading it as "how sure we are it failed".
func TestScheduler_FailedNodesCarryNoProvenance(t *testing.T) {
	g := mustGraph(t, "name: p\nnodes:\n  - { id: work, prompt: work, success_check: { exit_zero: true } }\n")
	feed, path := newEventStream(t, "run-provenance-fail")
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"work": {ExitCode: 1, Result: "nope"}})
	s, h, led := newHarness(t, fake, Options{EventSink: feed})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the run to fail")
	}
	if err := feed.Close(); err != nil {
		t.Fatalf("close feed: %v", err)
	}

	saw := false
	for _, e := range readEventStream(t, path) {
		if e.Type != runfeed.EventNodeFailed {
			continue
		}
		saw = true
		if e.Provenance != "" {
			t.Errorf("node_failed carries provenance %q", e.Provenance)
		}
	}
	if !saw {
		t.Fatal("the run emitted no node_failed event, so this asserted nothing")
	}
}
