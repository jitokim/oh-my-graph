# Contributing to oh-my-graph

Thanks for considering a contribution. oh-my-graph is a small, opinionated
project — please read [DESIGN.md](DESIGN.md) first. It is the architecture
source of truth: object responsibilities, the execution model, and the MVP
scope boundary all live there. If a PR and DESIGN.md disagree, DESIGN.md wins
until it is updated in the same PR.

## Build, test, smoke

```sh
make build   # go build -o bin/oh-my-graph ./cmd/oh-my-graph
make test    # go test ./... -race -count=1
make vet     # go vet ./...
make fmt     # gofmt -w .
make fmt-check  # gofmt -l . ; fails if anything is unformatted (CI gate)
```

`make build` writes the binary to `./bin/oh-my-graph`; running it in place
works for a one-off check. If you want your everyday `oh-my-graph` command to
track local edits while you iterate, symlink it onto your `PATH` instead of
invoking `./bin/oh-my-graph` directly: `mkdir -p ~/.local/bin && ln -sf
"$PWD/bin/oh-my-graph" ~/.local/bin/oh-my-graph` (assumes `~/.local/bin` is on
`PATH`). Because it's a symlink, every subsequent `make build` updates what's
on `PATH` with no separate install step — this is distinct from the README's
`go install github.com/jitokim/oh-my-graph/cmd/oh-my-graph@latest`, which
fetches a released version from the module proxy, not your local checkout;
the symlink is for contributors actively working on the source.

`make test` runs the entire suite against a scripted `FakeRunner`
(`internal/runner/fake.go`) — no test in the repo spawns a real `claude`
process. That is intentional: the ready-set scheduler, DAG validation,
handoff, retry, and halt-on-fail logic are all exercised through
`map[nodeID]NodeOutcome` fixtures, so CI never needs a `claude` login and
never costs money.

One file is a **sanctioned exception to "spawns nothing"**, and it is still an
exception to nothing above: `cmd/oh-my-graph/skillargv_test.go` drives the real
`CLIRunner` against a temporary `#!/bin/sh` stub it writes itself, so it
can assert the bytes of a node's argv — the one layer a real node obeys, and
the one a `FakeRunner` cannot reach. It never launches `claude`, never touches
the network, and costs nothing; what it does depend on is a POSIX `/bin/sh` and
`mktemp`, which is why the CI job runs on `ubuntu-latest`. A new file that
wants to spawn anything at all needs the same justification: a fact no double
can establish.

**Test doubles.** A double may block on its scripted sync channel or on
`ctx.Done()`, never on a wall-clock fallback; and an assertion must not be
satisfiable by a node or record simply being absent — state presence
explicitly. A wall-clock arm silently degrades choreography into an
unsynchronized race under CI load, and an absence-satisfiable assertion then
passes for the wrong reason: this exact combination produced the project's
only CI flake and survived four review layers before being caught.

```sh
make smoke   # builds the binary, then runs graphs/haiku-smoke.yaml for real
make smoke-codex  # the same graph through a saved Codex login
```

These are the only commands that intentionally spawn a **real** model CLI.
They spend plan allowance and require the selected CLI to be logged in. They
are manual, local-only steps — **never** add either to CI, and don't submit a
PR that wires one into a workflow.

## Branch and PR conventions

- Branch names: `feat/...`, `fix/...`, `chore/...`, `docs/...` — matching the
  change type.
- Commit messages: [Conventional Commits](https://www.conventionalcommits.org/)
  (`feat:`, `fix:`, `docs:`, `chore:`, optionally scoped, e.g.
  `fix(graph): ...`), matching the existing history.
- Keep PRs scoped to one change. If a PR touches the execution model (the
  scheduler, the `NodeRunner` interface, handoff, or the graph schema), update
  DESIGN.md in the same PR — don't let the doc drift from the code.
- New architectural decisions (not just implementation detail) belong in
  `docs/adr/`, following the style of `docs/adr/0001-subprocess-not-sdk.md`.

### Merging

**What `main` enforces**, and it applies to the maintainer too:

| rule | |
|---|---|
| `test` and `stress` must pass | both required; `stress` is the `-race -count=200` repeat over the concurrency-sensitive packages, and it reports green without repeating when the diff touches none of them |
| a CHANGELOG entry, or a stated reason | the `changelog` job fails a PR that changes files without touching `CHANGELOG.md`; put `no-changelog` in the PR body to skip it |
| the branch must be up to date with `main` | so the checks that passed are the checks for the merged tree |
| every conversation resolved | a review comment cannot be merged past by ignoring it |
| **administrators included** | there is no bypass, for anyone |

**There is deliberately no required-approval count**, and the reason is worth
stating because removing a rule usually means loosening one. This repo had one,
and it was unsatisfiable: GitHub forbids approving your own pull request, the
maintainer is currently the only person who merges, and the automated reviewer
rate-limits for days at a time. So the rule was not met — it was bypassed, six
times in one day, with `--admin`.

That was strictly worse than not having it. **`--admin` does not bypass the
review; it bypasses everything**, tests included, and a rule reached for with a
bypass every time teaches the bypass rather than the rule. Trading it for
administrator enforcement makes `main` harder to merge into than it was before,
not easier: nothing now reaches `main` without green checks, including a
maintainer in a hurry.

**Review is still required — as work, not as a checkbox:**

- Every PR gets a deep review before merge. In practice that is a review node in
  the lane that produced it (`graphs/backlog-batch.yaml` and friends gate their
  own lane on it) or a review run against the branch; either way the findings
  are **posted in the PR thread**, so what was examined is on the record.
- Every finding is applied or answered with a reason. Mechanical fixes may be
  applied by hand within the one-to-two-line threshold; anything more goes
  through `graphs/apply-flags.yaml`.
- CodeRabbit reviews when it can, and its findings are triaged the same way. It
  is **not** a gate: it skips drafts, skips PRs above its file limit, and
  rate-limits — so treating it as the gate is how a repo ends up unable to merge
  at all.
- An outside contribution is reviewed and approved by the maintainer, as
  [#170](https://github.com/jitokim/oh-my-graph/pull/170) was. That is the
  practice; it is simply no longer a rule that can deadlock the repository when
  the only available reviewer is the author.

Graph lanes open PRs as **drafts** — CodeRabbit skips drafts, so marking the PR
ready is what invites it.

### Attribution

Commits authored by a graph lane — a claude node running one of the shipped
`graphs/*.yaml` pipelines against this repo — end with the trailer

```text
Co-Authored-By: oh-my-graph <graphs@oh-my-graph.dev>
```

This is a transparency convention, not a GitHub account: the address receives
no mail and resolves to no user. It exists so the dogfooding claim in the
README ("It ships itself") stays verifiable —
`git log --grep="Co-Authored-By: oh-my-graph"` lists the commits that declare
it. Every commit-producing node in the shipped `graphs/*.yaml` prompts for
the trailer; like any trailer it is a convention, not cryptographic proof of
authorship.

**Auditing the claim.** The trailer above names the *pipeline*, and only from
2026-08-02 (KST) on. The trailer that names the *model* is Claude's own, it predates
that convention, and it is the one to count:

```sh
git log main --first-parent -i --grep="co-authored-by: claude"
```

The verifiable snapshot, taken 2026-08-06 (KST) and only going up: **49 of the 114
pull requests** merged into `main` carry a Claude co-author trailer in their
squash commit — the receipt that a claude session wrote them. The command above
reported **50** matches at that snapshot, because `--first-parent` also reaches
the initial commit, which is not a merged pull request. Don't take the
snapshot's word for it; count today's number yourself.

## The exec seams — the one rule that matters most

**Exactly four objects in this codebase may spawn a process**, each behind
its own injected interface:

| object | runs | interface |
|---|---|---|
| `internal/runner.CLIRunner` | a node's model CLI subprocess (`claude -p` or `codex exec`) | `runner.NodeRunner` |
| `internal/verify.ShellVerifier` | a node's `success_check.verify` command | `verify.Verifier` |
| `internal/worktree.GitManager` | the `git worktree` commands behind a node's `worktree:` | `worktree.Provider` |
| `internal/browser.ExecOpener` | the `open`/`xdg-open` launch of the `serve` URL | `browser.Opener` |

```go
type NodeRunner interface {
	Run(ctx context.Context, spec NodeInvocation) (NodeOutcome, error)
}

type Verifier interface {
	Verify(ctx context.Context, req Request) (Result, error)
}

type Provider interface {
	Acquire(ctx context.Context, name string) (string, error)
}

type Opener interface {
	Open(ctx context.Context, url string) error
}
```

Every other package — the scheduler, the CLI, handoff, ledger, coordinator —
depends only on those interfaces. That is what keeps the entire engine testable
via `FakeRunner`, `FakeVerifier`, `worktree.FakeManager` and
`browser.FakeOpener` with zero real spawns, and it keeps the
subscription-auth env scrub (`internal/childenv`) to exactly one call site
per spawner.

If your change needs to run a subprocess, it belongs behind one of the four
existing seams. A PR that spawns a process (via `os/exec` or any other way of
shelling out) outside `runner.CLIRunner`, `verify.ShellVerifier`,
`worktree.GitManager` and `browser.ExecOpener` should be treated as a design
regression, not a normal review comment — a **fifth** spawner needs an ADR
first, the way the second, third and fourth got
[ADR 0002](docs/adr/0002-verification-is-a-second-exec-seam.md),
[ADR 0005](docs/adr/0005-worktree-provisioning-is-a-third-exec-seam.md) and
[ADR 0006](docs/adr/0006-browser-open-is-a-fourth-exec-seam.md). (The invariant
is about *objects that spawn*, not `os/exec` imports per se: the runner and
verify seam packages also carry build-tagged process-group helpers that import
`os/exec` solely to kill a cancelled child's tree — they never start one.)

## Invariants a contribution must preserve

These are the load-bearing guarantees the whole project exists to make (see
[SECURITY.md](SECURITY.md) for the full stance). A PR that weakens any of them
without an explicit, discussed design change should not be merged:

- **Subscription-auth env scrub.** Every child process oh-my-graph spawns —
  a model node, a `success_check.verify` command, the git commands behind a
  node's `worktree:`, AND the `open`/`xdg-open` launch of the `serve` URL —
  has `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `OPENAI_API_KEY` and
  `CODEX_API_KEY` deleted from its
  environment by the shared `internal/childenv.Scrub`. There is one list and
  no runtime branch — a Claude node drops the OpenAI switches and a Codex node
  drops the Anthropic ones — because a per-runtime list is one provider
  variable away from billing silently. Each of those variables can make the
  selected CLI ignore its saved login and authenticate by API key instead —
  which is metered — and that is the defensible form of the claim
  (`internal/childenv/childenv.go` states it the same way). It is stated per
  variable rather than proven per variable: `ANTHROPIC_API_KEY` and
  `ANTHROPIC_AUTH_TOKEN` are documented `claude` behaviour, while
  `OPENAI_API_KEY` and `CODEX_API_KEY` are scrubbed on the same reasoning
  applied to the other CLI — nothing in this repo establishes which of the two
  `codex` actually reads, and the cost of being wrong is asymmetric. Scrub
  first, measure later; don't upgrade the wording to a proven claim without the
  measurement.
  `verify: { command: "claude -p ..." }` is a legal thing to write, a repo's
  own git hooks may invoke claude too, and the URL handler `open` dispatches
  to is arbitrary user-configured code — so every one of the four spawners
  must apply it. It is asserted in `internal/childenv/childenv_test.go` (the
  policy) and at each call site (`internal/runner/claude_test.go`,
  `internal/verify/shell_test.go`, `internal/worktree/git_test.go`,
  `internal/browser/exec_test.go`) — if you touch env construction, make sure
  those tests still prove the scrub.
- **Artifact handoff is the default.** `handoff: session` is opt-in and valid
  only with exactly one session-parent; don't make session handoff a default or
  an implicit path.
- **Never a provider SDK.** A node runs as the provider's own CLI subprocess —
  `claude -p ... --output-format json` or `codex exec --json`, one runtime per
  run (ADR 0025) — on that provider's saved login. Don't introduce an
  Anthropic/OpenAI API or Agent SDK dependency as an alternate or default
  runtime path; a new runtime means a new CLI protocol under `CLIRunner`, not
  a new way to reach a model.
- **Never `--bare`.** That flag disables OAuth and would break subscription
  auth. Don't add it to the built argv.
- **Never `--no-session-persistence`.** Nodes run with session persistence on,
  so every node stays observable as an ordinary session transcript of whichever
  CLI ran it (`~/.claude/projects` for claude).
  Don't add a flag or option that turns it off by default.
- **Every field on `graph.Node` has an explicit auto-mode disposition.** A
  planner reply is untrusted input, so `coordinator.validatePlannedNodes` must
  allow, constrain, or reject every field a plan can set — this class of hole
  recurs each time the schema grows. It is enforced, not just requested:
  `internal/coordinator/field_dispositions_test.go` walks `graph.Node` by
  reflection and fails on any field with no recorded disposition. **If you add
  a field to `Node`, that test will fail until you decide what auto mode does
  with it.** Recording it as `rejected` also requires a probe proving the
  rejection actually fires.
- **The auto-mode tool ceiling is auto mode's alone.** Hand-written graphs
  (`oh-my-graph run`) must keep running under the user's own settings, hooks
  and MCP servers — they are the user's reviewed artifact, and that path exists
  for precise control. Don't extend `--setting-sources ""`, `--tools`,
  `--strict-mcp-config` or `--disallowedTools` to it.
- **Don't narrow a published security claim ahead of a measurement.** The
  scoped-`Bash` wording in README/SECURITY.md was only softened after a real
  `claude` invocation demonstrated the behaviour (DESIGN.md, "Empirical
  verification of the tool ceiling"). `--help` prose has already been wrong
  once. If you can't measure it, say it's unverified — an honest partial
  mechanism beats an overclaimed complete one.

## Releasing

Maintainer checklist for cutting a release:

- **Version bump lands with the CHANGELOG heading.** Bump
  `cmd/oh-my-graph/version.go` and add the `## [x.y.z]` entry to
  `CHANGELOG.md` in the same commit — CI has guarded this pairing since
  v0.3.0 (`TestVersionMatchesChangelog` fails if they drift).
- **Bump the plugin manifests to the same version.**
  `plugin/.claude-plugin/plugin.json` and `.claude-plugin/marketplace.json`
  move together with the release. This one IS guarded now
  (`TestPluginManifestsMatchVersion`) — it used to say the checklist was the
  only thing guarding it, and then v0.8.0 shipped with both manifests still
  reading 0.7.0. A checklist item that depends on someone reading it is a note
  about a guard nobody wrote.
- **Sync the Korean README.** A release must not ship a stale translation:
  fold any `README.md` changes since the last release into `README.ko.md`
  (English is the source of truth; the ko file carries the precedence
  notice). Nothing in CI guards this — the checklist does.
- **Write the release's CHANGELOG section as prose.** The release body IS that
  section — `scripts/release-notes.sh` extracts it and the workflow fails the
  release if it is missing, so there is no auto-generated fallback to fall back
  on. `TestChangelogSectionHasSubstance` catches an empty one in the PR, where
  it is still cheap; a tag is public the moment it lands. The Contributors line
  is computed from `git log`, so it needs nothing from you.
- **`make smoke` and `make smoke-codex` before tagging.** Run both real-CLI
  smokes locally as the last gate — they are the only checks that spawn a real
  model CLI, and neither runs in CI.
- **One scoped deep meta-review per release, on a rotating subject** (tests →
  docs → security). Pick the release's subject and ask one targeted question
  about that area's blind spots, rather than adding another generic review
  pass — the v0.3.0 flake was found by asking a targeted question about
  test-double synchronization, after four generic review layers had missed it.

## Scope

Before proposing a feature, check DESIGN.md's "MVP scope (v0.1)" section (its
IN and DEFERRED lists) and
[docs/LIMITATIONS.md](docs/LIMITATIONS.md#deferred-not-implemented).
Things like a graph DSL beyond `depends_on` and a terminal TUI are deliberately
out of scope — that's not an oversight, it's a documented boundary. If you want
to build one of those, open an issue to discuss the design first rather than
sending a large PR.

## Questions

Open an issue. This is a young project — there's no separate chat/forum yet.
