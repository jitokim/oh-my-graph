package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// #356 on `auto`: no graph exists at load, so a bound key is judged against
// the PLANNED graph's inputs: list, on the plan screen. Driven through the
// scripted planner of goalcycle_test.go; secretValue356 is shared with
// undeclaredinput_wiring_test.go.

// repoCycleSpec356 is cycleSpec with `inputs: [repo]` declared.
const repoCycleSpec356 = `{"name":"cycle-work","version":"1","inputs":["repo"],"nodes":[` +
	`{"id":"work","prompt":"work","allowed_tools":["Read"]}]}`

// planScreenLine356 is the undeclared-input text after the "warning: <spec>: "
// prefix, which names a plans/ or runs/ path the test does not know up front.
func planScreenLine356(key, source string) string {
	return "input \"" + key + "\" (from " + source + ") is not declared in the graph's inputs list; it is bound anyway"
}

func TestAutoPlanOnly356_UndeclaredKeyWarnsOnThePlanScreen(t *testing.T) {
	isolateRunHome(t)
	file := writeInputFile(t, "in.yaml", "zzz: "+secretValue356+"\n")
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {ExitCode: 0, Result: repoCycleSpec356, TotalCostUSD: 0.01},
	})

	var err error
	out := captureStdout(t, func() {
		err = runAutoWith([]string{"add a README section", "--plan-only", "--no-agent-mapping", "--no-skill-mapping",
			"--input", "repo=/r", "--input", "reop=" + secretValue356, "--input-file", file},
			fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("--plan-only with an undeclared key must still succeed: %v", err)
	}

	screen := strings.Index(out, "Planned graph ")
	stop := strings.Index(out, "plan only: no node was executed")
	if screen < 0 || stop < 0 {
		t.Fatalf("plan screen or plan-only note missing:\n%s", out)
	}
	for _, want := range []string{
		planScreenLine356("reop", "--input") + ` — did you mean "repo"?`,
		planScreenLine356("zzz", file),
	} {
		at := strings.Index(out, want)
		if at < 0 {
			t.Errorf("output missing %q:\n%s", want, out)
			continue
		}
		if at < screen || at > stop {
			t.Errorf("%q printed at %d, outside the plan screen [%d, %d]:\n%s", want, at, screen, stop, out)
		}
		lineStart := strings.LastIndex(out[:at], "\n") + 1
		if !strings.HasPrefix(out[lineStart:], "warning: ") {
			t.Errorf("the line must carry the warning: prefix, got %q", out[lineStart:at+len(want)])
		}
	}
	if strings.Contains(out, `input "repo"`) {
		t.Errorf("declared key repo was warned about:\n%s", out)
	}
	if strings.Contains(out, secretValue356) {
		t.Errorf("a bound value leaked onto the plan screen:\n%s", out)
	}
}

func TestAutoPlanOnly356_DeclaredKeyPrintsNothing(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {ExitCode: 0, Result: repoCycleSpec356, TotalCostUSD: 0.01},
	})

	var err error
	out := captureStdout(t, func() {
		err = runAutoWith([]string{"add a README section", "--plan-only", "--no-agent-mapping", "--no-skill-mapping",
			"--input", "repo=" + secretValue356},
			fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("--plan-only: %v", err)
	}
	if strings.Contains(out, "is not declared in the graph's inputs list") {
		t.Errorf("a key the planned graph declares was warned about:\n%s", out)
	}
	if strings.Contains(out, secretValue356) {
		t.Errorf("a bound value leaked onto the plan screen:\n%s", out)
	}
}

func TestAuto356_WarningPrecedesAnyNodeRun(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {ExitCode: 0, Result: repoCycleSpec356, TotalCostUSD: 0.01},
		"work-1": {SessionID: "s-work-1", Result: "PASS", ExitCode: 0},
	})
	// The out-writer as it stood when the first planned node was launched.
	// executePlan writes nothing to it, so it is settled by then.
	var out bytes.Buffer
	beforeNode, nodeRan := "", false
	key := fake.KeyFn
	fake.KeyFn = func(spec runner.NodeInvocation) string {
		k := key(spec)
		if strings.HasPrefix(k, "work-") && !nodeRan {
			beforeNode, nodeRan = out.String(), true
		}
		return k
	}

	flags := commonRunFlags{
		inputs:       inputFlag{"zzz": secretValue356},
		inputSources: map[string]string{"zzz": "--input"},
	}
	var err error
	stdout := captureStdout(t, func() {
		err = planAndExecute(context.Background(), &out, coordinator.New(fake), fake, flags,
			"add a README section", goalCycleOptions{maxCycles: 1}, false, nil, nil)
	})
	if err != nil {
		t.Fatalf("an undeclared key must not change the run's outcome: %v", err)
	}
	if !nodeRan {
		t.Fatal("the planned node never ran")
	}
	if !strings.Contains(beforeNode, planScreenLine356("zzz", "--input")) {
		t.Errorf("the warning was not printed before the first node ran; out at that moment:\n%s", beforeNode)
	}
	if strings.Contains(out.String()+stdout, secretValue356) {
		t.Errorf("a bound value leaked:\n%s%s", out.String(), stdout)
	}
}
