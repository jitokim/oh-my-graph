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

func TestInputFlag_RejectsMalformedPair(t *testing.T) {
	f := make(inputFlag)
	if err := f.Set("no-equals-sign"); err == nil {
		t.Fatal("expected an error for a pair without '='")
	}
}
