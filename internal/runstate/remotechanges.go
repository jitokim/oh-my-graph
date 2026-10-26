package runstate

import (
	"fmt"
	"sort"
	"strings"
)

// RemotePart names one part of a RemoteSnapshot (ADR 0046 §2.1).
type RemotePart string

const (
	// RemotePartRemotes is the configured remotes: `git remote -v`.
	RemotePartRemotes RemotePart = "remotes"
	// RemotePartBranches is the remote branch refs: `git ls-remote --heads`
	// for each configured remote.
	RemotePartBranches RemotePart = "branches"
	// RemotePartPullRequests is the open pull requests: `gh pr list --state
	// open`, run in the invocation repository.
	RemotePartPullRequests RemotePart = "pull_requests"
)

// RemotePartStatus says whether the engine saw a part.
type RemotePartStatus string

const (
	// RemotePartObserved: the part was read, and its list is what it holds —
	// an empty list is an observed "none".
	RemotePartObserved RemotePartStatus = "observed"
	// RemotePartUnavailable: the part could not be read, and Reason says why.
	// Its list is not empty, it is unknown: such a part is never compared.
	RemotePartUnavailable RemotePartStatus = "unavailable"
)

// RemoteSnapshot is what the engine saw of the invocation repository's
// remote state at one moment (ADR 0046 §2.1): its configured remotes, the
// branch refs those remotes advertise and its open pull requests, each part
// with its own status. A leg takes one before its first node and one after
// its last.
type RemoteSnapshot struct {
	Remotes  RemotesSnapshot  `json:"remotes"`
	Branches BranchesSnapshot `json:"branches"`
	// PullRequests is additive: a snapshot written before the part existed
	// has no key and loads with a zero part — Status "", "not recorded" —
	// which, like an unavailable one, is never compared.
	PullRequests PullRequestsSnapshot `json:"pull_requests,omitzero"`
}

// UnavailableRemoteSnapshot is a snapshot not taken: every part unavailable
// with reason, so a diff against it compares nothing and says why — the
// after a leg records when the run was interrupted before it could read one.
func UnavailableRemoteSnapshot(reason string) RemoteSnapshot {
	return RemoteSnapshot{
		Remotes:      RemotesSnapshot{Status: RemotePartUnavailable, Reason: reason},
		Branches:     BranchesSnapshot{Status: RemotePartUnavailable, Reason: reason},
		PullRequests: PullRequestsSnapshot{Status: RemotePartUnavailable, Reason: reason},
	}
}

// RemotesSnapshot is the configured-remotes part.
type RemotesSnapshot struct {
	Status RemotePartStatus `json:"status"`
	// Reason is what the engine saw when the part is unavailable: git's exit
	// and a summary of what it printed, a timeout, not a git repository.
	// Empty when observed.
	Reason string `json:"reason,omitempty"`
	// Configured is every remote, sorted by name. Empty both when observed
	// with none and when unavailable; only Status tells them apart.
	Configured []RemoteConfig `json:"configured,omitempty"`
}

// RemoteConfig is one configured remote. The URLs are as recorded, with any
// password in them replaced, since the snapshot is read by every consumer of
// the run.
type RemoteConfig struct {
	Name     string `json:"name"`
	FetchURL string `json:"fetch_url"`
	PushURL  string `json:"push_url"`
}

// BranchesSnapshot is the remote-branch-refs part. It is unavailable when
// any remote's ls-remote failed, or when the remotes part itself was — a
// branch list missing one remote's refs would compare as that remote's
// branches all deleted.
type BranchesSnapshot struct {
	Status RemotePartStatus `json:"status"`
	Reason string           `json:"reason,omitempty"`
	// Refs is every advertised branch, sorted by remote then branch.
	Refs []RemoteBranchRef `json:"refs,omitempty"`
	// ReadThroughBeforeURL names, on a guarded leg's after snapshot, each
	// remote whose refs were read through the fetch URL that leg's before
	// recorded rather than by name (ADR 0046 §5.1), sorted. Such a remote's
	// refs come from the repository the before read, so they are compared
	// even when the remote was removed or re-pointed during the leg. Only
	// names are held, never a URL; additive, and absent everywhere else.
	ReadThroughBeforeURL []string `json:"read_through_before_url,omitempty"`
}

// RemoteBranchRef is one branch a remote advertised.
type RemoteBranchRef struct {
	Remote string `json:"remote"`
	Branch string `json:"branch"`
	SHA    string `json:"sha"`
}

// PullRequestsSnapshot is the open-pull-requests part. It is unavailable when
// gh is not on PATH or not authenticated, exits non-zero, times out or prints
// what is not the list asked for, and when the list holds the whole --limit
// ("truncated"), since more may exist.
type PullRequestsSnapshot struct {
	Status RemotePartStatus `json:"status"`
	// Reason is one of internal/remote's short, stable reasons when the part
	// is unavailable; empty when observed.
	Reason string `json:"reason,omitempty"`
	// Detail is gh's own diagnosis, one bounded line with any credential
	// redacted; it is kept for the reader, never compared.
	Detail string `json:"detail,omitempty"`
	// Open is every open pull request, sorted by number. Empty both when
	// observed with none and when unavailable; only Status tells them apart.
	Open []OpenPullRequest `json:"open,omitempty"`
	// Repo is the GitHub repository gh was expected to read, as
	// host/owner/name, and RepoSource what it was worked out from: GH_REPO,
	// gh's default or the single GitHub remote (#410). Both are additive and
	// empty when no repository was worked out — none applied, gh was not
	// asked, or the snapshot was written before they existed.
	Repo       string `json:"repo,omitempty"`
	RepoSource string `json:"repo_source,omitempty"`
}

// OpenPullRequest is one open pull request: only what a change is keyed on.
// HeadRefOid is its head commit; updatedAt, the title and the rest are not
// collected, so a comment or a label never reads as a change (ADR 0046 §2.2).
type OpenPullRequest struct {
	Number      int    `json:"number"`
	HeadRefName string `json:"head_ref_name"`
	HeadRefOid  string `json:"head_ref_oid"`
}

// RemoteChangeKind is the kind of one RemoteChangeEntry (ADR 0046 §2.2).
type RemoteChangeKind string

const (
	RemoteAdded      RemoteChangeKind = "remote_added"
	RemoteRemoved    RemoteChangeKind = "remote_removed"
	RemoteURLChanged RemoteChangeKind = "remote_url_changed"
	BranchCreated    RemoteChangeKind = "branch_created"
	// BranchMoved carries both the old and the new sha.
	BranchMoved   RemoteChangeKind = "branch_moved"
	BranchDeleted RemoteChangeKind = "branch_deleted"
	PROpened      RemoteChangeKind = "pr_opened"
	// PRUpdated is the same PR number with its head sha moved; it carries
	// both shas.
	PRUpdated      RemoteChangeKind = "pr_updated"
	PRNoLongerOpen RemoteChangeKind = "pr_no_longer_open"
)

// RemoteAttribution is whether an entry points at a commit this run made,
// by ADR 0046 §5.1's local check on its new sha.
type RemoteAttribution string

const (
	// AttributionRunCommit: the entry's new sha is reachable from one of the
	// run's own omg/<run-id>/ branches and not from run_base_sha — "observed
	// during the run; points at a commit this run made". It says nothing
	// about who moved the ref.
	AttributionRunCommit RemoteAttribution = "run_commit"
	// AttributionNotAttributable is every other entry: one whose sha fails
	// the check, one with no new sha (remote_added, remote_removed,
	// remote_url_changed, branch_deleted, pr_no_longer_open), and every
	// between-legs entry —
	// "observed, not attributable".
	AttributionNotAttributable RemoteAttribution = "not_attributable"
)

// RemoteChangeEntry is one difference between two snapshots. Which fields
// are set follows Kind: the remote kinds carry the URLs on the side(s) that
// had the remote; the branch kinds carry Remote, Branch and the sha(s) on
// the side(s) that had the branch; the PR kinds carry PRNumber, HeadRefName
// and the head sha(s) on the side(s) that had the PR open, and no Remote.
type RemoteChangeEntry struct {
	Kind        RemoteChangeKind  `json:"kind"`
	Remote      string            `json:"remote,omitempty"`
	Branch      string            `json:"branch,omitempty"`
	PRNumber    int               `json:"pr_number,omitempty"`
	HeadRefName string            `json:"head_ref_name,omitempty"`
	OldSHA      string            `json:"old_sha,omitempty"`
	NewSHA      string            `json:"new_sha,omitempty"`
	OldFetchURL string            `json:"old_fetch_url,omitempty"`
	NewFetchURL string            `json:"new_fetch_url,omitempty"`
	OldPushURL  string            `json:"old_push_url,omitempty"`
	NewPushURL  string            `json:"new_push_url,omitempty"`
	Attribution RemoteAttribution `json:"attribution"`
}

// RemoteNotCompared names a part — or, for branches, one remote's refs —
// that yielded no entries because it could not be compared, and why. It is
// what keeps "no change" and "could not see" from reading the same.
type RemoteNotCompared struct {
	Part   RemotePart `json:"part"`
	Remote string     `json:"remote,omitempty"`
	Reason string     `json:"reason"`
}

// RemoteDiff is the comparison of two snapshots. Observed lists the parts
// that were compared (observed on both sides), so an empty Entries with a
// non-empty Observed is a seen "no change". Every slice is non-nil, so the
// record always spells all three out.
type RemoteDiff struct {
	Observed    []RemotePart        `json:"observed"`
	Entries     []RemoteChangeEntry `json:"entries"`
	NotCompared []RemoteNotCompared `json:"not_compared"`
}

// RemoteLeg is one leg's record: the before snapshot taken when it started,
// and — once it ended — the after snapshot and the diff of the two. After
// and Changes are nil on a leg that has not ended, or that stopped without
// taking one.
type RemoteLeg struct {
	Before  RemoteSnapshot  `json:"before"`
	After   *RemoteSnapshot `json:"after,omitempty"`
	Changes *RemoteDiff     `json:"changes,omitempty"`
}

// RemoteBetweenLegs is what changed between the end of leg FromLeg and the
// start of leg ToLeg (1-based indexes into RemoteChanges.Legs), ADR 0046
// §3.1's between_legs set. Its entries are always not_attributable,
// whatever their sha: the change happened while no leg ran.
type RemoteBetweenLegs struct {
	FromLeg int        `json:"from_leg"`
	ToLeg   int        `json:"to_leg"`
	Changes RemoteDiff `json:"changes"`
}

// RemoteChanges is the remote change record (ADR 0046 §2.2), stored as
// Snapshot.RemoteChanges: each leg's two snapshots and its entries, and the
// between-legs entries. It is a record of what the engine observed, for a
// report; the one decision read off it is the CLI's, on a guarded run
// (--forbid-remote-changes): a forbidden entry in a leg's own diff fails
// that leg (ADR 0046 §5.1).
type RemoteChanges struct {
	Legs        []RemoteLeg         `json:"legs"`
	BetweenLegs []RemoteBetweenLegs `json:"between_legs,omitempty"`
}

// BeginLeg records a new leg's before snapshot. Every leg takes its own
// (ADR 0046 §3.1), so a change made while the run was stopped is never
// counted against the next leg; it is diffed instead, against the previous
// leg's after, into the between-legs set with no attribution. A previous leg
// that recorded no after leaves nothing to diff against, and says so.
func (c *RemoteChanges) BeginLeg(before RemoteSnapshot) {
	if n := len(c.Legs); n > 0 {
		between := RemoteBetweenLegs{FromLeg: n, ToLeg: n + 1}
		if prev := c.Legs[n-1]; prev.After != nil {
			between.Changes = DiffRemoteSnapshots(*prev.After, before, nil)
		} else {
			reason := fmt.Sprintf("leg %d recorded no after snapshot", n)
			between.Changes = RemoteDiff{
				Observed: []RemotePart{},
				Entries:  []RemoteChangeEntry{},
				NotCompared: []RemoteNotCompared{
					{Part: RemotePartRemotes, Reason: reason},
					{Part: RemotePartBranches, Reason: reason},
					{Part: RemotePartPullRequests, Reason: reason},
				},
			}
		}
		c.BetweenLegs = append(c.BetweenLegs, between)
	}
	c.Legs = append(c.Legs, RemoteLeg{Before: before})
}

// EndLeg records the open leg's after snapshot and its diff against the
// leg's own before. isRunCommit is §5.1's check on an entry's new sha (nil
// attributes nothing). It reports false, recording nothing, when there is no
// open leg — no BeginLeg, or the last leg already ended.
func (c *RemoteChanges) EndLeg(after RemoteSnapshot, isRunCommit func(sha string) bool) bool {
	n := len(c.Legs)
	if n == 0 || c.Legs[n-1].After != nil {
		return false
	}
	leg := &c.Legs[n-1]
	diff := DiffRemoteSnapshots(leg.Before, after, isRunCommit)
	leg.After = &after
	leg.Changes = &diff
	return true
}

// DiffRemoteSnapshots compares before with after, part by part. A part
// unavailable on either side yields no entries and a not_compared item
// naming it and the reason. Branch refs are compared only for a remote both
// sides list with the same fetch URL: an added or removed remote has no refs
// on one side to compare against, and a changed fetch URL means the refs
// come from another repository; each such remote gets its own not_compared
// item, beside the remote entry that already reports it — unless the after
// read that remote's refs through the before's fetch URL
// (ReadThroughBeforeURL), which makes them the same repository's refs. Pull
// requests are
// compared on their own: gh reads the GitHub repository, not a remote, and
// two lists that name different repositories are not compared either.
//
// isRunCommit decides the attribution of a branch_created, branch_moved,
// pr_opened or pr_updated entry from its new sha; nil — the between-legs
// diff — attributes nothing. Entries come out remote kinds first, then
// branch kinds, each sorted by remote and branch, then PR kinds, sorted by
// number.
func DiffRemoteSnapshots(before, after RemoteSnapshot, isRunCommit func(sha string) bool) RemoteDiff {
	d := RemoteDiff{Observed: []RemotePart{}, Entries: []RemoteChangeEntry{}, NotCompared: []RemoteNotCompared{}}
	attribute := func(sha string) RemoteAttribution {
		if isRunCommit != nil && sha != "" && isRunCommit(sha) {
			return AttributionRunCommit
		}
		return AttributionNotAttributable
	}

	remotesCompared := false
	if reason, ok := comparable(before.Remotes.Status, before.Remotes.Reason, after.Remotes.Status, after.Remotes.Reason); ok {
		remotesCompared = true
		d.Observed = append(d.Observed, RemotePartRemotes)
		d.Entries = append(d.Entries, diffRemotes(before.Remotes.Configured, after.Remotes.Configured)...)
	} else {
		d.NotCompared = append(d.NotCompared, RemoteNotCompared{Part: RemotePartRemotes, Reason: reason})
	}

	reason, ok := comparable(before.Branches.Status, before.Branches.Reason, after.Branches.Status, after.Branches.Reason)
	switch {
	case !ok:
		d.NotCompared = append(d.NotCompared, RemoteNotCompared{Part: RemotePartBranches, Reason: reason})
	case !remotesCompared:
		// Which remotes' refs are comparable is decided by the remotes part.
		d.NotCompared = append(d.NotCompared, RemoteNotCompared{
			Part: RemotePartBranches, Reason: "the remotes part was not compared, so no remote's refs can be matched",
		})
	default:
		d.Observed = append(d.Observed, RemotePartBranches)
		entries, skipped := diffBranches(before, after, attribute)
		d.Entries = append(d.Entries, entries...)
		d.NotCompared = append(d.NotCompared, skipped...)
	}

	reason, ok = comparable(before.PullRequests.Status, before.PullRequests.Reason, after.PullRequests.Status, after.PullRequests.Reason)
	if ok {
		reason, ok = sameRepo(before.PullRequests, after.PullRequests)
	}
	if ok {
		d.Observed = append(d.Observed, RemotePartPullRequests)
		d.Entries = append(d.Entries, diffPullRequests(before.PullRequests.Open, after.PullRequests.Open, attribute)...)
	} else {
		d.NotCompared = append(d.NotCompared, RemoteNotCompared{Part: RemotePartPullRequests, Reason: reason})
	}
	return d
}

// comparable reports whether a part was observed on both sides, and when it
// was not, the reason naming the side(s) that could not see it.
func comparable(beforeStatus RemotePartStatus, beforeReason string, afterStatus RemotePartStatus, afterReason string) (string, bool) {
	beforeOK, afterOK := beforeStatus == RemotePartObserved, afterStatus == RemotePartObserved
	switch {
	case beforeOK && afterOK:
		return "", true
	case !beforeOK && !afterOK:
		return fmt.Sprintf("unavailable before (%s) and after (%s)", unavailableReason(beforeStatus, beforeReason), unavailableReason(afterStatus, afterReason)), false
	case !beforeOK:
		return fmt.Sprintf("unavailable before: %s", unavailableReason(beforeStatus, beforeReason)), false
	default:
		return fmt.Sprintf("unavailable after: %s", unavailableReason(afterStatus, afterReason)), false
	}
}

// sameRepo reports whether two pull-request parts list the same GitHub
// repository, and when they do not, the reason naming both. GitHub compares
// host and owner/name case-insensitively. A side that recorded no repository
// names none to differ from, and is compared as it always was.
func sameRepo(before, after PullRequestsSnapshot) (string, bool) {
	if before.Repo == "" || after.Repo == "" || strings.EqualFold(before.Repo, after.Repo) {
		return "", true
	}
	return fmt.Sprintf("different repos: before %s, after %s", before.Repo, after.Repo), false
}

// unavailableReason never lets a missing reason print as nothing. No status
// at all is a part the snapshot did not record — the pull requests of one
// written before that part existed; any other status that is neither value
// (a hand-edited file) is named as found.
func unavailableReason(status RemotePartStatus, reason string) string {
	switch {
	case reason != "":
		return reason
	case status == "":
		return "not recorded"
	case status != RemotePartUnavailable:
		return fmt.Sprintf("status %q", status)
	default:
		return "no reason recorded"
	}
}

func diffRemotes(before, after []RemoteConfig) []RemoteChangeEntry {
	old, cur := remotesByName(before), remotesByName(after)
	entries := []RemoteChangeEntry{}
	for _, name := range unionKeys(old, cur) {
		o, inOld := old[name]
		n, inCur := cur[name]
		switch {
		case !inOld:
			entries = append(entries, RemoteChangeEntry{Kind: RemoteAdded, Remote: name,
				NewFetchURL: n.FetchURL, NewPushURL: n.PushURL, Attribution: AttributionNotAttributable})
		case !inCur:
			entries = append(entries, RemoteChangeEntry{Kind: RemoteRemoved, Remote: name,
				OldFetchURL: o.FetchURL, OldPushURL: o.PushURL, Attribution: AttributionNotAttributable})
		case o.FetchURL != n.FetchURL || o.PushURL != n.PushURL:
			entries = append(entries, RemoteChangeEntry{Kind: RemoteURLChanged, Remote: name,
				OldFetchURL: o.FetchURL, NewFetchURL: n.FetchURL, OldPushURL: o.PushURL, NewPushURL: n.PushURL,
				Attribution: AttributionNotAttributable})
		}
	}
	return entries
}

func diffBranches(before, after RemoteSnapshot, attribute func(string) RemoteAttribution) ([]RemoteChangeEntry, []RemoteNotCompared) {
	oldRemotes, curRemotes := remotesByName(before.Remotes.Configured), remotesByName(after.Remotes.Configured)
	entries, skipped := []RemoteChangeEntry{}, []RemoteNotCompared{}

	readThrough := map[string]bool{}
	for _, name := range after.Branches.ReadThroughBeforeURL {
		readThrough[name] = true
	}
	comparedRemote := map[string]bool{}
	for _, name := range unionKeys(oldRemotes, curRemotes) {
		o, inOld := oldRemotes[name]
		n, inCur := curRemotes[name]
		switch {
		case !inOld:
			skipped = append(skipped, RemoteNotCompared{Part: RemotePartBranches, Remote: name,
				Reason: "remote added: it has no before refs to compare"})
		case readThrough[name]:
			// The before repository's refs are compared; a re-pointed
			// remote's new repository was never read, and has no before.
			comparedRemote[name] = true
			if inCur && o.FetchURL != n.FetchURL {
				skipped = append(skipped, RemoteNotCompared{Part: RemotePartBranches, Remote: name,
					Reason: "fetch URL changed: the new repository has no before refs to compare"})
			}
		case !inCur:
			skipped = append(skipped, RemoteNotCompared{Part: RemotePartBranches, Remote: name,
				Reason: "remote removed: it has no after refs to compare"})
		case o.FetchURL != n.FetchURL:
			skipped = append(skipped, RemoteNotCompared{Part: RemotePartBranches, Remote: name,
				Reason: "fetch URL changed: the refs on each side come from a different repository"})
		default:
			comparedRemote[name] = true
		}
	}

	old, cur := refsByKey(before.Branches.Refs, comparedRemote), refsByKey(after.Branches.Refs, comparedRemote)
	for _, key := range unionKeys(old, cur) {
		o, inOld := old[key]
		n, inCur := cur[key]
		switch {
		case !inOld:
			entries = append(entries, RemoteChangeEntry{Kind: BranchCreated, Remote: n.Remote, Branch: n.Branch,
				NewSHA: n.SHA, Attribution: attribute(n.SHA)})
		case !inCur:
			entries = append(entries, RemoteChangeEntry{Kind: BranchDeleted, Remote: o.Remote, Branch: o.Branch,
				OldSHA: o.SHA, Attribution: AttributionNotAttributable})
		case o.SHA != n.SHA:
			entries = append(entries, RemoteChangeEntry{Kind: BranchMoved, Remote: n.Remote, Branch: n.Branch,
				OldSHA: o.SHA, NewSHA: n.SHA, Attribution: attribute(n.SHA)})
		}
	}
	return entries, skipped
}

// diffPullRequests keys each PR on its number, and a change on its head sha
// alone: a PR whose head did not move yields no entry, whatever else about
// it did.
func diffPullRequests(before, after []OpenPullRequest, attribute func(string) RemoteAttribution) []RemoteChangeEntry {
	old, cur := prsByNumber(before), prsByNumber(after)
	numbers := make([]int, 0, len(old)+len(cur))
	for n := range old {
		numbers = append(numbers, n)
	}
	for n := range cur {
		if _, ok := old[n]; !ok {
			numbers = append(numbers, n)
		}
	}
	sort.Ints(numbers)

	entries := []RemoteChangeEntry{}
	for _, num := range numbers {
		o, inOld := old[num]
		n, inCur := cur[num]
		switch {
		case !inOld:
			entries = append(entries, RemoteChangeEntry{Kind: PROpened, PRNumber: num, HeadRefName: n.HeadRefName,
				NewSHA: n.HeadRefOid, Attribution: attribute(n.HeadRefOid)})
		case !inCur:
			entries = append(entries, RemoteChangeEntry{Kind: PRNoLongerOpen, PRNumber: num, HeadRefName: o.HeadRefName,
				OldSHA: o.HeadRefOid, Attribution: AttributionNotAttributable})
		case o.HeadRefOid != n.HeadRefOid:
			entries = append(entries, RemoteChangeEntry{Kind: PRUpdated, PRNumber: num, HeadRefName: n.HeadRefName,
				OldSHA: o.HeadRefOid, NewSHA: n.HeadRefOid, Attribution: attribute(n.HeadRefOid)})
		}
	}
	return entries
}

func prsByNumber(prs []OpenPullRequest) map[int]OpenPullRequest {
	m := make(map[int]OpenPullRequest, len(prs))
	for _, pr := range prs {
		m[pr.Number] = pr
	}
	return m
}

func remotesByName(remotes []RemoteConfig) map[string]RemoteConfig {
	m := make(map[string]RemoteConfig, len(remotes))
	for _, r := range remotes {
		m[r.Name] = r
	}
	return m
}

// refsByKey indexes the refs of the compared remotes by remote and branch.
// The key joins them with a NUL, which neither a remote name nor a ref name
// can hold, so sorting the keys sorts by remote, then branch.
func refsByKey(refs []RemoteBranchRef, compared map[string]bool) map[string]RemoteBranchRef {
	m := make(map[string]RemoteBranchRef, len(refs))
	for _, r := range refs {
		if compared[r.Remote] {
			m[r.Remote+"\x00"+r.Branch] = r
		}
	}
	return m
}

func unionKeys[V any](a, b map[string]V) []string {
	keys := make([]string, 0, len(a)+len(b))
	for k := range a {
		keys = append(keys, k)
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// clone deep-copies the record, so a recorder holding it never shares a
// slice or pointer with the caller that goes on to begin or end a leg.
func (c *RemoteChanges) clone() *RemoteChanges {
	if c == nil {
		return nil
	}
	out := &RemoteChanges{Legs: make([]RemoteLeg, len(c.Legs))}
	for i, leg := range c.Legs {
		out.Legs[i] = RemoteLeg{Before: leg.Before.clone()}
		if leg.After != nil {
			after := leg.After.clone()
			out.Legs[i].After = &after
		}
		if leg.Changes != nil {
			changes := leg.Changes.clone()
			out.Legs[i].Changes = &changes
		}
	}
	if c.BetweenLegs != nil {
		out.BetweenLegs = make([]RemoteBetweenLegs, len(c.BetweenLegs))
		for i, b := range c.BetweenLegs {
			out.BetweenLegs[i] = RemoteBetweenLegs{FromLeg: b.FromLeg, ToLeg: b.ToLeg, Changes: b.Changes.clone()}
		}
	}
	return out
}

func (s RemoteSnapshot) clone() RemoteSnapshot {
	s.Remotes.Configured = cloneSlice(s.Remotes.Configured)
	s.Branches.Refs = cloneSlice(s.Branches.Refs)
	s.Branches.ReadThroughBeforeURL = cloneSlice(s.Branches.ReadThroughBeforeURL)
	s.PullRequests.Open = cloneSlice(s.PullRequests.Open)
	return s
}

func (d RemoteDiff) clone() RemoteDiff {
	d.Observed = cloneSlice(d.Observed)
	d.Entries = cloneSlice(d.Entries)
	d.NotCompared = cloneSlice(d.NotCompared)
	return d
}

// cloneSlice copies s, keeping nil nil and empty empty, so a clone encodes
// to the same bytes as its original.
func cloneSlice[T any](s []T) []T {
	if s == nil {
		return nil
	}
	return append(make([]T, 0, len(s)), s...)
}
