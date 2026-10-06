package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/interview"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// `design` is ADR 0044 §2.1(b): the interview, ONE planner call carrying its
// answers, the graph written to --out as YAML, and the same lint `lint` runs on
// the file just written. It never runs the graph — no node spawns, no run
// directory exists — because the file is for a human to review, edit and run
// with `run`. It has no off mode: the interview is the command.

// designFlags holds the parsed `design` options. --conventions is deliberately
// not registered: design spawns no node, so nothing would carry them, and a
// flag that silently does nothing is refused (refuseDesignConventions) rather
// than accepted.
type designFlags struct {
	goal string
	out  string
	set  *flag.FlagSet
}

func newDesignFlags() *designFlags {
	f := &designFlags{set: flag.NewFlagSet("design", flag.ContinueOnError)}
	f.set.StringVar(&f.out, "out", "", "REQUIRED: the `file` the planned graph is written to, as YAML. An existing file is refused before any model call, so no graph you wrote is overwritten. The file is linted once written; a lint failure keeps it, names it and exits non-zero. Nothing runs: review it, then run it with: oh-my-graph run <file>")
	return f
}

// parse reads `"<goal>" --out <file>`. Everything here is free, so it all
// happens before the first model call.
func (f *designFlags) parse(args []string) error {
	if req := helpRequest(args, "design", f.set); req != nil {
		return req
	}
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf(`design: missing goal (usage: oh-my-graph design "<goal>" --out <file> — the quoted goal comes first)`)
	}
	f.goal = args[0]
	if err := refuseDesignConventions(args[1:]); err != nil {
		return err
	}
	if err := f.set.Parse(args[1:]); err != nil {
		return err
	}
	if f.set.NArg() > 0 {
		return fmt.Errorf("design: unexpected argument %q after the flags — quote the goal so it is a single argument", f.set.Arg(0))
	}
	if strings.TrimSpace(f.out) == "" {
		return errors.New(`design: --out is required (usage: oh-my-graph design "<goal>" --out <file>) — there is no default path, so no graph you wrote can be overwritten`)
	}
	return nil
}

// refuseDesignConventions names --conventions instead of letting the FlagSet
// answer "flag provided but not defined": the operator meant something by it,
// and the answer is why it cannot mean anything here (ADR 0041 §2.6 refuses it
// on `run` and `chat` for the same reason).
func refuseDesignConventions(args []string) error {
	for _, arg := range args {
		if arg == "--" {
			return nil
		}
		name := strings.TrimLeft(arg, "-")
		if name == arg {
			continue
		}
		name, _, _ = strings.Cut(name, "=")
		if name == "conventions" {
			return errors.New("design: --conventions is refused: design runs no node, so nothing would carry the conventions and the flag would silently do nothing. " +
				"They reach planned nodes on `auto --conventions`; a graph from design runs with `run`, which does not prefix them")
		}
	}
	return nil
}

// errDesignNeedsTerminal is design's no-TTY refusal: the same check and the
// same place as `auto --interview`'s (errInterviewNeedsTerminal), with the way
// out design has — it has no off mode, so dropping a flag is not one.
var errDesignNeedsTerminal = errors.New("design: the interview asks its questions on a terminal, and stdin is not one; " +
	"run it from a terminal, or use `auto --plan-only` for a planned graph without an interview. Nothing was spent")

// designLint is the lint design runs on the file it wrote. Production passes
// lintGraphForRuntime, the function `lint` itself calls; it is a parameter so a
// test can reach the failure branch, which a plan the coordinator already
// validated does not reach on its own.
type designLint func(w, warnW io.Writer, path string, runtime runner.Runtime) error

func runDesignRuntime(runtime runner.Runtime, args []string) error {
	return runDesignWithRuntime(runtime, args, runner.NewCLIRunner(runtime), os.Stdout, os.Stderr, osStdin(), lintGraphForRuntime)
}

// runDesignWithRuntime is design with every seam injectable: the runner (a
// FakeRunner must see the interviewer and the planner and nothing else), the
// output streams, stdin and its terminal check (the seam `auto --interview`
// takes), and the lint.
func runDesignWithRuntime(runtime runner.Runtime, args []string, nodeRunner runner.NodeRunner, out, warnW io.Writer, stdin terminalInput, lint designLint) error {
	flags := newDesignFlags()
	if err := flags.parse(args); err != nil {
		return err
	}
	// The refusals, all free and in the order `auto` keeps: the destination,
	// then the machine, then the terminal.
	if err := checkDesignOut(flags.out); err != nil {
		return err
	}
	if err := runner.CheckRunnerCLI(nodeRunner); err != nil {
		return err
	}
	if !stdin.isTerminal() {
		return errDesignNeedsTerminal
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	result, err := interview.Run(ctx, flags.goal, coordinator.New(nodeRunner).Interviewer(), stdin.r, out)
	if err != nil {
		return fmt.Errorf("design: %w (the interview spent %s; nothing was planned and %s was not written)",
			err, formatCost(result.Cost.CostUSD, result.Cost.CostUnknown), flags.out)
	}
	prefix, err := result.Render()
	if err != nil {
		return fmt.Errorf("design: %w (the interview spent %s; nothing was planned and %s was not written)",
			err, formatCost(result.Cost.CostUSD, result.Cost.CostUnknown), flags.out)
	}
	ivCost, ivUnknown, ivUsage := interviewAccounting(result)
	fmt.Fprintf(out, "\nInterview ended (%s): %d asked, %d answered, %d skipped; cost %s.\n",
		result.Ending, result.Asked, result.Answered, result.Skipped, formatCost(ivCost, ivUnknown))
	var options []coordinator.Option
	if prefix == "" {
		fmt.Fprint(out, "No answer was given, so the planner's prompt is exactly what `auto` would send.\n\n")
	} else {
		fmt.Fprintf(out, "The planner receives this before its instructions, and the graph's nodes do not:\n\n%s", prefix)
		options = append(options, coordinator.WithInterviewPrefix(prefix))
	}

	fmt.Fprintf(out, "Planning a graph for goal %q...\n", flags.goal)
	plan, err := coordinator.New(nodeRunner, options...).Plan(ctx, flags.goal, nil)
	if err != nil {
		fmt.Fprintf(out, "The interview before it cost %s.\n", formatCost(ivCost, ivUnknown))
		// The same rule as a refused --plan-only plan: no run leg exists, so a
		// paid-for rejected spec is kept under plans/, never at --out.
		return noteRejectedPlan(out, planDirFor(newRunID()), err)
	}

	data, err := graphSpecYAML(plan.Spec)
	if err != nil {
		return fmt.Errorf("design: %w (nothing was written to %s)", err, flags.out)
	}
	if err := writeDesignFile(flags.out, data); err != nil {
		return err
	}
	fmt.Fprintf(out, "Graph written to %s.\n", flags.out)
	total := addTokenUsage(plan.Usage, ivUsage)
	fmt.Fprintf(out, "Cost: interview %s, planner %s (%s), total %s; tokens: %s.\n",
		formatCost(ivCost, ivUnknown),
		formatCost(plan.CostUSD, plan.CostUnknown), designPlannerCalls(plan),
		formatCost(ivCost+plan.CostUSD, ivUnknown || plan.CostUnknown), formatUsage(total))

	fmt.Fprintf(out, "\nLint (the same check `oh-my-graph lint %s` runs):\n", flags.out)
	if err := lint(out, warnW, flags.out, runtime); err != nil {
		return fmt.Errorf("design: %s was written and is kept, but it does not lint: %w — read it, fix it, and check it with `oh-my-graph lint %s`",
			flags.out, err, flags.out)
	}
	fmt.Fprintf(out, "\nNothing ran. Review %s, then run it with `oh-my-graph run %s`.\n"+
		"It runs as a hand-written graph: under your own settings, not the planned-node tool ceiling `auto` applies.\n",
		flags.out, flags.out)
	return nil
}

// designPlannerCalls says how many planner calls the planner's figure covers.
func designPlannerCalls(plan coordinator.Plan) string {
	if plan.Repaired == nil {
		return "1 call"
	}
	return "2 calls: the refused first reply and its correction"
}

// checkDesignOut refuses an --out that is already taken, or whose directory
// does not exist, before anything is spent. writeDesignFile repeats the
// existence check atomically (O_EXCL); this one is what keeps a refusal free.
func checkDesignOut(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("design: %s already exists; design never overwrites a file — pick a new --out, or move that one aside. Nothing was spent", path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("design: check --out %s: %w", path, err)
	}
	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("design: the directory of --out %s: %w. Nothing was spent", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("design: the directory of --out %s, %s, is not a directory. Nothing was spent", path, dir)
	}
	return nil
}

// writeDesignFile creates path exclusively, so a file that appeared while the
// interview and the planner ran is still never overwritten. Owner-only, as
// every spec the engine saves is (saveSpecAs): the planner's prompts may quote
// the operator's answers.
func writeDesignFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("design: %s appeared while planning; design never overwrites a file, so the planned graph was not written", path)
		}
		return fmt.Errorf("design: write %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("design: write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("design: write %s: %w", path, err)
	}
	return nil
}

// graphSpecYAML turns the planner's JSON spec into block-style YAML for a human
// to read: keys in the planner's order, multi-line prompts as literal blocks.
// The spec is already valid YAML (saveGeneratedSpec relies on that); this only
// changes how it is laid out, and it proves so by decoding both and comparing,
// so a layout bug can never change the graph the file describes.
func graphSpecYAML(spec []byte) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(spec, &doc); err != nil {
		return nil, fmt.Errorf("read the planned spec: %w", err)
	}
	blockStyle(&doc)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("encode the planned graph as YAML: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode the planned graph as YAML: %w", err)
	}
	var want, got any
	if err := yaml.Unmarshal(spec, &want); err != nil {
		return nil, fmt.Errorf("read the planned spec: %w", err)
	}
	if err := yaml.Unmarshal(buf.Bytes(), &got); err != nil {
		return nil, fmt.Errorf("re-read the YAML written for the planned graph: %w", err)
	}
	if !reflect.DeepEqual(want, got) {
		return nil, errors.New("the YAML layout of the planned graph does not decode to the planner's spec")
	}
	return buf.Bytes(), nil
}

// blockStyle clears the flow and quoting styles JSON input decodes with, and
// lays a multi-line string out as a literal block. A scalar that would read
// as another type unquoted is quoted again by the encoder itself.
func blockStyle(n *yaml.Node) {
	n.Style = 0
	if n.Kind == yaml.ScalarNode && n.Tag == "!!str" && strings.Contains(n.Value, "\n") {
		n.Style = yaml.LiteralStyle
	}
	for _, c := range n.Content {
		blockStyle(c)
	}
}
