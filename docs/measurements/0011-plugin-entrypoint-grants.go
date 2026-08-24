//go:build ignore

// Measurement 0011 — does a prefix grant of the form `Bash(<prog> *)` distinguish
// SUBCOMMANDS at all, or does it authorise the binary and therefore every verb?
//
// The question comes from the plugin's entry points. plugin/commands/graph.md:4
// scopes the slash command to `Bash(oh-my-graph run *), Bash(oh-my-graph auto *)`
// — two NARROW grants, one per verb — while plugin/agents/oh-my-graph.md:4 names
// the WIDE `Bash(oh-my-graph *)`. If a prefix grant cannot tell `run` from
// `serve`, the narrow pair buys nothing over the wide one, and plugin/README.md's
// claim that "nothing outside those command prefixes is granted" (line 27) is
// false. If it CAN, the narrow pair is load-bearing and the agent's wide form is
// a genuinely larger surface.
//
// This is a MEASUREMENT, not a lint. It ships no behaviour and nothing in the
// engine calls it. The `ignore` build tag keeps it out of `go build ./...` and
// `go test ./...`; run it explicitly, which does not apply build constraints:
//
//	go run docs/measurements/0011-plugin-entrypoint-grants.go
//
// It takes NO ARGUMENTS and — unlike docs/measurements/0213b-compound-commands.go,
// which writes two result files — it WRITES NOTHING. Its brief permitted exactly
// one new file in the repository, this one, so the whole report is stdout and a
// caller who wants it durable redirects it themselves.
//
// PARSE, DO NOT GREP. Same scar as #213 and #218: a `grep -c` figure in this
// repository reached three documents before anyone noticed it was wrong. Every
// number below comes out of encoding/json walking actual transcript records, and
// every declared grant out of gopkg.in/yaml.v3 walking actual YAML scalars —
// never out of a line match, which would also count the six `Bash(...)` strings
// that appear in this repo's YAML COMMENTS (graphs/adr-driven-dev.yaml:230-232,
// graphs/fragments/e2e-verify.yaml:35-36, graphs/fragments/gated-lane.yaml:83,
// graphs/merge-shepherd.yaml:689) as if a node held them.
//
// METHOD, and the conventions it inherits:
//
//   - CORPUS = EVERY TRANSCRIPT, not every run. 0213b and 0218 start from
//     $OMG_HOME/runs and reach transcripts through each node's session_id; this
//     one starts from ~/.claude/projects/*/*.jsonl directly, because the question
//     is about the MATCHER and not about a run. The run corpus is still read, but
//     only to attribute a transcript to the grant list its node held (see
//     sessionGrants): a transcript's file name IS its session id.
//   - JOIN BY tool_use_id, NEVER BY ADJACENCY — carried from 0213b. A tool_result
//     names the tool_use it answers. This CLI splits one assistant message across
//     several JSONL lines and interleaves attachment / last-prompt / ai-title
//     records between a call and its result, so adjacency is wrong here.
//   - is_error IS NOT DENIAL — carried from 0213b's classifyError. An ordinary
//     tool failure also sets is_error but is wrapped in <tool_use_error>; a
//     permission denial is unwrapped and starts "Permission to use ", anchored at
//     offset 0. A Contains test would match this repo's own sessions grepping for
//     the denial sentence, and every node that quoted its denial back into an
//     artifact. A `go build` that exits 1 is an ALLOWED call in this measurement:
//     the matcher let it run. Only a policy denial counts as DENIED.
//   - THE SCANNER is 0218's: bufio with a 64 MiB line bound, because a transcript
//     line holding a large tool result can be megabytes and bufio's 64 KiB default
//     would silently drop a well-formed record — a dropped record is a missed
//     denial.
//   - NO COMPOUND COMMANDS. A command containing | ; or && is skipped, per brief.
//     docs/measurements/0213b-compound-commands-defeat-grants.md already measured
//     that shape separately (64 of 246 denials); mixing it in here would confound
//     "the grant did not cover the verb" with "the grant did not cover `head`".
//   - EXCLUDE THIS LANE'S OWN SESSIONS. ~/.claude/projects/-private-tmp-w-b3 is
//     the project directory of the worktree this program was written in; its
//     transcripts are being appended to while it runs, so including them would
//     make the corpus measure itself and report a figure that moves under its own
//     feet. #218 published two unreproducible fractions by making exactly this
//     mistake. The excluded files are named in section 1, with how many were node
//     sessions. One was: run 20260822-010107.356534000-1 node `entrypoints`, this
//     same lane's earlier node. It carries no `setting_sources`, so the isolation
//     filter below would drop it anyway and the exclusion costs no evidence.
//
// THE TWO CONFOUNDS THIS MEASUREMENT EXISTS TO AVOID, stated up front:
//
//	(1) ~/.claude/settings.json declares "Bash(*)" in permissions.allow. In any
//	session that loads it, that ONE rule allows every Bash call ever made, so
//	"this command was allowed" is evidence about that rule and about nothing
//	else. A whole-corpus tally is still reported (section 7, the brief asks for
//	it) but it is NOT the evidence.
//
//	(2) NOT EVERY NODE SESSION IS ISOLATED FROM IT, and this is the finding that
//	decides the measurement. internal/runstate/runstate.go:130-134 and
//	internal/runner/runner.go:55-60 both say it: `setting_sources` is a *string
//	where a pointer to "" renders `--setting-sources ""` ("loads NONE of them,
//	leaving this argv as the only allow-rule source") and NIL OMITS THE FLAG, so
//	"the user's user/project/local settings load as usual". The field is
//	`omitempty`, so an ABSENT `setting_sources` on disk means nil, means THE
//	Bash(*) ABOVE WAS IN FORCE for that node. A large minority of this corpus's
//	node policies are that shape (section 4 prints the split). Counting them
//	would undo the whole measurement: in such a run a node holding only
//	`Bash(gh pr *)` runs `gh issue create` perfectly happily, and that says
//	nothing whatever about the matcher. Sections 5 and 6 are therefore restricted
//	to ISOLATED node sessions, and section 4 shows the filter is not cosmetic.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Constants of the measurement
// ---------------------------------------------------------------------------

// ownProjectDir is the ~/.claude/projects slug of the worktree this program was
// written in. See the "EXCLUDE THIS LANE'S OWN SESSIONS" note in the header.
const ownProjectDir = "-private-tmp-w-b3"

// denialPolicyPrefix is the exact opening of a permission-denial tool_result,
// anchored at offset 0. Carried verbatim from 0213b (denialPolicyPrefix) and
// 0218 (denialHead).
const denialPolicyPrefix = "Permission to use "

// userRejectPrefix is the second denial-shaped wording in the corpus: a human
// pressing "no" interactively. It cannot occur in an unattended `dontAsk` node,
// which is why it is counted separately rather than folded into the denominator.
const userRejectPrefix = "The user doesn't want to proceed with this tool use."

// toolUseErrorPrefix wraps an ORDINARY tool failure (a bad path, a non-zero
// exit). It also sets is_error, and it is NOT a denial — the call ran.
const toolUseErrorPrefix = "<tool_use_error>"

// errorPrefix is what the duplicate .toolUseResult copy prepends. Only
// message.content[] is read, so this is belt-and-braces.
const errorPrefix = "Error: "

// maxLine is 0218's bound, and generous on purpose: bufio's 64 KiB default
// reports ErrTooLong on perfectly well-formed records holding a big tool result,
// and a silently dropped record is a silently missed denial.
const maxLine = 64 << 20

// maxExamples is how many distinct command strings are kept per cell. They are
// the LEXICALLY SMALLEST distinct commands, not the first seen, so the report is
// deterministic: two runs over an unchanged corpus print identical text.
const maxExamples = 3

// ---------------------------------------------------------------------------
// THE ASSUMPTION
// ---------------------------------------------------------------------------

// assumptionText is printed at the top of the report. How Claude Code applies
// `Bash(go *)` lives in the Claude Code binary; no source in this repository
// implements it, and this program does not claim to. What it does is OBSERVE the
// matcher's decisions and ask whether they are consistent with a verb-blind or a
// verb-aware rule. A stated assumption is honest; a silent one is the failure
// mode this repo keeps writing down.
const assumptionText = `WHAT IS OBSERVED vs WHAT IS ASSUMED:

  OBSERVED (from transcripts): for each Bash call, the command string, and
    whether the CLI ran it or answered with a policy denial. Nothing else about
    the matcher is visible — the denial text carries NO reason code and is
    byte-identical for "no rule matched", an explicit deny, and a sandbox refusal
    (0213b established this).
  OBSERVED (from state.json tool_policies): the exact --allowedTools list the
    engine handed that node's CLI. This is the durable record; DESIGN.md:2195-2199
    notes a grant can be invisible in graph.json while present here.
  ASSUMED: that the grant list in tool_policies was the ONLY allow rule in force
    for that node session. Section 4 tests this rather than asserting it.
  NOT MODELLED: whether a denial arose from failure-to-match, an explicit deny,
    or a sandbox/working-directory check. docs/measurements/
    bash-denials-are-path-sensitive (memory) records that something
    path-sensitive is also operating, so a single denial is never proof on its
    own; a verb denied ACROSS nodes and paths while its sibling verb ran is.

THE claude VERSION THAT PRODUCED THIS CORPUS IS UNKNOWN. No run record carries
it. If grant matching changed across versions, the corpus is two populations.`

// ---------------------------------------------------------------------------
// Grant classification — the one rule this report's tables rest on
// ---------------------------------------------------------------------------

// grantClass says how a node's grant list relates to one PROGRAM.
type grantClass int

const (
	classNone   grantClass = iota // no grant in the list mentions this program
	classWide                     // Bash(prog *) or Bash(prog*) — binary, no verb
	classNarrow                   // Bash(prog verb *) — binary AND verb
	classBoth                     // both forms present: the wide one subsumes
)

func (g grantClass) String() string {
	switch g {
	case classWide:
		return "wide"
	case classNarrow:
		return "narrow"
	case classBoth:
		return "wide+narrow"
	default:
		return "none"
	}
}

// parseGrant splits a grant string into its program and, when the grant names a
// verb, that verb. It returns ok=false for anything that is not a Bash grant
// with a program token: a bare "Bash", "Bash(*)", "Read", "Skill", and the
// wildcard-only forms carry no program and cannot appear in either table.
//
// The trailing "*" is accepted BOTH detached and attached, because this repo
// declares both spellings: graphs/merge-shepherd.yaml:695 writes
// "Bash(gh pr view *)" and graphs/fragments/review-style.yaml:31 writes
// "Bash(git diff*)". Treating "git diff*" as if the program were `git diff*`
// would drop every one of the attached-star grants out of the narrow table,
// which is the table the contrast case lives in.
func parseGrant(grant string) (prog, verb string, wide, ok bool) {
	grant = strings.TrimSpace(grant)
	if !strings.HasPrefix(grant, "Bash(") || !strings.HasSuffix(grant, ")") {
		return "", "", false, false
	}
	pattern := strings.TrimSpace(grant[len("Bash(") : len(grant)-1])
	if pattern == "" || pattern == "*" {
		return "", "", false, false
	}
	// Detached star: "gh pr view *" -> tokens [gh pr view].
	// Attached star: "git diff*"    -> tokens [git diff] with the star stripped.
	tokens := strings.Fields(pattern)
	if len(tokens) == 0 {
		return "", "", false, false
	}
	if tokens[len(tokens)-1] == "*" {
		tokens = tokens[:len(tokens)-1]
	} else {
		tokens[len(tokens)-1] = strings.TrimSuffix(tokens[len(tokens)-1], "*")
		if tokens[len(tokens)-1] == "" {
			tokens = tokens[:len(tokens)-1]
		}
	}
	if len(tokens) == 0 {
		return "", "", false, false
	}
	prog = tokens[0]
	if len(tokens) == 1 {
		return prog, "", true, true
	}
	return prog, tokens[1], false, true
}

// grantsFor reduces a node's whole grant list to one program's class plus the
// verbs the narrow grants named.
func grantsFor(grants []string, prog string) (grantClass, []string) {
	class := classNone
	var verbs []string
	for _, g := range grants {
		p, v, wide, ok := parseGrant(g)
		if !ok || p != prog {
			continue
		}
		if wide {
			if class == classNarrow || class == classBoth {
				class = classBoth
			} else {
				class = classWide
			}
			continue
		}
		verbs = append(verbs, v)
		if class == classWide || class == classBoth {
			class = classBoth
		} else {
			class = classNarrow
		}
	}
	sort.Strings(verbs)
	return class, uniq(verbs)
}

// ---------------------------------------------------------------------------
// Declared grants, with an address for each
// ---------------------------------------------------------------------------

// declaration is one place a grant string is DECLARED. A grant only counts as
// evidence if it has one of these; the brief is explicit about that, and so is
// this repo's first rule ("근거에는 주소가 있어야 한다").
type declaration struct {
	Grant string
	Path  string
	Line  int // 0 when the source has no meaningful line (a state.json policy)
	Note  string
}

func (d declaration) address() string {
	if d.Line > 0 {
		return fmt.Sprintf("%s:%d", d.Path, d.Line)
	}
	return d.Path
}

// scanGraphYAML walks graphs/**/*.yaml with the yaml.v3 NODE API — not a line
// match — and returns every SCALAR whose value is a Bash grant, with the line the
// parser reports. Comments are not scalars, so the `Bash(go *)` strings written
// in this repo's YAML prose never reach this list.
func scanGraphYAML(root string) ([]declaration, []string, error) {
	var decls []declaration
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil // unreadable file: reported by absence from files
		}
		var doc yaml.Node
		if yaml.Unmarshal(data, &doc) != nil {
			return nil
		}
		files = append(files, path)
		walkYAML(&doc, func(n *yaml.Node) {
			if n.Kind != yaml.ScalarNode {
				return
			}
			v := strings.TrimSpace(n.Value)
			if strings.HasPrefix(v, "Bash(") && strings.HasSuffix(v, ")") {
				decls = append(decls, declaration{Grant: v, Path: path, Line: n.Line})
			}
		})
		return nil
	})
	return decls, files, err
}

func walkYAML(n *yaml.Node, fn func(*yaml.Node)) {
	if n == nil {
		return
	}
	fn(n)
	for _, c := range n.Content {
		walkYAML(c, fn)
	}
}

// scanSettings reads a settings JSON file with encoding/json (a parser), then
// LOCATES each grant's line by finding its JSON-quoted spelling. Locating is the
// one thing a text search is allowed to do here — the SET of grants comes from
// the parser, and a grant whose line cannot be located is still reported, with
// line 0, rather than dropped.
func scanSettings(path string) ([]declaration, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var doc struct {
		Permissions struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return nil, false
	}
	lines := strings.Split(string(data), "\n")
	var decls []declaration
	for _, g := range doc.Permissions.Allow {
		if !strings.HasPrefix(g, "Bash") {
			continue
		}
		decls = append(decls, declaration{
			Grant: g, Path: path, Line: locateLiteral(lines, g),
			Note: "permissions.allow",
		})
	}
	return decls, true
}

func locateLiteral(lines []string, want string) int {
	quoted, err := json.Marshal(want)
	if err != nil {
		return 0
	}
	for i, ln := range lines {
		if strings.Contains(ln, string(quoted)) {
			return i + 1
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// The run corpus — only to attribute a session to the grants its node held
// ---------------------------------------------------------------------------

type nodePolicy struct {
	AllowedTools    []string `json:"allowed_tools"`
	DisallowedTools []string `json:"disallowed_tools"`
	// SettingSources is a POINTER for the same reason runstate makes it one:
	// absent (nil) and explicitly "" are OPPOSITE policies, and collapsing them
	// into a string would make the isolation filter silently pass everything.
	// nil  -> no --setting-sources flag -> ~/.claude/settings.json LOADED
	// ""   -> --setting-sources ""      -> this argv is the only allow source
	SettingSources *string `json:"setting_sources"`
}

type stateFile struct {
	RunID        string                `json:"run_id"`
	ToolPolicies map[string]nodePolicy `json:"tool_policies"`
	Nodes        map[string]struct {
		SessionID string `json:"session_id"`
		Verdict   string `json:"verdict"`
	} `json:"nodes"`
}

// nodeScope is what one node session was allowed to run, plus its address.
type nodeScope struct {
	RunID  string
	NodeID string
	Grants []string
	// BashByName is true when the node's disallowed_tools holds the BARE name
	// "Bash". DESIGN.md:113-116 records that a bare disallowed name beats every
	// allow, so such a session's denials say nothing about prefix matching and it
	// is excluded from the scoped tables.
	BashByName bool
	// Isolated is the load-bearing filter: true only when setting_sources is
	// PRESENT and empty, i.e. the node ran under `--setting-sources ""` and its
	// own --allowedTools list was the only allow-rule source. See confound (2).
	Isolated bool
}

func (s nodeScope) address() string { return s.RunID + "/" + s.NodeID }

// runsDir is $OMG_HOME/runs when OMG_HOME is set, else $HOME/.oh-my-graph/runs.
// Carried from 0218.
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

// sessionScopes maps a session id to the node whose CLI held it. A session id
// claimed by two nodes (a `handoff: session` resume) is reported and dropped:
// two grant lists over one transcript cannot be told apart call by call, and
// guessing which applied is how a measurement invents evidence.
func sessionScopes(dir string) (map[string]nodeScope, runCounts, []string, error) {
	scopes := map[string]nodeScope{}
	var shared []string
	var rc runCounts
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, rc, nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		rc.runDirs++
		raw, rerr := os.ReadFile(filepath.Join(dir, e.Name(), "state.json"))
		if rerr != nil {
			continue
		}
		var st stateFile
		if json.Unmarshal(raw, &st) != nil {
			continue
		}
		if len(st.ToolPolicies) == 0 {
			continue
		}
		rc.withPolicies++
		for _, pol := range st.ToolPolicies {
			if pol.SettingSources != nil && *pol.SettingSources == "" {
				rc.isolatedPolicies++
			} else {
				rc.loadedPolicies++
			}
		}
		runID := st.RunID
		if runID == "" {
			runID = e.Name()
		}
		for nodeID, rec := range st.Nodes {
			if rec.SessionID == "" {
				continue
			}
			pol, ok := st.ToolPolicies[nodeID]
			if !ok {
				continue
			}
			sc := nodeScope{
				RunID: runID, NodeID: nodeID, Grants: pol.AllowedTools,
				BashByName: contains(pol.DisallowedTools, "Bash"),
				Isolated:   pol.SettingSources != nil && *pol.SettingSources == "",
			}
			if prev, dup := scopes[rec.SessionID]; dup {
				if prev.address() != sc.address() {
					shared = append(shared, rec.SessionID+" "+prev.address()+" "+sc.address())
				}
				continue
			}
			scopes[rec.SessionID] = sc
		}
	}
	sort.Strings(shared)
	return scopes, rc, uniq(shared), nil
}

// runCounts is the corpus arithmetic sessionScopes can see and main cannot.
type runCounts struct {
	runDirs          int
	withPolicies     int
	isolatedPolicies int
	loadedPolicies   int
}

// ---------------------------------------------------------------------------
// Transcript records — shapes carried from 0213b
// ---------------------------------------------------------------------------

type record struct {
	Type    string `json:"type"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

type bashInput struct {
	Command string `json:"command"`
}

type errShape int

const (
	shapeNotError errShape = iota
	shapeToolFailure
	shapePolicyDenial
	shapeUserRejection
	shapeUnrecognised
)

func (e errShape) String() string {
	switch e {
	case shapeToolFailure:
		return "tool-failure"
	case shapePolicyDenial:
		return "policy-denial"
	case shapeUserRejection:
		return "user-rejection"
	case shapeUnrecognised:
		return "unrecognised-error-wording"
	default:
		return "not-an-error"
	}
}

// classifyError is 0213b's discriminator, unchanged. See the header.
func classifyError(isError bool, text string) errShape {
	if !isError {
		return shapeNotError
	}
	t := strings.TrimPrefix(strings.TrimSpace(text), errorPrefix)
	switch {
	case strings.HasPrefix(t, toolUseErrorPrefix):
		return shapeToolFailure
	case strings.HasPrefix(t, denialPolicyPrefix):
		return shapePolicyDenial
	case strings.HasPrefix(t, userRejectPrefix):
		return shapeUserRejection
	default:
		return shapeUnrecognised
	}
}

// blockText renders a tool_result's content — a bare string in the shapes on
// disk, an array of blocks in the API's own form. Both are handled.
func blockText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var b strings.Builder
	for _, blk := range blocks {
		b.WriteString(blk.Text)
	}
	return b.String()
}

type bashCall struct {
	command string
	outcome errShape
	hasRes  bool
}

type fileScan struct {
	calls       []bashCall
	lines       int
	undecodable int
	results     int
	orphans     int // a tool_result naming no tool_use in this file
	nonBashDeny int
}

// scanTranscript reads one .jsonl, collecting every Bash tool_use and joining
// each to its tool_result BY tool_use_id.
func scanTranscript(path string, fs *fileScan) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	type use struct {
		name    string
		command string
	}
	uses := map[string]use{}
	type res struct {
		isError bool
		text    string
	}
	var results []struct {
		id string
		r  res
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), maxLine)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		fs.lines++
		var rec record
		if json.Unmarshal([]byte(line), &rec) != nil {
			fs.undecodable++
			continue
		}
		if rec.Type != "assistant" && rec.Type != "user" {
			continue
		}
		var blocks []contentBlock
		if json.Unmarshal(rec.Message.Content, &blocks) != nil {
			continue // plain-string content: prose, no tool traffic
		}
		for _, b := range blocks {
			switch b.Type {
			case "tool_use":
				if _, seen := uses[b.ID]; seen {
					continue // one assistant message is split across JSONL lines
				}
				var in bashInput
				if len(b.Input) > 0 {
					_ = json.Unmarshal(b.Input, &in)
				}
				uses[b.ID] = use{name: b.Name, command: in.Command}
			case "tool_result":
				results = append(results, struct {
					id string
					r  res
				}{b.ToolUseID, res{b.IsError, blockText(b.Content)}})
			}
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}

	fs.results += len(results)
	answered := map[string]errShape{}
	for _, r := range results {
		u, ok := uses[r.id]
		if !ok {
			fs.orphans++
			continue
		}
		shape := classifyError(r.r.isError, r.r.text)
		if u.name != "Bash" {
			if shape == shapePolicyDenial {
				fs.nonBashDeny++
			}
			continue
		}
		// First answer wins: a repeated result record for one id is the same
		// answer echoed, not a second decision.
		if _, done := answered[r.id]; !done {
			answered[r.id] = shape
		}
	}

	ids := make([]string, 0, len(uses))
	for id := range uses {
		ids = append(ids, id)
	}
	sort.Strings(ids) // determinism: map order must not reach the report
	for _, id := range ids {
		u := uses[id]
		if u.name != "Bash" {
			continue
		}
		shape, ok := answered[id]
		fs.calls = append(fs.calls, bashCall{command: u.command, outcome: shape, hasRes: ok})
	}
	return nil
}

// ---------------------------------------------------------------------------
// Command → (program, subcommand)
// ---------------------------------------------------------------------------

// isCompound is the brief's skip test, deliberately literal: a pipe, a semicolon
// or an &&, anywhere in the raw string, including inside quotes. It therefore
// over-skips (a `git commit -m "a; b"` is dropped) — an over-skip loses evidence,
// while an under-skip would import 0213b's confound into this table, and losing
// evidence is the cheaper error here. The skipped count is reported so the size
// of the loss is visible.
func isCompound(cmd string) bool {
	return strings.ContainsAny(cmd, "|;") || strings.Contains(cmd, "&&")
}

// splitWords splits on unquoted whitespace and strips quoting from each word.
// COPIED VERBATIM from docs/measurements/0213b-compound-commands.go, so the
// measurements tokenise identically rather than two ways that drift.
func splitWords(s string) []string {
	var words []string
	var cur strings.Builder
	started := false
	flush := func() {
		if started {
			words = append(words, cur.String())
			cur.Reset()
			started = false
		}
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			flush()
			i++
		case c == '\\':
			started = true
			if i+1 < len(s) {
				cur.WriteByte(s[i+1])
				i += 2
			} else {
				i++
			}
		case c == '\'':
			started = true
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				cur.WriteString(s[i+1:])
				i = len(s)
			} else {
				cur.WriteString(s[i+1 : i+1+j])
				i = i + 1 + j + 1
			}
		case c == '"':
			started = true
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) {
					cur.WriteByte(s[i+1])
					i += 2
					continue
				}
				cur.WriteByte(s[i])
				i++
			}
			if i < len(s) {
				i++
			}
		default:
			started = true
			cur.WriteByte(c)
			i++
		}
	}
	flush()
	return words
}

// stripNonCommandPrefix removes leading VAR=value assignments and bare sudo/env
// wrappers. COPIED VERBATIM from 0213b.
func stripNonCommandPrefix(raw []string) []string {
	i := 0
	for i < len(raw) {
		if isAssignment(raw[i]) {
			i++
			continue
		}
		if (raw[i] == "sudo" || raw[i] == "env") && i+1 < len(raw) {
			i++
			continue
		}
		break
	}
	return raw[i:]
}

func isAssignment(tok string) bool {
	eq := strings.IndexByte(tok, '=')
	if eq <= 0 {
		return false
	}
	for i := 0; i < eq; i++ {
		c := tok[i]
		ok := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (i > 0 && c >= '0' && c <= '9')
		if !ok {
			return false
		}
	}
	return true
}

// progAndVerb returns the first word (the program) and the second (the candidate
// subcommand). A second word that begins with "-" is a FLAG, not a verb; it is
// returned with flag=true so the verb tables can leave it out while the raw
// tally in §3 still counts it. `git -C /tmp status` is the shape that matters:
// its real verb is the third word, and pretending "-C" is the verb would put a
// fake row in both tables.
func progAndVerb(cmd string) (prog, verb string, flag, ok bool) {
	tokens := splitWords(cmd)
	if stripped := stripNonCommandPrefix(tokens); len(stripped) > 0 {
		tokens = stripped
	}
	if len(tokens) == 0 {
		return "", "", false, false
	}
	prog = tokens[0]
	if len(tokens) == 1 {
		return prog, "", false, true
	}
	verb = tokens[1]
	return prog, verb, strings.HasPrefix(verb, "-"), true
}

// ---------------------------------------------------------------------------
// Tallies
// ---------------------------------------------------------------------------

type cell struct {
	allowed  int
	denied   int
	rejected int // interactive human "no" — never a matcher decision
	noResult int // the call has no tool_result at all (session ended mid-call)
	examples []string
	denials  []string // example DENIED command strings, same lexical rule
	nodes    []string // node addresses that contributed, for §6's citations
}

func (c *cell) add(cmd string, shape errShape, hasRes bool, node string) {
	switch {
	case !hasRes:
		c.noResult++
	case shape == shapePolicyDenial:
		c.denied++
		c.denials = keepSmallest(c.denials, cmd)
	case shape == shapeUserRejection:
		c.rejected++
	default:
		c.allowed++
	}
	c.examples = keepSmallest(c.examples, cmd)
	if node != "" && shape == shapePolicyDenial {
		c.nodes = keepSmallest(c.nodes, node)
	}
}

// keepSmallest maintains the maxExamples lexically smallest DISTINCT strings.
// Lexical, not first-seen, because first-seen depends on directory iteration
// order and would make two runs of this program disagree.
func keepSmallest(cur []string, s string) []string {
	for _, e := range cur {
		if e == s {
			return cur
		}
	}
	cur = append(cur, s)
	sort.Strings(cur)
	if len(cur) > maxExamples {
		cur = cur[:maxExamples]
	}
	return cur
}

type key struct{ prog, verb string }

// scopedKey adds the grant class the node held for that program, so a `git`
// call made under `Bash(git *)` and one made under `Bash(git diff*)` never land
// in the same row.
type scopedKey struct {
	prog  string
	class grantClass
	verb  string
}

func main() {
	home := homeDir()
	projects := filepath.Join(home, ".claude", "projects")

	// ---- declared grants ---------------------------------------------------
	graphDecls, graphFiles, gerr := scanGraphYAML(filepath.Join("graphs"))
	if gerr != nil {
		fmt.Fprintf(os.Stderr, "walk graphs/: %v\n", gerr)
	}
	pluginDecls, pluginFiles, perr := scanPluginFrontmatter(filepath.Join("plugin"))
	if perr != nil {
		fmt.Fprintf(os.Stderr, "walk plugin/: %v\n", perr)
	}
	var settingsDecls []declaration
	var settingsRead, settingsMissing []string
	for _, p := range []string{
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(home, ".claude", "settings.local.json"),
		filepath.Join(".claude", "settings.json"),
		filepath.Join(".claude", "settings.local.json"),
	} {
		d, ok := scanSettings(p)
		if !ok {
			settingsMissing = append(settingsMissing, p)
			continue
		}
		settingsRead = append(settingsRead, p)
		settingsDecls = append(settingsDecls, d...)
	}

	// ---- node scopes -------------------------------------------------------
	rdir := runsDir()
	scopes, rc, sharedSessions, serr := sessionScopes(rdir)
	runDirs, runsWithPolicies := rc.runDirs, rc.withPolicies
	isolatedPolicies, loadedPolicies := rc.isolatedPolicies, rc.loadedPolicies
	if serr != nil {
		fmt.Fprintf(os.Stderr, "cannot read %s: %v\n", rdir, serr)
		os.Exit(1)
	}

	// ---- transcripts -------------------------------------------------------
	all, ferr := filepath.Glob(filepath.Join(projects, "*", "*.jsonl"))
	if ferr != nil {
		fmt.Fprintf(os.Stderr, "glob %s: %v\n", projects, ferr)
		os.Exit(1)
	}
	sort.Strings(all)
	if len(all) == 0 {
		fmt.Printf("CORPUS EMPTY: no transcript matched %s/*/*.jsonl\n", projects)
		fmt.Println("Nothing can be established about the matcher from this machine.")
		os.Exit(1)
	}

	var files, excludedOwn []string
	for _, p := range all {
		if filepath.Base(filepath.Dir(p)) == ownProjectDir {
			excludedOwn = append(excludedOwn, p)
			continue
		}
		files = append(files, p)
	}

	var (
		raw          = map[key]*cell{}       // §3: whole corpus, every session
		scoped       = map[scopedKey]*cell{} // §5/§6: node sessions only
		nodeFiles    int
		nodeCalls    int
		nodeDenials  int
		isoFiles     int
		isoCalls     int
		isoDenials   int
		loadedFiles  int
		loadedCalls  int
		loadedDeny   int
		totalCalls   int
		totalDenials int
		totalReject  int
		totalUnrecog int
		compoundSkip int
		emptyCmd     int
		readErrs     []string
		lines        int
		undecodable  int
		results      int
		orphans      int
		nonBashDeny  int
		ownWasNode   []string
	)
	for _, p := range excludedOwn {
		if _, isNode := scopes[strings.TrimSuffix(filepath.Base(p), ".jsonl")]; isNode {
			ownWasNode = append(ownWasNode, p)
		}
	}

	for _, path := range files {
		var fs fileScan
		if err := scanTranscript(path, &fs); err != nil {
			readErrs = append(readErrs, fmt.Sprintf("%s: %v", path, err))
			// A partially read transcript is a floor, not a negative; it is
			// excluded whole rather than contributing half its calls.
			continue
		}
		lines += fs.lines
		undecodable += fs.undecodable
		results += fs.results
		orphans += fs.orphans
		nonBashDeny += fs.nonBashDeny

		sess := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		scope, isNode := scopes[sess]
		if isNode {
			nodeFiles++
			if scope.Isolated {
				isoFiles++
			} else {
				loadedFiles++
			}
		}
		for _, c := range fs.calls {
			totalCalls++
			if c.outcome == shapePolicyDenial {
				totalDenials++
			}
			if c.outcome == shapeUserRejection {
				totalReject++
			}
			if c.outcome == shapeUnrecognised {
				totalUnrecog++
			}
			if isCompound(c.command) {
				compoundSkip++
				continue
			}
			prog, verb, flag, ok := progAndVerb(c.command)
			if !ok {
				emptyCmd++
				continue
			}
			k := key{prog, verb}
			if raw[k] == nil {
				raw[k] = &cell{}
			}
			raw[k].add(c.command, c.outcome, c.hasRes, "")

			if !isNode || scope.BashByName {
				continue
			}
			nodeCalls++
			denied := c.outcome == shapePolicyDenial
			if denied {
				nodeDenials++
			}
			if !scope.Isolated {
				// The node loaded ~/.claude/settings.json, whose Bash(*) allows
				// everything. Counted, named, and kept OUT of the tables.
				loadedCalls++
				if denied {
					loadedDeny++
				}
				continue
			}
			isoCalls++
			if denied {
				isoDenials++
			}
			if flag {
				continue // a flag is not a verb; see progAndVerb
			}
			class, _ := grantsFor(scope.Grants, prog)
			sk := scopedKey{prog, class, verb}
			if scoped[sk] == nil {
				scoped[sk] = &cell{}
			}
			scoped[sk].add(c.command, c.outcome, c.hasRes, scope.address())
		}
	}

	// ---- report ------------------------------------------------------------
	fmt.Println("MEASUREMENT 0011 — does `Bash(<prog> *)` distinguish subcommands?")
	fmt.Println("run: go run docs/measurements/0011-plugin-entrypoint-grants.go")
	fmt.Println()
	fmt.Println(assumptionText)
	fmt.Println()

	fmt.Println("§1  CORPUS")
	fmt.Printf("  transcripts root:                      %s\n", projects)
	fmt.Printf("  jsonl files matched:                   %d\n", len(all))
	fmt.Printf("  excluded (this lane's own project dir %s): %d\n", ownProjectDir, len(excludedOwn))
	for _, p := range excludedOwn {
		fmt.Printf("      %s\n", p)
	}
	fmt.Printf("  of those, any that WAS a node session: %d\n", len(ownWasNode))
	fmt.Printf("  jsonl files READ:                      %d\n", len(files)-len(readErrs))
	fmt.Printf("  jsonl files unreadable (excluded):     %d\n", len(readErrs))
	for _, e := range readErrs {
		fmt.Printf("      %s\n", e)
	}
	fmt.Printf("  jsonl lines parsed:                    %d\n", lines)
	fmt.Printf("  lines that would not decode as JSON:   %d\n", undecodable)
	fmt.Printf("  tool_result blocks:                    %d\n", results)
	fmt.Printf("  tool_results naming no tool_use:       %d\n", orphans)
	fmt.Println()
	fmt.Printf("  run directories seen:                  %d  (%s)\n", runDirs, rdir)
	fmt.Printf("  runs carrying tool_policies:           %d\n", runsWithPolicies)
	fmt.Printf("  node sessions addressable:             %d\n", len(scopes))
	fmt.Printf("  session ids claimed by two nodes:      %d  (dropped)\n", len(sharedSessions))
	for _, s := range sharedSessions {
		fmt.Printf("      %s\n", s)
	}
	fmt.Println()

	fmt.Println("§2  BASH CALLS")
	fmt.Printf("  Bash tool_use blocks parsed:           %d\n", totalCalls)
	fmt.Printf("    policy-denied:                       %d\n", totalDenials)
	fmt.Printf("    interactive user rejection:          %d  (not a matcher decision)\n", totalReject)
	fmt.Printf("    errored with an unknown wording:     %d\n", totalUnrecog)
	fmt.Printf("  skipped as COMPOUND (| ; &&):          %d  (see 0213b)\n", compoundSkip)
	fmt.Printf("  skipped as empty/untokenisable:        %d\n", emptyCmd)
	fmt.Printf("  policy denials of a NON-Bash tool:     %d  (not counted above)\n", nonBashDeny)
	fmt.Printf("  simple Bash calls in the tables:       %d\n", totalCalls-compoundSkip-emptyCmd)
	fmt.Println()
	fmt.Printf("  of which inside a NODE session:        %d calls in %d transcripts\n", nodeCalls, nodeFiles)
	fmt.Printf("    node-session policy denials:         %d\n", nodeDenials)
	fmt.Println()

	fmt.Println("§3  DECLARED GRANTS (a grant counts as evidence only with an address)")
	fmt.Printf("  graphs/**/*.yaml parsed (yaml.v3 node API): %d files\n", len(graphFiles))
	printDecls(graphDecls)
	fmt.Printf("  settings files read: %v\n", settingsRead)
	fmt.Printf("  settings files absent: %v\n", settingsMissing)
	printDecls(settingsDecls)
	fmt.Println()

	fmt.Println("§4  THE ISOLATION FILTER - which node sessions can testify at all")
	fmt.Println("  runstate.go:130-134 / runner.go:55-60: setting_sources is a *string;")
	fmt.Println("  a pointer to \"\" renders --setting-sources \"\" (this argv is the only")
	fmt.Println("  allow-rule source), and NIL OMITS THE FLAG (the user's settings load as")
	fmt.Println("  usual). omitempty means ABSENT == nil == the settings-level Bash(*) was")
	fmt.Println("  in force, and every allow in such a node is vacuous.")
	fmt.Printf("    node policies ISOLATED (setting_sources == \"\"):  %d\n", isolatedPolicies)
	fmt.Printf("    node policies that LOADED user settings (absent): %d   <-- excluded\n", loadedPolicies)
	fmt.Printf("    isolated node transcripts / calls / denials:      %d / %d / %d\n", isoFiles, isoCalls, isoDenials)
	fmt.Printf("    loaded   node transcripts / calls / denials:      %d / %d / %d   <-- excluded\n", loadedFiles, loadedCalls, loadedDeny)
	if isoDenials > 0 {
		fmt.Println("  The isolated population DOES get denied, so its --allowedTools list was")
		fmt.Println("  really in force. That is the check, not an assumption.")
	} else {
		fmt.Println("  UNDETERMINED: zero denials among isolated nodes; this corpus cannot say.")
	}
	fmt.Println()

	fmt.Println("§4b (control) PROGRAM NEVER GRANTED - does the matcher bind the BINARY?")
	fmt.Println("  Isolated-node calls whose program appears in NO grant the node held. If")
	fmt.Println("  the matcher were not reading the command at all, these would run.")
	printNoneTable(scoped)
	fmt.Println()

	fmt.Println("§4c THE ENTRY POINT THE QUESTION IS ABOUT")
	fmt.Printf("  plugin/**/*.md frontmatter parsed (yaml.v3): %d files\n", len(pluginFiles))
	printDecls(pluginDecls)
	printEntrypointAnswer(pluginDecls, scopes, "oh-my-graph")
	fmt.Println()

	fmt.Println("§5  (b) WIDE GRANT `Bash(<prog> *)` — did MULTIPLE DISTINCT VERBS run?")
	fmt.Println("  One row per program a node held a WIDE grant for. `clean verbs` counts")
	fmt.Println("  distinct second words that ran with ZERO denials under that grant.")
	printClassTable(scoped, classWide)
	fmt.Println()

	fmt.Println("§6  (c) NARROW GRANT `Bash(<prog> <verb> *)` — was a DIFFERENT verb denied?")
	fmt.Println("  One row per program a node held ONLY narrow grants for. A verb the grant")
	fmt.Println("  named should run; a verb it did not name should be denied if — and only")
	fmt.Println("  if — the matcher reads the verb.")
	printNarrowTable(scoped, scopes)
	fmt.Println()

	fmt.Println("§7  RAW WHOLE-CORPUS TALLY (every session, including interactive ones")
	fmt.Println("  under the settings-level Bash(*) — informational, NOT the evidence).")
	fmt.Println("  Programs with at least one denial, or at least 3 distinct verbs:")
	printRawTable(raw)
}

func printDecls(decls []declaration) {
	if len(decls) == 0 {
		fmt.Println("      (none)")
		return
	}
	sort.Slice(decls, func(i, j int) bool {
		if decls[i].Grant != decls[j].Grant {
			return decls[i].Grant < decls[j].Grant
		}
		if decls[i].Path != decls[j].Path {
			return decls[i].Path < decls[j].Path
		}
		return decls[i].Line < decls[j].Line
	})
	// One line per DISTINCT grant, with its first address and how many places
	// declare it — a hundred `Bash(git *)` rows would bury the narrow ones.
	byGrant := map[string][]declaration{}
	var order []string
	for _, d := range decls {
		if _, ok := byGrant[d.Grant]; !ok {
			order = append(order, d.Grant)
		}
		byGrant[d.Grant] = append(byGrant[d.Grant], d)
	}
	for _, g := range order {
		ds := byGrant[g]
		fmt.Printf("      %-34s %d site(s), first at %s\n", g, len(ds), ds[0].address())
	}
}

type row struct {
	prog     string
	verbs    []string
	clean    []string
	denied   []string
	allowedN int
	deniedN  int
	cells    map[string]*cell
}

func collectRows(scoped map[scopedKey]*cell, want grantClass) []row {
	byProg := map[string]*row{}
	for k, c := range scoped {
		if k.class != want && !(want == classWide && k.class == classBoth) {
			continue
		}
		if k.verb == "" {
			continue // a bare `git` with no second word says nothing about verbs
		}
		r := byProg[k.prog]
		if r == nil {
			r = &row{prog: k.prog, cells: map[string]*cell{}}
			byProg[k.prog] = r
		}
		r.verbs = append(r.verbs, k.verb)
		r.cells[k.verb] = c
		r.allowedN += c.allowed
		r.deniedN += c.denied
		if c.denied == 0 && c.allowed > 0 {
			r.clean = append(r.clean, k.verb)
		}
		if c.denied > 0 {
			r.denied = append(r.denied, k.verb)
		}
	}
	var rows []row
	for _, r := range byProg {
		sort.Strings(r.verbs)
		sort.Strings(r.clean)
		sort.Strings(r.denied)
		rows = append(rows, *r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if len(rows[i].clean) != len(rows[j].clean) {
			return len(rows[i].clean) > len(rows[j].clean)
		}
		return rows[i].prog < rows[j].prog
	})
	return rows
}

func printClassTable(scoped map[scopedKey]*cell, want grantClass) {
	rows := collectRows(scoped, want)
	if len(rows) == 0 {
		fmt.Println("      (no node in the corpus held such a grant)")
		return
	}
	fmt.Printf("      %-12s %6s %7s %7s   %s\n", "program", "clean", "denied", "calls", "verbs that ran clean")
	for _, r := range rows {
		fmt.Printf("      %-12s %6d %7d %7d   %s\n",
			r.prog, len(r.clean), len(r.denied), r.allowedN+r.deniedN, strings.Join(r.clean, " "))
		if len(r.denied) > 0 {
			fmt.Printf("      %-12s   DENIED VERBS UNDER A WIDE GRANT: %s\n", "", strings.Join(r.denied, " "))
			for _, v := range r.denied {
				for _, d := range r.cells[v].denials {
					fmt.Printf("      %-12s     %q\n", "", trunc(d, 90))
					fmt.Printf("      %-12s       [%s]  node %s\n", "", hint(d), strings.Join(r.cells[v].nodes, ","))
				}
			}
		}
		for _, v := range r.clean {
			c := r.cells[v]
			fmt.Printf("      %-12s     %-14s %d allowed   e.g. %q\n", "", v, c.allowed, trunc(firstOr(c.examples, ""), 80))
		}
	}
}

func printNarrowTable(scoped map[scopedKey]*cell, scopes map[string]nodeScope) {
	// The verbs some node NAMED in a narrow grant, per program — needed to say
	// whether a denied verb was in or out of the grant.
	granted := map[string]map[string]bool{}
	for _, s := range scopes {
		for _, g := range s.Grants {
			p, v, wide, ok := parseGrant(g)
			if !ok || wide {
				continue
			}
			if granted[p] == nil {
				granted[p] = map[string]bool{}
			}
			granted[p][v] = true
		}
	}
	rows := collectRows(scoped, classNarrow)
	if len(rows) == 0 {
		fmt.Println("      (no node in the corpus held only narrow grants)")
		return
	}
	fmt.Printf("      %-10s %-14s %8s %8s  %s\n", "program", "verb", "allowed", "denied", "named by a narrow grant?")
	for _, r := range rows {
		for _, v := range r.verbs {
			c := r.cells[v]
			named := "NO  <- out of grant"
			if granted[r.prog][v] {
				named = "yes"
			}
			fmt.Printf("      %-10s %-14s %8d %8d  %s\n", r.prog, v, c.allowed, c.denied, named)
			for _, d := range c.denials {
				fmt.Printf("      %-10s   denied: %q\n", "", trunc(d, 96))
				fmt.Printf("      %-10s           [%s]\n", "", hint(d))
			}
			for _, n := range c.nodes {
				fmt.Printf("      %-10s   at node: %s\n", "", n)
			}
		}
	}
}

// printNoneTable is the CONTROL for the whole measurement. It shows what happens
// in an isolated node when the command's PROGRAM appears in no grant at all. If
// these ran, the matcher would not be reading the command and neither table
// below could mean anything; if they are denied, the matcher demonstrably binds
// at least the binary, and the open question is only whether it also binds the
// verb.
// hint names the properties of a command, OTHER than its verb, that this
// repository has already measured as correlating with denial. It is printed
// beside every denial example so that "the verb was not granted" is never the
// only explanation on offer when a cheaper one is sitting in the string:
//
//	redirect   a > or < — 0213b's compound cousin; the target is usually /tmp,
//	           i.e. outside the node's working directory
//	ansi-c     a $'...' quoted word, the shape #204's probe node was testing
//	multiline  an embedded newline inside a quoted argument
//	abs-path   an argument that is an absolute path (see the memory note
//	           bash-denials-are-path-sensitive: the same first word is allowed
//	           inside the working dir and denied outside it)
//
// An empty hint means none of these is present and the verb is the live
// hypothesis. This is a CORRELATION, exactly as 0213b's was; the denial text
// carries no reason code and cannot settle it.
func hint(cmd string) string {
	var h []string
	if strings.ContainsAny(cmd, "><") {
		h = append(h, "redirect")
	}
	if strings.Contains(cmd, "$'") {
		h = append(h, "ansi-c")
	}
	if strings.Contains(cmd, "\n") {
		h = append(h, "multiline")
	}
	for _, tok := range splitWords(cmd)[minInt(1, len(splitWords(cmd))):] {
		if strings.HasPrefix(tok, "/") {
			h = append(h, "abs-path")
			break
		}
	}
	if len(h) == 0 {
		return "no non-verb hint"
	}
	return strings.Join(h, "+")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func printNoneTable(scoped map[scopedKey]*cell) {
	type agg struct {
		prog      string
		allowed   int
		denied    int
		examples  []string
		allowedEx []string
	}
	byProg := map[string]*agg{}
	for k, c := range scoped {
		if k.class != classNone {
			continue
		}
		a := byProg[k.prog]
		if a == nil {
			a = &agg{prog: k.prog}
			byProg[k.prog] = a
		}
		a.allowed += c.allowed
		a.denied += c.denied
		for _, d := range c.denials {
			a.examples = keepSmallest(a.examples, d)
		}
		if c.allowed > 0 {
			for _, e := range c.examples {
				a.allowedEx = keepSmallest(a.allowedEx, e)
			}
		}
	}
	var rows []*agg
	totalAllowed, totalDenied := 0, 0
	for _, a := range byProg {
		totalAllowed += a.allowed
		totalDenied += a.denied
		if a.denied > 0 {
			rows = append(rows, a)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].denied != rows[j].denied {
			return rows[i].denied > rows[j].denied
		}
		return rows[i].prog < rows[j].prog
	})
	fmt.Printf("      ungranted-program calls in isolated nodes: %d denied, %d ALLOWED\n",
		totalDenied, totalAllowed)
	if len(rows) == 0 {
		fmt.Println("      (no such call was denied - the control is empty)")
	}
	shown := 0
	for _, a := range rows {
		fmt.Printf("      DENIED  %-12s %4d   e.g. %q\n",
			a.prog, a.denied, trunc(firstOr(a.examples, ""), 68))
		shown++
		if shown >= 12 {
			fmt.Printf("      ... %d further denied programs not shown\n", len(rows)-shown)
			break
		}
	}
	// The ALLOWED half is the half that matters most, and the first cut of this
	// program printed only the denied one. A call whose program the node never
	// granted, that ran anyway, means the node's --allowedTools list is NOT the
	// only thing deciding: the CLI has some allowance this measurement cannot
	// see from a transcript. Printing only denials would have let the control
	// read as a clean pass when it is not one.
	var allowedRows []*agg
	for _, a := range byProg {
		if a.allowed > 0 {
			allowedRows = append(allowedRows, a)
		}
	}
	sort.Slice(allowedRows, func(i, j int) bool {
		if allowedRows[i].allowed != allowedRows[j].allowed {
			return allowedRows[i].allowed > allowedRows[j].allowed
		}
		return allowedRows[i].prog < allowedRows[j].prog
	})
	for _, a := range allowedRows {
		fmt.Printf("      ALLOWED %-12s %4d   e.g. %q\n",
			a.prog, a.allowed, trunc(firstOr(a.allowedEx, ""), 68))
	}
	if len(allowedRows) > 0 {
		fmt.Println("      ^ these ran with NO grant naming their program. The matcher is")
		fmt.Println("        therefore not a pure rule lookup, and a bare ALLOW is weaker")
		fmt.Println("        evidence than a DENIAL anywhere in this report.")
	}
}

// scanPluginFrontmatter reads the plugin entry points the question is actually
// about. Each is a markdown file whose leading `---` block is YAML, and whose
// `tools:` / `allowed-tools:` key is a COMMA-SEPARATED STRING rather than a
// list — `Bash(oh-my-graph *), Bash(git *), Read, Edit` — so the value is parsed
// by yaml.v3 and then split on commas, never line-matched. The line number the
// parser reports is relative to the frontmatter body, so the offset of the
// opening `---` is added back to give a real file:line.
func scanPluginFrontmatter(root string) ([]declaration, []string, error) {
	var decls []declaration
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.ToLower(filepath.Ext(path)) != ".md" {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		text := string(data)
		if !strings.HasPrefix(text, "---\n") {
			return nil
		}
		end := strings.Index(text[4:], "\n---")
		if end < 0 {
			return nil
		}
		body := text[4 : 4+end]
		var doc yaml.Node
		if yaml.Unmarshal([]byte(body), &doc) != nil {
			return nil
		}
		files = append(files, path)
		// A mapping node's Content alternates key, value; only the values of the
		// two tool keys are read, so a `description:` mentioning a grant string
		// in prose cannot be mistaken for a declaration.
		walkYAML(&doc, func(n *yaml.Node) {
			if n.Kind != yaml.MappingNode {
				return
			}
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				if k.Value != "tools" && k.Value != "allowed-tools" {
					continue
				}
				for _, part := range strings.Split(v.Value, ",") {
					g := strings.TrimSpace(part)
					if strings.HasPrefix(g, "Bash(") && strings.HasSuffix(g, ")") {
						decls = append(decls, declaration{
							Grant: g, Path: path, Line: v.Line + 1, Note: k.Value,
						})
					}
				}
			}
		})
		return nil
	})
	return decls, files, err
}

// printEntrypointAnswer answers the literal question the brief asked, rather
// than leaving a reader to infer it from the tables: is a grant naming
// `oh-my-graph` declared anywhere, and did ANY node session in this corpus ever
// run under one? If the answer to the second is no, every statement about
// `Bash(oh-my-graph *)` in the report above is an INFERENCE from other programs
// and must be labelled as one.
func printEntrypointAnswer(pluginDecls []declaration, scopes map[string]nodeScope, prog string) {
	var declared []declaration
	for _, d := range pluginDecls {
		if p, _, _, ok := parseGrant(d.Grant); ok && p == prog {
			declared = append(declared, d)
		}
	}
	sort.Slice(declared, func(i, j int) bool { return declared[i].address() < declared[j].address() })
	fmt.Printf("      grants naming %q declared in plugin/: %d\n", prog, len(declared))
	for _, d := range declared {
		fmt.Printf("        %-38s %s (%s)\n", d.Grant, d.address(), d.Note)
	}
	held, isolatedHeld := 0, 0
	for _, sc := range scopes {
		for _, g := range sc.Grants {
			if p, _, _, ok := parseGrant(g); ok && p == prog {
				held++
				if sc.Isolated {
					isolatedHeld++
				}
				break
			}
		}
	}
	fmt.Printf("      node sessions in this corpus that HELD such a grant: %d (isolated: %d)\n", held, isolatedHeld)
	if held == 0 {
		fmt.Printf("      => THE CORPUS CONTAINS NO %q GRANT IN FORCE. Everything this report\n", "Bash("+prog+" *)")
		fmt.Println("         says about that grant is INFERENCE from git / go / make / gh, and")
		fmt.Println("         must be labelled `inference` wherever it is quoted.")
	}
}

func printRawTable(raw map[key]*cell) {
	type pr struct {
		prog     string
		verbs    map[string]*cell
		allowedN int
		deniedN  int
	}
	byProg := map[string]*pr{}
	for k, c := range raw {
		p := byProg[k.prog]
		if p == nil {
			p = &pr{prog: k.prog, verbs: map[string]*cell{}}
			byProg[k.prog] = p
		}
		p.verbs[k.verb] = c
		p.allowedN += c.allowed
		p.deniedN += c.denied
	}
	var progs []*pr
	for _, p := range byProg {
		if p.deniedN == 0 && len(p.verbs) < 3 {
			continue
		}
		progs = append(progs, p)
	}
	sort.Slice(progs, func(i, j int) bool {
		if progs[i].deniedN != progs[j].deniedN {
			return progs[i].deniedN > progs[j].deniedN
		}
		if len(progs[i].verbs) != len(progs[j].verbs) {
			return len(progs[i].verbs) > len(progs[j].verbs)
		}
		return progs[i].prog < progs[j].prog
	})
	fmt.Printf("      %-16s %8s %8s %8s\n", "program", "verbs", "allowed", "denied")
	shown := 0
	for _, p := range progs {
		fmt.Printf("      %-16s %8d %8d %8d\n", p.prog, len(p.verbs), p.allowedN, p.deniedN)
		shown++
		if shown >= 30 {
			fmt.Printf("      ... %d further programs not shown\n", len(progs)-shown)
			break
		}
	}
}

// ---------------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------------

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

func uniq(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := in[:1]
	for _, s := range in[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}

func firstOr(in []string, alt string) string {
	if len(in) == 0 {
		return alt
	}
	return in[0]
}

func trunc(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
