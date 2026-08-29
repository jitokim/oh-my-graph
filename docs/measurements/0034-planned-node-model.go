//go:build ignore

// Measure: which MODEL actually answered, per graph node, split by whether the
// run was PLANNED (auto mode) or HAND-WRITTEN.
//
// This is a MEASUREMENT, not a lint. It ships no behaviour and nothing in the
// engine calls it. The `ignore` build tag keeps it out of `go build ./...` and
// `go test ./...`; run it explicitly:
//
//	go run docs/measurements/0034-planned-node-model.go
//
// Corpus definition and the join are lifted UNCHANGED from
// docs/measurements/0218-denied-nodes-that-passed.go so the two measurements
// share one definition of "planned" rather than two that drift: a run is
// planned exactly when its snapshot's graph_source_path is the SAME FILE
// (device+inode, os.SameFile, after EvalSymlinks) as that run's own graph.json.
//
// TWO THINGS 0218 DID NOT HAVE TO CARE ABOUT, AND THIS ONE DOES:
//
//  1. THE HAND-WRITTEN BUCKET IS NOT "every session on this machine".
//     ~/.claude/projects holds a transcript for every ordinary interactive
//     claude session on this machine — the operator's own chats, other tools'
//     spawns — not only graph nodes. A model census taken over that directory
//     is a census of the operator's laptop, not of hand-written graph nodes.
//     This program therefore reports the hand-written bucket ONLY as a join
//     through state.json node records (session_id -> transcript), and reports
//     the whole-directory census SEPARATELY and labelled as contaminated, so
//     the difference between the two is visible rather than assumed away.
//
//  2. 0218's scan() dropped a run with no graph.json into "skipped". That is
//     right for 0218 (it only wanted planned runs) and WRONG here: a
//     hand-written run writes no graph.json, so 0218's skip bucket IS most of
//     the hand-written corpus. handwritten below is therefore "readable
//     state.json AND (no graph.json OR not the same file)", and the two
//     sub-reasons are reported separately.
//
// WHAT message.model CANNOT TELL US. The transcript records the model as e.g.
// "claude-opus-5". The operator's ~/.claude/settings.json on this machine holds
// the alias "opus[1m]". The context-window variant is NOT in message.model:
// verified on this measurement's own session, which the harness reports as
// claude-opus-5[1m] and whose transcript records "claude-opus-5" throughout.
// So this corpus can separate opus from fable from sonnet, and CANNOT separate
// opus from opus[1m]. Every number below is a family census only.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type snapshot struct {
	GraphSourcePath string                `json:"graph_source_path"`
	Graph           json.RawMessage       `json:"graph"`
	Nodes           map[string]nodeRecord `json:"nodes"`
}

type nodeRecord struct {
	Verdict   string `json:"verdict"`
	SessionID string `json:"session_id"`
}

type graphFile struct {
	Name  string      `json:"name"`
	Nodes []graphNode `json:"nodes"`
}

type graphNode struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

const (
	verdictFail = "FAIL"
	synthetic   = "<synthetic>"
)

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return os.Getenv("HOME")
}

func runsDir() string {
	if home := os.Getenv("OMG_HOME"); home != "" {
		return filepath.Join(home, "runs")
	}
	return filepath.Join(homeDir(), ".oh-my-graph", "runs")
}

// sameFile is the PLANNED test, verbatim from 0218.
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

func decodeGraph(raw json.RawMessage) (graphFile, bool) {
	var g graphFile
	if len(raw) == 0 {
		return g, false
	}
	if err := json.Unmarshal(raw, &g); err == nil {
		return g, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return g, false
	}
	if err := json.Unmarshal([]byte(s), &g); err != nil {
		return g, false
	}
	return g, true
}

// inFlight is 0218's test: a graph node with no record AND no FAIL anywhere
// means the run was still executing when this program read it — including, if
// this program is itself running as a node, its own run.
func inFlight(g graphFile, snap snapshot) bool {
	missing := false
	for _, n := range g.Nodes {
		if _, ok := snap.Nodes[n.ID]; !ok {
			missing = true
			break
		}
	}
	if !missing {
		return false
	}
	for _, rec := range snap.Nodes {
		if rec.Verdict == verdictFail {
			return false
		}
	}
	return true
}

type run struct {
	id      string
	snap    snapshot
	planned bool
}

type corpus struct {
	seen           int
	runs           []run
	noState        []string
	unparseable    []string
	inFlightIDs    []string
	handNoGraph    int // hand-written because the run wrote no graph.json
	handNotSame    int // has a graph.json, but graph_source_path is a different file
	plannedRunsNum int
}

func scan(dir string) (corpus, error) {
	c := corpus{}
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

		raw, serr := os.ReadFile(statePath)
		if serr != nil {
			c.noState = append(c.noState, id)
			continue
		}
		var snap snapshot
		if json.Unmarshal(raw, &snap) != nil {
			c.unparseable = append(c.unparseable, id)
			continue
		}

		_, gerr := os.Stat(graphPath)
		planned := gerr == nil && snap.GraphSourcePath != "" && sameFile(snap.GraphSourcePath, graphPath)
		if planned {
			g, ok := decodeGraph(snap.Graph)
			if ok && inFlight(g, snap) {
				c.inFlightIDs = append(c.inFlightIDs, id)
				continue
			}
			c.plannedRunsNum++
		} else if gerr != nil {
			c.handNoGraph++
		} else {
			c.handNotSame++
		}
		c.runs = append(c.runs, run{id: id, snap: snap, planned: planned})
	}
	sort.Slice(c.runs, func(i, j int) bool { return c.runs[i].id < c.runs[j].id })
	sort.Strings(c.inFlightIDs)
	return c, nil
}

// ---------------------------------------------------------------------------
// the join: session_id -> transcript -> model
// ---------------------------------------------------------------------------

// transcriptsFor finds a session's transcript BY FILENAME (0218's rule: never
// reconstruct the project slug).
func transcriptsFor(sessionID string) []string {
	if sessionID == "" {
		return nil
	}
	m, err := filepath.Glob(filepath.Join(homeDir(), ".claude", "projects", "*", sessionID+".jsonl"))
	if err != nil {
		return nil
	}
	sort.Strings(m)
	return m
}

const maxLine = 64 << 20

type assistantRecord struct {
	Type    string `json:"type"`
	Message struct {
		Model string `json:"model"`
	} `json:"message"`
}

// modelsIn returns how many assistant turns each model produced in one
// transcript. PARSE, DO NOT GREP: the string "claude-opus-5" appears inside
// ordinary tool output in this repository's own sessions (this very program's
// source contains it), so a grep over the file counts the operator's shell
// output as model turns.
func modelsIn(path string) (map[string]int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	counts := map[string]int{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), maxLine)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec assistantRecord
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		if rec.Type != "assistant" || rec.Message.Model == "" || rec.Message.Model == synthetic {
			continue
		}
		counts[rec.Message.Model]++
	}
	return counts, sc.Err()
}

// dominant names the model that produced the most assistant turns in a
// transcript, and reports whether more than one model appeared. A node whose
// transcript holds two models (a mid-session /model switch, or a resume) is
// counted under its dominant model AND listed separately, because folding it
// in silently is how a census hides its own ambiguity.
func dominant(counts map[string]int) (string, bool) {
	best, bestN, distinct := "", -1, 0
	names := make([]string, 0, len(counts))
	for m := range counts {
		names = append(names, m)
	}
	sort.Strings(names) // deterministic tie-break
	for _, m := range names {
		distinct++
		if counts[m] > bestN {
			best, bestN = m, counts[m]
		}
	}
	return best, distinct > 1
}

type nodeRow struct {
	RunID      string         `json:"run_id"`
	NodeID     string         `json:"node_id"`
	Planned    bool           `json:"planned"`
	SessionID  string         `json:"session_id"`
	Transcript string         `json:"transcript,omitempty"`
	Model      string         `json:"model,omitempty"`
	Mixed      bool           `json:"mixed,omitempty"`
	Turns      map[string]int `json:"turns,omitempty"`
	Note       string         `json:"note,omitempty"`
}

const (
	noteNoSession  = "record carries no session_id"
	noteMissing    = "session_id present but no ~/.claude/projects/*/<id>.jsonl matched (a codex node has no claude transcript)"
	noteNoAssist   = "transcript matched but held no assistant record naming a model"
	noteUnreadable = "transcript matched but could not be read in full"
)

type bucket struct {
	byModel    map[string]int
	nodes      int
	joined     int
	noSession  int
	missing    int
	noAssist   int
	unreadable int
	mixed      int
}

func newBucket() *bucket { return &bucket{byModel: map[string]int{}} }

func (b *bucket) report(name string) {
	fmt.Printf("%s\n", name)
	fmt.Printf("  node records seen:                       %d\n", b.nodes)
	fmt.Printf("  JOINED to a model (the denominator):     %d\n", b.joined)
	fmt.Printf("    of those, transcript held >1 model:    %d\n", b.mixed)
	fmt.Printf("  excluded, no session_id on the record:   %d\n", b.noSession)
	fmt.Printf("  excluded, no transcript file matched:    %d\n", b.missing)
	fmt.Printf("  excluded, no assistant record w/ model:  %d\n", b.noAssist)
	fmt.Printf("  excluded, transcript unreadable:         %d\n", b.unreadable)
	models := make([]string, 0, len(b.byModel))
	for m := range b.byModel {
		models = append(models, m)
	}
	sort.Slice(models, func(i, j int) bool { return b.byModel[models[i]] > b.byModel[models[j]] })
	for _, m := range models {
		fmt.Printf("      %-24s %5d\n", m, b.byModel[m])
	}
	fmt.Println()
}

func main() {
	dir := runsDir()
	c, err := scan(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot read %s: %v\n", dir, err)
		os.Exit(1)
	}

	planned, hand := newBucket(), newBucket()
	var rows []nodeRow
	nodeSessions := map[string]bool{} // every session id any graph node claims

	for _, r := range c.runs {
		b := hand
		if r.planned {
			b = planned
		}
		ids := make([]string, 0, len(r.snap.Nodes))
		for id := range r.snap.Nodes {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			rec := r.snap.Nodes[id]
			row := nodeRow{RunID: r.id, NodeID: id, Planned: r.planned, SessionID: rec.SessionID}
			b.nodes++
			if rec.SessionID == "" {
				b.noSession++
				row.Note = noteNoSession
				rows = append(rows, row)
				continue
			}
			nodeSessions[rec.SessionID] = true
			paths := transcriptsFor(rec.SessionID)
			if len(paths) == 0 {
				b.missing++
				row.Note = noteMissing
				rows = append(rows, row)
				continue
			}
			row.Transcript = paths[0]
			counts := map[string]int{}
			bad := false
			for _, p := range paths {
				m, err := modelsIn(p)
				if err != nil {
					bad = true
				}
				for k, v := range m {
					counts[k] += v
				}
			}
			if bad {
				b.unreadable++
				row.Note = noteUnreadable
				rows = append(rows, row)
				continue
			}
			if len(counts) == 0 {
				b.noAssist++
				row.Note = noteNoAssist
				rows = append(rows, row)
				continue
			}
			model, mixed := dominant(counts)
			row.Model, row.Mixed, row.Turns = model, mixed, counts
			b.joined++
			b.byModel[model]++
			if mixed {
				b.mixed++
			}
			rows = append(rows, row)
		}
	}

	// ---- the report -------------------------------------------------------
	fmt.Printf("runs directory:                  %s\n", dir)
	fmt.Printf("run directories seen:            %d\n", c.seen)
	fmt.Printf("  no state.json (skipped):       %d\n", len(c.noState))
	fmt.Printf("  state.json unparseable:        %d\n", len(c.unparseable))
	fmt.Printf("  PLANNED, in flight (excluded): %d  %s\n", len(c.inFlightIDs), strings.Join(c.inFlightIDs, " "))
	fmt.Printf("  PLANNED runs:                  %d\n", c.plannedRunsNum)
	fmt.Printf("  HAND-WRITTEN runs:             %d  (no graph.json: %d, graph.json but different file: %d)\n",
		c.handNoGraph+c.handNotSame, c.handNoGraph, c.handNotSame)
	fmt.Println()

	planned.report("PLANNED nodes (auto mode) — model that answered:")
	hand.report("HAND-WRITTEN nodes (joined through state.json — UNCONTAMINATED):")

	// ---- the contamination check -----------------------------------------
	// Every transcript on this machine, whether or not a graph node ever
	// claimed it. This is the census a directory-wide count produces, and the
	// gap between it and the hand-written bucket above IS the contamination.
	all, err := filepath.Glob(filepath.Join(homeDir(), ".claude", "projects", "*", "*.jsonl"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "glob transcripts: %v\n", err)
		os.Exit(1)
	}
	sort.Strings(all)
	byModelAll := map[string]int{}
	attributable, orphan, unreadableAll, noModel := 0, 0, 0, 0
	for _, p := range all {
		sid := strings.TrimSuffix(filepath.Base(p), ".jsonl")
		counts, err := modelsIn(p)
		if err != nil {
			unreadableAll++
			continue
		}
		if len(counts) == 0 {
			noModel++
			continue
		}
		m, _ := dominant(counts)
		byModelAll[m]++
		if nodeSessions[sid] {
			attributable++
		} else {
			orphan++
		}
	}
	fmt.Println("CONTAMINATION CHECK — every transcript in ~/.claude/projects:")
	fmt.Printf("  transcript files:                              %d\n", len(all))
	fmt.Printf("  claimed by SOME graph node's state.json:       %d\n", attributable)
	fmt.Printf("  claimed by NO graph node (ordinary sessions):  %d   <-- the contamination\n", orphan)
	fmt.Printf("  held no assistant record naming a model:       %d\n", noModel)
	fmt.Printf("  unreadable:                                    %d\n", unreadableAll)
	models := make([]string, 0, len(byModelAll))
	for m := range byModelAll {
		models = append(models, m)
	}
	sort.Slice(models, func(i, j int) bool { return byModelAll[models[i]] > byModelAll[models[j]] })
	fmt.Println("  model census over ALL transcripts (NOT a node census):")
	for _, m := range models {
		fmt.Printf("      %-24s %5d\n", m, byModelAll[m])
	}
	fmt.Println()
	fmt.Println("NOTE: message.model does not record the context-window variant, so this")
	fmt.Println("census cannot distinguish opus from opus[1m]. See the file header.")

	if rows == nil {
		rows = []nodeRow{}
	}
	out, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal: %v\n", err)
		os.Exit(1)
	}
	dest := filepath.Join("docs", "measurements", "0034-planned-node-model-raw.json")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(dest, append(out, '\n'), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", dest, err)
		os.Exit(1)
	}
	fmt.Printf("\nwrote %s (%d node rows)\n", dest, len(rows))
}
