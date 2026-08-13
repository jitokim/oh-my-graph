package runfeed

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeStream writes events as one JSON line each, at the current Schema, and
// returns the path — the shape a real StreamWriter leaves behind.
func writeStream(t *testing.T, events ...Event) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), FileName)
	var b strings.Builder
	for _, e := range events {
		e.Schema = Schema
		e.RunID = "r1"
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write stream: %v", err)
	}
	return path
}

// TestLastLeg_ReadsBothDirectionsOfThePhaseAffirmatively is ADR 0023 §2.3's
// core reading rule: PLANNING is "the latest run_started SAYS planning" and
// RUNNING is "the latest run_started says nothing" — never "no node has started
// yet". The committed-auto case is the one that would break under an
// absence-based reading: a stream carrying a planning open, its untagged open,
// and no node event at all must already read as the scheduler's leg.
func TestLastLeg_ReadsBothDirectionsOfThePhaseAffirmatively(t *testing.T) {
	for _, tc := range []struct {
		name        string
		events      []Event
		wantOpen    bool
		wantPhase   string
		wantOutcome string
	}{
		{
			name:      "planner call in progress",
			events:    []Event{{Type: EventRunStarted, Phase: PhasePlanning}},
			wantOpen:  true,
			wantPhase: PhasePlanning,
		},
		{
			name: "the plan was committed — no node has launched yet",
			events: []Event{
				{Type: EventRunStarted, Phase: PhasePlanning},
				{Type: EventRunStarted},
			},
			wantOpen:  true,
			wantPhase: "",
		},
		{
			name:      "a hand-written run's first instants carry no phase",
			events:    []Event{{Type: EventRunStarted}},
			wantOpen:  true,
			wantPhase: "",
		},
		{
			name: "a refused plan: the planning leg closes with failed, zero node events",
			events: []Event{
				{Type: EventRunStarted, Phase: PhasePlanning},
				{Type: EventRunFinished, Outcome: OutcomeFailed},
			},
			wantOpen: false,
			// Stale but unread: an open leg answers before the phase question,
			// so nothing consults Phase on a closed leg.
			wantPhase:   PhasePlanning,
			wantOutcome: OutcomeFailed,
		},
		{
			name: "a paused leg reports the outcome PAUSED is read off",
			events: []Event{
				{Type: EventRunStarted},
				{Type: EventNodeStarted, NodeID: "a"},
				{Type: EventRunFinished, Outcome: OutcomePaused},
			},
			wantOpen:    false,
			wantOutcome: OutcomePaused,
		},
		{
			name: "a resumed leg re-opens after a closed one",
			events: []Event{
				{Type: EventRunStarted},
				{Type: EventRunFinished, Outcome: OutcomePaused},
				{Type: EventRunStarted},
			},
			wantOpen:  true,
			wantPhase: "",
		},
		{
			name: "the LAST close wins over an earlier one",
			events: []Event{
				{Type: EventRunStarted},
				{Type: EventRunFinished, Outcome: OutcomePaused},
				{Type: EventRunStarted},
				{Type: EventRunFinished, Outcome: OutcomePassed},
			},
			wantOpen:    false,
			wantOutcome: OutcomePassed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			leg, err := LastLeg(writeStream(t, tc.events...))
			if err != nil {
				t.Fatalf("LastLeg: %v", err)
			}
			if leg.Open != tc.wantOpen {
				t.Errorf("Open = %v, want %v", leg.Open, tc.wantOpen)
			}
			if leg.Phase != tc.wantPhase {
				t.Errorf("Phase = %q, want %q", leg.Phase, tc.wantPhase)
			}
			if !tc.wantOpen && leg.LastOutcome != tc.wantOutcome {
				t.Errorf("LastOutcome = %q, want %q", leg.LastOutcome, tc.wantOutcome)
			}
		})
	}
}

// TestLastLeg_MissingStreamIsNoLegsAtAll keeps InFlight's own tolerance: a
// directory with no stream (pre-runfeed, or one whose first event has not
// landed) is not an error and has no legs — which is what lets a run directory
// that has said NOTHING keep the dashboard's `pending` rather than being
// derived into a status (ADR 0023 §2.1.1).
func TestLastLeg_MissingStreamIsNoLegsAtAll(t *testing.T) {
	leg, err := LastLeg(filepath.Join(t.TempDir(), FileName))
	if err != nil {
		t.Fatalf("LastLeg on a missing stream: %v, want nil", err)
	}
	if leg != (Leg{}) {
		t.Errorf("LastLeg on a missing stream = %+v, want the zero Leg", leg)
	}
}

// TestLastLeg_RefusesAStreamThisBinaryCannotRead is the other half: an
// unreadable stream is a fact about the READER, not about the run, so it stays
// an error the caller translates (a WARNING+skip row, an `unknown` card) rather
// than becoming a status.
func TestLastLeg_RefusesAStreamThisBinaryCannotRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	line := `{"schema":` + string(rune('0'+Schema+1)) + `,"event":"run_started"}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatalf("write stream: %v", err)
	}
	if _, err := LastLeg(path); err == nil {
		t.Fatal("LastLeg on a newer schema = nil error, want a refusal")
	}
}

// TestInFlight_StaysLastLegsAnswer pins that the contract's published reading
// did not change shape when LastLeg was carved out beneath it: InFlight is
// exactly Leg.Open, so an external consumer implementing "the last leg is open"
// still agrees with this binary — including through a planning phase, which is
// the free half of #163's fix for an unmodified consumer (ADR 0023 §2.3).
func TestInFlight_StaysLastLegsAnswer(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events []Event
		want   bool
	}{
		{"planning", []Event{{Type: EventRunStarted, Phase: PhasePlanning}}, true},
		{"planning then committed", []Event{{Type: EventRunStarted, Phase: PhasePlanning}, {Type: EventRunStarted}}, true},
		{"refused plan", []Event{{Type: EventRunStarted, Phase: PhasePlanning}, {Type: EventRunFinished, Outcome: OutcomeFailed}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeStream(t, tc.events...)
			got, err := InFlight(path)
			if err != nil {
				t.Fatalf("InFlight: %v", err)
			}
			if got != tc.want {
				t.Errorf("InFlight = %v, want %v", got, tc.want)
			}
			leg, err := LastLeg(path)
			if err != nil {
				t.Fatalf("LastLeg: %v", err)
			}
			if got != leg.Open {
				t.Errorf("InFlight = %v but Leg.Open = %v — they must be the one answer", got, leg.Open)
			}
		})
	}
}

// TestEvent_PhaseIsAdditiveAndOmitted pins the compatibility claim ADR 0023 §6
// rests on: the field is absent from every event that does not set it, so the
// bytes of a `run` or `resume` leg's stream are unchanged and no schema bump is
// owed. Schema itself must not have moved.
func TestEvent_PhaseIsAdditiveAndOmitted(t *testing.T) {
	if Schema != 2 {
		t.Fatalf("Schema = %d, want 2 — ADR 0023 adds an optional field and bumps nothing", Schema)
	}
	line, err := json.Marshal(Event{Schema: Schema, Type: EventRunStarted})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(line), "phase") {
		t.Errorf("an unphased run_started encodes as %s — the field must be omitempty", line)
	}
	line, err = json.Marshal(Event{Schema: Schema, Type: EventRunStarted, Phase: PhasePlanning})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(line), `"phase":"planning"`) {
		t.Errorf("a planning run_started encodes as %s, want a phase:planning key", line)
	}
}

// TestLastLeg_ATerminalEventThatClosesNoLegSaysNothing pins the reading a
// stray run_finished gets: only a terminal event that actually closes an OPEN
// leg carries a verdict. A duplicate finish must not overwrite the outcome the
// real one recorded, and a lone finish — damage the contract already refuses to
// call a closed leg — must not supply an outcome at all.
func TestLastLeg_ATerminalEventThatClosesNoLegSaysNothing(t *testing.T) {
	for _, tc := range []struct {
		name        string
		events      []Event
		wantOutcome string
	}{
		{
			name: "a duplicate finish does not rewrite the verdict",
			events: []Event{
				{Type: EventRunStarted},
				{Type: EventRunFinished, Outcome: OutcomePassed},
				{Type: EventRunFinished, Outcome: OutcomePaused},
			},
			wantOutcome: OutcomePassed,
		},
		{
			name:        "a lone finish carries no outcome",
			events:      []Event{{Type: EventRunFinished, Outcome: OutcomePaused}},
			wantOutcome: "",
		},
		{
			// The legitimate two-leg stream an auto run writes: each finish
			// closes its own open leg, so the second one DOES answer.
			name: "the second leg of an auto run still answers",
			events: []Event{
				{Type: EventRunStarted, Phase: PhasePlanning},
				{Type: EventRunFinished, Outcome: OutcomeFailed},
				{Type: EventRunStarted},
				{Type: EventRunFinished, Outcome: OutcomePassed},
			},
			wantOutcome: OutcomePassed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			leg, err := LastLeg(writeStream(t, tc.events...))
			if err != nil {
				t.Fatalf("LastLeg: %v", err)
			}
			if leg.Open {
				t.Errorf("leg reads open after a terminal event: %+v", leg)
			}
			if leg.LastOutcome != tc.wantOutcome {
				t.Errorf("LastOutcome = %q, want %q (leg = %+v)", leg.LastOutcome, tc.wantOutcome, leg)
			}
		})
	}
}

// TestLastLeg_StartedSeparatesASilentStreamFromAClosedLeg pins the fact the
// status layer's "has this directory said anything" question rests on
// (runstatus.Spoken). Open answers whether the LAST leg is still open; Started
// answers whether there was ever a leg — and the two disagree on exactly the
// stream that is damage: a run_finished with no run_started before it, which
// the contract does not call a closed leg.
func TestLastLeg_StartedSeparatesASilentStreamFromAClosedLeg(t *testing.T) {
	for _, tc := range []struct {
		name        string
		events      []Event
		wantStarted bool
	}{
		{name: "an empty stream", wantStarted: false},
		{
			name:        "a close with no open before it",
			events:      []Event{{Type: EventRunFinished, Outcome: OutcomePassed}},
			wantStarted: false,
		},
		{
			name:        "an open leg",
			events:      []Event{{Type: EventRunStarted}},
			wantStarted: true,
		},
		{
			name: "a leg that opened and closed",
			events: []Event{
				{Type: EventRunStarted, Phase: PhasePlanning},
				{Type: EventRunFinished, Outcome: OutcomeFailed},
			},
			wantStarted: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			leg, err := LastLeg(writeStream(t, tc.events...))
			if err != nil {
				t.Fatalf("LastLeg: %v", err)
			}
			if leg.Started != tc.wantStarted {
				t.Errorf("Started = %v, want %v (leg = %+v)", leg.Started, tc.wantStarted, leg)
			}
		})
	}
}
