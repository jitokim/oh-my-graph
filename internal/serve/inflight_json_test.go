package serve

import (
	"strings"
	"testing"
)

// The CLI now states how many runs are in flight in a sentence, on three human
// surfaces. This is the fence around that: the JSON a machine reads must not
// grow the sentence with it. A card carries the run's state as a TOKEN — the
// consumer counts them itself, as internal/serve/ui/dashboard.js does through
// LIVE_STATES — and a corpus-wide count, if one is ever wanted here, arrives as
// a numeric field, never as prose.
//
// The assertion is deliberately two-sided: it names the token that must be
// there (so it cannot pass on an endpoint that returned nothing) and then the
// prose that must not.
func TestAPICards_CarryTheStateTokenAndNoHumanInFlightProse(t *testing.T) {
	root := runsRootWith(t, "run-live", "run-done")
	seedInFlightRun(t, root, "run-live")
	seedSettledRun(t, root, "run-done")

	rec := get(t, newTestDashboard(root), "/api/cards")
	if rec.Code != 200 {
		t.Fatalf("GET /api/cards status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	// Present: the machine's own answer, one token per run.
	if !strings.Contains(body, `"state":"`+stateRunning+`"`) {
		t.Errorf("the live run's state token must be in the payload: %s", body)
	}
	if !strings.Contains(body, `"state":"`+statePassed+`"`) {
		t.Errorf("the settled run's state token must be in the payload: %s", body)
	}

	// Absent: the human wording, which belongs to the CLI surfaces only.
	for _, prose := range []string{"in flight", "abandoned;", "run(s) shown"} {
		if strings.Contains(body, prose) {
			t.Errorf("the card payload gained human prose %q: %s", prose, body)
		}
	}
}
