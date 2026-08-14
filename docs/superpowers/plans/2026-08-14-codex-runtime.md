# Codex Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let one oh-my-graph invocation run every model call through either the existing Claude Code CLI or Codex CLI, with durable resume identity and honest accounting.

**Architecture:** Keep `NodeRunner` as the scheduler boundary and replace the Claude-specific process object with one `CLIRunner` composed with a Claude or Codex protocol. Parse the runtime once at the CLI boundary, persist it in `state.json`, and let resume/serve reconstruct the same runner from that state.

**Tech Stack:** Go 1.25, standard `flag`/`os/exec`/`encoding/json`, YAML graph schema, JSONL run feed, embedded HTML/JavaScript live view.

**Spec:** `docs/adr/0025-one-run-one-cli-runtime.md`

## Global Constraints

- Default runtime is exactly `claude`; selecting Codex is `oh-my-graph --runtime codex <command>`.
- One run uses one runtime, including planner, assessor, nodes, retries, resume, and browser gate resume.
- Keep exactly four `os/exec` seams; the runner remains one seam.
- Claude argv and existing snapshot compatibility stay unchanged.
- Codex never renders unknown USD spend as zero.
- Codex rejects `agent:`, positive `budget_usd`, and `--max-goal-budget-usd` instead of dropping them.
- Apply Delete → Replace → Add: remove Claude-owned scheduling and scattered constructors before adding runtime branches.

---

### Task 1: Runtime value and CLI selection

**Files:**
- Create: `internal/runner/runtime.go`
- Modify: `cmd/oh-my-graph/main.go`
- Modify: `cmd/oh-my-graph/lint.go`
- Test: `internal/runner/runtime_test.go`
- Test: `cmd/oh-my-graph/usage_test.go`

**Interfaces:**
- Produces: `runner.Runtime`, `runner.RuntimeClaude`, `runner.RuntimeCodex`, `runner.ParseRuntime(string) (Runtime, error)`.
- Produces: a top-level parser returning `(runner.Runtime, commandArgs, error)` before dispatch.

- [ ] **Step 1: Write the failing runtime parsing and top-level CLI tests**

```go
func TestParseRuntime(t *testing.T) {
    for _, tc := range []struct{ in string; want Runtime }{
        {"", RuntimeClaude}, {"claude", RuntimeClaude}, {"codex", RuntimeCodex},
    } {
        got, err := ParseRuntime(tc.in)
        if err != nil || got != tc.want { t.Fatalf("ParseRuntime(%q) = %q, %v", tc.in, got, err) }
    }
}
```

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./internal/runner ./cmd/oh-my-graph -run 'Runtime|Usage'`
Expected: FAIL because `Runtime`, `ParseRuntime`, and the global flag parser do not exist.

- [ ] **Step 3: Implement the runtime value and parse `--runtime` only before the subcommand**

```go
type Runtime string
const (
    RuntimeClaude Runtime = "claude"
    RuntimeCodex Runtime = "codex"
)
```

The parser accepts `--runtime codex` and `--runtime=codex`, defaults to Claude,
rejects duplicates/unknown values, and passes the remaining argv to dispatch.

- [ ] **Step 4: Run focused tests and verify GREEN**

Run: `go test ./internal/runner ./cmd/oh-my-graph -run 'Runtime|Usage'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runner/runtime.go internal/runner/runtime_test.go cmd/oh-my-graph/main.go cmd/oh-my-graph/lint.go cmd/oh-my-graph/usage_test.go
git commit -m "feat(cli): select a graph runtime"
```

### Task 2: Replace the Claude process object with runtime protocols

**Files:**
- Delete: `internal/runner/claude.go`
- Delete: `internal/runner/session.go`
- Create: `internal/runner/cli.go`
- Create: `internal/runner/claude_protocol.go`
- Create: `internal/runner/codex_protocol.go`
- Modify: `internal/runner/runner.go`
- Replace tests: `internal/runner/claude_test.go` with `internal/runner/cli_test.go`, `internal/runner/claude_protocol_test.go`, and `internal/runner/codex_protocol_test.go`
- Modify: `internal/invariants/exec_seam_test.go`
- Modify: `internal/childenv/childenv.go`
- Test: `internal/childenv/childenv_test.go`

**Interfaces:**
- Produces: `runner.NewCLIRunner(runtime Runtime, opts ...CLIOption) *CLIRunner`.
- Produces: `NodeInvocation.SessionStarted func(string)`.
- Produces: `TokenUsage{InputTokens, CachedInputTokens, OutputTokens, ReasoningOutputTokens int64}` and `NodeOutcome.CostUnknown`/`Usage`.

- [ ] **Step 1: Write failing Codex argv, JSONL, session, usage, and env tests**

```go
func TestCodexProtocolParsesJSONL(t *testing.T) {
    raw := strings.Join([]string{
        `{"type":"thread.started","thread_id":"abc"}`,
        `{"type":"item.completed","item":{"type":"agent_message","text":"PASS"}}`,
        `{"type":"turn.completed","usage":{"input_tokens":11,"cached_input_tokens":3,"output_tokens":2,"reasoning_output_tokens":1}}`,
    }, "\n")
    got, err := parseCodexJSONL([]byte(raw), nil)
    if err != nil || got.SessionID != "abc" || got.Result != "PASS" || !got.CostUnknown || got.Usage.InputTokens != 11 { t.Fatalf("got %+v, %v", got, err) }
}
```

Assert fresh argv uses `codex exec ... --json`; resumed argv places runtime
options before `resume <id> <prompt>`; auto isolation includes the four fixed
exclusions; sandbox mapping follows the ADR; both OpenAI key names are scrubbed.

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./internal/runner ./internal/childenv ./internal/invariants`
Expected: FAIL because the generic runner/protocol and Codex parser are absent.

- [ ] **Step 3: Implement the generic runner and protocols, then delete Claude-owned scheduling**

```go
type cliProtocol interface {
    name() string
    binary() string
    args(NodeInvocation) ([]string, error)
    parse(stdout, stderr []byte, sessionStarted func(string)) (NodeOutcome, error)
}
```

`CLIRunner.Run` alone owns timeout, process groups, environment scrub, exit
codes, and protocol parsing. Claude protocol preserves the old JSON envelope
and UUID behavior. Codex protocol reads every JSONL event, remembers the last
agent message, reports `turn.failed`, and requires `thread.started` plus a
terminal turn event.

- [ ] **Step 4: Run focused tests and verify GREEN**

Run: `go test ./internal/runner ./internal/childenv ./internal/invariants`
Expected: PASS with the invariant naming `internal/runner/cli.go` as seam one.

- [ ] **Step 5: Commit**

```bash
git add internal/runner internal/childenv internal/invariants
git commit -m "feat(runner): execute Claude or Codex protocols"
```

### Task 3: Move session ownership and add runtime preflight

**Files:**
- Modify: `internal/schedule/scheduler.go`
- Modify: `internal/runfeed/runfeed.go`
- Modify: `internal/graph/graph.go`
- Create: `internal/runner/preflight.go`
- Test: `internal/schedule/scheduler_test.go`
- Test: `internal/runner/preflight_test.go`

**Interfaces:**
- Produces: `runner.ValidateGraphForRuntime(runtime Runtime, graph *graph.Graph) error`.
- Reuses `node_started` / `node_retried`, emitted when the runtime owns the
  session id.

- [ ] **Step 1: Write failing ownership and unsupported-field tests**

```go
func TestValidateGraphCodexRejectsClaudeOnlyFields(t *testing.T) {
    g := &graph.Graph{Nodes: []graph.Node{{ID:"n", BudgetUSD:0.5}}}
    if err := ValidateGraph(RuntimeCodex, g); err == nil || !strings.Contains(err.Error(), "budget_usd") { t.Fatalf("got %v", err) }
}
```

Add a scheduler test whose fake invokes `spec.SessionStarted("thread-1")` and
asserts the existing start event appears before the terminal event.

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./internal/runner ./internal/schedule ./internal/runfeed`
Expected: FAIL because preflight and session event do not exist.

- [ ] **Step 3: Remove scheduler UUID minting and connect the callback**

`runNode` emits no start event before the runtime owns a session. Immediately
before each `Run`, it assigns a callback that emits the existing `node_started`
or `node_retried` event. Retries reset the callback with their retry number.
`prepareRetry` only clears resume state and rebuilds the prompt.

- [ ] **Step 4: Run focused tests and verify GREEN**

Run: `go test ./internal/runner ./internal/schedule ./internal/runfeed`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runner/preflight.go internal/runner/preflight_test.go internal/schedule internal/runfeed internal/graph/graph.go
git commit -m "refactor(schedule): let runtimes own sessions"
```

### Task 4: Persist runtime and propagate unknown cost/token usage

**Files:**
- Modify: `internal/runstate/runstate.go`
- Modify: `internal/runstate/recorder.go`
- Modify: `internal/ledger/ledger.go`
- Modify: `internal/runfeed/runfeed.go`
- Modify: `internal/schedule/scheduler.go`
- Modify: `cmd/oh-my-graph/show.go`
- Modify: `cmd/oh-my-graph/runs.go`
- Modify: `cmd/oh-my-graph/watch.go`
- Modify: `internal/serve/card.go`
- Modify: `internal/serve/ui/app.js`
- Modify: `internal/serve/ui/dashboard.js`
- Tests: matching `_test.go` files in those packages

**Interfaces:**
- Produces: snapshot `runtime`, `cost_unknown`, and `usage` fields under
  state/feed schema 3; an empty in-memory runtime canonicalizes to Claude.
- Produces: ledger rendering `unknown` plus a known subtotal and token totals.

- [ ] **Step 1: Write failing round-trip and rendering tests**

```go
func TestRenderUnknownCostIsNeverZeroDollars(t *testing.T) {
    led := New("r")
    led.Record(Record{NodeID:"n", Verdict:VerdictPass, CostUnknown:true, Usage:TokenUsage{InputTokens:11}})
    got := led.Render()
    if strings.Contains(got, "$0.0000") || !strings.Contains(got, "unknown") || !strings.Contains(got, "11 input") { t.Fatal(got) }
}
```

Round-trip a Codex snapshot and assert `show`, `runs list`, watch events, and
serve card JSON preserve unknown cost and tokens. Round-trip a legacy snapshot
with `runtime: "claude"` and assert it selects Claude.

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./internal/runstate ./internal/ledger ./internal/runfeed ./internal/serve ./cmd/oh-my-graph`
Expected: FAIL because the persisted and rendered fields are absent.

- [ ] **Step 3: Implement additive persistence and provider-neutral rendering**

Use `CostUnknown bool` rather than a sentinel float. Sum known USD separately;
if any row is unknown, label the total unknown and show the known subtotal.
Accumulate token counts structurally, including feedback rounds and resumes.

- [ ] **Step 4: Run focused tests and verify GREEN**

Run: `go test ./internal/runstate ./internal/ledger ./internal/runfeed ./internal/serve ./cmd/oh-my-graph`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runstate internal/ledger internal/runfeed internal/schedule internal/serve cmd/oh-my-graph
git commit -m "feat(accounting): report Codex token usage honestly"
```

### Task 5: Wire fresh runs, resume, serve, auto, and chat

**Files:**
- Modify: `cmd/oh-my-graph/main.go`
- Modify: `cmd/oh-my-graph/flags.go`
- Modify: `cmd/oh-my-graph/chat.go`
- Modify: `cmd/oh-my-graph/resume.go`
- Modify: `cmd/oh-my-graph/serve.go`
- Modify: `cmd/oh-my-graph/gateresume.go`
- Modify: `cmd/oh-my-graph/goal.go`
- Modify: `internal/coordinator/coordinator.go`
- Tests: `cmd/oh-my-graph/wiring_test.go`, `resume_test.go`, `serve_test.go`, `gateresume_test.go`, `flags_test.go`

**Interfaces:**
- Consumes: `runner.NewCLIRunner`, `runner.ValidateGraph`, persisted `Snapshot.Runtime`.
- Produces: runtime-aware constructors at the CLI boundary only.

- [ ] **Step 1: Write failing wiring tests**

Assert a Codex fresh run writes `"runtime":"codex"`; a resume factory receives
Codex from state; a live-view gate resume does the same; unknown/blank state
selects Claude; Codex auto disables Claude agent/skill scans; Codex rejects
`--max-goal-budget-usd` before the planner fake is called.

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./cmd/oh-my-graph ./internal/coordinator -run 'Runtime|Codex|Resume|Gate'`
Expected: FAIL because every production call site still constructs Claude.

- [ ] **Step 3: Replace scattered constructors with runtime injection**

Fresh commands construct one runner from the parsed runtime. Resume loads the
snapshot before constructing a production runner; test-provided runners remain
injectable. `cliGateResumer` carries a runner factory, not a Claude instance.
Codex coordinator options omit Claude agent/skill directories. Preflight runs
after graph load/plan and before `executeGraph` writes or spawns nodes.

- [ ] **Step 4: Run focused tests and verify GREEN**

Run: `go test ./cmd/oh-my-graph ./internal/coordinator`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/oh-my-graph internal/coordinator
git commit -m "feat(cli): wire Codex through every run lifecycle"
```

### Task 6: Documentation and end-to-end verification

**Files:**
- Modify: `README.md`
- Modify: `README.ko.md`
- Modify: `DESIGN.md`
- Modify: `SECURITY.md`
- Modify: `docs/INSTALL.md`
- Modify: `docs/LIMITATIONS.md`
- Modify: `docs/adr/0001-subprocess-not-sdk.md`
- Modify: `docs/adr/0006-browser-open-is-a-fourth-exec-seam.md`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Documents: setup with `claude login` or `codex login`, global runtime syntax, sandbox mapping, unsupported fields, resume behavior, and unknown USD/token display.

- [ ] **Step 1: Update user and architecture documentation**

Add runnable examples:

```bash
oh-my-graph --runtime codex run graph.yaml
oh-my-graph --runtime codex auto "review this repository"
oh-my-graph resume 20260814-120000 --retry-failed
```

- [ ] **Step 2: Run formatting and the complete automated suite**

Run: `gofmt -w <all changed Go files> && go test ./... && git diff --check`
Expected: every package passes and the diff has no whitespace errors.

- [ ] **Step 3: Build through the repository's release path**

Run: `make local`
Expected: the local binary builds successfully.

- [ ] **Step 4: Run real Claude compatibility and Codex smoke tests**

Run the built binary with a one-node temporary graph whose prompt asks for a
haiku and whose success check matches a stable marker. Use `--runtime codex`,
then inspect `state.json`, `show`, and a retry/resume-capable session id. Run
the same graph without `--runtime` against Claude when the saved login is
available; otherwise retain the automated exact-argv compatibility evidence.

- [ ] **Step 5: Commit**

```bash
git add README.md README.ko.md DESIGN.md SECURITY.md docs CHANGELOG.md
git commit -m "docs: explain Claude and Codex runtimes"
```

### Task 7: Review, push, and PR

**Files:**
- Review: every diff from `origin/main...HEAD`

**Interfaces:**
- Produces: a pushed `feat/codex-runtime` branch and GitHub pull request.

- [ ] **Step 1: Run final verification from a clean tree**

Run: `go test ./... && make local && git diff --check && git status --short`
Expected: tests/build pass; only intended committed files exist.

- [ ] **Step 2: Review the complete diff for the approved design**

Run: `git diff --stat origin/main...HEAD && git diff origin/main...HEAD`
Expected: no second exec seam, no Claude fallback for Codex-only fields, and no
Codex `$0.0000` rendering.

- [ ] **Step 3: Push the feature branch**

Run: `git push -u origin feat/codex-runtime`
Expected: remote branch created or updated.

- [ ] **Step 4: Create the pull request**

Run: `gh pr create --base main --head feat/codex-runtime --title "feat: add Codex CLI runtime" --body-file <prepared-body>`
Expected: GitHub returns the PR URL.

- [ ] **Step 5: Report the PR and verification evidence**

Include the URL, test/build results, real runtime smoke result, and any
provider limitation that remains.
