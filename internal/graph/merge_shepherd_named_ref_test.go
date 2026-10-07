package graph_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/handoff"
)

// #360: merge-shepherd's verify and triage nodes used to test `FETCH_HEAD`,
// which any concurrent fetch in the repo moves. They now fetch the PR head into
// a named, content-addressed ref and build the worktree at the SHA, and the
// engine checks the worktree HEAD against the PR's headRefOid, then removes the
// worktree and exactly that run's ref. These tests run the SHIPPED graph's
// command, as TestMergeShepherdMergeVerify does, against a scratch repo with a
// fake `gh` that reads headRefOid from a file the test controls.

const shepherdGraphPath = "../../graphs/merge-shepherd.yaml"

// shepherdNode names one of the two checked nodes and where it builds its
// worktree. The paths stay fixed under /tmp because the shipped verify command
// hard-codes them, so t.TempDir() cannot stand in. Every test uses PR numbers no
// real run would use, and leftovers from an aborted earlier run are cleared:
// addWorktree and clearShepherdDir both RemoveAll the path first and register a
// cleanup that removes it again.
type shepherdNode struct {
	id  string
	dir string // printf format taking the PR number
}

var shepherdNodes = []shepherdNode{
	{id: "verify", dir: "/tmp/shepherd-%s"},
	{id: "triage", dir: "/tmp/shepherd-triage-%s"},
}

func loadShepherdNode(t *testing.T, id string) graph.Node {
	t.Helper()
	loaded, err := graph.LoadFile(filepath.Join(shepherdGraphPath))
	if err != nil {
		t.Fatalf("load merge-shepherd: %v", err)
	}
	for _, n := range loaded.Graph.Nodes {
		if n.ID == id {
			return n
		}
	}
	t.Fatalf("merge-shepherd has no %s node", id)
	return graph.Node{}
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func gitOK(dir string, args ...string) bool {
	return exec.Command("git", append([]string{"-C", dir}, args...)...).Run() == nil
}

// shepherdFixture is a bare origin, the repo the node runs in (a clone of it),
// a pusher clone that authors PR heads, and the fake gh's head files.
type shepherdFixture struct {
	origin, repo, pusher, ghDir string
	n                           int
}

func newShepherdFixture(t *testing.T) *shepherdFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on PATH")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on PATH; the verify command is POSIX sh")
	}
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.invalid")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)

	root := t.TempDir()
	fx := &shepherdFixture{
		origin: filepath.Join(root, "origin.git"),
		repo:   filepath.Join(root, "repo"),
		pusher: filepath.Join(root, "pusher"),
		ghDir:  filepath.Join(root, "bin"),
	}
	if err := os.MkdirAll(fx.ghDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "--bare", fx.origin},
		{"init", fx.pusher},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitIn(t, fx.pusher, "commit", "--allow-empty", "-m", "base")
	gitIn(t, fx.pusher, "push", fx.origin, "HEAD:refs/heads/main")
	if out, err := exec.Command("git", "clone", fx.origin, fx.repo).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}

	// The fake gh answers only `pr view <pr> --json headRefOid`, from the file
	// head-<pr>; an empty/missing file exits non-zero. Any other argv exits 97
	// so a check that asks something else cannot pass by accident.
	script := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"  \"pr view \"*\" --json headRefOid --jq .headRefOid\")\n" +
		"    pr=$3; f='" + fx.ghDir + "/head-'$pr\n" +
		"    [ -s \"$f\" ] || exit 1\n" +
		"    cat \"$f\" ;;\n" +
		"  *) exit 97 ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(fx.ghDir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fx.ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return fx
}

// openPR commits a fresh head in the pusher, publishes it as
// refs/pull/<pr>/head on origin, points the fake gh at it, and returns the SHA.
func (fx *shepherdFixture) openPR(t *testing.T, pr string) string {
	t.Helper()
	fx.n++
	gitIn(t, fx.pusher, "commit", "--allow-empty", "-m", fmt.Sprintf("pr %s head %d", pr, fx.n))
	sha := gitIn(t, fx.pusher, "rev-parse", "HEAD")
	gitIn(t, fx.pusher, "push", "--force", fx.origin, "HEAD:refs/pull/"+pr+"/head")
	gitIn(t, fx.pusher, "push", "--force", fx.origin, "HEAD:refs/heads/pr-"+pr)
	fx.setGHHead(t, pr, sha)
	return sha
}

func (fx *shepherdFixture) setGHHead(t *testing.T, pr, sha string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fx.ghDir, "head-"+pr), []byte(sha+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (fx *shepherdFixture) fetch(t *testing.T, pr, sha string) {
	t.Helper()
	gitIn(t, fx.repo, "fetch", "origin", "+pull/"+pr+"/head:refs/omg-shepherd/pr-"+pr+"/"+sha)
}

// clearShepherdDir starts a test from a clean fixed /tmp path and removes
// whatever the test leaves there.
func clearShepherdDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("clear leftover %s: %v", dir, err)
	}
	if _, err := os.Lstat(dir); err == nil {
		t.Fatalf("leftover %s still exists after RemoveAll", dir)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
}

func (fx *shepherdFixture) addWorktree(t *testing.T, dir, sha string) {
	t.Helper()
	clearShepherdDir(t, dir)
	t.Cleanup(func() {
		_ = exec.Command("git", "-C", fx.repo, "worktree", "remove", "--force", dir).Run()
		_ = os.RemoveAll(dir)
	})
	gitIn(t, fx.repo, "worktree", "add", "--detach", dir, sha)
}

func (fx *shepherdFixture) record(t *testing.T, dir, sha string) {
	t.Helper()
	gitIn(t, dir, "update-ref", "refs/worktree/omg-shepherd-sha", sha)
}

// prescribedRun performs the prompt's steps 3, 5 and 6 in one go.
func (fx *shepherdFixture) prescribedRun(t *testing.T, pr, sha, dir string) {
	t.Helper()
	fx.fetch(t, pr, sha)
	fx.addWorktree(t, dir, sha)
	fx.record(t, dir, sha)
}

func (fx *shepherdFixture) hasRef(ref string) bool {
	return exec.Command("git", "-C", fx.repo, "rev-parse", "--verify", "--quiet", ref).Run() == nil
}

// runCheck renders the node's shipped verify with the handoff interpolation the
// scheduler's resolveVerification performs (command, then cwd falling back to
// the repo) and runs it in the repo, returning output and whether it exited 0.
// It fails the test on a token it does not render: a self token, or any
// placeholder left unrendered after interpolation.
func (fx *shepherdFixture) runCheck(t *testing.T, node graph.Node, pr string) (string, bool) {
	t.Helper()
	if node.SuccessCheck.Verify == nil {
		t.Fatalf("%s declares no success_check.verify (#360)", node.ID)
	}
	h := handoff.New(t.TempDir(), map[string]string{"repo": fx.repo, "pr": pr})
	v := *node.SuccessCheck.Verify
	selfToken := regexp.MustCompile(`\{\{\s*self\.`)
	if selfToken.MatchString(v.Command) || selfToken.MatchString(v.Cwd) {
		t.Fatalf("%s verify names a self token; runCheck does not render self tokens and must be extended", node.ID)
	}
	command, err := h.Interpolate(v.Command)
	if err != nil {
		t.Fatalf("could not render %s verify command %q: %v", node.ID, v.Command, err)
	}
	cwd := fx.repo
	if v.Cwd != "" {
		if cwd, err = h.Interpolate(v.Cwd); err != nil {
			t.Fatalf("could not render %s verify cwd %q: %v", node.ID, v.Cwd, err)
		}
	}
	if strings.Contains(command, "{{") {
		t.Fatalf("%s verify command still holds an unrendered placeholder: %s", node.ID, command)
	}
	if strings.Contains(cwd, "{{") {
		t.Fatalf("%s verify cwd still holds an unrendered placeholder: %s", node.ID, cwd)
	}
	sh, _ := exec.LookPath("sh")
	cmd := exec.Command(sh, "-c", command)
	cmd.Dir = cwd
	out, runErr := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exitErr) {
		t.Fatalf("could not run the verify: %v", runErr)
	}
	t.Logf("%s check said: %s", node.ID, strings.TrimSpace(string(out)))
	return string(out), runErr == nil
}

// Requirement 1.
func TestMergeShepherd360NoFetchHead(t *testing.T) {
	raw, err := os.ReadFile(shepherdGraphPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "FETCH_HEAD") {
		t.Error("merge-shepherd.yaml mentions FETCH_HEAD — a concurrent fetch moves it (#360)")
	}
}

// Requirement 2.
func TestMergeShepherd360PromptsFetchNamedRefAndAddDetachedAtSHA(t *testing.T) {
	for _, sn := range shepherdNodes {
		t.Run(sn.id, func(t *testing.T) {
			prompt := loadShepherdNode(t, sn.id).Prompt
			dir := fmt.Sprintf(sn.dir, "{{ inputs.pr }}")
			for name, want := range map[string]string{
				"fetch into the named ref": "git fetch origin +pull/{{ inputs.pr }}/head:refs/omg-shepherd/pr-{{ inputs.pr }}/SHA",
				"worktree at the SHA":      "git worktree add --detach " + dir + " SHA",
			} {
				if !strings.Contains(prompt, want) {
					t.Errorf("%s: prompt lacks %q", name, want)
				}
			}
			if regexp.MustCompile(`worktree add [^\n]*(FETCH_HEAD|refs/)`).MatchString(prompt) {
				t.Error("worktree is built on a ref, not the SHA")
			}
		})
	}
}

// Requirement 3.
func TestMergeShepherd360CheckPassesWhenWorktreeAtPRHead(t *testing.T) {
	for _, sn := range shepherdNodes {
		t.Run(sn.id, func(t *testing.T) {
			fx := newShepherdFixture(t)
			pr := "936101"
			sha := fx.openPR(t, pr)
			fx.prescribedRun(t, pr, sha, fmt.Sprintf(sn.dir, pr))
			if out, ok := fx.runCheck(t, loadShepherdNode(t, sn.id), pr); !ok {
				t.Errorf("check failed at the PR head: %s", out)
			}
		})
	}
}

// Requirement 4.
func TestMergeShepherd360CheckFailsWhenHeadMovedAfterFetch(t *testing.T) {
	for _, sn := range shepherdNodes {
		t.Run(sn.id, func(t *testing.T) {
			fx := newShepherdFixture(t)
			pr := "936102"
			sha := fx.openPR(t, pr)
			fx.prescribedRun(t, pr, sha, fmt.Sprintf(sn.dir, pr))
			fx.setGHHead(t, pr, strings.Repeat("a", 40))
			out, ok := fx.runCheck(t, loadShepherdNode(t, sn.id), pr)
			if ok {
				t.Errorf("check passed though the PR head moved: %s", out)
			}
			if !strings.Contains(out, strings.Repeat("a", 40)) {
				t.Errorf("failed, but not by comparing against gh's head: %s", out)
			}
		})
	}
}

// Requirement 5, plus the gh failure modes, which must fail closed too.
func TestMergeShepherd360CheckFailsWithoutWorktreeOrHead(t *testing.T) {
	for _, sn := range shepherdNodes {
		t.Run(sn.id+"/worktree missing", func(t *testing.T) {
			fx := newShepherdFixture(t)
			pr := "936103"
			clearShepherdDir(t, fmt.Sprintf(sn.dir, pr))
			fx.openPR(t, pr)
			out, ok := fx.runCheck(t, loadShepherdNode(t, sn.id), pr)
			if ok {
				t.Errorf("check passed with no worktree: %s", out)
			}
			if !strings.Contains(out, "no worktree") {
				t.Errorf("failed for some other reason: %s", out)
			}
		})
		for name, content := range map[string]string{"gh fails": "", "gh prints nothing": "\n"} {
			t.Run(sn.id+"/"+name, func(t *testing.T) {
				fx := newShepherdFixture(t)
				pr := "936104"
				sha := fx.openPR(t, pr)
				fx.prescribedRun(t, pr, sha, fmt.Sprintf(sn.dir, pr))
				if err := os.WriteFile(filepath.Join(fx.ghDir, "head-"+pr), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
				if out, ok := fx.runCheck(t, loadShepherdNode(t, sn.id), pr); ok {
					t.Errorf("check passed without a head from gh: %s", out)
				}
			})
		}
	}
}

// Requirement 6.
func TestMergeShepherd360CheckCleansOwnWorktreeAndRefOnly(t *testing.T) {
	for _, sn := range shepherdNodes {
		for _, failing := range []bool{true, false} {
			name := sn.id + "/passing"
			if failing {
				name = sn.id + "/failing"
			}
			t.Run(name, func(t *testing.T) {
				fx := newShepherdFixture(t)
				pr, otherPR := "936105", "936106"
				sha := fx.openPR(t, pr)
				otherSHA := fx.openPR(t, otherPR)
				dir := fmt.Sprintf(sn.dir, pr)
				fx.prescribedRun(t, pr, sha, dir)

				// Unrelated refs: another PR's, and this PR's at another SHA.
				otherPRRef := "refs/omg-shepherd/pr-" + otherPR + "/" + otherSHA
				samePRRef := "refs/omg-shepherd/pr-" + pr + "/" + otherSHA
				fx.fetch(t, otherPR, otherSHA) // creates otherPRRef and brings the object in
				gitIn(t, fx.repo, "update-ref", samePRRef, otherSHA)
				ownRef := "refs/omg-shepherd/pr-" + pr + "/" + sha
				if !fx.hasRef(ownRef) {
					t.Fatalf("setup: %s missing", ownRef)
				}

				if failing {
					fx.setGHHead(t, pr, otherSHA)
				}
				out, ok := fx.runCheck(t, loadShepherdNode(t, sn.id), pr)
				if ok == failing {
					t.Fatalf("check passed=%v, want %v: %s", ok, !failing, out)
				}

				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Errorf("worktree %s still exists after the check", dir)
				}
				if strings.Contains(gitIn(t, fx.repo, "worktree", "list", "--porcelain"), dir) {
					t.Errorf("worktree %s still registered", dir)
				}
				if fx.hasRef(ownRef) {
					t.Errorf("own ref %s survived the check", ownRef)
				}
				for _, keep := range []string{otherPRRef, samePRRef} {
					if !fx.hasRef(keep) {
						t.Errorf("unrelated ref %s was deleted", keep)
					}
				}
			})
		}
	}
}

// Requirement 7: the prescribed steps, interleaved across two PRs in both
// orders, in one repo. A shared FETCH_HEAD would make one run test the other's
// head; named refs and SHA-pinned worktrees must not.
func TestMergeShepherd360InterleavedRunsKeepTheirOwnHead(t *testing.T) {
	type step func(fx *shepherdFixture, t *testing.T, pr, sha, dir string)
	steps := []step{
		func(fx *shepherdFixture, t *testing.T, pr, sha, _ string) { fx.fetch(t, pr, sha) },
		func(fx *shepherdFixture, t *testing.T, _, sha, dir string) { fx.addWorktree(t, dir, sha) },
		func(fx *shepherdFixture, t *testing.T, _, sha, dir string) { fx.record(t, dir, sha) },
	}
	orders := []struct {
		name   string
		prA    string
		prB    string
		first  int // which run goes first at each step: 0 = A then B, 1 = B then A
		verify string
	}{
		{"A then B", "936201", "936202", 0, "verify"},
		{"B then A", "936203", "936204", 1, "verify"},
		{"A then B triage", "936205", "936206", 0, "triage"},
		{"B then A triage", "936207", "936208", 1, "triage"},
	}
	for _, o := range orders {
		t.Run(o.name, func(t *testing.T) {
			fx := newShepherdFixture(t)
			var sn shepherdNode
			for _, c := range shepherdNodes {
				if c.id == o.verify {
					sn = c
				}
			}
			shaA, shaB := fx.openPR(t, o.prA), fx.openPR(t, o.prB)
			dirA, dirB := fmt.Sprintf(sn.dir, o.prA), fmt.Sprintf(sn.dir, o.prB)
			runs := [][3]string{{o.prA, shaA, dirA}, {o.prB, shaB, dirB}}
			if o.first == 1 {
				runs[0], runs[1] = runs[1], runs[0]
			}
			for _, s := range steps {
				for _, r := range runs {
					s(fx, t, r[0], r[1], r[2])
				}
			}
			for _, r := range runs {
				if got := gitIn(t, r[2], "rev-parse", "HEAD"); got != r[1] {
					t.Errorf("worktree %s at %s, want its PR's head %s", r[2], got, r[1])
				}
			}
			node := loadShepherdNode(t, sn.id)
			for _, r := range runs {
				if out, ok := fx.runCheck(t, node, r[0]); !ok {
					t.Errorf("PR %s check failed: %s", r[0], out)
				}
			}
		})
	}
}

// Requirement 8: triage commits in its worktree and pushes HEAD to the PR
// branch; the PR head is then the new commit, and the check must pass.
func TestMergeShepherd360LegitimateTriagePushPassesCheck(t *testing.T) {
	fx := newShepherdFixture(t)
	pr := "936301"
	sha := fx.openPR(t, pr)
	var triage shepherdNode
	for _, c := range shepherdNodes {
		if c.id == "triage" {
			triage = c
		}
	}
	dir := fmt.Sprintf(triage.dir, pr)
	fx.prescribedRun(t, pr, sha, dir)

	gitIn(t, dir, "commit", "--allow-empty", "-m", "triage fix")
	fixed := gitIn(t, dir, "rev-parse", "HEAD")
	if fixed == sha {
		t.Fatal("setup: commit did not move HEAD")
	}
	gitIn(t, dir, "push", "origin", "HEAD:refs/heads/pr-"+pr)
	if got := gitIn(t, fx.origin, "rev-parse", "refs/heads/pr-"+pr); got != fixed {
		t.Fatalf("push did not land: origin has %s, want %s", got, fixed)
	}
	fx.setGHHead(t, pr, fixed)

	out, ok := fx.runCheck(t, loadShepherdNode(t, "triage"), pr)
	if !ok {
		t.Errorf("a legitimate triage push failed its check: %s", out)
	}
	if gitOK(fx.repo, "rev-parse", "--verify", "--quiet", "refs/omg-shepherd/pr-"+pr+"/"+sha) {
		t.Error("the run's ref survived a passing check")
	}
}

// #360, split cleanup: the engine verify runs only after result_matches
// passes, so on a non-passing verdict the agent must remove its own worktree
// and ref, and on a passing one it must leave them for the engine check.
// Each node's verdict tokens, as its prompt and result_matches spell them.
var shepherdVerdicts = map[string]struct{ pass, fail string }{
	"verify": {pass: "PASS", fail: "FAIL"},
	"triage": {pass: "TRIAGED", fail: "BLOCKED"},
}

// verdictWindows returns, for every occurrence of the token after the
// worktree is built, the prompt text from that token up to the next
// occurrence of the other verdict token (or the end). Step 0's cleanup sits
// before the worktree add, so it is never inside a window.
func verdictWindows(t *testing.T, prompt, dir, token, other string) []string {
	t.Helper()
	add := strings.Index(prompt, "git worktree add --detach "+dir)
	if add < 0 {
		t.Fatalf("prompt never adds the worktree %s", dir)
	}
	rest := prompt[add:]
	tokRe := regexp.MustCompile(`\b` + token + `\b`)
	otherRe := regexp.MustCompile(`\b` + other + `\b`)
	var windows []string
	for _, loc := range tokRe.FindAllStringIndex(rest, -1) {
		w := rest[loc[0]:]
		if end := otherRe.FindStringIndex(w[len(token):]); end != nil {
			w = w[:len(token)+end[0]]
		}
		windows = append(windows, w)
	}
	return windows
}

func TestMergeShepherd360NonPassingVerdictAgentCleansUp(t *testing.T) {
	for _, sn := range shepherdNodes {
		t.Run(sn.id, func(t *testing.T) {
			v := shepherdVerdicts[sn.id]
			dir := fmt.Sprintf(sn.dir, "{{ inputs.pr }}")
			removeWT := "git worktree remove --force " + dir
			removeRef := "git update-ref -d refs/omg-shepherd/pr-{{ inputs.pr }}/SHA"
			prompt := loadShepherdNode(t, sn.id).Prompt
			for _, w := range verdictWindows(t, prompt, dir, v.fail, v.pass) {
				if strings.Contains(w, removeWT) && strings.Contains(w, removeRef) {
					return
				}
			}
			t.Errorf("no %s instruction after the worktree is built names both %q and %q", v.fail, removeWT, removeRef)
		})
	}
}

func TestMergeShepherd360PassingVerdictLeavesWorktree(t *testing.T) {
	leave := regexp.MustCompile(`(?i)\bleave\b[^.]*\bworktree\b[^.]*\bin place\b`)
	for _, sn := range shepherdNodes {
		t.Run(sn.id, func(t *testing.T) {
			v := shepherdVerdicts[sn.id]
			dir := fmt.Sprintf(sn.dir, "{{ inputs.pr }}")
			prompt := loadShepherdNode(t, sn.id).Prompt
			for _, w := range verdictWindows(t, prompt, dir, v.pass, v.fail) {
				if leave.MatchString(w) && !strings.Contains(w, "worktree remove") {
					return
				}
			}
			t.Errorf("no %s instruction says to leave the worktree in place", v.pass)
		})
	}
}

// bashGrantPermits reports whether some "Bash(<glob>)" entry in the grant
// matches the command, `*` matching any run of characters.
func bashGrantPermits(grant []string, command string) bool {
	for _, g := range grant {
		if !strings.HasPrefix(g, "Bash(") || !strings.HasSuffix(g, ")") {
			continue
		}
		parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(g, "Bash("), ")"), "*")
		for i, p := range parts {
			parts[i] = regexp.QuoteMeta(p)
		}
		if regexp.MustCompile(`^` + strings.Join(parts, `.*`) + `$`).MatchString(command) {
			return true
		}
	}
	return false
}

func TestMergeShepherd360GrantCoversAgentCleanup(t *testing.T) {
	for _, sn := range shepherdNodes {
		t.Run(sn.id, func(t *testing.T) {
			pr := "936401"
			grant := loadShepherdNode(t, sn.id).AllowedTools
			for _, cmd := range []string{
				"git worktree remove --force " + fmt.Sprintf(sn.dir, pr),
				"git update-ref -d refs/omg-shepherd/pr-" + pr + "/" + strings.Repeat("a", 40),
			} {
				if !bashGrantPermits(grant, cmd) {
					t.Errorf("allowed_tools %v does not permit %q", grant, cmd)
				}
			}
		})
	}
}

// #360, fork guard: pushing HEAD:refs/heads/BRANCH for a cross-repository PR
// would create or move a same-named branch in the BASE repository, so triage
// asks gh whether the PR is from a fork before it can push, and if so cleans
// up and answers `BLOCKED fork PR`. That verdict deliberately does NOT match
// result_matches (c): like every BLOCKED it fails the node, so a fork PR stops
// the run before the merge gate.
func TestMergeShepherd360TriageRefusesForkPRPush(t *testing.T) {
	var sn shepherdNode
	for _, n := range shepherdNodes {
		if n.id == "triage" {
			sn = n
		}
	}
	triage := loadShepherdNode(t, sn.id)
	prompt := triage.Prompt

	fork, push := strings.Index(prompt, "isCrossRepository"), strings.Index(prompt, "push origin")
	if fork < 0 || push < 0 || fork > push {
		t.Errorf("isCrossRepository at %d must come before the first push origin at %d", fork, push)
	}

	dir := fmt.Sprintf(sn.dir, "{{ inputs.pr }}")
	removeWT := "git worktree remove --force " + dir
	removeRef := "git update-ref -d refs/omg-shepherd/pr-{{ inputs.pr }}/SHA"
	cleans := false
	for _, w := range verdictWindows(t, prompt, dir, "BLOCKED fork PR", "TRIAGED") {
		if strings.Contains(w, removeWT) && strings.Contains(w, removeRef) {
			cleans = true
		}
	}
	if !cleans {
		t.Errorf("no BLOCKED fork PR instruction names both %q and %q", removeWT, removeRef)
	}

	re := regexp.MustCompile(triage.SuccessCheck.ResultMatches)
	if re.MatchString("BLOCKED fork PR — pushing would write the base repository") {
		t.Errorf("result_matches %q passes a BLOCKED fork PR verdict", re)
	}
	if !re.MatchString("TRIAGED 0") {
		t.Errorf("result_matches %q no longer passes TRIAGED 0", re)
	}

	cmd := "gh pr view 936401 --json isCrossRepository --jq .isCrossRepository"
	if !bashGrantPermits(triage.AllowedTools, cmd) {
		t.Errorf("allowed_tools %v does not permit %q", triage.AllowedTools, cmd)
	}
}
