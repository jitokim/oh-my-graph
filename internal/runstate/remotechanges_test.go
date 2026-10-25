package runstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var (
	shaBase = strings.Repeat("1", 40)
	shaRun  = strings.Repeat("2", 40)
	shaThem = strings.Repeat("3", 40)
)

var origin = RemoteConfig{Name: "origin", FetchURL: "/srv/origin.git", PushURL: "/srv/origin.git"}

// observed builds a snapshot with both parts observed.
func observed(remotes []RemoteConfig, refs ...RemoteBranchRef) RemoteSnapshot {
	return RemoteSnapshot{
		Remotes:  RemotesSnapshot{Status: RemotePartObserved, Configured: remotes},
		Branches: BranchesSnapshot{Status: RemotePartObserved, Refs: refs},
	}
}

func ref(remote, branch, sha string) RemoteBranchRef {
	return RemoteBranchRef{Remote: remote, Branch: branch, SHA: sha}
}

// runCommitIs is a §5.1 stand-in: exactly the named shas pass.
func runCommitIs(shas ...string) func(string) bool {
	return func(sha string) bool {
		for _, s := range shas {
			if s == sha {
				return true
			}
		}
		return false
	}
}

func TestDiffRemoteSnapshots_NoChangeSaysWhatWasObserved(t *testing.T) {
	snap := observed([]RemoteConfig{origin}, ref("origin", "main", shaBase))
	got := DiffRemoteSnapshots(snap, snap, runCommitIs(shaBase))
	want := RemoteDiff{
		Observed:    []RemotePart{RemotePartRemotes, RemotePartBranches},
		Entries:     []RemoteChangeEntry{},
		NotCompared: []RemoteNotCompared{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diff = %+v, want %+v", got, want)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"observed":["remotes","branches"],"entries":[],"not_compared":[]}` {
		t.Errorf("an empty diff encodes as %s; it must spell out all three lists", data)
	}
}

func TestDiffRemoteSnapshots_EachKind(t *testing.T) {
	mirror := RemoteConfig{Name: "mirror", FetchURL: "/srv/m.git", PushURL: "/srv/m.git"}
	moved := RemoteConfig{Name: "fork", FetchURL: "/srv/fork.git", PushURL: "/srv/fork.git"}
	movedPush := moved
	movedPush.PushURL = "/srv/fork-push.git"

	cases := []struct {
		name          string
		before, after RemoteSnapshot
		isRunCommit   func(string) bool
		want          []RemoteChangeEntry
	}{
		{
			name:   "remote_added",
			before: observed([]RemoteConfig{origin}),
			after:  observed([]RemoteConfig{mirror, origin}),
			want: []RemoteChangeEntry{{Kind: RemoteAdded, Remote: "mirror",
				NewFetchURL: "/srv/m.git", NewPushURL: "/srv/m.git", Attribution: AttributionNotAttributable}},
		},
		{
			name:   "remote_removed",
			before: observed([]RemoteConfig{mirror, origin}),
			after:  observed([]RemoteConfig{origin}),
			want: []RemoteChangeEntry{{Kind: RemoteRemoved, Remote: "mirror",
				OldFetchURL: "/srv/m.git", OldPushURL: "/srv/m.git", Attribution: AttributionNotAttributable}},
		},
		{
			name:   "remote_url_changed on the push URL",
			before: observed([]RemoteConfig{moved}),
			after:  observed([]RemoteConfig{movedPush}),
			want: []RemoteChangeEntry{{Kind: RemoteURLChanged, Remote: "fork",
				OldFetchURL: "/srv/fork.git", NewFetchURL: "/srv/fork.git",
				OldPushURL: "/srv/fork.git", NewPushURL: "/srv/fork-push.git", Attribution: AttributionNotAttributable}},
		},
		{
			name:        "branch_created at a run commit",
			before:      observed([]RemoteConfig{origin}, ref("origin", "main", shaBase)),
			after:       observed([]RemoteConfig{origin}, ref("origin", "feat/x", shaRun), ref("origin", "main", shaBase)),
			isRunCommit: runCommitIs(shaRun),
			want: []RemoteChangeEntry{{Kind: BranchCreated, Remote: "origin", Branch: "feat/x",
				NewSHA: shaRun, Attribution: AttributionRunCommit}},
		},
		{
			name:        "branch_created at a commit the run did not make",
			before:      observed([]RemoteConfig{origin}, ref("origin", "main", shaBase)),
			after:       observed([]RemoteConfig{origin}, ref("origin", "feat/y", shaThem), ref("origin", "main", shaBase)),
			isRunCommit: runCommitIs(shaRun),
			want: []RemoteChangeEntry{{Kind: BranchCreated, Remote: "origin", Branch: "feat/y",
				NewSHA: shaThem, Attribution: AttributionNotAttributable}},
		},
		{
			name:        "branch_moved carries both shas and its label",
			before:      observed([]RemoteConfig{origin}, ref("origin", "main", shaBase)),
			after:       observed([]RemoteConfig{origin}, ref("origin", "main", shaRun)),
			isRunCommit: runCommitIs(shaRun),
			want: []RemoteChangeEntry{{Kind: BranchMoved, Remote: "origin", Branch: "main",
				OldSHA: shaBase, NewSHA: shaRun, Attribution: AttributionRunCommit}},
		},
		{
			name:        "branch_deleted has no new sha, so is never attributable",
			before:      observed([]RemoteConfig{origin}, ref("origin", "feat/x", shaRun), ref("origin", "main", shaBase)),
			after:       observed([]RemoteConfig{origin}, ref("origin", "main", shaBase)),
			isRunCommit: func(string) bool { return true },
			want: []RemoteChangeEntry{{Kind: BranchDeleted, Remote: "origin", Branch: "feat/x",
				OldSHA: shaRun, Attribution: AttributionNotAttributable}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DiffRemoteSnapshots(tc.before, tc.after, tc.isRunCommit)
			if !reflect.DeepEqual(got.Entries, tc.want) {
				t.Errorf("entries = %+v\nwant      %+v", got.Entries, tc.want)
			}
			if len(got.NotCompared) != 0 && tc.name != "remote_added" && tc.name != "remote_removed" {
				t.Errorf("not_compared = %+v, want none", got.NotCompared)
			}
		})
	}
}

// A remote that exists on one side only, or whose fetch URL changed, has its
// refs left out of the branch comparison with a not_compared item each —
// never reported as all its branches created or deleted.
func TestDiffRemoteSnapshots_RefsOfAnUnmatchedRemoteAreNotCompared(t *testing.T) {
	mirror := RemoteConfig{Name: "mirror", FetchURL: "/srv/m.git", PushURL: "/srv/m.git"}
	before := observed([]RemoteConfig{origin}, ref("origin", "main", shaBase))
	after := observed([]RemoteConfig{mirror, origin}, ref("mirror", "feat/x", shaRun), ref("origin", "main", shaBase))

	got := DiffRemoteSnapshots(before, after, runCommitIs(shaRun))
	if len(got.Entries) != 1 || got.Entries[0].Kind != RemoteAdded {
		t.Fatalf("entries = %+v, want only remote_added", got.Entries)
	}
	if len(got.NotCompared) != 1 || got.NotCompared[0].Part != RemotePartBranches || got.NotCompared[0].Remote != "mirror" {
		t.Fatalf("not_compared = %+v, want the mirror's branches", got.NotCompared)
	}

	renamed := RemoteConfig{Name: "origin", FetchURL: "/srv/other.git", PushURL: "/srv/other.git"}
	got = DiffRemoteSnapshots(before, observed([]RemoteConfig{renamed}, ref("origin", "trunk", shaThem)), nil)
	if len(got.Entries) != 1 || got.Entries[0].Kind != RemoteURLChanged {
		t.Fatalf("entries = %+v, want only remote_url_changed", got.Entries)
	}
	if len(got.NotCompared) != 1 || !strings.Contains(got.NotCompared[0].Reason, "fetch URL changed") {
		t.Fatalf("not_compared = %+v, want origin's refs not compared", got.NotCompared)
	}
}

// An unavailable part is never empty: it yields no entries even when the
// other side lists things, and a not_compared item carrying the reason.
func TestDiffRemoteSnapshots_UnavailableIsNeverEmpty(t *testing.T) {
	full := observed([]RemoteConfig{origin}, ref("origin", "main", shaBase))
	lsFailed := observed([]RemoteConfig{origin})
	lsFailed.Branches = BranchesSnapshot{Status: RemotePartUnavailable, Reason: "ls-remote origin: exit status 128: fatal: not found"}
	noRepo := RemoteSnapshot{
		Remotes:  RemotesSnapshot{Status: RemotePartUnavailable, Reason: "fatal: not a git repository"},
		Branches: BranchesSnapshot{Status: RemotePartUnavailable, Reason: "the remotes part is unavailable"},
	}

	cases := []struct {
		name          string
		before, after RemoteSnapshot
		observed      []RemotePart
		reasons       map[RemotePart]string
	}{
		{"branches unavailable after", full, lsFailed, []RemotePart{RemotePartRemotes},
			map[RemotePart]string{RemotePartBranches: "unavailable after: ls-remote origin: exit status 128"}},
		{"branches unavailable before", lsFailed, full, []RemotePart{RemotePartRemotes},
			map[RemotePart]string{RemotePartBranches: "unavailable before: ls-remote origin"}},
		{"everything unavailable on both sides", noRepo, noRepo, []RemotePart{},
			map[RemotePart]string{RemotePartRemotes: "unavailable before (fatal: not a git repository) and after (fatal: not a git repository)",
				RemotePartBranches: "unavailable before"}},
		{"remotes unavailable after", full, noRepo, []RemotePart{},
			map[RemotePart]string{RemotePartRemotes: "unavailable after: fatal: not a git repository", RemotePartBranches: "unavailable after"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DiffRemoteSnapshots(tc.before, tc.after, func(string) bool { return true })
			if len(got.Entries) != 0 {
				t.Errorf("entries = %+v, want none from an uncompared part", got.Entries)
			}
			if !reflect.DeepEqual(got.Observed, tc.observed) {
				t.Errorf("observed = %v, want %v", got.Observed, tc.observed)
			}
			if len(got.NotCompared) != len(tc.reasons) {
				t.Fatalf("not_compared = %+v, want one per part in %v", got.NotCompared, tc.reasons)
			}
			for _, nc := range got.NotCompared {
				if want, ok := tc.reasons[nc.Part]; !ok || !strings.HasPrefix(nc.Reason, want) {
					t.Errorf("not_compared %s reason = %q, want prefix %q", nc.Part, nc.Reason, want)
				}
			}
		})
	}
}

func TestDiffRemoteSnapshots_AMissingReasonStillSaysSomething(t *testing.T) {
	blind := observed(nil)
	blind.Remotes = RemotesSnapshot{Status: RemotePartUnavailable}
	got := DiffRemoteSnapshots(observed(nil), blind, nil)
	if len(got.NotCompared) == 0 || got.NotCompared[0].Reason != "unavailable after: no reason recorded" {
		t.Fatalf("not_compared = %+v", got.NotCompared)
	}
}

// TestRemoteChanges_LegsAndBetweenLegs (ADR 0046 §3.1): each leg diffs its
// after against its own before, with attribution; what changed while no leg
// ran is the between-legs set, never attributed.
func TestRemoteChanges_LegsAndBetweenLegs(t *testing.T) {
	always := func(string) bool { return true }
	start := observed([]RemoteConfig{origin}, ref("origin", "main", shaBase))
	leg1End := observed([]RemoteConfig{origin}, ref("origin", "feat/a", shaRun), ref("origin", "main", shaBase))
	paused := observed([]RemoteConfig{origin}, ref("origin", "feat/a", shaRun), ref("origin", "feat/b", shaRun), ref("origin", "main", shaBase))

	var rc RemoteChanges
	if rc.EndLeg(start, always) {
		t.Fatal("EndLeg with no open leg recorded something")
	}
	rc.BeginLeg(start)
	if !rc.EndLeg(leg1End, always) {
		t.Fatal("EndLeg of an open leg recorded nothing")
	}
	if rc.EndLeg(leg1End, always) {
		t.Fatal("a second EndLeg of the same leg recorded something")
	}
	rc.BeginLeg(paused)
	rc.EndLeg(paused, always)

	if len(rc.Legs) != 2 || len(rc.BetweenLegs) != 1 {
		t.Fatalf("legs = %d, between = %d; want 2 and 1", len(rc.Legs), len(rc.BetweenLegs))
	}
	leg1 := rc.Legs[0].Changes.Entries
	if len(leg1) != 1 || leg1[0].Branch != "feat/a" || leg1[0].Attribution != AttributionRunCommit {
		t.Errorf("leg 1 entries = %+v, want feat/a as run_commit", leg1)
	}
	if got := rc.Legs[1].Changes.Entries; len(got) != 0 {
		t.Errorf("leg 2 entries = %+v; a change during the pause must not count against leg 2", got)
	}
	between := rc.BetweenLegs[0]
	if between.FromLeg != 1 || between.ToLeg != 2 {
		t.Errorf("between legs %d→%d, want 1→2", between.FromLeg, between.ToLeg)
	}
	if e := between.Changes.Entries; len(e) != 1 || e[0].Branch != "feat/b" || e[0].Attribution != AttributionNotAttributable {
		t.Errorf("between-legs entries = %+v, want feat/b, not_attributable whatever its sha", e)
	}
}

func TestRemoteChanges_ALegWithNoAfterLeavesNothingToDiff(t *testing.T) {
	var rc RemoteChanges
	rc.BeginLeg(observed(nil))
	rc.BeginLeg(observed([]RemoteConfig{origin}))

	between := rc.BetweenLegs[0].Changes
	if len(between.Entries) != 0 || len(between.Observed) != 0 || len(between.NotCompared) != 2 {
		t.Fatalf("between = %+v, want no entries and both parts not compared", between)
	}
	if between.NotCompared[0].Reason != "leg 1 recorded no after snapshot" {
		t.Errorf("reason = %q", between.NotCompared[0].Reason)
	}
}

func sampleRemoteChanges() *RemoteChanges {
	rc := &RemoteChanges{}
	rc.BeginLeg(observed([]RemoteConfig{origin}, ref("origin", "main", shaBase)))
	after := observed([]RemoteConfig{origin}, ref("origin", "feat/x", shaRun), ref("origin", "main", shaBase))
	after.Branches = BranchesSnapshot{Status: RemotePartUnavailable, Reason: "ls-remote origin: timed out after 30s"}
	rc.EndLeg(after, runCommitIs(shaRun))
	rc.BeginLeg(observed([]RemoteConfig{origin}, ref("origin", "main", shaRun)))
	rc.EndLeg(observed([]RemoteConfig{origin}, ref("origin", "feat/x", shaRun), ref("origin", "main", shaRun)), runCommitIs(shaRun))
	return rc
}

// TestWrite_RemoteChangesRoundTrip_410: the record and run_base_sha survive
// Write and Load, and do not move the schema stamp.
func TestWrite_RemoteChangesRoundTrip_410(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	snap := sampleSnapshot()
	snap.RunBaseSHA = shaBase
	snap.RemoteChanges = sampleRemoteChanges()
	if err := Write(path, snap); err != nil {
		t.Fatal(err)
	}
	if got := rawSchema(t, path); got != Schema || Schema != 3 {
		t.Errorf("stamped schema %d (Schema = %d), want 3 — the record is additive", got, Schema)
	}
	keys := snapshotKeys(t, path)
	if string(keys["run_base_sha"]) != `"`+shaBase+`"` {
		t.Errorf("run_base_sha = %s", keys["run_base_sha"])
	}
	var block map[string]json.RawMessage
	if err := json.Unmarshal(keys["remote_changes"], &block); err != nil {
		t.Fatalf("remote_changes block: %v", err)
	}
	if _, ok := block["legs"]; !ok {
		t.Errorf("remote_changes has no legs: %v", block)
	}
	if _, ok := block["between_legs"]; !ok {
		t.Errorf("remote_changes has no between_legs: %v", block)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RunBaseSHA != shaBase {
		t.Errorf("run_base_sha did not round-trip: %q", loaded.RunBaseSHA)
	}
	if !reflect.DeepEqual(loaded.RemoteChanges, snap.RemoteChanges) {
		got, _ := json.Marshal(loaded.RemoteChanges)
		want, _ := json.Marshal(snap.RemoteChanges)
		t.Errorf("remote_changes did not round-trip:\n got %s\nwant %s", got, want)
	}
}

// TestLoad_StateWithoutRemoteChangesLoads_410: a schema-3 state.json written
// before the record has neither key and loads with both absent.
func TestLoad_StateWithoutRemoteChangesLoads_410(t *testing.T) {
	snap, err := Load(filepath.Join("testdata", "pre-ceiling-record-state.json"))
	if err != nil {
		t.Fatalf("an old schema-3 snapshot no longer loads: %v", err)
	}
	if snap.RemoteChanges != nil || snap.RunBaseSHA != "" {
		t.Errorf("absent keys loaded as %+v / %q, want nil and empty", snap.RemoteChanges, snap.RunBaseSHA)
	}

	path := filepath.Join(t.TempDir(), "state.json")
	if err := Write(path, sampleSnapshot()); err != nil {
		t.Fatal(err)
	}
	keys := snapshotKeys(t, path)
	if _, ok := keys["remote_changes"]; ok {
		t.Error("a snapshot with no record carries a remote_changes key")
	}
	if _, ok := keys["run_base_sha"]; ok {
		t.Error("a snapshot with no base carries a run_base_sha key")
	}
}

// TestSnapshotRecorder_RecordRemoteChanges_410: the record is written, kept on
// every later write, and copied, so the caller ending a leg afterwards does
// not change what was recorded.
func TestSnapshotRecorder_RecordRemoteChanges_410(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	rec := NewSnapshotRecorder(path, baseSnapshot("run-1"))

	var rc RemoteChanges
	rc.BeginLeg(observed([]RemoteConfig{origin}))
	if err := rec.RecordRemoteChanges(rc); err != nil {
		t.Fatal(err)
	}
	rc.EndLeg(observed([]RemoteConfig{origin}), nil)
	if err := rec.RecordNode("a", NodeRecord{Verdict: VerdictPass}); err != nil {
		t.Fatal(err)
	}

	snap, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if snap.RemoteChanges == nil || len(snap.RemoteChanges.Legs) != 1 {
		t.Fatalf("record not kept: %+v", snap.RemoteChanges)
	}
	if snap.RemoteChanges.Legs[0].After != nil {
		t.Error("the recorder shares the caller's record: a later EndLeg reached the file without being recorded")
	}
}

// TestSnapshotRecorder_ClearPause_410: an interrupt during the after remote
// snapshot withdraws the leg's pause — the gate, its description and its
// "pause" decision, and a limit pause — and keeps the nodes, every other
// decision and the remote record.
func TestSnapshotRecorder_ClearPause_410(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	rec := NewSnapshotRecorder(path, baseSnapshot("run-1"))

	var rc RemoteChanges
	rc.BeginLeg(observed([]RemoteConfig{origin}))
	steps := []error{
		rec.RecordRemoteChanges(rc),
		rec.RecordNode("a", NodeRecord{Verdict: VerdictPass}),
		rec.RecordGateDecision("first", GateApprove),
		rec.RecordLimitPause(LimitPause{NodeIDs: []string{"b"}}),
		rec.RecordDescribedPause("second", "check it"),
		rec.ClearPause(),
	}
	for _, err := range steps {
		if err != nil {
			t.Fatal(err)
		}
	}

	snap, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Gate.PausedAt != "" || snap.Gate.PausedGateDescription != "" || snap.LimitPause != nil {
		t.Errorf("pause not withdrawn: gate %+v, limit pause %+v", snap.Gate, snap.LimitPause)
	}
	if want := map[string]GateDecision{"first": GateApprove}; !reflect.DeepEqual(snap.Gate.Decisions, want) {
		t.Errorf("decisions = %v, want %v: only the paused gate's pause is withdrawn", snap.Gate.Decisions, want)
	}
	if snap.Nodes["a"].Verdict != VerdictPass {
		t.Errorf("nodes = %+v, want a's PASS kept", snap.Nodes)
	}
	if snap.RemoteChanges == nil || len(snap.RemoteChanges.Legs) != 1 {
		t.Errorf("remote record not kept: %+v", snap.RemoteChanges)
	}
}

// TestSnapshotRecorder_RecordLegGate_410: a resumed leg seeded with the
// loaded pause writes its own gate state once its before snapshot completes —
// the decisions given, no paused gate, no limit pause — keeping the nodes and
// the remote record, and never sharing the caller's map.
func TestSnapshotRecorder_RecordLegGate_410(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	base := baseSnapshot("run-1")
	base.Nodes = map[string]NodeRecord{"a": {Verdict: VerdictPass}}
	base.Gate = GateState{PausedAt: "second", PausedGateDescription: "check it", Decisions: map[string]GateDecision{"first": GateApprove, "second": GatePause}}
	base.LimitPause = &LimitPause{NodeIDs: []string{"b"}}
	rec := NewSnapshotRecorder(path, base)

	var rc RemoteChanges
	rc.BeginLeg(observed([]RemoteConfig{origin}))
	if err := rec.RecordRemoteChanges(rc); err != nil {
		t.Fatal(err)
	}
	if snap, err := Load(path); err != nil {
		t.Fatal(err)
	} else if snap.Gate.PausedAt != "second" || snap.LimitPause == nil {
		t.Errorf("before RecordLegGate: gate %+v, limit pause %+v, want the loaded pause kept", snap.Gate, snap.LimitPause)
	}

	decisions := map[string]GateDecision{"first": GateApprove, "second": GateApprove}
	if err := rec.RecordLegGate(decisions); err != nil {
		t.Fatal(err)
	}
	if err := rec.RecordGateDecision("third", GateReject); err != nil {
		t.Fatal(err)
	}
	snap, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Gate.PausedAt != "" || snap.Gate.PausedGateDescription != "" || snap.LimitPause != nil {
		t.Errorf("pause not replaced: gate %+v, limit pause %+v", snap.Gate, snap.LimitPause)
	}
	if want := map[string]GateDecision{"first": GateApprove, "second": GateApprove, "third": GateReject}; !reflect.DeepEqual(snap.Gate.Decisions, want) {
		t.Errorf("decisions = %v, want %v", snap.Gate.Decisions, want)
	}
	if _, shared := decisions["third"]; shared {
		t.Error("the recorder shares the caller's decisions map")
	}
	if snap.Nodes["a"].Verdict != VerdictPass || snap.RemoteChanges == nil || len(snap.RemoteChanges.Legs) != 1 {
		t.Errorf("nodes %+v / remote record %+v not kept", snap.Nodes, snap.RemoteChanges)
	}
}

// TestSnapshotRecorder_RecordLegGateKeepsThePauseWhenItsWriteFails_410: a
// RecordLegGate whose write fails returns the error and keeps the loaded
// pause, so the next record that does reach disk still says paused — never
// the leg gate that was not written.
func TestSnapshotRecorder_RecordLegGateKeepsThePauseWhenItsWriteFails_410(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	// A file where the run directory should be: Write's MkdirAll fails.
	if err := os.WriteFile(dir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state.json")
	base := baseSnapshot("run-1")
	base.Gate = GateState{PausedAt: "second", PausedGateDescription: "check it", Decisions: map[string]GateDecision{"first": GateApprove, "second": GatePause}}
	base.LimitPause = &LimitPause{NodeIDs: []string{"b"}, Cause: "limit"}
	rec := NewSnapshotRecorder(path, base)

	if err := rec.RecordLegGate(map[string]GateDecision{"first": GateApprove, "second": GateApprove}); err == nil {
		t.Fatal("RecordLegGate returned nil on a write that cannot succeed")
	}
	if !reflect.DeepEqual(rec.snap.Gate, base.Gate) || !reflect.DeepEqual(rec.snap.LimitPause, base.LimitPause) {
		t.Errorf("after the failed write: gate %+v, limit pause %+v, want the loaded pause %+v, %+v", rec.snap.Gate, rec.snap.LimitPause, base.Gate, base.LimitPause)
	}

	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := rec.RecordNode("a", NodeRecord{Verdict: VerdictPass}); err != nil {
		t.Fatalf("RecordNode once the path is writable: %v", err)
	}
	snap, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snap.Gate, base.Gate) || snap.LimitPause == nil || !reflect.DeepEqual(snap.LimitPause.NodeIDs, base.LimitPause.NodeIDs) {
		t.Errorf("the next record persisted gate %+v, limit pause %+v, want the loaded pause kept", snap.Gate, snap.LimitPause)
	}
}
