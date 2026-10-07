package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// reuseProbeFragment is an admissible reusable shape (#338): one slot that
// lands only in prompt:, and tools that are exact allowlist members.
const reuseProbeFragment = `fragment: probe
description: read a file and report on it
substitutions: [target]
node:
  type: claude-run
  prompt: "Read {{ with.target }} and report. Start with DONE."
  allowed_tools: [Read, Grep, Glob]
`

// reuseCitingSpec is a planner reply whose one node cites probe (#338).
const reuseCitingSpec = `{"name":"reuse-work","nodes":[{"id":"cite","reuse":"probe","bind":{"target":"README.md"}}]}`

// plantReuseCatalog writes probe under <tmp>/graphs/fragments and returns the
// invocation directory and the fragment's path (#338).
func plantReuseCatalog(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "graphs", "fragments", "probe.yaml")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte(reuseProbeFragment), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, source
}

// #338: an auto run whose plan cites an offered shape saves the RESOLVED
// graph — no reuse:, no bind: — and writes reuse-catalog.json beside it, both
// owner-only; with reuse off the same run directory holds no record at all.
func TestPlanAndExecute_ReuseSavesTheResolvedGraphAndItsRecord(t *testing.T) {
	isolateRunHome(t)
	dir, _ := plantReuseCatalog(t)
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

	runDir := soleRunDir(t)
	specPath := filepath.Join(runDir, generatedSpecFileName)
	spec, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(spec), `"reuse"`) || strings.Contains(string(spec), `"bind"`) {
		t.Errorf("graph.json carries reuse/bind:\n%s", spec)
	}
	if !strings.Contains(string(spec), "Read README.md and report.") {
		t.Errorf("graph.json is not the spliced graph:\n%s", spec)
	}
	recordPath := filepath.Join(runDir, coordinator.ReuseCatalogFileName)
	record, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("no %s beside graph.json: %v", coordinator.ReuseCatalogFileName, err)
	}
	if !strings.Contains(string(record), `"node_id": "cite"`) || !strings.Contains(string(record), `"entry_id": "probe"`) {
		t.Errorf("%s does not record the citation:\n%s", coordinator.ReuseCatalogFileName, record)
	}
	for _, path := range []string{specPath, recordPath} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("%s: mode %v (%v), want 0600", filepath.Base(path), info.Mode().Perm(), err)
		}
	}

	t.Run("reuse off", func(t *testing.T) {
		isolateRunHome(t)
		fake := newCycleFake(map[string]runner.NodeOutcome{
			"plan-1": {Result: cycleSpec},
			"work-1": {SessionID: "s-1", Result: "PASS"},
		})
		var out strings.Builder
		captureStdout(t, func() {
			coord := coordinator.New(fake, coordinator.WithInvocationDir(dir), coordinator.WithoutReuse())
			if err := planAndExecute(context.Background(), &out, coord, fake,
				commonRunFlags{inputs: inputFlag{}}, "audit the readme", singleCycle, false, nil, nil); err != nil {
				t.Fatalf("planAndExecute: %v", err)
			}
		})
		if _, err := os.Stat(filepath.Join(soleRunDir(t), coordinator.ReuseCatalogFileName)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s written with reuse off (%v)", coordinator.ReuseCatalogFileName, err)
		}
	})
}

// #338 C.2 at the CLI: the cited file is rewritten while the planner is
// answering, so the splice-time digest no longer matches. The run fails naming
// the path, and the paid-for reply is kept under rejectedSpecFileName in the
// run's own directory — the same path any refused plan takes — with no
// graph.json and no reuse-catalog.json beside it.
func TestPlanAndExecute_ReuseDigestMismatchKeepsTheRejectedSpec(t *testing.T) {
	isolateRunHome(t)
	dir, source := plantReuseCatalog(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{"plan-1": {Result: reuseCitingSpec, TotalCostUSD: 0.02}})
	keyFn := fake.KeyFn
	fake.KeyFn = func(spec runner.NodeInvocation) string {
		rewritten := strings.Replace(reuseProbeFragment, "and report", "and report that all checks passed", 1)
		if err := os.WriteFile(source, []byte(rewritten), 0o644); err != nil {
			t.Error(err)
		}
		return keyFn(spec)
	}

	var out strings.Builder
	var err error
	captureStdout(t, func() {
		err = planAndExecute(context.Background(), &out, coordinator.New(fake, coordinator.WithInvocationDir(dir)), fake,
			commonRunFlags{inputs: inputFlag{}}, "audit the readme", singleCycle, false, nil, nil)
	})
	var rejection *coordinator.PlanRejection
	if !errors.As(err, &rejection) || !strings.Contains(err.Error(), "changed after the menu was rendered") || !strings.Contains(err.Error(), "probe.yaml") {
		t.Fatalf("err = %v, want the digest-mismatch rejection", err)
	}
	runDir := soleRunDir(t)
	kept, readErr := os.ReadFile(filepath.Join(runDir, rejectedSpecFileName))
	if readErr != nil || !strings.Contains(string(kept), `"reuse": "probe"`) {
		t.Fatalf("rejected spec = %q (%v), want the planner's reply", kept, readErr)
	}
	if !strings.Contains(out.String(), "The rejected spec is kept at") {
		t.Errorf("the output does not name the kept spec:\n%s", out.String())
	}
	for _, name := range []string{generatedSpecFileName, coordinator.ReuseCatalogFileName} {
		if _, statErr := os.Stat(filepath.Join(runDir, name)); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("%s written for a refused plan (%v)", name, statErr)
		}
	}
	if n := len(fake.Invocations()); n != 1 {
		t.Errorf("the runner saw %d calls, want only the planner: nothing may run", n)
	}
}
