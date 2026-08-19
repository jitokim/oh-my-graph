package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// The label's shape is what a reader compares against a commit, so it is
// asserted rather than left to whatever the toolchain stamped: the version is
// always there and always `v`-prefixed exactly once, and a missing detail is
// omitted whole — never an empty "()" or a stray ", " to puzzle over.
func TestBuildLabel_AlwaysCarriesTheVersionExactlyOnceVPrefixed(t *testing.T) {
	for _, version := range []string{"0.5.2", "v0.5.2"} {
		label := BuildLabel(version)
		if !strings.HasPrefix(label, "v0.5.2") {
			t.Errorf("BuildLabel(%q) = %q, want it to open with v0.5.2", version, label)
		}
		if strings.HasPrefix(label, "vv") {
			t.Errorf("BuildLabel(%q) = %q, double-prefixed the v", version, label)
		}
		if strings.Contains(label, "()") || strings.Contains(label, "(, ") || strings.Contains(label, ", )") {
			t.Errorf("BuildLabel(%q) = %q, a missing detail must be omitted, not rendered empty", version, label)
		}
		if detail, found := strings.CutPrefix(label, "v0.5.2"); found && detail != "" {
			if !strings.HasPrefix(detail, " (") || !strings.HasSuffix(detail, ")") {
				t.Errorf("BuildLabel(%q) = %q, want the detail parenthesised", version, label)
			}
		}
	}
}

// The version cannot tell two builds apart — every build between two tags
// carries the same one — so the label must always carry something that CAN.
// The VCS revision is absent in a linked git worktree (measured 2026-08-09,
// go1.26.5), which is how this project's own lanes build, so the executable's
// own mtime is what has to hold this up when the stamp does not.
func TestBuildLabel_SaysMoreThanTheVersion(t *testing.T) {
	label := BuildLabel("0.5.2")
	if label == "v0.5.2" {
		t.Fatal("the label is the bare version: two builds of the same tag are indistinguishable, which is the whole failure it exists to prevent")
	}
	if !strings.Contains(label, "built ") {
		t.Errorf("label = %q, want the build time — the one detail available with no VCS stamp", label)
	}
}

// The published promise is "read once at startup, never per request", and until
// buildInstant memoized it that was a convention two call sites kept — invisible
// to this suite, since a per-request caller would leave every other test green.
// Pinned by moving the executable's mtime underneath a live process, which is
// exactly what `go build -o` does to a running `serve`: neither the label nor
// the machine-readable atom may follow it, because the file at that path is no
// longer the build answering.
func TestBuildTime_IsStattedOncePerProcessNotPerCall(t *testing.T) {
	first := CurrentBuild("0.5.2")
	if first.BuiltAt == "" {
		t.Skip("the executable cannot be stat'd here, so there is no mtime to pin")
	}
	path, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Skipf("stat %s: %v", path, err)
	}
	was := info.ModTime()
	rebuilt := was.Add(-72 * time.Hour)
	if err := os.Chtimes(path, rebuilt, rebuilt); err != nil {
		t.Skipf("cannot move the executable's mtime here: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chtimes(path, was, was); err != nil {
			t.Errorf("restore %s mtime: %v", path, err)
		}
	})

	if again := CurrentBuild("0.5.2"); again.BuiltAt != first.BuiltAt {
		t.Errorf("CurrentBuild().BuiltAt = %q after the first call returned %q: the mtime is being re-read, so a rebuild renames the running build", again.BuiltAt, first.BuiltAt)
	}
	if label := BuildLabel("0.5.2"); label != first.Label {
		t.Errorf("BuildLabel = %q, want the startup label %q — the label and the tag read one stat", label, first.Label)
	}
	if !strings.Contains(first.Label, "built ") {
		t.Errorf("label = %q, want it to carry the startup build time alongside the atom %q", first.Label, first.BuiltAt)
	}
}

// The whole point of the label: it reaches the page a human is looking at. A
// server that was told nothing still renders — the label is an addition to the
// page, never a precondition for serving it.
func TestIndex_StatesTheBuildItIsServing(t *testing.T) {
	dir := t.TempDir()
	labelled := newTestServer(dir, "run-1").WithBuild(Build{Label: "v9.9.9 (deadbee, 2026-01-02)"})
	page := servedPage(t, labelled.Handler(), "http://127.0.0.1:8642/")
	if !strings.Contains(page, "v9.9.9 (deadbee, 2026-01-02)") {
		t.Errorf("the live view does not state its build:\n%s", page)
	}

	bare := servedPage(t, newTestServer(dir, "run-1").Handler(), "http://127.0.0.1:8642/")
	if strings.Contains(bare, "v9.9.9") {
		t.Error("a server given no build label rendered one anyway")
	}
}

// A stale `serve` is stale for every page it answers, so the dashboard states
// the same build — and, because a card click stays inside this process, so does
// each run view mounted under it. Two pages that could disagree here would put
// the reader back where the missing label left them.
func TestDashboard_AndItsMountedRunsStateTheSameBuild(t *testing.T) {
	root := runsRootWith(t, "run-live")
	d := newTestDashboard(root).WithBuild(Build{Label: "v9.9.9 (deadbee, 2026-01-02)"})
	handler := d.Handler()

	if page := servedPage(t, handler, "http://127.0.0.1:8642/"); !strings.Contains(page, "v9.9.9 (deadbee, 2026-01-02)") {
		t.Errorf("the dashboard does not state its build:\n%s", page)
	}
	if page := servedPage(t, handler, "http://127.0.0.1:8642/run/run-live/"); !strings.Contains(page, "v9.9.9 (deadbee, 2026-01-02)") {
		t.Errorf("a run mounted on the dashboard does not state the dashboard's build:\n%s", page)
	}
}

// servedPage GETs url off handler and returns the body, failing on any status
// but 200 — an assertion about a page's content is worth nothing if the page
// was an error.
func servedPage(t *testing.T, handler http.Handler, url string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), "GET", url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", url, rec.Code)
	}
	return rec.Body.String()
}
