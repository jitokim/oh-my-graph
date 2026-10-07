package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
)

// TestPrintCycleVerdict_SanitisesRemainingAndEvidence_349 feeds the cycle
// verdict an assessor reply whose remaining and evidence both carry a CSI
// colour/cursor sequence and U+202E. Neither reaches the printed lines, the
// text around them does — and assess.json still keeps the raw reply, because
// only the terminal printout is cleaned (#349).
func TestPrintCycleVerdict_SanitisesRemainingAndEvidence_349(t *testing.T) {
	a := coordinator.Assessment{
		Remaining: "rem-before\x1b[31m\x1b[2Arem-middle\u202erem-after",
		Evidence:  "ev-before\x1b[1;1Hev-middle\u202eev-after\x1b[0m",
	}
	var out strings.Builder
	printCycleVerdict(&out, coordinator.CycleReport{Cycle: 1, RunID: "r1", Assessment: a})

	got := out.String()
	if strings.ContainsRune(got, 0x1b) || strings.ContainsRune(got, '\u202e') {
		t.Errorf("the cycle verdict printed a raw escape or bidi override:\n%q", got)
	}
	for _, want := range []string{
		"  remaining: rem-beforerem-middlerem-after\n",
		"  evidence: ev-beforeev-middleev-after\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("verdict missing %q:\n%s", want, got)
		}
	}

	dir := t.TempDir()
	if err := saveAssessment(dir, a); err != nil {
		t.Fatalf("saveAssessment: %v", err)
	}
	saved, err := os.ReadFile(filepath.Join(dir, assessFileName))
	if err != nil {
		t.Fatalf("read assess.json: %v", err)
	}
	if !strings.Contains(string(saved), `\u001b[31m`) || !strings.Contains(string(saved), "\u202e") {
		t.Errorf("assess.json must keep the raw reply, got:\n%s", saved)
	}
}
