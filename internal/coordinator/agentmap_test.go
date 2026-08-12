package coordinator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runner"
)

// reviewSpec is a planner reply with one node whose id ("review") token-matches
// an agent named code-reviewer ("review" is a >=4-rune prefix of "reviewer").
const reviewSpec = `{"name":"review-run","version":"1","nodes":[` +
	`{"id":"review","prompt":"review the diff","allowed_tools":["Read","Grep"]}]}`

// writeAgentFile drops one Claude Code agent definition into dir: YAML
// frontmatter between --- fences, then a system prompt the mapping never reads.
func writeAgentFile(t *testing.T, dir, filename, frontmatter string) {
	t.Helper()
	body := "---\n" + frontmatter + "\n---\n\nYou are the agent.\n"
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func planWithAgents(t *testing.T, opts ...Option) Plan {
	t.Helper()
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: reviewSpec})
	plan, err := New(fake, opts...).Plan(context.Background(), "review the diff", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return plan
}

// The happy path: one agent, one clear token match, tools inside the node's
// ceiling — the node gets agent:, the decision lands on AgentMappings, the
// definition is staged, and the re-encoded Spec carries the mapping so a
// saved/resumed plan keeps it.
//
// The SettingSources assertion is inverted from what it was until ADR 0022,
// and it is the load-bearing cell of this whole file: a mapped node used to
// drop layer 1 so `--agent` could resolve from the user's own directories, and
// measurement (k) showed what that cost — the shipped argv breached ADR 0004's
// E1 ceiling 2 of 2 while the staged-definition argv was denied 3 of 3
// (docs/measurements/0017-staged-agent-restores-layer-1.md). A nil here again
// would mean the fix was silently reverted, so the message says so.
func TestPlan_AgentMappingHit(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "code-reviewer.md", "name: code-reviewer\ndescription: reviews code\ntools: Read, Grep")

	plan := planWithAgents(t, WithAgentDirs(dir))

	node, ok := plan.Graph.NodeByID("review")
	if !ok || node.Agent != "code-reviewer" {
		t.Fatalf("node agent = %q, want code-reviewer", node.Agent)
	}
	if len(plan.AgentMappings) != 1 || plan.AgentMappings[0].Agent != "code-reviewer" || plan.AgentMappings[0].SkippedReason != "" {
		t.Fatalf("mappings = %+v, want one applied code-reviewer mapping", plan.AgentMappings)
	}
	ss := plan.ToolPolicies["review"].SettingSources
	if ss == nil || *ss != "" {
		t.Errorf("SettingSources = %v, want a pointer to \"\": a mapped node keeps ceiling layer 1 since ADR 0022, and its agent resolves from the staged --plugin-dir instead", ss)
	}
	if plan.AgentStaging == nil {
		t.Fatal("an applied mapping must carry AgentStaging: without it --agent has nothing to resolve against under layer 1")
	}
	if agents := plan.AgentStaging.Agents(); len(agents) != 1 || agents[0].Name != "code-reviewer" {
		t.Errorf("staged agents = %+v, want exactly code-reviewer", agents)
	}
	if !strings.Contains(string(plan.Spec), `"agent":"code-reviewer"`) {
		t.Errorf("spec must carry the mapping for save/resume, got %s", plan.Spec)
	}
}

// Project agents shadow user agents of the same name (later dir wins). The
// versions are distinguishable by behavior: the user's declares Bash (beyond
// the ceiling, would be skipped), the project's declares Read (mappable) — so
// an applied mapping proves the project file won.
func TestPlan_ProjectAgentShadowsUserAgent(t *testing.T) {
	userDir, projectDir := t.TempDir(), t.TempDir()
	writeAgentFile(t, userDir, "code-reviewer.md", "name: code-reviewer\ntools: Bash")
	writeAgentFile(t, projectDir, "code-reviewer.md", "name: code-reviewer\ntools: Read")

	plan := planWithAgents(t, WithAgentDirs(userDir, projectDir))

	if len(plan.AgentMappings) != 1 || plan.AgentMappings[0].SkippedReason != "" {
		t.Fatalf("mappings = %+v, want the project version applied", plan.AgentMappings)
	}
	node, _ := plan.Graph.NodeByID("review")
	if node.Agent != "code-reviewer" {
		t.Errorf("node agent = %q, want code-reviewer", node.Agent)
	}
}

// Two agents matching the same node is ambiguity, and ambiguity is no mapping
// at all — not an entry, not a guess.
func TestPlan_AmbiguousMatchMapsNothing(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "code-reviewer.md", "name: code-reviewer\ntools: Read")
	writeAgentFile(t, dir, "review-bot.md", "name: review-bot\ntools: Read")

	plan := planWithAgents(t, WithAgentDirs(dir))

	if len(plan.AgentMappings) != 0 {
		t.Fatalf("mappings = %+v, want none for an ambiguous match", plan.AgentMappings)
	}
	node, _ := plan.Graph.NodeByID("review")
	if node.Agent != "" {
		t.Errorf("node agent = %q, want empty", node.Agent)
	}
}

// A candidate whose frontmatter tools exceed the node's planned allowlist is
// refused: the refusal is recorded (so the printout can say so), the node
// stays unmapped, its isolation policy stays intact, and the Spec is left
// byte-identical to the planner's reply.
func TestPlan_AgentBeyondCeilingSkippedWithReason(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "code-reviewer.md", "name: code-reviewer\ntools: Read, Bash")

	plan := planWithAgents(t, WithAgentDirs(dir))

	if len(plan.AgentMappings) != 1 {
		t.Fatalf("mappings = %+v, want one skipped entry", plan.AgentMappings)
	}
	skip := plan.AgentMappings[0]
	if skip.SkippedReason == "" || !strings.Contains(skip.SkippedReason, "Bash") {
		t.Errorf("SkippedReason = %q, want it to name the offending tool", skip.SkippedReason)
	}
	node, _ := plan.Graph.NodeByID("review")
	if node.Agent != "" {
		t.Errorf("node agent = %q, want empty after a ceiling skip", node.Agent)
	}
	if plan.ToolPolicies["review"].SettingSources == nil {
		t.Error("an unmapped node must keep its settings isolation")
	}
	if string(plan.Spec) != reviewSpec {
		t.Errorf("spec must stay untouched when nothing applied, got %s", plan.Spec)
	}
}

// Scan failures are silent no-mapping, never an error: a missing directory, a
// file with no frontmatter, and frontmatter with no name must all just drop
// out — zero-config stays zero-config.
func TestPlan_AgentScanFailuresAreSilent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.md"), []byte("no frontmatter here"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeAgentFile(t, dir, "nameless.md", "description: has no name")

	plan := planWithAgents(t, WithAgentDirs(filepath.Join(dir, "does-not-exist"), dir))

	if len(plan.AgentMappings) != 0 {
		t.Fatalf("mappings = %+v, want none from a failed scan", plan.AgentMappings)
	}
	node, _ := plan.Graph.NodeByID("review")
	if node.Agent != "" {
		t.Errorf("node agent = %q, want empty", node.Agent)
	}
}

// WithoutAgentMapping (the --no-agent-mapping flag) wins even over a directory
// holding a perfectly mappable agent: nothing is scanned, nothing changes.
func TestPlan_WithoutAgentMappingDisablesMapping(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "code-reviewer.md", "name: code-reviewer\ntools: Read")

	plan := planWithAgents(t, WithAgentDirs(dir), WithoutAgentMapping())

	if len(plan.AgentMappings) != 0 {
		t.Fatalf("mappings = %+v, want none with mapping off", plan.AgentMappings)
	}
	node, _ := plan.Graph.NodeByID("review")
	if node.Agent != "" {
		t.Errorf("node agent = %q, want empty", node.Agent)
	}
	if string(plan.Spec) != reviewSpec {
		t.Errorf("spec must stay untouched with mapping off, got %s", plan.Spec)
	}
}

// WithoutAgentsNamed (the --no-agent flag) refuses ONE agent while mapping
// stays on: the node keeps its settings isolation and therefore its scope
// ceiling and its Skill tool, the refusal is recorded so the printout can name
// it, and the Spec stays the planner's own bytes because nothing applied.
func TestPlan_DeclinedAgentIsRefusedWithMappingStillOn(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "code-reviewer.md", "name: code-reviewer\ntools: Read, Grep")

	plan := planWithAgents(t, WithAgentDirs(dir), WithoutAgentsNamed("code-reviewer"))

	if len(plan.AgentMappings) != 1 || plan.AgentMappings[0].SkippedReason != declinedReason {
		t.Fatalf("mappings = %+v, want one entry skipped by the flag", plan.AgentMappings)
	}
	if plan.AgentMappings[0].Agent != "code-reviewer" || plan.AgentMappings[0].NodeID != "review" {
		t.Errorf("the skip must name the agent and the node it would have taken: %+v", plan.AgentMappings[0])
	}
	node, _ := plan.Graph.NodeByID("review")
	if node.Agent != "" {
		t.Errorf("node agent = %q, want empty after a decline", node.Agent)
	}
	// Since ADR 0022 layer 1 is no longer what a decline buys back — a mapped
	// node keeps it too — so what is asserted here is that the decline changed
	// nothing about the ceiling, and the capability it does buy back (skill
	// activation eligibility) is asserted in skillstage_test.go.
	if plan.ToolPolicies["review"].SettingSources == nil {
		t.Error("a declined node must keep its settings isolation")
	}
	if plan.AgentStaging != nil {
		t.Error("nothing applied, so nothing may be staged")
	}
	if string(plan.Spec) != reviewSpec {
		t.Errorf("spec must stay untouched when nothing applied, got %s", plan.Spec)
	}
}

// The name is matched case-insensitively, and a name matching no agent is not
// an error: it declines nothing and every other mapping stands, exactly like a
// scanned directory that is not there.
func TestPlan_DeclineMatchesCaseInsensitivelyAndToleratesUnknownNames(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "code-reviewer.md", "name: code-reviewer\ntools: Read")

	folded := planWithAgents(t, WithAgentDirs(dir), WithoutAgentsNamed("Code-Reviewer"))
	if len(folded.AgentMappings) != 1 || folded.AgentMappings[0].SkippedReason != declinedReason {
		t.Fatalf("mappings = %+v, want the decline to match regardless of case", folded.AgentMappings)
	}

	unknown := planWithAgents(t, WithAgentDirs(dir), WithoutAgentsNamed("architect"))
	if len(unknown.AgentMappings) != 1 || unknown.AgentMappings[0].SkippedReason != "" {
		t.Fatalf("mappings = %+v, want an unknown decline to change nothing", unknown.AgentMappings)
	}
}

// The ordering guard: a decline may only REMOVE a mapping, never cause one.
// Two agents match "review", so today nothing maps. Declining one of them must
// leave it that way — if the decline were applied by dropping the definition
// before matching, the survivor would become the single candidate and this
// opt-out would have MAPPED a node that nothing mapped before.
func TestPlan_DecliningOneOfTwoAmbiguousAgentsStillMapsNothing(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "code-reviewer.md", "name: code-reviewer\ntools: Read")
	writeAgentFile(t, dir, "review-bot.md", "name: review-bot\ntools: Read")

	plan := planWithAgents(t, WithAgentDirs(dir), WithoutAgentsNamed("review-bot"))

	if len(plan.AgentMappings) != 0 {
		t.Fatalf("mappings = %+v, want none — the match is still ambiguous", plan.AgentMappings)
	}
	node, _ := plan.Graph.NodeByID("review")
	if node.Agent != "" {
		t.Errorf("node agent = %q — an opt-out promoted the other candidate", node.Agent)
	}
}

// A short shared token must not match by prefix: "dev" (under minMatchPrefix)
// against "developer" and "devops" would be a guess, and — belt and braces —
// even textually it is ambiguous, which already yields no mapping.
func TestPlan_ShortTokenDoesNotMatchByPrefix(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "developer.md", "name: developer\ntools: Read")

	fake, _ := newPlannerFake(runner.NodeOutcome{Result: `{"name":"dev-run","version":"1","nodes":[` +
		`{"id":"dev","prompt":"do the work","allowed_tools":["Read"]}]}`})
	plan, err := New(fake, WithAgentDirs(dir)).Plan(context.Background(), "do the work", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.AgentMappings) != 0 {
		t.Fatalf("mappings = %+v, want none for a sub-minimum prefix", plan.AgentMappings)
	}
}
