package schedule

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/verify"
)

// hostileText carries both kinds of terminal control #349 is about: a CSI
// colour + cursor-move sequence and a bidi override (U+202E), with visible
// text either side of each that must survive the cleaning.
const hostileText = "before\x1b[31m\x1b[2Amiddle‮after\x1b[0m"

// assertTerminalClean fails when s still carries an ESC or U+202E, or lost
// the visible text hostileText wraps around them.
func assertTerminalClean(t *testing.T, surface, s string) {
	t.Helper()
	if strings.ContainsRune(s, 0x1b) {
		t.Errorf("%s still carries an ESC: %q", surface, s)
	}
	if strings.ContainsRune(s, '‮') {
		t.Errorf("%s still carries U+202E: %q", surface, s)
	}
	if !strings.Contains(s, "beforemiddleafter") {
		t.Errorf("%s lost the visible text around the controls: %q", surface, s)
	}
}

// TestCapDetail_SanitisesBeforeTheBound_349 pins the order inside capDetail
// (#349): the string is cleaned FIRST, so the rune bound measures what a
// reader sees — escapes that pad a short message past maxDetailRunes do not
// earn it a cut — and a long one is still cut to the bound, tail kept.
func TestCapDetail_SanitisesBeforeTheBound_349(t *testing.T) {
	padded := strings.Repeat("\x1b[31m", maxDetailRunes) + hostileText
	got := capDetail(padded)
	assertTerminalClean(t, "capDetail", got)
	if strings.HasPrefix(got, "…") {
		t.Errorf("a short message padded with escapes must not be cut, got %q", got)
	}

	long := strings.Repeat("x", 1000) + "\n" + hostileText
	got = capDetail(long)
	assertTerminalClean(t, "capDetail (cut)", got)
	if n := utf8.RuneCountInString(got); n > maxDetailRunes+1 { // +1 for the "…" cut marker
		t.Errorf("capped detail is %d runes, want at most %d", n, maxDetailRunes+1)
	}
	if !strings.HasPrefix(got, "…") || strings.Contains(got, "\n") {
		t.Errorf("a long detail must be cut and flattened onto one line, got %q", got)
	}
}

// TestScheduler_LedgerTableShowsASanitisedDetail_349 drives both Detail
// sources through a FakeRunner/FakeVerifier run — a node's failure cause and
// a verify command's output tail — and asserts the end-of-run table the
// ledger renders carries neither an escape sequence nor a bidi override (#349).
func TestScheduler_LedgerTableShowsASanitisedDetail_349(t *testing.T) {
	g := mustGraph(t, `
name: hostile
nodes:
  - { id: crashed, prompt: crashed }
  - id: checked
    prompt: checked
    success_check:
      verify: { command: "make test" }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"crashed": {ExitCode: 1, FailureCause: hostileText},
		"checked": pass("s-checked", 0),
	})
	verifier := verify.NewFakeVerifier(map[string]verify.Result{
		"make test": {ExitCode: 1, Output: hostileText + "\n"},
	})
	s, h, led := newVerifyHarness(t, fake, verifier, Options{ContinueOnFail: true})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected both nodes to fail")
	}
	for _, id := range []string{"crashed", "checked"} {
		rec, ok := findRecord(led, id)
		if !ok {
			t.Fatalf("%s was never recorded in the ledger", id)
		}
		assertTerminalClean(t, id+" ledger detail", rec.Detail)
	}
	assertTerminalClean(t, "end-of-run table", led.Render())
}

// TestScheduler_VerificationIsJudgedOnTheRawOutput_349 pins what #349 must
// NOT change: output_matches is judged on the raw verify.Result.Output, escape
// sequences and all. A pattern that needs the raw ESC passes; one that only
// the cleaned text would satisfy fails — while the Detail shown is clean.
func TestScheduler_VerificationIsJudgedOnTheRawOutput_349(t *testing.T) {
	raw := "\x1b[32mok\x1b[0m‮ github.com/x\n"
	cases := []struct {
		name    string
		pattern string
		pass    bool
	}{
		{"a pattern needing the raw escape passes", `^\x1b\[32mok`, true},
		{"a pattern only the cleaned text matches fails", `^ok github`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := mustGraph(t, `
name: raw
nodes:
  - id: dev
    prompt: dev
    success_check:
      verify: { command: "go test", output_matches: '`+tc.pattern+`' }
`)
			fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"dev": pass("s-dev", 0)})
			verifier := verify.NewFakeVerifier(map[string]verify.Result{"go test": {ExitCode: 0, Output: raw}})
			s, h, led := newVerifyHarness(t, fake, verifier, Options{})

			err := s.Run(context.Background(), g, h, led)
			if tc.pass && err != nil {
				t.Fatalf("judged on the raw output this must pass, got %v", err)
			}
			if !tc.pass {
				if err == nil {
					t.Fatal("judged on the raw output this must fail, but it passed")
				}
				rec, _ := findRecord(led, "dev")
				if strings.ContainsRune(rec.Detail, 0x1b) || strings.ContainsRune(rec.Detail, '‮') {
					t.Errorf("the shown Detail must still be clean: %q", rec.Detail)
				}
			}
		})
	}
}
