//go:build ignore

// Measurement 0011 — does a prefix grant like `Bash(oh-my-graph *)` distinguish
// subcommands, or does it authorise the binary and therefore every subcommand?
//
// The plugin ships `Bash(oh-my-graph run *)` / `Bash(oh-my-graph auto *)` on some
// entry points and a wide `Bash(oh-my-graph *)` on others, and plugin/README.md
// claims the narrow pair grants "nothing outside those command prefixes". THE
// MATCHER THAT DECIDES THIS IS IN THE CLAUDE CODE BINARY, NOT IN THIS
// REPOSITORY. No source here implements it and none can be read. So this program
// does not reason about the rule; it looks for calls that the rule already
// judged, on this machine, and reports what happened to them.
//
// It is a MEASUREMENT, not a lint. It ships no behaviour and nothing in the
// engine calls it. The `ignore` build tag keeps it out of `go build ./...` and
// `go test ./...`; run it explicitly, which does not apply build constraints:
//
//	go run docs/measurements/0011-plugin-entrypoint-grants.go
//
// It takes NO ARGUMENTS, writes NO FILES, and prints plain text to stdout.
//
// WHAT IT REPORTS
//
//	(a) every Bash tool call in the whole local transcript corpus whose command's
//	    first word is `oh-my-graph`: session file, full command, subcommand, and
//	    permitted-or-denied;
//	(b) the analogous prefix-grant case the corpus does contain in volume — Bash
//	    calls whose first word is a program some in-force `Bash(<prog> ...)` grant
//	    names — grouped by grant and by the call's second word, restricted to
//	    sessions whose recorded policy is ISOLATED (see isolated() below), since a
//	    node that loaded the user's own settings ran under the user's wide
//	    `Bash(*)` and proves nothing about any grant the engine passed;
//	(c) totals per subcommand and per verb, and the size of the corpus parsed.
//
// PARSE, NEVER GREP. encoding/json over every JSONL record, standard library
// only, no os/exec. Same scar as #213, #218 and 0213b: a `grep -c` figure in this
// repository reached three documents before anyone noticed it was wrong.
//
// PROVENANCE OF THE COPIED PREDICATES. Nothing below is a third way of doing
// something two earlier measurements already do. Copied verbatim except where a
// deviation is named in a comment on the spot:
//
//	sameFile, inFlight                      docs/measurements/0218-denied-nodes-that-passed.go
//	                                        (also byte-identical in 0213 and 0213b)
//	isDontAskDenial (head+core)             docs/measurements/0218-denied-nodes-that-passed.go
//	classifyError + the four error shapes   docs/measurements/0213b-compound-commands.go
//	blockText, record/contentBlock/bashInput,
//	  the tool_use_id join, indexTranscripts,
//	  splitCommand and its whole splitter,
//	  grantMatches                          docs/measurements/0213b-compound-commands.go
//	nodePolicy / stateFile shape            docs/measurements/0213b-compound-commands.go,
//	                                        plus the setting_sources pointer, whose
//	                                        nil-vs-empty meaning is owned by
//	                                        internal/runstate/runstate.go:130-134
//
// TWO DENIAL PREDICATES ARE EVALUATED, NOT ONE. 0213b anchors on
// "Permission to use " alone and sorts everything else into named buckets; 0218
// additionally requires the dontAsk sentence tail. Both are anchored at offset 0,
// which is the load-bearing part (this repository's own sessions grep for the
// denial sentence, so the words appear inside ordinary Bash stdout). 0213b's is
// the one that decides the counts, because it is the wider of the two and so
// cannot under-count a denial; 0218's is evaluated alongside it and any
// disagreement is printed as a number in §6.
//
// WHAT THIS PROGRAM CANNOT SEE. The denial text carries NO REASON CODE: it is
// byte-identical for an out-of-scope command, a compound command whose later
// piece was ungranted, and a path-sensitive refusal. Every association below is a
// CORRELATION between the shape of a command and what happened to it, never a
// causal reading of the denial. And an ALLOW is weaker evidence than a DENY: this
// corpus contains calls that ran under isolated policies naming no matching grant
// at all (§5 counts them), so the matcher is not a pure lookup over the grant
// list the engine passed.
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
// Constants
// ---------------------------------------------------------------------------

// theProgram is the binary this measurement is really about: the plugin's
// entry point. Part (a) looks for exactly this as a command's first word.
const theProgram = "oh-my-graph"

// The denial wordings. denialPolicyPrefix and userRejectPrefix and
// toolUseErrorPrefix and errorPrefix are 0213b's, verbatim; denialHead and
// denialCore are 0218's, verbatim. See the package comment on why both.
const (
	denialPolicyPrefix = "Permission to use "
	userRejectPrefix   = "The user doesn't want to proceed with this tool use."
	toolUseErrorPrefix = "<tool_use_error>"
	errorPrefix        = "Error: "

	denialHead = "Permission to use "
	denialCore = " has been denied because Claude Code is running in don't ask mode."
)

// The verdict vocabulary internal/runstate declares. The empty string is a
// non-terminal record (ADR 0010's feedback marker), not a failure.
const verdictFail = "FAIL"

// maxLine is 0218's. A transcript line holding a large tool result can be
// megabytes; bufio's 64 KiB default reports ErrTooLong on records that are
// perfectly well formed, and a silently dropped record is a silently missed
// denial.
const maxLine = 64 << 20

// ---------------------------------------------------------------------------
// A. run state: which session ran under which declared policy
// ---------------------------------------------------------------------------

// nodePolicy is state.json's `tool_policies.<id>`: the EFFECTIVE argv the engine
// handed the CLI. DESIGN.md records it as the more durable record than
// graph.json — a grant can exist here and be invisible there — and it is the one
// this measurement uses, because the question is about what was IN FORCE.
//
// SettingSources is the addition to 0213b's copy of this struct, and it is a
// POINTER on purpose: internal/runstate/runstate.go:130-134 declares it
// `omitempty`, so an ABSENT key means the engine passed no --setting-sources at
// all and the CLI loaded the user's own settings (including the user's wide
// Bash(*)), while a key present as "" means "load none" — the isolation switch.
// Decoding it into a plain string would collapse those two into each other and
// would silently admit 92 unisolated policies into the evidence.
type nodePolicy struct {
	AllowedTools    []string `json:"allowed_tools"`
	DisallowedTools []string `json:"disallowed_tools"`
	Tools           []string `json:"tools"`
	SettingSources  *string  `json:"setting_sources"`
}

// isolated reports whether this policy is the only permission surface the node
// had. See SettingSources above: nil means the user's settings were loaded on
// top, so nothing that node was allowed to do can be attributed to the grants
// the engine passed.
func (p nodePolicy) isolated() bool { return p.SettingSources != nil }

type stateFile struct {
	RunID           string                `json:"run_id"`
	GraphSourcePath string                `json:"graph_source_path"`
	ToolPolicies    map[string]nodePolicy `json:"tool_policies"`
	Graph           struct {
		Name  string `json:"name"`
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
	} `json:"graph"`
	Nodes map[string]struct {
		Verdict   string `json:"verdict"`
		SessionID string `json:"session_id"`
	} `json:"nodes"`
}

// sameFile is the PLANNED test. COPIED UNCHANGED from
// docs/measurements/0218-denied-nodes-that-passed.go:152-170, where it is
// byte-identical with 0213 and 0213b, so the four measurements share one corpus
// definition rather than four that drift. It does NOT decide this measurement's
// corpus — a hand-written run's tool_policies is just as much a record of what
// was in force as a planned run's — but the planned/hand-written split is
// reported so this corpus can be reconciled against the earlier three.
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

// inFlight is #218's in-flight test, COPIED from
// docs/measurements/0218-denied-nodes-that-passed.go:256-272 (shape adapted to
// the id/verdict maps this file already has, exactly as 0213b:873-890 adapted
// it). It matters here for the same reason it mattered there: this program may
// itself be a node of a run that is still executing, and that run's state.json
// is a photograph taken mid-exposure. An in-flight run's policies are excluded
// from the session index and NAMED in the report.
//
// THE TEST: at least one graph node has no entry in state.nodes, AND no record
// carries FAIL. Under `on_fail: halt` a run that stopped early always leaves a
// FAIL behind; a run still executing has not. "Has a node with no record" alone
// is NOT the test — it would wrongly drop legitimately halted runs. An EMPTY
// verdict is not a FAIL and must not be read as one.
func inFlight(graphNodeIDs []string, recorded map[string]string) (bool, []string) {
	var noRecord []string
	for _, id := range graphNodeIDs {
		if _, ok := recorded[id]; !ok {
			noRecord = append(noRecord, id)
		}
	}
	if len(noRecord) == 0 {
		return false, nil
	}
	for _, verdict := range recorded {
		if verdict == verdictFail {
			return false, nil // halted on a failure, not still running
		}
	}
	sort.Strings(noRecord)
	return true, noRecord
}

// sessionPolicy is one session's declared permission surface, joined from the
// run that spawned it.
type sessionPolicy struct {
	runID   string
	nodeID  string
	planned bool
	policy  nodePolicy
}

// ---------------------------------------------------------------------------
// B. grants
// ---------------------------------------------------------------------------

// grantMatches reports whether a single declared grant covers a command given as
// its token list. COPIED VERBATIM from
// docs/measurements/0213b-compound-commands.go:273-318, including its stated
// assumption — which is NOT authoritative, because the real matcher lives in the
// Claude Code binary. Here it is used only to LABEL a call ("the grant's own
// prefix does or does not reach this call"); the corpus, not this function,
// says what the matcher then did.
func grantMatches(grant string, tokens []string) bool {
	if len(tokens) == 0 {
		return false
	}
	grant = strings.TrimSpace(grant)

	// A bare "Bash" with no scope matches everything.
	if grant == "Bash" {
		return true
	}
	if !strings.HasPrefix(grant, "Bash(") || !strings.HasSuffix(grant, ")") {
		return false // Read, Grep, Glob, Skill, ... never cover a shell word.
	}
	pattern := strings.TrimSpace(grant[len("Bash(") : len(grant)-1])
	if pattern == "" {
		return false
	}
	if pattern == "*" {
		return true
	}

	if strings.HasSuffix(pattern, " *") {
		prefix := strings.Fields(strings.TrimSuffix(pattern, " *"))
		if len(prefix) == 0 || len(prefix) > len(tokens) {
			return false
		}
		for i, want := range prefix {
			if tokens[i] != want {
				return false
			}
		}
		return true
	}

	// No wildcard: exact whole-command match on tokens.
	want := strings.Fields(pattern)
	if len(want) != len(tokens) {
		return false
	}
	for i := range want {
		if want[i] != tokens[i] {
			return false
		}
	}
	return true
}

// bashPattern returns the text inside `Bash(...)`, or ok=false for a non-Bash
// grant and for the bare `Bash`.
func bashPattern(grant string) (string, bool) {
	g := strings.TrimSpace(grant)
	if !strings.HasPrefix(g, "Bash(") || !strings.HasSuffix(g, ")") {
		return "", false
	}
	return strings.TrimSpace(g[len("Bash(") : len(g)-1]), true
}

// grantPrefixTokens returns the token prefix a `Bash(a b *)` grant authorises —
// here ["a","b"]. For a wildcard-free `Bash(ls)` it returns the whole command's
// tokens, which is the exact-match case. ok=false for the wide forms (`Bash`,
// `Bash(*)`) and for every non-Bash grant, because neither names a program.
//
// DEVIATION, NAMED: 0213's grantPrefix (0213-tool-grant-predicate.go:198-203)
// also strips a trailing ":" from the `Bash(git status:*)` colon spelling; the
// verbatim grantMatches above does not recognise that spelling at all. THE
// COLON FORM DOES NOT OCCUR IN THIS CORPUS — the report prints every distinct
// grant string it saw, so a reader can check that rather than take it on trust,
// and unrecognised pattern shapes are counted in §6 rather than dropped.
func grantPrefixTokens(grant string) ([]string, bool) {
	pattern, ok := bashPattern(grant)
	if !ok || pattern == "" || pattern == "*" {
		return nil, false
	}
	if strings.HasSuffix(pattern, " *") {
		return strings.Fields(strings.TrimSuffix(pattern, " *")), true
	}
	f := strings.Fields(pattern)
	if len(f) == 0 {
		return nil, false
	}
	return f, true
}

// isWideBash reports the two grants that authorise every command outright. A
// session holding one of these can say nothing about any prefix grant it also
// holds, so §4's table excludes such sessions and counts them separately.
func isWideBash(grant string) bool {
	g := strings.TrimSpace(grant)
	if g == "Bash" {
		return true
	}
	p, ok := bashPattern(g)
	return ok && p == "*"
}

// unrecognisedPattern reports a Bash grant whose shape neither grantMatches nor
// grantPrefixTokens models: not wide, not `... *`, and not a plain exact
// command. It is COUNTED rather than assumed away — an unmodelled shape is a
// silent misclassification otherwise.
func unrecognisedPattern(grant string) bool {
	p, ok := bashPattern(grant)
	if !ok {
		return false
	}
	if p == "" || p == "*" || strings.HasSuffix(p, " *") {
		return false
	}
	// A wildcard anywhere else, or the colon spelling, is a shape this program
	// does not model.
	return strings.Contains(p, "*") || strings.Contains(p, ":")
}

// ---------------------------------------------------------------------------
// C. the splitter — COPIED VERBATIM from 0213b (lines 334-776)
// ---------------------------------------------------------------------------
//
// It is copied whole rather than trimmed because its heredoc handling is
// load-bearing: 0213b recorded that without it a commit message or a Go file
// written via <<EOF is shredded into fake sub-commands, and one of those fake
// pieces starting with `git` would land straight in this measurement's verb
// table. Only the first sub-command's tokens are used below (the whole command's
// own first word), plus the count of sub-commands as the compound flag.

type subcommand struct {
	text   string   // the sub-command's own text, substitutions removed
	tokens []string // unquoted tokens; tokens[0] is the first word
	depth  int      // 0 = top level; >0 = inside a command substitution
}

func (s subcommand) firstWord() string {
	if len(s.tokens) == 0 {
		return ""
	}
	return s.tokens[0]
}

// secondWord is this file's own addition: the token after the first word, which
// for `git commit -m x` is the verb. It is the LITERAL second word, as briefed.
// A call whose second word begins with "-" is a global flag rather than a verb
// (`git -C dir status`); those are counted separately in §6 instead of being
// guessed at, because skipping a flag would then take the flag's ARGUMENT for
// the verb and invent a subcommand named after a directory.
func (s subcommand) secondWord() string {
	if len(s.tokens) < 2 {
		return ""
	}
	return s.tokens[1]
}

var leadingGrammar = map[string]bool{
	"do": true, "then": true, "else": true, "elif": true,
	"if": true, "while": true, "until": true,
	"!": true, "time": true, "{": true, "(": true,
}

var wholeGrammar = map[string]bool{
	"done": true, "fi": true, "esac": true, "}": true, ")": true, ";;": true,
	"for": true, "case": true, "select": true, "in": true,
}

type splitResult struct {
	subs         []subcommand
	heredocs     int
	grammarDrops int
}

func splitCommand(cmd string) splitResult {
	var r splitResult
	scan(cmd, 0, &r)
	return r
}

type heredoc struct {
	delim     string
	stripTabs bool
}

func scan(s string, depth int, r *splitResult) {
	var cur strings.Builder
	var pending []heredoc

	flush := func() {
		t := strings.TrimSpace(cur.String())
		cur.Reset()
		if t == "" {
			return
		}
		tokens := tokenize(t)
		for len(tokens) > 0 && leadingGrammar[tokens[0]] {
			tokens = tokens[1:]
		}
		if len(tokens) == 0 || wholeGrammar[tokens[0]] {
			r.grammarDrops++
			return
		}
		r.subs = append(r.subs, subcommand{text: t, tokens: tokens, depth: depth})
	}

	consumeHeredocs := func(from int) int {
		k := from
		for _, hd := range pending {
			for k < len(s) {
				eol := strings.IndexByte(s[k:], '\n')
				var lineStr string
				var next int
				if eol < 0 {
					lineStr, next = s[k:], len(s)
				} else {
					lineStr, next = s[k:k+eol], k+eol+1
				}
				cmp := lineStr
				if hd.stripTabs {
					cmp = strings.TrimLeft(cmp, "\t")
				}
				k = next
				if cmp == hd.delim {
					break
				}
			}
			r.heredocs++
		}
		pending = nil
		return k
	}

	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\\':
			cur.WriteByte(c)
			if i+1 < len(s) {
				cur.WriteByte(s[i+1])
				i += 2
			} else {
				i++
			}

		case c == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				cur.WriteString(s[i:])
				i = len(s)
			} else {
				cur.WriteString(s[i : i+1+j+1])
				i = i + 1 + j + 1
			}

		case c == '"':
			i = scanDoubleQuoted(s, i, depth, r, &cur)

		case c == '`':
			end := indexUnescaped(s, i+1, '`')
			if end < 0 {
				cur.WriteString(s[i:])
				i = len(s)
			} else {
				scan(s[i+1:end], depth+1, r)
				i = end + 1
			}

		case c == '$' && i+1 < len(s) && s[i+1] == '(':
			end := matchParen(s, i+1)
			if end < 0 {
				cur.WriteString(s[i:])
				i = len(s)
			} else {
				scan(s[i+2:end], depth+1, r)
				i = end + 1
			}

		case c == '<' && i+1 < len(s) && s[i+1] == '<':
			if hd, next, ok := parseHeredocOpener(s, i); ok {
				pending = append(pending, hd)
				cur.WriteString(s[i:next])
				i = next
			} else {
				cur.WriteByte(c)
				i++
			}

		case c == '|':
			flush()
			if i+1 < len(s) && s[i+1] == '|' {
				i += 2
			} else {
				i++
			}

		case c == '&':
			if i+1 < len(s) && s[i+1] == '&' {
				flush()
				i += 2
			} else {
				cur.WriteByte(c)
				i++
			}

		case c == ';':
			flush()
			i++

		case c == '\n':
			if len(pending) > 0 {
				next := consumeHeredocs(i + 1)
				flush()
				i = next
			} else {
				flush()
				i++
			}

		default:
			cur.WriteByte(c)
			i++
		}
	}
	flush()
}

func parseHeredocOpener(s string, i int) (heredoc, int, bool) {
	j := i + 2
	if j < len(s) && s[j] == '<' {
		return heredoc{}, i, false // <<< here-string
	}
	hd := heredoc{}
	if j < len(s) && s[j] == '-' {
		hd.stripTabs = true
		j++
	}
	for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
		j++
	}
	if j >= len(s) {
		return heredoc{}, i, false
	}
	var b strings.Builder
	switch s[j] {
	case '\'', '"':
		q := s[j]
		k := strings.IndexByte(s[j+1:], q)
		if k < 0 {
			return heredoc{}, i, false
		}
		b.WriteString(s[j+1 : j+1+k])
		j = j + 1 + k + 1
	default:
		for j < len(s) && !strings.ContainsRune(" \t\n|;&<>()", rune(s[j])) {
			b.WriteByte(s[j])
			j++
		}
	}
	if b.Len() == 0 {
		return heredoc{}, i, false
	}
	hd.delim = b.String()
	return hd, j, true
}

func scanDoubleQuoted(s string, i, depth int, r *splitResult, cur *strings.Builder) int {
	cur.WriteByte(s[i]) // opening quote
	i++
	for i < len(s) {
		c := s[i]
		switch {
		case c == '\\':
			cur.WriteByte(c)
			if i+1 < len(s) {
				cur.WriteByte(s[i+1])
				i += 2
			} else {
				i++
			}
		case c == '"':
			cur.WriteByte(c)
			return i + 1
		case c == '`':
			end := indexUnescaped(s, i+1, '`')
			if end < 0 {
				cur.WriteString(s[i:])
				return len(s)
			}
			scan(s[i+1:end], depth+1, r)
			i = end + 1
		case c == '$' && i+1 < len(s) && s[i+1] == '(':
			end := matchParen(s, i+1)
			if end < 0 {
				cur.WriteString(s[i:])
				return len(s)
			}
			scan(s[i+2:end], depth+1, r)
			i = end + 1
		default:
			cur.WriteByte(c)
			i++
		}
	}
	return i
}

func matchParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); {
		switch s[i] {
		case '\\':
			i += 2
			continue
		case '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return -1
			}
			i = i + 1 + j + 1
			continue
		case '"':
			j := indexUnescaped(s, i+1, '"')
			if j < 0 {
				return -1
			}
			i = j + 1
			continue
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
		i++
	}
	return -1
}

func indexUnescaped(s string, from int, want byte) int {
	for i := from; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == want {
			return i
		}
	}
	return -1
}

func tokenize(s string) []string {
	raw := splitWords(s)
	stripped := stripNonCommandPrefix(raw)
	if len(stripped) == 0 {
		return raw
	}
	return stripped
}

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
				i++ // closing quote
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

// ---------------------------------------------------------------------------
// D. transcripts — COPIED from 0213b, with 0218's bufio scanner
// ---------------------------------------------------------------------------

type record struct {
	Type        string `json:"type"`
	IsSidechain bool   `json:"isSidechain"`
	CWD         string `json:"cwd"`
	Message     struct {
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

type toolUse struct {
	id        string
	name      string
	command   string
	cwd       string
	sidechain bool
	seq       int
}

type toolResult struct {
	toolUseID string
	isError   bool
	text      string
	sidechain bool
	seq       int
}

// blockText renders a tool_result's content. Verbatim from 0213b:939-959.
func blockText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var b strings.Builder
		for _, blk := range blocks {
			b.WriteString(blk.Text)
		}
		return b.String()
	}
	return ""
}

// errShape and classifyError are verbatim from 0213b:966-1006. is_error alone is
// NOT denial: an ordinary tool failure is wrapped in <tool_use_error>, and a
// permission denial is unwrapped and opens with a known template.
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

func classifyError(r toolResult) errShape {
	if !r.isError {
		return shapeNotError
	}
	t := strings.TrimPrefix(strings.TrimSpace(r.text), errorPrefix)
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

// isDontAskDenial is 0218's stricter predicate, verbatim from
// 0218-denied-nodes-that-passed.go:353-362. It is the cross-check, not the
// decider; see the package comment.
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

type parsedTranscript struct {
	uses    map[string]toolUse
	results []toolResult
	lines   int
	bad     int
}

// parseTranscript is 0213b's parser with 0218's bufio.Scanner substituted for
// ReadFile+Split: this corpus is ~1 GiB across ~2700 files and holding a whole
// transcript plus its split in memory is needless. maxLine keeps the oversized
// records that ErrTooLong would have dropped.
func parseTranscript(path string) (*parsedTranscript, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	pt := &parsedTranscript{uses: map[string]toolUse{}}
	seq := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), maxLine)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		pt.lines++
		var rec record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			pt.bad++
			continue
		}
		// Not every line is a message: attachment / last-prompt / ai-title /
		// queue-operation records are interleaved and carry no content array.
		if rec.Type != "assistant" && rec.Type != "user" {
			continue
		}
		var blocks []contentBlock
		if err := json.Unmarshal(rec.Message.Content, &blocks); err != nil {
			continue // a plain-string content: ordinary prose, no tool traffic.
		}
		for _, b := range blocks {
			seq++
			switch b.Type {
			case "tool_use":
				var in bashInput
				if len(b.Input) > 0 {
					_ = json.Unmarshal(b.Input, &in)
				}
				// Deduplicate by id: one assistant message is split across
				// several JSONL lines and a block can therefore recur.
				if _, seen := pt.uses[b.ID]; !seen {
					pt.uses[b.ID] = toolUse{
						id: b.ID, name: b.Name, command: in.Command,
						cwd: rec.CWD, sidechain: rec.IsSidechain, seq: seq,
					}
				}
			case "tool_result":
				pt.results = append(pt.results, toolResult{
					toolUseID: b.ToolUseID, isError: b.IsError,
					text: blockText(b.Content), sidechain: rec.IsSidechain, seq: seq,
				})
			}
		}
	}
	if err := sc.Err(); err != nil {
		return pt, err
	}
	return pt, nil
}

// listTranscripts walks ~/.claude/projects once. Verbatim in spirit from
// 0213b's indexTranscripts:1769-1785, returning the paths themselves because
// this measurement scans EVERY transcript rather than looking sessions up by id.
// Only *.jsonl files count: a tool-results/<id>.txt sidecar holding a spilled
// oversized result is NOT a transcript, and one such file alone carries 324
// copies of the denial sentence.
func listTranscripts(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: skip, do not abort the walk
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		out = append(out, p)
		return nil
	})
	sort.Strings(out)
	return out, err
}

// ---------------------------------------------------------------------------
// E. the second confound: does the command reach outside the node's cwd
// ---------------------------------------------------------------------------
//
// COPIED VERBATIM from docs/measurements/0213b-compound-commands.go:1074-1154.
// 0213b established that something path-sensitive is operating in ADDITION to
// the pattern match, so a denial of a command the grant plainly covers is not
// evidence that the grant distinguished a verb until this factor is separated
// out. `within` compares PATH SEGMENTS, not string prefixes — a prefix test on
// ".." reported the whole Go package wildcard `./...` as escaping, first time
// round.

type pathScope int

const (
	scopeNoPathArgs pathScope = iota
	scopeAllInside
	scopeReachesOutside
)

func (p pathScope) String() string {
	switch p {
	case scopeAllInside:
		return "all-paths-inside-cwd"
	case scopeReachesOutside:
		return "reaches-outside-cwd"
	default:
		return "no-path-args"
	}
}

func classifyPathScope(subs []subcommand, cwd, home string) pathScope {
	saw := false
	for _, sc := range subs {
		for _, tok := range sc.tokens[minInt(1, len(sc.tokens)):] {
			p, ok := pathArg(tok, cwd, home)
			if !ok {
				continue
			}
			saw = true
			if !within(p, cwd) {
				return scopeReachesOutside
			}
		}
	}
	if saw {
		return scopeAllInside
	}
	return scopeNoPathArgs
}

func pathArg(tok, cwd, home string) (string, bool) {
	switch {
	case strings.HasPrefix(tok, "~/") || tok == "~":
		return filepath.Join(home, strings.TrimPrefix(tok, "~")), true
	case strings.HasPrefix(tok, "/"):
		return filepath.Clean(tok), true
	case strings.HasPrefix(tok, "./") || strings.HasPrefix(tok, "../"):
		if cwd == "" {
			return "", false
		}
		return filepath.Clean(filepath.Join(cwd, tok)), true
	}
	return "", false
}

func within(p, dir string) bool {
	if dir == "" {
		return false
	}
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// F. one measured call
// ---------------------------------------------------------------------------

type outcome string

const (
	outRan        outcome = "PERMITTED"     // it ran; it may have failed on its own merits
	outDenied     outcome = "DENIED"        // policy denial
	outUserReject outcome = "USER-REJECTED" // interactive human "no"
	outUnknownErr outcome = "ERROR-UNKNOWN" // an error wording this program does not know
	outNoResult   outcome = "NO-RESULT"     // tool_use with no tool_result naming it
)

type call struct {
	transcript string
	sessionID  string
	command    string
	first      string // first word of the WHOLE command
	second     string // the token after it — for `git commit -m x`, the verb
	compound   bool
	sidechain  bool
	scope      pathScope
	out        outcome
	strictAlso bool // 0218's stricter predicate agreed
	pol        *sessionPolicy
}

// unexplained is the only kind of denial that could be the grant distinguishing
// a subcommand. The other two shapes have their own measured explanations in
// this repository, and BOTH must be excluded before a denial counts as evidence
// here:
//
//	compound  — docs/measurements/0213b-compound-commands.md: a later
//	            sub-command the grant never named denies the whole call;
//	reaches-outside-cwd — 0213b §9 and the same file's path cross-tab: something
//	            path-sensitive is operating in addition to the pattern.
//
// A denial that is neither is unexplained by anything already measured, and
// those calls are printed in full in §4 rather than summarised.
func (c call) unexplained() bool {
	return c.out == outDenied && !c.compound && c.scope != scopeReachesOutside
}

func (c call) policyLabel() string {
	switch {
	case c.pol == nil:
		return "no run record"
	case c.pol.policy.isolated():
		return "isolated"
	default:
		return "UNISOLATED (user settings loaded)"
	}
}

// wordKind separates the three things a "second word" can be, because grouping
// `ls` calls by their second word otherwise produces one row per directory and
// buries the rows that are actually subcommands.
type wordKind int

const (
	kindVerb wordKind = iota // a bare word: `commit`, `build`, `fmt-check`
	kindFlag                 // starts with "-"
	kindPath                 // contains a "/", or is a redirect/other punctuation
	kindNone                 // the command had no second word
)

func (k wordKind) String() string {
	switch k {
	case kindVerb:
		return "verb"
	case kindFlag:
		return "flag"
	case kindPath:
		return "path/other"
	default:
		return "none"
	}
}

func classifyWord(w string) wordKind {
	switch {
	case w == "":
		return kindNone
	case strings.HasPrefix(w, "-"):
		return kindFlag
	case strings.ContainsAny(w, "/><|$"):
		return kindPath
	default:
		return kindVerb
	}
}

// ---------------------------------------------------------------------------
// G. main
// ---------------------------------------------------------------------------

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot resolve home dir:", err)
		os.Exit(1)
	}
	runsRoot := filepath.Join(home, ".oh-my-graph", "runs")
	if v := os.Getenv("OMG_HOME"); v != "" {
		runsRoot = filepath.Join(v, "runs")
	}
	projectsRoot := filepath.Join(home, ".claude", "projects")

	// ---- 1. session -> declared policy ------------------------------------
	sessions := map[string]sessionPolicy{}
	var (
		runDirs, runsNoState, runsBadState int
		plannedRuns, unplannedRuns         int
		inFlightRuns                       []string
		dupSessions                        []string
		policiesSeen, policiesIsolated     int
		policiesInFlight                   int
		nodesNoSession                     int
		grantStrings                       = map[string]int{}
		unrecognisedGrants                 = map[string]int{}
	)

	entries, rerr := os.ReadDir(runsRoot)
	if rerr != nil {
		fmt.Printf("NOTE: runs root %s could not be read (%v) — every session below is\n"+
			"      \"no run record\" and §3 is empty.\n\n", runsRoot, rerr)
	}
	var runIDs []string
	for _, e := range entries {
		if e.IsDir() {
			runIDs = append(runIDs, e.Name())
		}
	}
	sort.Strings(runIDs)
	runDirs = len(runIDs)

	for _, runID := range runIDs {
		runDir := filepath.Join(runsRoot, runID)
		raw, err := os.ReadFile(filepath.Join(runDir, "state.json"))
		if err != nil {
			runsNoState++
			continue
		}
		var st stateFile
		if err := json.Unmarshal(raw, &st); err != nil {
			runsBadState++
			continue
		}

		graphPath := filepath.Join(runDir, "graph.json")
		planned := st.GraphSourcePath != "" && sameFile(st.GraphSourcePath, graphPath)
		if planned {
			plannedRuns++
		} else {
			unplannedRuns++
		}

		var graphIDs []string
		for _, n := range st.Graph.Nodes {
			graphIDs = append(graphIDs, n.ID)
		}
		sort.Strings(graphIDs)
		recorded := map[string]string{}
		for id, rec := range st.Nodes {
			recorded[id] = rec.Verdict
		}
		// The policy tally is taken from tool_policies ITSELF, not from the node
		// records, and BEFORE the in-flight exclusion below. A tool_policies
		// entry exists for every node the planner scoped, including nodes that
		// never got a state.nodes record because the run halted or is still
		// running; counting policies per node record silently dropped 17 of them
		// and put this section's isolated count 6 below the figure the earlier
		// 0011 (commit 4e6c6fc, branch lane-b3) reported for a SMALLER corpus,
		// which is how the miscount surfaced. The in-flight runs' policies are
		// counted apart so §3's population, which does exclude them, stays
		// reconcilable with this total.
		var polIDs []string
		for id := range st.ToolPolicies {
			polIDs = append(polIDs, id)
		}
		sort.Strings(polIDs)
		running, noRecord := inFlight(graphIDs, recorded)
		for _, id := range polIDs {
			pol := st.ToolPolicies[id]
			policiesSeen++
			if pol.isolated() {
				policiesIsolated++
			}
			if running {
				policiesInFlight++
			}
			for _, g := range pol.AllowedTools {
				grantStrings[g]++
				if unrecognisedPattern(g) {
					unrecognisedGrants[g]++
				}
			}
		}

		if running {
			// Including a run that is still executing — above all, the one this
			// program may itself be a node of — reads a half-written state.json
			// and makes the corpus measure itself. Excluded and NAMED.
			inFlightRuns = append(inFlightRuns, fmt.Sprintf("%s | %d graph nodes, %d recorded | no record: %s",
				runID, len(graphIDs), len(st.Nodes), strings.Join(noRecord, " ")))
			continue
		}
		_ = noRecord

		var nodeIDs []string
		for id := range st.Nodes {
			nodeIDs = append(nodeIDs, id)
		}
		sort.Strings(nodeIDs)
		for _, nodeID := range nodeIDs {
			rec := st.Nodes[nodeID]
			pol := st.ToolPolicies[nodeID]
			if rec.SessionID == "" {
				nodesNoSession++
				continue
			}
			if prev, dup := sessions[rec.SessionID]; dup {
				// A session claimed twice: `handoff: session` resumes a
				// transcript, so two nodes under two policies share one file and
				// neither policy can be attributed to a given call. First
				// claimant wins and the collision is reported, exactly as
				// 0213b:1523-1528 does.
				dupSessions = append(dupSessions, fmt.Sprintf("%s: %s/%s also claimed by %s/%s",
					rec.SessionID, runID, nodeID, prev.runID, prev.nodeID))
				continue
			}
			sessions[rec.SessionID] = sessionPolicy{
				runID: runID, nodeID: nodeID, planned: planned, policy: pol,
			}
		}
	}

	// ---- 2. every Bash call in every transcript ---------------------------
	paths, werr := listTranscripts(projectsRoot)
	if werr != nil {
		fmt.Printf("NOTE: the transcript walk reported %v — some subtree was skipped.\n\n", werr)
	}

	var (
		filesParsed, filesUnreadable int
		linesParsed, badLines        int
		toolUses, bashUses           int
		resultsSeen, orphanResults   int
		strictDisagreements          int
		ohmyHead                     []call // (a): the WHOLE command's first word
		ohmyLater                    []call // the same word, but as a later sub-command
		ohmyPathForm                 []call // ./bin/oh-my-graph — the same binary, another spelling
		granted                      []call // (b): eligible, program named by an in-force grant
		unnamedProgram               = map[string]int{}
		unnamedDenied                = map[string]int{}
		eligibleCalls                int
		wideBashSkipped              int
		unisolatedSkipped            int
		noPolicySkipped              int
		sidechainSkipped             int
		unreadable                   []string
	)

	for _, p := range paths {
		pt, err := parseTranscript(p)
		if pt != nil {
			linesParsed += pt.lines
			badLines += pt.bad
		}
		if err != nil {
			filesUnreadable++
			if len(unreadable) < 20 {
				unreadable = append(unreadable, p+": "+err.Error())
			}
			// A partially read transcript is a FLOOR, not a negative. Its calls
			// so far are kept; the file is named in §5.
		}
		if pt == nil {
			continue
		}
		filesParsed++

		sid := strings.TrimSuffix(filepath.Base(p), ".jsonl")
		var polp *sessionPolicy
		if sp, ok := sessions[sid]; ok {
			cp := sp
			polp = &cp
		}

		// tool_use_id -> result. JOIN BY ID, NEVER BY ADJACENCY: this CLI splits
		// one assistant message across several JSONL lines and interleaves other
		// records between a call and its result.
		byID := map[string]toolResult{}
		sort.Slice(pt.results, func(i, j int) bool { return pt.results[i].seq < pt.results[j].seq })
		for _, r := range pt.results {
			resultsSeen++
			if _, ok := pt.uses[r.toolUseID]; !ok {
				orphanResults++
				continue
			}
			if _, seen := byID[r.toolUseID]; !seen {
				byID[r.toolUseID] = r
			}
		}

		var ids []string
		for id := range pt.uses {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			u := pt.uses[id]
			toolUses++
			if u.name != "Bash" || strings.TrimSpace(u.command) == "" {
				continue
			}
			bashUses++

			split := splitCommand(u.command)
			if len(split.subs) == 0 {
				continue
			}
			head := split.subs[0]

			res, hasRes := byID[id]
			out := outRan
			strictAlso := false
			if !hasRes {
				out = outNoResult
			} else {
				switch classifyError(res) {
				case shapePolicyDenial:
					out = outDenied
					_, strictAlso = isDontAskDenial(strings.TrimPrefix(strings.TrimSpace(res.text), errorPrefix))
					if !strictAlso {
						strictDisagreements++
					}
				case shapeUserRejection:
					out = outUserReject
				case shapeUnrecognised:
					out = outUnknownErr
				}
			}

			c := call{
				transcript: p, sessionID: sid, command: u.command,
				first: head.firstWord(), second: head.secondWord(),
				compound: len(split.subs) > 1, sidechain: u.sidechain || res.sidechain,
				scope: classifyPathScope(split.subs, u.cwd, home),
				out:   out, strictAlso: strictAlso, pol: polp,
			}

			// ---- (a) three populations, kept apart on purpose ---------------
			//
			// Only the FIRST is what the brief asks for, and only the first is
			// what a `Bash(oh-my-graph *)` token prefix could ever match. The
			// other two are reported because leaving them out would let a reader
			// think the corpus holds no such invocation at all, and folding them
			// in would count invocations the grant could not have covered.
			switch {
			case head.firstWord() == theProgram:
				ohmyHead = append(ohmyHead, c)
			case filepath.Base(head.firstWord()) == theProgram:
				ohmyPathForm = append(ohmyPathForm, c)
			default:
				for _, sc := range split.subs[1:] {
					if sc.firstWord() == theProgram || filepath.Base(sc.firstWord()) == theProgram {
						later := c
						later.first = sc.firstWord()
						later.second = sc.secondWord()
						ohmyLater = append(ohmyLater, later)
						break
					}
				}
			}

			// ---- (b) prefix-grant behaviour, isolated sessions only ----------
			if c.sidechain {
				// A sub-agent runs under its own policy, not this node's grant
				// list, so it cannot be judged against it (0213b:1587-1592).
				sidechainSkipped++
				continue
			}
			if polp == nil {
				noPolicySkipped++
				continue
			}
			if !polp.policy.isolated() {
				unisolatedSkipped++
				continue
			}
			wide := false
			for _, g := range polp.policy.AllowedTools {
				if isWideBash(g) {
					wide = true
					break
				}
			}
			if wide {
				wideBashSkipped++
				continue
			}
			eligibleCalls++

			namedByAGrant := false
			for _, g := range polp.policy.AllowedTools {
				if pre, ok := grantPrefixTokens(g); ok && pre[0] == c.first {
					namedByAGrant = true
				}
			}
			if !namedByAGrant {
				// A command whose program NO grant in force names. Every
				// PERMITTED one is why §5 treats an ALLOW as weak evidence.
				unnamedProgram[c.first]++
				if c.out == outDenied {
					unnamedDenied[c.first]++
				}
				continue
			}
			granted = append(granted, c)
		}
	}

	// ---- 3. aggregate ------------------------------------------------------

	// A row is one (grant string, second word) pair. A call is attributed to
	// EVERY in-force grant naming its program; the count of calls matched by
	// more than one is printed so that stays checkable rather than assumed.
	type verbKey struct{ grant, verb string }
	type verbStat struct {
		permitted, denied, other int
		deniedCompound           int
		deniedOutside            int
		deniedUnexplained        int
		notCovered               int
	}
	stats := map[verbKey]*verbStat{}
	grantCalls := map[string]int{}
	grantSessions := map[string]map[string]bool{}
	// wildcardWords[g] counts the values seen at the FIRST TOKEN POSITION THE
	// GRANT'S `*` COVERS — index len(prefix). For Bash(git *) that is the same
	// as the second word; for Bash(gh pr *) it is the third, and it is the only
	// position that grant leaves free.
	wildcardWords := map[string]map[string][2]int{}
	multiGrantCalls := 0
	var unexplainedCalls []call
	for _, c := range granted {
		if c.unexplained() {
			unexplainedCalls = append(unexplainedCalls, c)
		}
		hits := 0
		for _, g := range c.pol.policy.AllowedTools {
			pre, ok := grantPrefixTokens(g)
			if !ok || pre[0] != c.first {
				continue
			}
			hits++
			k := verbKey{g, c.second}
			st := stats[k]
			if st == nil {
				st = &verbStat{}
				stats[k] = st
			}
			switch c.out {
			case outDenied:
				st.denied++
				if c.compound {
					st.deniedCompound++
				}
				if c.scope == scopeReachesOutside {
					st.deniedOutside++
				}
				if c.unexplained() {
					st.deniedUnexplained++
				}
			case outRan:
				st.permitted++
			default:
				st.other++
			}
			toks := []string{c.first}
			if c.second != "" {
				toks = append(toks, c.second)
			}
			if !grantMatches(g, toks) {
				st.notCovered++
			}
			grantCalls[g]++
			if grantSessions[g] == nil {
				grantSessions[g] = map[string]bool{}
			}
			grantSessions[g][c.sessionID] = true

			// the grant's own wildcard position
			w := ""
			if len(pre) == 1 {
				w = c.second
			} else {
				// re-split to reach index len(pre); cheap, and only for the
				// handful of narrow grants in the corpus.
				sc := splitCommand(c.command)
				if len(sc.subs) > 0 && len(sc.subs[0].tokens) > len(pre) {
					w = sc.subs[0].tokens[len(pre)]
				}
			}
			if w == "" {
				w = "(nothing after the prefix)"
			}
			if wildcardWords[g] == nil {
				wildcardWords[g] = map[string][2]int{}
			}
			e := wildcardWords[g][w]
			if c.out == outDenied {
				e[1]++
			} else {
				e[0]++
			}
			wildcardWords[g][w] = e
		}
		if hits > 1 {
			multiGrantCalls++
		}
	}

	// ---- 4. report ---------------------------------------------------------
	line := strings.Repeat("=", 78)

	fmt.Println(line)
	fmt.Println("0011 — does a prefix grant distinguish subcommands, or authorise the binary?")
	fmt.Println(line)
	fmt.Println("THE MATCHER IS IN THE CLAUDE CODE BINARY. Nothing below reads it. Every row is")
	fmt.Println("a call this machine already made and a verdict the matcher already returned.")
	fmt.Println()

	fmt.Println("§1 CORPUS")
	fmt.Printf("  transcripts root                       %s\n", projectsRoot)
	fmt.Printf("  transcript files found                 %d\n", len(paths))
	fmt.Printf("  transcript files parsed                %d\n", filesParsed)
	fmt.Printf("  transcript files that errored mid-read %d\n", filesUnreadable)
	fmt.Printf("  JSONL lines parsed                     %d\n", linesParsed)
	fmt.Printf("  lines that were not valid JSON         %d\n", badLines)
	fmt.Printf("  tool_use blocks                        %d\n", toolUses)
	fmt.Printf("  Bash tool_use blocks                   %d\n", bashUses)
	fmt.Printf("  tool_result blocks                     %d\n", resultsSeen)
	fmt.Printf("  tool_results naming no tool_use        %d\n", orphanResults)
	fmt.Println()
	fmt.Printf("  runs root                              %s\n", runsRoot)
	fmt.Printf("  run directories                        %d\n", runDirs)
	fmt.Printf("    no state.json                        %d\n", runsNoState)
	fmt.Printf("    state.json would not parse           %d\n", runsBadState)
	fmt.Printf("    planned (graph_source_path == own graph.json)  %d\n", plannedRuns)
	fmt.Printf("    hand-written                                   %d\n", unplannedRuns)
	fmt.Printf("    IN FLIGHT, excluded                            %d\n", len(inFlightRuns))
	for _, r := range inFlightRuns {
		fmt.Printf("        %s\n", r)
	}
	fmt.Printf("  node tool_policies records (ALL runs)  %d\n", policiesSeen)
	fmt.Printf("    ISOLATED (setting_sources present)   %d\n", policiesIsolated)
	fmt.Printf("    unisolated (key absent => the user's own settings loaded too)  %d\n",
		policiesSeen-policiesIsolated)
	fmt.Printf("    held by an in-flight run, so mapped to no session below        %d\n",
		policiesInFlight)
	fmt.Printf("  sessions indexed to a policy           %d\n", len(sessions))
	fmt.Printf("  node records with no session_id        %d\n", nodesNoSession)
	fmt.Printf("  sessions claimed by >1 node (skipped)  %d\n", len(dupSessions))
	fmt.Println()
	fmt.Println("  THE CORPUS MOVES WHILE THIS PROGRAM READS IT. ~/.claude/projects holds the")
	fmt.Println("  transcript of the session running this program, and that transcript grows")
	fmt.Println("  with every tool call, so the file/line/tool_use counts above are NOT stable")
	fmt.Println("  across two runs the way 0213b's deliberately are. The counts §3, §4 and §5")
	fmt.Println("  rest on ARE stable: they are restricted to sessions holding an isolated")
	fmt.Println("  tool_policies record, and a session reading the corpus is not one of those.")
	fmt.Println()

	// ---- §2 (a) -----------------------------------------------------------
	fmt.Println(line)
	fmt.Printf("§2 (a) BASH CALLS WHOSE FIRST WORD IS %q\n", theProgram)
	fmt.Println(line)
	fmt.Printf("  THE ANSWER TO (a) IS THIS NUMBER: %d\n", len(ohmyHead))
	fmt.Println("  A grant is matched against the command the model wrote. Only a command")
	fmt.Printf("  whose own first word is %q could be matched by a token prefix\n", theProgram)
	fmt.Printf("  Bash(%s ...). Two neighbouring populations are counted apart below.\n", theProgram)
	fmt.Println()
	reportOhmy("  first word of the whole command", ohmyHead, true)
	reportOhmy("  a LATER sub-command's first word (`cd x && oh-my-graph run ...`) — a token\n"+
		"  prefix is matched against the command, and 0213b measured that a later piece\n"+
		"  can defeat the grant, so these are NOT the same population", ohmyLater, false)
	reportOhmy("  path-spelled (`./bin/oh-my-graph run ...`) — the same binary, but no token\n"+
		"  prefix Bash(oh-my-graph *) can match a first word that is a path", ohmyPathForm, false)

	fmt.Printf("  grants naming %s in ANY tool_policies record on this machine: ", theProgram)
	named := 0
	for _, g := range sortedCountKeys(grantStrings) {
		if pre, ok := grantPrefixTokens(g); ok && pre[0] == theProgram {
			fmt.Printf("%s x%d ", g, grantStrings[g])
			named++
		}
	}
	if named == 0 {
		fmt.Print("NONE — so no call above was judged against one")
	}
	fmt.Println()
	fmt.Println()

	// ---- §3 (b) -----------------------------------------------------------
	fmt.Println(line)
	fmt.Println("§3 (b) THE ANALOGY: prefix grants the corpus does hold, in force and isolated")
	fmt.Println(line)
	fmt.Println("  Population: Bash calls in a session whose recorded tool_policies entry is")
	fmt.Println("  ISOLATED, holds NO wide Bash/Bash(*), is not a sub-agent sidechain, and holds")
	fmt.Println("  at least one Bash(<prog> ...) grant naming the call's own first word.")
	fmt.Printf("  Bash calls examined                    %d\n", bashUses)
	fmt.Printf("    sub-agent sidechain, excluded        %d\n", sidechainSkipped)
	fmt.Printf("    session has no run record, excluded  %d\n", noPolicySkipped)
	fmt.Printf("    policy UNISOLATED, excluded          %d\n", unisolatedSkipped)
	fmt.Printf("    policy holds a wide Bash, excluded   %d\n", wideBashSkipped)
	fmt.Printf("    ELIGIBLE                             %d\n", eligibleCalls)
	fmt.Printf("      of those, program named by a grant %d\n", len(granted))
	fmt.Printf("      calls matched by >1 grant          %d\n", multiGrantCalls)
	fmt.Println()
	fmt.Println("  denied(cmp) = the denial is of a COMPOUND command  — 0213b's explanation")
	fmt.Println("  denied(out) = the denial reaches OUTSIDE the cwd    — 0213b §9's explanation")
	fmt.Println("  denied(!!)  = neither: unexplained by anything already measured, and the")
	fmt.Println("                only shape that could be the grant refusing a subcommand.")
	fmt.Println()

	var gs []string
	for g := range grantCalls {
		gs = append(gs, g)
	}
	sort.Slice(gs, func(i, j int) bool {
		if grantCalls[gs[i]] != grantCalls[gs[j]] {
			return grantCalls[gs[i]] > grantCalls[gs[j]]
		}
		return gs[i] < gs[j]
	})
	for _, g := range gs {
		pre, _ := grantPrefixTokens(g)
		fmt.Printf("  GRANT %-16s token prefix %v   %d calls in %d isolated session(s)\n",
			g, pre, grantCalls[g], len(grantSessions[g]))

		var keys []verbKey
		for k := range stats {
			if k.grant == g {
				keys = append(keys, k)
			}
		}
		sort.Slice(keys, func(i, j int) bool {
			a, b := stats[keys[i]], stats[keys[j]]
			ta := a.permitted + a.denied + a.other
			tb := b.permitted + b.denied + b.other
			if ta != tb {
				return ta > tb
			}
			return keys[i].verb < keys[j].verb
		})
		var (
			verbRows                          int
			nonVerb                           = map[wordKind]*verbStat{}
			nonVerbWords                      = map[wordKind]int{}
			distinct, denialFree, unexplained int
			outside                           int
		)
		for _, k := range keys {
			st := stats[k]
			distinct++
			if st.denied == 0 && st.permitted > 0 {
				denialFree++
			}
			unexplained += st.deniedUnexplained
			outside += st.notCovered
			kind := classifyWord(k.verb)
			if kind != kindVerb {
				agg := nonVerb[kind]
				if agg == nil {
					agg = &verbStat{}
					nonVerb[kind] = agg
				}
				agg.permitted += st.permitted
				agg.denied += st.denied
				agg.other += st.other
				agg.deniedCompound += st.deniedCompound
				agg.deniedOutside += st.deniedOutside
				agg.deniedUnexplained += st.deniedUnexplained
				nonVerbWords[kind]++
				continue
			}
			flag := ""
			if st.notCovered > 0 {
				flag = "  <-- OUTSIDE this grant's own token prefix"
			}
			fmt.Printf("    %-14s permitted %4d  denied %3d (cmp %2d, out %2d, !! %2d)  other %2d%s\n",
				k.verb, st.permitted, st.denied, st.deniedCompound, st.deniedOutside,
				st.deniedUnexplained, st.other, flag)
			verbRows++
		}
		for _, kind := range []wordKind{kindFlag, kindPath, kindNone} {
			agg := nonVerb[kind]
			if agg == nil {
				continue
			}
			fmt.Printf("    %-14s permitted %4d  denied %3d (cmp %2d, out %2d, !! %2d)  other %2d  [%d distinct]\n",
				"("+kind.String()+")", agg.permitted, agg.denied, agg.deniedCompound,
				agg.deniedOutside, agg.deniedUnexplained, agg.other, nonVerbWords[kind])
		}
		fmt.Printf("    -> %d distinct second words (%d of them bare verbs); %d denial-free;\n",
			distinct, verbRows, denialFree)
		fmt.Printf("       %d denial(s) unexplained by compound shape or by leaving the cwd;\n", unexplained)
		fmt.Printf("       %d call(s) fall outside this grant's own token prefix.\n", outside)

		// The position the grant's own `*` covers.
		ww := wildcardWords[g]
		var wl []string
		for w := range ww {
			wl = append(wl, w)
		}
		sort.Slice(wl, func(i, j int) bool {
			a, b := ww[wl[i]], ww[wl[j]]
			if a[0]+a[1] != b[0]+b[1] {
				return a[0]+a[1] > b[0]+b[1]
			}
			return wl[i] < wl[j]
		})
		clean := 0
		for _, w := range wl {
			if ww[w][1] == 0 {
				clean++
			}
		}
		shown := wl
		if len(shown) > 14 {
			shown = shown[:14]
		}
		var parts []string
		for _, w := range shown {
			parts = append(parts, fmt.Sprintf("%s(%d/%d)", w, ww[w][0], ww[w][1]))
		}
		more := ""
		if len(wl) > len(shown) {
			more = fmt.Sprintf(" ... +%d more", len(wl)-len(shown))
		}
		fmt.Printf("       at token index %d — the first position this grant's `*` covers —\n", len(pre))
		fmt.Printf("       %d distinct values, %d of them with zero denials: %s%s\n",
			len(wl), clean, strings.Join(parts, " "), more)
		fmt.Println()
	}

	// ---- §4 the decisive rows ---------------------------------------------
	fmt.Println(line)
	fmt.Println("§4 EVERY DENIAL NOT EXPLAINED BY COMPOUND SHAPE OR BY LEAVING THE CWD")
	fmt.Println(line)
	fmt.Println("  These are the only calls in the whole eligible population that a grant")
	fmt.Println("  refusing a subcommand could look like. Printed in full, all of them.")
	fmt.Printf("  count: %d of %d denied calls in the population\n", len(unexplainedCalls), countDenied(granted))
	sort.Slice(unexplainedCalls, func(i, j int) bool {
		if unexplainedCalls[i].first != unexplainedCalls[j].first {
			return unexplainedCalls[i].first < unexplainedCalls[j].first
		}
		return unexplainedCalls[i].command < unexplainedCalls[j].command
	})
	for _, c := range unexplainedCalls {
		fmt.Printf("    grants  %s\n", strings.Join(c.pol.policy.AllowedTools, ", "))
		fmt.Printf("    node    %s/%s   scope=%s   %s\n", c.pol.runID, c.pol.nodeID, c.scope, argShape(c.command))
		fmt.Printf("    command %s\n\n", oneLine(c.command, 300))
	}

	// The contrast that decides what those denials were ABOUT. For every
	// (program, second word) pair holding an unexplained denial, print EVERY
	// eligible call of that same pair — permitted ones included — with the four
	// argument-shape flags. If the permitted and the denied rows carry the same
	// verb and differ only in argument shape, the verb is not what was refused.
	// The flags are argument SHAPE, measured off the command string; the denial
	// text still names no reason, so this remains a correlation.
	fmt.Println("  THE SAME-VERB CONTRAST — every eligible call sharing a program and second")
	fmt.Println("  word with one of the denials above, permitted rows included:")
	type pair struct{ prog, verb string }
	want := map[pair]bool{}
	for _, c := range unexplainedCalls {
		want[pair{c.first, c.second}] = true
	}
	var contrast []call
	for _, c := range granted {
		if want[pair{c.first, c.second}] {
			contrast = append(contrast, c)
		}
	}
	sort.Slice(contrast, func(i, j int) bool {
		if contrast[i].first != contrast[j].first {
			return contrast[i].first < contrast[j].first
		}
		if contrast[i].second != contrast[j].second {
			return contrast[i].second < contrast[j].second
		}
		return len(contrast[i].command) < len(contrast[j].command)
	})
	var pairOrder []pair
	byPair := map[pair][]call{}
	for _, c := range contrast {
		k := pair{c.first, c.second}
		if _, seen := byPair[k]; !seen {
			pairOrder = append(pairOrder, k)
		}
		byPair[k] = append(byPair[k], c)
	}
	for _, k := range pairOrder {
		cs := byPair[k]
		perm, den, oth := 0, 0, 0
		for _, c := range cs {
			switch c.out {
			case outDenied:
				den++
			case outRan:
				perm++
			default:
				oth++
			}
		}
		fmt.Printf("    %q — permitted %d, denied %d, other %d, all under a grant whose token\n",
			k.prog+" "+k.verb, perm, den, oth)
		fmt.Println("      prefix reaches this exact call. Every DENIED row, then the PERMITTED rows")
		fmt.Println("      carrying the same argument shapes, then the plain remainder:")
		for _, c := range cs {
			if c.out == outDenied {
				fmt.Printf("        DENIED    %s %s\n", argShape(c.command), c.pol.nodeID)
			}
		}
		plainLo, plainHi, plain := 0, 0, 0
		shown := 0
		for _, c := range cs {
			if c.out != outRan {
				continue
			}
			if strings.Contains(c.command, "\n") || strings.Contains(c.command, "$'") ||
				strings.Contains(c.command, "`") {
				if shown < 8 {
					fmt.Printf("        PERMITTED %s %s\n", argShape(c.command), c.pol.nodeID)
					shown++
				}
				continue
			}
			plain++
			if plainLo == 0 || len(c.command) < plainLo {
				plainLo = len(c.command)
			}
			if len(c.command) > plainHi {
				plainHi = len(c.command)
			}
		}
		fmt.Printf("        PERMITTED with none of those shapes: %d call(s), len %d..%d\n\n",
			plain, plainLo, plainHi)
	}

	// ---- §5 totals per verb -----------------------------------------------
	fmt.Println(line)
	fmt.Println("§5 (c) PER-VERB TOTALS ACROSS EVERY GRANT IN §3")
	fmt.Println(line)
	verbTotals := map[string]*verbStat{}
	for k, st := range stats {
		if classifyWord(k.verb) != kindVerb {
			continue
		}
		t := verbTotals[k.verb]
		if t == nil {
			t = &verbStat{}
			verbTotals[k.verb] = t
		}
		t.permitted += st.permitted
		t.denied += st.denied
		t.other += st.other
		t.deniedCompound += st.deniedCompound
		t.deniedOutside += st.deniedOutside
		t.deniedUnexplained += st.deniedUnexplained
	}
	var vs []string
	for v := range verbTotals {
		vs = append(vs, v)
	}
	sort.Slice(vs, func(i, j int) bool {
		a, b := verbTotals[vs[i]], verbTotals[vs[j]]
		if a.permitted+a.denied != b.permitted+b.denied {
			return a.permitted+a.denied > b.permitted+b.denied
		}
		return vs[i] < vs[j]
	})
	totalPerm, totalDen, totalUnex := 0, 0, 0
	for _, v := range vs {
		t := verbTotals[v]
		totalPerm += t.permitted
		totalDen += t.denied
		totalUnex += t.deniedUnexplained
		fmt.Printf("  %-14s permitted %4d  denied %3d (cmp %2d, out %2d, !! %2d)  other %2d\n",
			v, t.permitted, t.denied, t.deniedCompound, t.deniedOutside,
			t.deniedUnexplained, t.other)
	}
	fmt.Printf("  -> %d distinct bare verbs; %d permitted, %d denied, of which %d unexplained.\n",
		len(vs), totalPerm, totalDen, totalUnex)
	fmt.Println()

	// ---- §6 the weak-evidence bucket --------------------------------------
	fmt.Println(line)
	fmt.Println("§6 ELIGIBLE CALLS WHOSE PROGRAM NO IN-FORCE GRANT NAMED")
	fmt.Println(line)
	fmt.Println("  These ran (or were denied) in an isolated, no-wide-Bash session whose grant")
	fmt.Println("  list names no such program at all. Every PERMITTED one is a reason to treat")
	fmt.Println("  an ALLOW as weaker evidence than a DENY: the matcher is not a pure lookup")
	fmt.Println("  over the grant list the engine passed.")
	var us []string
	for w := range unnamedProgram {
		us = append(us, w)
	}
	sort.Slice(us, func(i, j int) bool {
		if unnamedProgram[us[i]] != unnamedProgram[us[j]] {
			return unnamedProgram[us[i]] > unnamedProgram[us[j]]
		}
		return us[i] < us[j]
	})
	totalUnnamed, totalUnnamedDenied := 0, 0
	for _, w := range us {
		totalUnnamed += unnamedProgram[w]
		totalUnnamedDenied += unnamedDenied[w]
	}
	fmt.Printf("  calls %d, of which denied %d, over %d distinct programs\n",
		totalUnnamed, totalUnnamedDenied, len(us))
	for i, w := range us {
		if i == 25 {
			fmt.Printf("    ... %d further programs suppressed\n", len(us)-25)
			break
		}
		fmt.Printf("    %-18s calls %4d  denied %3d\n", w, unnamedProgram[w], unnamedDenied[w])
	}
	fmt.Println()

	// ---- §7 what the program could not read cleanly -----------------------
	fmt.Println(line)
	fmt.Println("§7 DATA THIS PROGRAM COULD NOT READ CLEANLY (never silently dropped)")
	fmt.Println(line)
	fmt.Printf("  denials the 0213b predicate caught but 0218's stricter one did not  %d\n", strictDisagreements)
	fmt.Printf("  distinct grant strings across every policy                          %d\n", len(grantStrings))
	for _, g := range sortedCountKeys(grantStrings) {
		fmt.Printf("      %-28s %d\n", g, grantStrings[g])
	}
	fmt.Printf("  grant patterns whose SHAPE this program does not model              %d\n", len(unrecognisedGrants))
	for _, g := range sortedCountKeys(unrecognisedGrants) {
		fmt.Printf("      %-28s %d\n", g, unrecognisedGrants[g])
	}
	fmt.Printf("  sessions claimed by more than one node (policy unattributable)      %d\n", len(dupSessions))
	for i, d := range dupSessions {
		if i == 5 {
			fmt.Printf("      ... %d more\n", len(dupSessions)-5)
			break
		}
		fmt.Printf("      %s\n", d)
	}
	fmt.Printf("  transcripts that errored mid-read (their calls are a FLOOR)         %d\n", filesUnreadable)
	for _, u := range unreadable {
		fmt.Printf("      %s\n", u)
	}
	fmt.Println()
	fmt.Println("  THE DENIAL TEXT CARRIES NO REASON CODE. It is byte-identical for an")
	fmt.Println("  out-of-scope command, a compound command whose later piece was ungranted,")
	fmt.Println("  and a path-sensitive refusal. Everything above is a correlation between the")
	fmt.Println("  shape of a command and what happened to it, never a causal reading.")
	fmt.Println("  THE claude VERSION THAT PRODUCED THIS CORPUS IS UNKNOWN: no run record")
	fmt.Println("  carries it, and asking this machine reports today's version, not the")
	fmt.Println("  corpus's. If grant matching changed across versions, this is two populations.")
}

// reportOhmy prints one of §2's three populations: a per-subcommand table
// always, every DENIED row in full always (they are the rare decisive ones), and
// the permitted rows only for the population the brief actually asks about.
func reportOhmy(title string, calls []call, full bool) {
	fmt.Printf("%s\n", title)
	fmt.Printf("    calls: %d\n", len(calls))
	if len(calls) == 0 {
		fmt.Println("    ZERO.")
		fmt.Println()
		return
	}
	subs := map[string]map[outcome]int{}
	for _, c := range calls {
		sub := c.second
		if sub == "" {
			sub = "(no argument)"
		}
		if subs[sub] == nil {
			subs[sub] = map[outcome]int{}
		}
		subs[sub][c.out]++
	}
	for _, sub := range sortedKeys(subs) {
		m := subs[sub]
		fmt.Printf("      %-16s permitted %3d  denied %3d  other %3d\n",
			sub, m[outRan], m[outDenied], m[outUserReject]+m[outUnknownErr]+m[outNoResult])
	}
	// Isolation matters even here: a permitted call under a policy that loaded
	// the user's own settings was permitted by the USER's grants, not by any
	// grant the engine passed.
	iso, unis, none := 0, 0, 0
	for _, c := range calls {
		switch {
		case c.pol == nil:
			none++
		case c.pol.policy.isolated():
			iso++
		default:
			unis++
		}
	}
	fmt.Printf("      policy behind these calls: isolated %d | unisolated %d | no run record %d\n",
		iso, unis, none)

	shown := 0
	for _, c := range calls {
		if c.out != outDenied && !(full && shown < 12) {
			continue
		}
		if c.out != outDenied {
			shown++
		}
		fmt.Printf("      [%s] policy=%s\n", c.out, c.policyLabel())
		fmt.Printf("          %s\n", c.transcript)
		fmt.Printf("          %s\n", oneLine(c.command, 220))
	}
	if full && len(calls) > 12 {
		fmt.Printf("      (%d permitted rows suppressed; every DENIED row above is printed in full)\n",
			len(calls)-12)
	}
	fmt.Println()
}

// argShape reports the four properties of a command string that this corpus's
// denials turned out to track — length, a literal newline, an ANSI-C $'...'
// quote, a backtick — none of which is the subcommand. They are printed rather
// than tested against a threshold: a threshold fitted to nineteen calls would
// measure the fitting.
func argShape(cmd string) string {
	return fmt.Sprintf("len=%-5d nl=%-5t ansiC=%-5t backtick=%-5t",
		len(cmd), strings.Contains(cmd, "\n"), strings.Contains(cmd, "$'"),
		strings.Contains(cmd, "`"))
}

func countDenied(calls []call) int {
	n := 0
	for _, c := range calls {
		if c.out == outDenied {
			n++
		}
	}
	return n
}

func oneLine(s string, n int) string {
	r := strings.NewReplacer("\n", "\\n", "\t", "\\t", "\r", "")
	s = r.Replace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func sortedKeys(m map[string]map[outcome]int) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedCountKeys(m map[string]int) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if m[out[i]] != m[out[j]] {
			return m[out[i]] > m[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}
