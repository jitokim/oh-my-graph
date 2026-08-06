package coordinator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// newCheckout makes dir look like a git checkout to the detector: an ordinary
// clone, whose .git is a directory.
func newCheckout(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// scanOf resolves the boundary from invocationDir and scans one goal plus a
// graph built from promptsByNode, in the order given.
func scanOf(t *testing.T, invocationDir, goal string, prompts ...[2]string) *UnisolatedScan {
	t.Helper()
	root, ok := resolveInvocationRoot(invocationDir)
	if !ok {
		t.Fatalf("could not resolve the invocation boundary at %s", invocationDir)
	}
	g := &graph.Graph{Name: "scanned", Version: "1"}
	for _, p := range prompts {
		g.Nodes = append(g.Nodes, graph.Node{ID: p[0], Prompt: p[1]})
	}
	return scanUnisolated(root, goal, g)
}

// The reported scenario (#103), reduced: `auto` invoked inside repo A, a goal
// naming repo B by absolute path, and the planner copying that path into a
// node's prompt. Both mentions are one warning, because both are one HEAD, and
// the warning names both sources so the user can see it came from their own
// goal as well as from the plan.
func TestScanUnisolated_ForeignCheckoutNamedByGoalAndNode(t *testing.T) {
	home := t.TempDir()
	repoA := newCheckout(t, filepath.Join(home, "lbox-argo-applications"))
	repoB := newCheckout(t, filepath.Join(home, "lbox-ai-memory"))
	if err := os.MkdirAll(repoA, 0o755); err != nil {
		t.Fatal(err)
	}

	scan := scanOf(t, repoA,
		"harden ingest latency in "+repoB+" and add DLQ alarms here",
		[2]string{"mem-impl", "Work in " + repoB + " on the feature branch."},
		[2]string{"argo-impl", "Add the alarms in this repository."},
	)

	if scan == nil {
		t.Fatal("a goal and a node both naming a second local checkout must warn")
	}
	if len(scan.Paths) != 1 {
		t.Fatalf("paths = %+v, want exactly one entry for %s", scan.Paths, repoB)
	}
	got := scan.Paths[0]
	if got.Repo != resolveSymlinks(repoB) {
		t.Errorf("repo = %s, want %s", got.Repo, repoB)
	}
	if !got.InGoal {
		t.Error("the goal named it; the warning must say so")
	}
	if len(got.NodeIDs) != 1 || got.NodeIDs[0] != "mem-impl" {
		t.Errorf("nodes = %v, want only mem-impl — argo-impl named no path", got.NodeIDs)
	}
	if !scan.IsRepo || scan.Root != resolveSymlinks(repoA) {
		t.Errorf("boundary = %s (isRepo %v), want the invocation repository %s", scan.Root, scan.IsRepo, repoA)
	}
}

// The false-positive suite, which is the whole risk of a heuristic: every one
// of these is a path-shaped string a real goal or prompt contains, and none of
// them is a checkout outside the boundary. A rule that fires here would train
// users to skip the warning, which is worse than not having it.
func TestScanUnisolated_QuietOnEverythingThatIsNotAForeignCheckout(t *testing.T) {
	home := t.TempDir()
	repo := newCheckout(t, filepath.Join(home, "invocation-repo"))
	if err := os.MkdirAll(filepath.Join(repo, "internal", "graph"), 0o755); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(home, "scratch")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		text string
	}{
		{"a path inside the invocation repository", "edit " + filepath.Join(repo, "internal", "graph", "graph.go")},
		{"the invocation repository itself", "commit everything in " + repo},
		{"a scratch directory that is no checkout", "write the report to " + scratch + "/report.md"},
		{"a path that does not exist at all", "read " + filepath.Join(home, "nowhere", "at", "all.txt")},
		{"a system path", "run /usr/bin/make and read /etc/hosts"},
		{"a branch name", "commit on feat/ingest-latency-hardening, not on main"},
		{"a templated worktree path", "git worktree add /tmp/shepherd-{{ inputs.pr }} FETCH_HEAD"},
		{"a fraction and a date", "roughly 3/4 of the nodes, by 2026/08/06"},
		{"a URL", "see https://github.com/jitokim/oh-my-graph/pull/103 for context"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if scan := scanOf(t, repo, tc.text); scan != nil {
				t.Fatalf("warned about %+v; %q names no checkout outside %s", scan.Paths, tc.text, repo)
			}
			if scan := scanOf(t, repo, "", [2]string{"node", tc.text}); scan != nil {
				t.Fatalf("warned about %+v when the same text was a node prompt", scan.Paths)
			}
		})
	}
}

// Goals are written the way people type, and people type ~. An unexpanded ~
// mention would silently miss the exact spelling the reporter's goal used.
func TestScanUnisolated_ExpandsTheHomeSpelling(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoA := newCheckout(t, filepath.Join(home, "IdeaProjects", "repo-a"))
	repoB := newCheckout(t, filepath.Join(home, "IdeaProjects", "lbox-ai-memory"))

	scan := scanOf(t, repoA, "also harden ~/IdeaProjects/lbox-ai-memory")

	if scan == nil || len(scan.Paths) != 1 {
		t.Fatalf("scan = %+v, want one warning for %s", scan, repoB)
	}
	if scan.Paths[0].Repo != resolveSymlinks(repoB) {
		t.Errorf("repo = %s, want %s", scan.Paths[0].Repo, repoB)
	}
	if scan.Paths[0].Mention != "~/IdeaProjects/lbox-ai-memory" {
		t.Errorf("mention = %s, want the path as it was written", scan.Paths[0].Mention)
	}
}

// Two files in one repository are one hazard: they share a HEAD. The mention
// kept is the first, so the warning points at text the user can find, and the
// entry is filed under the checkout's ROOT, which is what the advice ("make
// your own worktree there") applies to.
func TestScanUnisolated_OneWarningPerCheckoutNotPerPath(t *testing.T) {
	home := t.TempDir()
	repo := newCheckout(t, filepath.Join(home, "invocation-repo"))
	other := newCheckout(t, filepath.Join(home, "other-repo"))
	deep := filepath.Join(other, "pkg", "server")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	scan := scanOf(t, repo, "",
		[2]string{"a", "patch " + filepath.Join(deep, "handler.go")},
		[2]string{"b", "then patch " + filepath.Join(deep, "router.go") + " too"},
	)

	if scan == nil || len(scan.Paths) != 1 {
		t.Fatalf("scan = %+v, want a single entry for %s", scan, other)
	}
	got := scan.Paths[0]
	if got.Repo != resolveSymlinks(other) {
		t.Errorf("repo = %s, want the checkout root %s, not the file's directory", got.Repo, other)
	}
	if got.Mention != filepath.Join(deep, "handler.go") {
		t.Errorf("mention = %s, want the first path written", got.Mention)
	}
	if strings.Join(got.NodeIDs, ",") != "a,b" {
		t.Errorf("nodes = %v, want both nodes in graph order", got.NodeIDs)
	}
}

// A linked git worktree has a .git FILE, not a directory, and it has its own
// HEAD — so it is exactly the kind of checkout another process can be standing
// in. Missing it would exempt the safest thing a careful user does by hand.
func TestScanUnisolated_LinkedWorktreeIsACheckout(t *testing.T) {
	home := t.TempDir()
	repo := newCheckout(t, filepath.Join(home, "invocation-repo"))
	linked := filepath.Join(home, "other-repo-wt-feature")
	if err := os.MkdirAll(linked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: /elsewhere/.git/worktrees/feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	scan := scanOf(t, repo, "release from "+linked)

	if scan == nil || len(scan.Paths) != 1 || scan.Paths[0].Repo != resolveSymlinks(linked) {
		t.Fatalf("scan = %+v, want one warning for the linked worktree %s", scan, linked)
	}
}

// Invoked outside any repository, nothing is isolated at all — including the
// working directory itself. The boundary is then the invocation directory, and
// the printed message says so instead of claiming a repository that is not
// there.
func TestResolveInvocationRoot_OutsideARepository(t *testing.T) {
	dir := t.TempDir()

	root, ok := resolveInvocationRoot(dir)

	if !ok {
		t.Fatal("a plain directory must still resolve to a boundary")
	}
	if root.isRepo {
		t.Error("a directory with no .git above it is not a repository")
	}
	if root.dir != resolveSymlinks(dir) {
		t.Errorf("root = %s, want the invocation directory %s", root.dir, dir)
	}
}

// The rule is measured against the graphs this repo actually ships, which are
// the closest thing to a corpus of real prompt text under version control —
// and one of them (merge-shepherd) writes /tmp worktree paths into six
// prompts. They are hand-written graphs, so they never reach the planner, but
// a rule that warned on them would warn on every plan resembling them.
func TestScanUnisolated_ShippedGraphsWarnAboutNothing(t *testing.T) {
	root, ok := resolveInvocationRoot("")
	if !ok {
		t.Fatal("could not resolve the boundary from the working directory")
	}
	paths, err := filepath.Glob(filepath.Join("..", "..", "graphs", "*.yaml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no shipped graphs found to check: %v", err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			// LoadFile, not Load: three shipped graphs splice a `use:`
			// fragment, and the fragment's prompts are prompt text too.
			loaded, err := graph.LoadFile(path)
			if err != nil {
				t.Fatalf("load %s: %v", path, err)
			}
			if scan := scanUnisolated(root, "", loaded.Graph); scan != nil {
				t.Errorf("warned about %+v in a shipped graph", scan.Paths)
			}
			// Again from a boundary that contains none of this repository,
			// so the silence above cannot be explained by containment: these
			// prompts name work by RELATIVE path, which is the shape the rule
			// is blind to by design and the shape it must not guess at.
			elsewhere, ok := resolveInvocationRoot(t.TempDir())
			if !ok {
				t.Fatal("could not resolve a temp-dir boundary")
			}
			if scan := scanUnisolated(elsewhere, "", loaded.Graph); scan != nil {
				t.Errorf("warned about %+v in a shipped graph, from an unrelated boundary", scan.Paths)
			}
		})
	}
}

// The end-to-end contract: the warning reaches the Plan the CLI prints, from a
// goal alone — no node had to repeat the path.
func TestPlan_CarriesTheUnisolatedWarning(t *testing.T) {
	home := t.TempDir()
	repo := newCheckout(t, filepath.Join(home, "invocation-repo"))
	other := newCheckout(t, filepath.Join(home, "other-repo"))

	fake, _ := newPlannerFake(runner.NodeOutcome{Result: reviewSpec})
	plan, err := New(fake, WithInvocationDir(repo)).Plan(context.Background(), "review the diff in "+other, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if plan.Unisolated == nil || len(plan.Unisolated.Paths) != 1 {
		t.Fatalf("plan.Unisolated = %+v, want one warning for %s", plan.Unisolated, other)
	}
	if !plan.Unisolated.Paths[0].InGoal {
		t.Error("the goal named the checkout; the warning must attribute it there")
	}
}

// A plan naming nothing outside the boundary carries no warning at all, so the
// printout stays exactly as it was for the ordinary single-repository run.
func TestPlan_NoWarningForASingleRepositoryGoal(t *testing.T) {
	repo := newCheckout(t, filepath.Join(t.TempDir(), "invocation-repo"))

	fake, _ := newPlannerFake(runner.NodeOutcome{Result: reviewSpec})
	plan, err := New(fake, WithInvocationDir(repo)).Plan(context.Background(), "review the diff", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if plan.Unisolated != nil {
		t.Fatalf("plan.Unisolated = %+v, want nil for a goal naming no other checkout", plan.Unisolated)
	}
}

// Skill mapping inlines the user's own SKILL.md body into a planned prompt,
// and those bodies routinely name absolute paths of their own ("work under
// ~/IdeaProjects") that the plan never chose. The scan runs before inlining
// for exactly this reason: a warning attributed to a plan must come from the
// plan.
func TestPlan_SkillBodyPathsAreNotWarnedAbout(t *testing.T) {
	home := t.TempDir()
	repo := newCheckout(t, filepath.Join(home, "invocation-repo"))
	documented := newCheckout(t, filepath.Join(home, "some-repo-a-skill-mentions"))
	skills := t.TempDir()
	writeSkillFile(t, skills, "pr-code-review",
		"name: pr-code-review\ndescription: reviews pull requests",
		"Reference implementations live in "+documented+".")

	fake, _ := newPlannerFake(runner.NodeOutcome{Result: reviewSpec})
	plan, err := New(fake, WithInvocationDir(repo), WithSkillDirs(skills)).Plan(context.Background(), "review the diff", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plan.SkillMappings) != 1 || plan.SkillMappings[0].SkippedReason != "" {
		t.Fatalf("mappings = %+v, want the skill body inlined — the test is meaningless without it", plan.SkillMappings)
	}
	if plan.Unisolated != nil {
		t.Fatalf("plan.Unisolated = %+v, want nil: the path came from an inlined skill, not from the plan", plan.Unisolated)
	}
}
