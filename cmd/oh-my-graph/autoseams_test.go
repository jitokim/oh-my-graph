package main

import (
	"os"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/verify"
)

// runAutoWith is runAuto with its seams injectable, mirroring runGraphWith and
// for the same reason: --plan-only's whole claim is that no node runs, and the
// only way to prove that is through the real argv path with a FakeRunner that
// must see the planner call and nothing else. The stdout parameter is what
// gates the live view (a non-terminal one leaves it off), so a test needs no
// real spawn on that seam either. The starting-tree baseline (#315) gets
// greenBaseline, so a test written before it — and any test not about it —
// neither spawns a real shell nor changes behaviour beyond the green line.
func runAutoWith(args []string, nodeRunner runner.NodeRunner, opener browser.Opener, stdout *os.File) error {
	return runAutoWithRuntime(runner.RuntimeClaude, args, nodeRunner, opener, stdout, osStdin(), greenBaseline(args))
}

// greenBaseline is a FakeVerifier that answers exit 0 for the --verify-cmd in
// args (#315). FakeVerifier errors on an unscripted command, and an error is
// a red baseline, so the command must be scripted by name; with no
// --verify-cmd the verifier is never asked and the empty script is right.
func greenBaseline(args []string) *verify.FakeVerifier {
	results := map[string]verify.Result{}
	for i, arg := range args {
		switch {
		case (arg == "--verify-cmd" || arg == "-verify-cmd") && i+1 < len(args):
			results[args[i+1]] = verify.Result{ExitCode: 0}
		case strings.HasPrefix(arg, "--verify-cmd="):
			results[strings.TrimPrefix(arg, "--verify-cmd=")] = verify.Result{ExitCode: 0}
		}
	}
	return verify.NewFakeVerifier(results)
}
