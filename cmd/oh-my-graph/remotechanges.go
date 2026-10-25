package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/fence"
	"github.com/jitokim/oh-my-graph/internal/remote"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
	"github.com/jitokim/oh-my-graph/internal/worktree"
)

// remoteObserverFor is what a leg reads the invocation repository's remote
// state through (ADR 0046 §2.3): the run's own GitManager, whose branch
// prefix is what the §5.1 attribution check looks under. Tests swap it for a
// worktree.FakeRemoteObserver, so no test reaches a real remote; production
// code never reassigns it.
var remoteObserverFor = func(runID string) worktree.RemoteObserver {
	return worktreeManagerFor(runID)
}

// remotePRListerFor is what a leg reads the invocation repository's open
// pull requests through (ADR 0046 §2.1): the fifth seam's GHRunner, run in
// the same repository the GitManager reads ("" = the working directory).
// Tests swap it for a remote.FakePRLister, so no test runs a real gh;
// production code never reassigns it.
var remotePRListerFor = func(string) remote.PRLister {
	return remote.NewGHRunner("")
}

// runBaseCommit reads the run's base commit — the invocation repository's
// HEAD, "" outside one — once, at the start of a run's first leg (ADR 0046
// §5.1). An indirection for the same reason as remoteObserverFor.
var runBaseCommit = launchCommitOfWorkingDir

// remoteLeg records one leg's remote changes (ADR 0046 §2.2, §3.1): a before
// snapshot when the leg starts, an after snapshot when its last node ends,
// and the record written into state.json at each. It is a report: nothing it
// observes, and no write of it that fails, stops the leg — and only on a
// guarded run (--forbid-remote-changes) does a forbidden entry change its exit
// (ADR 0046 §5.1).
type remoteLeg struct {
	obs      worktree.RemoteObserver
	prs      remote.PRLister
	recorder *runstate.SnapshotRecorder
	baseSHA  string
	guarded  bool
	changes  runstate.RemoteChanges
}

// beginRemoteLeg takes the leg's before snapshot and records it. prior is the
// record the earlier legs left in state.json (nil on a first leg); the new
// leg is appended to a copy of it, so the change since the last leg's after
// lands in between_legs, never attributed. guarded is the run's
// --forbid-remote-changes, which finish checks the leg's own entries against.
func beginRemoteLeg(ctx context.Context, runID string, recorder *runstate.SnapshotRecorder, baseSHA string, prior *runstate.RemoteChanges, guarded bool) *remoteLeg {
	l := &remoteLeg{obs: remoteObserverFor(runID), prs: remotePRListerFor(runID), recorder: recorder, baseSHA: baseSHA, guarded: guarded}
	if prior != nil {
		l.changes = cloneRemoteChanges(*prior)
	}
	l.changes.BeginLeg(worktree.TakeRemoteSnapshot(ctx, l.obs, l.prs))
	l.record()
	return l
}

// end takes the leg's after snapshot, diffs it against the before with the
// §5.1 check, and records it. runCtx is the leg's own context: once the user
// has interrupted the run (Ctrl-C, SIGTERM) the after snapshot is not taken —
// an unreachable remote would hold the exit for the git or gh timeout — and
// all three parts are recorded unavailable, "interrupted", so the record never
// reads "no change". Otherwise it runs on a context of its own, since a
// halt-on-fail cancels only the scheduler's, and the leg still ended; that
// context is cancelled with runCtx, so an interrupt while the snapshot runs
// kills its git and gh and records it interrupted the same way. It reports
// whether the leg was interrupted by the time the record was written.
func (l *remoteLeg) end(runCtx context.Context) (interrupted bool) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := context.AfterFunc(runCtx, cancel)
	defer stop()

	after := interruptedRemoteSnapshot
	if runCtx.Err() == nil {
		after = worktree.TakeRemoteSnapshot(ctx, l.obs, l.prs)
	}
	// Read after the snapshot: an interrupt that landed while it ran made
	// its parts git's kill, not the remote's state.
	interrupted = runCtx.Err() != nil
	if interrupted {
		after = interruptedRemoteSnapshot
	}
	// The commit check is local and bounded: it runs on a context of its
	// own, so an interrupt that lands after the read above cannot fail it
	// and record a run commit not_attributable.
	l.changes.EndLeg(after, worktree.RunCommitCheck(context.Background(), l.obs, l.baseSHA))
	l.record()
	return interrupted
}

// finishHold is a leg's event sink while its scheduler runs: it passes every
// event on but holds the scheduler's run_finished back until the leg's after
// snapshot has ended (finish), so an interrupt during that snapshot can still
// close the leg as interrupted rather than as the pause or the pass the
// scheduler reported a moment before.
type finishHold struct {
	sink     schedule.EventSink
	finished *runfeed.Event
}

func (h *finishHold) Emit(e runfeed.Event) error {
	if e.Type == runfeed.EventRunFinished {
		h.finished = &e
		return nil
	}
	return h.sink.Emit(e)
}

// finish ends a leg whose scheduler returned schedErr: it takes the after
// snapshot (end), writes the run_finished hold kept back, and returns the
// error the leg exits with. An interrupt during the after snapshot ends the
// leg as interrupted, like one during any node: a pause or a pass the
// scheduler reported becomes runCtx's error (exit 1), the pause it recorded
// is withdrawn from state.json and run_finished says failed, so the exit, the
// snapshot, the stream and the pause hint (none) agree. A failure the
// scheduler returned is kept — its exit is 1 already and it says more.
//
// On a guarded run, a leg whose own entries hold a forbidden one (§5.1) ends
// with a *RemoteChangeForbiddenError (exit 6) whatever the scheduler
// returned (ADR 0046 §3.1): a node failure is kept inside it, so both are
// reported, and a pause or a pass is overridden as an interrupt overrides
// them — the pause withdrawn, run_finished failed. Between-legs entries are
// never checked, and an interrupted leg has none to check.
func (l *remoteLeg) finish(runCtx context.Context, hold *finishHold, schedErr error) error {
	runErr := schedErr
	interrupted := l.end(runCtx)
	switch {
	case interrupted && pausedOrPassed(schedErr):
		runErr = runCtx.Err()
		l.failLeg(hold, "interrupted")
	case !interrupted && l.guarded:
		if forbidden := forbiddenRemoteEntries(l.changes); len(forbidden) > 0 {
			runErr = &RemoteChangeForbiddenError{Entries: forbidden, Run: schedErr}
			if pausedOrPassed(schedErr) {
				l.failLeg(hold, "failed")
			}
		}
	}
	if hold.finished != nil {
		if err := hold.sink.Emit(*hold.finished); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: could not record this leg's run_finished: %v\n", err)
		}
	}
	return runErr
}

// failLeg turns a leg the scheduler reported paused or passed into a failed
// one: its pause, if any, is withdrawn from state.json and the held
// run_finished says failed. how names the leg's end in the warning.
func (l *remoteLeg) failLeg(hold *finishHold, how string) {
	if err := l.recorder.ClearPause(); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: could not withdraw this %s leg's pause: %v\n", how, err)
	}
	if hold.finished != nil {
		hold.finished.Outcome = runfeed.OutcomeFailed
		hold.finished.Detail = ""
	}
}

// forbiddenRemoteEntries is the last leg's own forbidden entries (ADR 0046
// §5.1), in record order. Only the leg's diff is read: a between-legs entry
// happened while no leg ran and is never forbidden.
func forbiddenRemoteEntries(c runstate.RemoteChanges) []runstate.RemoteChangeEntry {
	n := len(c.Legs)
	if n == 0 || c.Legs[n-1].Changes == nil {
		return nil
	}
	var forbidden []runstate.RemoteChangeEntry
	for _, e := range c.Legs[n-1].Changes.Entries {
		if remoteEntryForbidden(e) {
			forbidden = append(forbidden, e)
		}
	}
	return forbidden
}

// remoteEntryForbidden is §5.1's list, and nothing else is ever forbidden:
// a new branch or pull request at a commit this run made, or a new remote.
// branch_moved and pr_updated are reported only, whatever their label.
func remoteEntryForbidden(e runstate.RemoteChangeEntry) bool {
	switch e.Kind {
	case runstate.BranchCreated, runstate.PROpened:
		return e.Attribution == runstate.AttributionRunCommit
	case runstate.RemoteAdded:
		return true
	default:
		return false
	}
}

// RemoteChangeForbiddenError is a guarded leg's end when its change record
// holds a forbidden entry (ADR 0046 §5.1): exit 6, and the run reads FAIL.
// It is not a node failure and nothing prunes it; Run is what the scheduler
// returned beside it — nil when every node passed, a node failure that is
// reported with it, or the pause it withdrew. It deliberately has no Unwrap:
// a withdrawn pause must not read as one to exitCodeForError or the pause
// hint.
type RemoteChangeForbiddenError struct {
	Entries []runstate.RemoteChangeEntry
	Run     error
}

func (e *RemoteChangeForbiddenError) Error() string {
	entries := make([]string, len(e.Entries))
	for i, entry := range e.Entries {
		entries[i] = fence.SanitizeTerminalLine(describeRemoteEntry(entry)) + " (" + attributionLabel(entry.Attribution) + ")"
	}
	msg := "new remote state observed during the run, and --forbid-remote-changes was set: " + strings.Join(entries, "; ")
	var paused *schedule.PausedError
	var limited *schedule.LimitPausedError
	switch {
	case e.Run == nil:
	case errors.As(e.Run, &paused):
		msg += fmt.Sprintf("; the leg had paused at gate %q, and that pause is withdrawn", paused.GateID)
	case errors.As(e.Run, &limited):
		msg += fmt.Sprintf("; the leg had paused on the session limit at %s, and that pause is withdrawn", strings.Join(limited.NodeIDs, ", "))
	default:
		msg += "; the leg also failed: " + e.Run.Error()
	}
	return msg
}

// pausedOrPassed reports whether the scheduler's return is a pass, a gate
// pause or a session-limit pause: the outcomes an interrupt during the after
// snapshot overrides.
func pausedOrPassed(schedErr error) bool {
	var paused *schedule.PausedError
	var limited *schedule.LimitPausedError
	return schedErr == nil || errors.As(schedErr, &paused) || errors.As(schedErr, &limited)
}

// remoteSnapshotInterrupted is the reason of an after snapshot not taken
// because the run was interrupted.
const remoteSnapshotInterrupted = "interrupted"

var interruptedRemoteSnapshot = runstate.UnavailableRemoteSnapshot(remoteSnapshotInterrupted)

func (l *remoteLeg) record() {
	if err := l.recorder.RecordRemoteChanges(l.changes); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: could not record this leg's remote changes: %v\n", err)
	}
}

// print writes what this leg observed, after the ledger table.
func (l *remoteLeg) print(w io.Writer) { printRemoteChanges(w, &l.changes) }

// cloneRemoteChanges copies c deep enough for BeginLeg and EndLeg: they only
// append legs and set the last leg's After and Changes, so fresh top-level
// slices keep the loaded snapshot's record untouched.
func cloneRemoteChanges(c runstate.RemoteChanges) runstate.RemoteChanges {
	return runstate.RemoteChanges{
		Legs:        append([]runstate.RemoteLeg(nil), c.Legs...),
		BetweenLegs: append([]runstate.RemoteBetweenLegs(nil), c.BetweenLegs...),
	}
}

// The two attribution labels (ADR 0046 §2.2), word for word. Every entry is
// "observed": the snapshot sees state, not who changed it.
const (
	labelRunCommit       = "observed during the run; points at a commit this run made"
	labelNotAttributable = "observed, not attributable"
)

// printRemoteChanges prints the last leg's remote change record: the change
// since the previous leg's end when there was one, then the leg's own. A
// diff with nothing in it prints one line naming the parts it observed; a
// part that could not be compared gets a line saying why, so "no change"
// and "could not see" never read the same.
func printRemoteChanges(w io.Writer, c *runstate.RemoteChanges) {
	n := len(c.Legs)
	if n == 0 {
		return
	}
	fmt.Fprintln(w)
	leg := c.Legs[n-1]
	for _, between := range c.BetweenLegs {
		if between.ToLeg == n {
			printRemoteDiff(w, fmt.Sprintf("Remote changes between leg %d and leg %d, while no leg ran", between.FromLeg, between.ToLeg), between.Changes, leg.Before.PullRequests)
		}
	}
	if leg.Changes != nil {
		var prs runstate.PullRequestsSnapshot
		if leg.After != nil {
			prs = leg.After.PullRequests
		}
		printRemoteDiff(w, "Remote changes", *leg.Changes, prs)
	}
}

// printRemoteDiff prints d. prs is the diff's after pull-request part: when
// that part was compared, both sides name the same repo, and the summary
// line names it with where it was worked out from.
func printRemoteDiff(w io.Writer, heading string, d runstate.RemoteDiff, prs runstate.PullRequestsSnapshot) {
	observed := "none"
	if len(d.Observed) > 0 {
		parts := make([]string, len(d.Observed))
		for i, p := range d.Observed {
			parts[i] = string(p)
			if p == runstate.RemotePartPullRequests {
				parts[i] = describePRPart(prs)
			}
		}
		observed = strings.Join(parts, ", ")
	}
	if len(d.Entries) == 0 && len(d.NotCompared) == 0 {
		fmt.Fprintf(w, "%s: none observed (observed: %s)\n", heading, observed)
		return
	}
	if len(d.Entries) == 0 {
		fmt.Fprintf(w, "%s: none observed in the parts compared (observed: %s)\n", heading, observed)
	} else {
		fmt.Fprintf(w, "%s (observed: %s):\n", heading, observed)
	}
	for _, e := range d.Entries {
		fmt.Fprintf(w, "  %s — %s\n", fence.SanitizeTerminalLine(describeRemoteEntry(e)), attributionLabel(e.Attribution))
	}
	for _, nc := range d.NotCompared {
		part := string(nc.Part)
		if nc.Remote != "" {
			part += " of " + nc.Remote
		}
		fmt.Fprintf(w, "  not compared: %s\n", fence.SanitizeTerminalLine(part+": "+nc.Reason))
	}
}

// describePRPart names the pull-request part, and the repo gh read when one
// was recorded: "pull_requests of github.com/owner/name (gh default)".
func describePRPart(prs runstate.PullRequestsSnapshot) string {
	part := string(runstate.RemotePartPullRequests)
	if prs.Repo == "" {
		return part
	}
	part += " of " + prs.Repo
	if prs.RepoSource != "" {
		part += " (" + prs.RepoSource + ")"
	}
	return fence.SanitizeTerminalLine(part)
}

func attributionLabel(a runstate.RemoteAttribution) string {
	if a == runstate.AttributionRunCommit {
		return labelRunCommit
	}
	return labelNotAttributable
}

// describeRemoteEntry is one entry's kind and what it changed.
func describeRemoteEntry(e runstate.RemoteChangeEntry) string {
	ref := e.Remote + "/" + e.Branch
	switch e.Kind {
	case runstate.RemoteAdded:
		return fmt.Sprintf("%s %s %s", e.Kind, e.Remote, describeURLs(e.NewFetchURL, e.NewPushURL))
	case runstate.RemoteRemoved:
		return fmt.Sprintf("%s %s (was %s)", e.Kind, e.Remote, describeURLs(e.OldFetchURL, e.OldPushURL))
	case runstate.RemoteURLChanged:
		return fmt.Sprintf("%s %s %s -> %s", e.Kind, e.Remote, describeURLs(e.OldFetchURL, e.OldPushURL), describeURLs(e.NewFetchURL, e.NewPushURL))
	case runstate.BranchCreated:
		return fmt.Sprintf("%s %s at %s", e.Kind, ref, shortSHA(e.NewSHA))
	case runstate.BranchMoved:
		return fmt.Sprintf("%s %s %s -> %s", e.Kind, ref, shortSHA(e.OldSHA), shortSHA(e.NewSHA))
	case runstate.BranchDeleted:
		return fmt.Sprintf("%s %s (was %s)", e.Kind, ref, shortSHA(e.OldSHA))
	case runstate.PROpened:
		return fmt.Sprintf("%s #%d %s at %s", e.Kind, e.PRNumber, e.HeadRefName, shortSHA(e.NewSHA))
	case runstate.PRUpdated:
		return fmt.Sprintf("%s #%d %s %s -> %s", e.Kind, e.PRNumber, e.HeadRefName, shortSHA(e.OldSHA), shortSHA(e.NewSHA))
	case runstate.PRNoLongerOpen:
		return fmt.Sprintf("%s #%d %s (was %s)", e.Kind, e.PRNumber, e.HeadRefName, shortSHA(e.OldSHA))
	default:
		return fmt.Sprintf("%s %s", e.Kind, e.Remote)
	}
}

// describeURLs names a remote's fetch URL, and its push URL only when that
// differs.
func describeURLs(fetch, push string) string {
	if push == "" || push == fetch {
		return fetch
	}
	return fetch + " (push " + push + ")"
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
