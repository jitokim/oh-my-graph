package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// runChat is the `chat` subcommand — a THIN PROTOTYPE of ambient mode, where
// oh-my-graph is the host and natural language is auto-routed to graphs. Each
// turn is classified by one router call through the same env-scrubbed
// NodeRunner every node uses; a converse turn is answered inline, a graph turn
// takes the exact `auto` path (coordinator.Plan → validate → scheduler.Run,
// with the live progress feed and the run's events.jsonl). No flags yet:
// prototype scope is the loop and the router, nothing else.
func runChat(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("chat: unexpected argument %q (usage: oh-my-graph chat)", args[0])
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	nodeRunner := runner.NewClaudeCLIRunner()
	return chatLoop(ctx, os.Stdin, os.Stdout, coordinator.New(nodeRunner), nodeRunner, commonRunFlags{inputs: inputFlag{}})
}

// chatLoop is the REPL: one line in, one routed turn out, until EOF or an
// explicit exit/quit. A failed turn — router error, plan rejection, or a
// failed run — is printed and the loop continues; only reading stdin failing
// (or the surrounding context dying) ends the session, because a chat host
// that exits on its first bad turn is not a host. Separated from runChat and
// fed io.Reader/io.Writer so the routing half is testable against FakeRunner
// with zero real spawns; a graph turn's own run output (progress, ledger)
// still goes to os.Stdout, exactly as in `auto`.
func chatLoop(ctx context.Context, in io.Reader, out io.Writer, coord *coordinator.Coordinator, nodeRunner runner.NodeRunner, flags commonRunFlags) error {
	scanner := bufio.NewScanner(in)
	for {
		if ctx.Err() != nil {
			return nil
		}
		fmt.Fprint(out, "> ")
		if !scanner.Scan() {
			fmt.Fprintln(out)
			return scanner.Err()
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			return nil
		}

		route, err := coord.Route(ctx, line)
		if err != nil {
			fmt.Fprintf(out, "chat: %v\n", err)
			continue
		}
		switch route.Mode {
		case coordinator.RouteGraph:
			if err := planAndExecute(ctx, out, coord, nodeRunner, flags, route.Goal); err != nil {
				fmt.Fprintf(out, "chat: run failed: %v\n", err)
			}
		default:
			fmt.Fprintln(out, route.Reply)
		}
	}
}
