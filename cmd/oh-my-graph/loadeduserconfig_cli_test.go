package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// ADR 0032 at the CLI layer: the flag reaching the policies, the choice
// surviving a second process, and the one disclosure slot saying exactly one
// of two things.
//
// The policy shape itself is pinned in internal/coordinator and its two argv
// renderings in internal/runner. What is only checkable here is the wiring
// between them, and the two places it has historically broken: a screen that
// disagrees with the argv, and a recorder that rewrites the whole snapshot on
// every settled node and drops a field on the way through.

// The two literals ADR 0032 §2.6 fixes, and the two they replace. They are
// whole paragraphs rather than phrases on purpose: a disclosure test that
// matches a prefix is how a sentence loses its second half, and the second
// halves here are the bill — the grants on Claude, the surviving sandbox on
// Codex.
const (
	loadedClaudeNote = "  Planned nodes run with YOUR configuration (--accept-loaded-user-config): your user, project\n" +
		"  and local settings load, and with them your CLAUDE.md, your hooks and your MCP servers.\n" +
		"  Your standing permission grants load too, so a declared scope like Bash(git *) is a\n" +
		"  declaration again and not a limit — a call your own settings allow will run. Each node's\n" +
		"  --tools set and its deny list still bind. Enterprise and managed policy are unaffected by\n" +
		"  this flag and cannot be widened by it. Agent mapping and skill activation are off for this\n" +
		"  run: a staged definition would be shadowed by a same-named one your settings discover.\n"

	loadedCodexNote = "  Planned nodes run with YOUR configuration (--accept-loaded-user-config): ~/.codex/config.toml,\n" +
		"  your repository's rules and AGENTS.md files, and your MCP servers all load. The filesystem\n" +
		"  sandbox above and approval_policy=\"never\" are argv on every node, so this flag widens neither.\n"

	isolatedClaudeNote = "  Planned nodes run isolated: none of your user/project/local settings load, so a declared\n" +
		"  scope like Bash(git *) is enforced rather than merely requested — and your CLAUDE.md,\n" +
		"  hooks and MCP servers are unavailable to them. See SECURITY.md for what this does not cover.\n"

	isolatedCodexNote = "  Auto-planned Codex nodes also ignore user configuration, repository rules, project instructions, and MCP servers.\n"
)

// optInFlag is spelled out once, so a rename that misses a test file fails to
// compile rather than silently testing a flag that no longer exists.
const optInFlag = "--accept-loaded-user-config"

// planPolicies is a two-node plan's policy map, built the way `auto` builds it
// so the disclosure tests below drive the real predicate rather than a
// hand-set boolean.
func planPolicies(t *testing.T, loaded bool) coordinator.Plan {
	t.Helper()
	g, err := graph.Parse([]byte(`{"name":"p","version":"1","nodes":[` +
		`{"id":"scan","prompt":"scan","allowed_tools":["Read"]},` +
		`{"id":"fix","prompt":"fix","allowed_tools":["Edit"],"depends_on":["scan"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	policies := map[string]runner.ToolPolicy{}
	for _, node := range g.Nodes {
		policy := runner.ToolPolicy{AllowedTools: node.AllowedTools, Tools: []string{"Read"}, StrictMCPConfig: true}
		if !loaded {
			none := ""
			policy.SettingSources = &none
		} else {
			policy.StrictMCPConfig = false
		}
		policies[node.ID] = policy
	}
	return coordinator.Plan{Graph: g, ToolPolicies: policies}
}

// snapshotToolPolicies is the raw `tool_policies` object as it sits on disk,
// decoded loosely so a test can ask whether a KEY is present — which is the
// question, since ADR 0032 §2.7 records the choice as the ABSENCE of
// setting_sources rather than as a value.
func snapshotToolPolicies(t *testing.T, runID string) map[string]map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(runDirFor(runID), stateFileName))
	if err != nil {
		t.Fatalf("read state.json: %v", err)
	}
	var snap struct {
		ToolPolicies map[string]map[string]json.RawMessage `json:"tool_policies"`
	}
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("decode state.json: %v", err)
	}
	if len(snap.ToolPolicies) == 0 {
		t.Fatalf("state.json for %q records no tool_policies at all:\n%s", runID, raw)
	}
	return snap.ToolPolicies
}

// --- 1. the resumed leg does not erase the choice -----------------------------

// TestResume_DoesNotEraseTheLoadedConfigChoice is ADR 0032 §3(10), and it is
// written first because it is the defect this feature is most likely to ship
// with. `resume` rewrites the WHOLE snapshot on every settled node
// (runstate.NewSnapshotRecorder), so a field the second leg does not carry
// forward is a field the first settling node deletes — exactly what happened
// to ADR 0030's build_evidence.
//
// Here the "field" is an absence, which makes it sharper still: setting_sources
// is omitempty, so the choice is recorded by NOT being there, and any leg that
// rebuilt a policy from a default would put it back and silently re-isolate a
// run the operator opted out of. The precondition asserts the key is already
// absent before the resume, so this is a test of what the second leg does and
// not of what the first one wrote.
func TestResume_DoesNotEraseTheLoadedConfigChoice(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.04},
		"work-1": {ExitCode: 1, FailureCause: limitCauseMsg, SessionLimited: true},
		"work-2": {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.5},
	})

	var err error
	captureStdout(t, func() {
		err = runAutoWith([]string{"tidy the docs", optInFlag, "--accept-no-build-evidence"},
			fake, browser.NewFakeOpener(), os.Stdout)
	})
	var limited *schedule.LimitPausedError
	if !errors.As(err, &limited) {
		t.Fatalf("the first leg should pause on the session limit, got %T: %v", err, err)
	}
	runID := soleRunID(t)

	for id, policy := range snapshotToolPolicies(t, runID) {
		if _, present := policy["setting_sources"]; present {
			t.Fatalf("precondition failed: node %q recorded setting_sources = %s, so the opt-in never reached the snapshot", id, policy["setting_sources"])
		}
	}

	var resumeErr error
	captureStdout(t, func() {
		resumeErr = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), fake, nil)
	})
	if resumeErr != nil {
		t.Fatalf("the retry leg should finish the run cleanly: %v", resumeErr)
	}

	after := snapshotToolPolicies(t, runID)
	for id, policy := range after {
		if raw, present := policy["setting_sources"]; present {
			t.Errorf("the resumed leg wrote setting_sources = %s back onto node %q; a second process silently re-isolated a run the operator opted out of", raw, id)
		}
		// Paired positive: the policy is still a POLICY. A leg that dropped
		// the whole entry, or wrote an empty object, would satisfy the
		// assertion above while losing the ceiling entirely.
		if _, present := policy["allowed_tools"]; !present {
			t.Errorf("node %q's recorded policy lost allowed_tools: %v", id, policy)
		}
	}
	if len(after) != 1 {
		t.Errorf("tool_policies has %d entries after the resume, want 1 for this fixture's single node", len(after))
	}
}

// --- 2. resume inherits the choice, and says so -------------------------------

// TestResume_InheritsTheChoiceAndDisclosesIt is ADR 0032 §3(9). Two claims,
// and the second is the one with no other home: a resumed leg spawns
// config-carrying nodes with the first leg's terminal long gone, so the screen
// it prints is the only place that fact appears.
//
// `resume` registers no flag to make this choice and must not (§2.7) — it
// reads the policies it rehydrated. The control arm is a run that did NOT opt
// in: the same resume path must spawn an isolated node and print nothing,
// which is what proves the line is driven by the snapshot rather than printed
// on every resume.
func TestResume_InheritsTheChoiceAndDisclosesIt(t *testing.T) {
	for _, tc := range []struct {
		name       string
		autoArgs   []string
		wantLoaded bool
	}{
		{name: "the first leg opted in", autoArgs: []string{optInFlag}, wantLoaded: true},
		{name: "the first leg did not", wantLoaded: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunHome(t)
			fake := newCycleFake(map[string]runner.NodeOutcome{
				"plan-1": {Result: cycleSpec, TotalCostUSD: 0.04},
				"work-1": {ExitCode: 1, FailureCause: limitCauseMsg, SessionLimited: true},
				"work-2": {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.5},
			})

			args := append([]string{"tidy the docs", "--accept-no-build-evidence"}, tc.autoArgs...)
			var err error
			captureStdout(t, func() {
				err = runAutoWith(args, fake, browser.NewFakeOpener(), os.Stdout)
			})
			var limited *schedule.LimitPausedError
			if !errors.As(err, &limited) {
				t.Fatalf("the first leg should pause on the session limit, got %T: %v", err, err)
			}
			runID := soleRunID(t)

			before := len(fake.Invocations())
			captureStdout(t, func() {
				if resumeErr := executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), fake, nil); resumeErr != nil {
					t.Errorf("resume: %v", resumeErr)
				}
			})

			// What the resumed leg actually spawned, which is the half a
			// screen assertion cannot stand in for.
			spawned := fake.Invocations()[before:]
			if len(spawned) == 0 {
				t.Fatal("the resumed leg spawned nothing, so neither half below was checked")
			}
			for _, spec := range spawned {
				switch loaded := spec.Policy.SettingSources == nil; {
				case tc.wantLoaded && !loaded:
					t.Errorf("the resumed node re-isolated: SettingSources = %q, want nil inherited from the snapshot", *spec.Policy.SettingSources)
				case !tc.wantLoaded && loaded:
					t.Errorf("the resumed node lost layer 1 on a run that never opted in: %+v", spec.Policy)
				}
			}
		})
	}
}

// TestResume_PrintsTheInheritedDisclosureBeforeTheBanner is the screen half of
// the test above, split out because it needs the resumed leg's stdout and the
// two arms have to be compared against each other's literal.
func TestResume_PrintsTheInheritedDisclosureBeforeTheBanner(t *testing.T) {
	for _, tc := range []struct {
		name        string
		autoArgs    []string
		want, avoid string
	}{
		{name: "opted in", autoArgs: []string{optInFlag}, want: loadedClaudeNote},
		{name: "not opted in", avoid: loadedClaudeNote},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunHome(t)
			fake := newCycleFake(map[string]runner.NodeOutcome{
				"plan-1": {Result: cycleSpec, TotalCostUSD: 0.04},
				"work-1": {ExitCode: 1, FailureCause: limitCauseMsg, SessionLimited: true},
				"work-2": {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.5},
			})
			args := append([]string{"tidy the docs", "--accept-no-build-evidence"}, tc.autoArgs...)
			captureStdout(t, func() {
				_ = runAutoWith(args, fake, browser.NewFakeOpener(), os.Stdout)
			})
			runID := soleRunID(t)

			out := captureStdout(t, func() {
				if err := executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), fake, nil); err != nil {
					t.Errorf("resume: %v", err)
				}
			})

			if tc.want != "" {
				if !strings.Contains(out, tc.want) {
					t.Errorf("the resumed leg never disclosed the inherited configuration:\n%s", out)
				}
				banner := strings.Index(out, "Running graph")
				note := strings.Index(out, tc.want)
				if banner >= 0 && note > banner {
					t.Errorf("the disclosure printed AFTER the banner (note at %d, banner at %d); it belongs with the leg's other differences:\n%s", note, banner, out)
				}
			}
			if tc.avoid != "" && strings.Contains(out, tc.avoid) {
				t.Errorf("a resume of a run that never opted in claimed it loads the operator's configuration:\n%s", out)
			}
		})
	}
}

// --- 3. the flag reaches the spawned policies, and only when typed ------------

// TestRunAutoWith_TheFlagDecidesWhatEveryPlannedNodeIsSpawnedWith joins the
// flag to the invocation the scheduler builds. Both arms assert the same two
// fields, so neither can pass by the ceiling having quietly stopped existing:
// with no flag, every planned node is spawned with layer 1 as a pointer to ""
// and layer 4 on; with the flag, neither.
func TestRunAutoWith_TheFlagDecidesWhatEveryPlannedNodeIsSpawnedWith(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		wantLoaded bool
	}{
		{name: "no flag: today's ceiling, unchanged", wantLoaded: false},
		{name: optInFlag, args: []string{optInFlag}, wantLoaded: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunHome(t)
			fake := newCycleFake(map[string]runner.NodeOutcome{
				"plan-1": {Result: cycleSpec, TotalCostUSD: 0.04},
				"work-1": {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.5},
			})

			args := append([]string{"tidy the docs", "--accept-no-build-evidence"}, tc.args...)
			captureStdout(t, func() {
				if err := runAutoWith(args, fake, browser.NewFakeOpener(), os.Stdout); err != nil {
					t.Fatalf("auto: %v", err)
				}
			})

			nodes := 0
			for _, spec := range fake.Invocations() {
				if strings.Contains(spec.Prompt, "planning coordinator") || strings.Contains(spec.Prompt, "goal assessor") {
					continue
				}
				nodes++
				if tc.wantLoaded {
					if spec.Policy.SettingSources != nil {
						t.Errorf("planned node kept layer 1 under %s: %q", optInFlag, *spec.Policy.SettingSources)
					}
					if spec.Policy.StrictMCPConfig {
						t.Errorf("planned node kept layer 4 under %s", optInFlag)
					}
				} else {
					if spec.Policy.SettingSources == nil {
						t.Errorf("planned node lost layer 1 with no flag typed; the default moved")
					} else if *spec.Policy.SettingSources != "" {
						t.Errorf("layer 1 = %q, want the empty string", *spec.Policy.SettingSources)
					}
					if !spec.Policy.StrictMCPConfig {
						t.Errorf("planned node lost layer 4 with no flag typed")
					}
				}
				// Both arms: the tool ceiling is still there. This is what
				// makes the opted-in door narrower than `run`'s.
				if !slices.Contains(spec.Policy.AllowedTools, "Read") || len(spec.Policy.Tools) == 0 || len(spec.Policy.DisallowedTools) == 0 {
					t.Errorf("layers 2/3/5 = %v / %v / %v, want all three intact", spec.Policy.AllowedTools, spec.Policy.Tools, spec.Policy.DisallowedTools)
				}
			}
			if nodes == 0 {
				t.Fatal("no planned node was spawned, so nothing above was checked")
			}
		})
	}
}

// TestRunAutoWith_TheOptInReachesTheRealArgv is the seam the test above stops
// one short of. skillargv_test.go's own header names the failure it exists
// against — "the plan printout says ENABLED and the node runs without it" — and
// a widening flag has the sharper version of it: a policy that de-escalates on
// paper while the process is still spawned with --setting-sources "".
//
// So this drives the real `auto` path with a real CLIRunner against a stub
// binary that records the bytes it was spawned with, and asserts on those.
// Both arms, because "no --setting-sources in the argv" is trivially true of a
// build where the ceiling stopped being rendered at all.
func TestRunAutoWith_TheOptInReachesTheRealArgv(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		wantIsolat bool
	}{
		{name: "no flag: the ceiling is on the argv", wantIsolat: true},
		{name: optInFlag, args: []string{optInFlag}, wantIsolat: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, spawns := runAutoCapturingArgv(t, tc.args...)
			argv := nodeArgv(t, spawns, "draft the proposal")

			if _, present := argv.value("--setting-sources"); present != tc.wantIsolat {
				t.Errorf("layer 1 present = %t, want %t\nargv: %q", present, tc.wantIsolat, argv)
			}
			if present := argv.has("--strict-mcp-config"); present != tc.wantIsolat {
				t.Errorf("layer 4 present = %t, want %t\nargv: %q", present, tc.wantIsolat, argv)
			}
			// Both arms: the tool ceiling still reaches the process, which is
			// the claim that makes this door narrower than `run`'s.
			if tools := argv.tools(); !slices.Contains(tools, "Read") {
				t.Errorf("--tools = %v, want the node's declared set\nargv: %q", tools, argv)
			}
			if !argv.has("--disallowedTools") || !argv.has("--allowedTools") {
				t.Errorf("layers 2 and 5 did not reach the process\nargv: %q", argv)
			}
			// And under the opt-in, nothing was staged for it to be shadowed:
			// agent mapping and skill activation go off with the widening.
			if !tc.wantIsolat {
				if argv.has("--plugin-dir") || argv.has("--agent") {
					t.Errorf("an opted-in node was spawned with a staged definition your own settings could shadow\nargv: %q", argv)
				}
				if tools := argv.tools(); slices.Contains(tools, coordinator.SkillToolName) {
					t.Errorf("--tools = %v, want no %s under the opt-in", tools, coordinator.SkillToolName)
				}
			}
		})
	}
}

// --- 4. the disclosure slot ---------------------------------------------------

// TestPrintPlanForRuntime_TheSlotSaysExactlyOneOfTheTwoSentences is ADR 0032
// §3(6): four cases, {Claude, Codex} × {isolated, opted in}, each asserting
// the whole literal it must print AND the whole literal it must not. Never
// both, never neither.
//
// The predicate under test reads plan.ToolPolicies, so these cases are driven
// by the same value the argv is built from — a screen that agreed with a flag
// but disagreed with the policies is the failure this shape rules out.
func TestPrintPlanForRuntime_TheSlotSaysExactlyOneOfTheTwoSentences(t *testing.T) {
	for _, tc := range []struct {
		name        string
		runtime     runner.Runtime
		loaded      bool
		want, avoid string
	}{
		{name: "claude, isolated", runtime: runner.RuntimeClaude, want: isolatedClaudeNote, avoid: loadedClaudeNote},
		{name: "claude, opted in", runtime: runner.RuntimeClaude, loaded: true, want: loadedClaudeNote, avoid: isolatedClaudeNote},
		{name: "codex, isolated", runtime: runner.RuntimeCodex, want: isolatedCodexNote, avoid: loadedCodexNote},
		{name: "codex, opted in", runtime: runner.RuntimeCodex, loaded: true, want: loadedCodexNote, avoid: isolatedCodexNote},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			printPlanForRuntime(&out, planPolicies(t, tc.loaded), "", tc.runtime, nil)
			got := out.String()

			if !strings.Contains(got, tc.want) {
				t.Errorf("the slot did not print its sentence in full.\nwant:\n%s\ngot:\n%s", tc.want, got)
			}
			if strings.Contains(got, tc.avoid) {
				t.Errorf("the slot printed the OTHER sentence too, so the screen says two contradictory things:\n%s", got)
			}
		})
	}
}

// TestPrintPlanForRuntime_HandWrittenGraphsClaimNoFlagNobodyTyped is the third
// value nodeConfigPosture exists for. `run`, `--dry-run` and `lint` describe a
// graph the USER wrote, whose nodes have always carried the user's own
// configuration — with no flag, because there was never a ceiling to opt out
// of. Naming --accept-loaded-user-config there would be a screen inventing an
// argument nobody passed.
func TestPrintPlanForRuntime_HandWrittenGraphsClaimNoFlagNobodyTyped(t *testing.T) {
	g, err := graph.Parse([]byte(`{"name":"h","version":"1","nodes":[{"id":"work","prompt":"p","allowed_tools":["Read"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	noteCodexRuntimePolicy(&out, runner.RuntimeCodex, g, handWrittenNodes)
	got := out.String()

	if strings.Contains(got, optInFlag) {
		t.Errorf("a hand-written graph's disclosure names a flag its invocation never registered:\n%s", got)
	}
	if strings.Contains(got, isolatedCodexNote) {
		t.Errorf("a hand-written graph's nodes are not isolated, and the screen must not say they are:\n%s", got)
	}
	// Paired positive: the rest of the Codex disclosure is untouched, so this
	// is not passing on an empty writer.
	if !strings.Contains(got, "Codex filesystem sandbox") {
		t.Errorf("the hand-written Codex disclosure lost its body entirely:\n%s", got)
	}
}

// TestRunAutoWith_PlanOnlyPrintsTheDisclosure is ADR 0032 §3(7): the preview
// screen is the same screen, and it is the one an operator uses to decide
// whether to type the flag for real.
func TestRunAutoWith_PlanOnlyPrintsTheDisclosure(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{"plan-1": {Result: cycleSpec, TotalCostUSD: 0.04}})

	out := captureStdout(t, func() {
		if err := runAutoWith([]string{"tidy the docs", "--plan-only", optInFlag, "--accept-no-build-evidence"},
			fake, browser.NewFakeOpener(), os.Stdout); err != nil {
			t.Fatalf("auto --plan-only: %v", err)
		}
	})

	if !strings.Contains(out, loadedClaudeNote) {
		t.Errorf("--plan-only did not disclose the configuration the run it previews would carry:\n%s", out)
	}
	if strings.Contains(out, isolatedClaudeNote) {
		t.Errorf("--plan-only previewed an isolated run it would not have been:\n%s", out)
	}
}

// TestRunAutoWith_EveryGoalCycleDisclosesIt is ADR 0032 §3(8). Every cycle of a
// --max-cycles run plans afresh and re-enters printPlanForRuntime, so cycle 2
// is a second screen an operator reads — and the coordinator carrying the
// option rather than the cycle is what makes it say the same thing.
func TestRunAutoWith_EveryGoalCycleDisclosesIt(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1":   {Result: cycleSpec, TotalCostUSD: 0.1},
		"work-1":   {SessionID: "s-work-1", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.5},
		"assess-1": {Result: cycleAssessNotMet, TotalCostUSD: 0.02},
		"plan-2":   {Result: cycleSpec, TotalCostUSD: 0.1},
		"work-2":   {SessionID: "s-work-2", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.5},
		"assess-2": {Result: cycleAssessMet, TotalCostUSD: 0.02},
	})

	out := captureStdout(t, func() {
		if err := runAutoWith([]string{"tidy the docs", optInFlag, "--max-cycles", "2", "--accept-no-build-evidence"},
			fake, browser.NewFakeOpener(), os.Stdout); err != nil {
			t.Fatalf("goal loop: %v", err)
		}
	})

	if got := strings.Count(out, loadedClaudeNote); got != 2 {
		t.Errorf("the disclosure appeared %d times over a 2-cycle loop, want once per cycle:\n%s", got, out)
	}
	if strings.Contains(out, isolatedClaudeNote) {
		t.Errorf("some cycle claimed isolation on an opted-in run:\n%s", out)
	}
}

// --- 5. what the opt-in must not reach ----------------------------------------

// TestChatFlags_CannotReachTheOptIn is ADR 0032 §3(12) and §2.7. Two halves:
// `chat` does not parse the flag, and a chat-planned node is isolated anyway —
// because "the flag set rejects it" would still leave a build where chat
// passed the option some other way.
func TestChatFlags_CannotReachTheOptIn(t *testing.T) {
	if err := newChatFlags().parse([]string{optInFlag}); err == nil {
		t.Errorf("`chat` parsed %s; a flag that widens an unattended run must be typed at a launch, not implied by one [y/N]", optInFlag)
	}
	// The same refusal from `resume`, for the sharper reason: a resumed leg's
	// own flags may only de-escalate, so there must be no spelling by which a
	// second process widens a ceiling the first leg chose.
	if err := newResumeFlags().parse([]string{"run-1", optInFlag}); err == nil {
		t.Errorf("`resume` parsed %s; a resumed leg inherits the choice and never makes it", optInFlag)
	}
	// And `auto` DOES, which is what makes the two refusals above a scope and
	// not a flag nobody registered anywhere.
	f := newAutoFlags()
	if err := f.parse([]string{"a goal", optInFlag}); err != nil {
		t.Fatalf("`auto` must parse %s: %v", optInFlag, err)
	}
	if !f.acceptLoadedUserConfig {
		t.Errorf("`auto` parsed %s without setting the field", optInFlag)
	}
	if def := newAutoFlags(); def.acceptLoadedUserConfig {
		t.Errorf("the flag defaults to ON; the ceiling is the default and must be asked out of")
	}
}

// TestChatLoop_PlannedNodesStayIsolated is the second half of the claim above,
// through the whole chat path rather than through its flag set.
func TestChatLoop_PlannedNodesStayIsolated(t *testing.T) {
	plannedSpec := `{"name":"from-chat","nodes":[{"id":"work","prompt":"do the planned work","allowed_tools":["Read"]}]}`
	fake := newChatFake(map[string]runner.NodeOutcome{
		routerKey:             {Result: `{"mode":"graph","goal":"tidy the docs"}`},
		autoPlanKey:           {Result: plannedSpec, TotalCostUSD: 0.01},
		"do the planned work": {SessionID: "s-work", Result: "PASS", ExitCode: 0},
	})

	isolateRunHome(t)
	out := captureStdout(t, func() {
		runChatLoop(t, fake, "clean up the docs please\ny\n")
	})

	nodes := 0
	for _, spec := range fake.Invocations() {
		if spec.Prompt != "do the planned work" {
			continue
		}
		nodes++
		if spec.Policy.SettingSources == nil {
			t.Errorf("a chat-planned node was spawned without layer 1: %+v", spec.Policy)
		}
		if !spec.Policy.StrictMCPConfig {
			t.Errorf("a chat-planned node was spawned without layer 4: %+v", spec.Policy)
		}
	}
	if nodes != 1 {
		t.Fatalf("%d chat-planned nodes spawned, want 1 — nothing above was checked", nodes)
	}
	if strings.Contains(out, loadedClaudeNote) {
		t.Errorf("a chat plan screen claimed the operator's configuration loads:\n%s", out)
	}
}

// --- 6. the Codex resumed leg -------------------------------------------------

// The Codex arm of the resume, which is where all three of ADR 0032's resumed
// claims meet: the choice is inherited from the snapshot (`resume` has no flag
// and must not), the four isolation flags follow it onto the argv of a SECOND
// process, and the sentence that discloses it has to stand on its own — a
// resumed leg prints none of noteCodexRuntimePolicy, so "the filesystem sandbox
// above" would otherwise name a line this leg never emitted.

// codexResumeSpec is the stub planner's reply: two nodes, so the retry leg has
// exactly one node to re-run and its argv cannot be satisfied by the first
// leg's.
const codexResumeSpec = `{"name":"codex-resume","version":"1","nodes":[` +
	`{"id":"draft","prompt":"draft the codex proposal","allowed_tools":["Read"]},` +
	`{"id":"render","prompt":"render the codex artifact","allowed_tools":["Read"],"depends_on":["draft"]}]}`

// stubCodex is stubClaude's Codex twin: it records its own argv,
// NUL-separated, under $OMG_TEST_ARGV_DIR and answers with canned
// `codex exec --json` JSONL. No real `codex` is ever spawned; what IS real is
// everything between the policy and the bytes — CLIRunner, codexProtocol's
// buildArgs, and the process itself.
const stubCodex = `#!/bin/sh
out=$(mktemp "$OMG_TEST_ARGV_DIR/argv.XXXXXX")
for a in "$@"; do printf '%s\0' "$a" >> "$out"; done
for a in "$@"; do
  case "$a" in
    *"planning coordinator"*) cat "$OMG_TEST_REPLIES_DIR/plan.jsonl"; exit 0 ;;
  esac
done
if [ -n "$OMG_TEST_FAIL_PROMPT" ]; then
  for a in "$@"; do
    case "$a" in
      *"$OMG_TEST_FAIL_PROMPT"*) cat "$OMG_TEST_REPLIES_DIR/fail.jsonl"; exit 1 ;;
    esac
  done
fi
cat "$OMG_TEST_REPLIES_DIR/node.jsonl"
`

// codexFailJSONL is one node failing on request: a terminal turn.failed, which
// is what manufactures the FAILED record `resume --retry-failed` exists for.
const codexFailJSONL = `{"type":"thread.started","thread_id":"t-fail"}` + "\n" +
	`{"type":"turn.failed","error":{"message":"stub codex: failing on request"}}` + "\n"

// codexJSONL is the three-event stream parseCodexJSONL demands: the thread id,
// one agent message carrying the reply, and the terminal turn.
func codexJSONL(t *testing.T, threadID, message string) string {
	t.Helper()
	text, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	return `{"type":"thread.started","thread_id":"` + threadID + `"}` + "\n" +
		`{"type":"item.completed","item":{"type":"agent_message","text":` + string(text) + `}}` + "\n" +
		`{"type":"turn.completed","usage":{"input_tokens":7,"output_tokens":2}}` + "\n"
}

// codexArgvProbe is argvProbe's Codex counterpart, and it is a value for the
// same reason: a resumed leg is a SECOND process against the SAME run, so it
// needs the same stub and the same home with a fresh argv directory.
type codexArgvProbe struct {
	stub    string
	argvDir string
}

func newCodexArgvProbe(t *testing.T) *codexArgvProbe {
	t.Helper()
	isolateRunHome(t)
	// An empty home, so nothing the developer running these tests happens to
	// have configured can reach a node — including under the opt-in, whose
	// whole subject is the configuration a real home would supply.
	t.Setenv("HOME", t.TempDir())

	replies := t.TempDir()
	writeFileTree(t, filepath.Join(replies, "plan.jsonl"), codexJSONL(t, "t-plan", codexResumeSpec))
	writeFileTree(t, filepath.Join(replies, "node.jsonl"), codexJSONL(t, "t-node", "done"))
	writeFileTree(t, filepath.Join(replies, "fail.jsonl"), codexFailJSONL)
	t.Setenv(repliesDirEnv, replies)

	probe := &codexArgvProbe{stub: filepath.Join(t.TempDir(), "codex")}
	writeFileTree(t, probe.stub, stubCodex)
	if err := os.Chmod(probe.stub, 0o755); err != nil {
		t.Fatal(err)
	}
	probe.freshArgvDir(t)
	return probe
}

// runner is a REAL CLIRunner on the Codex protocol, pointed at the stub.
func (p *codexArgvProbe) runner() runner.NodeRunner {
	return runner.NewCLIRunner(runner.RuntimeCodex, runner.WithBinary(p.stub))
}

func (p *codexArgvProbe) freshArgvDir(t *testing.T) {
	t.Helper()
	p.argvDir = t.TempDir()
	t.Setenv(argvDirEnv, p.argvDir)
}

// nodeArgv is one recorded spawn, found by the prompt codexProtocol.buildArgs
// puts LAST. recordedArgv.prompt() cannot serve here: it reads `-p`, which is
// Claude's spelling and appears nowhere in a `codex exec` argv, so every Codex
// spawn would key as "" and collide. The planner's own spawn is dropped — its
// ceiling is a different decision (coordinatorInvocation) — and a miss lists
// what WAS recorded, a node that never ran being the failure worth naming.
func (p *codexArgvProbe) nodeArgv(t *testing.T, prompt string) recordedArgv {
	t.Helper()
	entries, err := os.ReadDir(p.argvDir)
	if err != nil {
		t.Fatalf("read recorded argv: %v", err)
	}
	recorded := make([]string, 0, len(entries))
	var matched []recordedArgv
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(p.argvDir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		// Each element is NUL-TERMINATED, so the split leaves a trailing "".
		fields := bytes.Split(raw, []byte{0})
		argv := make(recordedArgv, 0, len(fields))
		for _, field := range fields[:len(fields)-1] {
			argv = append(argv, string(field))
		}
		if len(argv) == 0 {
			continue
		}
		last := argv[len(argv)-1]
		if strings.Contains(last, "planning coordinator") {
			continue
		}
		recorded = append(recorded, last)
		if strings.HasPrefix(last, prompt) {
			matched = append(matched, argv)
		}
	}
	sort.Strings(recorded)
	switch len(matched) {
	case 1:
		return matched[0]
	case 0:
		t.Fatalf("no node spawned with prompt %q; recorded: %v", prompt, recorded)
	default:
		t.Fatalf("prompt %q is a prefix of %d recorded spawns (%v)", prompt, len(matched), recorded)
	}
	return recordedArgv{}
}

// hasSequence reports whether these arguments appear CONSECUTIVELY. It lives
// with the Codex tests because that is where it is load-bearing: two of the
// four isolation flags are `--config <expr>` pairs, and a bare search for
// "--config" would find approval_policy="never" — the one line that must
// survive the opt-in — and report the isolation present when it was gone.
func (a recordedArgv) hasSequence(want ...string) bool {
	for i := range a {
		if i+len(want) > len(a) {
			return false
		}
		if slices.Equal(a[i:i+len(want)], want) {
			return true
		}
	}
	return false
}

// codexIsolationArgv is what buildArgs appends for an isolated planned node,
// and the exact four ADR 0032's opt-in takes off the argv. Spelled out again
// here rather than shared with internal/runner's copy: these tests assert what
// a second PROCESS was spawned with, and a list imported from the code under
// test would go stale in both places at once.
var codexIsolationArgv = [][]string{
	{"--ignore-user-config"},
	{"--ignore-rules"},
	{"--config", "project_doc_max_bytes=0"},
	{"--config", "mcp_servers={}"},
}

// TestResumeCodex_TheInheritedChoiceDecidesTheResumedArgv is the Codex half of
// ADR 0032 §3(9), at the only layer a node actually obeys.
//
// `resume` registers no opt-in flag, so the resumed leg's argv is built from
// the policies it rehydrated — and the absent arm is what makes the present
// arm mean anything: a build that stopped appending the four entirely would
// satisfy every "want gone" line on its own. Each arm therefore asserts
// PRESENCE of its own expectation, on the first leg as a precondition and on
// the second as the claim.
//
// The sandbox and approval_policy assertions run on BOTH arms, because they are
// what the resumed disclosure says still binds: this is the argv that has to be
// true for that sentence to be honest.
func TestResumeCodex_TheInheritedChoiceDecidesTheResumedArgv(t *testing.T) {
	for _, tc := range []struct {
		name         string
		autoArgs     []string
		wantIsolated bool
	}{
		{name: "no flag: the resumed argv is today's, all four flags on it", wantIsolated: true},
		{name: optInFlag + ": the resumed argv carries none of the four", autoArgs: []string{optInFlag}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := newCodexArgvProbe(t)
			t.Setenv(failPromptEnv, "render the codex artifact")

			args := append([]string{"turn the issue into a codex proposal", "--accept-no-build-evidence"}, tc.autoArgs...)
			var runErr error
			captureStdout(t, func() {
				runErr = runAutoWithRuntime(runner.RuntimeCodex, args, probe.runner(), browser.NewFakeOpener(), os.Stdout)
			})
			if runErr == nil {
				t.Fatal("precondition failed: the first leg was supposed to fail at the render node")
			}
			runID := soleRunID(t)

			// The first leg is in the arm this test claims to be continuing —
			// otherwise the resumed argv below is not an inheritance of anything.
			first := probe.nodeArgv(t, "draft the codex proposal")
			for _, sequence := range codexIsolationArgv {
				if present := first.hasSequence(sequence...); present != tc.wantIsolated {
					t.Fatalf("precondition failed: the FIRST leg's argv has %#v present = %t, want %t\nargv: %q",
						sequence, present, tc.wantIsolated, first)
				}
			}

			// A second leg, recording into its own directory with the stub's
			// failure switched off, so what it asserts is THIS leg's spawn.
			probe.freshArgvDir(t)
			t.Setenv(failPromptEnv, "")

			var resumeErr error
			captureStdout(t, func() {
				resumeErr = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), probe.runner(), nil)
			})
			if resumeErr != nil {
				t.Fatalf("resume --retry-failed: %v", resumeErr)
			}

			argv := probe.nodeArgv(t, "render the codex artifact")
			for _, sequence := range codexIsolationArgv {
				switch present := argv.hasSequence(sequence...); {
				case tc.wantIsolated && !present:
					t.Errorf("the resumed argv is missing %#v — a second process silently widened a run that never opted in\nargv: %q", sequence, argv)
				case !tc.wantIsolated && present:
					t.Errorf("the resumed argv still carries %#v, so the inherited configuration does not load after all\nargv: %q", sequence, argv)
				}
			}
			// The floor, on both arms, and the reason the disclosure may claim
			// this flag widens neither.
			if !argv.hasSequence("--sandbox", "workspace-write") {
				t.Errorf("the resumed argv lost its filesystem sandbox: %q", argv)
			}
			if !argv.hasSequence("--config", `approval_policy="never"`) {
				t.Errorf(`the resumed argv lost approval_policy="never": %q`, argv)
			}
			if !argv.hasSequence("exec", "--json") {
				t.Errorf("the resumed argv is not a `codex exec --json` invocation at all: %q", argv)
			}
		})
	}
}

// TestResumeCodex_TheResumedDisclosureStandsAlone is review defect #4: the
// resumed screen's Codex sentence ends "the filesystem sandbox above ... are
// argv on every node", and a resumed leg prints none of noteCodexRuntimePolicy,
// so "above" used to name a line this leg never emitted. A reader met a
// reference with nothing behind it, and could not check the one thing the
// sentence claims still binds.
//
// The pin is the ORDER as much as the presence: the referenced line must be on
// the screen, and it must be on it FIRST. The last assertion is the general
// form of the defect and runs on both arms — any "sandbox above" on a resumed
// screen must have the sandbox line above it.
func TestResumeCodex_TheResumedDisclosureStandsAlone(t *testing.T) {
	for _, tc := range []struct {
		name     string
		autoArgs []string
		wantNote bool
	}{
		{name: "opted in: the sentence and the line it points at", autoArgs: []string{optInFlag}, wantNote: true},
		{name: "not opted in: neither, and the screen is unchanged", wantNote: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunHome(t)
			fake := newCycleFake(map[string]runner.NodeOutcome{
				"plan-1": {Result: cycleSpec, TotalCostUSD: 0.04},
				"work-1": {ExitCode: 1, FailureCause: "stub codex: failing on request", CostUnknown: true},
				"work-2": {SessionID: "t-work", Result: "PASS", ExitCode: 0, CostUnknown: true},
			})

			args := append([]string{"tidy the docs", "--accept-no-build-evidence"}, tc.autoArgs...)
			captureStdout(t, func() {
				if err := runAutoWithRuntime(runner.RuntimeCodex, args, fake, browser.NewFakeOpener(), os.Stdout); err == nil {
					t.Error("precondition failed: the first leg was supposed to fail at its only node")
				}
			})
			runID := soleRunID(t)

			out := captureStdout(t, func() {
				if err := executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), fake, nil); err != nil {
					t.Errorf("resume: %v", err)
				}
			})

			// Both arms assert the resumed screen EXISTS before asking what is
			// on it, so neither can pass on a leg that printed nothing at all.
			banner := strings.Index(out, "Resuming run")
			if banner < 0 {
				t.Fatalf("the resumed leg printed no banner, so nothing below was checked:\n%s", out)
			}

			if tc.wantNote {
				note := strings.Index(out, loadedCodexNote)
				if note < 0 {
					t.Fatalf("the resumed leg never disclosed the inherited configuration in full:\nwant:\n%s\ngot:\n%s", loadedCodexNote, out)
				}
				sandbox := strings.Index(out, codexSandboxLine)
				if sandbox < 0 {
					t.Fatalf("the resumed sentence points at a filesystem sandbox line this leg never printed:\nwant:\n%s\ngot:\n%s", codexSandboxLine, out)
				}
				if sandbox > note {
					t.Errorf("the line the sentence calls \"above\" was printed BELOW it (sandbox at %d, note at %d):\n%s", sandbox, note, out)
				}
				if note > banner {
					t.Errorf("the disclosure printed AFTER the banner (note at %d, banner at %d); it belongs with the leg's other differences:\n%s", note, banner, out)
				}
				if strings.Contains(out, isolatedCodexNote) {
					t.Errorf("the resumed screen also claimed the nodes ignore your configuration:\n%s", out)
				}
			} else {
				if strings.Contains(out, loadedCodexNote) {
					t.Errorf("a resume of a run that never opted in claimed it loads the operator's configuration:\n%s", out)
				}
				if strings.Contains(out, codexSandboxLine) {
					t.Errorf("a resumed leg printed the Codex plan-screen disclosure it has never printed:\n%s", out)
				}
			}

			// The defect in its general form, on both arms: nothing on a
			// resumed screen may point at text this leg did not emit.
			if pointer := strings.Index(out, "sandbox above"); pointer >= 0 {
				if target := strings.Index(out, codexSandboxLine); target < 0 || target > pointer {
					t.Errorf("the resumed screen says \"sandbox above\" with no sandbox line above it:\n%s", out)
				}
			}
		})
	}
}

// TestMappingOptions_TheOptInSaysWhyMappingAndActivationAreOff is the CLI half
// of ADR 0032 §2.4. The forcing itself is coordinator.WithLoadedUserConfig's
// and is asserted there; what belongs here is that it is not silent — two
// mechanisms the operator may have been relying on are off, and a missing
// [agent: ...] marker is not an explanation.
func TestMappingOptions_TheOptInSaysWhyMappingAndActivationAreOff(t *testing.T) {
	var loud strings.Builder
	if opts := mappingOptions(&loud, false, nil, false, true); len(opts) != 0 {
		t.Errorf("mappingOptions returned %d options under the opt-in; with mapping and activation both off there is nothing to scan", len(opts))
	}
	got := loud.String()
	for _, want := range []string{optInFlag, "agent mapping and skill activation are off", "shadowed", "0017-lifting-the-agent-mapped-exclusion.md"} {
		if !strings.Contains(got, want) {
			t.Errorf("the de-escalation reason does not mention %q:\n%s", want, got)
		}
	}

	// And an ordinary run says none of it: a reason printed where there is no
	// de-escalation teaches a reader to skip the line that matters.
	t.Setenv("HOME", t.TempDir())
	var quiet strings.Builder
	mappingOptions(&quiet, false, nil, false, false)
	if quiet.Len() != 0 {
		t.Errorf("an ordinary run printed a de-escalation reason:\n%s", quiet.String())
	}
}
