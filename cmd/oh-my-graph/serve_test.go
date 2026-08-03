package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

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
		{name: "no arguments means resolve the run and use the default port", args: nil, wantRunID: "", wantPort: serve.DefaultPort},
		{name: "a positional run id is taken", args: []string{"run-1"}, wantRunID: "run-1", wantPort: serve.DefaultPort},
		{name: "run id and port compose", args: []string{"run-1", "--port", "9000"}, wantRunID: "run-1", wantPort: 9000},
		{name: "port alone leaves the run id to resolution", args: []string{"--port", "9000"}, wantRunID: "", wantPort: 9000},
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

func TestRunServe_NoRunsAtAllErrors(t *testing.T) {
	isolateRunHome(t)
	err := runServe(nil)
	if err == nil || !strings.Contains(err.Error(), "no runs found") {
		t.Fatalf("err = %v, want the no-runs error", err)
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
