package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// chatFlags holds the parsed `chat` subcommand options. Like every other
// subcommand's flag type it is built by a constructor rather than inline, so
// the usage-synopsis guard can check the advertised flags against the SAME
// registration the production path makes instead of a hand-copied second list
// (cmd/oh-my-graph/usage_test.go).
type chatFlags struct {
	noAgentMapping    bool
	noAgents          agentNameFlag
	noSkillActivation bool

	set *flag.FlagSet
}

// newChatFlags builds a chatFlags with its FlagSet configured. The mapping
// opt-outs only — --no-agent-mapping, its per-agent form --no-agent, and
// --no-skill-activation (whose deprecated alias parse rewrites) — because a
// chat graph turn IS an auto run and must honor the same opt-outs. --no-agent
// is registered here for exactly that reason: a chat turn plans and runs
// unattended too, so the ceiling an agent mapping costs (agentmap.go) is
// bought on the same terms and has to be refusable on the same terms.
func newChatFlags() *chatFlags {
	f := &chatFlags{set: flag.NewFlagSet("chat", flag.ContinueOnError)}
	f.set.BoolVar(&f.noAgentMapping, "no-agent-mapping", false, "do not auto-map planned nodes onto your Claude Code agents (~/.claude/agents, ./.claude/agents)")
	f.set.Var(&f.noAgents, "no-agent", "do not auto-map this ONE agent, by its frontmatter name (repeatable) — the per-agent form of --no-agent-mapping")
	f.set.BoolVar(&f.noSkillActivation, "no-skill-activation", false, "do not stage your Claude Code skills (~/.claude/skills) for planned nodes")
	return f
}

// parse reads args as flags only — `chat` takes no positional argument, so a
// leftover one is a typo worth naming rather than silently ignoring.
func (f *chatFlags) parse(args []string) error {
	if err := f.set.Parse(rewriteDeprecatedSkillFlag(os.Stderr, f.set, args)); err != nil {
		return err
	}
	if f.set.NArg() > 0 {
		return fmt.Errorf("chat: unexpected argument %q (usage: oh-my-graph chat [--no-agent-mapping] [--no-agent <name>] [--no-skill-activation])", f.set.Arg(0))
	}
	return nil
}

// runChat is the `chat` subcommand — a THIN PROTOTYPE of ambient mode, where
// oh-my-graph is the host and natural language is auto-routed to graphs. Each
// turn is classified by one router call through the same env-scrubbed
// NodeRunner every node uses; a converse turn is answered inline, a graph turn
// takes the exact `auto` path (coordinator.Plan → validate → scheduler.Run,
// with the live progress feed and the run's events.jsonl). Everything else
// stays prototype scope — the loop and the router, nothing else.
func runChat(args []string) error {
	flags := newChatFlags()
	if err := flags.parse(args); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	nodeRunner := runner.NewClaudeCLIRunner()
	coord := coordinator.New(nodeRunner, mappingOptions(os.Stdout, flags.noAgentMapping, flags.noAgents, flags.noSkillActivation)...)
	return chatLoop(ctx, os.Stdin, os.Stdout, coord, nodeRunner, commonRunFlags{inputs: inputFlag{}})
}

// errConfirmEOF marks stdin closing at the plan-confirmation prompt. chatLoop
// turns it into the same graceful session end as EOF at the main prompt; it
// never escapes chatLoop.
var errConfirmEOF = errors.New("chat: input closed at the confirmation prompt")

// chatLoop is the REPL: one line in, one routed turn out, until EOF or an
// explicit exit/quit. A graph turn prints the planned topology and then asks
// `Run this plan? [y/N]` before executing — the answer is read from the SAME
// scanner as the main prompt (so scripted-input tests cover it), only
// y/yes (case-insensitive) proceeds, anything else discards the plan, and
// EOF at the prompt ends the session exactly like EOF at the main prompt.
// A declined plan is not an error: "plan discarded." is printed and the next
// turn is served. A failed turn — router error, plan rejection, or a
// failed run — is printed and the loop continues; only reading stdin failing
// (or the surrounding context dying) ends the session, because a chat host
// that exits on its first bad turn is not a host. Separated from runChat and
// fed io.Reader/io.Writer so the routing half is testable against FakeRunner
// with zero real spawns; a graph turn's own run output (progress, ledger)
// still goes to os.Stdout, exactly as in `auto`.
func chatLoop(ctx context.Context, in io.Reader, out io.Writer, coord *coordinator.Coordinator, nodeRunner runner.NodeRunner, flags commonRunFlags) error {
	scanner := bufio.NewScanner(in)
	confirm := func() (bool, error) {
		fmt.Fprint(out, "Run this plan? [y/N] ")
		if !scanner.Scan() {
			fmt.Fprintln(out)
			return false, errConfirmEOF
		}
		switch strings.ToLower(strings.TrimSpace(scanner.Text())) {
		case "y", "yes":
			return true, nil
		}
		return false, nil
	}
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
			// nil web: a chat graph turn never embeds the live view or opens
			// a browser (ADR 0006) — the host owns the terminal, `serve` owns
			// the optional window. singleCycle: chat stays single-cycle in v1
			// (ADR 0011 §1) — its confirm covers exactly the one plan it
			// gates. planOnly false: chat's [y/N] already sits between the
			// printed plan and execution, so it IS the interactive form of
			// what --plan-only gives the non-interactive shell.
			if err := planAndExecute(ctx, out, coord, nodeRunner, flags, route.Goal, singleCycle, false, confirm, nil); err != nil {
				if errors.Is(err, errConfirmEOF) {
					return scanner.Err()
				}
				fmt.Fprintf(out, "chat: run failed: %v\n", err)
			}
		default:
			fmt.Fprintln(out, route.Reply)
		}
	}
}
