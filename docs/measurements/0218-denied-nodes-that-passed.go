//go:build ignore

// Measure issue #218: how often did a planned node have a tool call DENIED at
// runtime, and how often did such a node nonetheless be recorded PASS?
//
// This is a MEASUREMENT, not a lint. It ships no behaviour and nothing in the
// engine calls it. The `ignore` build tag above keeps it out of
// `go build ./...` and `go test ./...`; run it explicitly:
//
//	go run docs/measurements/0218-denied-nodes-that-passed.go
//
// (`go run` with an explicit file argument does not apply build constraints,
// which is the whole reason the tag is safe here. Same convention as
// docs/measurements/0213-tool-grant-predicate.go.)
//
// PARSE, DO NOT GREP. Same scar as #213, and here it bites harder: this
// repository's own development sessions grep for the denial sentence, so the
// denial vocabulary appears inside ordinary Bash stdout. Every number below
// comes out of encoding/json walking actual snapshot objects and actual
// transcript records.
//
// The three questions, in order:
//
//	A. which runs on this machine are PLANNED, and what nodes did they run;
//	B. for each planned node, was at least one tool call denied — decided by
//	   the discriminator established by the prior node from real transcripts;
//	C. of the denied nodes, how many carry a recorded PASS, and what could
//	   that PASS have been made of.
//
// (C) is the point. A node that was denied a tool and still passed is only
// alarming in proportion to how little evidence its success_check demanded: a
// PASS resting on `verify` was judged by the engine running a command, a PASS
// resting on `result_matches` or on nothing but exit status was judged by the
// node's own words.
//
// ONE RUN IS DELIBERATELY MISSING FROM THE CORPUS: the one this measurement is
// itself a node of. A script that reads $OMG_HOME/runs while running under the
// engine reads its own half-written state.json — its own record is not written
// until it exits, so its own node counts as "never ran" and its own denials
// vanish from the numerator. The first pass of this file did exactly that and
// published 54 of 74 / 50 of 54, numbers that nobody, including a re-run of the
// script, could reproduce. scan() now excludes IN-FLIGHT runs and names them;
// see inFlight. Run the script twice and compare: every printed number must be
// identical, and that check is what catches this class of defect.
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

// ---------------------------------------------------------------------------
// A. the corpus
// ---------------------------------------------------------------------------

// snapshot is the subset of runstate.Snapshot this measurement reads. Field
// names were read off an actual state.json (schema 3), not guessed:
// internal/runstate/runstate.go owns them.
type snapshot struct {
	GraphSourcePath string `json:"graph_source_path"`
	// Graph is held opaquely by runstate as json.RawMessage, so on disk it is
	// an object; it is decoded in a second step to survive the (unobserved but
	// cheap to tolerate) case of a doubly-encoded string.
	Graph json.RawMessage `json:"graph"`
	// Nodes is a MAP keyed by node id — the id is not repeated inside the
	// record, which is why the graph half and the runtime half have to be
	// joined on that key below.
	Nodes map[string]nodeRecord `json:"nodes"`
}

// nodeRecord is runstate.NodeRecord's readable half. Provenance is carried into
// the raw output as a corroborator only; the (c) split below is decided by the
// graph's success_check, which is what #218 asks about.
type nodeRecord struct {
	Verdict    string `json:"verdict"`
	SessionID  string `json:"session_id"`
	Provenance string `json:"provenance"`
	Judged     bool   `json:"judged"`
}

type graphFile struct {
	Name  string      `json:"name"`
	Nodes []graphNode `json:"nodes"`
}

type graphNode struct {
	ID           string       `json:"id"`
	Type         string       `json:"type"`
	AllowedTools []string     `json:"allowed_tools"`
	SuccessCheck successCheck `json:"success_check"`
}

// successCheck mirrors graph.SuccessCheck's three predicate fields. Verify is
// kept raw for the same reason the product uses a pointer: "absent" and
// "declared but zero-valued" must stay distinguishable.
type successCheck struct {
	ExitZero      bool            `json:"exit_zero"`
	ResultMatches string          `json:"result_matches"`
	Verify        json.RawMessage `json:"verify"`
}

// The three kinds (c) splits on, in descending order of how much the engine
// itself had to see before it wrote PASS.
const (
	kindVerify  = "verify"         // the engine ran a command and judged it
	kindMatches = "result_matches" // the node passed by emitting the right words
	kindExit    = "exit-only"      // nothing but the subprocess exit status
)

// kind reports which of the three a check is. Verify wins when both are
// declared: an engine-run command is the stronger evidence and is what #218's
// question (c) is asking whether the node had.
func (c successCheck) kind() string {
	if len(c.Verify) > 0 && string(c.Verify) != "null" {
		return kindVerify
	}
	if c.ResultMatches != "" {
		return kindMatches
	}
	return kindExit
}

// runsDir is $OMG_HOME/runs when OMG_HOME is set, else $HOME/.oh-my-graph/runs.
func runsDir() string {
	if home := os.Getenv("OMG_HOME"); home != "" {
		return filepath.Join(home, "runs")
	}
	return filepath.Join(homeDir(), ".oh-my-graph", "runs")
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return os.Getenv("HOME")
}

// sameFile is the PLANNED test, and the only one used here. Copied unchanged
// from docs/measurements/0213-tool-grant-predicate.go so the two measurements
// share a corpus definition rather than two definitions that drift: a run is
// planned exactly when its snapshot's `graph_source_path`, after filepath.Clean
// and symlink resolution, is the same file as that run's own graph.json. A
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
	snap  snapshot
}

// skip reasons, reported separately because "300 skipped" with no breakdown is
// exactly the kind of headline number this repo has been burned by.
const (
	skipNoState     = "no state.json"
	skipNoGraph     = "no graph.json (a hand-written run writes none)"
	skipUnparseable = "state.json or graph.json would not parse"
)

type corpus struct {
	planned     []plannedRun
	skipped     map[string][]string // reason -> run ids
	handwritten []string            // readable, has a graph.json, but fails the planned test
	inFlight    []inFlightRun       // passes the planned test but was still executing
	seen        int
}

// inFlightRun is an excluded run plus the evidence for excluding it, because an
// exclusion nobody is told about is the same silent omission the skip
// breakdown above already guards against.
type inFlightRun struct {
	id        string
	graphSize int
	recorded  int
	noRecord  []string
}

func (c corpus) skippedCount() int {
	n := 0
	for _, ids := range c.skipped {
		n += len(ids)
	}
	return n
}

// decodeGraph reads the snapshot's opaque graph bytes. Handles the doubly
// encoded case (a JSON string holding JSON) rather than treating it as a parse
// failure, so an odd writer costs a defensive branch and not a silent skip.
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

// inFlight reports whether a planned run was still executing when this
// measurement read it — including, above all, the run this measurement is
// itself a node of. Such a run's snapshot is a photograph taken mid-exposure:
// the nodes that have not finished have no record at all, and the running node
// (this one) has none either, so its own denials are invisible to it.
//
// THE TEST: at least one node in the graph has no entry in snap.Nodes, AND no
// record in snap.Nodes carries the verdict FAIL. Under `on_fail: halt` a run
// that stopped early always leaves a FAIL behind, and a run still executing has
// not, so the two cases separate cleanly. This was verified against the corpus
// rather than assumed — exactly three planned runs hold a node with no record:
//
//	20260819-154136.440217000-1  no record [ship check]         has a FAIL -> halted,    kept
//	20260820-162555.890191000-1  no record [check]              has a FAIL -> halted,    kept
//	20260820-174446.982693000-1  no record [report commit ...]  no FAIL    -> in flight, EXCLUDED
//
// "has a node with no record" on its own is NOT the test: it would wrongly drop
// the two legitimately halted runs, and three more measurable nodes with them.
//
// A record carrying the EMPTY verdict is not a FAIL and must not be read as
// one: runstate documents the empty string as ADR 0010's non-terminal feedback
// marker, what a feedback declarer's record is rewritten to when its arc fires.
// That is the exact shape this measurement's own sibling review node carries.
func inFlight(g graphFile, snap snapshot) (bool, []string) {
	var noRecord []string
	for _, n := range g.Nodes {
		if _, ok := snap.Nodes[n.ID]; !ok {
			noRecord = append(noRecord, n.ID)
		}
	}
	if len(noRecord) == 0 {
		return false, nil
	}
	for _, rec := range snap.Nodes {
		if rec.Verdict == verdictFail {
			return false, nil // halted on a failure, not still running
		}
	}
	return true, noRecord
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
		if _, gerr := os.Stat(graphPath); gerr != nil {
			c.skipped[skipNoGraph] = append(c.skipped[skipNoGraph], id)
			continue
		}
		var snap snapshot
		if json.Unmarshal(stateRaw, &snap) != nil {
			c.skipped[skipUnparseable] = append(c.skipped[skipUnparseable], id)
			continue
		}
		g, ok := decodeGraph(snap.Graph)
		if !ok {
			c.skipped[skipUnparseable] = append(c.skipped[skipUnparseable], id)
			continue
		}
		if snap.GraphSourcePath == "" || !sameFile(snap.GraphSourcePath, graphPath) {
			c.handwritten = append(c.handwritten, id)
			continue
		}
		if running, noRecord := inFlight(g, snap); running {
			sort.Strings(noRecord)
			c.inFlight = append(c.inFlight, inFlightRun{
				id:        id,
				graphSize: len(g.Nodes),
				recorded:  len(snap.Nodes),
				noRecord:  noRecord,
			})
			continue
		}
		c.planned = append(c.planned, plannedRun{id: id, graph: g, snap: snap})
	}
	return c, nil
}

// ---------------------------------------------------------------------------
// B. the discriminator
// ---------------------------------------------------------------------------

// The predicate, verbatim from the discriminator established by the prior node
// against the real corpus (~/.oh-my-graph/runs/20260820-174446.982693000-1/
// discriminator.out). Two things about it are load-bearing and are NOT to be
// loosened:
//
//   - It is PROSE. Every is_error tool_result in the corpus — denials and
//     ordinary `go test` failures alike — carries the identical key set
//     {content, is_error, tool_use_id, type}. `is_error: true` on its own is
//     NOT a denial test and would count every failed command as one.
//   - The head is anchored at offset 0. This repository's own sessions grep for
//     these words, so ordinary Bash failures carry the denial sentence inside
//     captured stdout after an "Exit code 1\n" prefix. A Contains-based
//     predicate counts those as denials; HasPrefix rejects all of them.
//
// The apostrophe in "don't" is ASCII U+0027, checked at the codepoint.
const denialHead = "Permission to use "
const denialCore = " has been denied because Claude Code is running in don't ask mode."

// isDontAskDenial reports whether a rendered tool_result prose is a dontAsk
// permission denial, and names the tool the CLI said it denied.
func isDontAskDenial(prose string) (tool string, ok bool) {
	if !strings.HasPrefix(prose, denialHead) { // anchored at offset 0 — load-bearing
		return "", false
	}
	i := strings.Index(prose, denialCore)
	if i < 0 {
		return "", false
	}
	return prose[len(denialHead):i], true
}

// This predicate deliberately excludes four other denial-ish classes, each of
// which the prior node addressed separately and each of which is a different
// decision for #218: the rule/interactive phrasing that names the command
// ("Permission to use Bash with command … has been denied."), the auto-mode
// classifier ("Permission for this action was denied by the Claude Code auto
// mode classifier."), interactive user rejection (needs a TTY; cannot occur in
// a `claude -p` node), and "No such tool available" (a grant-time absence —
// that is #154's class, not a runtime denial). Numbers below are therefore a
// FLOOR on denials, not a total.

type record struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message"`
}

type messageEnvelope struct {
	Content json.RawMessage `json:"content"`
}

type block struct {
	Type      string          `json:"type"`
	IsError   *bool           `json:"is_error"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

type textBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// renderProse turns a tool_result's .content into the prose the predicate reads.
// It is a JSON string in every denial observed; the array-of-text-blocks form is
// accepted defensively and joined with "\n".
func renderProse(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []textBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// scanResult is what one transcript file yielded. undecodable and oversize are
// reported rather than swallowed for the same reason skips are: a line this
// program could not read is a line that could have held a denial.
type scanResult struct {
	denials     int
	tools       map[string]int
	lines       int
	undecodable int
	oversize    bool
}

// maxLine is generous on purpose. A transcript line holding a large tool result
// can be megabytes; bufio's 64 KiB default would report ErrTooLong on records
// that are perfectly well formed, and a silently dropped record is a silently
// missed denial.
const maxLine = 64 << 20

func scanTranscript(path string, res *scanResult) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), maxLine)
	for sc.Scan() {
		line := sc.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		res.lines++
		var rec record
		if json.Unmarshal(line, &rec) != nil {
			res.undecodable++
			continue
		}
		if rec.Type != "user" {
			continue
		}
		var msg messageEnvelope
		if json.Unmarshal(rec.Message, &msg) != nil {
			continue // a non-object message (e.g. a plain string) holds no blocks
		}
		var blocks []block
		if json.Unmarshal(msg.Content, &blocks) != nil {
			continue // content is a bare string; it carries no tool_result
		}
		for _, b := range blocks {
			if b.Type != "tool_result" || b.IsError == nil || !*b.IsError {
				continue
			}
			tool, ok := isDontAskDenial(renderProse(b.Content))
			if !ok {
				continue
			}
			res.denials++
			res.tools[tool]++
		}
	}
	if err := sc.Err(); err != nil {
		res.oversize = true
		return err
	}
	return nil
}

// transcriptsFor finds a node's transcript BY FILENAME. The project slug is
// never reconstructed: the slug is a lossy encoding of a working directory
// (worktrees, /private/var, symlinks) and rebuilding it is how a measurement
// invents a missing file. A session id is a uuid, so the glob is effectively
// exact; multiple matches are possible in principle (the same session copied
// under two project dirs) and are all scanned, and counted.
func transcriptsFor(sessionID string) []string {
	if sessionID == "" {
		return nil
	}
	pattern := filepath.Join(homeDir(), ".claude", "projects", "*", sessionID+".jsonl")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	sort.Strings(matches)
	return matches
}

// ---------------------------------------------------------------------------
// C. output
// ---------------------------------------------------------------------------

// nodeDetail is one row of the durable evidence file — one per planned node,
// whether or not it was denied, whether or not it even ran. Every number the
// summary prints is a count over these rows and can be re-derived from them.
type nodeDetail struct {
	RunID  string `json:"run_id"`
	NodeID string `json:"node_id"`
	// NodeType and AllowedTools are carried so a reader can see the ceiling the
	// node held; the prior node showed the ceiling does NOT predict denial
	// (denial is per-command, not per-tool), so they are evidence, not inputs.
	NodeType         string         `json:"node_type"`
	AllowedTools     []string       `json:"allowed_tools"`
	SessionID        string         `json:"session_id"`
	Transcript       string         `json:"transcript"`
	ExtraTranscripts []string       `json:"extra_transcripts,omitempty"`
	Ran              bool           `json:"ran"`
	Unreadable       bool           `json:"unreadable"`
	Denied           bool           `json:"denied"`
	DeniedTools      []string       `json:"denied_tools"`
	DenialCount      int            `json:"denial_count"`
	DenialsByTool    map[string]int `json:"denials_by_tool,omitempty"`
	Verdict          string         `json:"verdict"`
	Provenance       string         `json:"provenance"`
	SuccessCheckKind string         `json:"success_check_kind"`
	TranscriptLines  int            `json:"transcript_lines"`
	UndecodableLines int            `json:"undecodable_lines"`
	Note             string         `json:"note,omitempty"`
}

// Notes are the judgement calls, written into every row they apply to so the
// evidence file explains its own exclusions.
const (
	noteNoRecord   = "no record in state.nodes — the run halted before this node was reached"
	noteNoSession  = "record present but session_id is empty — no transcript addressable"
	noteMissing    = "session_id present but no ~/.claude/projects/*/<id>.jsonl matched"
	noteUnreadable = "transcript matched but could not be read in full — excluded from both numerator and denominator"
)

// The verdict vocabulary is closed: internal/runstate declares exactly
// VerdictPass="PASS", VerdictFail="FAIL", and documents the empty string for a
// non-terminal record. Only the exact string "PASS" is treated as a pass; any
// other non-empty, non-FAIL value is reported as an anomaly rather than folded
// in, because a verdict this program does not recognise is a number it cannot
// stand behind.
const verdictPass = "PASS"
const verdictFail = "FAIL"

func main() {
	dir := runsDir()
	c, err := scan(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot read %s: %v\n", dir, err)
		os.Exit(1)
	}
	sort.Slice(c.planned, func(i, j int) bool { return c.planned[i].id < c.planned[j].id })
	sort.Slice(c.inFlight, func(i, j int) bool { return c.inFlight[i].id < c.inFlight[j].id })

	var details []nodeDetail
	var readErrs []string
	oddVerdicts := map[string][]string{}

	for _, p := range c.planned {
		for _, n := range p.graph.Nodes {
			d := nodeDetail{
				RunID:            p.id,
				NodeID:           n.ID,
				NodeType:         n.Type,
				AllowedTools:     n.AllowedTools,
				DeniedTools:      []string{},
				SuccessCheckKind: n.SuccessCheck.kind(),
				Transcript:       "missing",
			}
			if d.AllowedTools == nil {
				d.AllowedTools = []string{}
			}

			rec, ok := p.snap.Nodes[n.ID]
			switch {
			case !ok:
				d.Note = noteNoRecord
			default:
				d.Ran = true
				d.Verdict = rec.Verdict
				d.Provenance = rec.Provenance
				d.SessionID = rec.SessionID
				if rec.Verdict != verdictPass && rec.Verdict != verdictFail && rec.Verdict != "" {
					oddVerdicts[rec.Verdict] = append(oddVerdicts[rec.Verdict], p.id+"/"+n.ID)
				}
				if rec.SessionID == "" {
					d.Note = noteNoSession
				}
			}

			paths := transcriptsFor(d.SessionID)
			if d.Ran && d.SessionID != "" && len(paths) == 0 {
				d.Note = noteMissing
			}
			if len(paths) > 0 {
				d.Transcript = paths[0]
				if len(paths) > 1 {
					d.ExtraTranscripts = paths[1:]
				}
				res := scanResult{tools: map[string]int{}}
				for _, path := range paths {
					if err := scanTranscript(path, &res); err != nil {
						readErrs = append(readErrs, fmt.Sprintf("%s/%s %s: %v", p.id, n.ID, path, err))
						// A partially read transcript is a silent negative: the
						// denials this program did see are a floor on a file whose
						// remainder it cannot speak for. Such a node leaves BOTH
						// sides of the fraction, exactly as a missing transcript
						// does, and is reported in its own bucket.
						d.Unreadable = true
						d.Note = noteUnreadable
					}
				}
				d.DenialCount = res.denials
				d.TranscriptLines = res.lines
				d.UndecodableLines = res.undecodable
				if !d.Unreadable {
					d.Denied = res.denials > 0
				}
				if len(res.tools) > 0 {
					d.DenialsByTool = res.tools
					for t := range res.tools {
						d.DeniedTools = append(d.DeniedTools, t)
					}
					sort.Strings(d.DeniedTools)
				}
			}
			details = append(details, d)
		}
	}

	// ---- the counts -------------------------------------------------------
	var (
		plannedNodes   = len(details)
		noRecord       int
		noSession      int
		missing        int
		unreadable     int
		withTranscript int
		denied         []nodeDetail
		deniedPass     []nodeDetail
		byKind         = map[string]int{}
		undecodable    int
		multiMatch     int
	)
	for _, d := range details {
		undecodable += d.UndecodableLines
		if len(d.ExtraTranscripts) > 0 {
			multiMatch++
		}
		switch {
		case !d.Ran:
			noRecord++
			continue
		case d.SessionID == "":
			noSession++
			continue
		case d.Transcript == "missing":
			missing++
			continue
		case d.Unreadable:
			unreadable++
			continue
		}
		withTranscript++
		if !d.Denied {
			continue
		}
		denied = append(denied, d)
		if d.Verdict == verdictPass {
			deniedPass = append(deniedPass, d)
			byKind[d.SuccessCheckKind]++
		}
	}

	// ---- the report -------------------------------------------------------
	fmt.Printf("runs directory:                  %s\n", dir)
	fmt.Printf("run directories seen:            %d\n", c.seen)
	fmt.Printf("  skipped:                       %d\n", c.skippedCount())
	for _, reason := range []string{skipNoState, skipNoGraph, skipUnparseable} {
		fmt.Printf("      %-46s %d\n", reason, len(c.skipped[reason]))
	}
	fmt.Printf("  readable, has graph.json, but FAILS the planned test: %d\n", len(c.handwritten))
	fmt.Printf("  planned but IN FLIGHT (excluded, see below):           %d\n", len(c.inFlight))
	fmt.Printf("  PLANNED runs (the corpus):                            %d\n", len(c.planned))
	fmt.Printf("planned nodes:                   %d\n", plannedNodes)
	fmt.Println()

	if len(c.inFlight) > 0 {
		fmt.Println("IN-FLIGHT runs excluded from the corpus (a node has no record in state.nodes")
		fmt.Println("and no record carries FAIL, so the run was still executing rather than halted;")
		fmt.Println("its unfinished nodes' denials are not yet on disk — including this script's own):")
		for _, r := range c.inFlight {
			fmt.Printf("  %s | %d graph nodes, %d recorded | no record: %s\n",
				r.id, r.graphSize, r.recorded, strings.Join(r.noRecord, " "))
		}
		fmt.Println()
	}

	fmt.Println("reachability of the planned nodes:")
	fmt.Printf("  with a readable transcript (the DENOMINATOR):        %d\n", withTranscript)
	fmt.Printf("  MISSING TRANSCRIPT (session_id, no matching file):   %d   <-- excluded\n", missing)
	fmt.Printf("  transcript matched but UNREADABLE:                   %d   <-- excluded\n", unreadable)
	fmt.Printf("  no session_id on the record:                         %d   <-- excluded\n", noSession)
	fmt.Printf("  no record in state.nodes (run halted before it):     %d   <-- excluded\n", noRecord)
	fmt.Printf("  session_id matched more than one project dir:        %d\n", multiMatch)
	fmt.Printf("  transcript lines that would not decode as JSON:      %d\n", undecodable)
	if len(readErrs) > 0 {
		fmt.Printf("  transcripts that could not be read in full:          %d\n", len(readErrs))
		for _, e := range readErrs {
			fmt.Printf("      %s\n", e)
		}
	}
	fmt.Println()

	fmt.Println("the discriminator (anchored dontAsk denial prose; see the file header):")
	fmt.Printf("(a) planned nodes DENIED at least one tool call:  %d of %d\n", len(denied), withTranscript)
	fmt.Printf("(b)   of those, recorded verdict %q:            %d of %d\n", verdictPass, len(deniedPass), len(denied))
	fmt.Printf("(c)     of those, success_check declared:\n")
	fmt.Printf("          %-16s (engine ran a command) %d of %d\n", kindVerify, byKind[kindVerify], len(deniedPass))
	fmt.Printf("          %-16s (node's own words)     %d of %d\n", kindMatches, byKind[kindMatches], len(deniedPass))
	fmt.Printf("          %-16s (nothing but exit)     %d of %d\n", kindExit, byKind[kindExit], len(deniedPass))
	fmt.Println()

	fmt.Printf("verdict strings treated as PASS: exactly %q (internal/runstate declares only\n", verdictPass)
	fmt.Printf("  %q, %q and the empty string for a non-terminal record).\n", verdictPass, verdictFail)
	if len(oddVerdicts) > 0 {
		fmt.Println("  UNEXPECTED verdict strings found — NOT counted as PASS:")
		var vs []string
		for v := range oddVerdicts {
			vs = append(vs, v)
		}
		sort.Strings(vs)
		for _, v := range vs {
			fmt.Printf("      %q  %d: %s\n", v, len(oddVerdicts[v]), strings.Join(oddVerdicts[v], ", "))
		}
	} else {
		fmt.Println("  no unexpected verdict string occurred in this corpus.")
	}
	fmt.Println()

	if len(denied) > 0 {
		fmt.Println("every denied node (run | node | verdict | success_check | denied tools):")
		for _, d := range denied {
			fmt.Printf("  %s | %-12s | %-4s | %-14s | %s (%d denial(s))\n",
				d.RunID, d.NodeID, blankAs(d.Verdict, "-"), d.SuccessCheckKind,
				strings.Join(d.DeniedTools, ","), d.DenialCount)
		}
		fmt.Println()
	}

	if missing > 0 {
		fmt.Printf("nodes excluded for a MISSING TRANSCRIPT (%d) — these are neither numerator\n", missing)
		fmt.Println("nor denominator, and any rate above is a rate over what remains:")
		for _, d := range details {
			if d.Ran && d.SessionID != "" && d.Transcript == "missing" {
				fmt.Printf("  %s | %-12s | session %s | verdict %s\n",
					d.RunID, d.NodeID, d.SessionID, blankAs(d.Verdict, "-"))
			}
		}
		fmt.Println()
	}

	if unreadable > 0 {
		fmt.Printf("nodes excluded for an UNREADABLE TRANSCRIPT (%d) — a partially read file is a\n", unreadable)
		fmt.Println("floor, not a negative, so these leave both sides of the fraction:")
		for _, d := range details {
			if d.Unreadable {
				fmt.Printf("  %s | %-12s | %s | %d line(s) read, %d denial(s) seen\n",
					d.RunID, d.NodeID, d.Transcript, d.TranscriptLines, d.DenialCount)
			}
		}
		fmt.Println()
	}

	if details == nil {
		details = []nodeDetail{}
	}
	out, err := json.MarshalIndent(details, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal: %v\n", err)
		os.Exit(1)
	}
	dest := filepath.Join("docs", "measurements", "0218-denied-nodes-raw.json")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(dest, append(out, '\n'), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", dest, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d planned nodes)\n", dest, len(details))
}

func blankAs(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}
