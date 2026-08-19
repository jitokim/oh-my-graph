package main

import (
	"errors"
	"strings"
	"testing"
)

// --- isHelpToken --------------------------------------------------------------

func TestIsHelpToken(t *testing.T) {
	cases := []struct {
		arg  string
		want bool
	}{
		{"-h", true},
		{"-help", true},
		{"--help", true},
		{"-x", false},
		{"--input", false},
		{"", false},
		{"run-1", false},
		{"20260818-234944.646288000-1", false},
		// A value that merely CONTAINS "help" is not a help token: the three
		// exact spellings are the whole contract.
		{"--helpful", false},
		{"help", false},
	}
	for _, tc := range cases {
		if got := isHelpToken(tc.arg); got != tc.want {
			t.Errorf("isHelpToken(%q) = %v, want %v", tc.arg, got, tc.want)
		}
	}
}

// --- positionalArg -------------------------------------------------------------
//
// The four shapes #200's fix depends on: no args at all, a leading dash (one
// hyphen and two), a bare run id, and a run id followed by flags.

func TestPositionalArg(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantValue string
		wantRest  []string
		wantOK    bool
	}{
		{name: "empty args hold no value", args: nil, wantValue: "", wantRest: nil, wantOK: false},
		{name: "a leading single-dash flag is not a value", args: []string{"-h"}, wantValue: "", wantRest: []string{"-h"}, wantOK: false},
		{name: "a leading double-dash flag is not a value", args: []string{"--help"}, wantValue: "", wantRest: []string{"--help"}, wantOK: false},
		{name: "a normal run id alone is the value, with nothing left over", args: []string{"run-1"}, wantValue: "run-1", wantRest: []string{}, wantOK: true},
		{name: "a run id followed by flags splits value from rest", args: []string{"run-1", "--port", "9000"}, wantValue: "run-1", wantRest: []string{"--port", "9000"}, wantOK: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, rest, ok := positionalArg(tc.args)
			if value != tc.wantValue || ok != tc.wantOK {
				t.Errorf("positionalArg(%v) = (%q, _, %v), want (%q, _, %v)", tc.args, value, ok, tc.wantValue, tc.wantOK)
			}
			if !equalStringSlices(rest, tc.wantRest) {
				t.Errorf("positionalArg(%v) rest = %v, want %v", tc.args, rest, tc.wantRest)
			}
		})
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- helpRequest ---------------------------------------------------------------

func TestHelpRequest(t *testing.T) {
	if req := helpRequest(nil, "resume", nil); req != nil {
		t.Errorf("helpRequest(nil, ...) = %v, want nil", req)
	}
	if req := helpRequest([]string{"run-1"}, "resume", nil); req != nil {
		t.Errorf("helpRequest with a non-help positional = %v, want nil", req)
	}
	for _, token := range []string{"-h", "-help", "--help"} {
		req := helpRequest([]string{token}, "resume", nil)
		if req == nil {
			t.Fatalf("helpRequest([%q], ...) = nil, want a usage request", token)
		}
		if req.subcommand != "resume" {
			t.Errorf("helpRequest(%q).subcommand = %q, want %q", token, req.subcommand, "resume")
		}
	}
	// A FlagSet handed in rides along on the answer, so its own descriptions can
	// be printed.
	set := newServeFlags().set
	req := helpRequest([]string{"--help"}, "serve", set)
	if req == nil || req.set != set {
		t.Fatalf("helpRequest did not carry the FlagSet through: %+v", req)
	}
	// Only element 0 is ever examined: a help token further along argv is not
	// this function's business (it belongs to whatever follows the positional).
	if req := helpRequest([]string{"run-1", "--help"}, "resume", nil); req != nil {
		t.Errorf("helpRequest must not look past element 0, got %v", req)
	}
}

// --- flagInPositionalSlot -------------------------------------------------------

func TestFlagInPositionalSlot(t *testing.T) {
	if err := flagInPositionalSlot(nil, "show"); err != nil {
		t.Errorf("flagInPositionalSlot(nil, ...) = %v, want nil", err)
	}
	if err := flagInPositionalSlot([]string{"run-1"}, "show"); err != nil {
		t.Errorf("flagInPositionalSlot with a normal value = %v, want nil", err)
	}

	// A help token is answered, not refused: the caller gets a *usageRequest
	// back, which mainExitCode prints to stdout and exits 0 for.
	err := flagInPositionalSlot([]string{"--help"}, "show")
	var usage *usageRequest
	if !errors.As(err, &usage) {
		t.Fatalf("flagInPositionalSlot([--help]) = %v (%T), want a *usageRequest", err, err)
	}
	if usage.subcommand != "show" {
		t.Errorf("usage.subcommand = %q, want %q", usage.subcommand, "show")
	}

	// Any OTHER dash-prefixed token in the slot is an unknown flag, reported by
	// name — never mistaken for the value show/watch/lint/init would otherwise
	// have tried to load or create.
	err = flagInPositionalSlot([]string{"--bogus"}, "show")
	if err == nil {
		t.Fatal("flagInPositionalSlot([--bogus]) = nil, want an unknown-flag error")
	}
	if errors.As(err, &usage) {
		t.Errorf("an unrecognised flag must not be answered as help: %v", err)
	}
	if !strings.Contains(err.Error(), `unknown flag "--bogus"`) {
		t.Errorf("err = %q, want it to name the flag", err.Error())
	}
	if !strings.Contains(err.Error(), "show") {
		t.Errorf("err = %q, want it to name the subcommand", err.Error())
	}
}

// --- usageSynopsisFor ------------------------------------------------------------

func TestUsageSynopsisFor(t *testing.T) {
	line, ok := usageSynopsisFor("resume")
	if !ok || !strings.HasPrefix(line, "oh-my-graph resume") {
		t.Errorf("usageSynopsisFor(resume) = (%q, %v), want a line starting with %q", line, ok, "oh-my-graph resume")
	}
	if _, ok := usageSynopsisFor("nonexistent-subcommand"); ok {
		t.Error("usageSynopsisFor of an undocumented subcommand claimed to find a line")
	}
}

// --- usageRequest: Error() and print() -------------------------------------------

func TestUsageRequest_ErrorQuotesTheRealSynopsisLine(t *testing.T) {
	req := &usageRequest{subcommand: "show"}
	line, _ := usageSynopsisFor("show")
	if want := "usage: " + line; req.Error() != want {
		t.Errorf("Error() = %q, want %q", req.Error(), want)
	}
}

// TestUsageRequest_ErrorFallsBackForAnUndocumentedSubcommand guards the one
// defensive branch usageRequest.Error() carries: a subcommand string that does
// not match any usageLines entry falls back to the top-level runtime usage
// rather than printing an empty "usage: " line.
func TestUsageRequest_ErrorFallsBackForAnUndocumentedSubcommand(t *testing.T) {
	req := &usageRequest{subcommand: "nonexistent-subcommand"}
	if want := "usage: " + runtimeUsage; req.Error() != want {
		t.Errorf("Error() = %q, want the runtime usage fallback %q", req.Error(), want)
	}
}

func TestUsageRequest_PrintWithNoFlagSetPrintsOnlyTheSynopsis(t *testing.T) {
	req := &usageRequest{subcommand: "show"}
	var out strings.Builder
	req.print(&out)
	line, _ := usageSynopsisFor("show")
	if want := "usage: " + line + "\n"; out.String() != want {
		t.Errorf("print() = %q, want exactly %q for a subcommand with no FlagSet", out.String(), want)
	}
}

func TestUsageRequest_PrintWithAFlagSetAlsoPrintsItsDefaults(t *testing.T) {
	req := &usageRequest{subcommand: "serve", set: newServeFlags().set}
	var out strings.Builder
	req.print(&out)
	got := out.String()
	line, _ := usageSynopsisFor("serve")
	if !strings.HasPrefix(got, "usage: "+line+"\n") {
		t.Errorf("print() = %q, want it to open with the synopsis line", got)
	}
	if !strings.Contains(got, "-port") || !strings.Contains(got, "-no-open") {
		t.Errorf("print() with a FlagSet must also print its flags, got %q", got)
	}
}
