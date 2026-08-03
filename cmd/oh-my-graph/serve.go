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

	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/serve"
)

// serveFlags holds the parsed `serve` subcommand options. The run id is an
// optional positional argument, and which of the two things `serve` is
// depends on it: named, one run's live view; omitted, the dashboard over
// every run.
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

// runServe is the `serve` subcommand, which is two things depending on its
// one optional argument:
//
//   - `serve` with NO run id serves the DASHBOARD: one port and one page for
//     every run, each a live mini-DAG card, with each run's own live view
//     mounted at /run/<id>/. Watching four concurrent runs is one process,
//     one port and one tab.
//   - `serve <run-id>` goes straight to that run's live view at /, exactly as
//     before — the direct route when you already know which run you mean.
//
// Either way it binds the loopback-only listener and serves until interrupted.
//
// This is also the only process that can decide a gate from the browser, so
// it is the only one that injects a resumer (ADR 0014): a run paused at a gate
// has already exited (ADR 0003), taking its embedded live view with it, so
// `serve` is by definition the view a paused run is looked at through. The
// resumer runs the leg's nodes through the production ClaudeCLIRunner, exactly
// as `resume` does. The dashboard injects the same one — its interface names
// the run per call, so one resumer serves every run mounted under it.
func runServe(args []string) error {
	flags := newServeFlags()
	if err := flags.parse(args); err != nil {
		return err
	}

	// Resolved before the listener is bound, so a mistyped run id fails
	// without ever taking a port. The dashboard has nothing to resolve: it is
	// the view of every run, including the ones that do not exist yet.
	runID := ""
	if flags.runID != "" {
		var err error
		if runID, err = serve.ResolveRun(runsRoot(), flags.runID); err != nil {
			return err
		}
	}

	// Bound to 127.0.0.1 only — run directories hold prompts and session ids,
	// so the live view must never be reachable off-host (serve.Listen).
	listener, err := serve.Listen(flags.port)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	resumer := cliGateResumer{nodeRunner: runner.NewClaudeCLIRunner(), errOut: os.Stderr}
	if runID == "" {
		return serveDashboard(ctx, os.Stdout, listener, runsRoot(), resumer)
	}
	return serveRun(ctx, os.Stdout, listener, runDirFor(runID), runID, resumer)
}

// serveRun announces the URL and serves ONE run's live view until ctx is
// cancelled. Split from runServe so a test can drive it with its own listener
// and context, no signals involved.
//
// resumer is what makes the view's gate buttons work; nil (the embedded live
// view of a run in flight — startLiveView) leaves every gate decision a 409,
// which is the honest answer there: that run holds the resume.lock a leg would
// need, and it is not paused as long as it is running.
func serveRun(ctx context.Context, w io.Writer, listener net.Listener, runDir, runID string, resumer serve.GateResumer) error {
	return serveUntilCancelled(ctx, w, listener,
		serve.New(runDir, runID).WithGateResumer(resumer).Handler(),
		fmt.Sprintf("Serving live view of run %s at http://%s/\nOpen it in your browser; Ctrl-C stops the server.\n",
			runID, listener.Addr()))
}

// serveDashboard announces the URL and serves the dashboard over every run
// under runsRoot until ctx is cancelled. An empty (or absent) runs root is not
// an error: the dashboard renders empty and fills in the moment something
// runs, which is the point of subscribing to the root rather than to a run.
func serveDashboard(ctx context.Context, w io.Writer, listener net.Listener, runsRoot string, resumer serve.GateResumer) error {
	return serveUntilCancelled(ctx, w, listener,
		serve.NewDashboard(runsRoot).WithGateResumer(resumer).Handler(),
		fmt.Sprintf("Serving the run dashboard at http://%s/\nEvery run under %s is a card; click one for its live view. Ctrl-C stops the server.\n",
			listener.Addr(), runsRoot))
}

// serveUntilCancelled prints banner and serves handler on listener until ctx
// is cancelled (Ctrl-C), which is the normal way to stop and not a failure.
// Serve runs in a goroutine and this function selects on its result against
// ctx, so a Serve that fails on its own surfaces immediately with nothing
// left running, and a cancel drains the Serve goroutine before returning.
// The two front-ends share it so a dashboard and a single-run view cannot
// differ in how they announce, time out, or shut down.
func serveUntilCancelled(ctx context.Context, w io.Writer, listener net.Listener, handler http.Handler, banner string) error {
	fmt.Fprint(w, banner)

	server := &http.Server{
		Handler: handler,
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
