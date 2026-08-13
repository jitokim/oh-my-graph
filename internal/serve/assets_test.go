package serve

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runstatus"
)

// The card's state token is a CONTRACT between four files that no compiler
// checks: card.go chooses the word, dashboard.css paints the tile's stripe with
// it, style.css owns the colour token and the header chip's dot, and
// dashboard.js decides whether a card carrying it is live and whether it gets a
// chip at all. Three of those four are text assets with no build step.
//
// Before these tests the agreement was held by comments. Renaming a token, or
// adding a seventh status, would leave a card with no border colour, no chip and
// no dot — and a green test run, because dashboard_test.go asserts the Go
// strings and never opens the assets. Worse, dashboard.js's LIVE_STATES is a
// hand-copy of runstatus.Status.InFlight(), the very predicate serve/resolve.go
// was rewritten to use BECAUSE an equality test silently drops a new in-flight
// value: Go was made safe and the page was left filing a future in-flight run
// under "settled".
//
// So the tests below derive everything from the Go side and read the assets
// back. No list here restates the enumeration; the one thing they hard-code is
// which asset is expected to say what.

// derivableStatuses is every value of the runstatus enumeration, taken from the
// enumeration itself rather than transcribed: the constants are `iota + 1` and
// String reports "UNKNOWN" for the zero value and for anything past the last
// one, so walking up from 1 until the word appears IS the closed set. A seventh
// status is therefore picked up by these tests on the commit that adds it.
func derivableStatuses(t *testing.T) []runstatus.Status {
	t.Helper()
	const sanityBound = 64 // a runaway guard; the set is single digits
	var all []runstatus.Status
	for s := runstatus.Status(1); s.String() != runstatus.Status(0).String(); s++ {
		all = append(all, s)
		if len(all) > sanityBound {
			t.Fatalf("the runstatus enumeration did not terminate within %d values — String's default arm "+
				"is what bounds this walk, so a value whose String does not fall through it breaks the "+
				"derivation these tests rest on", sanityBound)
		}
	}
	if len(all) == 0 {
		t.Fatal("runstatus has no derivable values: Status(1).String() already reports the zero value's word")
	}
	return all
}

// cardStateTokens is every value runCard.State can carry: one per derivable
// status, plus the two the card owns rather than derives — the `pending` it
// starts as for a directory that has said nothing (buildCard) and the `unknown`
// of a directory this binary refuses to read (brokenCard). Those two are named
// here because they are not statuses; everything else is derived.
func cardStateTokens(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{statePending: true, stateUnknown: true}
	for _, status := range derivableStatuses(t) {
		seen[runState(status)] = true
	}
	tokens := make([]string, 0, len(seen))
	for token := range seen {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	return tokens
}

// TestRunState_CoversEveryDerivableStatus makes runState's promise real. Its doc
// comment says a new status must be given a colour rather than silently
// inheriting one — but the function has a default arm, so without this test a
// seventh value would fall through it and render as `unknown` on the dashboard,
// caught only if some fixture happened to produce that status.
func TestRunState_CoversEveryDerivableStatus(t *testing.T) {
	byToken := map[string]runstatus.Status{}
	for _, status := range derivableStatuses(t) {
		token := runState(status)
		if token == stateUnknown {
			t.Errorf("runState(%v) fell through to %q: the status is derivable, so it is a real state a run "+
				"can be in, and the dashboard would paint it as a directory it cannot read. Give it a case "+
				"in runState, a token, and a rule in each asset TestCardStateTokens_AreDefinedByEveryAsset "+
				"checks", status, stateUnknown)
			continue
		}
		if other, dup := byToken[token]; dup {
			t.Errorf("runState(%v) and runState(%v) both return %q: two statuses sharing one token cannot be "+
				"told apart on a card, which is the conflation ADR 0023 exists to end", status, other, token)
		}
		byToken[token] = status
	}
}

// TestCardStateTokens_AreDefinedByEveryAsset holds each token to a rule in every
// asset that has to paint it.
func TestCardStateTokens_AreDefinedByEveryAsset(t *testing.T) {
	dashboardCSS := readAsset(t, "dashboard.css")
	styleCSS := readAsset(t, "style.css")
	dashboardJS := readAsset(t, "dashboard.js")

	vars := cssVarNames(styleCSS)
	cardRules := cssRuleBodies(dashboardCSS, "card")
	dotRules := cssRuleBodies(styleCSS, "dot")
	counted := jsStringList(t, dashboardJS, "COUNT_ORDER")

	for _, token := range cardStateTokens(t) {
		// The tile's left stripe. `pending` is the exception and an affirmative
		// one: it is the colour the base `.card` rule already carries, so a card
		// gets it by having no state class at all — asserted below rather than
		// waived here.
		if token != statePending {
			body, ok := cardRules[token]
			if !ok {
				t.Errorf("ui/dashboard.css has no `.card.%s` rule: a card in that state renders with the "+
					"default stripe, i.e. as if it had no state at all", token)
			} else {
				assertVarsDefined(t, "ui/dashboard.css", ".card."+token, body, vars)
			}
		}
		// The header chip's dot, shared with the map legend and the feed.
		body, ok := dotRules[token]
		if !ok {
			t.Errorf("ui/style.css has no `.dot.%s` rule: the header chip for that state renders an "+
				"invisible dot beside its count", token)
		} else {
			assertVarsDefined(t, "ui/style.css", ".dot."+token, body, vars)
		}
		// The header chip itself.
		if !counted[token] {
			t.Errorf("ui/dashboard.js's COUNT_ORDER omits %q: a run in that state is counted in no chip, so "+
				"the header's tally silently disagrees with the cards below it", token)
		}
	}

	// And nothing extra: a token left in COUNT_ORDER after the Go side stopped
	// producing it is a chip that can never appear, which reads to the next
	// person as a state that exists.
	for token := range counted {
		if !contains(cardStateTokens(t), token) {
			t.Errorf("ui/dashboard.js's COUNT_ORDER carries %q, which serve.runState no longer produces and "+
				"the card never sets", token)
		}
	}

	// `pending`'s affirmative half: the base rule IS its colour.
	base, ok := cssBaseRule(dashboardCSS, "card")
	if !ok {
		t.Fatalf("ui/dashboard.css has no bare `.card` rule; %q has no colour to inherit", statePending)
	}
	if !strings.Contains(base, "var(--"+statePending+")") {
		t.Errorf("ui/dashboard.css's base `.card` rule does not reference var(--%s). That rule is where a "+
			"card with no state class gets its stripe, which is the whole reason `.card.%s` does not exist",
			statePending, statePending)
	}
}

// TestLiveStates_AgreeWithTheInFlightPredicate is the one that matters most: the
// page's LIVE_STATES decides which group a card lands in, and it is a hand-copy
// of Status.InFlight(). resolve.go was rewritten from an equality test to that
// predicate precisely so a new in-flight value could not be dropped; this test
// is what extends the same protection to the page, which cannot import Go.
func TestLiveStates_AgreeWithTheInFlightPredicate(t *testing.T) {
	live := jsStringList(t, readAsset(t, "dashboard.js"), "LIVE_STATES")

	for _, status := range derivableStatuses(t) {
		token := runState(status)
		switch {
		case status.InFlight() && !live[token]:
			t.Errorf("%v is in flight but ui/dashboard.js's LIVE_STATES omits %q: the dashboard would file a "+
				"run that is happening right now under `settled`, and its elapsed clock would stop ticking",
				status, token)
		case !status.InFlight() && live[token]:
			t.Errorf("%v has settled but ui/dashboard.js's LIVE_STATES carries %q: the dashboard would keep it "+
				"in the working group and tick an elapsed clock on a run that is over", status, token)
		}
	}
	for token := range live {
		if !contains(cardStateTokens(t), token) {
			t.Errorf("ui/dashboard.js's LIVE_STATES carries %q, which is not a state any card can be in", token)
		}
	}
}

// --- reading the assets ------------------------------------------------------

// readAsset reads one embedded UI file — the same bytes the server hands the
// browser, so these tests cannot pass against a copy on disk that is not shipped.
func readAsset(t *testing.T, name string) string {
	t.Helper()
	src, err := uiFS.ReadFile("ui/" + name)
	if err != nil {
		t.Fatalf("reading the embedded ui/%s: %v", name, err)
	}
	return string(src)
}

// cssVarNames collects every custom property style.css DEFINES (`--name:`), so a
// rule referencing one that was renamed away is caught rather than rendering
// with no colour.
func cssVarNames(css string) map[string]bool {
	names := map[string]bool{}
	for _, m := range regexp.MustCompile(`--([a-z0-9-]+)\s*:`).FindAllStringSubmatch(css, -1) {
		names[m[1]] = true
	}
	return names
}

// cssRuleBodies maps `.<class>.<token> { … }` to its body, for the one-line
// state rules these assets are written in. The `{` must follow the token
// immediately, so a descendant selector (`.card.running .card-state`) is not
// mistaken for a state rule.
func cssRuleBodies(css, class string) map[string]string {
	re := regexp.MustCompile(`\.` + regexp.QuoteMeta(class) + `\.([a-z0-9-]+)\s*\{([^}]*)\}`)
	bodies := map[string]string{}
	for _, m := range re.FindAllStringSubmatch(css, -1) {
		bodies[m[1]] = m[2]
	}
	return bodies
}

// cssBaseRule is the same read for a bare `.<class> { … }` — no state token. It
// is where `pending` gets its colour, so it is asserted rather than assumed.
func cssBaseRule(css, class string) (string, bool) {
	m := regexp.MustCompile(`\.` + regexp.QuoteMeta(class) + `\s*\{([^}]*)\}`).FindStringSubmatch(css)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// assertVarsDefined fails when a rule paints itself with a custom property
// style.css does not define.
func assertVarsDefined(t *testing.T, file, selector, body string, defined map[string]bool) {
	t.Helper()
	for _, m := range regexp.MustCompile(`var\(--([a-z0-9-]+)\)`).FindAllStringSubmatch(body, -1) {
		if !defined[m[1]] {
			t.Errorf("%s's `%s` paints with var(--%s), which ui/style.css does not define — the rule applies "+
				"and colours nothing", file, selector, m[1])
		}
	}
}

// jsStringList reads a named top-level string list out of a JS asset — both
// shapes the page writes, `const NAME = [...]` and `const NAME = new Set([...])`
// — and returns it as a set. It is deliberately a text read: the page has no
// build step and no module system, so there is nothing to import, and a regex
// that fails to find the declaration fails the test rather than quietly
// asserting over an empty set.
func jsStringList(t *testing.T, js, name string) map[string]bool {
	t.Helper()
	re := regexp.MustCompile(fmt.Sprintf(`const %s = (?:new Set\()?\[([^\]]*)\]`, regexp.QuoteMeta(name)))
	m := re.FindStringSubmatch(js)
	if m == nil {
		t.Fatalf("ui/dashboard.js has no `const %s = [...]` declaration. These tests hold that literal to the "+
			"Go enumeration; if it was renamed or restructured, teach this reader the new shape rather than "+
			"dropping the guard", name)
	}
	items := map[string]bool{}
	for _, q := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(m[1], -1) {
		items[q[1]] = true
	}
	if len(items) == 0 {
		t.Fatalf("ui/dashboard.js's %s is empty", name)
	}
	return items
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
