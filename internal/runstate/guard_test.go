package runstate

import (
	"reflect"
	"testing"
)

// observedSnapshot is a snapshot with every part observed.
func observedSnapshot() RemoteSnapshot {
	return RemoteSnapshot{
		Remotes:      RemotesSnapshot{Status: RemotePartObserved},
		Branches:     BranchesSnapshot{Status: RemotePartObserved},
		PullRequests: PullRequestsSnapshot{Status: RemotePartObserved},
	}
}

func TestGuardOfLeg_ALegWithNoDiffHasNoGuard(t *testing.T) {
	if _, ok := GuardOfLeg(RemoteLeg{Before: observedSnapshot()}, false); ok {
		t.Error("GuardOfLeg reported a guard for a leg with no diff")
	}
}

func TestGuardOfLeg_EveryComparedPartIsGuarded(t *testing.T) {
	after := observedSnapshot()
	all := []RemotePart{RemotePartRemotes, RemotePartBranches, RemotePartPullRequests}
	leg := RemoteLeg{Before: observedSnapshot(), After: &after, Changes: &RemoteDiff{Observed: all}}
	g, ok := GuardOfLeg(leg, false)
	if !ok {
		t.Fatal("GuardOfLeg reported no guard for a leg with a diff")
	}
	if !reflect.DeepEqual(g.Guarded, all) || len(g.NotGuarded) != 0 || g.Plain {
		t.Errorf("GuardOfLeg = %+v, want every part guarded, none not guarded, not plain", g)
	}
}

func TestGuardOfLeg_AnAutoLegNeverClaimsItsRunBranchParts(t *testing.T) {
	after := observedSnapshot()
	all := []RemotePart{RemotePartRemotes, RemotePartBranches, RemotePartPullRequests}
	leg := RemoteLeg{Before: observedSnapshot(), After: &after, Changes: &RemoteDiff{Observed: all}}
	g, _ := GuardOfLeg(leg, true)
	if !reflect.DeepEqual(g.Guarded, []RemotePart{RemotePartRemotes}) {
		t.Errorf("Guarded = %v, want only remotes", g.Guarded)
	}
	want := []NotGuardedPart{
		{Part: RemotePartBranches, Cause: NotGuardedNoRunBranches, Reason: AutoNoRunBranches},
		{Part: RemotePartPullRequests, Cause: NotGuardedNoRunBranches, Reason: AutoNoRunBranches},
	}
	if !reflect.DeepEqual(g.NotGuarded, want) || !g.Plain {
		t.Errorf("GuardOfLeg = %+v, want %+v and plain", g, want)
	}
}

func TestGuardOfLeg_AnUnattributablePartIsNamedWithItsReason(t *testing.T) {
	after := observedSnapshot()
	leg := RemoteLeg{Before: observedSnapshot(), After: &after, Changes: &RemoteDiff{
		Observed:    []RemotePart{RemotePartRemotes, RemotePartBranches},
		NotCompared: []RemoteNotCompared{{Part: RemotePartBranches, Reason: AttributionConfigUnreadable}},
	}}
	g, _ := GuardOfLeg(leg, false)
	if !reflect.DeepEqual(g.Guarded, []RemotePart{RemotePartRemotes}) {
		t.Errorf("Guarded = %v, want only remotes", g.Guarded)
	}
	if len(g.NotGuarded) != 1 || g.NotGuarded[0].Cause != NotGuardedAttributionUnavailable || !g.Plain {
		t.Fatalf("GuardOfLeg = %+v, want branches not guarded for unavailable attribution, plain", g)
	}
	if got, want := g.NotGuarded[0].Describe(g.Plain), "branches ("+AttributionConfigUnreadable+")"; got != want {
		t.Errorf("Describe = %q, want %q", got, want)
	}
}

func TestGuardOfLeg_APartUnavailableOnBothSidesForOneReasonSaysThatReason(t *testing.T) {
	before := observedSnapshot()
	before.PullRequests = PullRequestsSnapshot{Status: RemotePartUnavailable, Reason: "gh not found"}
	after := before
	leg := RemoteLeg{Before: before, After: &after, Changes: &RemoteDiff{
		Observed:    []RemotePart{RemotePartRemotes, RemotePartBranches},
		NotCompared: []RemoteNotCompared{{Part: RemotePartPullRequests, Reason: "unavailable on both sides"}},
	}}
	g, _ := GuardOfLeg(leg, false)
	want := []NotGuardedPart{{Part: RemotePartPullRequests, Cause: NotGuardedNotCompared, Reason: "gh not found"}}
	if !reflect.DeepEqual(g.NotGuarded, want) || g.Plain {
		t.Errorf("GuardOfLeg = %+v, want %+v, not plain", g, want)
	}
	if got := g.NotGuarded[0].Describe(true); got != "pull_requests (not compared: gh not found)" {
		t.Errorf("Describe(plain) = %q", got)
	}
}

func TestNotGuardedPart_NameNamesOneRemotesPiece(t *testing.T) {
	n := NotGuardedPart{Part: RemotePartBranches, Remote: "origin"}
	if got := n.Name(); got != "branches of origin" {
		t.Errorf("Name = %q, want %q", got, "branches of origin")
	}
}

func TestRunBranchPart(t *testing.T) {
	for part, want := range map[RemotePart]bool{
		RemotePartRemotes: false, RemotePartBranches: true, RemotePartPullRequests: true,
	} {
		if got := RunBranchPart(part); got != want {
			t.Errorf("RunBranchPart(%s) = %v, want %v", part, got, want)
		}
	}
}

func TestIsAttributionUnavailable(t *testing.T) {
	for _, tc := range []struct {
		nc   RemoteNotCompared
		want bool
	}{
		{RemoteNotCompared{Part: RemotePartBranches, Reason: AttributionConfigUnreadable}, true},
		{RemoteNotCompared{Part: RemotePartBranches, Reason: AttributionCheckReadFailed}, true},
		{RemoteNotCompared{Part: RemotePartPullRequests, Reason: AttributionShallowChanged}, true},
		{RemoteNotCompared{Part: RemotePartBranches, Remote: "origin", Reason: AttributionConfigUnreadable}, false},
		{RemoteNotCompared{Part: RemotePartBranches, Reason: "exit status 128"}, false},
	} {
		if got := IsAttributionUnavailable(tc.nc); got != tc.want {
			t.Errorf("IsAttributionUnavailable(%+v) = %v, want %v", tc.nc, got, tc.want)
		}
	}
}
