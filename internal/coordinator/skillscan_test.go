package coordinator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runner"
)

// writeSkillFile drops one Claude Code skill definition into dir: a
// <name>/SKILL.md with YAML frontmatter between --- fences, then the body.
// Under ADR 0017 nothing reads that body into a prompt — the CLI does, at run
// time, from the staged copy — but a definition with no body is not a usable
// skill, so the fixture writes one.
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

// stagedNames is the corpus a plan reports, by name — the unit almost every
// assertion below is about, since ADR 0017 stages the WHOLE corpus and the
// interesting question is always which definitions the scan judged usable.
func stagedNames(plan Plan) []string {
	if plan.SkillActivation == nil {
		return nil
	}
	names := make([]string, 0, len(plan.SkillActivation.Skills))
	for _, sk := range plan.SkillActivation.Skills {
		names = append(names, sk.Name)
	}
	return names
}

// Later directories shadow earlier ones on a name collision — the same
// precedence shape as agent scanning (v1's CLI only passes the user dir, but
// the mechanism must not silently change if a measured project scan is ever
// added). The versions are distinguishable by body, so the staged bytes prove
// which file won.
func TestPlan_LaterSkillDirShadowsEarlier(t *testing.T) {
	userDir, projectDir := t.TempDir(), t.TempDir()
	writeSkillFile(t, userDir, "pr-code-review", "name: pr-code-review", "USER VERSION")
	writeSkillFile(t, projectDir, "pr-code-review", "name: pr-code-review", "PROJECT VERSION")

	plan := planWithSkills(t, WithSkillDirs(userDir, projectDir))

	staged := stageInto(t, plan)
	body := readStaged(t, staged, "skills/pr-code-review/SKILL.md")
	if !strings.Contains(body, "PROJECT VERSION") || strings.Contains(body, "USER VERSION") {
		t.Errorf("the staged copy must be the later directory's file:\n%s", body)
	}

	// Winning silently would be fine; losing silently is not. The count the
	// printout shows is the size of the deduped set, so a collision lowers it
	// with no explanation available anywhere unless the loser is named.
	if plan.SkillScan == nil {
		t.Fatal("SkillScan = nil, want the scan that resolved the collision")
	}
	if plan.SkillScan.Found != 1 {
		t.Errorf("Found = %d, want 1: two files, one surviving name", plan.SkillScan.Found)
	}
	want := filepath.Join(userDir, "pr-code-review", "SKILL.md")
	if got := plan.SkillScan.Shadowed; len(got) != 1 || got[0] != want {
		t.Errorf("Shadowed = %v, want the losing file [%s]", got, want)
	}
}

// The collision available on a real machine today needs no second directory:
// `name:` need not equal the directory it sits in, so two skill directories
// under the one scanned tree can declare the same name, and then the winner is
// only whichever os.ReadDir returned later. Deterministic, but not something a
// user could ever infer from a count that quietly dropped by one.
func TestPlan_SameNameInOneDirIsReportedShadowed(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "a-first", "name: pr-code-review", "FIRST VERSION")
	writeSkillFile(t, dir, "b-second", "name: pr-code-review", "SECOND VERSION")

	plan := planWithSkills(t, WithSkillDirs(dir))

	if plan.SkillScan == nil || plan.SkillScan.Found != 1 {
		t.Fatalf("SkillScan = %+v, want one surviving definition", plan.SkillScan)
	}
	want := filepath.Join(dir, "a-first", "SKILL.md")
	if got := plan.SkillScan.Shadowed; len(got) != 1 || got[0] != want {
		t.Fatalf("Shadowed = %v, want the lexically earlier file [%s]", got, want)
	}
	staged := stageInto(t, plan)
	if body := readStaged(t, staged, "skills/pr-code-review/SKILL.md"); !strings.Contains(body, "SECOND VERSION") {
		t.Errorf("the reported winner must be the file that was actually staged:\n%s", body)
	}
}

// No collision, nothing to report: the shadow line must not appear on the
// normal plan, or it stops being a signal.
func TestPlan_NoCollisionRecordsNoShadow(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "pr-code-review", "name: pr-code-review", "the body")

	plan := planWithSkills(t, WithSkillDirs(dir))

	if plan.SkillScan == nil {
		t.Fatal("SkillScan = nil, want a recorded scan")
	}
	if len(plan.SkillScan.Shadowed) != 0 {
		t.Errorf("Shadowed = %v, want empty when every skill name is unique", plan.SkillScan.Shadowed)
	}
}

// A pathologically large SKILL.md is not read at all. Under ADR 0012 this was
// the bound on what could be held in memory to inline; under ADR 0017 nothing
// is inlined, so the same bound is what keeps one absurd file out of the
// staged corpus and out of the per-spawn re-materialization. It is silent —
// the positive control beside it proves the whole scan did not just give up.
func TestPlan_PathologicallyLargeSkillFileIsIgnored(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "huge", "name: huge", strings.Repeat("x", maxSkillFileBytes+1))
	writeSkillFile(t, dir, "pr-code-review", "name: pr-code-review", "small enough")

	plan := planWithSkills(t, WithSkillDirs(dir))

	if plan.SkillScan == nil || plan.SkillScan.Found != 1 {
		t.Fatalf("SkillScan = %+v, want only the readable skill counted", plan.SkillScan)
	}
	if got := stagedNames(plan); len(got) != 1 || got[0] != "pr-code-review" {
		t.Fatalf("staged = %v, want only the readable skill", got)
	}
}

// Scan failures are silent no-activation, never an error: a missing directory,
// a SKILL.md with no frontmatter, frontmatter with no name, and a skill with
// an empty body must all just drop out — zero-config stays zero-config. A
// valid skill sits in the SAME directory as the broken ones and must still be
// staged: without that positive control the assertions are satisfiable by a
// scanner that rejects everything.
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

	if got := stagedNames(plan); len(got) != 1 || got[0] != "pr-code-review" {
		t.Fatalf("staged = %v, want exactly the one valid skill past its broken neighbours", got)
	}
	staged := stageInto(t, plan)
	if body := readStaged(t, staged, "skills/pr-code-review/SKILL.md"); !strings.Contains(body, "the valid body") {
		t.Errorf("the valid skill must be staged despite broken neighbours:\n%s", body)
	}
}

// A SKILL.md saved with a UTF-8 BOM and CRLF line endings — the shape a
// Windows editor produces — must still parse: parseSkillFile strips the BOM
// and accepts \r\n around the frontmatter fences.
func TestPlan_BOMAndCRLFSkillFileIsStaged(t *testing.T) {
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

	if got := stagedNames(plan); len(got) != 1 {
		t.Fatalf("staged = %v, want the BOM+CRLF skill", got)
	}
	staged := stageInto(t, plan)
	if body := readStaged(t, staged, "skills/pr-code-review/SKILL.md"); !strings.Contains(body, "the windows body") {
		t.Errorf("staged copy does not carry the BOM+CRLF skill's body:\n%s", body)
	}
}

// A skill scan that ran must SAY it ran, whatever it found. An empty corpus is
// the case the record exists for: "your skills were read and there are none"
// and "your skills were never looked at" are the same silence otherwise. A
// scan that finds nothing stays a silent no-activation, never an error: the
// plan below still succeeds.
func TestPlan_SkillScanIsRecordedEvenWhenNothingIsStaged(t *testing.T) {
	empty := t.TempDir()
	missing := filepath.Join(empty, "does-not-exist")

	plan := planWithSkills(t, WithSkillDirs(missing, empty))

	if plan.SkillScan == nil {
		t.Fatal("SkillScan = nil, but a scan ran: an empty corpus must still name where it looked")
	}
	if plan.SkillScan.Found != 0 {
		t.Errorf("Found = %d, want 0", plan.SkillScan.Found)
	}
	if got := plan.SkillScan.Dirs; len(got) != 2 || got[0] != missing || got[1] != empty {
		t.Errorf("Dirs = %v, want both scanned directories in order [%s %s]", got, missing, empty)
	}
	if plan.SkillActivation == nil || plan.SkillActivation.Enabled {
		t.Fatalf("SkillActivation = %+v, want activation off over an empty corpus", plan.SkillActivation)
	}
	if plan.SkillActivation.DisabledReason == "" {
		t.Error("DisabledReason is empty: an OFF activation that says nothing is the silence this record exists to remove")
	}
}

// The recorded Dirs must be the plan's own copy: a caller that mutates the
// slice it passed to WithSkillDirs must not be able to rewrite what an
// already-printed plan says it scanned.
func TestPlan_SkillScanDirsAreNotAliased(t *testing.T) {
	dirs := []string{t.TempDir()}
	plan := planWithSkills(t, WithSkillDirs(dirs...))
	if plan.SkillScan == nil {
		t.Fatal("SkillScan = nil, want a recorded scan")
	}
	recorded := plan.SkillScan.Dirs[0]

	dirs[0] = "/somewhere/else"
	if plan.SkillScan.Dirs[0] != recorded {
		t.Errorf("Dirs[0] = %q after the caller's slice changed, want the recorded %q", plan.SkillScan.Dirs[0], recorded)
	}
}

// A skill kept under version control is commonly symlinked into
// ~/.claude/skills rather than copied there. os.ReadDir does not follow
// symlinks, so entry.IsDir() is false for such a directory — the scan must
// stat the entry instead, or every dotfiles-managed skill is silently
// invisible while the equivalent symlinked agent .md already scans fine. The
// STAGER has the mirror-image obligation: filepath.WalkDir would not descend
// through the symlink either, so a corpus that lists a skill it cannot stage
// is the failure this pins.
func TestPlan_SymlinkedSkillDirIsScanned(t *testing.T) {
	store := t.TempDir()
	writeSkillFile(t, store, "pr-code-review", "name: pr-code-review", "the symlinked body")

	scanned := t.TempDir()
	if err := os.Symlink(filepath.Join(store, "pr-code-review"), filepath.Join(scanned, "pr-code-review")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	plan := planWithSkills(t, WithSkillDirs(scanned))

	if got := stagedNames(plan); len(got) != 1 || got[0] != "pr-code-review" {
		t.Fatalf("staged = %v, want the symlinked skill", got)
	}
	staged := stageInto(t, plan)
	if body := readStaged(t, staged, "skills/pr-code-review/SKILL.md"); !strings.Contains(body, "the symlinked body") {
		t.Errorf("staged copy does not carry the symlinked skill's body:\n%s", body)
	}
}

// DefaultSkillDirs is the only place the real filesystem location enters the
// coordinator, and its shape is the security-relevant half of ADR 0012, held
// by ADR 0017 for a sharper reason: under activation a project directory's
// skills would be STAGED for every unattended node, so the surface a cloned
// repository gets is larger than it was under a 7% matcher, not smaller. A
// later append of a project directory must fail here, not ship.
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
			t.Errorf("dir %q sits under the working directory — the project scan is cut", dir)
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
