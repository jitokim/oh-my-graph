package schedule

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/handoff"
	"github.com/jitokim/oh-my-graph/internal/ledger"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/verify"
)

// recordingSequenceRunner is sequenceRunner with the prompts kept: these tests
// are entirely about what the Nth attempt was ASKED, which the outcome-only
// fakes throw away.
type recordingSequenceRunner struct {
	outcomes []runner.NodeOutcome
	err      error // returned instead of an outcome on every attempt in errAt
	errAt    map[int]bool

	prompts []string
	resumes []string
}

func (r *recordingSequenceRunner) Run(_ context.Context, spec runner.NodeInvocation) (runner.NodeOutcome, error) {
	i := len(r.prompts)
	r.prompts = append(r.prompts, spec.Prompt)
	r.resumes = append(r.resumes, spec.ResumeSession)
	if r.errAt[i] {
		return runner.NodeOutcome{}, r.err
	}
	if i >= len(r.outcomes) {
		i = len(r.outcomes) - 1
	}
	return r.outcomes[i], nil
}

// fenceNonceOfQuote pulls the nonce out of a retry prompt's opening marker, so
// a test can check the CLOSING marker carries the same one — the property that
// makes the fence unforgeable is that both markers move together.
func fenceNonceOfQuote(t *testing.T, prompt string) string {
	t.Helper()
	m := regexp.MustCompile(`--- previous attempt ([0-9a-f]+) \(`).FindStringSubmatch(prompt)
	if m == nil {
		t.Fatalf("prompt carries no fenced previous attempt:\n%s", prompt)
	}
	return m[1]
}

// TestRetry_CarriesThePreviousAttemptFencedAndUnquotedCheck is the feature in
// one test: the second attempt's prompt still opens with the node's own prompt,
// then quotes the FIRST attempt's reply between markers that both carry a nonce
// the reply could not have contained — and never names the predicate that
// rejected it.
func TestRetry_CarriesThePreviousAttemptFencedAndUnquotedCheck(t *testing.T) {
	g := mustGraph(t, `
name: retry-quote
nodes:
  - id: work
    prompt: do the work
    success_check: { result_matches: "SHIBBOLETH" }
    retry: { max: 1, on: [result_mismatch] }
`)
	rec := &recordingSequenceRunner{outcomes: []runner.NodeOutcome{
		{Result: "my first answer, which was wrong", ExitCode: 0},
	}}
	s, h, led := newHarness(t, rec, Options{})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the run to fail once the retry also missed the check")
	}
	if len(rec.prompts) != 2 {
		t.Fatalf("attempts = %d, want 2 (initial + one retry)", len(rec.prompts))
	}

	first, second := rec.prompts[0], rec.prompts[1]
	if first != "do the work" {
		t.Errorf("first attempt's prompt = %q, want the node's own prompt untouched", first)
	}
	if !strings.HasPrefix(second, "do the work") {
		t.Errorf("retry prompt does not start with the node's own prompt:\n%s", second)
	}
	if !strings.Contains(second, "my first answer, which was wrong") {
		t.Errorf("retry prompt does not quote the previous attempt's reply:\n%s", second)
	}

	nonce := fenceNonceOfQuote(t, second)
	if got := strings.Count(second, nonce); got != 3 {
		t.Errorf("nonce %q appears %d time(s), want 3 — the explanation plus BOTH markers; "+
			"a closing marker without it is a fence the quoted reply can forge its way out of", nonce, got)
	}
	if !strings.Contains(second, "--- end previous attempt "+nonce+" ---") {
		t.Errorf("retry prompt has no nonce-carrying closing marker:\n%s", second)
	}

	// The steelman this design answers: quoting the predicate back teaches the
	// cheapest pass, which is to print whatever it matches.
	if strings.Contains(second, "SHIBBOLETH") {
		t.Errorf("retry prompt names the success check's own expression — that teaches the node to "+
			"satisfy the assertion instead of doing the work:\n%s", second)
	}
	if strings.Contains(second, "result_matches") {
		t.Errorf("retry prompt names the check predicate:\n%s", second)
	}
}

// TestRetry_QuotesOnlyTheImmediatelyPrecedingAttempt pins the bound. Three
// attempts run; the third carries the second's reply and NOT the first's, so
// the quoted material stays flat in the attempt index instead of growing with
// it.
func TestRetry_QuotesOnlyTheImmediatelyPrecedingAttempt(t *testing.T) {
	if priorAttemptsInPrompt != 1 {
		t.Fatalf("priorAttemptsInPrompt = %d; this test pins the bound at 1 and would have to be "+
			"rewritten — deliberately, with the cost argument in retryfeedback.go re-made", priorAttemptsInPrompt)
	}
	g := mustGraph(t, `
name: retry-bound
nodes:
  - id: work
    prompt: do the work
    success_check: { result_matches: "PASS" }
    retry: { max: 2, on: [result_mismatch] }
`)
	rec := &recordingSequenceRunner{outcomes: []runner.NodeOutcome{
		{Result: "ATTEMPT-ONE-REPLY", ExitCode: 0},
		{Result: "ATTEMPT-TWO-REPLY", ExitCode: 0},
		{Result: "ATTEMPT-THREE-REPLY", ExitCode: 0},
	}}
	s, h, led := newHarness(t, rec, Options{})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the run to fail after both retries missed the check")
	}
	if len(rec.prompts) != 3 {
		t.Fatalf("attempts = %d, want 3 (initial + two retries)", len(rec.prompts))
	}

	third := rec.prompts[2]
	if !strings.Contains(third, "ATTEMPT-TWO-REPLY") {
		t.Errorf("third attempt does not quote the second's reply:\n%s", third)
	}
	if strings.Contains(third, "ATTEMPT-ONE-REPLY") {
		t.Errorf("third attempt still carries the FIRST attempt's reply — the quote is accumulating, "+
			"which makes a node's prompt grow with its own retry bound:\n%s", third)
	}
	if got := strings.Count(third, "--- previous attempt "); got != 1 {
		t.Errorf("third attempt carries %d fenced quotes, want exactly 1", got)
	}
	// Each attempt fences with its own nonce: a nonce reused across attempts is
	// one the previous attempt's reply has already seen and could echo.
	if a, b := fenceNonceOfQuote(t, rec.prompts[1]), fenceNonceOfQuote(t, third); a == b {
		t.Errorf("both retries fenced with nonce %q; each call must mint its own", a)
	}
}

// TestRetry_BoundsTheQuotedReply proves the prompt's own cap applies, and that
// the cut is announced rather than silent.
func TestRetry_BoundsTheQuotedReply(t *testing.T) {
	// The number itself, not just the code's agreement with itself: DESIGN.md,
	// ADR 0020 §3 and the CHANGELOG all publish 8000 bytes as a cost promise,
	// and every other assertion here derives BOTH its input and its ceiling
	// from the constant, so raising it could never fail one of them.
	if maxPriorReplyInPrompt != 8000 {
		t.Fatalf("maxPriorReplyInPrompt = %d, want 8000 — the bound is published as a per-attempt cost "+
			"promise; moving it means moving DESIGN.md, ADR 0020 §3 and the CHANGELOG with it",
			maxPriorReplyInPrompt)
	}
	huge := strings.Repeat("x", maxPriorReplyInPrompt*3)
	g := mustGraph(t, `
name: retry-bound-bytes
nodes:
  - id: work
    prompt: do the work
    success_check: { result_matches: "PASS" }
    retry: { max: 1, on: [result_mismatch] }
`)
	rec := &recordingSequenceRunner{outcomes: []runner.NodeOutcome{{Result: huge, ExitCode: 0}}}
	s, h, led := newHarness(t, rec, Options{})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the run to fail")
	}
	second := rec.prompts[1]
	if len(second) > len("do the work")+len(retryFeedbackTemplate)+maxPriorReplyInPrompt+64 {
		t.Errorf("retry prompt is %d bytes; the quoted reply is not bounded by maxPriorReplyInPrompt (%d)",
			len(second), maxPriorReplyInPrompt)
	}
	if !strings.Contains(second, "excerpted") {
		// Bounded by len(second), not by 200: a mutation that empties the quote
		// makes this the failing branch, and a panic here takes the rest of the
		// file's tests down with it — reporting a suite as smaller than it is,
		// which is the worst way to be wrong about a test suite.
		t.Errorf("the quote was cut without saying so — a node must never be handed a truncated reply "+
			"as though it were whole:\n%s", second[:min(len(second), 200)])
	}
}

// TestRetry_NonJudgmentFailureQuotesNothing pins the gate: a blown budget is a
// spend fault, not a verdict on the reply, so the retry is asked exactly what
// the first attempt was. This is ADR 0010's judgment/infrastructure split,
// reused rather than re-invented.
func TestRetry_NonJudgmentFailureQuotesNothing(t *testing.T) {
	g := mustGraph(t, `
name: retry-budget
nodes:
  - id: work
    prompt: do the work
    budget_usd: 0.01
    retry: { max: 1, on: [budget_exceeded] }
`)
	rec := &recordingSequenceRunner{outcomes: []runner.NodeOutcome{
		{Result: "an expensive but unjudged answer", ExitCode: 0, TotalCostUSD: 5},
	}}
	s, h, led := newHarness(t, rec, Options{})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the run to fail on budget")
	}
	if len(rec.prompts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(rec.prompts))
	}
	if rec.prompts[1] != rec.prompts[0] {
		t.Errorf("a budget failure quoted the previous attempt back; nothing judged that reply, so the "+
			"retry has nothing to tell it to repair:\n%s", rec.prompts[1])
	}
}

// TestRetry_VerifyInfrastructureFaultQuotesNothing is the other half of the
// gate, and the one the token alone would get wrong: a verification that could
// not be COMPLETED fails the node under verify_failed for retry purposes but
// rendered no verdict on the work.
func TestRetry_VerifyInfrastructureFaultQuotesNothing(t *testing.T) {
	g := mustGraph(t, `
name: retry-verify-fault
nodes:
  - id: work
    prompt: do the work
    success_check:
      verify: { command: "make test" }
    retry: { max: 1, on: [verify_failed] }
`)
	rec := &recordingSequenceRunner{outcomes: []runner.NodeOutcome{{Result: "a fine answer", ExitCode: 0}}}
	verifier := verify.NewFakeVerifier(nil)
	verifier.InjectError("make test", errors.New("verifier could not spawn"))
	s, h, led := newVerifyHarness(t, rec, verifier, Options{})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the run to fail")
	}
	if len(rec.prompts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(rec.prompts))
	}
	if rec.prompts[1] != rec.prompts[0] {
		t.Errorf("a verification that never ran quoted the reply back as though a check had rejected it:\n%s",
			rec.prompts[1])
	}
}

// TestRetry_ARunErrorDropsAnEarlierQuote is the subtle case: attempt 2 carries
// attempt 1's reply, then dies before producing one of its own. Attempt 3 must
// carry NOTHING — the attempt it repeats said nothing, and "the last attempt"
// is the only thing a prompt may quote.
func TestRetry_ARunErrorDropsAnEarlierQuote(t *testing.T) {
	g := mustGraph(t, `
name: retry-run-error
nodes:
  - id: work
    prompt: do the work
    success_check: { result_matches: "PASS" }
    retry: { max: 2, on: [result_mismatch, run_error] }
`)
	rec := &recordingSequenceRunner{
		outcomes: []runner.NodeOutcome{{Result: "ATTEMPT-ONE-REPLY", ExitCode: 0}},
		err:      errors.New("claude run: exec: no such file"),
		errAt:    map[int]bool{1: true},
	}
	s, h, led := newHarness(t, rec, Options{})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the run to fail")
	}
	if len(rec.prompts) != 3 {
		t.Fatalf("attempts = %d, want 3", len(rec.prompts))
	}
	if !strings.Contains(rec.prompts[1], "ATTEMPT-ONE-REPLY") {
		t.Fatalf("second attempt should carry the first's reply:\n%s", rec.prompts[1])
	}
	if rec.prompts[2] != rec.prompts[0] {
		t.Errorf("third attempt carries a quote although the attempt before it produced no reply:\n%s",
			rec.prompts[2])
	}
}

// TestRetry_SessionHandoffNodeStaysColdAndStillCarriesTheQuote establishes the
// session-handoff case rather than leaving it to be discovered. The rule does
// not change: prepareRetry clears ResumeSession, so a retried session node
// starts cold exactly as it did before. What changes is that the cold start is
// no longer empty-handed — the quote is the node's own words from a
// conversation it can no longer see, which is why the prompt says so out loud.
func TestRetry_SessionHandoffNodeStaysColdAndStillCarriesTheQuote(t *testing.T) {
	g := mustGraph(t, `
name: retry-session
nodes:
  - id: parent
    prompt: parent
  - id: child
    prompt: child work
    depends_on: [parent]
    handoff: session
    success_check: { result_matches: "PASS" }
    retry: { max: 1, on: [result_mismatch] }
`)
	rec := &recordingSequenceRunner{outcomes: []runner.NodeOutcome{
		{Result: "PASS", SessionID: "s-parent", ExitCode: 0},
		{Result: "CHILD-FIRST-REPLY", ExitCode: 0},
	}}
	s, h, led := newHarness(t, rec, Options{})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the child to fail after its retry also missed the check")
	}
	if len(rec.prompts) != 3 {
		t.Fatalf("invocations = %d, want 3 (parent + child + child's retry)", len(rec.prompts))
	}
	if rec.resumes[1] != "s-parent" {
		t.Fatalf("the child's first attempt should resume the parent, got %q", rec.resumes[1])
	}
	if rec.resumes[2] != "" {
		t.Errorf("the RETRY resumed session %q; a retry starts cold by design and this change does not "+
			"touch that", rec.resumes[2])
	}
	if !strings.Contains(rec.prompts[2], "CHILD-FIRST-REPLY") {
		t.Errorf("the cold retry was not handed its own previous reply:\n%s", rec.prompts[2])
	}
	if !strings.Contains(rec.prompts[2], "FRESH claude session") {
		t.Errorf("the quote does not tell the node the attempt is not in its context, which for a "+
			"session node is the difference between notes and an impossible instruction:\n%s", rec.prompts[2])
	}
}

// seedPriorLegReply stages what a `resume --retry-failed` leg finds: the reply
// an EARLIER PROCESS left at failed/<node-id>.out, re-read into the handoff the
// way resume.go re-reads it for every cleared node whose failure was Judged.
func seedPriorLegReply(t *testing.T, runDir string, h *handoff.Handoff, nodeID, reply string) {
	t.Helper()
	path := handoff.FailedOutputPath(runDir, nodeID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("stage failed/: %v", err)
	}
	if err := os.WriteFile(path, []byte(reply), 0o644); err != nil {
		t.Fatalf("stage the previous leg's reply: %v", err)
	}
	seeded, err := h.SeedPriorReply(nodeID)
	if err != nil {
		t.Fatalf("SeedPriorReply: %v", err)
	}
	if !seeded {
		t.Fatalf("SeedPriorReply seeded nothing for %s; the staged reply never reached the leg", nodeID)
	}
}

// TestRetry_PriorLegExecutionOfASessionNodeIsCold is the cross-process half of
// TestRetry_SessionHandoffNodeStaysColdAndStillCarriesTheQuote, and it is the
// case that made the FRESH-SESSION paragraph a lie: a `resume --retry-failed`
// leg quotes the previous leg's reply into the node's FIRST execution of this
// leg, where nothing had cleared the session it resumes.
//
// A node re-run with the attempt it is repeating in its prompt is a retry, in
// this process or the last one, and a retry is cold. Note the graph declares no
// `retry:` at all — `--retry-failed` needs none, which is why LintSessions'
// retry-shaped warning never fires here.
func TestRetry_PriorLegExecutionOfASessionNodeIsCold(t *testing.T) {
	g := mustGraph(t, `
name: prior-leg-session
nodes:
  - id: parent
    prompt: parent
  - id: child
    prompt: child work
    depends_on: [parent]
    handoff: session
    success_check: { result_matches: "PASS" }
`)
	rec := &recordingSequenceRunner{outcomes: []runner.NodeOutcome{
		{Result: "PASS", SessionID: "s-parent", ExitCode: 0},
		{Result: "PASS", SessionID: "s-child-2", ExitCode: 0},
	}}
	runDir := t.TempDir()
	h := handoff.New(runDir, nil)
	led := ledger.New("test")
	s := NewScheduler(rec, Options{ProgressWriter: io.Discard})
	seedPriorLegReply(t, runDir, h, "child", "LEG-ONE-WRONG-ANSWER")

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(rec.prompts) != 2 {
		t.Fatalf("invocations = %d, want 2 (parent + the child's retry leg)", len(rec.prompts))
	}
	if !strings.Contains(rec.prompts[1], "LEG-ONE-WRONG-ANSWER") {
		t.Fatalf("the retry leg was not handed the reply the previous leg left on disk:\n%s", rec.prompts[1])
	}
	if rec.resumes[1] != "" {
		t.Errorf("the child resumed session %q while its own prompt told it it is a FRESH session with "+
			"no conversation behind it; a retry is cold in this process or the last one", rec.resumes[1])
	}
	if !strings.Contains(rec.prompts[1], "FRESH claude session") {
		t.Errorf("the quote dropped the paragraph that makes it readable as notes:\n%s", rec.prompts[1])
	}

	// The surfaces have to say it too — the ledger detail is where ADR 0020 §6
	// rests the claim that a session node's retry not resuming its parent is
	// visible rather than silent.
	row, ok := findRecord(led, "child")
	if !ok {
		t.Fatal("child was never recorded in the ledger")
	}
	if !strings.Contains(row.Detail, "parent session not resumed") {
		t.Errorf("ledger detail = %q; a session node that ran without its parent's conversation must "+
			"say so on the row that priced it", row.Detail)
	}
}

// TestRetryPrompt_NothingToQuoteLeavesThePromptAlone covers the unit directly:
// a reply that is empty or all whitespace appends no block at all, so an empty
// fence never claims the node said something.
func TestRetryPrompt_NothingToQuoteLeavesThePromptAlone(t *testing.T) {
	for _, reply := range []string{"", "   ", "\n\t\n"} {
		got, err := retryPrompt("base prompt", reply)
		if err != nil {
			t.Fatalf("retryPrompt(%q) errored: %v", reply, err)
		}
		if got != "base prompt" {
			t.Errorf("retryPrompt(%q) = %q, want the base prompt unchanged", reply, got)
		}
	}
}
