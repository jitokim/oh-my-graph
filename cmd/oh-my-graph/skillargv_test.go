package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// The argv layer, which is the only layer a real node actually obeys.
//
// Every other skill-activation test stops one seam short: skillactivation_test
// asserts the runner.ToolPolicy the FakeRunner was handed, and
// internal/runner/claude_test asserts that buildArgs renders a policy it was
// given directly. Neither joins the two, so a policy that is built correctly
// and then never reaches the process — the whole class of "the plan printout
// says ENABLED and the node runs without it" — passes both. These tests close
// that by driving the real `auto` argv path with a real ClaudeCLIRunner
// pointed at a stub binary that records the argv it was spawned with, and
// asserting on those bytes.
//
// What is asserted here is ADR 0017 §1's decision as a node sees it:
// `--plugin-dir <staged>` and `Skill` inside `--tools` on an activated node,
// `--setting-sources ""` unmoved beside them, and none of it on an
// agent-mapped node or under --no-skill-activation.

const (
	// argvDirEnv is where the stub writes one file per invocation, and
	// repliesDirEnv is where it reads its canned envelopes from. They travel
	// as environment variables because childenv.Scrub deletes only the two
	// ANTHROPIC_* keys, so everything else the test sets reaches the child.
	argvDirEnv    = "OMG_TEST_ARGV_DIR"
	repliesDirEnv = "OMG_TEST_REPLIES_DIR"
	// failPromptEnv makes the stub exit non-zero for the one node whose prompt
	// contains it, which is how a test manufactures the failed node that
	// `resume --retry-failed` exists for. Empty means every node succeeds.
	failPromptEnv = "OMG_TEST_FAIL_PROMPT"
)

// stubClaude is a `claude` that never thinks: it appends its own argv,
// NUL-separated, to a fresh file under $OMG_TEST_ARGV_DIR, then answers with
// the planner's spec or a node's result depending on whether it was given the
// planner prompt. NUL is the separator because it is the one byte an argv
// element cannot contain, and a planner prompt is full of newlines and quotes.
const stubClaude = `#!/bin/sh
out=$(mktemp "$OMG_TEST_ARGV_DIR/argv.XXXXXX")
for a in "$@"; do printf '%s\0' "$a" >> "$out"; done
for a in "$@"; do
  case "$a" in
    *"planning coordinator"*) cat "$OMG_TEST_REPLIES_DIR/plan.json"; exit 0 ;;
  esac
done
if [ -n "$OMG_TEST_FAIL_PROMPT" ]; then
  for a in "$@"; do
    case "$a" in
      *"$OMG_TEST_FAIL_PROMPT"*) echo "stub claude: failing on request" >&2; exit 1 ;;
    esac
  done
fi
cat "$OMG_TEST_REPLIES_DIR/node.json"
`

// argvProbeSpec is the plan the stub planner returns: a four-node chain whose
// `review` node is name-matched by the agent staged below, so one run exercises
// both an activated node and the agent-mapped exclusion — the "3 of 4" shape.
const argvProbeSpec = `{"name":"argv-probe","version":"1","nodes":[` +
	`{"id":"propose","prompt":"draft the proposal","allowed_tools":["Read"]},` +
	`{"id":"review","prompt":"judge the proposal","allowed_tools":["Read"],"depends_on":["propose"]},` +
	`{"id":"artifact","prompt":"render the artifact","allowed_tools":["Read"],"depends_on":["review"]},` +
	`{"id":"check","prompt":"check the artifact","allowed_tools":["Read"],"depends_on":["artifact"]}]}`

// recordedArgv is one spawn's argv exactly as the CLI built it, minus argv[0].
type recordedArgv []string

// value is the argument following flag, and false when the flag is absent.
// The two are separate results because layer 1's whole value is the EMPTY
// string: "--setting-sources" with "" and no "--setting-sources" at all are
// the difference between an isolated node and one loading the user's standing
// grants, and a lone string cannot tell them apart.
func (a recordedArgv) value(flag string) (string, bool) {
	for i, arg := range a {
		if arg == flag && i+1 < len(a) {
			return a[i+1], true
		}
	}
	return "", false
}

func (a recordedArgv) has(flag string) bool {
	return slices.Contains(a, flag)
}

// tools is the parsed --tools list: the node's ENTIRE built-in tool set.
func (a recordedArgv) tools() []string {
	joined, ok := a.value("--tools")
	if !ok {
		return nil
	}
	return strings.Split(joined, ",")
}

// prompt identifies which node (or the planner) this spawn was.
func (a recordedArgv) prompt() string {
	p, _ := a.value("-p")
	return p
}

// argvProbe is the stub-claude harness: an isolated home with one skill and
// one name-matching agent, the canned envelopes, and the directory the stub
// records argv into. It is a value rather than inline setup because a resumed
// leg is a SECOND process against the SAME run, and it needs the same stub and
// the same home with a fresh argv directory.
type argvProbe struct {
	stub    string
	argvDir string
}

func newArgvProbe(t *testing.T) *argvProbe {
	t.Helper()
	isolateRunHome(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFileTree(t, filepath.Join(home, ".claude", "skills", "architecture-design", "SKILL.md"),
		"---\nname: architecture-design\ndescription: designs systems\n---\n\nthe design procedure\n")
	// A user agent that name-matches the `review` node, so the run reproduces
	// the shape a real user gets: activation on some nodes and the
	// agent-mapped exclusion on another.
	writeFileTree(t, filepath.Join(home, ".claude", "agents", "code-reviewer.md"),
		"---\nname: code-reviewer\ndescription: reviews changes\ntools: Read\n---\n\nreview carefully\n")

	replies := t.TempDir()
	writeFileTree(t, filepath.Join(replies, "plan.json"), envelopeJSON(t, "s-plan", argvProbeSpec))
	writeFileTree(t, filepath.Join(replies, "node.json"), envelopeJSON(t, "s-node", "done"))
	t.Setenv(repliesDirEnv, replies)

	probe := &argvProbe{stub: filepath.Join(t.TempDir(), "claude")}
	writeFileTree(t, probe.stub, stubClaude)
	if err := os.Chmod(probe.stub, 0o755); err != nil {
		t.Fatal(err)
	}
	probe.freshArgvDir(t)
	return probe
}

// runner is a REAL ClaudeCLIRunner pointed at the stub — the whole point of
// this file is that nothing between the policy and the argv is faked.
func (p *argvProbe) runner() runner.NodeRunner {
	return runner.NewClaudeCLIRunner(runner.WithBinary(p.stub))
}

// freshArgvDir points the stub at an empty recording directory, so a second
// leg's spawns are its own and cannot be satisfied by the first leg's.
func (p *argvProbe) freshArgvDir(t *testing.T) {
	t.Helper()
	p.argvDir = t.TempDir()
	t.Setenv(argvDirEnv, p.argvDir)
}

func (p *argvProbe) spawns(t *testing.T) map[string]recordedArgv {
	t.Helper()
	return readRecordedArgv(t, p.argvDir)
}

// runAutoCapturingArgv drives a whole `auto` run through the real argv path
// against a stub claude, and returns the run id plus every node spawn's argv
// keyed by node prompt. The planner's own spawn is dropped: its ceiling is a
// different decision (coordinatorInvocation), tested elsewhere.
func runAutoCapturingArgv(t *testing.T, args ...string) (string, map[string]recordedArgv) {
	t.Helper()
	probe := newArgvProbe(t)

	var err error
	captureStdout(t, func() {
		err = runAutoWith(append([]string{"turn the issue into a proposal"}, args...),
			probe.runner(), browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("auto run: %v", err)
	}
	return soleRunID(t), probe.spawns(t)
}

// readRecordedArgv loads every spawn the stub recorded, keyed by node prompt.
func readRecordedArgv(t *testing.T, dir string) map[string]recordedArgv {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read recorded argv: %v", err)
	}
	byPrompt := make(map[string]recordedArgv, len(entries))
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		// Each element is NUL-TERMINATED, so the split leaves a trailing "".
		fields := bytes.Split(raw, []byte{0})
		argv := make(recordedArgv, 0, len(fields))
		for _, field := range fields[:len(fields)-1] {
			argv = append(argv, string(field))
		}
		if strings.Contains(argv.prompt(), "planning coordinator") {
			continue
		}
		byPrompt[argv.prompt()] = argv
	}
	return byPrompt
}

// nodeArgv returns one node's recorded spawn, failing with the prompts that
// WERE recorded — a node that never ran is the failure most worth naming.
func nodeArgv(t *testing.T, spawns map[string]recordedArgv, prompt string) recordedArgv {
	t.Helper()
	argv, ok := spawns[prompt]
	if !ok {
		recorded := make([]string, 0, len(spawns))
		for p := range spawns {
			recorded = append(recorded, p)
		}
		sort.Strings(recorded)
		t.Fatalf("no node spawned with prompt %q; recorded: %v", prompt, recorded)
	}
	return argv
}

// envelopeJSON is the `claude --output-format json` envelope carrying result.
func envelopeJSON(t *testing.T, sessionID, result string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"session_id":     sessionID,
		"result":         result,
		"total_cost_usd": 0.01,
		"subtype":        "success",
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw) + "\n"
}

func writeFileTree(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// An activated node's REAL argv carries both halves of the mechanism, and the
// ceiling beside them is unmoved. Either half alone does nothing: measured in
// ADR 0017 (f), without `Skill` in --tools the staged definitions load and
// cannot run, and a --plugin-dir pointing at nothing exits 0 in silence.
func TestRunAuto_ActivatedNodeArgvCarriesTheSkillToolAndThePluginDir(t *testing.T) {
	runID, spawns := runAutoCapturingArgv(t)

	wantDir := filepath.Join(runDirFor(runID), "skills-plugin")
	for _, prompt := range []string{"draft the proposal", "render the artifact", "check the artifact"} {
		argv := nodeArgv(t, spawns, prompt)

		if dir, ok := argv.value("--plugin-dir"); !ok || dir != wantDir {
			t.Errorf("%s: --plugin-dir = %q (present=%t), want %q\nargv: %q", prompt, dir, ok, wantDir, argv)
		}
		if tools := argv.tools(); !slices.Contains(tools, coordinator.SkillToolName) {
			t.Errorf("%s: --tools = %v, want %s among them\nargv: %q", prompt, tools, coordinator.SkillToolName, argv)
		}
		// Layer 1 did not move. This is the assertion the whole ADR turns on:
		// measurement (g) showed that relaxing it lets a node declaring
		// Bash(git *) run an out-of-scope command.
		if sources, ok := argv.value("--setting-sources"); !ok || sources != "" {
			t.Errorf("%s: --setting-sources = %q (present=%t), want a rendered empty value\nargv: %q", prompt, sources, ok, argv)
		}
		if !argv.has("--strict-mcp-config") {
			t.Errorf("%s: layer 4 is missing from the argv: %q", prompt, argv)
		}
		// The grant is layer 3 only: --allowedTools stays the node's own
		// declaration, which is what keeps `Skill` out of graph.json too.
		if grant, _ := argv.value("--allowedTools"); strings.Contains(grant, coordinator.SkillToolName) {
			t.Errorf("%s: --allowedTools = %q, want layer 2 untouched", prompt, grant)
		}
	}

	// The directory the argv names holds the corpus, so "the flag was passed"
	// is backed by something the CLI can actually read.
	staged := filepath.Join(wantDir, "skills", "architecture-design", "SKILL.md")
	if raw, err := os.ReadFile(staged); err != nil {
		t.Errorf("staged skill missing: %v", err)
	} else if !strings.Contains(string(raw), "the design procedure") {
		t.Errorf("staged skill is not the user's file:\n%s", raw)
	}
}

// The agent-mapped node is excluded, and the argv is where that has to show:
// applyAgentMapping drops its layer 1 so `--agent` can resolve, and
// `--agent` + a staged plugin + the user's settings is a composite ADR 0017
// never measured.
func TestRunAuto_AgentMappedNodeArgvGetsNoActivation(t *testing.T) {
	_, spawns := runAutoCapturingArgv(t)
	argv := nodeArgv(t, spawns, "judge the proposal")

	if name, ok := argv.value("--agent"); !ok || name != "code-reviewer" {
		t.Fatalf("precondition failed: --agent = %q (present=%t), want code-reviewer\nargv: %q", name, ok, argv)
	}
	if argv.has("--plugin-dir") {
		t.Errorf("an agent-mapped node was given --plugin-dir: %q", argv)
	}
	if tools := argv.tools(); slices.Contains(tools, coordinator.SkillToolName) {
		t.Errorf("--tools = %v, want no %s on an agent-mapped node", tools, coordinator.SkillToolName)
	}
}

// The kill switch, checked at the same layer: --no-skill-activation must leave
// nothing in any node's argv, not merely nothing in the policy struct.
func TestRunAuto_NoSkillActivationArgvIsUnchanged(t *testing.T) {
	runID, spawns := runAutoCapturingArgv(t, "--no-skill-activation")

	for prompt, argv := range spawns {
		if argv.has("--plugin-dir") {
			t.Errorf("%s: --plugin-dir survived --no-skill-activation: %q", prompt, argv)
		}
		if tools := argv.tools(); slices.Contains(tools, coordinator.SkillToolName) {
			t.Errorf("%s: --tools = %v, want no %s", prompt, tools, coordinator.SkillToolName)
		}
	}
	if _, err := os.Stat(filepath.Join(runDirFor(runID), "skills-plugin")); !os.IsNotExist(err) {
		t.Errorf("a staged directory exists with activation off (stat err = %v)", err)
	}
	if len(spawns) == 0 {
		t.Fatal("no node spawned at all, so nothing above was actually checked")
	}
}

// THE RESUMED LEG, at the same layer. `resume` does not carry the first leg's
// argv over: `continueRun` rebuilds the policy map from the snapshot
// (toRunnerToolPolicies drops PluginDirs on purpose) and resumeSkillStaging
// re-establishes the directory from its manifest. That is a SECOND
// construction of the whole mechanism, and until this test it was checked only
// as far as the policy map — the exact "built correctly and never reaches the
// process" gap this file's header names, left open on `resume --retry-failed`,
// which ADR 0017 §6 calls the real resume path for an auto run.
func TestResumeRetryFailed_RespawnsWithThePluginDirAndTheCeiling(t *testing.T) {
	probe := newArgvProbe(t)
	t.Setenv(failPromptEnv, "render the artifact")

	var runErr error
	captureStdout(t, func() {
		runErr = runAutoWith([]string{"turn the issue into a proposal"},
			probe.runner(), browser.NewFakeOpener(), os.Stdout)
	})
	if runErr == nil {
		t.Fatal("precondition failed: the auto run was supposed to fail at the artifact node")
	}
	runID := soleRunID(t)

	// A second leg, recording into its own directory and with the stub's
	// failure switched off, so what it asserts is this leg's spawn.
	probe.freshArgvDir(t)
	t.Setenv(failPromptEnv, "")

	var resumeErr error
	captureStdout(t, func() {
		resumeErr = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), probe.runner(), nil)
	})
	if resumeErr != nil {
		t.Fatalf("resume --retry-failed: %v", resumeErr)
	}

	spawns := probe.spawns(t)
	argv := nodeArgv(t, spawns, "render the artifact")
	wantDir := filepath.Join(runDirFor(runID), "skills-plugin")

	if dir, ok := argv.value("--plugin-dir"); !ok || dir != wantDir {
		t.Errorf("--plugin-dir = %q (present=%t), want %q — a resumed node with no plugin dir runs with no skills and exits 0\nargv: %q", dir, ok, wantDir, argv)
	}
	if tools := argv.tools(); !slices.Contains(tools, coordinator.SkillToolName) {
		t.Errorf("--tools = %v, want %s among them\nargv: %q", tools, coordinator.SkillToolName, argv)
	}
	if sources, ok := argv.value("--setting-sources"); !ok || sources != "" {
		t.Errorf("--setting-sources = %q (present=%t), want a rendered empty value — resume must not widen layer 1\nargv: %q", sources, ok, argv)
	}
	if !argv.has("--strict-mcp-config") {
		t.Errorf("layer 4 is missing from the resumed argv: %q", argv)
	}
	if raw, err := os.ReadFile(filepath.Join(wantDir, "skills", "architecture-design", "SKILL.md")); err != nil {
		t.Errorf("the resumed leg's staged skill is missing: %v", err)
	} else if !strings.Contains(string(raw), "the design procedure") {
		t.Errorf("the resumed leg's staged skill is not the user's file:\n%s", raw)
	}
}

// The kill switch on the resume path, argv-deep: `resume --no-skill-activation`
// is the only way an activation-enabled run can be de-escalated once started,
// so "the policy dropped Skill" is not enough — the re-spawned process must
// carry neither half.
func TestResumeRetryFailed_NoSkillActivationArgvIsUnchanged(t *testing.T) {
	probe := newArgvProbe(t)
	t.Setenv(failPromptEnv, "render the artifact")

	var runErr error
	captureStdout(t, func() {
		runErr = runAutoWith([]string{"turn the issue into a proposal"},
			probe.runner(), browser.NewFakeOpener(), os.Stdout)
	})
	if runErr == nil {
		t.Fatal("precondition failed: the auto run was supposed to fail at the artifact node")
	}
	runID := soleRunID(t)

	probe.freshArgvDir(t)
	t.Setenv(failPromptEnv, "")

	var resumeErr error
	captureStdout(t, func() {
		resumeErr = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed", "--no-skill-activation"}), probe.runner(), nil)
	})
	if resumeErr != nil {
		t.Fatalf("resume --retry-failed --no-skill-activation: %v", resumeErr)
	}

	spawns := probe.spawns(t)
	argv := nodeArgv(t, spawns, "render the artifact")
	if argv.has("--plugin-dir") {
		t.Errorf("--plugin-dir survived --no-skill-activation on resume: %q", argv)
	}
	if tools := argv.tools(); slices.Contains(tools, coordinator.SkillToolName) {
		t.Errorf("--tools = %v, want no %s\nargv: %q", tools, coordinator.SkillToolName, argv)
	}
	if sources, ok := argv.value("--setting-sources"); !ok || sources != "" {
		t.Errorf("--setting-sources = %q (present=%t); de-escalation must not move layer 1 either\nargv: %q", sources, ok, argv)
	}
}

// A sanity check on the probe itself: a test that silently records nothing
// would report every assertion above as passing.
func TestRunAutoCapturingArgv_RecordsEveryPlannedNode(t *testing.T) {
	_, spawns := runAutoCapturingArgv(t)
	if len(spawns) != 4 {
		got := make([]string, 0, len(spawns))
		for p := range spawns {
			got = append(got, p)
		}
		sort.Strings(got)
		t.Fatalf("recorded %d node spawns, want 4: %s", len(spawns), fmt.Sprint(got))
	}
}
