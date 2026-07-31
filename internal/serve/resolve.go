package serve

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/jitokim/oh-my-graph/internal/runfeed"
)

// ResolveRun picks which run under root (the runs directory) to serve:
//
//  1. An explicit run id wins. It must exist on disk — a mistyped id is the
//     one failure the user causes, so it is a clearly worded error rather
//     than an empty page.
//  2. Otherwise the newest run whose event stream says it is in flight
//     (runfeed.InFlight — the same leg-walking `runs list` uses to render
//     RUNNING), because "the run happening right now" is what a live view is
//     for.
//  3. Otherwise the newest run directory, so `oh-my-graph serve` right after
//     a run finishes shows that run.
//
// Newest is a descending sort of directory names: run ids are UTC timestamps
// chosen to sort lexically (see newRunID in cmd/oh-my-graph). A directory
// whose stream cannot be judged (unreadable, or a schema newer than this
// binary) is simply not *preferred* as in-flight; it can still be picked as
// the newest fallback, where the server itself reports the problem honestly
// on its endpoints.
func ResolveRun(root, explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(filepath.Join(root, explicit)); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return "", fmt.Errorf("unknown run %q: no run directory under %s (see `oh-my-graph runs list`)", explicit, root)
			}
			return "", fmt.Errorf("stat run %q: %w", explicit, err)
		}
		return explicit, nil
	}

	entries, err := os.ReadDir(root)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("read runs dir %q: %w", root, err)
	}
	var runIDs []string
	for _, entry := range entries {
		if entry.IsDir() {
			runIDs = append(runIDs, entry.Name())
		}
	}
	if len(runIDs) == 0 {
		return "", fmt.Errorf("no runs found under %s (start one with `oh-my-graph run` or `auto`)", root)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(runIDs)))

	for _, runID := range runIDs {
		inFlight, err := runfeed.InFlight(filepath.Join(root, runID, runfeed.FileName))
		if err == nil && inFlight {
			return runID, nil
		}
	}
	return runIDs[0], nil
}
