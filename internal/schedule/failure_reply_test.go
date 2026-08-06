package schedule

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/handoff"
	"github.com/jitokim/oh-my-graph/internal/ledger"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// TestScheduler_FailedNodeKeepsItsOwnReply is the point of the whole thing.
// The engine's account of this failure is "result did not match /^SHIP IT/" —
// not one byte of what the node actually worked out. That analysis was paid
// for; after the run it is still on disk and the progress feed says where.
func TestScheduler_FailedNodeKeepsItsOwnReply(t *testing.T) {
	g := mustGraph(t, `
name: keeps-reply
nodes:
  - id: verify
    prompt: verify
    success_check: { result_matches: "^SHIP IT" }
`)
	diagnosis := "the lock is pre-flock, so ProbeLock folds unmarked into LivenessUnknown"
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"verify": {Result: diagnosis, ExitCode: 0, SessionID: "s-verify", TotalCostUSD: 1.36},
	})
	runDir := t.TempDir()
	h := handoff.New(runDir, nil)
	led := ledger.New("test")
	var progress bytes.Buffer
	s := NewScheduler(fake, Options{ProgressWriter: &progress})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the node to fail its success_check")
	}

	path := handoff.FailedOutputPath(runDir, "verify")
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the failed node's reply was not kept: %v", err)
	}
	if string(onDisk) != diagnosis {
		t.Fatalf("kept reply = %q, want the node's own words %q", onDisk, diagnosis)
	}

	// The engine's own account really does carry none of it — which is why the
	// file has to exist.
	rec, ok := findRecord(led, "verify")
	if !ok {
		t.Fatal("verify was never recorded in the ledger")
	}
	if strings.Contains(rec.Detail, diagnosis) {
		t.Fatalf("this test is no longer testing anything: the ledger detail already holds the reply (%q)", rec.Detail)
	}

	// A human reading the run has to be told where it went.
	if !strings.Contains(progress.String(), path) {
		t.Errorf("progress feed never points at the kept reply:\n%s", progress.String())
	}
}

// TestScheduler_KeptReplyIsNotAnArtifact: a failure record must never be
// confusable with a success artifact. The reply is NOT at the flat path
// {{ artifacts.<id> }} resolves to and a dependent still cannot interpolate
// it — a node that failed produced no artifact, and keeping its words must
// not quietly promote it to one.
func TestScheduler_KeptReplyIsNotAnArtifact(t *testing.T) {
	g := mustGraph(t, `
name: not-an-artifact
nodes:
  - id: dev
    prompt: dev
    success_check: { result_matches: "^SHIP IT" }
  - { id: child, prompt: "child: {{ artifacts.dev | inline }}", depends_on: [dev] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"dev": {Result: "here is what I found", ExitCode: 0, SessionID: "s-dev"},
	})
	runDir := t.TempDir()
	h := handoff.New(runDir, nil)
	led := ledger.New("test")
	s := NewScheduler(fake, Options{ProgressWriter: io.Discard})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the failing node to fail the run")
	}

	if _, err := os.ReadFile(handoff.FailedOutputPath(runDir, "dev")); err != nil {
		t.Fatalf("the failed node's reply was not kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runDir, "dev.out")); !os.IsNotExist(err) {
		t.Errorf("a failed reply appeared at the artifact path (stat err = %v)", err)
	}
	if _, err := h.Interpolate("{{ artifacts.dev }}"); err == nil {
		t.Error("{{ artifacts.dev }} resolved for a node that FAILED")
	}
	if indexOf(fake.Calls(), "child: here is what I found") != -1 {
		t.Errorf("a dependent consumed the failed reply as an artifact; calls=%v", fake.Calls())
	}
}

// TestScheduler_PassingNodeKeepsNoFailedReply: the file means FAILED. A node
// that passed must leave nothing under failed/, or the directory stops being
// a statement about anything.
func TestScheduler_PassingNodeKeepsNoFailedReply(t *testing.T) {
	g := mustGraph(t, `
name: passing
nodes:
  - { id: dev, prompt: dev }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"dev": pass("s-dev", 0.02)})
	runDir := t.TempDir()
	h := handoff.New(runDir, nil)
	led := ledger.New("test")
	s := NewScheduler(fake, Options{ProgressWriter: io.Discard})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(runDir, "dev.out")); err != nil {
		t.Fatalf("a passing node must still leave its artifact: %v", err)
	}
	if _, err := os.Stat(handoff.FailedOutputPath(runDir, "dev")); !os.IsNotExist(err) {
		t.Errorf("a passing node left a failed reply behind (stat err = %v)", err)
	}
}

// TestScheduler_PassingRetryLegDropsTheStaleFailedReply is the same claim
// across the process boundary: a node that failed in an earlier leg and passed
// in this one must not leave the losing reply beside the winning artifact.
// Otherwise the run directory holds failed/dev.out saying one thing and
// state.json saying PASS, and failed/ stops being a statement about anything.
func TestScheduler_PassingRetryLegDropsTheStaleFailedReply(t *testing.T) {
	g := mustGraph(t, `
name: retry-leg
nodes:
  - { id: dev, prompt: dev }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"dev": pass("s-dev", 0.02)})
	runDir := t.TempDir()
	h := handoff.New(runDir, nil)
	stale := handoff.FailedOutputPath(runDir, "dev")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatalf("stage failed/: %v", err)
	}
	if err := os.WriteFile(stale, []byte("LEG-ONE-WRONG-ANSWER"), 0o644); err != nil {
		t.Fatalf("stage the previous leg's reply: %v", err)
	}
	led := ledger.New("test")
	s := NewScheduler(fake, Options{ProgressWriter: io.Discard})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("the previous leg's failed reply outlived the PASS that superseded it (stat err = %v)", err)
	}
	onDisk, err := os.ReadFile(filepath.Join(runDir, "dev.out"))
	if err != nil || string(onDisk) != "PASS" {
		t.Errorf("the passing artifact = %q, %v; dropping the stale reply must not touch it", onDisk, err)
	}
}

// TestScheduler_SpawnFailureHasNoReplyToKeep: a node whose subprocess never
// produced an outcome said nothing, so there is nothing to keep and no file to
// mislead a reader into thinking there was.
func TestScheduler_SpawnFailureHasNoReplyToKeep(t *testing.T) {
	g := mustGraph(t, `
name: spawn-failure
nodes:
  - { id: dev, prompt: dev }
`)
	fake := runner.NewFakeRunner(nil)
	fake.InjectError("dev", errors.New("claude run: exec: no such file"))
	runDir := t.TempDir()
	h := handoff.New(runDir, nil)
	led := ledger.New("test")
	s := NewScheduler(fake, Options{ProgressWriter: io.Discard})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the spawn failure to fail the run")
	}

	if _, err := os.Stat(handoff.FailedOutputPath(runDir, "dev")); !os.IsNotExist(err) {
		t.Errorf("a node that never replied left a reply file (stat err = %v)", err)
	}
}

// TestScheduler_LosingTheReplyDoesNotChangeTheVerdict: keeping the reply is
// best-effort by design. The node has already failed; a filesystem that will
// not take the copy must not rewrite why it failed or invent a second failure.
// The unwritable path is forced by putting a regular FILE where failed/ needs
// to be a directory.
func TestScheduler_LosingTheReplyDoesNotChangeTheVerdict(t *testing.T) {
	g := mustGraph(t, `
name: unwritable
nodes:
  - id: dev
    prompt: dev
    success_check: { result_matches: "^SHIP IT" }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"dev": {Result: "a reply that cannot be saved", ExitCode: 0, SessionID: "s-dev"},
	})
	runDir := t.TempDir()
	blocker := filepath.Dir(handoff.FailedOutputPath(runDir, "dev"))
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("stage the unwritable path: %v", err)
	}
	h := handoff.New(runDir, nil)
	led := ledger.New("test")
	var progress bytes.Buffer
	s := NewScheduler(fake, Options{ProgressWriter: &progress})

	err := s.Run(context.Background(), g, h, led)

	var checkErr *NodeCheckError
	if !errors.As(err, &checkErr) || checkErr.Predicate != "result_matches" {
		t.Fatalf("the verdict changed when the reply could not be kept: %T: %v", err, err)
	}
	rec, ok := findRecord(led, "dev")
	if !ok {
		t.Fatal("dev was never recorded in the ledger")
	}
	if rec.Verdict != ledger.VerdictFail || !strings.Contains(rec.Detail, "result did not match") {
		t.Errorf("ledger row = %+v, want the FAIL the success_check produced", rec)
	}
	if !strings.Contains(progress.String(), "not saved") {
		t.Errorf("a lost reply must be said out loud on the progress feed:\n%s", progress.String())
	}
}

// TestScheduler_RetriedNodeKeepsTheFinalReply: retries are attempts at the
// same node, and the one that ended the node is the one worth reading. Each
// attempt overwrites the last, so the file never accumulates and never holds
// a superseded attempt.
func TestScheduler_RetriedNodeKeepsTheFinalReply(t *testing.T) {
	g := mustGraph(t, `
name: retried
nodes:
  - id: dev
    prompt: dev
    retry: { max: 1, on: [result_mismatch] }
    success_check: { result_matches: "^SHIP IT" }
`)
	seq := &sequenceRunner{outcomes: []runner.NodeOutcome{
		{Result: "first attempt: I misread the lock file", ExitCode: 0, SessionID: "s-1"},
		{Result: "second attempt: the lock predates flock", ExitCode: 0, SessionID: "s-2"},
	}}
	runDir := t.TempDir()
	h := handoff.New(runDir, nil)
	led := ledger.New("test")
	s := NewScheduler(seq, Options{ProgressWriter: io.Discard})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the node to fail after its retry")
	}

	onDisk, err := os.ReadFile(handoff.FailedOutputPath(runDir, "dev"))
	if err != nil {
		t.Fatalf("the failed node's reply was not kept: %v", err)
	}
	if string(onDisk) != "second attempt: the lock predates flock" {
		t.Fatalf("kept reply = %q, want the attempt that ended the node", onDisk)
	}
}

// TestScheduler_ExhaustedFeedbackDeclarerKeepsItsFinalReply: a declarer whose
// rounds run out fails like any other node, and the round that gave up is the
// one a human needs. The per-round payload under feedback/ is a different
// file with a different job (it is what the BODY was told), and both survive.
func TestScheduler_ExhaustedFeedbackDeclarerKeepsItsFinalReply(t *testing.T) {
	g := mustGraph(t, `
name: exhausted
nodes:
  - { id: impl, prompt: "impl: {{ feedback.review }}" }
  - id: review
    prompt: "review: {{ artifacts.impl | inline }}"
    depends_on: [impl]
    success_check: { result_matches: "ship it" }
    feedback: { rerun: impl, max: 1 }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"impl: ":                         result("draft-v1", 0.10),
		"review: draft-v1":               result("round 1: rename the flag", 0.20),
		"impl: round 1: rename the flag": result("draft-v2", 0.30),
		"review: draft-v2":               result("round 2: still wrong, and here is why", 0.40),
	})
	runDir := t.TempDir()
	h := handoff.New(runDir, nil)
	led := ledger.New("test")
	s := NewScheduler(fake, Options{ProgressWriter: io.Discard})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the exhausted declarer to fail the run")
	}

	kept, err := os.ReadFile(handoff.FailedOutputPath(runDir, "review"))
	if err != nil {
		t.Fatalf("the exhausted declarer's reply was not kept: %v", err)
	}
	if string(kept) != "round 2: still wrong, and here is why" {
		t.Fatalf("kept reply = %q, want the final round's", kept)
	}
	payload, err := os.ReadFile(filepath.Join(runDir, "feedback", "review.out"))
	if err != nil {
		t.Fatalf("the feedback payload was lost: %v", err)
	}
	if string(payload) != "round 1: rename the flag" {
		t.Fatalf("feedback payload = %q, want what the body was actually told", payload)
	}
}
