package runstatus

import (
	"strings"
	"testing"
)

// Every assertion here states PRESENCE. "The clause does not say RUNNING" is
// satisfied by a function that returns the empty string, so no test is allowed
// to rest on it: each one names the words that must be there, including — above
// all — at zero.

func TestInFlightCount_EmptySliceIsZero(t *testing.T) {
	if got := InFlightCount(); got != 0 {
		t.Errorf("InFlightCount() over nothing = %d, want 0", got)
	}
	if got := InFlightCount([]Status(nil)...); got != 0 {
		t.Errorf("InFlightCount(nil...) = %d, want 0", got)
	}
}

// TestInFlightCount_CountsTheTwoLiveValuesAndNothingElse pins the membership to
// Status.InFlight: PLANNING is alive (a planner call IS work in progress, the
// split ADR 0023 exists for), and ABANDONED is not, however open its leg looks.
func TestInFlightCount_CountsTheTwoLiveValuesAndNothingElse(t *testing.T) {
	for _, tc := range []struct {
		name     string
		statuses []Status
		want     int
	}{
		{"planning", []Status{Planning}, 1},
		{"running", []Status{Running}, 1},
		{"both live values", []Status{Planning, Running}, 2},
		{"abandoned is not alive", []Status{Abandoned}, 0},
		{"settled values are not alive", []Status{Pass, Fail, Paused}, 0},
		{"the underived zero value is not alive", []Status{Status(0)}, 0},
		{"a mixed corpus counts only the live ones", []Status{Running, Pass, Abandoned, Planning, Fail, Paused, Status(0)}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := InFlightCount(tc.statuses...); got != tc.want {
				t.Errorf("InFlightCount(%v) = %d, want %d", tc.statuses, got, tc.want)
			}
		})
	}
}

func TestAbandonedCount_CountsOnlyAbandoned(t *testing.T) {
	if got := AbandonedCount(); got != 0 {
		t.Errorf("AbandonedCount() over nothing = %d, want 0", got)
	}
	got := AbandonedCount(Running, Abandoned, Pass, Abandoned, Status(0))
	if got != 2 {
		t.Errorf("AbandonedCount() = %d, want 2", got)
	}
}

// TestInFlightClause_SaysZeroInWords is the hardest case and the reason this
// package gained a renderer at all: with nothing running, the answer must still
// be on screen, in a number.
func TestInFlightClause_SaysZeroInWords(t *testing.T) {
	got := InFlightClause()
	if !strings.Contains(got, "0 in flight") {
		t.Errorf("InFlightClause() over nothing = %q, want it to state 0 in flight", got)
	}

	got = InFlightClause(Pass, Fail, Paused)
	if !strings.Contains(got, "0 in flight") {
		t.Errorf("InFlightClause(settled runs) = %q, want it to state 0 in flight", got)
	}
}

func TestInFlightClause_NamesTheLiveCount(t *testing.T) {
	got := InFlightClause(Running, Pass, Planning, Fail)
	if !strings.Contains(got, "2 in flight") {
		t.Errorf("InFlightClause() = %q, want it to state 2 in flight", got)
	}
}

// TestInFlightClause_KeepsAbandonedOutOfTheLiveNumber is the decision stated as
// an assertion: an abandoned run is named, because it holds resources nobody is
// using, but it is never added to the number that answers "is anything running".
func TestInFlightClause_KeepsAbandonedOutOfTheLiveNumber(t *testing.T) {
	got := InFlightClause(Running, Abandoned, Abandoned, Pass)
	if !strings.Contains(got, "1 in flight") {
		t.Errorf("InFlightClause() = %q, want the live count to be 1, not 3", got)
	}
	if !strings.Contains(got, "2 abandoned") {
		t.Errorf("InFlightClause() = %q, want the abandoned runs named", got)
	}
}

// TestInFlightClause_IsSilentAboutAbandonedWhenThereAreNone keeps the clause
// short in the ordinary case: a reader who has no dead runs is told about none,
// so the words that DO appear always mean something happened.
func TestInFlightClause_IsSilentAboutAbandonedWhenThereAreNone(t *testing.T) {
	got := InFlightClause(Running, Pass)
	if !strings.Contains(got, "1 in flight") {
		t.Fatalf("InFlightClause() = %q, want it to state 1 in flight", got)
	}
	if strings.Contains(got, "abandoned") {
		t.Errorf("InFlightClause() = %q, must not mention abandoned runs when there are none", got)
	}
}
