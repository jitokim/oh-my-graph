package main

import "testing"

func TestRunFlags_ParsesInputsAndOptions(t *testing.T) {
	f := newRunFlags()
	err := f.parse([]string{
		"graphs/x.yaml",
		"--input", "repo=/work/app",
		"--input", "port=8080",
		"--concurrency", "3",
		"--continue-on-fail",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.graphPath != "graphs/x.yaml" {
		t.Errorf("graphPath = %q", f.graphPath)
	}
	if f.inputs["repo"] != "/work/app" || f.inputs["port"] != "8080" {
		t.Errorf("inputs = %v", f.inputs)
	}
	if f.concurrency != 3 {
		t.Errorf("concurrency = %d, want 3", f.concurrency)
	}
	if !f.continueOnFail {
		t.Error("continue-on-fail flag not set")
	}
}

func TestRunFlags_MissingGraphPath(t *testing.T) {
	if err := newRunFlags().parse(nil); err == nil {
		t.Fatal("expected an error when no graph file is given")
	}
}

func TestAutoFlags_ParsesGoalAndOptions(t *testing.T) {
	f := newAutoFlags()
	err := f.parse([]string{
		"lint the repo and summarize findings",
		"--input", "repo=/work/app",
		"--concurrency", "2",
		"--continue-on-fail",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.goal != "lint the repo and summarize findings" {
		t.Errorf("goal = %q", f.goal)
	}
	if f.inputs["repo"] != "/work/app" {
		t.Errorf("inputs = %v", f.inputs)
	}
	if f.concurrency != 2 {
		t.Errorf("concurrency = %d, want 2", f.concurrency)
	}
	if !f.continueOnFail {
		t.Error("continue-on-fail flag not set")
	}
}

func TestAutoFlags_MissingGoal(t *testing.T) {
	if err := newAutoFlags().parse(nil); err == nil {
		t.Fatal("expected an error when no goal is given")
	}
	if err := newAutoFlags().parse([]string{"   "}); err == nil {
		t.Fatal("expected an error for a blank goal")
	}
}

func TestAutoFlags_RejectsFlagShapedGoal(t *testing.T) {
	// Flags placed before the goal must not be swallowed as the goal — that
	// would spend a real planner call on the literal string "--input".
	err := newAutoFlags().parse([]string{"--input", "repo=/x", "lint the repo"})
	if err == nil {
		t.Fatal("expected an error when a flag precedes the goal")
	}
}

func TestAutoFlags_RejectsUnquotedMultiWordGoal(t *testing.T) {
	err := newAutoFlags().parse([]string{"lint", "the", "repo"})
	if err == nil {
		t.Fatal("expected an error for trailing arguments after the goal")
	}
}

func TestInputFlag_RejectsMalformedPair(t *testing.T) {
	f := make(inputFlag)
	if err := f.Set("no-equals-sign"); err == nil {
		t.Fatal("expected an error for a pair without '='")
	}
}
