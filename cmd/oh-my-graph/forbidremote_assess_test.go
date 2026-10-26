package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/remote"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/worktree"
)

// A guarded run's end line and the goal assessor's ENGINE-OBSERVED remote
// changes block render one rule (worktree.GuardOfLeg): the operator and the
// assessor are told the same parts guarded and the same parts not guarded,
// with the same reasons.

// endGuard splits a guarded leg's end line into its not-guarded entries and
// its guarded parts.
func endGuard(t *testing.T, line string) (notGuarded []string, guarded string) {
	t.Helper()
	rest, ok := strings.CutPrefix(line, "--forbid-remote-changes: not guarded: ")
	if !ok {
		t.Fatalf("end line %q is not a plain guard line", line)
	}
	entries, guarded, ok := strings.Cut(rest, "; guarded: ")
	if !ok {
		t.Fatalf("end line %q names no guarded parts", line)
	}
	return strings.Split(entries, "; "), guarded
}

// assessorGuard is the not-guarded entries and guarded parts of the
// assessor prompt's remote changes block.
func assessorGuard(t *testing.T, prompt string) (notGuarded []string, guarded string) {
	t.Helper()
	g := strings.TrimPrefix(lineContaining(t, prompt, "  guarded: "), "  guarded: ")
	ng := strings.TrimPrefix(lineContaining(t, prompt, "  not guarded: "), "  not guarded: ")
	return strings.Split(ng, "; "), g
}

// assertSameGuard checks the end line and the assessor block name the same
// parts with the same reasons.
func assertSameGuard(t *testing.T, endLine, prompt string) {
	t.Helper()
	endNG, endG := endGuard(t, endLine)
	assessNG, assessG := assessorGuard(t, prompt)
	if !slices.Equal(endNG, assessNG) || endG != assessG {
		t.Errorf("end line and assessor disagree:\n end:      not guarded %q, guarded %q\n assessor: not guarded %q, guarded %q\n%s",
			endNG, endG, assessNG, assessG, prompt)
	}
}

// assessPrompts is every assessor prompt the fake was given, in order.
func assessPrompts(fake *runner.FakeRunner) []string {
	var prompts []string
	for _, inv := range fake.Invocations() {
		if strings.Contains(inv.Prompt, "goal assessor") {
			prompts = append(prompts, inv.Prompt)
		}
	}
	return prompts
}

// A guarded auto cycle's assessor is told what its end line says: branches
// and pull requests not guarded, "auto: no run branches", and only remotes
// guarded — never all three.
func TestRunAuto_TheAssessorIsToldTheGuardTheEndLineNames(t *testing.T) {
	out, err, _, fake := runGuardedGoalFake(t, 0, cycleAssessNotMet, cycleAssessMet, "--max-cycles", "2", "--forbid-remote-changes")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	prompts := assessPrompts(fake)
	if len(prompts) != 2 {
		t.Fatalf("%d assessor prompts, want one per cycle (2)", len(prompts))
	}
	endLine := out[strings.LastIndex(out, autoGuardLine):]
	endLine, _, _ = strings.Cut(endLine, "\n")
	for i, prompt := range prompts {
		for _, want := range []string{
			"\n  guarded: remotes\n",
			"\n  not guarded: branches (" + worktree.AutoNoRunBranches + "); pull_requests (" + worktree.AutoNoRunBranches + ")\n",
		} {
			if !strings.Contains(prompt, want) {
				t.Errorf("assessor prompt %d lacks %q:\n%s", i+1, want, prompt)
			}
		}
		if strings.Contains(prompt, "guarded: remotes, branches") {
			t.Errorf("assessor prompt %d claims branches guarded:\n%s", i+1, prompt)
		}
		assertSameGuard(t, endLine, prompt)
	}
}

// A guarded run whose repository config was unreadable at the after, or
// whose commit check's own read failed with the config still readable, names
// branches and pull requests not guarded with that attribution-unavailable
// reason, on its end line and in the block its assessor would read. The
// block still names them compared and quotes their entries: the engine saw
// them, it only could not attribute in them.
func TestRunGraph_AnUnreadableConfigIsNamedAlikeOnTheEndLineAndToTheAssessor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		during func(o *worktree.FakeRemoteObserver)
		reason string
	}{
		{"config unreadable", func(o *worktree.FakeRemoteObserver) { o.SetRepoConfig("", errors.New("exit status 128")) },
			worktree.AttributionConfigUnreadable},
		{"check read failed", func(o *worktree.FakeRemoteObserver) { o.SetAttributionErr(errors.New("signal: killed")) },
			worktree.AttributionCheckReadFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunHome(t)
			obs, prs := guardedRemote(t, nil)
			change := func(o *worktree.FakeRemoteObserver, p *remote.FakePRLister) {
				pushBranch("feat/x", remoteHumaSHA)(o, p)
				tc.during(o)
			}
			out, err := runWithRemoteChange(t, obs, prs, change, "--forbid-remote-changes")
			if err != nil {
				t.Fatalf("err = %v (exit %d), want nil", err, exitCodeForError(err))
			}
			endLine := lineContaining(t, out, "--forbid-remote-changes: not guarded")
			want := "--forbid-remote-changes: not guarded: branches (" + tc.reason + "); pull_requests (" +
				tc.reason + "); guarded: remotes"
			if endLine != want {
				t.Errorf("end line = %q, want %q", endLine, want)
			}

			evidence, err := cycleEvidence(soleRunID(t), coordinator.Plan{Graph: &graph.Graph{Nodes: []graph.Node{{ID: "work"}}}}, true)
			if err != nil {
				t.Fatalf("cycleEvidence: %v", err)
			}
			if evidence.NoRunBranches {
				t.Error("a `run` has run branches: NoRunBranches = true")
			}
			fake := runner.NewFakeRunner(nil)
			fake.KeyFn = func(runner.NodeInvocation) string { return "assess" }
			fake.SetOutcome("assess", runner.NodeOutcome{Result: cycleAssessMet})
			if _, err := coordinator.New(fake).Assess(context.Background(), "ship it", evidence); err != nil {
				t.Fatalf("Assess: %v", err)
			}
			prompt := fake.Invocations()[0].Prompt
			if want := "\n  not guarded: branches (" + tc.reason + "); pull_requests (" + tc.reason + ")\n"; !strings.Contains(prompt, want) {
				t.Errorf("assessor prompt lacks %q:\n%s", want, prompt)
			}
			if got := lineContaining(t, prompt, "  compared: "); got != "  compared: remotes, branches, pull_requests" {
				t.Errorf("compared line = %q, want %q:\n%s", got, "  compared: remotes, branches, pull_requests", prompt)
			}
			if strings.Contains(prompt, "  not compared: ") {
				t.Errorf("assessor prompt names a part not compared:\n%s", prompt)
			}
			if !strings.Contains(prompt, "\n  leg 1: branch_created origin/feat/x at ") {
				t.Errorf("assessor prompt does not quote the pushed branch's entry:\n%s", prompt)
			}
			assertSameGuard(t, endLine, prompt)
		})
	}
}
