package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/serve"
)

// webOpener decides whether a leg — a fresh `run`/`auto`, or a `resume` of an
// earlier one — gets the embedded live view, and with
// which Opener: the caller's opener when stdout is a terminal and --no-web was
// not passed, nil (no server, no browser, no output change) otherwise. The
// nil-for-none convention matches executeGraph's web parameter. Gating on
// stdout keeps every scripted use — CI, pipes, redirection — byte-identical
// to a build without the live view, with --no-web as the explicit opt-out for
// an interactive run.
func webOpener(noWeb bool, stdout *os.File, opener browser.Opener) browser.Opener {
	if noWeb || !isTerminal(stdout) {
		return nil
	}
	return opener
}

// isTerminal reports whether f is a character device — a terminal, as opposed
// to the pipe or file stdout is in scripts, CI and tests. The stdlib-only
// check; good enough here because a false negative just means no auto-opened
// browser, never a wrong run.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// startLiveView embeds `serve`'s live view for the duration of one run: bind
// an ephemeral loopback port (serve.Listen), announce and serve exactly as
// the standalone subcommand does (serveRun — the same handler and lifecycle,
// not a fork of them), and hand the URL to the injected Opener. A failure
// never fails the run — the live view is a convenience around the run, not
// part of it — so a bind error disables it with a stderr note and a failed
// browser launch leaves the printed URL as the way in.
//
// The returned stop tears the server down and waits for it to exit; callers
// defer it so the server lives exactly as long as the run.
func startLiveView(ctx context.Context, opener browser.Opener, runID string) (stop func()) {
	// Port 0: an ephemeral port. The run's view is announced and auto-opened,
	// so a memorable port number buys nothing, and a fixed one would collide
	// with a standalone `serve` or a concurrent run.
	listener, err := serve.Listen(0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "live view disabled: %v\n", err)
		return func() {}
	}

	serveCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := serveRun(serveCtx, os.Stdout, listener, runDirFor(runID), runID); err != nil {
			fmt.Fprintf(os.Stderr, "live view: %v\n", err)
		}
	}()

	if err := opener.Open(ctx, "http://"+listener.Addr().String()+"/"); err != nil {
		fmt.Fprintf(os.Stderr, "open live view in browser: %v — the printed URL still works\n", err)
	}
	return func() {
		cancel()
		<-done
	}
}
