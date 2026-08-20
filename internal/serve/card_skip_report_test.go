package serve

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runstatus"
)

// This file tests the dashboard's half of the skip-reporting change:
// buildCard's Error stays the per-run sentence (unchanged), but the card also
// now carries ErrorCode — the ONE thing the payload gains — through the same
// classifier (runstatus.ReasonOf) the CLI's collapse uses, so the page and
// `runs list` can never disagree about which runs are the same kind of
// broken. It also holds the page's own collapsed grouping (ui/dashboard.js,
// ui/dashboard.html, ui/dashboard.css) to the Go side, at the level a Go test
// can reach without a browser: the elements and grouping logic are present,
// statically.

// --- buildCard: the machine-readable code --------------------------------

// TestBuildCard_SchemaMismatchCarriesIncompatibleSnapshotErrorCode pins the
// one error shape that is eligible to collapse on the page, exactly as it is
// in the CLI's SkipReport.
func TestBuildCard_SchemaMismatchCarriesIncompatibleSnapshotErrorCode(t *testing.T) {
	root := runsRootWith(t, "run-old-schema")
	if err := os.WriteFile(filepath.Join(root, "run-old-schema", stateFileName), []byte(`{"schema":2}`), 0o644); err != nil {
		t.Fatalf("write schema-mismatch snapshot: %v", err)
	}

	card := buildCard(root, "run-old-schema")
	if card.State != stateUnknown {
		t.Fatalf("card.State = %q, want %q", card.State, stateUnknown)
	}
	if card.Error == "" {
		t.Errorf("card.Error is empty, want the per-run sentence naming the schema mismatch")
	}
	if !strings.Contains(card.Error, "schema version 2") {
		t.Errorf("card.Error = %q, want it to name the found schema", card.Error)
	}
	if card.ErrorCode != runstatus.ReasonIncompatibleSnapshot {
		t.Errorf("card.ErrorCode = %q, want %q", card.ErrorCode, runstatus.ReasonIncompatibleSnapshot)
	}
}

// TestBuildCard_CorruptSnapshotCarriesUnreadableErrorCode pins the flat side:
// a corrupt snapshot is a different reason, and it must classify as the ONE
// unreadable code rather than being lumped in with, or mistaken for, an
// incompatible schema.
func TestBuildCard_CorruptSnapshotCarriesUnreadableErrorCode(t *testing.T) {
	root := runsRootWith(t, "run-corrupt")
	if err := os.WriteFile(filepath.Join(root, "run-corrupt", stateFileName), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt snapshot: %v", err)
	}

	card := buildCard(root, "run-corrupt")
	if card.State != stateUnknown {
		t.Fatalf("card.State = %q, want %q", card.State, stateUnknown)
	}
	if card.Error == "" {
		t.Errorf("card.Error is empty, want the per-run decode failure")
	}
	if card.ErrorCode != runstatus.ReasonUnreadable {
		t.Errorf("card.ErrorCode = %q, want %q", card.ErrorCode, runstatus.ReasonUnreadable)
	}
	if card.ErrorCode == runstatus.ReasonIncompatibleSnapshot {
		t.Errorf("card.ErrorCode = %q, a decode failure must not be mistaken for a schema mismatch", card.ErrorCode)
	}
}

// TestRunCard_ErrorCodeOmittedWhenThereIsNoError is the paired negative
// control: a healthy card's JSON must not carry an `error_code` key at all
// (omitempty), so a consumer can test for the key's presence as "this run is
// broken" without also checking the code's value.
func TestRunCard_ErrorCodeOmittedWhenThereIsNoError(t *testing.T) {
	encoded, err := json.Marshal(runCard{RunID: "run-1", State: statePassed})
	if err != nil {
		t.Fatalf("marshal run card: %v", err)
	}
	if strings.Contains(string(encoded), "error_code") {
		t.Errorf("encoded card = %s, want no error_code key on a card with no error", encoded)
	}
}

// TestRunCard_ErrorCodeIsPresentAndCorrectWhenMarshaled is the paired
// positive control: a broken card's JSON DOES carry the code, spelled exactly
// as runstatus.SkipReason renders it, so the page's `error_code` grouping has
// something to key on.
func TestRunCard_ErrorCodeIsPresentAndCorrectWhenMarshaled(t *testing.T) {
	card := runCard{RunID: "run-1", State: stateUnknown, Error: "boom", ErrorCode: runstatus.ReasonIncompatibleSnapshot}
	encoded, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal run card: %v", err)
	}
	if !strings.Contains(string(encoded), `"error_code":"incompatible_snapshot"`) {
		t.Errorf("encoded card = %s, want the error_code field spelled out", encoded)
	}
}

// --- /api/cards: the machine-readable surface carries the code, not prose --

// TestDashboard_APICardsCarriesStructuredCodeAndDecodesWithEveryRun is the
// machine-readable-surface requirement: /api/cards must carry the structured
// skip data (error_code) and must NOT carry the CLI's human collapse
// sentence, paired with the positive control that the body still decodes as
// JSON and names every run, broken and healthy alike — a broken card is never
// dropped from the list, only marked.
func TestDashboard_APICardsCarriesStructuredCodeAndDecodesWithEveryRun(t *testing.T) {
	root := runsRootWith(t, "run-old-schema", "run-good")
	if err := os.WriteFile(filepath.Join(root, "run-old-schema", stateFileName), []byte(`{"schema":2}`), 0o644); err != nil {
		t.Fatalf("write schema-mismatch snapshot: %v", err)
	}
	seedSettledRun(t, root, "run-good")

	rec := get(t, newTestDashboard(root), "/api/cards")
	if rec.Code != 200 {
		t.Fatalf("GET /api/cards status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	// Absence: none of the CLI's human collapse wording ever reaches this
	// machine surface.
	for _, forbidden := range []string{
		"could not be read and are not shown",
		"names them one by one",
		"WARNING:",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("/api/cards body contains %q, a machine surface must carry the code, not the CLI's prose:\n%s", forbidden, body)
		}
	}

	// Presence, paired: the body decodes as JSON and carries both runs.
	var cards []runCard
	if err := json.Unmarshal(rec.Body.Bytes(), &cards); err != nil {
		t.Fatalf("/api/cards body does not decode as JSON: %v (%s)", err, body)
	}
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2 (one broken, one healthy)", len(cards))
	}
	broken := cardByID(t, cards, "run-old-schema")
	if broken.ErrorCode != runstatus.ReasonIncompatibleSnapshot {
		t.Errorf("broken card ErrorCode = %q, want %q", broken.ErrorCode, runstatus.ReasonIncompatibleSnapshot)
	}
	good := cardByID(t, cards, "run-good")
	if good.State != statePassed {
		t.Errorf("healthy card state = %q, want %q — it must not be affected by its broken sibling", good.State, statePassed)
	}
}

// --- the page's own collapsed grouping, checked statically ------------------

// TestDashboardJS_UnreadableWhyCoversEverySkipReason holds ui/dashboard.js's
// UNREADABLE_WHY prose map to the Go side's set of reason codes, the same way
// TestCardStateTokens_AreDefinedByEveryAsset holds COUNT_ORDER to the state
// enumeration: a reason with no entry here still groups (paintUnreadable's
// fallback), but silently loses its explanatory sentence.
func TestDashboardJS_UnreadableWhyCoversEverySkipReason(t *testing.T) {
	js := readAsset(t, "dashboard.js")
	loc := strings.Index(js, "UNREADABLE_WHY")
	if loc == -1 {
		t.Fatalf("ui/dashboard.js has no UNREADABLE_WHY declaration")
	}
	// The object body: from the declaration to the closing `};` that ends it.
	end := strings.Index(js[loc:], "};")
	if end == -1 {
		t.Fatalf("ui/dashboard.js's UNREADABLE_WHY object is never closed")
	}
	block := js[loc : loc+end]

	for _, reason := range []runstatus.SkipReason{runstatus.ReasonIncompatibleSnapshot, runstatus.ReasonUnreadable} {
		key := string(reason)
		if !strings.Contains(block, key+":") {
			t.Errorf("ui/dashboard.js's UNREADABLE_WHY has no entry for %q — a run skipped for this reason "+
				"would group correctly but carry no explanatory sentence", key)
		}
	}
}

// TestDashboardJS_PaintUnreadableGroupsByErrorCodeNotByRun pins the page's own
// collapse: paintUnreadable must key its counts on `error_code` — the same
// field buildCard sets and the CLI's SkipReport groups on — rather than
// printing one summary line per card, which is the per-run spam this whole
// change replaces.
func TestDashboardJS_PaintUnreadableGroupsByErrorCodeNotByRun(t *testing.T) {
	body := jsCodeOnly(jsFunctionBody(t, readAsset(t, "dashboard.js"), "paintUnreadable"))

	if !strings.Contains(body, "card.error_code") {
		t.Errorf("paintUnreadable() = %q, want it to group on card.error_code", body)
	}
	if !strings.Contains(body, "unreadable-count") || !strings.Contains(body, "group.length") {
		t.Errorf("paintUnreadable() = %q, want the section header's count set from the group's total length", body)
	}
	if !strings.Contains(body, "unreadable-why") {
		t.Errorf("paintUnreadable() = %q, want the collapsed per-reason note to be populated", body)
	}
	// The per-card tiles are still drawn underneath the collapsed note — the
	// collapse replaces the WALL OF TEXT, not the cards themselves, each of
	// which still carries its own `.card-error` (cardEl, checked separately).
	if !strings.Contains(body, "unreadable-cards") {
		t.Errorf("paintUnreadable() = %q, want the individual cards still rendered below the summary", body)
	}
}

// TestDashboardJS_CardStillCarriesItsOwnErrorText is the paired control for
// the test above: collapsing the SECTION summary must not remove each card's
// own per-run error line — that stays exactly as it was, the same way
// `runs list --verbose` still names every run.
func TestDashboardJS_CardStillCarriesItsOwnErrorText(t *testing.T) {
	body := jsCodeOnly(jsFunctionBody(t, readAsset(t, "dashboard.js"), "cardEl"))
	if !strings.Contains(body, "card.error") || !strings.Contains(body, "card-error") {
		t.Errorf("cardEl() = %q, want each card to still render its own card.error text", body)
	}
}

// TestDashboardHTML_HasTheCollapsedUnreadableSection pins the markup the
// script above paints into: a details/summary section, hidden by default (no
// runs are unreadable until proven otherwise), with the count, the per-reason
// note host, and the card host all present.
func TestDashboardHTML_HasTheCollapsedUnreadableSection(t *testing.T) {
	html := readAsset(t, "dashboard.html")
	for _, want := range []string{
		`id="unreadable-section"`,
		`id="unreadable-count"`,
		`id="unreadable-why"`,
		`id="unreadable-cards"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("ui/dashboard.html is missing %s, which ui/dashboard.js's paintUnreadable writes into", want)
		}
	}
	if !strings.Contains(html, `<details id="unreadable-section" hidden>`) {
		t.Errorf("ui/dashboard.html's unreadable section must start hidden — an empty accusation is worse than none")
	}
}

// TestDashboardCSS_HasTheSectionWhyRule pins the one new style rule the
// collapsed per-reason note renders with.
func TestDashboardCSS_HasTheSectionWhyRule(t *testing.T) {
	css := readAsset(t, "dashboard.css")
	if !strings.Contains(css, ".section-why") {
		t.Errorf("ui/dashboard.css has no .section-why rule, which ui/dashboard.js's paintUnreadable applies " +
			"to every per-reason note it writes")
	}
}
