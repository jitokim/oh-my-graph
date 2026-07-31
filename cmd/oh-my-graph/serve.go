package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jitokim/oh-my-graph/internal/serve"
)

// serveFlags holds the parsed `serve` subcommand options. The run id is an
// optional positional argument: omitted, `serve` prefers the run currently
// in flight and falls back to the newest one (serve.ResolveRun).
type serveFlags struct {
	runID string
	port  int

	set *flag.FlagSet
}

// newServeFlags builds a serveFlags with its FlagSet configured.
func newServeFlags() *serveFlags {
	f := &serveFlags{set: flag.NewFlagSet("serve", flag.ContinueOnError)}
	f.set.IntVar(&f.port, "port", serve.DefaultPort, "port to serve the live view on (always bound to 127.0.0.1)")
	return f
}

// parse reads args in the order `[<run-id>] [flags...]`, mirroring the other
// subcommands' positional-first convention — except the positional is
// optional here, so a leading flag is flags-only argv, not an error.
func (f *serveFlags) parse(args []string) error {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		f.runID = args[0]
		args = args[1:]
	}
	if err := f.set.Parse(args); err != nil {
		return err
	}
	if f.set.NArg() > 0 {
		return fmt.Errorf("serve: unexpected argument %q (usage: oh-my-graph serve [<run-id>] [--port N])", f.set.Arg(0))
	}
	return nil
}

// runServe is the `serve` subcommand: resolve which run to show, bind the
// loopback-only listener, print the URL, and serve the read-only live view
// until interrupted. The URL is printed rather than the browser being opened:
// auto-opening would shell out to `open`/`xdg-open`, a fourth exec seam the
// invariant in internal/invariants forbids without its own ADR — a deliberate
// follow-up, not an oversight (see the internal/serve package doc).
func runServe(args []string) error {
	flags := newServeFlags()
	if err := flags.parse(args); err != nil {
		return err
	}

	runID, err := serve.ResolveRun(runsRoot(), flags.runID)
	if err != nil {
		return err
	}

	// Bound to 127.0.0.1 only — run directories hold prompts and session ids,
	// so the live view must never be reachable off-host (serve.Listen).
	listener, err := serve.Listen(flags.port)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return serveRun(ctx, os.Stdout, listener, runDirFor(runID), runID)
}

// serveRun announces the URL and serves the live view on listener until ctx
// is cancelled (Ctrl-C), which is the normal way to stop and not a failure.
// Serve runs in a goroutine and this function selects on its result against
// ctx, so a Serve that fails on its own surfaces immediately with nothing
// left running, and a cancel drains the Serve goroutine before returning.
// Split from runServe so a test can drive it with its own listener and
// context, no signals involved.
func serveRun(ctx context.Context, w io.Writer, listener net.Listener, runDir, runID string) error {
	fmt.Fprintf(w, "Serving live view of run %s at http://%s/\nOpen it in your browser; Ctrl-C stops the server.\n", runID, listener.Addr())

	server := &http.Server{
		Handler: serve.New(runDir, runID).Handler(),
		// Request contexts derive from ctx, so cancelling it also ends the
		// long-lived /api/events SSE streams; without this, Shutdown below
		// would wait forever on a connected viewer.
		BaseContext: func(net.Listener) context.Context { return ctx },
		// Bound a client that connects but never finishes its headers. Write
		// and Idle timeouts stay unset on purpose: /api/events is a
		// long-lived SSE stream, and either one would sever a healthy viewer
		// mid-run.
		ReadHeaderTimeout: 5 * time.Second,
	}

	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()

	select {
	case err := <-served:
		// Serve returned before any shutdown was asked for: a real failure.
		return fmt.Errorf("serve live view: %w", err)
	case <-ctx.Done():
	}
	if err := server.Shutdown(context.Background()); err != nil {
		return fmt.Errorf("shut down live view: %w", err)
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve live view: %w", err)
	}
	return nil
}
