package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/usermodel"
)

// This file EXERCISES the settings-read → argv path instead of reasoning about
// it. Every other test of ADR 0034 starts halfway along: usermodel_test.go
// hands Read a path it built itself, claude_test.go hands buildArgs a Model
// string a human typed, and the coordinator tests inject a fake reader. None of
// them starts where a real run starts — at a settings.json on disk — so none of
// them can tell you what a planned node's argv actually becomes when the
// operator's file says something surprising.
//
// So each subtest below writes a real settings.json into a scratch HOME, calls
// usermodel.DefaultPath() (no path passed in — the resolution is part of what is
// under test), calls usermodel.Read, feeds whatever came back into a real
// NodeInvocation, and captures the argv CLIRunner built from it. What the code
// did is logged verbatim in every case, including the ones that pass, because
// the value of this file is the record, not only the green.
//
// Nothing here spawns real claude. The one subtest that needs a CLI rejection
// uses the scripted stub pattern already established in claude_test.go
// (writeStub), which is a shell script standing in for the CLI's exit code and
// stderr.

// scratchSettingsHome gives one subtest a private HOME and makes it the only
// place usermodel.DefaultPath can resolve to.
//
// CLAUDE_CONFIG_DIR is cleared rather than left alone: DefaultPath prefers it
// over the home directory (usermodel.go:81-83), so a developer who has it set
// would otherwise run these subtests against their own settings file and see
// them pass or fail for reasons that have nothing to do with the fixture.
// USERPROFILE is cleared for the same reason on Windows, where os.UserHomeDir
// reads it instead of HOME. OMG_HOME is pointed inside the scratch too — no
// code on this path reads it, and setting it is how that stays true visibly
// rather than by assumption.
func scratchSettingsHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("OMG_HOME", filepath.Join(home, ".oh-my-graph"))
	return home
}

// writeSettings puts content at the exact path DefaultPath will look for and
// returns that path, so a subtest asserting the error names the file has a real
// address to compare against.
func writeSettings(t *testing.T, home, content string) string {
	t.Helper()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, usermodel.SettingsFileName)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// resolveModelToArgv walks the whole path a planned node walks: resolve the
// settings location, read the one key, carry the result into an invocation,
// build the argv. It returns all three observations so a subtest can record
// what happened even when it only asserts one of them.
//
// The invocation is shaped like a planned node (layer 1 isolation, dontAsk)
// because that is the shape the model choice exists for — a hand-written node
// loads the operator's settings itself and never reaches usermodel at all.
func resolveModelToArgv(t *testing.T) (model string, readErr error, argv []string) {
	t.Helper()
	path := usermodel.DefaultPath()
	model, readErr = usermodel.Read(path)

	r := NewCLIRunner(RuntimeClaude, WithBinary("claude"))
	cmd := r.buildCmd(context.Background(), NodeInvocation{
		Prompt:         testPrompt,
		PermissionMode: "dontAsk",
		Model:          model,
		Policy: ToolPolicy{
			SettingSources: noSettings(),
			AllowedTools:   []string{"Read", "Glob"},
		},
	})
	return model, readErr, cmd.Args
}

// TestModelResolvesFromSettingsToArgv runs the four failure-shaped cases and the
// positive control against a settings file this test wrote, and records the argv
// or the error each one really produced.
func TestModelResolvesFromSettingsToArgv(t *testing.T) {
	// Case 1. A name no build of the CLI knows. The claim under test is that
	// nothing between the file and argv inspects the string — no allowlist, no
	// normalisation (usermodel.go:45-48, runner.go:129-130) — and that the
	// rejection is therefore the CLI's, arriving as a dead node rather than as a
	// quiet fall back to the default (runner.go:130-133).
	t.Run("unknown model name reaches argv verbatim and the rejection kills the node", func(t *testing.T) {
		const unknown = "claude-not-a-real-model-9"
		home := scratchSettingsHome(t)
		writeSettings(t, home, `{"model": "`+unknown+`"}`)

		model, err, argv := resolveModelToArgv(t)
		t.Logf("Read returned model=%q err=%v", model, err)
		t.Logf("argv = %q", argv)

		if err != nil {
			t.Fatalf("Read rejected a name it has no vocabulary for: %v", err)
		}
		if model != unknown {
			t.Errorf("Read normalised the operator's string: got %q, want %q", model, unknown)
		}
		if !hasFlagValue(argv, "--model", unknown) {
			t.Errorf("argv does not carry --model %q: %q", unknown, argv)
		}

		// The second half: what happens when the CLI then refuses it. The stub
		// is claude's documented shape for a bad flag value — nothing on stdout,
		// a complaint on stderr, non-zero exit — and it echoes the argv it
		// received so the assertion proves the flag reached the child process
		// rather than merely reaching a []string in this process.
		if runtime.GOOS == "windows" {
			t.Skip("the stub is a shebang script; the argv half above already ran")
		}
		stub := writeStub(t, `#!/bin/sh
echo "argv: $*" >&2
echo "Unknown model: claude-not-a-real-model-9" >&2
exit 1
`)
		r := NewCLIRunner(RuntimeClaude, WithBinary(stub))
		outcome, runErr := r.Run(context.Background(), NodeInvocation{
			Prompt:         testPrompt,
			PermissionMode: "dontAsk",
			Model:          model,
		})
		t.Logf("Run returned outcome=%+v err=%v", outcome, runErr)

		if runErr == nil {
			t.Fatalf("a CLI that refused the model produced no error; outcome = %+v", outcome)
		}
		var outErr *NodeOutputError
		if !errors.As(runErr, &outErr) {
			t.Fatalf("Run error = %T: %v, want *NodeOutputError", runErr, runErr)
		}
		if !strings.Contains(runErr.Error(), unknown) {
			t.Errorf("the failure does not name the model that caused it: %v", runErr)
		}
		if !strings.Contains(outErr.Stderr, "--model "+unknown) {
			t.Errorf("the child's own argv does not show --model %q: %q", unknown, outErr.Stderr)
		}
	})

	// Case 2. The key is absent from an otherwise valid settings document. The
	// document below is realistic: it holds the capability keys layer 1 exists
	// to withhold, and none of them may become a model choice.
	t.Run("absent model key expresses no choice", func(t *testing.T) {
		home := scratchSettingsHome(t)
		path := writeSettings(t, home, `{
  "permissions": {"allow": ["Bash(*)"]},
  "env": {"SOME_SETTING": "some-value"}
}`)

		model, err, argv := resolveModelToArgv(t)
		t.Logf("settings at %s", path)
		t.Logf("Read returned model=%q err=%v", model, err)
		t.Logf("argv = %q", argv)

		if err != nil {
			t.Fatalf("a settings file without the key is ordinary, not an error: %v", err)
		}
		if model != "" {
			t.Errorf("Read invented a choice the operator did not express: %q", model)
		}
		// What IS in the argv, asserted positively: the node is still fully
		// formed and still isolated. The interesting property is that the run
		// proceeds, not that a flag is missing.
		if !hasFlagValue(argv, "-p", testPrompt) {
			t.Errorf("argv lost the prompt: %q", argv)
		}
		if !hasFlagValue(argv, "--permission-mode", "dontAsk") {
			t.Errorf("argv lost the permission mode: %q", argv)
		}
		if !hasFlagValue(argv, "--setting-sources", "") {
			t.Errorf("argv lost layer 1: %q", argv)
		}
	})

	// Case 3. No settings file at all. usermodel.go:77-79 calls this a supported
	// machine rather than a failure, so the observation to record is that the
	// read is clean and the node is still runnable.
	t.Run("absent settings file is not a failure", func(t *testing.T) {
		home := scratchSettingsHome(t)
		path := filepath.Join(home, ".claude", usermodel.SettingsFileName)
		if _, statErr := os.Stat(path); statErr == nil {
			t.Fatalf("fixture is wrong: %s exists before the subtest wrote anything", path)
		}

		model, err, argv := resolveModelToArgv(t)
		t.Logf("no file at %s", path)
		t.Logf("Read returned model=%q err=%v", model, err)
		t.Logf("argv = %q", argv)

		if err != nil {
			t.Fatalf("a machine with no settings file must still plan: %v", err)
		}
		if model != "" {
			t.Errorf("Read produced a model from a file that does not exist: %q", model)
		}
		if !hasFlagValue(argv, "--permission-mode", "dontAsk") {
			t.Errorf("argv is not a runnable node: %q", argv)
		}
	})

	// Case 4. The file exists and will not decode. Two things are under test:
	// that the read fails LOUDLY enough to reach a caller (a non-nil error,
	// message recorded verbatim), and that the message carries the path and not
	// the document — the file being parsed holds the operator's credentials
	// (usermodel.go:30-35, :136-138), so a fixture credential is planted here to
	// make that a real check rather than a stated intention.
	t.Run("malformed settings file returns an error naming the path only", func(t *testing.T) {
		const planted = "sk-ant-oat01-NOT-A-REAL-TOKEN"
		home := scratchSettingsHome(t)
		path := writeSettings(t, home, `{"model": "opus[1m]", "env": {"ANTHROPIC_AUTH_TOKEN": "`+planted+`"},`)

		model, err, argv := resolveModelToArgv(t)
		t.Logf("settings at %s", path)
		t.Logf("Read returned model=%q err=%v", model, err)
		t.Logf("argv = %q", argv)

		if err == nil {
			t.Fatalf("a settings file that will not decode read clean: model = %q", model)
		}
		t.Logf("verbatim error: %s", err.Error())
		if !strings.Contains(err.Error(), path) {
			t.Errorf("the error does not name the file the operator must fix: %v", err)
		}
		if strings.Contains(err.Error(), planted) {
			t.Errorf("the error leaks the settings document's contents: %v", err)
		}
		if model != "" {
			t.Errorf("a failed read produced a model anyway: %q", model)
		}
		// And the recorded consequence: the error is the READ's, not the run's.
		// The invocation built from it is a complete, runnable node, so this seam
		// does not stop the run — the warning that tells the operator lives one
		// layer up (coordinator.chosenModel, exercised by
		// TestPlan_MalformedSettingsWarnsButStillPlans and
		// TestExecutePlan_ModelWarningIsPrintedOnce).
		if !hasFlagValue(argv, "--permission-mode", "dontAsk") {
			t.Errorf("argv is not a runnable node: %q", argv)
		}
	})

	// The positive control. "opus[1m]" is not an invented fixture: it is the
	// value in this project's own settings.json, so this subtest is the closest
	// thing here to a real run's argv.
	t.Run("a configured model reaches argv", func(t *testing.T) {
		const chosen = "opus[1m]"
		home := scratchSettingsHome(t)
		writeSettings(t, home, `{"model": "`+chosen+`", "permissions": {"allow": ["Bash(*)"]}}`)

		model, err, argv := resolveModelToArgv(t)
		t.Logf("Read returned model=%q err=%v", model, err)
		t.Logf("argv = %q", argv)

		if err != nil {
			t.Fatalf("a well-formed settings file failed to read: %v", err)
		}
		if model != chosen {
			t.Errorf("Read returned %q, want the operator's own %q", model, chosen)
		}
		if !hasFlagValue(argv, "--model", chosen) {
			t.Errorf("argv does not carry --model %q: %q", chosen, argv)
		}
	})
}
