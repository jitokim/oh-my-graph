package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

// The POSITIONAL SLOT is the first element of a subcommand's argv when that
// subcommand takes a value there: `resume <run-id>`, `show <run-id>`,
// `watch <run-id>`, `serve [<run-id>]`, `run <graph.yaml>`, `lint <graph.yaml>`,
// `init [dir]`, `auto "<goal>"`. An element standing in that slot and beginning
// with "-" is a FLAG, never the value — a run id is minted only by newRunID and
// always begins with a digit (`20260818-234944.646288000-1`), so no legal
// invocation is lost by refusing to read one as a run id, and the same holds in
// practice for a graph path, a target directory and a goal.
//
// Reading a flag as the value is #200: `resume --help` answered `load run
// "--help": ... no such file or directory` — at the one moment the flag list was
// what the user needed — and `init --help` went further and created a directory
// literally named "--help". The rule lives here once and every subcommand that
// owns a positional slot reads argv through this file, rather than each
// indexing args[0] on its own: `serve` already had the check and its siblings
// did not, which is how one bug wore seven faces.
//
// Only element 0 is ever examined, because only element 0 is the slot. A dash
// further along argv is a flag or a flag's VALUE — `--verify-cmd 'make -h'` —
// and is none of this file's business.

// isHelpToken reports whether arg asks for usage. The three spellings are Go's
// flag package's own, so a subcommand with no FlagSet answers `-h`, `-help` and
// `--help` exactly as one with a FlagSet does.
func isHelpToken(arg string) bool {
	return arg == "-h" || arg == "-help" || arg == "--help"
}

// positionalArg splits the value standing in a subcommand's positional slot
// from the flags that follow it. ok is false when the slot holds no value at
// all — argv is empty, or it opens with a flag — and rest is then the whole
// argv, for the caller's FlagSet to parse as flags.
func positionalArg(args []string) (value string, rest []string, ok bool) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "", args, false
	}
	return args[0], args[1:], true
}

// usageRequest is what a subcommand returns when the argument in its positional
// slot asks for help. Help is not a failure and does not travel as one:
// mainExitCode prints it to stdout and exits 0, instead of the stderr
// `oh-my-graph: %v` line and exit 1 an error gets. It implements error only so
// that it can ride the return path every subcommand already has, keeping the
// single exit point (mainExitCode) the single place anything is printed to.
type usageRequest struct {
	subcommand string
	// set is the subcommand's FlagSet when it has one, so the answer can carry
	// each flag's own description; nil for show/watch/lint/init/runs, which
	// register no flags at all and are answered by their synopsis line alone.
	set *flag.FlagSet
}

// Error is the synopsis line the CLI already advertises for this subcommand, so
// help and the usage errors quote one source and cannot drift.
func (u *usageRequest) Error() string {
	line, ok := usageSynopsisFor(u.subcommand)
	if !ok {
		return "usage: " + runtimeUsage
	}
	return "usage: " + line
}

// print writes the answer: the synopsis line, then every flag the subcommand
// registers with the descriptions its FlagSet already carries.
func (u *usageRequest) print(w io.Writer) {
	fmt.Fprintln(w, u.Error())
	if u.set != nil {
		u.set.SetOutput(w)
		u.set.PrintDefaults()
	}
}

// helpRequest returns the usage answer when the positional slot holds a help
// token, and nil otherwise. Subcommands with a FlagSet call it before taking
// their positional; the ones without call it through flagInPositionalSlot.
func helpRequest(args []string, subcommand string, set *flag.FlagSet) *usageRequest {
	if len(args) == 0 || !isHelpToken(args[0]) {
		return nil
	}
	return &usageRequest{subcommand: subcommand, set: set}
}

// flagInPositionalSlot is the whole rule for the subcommands that have NO
// FlagSet: a dash-prefixed slot is a help request or an unknown flag, and
// either way it is not the value the slot holds. Their siblings with a FlagSet
// reach those same two answers by handing rest to flag.Parse. nil means the
// slot holds a value (or nothing at all), and the caller's own argument checks
// then apply unchanged.
func flagInPositionalSlot(args []string, subcommand string) error {
	if req := helpRequest(args, subcommand, nil); req != nil {
		return req
	}
	if len(args) == 0 || !strings.HasPrefix(args[0], "-") {
		return nil
	}
	line, _ := usageSynopsisFor(subcommand)
	return fmt.Errorf("%s: unknown flag %q (usage: %s)", subcommand, args[0], line)
}

// usageSynopsisFor returns the line of usageLines documenting subcommand, and
// whether there is one. Derived from the constant the CLI already prints rather
// than from a second list, for the reason usageLines' own comment gives: a
// second copy is a second thing to drift.
func usageSynopsisFor(subcommand string) (string, bool) {
	want := "oh-my-graph " + subcommand
	for _, line := range strings.Split(usageLines, "\n") {
		line = strings.TrimSpace(line)
		if line == want || strings.HasPrefix(line, want+" ") {
			return line, true
		}
	}
	return "", false
}
