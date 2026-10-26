package runstate

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// A guarded after read a remote's refs through its before's fetch URL
// (ReadThroughBeforeURL): those refs come from the repository the before
// read, so they are compared even when the remote was removed or re-pointed,
// beside the remote entry that reports it. Without the field they are not.
// A re-pointed remote's new repository was never read, so beside the
// comparison its branches are still named as not compared.
func TestDiffRemoteSnapshots_ComparesARemoteReadThroughItsBeforeURL(t *testing.T) {
	origin := RemoteConfig{Name: "origin", FetchURL: "https://example.com/o.git", PushURL: "https://example.com/o.git"}
	moved := RemoteConfig{Name: "origin", FetchURL: "https://example.com/m.git", PushURL: "https://example.com/m.git"}
	main := RemoteBranchRef{Remote: "origin", Branch: "main", SHA: strings.Repeat("1", 40)}
	pushed := RemoteBranchRef{Remote: "origin", Branch: "feat/x", SHA: strings.Repeat("3", 40)}
	before := RemoteSnapshot{
		Remotes:      RemotesSnapshot{Status: RemotePartObserved, Configured: []RemoteConfig{origin}},
		Branches:     BranchesSnapshot{Status: RemotePartObserved, Refs: []RemoteBranchRef{main}},
		PullRequests: PullRequestsSnapshot{Status: RemotePartObserved},
	}
	runCommit := func(sha string) bool { return sha == pushed.SHA }

	for _, tc := range []struct {
		name       string
		configured []RemoteConfig
		kind       RemoteChangeKind
		skipped    []RemoteNotCompared
	}{
		{"removed", nil, RemoteRemoved, nil},
		{"re-pointed", []RemoteConfig{moved}, RemoteURLChanged, []RemoteNotCompared{{Part: RemotePartBranches, Remote: "origin",
			Reason: "fetch URL changed: the new repository has no before refs to compare"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			after := RemoteSnapshot{
				Remotes:      RemotesSnapshot{Status: RemotePartObserved, Configured: tc.configured},
				Branches:     BranchesSnapshot{Status: RemotePartObserved, Refs: []RemoteBranchRef{pushed, main}},
				PullRequests: PullRequestsSnapshot{Status: RemotePartObserved},
			}
			byName := DiffRemoteSnapshots(before, after, runCommit)
			if len(byName.Entries) != 1 || byName.Entries[0].Kind != tc.kind || len(byName.NotCompared) != 1 {
				t.Fatalf("read by name: diff = %+v, want only %s and origin's refs not compared", byName, tc.kind)
			}

			after.Branches.ReadThroughBeforeURL = []string{"origin"}
			through := DiffRemoteSnapshots(before, after, runCommit)
			want := []RemoteChangeEntry{
				through.Entries[0],
				{Kind: BranchCreated, Remote: "origin", Branch: "feat/x", NewSHA: pushed.SHA, Attribution: AttributionRunCommit},
			}
			if through.Entries[0].Kind != tc.kind || !reflect.DeepEqual(through.Entries, want) {
				t.Errorf("entries = %+v, want %s and the run_commit branch_created", through.Entries, tc.kind)
			}
			if len(through.NotCompared) != len(tc.skipped) || (len(tc.skipped) != 0 && !reflect.DeepEqual(through.NotCompared, tc.skipped)) {
				t.Errorf("not_compared = %+v, want %+v", through.NotCompared, tc.skipped)
			}
		})
	}
}

// The field is additive: a snapshot without it encodes as it always did.
func TestBranchesSnapshot_ReadThroughBeforeURLIsOmittedWhenEmpty(t *testing.T) {
	data, err := json.Marshal(BranchesSnapshot{Status: RemotePartObserved})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != `{"status":"observed"}` {
		t.Errorf("encoded = %s", got)
	}
}
