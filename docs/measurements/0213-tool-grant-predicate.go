//go:build ignore

// Measure a candidate lint predicate for issue #213: "the prompt tells this
// node to run a command its allowed_tools does not permit".
//
// This is a MEASUREMENT, not a lint. It ships no behaviour and nothing in the
// engine calls it. The `ignore` build tag above keeps it out of
// `go build ./...` and `go test ./...`; run it explicitly:
//
//	go run docs/measurements/0213-tool-grant-predicate.go
//
// (`go run` with an explicit file argument does not apply build constraints,
// which is the whole reason the tag is safe here.)
//
// PARSE, DO NOT GREP. This repo has a written scar: a `grep -c` count went
// into three documents and was wrong, because grep counted comments and
// counted per file. Every number below comes out of encoding/json walking the
// actual snapshot and graph objects. Standard library only.
//
// The three questions, in order:
//
//	A. which runs on this machine are PLANNED (auto-generated) rather than
//	   hand-written, and what is the corpus of graphs they ran;
//	B. what shapes of `allowed_tools` grant does that corpus actually hold;
//	C. what does the candidate predicate produce over it.
//
// The point of (C) is the NOISE, not the hits. The extraction rule below is
// deliberately mechanical and deliberately not tuned: a rule tuned until the
// number looked good would measure the tuning, not the predicate.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// A. the corpus
// ---------------------------------------------------------------------------

// snapshot is the subset of runstate.Snapshot this measurement reads.
// GraphSourcePath is documented there as "where the graph was originally
// loaded from — the .yaml for a hand-written `run`, or the generated
// graph.json for an auto run" (internal/runstate/runstate.go).
type snapshot struct {
	GraphSourcePath string `json:"graph_source_path"`
}

// node is the subset of graph.Node this measurement reads.
type node struct {
	ID           string   `json:"id"`
	Type         string   `json:"type"`
	Prompt       string   `json:"prompt"`
	AllowedTools []string `json:"allowed_tools"`
}

type graphFile struct {
	Name  string `json:"name"`
	Nodes []node `json:"nodes"`
}

// runsDir is $OMG_HOME/runs when OMG_HOME is set, else $HOME/.oh-my-graph/runs.
func runsDir() string {
	if home := os.Getenv("OMG_HOME"); home != "" {
		return filepath.Join(home, "runs")
	}
	return filepath.Join(os.Getenv("HOME"), ".oh-my-graph", "runs")
}

// sameFile is the PLANNED test, and the only one used here: a run is planned
// exactly when its snapshot's `graph_source_path`, after filepath.Clean and
// symlink resolution, is the same file as that run's own graph.json. A
// hand-written run points at a .yaml somewhere else in the filesystem; an auto
// run points at the graph.json the planner wrote into the run directory.
// os.SameFile compares device+inode, so it survives /var vs /private/var and
// any other path spelling.
func sameFile(a, b string) bool {
	ra, err := filepath.EvalSymlinks(filepath.Clean(a))
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(filepath.Clean(b))
	if err != nil {
		return false
	}
	fa, err := os.Stat(ra)
	if err != nil {
		return false
	}
	fb, err := os.Stat(rb)
	if err != nil {
		return false
	}
	return os.SameFile(fa, fb)
}

type plannedRun struct {
	id    string
	graph graphFile
}

// skip reasons, reported separately because "300 skipped" with no breakdown is
// exactly the kind of headline number this repo has been burned by. The bulk of
// them is the third: a hand-written `run` writes a state.json and no graph.json
// at all, so it cannot even reach the planned test.
const (
	skipNoState     = "no state.json"
	skipNoGraph     = "no graph.json (a hand-written run writes none)"
	skipUnparseable = "state.json or graph.json would not parse"
)

type corpus struct {
	planned     []plannedRun
	skipped     map[string][]string // reason -> run ids
	handwritten []string            // readable, has a graph.json, but fails the planned test
	seen        int
}

func (c corpus) skippedCount() int {
	n := 0
	for _, ids := range c.skipped {
		n += len(ids)
	}
	return n
}

// scan classifies every run directory. Skips are returned rather than
// swallowed: an omission nobody is told about moves the numbers silently.
func scan(dir string) (corpus, error) {
	c := corpus{skipped: map[string][]string{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return c, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		c.seen++
		statePath := filepath.Join(dir, id, "state.json")
		graphPath := filepath.Join(dir, id, "graph.json")

		stateRaw, serr := os.ReadFile(statePath)
		if serr != nil {
			c.skipped[skipNoState] = append(c.skipped[skipNoState], id)
			continue
		}
		graphRaw, gerr := os.ReadFile(graphPath)
		if gerr != nil {
			c.skipped[skipNoGraph] = append(c.skipped[skipNoGraph], id)
			continue
		}
		var snap snapshot
		var g graphFile
		if json.Unmarshal(stateRaw, &snap) != nil || json.Unmarshal(graphRaw, &g) != nil {
			c.skipped[skipUnparseable] = append(c.skipped[skipUnparseable], id)
			continue
		}
		if snap.GraphSourcePath == "" || !sameFile(snap.GraphSourcePath, graphPath) {
			c.handwritten = append(c.handwritten, id)
			continue
		}
		c.planned = append(c.planned, plannedRun{id: id, graph: g})
	}
	return c, nil
}

// ---------------------------------------------------------------------------
// B. grant shapes
// ---------------------------------------------------------------------------

const (
	shapeBashAll   = "bash-unrestricted"  // `Bash` or `Bash(*)` — covers everything
	shapeBashParam = "bash-parameterised" // `Bash(go *)`, `Bash(git status:*)` — covers a prefix
	shapeOther     = "non-bash"           // Read, Write, Glob, Grep, ... — covers no command
)

func grantShape(grant string) string {
	g := strings.TrimSpace(grant)
	if g == "Bash" || g == "Bash(*)" {
		return shapeBashAll
	}
	if strings.HasPrefix(g, "Bash(") && strings.HasSuffix(g, ")") {
		return shapeBashParam
	}
	return shapeOther
}

// grantPrefix is the command prefix a parameterised Bash grant authorises:
// the text inside the parentheses with its trailing wildcard removed.
// `Bash(go *)` -> "go"; `Bash(git status:*)` -> "git status"; `Bash(ls)` -> "ls".
func grantPrefix(grant string) string {
	inner := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(grant), "Bash("), ")")
	inner = strings.TrimSuffix(inner, "*")
	inner = strings.TrimRight(inner, " :")
	return strings.TrimSpace(inner)
}

// ---------------------------------------------------------------------------
// C. the candidate predicate
// ---------------------------------------------------------------------------

// EXTRACTION RULE — fixed before the first run, and NOT revised afterwards.
// Written out here in full because the noise it produces is the thing being
// measured; a reader must be able to see exactly which noise is the rule's
// fault.
//
// Candidate spans come from two sources in a node's prompt:
//
//	S1. a single-backticked span on a line that is not inside a fence,
//	    matched per line so a span never runs across a newline;
//	S2. every non-blank line inside a ``` fenced block.
//
// From a span, the leading token is taken after stripping a leading shell
// prompt marker (`$ `). A span whose first character is `#` is dropped (a
// shell comment or a markdown heading).
//
// The leading token is treated as a COMMAND only if:
//
//	R1. it matches ^[a-z][a-z0-9_.+-]*$ — lowercase, no slashes, no
//	    punctuation a command name does not carry. This alone drops paths
//	    (`docs/measurements/*.md`), templates (`{{ artifacts.x }}`), flags,
//	    capitalised identifiers and anything with a space in front of it;
//	R2. it is not in englishStop, a FIXED list of common English function
//	    words, modals and boolean/null literals — prose that happens to be
//	    backticked for emphasis;
//	R3. it is not a field name of this corpus's own schema. That set is
//	    DERIVED MECHANICALLY, not hand-written: it is every JSON object key
//	    appearing anywhere in any planned graph.json (`prompt`, `allowed_tools`,
//	    `depends_on`, `success_check`, `verify`, ...). Prompts in this repo
//	    backtick their own config keys constantly, and a config key is never a
//	    command.
//
// Everything else that survives is offered as a command, INCLUDING the noise.
// Nothing below tries to tell an instruction from an example, a prohibition or
// a quoted log line; deciding that is what the hand-check downstream is for,
// and it is the actual result of this measurement.
var (
	commandToken = regexp.MustCompile(`^[a-z][a-z0-9_.+-]*$`)
	inlineSpan   = regexp.MustCompile("`([^`]+)`")

	// englishStop is fixed. Do not extend it after seeing results.
	englishStop = map[string]bool{
		"a": true, "an": true, "and": true, "any": true, "all": true, "are": true,
		"as": true, "at": true, "be": true, "been": true, "both": true, "but": true,
		"by": true, "can": true, "do": true, "does": true, "done": true, "each": true,
		"else": true, "empty": true, "every": true, "false": true, "for": true,
		"from": true, "has": true, "have": true, "how": true, "if": true, "in": true,
		"into": true, "is": true, "it": true, "its": true, "just": true, "may": true,
		"must": true, "never": true, "nil": true, "no": true, "none": true, "not": true,
		"null": true, "of": true, "on": true, "one": true, "only": true, "or": true,
		"other": true, "over": true, "same": true, "should": true, "so": true,
		"some": true, "than": true, "that": true, "the": true, "then": true,
		"there": true, "this": true, "to": true, "true": true, "two": true,
		"under": true, "up": true, "use": true, "was": true, "were": true,
		"what": true, "when": true, "where": true, "which": true, "why": true,
		"will": true, "with": true, "would": true, "yes": true, "you": true, "your": true,
	}
)

type span struct {
	command string // the leading token
	text    string // the whole extracted span
	context string // ~200 chars of surrounding prompt
	offset  int
}

// schemaKeys walks arbitrary decoded JSON and collects every object key. This
// is R3's input, derived from the corpus itself.
func schemaKeys(v any, out map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, sub := range t {
			out[strings.ToLower(k)] = true
			schemaKeys(sub, out)
		}
	case []any:
		for _, sub := range t {
			schemaKeys(sub, out)
		}
	}
}

func contextAround(prompt string, start, end int) string {
	lo := start - 100
	if lo < 0 {
		lo = 0
	}
	hi := end + 100
	if hi > len(prompt) {
		hi = len(prompt)
	}
	s := strings.ToValidUTF8(prompt[lo:hi], "")
	s = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(s)
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}

// extract applies S1/S2 and R1/R2/R3 to one prompt.
func extract(prompt string, schema map[string]bool) []span {
	var out []span
	consider := func(text string, start, end int) {
		t := strings.TrimSpace(text)
		if t == "" || strings.HasPrefix(t, "#") {
			return
		}
		t = strings.TrimPrefix(t, "$ ")
		fields := strings.Fields(t)
		if len(fields) == 0 {
			return
		}
		tok := fields[0]
		if !commandToken.MatchString(tok) { // R1
			return
		}
		if englishStop[tok] { // R2
			return
		}
		if schema[tok] { // R3
			return
		}
		out = append(out, span{
			command: tok,
			text:    strings.TrimSpace(text),
			context: contextAround(prompt, start, end),
			offset:  start,
		})
	}

	inFence := false
	offset := 0
	for _, line := range strings.SplitAfter(prompt, "\n") {
		body := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if strings.HasPrefix(strings.TrimSpace(body), "```") {
			inFence = !inFence
			offset += len(line)
			continue
		}
		if inFence { // S2
			consider(body, offset, offset+len(body))
		} else { // S1
			for _, m := range inlineSpan.FindAllStringSubmatchIndex(body, -1) {
				consider(body[m[2]:m[3]], offset+m[2], offset+m[3])
			}
		}
		offset += len(line)
	}
	return out
}

// covers implements the grant semantics: a bare `Bash`/`Bash(*)` covers
// everything; a `Bash(<prefix> *)` covers an invocation that starts with
// <prefix>; a non-Bash grant covers no command at all.
func covers(grants []string, invocation string) bool {
	inv := strings.TrimPrefix(strings.TrimSpace(invocation), "$ ")
	for _, g := range grants {
		switch grantShape(g) {
		case shapeBashAll:
			return true
		case shapeBashParam:
			p := grantPrefix(g)
			if p == "" {
				return true
			}
			if inv == p || strings.HasPrefix(inv, p+" ") {
				return true
			}
		}
	}
	return false
}

type hit struct {
	RunID       string   `json:"run_id"`
	NodeID      string   `json:"node_id"`
	Command     string   `json:"command"`
	Span        string   `json:"span"`
	Context     string   `json:"context"`
	Grants      []string `json:"grants"`
	Occurrences int      `json:"occurrences"`
}

func main() {
	dir := runsDir()
	c, err := scan(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot read %s: %v\n", dir, err)
		os.Exit(1)
	}
	sort.Slice(c.planned, func(i, j int) bool { return c.planned[i].id < c.planned[j].id })

	// R3's input: every JSON key anywhere in the planned corpus.
	schema := map[string]bool{}
	for _, p := range c.planned {
		raw, err := os.ReadFile(filepath.Join(dir, p.id, "graph.json"))
		if err != nil {
			continue
		}
		var doc any
		if json.Unmarshal(raw, &doc) == nil {
			schemaKeys(doc, schema)
		}
	}

	// ---- B. characterise -------------------------------------------------
	totalNodes, nodesWithGrants, nodesNilGrants, nodesEmptyGrants := 0, 0, 0, 0
	grantCount := map[string]int{}
	nodesHolding := map[string]int{}
	perGrant := map[string]int{}
	for _, p := range c.planned {
		for _, n := range p.graph.Nodes {
			totalNodes++
			if n.AllowedTools == nil {
				nodesNilGrants++
				continue
			}
			if len(n.AllowedTools) == 0 {
				nodesEmptyGrants++
				continue
			}
			nodesWithGrants++
			held := map[string]bool{}
			for _, g := range n.AllowedTools {
				grantCount[grantShape(g)]++
				perGrant[g]++
				held[grantShape(g)] = true
			}
			for s := range held {
				nodesHolding[s]++
			}
		}
	}

	// ---- C. the predicate ------------------------------------------------
	var hits []hit
	index := map[string]int{}
	occurrences := 0
	nodesScanned, nodesHit := 0, 0
	for _, p := range c.planned {
		for _, n := range p.graph.Nodes {
			if len(n.AllowedTools) == 0 {
				continue // already covered by handoff.LintToolGrants
			}
			nodesScanned++
			nodeHadHit := false
			for _, s := range extract(n.Prompt, schema) {
				if covers(n.AllowedTools, s.text) {
					continue
				}
				occurrences++
				nodeHadHit = true
				key := p.id + "\x00" + n.ID + "\x00" + s.command
				if i, ok := index[key]; ok {
					hits[i].Occurrences++
					continue
				}
				index[key] = len(hits)
				hits = append(hits, hit{
					RunID: p.id, NodeID: n.ID, Command: s.command,
					Span: s.text, Context: s.context,
					Grants: n.AllowedTools, Occurrences: 1,
				})
			}
			if nodeHadHit {
				nodesHit++
			}
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].RunID != hits[j].RunID {
			return hits[i].RunID < hits[j].RunID
		}
		if hits[i].NodeID != hits[j].NodeID {
			return hits[i].NodeID < hits[j].NodeID
		}
		return hits[i].Command < hits[j].Command
	})

	// ---- D. output -------------------------------------------------------
	fmt.Printf("runs directory:                  %s\n", dir)
	fmt.Printf("run directories seen:            %d\n", c.seen)
	fmt.Printf("  skipped:                       %d\n", c.skippedCount())
	for _, reason := range []string{skipNoState, skipNoGraph, skipUnparseable} {
		fmt.Printf("      %-46s %d\n", reason, len(c.skipped[reason]))
	}
	fmt.Printf("  readable, has graph.json, but FAILS the planned test: %d\n", len(c.handwritten))
	fmt.Printf("  PLANNED graphs:                                       %d\n", len(c.planned))
	for _, p := range c.planned {
		fmt.Printf("      %s  %s (%d nodes)\n", p.id, p.graph.Name, len(p.graph.Nodes))
	}
	fmt.Println()
	fmt.Printf("total nodes in planned graphs:   %d\n", totalNodes)
	fmt.Printf("  declaring a non-empty allowed_tools: %d\n", nodesWithGrants)
	fmt.Printf("  declaring an empty allowed_tools []: %d\n", nodesEmptyGrants)
	fmt.Printf("  declaring no allowed_tools key:      %d\n", nodesNilGrants)
	fmt.Println()
	fmt.Println("grant shapes (per grant / nodes holding at least one):")
	for _, s := range []string{shapeBashAll, shapeBashParam, shapeOther} {
		fmt.Printf("  %-20s %4d grants   %3d nodes\n", s, grantCount[s], nodesHolding[s])
	}
	fmt.Println()
	fmt.Println("every distinct grant string in the corpus:")
	var gs []string
	for g := range perGrant {
		gs = append(gs, g)
	}
	sort.Strings(gs)
	for _, g := range gs {
		fmt.Printf("  %-24s %3d  [%s]\n", g, perGrant[g], grantShape(g))
	}
	fmt.Println()
	fmt.Printf("nodes entering the predicate (declare >=1 grant):  %d\n", nodesScanned)
	fmt.Printf("nodes excluded (declare nothing; LintToolGrants):  %d\n", nodesEmptyGrants+nodesNilGrants)
	fmt.Printf("uncovered command OCCURRENCES:                     %d\n", occurrences)
	fmt.Printf("HITS (distinct run x node x command):              %d\n", len(hits))
	fmt.Printf("nodes with at least one hit:                       %d of %d\n", nodesHit, nodesScanned)
	fmt.Println()
	for _, h := range hits {
		fmt.Printf("%s | %s | %s | %s\n", h.RunID, h.NodeID, h.Command, strings.Join(h.Grants, ", "))
	}

	if hits == nil {
		hits = []hit{}
	}
	out, err := json.MarshalIndent(hits, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal: %v\n", err)
		os.Exit(1)
	}
	dest := filepath.Join("docs", "measurements", "0213-hits.json")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(dest, append(out, '\n'), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", dest, err)
		os.Exit(1)
	}
	fmt.Printf("\nwrote %s (%d hits)\n", dest, len(hits))
}
