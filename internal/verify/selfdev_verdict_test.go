//go:build !windows

package verify_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/verify"
)

// maxEvidenceRunes mirrors internal/schedule's bound on the verify output a
// feedback re-run is handed (it keeps the tail); move both together.
const maxEvidenceRunes = 4000

// TestSelfDevVerdictCommandJudgesTheReviewArtifacts runs graphs/self-dev.yaml's
// review-verdict `verify` command — the engine's own reading of the two review
// artifacts, which is what keeps a model's CLEAN from being taken on its word
// (#293) — through the real ShellVerifier, against review files written the
// way the reviewers actually reply. It is shell, so it is tested as shell: a
// quoting slip here passes every load-time check and gates nothing.
func TestSelfDevVerdictCommandJudgesTheReviewArtifacts(t *testing.T) {
	loaded, err := graph.LoadFile(filepath.Join("..", "..", "graphs", "self-dev.yaml"))
	if err != nil {
		t.Fatalf("load self-dev.yaml: %v", err)
	}
	var command string
	for _, n := range loaded.Graph.Nodes {
		if n.ID == "review-verdict" && n.SuccessCheck.Verify != nil {
			command = n.SuccessCheck.Verify.Command
		}
	}
	if command == "" {
		t.Fatal("self-dev's review-verdict declares no verify command")
	}

	const securityFindings = "**FINDINGS:**\n\n- verify.command splices a model's reply into sh -c\n"
	const styleFindings = "FINDINGS:\n- the helper name says nothing about what it returns\n"
	const securityMinor = "MINOR:\n- the default branch could also refuse an empty path\n"
	const styleMinor = "**MINOR:**\n\n- the default branch reads oddly\n"
	// Long enough that the two reviews together overflow the engine's
	// evidence bound — the case where a plain `cat` of both lost the head of
	// the first one, and with it the worst finding.
	longSecurity := securityFindings + strings.Repeat("- one more security finding, lower down the list\n", 120)
	longStyle := styleFindings + strings.Repeat("- one more style nit, lower down the list\n", 120)
	cases := []struct {
		name      string
		security  string
		style     string
		wantExit  int
		wantOut   []string
		forbidOut []string
	}{
		{name: "both clean", security: "CLEAN — no security issues.\n", style: "**CLEAN**\n", wantExit: 0},
		{name: "clean after leading blank lines", security: "\n\n  CLEAN\n", style: "`CLEAN` nothing to change\n", wantExit: 0},
		{name: "security findings", security: securityFindings, style: "CLEAN\n", wantExit: 1,
			wantOut: []string{"splices a model's reply"}, forbidOut: []string{"CLEAN"}},
		{name: "style findings", security: "CLEAN\n", style: styleFindings, wantExit: 1,
			wantOut: []string{"helper name"}, forbidOut: []string{"CLEAN"}},
		{name: "both findings", security: securityFindings, style: styleFindings, wantExit: 1,
			wantOut: []string{"splices a model's reply", "helper name"}},
		{name: "clean only mentioned, not the verdict", security: "Still reviewing; CLEAN so far.\n", style: "CLEAN\n", wantExit: 1,
			wantOut: []string{"Still reviewing"}},
		{name: "empty review", security: "", style: "CLEAN\n", wantExit: 1},
		// #309: CLEAN is a whole word, as result_matches' CLEAN\b reads it — a
		// review whose first word only starts with CLEAN is not clean.
		{name: "cleanup is not clean", security: "CLEANUP: the temp dir is never removed\n", style: "CLEAN\n", wantExit: 1,
			wantOut: []string{"temp dir is never removed"}},
		{name: "cleanly with emphasis is not clean", security: "CLEAN\n", style: "**CLEANLY** split, but the helper name says nothing\n", wantExit: 1,
			wantOut: []string{"helper name"}},
		{name: "clean with no trailing newline", security: "CLEAN", style: "**CLEAN**", wantExit: 0},
		{name: "clean then punctuation", security: "CLEAN.\n", style: "CLEAN: nothing to change\n", wantExit: 0},
		// `_` is a word character to result_matches' CLEAN\b, so it is kept:
		// CLEAN_ is not CLEAN, and underscore emphasis passes only as a pair.
		{name: "clean then an underscore is not clean", security: "CLEAN_ the temp dir is never removed\n", style: "CLEAN\n", wantExit: 1,
			wantOut: []string{"temp dir is never removed"}},
		{name: "clean in underscore emphasis", security: "__CLEAN__\n", style: "_CLEAN_ nothing to change\n", wantExit: 0},
		{name: "minor in underscore emphasis", security: "__MINOR__:\n- a header could be set\n", style: "CLEAN\n", wantExit: 0},
		{name: "cleanup style beside clean security", security: "CLEAN\n", style: "CLEANUP: the helper name says nothing\n", wantExit: 1,
			wantOut: []string{"helper name"}, forbidOut: []string{"CLEAN\n"}},
		// A byte cap ahead of the match would cut this CLEANUP to CLEAN at
		// its edge, and the end-of-input arm would pass it.
		{name: "cleanup past a byte cap is not clean", security: strings.Repeat(" ", 251) + "CLEANUP: the temp dir is never removed\n", style: "CLEAN\n", wantExit: 1,
			wantOut: []string{"temp dir is never removed"}},
		{name: "both findings, both long", security: longSecurity, style: longStyle, wantExit: 1,
			wantOut: []string{"splices a model's reply", "helper name"}},
		// ADR 0043: a MINOR: review passes the gate like a CLEAN one, so only
		// a blocking review is ever dev's payload.
		{name: "clean and minor", security: "CLEAN\n", style: styleMinor, wantExit: 0},
		{name: "both minor", security: securityMinor, style: styleMinor, wantExit: 0},
		{name: "minor with emphasis after leading blank lines", security: "\n\n  **MINOR**:\n- a header could be set\n", style: "CLEAN\n", wantExit: 0},
		{name: "minor with a space before its colon", security: "MINOR :\n- a header could be set\n", style: "CLEAN\n", wantExit: 0},
		{name: "findings beside minor", security: securityFindings, style: styleMinor, wantExit: 1,
			wantOut: []string{"splices a model's reply"}, forbidOut: []string{"default branch"}},
		{name: "minor without its colon", security: "MINOR - a header could be set\n", style: "CLEAN\n", wantExit: 1,
			wantOut: []string{"MINOR - a header"}},
		{name: "minor in the wrong case", security: "CLEAN\n", style: "Minor: the default branch reads oddly\n", wantExit: 1,
			wantOut: []string{"Minor: the default branch"}},
		{name: "minor only mentioned, not the verdict", security: "Still reviewing; MINOR: so far.\n", style: "CLEAN\n", wantExit: 1,
			wantOut: []string{"Still reviewing"}},
		{name: "empty review beside minor", security: "", style: styleMinor, wantExit: 1, forbidOut: []string{"default branch"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A space in the run directory: OMG_HOME is the user's to choose,
			// and the artifact paths must survive it.
			dir := filepath.Join(t.TempDir(), "omg home")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			security := filepath.Join(dir, "review-security.out")
			style := filepath.Join(dir, "review-style.out")
			for path, body := range map[string]string{security: tc.security, style: tc.style} {
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			resolved := strings.NewReplacer(
				"{{ artifacts.review-security }}", security,
				"{{ artifacts.review-style }}", style,
			).Replace(command)

			result, err := verify.NewShellVerifier().Verify(context.Background(), verify.Request{Command: resolved, Cwd: dir})
			if err != nil {
				t.Fatalf("verify did not run: %v", err)
			}
			if result.ExitCode != tc.wantExit {
				t.Errorf("exit %d, want %d; output:\n%s", result.ExitCode, tc.wantExit, result.Output)
			}
			// The engine hands dev only the tail of this output, so
			// anything within the bound reaches dev whole and every wantOut
			// below is a finding dev actually reads.
			if n := len([]rune(strings.TrimSpace(result.Output))); n > maxEvidenceRunes {
				t.Errorf("output is %d runes, over the engine's %d-rune evidence bound — the head of the first review, its worst finding, would be cut before dev reads it", n, maxEvidenceRunes)
			}
			for _, want := range tc.wantOut {
				if !strings.Contains(result.Output, want) {
					t.Errorf("output does not carry %q — dev's repair round would not see that finding; output:\n%s", want, result.Output)
				}
			}
			for _, forbid := range tc.forbidOut {
				if strings.Contains(result.Output, forbid) {
					t.Errorf("output carries %q from the clean review — only open reviews belong in dev's payload; output:\n%s", forbid, result.Output)
				}
			}
		})
	}
}
