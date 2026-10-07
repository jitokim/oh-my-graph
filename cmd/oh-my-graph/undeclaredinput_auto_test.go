package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// #356 on `auto`: no graph exists at load, so a bound key is judged on the
// plan screen against the input names the PLANNED graph declares or
// references, and warns only as a near miss of one of them — the planner saw
// every bound input and may simply not need one. Driven through the scripted
// planner of goalcycle_test.go; secretValue356 is shared with
// undeclaredinput_wiring_test.go.

// repoCycleSpec356 is cycleSpec with `inputs: [repo]` declared.
const repoCycleSpec356 = `{"name":"cycle-work","version":"1","inputs":["repo"],"nodes":[` +
	`{"id":"work","prompt":"work","allowed_tools":["Read"]}]}`

// repoRefSpec356 is cycleSpec whose prompt references {{ inputs.repo }}
// without declaring it — the shape a planner most often writes.
const repoRefSpec356 = `{"name":"cycle-work","version":"1","nodes":[` +
	`{"id":"work","prompt":"work on {{ inputs.repo }}","allowed_tools":["Read"]}]}`

// planScreenLine356 is the undeclared-input text after the "warning: <spec>: "
// prefix, which names a plans/ or runs/ path the test does not know up front.
func planScreenLine356(key, source string) string {
	return "input \"" + key + "\" (from " + source + ") is not declared in the graph's inputs list; it is bound anyway"
}

// autoPlanOnly356 runs `auto --plan-only` with the planner replying spec and
// returns stdout.
func autoPlanOnly356(t *testing.T, spec string, args ...string) string {
	t.Helper()
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {ExitCode: 0, Result: spec, TotalCostUSD: 0.01},
	})
	var err error
	out := captureStdout(t, func() {
		err = runAutoWith(append([]string{"add a README section", "--plan-only", "--no-agent-mapping", "--no-skill-mapping"}, args...),
			fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("--plan-only must succeed whatever is bound: %v", err)
	}
	if strings.Contains(out, secretValue356) {
		t.Errorf("a bound value leaked onto the plan screen:\n%s", out)
	}
	return out
}

// #356: a plan that declares and references no input has nothing a key could
// be a typo of, so nothing warns — not repo, not its near miss reop.
func TestAutoPlanOnly356_PlanUsingNoInputPrintsNothing(t *testing.T) {
	out := autoPlanOnly356(t, cycleSpec,
		"--input", "repo="+secretValue356, "--input", "reop="+secretValue356)
	if strings.Contains(out, "is not declared in the graph's inputs list") {
		t.Errorf("a plan using no input warned about a bound key:\n%s", out)
	}
}

func TestAutoPlanOnly356_NearMissOfAReferencedKeyWarnsOnThePlanScreen(t *testing.T) {
	file := writeInputFile(t, "in.yaml", "zzz: "+secretValue356+"\n")
	out := autoPlanOnly356(t, repoRefSpec356,
		"--input", "reop="+secretValue356, "--input-file", file)

	screen := strings.Index(out, "Planned graph ")
	stop := strings.Index(out, "plan only: no node was executed")
	if screen < 0 || stop < 0 {
		t.Fatalf("plan screen or plan-only note missing:\n%s", out)
	}
	want := planScreenLine356("reop", "--input") + ` — did you mean "repo"?`
	at := strings.Index(out, want)
	if at < 0 {
		t.Fatalf("output missing %q:\n%s", want, out)
	}
	if at < screen || at > stop {
		t.Errorf("%q printed at %d, outside the plan screen [%d, %d]:\n%s", want, at, screen, stop, out)
	}
	lineStart := strings.LastIndex(out[:at], "\n") + 1
	if !strings.HasPrefix(out[lineStart:], "warning: ") {
		t.Errorf("the line must carry the warning: prefix, got %q", out[lineStart:at+len(want)])
	}
	// zzz is undeclared and unreferenced but near nothing: on `auto` that is
	// no typo signal.
	if strings.Contains(out, `input "zzz"`) {
		t.Errorf("zzz, near no name the plan uses, was warned about:\n%s", out)
	}
}

func TestAutoPlanOnly356_ReferencedKeyPrintsNothing(t *testing.T) {
	out := autoPlanOnly356(t, repoRefSpec356, "--input", "repo="+secretValue356)
	if strings.Contains(out, "is not declared in the graph's inputs list") {
		t.Errorf("a key the planned graph references was warned about:\n%s", out)
	}
}

func TestAutoPlanOnly356_DeclaredKeyPrintsNothing(t *testing.T) {
	out := autoPlanOnly356(t, repoCycleSpec356, "--input", "repo="+secretValue356)
	if strings.Contains(out, "is not declared in the graph's inputs list") {
		t.Errorf("a key the planned graph declares was warned about:\n%s", out)
	}
}

// #356: a reference counts wherever the engine interpolates, not only in a
// prompt. A planner may set neither cwd nor verify itself — they reach a
// planned graph through a spliced reuse citation or an attached
// --verify-cmd — so the plan is built directly and handed to the plan screen.
func TestPrintPlanForRuntime356_NearMissOfACwdOrVerifyReference(t *testing.T) {
	for name, node := range map[string]string{
		"cwd":    `{"id":"work","prompt":"work","cwd":"{{ inputs.repo }}","allowed_tools":["Read"]}`,
		"verify": `{"id":"work","prompt":"work","allowed_tools":["Read"],"success_check":{"verify":{"command":"test -d {{ inputs.repo }}"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			g, err := graph.Parse([]byte(`{"name":"p","version":"1","nodes":[` + node + `]}`))
			if err != nil {
				t.Fatal(err)
			}
			var out strings.Builder
			printPlanForRuntime(&out, coordinator.Plan{Graph: g}, "", runner.RuntimeClaude, nil, false, conventionsDisclosure{},
				map[string]string{"reop": "--input"})
			want := "warning: " + planScreenLine356("reop", "--input") + ` — did you mean "repo"?`
			if !strings.Contains(out.String(), want) {
				t.Errorf("screen lacks %q:\n%s", want, out.String())
			}
		})
	}
}

// #356: a reuse citation's bind: values are a reference site too. The
// coordinator splices them into the cited shape's fields before the plan
// reaches the screen, so a {{ inputs.repo }} that appears ONLY in a bind:
// value is a name the plan uses, and its near miss reop warns. Driven through
// the real catalog and splice: probe from reusesplice_test.go, planted where
// the coordinator scans.
func TestAutoPlanOnly356_NearMissOfABindOnlyReference(t *testing.T) {
	isolateRunHome(t)
	dir, _ := plantReuseCatalog(t)
	const spec = `{"name":"reuse-work","nodes":[{"id":"cite","reuse":"probe","bind":{"target":"{{ inputs.repo }}"}}]}`
	if strings.Contains(strings.Replace(spec, `"bind":{"target":"{{ inputs.repo }}"}`, "", 1), "inputs.") {
		t.Fatal("the reference must appear only inside the bind: value")
	}
	fake := newCycleFake(map[string]runner.NodeOutcome{"plan-1": {Result: spec, TotalCostUSD: 0.01}})
	flags := commonRunFlags{
		inputs:       inputFlag{"reop": secretValue356},
		inputSources: map[string]string{"reop": "--input"},
	}
	var out strings.Builder
	var err error
	stdout := captureStdout(t, func() {
		err = planAndExecute(context.Background(), &out, coordinator.New(fake, coordinator.WithInvocationDir(dir)), fake,
			flags, "audit the readme", singleCycle, true, nil, nil)
	})
	if err != nil {
		t.Fatalf("--plan-only must succeed whatever is bound: %v\n%s", err, out.String())
	}
	all := out.String() + stdout
	want := planScreenLine356("reop", "--input") + ` — did you mean "repo"?`
	at := strings.Index(all, want)
	if at < 0 {
		t.Fatalf("screen lacks %q:\n%s", want, all)
	}
	if lineStart := strings.LastIndex(all[:at], "\n") + 1; !strings.HasPrefix(all[lineStart:], "warning: ") {
		t.Errorf("the line must carry the warning: prefix, got %q", all[lineStart:at+len(want)])
	}
	if strings.Contains(all, secretValue356) {
		t.Errorf("a bound value leaked:\n%s", all)
	}
}

func TestAuto356_WarningPrecedesAnyNodeRun(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1":      {ExitCode: 0, Result: repoRefSpec356, TotalCostUSD: 0.01},
		"work on r-1": {SessionID: "s-work-1", Result: "PASS", ExitCode: 0},
	})
	// The out-writer as it stood when the first planned node was launched.
	// executePlan writes nothing to it, so it is settled by then.
	var out bytes.Buffer
	beforeNode, nodeRan := "", false
	key := fake.KeyFn
	fake.KeyFn = func(spec runner.NodeInvocation) string {
		k := key(spec)
		if strings.HasPrefix(k, "work on ") && !nodeRan {
			beforeNode, nodeRan = out.String(), true
		}
		return k
	}

	flags := commonRunFlags{
		inputs:       inputFlag{"repo": "r", "reop": secretValue356},
		inputSources: map[string]string{"repo": "--input", "reop": "--input"},
	}
	var err error
	stdout := captureStdout(t, func() {
		err = planAndExecute(context.Background(), &out, coordinator.New(fake), fake, flags,
			"add a README section", goalCycleOptions{maxCycles: 1}, false, nil, nil)
	})
	if err != nil {
		t.Fatalf("a near-miss key must not change the run's outcome: %v", err)
	}
	if !nodeRan {
		t.Fatal("the planned node never ran")
	}
	if !strings.Contains(beforeNode, planScreenLine356("reop", "--input")) {
		t.Errorf("the warning was not printed before the first node ran; out at that moment:\n%s", beforeNode)
	}
	if strings.Contains(out.String()+stdout, secretValue356) {
		t.Errorf("a bound value leaked:\n%s%s", out.String(), stdout)
	}
}

// #356: in a goal loop every cycle's plan is judged, but each near-miss key
// warns once across the whole loop, and a key is spent only by a line it
// actually printed. repo and ticket are bound too, so every planned node
// resolves and the loop runs clean; neither warns. Both plans reference repo, so reop warns on cycle 1 only.
// Only cycle 2's plan references ticket, so tickt — near nothing on cycle 1,
// and so not spent there — warns on cycle 2. zzz is near nothing either time.
func TestAutoGoalLoop356_EachNearMissKeyWarnsOnce(t *testing.T) {
	isolateRunHome(t)
	const plan2 = `{"name":"cycle-work","version":"1","nodes":[` +
		`{"id":"work","prompt":"work on {{ inputs.repo }} for {{ inputs.ticket }}","allowed_tools":["Read"]}]}`
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1":            {Result: repoRefSpec356, TotalCostUSD: 0.01},
		"work on r-1":       {SessionID: "s-work-1", Result: "PASS", ExitCode: 0},
		"assess-1":          {Result: cycleAssessNotMet},
		"plan-2":            {Result: plan2, TotalCostUSD: 0.01},
		"work on r for t-2": {SessionID: "s-work-2", Result: "PASS", ExitCode: 0},
		"assess-2":          {Result: cycleAssessMet},
	})

	flags := commonRunFlags{
		inputs: inputFlag{"repo": "r", "ticket": "t",
			"reop": secretValue356, "tickt": secretValue356, "zzz": secretValue356},
		inputSources: map[string]string{"repo": "--input", "ticket": "--input",
			"reop": "--input", "tickt": "--input", "zzz": "--input"},
	}
	var out bytes.Buffer
	var err error
	stdout := captureStdout(t, func() {
		err = planAndExecute(context.Background(), &out, coordinator.New(fake), fake, flags,
			"add a README section", goalCycleOptions{maxCycles: 2}, false, nil, nil)
	})
	if err != nil {
		t.Fatalf("a met goal must exit clean whatever was bound: %v", err)
	}
	all := out.String() + stdout

	cycle2 := strings.Index(all, "— goal cycle 2/2")
	if cycle2 < 0 {
		t.Fatalf("the loop never reached cycle 2:\n%s", all)
	}
	for key, want := range map[string]struct {
		near   string
		cycle2 bool
	}{"reop": {"repo", false}, "tickt": {"ticket", true}} {
		line := planScreenLine356(key, "--input") + ` — did you mean "` + want.near + `"?`
		if n := strings.Count(all, line); n != 1 {
			t.Errorf("%s warned %d times across the loop, want exactly once:\n%s", key, n, all)
			continue
		}
		if inCycle2 := strings.Index(all, line) > cycle2; inCycle2 != want.cycle2 {
			t.Errorf("%s warned in cycle 2 = %v, want %v:\n%s", key, inCycle2, want.cycle2, all)
		}
	}
	for _, key := range []string{"zzz", "repo", "ticket"} {
		if strings.Contains(all, `input "`+key+`"`) {
			t.Errorf("%s, no near miss of a name either plan uses, was warned about:\n%s", key, all)
		}
	}
	if strings.Contains(all, secretValue356) {
		t.Errorf("a bound value leaked:\n%s", all)
	}
}
