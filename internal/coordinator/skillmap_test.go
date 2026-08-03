package coordinator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/handoff"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// writeSkillFile drops one Claude Code skill definition into dir: a
// <name>/SKILL.md with YAML frontmatter between --- fences, then the body the
// mapping inlines (unlike an agent's system prompt, which mapping never reads).
func writeSkillFile(t *testing.T, dir, dirname, frontmatter, body string) {
	t.Helper()
	skillDir := filepath.Join(dir, dirname)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\n" + frontmatter + "\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func planWithSkills(t *testing.T, opts ...Option) Plan {
	t.Helper()
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: reviewSpec})
	plan, err := New(fake, opts...).Plan(context.Background(), "review the diff", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return plan
}

// The happy path: one skill, one clear token match ("review" is a token of
// "pr-code-review"), body under the cap — the body lands in the node's prompt
// inside a nonce-fenced, attributed block; the decision records the source
// path, byte count and SHA-256 of that exact inlined text; and the re-encoded
// Spec carries the inlined prompt so a saved/resumed plan keeps exactly the
// text that was approved.
func TestPlan_SkillMappingHit(t *testing.T) {
	dir := t.TempDir()
	body := "Always begin your reply with the word FENCEPOST."
	writeSkillFile(t, dir, "pr-code-review", "name: pr-code-review\ndescription: reviews pull requests", body)

	plan := planWithSkills(t, WithSkillDirs(dir))

	if len(plan.SkillMappings) != 1 {
		t.Fatalf("mappings = %+v, want one applied pr-code-review mapping", plan.SkillMappings)
	}
	m := plan.SkillMappings[0]
	if m.Skill != "pr-code-review" || m.NodeID != "review" || m.SkippedReason != "" {
		t.Fatalf("mapping = %+v, want pr-code-review applied to review", m)
	}
	if m.Description != "reviews pull requests" {
		t.Errorf("Description = %q, want the frontmatter description carried for the printout", m.Description)
	}
	if m.SourcePath != filepath.Join(dir, "pr-code-review", "SKILL.md") {
		t.Errorf("SourcePath = %q, want the scanned SKILL.md path", m.SourcePath)
	}
	if m.InlinedBytes != len(body) {
		t.Errorf("InlinedBytes = %d, want %d (the exact inlined text)", m.InlinedBytes, len(body))
	}
	sum := sha256.Sum256([]byte(body))
	if m.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("SHA256 = %q does not hash the inlined text — the printed hash would not verify against the saved spec", m.SHA256)
	}

	node, ok := plan.Graph.NodeByID("review")
	if !ok || !strings.Contains(node.Prompt, body) {
		t.Fatalf("node prompt does not carry the inlined body:\n%s", node.Prompt)
	}
	if !strings.HasPrefix(node.Prompt, "review the diff") {
		t.Errorf("inlining must APPEND — the planner-authored prompt must stay first:\n%s", node.Prompt)
	}
	if !strings.Contains(string(plan.Spec), "FENCEPOST") {
		t.Errorf("spec must carry the inlined prompt for save/resume, got %s", plan.Spec)
	}
}

// fenceNonceOf extracts the nonce from a mapped node's opening fence marker.
func fenceNonceOf(t *testing.T, plan Plan) string {
	t.Helper()
	node, _ := plan.Graph.NodeByID("review")
	_, opening, found := strings.Cut(node.Prompt, "--- skill: pr-code-review ")
	if !found {
		t.Fatalf("prompt carries no opening fence marker:\n%s", node.Prompt)
	}
	nonce, _, _ := strings.Cut(opening, " ")
	return nonce
}

// The fence must carry entropy the fenced text cannot predict: both markers
// name the skill and share one per-plan nonce, and the block is attributed to
// its source file — an unfenced delimiter would be forgeable by the very file
// it delimits. Unpredictability is the fence's entire property, so a shape
// check is not enough: the nonce must decode as hex AND vary across plans —
// a hardcoded "abcdef" must fail here.
func TestPlan_InlinedBodyIsNonceFencedAndAttributed(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "pr-code-review", "name: pr-code-review", "the body")

	plan := planWithSkills(t, WithSkillDirs(dir))

	node, _ := plan.Graph.NodeByID("review")
	nonce := fenceNonceOf(t, plan)
	if len(nonce) != 6 {
		t.Fatalf("fence nonce = %q, want 6 hex characters", nonce)
	}
	if _, err := hex.DecodeString(nonce); err != nil {
		t.Fatalf("fence nonce = %q does not decode as hex: %v", nonce, err)
	}
	if !strings.Contains(node.Prompt, "--- end skill: pr-code-review "+nonce+" ---") {
		t.Errorf("closing fence does not repeat the nonce, so the fence is forgeable:\n%s", node.Prompt)
	}
	if !strings.Contains(node.Prompt, "(mapped by oh-my-graph from "+filepath.Join(dir, "pr-code-review", "SKILL.md")+")") {
		t.Errorf("fence does not attribute the inlined text to its source file:\n%s", node.Prompt)
	}

	if second := fenceNonceOf(t, planWithSkills(t, WithSkillDirs(dir))); second == nonce {
		t.Errorf("two plans minted the same nonce %q — a constant nonce is forgeable by the fenced text", nonce)
	}
}

// Every '{{' in an inlined body is neutralized before appending: node.Prompt
// is a handoff template, and un-neutralized skill prose would become template
// code — a {{ artifacts.<id> | inline }} would read another node's artifact
// file with no tool involved, and an unresolvable {{ inputs.x }} would kill
// the node at run time with an InterpolationError (ADR 0012 §4). The recorded
// size and hash must describe the NEUTRALIZED text, or the printed hash would
// not verify against the saved spec.
func TestPlan_InlinedBodyBracesAreNeutralized(t *testing.T) {
	dir := t.TempDir()
	body := "See {{ artifacts.review | inline }} and {{ inputs.x }}."
	writeSkillFile(t, dir, "pr-code-review", "name: pr-code-review", body)

	plan := planWithSkills(t, WithSkillDirs(dir))

	node, _ := plan.Graph.NodeByID("review")
	if strings.Contains(node.Prompt, "{{") {
		t.Fatalf("inlined text still carries '{{' — skill prose became template code:\n%s", node.Prompt)
	}
	neutralized := "See { { artifacts.review | inline }} and { { inputs.x }}."
	if !strings.Contains(node.Prompt, neutralized) {
		t.Fatalf("prompt does not carry the neutralized body:\n%s", node.Prompt)
	}
	m := plan.SkillMappings[0]
	if m.InlinedBytes != len(neutralized) {
		t.Errorf("InlinedBytes = %d, want %d — size must describe the text as inlined", m.InlinedBytes, len(neutralized))
	}
	sum := sha256.Sum256([]byte(neutralized))
	if m.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("SHA256 must hash the neutralized text as inlined, got %q", m.SHA256)
	}
}

// Neutralization must survive adversarial brace runs, and the judge is
// handoff's own placeholderPattern — not the literal "{{" — so the neutralizer
// can never drift from what lint and the runtime actually match. A single
// non-overlapping ReplaceAll fails this: "{{{" becomes "{ {{", re-forming a
// live token (deep review #1).
func TestInlinedSkillText_AdversarialBraceRunsNeverFormPlaceholders(t *testing.T) {
	bodies := []string{
		"{{{ artifacts.review | inline }}}", // odd run: single ReplaceAll leaves "{ {{ artifacts... }}" live
		"{{{ inputs.x }}}",
		"{{{{ inputs.x }}}}", // even run
		"{{{{{ artifacts.a }}}}}",
		"{{ {{ artifacts.a | inline }} }}", // nested
		"{ {{{ artifacts.a }}}",            // pre-spaced prefix plus odd run
		strings.Repeat("{", 63) + " inputs.x " + strings.Repeat("}", 63), // long odd run
		strings.Repeat("{", 64) + " feedback.n " + strings.Repeat("}", 64) + "\n{{{ artifacts.b | inline }}}",
	}
	for _, body := range bodies {
		out := inlinedSkillText(skillDef{body: body})
		if strings.Contains(out, "{{") {
			t.Errorf("neutralized %q still carries '{{': %q", body, out)
		}
		if handoff.ContainsPlaceholder(out) {
			t.Errorf("neutralized %q still forms a live placeholder: %q", body, out)
		}
	}
}

// The cap must measure the NEUTRALIZED size: neutralization grows brace-heavy
// text, so a body under the cap raw can cross it neutralized. Such a body is
// skipped whole — there is deliberately no truncation path, because a cut at
// the boundary could sever a brace run and re-form a live placeholder.
func TestPlan_BodyCrossingCapWhenNeutralizedIsSkipped(t *testing.T) {
	dir := t.TempDir()
	// 3 bytes raw per unit, 4 neutralized: raw stays under the cap, the
	// neutralized text crosses it.
	unit := "{{ "
	count := maxInlinedSkillBytes/len(unit) - 8
	if neutralized := len("{ { ") * count; neutralized <= maxInlinedSkillBytes {
		t.Fatalf("fixture broken: neutralized size %d does not cross the cap", neutralized)
	}
	writeSkillFile(t, dir, "pr-code-review", "name: pr-code-review", strings.TrimSpace(strings.Repeat(unit, count)))

	plan := planWithSkills(t, WithSkillDirs(dir))

	if len(plan.SkillMappings) != 1 || !strings.Contains(plan.SkillMappings[0].SkippedReason, "cap") {
		t.Fatalf("mappings = %+v, want one cap skip measured on the neutralized size", plan.SkillMappings)
	}
	node, _ := plan.Graph.NodeByID("review")
	if node.Prompt != "review the diff" {
		t.Errorf("node prompt = %q, want it untouched — skip must never truncate", node.Prompt)
	}
}

// Later directories shadow earlier ones on a name collision — the same
// precedence shape as agent scanning (v1's CLI only passes the user dir, but
// the mechanism must not silently change if a measured project scan is ever
// added). The versions are distinguishable by body, so the inlined text proves
// which file won.
func TestPlan_LaterSkillDirShadowsEarlier(t *testing.T) {
	userDir, projectDir := t.TempDir(), t.TempDir()
	writeSkillFile(t, userDir, "pr-code-review", "name: pr-code-review", "USER VERSION")
	writeSkillFile(t, projectDir, "pr-code-review", "name: pr-code-review", "PROJECT VERSION")

	plan := planWithSkills(t, WithSkillDirs(userDir, projectDir))

	node, _ := plan.Graph.NodeByID("review")
	if !strings.Contains(node.Prompt, "PROJECT VERSION") || strings.Contains(node.Prompt, "USER VERSION") {
		t.Errorf("prompt must carry the later directory's body:\n%s", node.Prompt)
	}
}

// Two skills matching the same node is ambiguity, and ambiguity is no mapping
// at all — not an entry, not a guess (the same silence as agent mapping).
func TestPlan_AmbiguousSkillMatchMapsNothing(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "pr-code-review", "name: pr-code-review", "body a")
	writeSkillFile(t, dir, "review-bot", "name: review-bot", "body b")

	plan := planWithSkills(t, WithSkillDirs(dir))

	if len(plan.SkillMappings) != 0 {
		t.Fatalf("mappings = %+v, want none for an ambiguous match", plan.SkillMappings)
	}
	node, _ := plan.Graph.NodeByID("review")
	if node.Prompt != "review the diff" {
		t.Errorf("node prompt = %q, want it untouched", node.Prompt)
	}
}

// A candidate whose body exceeds the 16 KiB cap is refused, never truncated
// (severed instructions can invert meaning): the refusal is recorded with a
// reason naming the sizes, the prompt stays untouched, and the Spec is left
// byte-identical to the planner's reply.
func TestPlan_OversizeSkillSkippedWithReason(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "pr-code-review", "name: pr-code-review", strings.Repeat("x", maxInlinedSkillBytes+1))

	plan := planWithSkills(t, WithSkillDirs(dir))

	if len(plan.SkillMappings) != 1 {
		t.Fatalf("mappings = %+v, want one skipped entry", plan.SkillMappings)
	}
	skip := plan.SkillMappings[0]
	if skip.SkippedReason == "" || !strings.Contains(skip.SkippedReason, "16 KiB cap") {
		t.Errorf("SkippedReason = %q, want it to name the cap", skip.SkippedReason)
	}
	node, _ := plan.Graph.NodeByID("review")
	if node.Prompt != "review the diff" {
		t.Errorf("node prompt = %q, want it untouched after a cap skip", node.Prompt)
	}
	if string(plan.Spec) != reviewSpec {
		t.Errorf("spec must stay untouched when nothing applied, got %s", plan.Spec)
	}
}

// A body exactly AT the cap is the boundary and must map — otherwise the cap
// is enforced as > rather than >=, silently shrinking the measured fit.
func TestPlan_SkillExactlyAtCapIsMapped(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "pr-code-review", "name: pr-code-review", strings.Repeat("x", maxInlinedSkillBytes))

	plan := planWithSkills(t, WithSkillDirs(dir))

	if len(plan.SkillMappings) != 1 || plan.SkillMappings[0].SkippedReason != "" {
		t.Fatalf("mappings = %+v, want the at-cap body applied", plan.SkillMappings)
	}
}

// An over-cap body is refused on its RAW size, before neutralization runs:
// the printed size names the file's own bytes (16385 -> "16.0 KiB", where the
// neutralized text would have grown well past that), which pins the
// cap-then-neutralize order — an oversize brace-heavy file must never cost
// the repeated neutralization passes.
func TestPlan_OversizeBodyRefusedOnRawSizeBeforeNeutralization(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "pr-code-review", "name: pr-code-review", strings.Repeat("{", maxInlinedSkillBytes+1))

	plan := planWithSkills(t, WithSkillDirs(dir))

	if len(plan.SkillMappings) != 1 {
		t.Fatalf("mappings = %+v, want one skipped entry", plan.SkillMappings)
	}
	skip := plan.SkillMappings[0]
	if !strings.Contains(skip.SkippedReason, "body 16.0 KiB exceeds") {
		t.Errorf("SkippedReason = %q, want the RAW body size — a grown size means neutralization ran on an already-oversize body", skip.SkippedReason)
	}
}

// A body within the cap whose neutralization GROWS it past the cap is still
// refused: the cap binds the inlined text (what actually rides in the prompt),
// not the file, so the second check reports the grown size.
func TestPlan_NeutralizationGrowthPastCapIsSkipped(t *testing.T) {
	dir := t.TempDir()
	// len = 3*(cap/3) <= cap raw; each "{{x" neutralizes to "{ {x", growing
	// the text by a third — past the cap.
	writeSkillFile(t, dir, "pr-code-review", "name: pr-code-review", strings.Repeat("{{x", maxInlinedSkillBytes/3))

	plan := planWithSkills(t, WithSkillDirs(dir))

	if len(plan.SkillMappings) != 1 {
		t.Fatalf("mappings = %+v, want one skipped entry", plan.SkillMappings)
	}
	skip := plan.SkillMappings[0]
	if skip.SkippedReason == "" || !strings.Contains(skip.SkippedReason, "16 KiB cap") {
		t.Errorf("SkippedReason = %q, want the grown body refused against the cap", skip.SkippedReason)
	}
	node, _ := plan.Graph.NodeByID("review")
	if node.Prompt != "review the diff" {
		t.Errorf("node prompt = %q, want it untouched after a cap skip", node.Prompt)
	}
}

// A SKILL.md over maxSkillFileBytes is refused at read time — silently, like
// any other unusable file, and unlike the printed over-cap skip: nothing that
// large could ever inline, so it is never worth holding in memory, and no
// candidate entry appears at all.
func TestPlan_PathologicallyLargeSkillFileIsIgnored(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "pr-code-review", "name: pr-code-review", strings.Repeat("x", maxSkillFileBytes))

	plan := planWithSkills(t, WithSkillDirs(dir))

	if len(plan.SkillMappings) != 0 {
		t.Fatalf("mappings = %+v, want none for a file over maxSkillFileBytes", plan.SkillMappings)
	}
	node, _ := plan.Graph.NodeByID("review")
	if node.Prompt != "review the diff" {
		t.Errorf("node prompt = %q, want it untouched", node.Prompt)
	}
}

// An agent-mapped node is never mapped a skill: applyAgentMapping drops
// ceiling Layer 1 on that node, and ADR 0012's probe established "no skills
// listing" only under the full ceiling — the composite is unmeasured, so v1
// refuses it with a recorded, printable reason instead of assuming it.
func TestPlan_AgentMappedNodeIsNotSkillMapped(t *testing.T) {
	agentDir, skillDir := t.TempDir(), t.TempDir()
	writeAgentFile(t, agentDir, "code-reviewer.md", "name: code-reviewer\ntools: Read, Grep")
	writeSkillFile(t, skillDir, "pr-code-review", "name: pr-code-review", "the body")

	plan := planWithSkills(t, WithAgentDirs(agentDir), WithSkillDirs(skillDir))

	node, _ := plan.Graph.NodeByID("review")
	if node.Agent != "code-reviewer" {
		t.Fatalf("precondition failed: node agent = %q, want code-reviewer", node.Agent)
	}
	if len(plan.SkillMappings) != 1 {
		t.Fatalf("mappings = %+v, want one skipped entry", plan.SkillMappings)
	}
	skip := plan.SkillMappings[0]
	if !strings.Contains(skip.SkippedReason, "agent-mapped") {
		t.Errorf("SkippedReason = %q, want it to name the agent-mapped refusal", skip.SkippedReason)
	}
	if strings.Contains(node.Prompt, "the body") {
		t.Errorf("an agent-mapped node's prompt must stay uninlined:\n%s", node.Prompt)
	}
}

// Scan failures are silent no-mapping, never an error: a missing directory, a
// SKILL.md with no frontmatter, frontmatter with no name, and a skill with an
// empty body must all just drop out — zero-config stays zero-config. A valid
// skill sits in the SAME directory as the broken ones and must still map:
// without that positive control the assertions are satisfiable by a scanner
// that rejects everything (deep review #4).
func TestPlan_SkillScanFailuresAreSilent(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken", "SKILL.md"), []byte("no frontmatter here"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSkillFile(t, dir, "nameless", "description: has no name", "a body")
	writeSkillFile(t, dir, "bodyless-review", "name: bodyless-review", "")
	writeSkillFile(t, dir, "pr-code-review", "name: pr-code-review", "the valid body")

	plan := planWithSkills(t, WithSkillDirs(filepath.Join(dir, "does-not-exist"), dir))

	if len(plan.SkillMappings) != 1 {
		t.Fatalf("mappings = %+v, want exactly the one valid skill mapped past its broken neighbours", plan.SkillMappings)
	}
	if m := plan.SkillMappings[0]; m.Skill != "pr-code-review" || m.SkippedReason != "" {
		t.Fatalf("mapping = %+v, want pr-code-review applied", m)
	}
	node, _ := plan.Graph.NodeByID("review")
	if !strings.Contains(node.Prompt, "the valid body") {
		t.Errorf("the valid skill's body must land despite broken neighbours:\n%s", node.Prompt)
	}
}

// A SKILL.md saved with a UTF-8 BOM and CRLF line endings — the shape a
// Windows editor produces — must still parse and map: parseSkillFile strips
// the BOM and accepts \r\n around the frontmatter fences.
func TestPlan_BOMAndCRLFSkillFileStillMaps(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "pr-code-review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "\ufeff---\r\nname: pr-code-review\r\n---\r\n\r\nthe windows body\r\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := planWithSkills(t, WithSkillDirs(dir))

	if len(plan.SkillMappings) != 1 || plan.SkillMappings[0].SkippedReason != "" {
		t.Fatalf("mappings = %+v, want the BOM+CRLF skill applied", plan.SkillMappings)
	}
	node, _ := plan.Graph.NodeByID("review")
	if !strings.Contains(node.Prompt, "the windows body") {
		t.Errorf("node prompt does not carry the BOM+CRLF skill's body:\n%s", node.Prompt)
	}
}

// One skill matching two nodes is not ambiguity — ambiguity is per node, over
// skills — so both nodes get the body, inside fences sharing the one per-plan
// nonce.
func TestPlan_OneSkillMapsOntoTwoNodes(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "pr-code-review", "name: pr-code-review", "the shared body")

	fake, _ := newPlannerFake(runner.NodeOutcome{Result: `{"name":"two-reviews","version":"1","nodes":[` +
		`{"id":"review-a","prompt":"first pass","allowed_tools":["Read"]},` +
		`{"id":"review-b","prompt":"second pass","allowed_tools":["Read"],"depends_on":["review-a"]}]}`})
	plan, err := New(fake, WithSkillDirs(dir)).Plan(context.Background(), "review twice", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plan.SkillMappings) != 2 {
		t.Fatalf("mappings = %+v, want the skill applied to both matching nodes", plan.SkillMappings)
	}
	for _, id := range []string{"review-a", "review-b"} {
		node, _ := plan.Graph.NodeByID(id)
		if !strings.Contains(node.Prompt, "the shared body") {
			t.Errorf("node %s does not carry the shared body:\n%s", id, node.Prompt)
		}
	}
	nodeA, _ := plan.Graph.NodeByID("review-a")
	_, opening, _ := strings.Cut(nodeA.Prompt, "--- skill: pr-code-review ")
	nonce, _, _ := strings.Cut(opening, " ")
	nodeB, _ := plan.Graph.NodeByID("review-b")
	if !strings.Contains(nodeB.Prompt, "--- skill: pr-code-review "+nonce+" ") {
		t.Errorf("both nodes must share the one per-plan nonce %q:\n%s", nonce, nodeB.Prompt)
	}
}

// WithoutSkillMapping (the --no-skill-mapping flag) wins even over a directory
// holding a perfectly mappable skill: nothing is scanned, nothing changes.
func TestPlan_WithoutSkillMappingDisablesMapping(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "pr-code-review", "name: pr-code-review", "the body")

	plan := planWithSkills(t, WithSkillDirs(dir), WithoutSkillMapping())

	if len(plan.SkillMappings) != 0 {
		t.Fatalf("mappings = %+v, want none with mapping off", plan.SkillMappings)
	}
	node, _ := plan.Graph.NodeByID("review")
	if node.Prompt != "review the diff" {
		t.Errorf("node prompt = %q, want it untouched with mapping off", node.Prompt)
	}
	if string(plan.Spec) != reviewSpec {
		t.Errorf("spec must stay untouched with mapping off, got %s", plan.Spec)
	}
}

// A short shared token must not match by prefix: "dev" (under minMatchPrefix)
// against a "developer" skill would be a guess — the same rule, and the same
// boundary, as agent matching.
func TestPlan_ShortTokenDoesNotMatchSkillByPrefix(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "developer", "name: developer", "the body")

	fake, _ := newPlannerFake(runner.NodeOutcome{Result: `{"name":"dev-run","version":"1","nodes":[` +
		`{"id":"dev","prompt":"do the work","allowed_tools":["Read"]}]}`})
	plan, err := New(fake, WithSkillDirs(dir)).Plan(context.Background(), "do the work", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.SkillMappings) != 0 {
		t.Fatalf("mappings = %+v, want none for a sub-minimum prefix", plan.SkillMappings)
	}
}

// The fence's METADATA is chosen by the scanned file too: a skill whose
// frontmatter name — or whose directory name, which becomes the printed source
// path — carries '{{' would put a live placeholder into node.Prompt from
// outside the body the neutralizer covers, and the scheduler's
// handoff.Interpolate would then resolve it (a file read, or a run-killing
// InterpolationError). Both must land inert (review round 4).
func TestPlan_FenceMetadataBracesAreNeutralized(t *testing.T) {
	cases := []struct {
		what     string
		dirname  string
		nameLine string
	}{
		// A name may carry no whitespace (parseSkillFile refuses that), so the
		// reachable shape is the whitespace-free placeholder — which
		// placeholderPattern matches just as happily.
		{"frontmatter name", "pr-code-review", "name: pr-code-review{{artifacts.review}}"},
		{"source path", "pr-code-review{{ inputs.x }}", "name: pr-code-review"},
	}
	for _, tc := range cases {
		t.Run(tc.what, func(t *testing.T) {
			dir := t.TempDir()
			writeSkillFile(t, dir, tc.dirname, tc.nameLine+"\ndescription: reviews pull requests", "the body")

			plan := planWithSkills(t, WithSkillDirs(dir))

			if len(plan.SkillMappings) != 1 || plan.SkillMappings[0].SkippedReason != "" {
				t.Fatalf("mappings = %+v, want one applied mapping", plan.SkillMappings)
			}
			node, ok := plan.Graph.NodeByID("review")
			if !ok || !strings.Contains(node.Prompt, "the body") {
				t.Fatalf("node prompt does not carry the inlined body:\n%s", node.Prompt)
			}
			if handoff.ContainsPlaceholder(node.Prompt) {
				t.Errorf("fence metadata left a live placeholder in the prompt:\n%s", node.Prompt)
			}
		})
	}
}

// A skill kept under version control is commonly symlinked into
// ~/.claude/skills rather than copied there. os.ReadDir does not follow
// symlinks, so entry.IsDir() is false for such a directory — the scan must
// stat the entry instead, or every dotfiles-managed skill is silently
// invisible while the equivalent symlinked agent .md already scans fine.
func TestPlan_SymlinkedSkillDirIsScanned(t *testing.T) {
	store := t.TempDir()
	writeSkillFile(t, store, "pr-code-review", "name: pr-code-review", "the symlinked body")

	scanned := t.TempDir()
	if err := os.Symlink(filepath.Join(store, "pr-code-review"), filepath.Join(scanned, "pr-code-review")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	plan := planWithSkills(t, WithSkillDirs(scanned))

	if len(plan.SkillMappings) != 1 || plan.SkillMappings[0].SkippedReason != "" {
		t.Fatalf("mappings = %+v, want the symlinked skill mapped", plan.SkillMappings)
	}
	node, _ := plan.Graph.NodeByID("review")
	if !strings.Contains(node.Prompt, "the symlinked body") {
		t.Errorf("node prompt does not carry the symlinked skill's body:\n%s", node.Prompt)
	}
}

// DefaultSkillDirs is the only place the real filesystem location enters the
// coordinator, and its shape is the security-relevant half of ADR 0012: the
// user's own directory is scanned and the PROJECT directory
// (<cwd>/.claude/skills) — 100% of the genuinely new injection surface — is
// not. A later append of a project directory must fail here, not ship.
func TestDefaultSkillDirs_UserSkillsOnlyNeverTheProjectDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dirs := DefaultSkillDirs()

	want := []string{filepath.Join(home, ".claude", "skills")}
	if len(dirs) != len(want) || dirs[0] != want[0] {
		t.Fatalf("DefaultSkillDirs() = %v, want exactly %v", dirs, want)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		if !filepath.IsAbs(dir) {
			t.Errorf("dir %q is relative — it would resolve against the invocation directory", dir)
		}
		if rel, err := filepath.Rel(cwd, dir); err == nil && !strings.HasPrefix(rel, "..") {
			t.Errorf("dir %q sits under the working directory — the project scan is cut from v1", dir)
		}
	}
}

// An unresolvable home just drops out: the scan is silent about missing
// directories anyway, and a malformed path (".claude/skills" relative, or
// "/.claude/skills") would be a scan of somewhere nobody asked for.
func TestDefaultSkillDirs_NoHomeScansNothing(t *testing.T) {
	t.Setenv("HOME", "")

	if dirs := DefaultSkillDirs(); len(dirs) != 0 {
		t.Fatalf("DefaultSkillDirs() = %v, want none when the home cannot be resolved", dirs)
	}
}
