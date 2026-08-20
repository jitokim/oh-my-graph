package serve

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/jitokim/oh-my-graph/internal/runstatus"
)

// ResolveRun picks which run under root (the runs directory) to serve:
//
//  1. An explicit run id wins. It must exist on disk — a mistyped id is the
//     one failure the user causes, so it is a clearly worded error rather
//     than an empty page.
//
//  2. Otherwise the newest run that is actually in flight (runstatus.Status's
//     own InFlight predicate over the same shared derivation `runs list`
//     renders from), because "the run happening right now" is what a live view
//     is for. An ABANDONED run is deliberately NOT preferred: its leg is open
//     only because the process that opened it died, and parking a live view on
//     a corpse instead of the newest real run is exactly what ADR 0015 §4
//     fixes here.
//
//     It is the PREDICATE and not an equality test, and ADR 0023 §2.1.1 names
//     this line as the one place a mechanical rewrite of that ADR would have
//     lost silently: PLANNING splits the in-flight side, so `== Running` would
//     stop preferring a run that is inside its planner call — withholding the
//     very state ADR 0023 exists to make visible, on the surface #163 names.
//
//  3. Otherwise the newest run directory.
//
// The CLI only ever takes branch 1: since the dashboard landed, `oh-my-graph
// serve` with no run id serves EVERY run rather than guessing one, so runServe
// calls this only when the user named a run. Branches 2 and 3 are the answer
// to "which single run would a caller with no id mean", which the package
// still owes callers that ask: they are exercised by TestResolveRun and relied
// on by cmd/oh-my-graph's --plan-only test, which asserts that a preview left
// nothing under runs/ for an id-less resolve to land on. Do not read them as
// the no-argument CLI path; that path is Dashboard.
//
// Newest is a descending sort of directory names: run ids are UTC timestamps
// chosen to sort lexically (see newRunID in cmd/oh-my-graph). A directory
// whose stream cannot be judged (unreadable, or a schema newer than this
// binary) is simply not *preferred* as in-flight; it can still be picked as
// the newest fallback, where the server itself reports the problem honestly
// on its endpoints.
func ResolveRun(root, explicit string) (string, error) {
	if explicit != "" {
		info, err := os.Stat(filepath.Join(root, explicit))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return "", fmt.Errorf("unknown run %q: no run directory under %s (see `oh-my-graph runs list`)", explicit, root)
			}
			return "", fmt.Errorf("stat run %q: %w", explicit, err)
		}
		if !info.IsDir() {
			// Fail here with a clear message rather than confusingly at the
			// endpoints — a run is always a directory.
			return "", fmt.Errorf("run %q is not a run directory under %s (see `oh-my-graph runs list`)", explicit, root)
		}
		return explicit, nil
	}

	runIDs, err := listRunIDs(root)
	if err != nil {
		return "", err
	}
	if len(runIDs) == 0 {
		return "", fmt.Errorf("no runs found under %s (start one with `oh-my-graph run` or `auto`)", root)
	}

	for _, runID := range runIDs {
		status, err := runstatus.Of(filepath.Join(root, runID))
		// Deliberately silent, unlike every other surface the skip report
		// reached: this loop is only asking "is this one in flight?", and a run
		// this build cannot read cannot be in flight. Reporting the error here
		// would not change the answer, and refusing to skip past it would
		// change which run `serve` opens.
		if err == nil && status.InFlight() {
			return runID, nil
		}
	}
	return runIDs[0], nil
}
