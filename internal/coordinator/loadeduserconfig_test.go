package coordinator

import (
	"context"
	"slices"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runner"
)

// ADR 0032's policy layer: what --accept-loaded-user-config does to
// toolPolicyFor, and what it must leave alone.
//
// Everything here is asserted on the POLICY MAP rather than on an argv,
// because the policy is where the decision is made and it is runtime-blind by
// construction — one bit, `SettingSources != nil`, which both protocols branch
// on (ADR 0032 §1.2). The two runtimes' renderings of that bit are pinned
// where the rendering lives, in internal/runner/codex_protocol_test.go and
// internal/runner/claude_test.go, and joined to this end to end in
// cmd/oh-my-graph/loadeduserconfig_cli_test.go.

// twoNodeSpec is a planner reply whose two nodes declare different tools, so
// layers 2, 3 and 5 have per-node content that a comparison can actually
// notice moving.
const twoNodeSpec = `{"name":"two","version":"1","nodes":[` +
	`{"id":"scan","prompt":"scan the tree","allowed_tools":["Read","Bash(git *)"]},` +
	`{"id":"fix","prompt":"fix what scan found","allowed_tools":["Edit"],"depends_on":["scan"]}]}`

func planTwoNodes(t *testing.T, opts ...Option) Plan {
	t.Helper()
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: twoNodeSpec})
	plan, err := New(fake, opts...).Plan(context.Background(), "scan and fix", nil)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.ToolPolicies) != 2 {
		t.Fatalf("ToolPolicies has %d entries, want 2 — nothing below was checked", len(plan.ToolPolicies))
	}
	return plan
}

// TestToolPolicy_DefaultIsolatesEveryPlannedNode is ADR 0032 §3(1): the
// regression pin on today's behaviour, and the assertion that the new option
// changed no default. A run that types nothing gets layer 1 as a pointer to
// the empty string and layer 4 on, for every node, on both runtimes — the
// policy is one value serving both, which is the thing being pinned.
//
// It is a positive assertion on purpose. Every "the flag is gone" test below
// would pass just as happily against a build where isolation had silently
// stopped being applied at all, and this is the test that would not.
func TestToolPolicy_DefaultIsolatesEveryPlannedNode(t *testing.T) {
	plan := planTwoNodes(t)

	for id, policy := range plan.ToolPolicies {
		if policy.SettingSources == nil {
			t.Errorf("node %q lost layer 1 with no flag asked for: SettingSources = nil, want a pointer to \"\"", id)
			continue
		}
		if *policy.SettingSources != "" {
			t.Errorf("node %q: *SettingSources = %q, want the empty string", id, *policy.SettingSources)
		}
		if !policy.StrictMCPConfig {
			t.Errorf("node %q lost layer 4 with no flag asked for: StrictMCPConfig = false", id)
		}
	}
}

// TestToolPolicy_LoadedUserConfigDropsLayers1And4AndMovesNothingElse is ADR
// 0032 §3(2) and §2.3's table, both directions in one comparison: the two
// layers the operator asked about are off, and the three they did not ask
// about are byte-identical to the isolated run's for the same graph.
//
// The isolated plan is built from the same spec in the same process, so this
// cannot pass by agreeing with a hard-coded expectation that drifted: the
// comparison is against what this build actually does without the option.
func TestToolPolicy_LoadedUserConfigDropsLayers1And4AndMovesNothingElse(t *testing.T) {
	isolated := planTwoNodes(t)
	loaded := planTwoNodes(t, WithLoadedUserConfig())

	for id, policy := range loaded.ToolPolicies {
		if policy.SettingSources != nil {
			t.Errorf("node %q: SettingSources = %q, want nil — layer 1 is the opt-in's whole point", id, *policy.SettingSources)
		}
		if policy.StrictMCPConfig {
			t.Errorf("node %q kept layer 4; --strict-mcp-config on the argv would make \"your MCP servers load\" a claim nobody measured (E5)", id)
		}

		was, ok := isolated.ToolPolicies[id]
		if !ok {
			t.Fatalf("node %q is not in the isolated plan, so there is nothing to compare it against", id)
		}
		if !slices.Equal(policy.AllowedTools, was.AllowedTools) {
			t.Errorf("node %q layer 2 moved: AllowedTools = %v, want %v unchanged", id, policy.AllowedTools, was.AllowedTools)
		}
		if !slices.Equal(policy.Tools, was.Tools) {
			t.Errorf("node %q layer 3 moved: Tools = %v, want %v unchanged — --tools is what keeps this door narrower than `run`", id, policy.Tools, was.Tools)
		}
		if !slices.Equal(policy.DisallowedTools, was.DisallowedTools) {
			t.Errorf("node %q layer 5 moved: DisallowedTools = %v, want %v unchanged", id, policy.DisallowedTools, was.DisallowedTools)
		}
		// The residual layers are asserted non-empty as well as unchanged: two
		// empty slices compare equal, and a build that stopped deriving them
		// would satisfy every line above.
		if len(policy.AllowedTools) == 0 || len(policy.Tools) == 0 || len(policy.DisallowedTools) == 0 {
			t.Errorf("node %q: layers 2/3/5 = %v / %v / %v, want all three still populated", id, policy.AllowedTools, policy.Tools, policy.DisallowedTools)
		}
	}
}

// TestWithLoadedUserConfig_ForcesTheDeescalationsItself is ADR 0032 §3(5) and
// §2.4. Agent mapping and skill activation both rest on layer 1 being "": with
// settings restored, measurement (j) arm X had the working repository's
// same-named skill beat the staged copy 3 of 3, so ADR 0017's and ADR 0022's
// guarantees would be written down and false.
//
// The option is given here with NO CLI flags and WITH both directories that
// would otherwise map and activate, so what passes this test is the
// coordinator's own guarantee and not a call site's discipline. The
// precondition below is why: the identical fixture without the option DOES map
// and DOES activate, so the assertions are a de-escalation and not a fixture
// that never had anything to lose.
func TestWithLoadedUserConfig_ForcesTheDeescalationsItself(t *testing.T) {
	agentDir, skillDir := t.TempDir(), t.TempDir()
	writeAgentFile(t, agentDir, "code-reviewer.md", "name: code-reviewer\ndescription: reviews code\ntools: Read, Grep")
	writeSkillFile(t, skillDir, "architecture-design", "name: architecture-design", "the body")

	spec := `{"name":"two","version":"1","nodes":[` +
		`{"id":"review","prompt":"review the diff","allowed_tools":["Read","Grep"]},` +
		`{"id":"write-up","prompt":"write it up","allowed_tools":["Write"],"depends_on":["review"]}]}`
	plan := func(t *testing.T, opts ...Option) Plan {
		t.Helper()
		fake, _ := newPlannerFake(runner.NodeOutcome{Result: spec})
		p, err := New(fake, append([]Option{WithAgentDirs(agentDir), WithSkillDirs(skillDir)}, opts...)...).
			Plan(context.Background(), "review", nil)
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		return p
	}

	// Precondition: this fixture maps and activates when nothing turns it off.
	mapped := plan(t)
	if node, _ := mapped.Graph.NodeByID("review"); node.Agent != "code-reviewer" {
		t.Fatalf("precondition failed: agent = %q, want code-reviewer — the fixture maps nothing, so nothing below is a de-escalation", node.Agent)
	}
	if !slices.Contains(mapped.ToolPolicies["write-up"].Tools, SkillToolName) {
		t.Fatalf("precondition failed: Tools = %v, want %s — the fixture activates nothing", mapped.ToolPolicies["write-up"].Tools, SkillToolName)
	}

	loaded := plan(t, WithLoadedUserConfig())

	for _, node := range loaded.Graph.Nodes {
		if node.Agent != "" {
			t.Errorf("node %q carries agent %q under the opt-in; ADR 0022's staged system prompt would be shadowed by a same-named agent your own settings discover", node.ID, node.Agent)
		}
	}
	if len(loaded.AgentMappings) != 0 {
		t.Errorf("AgentMappings = %v, want none", loaded.AgentMappings)
	}
	if loaded.SkillActivation != nil {
		t.Errorf("SkillActivation = %+v, want nil", loaded.SkillActivation)
	}
	for id, policy := range loaded.ToolPolicies {
		if slices.Contains(policy.Tools, SkillToolName) {
			t.Errorf("node %q: Tools = %v, want no %s", id, policy.Tools, SkillToolName)
		}
		if len(policy.PluginDirs) != 0 {
			t.Errorf("node %q: PluginDirs = %v, want none", id, policy.PluginDirs)
		}
	}
	// And the widening it was asked for did happen, so this is not a test that
	// passes because the option silently did nothing at all.
	for id, policy := range loaded.ToolPolicies {
		if policy.SettingSources != nil {
			t.Errorf("node %q kept layer 1, so the option was never applied and the de-escalations above prove nothing", id)
		}
	}
}
