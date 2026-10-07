package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// The runtime tests for the SHIPPED graphs/gated-dev.yaml (#345). The pin on
// review, the head check on check-head and the published-head check on pr are
// ENGINE verify commands, so these legs run against a real temporary git
// repository through the real worktree.GitManager and verify.ShellVerifier
// that executeGraph and executeResume wire. Only the model runner and `gh` are
// fake: gatedDevRunner tells the six nodes apart by their prompts, its dev
// commits in the node's working directory and its pr really pushes to a bare
// origin, as real ones would; stubGH answers pr's verify from that origin, so
// no test reaches GitHub.
//
// What the engine does to the lane at the pause, which every "move HEAD" step
// below relies on: the paused leg's worktree cleanup REMOVES the lane's
// worktree directory and keeps its branch omg/<run-id>/lane because it carries
// dev's commits; the resumed leg's GitManager.Acquire then re-attaches that
// retained branch. So the HEAD the resumed check-head sees is the tip of
// refs/heads/omg/<run-id>/lane in the shared repo, and moving that ref is what
// moving the lane's HEAD means.

// gatedDevNode names the node a prompt belongs to, from text only that node's
// prompt carries (the inline prompts in the graph, and the three fragments').
func gatedDevNode(prompt string) string {
	switch {
	case strings.Contains(prompt, "Implement this task"):
		return "dev"
	case strings.Contains(prompt, "Continue the work"):
		return "e2e"
	case strings.Contains(prompt, "for style, naming, and simplicity"):
		return "review"
	case strings.Contains(prompt, "Do not change anything."):
		return "check-head"
	case strings.Contains(prompt, "Your grant is exactly this"):
		return "pr"
	}
	return ""
}

// gatedDevCall is one invocation the fake received: which node, where it ran,
// and the HEAD of that directory when it started.
type gatedDevCall struct {
	node string
	cwd  string
	head string
}

// gatedDevRunner is the fake model runner for gated-dev. dev commits a new
// file in its working directory on every invocation; review replies with the
// next verdict from reviews (the last one repeats); pr pushes its HEAD to
// origin — after committing once more when prCommits is set, the move its
// narrow grant exists to rule out; every node replies with the token its
// success_check wants.
type gatedDevRunner struct {
	reviews   []string
	prCommits bool
	// prAct, when set, is everything the fake pr agent does in its working
	// directory, in place of the default push of HEAD (and of prCommits).
	prAct func(cwd string) error

	mu    sync.Mutex
	calls []gatedDevCall
	seq   int // numbers the files dev commits, so every commit is new
}

func newGatedDevRunner(reviews ...string) *gatedDevRunner {
	if len(reviews) == 0 {
		reviews = []string{"CLEAN"}
	}
	return &gatedDevRunner{reviews: reviews}
}

func (r *gatedDevRunner) Run(_ context.Context, spec runner.NodeInvocation) (runner.NodeOutcome, error) {
	node := gatedDevNode(spec.Prompt)
	if node == "" {
		return runner.NodeOutcome{}, fmt.Errorf("gatedDevRunner: no node of gated-dev has the prompt %q", spec.Prompt)
	}
	head, err := gitOutput(spec.Cwd, "rev-parse", "HEAD")
	if err != nil {
		return runner.NodeOutcome{}, err
	}

	r.mu.Lock()
	r.calls = append(r.calls, gatedDevCall{node: node, cwd: spec.Cwd, head: head})
	reviewRound := r.countLocked("review")
	r.seq++
	seq := r.seq
	r.mu.Unlock()

	var result string
	switch node {
	case "dev":
		if err := commitWork(spec.Cwd, fmt.Sprintf("work-%d.txt", seq)); err != nil {
			return runner.NodeOutcome{}, err
		}
		result = "DONE implemented the task"
	case "e2e":
		result = "PASS"
	case "review":
		result = r.reviews[min(reviewRound, len(r.reviews))-1]
	case "check-head":
		result = "DONE"
	case "pr":
		if r.prAct != nil {
			if err := r.prAct(spec.Cwd); err != nil {
				return runner.NodeOutcome{}, err
			}
			result = "PR https://github.com/example/repo/pull/1"
			break
		}
		if r.prCommits {
			if err := commitWork(spec.Cwd, fmt.Sprintf("unreviewed-%d.txt", seq)); err != nil {
				return runner.NodeOutcome{}, err
			}
		}
		if _, err := gitOutput(spec.Cwd, "push", "-q", "origin", "HEAD"); err != nil {
			return runner.NodeOutcome{}, err
		}
		result = "PR https://github.com/example/repo/pull/1"
	}
	outcome := runner.NodeOutcome{SessionID: "s-" + node, Result: result, ExitCode: 0}
	if spec.SessionStarted != nil {
		spec.SessionStarted(outcome.SessionID)
	}
	return outcome, nil
}

func (r *gatedDevRunner) countLocked(node string) int {
	n := 0
	for _, c := range r.calls {
		if c.node == node {
			n++
		}
	}
	return n
}

// callsTo returns every invocation the fake received for node, in order.
func (r *gatedDevRunner) callsTo(node string) []gatedDevCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []gatedDevCall
	for _, c := range r.calls {
		if c.node == node {
			out = append(out, c)
		}
	}
	return out
}

// assertNeverStarted fails unless the fake received NO invocation for node —
// the agent was never started, which is a stronger claim than the run failing.
func (r *gatedDevRunner) assertNeverStarted(t *testing.T, node string) {
	t.Helper()
	if calls := r.callsTo(node); len(calls) != 0 {
		t.Errorf("%s's agent was started %d time(s); it must never start here", node, len(calls))
	}
}

// gitOutput runs git in dir and returns its trimmed output. It returns an
// error rather than failing a test, because the fake runner calls it from the
// scheduler's goroutines.
func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v in %s: %w\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitOutput(dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// commitWork writes one file in dir and commits it, staging it by its path.
// The file names dir, so two runs' lanes never make byte-identical commits
// (same parent, same tree, same second) that would share one SHA.
func commitWork(dir, name string) error {
	if err := os.WriteFile(filepath.Join(dir, name), []byte(name+" in "+dir+"\n"), 0o644); err != nil {
		return err
	}
	if _, err := gitOutput(dir, "add", name); err != nil {
		return err
	}
	_, err := gitOutput(dir, "commit", "-q", "-m", "work: "+name)
	return err
}

// gatedDevRepo makes a scratch repository with one commit on main and makes
// it the process's working directory: the run's GitManager provisions the
// lane off the invocation repo, which is the cwd. The developer's own git
// configuration (hooks, signing) must reach neither the test's git nor the
// engine's, so both are pointed away from it for the whole test. Its `origin`
// is a bare repository beside it, which is where the fake pr publishes and
// what stubGH reports.
func gatedDevRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("LC_ALL", "C")
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q", "-b", "main")
	mustGit(t, dir, "config", "user.email", "test@example.com")
	mustGit(t, dir, "config", "user.name", "test")
	mustGit(t, dir, "config", "commit.gpgsign", "false")
	if err := commitWork(dir, "README"); err != nil {
		t.Fatal(err)
	}
	origin := filepath.Join(t.TempDir(), "origin.git")
	mustGit(t, dir, "init", "-q", "--bare", origin)
	mustGit(t, dir, "remote", "add", "origin", origin)
	stubGH(t)
	t.Chdir(dir)
	return dir
}

// stubGH puts a fake `gh` first on PATH for the whole test, so pr's engine
// verify never calls the real one or the network. It answers exactly one
// question, `gh pr view --json headRefOid --jq .headRefOid`, with what was
// really published: origin's tip of the branch checked out in the directory
// it runs in. Anything else it is asked fails, as does a branch never pushed.
func stubGH(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
if [ "$*" != "pr view --json headRefOid --jq .headRefOid" ]; then
	echo "stub gh: unexpected arguments: $*" >&2
	exit 1
fi
branch="$(git symbolic-ref --short HEAD)" || exit 1
line="$(git ls-remote --exit-code origin "refs/heads/$branch")" || exit 1
echo "${line%%	*}"
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// publishedSHA is origin's tip of branch: what pr really published.
func publishedSHA(t *testing.T, repo, branch string) string {
	t.Helper()
	origin := mustGit(t, repo, "remote", "get-url", "origin")
	return mustGit(t, origin, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}")
}

// gatedDevGraphPath is the shipped graph, resolved before any test changes
// directory.
func gatedDevGraphPath(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "graphs", "gated-dev.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// startGatedDev runs the shipped graph's first leg as `run` does, under the
// given run id, and returns what the leg returned.
func startGatedDev(t *testing.T, graphPath, repo, runID string, rec *gatedDevRunner) error {
	t.Helper()
	loaded, err := graph.LoadFile(graphPath)
	if err != nil {
		t.Fatalf("load %s: %v", graphPath, err)
	}
	inputs := inputFlag{
		"repo":           repo,
		"task":           "implement the feature",
		"checks":         "run the project's checks.",
		"verify_command": "true",
		"focus":          "Judge correctness as well as style.",
		"publish":        "Open a draft pull request.",
	}
	var runErr error
	captureStdout(t, func() {
		runErr = executeGraph(context.Background(), runID, loaded.Graph, rec, commonRunFlags{inputs: inputs}, nil, 0, graphPath, loaded.Source, len(loaded.Resolutions) > 0, nil, nil, nil)
	})
	return runErr
}

// pausedGatedDev starts the shipped graph and asserts it paused at
// approve-publish with exit code 2, before check-head or pr started.
func pausedGatedDev(t *testing.T, graphPath, repo, runID string, rec *gatedDevRunner) {
	t.Helper()
	err := startGatedDev(t, graphPath, repo, runID, rec)
	var paused *schedule.PausedError
	if !errors.As(err, &paused) {
		t.Fatalf("run %s: want a pause at approve-publish, got %T: %v", runID, err, err)
	}
	if paused.GateID != "approve-publish" {
		t.Fatalf("run %s paused at %q, want approve-publish", runID, paused.GateID)
	}
	if code := exitCodeForError(err); code != 2 {
		t.Fatalf("run %s: a pause exits %d, want 2", runID, code)
	}
	rec.assertNeverStarted(t, "check-head")
	rec.assertNeverStarted(t, "pr")
}

// resumeGatedDev resumes the run with one decision on approve-publish, as
// `oh-my-graph resume <run-id> --approve|--reject approve-publish` does.
func resumeGatedDev(t *testing.T, runID, decision string, rec *gatedDevRunner) error {
	t.Helper()
	var err error
	captureStdout(t, func() {
		err = executeResume(parseResumeFlags(t, []string{runID, decision, "approve-publish"}), rec, nil)
	})
	return err
}

func laneBranch(runID string) string { return "omg/" + runID + "/lane" }

// pinRef is the ref review's verify writes for a run: named after the lane's
// per-run branch.
func pinRef(runID string) string { return "refs/omg-approved/" + laneBranch(runID) }

// refSHA resolves ref in repo, reporting whether it exists.
func refSHA(t *testing.T, repo, ref string) (string, bool) {
	t.Helper()
	out, err := gitOutput(repo, "rev-parse", "--verify", "-q", ref)
	if err != nil {
		return "", false
	}
	return out, true
}

func gatedDevSnapshot(t *testing.T, runID string) runstate.Snapshot {
	t.Helper()
	snap, err := runstate.Load(filepath.Join(runDirFor(runID), stateFileName))
	if err != nil {
		t.Fatalf("load snapshot of run %s: %v", runID, err)
	}
	return snap
}

func assertVerdict(t *testing.T, snap runstate.Snapshot, node string, want runstate.Verdict) {
	t.Helper()
	if got := snap.Nodes[node].Verdict; got != want {
		t.Errorf("%s's verdict = %q, want %q", node, got, want)
	}
}

// --- 3. the pause, then approve or reject -----------------------------------

func TestGatedDev_PausesThenApproveRunsCheckHeadAndPR_345(t *testing.T) {
	isolateRunHome(t)
	graphPath := gatedDevGraphPath(t)
	repo := gatedDevRepo(t)
	rec := newGatedDevRunner("CLEAN")
	const runID = "gd-approve"

	pausedGatedDev(t, graphPath, repo, runID, rec)

	if err := resumeGatedDev(t, runID, "--approve", rec); err != nil {
		t.Fatalf("resume --approve approve-publish: %v", err)
	}
	snap := gatedDevSnapshot(t, runID)
	assertVerdict(t, snap, "check-head", runstate.VerdictPass)
	assertVerdict(t, snap, "pr", runstate.VerdictPass)
	if n := len(rec.callsTo("pr")); n != 1 {
		t.Errorf("pr's agent started %d time(s) after the approval, want 1", n)
	}
	if snap.Gate.Decisions["approve-publish"] != runstate.GateApprove {
		t.Errorf("approve-publish's recorded decision = %q, want %q", snap.Gate.Decisions["approve-publish"], runstate.GateApprove)
	}
}

func TestGatedDev_RejectEndsTheRunBeforeCheckHeadAndPR_345(t *testing.T) {
	isolateRunHome(t)
	graphPath := gatedDevGraphPath(t)
	repo := gatedDevRepo(t)
	rec := newGatedDevRunner("CLEAN")
	const runID = "gd-reject"

	pausedGatedDev(t, graphPath, repo, runID, rec)

	if err := resumeGatedDev(t, runID, "--reject", rec); err == nil {
		t.Fatal("resume --reject approve-publish returned nil; a rejected gate ends the run as failed")
	}
	rec.assertNeverStarted(t, "check-head")
	rec.assertNeverStarted(t, "pr")
	snap := gatedDevSnapshot(t, runID)
	if snap.Gate.PausedAt != "" {
		t.Errorf("the run is still paused at %q after the rejection", snap.Gate.PausedAt)
	}
	if snap.Gate.Decisions["approve-publish"] != runstate.GateReject {
		t.Errorf("approve-publish's recorded decision = %q, want %q", snap.Gate.Decisions["approve-publish"], runstate.GateReject)
	}
}

// --- 5e. the published head spends the pin -----------------------------------

// check-head guards and leaves the pin; pr's verify finds the published head
// equal to it and only then deletes it.
func TestGatedDev_PublishedPinnedHeadSpendsThePin_345(t *testing.T) {
	isolateRunHome(t)
	graphPath := gatedDevGraphPath(t)
	repo := gatedDevRepo(t)
	rec := newGatedDevRunner("CLEAN")
	const runID = "gd-spend"

	pausedGatedDev(t, graphPath, repo, runID, rec)
	pinned, ok := refSHA(t, repo, pinRef(runID))
	if !ok {
		t.Fatalf("no pin %s at the pause; the test below would prove nothing", pinRef(runID))
	}

	if err := resumeGatedDev(t, runID, "--approve", rec); err != nil {
		t.Fatalf("resume --approve approve-publish: %v", err)
	}
	snap := gatedDevSnapshot(t, runID)
	assertVerdict(t, snap, "check-head", runstate.VerdictPass)
	assertVerdict(t, snap, "pr", runstate.VerdictPass)
	if got := publishedSHA(t, repo, laneBranch(runID)); got != pinned {
		t.Errorf("origin's %s is at %s, want the pinned %s", laneBranch(runID), got, pinned)
	}
	if sha, ok := refSHA(t, repo, pinRef(runID)); ok {
		t.Errorf("the pin %s still exists (at %s) after pr published it; pr's verify must spend it", pinRef(runID), sha)
	}
}

// --- 5f. a pr that publishes another commit fails ------------------------------

// The narrow grant makes this unlikely, not impossible: a pr that commits (or
// pushes another SHA) publishes a head nobody approved. Its verify must fail
// and keep the pin, so the person can see what happened and clean it up.
func TestGatedDev_PRPublishingAnUnapprovedHeadFailsAndKeepsThePin_345(t *testing.T) {
	isolateRunHome(t)
	graphPath := gatedDevGraphPath(t)
	repo := gatedDevRepo(t)
	rec := newGatedDevRunner("CLEAN")
	rec.prCommits = true
	const runID = "gd-unapproved"

	pausedGatedDev(t, graphPath, repo, runID, rec)
	pinned, ok := refSHA(t, repo, pinRef(runID))
	if !ok {
		t.Fatalf("no pin %s at the pause", pinRef(runID))
	}

	if err := resumeGatedDev(t, runID, "--approve", rec); err == nil {
		t.Error("the resumed leg returned nil; pr published an unapproved head and must fail it")
	}
	snap := gatedDevSnapshot(t, runID)
	assertVerdict(t, snap, "check-head", runstate.VerdictPass)
	assertVerdict(t, snap, "pr", runstate.VerdictFail)
	if got := publishedSHA(t, repo, laneBranch(runID)); got == pinned {
		t.Fatalf("origin's %s is at the pinned %s; the fake pr did not publish another commit, so this proves nothing", laneBranch(runID), pinned)
	}
	if sha, ok := refSHA(t, repo, pinRef(runID)); !ok || sha != pinned {
		t.Errorf("a failed pr changed the pin: %s is %q (exists %v), want %s", pinRef(runID), sha, ok, pinned)
	}
}

// --- 5g. pr's verify judges what was published, not what the agent did --------

// assertPRFailedItsVerify is the outcome every leg below wants: the resumed
// leg failed, check-head passed, pr's agent ran once and ended with a failing
// verdict, which only its engine verify can have given it (the fake's reply
// matches result_matches).
func assertPRFailedItsVerify(t *testing.T, runID string, resumeErr error, rec *gatedDevRunner) {
	t.Helper()
	if resumeErr == nil {
		t.Error("the resumed leg returned nil; pr's verify must fail it")
	}
	if n := len(rec.callsTo("pr")); n != 1 {
		t.Errorf("pr's agent started %d time(s), want 1", n)
	}
	snap := gatedDevSnapshot(t, runID)
	assertVerdict(t, snap, "check-head", runstate.VerdictPass)
	assertVerdict(t, snap, "pr", runstate.VerdictFail)
}

func currentBranch(dir string) (string, error) {
	return gitOutput(dir, "symbolic-ref", "--short", "HEAD")
}

func TestGatedDev_PRCommitThenPushFailsAtItsVerifyAndKeepsThePin_345(t *testing.T) {
	isolateRunHome(t)
	graphPath := gatedDevGraphPath(t)
	repo := gatedDevRepo(t)
	rec := newGatedDevRunner("CLEAN")
	rec.prAct = func(cwd string) error {
		if err := commitWork(cwd, "sneaked.txt"); err != nil {
			return err
		}
		_, err := gitOutput(cwd, "push", "-q", "origin", "HEAD")
		return err
	}
	const runID = "gd-pr-commit"

	pausedGatedDev(t, graphPath, repo, runID, rec)
	pinned, ok := refSHA(t, repo, pinRef(runID))
	if !ok {
		t.Fatalf("no pin %s at the pause", pinRef(runID))
	}

	err := resumeGatedDev(t, runID, "--approve", rec)
	assertPRFailedItsVerify(t, runID, err, rec)
	if got := publishedSHA(t, repo, laneBranch(runID)); got == pinned {
		t.Fatalf("origin's %s is at the pinned %s; nothing else was published, so this proves nothing", laneBranch(runID), pinned)
	}
	if sha, ok := refSHA(t, repo, pinRef(runID)); !ok || sha != pinned {
		t.Errorf("a failed pr changed the pin: %s is %q (exists %v), want %s", pinRef(runID), sha, ok, pinned)
	}
}

func TestGatedDev_PRPushingADifferentSHAFailsAtItsVerify_345(t *testing.T) {
	isolateRunHome(t)
	graphPath := gatedDevGraphPath(t)
	repo := gatedDevRepo(t)
	rec := newGatedDevRunner("CLEAN")
	var head, pushed string
	rec.prAct = func(cwd string) error {
		branch, err := currentBranch(cwd)
		if err != nil {
			return err
		}
		if head, err = gitOutput(cwd, "rev-parse", "HEAD"); err != nil {
			return err
		}
		// HEAD is left alone; its parent is published instead.
		if pushed, err = gitOutput(cwd, "rev-parse", "HEAD~1"); err != nil {
			return err
		}
		_, err = gitOutput(cwd, "push", "-q", "origin", pushed+":refs/heads/"+branch)
		return err
	}
	const runID = "gd-pr-other-sha"

	pausedGatedDev(t, graphPath, repo, runID, rec)
	pinned, ok := refSHA(t, repo, pinRef(runID))
	if !ok {
		t.Fatalf("no pin %s at the pause", pinRef(runID))
	}

	err := resumeGatedDev(t, runID, "--approve", rec)
	assertPRFailedItsVerify(t, runID, err, rec)
	if head != pinned {
		t.Errorf("pr's HEAD was %s, want the pin %s: HEAD must be untouched", head, pinned)
	}
	if got := publishedSHA(t, repo, laneBranch(runID)); got != pushed || got == head {
		t.Errorf("origin's %s is at %s, want the different SHA %s (HEAD %s)", laneBranch(runID), got, pushed, head)
	}
	if sha, ok := refSHA(t, repo, pinRef(runID)); !ok || sha != pinned {
		t.Errorf("a failed pr changed the pin: %s is %q (exists %v), want %s", pinRef(runID), sha, ok, pinned)
	}
}

// check-head must not delete the pin: it is still there when pr's agent
// starts, and only pr's verify removes it, once the published head matches.
func TestGatedDev_PinSurvivesCheckHeadAndIsDeletedByPRVerify_345(t *testing.T) {
	isolateRunHome(t)
	graphPath := gatedDevGraphPath(t)
	repo := gatedDevRepo(t)
	rec := newGatedDevRunner("CLEAN")
	var pinAtPR, headAtPR string
	var pinErr error
	rec.prAct = func(cwd string) error {
		pinAtPR, pinErr = gitOutput(repo, "rev-parse", "--verify", "-q", pinRef("gd-pin-lifetime"))
		var err error
		if headAtPR, err = gitOutput(cwd, "rev-parse", "HEAD"); err != nil {
			return err
		}
		_, err = gitOutput(cwd, "push", "-q", "origin", "HEAD")
		return err
	}
	const runID = "gd-pin-lifetime"

	pausedGatedDev(t, graphPath, repo, runID, rec)
	pinned, ok := refSHA(t, repo, pinRef(runID))
	if !ok {
		t.Fatalf("no pin %s at the pause", pinRef(runID))
	}

	if err := resumeGatedDev(t, runID, "--approve", rec); err != nil {
		t.Fatalf("resume --approve approve-publish: %v", err)
	}
	if pinErr != nil || pinAtPR != pinned {
		t.Errorf("when pr started the pin was %q (err %v), want %s: check-head must not delete it", pinAtPR, pinErr, pinned)
	}
	if headAtPR != pinned {
		t.Errorf("pr started on %s, want the pin %s", headAtPR, pinned)
	}
	assertVerdict(t, gatedDevSnapshot(t, runID), "pr", runstate.VerdictPass)
	if sha, ok := refSHA(t, repo, pinRef(runID)); ok {
		t.Errorf("the pin %s still exists (at %s) after the run; pr's verify must delete it", pinRef(runID), sha)
	}
}

func TestGatedDev_PinRemovedBeforePRVerifyFailsPRClosed_345(t *testing.T) {
	isolateRunHome(t)
	graphPath := gatedDevGraphPath(t)
	repo := gatedDevRepo(t)
	rec := newGatedDevRunner("CLEAN")
	const runID = "gd-pin-removed"
	rec.prAct = func(cwd string) error {
		if _, err := gitOutput(repo, "update-ref", "-d", pinRef(runID)); err != nil {
			return err
		}
		_, err := gitOutput(cwd, "push", "-q", "origin", "HEAD")
		return err
	}

	pausedGatedDev(t, graphPath, repo, runID, rec)
	pinned, ok := refSHA(t, repo, pinRef(runID))
	if !ok {
		t.Fatalf("no pin %s at the pause", pinRef(runID))
	}

	err := resumeGatedDev(t, runID, "--approve", rec)
	assertPRFailedItsVerify(t, runID, err, rec)
	if got := publishedSHA(t, repo, laneBranch(runID)); got != pinned {
		t.Errorf("origin's %s is at %s, want the approved %s: the head was published, only the pin was missing", laneBranch(runID), got, pinned)
	}
	if sha, ok := refSHA(t, repo, pinRef(runID)); ok {
		t.Errorf("the pin %s reappeared at %s", pinRef(runID), sha)
	}
}

// A `gh` that fails fails pr's verify on its exit status alone: this one
// prints the published head, which IS the pin, and then exits 1.
func TestGatedDev_FailingGHFailsPRAtItsVerifyAndKeepsThePin_345(t *testing.T) {
	isolateRunHome(t)
	graphPath := gatedDevGraphPath(t)
	repo := gatedDevRepo(t)
	bin := t.TempDir()
	script := `#!/bin/sh
branch="$(git symbolic-ref --short HEAD)" || exit 1
line="$(git ls-remote --exit-code origin "refs/heads/$branch")" || exit 1
echo "${line%%	*}"
echo "stub gh: failing after printing the head" >&2
exit 1
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	rec := newGatedDevRunner("CLEAN")
	const runID = "gd-gh-fails"

	pausedGatedDev(t, graphPath, repo, runID, rec)
	pinned, ok := refSHA(t, repo, pinRef(runID))
	if !ok {
		t.Fatalf("no pin %s at the pause", pinRef(runID))
	}

	err := resumeGatedDev(t, runID, "--approve", rec)
	assertPRFailedItsVerify(t, runID, err, rec)
	if got := publishedSHA(t, repo, laneBranch(runID)); got != pinned {
		t.Fatalf("origin's %s is at %s, want the pinned %s: only gh's exit status may fail this leg", laneBranch(runID), got, pinned)
	}
	if sha, ok := refSHA(t, repo, pinRef(runID)); !ok || sha != pinned {
		t.Errorf("a failed pr changed the pin: %s is %q (exists %v), want %s", pinRef(runID), sha, ok, pinned)
	}
}
