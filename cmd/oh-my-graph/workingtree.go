package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/fence"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// workingTreeLinePaths is how many paths the working tree line names before
// it elides the rest with "…".
const workingTreeLinePaths = 5

// readWorkingTree reads the invocation repository's uncommitted paths at a
// leg's end (#455), through the leg's RemoteObserver: the record for them,
// nil for a clean tree. A tree git could not read is nil too — nothing is
// recorded and nothing printed — so it never reads as a failure of the run.
func (l *remoteLeg) readWorkingTree(ctx context.Context) *runstate.WorkingTree {
	paths, err := l.obs.WorkingTreeChanges(ctx)
	if err != nil {
		return nil
	}
	return runstate.NewWorkingTree(paths)
}

// recordWorkingTree stores the working tree this leg ended with in
// state.json, replacing an earlier leg's; nil removes it. Like the remote
// record it is a report: a failed write warns and the leg goes on.
func (l *remoteLeg) recordWorkingTree() {
	if err := l.recorder.RecordWorkingTree(l.workingTree); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: could not record this leg's working tree: %v\n", err)
	}
}

// workingTreeLine is the one summary line a dirty working tree prints after
// the ledger, at a leg's end and in `show` alike (#455): "working tree: N
// files changed, uncommitted (a, b, …)", naming the first
// workingTreeLinePaths paths. A clean tree, or none recorded, is "".
func workingTreeLine(wt *runstate.WorkingTree) string {
	if wt == nil || wt.Changed <= 0 {
		return ""
	}
	files := "files"
	if wt.Changed == 1 {
		files = "file"
	}
	paths := wt.Paths
	more := ""
	if len(paths) > workingTreeLinePaths {
		paths, more = paths[:workingTreeLinePaths], ", …"
	}
	return fence.SanitizeTerminalLine(fmt.Sprintf("working tree: %d %s changed, uncommitted (%s%s)", wt.Changed, files, strings.Join(paths, ", "), more))
}

// printWorkingTree writes workingTreeLine, when there is one.
func printWorkingTree(w io.Writer, wt *runstate.WorkingTree) {
	if line := workingTreeLine(wt); line != "" {
		fmt.Fprintln(w, line)
	}
}
