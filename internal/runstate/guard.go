package runstate

// AutoNoRunBranches is the reason a guarded auto leg names its branches and
// pull requests not guarded: its planned nodes run without engine worktrees,
// so no commit of theirs is on the run's own branches and every new branch or
// pull request reads not_attributable — only a new remote can fail it.
const AutoNoRunBranches = "auto: no run branches"

// The reasons a guarded leg's branches and pull-request parts are not
// guarded when its §5.1 check could not run (the worktree package's
// GuardedBefore.AttributionUnavailable).
const (
	AttributionConfigUnreadable = "attribution unavailable: repository config unreadable"
	AttributionCheckReadFailed  = "attribution unavailable: attribution check read failed"
	AttributionShallowChanged   = "attribution unavailable: shallow boundary changed"
)

// NotGuardedCause is why a guarded leg's part is not guarded (GuardOfLeg).
type NotGuardedCause int

const (
	// NotGuardedNotCompared: the part, or one remote's piece of it, was not
	// compared; the reason is the record's, git's or gh's text.
	NotGuardedNotCompared NotGuardedCause = iota
	// NotGuardedAttributionUnavailable: the part was compared but the §5.1
	// check could not attribute in it; the reason is one of the Attribution*
	// constants.
	NotGuardedAttributionUnavailable
	// NotGuardedNoRunBranches: an auto leg's branches or pull requests;
	// the reason is AutoNoRunBranches.
	NotGuardedNoRunBranches
)

// NotGuardedPart is one part a guarded leg did not guard, with why.
type NotGuardedPart struct {
	Part RemotePart
	// Remote is set for one remote's own piece of the part ("branches of
	// <remote>"), "" for the whole part.
	Remote string
	Cause  NotGuardedCause
	Reason string
}

// Name is the part as the guard line names it: "branches of origin" for a
// remote's own piece, the part's name otherwise.
func (n NotGuardedPart) Name() string {
	if n.Remote != "" {
		return string(n.Part) + " of " + n.Remote
	}
	return string(n.Part)
}

// Describe is "<name> (<reason>)", the reason prefixed "not compared: " when
// plain (LegGuard.Plain) and the part was not compared.
func (n NotGuardedPart) Describe(plain bool) string {
	reason := n.Reason
	if plain && n.Cause == NotGuardedNotCompared {
		reason = "not compared: " + reason
	}
	return n.Name() + " (" + reason + ")"
}

// LegGuard is which parts a guarded leg's verdict covered and which it did
// not, with why (ADR 0046 §5.2): the one rule the CLI's end line and the goal
// assessor's ENGINE-OBSERVED remote changes block both render, so the
// operator and the assessor are told the same thing.
type LegGuard struct {
	// Guarded is the parts compared for every remote and attributable, in
	// the record's observed order.
	Guarded []RemotePart
	// NotGuarded is every piece not guarded: what the record did not compare,
	// in record order, then an auto leg's branches and pull requests.
	NotGuarded []NotGuardedPart
	// Plain is true when the reasons are of more than one kind — an auto leg,
	// or a part whose attribution was unavailable — so each not-compared one
	// says so itself, rather than all sitting under "(not compared)".
	Plain bool
}

// GuardOfLeg is leg's guard (LegGuard), or false for a leg with no diff.
// noRunBranches is an auto leg's: its branches and pull-request parts are
// never claimed guarded, each named with AutoNoRunBranches. A part
// unavailable on both sides for one reason is said with that reason, so the
// launch notice and the end line read the same.
func GuardOfLeg(leg RemoteLeg, noRunBranches bool) (LegGuard, bool) {
	d := leg.Changes
	if d == nil {
		return LegGuard{}, false
	}
	g := LegGuard{Plain: noRunBranches}
	partial := map[RemotePart]bool{}
	for _, nc := range d.NotCompared {
		partial[nc.Part] = true
		n := NotGuardedPart{Part: nc.Part, Remote: nc.Remote, Reason: nc.Reason}
		switch {
		case IsAttributionUnavailable(nc):
			n.Cause = NotGuardedAttributionUnavailable
			g.Plain = true
		case nc.Remote == "" && leg.After != nil:
			before, beforeOK := RemotePartReason(leg.Before, nc.Part)
			after, afterOK := RemotePartReason(*leg.After, nc.Part)
			if !beforeOK && !afterOK && before == after && before != "" {
				n.Reason = before
			}
		}
		g.NotGuarded = append(g.NotGuarded, n)
	}
	for _, part := range d.Observed {
		switch {
		case noRunBranches && RunBranchPart(part):
			g.NotGuarded = append(g.NotGuarded, NotGuardedPart{Part: part, Cause: NotGuardedNoRunBranches, Reason: AutoNoRunBranches})
		case !partial[part]:
			g.Guarded = append(g.Guarded, part)
		}
	}
	return g, true
}

// RunBranchPart reports whether part's guard rests on the run's own branches,
// as a new branch's or pull request's attribution does (ADR 0046 §5.1).
func RunBranchPart(part RemotePart) bool {
	return part == RemotePartBranches || part == RemotePartPullRequests
}

// IsAttributionUnavailable reports whether nc is a part the leg compared but
// could not attribute in (the worktree package's NoteAttributionUnavailable).
func IsAttributionUnavailable(nc RemoteNotCompared) bool {
	if nc.Remote != "" {
		return false
	}
	switch nc.Reason {
	case AttributionConfigUnreadable, AttributionCheckReadFailed, AttributionShallowChanged:
		return true
	}
	return false
}

// RemotePartReason is part's reason in s, and whether s observed it.
func RemotePartReason(s RemoteSnapshot, part RemotePart) (string, bool) {
	switch part {
	case RemotePartRemotes:
		return s.Remotes.Reason, s.Remotes.Status == RemotePartObserved
	case RemotePartBranches:
		return s.Branches.Reason, s.Branches.Status == RemotePartObserved
	default:
		return s.PullRequests.Reason, s.PullRequests.Status == RemotePartObserved
	}
}
