package serve

import (
	"regexp"
	"strings"
	"testing"
)

// homeLinkHref extracts the href of the header's home-link anchor from a
// served page's body, the same way metaContent (build_test.go) extracts one
// <meta> tag's content: anchored to the exact attribute order index.html
// emits (class before href), so a template that reordered them would stop
// matching rather than silently keep passing. ok is false when the anchor is
// not present at all.
func homeLinkHref(t *testing.T, page string) (href string, ok bool) {
	t.Helper()
	match := regexp.MustCompile(`<a class="home-link" href="([^"]*)"`).FindStringSubmatch(page)
	if match == nil {
		return "", false
	}
	return match[1], true
}

// TestDashboard_MountedRunOffersTheWayHomeAndItPointsAtTheDashboard covers the
// DASHBOARD mount (`oh-my-graph serve`): a run's live view, reached under
// /run/<id>/, must offer a way back — and that way must actually lead to the
// dashboard root, not back to the page it is already on. Asserting that an
// anchor merely exists would pass a naive fix that linked the run page to
// itself (e.g. href="/run/run-live/" or a relative "."); the href is checked
// against the dashboard root exactly, and separately against the run id, to
// catch that.
func TestDashboard_MountedRunOffersTheWayHomeAndItPointsAtTheDashboard(t *testing.T) {
	root := runsRootWith(t, "run-live")
	handler := newTestDashboard(root).Handler()
	page := servedPage(t, handler, "http://127.0.0.1:8642/run/run-live/")

	href, ok := homeLinkHref(t, page)
	if !ok {
		t.Fatalf("mounted run view carries no home-link anchor at all:\n%s", page)
	}
	if href != "/" {
		t.Errorf("home-link href = %q, want %q — the dashboard root, not the page this link is rendered on", href, "/")
	}
	if strings.Contains(href, "run-live") {
		t.Errorf("home-link href = %q names the run it is rendered on; the way home must not point back at the page you are already on", href)
	}
}

// TestIndex_StandaloneOffersNoWayHomeBecauseThereIsNoDashboard covers the
// STANDALONE mount (`oh-my-graph serve <run-id>`): the run view IS the whole
// site, so it must offer no home link at all — a link to "/" there would be a
// link to the very page it is on, which is the naive fix this design
// explicitly rejects. Checked by the absence of the whole control (its class
// name), not just of a particular href, so a stray anchor without that class
// would not hide a regression.
func TestIndex_StandaloneOffersNoWayHomeBecauseThereIsNoDashboard(t *testing.T) {
	dir := t.TempDir()
	handler := newTestServer(dir, "run-1").Handler()
	page := servedPage(t, handler, "http://127.0.0.1:8642/")

	if strings.Contains(page, "home-link") {
		t.Errorf("standalone run view carries a home-link control, but serve <run-id> has no dashboard to go home to:\n%s", page)
	}
	if _, ok := homeLinkHref(t, page); ok {
		t.Error("standalone run view carries a home-link anchor; serve <run-id> has no dashboard to go home to")
	}
}
