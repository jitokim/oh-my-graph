package main

import (
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The usage synopsis is the one piece of documentation a user reads by
// accident — they typed the command wrong and the CLI answered. Every claim it
// makes is therefore checked against the code that would have to honor it,
// never against a second hand-written list:
//
//   - the subcommand names come from run()'s dispatch switch, read out of the
//     AST (subcommandsFromDispatch);
//   - each line's flags come from that subcommand's real FlagSet, read by
//     VisitAll (registeredFlags).
//
// Both directions are enforced, for subcommands and for flags alike. A
// registered flag missing from the synopsis hides a feature; a synopsis flag
// nobody registered is worse — the user types it and gets an error. This file's
// `serve --run` was live for two releases. A synopsis line for a subcommand
// run() does not dispatch fails the same way, and fails silently in the flag
// check: an unknown subcommand has no FlagSet, so a line carrying no flags has
// no mismatch left to report. It gets its own test.

// usageFlagPattern finds every --flag token on a synopsis line. The name stops
// at the first character a Go flag name cannot contain, so `[--concurrency N]`
// and `(--approve <gate-id>` both yield just the name.
var usageFlagPattern = regexp.MustCompile(`--([a-z0-9-]+)`)

// usageSubcommandPattern finds the subcommand a synopsis line documents.
var usageSubcommandPattern = regexp.MustCompile(`^oh-my-graph ([a-z-]+)`)

// flagSetsBySubcommand maps each subcommand that parses flags to a freshly
// built FlagSet. A subcommand absent here takes no flags at all, which the
// synopsis must also reflect.
//
// Every entry comes from the subcommand's own constructor — the same call the
// production path makes. None is re-registered here: a hand-copy would make
// this guard compare the synopsis against itself, so a flag added to or
// dropped from the real parser would pass in BOTH directions.
func flagSetsBySubcommand() map[string]*flag.FlagSet {
	return map[string]*flag.FlagSet{
		"run":    newRunFlags().set,
		"auto":   newAutoFlags().set,
		"resume": newResumeFlags().set,
		"serve":  newServeFlags().set,
		"chat":   newChatFlags().set,
	}
}

// subcommandsFromDispatch reads the case labels of run()'s switch out of
// main.go's AST. Derived, not transcribed: adding a `case "foo":` and no
// synopsis line for it fails this test without anyone remembering to update a
// list here.
func subcommandsFromDispatch(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	var found []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "run" || fn.Recv != nil {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			clause, ok := n.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expr := range clause.List {
				lit, ok := expr.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				name, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("unquote case label %s: %v", lit.Value, err)
				}
				found = append(found, name)
			}
			return true
		})
	}
	if len(found) == 0 {
		t.Fatal("found no dispatch cases in run() — the derivation broke, so this test proves nothing")
	}
	sort.Strings(found)
	return found
}

// registeredFlags is the set of flag names a FlagSet actually accepts.
func registeredFlags(set *flag.FlagSet) map[string]bool {
	names := map[string]bool{}
	set.VisitAll(func(f *flag.Flag) { names[f.Name] = true })
	return names
}

// usageLineFor returns the synopsis line documenting subcommand, and whether
// there is one.
func usageLineFor(subcommand string) (string, bool) {
	for _, line := range strings.Split(usageLines, "\n") {
		line = strings.TrimSpace(line)
		m := usageSubcommandPattern.FindStringSubmatch(line)
		if m != nil && m[1] == subcommand {
			return line, true
		}
	}
	return "", false
}

func TestUsage_DocumentsEverySubcommandTheDispatchAccepts(t *testing.T) {
	for _, name := range subcommandsFromDispatch(t) {
		if _, ok := usageLineFor(name); !ok {
			t.Errorf("run() dispatches %q but the usage synopsis never mentions it:\n%s", name, usageLines)
		}
	}
}

// TestUsage_EverySynopsisSubcommandIsDispatched is the reverse of the test
// above: a line the CLI prints for a command run() no longer routes anywhere
// sends the user straight into the `unknown command` branch.
func TestUsage_EverySynopsisSubcommandIsDispatched(t *testing.T) {
	dispatched := map[string]bool{}
	for _, name := range subcommandsFromDispatch(t) {
		dispatched[name] = true
	}
	for _, line := range strings.Split(usageLines, "\n") {
		line = strings.TrimSpace(line)
		m := usageSubcommandPattern.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("usage line does not start with a subcommand: %q", line)
		}
		if !dispatched[m[1]] {
			t.Errorf("usage documents %q, but run() dispatches no such command — a user who copies the "+
				"synopsis gets `unknown command`:\n  %s", m[1], line)
		}
	}
}

func TestUsage_EveryFlagItAdvertisesIsRegistered(t *testing.T) {
	sets := flagSetsBySubcommand()
	for _, line := range strings.Split(usageLines, "\n") {
		line = strings.TrimSpace(line)
		m := usageSubcommandPattern.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("usage line does not start with a subcommand: %q", line)
		}
		subcommand := m[1]
		registered := map[string]bool{}
		if set, ok := sets[subcommand]; ok {
			registered = registeredFlags(set)
		}
		for _, match := range usageFlagPattern.FindAllStringSubmatch(line, -1) {
			if !registered[match[1]] {
				t.Errorf("usage advertises `%s --%s`, which no FlagSet registers — a user typing it gets an error", subcommand, match[1])
			}
		}
	}
}

func TestUsage_AdvertisesEveryRegisteredFlag(t *testing.T) {
	for subcommand, set := range flagSetsBySubcommand() {
		line, ok := usageLineFor(subcommand)
		if !ok {
			t.Errorf("%s has a FlagSet but no usage line", subcommand)
			continue
		}
		advertised := map[string]bool{}
		for _, match := range usageFlagPattern.FindAllStringSubmatch(line, -1) {
			advertised[match[1]] = true
		}
		for name := range registeredFlags(set) {
			if !advertised[name] {
				t.Errorf("`%s --%s` is registered but the usage synopsis omits it, so nothing tells a user it exists:\n  %s", subcommand, name, line)
			}
		}
	}
}

// TestUsage_PackageDocCarriesTheSameSynopsis keeps godoc honest: the doc
// comment is a second copy by necessity (a comment cannot be a constant), so
// it is compared line for line against the constant the CLI prints rather than
// trusted to be maintained alongside it.
func TestUsage_PackageDocCarriesTheSameSynopsis(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	if file.Doc == nil {
		t.Fatal("main.go has no package doc comment")
	}

	var documented []string
	for _, line := range strings.Split(file.Doc.Text(), "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "oh-my-graph ") {
			documented = append(documented, line)
		}
	}

	var printed []string
	for _, line := range strings.Split(usageLines, "\n") {
		printed = append(printed, strings.TrimSpace(line))
	}

	if len(documented) != len(printed) {
		t.Fatalf("package doc lists %d synopsis lines, usageLines has %d", len(documented), len(printed))
	}
	for i := range printed {
		if documented[i] != printed[i] {
			t.Errorf("synopsis line %d drifted:\n  doc:     %s\n  printed: %s", i+1, documented[i], printed[i])
		}
	}
}
