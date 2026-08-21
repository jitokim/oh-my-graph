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
	"syscall"
	"time"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/serve"
)

// serveFlags holds the parsed `serve` subcommand options. The run id is an
// optional positional argument, and which of the two things `serve` is
// depends on it: named, one run's live view; omitted, the dashboard over
// every run.
type serveFlags struct {
	runID  string
	port   int
	noOpen bool

	set *flag.FlagSet
}

// newServeFlags builds a serveFlags with its FlagSet configured.
func newServeFlags() *serveFlags {
	f := &serveFlags{set: flag.NewFlagSet("serve", flag.ContinueOnError)}
	f.set.IntVar(&f.port, "port", serve.DefaultPort, "port to serve the live view on (always bound to 127.0.0.1)")
	f.set.BoolVar(&f.noOpen, "no-open", false, "print the URL without opening a browser (it only opens when stdout is a terminal)")
	return f
}

// autoOpener is the Opener this `serve` invocation should hand its URL to:
// the injected one when stdout is a terminal and --no-open was not passed,
// nil otherwise. It is webOpener's gate verbatim — the same TTY-and-not-opted-
// out rule `run`, `auto` and `resume` apply to their embedded live views — so
// there is one answer in this repo to "should we open a browser", and
// --no-open is `serve`'s name for --no-web's opt-out.
//
// nil is the feature-off value throughout (see browser.RefusingOpener's note):
// a scripted `serve` never consults an Opener at all, so its output stays
// byte-identical to a build without auto-open.
func (f *serveFlags) autoOpener(stdout *os.File, opener browser.Opener) browser.Opener {
	return webOpener(f.noOpen, stdout, opener)
}

// parse reads args in the order `[<run-id>] [flags...]`, mirroring the other
// subcommands' positional-first convention — except the positional is
// optional here, so a leading flag is flags-only argv, not an error. The
// leading-flag check that used to be written out here is now positionalArg
// (argslot.go), shared with every sibling that owns a positional slot.
func (f *serveFlags) parse(args []string) error {
	if req := helpRequest(args, "serve", f.set); req != nil {
		return req
	}
	runID, args, ok := positionalArg(args)
	if ok {
		f.runID = runID
	}
	if err := f.set.Parse(args); err != nil {
		return err
	}
	if f.set.NArg() > 0 {
		return fmt.Errorf("serve: unexpected argument %q (usage: oh-my-graph serve [<run-id>] [--port N] [--no-open])", f.set.Arg(0))
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
// Either way it binds the loopback-only listener, prints the URL, hands it to
// the browser when stdout is a terminal and --no-open was not passed (through
// the browser.Opener seam, ADR 0006 — the ExecOpener is injected right here,
// at the CLI call site, exactly as run/auto/resume inject it), and serves
// until interrupted. A scripted `serve` — a pipe, CI, a redirect — reaches no
// Opener at all and its output is byte-identical to before.
//
// This is also the only process that can decide a gate from the browser, so
// it is the only one that injects a resumer (ADR 0014): a run paused at a gate
// has already exited (ADR 0003), taking its embedded live view with it, so
// `serve` is by definition the view a paused run is looked at through. The
// resumer runs the leg's nodes through the production CLIRunner, exactly
// as `resume` does. The dashboard injects the same one — its interface names
// the run per call, so one resumer serves every run mounted under it.
func runServe(args []string) error {
	return runServeRuntime(runner.RuntimeClaude, false, args)
}

func runServeRuntime(requested runner.Runtime, requestedSet bool, args []string) error {
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
	// so the live view must never be reachable off-host (serve.ListenAs).
	//
	// This binary's build goes in because this is the one bind that can lose a
	// race for a FIXED port: --port (or the default 8642) is the port a previous
	// `serve` is still holding. Told who we are, the failure can name both
	// builds — the one answering on that port and the one that just failed to
	// take it — which is the difference between "port taken" and knowing the
	// browser is showing yesterday's server.
	listener, err := serve.ListenAs(flags.port, serve.CurrentBuild(Version))
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	resumer := cliGateResumer{
		runnerFor: func(runID string) (runner.NodeRunner, error) {
			return persistedNodeRunner(runID, requested, requestedSet)
		},
		errOut: os.Stderr,
	}
	// The fourth exec seam's production implementation, injected here at the
	// CLI call site and nowhere else (ADR 0006). The gate decides whether it
	// is ever consulted.
	opener := flags.autoOpener(os.Stdout, browser.NewExecOpener())

	handler, banner := dashboardView(listener, runsRoot(), resumer)
	if runID != "" {
		handler, banner = runView(listener, runDirFor(runID), runID, resumer)
	}
	return serveUntilCancelled(ctx, os.Stdout, listener, handler, banner, opener)
}

// runView is the handler and announcement for ONE run's live view.
//
// resumer is what makes the view's gate buttons work; nil (the embedded live
// view of a run in flight — startLiveView) leaves every gate decision a 409,
// which is the honest answer there: that run holds the resume.lock a leg would
// need, and it is not paused as long as it is running.
func runView(listener net.Listener, runDir, runID string, resumer serve.GateResumer) (http.Handler, string) {
	return serve.New(runDir, runID).WithGateResumer(resumer).WithBuild(serve.CurrentBuild(Version)).Handler(),
		fmt.Sprintf("Serving live view of run %s at http://%s/\nOpen it in your browser; Ctrl-C stops the server.\n",
			runID, listener.Addr())
}

// dashboardView is the handler and announcement for the dashboard over every
// run under runsRoot. An empty (or absent) runs root is not an error: the
// dashboard renders empty and fills in the moment something runs, which is the
// point of subscribing to the root rather than to a run.
func dashboardView(listener net.Listener, runsRoot string, resumer serve.GateResumer) (http.Handler, string) {
	return serve.NewDashboard(runsRoot).WithGateResumer(resumer).WithBuild(serve.CurrentBuild(Version)).Handler(),
		fmt.Sprintf("Serving the run dashboard at http://%s/\nEvery run under %s is a card; click one for its live view. Ctrl-C stops the server.\n",
			listener.Addr(), runsRoot)
}

// serveRun serves ONE run's live view until ctx is cancelled, opening nothing.
// It is the embedded live view `run`, `auto` and `resume` start (startLiveView,
// which owns its own browser launch), split out so a test can drive it with its
// own listener and context, no signals involved.
func serveRun(ctx context.Context, w io.Writer, listener net.Listener, runDir, runID string, resumer serve.GateResumer) error {
	handler, banner := runView(listener, runDir, runID, resumer)
	return serveUntilCancelled(ctx, w, listener, handler, banner, nil)
}

// serveUntilCancelled prints banner, hands the served URL to opener, and
// serves handler on listener until ctx is cancelled (Ctrl-C), which is the
// normal way to stop and not a failure. Serve runs in a goroutine and this
// function selects on its result against ctx, so a Serve that fails on its own
// surfaces immediately with nothing left running, and a cancel drains the
// Serve goroutine before returning. Both front-ends share it so a dashboard
// and a single-run view cannot differ in how they announce, open, time out, or
// shut down.
//
// A nil opener opens nothing and writes nothing — the scripted case, whose
// output is byte-identical to a build with no auto-open at all. A failed
// launch is a stderr note and nothing more: the URL is already printed, and
// the server is the thing the user asked for.
func serveUntilCancelled(ctx context.Context, w io.Writer, listener net.Listener, handler http.Handler, banner string, opener browser.Opener) error {
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

	if opener != nil {
		// The listener is already bound, so a connection that arrives before
		// Serve accepts it simply waits in the kernel's backlog — and a browser
		// takes far longer to launch than Serve takes to start.
		if err := opener.Open(ctx, "http://"+listener.Addr().String()+"/"); err != nil {
			fmt.Fprintf(os.Stderr, "open live view in browser: %v — the printed URL still works\n", err)
		}
	}

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
