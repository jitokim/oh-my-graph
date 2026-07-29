// Package schedule drives the DAG: it walks the graph in dependency order,
// runs the ready set concurrently under a cap, and enforces the run policy
// (success checks, flat retry, halt-on-fail). It owns coordination only — it
// asks the injected NodeRunner to execute a node, the Handoff to resolve inputs
// and persist outputs, and the RunLedger to record results. It never spawns a
// process itself and never learns whether a real claude ran, which is exactly
// what lets the whole engine be tested against a FakeRunner.
package schedule

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/jitokim/oh-my-graph/internal/gate"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/handoff"
	"github.com/jitokim/oh-my-graph/internal/ledger"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// Concurrency bounds: the default ready-set width and the hard ceiling no graph
// may exceed. The default permission mode is unattended.
const (
	defaultConcurrency    = 4
	globalConcurrencyCap  = 10
	defaultPermissionMode = "dontAsk"
)

// Options configures a Scheduler at construction. Zero values are the safe
// defaults: use the graph's own concurrency, halt on the first failure.
type Options struct {
	// Concurrency overrides the graph's declared cap. 0 means "use the graph's".
	// The effective value is always clamped to globalConcurrencyCap.
	Concurrency int
	// ContinueOnFail prunes only a failed node's subtree instead of halting the
	// whole run (the --continue-on-fail flag).
	ContinueOnFail bool
	// Gate resolves gate nodes. Defaults to the v0.1 refuse-everything stub.
	Gate gate.GateController
	// ProgressWriter receives one line per node lifecycle event (start, pass,
	// fail, retry) as the run executes, so a long-running graph doesn't leave
	// the terminal looking dead. Defaults to os.Stderr; pass io.Discard to
	// silence it (tests do this).
	ProgressWriter io.Writer
	// DisallowedTools is a per-node execution ceiling keyed by node id: the
	// tools that node's subprocess must be denied outright (the runner renders
	// them as --disallowedTools, which subtracts from the user's own settings
	// rather than adding to them). Auto mode supplies it from coordinator.Plan
	// because a planned graph is unreviewed LLM output; hand-written graphs
	// pass nil, so their nodes get no --disallowedTools flag at all and behave
	// exactly as before. The Scheduler only forwards it — the policy of what
	// belongs in it is the coordinator's.
	DisallowedTools map[string][]string
}

// Scheduler executes graphs. Construct it with NewScheduler (constructor
// injection — no globals); its collaborators are supplied per Run.
type Scheduler struct {
	runner         runner.NodeRunner
	continueOnFail bool
	concurrency    int
	gate           gate.GateController
	progress       io.Writer
	// disallowedTools is the per-node deny list keyed by node id (nil for
	// hand-written graphs). Reading a missing key yields nil, which the runner
	// renders as "no --disallowedTools flag".
	disallowedTools map[string][]string
	// progressMu serializes writes to progress: parallel nodes emit events from
	// separate goroutines, and io.Writer (e.g. a *bytes.Buffer) is not safe for
	// concurrent use without one.
	progressMu sync.Mutex
}

// NewScheduler builds a Scheduler bound to a NodeRunner. The runner is the seam:
// production injects ClaudeCLIRunner, tests inject FakeRunner.
func NewScheduler(nodeRunner runner.NodeRunner, opts Options) *Scheduler {
	gateController := opts.Gate
	if gateController == nil {
		gateController = gate.NewStubController()
	}
	progressWriter := opts.ProgressWriter
	if progressWriter == nil {
		progressWriter = os.Stderr
	}
	return &Scheduler{
		runner:          nodeRunner,
		continueOnFail:  opts.ContinueOnFail,
		concurrency:     opts.Concurrency,
		gate:            gateController,
		progress:        progressWriter,
		disallowedTools: opts.DisallowedTools,
	}
}

// Run executes g to completion, coordinating through h (inputs/outputs) and
// recording into led. It returns nil only when every node passed. On failure it
// returns a *HaltError (default: the first failure cancelled the run and killed
// in-flight siblings) or a *RunFailedError (continue-on-fail: some subtrees were
// pruned but independent branches finished).
func (s *Scheduler) Run(ctx context.Context, g *graph.Graph, h *handoff.Handoff, led *ledger.RunLedger) error {
	sem := make(chan struct{}, effectiveConcurrency(s.concurrency, g.Concurrency))
	grp, ctx := errgroup.WithContext(ctx)

	var mu sync.Mutex
	inDegree := make(map[string]int, len(g.Nodes))
	for _, n := range g.Nodes {
		inDegree[n.ID] = len(n.DependsOn)
	}

	var failMu sync.Mutex
	var prunedFailures []string

	var launch func(id string)
	launch = func(id string) {
		node, _ := g.NodeByID(id)
		grp.Go(func() error {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return ctx.Err()
			}
			defer func() { <-sem }()

			if err := s.runNode(ctx, node, h, led); err != nil {
				if s.continueOnFail {
					failMu.Lock()
					prunedFailures = append(prunedFailures, id)
					failMu.Unlock()
					// Prune the subtree: do not enqueue dependents, but do not
					// cancel the shared context — independent branches continue.
					return nil
				}
				// Halt-on-fail: returning a non-nil error cancels ctx, which
				// propagates to every in-flight child (killing wedged claude
				// subprocesses) and stops new nodes from starting.
				return &HaltError{NodeID: id, Err: err}
			}

			// Success: every dependent loses one in-degree; any that reach zero
			// join the ready set right now.
			for _, dependent := range g.DependentsOf(id) {
				mu.Lock()
				inDegree[dependent]--
				ready := inDegree[dependent] == 0
				mu.Unlock()
				if ready {
					launch(dependent)
				}
			}
			return nil
		})
	}

	for _, root := range g.Roots() {
		launch(root)
	}

	if err := grp.Wait(); err != nil {
		return err
	}

	failMu.Lock()
	defer failMu.Unlock()
	if len(prunedFailures) > 0 {
		sort.Strings(prunedFailures)
		return &RunFailedError{FailedNodes: prunedFailures}
	}
	return nil
}

// runNode executes one node under the run policy and records exactly one ledger
// row for it. It returns nil on success, or the failure (a gate refusal, an
// interpolation error, a runner error, or a *NodeCheckError) after retries are
// exhausted.
func (s *Scheduler) runNode(ctx context.Context, node graph.Node, h *handoff.Handoff, led *ledger.RunLedger) error {
	start := time.Now()
	s.logProgress("▶ %s  running…\n", node.ID)

	if node.Type == graph.TypeGate {
		err := s.gate.Evaluate(ctx, node)
		return s.recordFail(led, node.ID, "", 0, time.Since(start), err)
	}

	invocation, err := s.buildInvocation(node, h)
	if err != nil {
		return s.recordFail(led, node.ID, "", 0, time.Since(start), err)
	}

	attempts := 1
	if node.Retry != nil && node.Retry.Max > 0 {
		attempts += node.Retry.Max
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			// A retry never resumes a failed session — start fresh.
			invocation.ResumeSession = ""
		}

		outcome, runErr := s.runner.Run(ctx, invocation)
		if runErr != nil {
			lastErr = runErr
			if s.shouldRetry(node, attempt, attempts, causeFromRunError(runErr)) {
				s.logProgress("↻ %s  retry\n", node.ID)
				continue
			}
			return s.recordFail(led, node.ID, "", 0, time.Since(start), runErr)
		}

		checkErr := evaluateSuccessCheck(node, outcome)
		if checkErr == nil {
			if persistErr := h.PersistOutput(node.ID, outcome.Result, outcome.SessionID); persistErr != nil {
				return s.recordFail(led, node.ID, outcome.SessionID, outcome.TotalCostUSD, time.Since(start), persistErr)
			}
			return s.recordPass(led, node.ID, outcome, time.Since(start), attempt)
		}

		lastErr = checkErr
		if s.shouldRetry(node, attempt, attempts, causeFromCheck(checkErr)) {
			s.logProgress("↻ %s  retry\n", node.ID)
			continue
		}
		return s.recordFail(led, node.ID, outcome.SessionID, outcome.TotalCostUSD, time.Since(start), checkErr)
	}

	// Unreachable in practice — the final attempt always records and returns
	// above — but the compiler cannot prove the loop terminates with a return.
	return lastErr
}

// recordFail writes the node's live "✗ FAILED" progress line and its ledger
// row, then returns cause so callers can `return s.recordFail(...)` directly.
func (s *Scheduler) recordFail(led *ledger.RunLedger, nodeID, sessionID string, cost float64, duration time.Duration, cause error) error {
	s.logProgress("✗ %s  FAILED: %s\n", nodeID, cause.Error())
	led.Record(failRecord(nodeID, sessionID, cost, duration, cause))
	return cause
}

// recordPass writes the node's live "✓ PASS" progress line and its ledger row,
// then returns nil so callers can `return s.recordPass(...)` directly.
func (s *Scheduler) recordPass(led *ledger.RunLedger, nodeID string, outcome runner.NodeOutcome, duration time.Duration, attempt int) error {
	s.logProgress("✓ %s  %s  $%.4f  %s\n", nodeID, ledger.VerdictPass, outcome.TotalCostUSD, duration.Round(time.Millisecond))
	led.Record(passRecord(nodeID, outcome, duration, attempt))
	return nil
}

// logProgress writes one progress line, serialized by progressMu: parallel
// nodes call this from separate goroutines, and the injected io.Writer (e.g. a
// *bytes.Buffer in tests) is not safe for concurrent writes on its own.
func (s *Scheduler) logProgress(format string, args ...any) {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	fmt.Fprintf(s.progress, format, args...)
}

// buildInvocation renders a node into a runner.NodeInvocation: interpolate its
// prompt and cwd, resolve the session it resumes (if any), default the
// permission mode, and attach the node's execution ceiling (empty unless the
// caller imposed one).
func (s *Scheduler) buildInvocation(node graph.Node, h *handoff.Handoff) (runner.NodeInvocation, error) {
	prompt, err := h.Interpolate(node.Prompt)
	if err != nil {
		return runner.NodeInvocation{}, err
	}
	cwd, err := h.Interpolate(node.Cwd)
	if err != nil {
		return runner.NodeInvocation{}, err
	}
	resume, err := h.ResumeSessionFor(node)
	if err != nil {
		return runner.NodeInvocation{}, err
	}

	permissionMode := node.PermissionMode
	if permissionMode == "" {
		permissionMode = defaultPermissionMode
	}

	return runner.NodeInvocation{
		Prompt:          prompt,
		Cwd:             cwd,
		PermissionMode:  permissionMode,
		ResumeSession:   resume,
		AllowedTools:    node.AllowedTools,
		DisallowedTools: s.disallowedTools[node.ID],
	}, nil
}

// shouldRetry reports whether a failed attempt should be retried: there must be
// an attempt left and the failure cause must be listed in the node's retry.on.
func (s *Scheduler) shouldRetry(node graph.Node, attempt, attempts int, cause string) bool {
	if attempt >= attempts-1 {
		return false
	}
	if node.Retry == nil {
		return false
	}
	for _, allowed := range node.Retry.On {
		if allowed == cause {
			return true
		}
	}
	return false
}

// effectiveConcurrency resolves the ready-set width: the CLI override if given,
// else the graph's own value, else the default — clamped to the global ceiling.
func effectiveConcurrency(override, graphConcurrency int) int {
	width := override
	if width <= 0 {
		width = graphConcurrency
	}
	if width <= 0 {
		width = defaultConcurrency
	}
	if width > globalConcurrencyCap {
		width = globalConcurrencyCap
	}
	return width
}

// evaluateSuccessCheck applies a node's success_check to its outcome, returning
// nil on pass or a *NodeCheckError naming the predicate that failed. An empty
// check means "exit zero is enough".
func evaluateSuccessCheck(node graph.Node, outcome runner.NodeOutcome) error {
	check := node.SuccessCheck

	if check.IsZero() {
		if outcome.ExitCode != 0 {
			return &NodeCheckError{NodeID: node.ID, Predicate: "exit_zero", Detail: fmt.Sprintf("exit code %d", outcome.ExitCode)}
		}
		return nil
	}

	if check.ExitZero && outcome.ExitCode != 0 {
		return &NodeCheckError{NodeID: node.ID, Predicate: "exit_zero", Detail: fmt.Sprintf("exit code %d", outcome.ExitCode)}
	}

	if check.ResultMatches != "" {
		re, err := regexp.Compile(check.ResultMatches)
		if err != nil {
			return &NodeCheckError{NodeID: node.ID, Predicate: "result_matches", Detail: fmt.Sprintf("invalid regex %q: %v", check.ResultMatches, err)}
		}
		if !re.MatchString(outcome.Result) {
			return &NodeCheckError{NodeID: node.ID, Predicate: "result_matches", Detail: fmt.Sprintf("result did not match /%s/", check.ResultMatches)}
		}
	}
	return nil
}

// causeFromRunError maps a runner error to a retry cause token. An unparseable
// output is "output_error"; anything else (spawn failure, context) is
// "run_error". Non-zero exits never reach here — the runner returns those inside
// the outcome, and evaluateSuccessCheck classifies them.
func causeFromRunError(err error) string {
	var outputErr *runner.NodeOutputError
	if asErr(err, &outputErr) {
		return "output_error"
	}
	return "run_error"
}

// causeFromCheck maps a failed success_check to a retry cause token: a failed
// exit_zero predicate is "nonzero_exit"; a failed result_matches is
// "result_mismatch".
func causeFromCheck(err error) string {
	var checkErr *NodeCheckError
	if asErr(err, &checkErr) && checkErr.Predicate == "result_matches" {
		return "result_mismatch"
	}
	return "nonzero_exit"
}

// passRecord / failRecord build the single ledger row for a node's terminal
// result, keeping the record shape in one place.
func passRecord(nodeID string, outcome runner.NodeOutcome, duration time.Duration, attempt int) ledger.Record {
	detail := ""
	if attempt > 0 {
		detail = fmt.Sprintf("passed after %d retr%s", attempt, plural(attempt))
	}
	return ledger.Record{
		NodeID:    nodeID,
		SessionID: outcome.SessionID,
		CostUSD:   outcome.TotalCostUSD,
		Verdict:   ledger.VerdictPass,
		Duration:  duration,
		Detail:    detail,
	}
}

func failRecord(nodeID, sessionID string, cost float64, duration time.Duration, cause error) ledger.Record {
	return ledger.Record{
		NodeID:    nodeID,
		SessionID: sessionID,
		CostUSD:   cost,
		Verdict:   ledger.VerdictFail,
		Duration:  duration,
		Detail:    cause.Error(),
	}
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
