package serve

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
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

// --- head <meta> tags: the machine-readable form of the same Build ----------
//
// The properties below are checked against BUILD.GO'S OWN newBuild — the same
// constructor CurrentBuild calls — never against a literal copied from a
// template or a report: a test that hardcoded "9.9.9" or a commit string
// would pass against a page naming the wrong build, which is exactly the
// failure this feature exists to catch.

// metaContent extracts the content attribute of ONE <meta name="..."> tag
// from a served page's body. The regexp is anchored to name="..." BEFORE
// content="...", the exact order both templates emit, so a template that
// reordered the attributes would stop matching rather than silently keep
// passing. ok is false when the tag is not present AT ALL, distinct from a
// tag present with empty content — the two are different failures this
// feature is built to tell apart. The captured content is run through
// html.UnescapeString, because html/template escapes an RFC3339 offset's '+'
// as "&#43;" in attribute context; a caller comparing against a Build field's
// own value should not have to know that.
func metaContent(t *testing.T, page, name string) (content string, ok bool) {
	t.Helper()
	pattern := `<meta name="` + regexp.QuoteMeta(name) + `" content="([^"]*)">`
	match := regexp.MustCompile(pattern).FindStringSubmatch(page)
	if match == nil {
		return "", false
	}
	return html.UnescapeString(match[1]), true
}

// buildMetaSurface is one page a served build must state its <meta> tags on.
type buildMetaSurface struct {
	name string
	// page renders build through the surface and returns the served body,
	// built the same way the rest of this suite builds a handler for that
	// surface (newTestServer / newTestDashboard, servedPage).
	page func(t *testing.T, build Build) string
}

// buildMetaSurfaces is the ONE table every property below drives its
// assertions from, one subtest per entry with the SAME check function — so
// "the dashboard carries the tags and the run view does not" (or the
// reverse) turns exactly the surfaces built from the half that regressed
// red, and the other surfaces stay green, pointing at the offending
// template. The single-run view is covered both standalone (serve <run-id>)
// and mounted under the dashboard's /run/<id>/ (a card click), since the
// mounted path is how a real reader reaches it and is a distinct render call
// (Dashboard.serverFor → Server.handleIndex) from the standalone one.
var buildMetaSurfaces = []buildMetaSurface{
	{
		name: "dashboard index (dashboard.html)",
		page: func(t *testing.T, build Build) string {
			root := runsRootWith(t, "run-1")
			handler := newTestDashboard(root).WithBuild(build).Handler()
			return servedPage(t, handler, "http://127.0.0.1:8642/")
		},
	},
	{
		name: "single-run view, standalone (index.html)",
		page: func(t *testing.T, build Build) string {
			dir := t.TempDir()
			handler := newTestServer(dir, "run-1").WithBuild(build).Handler()
			return servedPage(t, handler, "http://127.0.0.1:8642/")
		},
	},
	{
		name: "single-run view, mounted under the dashboard (index.html via /run/<id>/)",
		page: func(t *testing.T, build Build) string {
			root := runsRootWith(t, "run-1")
			handler := newTestDashboard(root).WithBuild(build).Handler()
			return servedPage(t, handler, "http://127.0.0.1:8642/run/run-1/")
		},
	},
}

// testBuildInstant and testBuildVersion are fed straight into newBuild — the
// same constructor CurrentBuild calls — so every assertion below is a
// field-for-field comparison against build.go's own rendering of this exact
// input, not a second, hand-copied spelling of what that rendering should be.
var testBuildInstant = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

const testBuildVersion = "9.9.9-meta-test"

// TestPageMeta_VersionRevisionBuiltAtMatchBuildGo pins the three atoms named
// in the CHANGELOG entry — omg-version, omg-revision, omg-built-at — each
// checked against the SAME Build value handed to WithBuild, on all three
// surfaces. Deleting any one of the three <meta> lines from either template
// turns every surface built from that template red on that one tag, and
// deleting a whole template's block turns every tag red on that template's
// surfaces — while leaving the other template's surfaces green, which is
// what would catch "the dashboard has it and the run view does not" (or the
// reverse) rather than passing on either half alone.
func TestPageMeta_VersionRevisionBuiltAtMatchBuildGo(t *testing.T) {
	want := newBuild(testBuildVersion, testBuildInstant)
	if want.Revision == "" && want.BuiltAt == "" {
		t.Fatal("test fixture produced a Build with nothing but the version; sharpen the fixture rather than the assertions below")
	}

	tags := []struct {
		name string
		want string
	}{
		{"omg-version", want.Version},
		{"omg-revision", want.Revision},
		{"omg-built-at", want.BuiltAt},
	}

	for _, surface := range buildMetaSurfaces {
		t.Run(surface.name, func(t *testing.T) {
			page := surface.page(t, want)
			for _, tag := range tags {
				content, ok := metaContent(t, page, tag.name)
				if !ok {
					t.Fatalf("%s: no <meta name=%q> tag at all", surface.name, tag.name)
				}
				if content != tag.want {
					t.Errorf("%s: <meta name=%q content=%q>, want %q — build.go's own newBuild(%q, …) for this same input", surface.name, tag.name, content, tag.want, testBuildVersion)
				}
			}
		})
	}
}

// TestPageMeta_UnknownAtomIsAnEmptyTagNotAnAbsentOne pins the other half of
// the contract the CHANGELOG entry states: an atom the process could not
// establish (no VCS stamp, an executable that could not be stat'd) renders
// as a <meta> tag with EMPTY content, never as an omitted tag — because an
// absent tag has to mean one thing only: a server that predates this change.
// A template that dropped an always-emit tag under some Build value (an
// `if`/`with` guarding it) would collapse that distinction and fail here
// even though TestPageMeta_VersionRevisionBuiltAtMatchBuildGo above,
// exercised with a fully-populated Build, would not catch it.
func TestPageMeta_UnknownAtomIsAnEmptyTagNotAnAbsentOne(t *testing.T) {
	unknown := Build{Version: "9.9.9", Revision: "", BuiltAt: "", Label: "v9.9.9"}
	for _, surface := range buildMetaSurfaces {
		t.Run(surface.name, func(t *testing.T) {
			page := surface.page(t, unknown)
			for _, name := range []string{"omg-revision", "omg-built-at"} {
				content, ok := metaContent(t, page, name)
				if !ok {
					t.Fatalf("%s: no <meta name=%q> tag at all — a missing detail must render as an EMPTY tag, since an absent tag has to mean a server that predates this change", surface.name, name)
				}
				if content != "" {
					t.Errorf("%s: <meta name=%q content=%q>, want empty content for this Build's unset field", surface.name, name, content)
				}
			}
			if content, ok := metaContent(t, page, "omg-version"); !ok || content != unknown.Version {
				t.Errorf("%s: omg-version = (%q, present=%v), want (%q, true)", surface.name, content, ok, unknown.Version)
			}
		})
	}
}

// TestPageMeta_BuiltAtParsesAsRFC3339 pins the one property that makes
// omg-built-at usable for ordering two servers rather than just for display:
// it must parse as RFC3339, and the parsed instant must be the one build.go
// was given — a scraper that has to unescape "&#43;" before time.Parse (the
// documented html/template attribute-escaping wrinkle) still reaches the same
// instant.
func TestPageMeta_BuiltAtParsesAsRFC3339(t *testing.T) {
	want := newBuild(testBuildVersion, testBuildInstant)
	if want.BuiltAt == "" {
		t.Fatal("test fixture instant produced an empty BuiltAt; the fixture itself is broken")
	}
	for _, surface := range buildMetaSurfaces {
		t.Run(surface.name, func(t *testing.T) {
			page := surface.page(t, want)
			content, ok := metaContent(t, page, "omg-built-at")
			if !ok {
				t.Fatalf("%s: no omg-built-at tag at all", surface.name)
			}
			parsed, err := time.Parse(time.RFC3339, content)
			if err != nil {
				t.Fatalf("%s: omg-built-at = %q does not parse as RFC3339: %v", surface.name, content, err)
			}
			if !parsed.Equal(testBuildInstant) {
				t.Errorf("%s: parsed omg-built-at = %v, want the instant build.go was given, %v", surface.name, parsed, testBuildInstant)
			}
		})
	}
}

// TestPageMeta_HumanLabelStillMatchesBuildGo pins that the footer's prose is
// untouched by the head gaining these tags: it must still be exactly what
// newBuild rendered for the same Build — the label and the atoms are one
// value (build.go's own doc comment), so a regression that made the label a
// second, disagreeing rendering must fail here on the same table the atoms
// are checked against.
func TestPageMeta_HumanLabelStillMatchesBuildGo(t *testing.T) {
	want := newBuild(testBuildVersion, testBuildInstant)
	for _, surface := range buildMetaSurfaces {
		t.Run(surface.name, func(t *testing.T) {
			page := surface.page(t, want)
			if !strings.Contains(page, want.Label) {
				t.Errorf("%s: page body does not contain %q, build.go's own label for this same input", surface.name, want.Label)
			}
		})
	}
}
