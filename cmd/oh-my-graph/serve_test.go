package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/runfeed"
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

// --- wiring: an unresolvable run fails before anything listens ---------------

func TestRunServe_UnknownRunIDErrors(t *testing.T) {
	isolateRunHome(t)
	err := runServe([]string{"no-such-run"})
	if err == nil || !strings.Contains(err.Error(), "unknown run") {
		t.Fatalf("err = %v, want the unknown-run error", err)
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
	go func() { done <- serveDashboard(ctx, &out, listener, root, nil) }()

	body := getWithRetry(t, "http://"+listener.Addr().String()+"/api/cards")
	if strings.TrimSpace(body) != "[]" {
		t.Errorf("/api/cards = %q, want an empty card list", body)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("a cancelled serveDashboard must return nil, got %v", err)
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
	feed.Close()

	listener, err := serve.Listen(0)
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serveDashboard(ctx, &strings.Builder{}, listener, runsDir, nil) }()

	base := "http://" + listener.Addr().String()
	body := getWithRetry(t, base+"/api/cards")
	if !strings.Contains(body, runID) || !strings.Contains(body, `"state":"running"`) {
		t.Errorf("/api/cards = %s, want a running card for %s", body, runID)
	}
	// The card's link: the single-run view, mounted.
	if page := getWithRetry(t, base+"/run/"+runID+"/"); !strings.Contains(page, "oh-my-graph") {
		t.Errorf("/run/<id>/ did not serve the run view:\n%s", page)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("a cancelled serveDashboard must return nil, got %v", err)
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
