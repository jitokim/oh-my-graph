package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// liveGraphYAML is the one-node fixture every live-view test runs. The node id
// doubles as the FakeRunner outcome key.
const liveGraphYAML = `
name: live
nodes:
  - { id: probe, prompt: probe }
`

// openPTY opens the pseudo-terminal multiplexer as a stand-in for an
// interactive stdout: a real character device, so isTerminal answers true
// without the test needing an actual terminal. Skips where /dev/ptmx does not
// exist (Windows).
func openPTY(t *testing.T) *os.File {
	t.Helper()
	pty, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no /dev/ptmx on this platform, cannot simulate a TTY: %v", err)
	}
	t.Cleanup(func() { pty.Close() })
	return pty
}

// --- the gate ----------------------------------------------------------------

func TestWebOpener_GatesOnTTYAndNoWebFlag(t *testing.T) {
	fake := browser.NewFakeOpener()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	if got := webOpener(false, w, fake); got != nil {
		t.Errorf("a pipe stdout (scripts, CI) must yield no opener, got %T", got)
	}

	pty := openPTY(t)
	if got := webOpener(true, pty, fake); got != nil {
		t.Errorf("--no-web must yield no opener even on a terminal, got %T", got)
	}
	if got := webOpener(false, pty, fake); got != fake {
		t.Errorf("a terminal without --no-web must yield the injected opener, got %T", got)
	}
}

// --- TTY: the view is served, opened, and dies with the run ------------------

// liveViewProbeRunner stands in for the node runner and, from INSIDE the only
// node's Run — i.e. strictly mid-run — fetches /api/graph from whatever URL
// the FakeOpener was handed. Whatever it saw is recorded for the test to
// judge after the run ends.
type liveViewProbeRunner struct {
	opener *browser.FakeOpener

	err    error
	status int
	body   string
}

func (r *liveViewProbeRunner) Run(_ context.Context, spec runner.NodeInvocation) (runner.NodeOutcome, error) {
	outcome := runner.NodeOutcome{SessionID: "s-" + spec.Prompt, Result: "PASS", ExitCode: 0}
	urls := r.opener.URLs()
	if len(urls) != 1 {
		r.err = fmt.Errorf("expected exactly one opened URL before the first node ran, got %v", urls)
		return outcome, nil
	}
	resp, err := http.Get(urls[0] + "api/graph")
	if err != nil {
		r.err = fmt.Errorf("GET /api/graph mid-run: %w", err)
		return outcome, nil
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		r.err = fmt.Errorf("read /api/graph body: %w", err)
		return outcome, nil
	}
	r.status = resp.StatusCode
	r.body = string(body)
	return outcome, nil
}

func TestRunGraph_TTYServesLiveViewOpensBrowserAndStopsWithRun(t *testing.T) {
	pty := openPTY(t)
	isolateRunHome(t)
	path := writeGraphFile(t, liveGraphYAML)
	fake := browser.NewFakeOpener()
	probe := &liveViewProbeRunner{opener: fake}

	out := captureStdout(t, func() {
		if err := runGraphWith([]string{path}, probe, fake, pty); err != nil {
			t.Errorf("run returned error: %v", err)
		}
	})

	urls := fake.URLs()
	if len(urls) != 1 || !regexp.MustCompile(`^http://127\.0\.0\.1:\d+/$`).MatchString(urls[0]) {
		t.Fatalf("the opener must receive exactly the served loopback URL, got %v", urls)
	}
	if probe.err != nil {
		t.Fatalf("the embedded server must answer mid-run: %v", probe.err)
	}
	if probe.status != http.StatusOK || !strings.Contains(probe.body, `"run_id"`) {
		t.Errorf("mid-run GET /api/graph = %d %q, want 200 with a run_id payload", probe.status, probe.body)
	}
	if !strings.Contains(out, "Serving live view of run") {
		t.Errorf("the URL must be announced exactly as `serve` announces it, got:\n%s", out)
	}

	// The server must die with the run: once runGraphWith has returned, the
	// port answers no more.
	if resp, err := http.Get(urls[0]); err == nil {
		resp.Body.Close()
		t.Error("the live view is still answering after the run ended — the server outlived its run")
	}
}

// --- non-TTY and --no-web: nothing served, nothing opened, output unchanged --

// runIDPattern matches newRunID's minted ids wherever they appear in output,
// so two runs' outputs can be compared net of the one value that legitimately
// differs.
var runIDPattern = regexp.MustCompile(`\d{8}-\d{6}\.\d{9}-\d+`)

func TestRunGraph_NonTTYSpawnsNothingAndOutputIsByteIdentical(t *testing.T) {
	isolateRunHome(t)
	path := writeGraphFile(t, liveGraphYAML)
	fake := browser.NewFakeOpener()
	outcomes := map[string]runner.NodeOutcome{
		"probe": {SessionID: "s-probe", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.01},
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	got := captureStdout(t, func() {
		if err := runGraphWith([]string{path}, runner.NewFakeRunner(outcomes), fake, w); err != nil {
			t.Errorf("run returned error: %v", err)
		}
	})
	if urls := fake.URLs(); len(urls) != 0 {
		t.Fatalf("a non-TTY run must never reach the opener, got %v", urls)
	}

	// Baseline: executeGraph with no web opener is, verbatim, the execution
	// path as it existed before the live view — so matching it byte-for-byte
	// (net of the minted run id) proves scripts and CI see no change at all.
	g := mustParse(t, liveGraphYAML)
	want := captureStdout(t, func() {
		if err := executeGraph(context.Background(), "baseline-run-id", g, runner.NewFakeRunner(outcomes),
			commonRunFlags{inputs: inputFlag{}}, nil, 0, path, []byte(liveGraphYAML), false, nil, nil); err != nil {
			t.Errorf("baseline run returned error: %v", err)
		}
	})

	gotNorm := runIDPattern.ReplaceAllString(got, "RUNID")
	wantNorm := strings.ReplaceAll(want, "baseline-run-id", "RUNID")
	if gotNorm != wantNorm {
		t.Errorf("non-TTY output diverged from the pre-live-view baseline:\n--- got ---\n%s\n--- want ---\n%s", gotNorm, wantNorm)
	}
}

func TestRunGraph_NoWebSuppressesLiveViewOnTTY(t *testing.T) {
	pty := openPTY(t)
	isolateRunHome(t)
	path := writeGraphFile(t, liveGraphYAML)
	fake := browser.NewFakeOpener()
	outcomes := map[string]runner.NodeOutcome{
		"probe": {SessionID: "s-probe", Result: "PASS", ExitCode: 0},
	}

	out := captureStdout(t, func() {
		if err := runGraphWith([]string{path, "--no-web"}, runner.NewFakeRunner(outcomes), fake, pty); err != nil {
			t.Errorf("run returned error: %v", err)
		}
	})
	if urls := fake.URLs(); len(urls) != 0 {
		t.Fatalf("--no-web must never reach the opener, got %v", urls)
	}
	if strings.Contains(out, "Serving live view") {
		t.Errorf("--no-web must not serve anything, got:\n%s", out)
	}
}

// --- a failed browser launch must not fail the run ---------------------------

func TestRunGraph_FailedBrowserLaunchDoesNotFailTheRun(t *testing.T) {
	pty := openPTY(t)
	isolateRunHome(t)
	path := writeGraphFile(t, liveGraphYAML)
	fake := browser.NewFakeOpener()
	fake.InjectError(errors.New("no display"))
	outcomes := map[string]runner.NodeOutcome{
		"probe": {SessionID: "s-probe", Result: "PASS", ExitCode: 0},
	}

	captureStdout(t, func() {
		if err := runGraphWith([]string{path}, runner.NewFakeRunner(outcomes), fake, pty); err != nil {
			t.Errorf("a failed browser launch must leave the run intact, got %v", err)
		}
	})
	if urls := fake.URLs(); len(urls) != 1 {
		t.Errorf("the launch must still have been attempted once, got %v", urls)
	}
}
