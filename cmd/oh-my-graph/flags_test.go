package main

import (
	"errors"
	"strings"
	"testing"
)

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

// TestRunFlags_Help pins #200 for `run`: a help token in the graph-path slot
// must answer with the synopsis (as a *usageRequest, which mainExitCode
// prints to stdout and exits 0 for) rather than being read as a literal
// path and failed as "read graph file \"--help\": ... no such file". Both
// spellings the flag package accepts are checked, since `run` owns a
// FlagSet and either could in principle reach flag.Parse instead.
func TestRunFlags_Help(t *testing.T) {
	for _, arg := range []string{"--help", "-h"} {
		f := newRunFlags()
		err := f.parse([]string{arg})
		var usage *usageRequest
		if !errors.As(err, &usage) {
			t.Fatalf("parse([%q]) = %v (%T), want a *usageRequest", arg, err, err)
		}
		if !strings.Contains(usage.Error(), "oh-my-graph run") {
			t.Errorf("usage.Error() = %q, want it to name `run`'s synopsis", usage.Error())
		}
		if f.graphPath != "" {
			t.Errorf("graphPath = %q after a help request, want it left unset", f.graphPath)
		}
	}
}

// TestRunFlags_DashPrefixedGraphPathIsNotSwallowed is the guard the other
// direction: an unrecognised flag standing where the graph path goes must be
// reported as a flag error, never opened as a file named after itself.
func TestRunFlags_DashPrefixedGraphPathIsNotSwallowed(t *testing.T) {
	f := newRunFlags()
	f.set.SetOutput(&strings.Builder{}) // silence flag's own usage print
	err := f.parse([]string{"--bogus"})
	if err == nil {
		t.Fatal("expected an error for an unknown flag in the graph-path slot")
	}
	if strings.Contains(err.Error(), "missing graph file") || strings.Contains(err.Error(), "no such file") {
		t.Errorf("err = %v, an unknown flag must not read as a missing or unopenable graph path", err)
	}
	if f.graphPath != "" {
		t.Errorf("graphPath = %q, want it left unset when the slot held a flag", f.graphPath)
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

// TestAutoFlags_Help pins #200 for `auto`: before the fix a flag-shaped goal
// was refused with "missing goal" regardless of what it was — so `auto
// --help` never got the flag list it asked for, only the same refusal a typo
// would get. Help must now be answered instead of refused.
func TestAutoFlags_Help(t *testing.T) {
	for _, arg := range []string{"--help", "-h"} {
		f := newAutoFlags()
		err := f.parse([]string{arg})
		var usage *usageRequest
		if !errors.As(err, &usage) {
			t.Fatalf("parse([%q]) = %v (%T), want a *usageRequest", arg, err, err)
		}
		if !strings.Contains(usage.Error(), "oh-my-graph auto") {
			t.Errorf("usage.Error() = %q, want it to name `auto`'s synopsis", usage.Error())
		}
		if f.goal != "" {
			t.Errorf("goal = %q after a help request, want it left unset", f.goal)
		}
	}
}

// TestAutoFlags_UnknownDashPrefixedGoalStaysRefusedNotAFlagError is the
// contrast with run/resume/show/watch: `auto`'s positional-slot rule was
// already REFUSAL rather than a FlagSet, since a flag-shaped goal is rejected
// before any planner call. That refusal must survive unchanged for anything
// other than a literal help token — the fix must not turn every dash-shaped
// goal into a flag.Parse error.
func TestAutoFlags_UnknownDashPrefixedGoalStaysRefusedNotAFlagError(t *testing.T) {
	err := newAutoFlags().parse([]string{"--not-a-real-flag"})
	if err == nil || !strings.Contains(err.Error(), "missing goal") {
		t.Errorf("err = %v, want the pre-existing \"missing goal\" refusal", err)
	}
	var usage *usageRequest
	if errors.As(err, &usage) {
		t.Errorf("an unrecognised flag-shaped goal must not be answered as help: %v", err)
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

// --no-agent is repeatable and order-preserving: it names one agent, and a
// user with two agents to decline has to be able to say so without giving up
// every mapping in the plan (which is what --no-agent-mapping costs).
func TestAutoFlags_CollectsRepeatedNoAgent(t *testing.T) {
	f := newAutoFlags()
	err := f.parse([]string{"write the design doc", "--no-agent", "architect", "--no-agent", "doc-writer"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := []string(f.noAgents); len(got) != 2 || got[0] != "architect" || got[1] != "doc-writer" {
		t.Errorf("noAgents = %v, want both names in order", got)
	}
	// The two opt-outs are independent: naming one agent must not read as
	// turning mapping off.
	if f.noAgentMapping {
		t.Error("--no-agent must not set --no-agent-mapping")
	}
}

// A blank name is a typo, and a typo that silently declined nothing would read
// exactly like an opt-out that took.
func TestAgentNameFlag_RejectsABlankName(t *testing.T) {
	var f agentNameFlag
	if err := f.Set("  "); err == nil {
		t.Fatal("expected an error for a blank --no-agent value")
	}
	if len(f) != 0 {
		t.Errorf("noAgents = %v, want nothing collected from a rejected value", f)
	}
}

func TestInputFlag_RejectsMalformedPair(t *testing.T) {
	f := make(inputFlag)
	if err := f.Set("no-equals-sign"); err == nil {
		t.Fatal("expected an error for a pair without '='")
	}
}

func TestResumeFlags_ParsesRunIDAndGateFlags(t *testing.T) {
	f := newResumeFlags()
	if err := f.parse([]string{"run-1", "--approve", "gate-a", "--concurrency", "2"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.runID != "run-1" {
		t.Errorf("runID = %q, want run-1", f.runID)
	}
	if f.approveGate != "gate-a" {
		t.Errorf("approveGate = %q, want gate-a", f.approveGate)
	}
	if f.concurrency != 2 {
		t.Errorf("concurrency = %d, want 2", f.concurrency)
	}
}

func TestResumeFlags_MissingRunID(t *testing.T) {
	if err := newResumeFlags().parse(nil); err == nil {
		t.Fatal("expected an error when no run id is given")
	}
}

// TestResumeFlags_Help is the reported bug itself (#198's reporter, #200):
// `resume --help` used to be read as `f.runID = "--help"`, so the flag list
// the user asked for was replaced by `load run "--help": ... no such file or
// directory`. Both `--help` and `-h` must now answer with the synopsis
// instead, and must never set runID.
func TestResumeFlags_Help(t *testing.T) {
	for _, arg := range []string{"--help", "-h"} {
		f := newResumeFlags()
		err := f.parse([]string{arg})
		var usage *usageRequest
		if !errors.As(err, &usage) {
			t.Fatalf("parse([%q]) = %v (%T), want a *usageRequest", arg, err, err)
		}
		if !strings.Contains(usage.Error(), "oh-my-graph resume") {
			t.Errorf("usage.Error() = %q, want it to name `resume`'s synopsis", usage.Error())
		}
		if f.runID != "" {
			t.Errorf("runID = %q after a help request, want it left unset", f.runID)
		}
	}
}

// TestResumeFlags_DashPrefixedRunIDIsNotSwallowed is the guard the other
// direction: an unrecognised flag standing in the run-id slot must be
// reported as a flag error, never taken as the run id to load.
func TestResumeFlags_DashPrefixedRunIDIsNotSwallowed(t *testing.T) {
	f := newResumeFlags()
	f.set.SetOutput(&strings.Builder{}) // silence flag's own usage print
	err := f.parse([]string{"--bogus"})
	if err == nil {
		t.Fatal("expected an error for an unknown flag in the run-id slot")
	}
	if strings.Contains(err.Error(), "missing run id") {
		t.Errorf("err = %v, an unknown flag must not read as a missing run id", err)
	}
	if f.runID != "" {
		t.Errorf("runID = %q, want it left unset when the slot held a flag", f.runID)
	}
}

// TestResumeFlags_RejectsInput proves resume has no --input flag at all, so
// `--input k=v` fails at argv parsing rather than being silently accepted and
// ignored — DESIGN.md, "--input on resume is rejected": inputs come from the
// snapshot, and changing one mid-run would make the already-persisted
// artifacts inconsistent with the prompts that produced them.
func TestResumeFlags_RejectsInput(t *testing.T) {
	err := newResumeFlags().parse([]string{"run-1", "--input", "repo=/work/app"})
	if err == nil {
		t.Fatal("expected an error for --input on resume")
	}
}
