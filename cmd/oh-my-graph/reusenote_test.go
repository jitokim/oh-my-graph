package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// #338: with reuse on, the plan screen prints ONE scan line — directory,
// offered count, skipped counts by reason — and one line per citation with
// the entry id, the source path and the digest.
func TestPrintPlanForRuntime_ReuseOnNamesTheScanAndEveryCitation(t *testing.T) {
	dir := filepath.Join("/work", "graphs", "fragments")
	plan := planPolicies(t, false)
	plan.Reuse = &coordinator.ReuseRecord{
		Catalog: coordinator.ReuseCatalog{
			Dir:     dir,
			Offered: []coordinator.ReuseEntry{{ID: "read-and-report"}},
			Skipped: []coordinator.ReuseSkip{
				{Source: "a.yaml", Reason: coordinator.ReuseSkipTool},
				{Source: "b.yaml", Reason: coordinator.ReuseSkipNonPromptSlot},
				{Source: "c.yaml", Reason: coordinator.ReuseSkipTool},
			},
		},
		Citations: []coordinator.ReuseCitation{
			{NodeID: "cite", EntryID: "read-and-report", Source: filepath.Join(dir, "read-and-report.yaml"), SHA256: strings.Repeat("ab", 32)},
			{NodeID: "again", EntryID: "read-and-report", Source: filepath.Join(dir, "read-and-report.yaml"), SHA256: strings.Repeat("ab", 32)},
		},
	}
	var out strings.Builder
	printPlanForRuntime(&out, plan, "", runner.RuntimeClaude, nil, false, conventionsDisclosure{}, nil)
	screen := out.String()

	var scanLines []string
	for _, line := range strings.Split(screen, "\n") {
		if strings.Contains(line, "reuse: scanned") {
			scanLines = append(scanLines, line)
		}
	}
	want := "  reuse: scanned " + dir + " — 1 offered, 3 skipped (non-prompt slot: 1, tool not read-only: 2)"
	if len(scanLines) != 1 || scanLines[0] != want {
		t.Errorf("scan lines = %q, want exactly [%q]\n%s", scanLines, want, screen)
	}
	for _, c := range plan.Reuse.Citations {
		line := "    " + c.NodeID + " cites " + c.EntryID + " — " + c.Source + " sha256:" + c.SHA256 + "\n"
		if !strings.Contains(screen, line) {
			t.Errorf("screen lacks citation line %q:\n%s", line, screen)
		}
	}
}

// #338: a plan with reuse on that cites nothing still states the scan, and a
// catalog that skipped nothing says "0 skipped" rather than an empty list.
func TestPrintPlanForRuntime_ReuseOnWithNoCitationStillNamesTheScan(t *testing.T) {
	plan := planPolicies(t, false)
	plan.Reuse = &coordinator.ReuseRecord{Catalog: coordinator.ReuseCatalog{Dir: "/work/graphs/fragments"}}
	var out strings.Builder
	printPlanForRuntime(&out, plan, "", runner.RuntimeClaude, nil, false, conventionsDisclosure{}, nil)
	if want := "  reuse: scanned /work/graphs/fragments — 0 offered, 0 skipped\n"; !strings.Contains(out.String(), want) {
		t.Errorf("screen lacks %q:\n%s", want, out.String())
	}
	if strings.Contains(out.String(), " cites ") {
		t.Errorf("a plan citing nothing printed a citation line:\n%s", out.String())
	}
}

// #338: with reuse off (plan.Reuse nil) the screen says nothing about reuse,
// on either runtime.
func TestPrintPlanForRuntime_ReuseOffPrintsNothingAboutReuse(t *testing.T) {
	for _, runtime := range []runner.Runtime{runner.RuntimeClaude, runner.RuntimeCodex} {
		plan := planPolicies(t, false)
		plan.Reuse = nil
		var out strings.Builder
		printPlanForRuntime(&out, plan, "", runtime, nil, false, conventionsDisclosure{}, nil)
		if strings.Contains(strings.ToLower(out.String()), "reuse") {
			t.Errorf("%s: reuse off, but the screen mentions reuse:\n%s", runtime, out.String())
		}
	}
}

// #338: end to end through planAndExecute, the citation the coordinator
// spliced is the one the screen names, with the digest it recorded.
func TestPlanAndExecute_ScreenNamesTheSplicedCitation(t *testing.T) {
	isolateRunHome(t)
	dir, source := plantReuseCatalog(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: reuseCitingSpec},
		"Read README.md and report. Start with DONE.-1": {SessionID: "s-1", Result: "DONE all read"},
	})
	var out strings.Builder
	captureStdout(t, func() {
		if err := planAndExecute(context.Background(), &out, coordinator.New(fake, coordinator.WithInvocationDir(dir)), fake,
			commonRunFlags{inputs: inputFlag{}}, "audit the readme", singleCycle, false, nil, nil); err != nil {
			t.Fatalf("planAndExecute: %v\n%s", err, out.String())
		}
	})
	screen := out.String()
	// The scan resolves the invocation root, so the screen carries the
	// symlink-free path (macOS's /var is /private/var).
	source, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if want := "reuse: scanned " + filepath.Dir(source) + " — 1 offered, 0 skipped"; !strings.Contains(screen, want) {
		t.Errorf("screen lacks %q:\n%s", want, screen)
	}
	if want := "    cite cites probe — " + source + " sha256:" + fmt.Sprintf("%x", sha256.Sum256(data)) + "\n"; !strings.Contains(screen, want) {
		t.Errorf("screen lacks %q:\n%s", want, screen)
	}
}
