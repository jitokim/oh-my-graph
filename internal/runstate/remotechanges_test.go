package runstate

import (
	"bytes"
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

// observed builds a snapshot with every part observed and no open PRs.
func observed(remotes []RemoteConfig, refs ...RemoteBranchRef) RemoteSnapshot {
	return RemoteSnapshot{
		Remotes:      RemotesSnapshot{Status: RemotePartObserved, Configured: remotes},
		Branches:     BranchesSnapshot{Status: RemotePartObserved, Refs: refs},
		PullRequests: PullRequestsSnapshot{Status: RemotePartObserved},
	}
}

// withPRs is snap with its PR part observed holding prs.
func withPRs(snap RemoteSnapshot, prs ...OpenPullRequest) RemoteSnapshot {
	snap.PullRequests = PullRequestsSnapshot{Status: RemotePartObserved, Open: prs}
	return snap
}

func pr(number int, head, sha string) OpenPullRequest {
	return OpenPullRequest{Number: number, HeadRefName: head, HeadRefOid: sha}
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
		Observed:    []RemotePart{RemotePartRemotes, RemotePartBranches, RemotePartPullRequests},
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
	if string(data) != `{"observed":["remotes","branches","pull_requests"],"entries":[],"not_compared":[]}` {
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
		// gh reads the GitHub repository, not git: it may see what git cannot.
		PullRequests: PullRequestsSnapshot{Status: RemotePartObserved},
	}

	cases := []struct {
		name          string
		before, after RemoteSnapshot
		observed      []RemotePart
		reasons       map[RemotePart]string
	}{
		{"branches unavailable after", full, lsFailed, []RemotePart{RemotePartRemotes, RemotePartPullRequests},
			map[RemotePart]string{RemotePartBranches: "unavailable after: ls-remote origin: exit status 128"}},
		{"branches unavailable before", lsFailed, full, []RemotePart{RemotePartRemotes, RemotePartPullRequests},
			map[RemotePart]string{RemotePartBranches: "unavailable before: ls-remote origin"}},
		{"the git parts unavailable on both sides", noRepo, noRepo, []RemotePart{RemotePartPullRequests},
			map[RemotePart]string{RemotePartRemotes: "unavailable before (fatal: not a git repository) and after (fatal: not a git repository)",
				RemotePartBranches: "unavailable before"}},
		{"remotes unavailable after", full, noRepo, []RemotePart{RemotePartPullRequests},
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
	if len(between.Entries) != 0 || len(between.Observed) != 0 || len(between.NotCompared) != 3 {
		t.Fatalf("between = %+v, want no entries and every part not compared", between)
	}
	if between.NotCompared[2].Part != RemotePartPullRequests {
		t.Errorf("not_compared = %+v, want the pull requests among them", between.NotCompared)
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
	if err := rec.RecordLegGate(decisions, base.Nodes); err != nil {
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

	if err := rec.RecordLegGate(map[string]GateDecision{"first": GateApprove, "second": GateApprove}, nil); err == nil {
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

// TestSnapshotRecorder_RecordLegGateWritesTheCarriedRecords_410: a retry leg
// seeded with every record as loaded keeps the failed one, and its cost, on
// disk until RecordLegGate writes the retained subset in the same write as
// the leg's gate state; the retained records keep their cost, and the
// recorder never shares the caller's map.
func TestSnapshotRecorder_RecordLegGateWritesTheCarriedRecords_410(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	base := baseSnapshot("run-1")
	base.Nodes = map[string]NodeRecord{
		"a": {Verdict: VerdictPass, CostUSD: 0.25},
		"b": {Verdict: VerdictFail, CostUSD: 0.5},
	}
	base.Gate = GateState{Decisions: map[string]GateDecision{"gate1": GateApprove}}
	rec := NewSnapshotRecorder(path, base)

	var rc RemoteChanges
	rc.BeginLeg(observed([]RemoteConfig{origin}))
	if err := rec.RecordRemoteChanges(rc); err != nil {
		t.Fatal(err)
	}
	if snap, err := Load(path); err != nil {
		t.Fatal(err)
	} else if !reflect.DeepEqual(snap.Nodes, base.Nodes) {
		t.Errorf("before RecordLegGate: nodes %+v, want the loaded records %+v", snap.Nodes, base.Nodes)
	}

	retained := map[string]NodeRecord{"a": base.Nodes["a"]}
	if err := rec.RecordLegGate(base.Gate.Decisions, retained); err != nil {
		t.Fatal(err)
	}
	snap, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snap.Nodes, retained) {
		t.Errorf("nodes = %+v, want the retained records %+v", snap.Nodes, retained)
	}
	if !reflect.DeepEqual(snap.Gate, GateState{Decisions: base.Gate.Decisions}) || snap.RemoteChanges == nil || len(snap.RemoteChanges.Legs) != 1 {
		t.Errorf("gate %+v / remote record %+v not kept", snap.Gate, snap.RemoteChanges)
	}

	if err := rec.RecordNode("b", NodeRecord{Verdict: VerdictPass, CostUSD: 0.75}); err != nil {
		t.Fatal(err)
	}
	if _, shared := retained["b"]; shared {
		t.Error("the recorder shares the caller's nodes map")
	}
	if snap, err := Load(path); err != nil {
		t.Fatal(err)
	} else if got := snap.Nodes["b"]; got.Verdict != VerdictPass || got.CostUSD != 0.75 {
		t.Errorf("b = %+v, want the re-run's PASS at its own cost, nothing folded in from the cleared record", got)
	}
}

// TestSnapshotRecorder_RecordLegGateKeepsTheLoadedRecordsWhenItsWriteFails_410:
// a RecordLegGate whose write fails returns the error and keeps the records
// as loaded, so the next record that does reach disk still holds the failed
// node's record and cost — never the retained subset that was not written.
func TestSnapshotRecorder_RecordLegGateKeepsTheLoadedRecordsWhenItsWriteFails_410(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	// A file where the run directory should be: Write's MkdirAll fails.
	if err := os.WriteFile(dir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state.json")
	base := baseSnapshot("run-1")
	base.Nodes = map[string]NodeRecord{
		"a": {Verdict: VerdictPass, CostUSD: 0.25},
		"b": {Verdict: VerdictFail, CostUSD: 0.5},
	}
	rec := NewSnapshotRecorder(path, base)
	before := rec.snap

	if err := rec.RecordLegGate(nil, map[string]NodeRecord{"a": base.Nodes["a"]}); err == nil {
		t.Fatal("RecordLegGate returned nil on a write that cannot succeed")
	}
	if !reflect.DeepEqual(rec.snap, before) {
		t.Errorf("after the failed write: recorder holds %+v, want it unchanged: %+v", rec.snap, before)
	}

	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := rec.RecordRemoteChanges(RemoteChanges{}); err != nil {
		t.Fatalf("RecordRemoteChanges once the path is writable: %v", err)
	}
	snap, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snap.Nodes, base.Nodes) {
		t.Errorf("the next record persisted nodes %+v, want the loaded records %+v", snap.Nodes, base.Nodes)
	}
}

// TestDiffRemoteSnapshots_EachPRKind (ADR 0046 §8.2): one case per PR kind,
// keyed on the PR number and its head sha. updatedAt, the title and the rest
// are not collected, so a PR whose only change is one of them reads the same
// on both sides and yields no entry — and neither does a renamed head branch
// whose sha stayed put.
func TestDiffRemoteSnapshots_EachPRKind(t *testing.T) {
	base := observed([]RemoteConfig{origin})
	cases := []struct {
		name          string
		before, after []OpenPullRequest
		want          []RemoteChangeEntry
	}{
		{"pr_opened", nil, []OpenPullRequest{pr(12, "feat/x", shaRun)},
			[]RemoteChangeEntry{{Kind: PROpened, PRNumber: 12, HeadRefName: "feat/x", NewSHA: shaRun, Attribution: AttributionRunCommit}}},
		{"pr_updated", []OpenPullRequest{pr(12, "feat/x", shaThem)}, []OpenPullRequest{pr(12, "feat/x", shaRun)},
			[]RemoteChangeEntry{{Kind: PRUpdated, PRNumber: 12, HeadRefName: "feat/x", OldSHA: shaThem, NewSHA: shaRun, Attribution: AttributionRunCommit}}},
		{"pr_no_longer_open", []OpenPullRequest{pr(12, "feat/x", shaRun)}, nil,
			[]RemoteChangeEntry{{Kind: PRNoLongerOpen, PRNumber: 12, HeadRefName: "feat/x", OldSHA: shaRun, Attribution: AttributionNotAttributable}}},
		{"only updatedAt moved: the collected fields are equal", []OpenPullRequest{pr(12, "feat/x", shaThem)}, []OpenPullRequest{pr(12, "feat/x", shaThem)},
			[]RemoteChangeEntry{}},
		{"only the head branch name changed", []OpenPullRequest{pr(12, "feat/x", shaThem)}, []OpenPullRequest{pr(12, "feat/y", shaThem)},
			[]RemoteChangeEntry{}},
		{"sorted by number", []OpenPullRequest{pr(30, "c", shaThem), pr(4, "a", shaThem)}, []OpenPullRequest{pr(4, "a", shaRun), pr(21, "b", shaThem)},
			[]RemoteChangeEntry{
				{Kind: PRUpdated, PRNumber: 4, HeadRefName: "a", OldSHA: shaThem, NewSHA: shaRun, Attribution: AttributionRunCommit},
				{Kind: PROpened, PRNumber: 21, HeadRefName: "b", NewSHA: shaThem, Attribution: AttributionNotAttributable},
				{Kind: PRNoLongerOpen, PRNumber: 30, HeadRefName: "c", OldSHA: shaThem, Attribution: AttributionNotAttributable},
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DiffRemoteSnapshots(withPRs(base, tc.before...), withPRs(base, tc.after...), runCommitIs(shaRun))
			if !reflect.DeepEqual(got.Entries, tc.want) {
				t.Errorf("entries = %+v\nwant      %+v", got.Entries, tc.want)
			}
			if want := []RemotePart{RemotePartRemotes, RemotePartBranches, RemotePartPullRequests}; !reflect.DeepEqual(got.Observed, want) {
				t.Errorf("observed = %v, want %v", got.Observed, want)
			}
			if len(got.NotCompared) != 0 {
				t.Errorf("not_compared = %+v, want none", got.NotCompared)
			}
		})
	}
}

// TestDiffRemoteSnapshots_PREntriesComeAfterTheGitOnes: one diff orders the
// kinds remote, branch, PR; and a PR entry carries no remote.
func TestDiffRemoteSnapshots_PREntriesComeAfterTheGitOnes(t *testing.T) {
	before := withPRs(observed(nil))
	after := withPRs(observed([]RemoteConfig{origin}), pr(1, "feat/x", shaRun))
	got := DiffRemoteSnapshots(before, after, nil)
	if len(got.Entries) != 2 || got.Entries[0].Kind != RemoteAdded || got.Entries[1].Kind != PROpened {
		t.Fatalf("entries = %+v, want remote_added then pr_opened", got.Entries)
	}
	data, err := json.Marshal(got.Entries[1])
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"kind":"pr_opened","pr_number":1,"head_ref_name":"feat/x","new_sha":"` + shaRun + `","attribution":"not_attributable"}`; string(data) != want {
		t.Errorf("pr entry encodes as %s\nwant                 %s", data, want)
	}
}

// TestDiffRemoteSnapshots_PRAttribution (ADR 0046 §2.2): pr_opened and
// pr_updated are run_commit exactly when their head sha passes the check the
// branch entries use; pr_no_longer_open never is, whatever its old sha.
func TestDiffRemoteSnapshots_PRAttribution(t *testing.T) {
	base := observed([]RemoteConfig{origin})
	before := withPRs(base, pr(1, "a", shaBase), pr(2, "b", shaBase), pr(3, "gone", shaRun))
	after := withPRs(base, pr(1, "a", shaRun), pr(2, "b", shaThem), pr(4, "mine", shaRun), pr(5, "theirs", shaThem))
	got := DiffRemoteSnapshots(before, after, runCommitIs(shaRun))
	want := map[int]RemoteAttribution{
		1: AttributionRunCommit,       // pr_updated to the run's commit
		2: AttributionNotAttributable, // pr_updated to someone else's
		3: AttributionNotAttributable, // pr_no_longer_open, though its sha is the run's
		4: AttributionRunCommit,       // pr_opened at the run's commit
		5: AttributionNotAttributable, // pr_opened at someone else's
	}
	if len(got.Entries) != len(want) {
		t.Fatalf("entries = %+v, want one per PR in %v", got.Entries, want)
	}
	for _, e := range got.Entries {
		if e.Attribution != want[e.PRNumber] {
			t.Errorf("PR #%d %s: attribution %s, want %s", e.PRNumber, e.Kind, e.Attribution, want[e.PRNumber])
		}
	}

	// No check (the between-legs diff) attributes nothing.
	for _, e := range DiffRemoteSnapshots(before, after, nil).Entries {
		if e.Attribution != AttributionNotAttributable {
			t.Errorf("PR #%d %s with no check: attribution %s", e.PRNumber, e.Kind, e.Attribution)
		}
	}
}

// TestRemoteChanges_PRsBetweenLegsAreNotAttributable (ADR 0046 §3.1): a PR
// opened at the run's own commit while no leg ran is in between_legs,
// not_attributable, and does not count against the next leg.
func TestRemoteChanges_PRsBetweenLegsAreNotAttributable(t *testing.T) {
	always := func(string) bool { return true }
	start := withPRs(observed([]RemoteConfig{origin}))
	paused := withPRs(start, pr(7, "omg/run/x", shaRun))

	var rc RemoteChanges
	rc.BeginLeg(start)
	rc.EndLeg(start, always)
	rc.BeginLeg(paused)
	rc.EndLeg(paused, always)

	between := rc.BetweenLegs[0].Changes.Entries
	if len(between) != 1 || between[0].Kind != PROpened || between[0].Attribution != AttributionNotAttributable {
		t.Errorf("between-legs entries = %+v, want pr_opened, not_attributable", between)
	}
	if got := rc.Legs[1].Changes.Entries; len(got) != 0 {
		t.Errorf("leg 2 entries = %+v, want none: the PR opened during the pause", got)
	}
}

// TestDiffRemoteSnapshots_PRsUnavailableAfterOnly (ADR 0046 §8.3): "not
// authenticated" on the after only compares no PRs: no pr_no_longer_open for
// the PRs the before listed, and a not_compared item naming the part.
func TestDiffRemoteSnapshots_PRsUnavailableAfterOnly(t *testing.T) {
	before := withPRs(observed([]RemoteConfig{origin}), pr(1, "a", shaBase), pr(2, "b", shaThem))
	after := observed([]RemoteConfig{origin})
	after.PullRequests = PullRequestsSnapshot{Status: RemotePartUnavailable, Reason: "not authenticated",
		Detail: "To get started with GitHub CLI, please run: gh auth login"}

	got := DiffRemoteSnapshots(before, after, func(string) bool { return true })
	if len(got.Entries) != 0 {
		t.Errorf("entries = %+v, want none: an unavailable list is not an empty one", got.Entries)
	}
	if want := []RemotePart{RemotePartRemotes, RemotePartBranches}; !reflect.DeepEqual(got.Observed, want) {
		t.Errorf("observed = %v, want %v", got.Observed, want)
	}
	want := []RemoteNotCompared{{Part: RemotePartPullRequests, Reason: "unavailable after: not authenticated"}}
	if !reflect.DeepEqual(got.NotCompared, want) {
		t.Errorf("not_compared = %+v, want %+v", got.NotCompared, want)
	}

	// And the other way: unavailable before only yields no pr_opened.
	got = DiffRemoteSnapshots(after, before, func(string) bool { return true })
	if len(got.Entries) != 0 || len(got.NotCompared) != 1 || got.NotCompared[0].Reason != "unavailable before: not authenticated" {
		t.Errorf("diff = %+v, want no entries and pull_requests unavailable before", got)
	}
}

// TestRemoteChanges_AnInterruptedAfterComparesNothing: the after an
// interrupted leg records has every part unavailable, "interrupted", the PR
// part included, so the record never reads "no change".
func TestRemoteChanges_AnInterruptedAfterComparesNothing(t *testing.T) {
	var rc RemoteChanges
	rc.BeginLeg(withPRs(observed([]RemoteConfig{origin}), pr(1, "a", shaBase)))
	rc.EndLeg(UnavailableRemoteSnapshot("interrupted"), func(string) bool { return true })

	d := rc.Legs[0].Changes
	if len(d.Entries) != 0 || len(d.Observed) != 0 {
		t.Fatalf("diff = %+v, want nothing compared", d)
	}
	want := []RemoteNotCompared{
		{Part: RemotePartRemotes, Reason: "unavailable after: interrupted"},
		{Part: RemotePartBranches, Reason: "unavailable after: interrupted"},
		{Part: RemotePartPullRequests, Reason: "unavailable after: interrupted"},
	}
	if !reflect.DeepEqual(d.NotCompared, want) {
		t.Errorf("not_compared = %+v\nwant          %+v", d.NotCompared, want)
	}
}

// TestRemoteSnapshot_PullRequestsEncoding: an observed part with no PRs keeps
// its status, so it stays distinct from an unavailable one; a part never
// recorded has no key at all.
func TestRemoteSnapshot_PullRequestsEncoding(t *testing.T) {
	keys := func(s RemoteSnapshot) map[string]json.RawMessage {
		t.Helper()
		data, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if got := string(keys(observed(nil))["pull_requests"]); got != `{"status":"observed"}` {
		t.Errorf("an observed empty PR part encodes as %s", got)
	}
	unseen := observed(nil)
	unseen.PullRequests = PullRequestsSnapshot{}
	if raw, ok := keys(unseen)["pull_requests"]; ok {
		t.Errorf("a PR part never recorded encodes as %s, want no key", raw)
	}
}

// TestLoad_RemoteChangesWithoutThePRPartLoads_410: a schema-3 state.json
// whose record was written before the PR part existed loads with that part
// zero ("not recorded"), compares nothing for it against a new snapshot,
// and is written back without inventing the key.
func TestLoad_RemoteChangesWithoutThePRPartLoads_410(t *testing.T) {
	fixture := filepath.Join("testdata", "pre-pr-part-remote-changes-state.json")
	if strings.Contains(string(mustReadFile(t, fixture)), "pull_requests") {
		t.Fatal("the fixture must predate the PR part")
	}
	snap, err := Load(fixture)
	if err != nil {
		t.Fatalf("a pre-change schema-3 snapshot no longer loads: %v", err)
	}
	if snap.RemoteChanges == nil || len(snap.RemoteChanges.Legs) != 1 || snap.RemoteChanges.Legs[0].After == nil {
		t.Fatalf("remote_changes = %+v, want the old leg", snap.RemoteChanges)
	}
	leg := snap.RemoteChanges.Legs[0]
	if !reflect.DeepEqual(leg.Before.PullRequests, PullRequestsSnapshot{}) || !reflect.DeepEqual(leg.After.PullRequests, PullRequestsSnapshot{}) {
		t.Errorf("an absent PR part loaded as %+v / %+v, want zero", leg.Before.PullRequests, leg.After.PullRequests)
	}
	if e := leg.Changes.Entries; len(e) != 1 || e[0].Kind != BranchCreated || e[0].Remote != "origin" {
		t.Errorf("old entries = %+v", e)
	}

	// A resumed leg's before, taken by this build, against the old after.
	rc := *snap.RemoteChanges.clone()
	rc.BeginLeg(withPRs(*leg.After, pr(1, "a", shaRun)))
	between := rc.BetweenLegs[0].Changes
	if len(between.Entries) != 0 {
		t.Errorf("between-legs entries = %+v, want no PR guessed from a part never recorded", between.Entries)
	}
	if want := []RemoteNotCompared{{Part: RemotePartPullRequests, Reason: "unavailable before: not recorded"}}; !reflect.DeepEqual(between.NotCompared, want) {
		t.Errorf("not_compared = %+v, want %+v", between.NotCompared, want)
	}

	path := filepath.Join(t.TempDir(), "state.json")
	if err := Write(path, snap); err != nil {
		t.Fatal(err)
	}
	if got := rawSchema(t, path); got != 3 {
		t.Errorf("stamped schema %d, want 3", got)
	}
	if strings.Contains(string(mustReadFile(t, path)), "pull_requests") {
		t.Error("rewriting the old record invented a pull_requests key")
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// withRepo is snap with its PR part naming repo, worked out from source.
func withRepo(snap RemoteSnapshot, repo, source string) RemoteSnapshot {
	snap.PullRequests.Repo, snap.PullRequests.RepoSource = repo, source
	return snap
}

// TestWrite_PullRequestsRepoRoundTrip_410: the repo gh was expected to read
// and its source survive Write and Load under their own keys, and do not
// move the schema stamp.
func TestWrite_PullRequestsRepoRoundTrip_410(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	snap := sampleSnapshot()
	rc := &RemoteChanges{}
	rc.BeginLeg(withRepo(withPRs(observed([]RemoteConfig{origin}), pr(1, "a", shaBase)), "github.com/me/cli", "gh default"))
	rc.EndLeg(withRepo(withPRs(observed([]RemoteConfig{origin}), pr(1, "a", shaRun)), "github.com/me/cli", "gh default"), runCommitIs(shaRun))
	snap.RemoteChanges = rc
	if err := Write(path, snap); err != nil {
		t.Fatal(err)
	}
	if got := rawSchema(t, path); got != Schema || Schema != 3 {
		t.Errorf("stamped schema %d (Schema = %d), want 3 — the repo fields are additive", got, Schema)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, mustReadFile(t, path)); err != nil {
		t.Fatal(err)
	}
	if data := compact.String(); !strings.Contains(data, `"repo":"github.com/me/cli","repo_source":"gh default"`) {
		t.Errorf("state.json lacks the repo keys:\n%s", data)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.RemoteChanges, snap.RemoteChanges) {
		got, _ := json.Marshal(loaded.RemoteChanges)
		want, _ := json.Marshal(snap.RemoteChanges)
		t.Errorf("remote_changes did not round-trip:\n got %s\nwant %s", got, want)
	}
	if d := loaded.RemoteChanges.Legs[0].Changes; len(d.Entries) != 1 || d.Entries[0].Kind != PRUpdated {
		t.Errorf("changes = %+v, want the one pr_updated of a repo both sides name", d)
	}
}

// TestRemoteSnapshot_PullRequestsRepoOmittedWhenEmpty: a PR part with no
// repo worked out has neither key, and a part written before the keys
// existed loads with both empty.
func TestRemoteSnapshot_PullRequestsRepoOmittedWhenEmpty(t *testing.T) {
	for _, part := range []PullRequestsSnapshot{
		{Status: RemotePartObserved},
		{Status: RemotePartUnavailable, Reason: "ambiguous gh repo"},
	} {
		data, err := json.Marshal(part)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), `"repo`) {
			t.Errorf("a PR part with no repo encodes as %s, want no repo keys", data)
		}
	}
	var old PullRequestsSnapshot
	if err := json.Unmarshal([]byte(`{"status":"observed","open":[{"number":1,"head_ref_name":"a","head_ref_oid":"`+shaBase+`"}]}`), &old); err != nil {
		t.Fatalf("a PR part without the repo keys no longer loads: %v", err)
	}
	if old.Repo != "" || old.RepoSource != "" || len(old.Open) != 1 {
		t.Errorf("old PR part loaded as %+v", old)
	}
}

// TestDiffRemoteSnapshots_PRsOfDifferentReposAreNotCompared: two lists naming
// different repositories are not compared, with both named; the same repo
// spelt in another case, or a side that recorded none, compares as before.
func TestDiffRemoteSnapshots_PRsOfDifferentReposAreNotCompared(t *testing.T) {
	base := withPRs(observed([]RemoteConfig{origin}), pr(1, "a", shaBase))
	moved := withPRs(observed([]RemoteConfig{origin}), pr(2, "b", shaRun))

	got := DiffRemoteSnapshots(withRepo(base, "github.com/me/cli", "single GitHub remote"),
		withRepo(moved, "github.com/cli/cli", "gh default"), runCommitIs(shaRun))
	if len(got.Entries) != 0 {
		t.Errorf("entries = %+v, want none: another repo's PRs are not this one's changes", got.Entries)
	}
	if want := []RemotePart{RemotePartRemotes, RemotePartBranches}; !reflect.DeepEqual(got.Observed, want) {
		t.Errorf("observed = %v, want %v", got.Observed, want)
	}
	want := []RemoteNotCompared{{Part: RemotePartPullRequests, Reason: "different repos: before github.com/me/cli, after github.com/cli/cli"}}
	if !reflect.DeepEqual(got.NotCompared, want) {
		t.Errorf("not_compared = %+v, want %+v", got.NotCompared, want)
	}

	for name, after := range map[string]RemoteSnapshot{
		"same repo, other case": withRepo(moved, "github.com/Me/CLI", "GH_REPO"),
		"no repo recorded":      moved,
	} {
		got := DiffRemoteSnapshots(withRepo(base, "github.com/me/cli", "gh default"), after, runCommitIs(shaRun))
		if len(got.NotCompared) != 0 || len(got.Entries) != 2 {
			t.Errorf("%s: diff = %+v, want the PR part compared", name, got)
		}
	}
}

// TestDiffRemoteSnapshots_AnAmbiguousRepoIsNotComparedNeverEmpty: a PR part
// left unavailable because gh's repo could not be told flows through the
// diff as not_compared, never as an empty list against the other side.
func TestDiffRemoteSnapshots_AnAmbiguousRepoIsNotComparedNeverEmpty(t *testing.T) {
	const ambiguous = "ambiguous gh repo: more than one GitHub remote and no gh default (run gh repo set-default)"
	before := observed([]RemoteConfig{origin})
	before.PullRequests = PullRequestsSnapshot{Status: RemotePartUnavailable, Reason: ambiguous}
	after := withRepo(withPRs(observed([]RemoteConfig{origin}), pr(5, "x", shaRun)), "github.com/me/cli", "gh default")

	got := DiffRemoteSnapshots(before, after, runCommitIs(shaRun))
	if len(got.Entries) != 0 {
		t.Errorf("entries = %+v, want no pr_opened from an unknown before", got.Entries)
	}
	want := []RemoteNotCompared{{Part: RemotePartPullRequests, Reason: "unavailable before: " + ambiguous}}
	if !reflect.DeepEqual(got.NotCompared, want) {
		t.Errorf("not_compared = %+v, want %+v", got.NotCompared, want)
	}
}
