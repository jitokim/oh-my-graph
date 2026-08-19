package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/serve"
)

// --- CLI contract: argument parsing ------------------------------------------

func TestServeFlags_ParsesOptionalRunIDAndPort(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantRunID string
		wantPort  int
		wantErr   string
	}{
		{name: "no arguments means the dashboard, on the default port", args: nil, wantRunID: "", wantPort: serve.DefaultPort},
		{name: "a positional run id is taken", args: []string{"run-1"}, wantRunID: "run-1", wantPort: serve.DefaultPort},
		{name: "run id and port compose", args: []string{"run-1", "--port", "9000"}, wantRunID: "run-1", wantPort: 9000},
		{name: "port alone is still the dashboard", args: []string{"--port", "9000"}, wantRunID: "", wantPort: 9000},
		{name: "a second positional is rejected and named", args: []string{"run-1", "extra"}, wantErr: "extra"},
		{name: "an unknown flag is rejected", args: []string{"--browser"}, wantErr: "browser"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flags := newServeFlags()
			flags.set.SetOutput(&strings.Builder{}) // silence flag's own usage print
			err := flags.parse(tc.args)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse returned error: %v", err)
			}
			if flags.runID != tc.wantRunID || flags.port != tc.wantPort {
				t.Errorf("parsed (runID %q, port %d), want (%q, %d)", flags.runID, flags.port, tc.wantRunID, tc.wantPort)
			}
		})
	}
}

// TestServeFlags_Help pins #200 for `serve`, which is the one subcommand the
// survey marked NO for swallowing — its leading-dash guard already existed —
// but YES for the wrong exit code and stream: help worked, but printed flag
// defaults to stderr and then exited 1 with `flag: help requested`. Both
// spellings must now come back as a *usageRequest instead, leaving runID and
// port at their zero values.
func TestServeFlags_Help(t *testing.T) {
	for _, arg := range []string{"--help", "-h"} {
		f := newServeFlags()
		err := f.parse([]string{arg})
		var usage *usageRequest
		if !errors.As(err, &usage) {
			t.Fatalf("parse([%q]) = %v (%T), want a *usageRequest", arg, err, err)
		}
		if !strings.Contains(usage.Error(), "oh-my-graph serve") {
			t.Errorf("usage.Error() = %q, want it to name `serve`'s synopsis", usage.Error())
		}
		if f.runID != "" {
			t.Errorf("runID = %q after a help request, want it left unset", f.runID)
		}
	}
}

// --- wiring: an unresolvable run fails before anything listens ---------------

func TestRunServe_UnknownRunIDErrors(t *testing.T) {
	isolateRunHome(t)
	err := runServe([]string{"no-such-run"})
	if err == nil || !strings.Contains(err.Error(), "unknown run") {
		t.Fatalf("err = %v, want the unknown-run error", err)
	}
}

// TestRunServeRuntime_HelpNeverResolvesARunOrBindsAListener is the wiring-
// level guard: a help request must return before serve.ResolveRun or
// serve.Listen are ever reached — reaching either would mean this test either
// hangs (a bound dashboard serves until cancelled) or leaks a listening
// socket, so returning promptly with a *usageRequest and no port bound is
// itself the assertion that neither ran.
func TestRunServeRuntime_HelpNeverResolvesARunOrBindsAListener(t *testing.T) {
	isolateRunHome(t)
	err := runServeRuntime(runner.RuntimeClaude, false, []string{"--help"})
	var usage *usageRequest
	if !errors.As(err, &usage) {
		t.Fatalf("runServeRuntime([--help]) = %v (%T), want a *usageRequest", err, err)
	}
}

// --- wiring: no run id is the dashboard, not a resolved run -----------------

func TestServeDashboard_AnEmptyRunsRootServesAnEmptyDashboard(t *testing.T) {
	// `serve` with no run id no longer resolves a run, so it no longer fails
	// when there is none: it is the dashboard over the runs ROOT, which is
	// empty until something runs and fills in without a restart.
	listener, err := serve.Listen(0)
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}

	root := filepath.Join(t.TempDir(), "never-created")
	ctx, cancel := context.WithCancel(context.Background())
	var out strings.Builder
	done := make(chan error, 1)
	handler, banner := dashboardView(listener, root, nil)
	go func() { done <- serveUntilCancelled(ctx, &out, listener, handler, banner, nil) }()

	body := getWithRetry(t, "http://"+listener.Addr().String()+"/api/cards")
	if strings.TrimSpace(body) != "[]" {
		t.Errorf("/api/cards = %q, want an empty card list", body)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("a cancelled dashboard must return nil, got %v", err)
	}
	if got := out.String(); !strings.Contains(got, "dashboard") || !strings.Contains(got, "http://127.0.0.1:") {
		t.Errorf("the announcement must name the dashboard and a loopback URL, got %q", got)
	}
}

func TestServeDashboard_CardsAndTheMountedRunView(t *testing.T) {
	// The end-to-end shape of the feature through the CLI's own wiring: a run
	// on disk is a card, and that card's href is a live view of that run.
	isolateRunHome(t)
	runsDir := runsRoot()
	const runID = "20260804-000000.000000000-1"
	if err := os.MkdirAll(filepath.Join(runsDir, runID), 0o755); err != nil {
		t.Fatalf("create run dir: %v", err)
	}
	feed, err := runfeed.NewStreamWriter(filepath.Join(runsDir, runID, runfeed.FileName), runID)
	if err != nil {
		t.Fatalf("open fixture stream: %v", err)
	}
	if err := feed.Emit(runfeed.Event{Type: runfeed.EventRunStarted}); err != nil {
		t.Fatalf("emit fixture event: %v", err)
	}
	// Closed before the dashboard reads it, and checked: Close is where a final
	// write error surfaces, so an unchecked one would leave this test asserting
	// against a fixture that never fully landed.
	if err := feed.Close(); err != nil {
		t.Fatalf("close fixture stream: %v", err)
	}

	listener, err := serve.Listen(0)
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	handler, banner := dashboardView(listener, runsDir, nil)
	go func() { done <- serveUntilCancelled(ctx, &strings.Builder{}, listener, handler, banner, nil) }()

	base := "http://" + listener.Addr().String()
	body := getWithRetry(t, base+"/api/cards")
	if !strings.Contains(body, runID) || !strings.Contains(body, `"state":"running"`) {
		t.Errorf("/api/cards = %s, want a running card for %s", body, runID)
	}
	// The card's link: the single-run view, mounted.
	page := getWithRetry(t, base+"/run/"+runID+"/")
	if !strings.Contains(page, "oh-my-graph") {
		t.Errorf("/run/<id>/ did not serve the run view:\n%s", page)
	}
	// The footer's build label comes through the CLI's own wiring, so it is
	// pinned here rather than in internal/serve, whose tests supply their own
	// label: `Version` is the single literal both `--version` and the page must
	// report, and a second one introduced here would be invisible until two
	// surfaces disagreed in front of a reader.
	if !strings.Contains(page, Version) {
		t.Errorf("/run/<id>/ does not name the build serving it (want %q in the footer):\n%s", Version, page)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("a cancelled dashboard must return nil, got %v", err)
	}
}

// getWithRetry GETs url until the server is up, and returns the body.
func getWithRetry(t *testing.T, url string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(url)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s status = %d, want 200", url, resp.StatusCode)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read %s: %v", url, err)
			}
			return string(body)
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never answered on %s: %v", url, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// --- wiring: serveRun announces the URL and serves until cancelled -----------

func TestServeRun_PrintsLoopbackURLAndStopsOnCancel(t *testing.T) {
	listener, err := serve.Listen(0)
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var out strings.Builder
	done := make(chan error, 1)
	go func() { done <- serveRun(ctx, &out, listener, t.TempDir(), "run-1", nil) }()

	// The server must actually answer on the announced address before we
	// cancel — proving serveRun serves, not just prints.
	url := "http://" + listener.Addr().String() + "/api/graph"
	var resp *http.Response
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err = http.Get(url)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never answered on %s: %v", url, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET %s status = %d, want 200", url, resp.StatusCode)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("a cancelled serveRun must return nil (Ctrl-C is not a failure), got %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "run-1") || !strings.Contains(got, "http://127.0.0.1:") {
		t.Errorf("the announcement must carry the run id and a loopback URL, got %q", got)
	}
}

func TestServeRun_ServeFailureSurfacesWithoutACancel(t *testing.T) {
	// Serve can return for a non-cancel reason (here: a dead listener). That
	// must surface as an error immediately — not hang waiting on a ctx that
	// will never be cancelled, and not leak a shutdown goroutine.
	listener, err := serve.Listen(0)
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}
	listener.Close()

	done := make(chan error, 1)
	go func() {
		done <- serveRun(context.Background(), &strings.Builder{}, listener, t.TempDir(), "run-1", nil)
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "serve live view") {
			t.Fatalf("err = %v, want the serve-live-view failure", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveRun hung on a Serve failure instead of returning it")
	}
}

func TestServeRun_CancelEndsAnOpenSSEStream(t *testing.T) {
	// /api/events streams are never idle, and Shutdown waits for idle
	// connections — so a cancel must also cancel the request contexts
	// (server.BaseContext), or serveRun hangs forever behind one connected
	// viewer. This pins the whole chain: connect a live SSE client, cancel,
	// and require serveRun to return promptly and cleanly.
	listener, err := serve.Listen(0)
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serveRun(ctx, &strings.Builder{}, listener, t.TempDir(), "run-1", nil) }()

	resp, err := http.Get("http://" + listener.Addr().String() + "/api/events")
	if err != nil {
		t.Fatalf("open SSE stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE status = %d, want 200", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a cancelled serveRun must return nil, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveRun hung on shutdown behind an open SSE stream")
	}
}

// --- auto-open: the URL goes to the browser on a terminal --------------------

func TestServeFlags_AutoOpenerGatesOnTTYAndNoOpen(t *testing.T) {
	// `serve` prints a URL and waits, so on a terminal it should also OPEN it.
	// The gate is the one the whole CLI shares (webOpener): a terminal and no
	// opt-out. Everything else yields nil — no Opener is consulted at all, so
	// a scripted `serve` cannot spawn anything.
	fake := browser.NewFakeOpener()
	pipe, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer pipe.Close()
	defer w.Close()
	pty := openPTY(t)

	cases := []struct {
		name   string
		args   []string
		stdout *os.File
		want   browser.Opener
	}{
		{name: "a terminal and no opt-out opens", args: nil, stdout: pty, want: fake},
		{name: "--no-open on a terminal opens nothing", args: []string{"--no-open"}, stdout: pty, want: nil},
		{name: "a pipe (scripts, CI) opens nothing", args: nil, stdout: w, want: nil},
		{name: "--no-open composes with a run id and a port", args: []string{"run-1", "--port", "9100", "--no-open"}, stdout: pty, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flags := newServeFlags()
			flags.set.SetOutput(&strings.Builder{})
			if err := flags.parse(tc.args); err != nil {
				t.Fatalf("parse returned error: %v", err)
			}
			if got := flags.autoOpener(tc.stdout, fake); got != tc.want {
				t.Errorf("autoOpener = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestServeUntilCancelled_HandsTheServedURLToTheOpener(t *testing.T) {
	listener, err := serve.Listen(0)
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}
	fake := browser.NewFakeOpener()

	ctx, cancel := context.WithCancel(context.Background())
	var out strings.Builder
	handler, banner := dashboardView(listener, t.TempDir(), nil)
	done := make(chan error, 1)
	go func() { done <- serveUntilCancelled(ctx, &out, listener, handler, banner, fake) }()

	// Opened, and opened at the address actually being served — the URL the
	// banner printed, not a reconstruction of it.
	body := getWithRetry(t, "http://"+listener.Addr().String()+"/api/cards")
	if strings.TrimSpace(body) != "[]" {
		t.Errorf("the opened server did not answer: %q", body)
	}
	want := "http://" + listener.Addr().String() + "/"
	if urls := fake.URLs(); len(urls) != 1 || urls[0] != want {
		t.Errorf("opener received %v, want exactly [%s]", urls, want)
	}
	if !strings.Contains(out.String(), want) {
		t.Errorf("the opened URL is not the announced one:\n%s", out.String())
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("a cancelled serve must return nil, got %v", err)
	}
}

func TestServeUntilCancelled_WithoutAnOpenerNothingSpawnsAndStdoutIsIdentical(t *testing.T) {
	// The non-TTY / --no-open contract: byte-identical output to a build with
	// no auto-open at all. Proven by running both paths and comparing.
	serveOnce := func(opener browser.Opener) string {
		t.Helper()
		listener, err := serve.Listen(0)
		if err != nil {
			t.Fatalf("Listen returned error: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		var out strings.Builder
		handler, banner := runView(listener, t.TempDir(), "run-1", nil)
		done := make(chan error, 1)
		go func() { done <- serveUntilCancelled(ctx, &out, listener, handler, banner, opener) }()
		getWithRetry(t, "http://"+listener.Addr().String()+"/api/graph")
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("a cancelled serve must return nil, got %v", err)
		}
		// The port is the one value that legitimately differs between runs.
		return strings.ReplaceAll(out.String(), listener.Addr().String(), "ADDR")
	}

	fake := browser.NewFakeOpener()
	opened := serveOnce(fake)
	quiet := serveOnce(nil)

	if len(fake.URLs()) != 1 {
		t.Errorf("the opened run must have reached the opener exactly once, got %v", fake.URLs())
	}
	if quiet != opened {
		t.Errorf("stdout differs with and without auto-open:\n--- no opener ---\n%s\n--- opener ---\n%s", quiet, opened)
	}
}

func TestServeUntilCancelled_AFailedLaunchStillServes(t *testing.T) {
	// No display, no registered handler: the launch fails, the server does not.
	// The URL is already printed, and the server is what was asked for.
	listener, err := serve.Listen(0)
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}
	fake := browser.NewFakeOpener()
	fake.InjectError(errors.New("no display"))

	ctx, cancel := context.WithCancel(context.Background())
	handler, banner := dashboardView(listener, t.TempDir(), nil)
	done := make(chan error, 1)
	go func() { done <- serveUntilCancelled(ctx, &strings.Builder{}, listener, handler, banner, fake) }()

	if body := getWithRetry(t, "http://"+listener.Addr().String()+"/api/cards"); strings.TrimSpace(body) != "[]" {
		t.Errorf("the server stopped answering after a failed launch: %q", body)
	}
	if urls := fake.URLs(); len(urls) != 1 {
		t.Errorf("the launch must still have been attempted once, got %v", urls)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("a failed browser launch must not fail the serve, got %v", err)
	}
}
