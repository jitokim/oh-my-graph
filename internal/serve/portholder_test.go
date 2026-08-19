package serve

import (
	"fmt"
	"html"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// This file drives probePortHolder and (portHolder).message() directly
// against httptest loopback servers — never a real bind failure. That is
// deliberate (portholder.go's own doc comment): the classification is pure
// enough to exercise this way, and a loopback httptest.Server is not the
// exec seam internal/invariants guards; nothing here spawns a process.
//
// The four worlds a probe can land in (holderKind's doc comment) are each
// covered by exactly one test below:
//
//	(a) another oh-my-graph, a DIFFERENT build  → TestProbePortHolder_OtherBuildNamesBothAndSaysOlder
//	(b) another oh-my-graph, the SAME build     → TestProbePortHolder_SameBuildSaysAlreadyRunning
//	(c) not identifiable as oh-my-graph at all  → TestProbePortHolder_UnidentifiedFallsBackToPortFlag
//	(d) accepts the connection, never responds  → TestProbePortHolder_HolderNeverRespondsGivesUpWithinBound

// metaPage renders a minimal page carrying exactly the three <meta> tags
// holderBuild reads, in the same name-before-content order and escaping
// (html.EscapeString) the real templates emit — so a test fixture is probed
// the same way a served page is, not through a shortcut holderBuild does not
// actually take.
func metaPage(b Build) string {
	var sb strings.Builder
	sb.WriteString("<!doctype html><html><head>")
	for _, tag := range []struct{ name, content string }{
		{"omg-version", b.Version},
		{"omg-revision", b.Revision},
		{"omg-built-at", b.BuiltAt},
	} {
		fmt.Fprintf(&sb, `<meta name="%s" content="%s">`, tag.name, html.EscapeString(tag.content))
	}
	sb.WriteString("</head><body></body></html>")
	return sb.String()
}

// newHolderServer starts a loopback httptest.Server that answers every
// request with body, closed automatically at test cleanup, and returns the
// port it bound — the port probePortHolder is told to ask, since the probe
// always addresses 127.0.0.1:<port> itself (probeURL) rather than taking a
// server to talk to.
func newHolderServer(t *testing.T, body string) int {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return serverPort(t, server)
}

// serverPort extracts the loopback port an httptest.Server bound, failing
// loudly if it somehow bound something other than a TCP address.
func serverPort(t *testing.T, server *httptest.Server) int {
	t.Helper()
	addr, ok := server.Listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("httptest server address is %T, want *net.TCPAddr", server.Listener.Addr())
	}
	return addr.Port
}

// freeLoopbackPort returns a loopback port nothing is listening on: opened
// and immediately closed, so a probe against it hits ECONNREFUSED rather
// than a live server. The same technique TestListen_PortInUseErrorNamesThePortFlag
// uses to find a free port, run in reverse.
func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a free loopback port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release reserved port %d: %v", port, err)
	}
	return port
}

// --- (a) another oh-my-graph, a different build -----------------------------

// TestProbePortHolder_OtherBuildNamesBothAndSaysOlder covers world (a): the
// holder is another oh-my-graph, but not this build. The message must name
// BOTH builds' labels and must say the reachable server is OLDER than the
// binary that just ran — the exact sentence a stale long-lived `serve`
// deserves (portholder.go's package doc: the whole reason the probe exists).
func TestProbePortHolder_OtherBuildNamesBothAndSaysOlder(t *testing.T) {
	self := newBuild("2.0.0", time.Date(2026, 8, 20, 9, 12, 0, 0, time.UTC))
	holderFixture := newBuild("1.0.0", time.Date(2026, 8, 1, 14, 2, 0, 0, time.UTC))
	if self.BuiltAt == "" || holderFixture.BuiltAt == "" {
		t.Fatal("test fixtures produced an empty BuiltAt; sharpen the fixtures rather than the assertions below")
	}

	port := newHolderServer(t, metaPage(holderFixture))

	holder := probePortHolder(nil, port, self)
	if holder.kind != holderOtherBuild {
		t.Fatalf("probePortHolder kind = %v, want holderOtherBuild", holder.kind)
	}

	msg := holder.message()
	if !strings.Contains(msg, self.Label) {
		t.Errorf("message %q does not name this binary's own build %q", msg, self.Label)
	}
	if !strings.Contains(msg, holderFixture.Label) {
		t.Errorf("message %q does not name the holder's build %q", msg, holderFixture.Label)
	}
	if !strings.Contains(msg, "older") {
		t.Errorf("message %q does not say the reachable server is OLDER than the binary just run — the holder's BuiltAt is earlier", msg)
	}
	if strings.Contains(msg, "newer") {
		t.Errorf("message %q says the holder is newer, but the holder's BuiltAt is earlier than self's", msg)
	}
}

// --- (b) another oh-my-graph, the same build --------------------------------

// TestProbePortHolder_SameBuildSaysAlreadyRunning covers world (b): the
// holder answers with the exact same build atoms as this binary. The message
// must say so, not just fall back to naming a difference that is not there.
func TestProbePortHolder_SameBuildSaysAlreadyRunning(t *testing.T) {
	instant := time.Date(2026, 8, 20, 9, 12, 0, 0, time.UTC)
	self := newBuild("1.2.3", instant)
	holderFixture := newBuild("1.2.3", instant)
	if self != holderFixture {
		t.Fatalf("test fixture: two newBuild() calls given identical inputs produced different values, self=%+v holder=%+v", self, holderFixture)
	}

	port := newHolderServer(t, metaPage(holderFixture))

	holder := probePortHolder(nil, port, self)
	if holder.kind != holderSameBuild {
		t.Fatalf("probePortHolder kind = %v, want holderSameBuild", holder.kind)
	}

	msg := holder.message()
	if !strings.Contains(msg, "same build") {
		t.Errorf("message %q does not say the holder is running the SAME build", msg)
	}
	if !strings.Contains(msg, self.Label) {
		t.Errorf("message %q does not name the shared build %q", msg, self.Label)
	}
}

// --- (c) not identifiable as oh-my-graph ------------------------------------

// TestProbePortHolder_UnidentifiedFallsBackToPortFlag covers world (c) in
// every shape holderKind's doc comment says lands there: a connection that is
// refused outright, a 200 with a body that carries none of the three tags, a
// body with only some of them (a `serve` old enough to predate this change),
// and a body whose omg-version tag is present but empty. All four must
// produce holderUnidentified and the message must be BYTE-FOR-BYTE the
// sentence the bind error has always carried — TestListen_PortInUseErrorNamesThePortFlag
// pins that this text still mentions --port, and this test pins that it did
// not change at all.
func TestProbePortHolder_UnidentifiedFallsBackToPortFlag(t *testing.T) {
	const wantMessage = "(if the port is taken, pick another with --port)"
	self := newBuild("2.0.0", time.Date(2026, 8, 20, 9, 12, 0, 0, time.UTC))

	cases := []struct {
		name string
		port func(t *testing.T) int
	}{
		{
			name: "connection refused: nothing listens on the probed port",
			port: freeLoopbackPort,
		},
		{
			name: "200 OK but no oh-my-graph tags at all",
			port: func(t *testing.T) int { return newHolderServer(t, "<html><body>some other server</body></html>") },
		},
		{
			name: "200 OK with only two of the three tags (a serve that predates this change)",
			port: func(t *testing.T) int {
				return newHolderServer(t, `<html><head>`+
					`<meta name="omg-version" content="1.0.0">`+
					`<meta name="omg-revision" content="abc1234">`+
					`</head></html>`)
			},
		},
		{
			name: "200 OK with all three tags but an empty version",
			port: func(t *testing.T) int {
				return newHolderServer(t, `<html><head>`+
					`<meta name="omg-version" content="">`+
					`<meta name="omg-revision" content="abc1234">`+
					`<meta name="omg-built-at" content="2026-08-01T14:02:00Z">`+
					`</head></html>`)
			},
		},
		{
			name: "non-200 status",
			port: func(t *testing.T) int {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Error(w, "not found", http.StatusNotFound)
				}))
				t.Cleanup(server.Close)
				return serverPort(t, server)
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			holder := probePortHolder(nil, c.port(t), self)
			if holder.kind != holderUnidentified {
				t.Fatalf("probePortHolder kind = %v, want holderUnidentified", holder.kind)
			}
			if msg := holder.message(); msg != wantMessage {
				t.Errorf("message = %q, want the unchanged sentence %q", msg, wantMessage)
			}
		})
	}
}

// --- (d) the holder accepts the connection and never responds --------------

// TestProbePortHolder_HolderNeverRespondsGivesUpWithinBound covers world (d):
// a holder that accepts the TCP connection but never writes a response. The
// probe must give up within its bound (probeTimeout, 2s) rather than hang the
// bind-failure path forever.
//
// The handler below blocks on a channel the test closes at cleanup, so the
// server cannot answer until this test says so. The call itself runs in a
// goroutine and is raced against a ceiling far above probeTimeout: if the
// probe regresses into hanging, this test FAILS at the ceiling instead of
// hanging the test binary itself, and the separate elapsed-time assertion
// pins that the real return happens close to probeTimeout, not just
// sometime before the generous ceiling.
func TestProbePortHolder_HolderNeverRespondsGivesUpWithinBound(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // never returns during this test; only cleanup unblocks it.
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})
	port := serverPort(t, server)
	self := newBuild("2.0.0", time.Date(2026, 8, 20, 9, 12, 0, 0, time.UTC))

	// Generous ceiling: this is what stops the TEST ITSELF from hanging if
	// probePortHolder regresses and loses its internal bound. Far above
	// probeTimeout so it never flakes on a loaded CI box.
	const hangGuardCeiling = 4 * probeTimeout
	// Tighter bound: proves the probe actually gives up NEAR its documented
	// timeout, not merely sometime before the generous ceiling above.
	const givesUpNear = probeTimeout + 2*time.Second

	type result struct {
		holder  portHolder
		elapsed time.Duration
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		holder := probePortHolder(nil, port, self)
		done <- result{holder: holder, elapsed: time.Since(start)}
	}()

	select {
	case r := <-done:
		if r.elapsed > givesUpNear {
			t.Errorf("probePortHolder against a holder that never responds took %v, want it bounded near probeTimeout (%v)", r.elapsed, probeTimeout)
		}
		if r.holder.kind != holderUnidentified {
			t.Errorf("probePortHolder kind = %v, want holderUnidentified — a timeout learns nothing about the holder", r.holder.kind)
		}
		if msg := r.holder.message(); msg != "(if the port is taken, pick another with --port)" {
			t.Errorf("message = %q, want the unchanged --port sentence for a holder that never answered", msg)
		}
	case <-time.After(hangGuardCeiling):
		t.Fatalf("probePortHolder did not return within %v against a holder that accepts the connection and never responds — it appears to hang instead of giving up", hangGuardCeiling)
	}
}
