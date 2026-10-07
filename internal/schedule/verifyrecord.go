package schedule

import (
	"context"
	"errors"
	"time"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/verify"
)

// ranVerificationRecord is the snapshot's record of a verification command
// that RAN and exited (#332): the exit code it really exited with, and the
// marked byte-bounded tail of what it printed. verdictErr is
// judgeVerification's answer for it.
//
// A command that exited but could not be JUDGED — judgeVerification's fault
// path, an invalid output_matches on a hand-built node — still records its
// exit code. The status already says no verdict was reached, and the exit code
// is a fact the engine observed; withholding it would make the record claim
// less than happened, and a reader can tell the two cases apart by Status.
//
// A command ended by a signal — killed, OOM-killed, crashed — never exited on
// its own, and verify reports it as exit code -1 (os.ProcessState's own
// "no exit code"). That -1 is not an exit code, so it is not recorded: the
// field stays absent, never faked. The status is still the verdict the node
// got for it, a failure, because -1 matched no expected code.
func ranVerificationRecord(command string, v graph.Verification, duration time.Duration, result verify.Result, verdictErr error) *runstate.VerificationRecord {
	status := runstate.VerificationPassed
	if verdictErr != nil {
		status = runstate.VerificationFailed
		var checkErr *NodeCheckError
		if errors.As(verdictErr, &checkErr) && checkErr.Infrastructure {
			status = runstate.VerificationNotJudged
		}
	}
	rec := &runstate.VerificationRecord{
		Command:          command,
		ExpectedExitCode: v.ExpectedExitCode(),
		Duration:         duration,
		Status:           status,
		OutputTail:       verify.RetainedTail(result.Output),
	}
	if result.ExitCode >= 0 {
		exitCode := result.ExitCode
		rec.ExitCode = &exitCode
	}
	return rec
}

// brokenVerificationRecord is the snapshot's record of a verification that
// broke before a verdict: the Verifier returned an error instead of a Result.
// There is no exit code to record, so none is — a timed-out command was
// killed, a cancelled one was killed, and one that could not start never
// exited. A *verify.TimeoutError's own bounded Output is the only output any
// of them captured, and it is kept as the tail.
func brokenVerificationRecord(command string, v graph.Verification, duration time.Duration, err error) *runstate.VerificationRecord {
	rec := &runstate.VerificationRecord{
		Command:          command,
		ExpectedExitCode: v.ExpectedExitCode(),
		Duration:         duration,
		Status:           runstate.VerificationDidNotRun,
	}
	var timeout *verify.TimeoutError
	switch {
	case errors.As(err, &timeout):
		rec.Status = runstate.VerificationTimedOut
		rec.OutputTail = verify.RetainedTail(timeout.Output)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		rec.Status = runstate.VerificationCancelled
	}
	return rec
}

// unresolvedVerificationRecord is the snapshot's record of a verification
// whose command or cwd did not interpolate, so nothing ran. The command is
// the DECLARED text: no resolved one exists, and the declared one is what a
// reader needs to see why it did not resolve.
func unresolvedVerificationRecord(v graph.Verification) *runstate.VerificationRecord {
	return &runstate.VerificationRecord{
		Command:          v.Command,
		ExpectedExitCode: v.ExpectedExitCode(),
		Status:           runstate.VerificationInterpolationError,
	}
}
