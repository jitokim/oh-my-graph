package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/fence"
	"github.com/jitokim/oh-my-graph/internal/runstate"
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

// runBaseCommit reads the run's base commit — the invocation repository's
// HEAD, "" outside one — once, at the start of a run's first leg (ADR 0046
// §5.1). An indirection for the same reason as remoteObserverFor.
var runBaseCommit = launchCommitOfWorkingDir

// remoteLeg records one leg's remote changes (ADR 0046 §2.2, §3.1): a before
// snapshot when the leg starts, an after snapshot when its last node ends,
// and the record written into state.json at each. It is a report: nothing it
// observes, and no write of it that fails, stops the leg or changes its exit.
type remoteLeg struct {
	obs      worktree.RemoteObserver
	recorder *runstate.SnapshotRecorder
	baseSHA  string
	changes  runstate.RemoteChanges
}

// beginRemoteLeg takes the leg's before snapshot and records it. prior is the
// record the earlier legs left in state.json (nil on a first leg); the new
// leg is appended to a copy of it, so the change since the last leg's after
// lands in between_legs, never attributed.
func beginRemoteLeg(ctx context.Context, runID string, recorder *runstate.SnapshotRecorder, baseSHA string, prior *runstate.RemoteChanges) *remoteLeg {
	l := &remoteLeg{obs: remoteObserverFor(runID), recorder: recorder, baseSHA: baseSHA}
	if prior != nil {
		l.changes = cloneRemoteChanges(*prior)
	}
	l.changes.BeginLeg(worktree.TakeRemoteSnapshot(ctx, l.obs))
	l.record()
	return l
}

// end takes the leg's after snapshot, diffs it against the before with the
// §5.1 check, and records it. runCtx is the leg's own context: once the user
// has interrupted the run (Ctrl-C, SIGTERM) the after snapshot is not taken
// — an unreachable remote would hold the exit for the git timeout — and both
// parts are recorded unavailable, "interrupted", so the record never reads
// "no change". Otherwise it runs on a fresh context, since a halt-on-fail
// cancels only the scheduler's own, and the leg still ended.
func (l *remoteLeg) end(runCtx context.Context) {
	ctx := context.Background()
	after := interruptedRemoteSnapshot
	if runCtx.Err() == nil {
		after = worktree.TakeRemoteSnapshot(ctx, l.obs)
	}
	l.changes.EndLeg(after, worktree.RunCommitCheck(ctx, l.obs, l.baseSHA))
	l.record()
}

// remoteSnapshotInterrupted is the reason of an after snapshot not taken
// because the run was interrupted.
const remoteSnapshotInterrupted = "interrupted"

var interruptedRemoteSnapshot = runstate.RemoteSnapshot{
	Remotes:  runstate.RemotesSnapshot{Status: runstate.RemotePartUnavailable, Reason: remoteSnapshotInterrupted},
	Branches: runstate.BranchesSnapshot{Status: runstate.RemotePartUnavailable, Reason: remoteSnapshotInterrupted},
}

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
	for _, between := range c.BetweenLegs {
		if between.ToLeg == n {
			printRemoteDiff(w, fmt.Sprintf("Remote changes between leg %d and leg %d, while no leg ran", between.FromLeg, between.ToLeg), between.Changes)
		}
	}
	if leg := c.Legs[n-1]; leg.Changes != nil {
		printRemoteDiff(w, "Remote changes", *leg.Changes)
	}
}

func printRemoteDiff(w io.Writer, heading string, d runstate.RemoteDiff) {
	observed := "none"
	if len(d.Observed) > 0 {
		parts := make([]string, len(d.Observed))
		for i, p := range d.Observed {
			parts[i] = string(p)
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
