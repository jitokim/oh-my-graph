package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// autoRunWithSkills drives a whole `auto` run against a temp $HOME holding one
// skill, and returns the run id and the FakeRunner. It goes through runAutoWith
// — the real argv path — so what it observes is what a user's run does.
func autoRunWithSkills(t *testing.T, args ...string) (string, *runner.FakeRunner) {
	t.Helper()
	isolateRunHome(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	skillDir := filepath.Join(home, ".claude", "skills", "architecture-design")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: architecture-design\ndescription: designs systems\n---\n\nthe design procedure\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {ExitCode: 0, Result: cycleSpec, TotalCostUSD: 0.01},
		"work-1": {ExitCode: 0, Result: "done", SessionID: "s-work"},
	})
	var err error
	captureStdout(t, func() {
		err = runAutoWith(append([]string{"design the thing", "--no-agent-mapping"}, args...),
			fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("auto run: %v", err)
	}
	return soleRunID(t), fake
}

// The end-to-end shape ADR 0017 decided, observed at the seam a real node
// spawns through: `--setting-sources ""`, `--plugin-dir <staged>`,
// `--tools ...,Skill`, `--strict-mcp-config`. Asserted on the POLICY the
// runner receives rather than on a printed string, since that is what
// buildArgs renders.
func TestRunAuto_PlannedNodeSpawnsWithTheStagedPluginAndTheSkillTool(t *testing.T) {
	runID, fake := autoRunWithSkills(t)

	var node runner.NodeInvocation
	for _, inv := range fake.Invocations() {
		// Prefix, not equality: an activated node's prompt is the planner's
		// text plus coordinator's activationNotice.
		if strings.HasPrefix(inv.Prompt, "work") {
			node = inv
		}
	}
	if node.Prompt == "" {
		t.Fatal("the planned node never ran")
	}
	if !slices.Contains(node.Policy.Tools, coordinator.SkillToolName) {
		t.Errorf("node Tools = %v, want %s: without it the definitions load and cannot run",
			node.Policy.Tools, coordinator.SkillToolName)
	}
	wantDir := filepath.Join(runDirFor(runID), "skills-plugin")
	if !slices.Equal(node.Policy.PluginDirs, []string{wantDir}) {
		t.Errorf("node PluginDirs = %v, want [%s] — inside the run directory, not the node's cwd", node.Policy.PluginDirs, wantDir)
	}
	// Layer 1 did not move. This is the assertion the whole ADR turns on:
	// relaxing it is what measurement (g) showed lets a node that declared
	// Bash(git *) run an out-of-scope command.
	if node.Policy.SettingSources == nil || *node.Policy.SettingSources != "" {
		t.Errorf("node SettingSources = %v, want a pointer to \"\"", node.Policy.SettingSources)
	}
	if !node.Policy.StrictMCPConfig {
		t.Error("node StrictMCPConfig = false, want layer 4 unchanged")
	}
	if slices.Contains(node.Policy.AllowedTools, coordinator.SkillToolName) {
		t.Errorf("node AllowedTools = %v, want layer 2 untouched", node.Policy.AllowedTools)
	}

	// The directory the node was pointed at actually holds the corpus: a
	// --plugin-dir pointing at nothing exits 0 with no warning, so "the flag
	// was passed" is not evidence that anything loaded.
	staged := filepath.Join(wantDir, "skills", "architecture-design", "SKILL.md")
	if raw, err := os.ReadFile(staged); err != nil {
		t.Errorf("staged skill missing: %v", err)
	} else if !strings.Contains(string(raw), "the design procedure") {
		t.Errorf("staged skill is not the user's file:\n%s", raw)
	}

	// The grant's only durable record is the snapshot — it is deliberately
	// invisible in graph.json — so a resumed leg and the acceptance test both
	// depend on it being there.
	snap := loadSnapshot(t, runID)
	persisted, ok := snap.ToolPolicies["work"]
	if !ok {
		t.Fatal("state.json records no policy for the planned node")
	}
	if !slices.Contains(persisted.Tools, coordinator.SkillToolName) || !slices.Equal(persisted.PluginDirs, []string{wantDir}) {
		t.Errorf("persisted policy = %+v, want the Skill tool and the staged plugin dir", persisted)
	}
	if persisted.SettingSources == nil || *persisted.SettingSources != "" {
		t.Errorf("persisted SettingSources = %v, want \"\"", persisted.SettingSources)
	}
	spec, err := os.ReadFile(filepath.Join(runDirFor(runID), "graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(spec), coordinator.SkillToolName) {
		t.Errorf("graph.json names %s; the grant is a policy-level act (ADR 0017 §2):\n%s", coordinator.SkillToolName, spec)
	}
}

// --no-skill-activation is the kill switch, and it must reach every layer it
// turned on: no Skill in the tool set, no --plugin-dir, and no staged
// directory on disk at all.
func TestRunAuto_NoSkillActivationLeavesTheRunIsolated(t *testing.T) {
	runID, fake := autoRunWithSkills(t, "--no-skill-activation")

	saw := false
	for _, inv := range fake.Invocations() {
		if inv.Prompt != "work" {
			continue
		}
		saw = true
		if slices.Contains(inv.Policy.Tools, coordinator.SkillToolName) {
			t.Errorf("Tools = %v, want no %s with activation off", inv.Policy.Tools, coordinator.SkillToolName)
		}
		if len(inv.Policy.PluginDirs) != 0 {
			t.Errorf("PluginDirs = %v, want none with activation off", inv.Policy.PluginDirs)
		}
	}
	if !saw {
		t.Fatal("the planned node never ran; the assertions above proved nothing")
	}
	if _, err := os.Stat(filepath.Join(runDirFor(runID), "skills-plugin")); !os.IsNotExist(err) {
		t.Errorf("a staged directory exists with activation off (stat err = %v)", err)
	}
}

// The deprecated spelling still works and still says so. Both halves matter:
// dropping it would break a script that already passes it, and accepting it
// silently would leave a user believing a mechanism that no longer exists is
// what they turned off.
func TestAutoFlags_DeprecatedSkillMappingFlagIsRewrittenLoudly(t *testing.T) {
	var notice strings.Builder
	rewritten := rewriteDeprecatedSkillFlag(&notice, newAutoFlags().set, []string{"--no-skill-mapping", "--plan-only"})

	if !slices.Equal(rewritten, []string{"--no-skill-activation", "--plan-only"}) {
		t.Fatalf("rewritten = %v, want the deprecated flag translated and the rest untouched", rewritten)
	}
	if !strings.Contains(notice.String(), "--no-skill-mapping is deprecated") ||
		!strings.Contains(notice.String(), "--no-skill-activation") {
		t.Errorf("the rewrite must name both spellings:\n%s", notice.String())
	}

	// And nothing is said when nobody typed it.
	var quiet strings.Builder
	rewriteDeprecatedSkillFlag(&quiet, newAutoFlags().set, []string{"--plan-only"})
	if quiet.Len() != 0 {
		t.Errorf("a run that used no deprecated flag must stay silent:\n%s", quiet.String())
	}

	// The flag itself still parses through `auto`'s real argv path.
	f := newAutoFlags()
	if err := f.parse([]string{"a goal", "--no-skill-mapping"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !f.noSkillActivation {
		t.Error("--no-skill-mapping did not turn activation off")
	}
}

// A rewrite that is not positional-aware edits the user's own strings. The
// deprecated spelling is only a flag where a flag can appear: as another
// flag's VALUE it is data — a build command, a goal, an input — and the one
// thing this function must never do is quietly rewrite what a node will run.
func TestRewriteDeprecatedSkillFlag_LeavesValuePositionsAlone(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"a separate value", []string{"--verify-cmd", "--no-skill-mapping"}},
		{"after the terminator", []string{"--", "--no-skill-mapping"}},
		{"a repeated input value", []string{"--input", "--no-skill-mapping"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var notice strings.Builder
			got := rewriteDeprecatedSkillFlag(&notice, newAutoFlags().set, tc.args)
			if !slices.Equal(got, tc.args) {
				t.Errorf("rewritten = %v, want %v untouched — that element is a value, not a flag", got, tc.args)
			}
			if notice.Len() != 0 {
				t.Errorf("a notice was printed for a flag nobody typed:\n%s", notice.String())
			}
		})
	}

	// And the flag AFTER a consumed value is still rewritten, so the skip is a
	// skip of one element and not of the rest of the argv.
	var notice strings.Builder
	got := rewriteDeprecatedSkillFlag(&notice, newAutoFlags().set,
		[]string{"--verify-cmd", "make build", "--no-skill-mapping"})
	if !slices.Equal(got, []string{"--verify-cmd", "make build", "--no-skill-activation"}) {
		t.Errorf("rewritten = %v, want the trailing deprecated flag still translated", got)
	}
}

// dropSkillActivation is the resume boundary, and since 2026-08-07 it has one
// outcome for an activation-enabled run and not three: OFF. The hazard it
// answers is that a resumed leg has no in-memory manifest, so the only thing
// it could re-stage from is a file in a directory the previous leg's nodes
// could write (ADR 0017 §6). What is pinned here is that the de-escalation is
// total and that it is SAID; the argv-deep proof is in skillargv_test.
func TestDropSkillActivation(t *testing.T) {
	// A helper that produces the state a real activated run leaves behind.
	stage := func(t *testing.T) map[string]runstate.NodeToolPolicy {
		t.Helper()
		runID, _ := autoRunWithSkills(t)
		return loadSnapshot(t, runID).ToolPolicies
	}

	t.Run("takes both halves off an activated run and says why", func(t *testing.T) {
		snapPolicies := stage(t)
		policies := toRunnerToolPolicies(snapPolicies)
		if !slices.Contains(policies["work"].Tools, coordinator.SkillToolName) {
			t.Fatal("precondition failed: the snapshot did not record the Skill grant")
		}
		if got := policies["work"].PluginDirs; len(got) != 0 {
			t.Fatalf("toRunnerToolPolicies rehydrated PluginDirs = %v; a path must never be trusted verbatim", got)
		}

		var out strings.Builder
		dropSkillActivation(&out, snapPolicies, policies, false, nil)

		if slices.Contains(policies["work"].Tools, coordinator.SkillToolName) {
			t.Errorf("Tools = %v, want %s dropped", policies["work"].Tools, coordinator.SkillToolName)
		}
		if len(policies["work"].PluginDirs) != 0 {
			t.Errorf("PluginDirs = %v, want none", policies["work"].PluginDirs)
		}
		// A resumed leg that behaves differently from its first leg without
		// saying so is the unexplained-absence shape this mechanism is most
		// exposed to, so the line is part of the contract.
		if !strings.Contains(out.String(), "skill activation is off for this leg") {
			t.Errorf("the de-escalation must be disclosed:\n%s", out.String())
		}
		if !strings.Contains(out.String(), "ADR 0017") {
			t.Errorf("the disclosure must point at where the decision lives:\n%s", out.String())
		}
		// Every other layer is untouched: this subtracts one tool name and one
		// directory, it does not rewrite the ceiling.
		if policies["work"].SettingSources == nil || *policies["work"].SettingSources != "" {
			t.Errorf("SettingSources = %v, want layer 1 untouched", policies["work"].SettingSources)
		}
		if !slices.Contains(policies["work"].Tools, "Read") {
			t.Errorf("Tools = %v, want the node's declared tools intact", policies["work"].Tools)
		}
	})

	t.Run("a manifest a node could have forged changes nothing", func(t *testing.T) {
		snapPolicies := stage(t)
		// The whole point of the decision: the sidecar is not consulted, so
		// rewriting it — which a node with unscoped Write can do — has no
		// effect on what the resumed leg hands anybody.
		forged := 0
		for _, p := range snapPolicies {
			for _, dir := range p.PluginDirs {
				if err := os.WriteFile(dir+".manifest.json", []byte(`{"plugin":"oh-my-graph-staged-skills","files":[{"source":"/dev/null","rel":"skills/pwned/SKILL.md","sha256":"0"}]}`), 0o600); err != nil {
					t.Fatal(err)
				}
				forged++
			}
		}
		if forged == 0 {
			t.Fatal("precondition failed: no staged plugin directory to forge a manifest beside")
		}
		policies := toRunnerToolPolicies(snapPolicies)
		dropSkillActivation(io.Discard, snapPolicies, policies, false, nil)

		if slices.Contains(policies["work"].Tools, coordinator.SkillToolName) {
			t.Errorf("Tools = %v; a forged manifest must not be able to keep activation on", policies["work"].Tools)
		}
		if len(policies["work"].PluginDirs) != 0 {
			t.Errorf("PluginDirs = %v, want none", policies["work"].PluginDirs)
		}
	})

	t.Run("--no-skill-activation says so and lands in the same place", func(t *testing.T) {
		snapPolicies := stage(t)
		policies := toRunnerToolPolicies(snapPolicies)

		var out strings.Builder
		dropSkillActivation(&out, snapPolicies, policies, true, nil)

		if slices.Contains(policies["work"].Tools, coordinator.SkillToolName) {
			t.Errorf("Tools = %v, want %s dropped", policies["work"].Tools, coordinator.SkillToolName)
		}
		if len(policies["work"].PluginDirs) != 0 {
			t.Errorf("PluginDirs = %v, want none", policies["work"].PluginDirs)
		}
		// The flag is now redundant rather than load-bearing, and a user who
		// passed it deserves to be told that rather than left thinking it is
		// what turned activation off.
		if !strings.Contains(out.String(), "--no-skill-activation") {
			t.Errorf("the flag must be acknowledged when it was given:\n%s", out.String())
		}
		if !strings.Contains(out.String(), "in any case") {
			t.Errorf("the line must say the flag was not what decided it:\n%s", out.String())
		}
	})

	t.Run("does nothing to a run that never had activation", func(t *testing.T) {
		snapPolicies := map[string]runstate.NodeToolPolicy{"work": {Tools: []string{"Read"}}}
		policies := toRunnerToolPolicies(snapPolicies)
		var out strings.Builder
		dropSkillActivation(&out, snapPolicies, policies, false, nil)

		if !slices.Equal(policies["work"].Tools, []string{"Read"}) {
			t.Errorf("Tools = %v, want them untouched", policies["work"].Tools)
		}
		// Nothing was taken away, so there is nothing to disclose: a line here
		// would tell every ordinary `run` resume about a feature it never had.
		if out.String() != "" {
			t.Errorf("printed %q for a run that never activated anything", out.String())
		}
	})
}

// The snapshot type must mirror the runtime one, and PluginDirs is the field
// most likely to be dropped from the persisted shape — it is not a ceiling
// layer, so it reads as optional. It is not: it is the only durable record
// that a run was activation-enabled.
func TestNodeToolPolicy_PersistsThePluginDirs(t *testing.T) {
	raw, err := json.Marshal(runstate.NodeToolPolicy{PluginDirs: []string{"/runs/r1/skills-plugin"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"plugin_dirs":["/runs/r1/skills-plugin"]`) {
		t.Errorf("marshaled policy = %s, want a plugin_dirs field", raw)
	}
	var back runstate.NodeToolPolicy
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(back.PluginDirs, []string{"/runs/r1/skills-plugin"}) {
		t.Errorf("round-tripped PluginDirs = %v", back.PluginDirs)
	}
	// An old snapshot without the field rehydrates as an isolated run, which
	// is the correct default.
	var old runstate.NodeToolPolicy
	if err := json.Unmarshal([]byte(`{"tools":["Read"]}`), &old); err != nil {
		t.Fatal(err)
	}
	if len(old.PluginDirs) != 0 {
		t.Errorf("PluginDirs = %v for a pre-ADR-0017 snapshot, want none", old.PluginDirs)
	}
}
