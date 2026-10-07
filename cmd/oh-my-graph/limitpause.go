package main

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/fence"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// limitPauseSummary names a usage-limit pause from its record (ADR 0031 §8.3):
// `usage limit at <node ids>: <cause>`, or without the cause when none was
// captured. The cause is runtime text and the node ids may be planner-written,
// so the whole line goes through fence.SanitizeTerminalLine.
func limitPauseSummary(p *runstate.LimitPause) string {
	s := "usage limit at " + strings.Join(p.NodeIDs, ", ")
	if p.Cause != "" {
		s += ": " + p.Cause
	}
	return fence.SanitizeTerminalLine(s)
}

// recordedLimitPause is the limit-pause record the leg that just ended wrote,
// or nil when runErr is not a limit pause or the snapshot holds no record (its
// write is non-fatal, so it may be missing). Read only for display: the
// pause hint names the cause from it (ADR 0031 §8.3).
func recordedLimitPause(runID string, runErr error) *runstate.LimitPause {
	var limited *schedule.LimitPausedError
	if !errors.As(runErr, &limited) {
		return nil
	}
	snap, err := runstate.Load(filepath.Join(runDirFor(runID), stateFileName))
	if err != nil {
		return nil
	}
	return snap.LimitPause
}
