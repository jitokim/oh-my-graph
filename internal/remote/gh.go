// Package remote reads the part of a run's remote snapshot (ADR 0046 §2.1)
// that git cannot: the invocation repository's open pull requests, through
// `gh`.
//
// GHRunner is the fifth exec seam (ADR 0046 §2.3), beside runner.CLIRunner,
// verify.ShellVerifier, worktree.GitManager and browser.ExecOpener. It runs
// one fixed, read-only `gh pr list` and nothing wider; no model text reaches
// its argv. Everything else depends on the PRLister interface, so code that
// takes a snapshot is tested against FakePRLister with no real spawn.
package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/jitokim/oh-my-graph/internal/childenv"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/worktree"
)

// ghTimeout bounds the one gh command, matching worktree's gitTimeout: `gh pr
// list` reaches the network, and a wedged gh must not hold a snapshot — and
// with it the start or end of a run leg — indefinitely.
const ghTimeout = 30 * time.Second

// ghWaitDelay is how long gh's output pipes may stay open after gh was
// killed — see ghCmd.
const ghWaitDelay = time.Second

// prListLimit is the --limit the PR list is read with (ADR 0046 §2.1). `gh pr
// list` returns 30 PRs unless told otherwise, so two snapshots of a busy
// repository could list different PRs that never changed. A result holding
// exactly this many may have been cut short, and is ReasonTruncated, never
// compared.
const prListLimit = 1000

// ghPRListArgs is the whole argv: fixed by the engine, built from nothing a
// node or a model wrote.
var ghPRListArgs = []string{
	"pr", "list",
	"--state", "open",
	"--limit", strconv.Itoa(prListLimit),
	"--json", "number,headRefName,headRefOid",
}

// The reasons an open-PR list is unavailable. They are short and stable on
// purpose: they are recorded in state.json and compared by the snapshot code,
// so gh's own wording — which varies by version — goes in PRList.Detail
// instead.
const (
	// ReasonGHNotFound: no gh executable on PATH.
	ReasonGHNotFound = "gh not found"
	// ReasonNotAuthenticated: gh has no login for the repository's host
	// (gh's exit status 4, or its "gh auth login" advice).
	ReasonNotAuthenticated = "not authenticated"
	// ReasonNonZeroExit: gh failed for any other reason — not a GitHub
	// repository, no network, an API error.
	ReasonNonZeroExit = "non-zero exit"
	// ReasonTimeout: gh did not finish within the timeout.
	ReasonTimeout = "timeout"
	// ReasonCanceled: the caller's context ended before gh finished.
	ReasonCanceled = "canceled"
	// ReasonUnparsable: gh exited 0 but its output was not the JSON list the
	// argv asked for.
	ReasonUnparsable = "unparsable output"
	// ReasonTruncated: the list held exactly prListLimit PRs, so more may
	// exist.
	ReasonTruncated = "truncated"
)

// ghAuthExitCode is the exit status gh documents for "authentication
// required".
const ghAuthExitCode = 4

// OpenPR is one open pull request whose base is the invocation repository's
// GitHub repo. HeadRefOid is the head commit's sha: a PR is "updated" when it
// moves, never when updatedAt does (ADR 0046 §2.2).
type OpenPR struct {
	Number      int    `json:"number"`
	HeadRefName string `json:"headRefName"`
	HeadRefOid  string `json:"headRefOid"`
}

// PRList is one read of the open PRs. Status is observed — PRs is the whole
// list, possibly empty — or unavailable, with Reason one of the Reason
// constants and PRs nil. An unavailable list is never an empty one (ADR 0046
// §2.1): it is not compared.
type PRList struct {
	Status runstate.RemotePartStatus
	Reason string
	// Detail is gh's own diagnosis of an unavailable read, one bounded line
	// with any credential redacted; empty when observed or when there is
	// nothing to add to Reason.
	Detail string
	PRs    []OpenPR
}

// Observed reports whether l is a list the snapshot may compare.
func (l PRList) Observed() bool { return l.Status == runstate.RemotePartObserved }

// ObservedPRs is an observed list holding prs (never nil, so an observed
// empty list stays distinguishable from an unavailable one).
func ObservedPRs(prs []OpenPR) PRList {
	return PRList{Status: runstate.RemotePartObserved, PRs: append([]OpenPR{}, prs...)}
}

// UnavailablePRs is an unavailable list with reason and detail; detail is
// redacted and bounded here, so no caller can record it raw.
func UnavailablePRs(reason, detail string) PRList {
	return PRList{Status: runstate.RemotePartUnavailable, Reason: reason, Detail: worktree.SummarizeReason(detail)}
}

// PRLister is what the remote snapshot reads the open PRs through. GHRunner
// is the production implementation; FakePRLister is what tests inject.
//
// It never returns an error: every failure is an unavailable PRList, and no
// caller may treat one as a reason to stop the run.
type PRLister interface {
	ListOpenPRs(ctx context.Context) PRList
}

// GHRunner is the production PRLister and the fifth exec seam: the only
// object in this package that spawns a process, and one of exactly five in
// the project (the others: runner.CLIRunner, verify.ShellVerifier,
// worktree.GitManager and browser.ExecOpener) — see ADR 0046 §2.3.
//
// Construct it in cmd/oh-my-graph and inject it; nothing else builds one, so
// no test picks up a real spawn by accident.
type GHRunner struct {
	// repoDir is where gh runs — the invocation repository, so the list is
	// the PRs whose base is its GitHub repo. Empty means the process's own
	// working directory.
	repoDir string
	// environ supplies the parent environment to scrub. A field (defaulting
	// to os.Environ) for the same reason as GitManager's.
	environ func() []string
	// timeout bounds the gh command. A field (defaulting to ghTimeout) so a
	// test can trip the deadline in milliseconds instead of 30 seconds.
	timeout time.Duration
}

// NewGHRunner builds the production PRLister for the repository at repoDir
// ("" = the process's working directory).
func NewGHRunner(repoDir string) *GHRunner {
	return &GHRunner{repoDir: repoDir, environ: os.Environ, timeout: ghTimeout}
}

var _ PRLister = (*GHRunner)(nil)

// ListOpenPRs runs the fixed `gh pr list` under the timeout and classifies
// the outcome. gh's JSON is on stdout; its diagnosis on stderr is kept, as
// Detail, only when the read failed.
func (r *GHRunner) ListOpenPRs(ctx context.Context) PRList {
	cmdCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := r.ghCmd(cmdCtx)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		var exitErr *exec.ExitError
		switch {
		case errors.Is(err, exec.ErrNotFound):
			return UnavailablePRs(ReasonGHNotFound, "")
		case ctx.Err() != nil:
			return UnavailablePRs(ReasonCanceled, "")
		case errors.Is(cmdCtx.Err(), context.DeadlineExceeded):
			return UnavailablePRs(ReasonTimeout, "gh pr list timed out after "+r.timeout.String())
		case errors.As(err, &exitErr) && (exitErr.ExitCode() == ghAuthExitCode || strings.Contains(detail, "gh auth login")):
			return UnavailablePRs(ReasonNotAuthenticated, detail)
		case errors.As(err, &exitErr):
			return UnavailablePRs(ReasonNonZeroExit, "exit status "+strconv.Itoa(exitErr.ExitCode())+": "+detail)
		default:
			return UnavailablePRs(ReasonNonZeroExit, err.Error())
		}
	}
	return parsePRList(stdout.Bytes())
}

// parsePRList reads gh's JSON array. Anything else — empty output, a JSON
// object, a PR with no number — is unparsable rather than a guess at an
// empty list.
func parsePRList(out []byte) PRList {
	var prs []OpenPR
	if err := json.Unmarshal(bytes.TrimSpace(out), &prs); err != nil {
		return UnavailablePRs(ReasonUnparsable, err.Error())
	}
	if prs == nil {
		return UnavailablePRs(ReasonUnparsable, "not a JSON list")
	}
	for _, pr := range prs {
		if pr.Number <= 0 || pr.HeadRefOid == "" {
			return UnavailablePRs(ReasonUnparsable, "a pull request has no number or head sha")
		}
	}
	if len(prs) >= prListLimit {
		return UnavailablePRs(ReasonTruncated, "gh returned "+strconv.Itoa(len(prs))+" open pull requests, the --limit")
	}
	return ObservedPRs(prs)
}

// ghCmd assembles the exact *exec.Cmd for the gh invocation: the fixed argv,
// the repository to run against, and the scrubbed child environment. It is
// the unit under test — gh_test.go asserts all four scrubbed variables
// (ANTHROPIC_API_KEY, ANTHROPIC_AUTH_TOKEN, OPENAI_API_KEY, CODEX_API_KEY)
// are absent from the child. Nothing here spawns; ListOpenPRs wires it to the
// OS.
//
// The scrub leaves GH_TOKEN, so gh authenticates as the operator.
// GH_PROMPT_DISABLED=1 is appended after it: gh must fail at once rather than
// ask anything on a terminal and hold the snapshot until the timeout.
//
// WaitDelay makes the timeout hold: killing gh does not kill what gh spawned
// (a credential helper, git), and a grandchild still holding an output pipe
// would otherwise keep Run waiting for as long as it lives.
func (r *GHRunner) ghCmd(ctx context.Context) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "gh", ghPRListArgs...)
	cmd.Dir = r.repoDir
	cmd.Env = childenv.Scrub(r.environ())
	cmd.Env = append(cmd.Env, "GH_PROMPT_DISABLED=1")
	cmd.WaitDelay = ghWaitDelay
	return cmd
}
