package handoff

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// TestLintFeedbackQuoting_TheSpecimen is run 20260816-163759.091162000-1
// reduced to its two nodes: a correct arc, and a repair prompt that branches on
// feedback it is never handed. The graph is VALID — every ADR 0010 rule holds —
// which is exactly why nothing said anything before this sweep.
func TestLintFeedbackQuoting_TheSpecimen(t *testing.T) {
	g := parseGraph(t, `
name: specimen
nodes:
  - id: build
    prompt: |
      Write /tmp/out.txt. Its ONLY content must be one word:
        - if a FEEDBACK section appears below, write: alpha
        - if there is no feedback section, write: draft
  - id: check
    depends_on: [build]
    prompt: read the file and report what it contains
    success_check:
      exit_zero: true
      verify: { command: 'test "$(cat /tmp/out.txt)" = "alpha"' }
    feedback: { rerun: build, max: 1 }
`)
	warnings := LintFeedbackQuoting(g)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly one warning, got %v", warnings)
	}
	w := warnings[0]
	// On the REPAIR node, because that is where the missing line goes — not on
	// the declarer, which is written correctly.
	if w.NodeID != "build" || w.Field != "prompt" {
		t.Fatalf("warning = %+v, want node build field prompt", w)
	}
	// The message has to carry the fix, not just the fault: who reruns this
	// node, the exact token to paste, and that it is empty on the first pass
	// (an author who does not know that reads the token as a bug).
	for _, want := range []string{`"check"`, "{{ feedback.check }}", "empty on the first pass"} {
		if !strings.Contains(w.Detail, want) {
			t.Fatalf("detail %q should contain %q", w.Detail, want)
		}
	}
}

// TestLintFeedbackQuoting_QuotedShapesAreSilent drives the predicate over the
// ways a two-node loop can name its payload. The token is found with the
// runtime's own placeholderPattern, so whitespace inside it is not the
// predicate — a graph that interpolates must not be warned about.
func TestLintFeedbackQuoting_QuotedShapesAreSilent(t *testing.T) {
	cases := []struct {
		name   string
		quote  string
		silent bool
	}{
		{name: "the shipped shape", quote: "Findings: {{ feedback.check }}", silent: true},
		{name: "a tight token interpolates identically", quote: "{{feedback.check}}", silent: true},
		{name: "no mention of the payload at all", quote: "Do the work again.", silent: false},
		{
			// Near-misses that do not interpolate. The engine substitutes what
			// its pattern matches and nothing else, so neither of these hands
			// the model anything.
			name: "prose about feedback is not a quote", quote: "Apply the feedback from the checker.", silent: false,
		},
		{name: "the wrong namespace is not this payload", quote: "{{ artifacts.check }}", silent: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := parseGraph(t, `
name: quoted
nodes:
  - id: build
    prompt: |
      Do the work.
      `+tc.quote+`
  - id: check
    depends_on: [build]
    prompt: judge the work
    success_check: { exit_zero: true, result_matches: PASS }
    feedback: { rerun: build, max: 1 }
`)
			warnings := LintFeedbackQuoting(g)
			if tc.silent && len(warnings) != 0 {
				t.Fatalf("expected silence for %q, got %v", tc.quote, warnings)
			}
			if !tc.silent && len(warnings) != 1 {
				t.Fatalf("expected one warning for %q, got %v", tc.quote, warnings)
			}
		})
	}
}

// TestLintFeedbackQuoting_SurvivesFragmentSplicing is the case the specimen was
// actually made of: the ids that ran were `qa-a/build` and `qa-a/check`, and
// the token the loader rewrites to is {{ feedback.qa-a/check }} (ADR 0027). A
// sweep that only worked on hand-written ids would have missed the very run
// that motivated it — and it cannot be faked with a literal graph, because
// graph.Parse refuses an authored '/'. So this drives the real loader over a
// real fragment, both ways round.
func TestLintFeedbackQuoting_SurvivesFragmentSplicing(t *testing.T) {
	cases := []struct {
		name        string
		buildPrompt string
		wantWarning string // the node id expected to be warned; "" = expect silence
	}{
		{
			name:        "the spliced repair never quotes the spliced declarer",
			buildPrompt: "write the file; if a FEEDBACK section appears below, write alpha",
			wantWarning: "qa-a/build",
		},
		{
			name:        "the fragment quotes its own declarer, and splicing rewrites the id",
			buildPrompt: "write the file\nFindings from the last round: {{ feedback.check }}",
			wantWarning: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mustWrite(t, filepath.Join(dir, "fragments", "qa-loop.yaml"), `
fragment: qa-loop
description: build then check, with a bounded repair round
exit: check
nodes:
  - id: build
    prompt: |
      `+strings.ReplaceAll(tc.buildPrompt, "\n", "\n      ")+`
  - id: check
    depends_on: [build]
    prompt: read the file and report what it contains
    success_check:
      exit_zero: true
      verify: { command: 'test "$(cat /tmp/out.txt)" = "alpha"' }
    feedback: { rerun: build, max: 1 }
`)
			path := filepath.Join(dir, "graph.yaml")
			mustWrite(t, path, `
name: spliced
nodes:
  - id: qa-a
    use: qa-loop
`)
			issues, _, loaded, err := graph.LintLoadFile(path)
			if err != nil || len(issues) > 0 {
				t.Fatalf("fixture must load: err=%v issues=%v", err, issues)
			}
			// The premise: the ids really are namespaced after splicing, so the
			// token this sweep must find really is {{ feedback.qa-a/check }}.
			if _, ok := loaded.Graph.NodeByID("qa-a/check"); !ok {
				t.Fatalf("fixture did not splice: nodes are %v", loaded.Graph.Nodes)
			}

			warnings := LintFeedbackQuoting(loaded.Graph)
			if tc.wantWarning == "" {
				if len(warnings) != 0 {
					t.Fatalf("expected silence after splicing, got %v", warnings)
				}
				return
			}
			if len(warnings) != 1 {
				t.Fatalf("expected exactly one warning, got %v", warnings)
			}
			if warnings[0].NodeID != tc.wantWarning {
				t.Fatalf("warning on %q, want %q", warnings[0].NodeID, tc.wantWarning)
			}
			if !strings.Contains(warnings[0].Detail, "{{ feedback.qa-a/check }}") {
				t.Fatalf("detail %q must name the SPLICED token, or the fix it prints does not interpolate", warnings[0].Detail)
			}
		})
	}
}

// TestLintFeedbackQuoting_AnyBodyNodeButTheDeclarerCounts pins the one place
// this predicate is narrower than "the rerun target's prompt must quote it".
// A middle body node reading the payload means round two really does produce
// something different — the loop repairs, just not at its first node. The
// declarer is the opposite case: it is the judge, so its own quote is not a
// repair, it is a reviewer being asked to change its mind about unchanged
// artifacts.
func TestLintFeedbackQuoting_AnyBodyNodeButTheDeclarerCounts(t *testing.T) {
	body := func(buildQuote, refineQuote, checkQuote string) *graph.Graph {
		return parseGraph(t, `
name: three-node-body
nodes:
  - id: build
    prompt: |
      build it. `+buildQuote+`
  - id: refine
    depends_on: [build]
    prompt: |
      refine it. `+refineQuote+`
  - id: check
    depends_on: [refine]
    prompt: |
      judge it. `+checkQuote+`
    success_check: { exit_zero: true, result_matches: PASS }
    feedback: { rerun: build, max: 1 }
`)
	}
	token := "{{ feedback.check }}"

	if warnings := LintFeedbackQuoting(body(token, "", "")); len(warnings) != 0 {
		t.Fatalf("the rerun target quotes it: %v", warnings)
	}
	if warnings := LintFeedbackQuoting(body("", token, "")); len(warnings) != 0 {
		t.Fatalf("a middle body node reads the findings, so the loop repairs: %v", warnings)
	}
	warnings := LintFeedbackQuoting(body("", "", token))
	if len(warnings) != 1 || warnings[0].NodeID != "build" {
		t.Fatalf("only the JUDGE quotes its own payload — nothing repairs: %v", warnings)
	}
}

// TestLintFeedbackQuoting_OnlyThePromptIsRead pins the scope. The token is
// legal in a cwd and in a verify command too (validateFeedbackPlaceholders
// permits the whole body), but neither is read by a MODEL: a payload on a
// command line is LintVerifyInlining's finding, and one in a cwd is a path.
func TestLintFeedbackQuoting_OnlyThePromptIsRead(t *testing.T) {
	g := parseGraph(t, `
name: not-a-prompt
nodes:
  - id: build
    prompt: do the work
    success_check:
      exit_zero: true
      verify: { command: "echo {{ feedback.check }}" }
  - id: check
    depends_on: [build]
    prompt: judge the work
    success_check: { exit_zero: true, result_matches: PASS }
    feedback: { rerun: build, max: 1 }
`)
	warnings := LintFeedbackQuoting(g)
	if len(warnings) != 1 || warnings[0].NodeID != "build" {
		t.Fatalf("a payload in a verify command is not something the model reads: %v", warnings)
	}
	// And the other sweep still speaks about that command, so the two findings
	// are complementary rather than one covering for the other.
	if inlining := LintVerifyInlining(g); len(inlining) != 1 {
		t.Fatalf("LintVerifyInlining should still condemn the command, got %v", inlining)
	}
}

// TestLintFeedbackQuoting_QuietWhereThereIsNoLoop covers the shapes with
// nothing to say: no arc at all, and an arc whose body FeedbackBody reports as
// empty because validateFeedback has already refused it. The second is reached
// through Issues rather than Parse, since a graph that invalid never loads.
func TestLintFeedbackQuoting_QuietWhereThereIsNoLoop(t *testing.T) {
	plain := parseGraph(t, `
name: no-feedback
nodes:
  - id: build
    prompt: do the work
  - id: check
    depends_on: [build]
    prompt: judge the work
`)
	if warnings := LintFeedbackQuoting(plain); len(warnings) != 0 {
		t.Fatalf("a graph with no arc has no loop to judge: %v", warnings)
	}

	// An arc whose target is not an ancestor: FeedbackBody reports no body, and
	// without the guard this sweep would invent a finding against a node the
	// graph does not have. It is built as a struct rather than parsed because
	// no loading path can produce it — Parse and LintLoadFile both refuse the
	// arc first (validateFeedback), which is precisely why the guard is what
	// keeps the sweep honest if it is ever called from somewhere new.
	invalid := &graph.Graph{Nodes: []graph.Node{
		{ID: "build", Prompt: "do the work"},
		{ID: "check", DependsOn: []string{"build"}, Prompt: "judge the work",
			Feedback: &graph.Feedback{Rerun: "nowhere", Max: 1}},
	}}
	if warnings := LintFeedbackQuoting(invalid); len(warnings) != 0 {
		t.Fatalf("an arc validateFeedback already refused has no body: %v", warnings)
	}
}

// TestLintFeedbackQuoting_OneWarningPerBlindArc: two independent loops, one
// wired and one not, report exactly the unwired one.
func TestLintFeedbackQuoting_OneWarningPerBlindArc(t *testing.T) {
	g := parseGraph(t, `
name: two-lanes
nodes:
  - id: build-a
    prompt: |
      lane a. Findings: {{ feedback.check-a }}
  - id: check-a
    depends_on: [build-a]
    prompt: judge lane a
    success_check: { exit_zero: true, result_matches: PASS }
    feedback: { rerun: build-a, max: 1 }
  - id: build-b
    prompt: lane b
  - id: check-b
    depends_on: [build-b]
    prompt: judge lane b
    success_check: { exit_zero: true, result_matches: PASS }
    feedback: { rerun: build-b, max: 1 }
`)
	warnings := LintFeedbackQuoting(g)
	if len(warnings) != 1 || warnings[0].NodeID != "build-b" {
		t.Fatalf("expected one warning, on the blind lane only: %v", warnings)
	}
}

// TestLintFeedbackQuoting_ShippedGraphsAreClean is the sweep's own regression
// test, mirroring TestLintVerifyInlining_ShippedGraphsAreClean: no graph this
// repo ships may declare a loop nothing in its body reads. It is also half of
// the shipped measurement (docs/measurements/0028-feedback-quote-corpus.md — 8
// entry graphs, 2 declarers, 0 hits), and pinning it here is what keeps that
// claim from decaying silently: a template or a cited fragment that later grows
// a blind loop fails in the test suite rather than in a paid run.
//
// Loaded through graph.LoadFile so `use:` fragments are resolved in (ADR 0013),
// which is the only way a FRAGMENT's loop is judged at all — a fragment file is
// not an entry graph and cannot be linted on its own (ADR 0028 §Failure modes).
// `graphs/adr-driven-dev.yaml` citing `repair-round` twice is what brings that
// fragment's spliced nodes, namespaced ids and all, into this population.
func TestLintFeedbackQuoting_ShippedGraphsAreClean(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "graphs", "*.yaml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no shipped graphs found to sweep: %v", err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			loaded, err := graph.LoadFile(path)
			if err != nil {
				t.Fatalf("load %s: %v", path, err)
			}
			if warnings := LintFeedbackQuoting(loaded.Graph); len(warnings) != 0 {
				t.Fatalf("%s: a shipped loop must quote its own feedback payload, got %v", path, warnings)
			}
		})
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
