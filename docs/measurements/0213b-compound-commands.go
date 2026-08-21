//go:build ignore

// Measurement 0213b — does a compound command defeat a prefix grant?
//
// Issue #213 asks whether a node that holds `Bash(go *)` gets denied because it
// wrote `go build ... | head -50`: the grant covers the command the node MEANT
// to run, but a later sub-command (`head`) was never granted. This program finds
// every Bash tool call that was DENIED inside a PLANNED node, across the whole
// local run corpus, and classifies each one:
//
//	(A) first sub-command granted, a later one not  — the #213 compound shape
//	(B) the command's own first word was never granted — an ordinary out-of-scope call
//	(C) every sub-command granted, denied anyway     — unexplained by either
//
// (C) is not a catch-all; it is the interesting bucket. If it is non-empty, the
// compound hypothesis is not the whole story and a transcript can never say why,
// because THE DENIAL TEXT CARRIES NO REASON CODE — the string is byte-identical
// for a compound call, a simple out-of-scope call, and a sandbox refusal. This
// program therefore reports a CORRELATION between command shape and denial. It
// cannot establish causation, and nothing it prints should be read as doing so.
// A supplementary cross-tab (§9) prints the one confound visible on disk — path
// arguments reaching outside the node's cwd — so the two factors can be told
// apart rather than conflated.
//
// It is a MEASUREMENT, not a lint. It ships no behaviour and nothing in the
// engine calls it. The `ignore` build tag keeps it out of `go build ./...` and
// `go test ./...`; run it explicitly, which does not apply build constraints:
//
//	go run docs/measurements/0213b-compound-commands.go
//
// It takes NO ARGUMENTS and writes its own two output files, so a caller under a
// tool grant that forbids shell redirection never needs any:
//
//	docs/measurements/0213b-results.json  machine-readable: every count, and one
//	                                      record per denied call
//	docs/measurements/0213b-results.txt   the human summary, also printed to stdout
//
// The .txt and stdout are DETERMINISTIC — no timestamps, no map iteration order,
// every list sorted — so two runs over an unchanged corpus produce byte-identical
// files and a re-run is a real check. The .json carries the wall-clock snapshot
// timestamp (time.Now, taken inside the program), which is the one field that
// moves between runs; it is deliberately kept out of the .txt so that the
// byte-comparison stays available.
//
// METHOD, and the traps it is built to avoid:
//
//   - PARSE, NEVER GREP. encoding/json over every JSONL record, standard library
//     only. This program shells out to nothing (no os/exec) and never infers
//     structure from text. Same scar as #213 and #218: a `grep -c` figure in this
//     repo reached three documents before anyone noticed it was wrong.
//   - JOIN BY tool_use_id, NEVER BY ADJACENCY. A tool_result names the tool_use
//     it answers. Adjacency is wrong here: this CLI splits one assistant message
//     across several JSONL lines, and interleaves attachment/last-prompt/ai-title
//     records between a call and its result. A tool_result whose id matches no
//     tool_use is a DATA DEFECT — counted and reported (§6), never dropped.
//   - is_error IS NOT DENIAL. An ordinary tool failure also sets is_error, but is
//     wrapped in <tool_use_error>. A permission denial is unwrapped and starts
//     "Permission to use ". Anchor at offset 0; do NOT substring-search for
//     "denied", which also matches ordinary model prose (a node quoting its own
//     denial back into its artifact) and matches this repo's own sessions
//     grepping for the denial sentence.
//   - COUNT THE WORDINGS YOU DO NOT KNOW. Two denial-shaped wordings exist in the
//     local corpus: the policy denial above, and "The user doesn't want to
//     proceed with this tool use." — an INTERACTIVE HUMAN REJECTION, which cannot
//     occur in an unattended `dontAsk` node and is therefore counted separately
//     rather than folded into the denominator. Any third wording lands in a named
//     residual bucket (§6) so it surfaces as a number instead of vanishing.
//   - THE DUPLICATE COPY. The same record repeats the result text in a top-level
//     `toolUseResult` string prefixed "Error: ". Only message.content[] is read,
//     so nothing is counted twice.
//   - NO FILE CAP. Every run directory, every transcript.
//   - Sessions with no transcript on disk are a NAMED BUCKET with a count.
//   - A HEREDOC BODY IS NOT SHELL. The first cut of this program scanned heredoc
//     bodies as if they were commands, and bucket (A)'s offender table filled up
//     with `Co-Authored-By:`, `EOF`, `package`, `import`, `func` and loose prose
//     words — a commit message and a Go file being written to disk, shredded into
//     imaginary sub-commands. Every one of those inflated (A), the very bucket
//     this measurement exists to size. Heredocs are now consumed, and shell
//     grammar words (`do`, `done`, `fi`, `}`) are no longer counted as commands.
//     §2 of the report states exactly what the splitter does and does not do.
//
// PROVENANCE OF THE PLANNED-RUN PREDICATE — sameFile below is COPIED VERBATIM,
// as briefed, from two files where it is byte-identical:
//
//	docs/measurements/0213-tool-grant-predicate.go     commit b1a55ba (#213),
//	                                                   branch measure/tool-grant-predicate
//	docs/measurements/0218-denied-nodes-that-passed.go commit 0736635 (#218),
//	                                                   branch measure/denied-nodes
//
// THEY LIVE ON TWO DIFFERENT BRANCHES — an earlier draft of this header said both
// were on `measure/denied-nodes`, which is false and fails the moment anyone
// checks it (`git branch -a --contains b1a55ba` names only
// measure/tool-grant-predicate). Neither commit is an ancestor of this branch's
// HEAD, which is why a `Glob docs/measurements/*.go` of the working tree reports
// both files absent; both were read out of the object store with
// `git show <commit>:<path>`. The three measurements therefore share ONE corpus
// definition rather than three that drift.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Constants of the measurement
// ---------------------------------------------------------------------------

// excludedRunID is this lane's own run (graph `compound-commands-defeat-grants`).
// It is in flight while this program runs: its later nodes keep appending to it,
// so including it would make the corpus measure itself and report a figure that
// changes under its own feet. It is also, inconveniently, the lexically NEWEST
// run id — a "newest run" heuristic would select exactly the run that must go.
// #218 hit the same hazard and solved it with a general in-flight predicate;
// that predicate is ALSO evaluated here, as a cross-check, but only reported —
// this measurement's brief specifies exclusion by run id, so exclusion by run id
// is what decides the corpus. See §6.
const excludedRunID = "20260820-195627.390122000-1"

// denialPolicyPrefix is the exact opening of a permission-denial tool_result.
// The remainder is fixed boilerplate; the whole string is a template, identical
// for every denied call, and NAMES NO REASON — it is byte-identical for the
// compound `go build ... | head -50; echo ...` and for the simple
// `ls -d ~/.oh-my-graph/runs`.
const denialPolicyPrefix = "Permission to use "

// userRejectPrefix is the second denial-shaped wording in the corpus: a human
// pressing "no" in an interactive session. A planned node runs unattended under
// `dontAsk` with nobody to press anything, so this should never appear inside
// the measured population — which is exactly why it is counted rather than
// assumed away.
const userRejectPrefix = "The user doesn't want to proceed with this tool use."

// toolUseErrorPrefix wraps an ORDINARY tool failure (a bad path, a non-zero
// exit). It also sets is_error, and it is not a denial.
const toolUseErrorPrefix = "<tool_use_error>"

// errorPrefix is what the duplicate .toolUseResult copy prepends. Only
// message.content[] is read, so this is belt-and-braces.
const errorPrefix = "Error: "

// The verdict vocabulary internal/runstate declares. The empty string is a
// non-terminal record (ADR 0010's feedback marker), not a failure.
const (
	verdictPass = "PASS"
	verdictFail = "FAIL"
)

// Output file names. The program writes both itself; see the package comment.
const (
	outJSONName = "0213b-results.json"
	outTextName = "0213b-results.txt"
)

// ---------------------------------------------------------------------------
// THE ASSUMPTION
// ---------------------------------------------------------------------------

// assumptionText is printed at the top of the report and copied into the JSON.
// The rule below is NOT authoritative: how Claude Code applies `Bash(go *)`
// lives in the Claude Code binary, and no source in this repository implements
// it. A stated assumption is honest; a silent one is the failure mode this repo
// keeps writing down.
const assumptionText = `ASSUMED GRANT-MATCHING RULE (not authoritative — see grantMatches(), one function,
change it in one place):

  * A grant "Bash(P)" applies only to Bash calls. Non-Bash grants (Read, Grep,
    Glob, Skill, ...) never match a sub-command.
  * If P ends in " *", the part before it is a TOKEN PREFIX. "Bash(go *)" matches
    a sub-command whose first token is "go". "Bash(gh pr *)" requires the first
    TWO tokens to be "gh" then "pr". The "*" covers all remaining arguments and
    is not matched character-wise.
  * P == "*" matches every sub-command.
  * P with no "*" is an EXACT whole-command match: "Bash(ls)" matches the
    sub-command "ls" and nothing else.
  * A bare grant "Bash" with no parentheses matches EVERY sub-command.
  * Matching is on tokens, never on the raw string, so quoting and spacing in the
    arguments cannot change the verdict.

WHAT THIS RULE DELIBERATELY DOES NOT MODEL, because it is unknown:
  * whether the real matcher splits a compound command at all, or matches the raw
    shell string against the pattern as one unit;
  * whether an unmatched later sub-command denies the whole call;
  * whether a working-directory / sandbox constraint is applied to path arguments
    IN ADDITION to the pattern (§9 gives evidence that something path-sensitive
    is operating, but not what);
  * whether a denial arises from failure-to-match, an explicit deny rule, or a
    sandbox check. The denial text is identical in all three cases.

WHAT THE REPOSITORY DOES PIN DOWN (measured, not from source — DESIGN.md):
  * DESIGN.md:106-110 — "A tool call is matched against every loaded permission
    rule; if nothing matches, the call resolves to *ask*, and the mode decides
    what an unanswerable ask becomes — under dontAsk (our unattended default) it
    becomes a deny." So a planned node is DEFAULT-DENY, and "denied" means "no
    rule matched" — indistinguishable on disk from an explicit deny or a sandbox
    refusal.
  * DESIGN.md:113-116 — "--disallowedTools subtracts and beats a prior allow, but
    only at bare-tool-name granularity (Bash); a scoped deny like Bash(*) matches
    a command literally starting with * and enforces nothing. Measured on claude
    2.1.220." A bare "Bash" in a node's disallowed_tools therefore beats every
    allow it holds; §6 counts how many denied calls sat under one.
  * DESIGN.md:2195-2199 — --tools bounds tool NAMES and not SCOPES, and a grant
    can be invisible in graph.json while its durable record is state.json's
    tool_policies. §6 cross-checks the two records against each other.

THE claude VERSION THAT PRODUCED THIS CORPUS IS UNKNOWN. No run record carries
it, and asking this machine would report today's version, not the corpus's. If
grant matching changed across versions, the corpus is silently two populations.`

// splitterText documents the splitter's real limits. Everything listed under
// "DOES NOT" is a known way this program could misclassify a command.
const splitterText = `SUB-COMMAND SPLITTER — WHAT IT HANDLES:
  * splits on | || ; && and newline, at top level only;
  * single quotes: no split point and no escape is recognised inside them, so
    'a | b' is one literal argument, exactly as sh reads it;
  * double quotes: | ; and newline inside are NOT split points, but $( ) and
    backticks inside ARE still expanded and their contents split — so a git
    commit message passed as -m "...multi-line..." stays one argument;
  * backslash escapes outside single quotes;
  * command substitution $( ) — contents become their own sub-commands, tracked
    at a nesting depth, and NESTED $( $( ) ) is handled by paren counting;
  * backtick substitution — same, one level;
  * HEREDOCS: << and <<- with a bare, 'quoted' or "quoted" delimiter. The body is
    consumed as DATA and never scanned as shell, the terminator line is consumed
    with it, <<- strips leading tabs before comparing, and several heredocs queued
    on one line are consumed in order. This matters: without it a commit message
    or a Go source file written via <<EOF is shredded into fake sub-commands that
    land in bucket (A), which is the bucket this measurement exists to size;
  * <<< here-strings are left as ordinary text, not treated as heredocs;
  * SHELL GRAMMAR IS NOT A COMMAND: a leading do/then/else/elif/if/while/until/
    time/!/{/( is stripped and the command after it is judged, while a piece that
    is only grammar (done/fi/esac/}/)/;;) or a loop header (for/case/select) is
    dropped rather than counted as an ungranted command named "done";
  * redirections (2>&1, > f, < f) are not split points, so "go build 2>&1" stays
    one sub-command whose first token is "go";
  * leading VAR=value assignments are skipped when taking the first token, so
    "CGO_ENABLED=0 go build" has first token "go"; a leading "sudo" or "env" is
    skipped too, so "sudo rm -rf x" and "env FOO=1 go build" have first token
    "rm" and "go" and are judged against the command that would really run.

WHAT IT DOES NOT HANDLE (a command using these may be misclassified):
  * process substitution <(...) and >(...) — the contents are NOT extracted as
    sub-commands, and the parentheses are scanned as ordinary text;
  * a single & (background) is not a split point, so "a & b" reads as one
    sub-command "a & b" with first token "a";
  * arithmetic $(( )) is treated as a command substitution containing an
    expression, which yields a junk sub-command rather than an error;
  * ANSI-C quoting $'...' is not unwrapped: the $'...' opener is scanned as an
    ordinary word, so a $'...' argument containing an unescaped ' can confuse the
    single-quote scanner;
  * a $( ) whose body contains UNBALANCED parentheses (possible inside a heredoc
    that is itself inside the substitution) defeats the paren counter, and the
    rest of the command is then absorbed as literal text;
  * sudo/env are stripped only in their BARE form: "sudo -u root rm" leaves first
    token "-u", and the other wrappers are not unwrapped at all — "xargs rm",
    "sh -c 'rm x'" and "find . -exec rm {} ;" have first token xargs / sh / find,
    so the command they actually run stays invisible to the match;
  * aliases, functions and $PATH resolution — irrelevant to a token match, but it
    means "./script.sh" is its own first token and matches no prefix grant.`

// ---------------------------------------------------------------------------
// grantMatches — THE ASSUMPTION, as one function
// ---------------------------------------------------------------------------

// grantMatches reports whether a single declared grant covers a sub-command
// given as its token list. This is the one place a reviewer needs to edit to
// change the rule the whole measurement rests on. See assumptionText.
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

// anyGrantMatches asks whether SOME grant the node held covers this sub-command.
func anyGrantMatches(grants []string, tokens []string) bool {
	for _, g := range grants {
		if grantMatches(g, tokens) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Splitter
// ---------------------------------------------------------------------------

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

// leadingGrammar words prefix a real command; strip them and judge what follows.
var leadingGrammar = map[string]bool{
	"do": true, "then": true, "else": true, "elif": true,
	"if": true, "while": true, "until": true,
	"!": true, "time": true, "{": true, "(": true,
}

// wholeGrammar pieces are grammar with no command in them at all: dropping them
// is the difference between reporting an ungranted command called "done" and
// reporting the truth, which is that a loop ended.
var wholeGrammar = map[string]bool{
	"done": true, "fi": true, "esac": true, "}": true, ")": true, ";;": true,
	"for": true, "case": true, "select": true, "in": true,
}

// splitResult carries the sub-commands plus the tallies the report needs to be
// honest about how much of the input the splitter chose not to treat as shell.
type splitResult struct {
	subs         []subcommand
	heredocs     int // heredoc bodies consumed as data
	grammarDrops int // pieces dropped as pure shell grammar
}

// splitCommand returns the sub-commands of a shell string in source order:
// top-level pieces and the contents of every command substitution.
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
		// Strip leading grammar until a real command word is on top.
		for len(tokens) > 0 && leadingGrammar[tokens[0]] {
			tokens = tokens[1:]
		}
		if len(tokens) == 0 || wholeGrammar[tokens[0]] {
			r.grammarDrops++
			return
		}
		r.subs = append(r.subs, subcommand{text: t, tokens: tokens, depth: depth})
	}

	// consumeHeredocs eats the bodies queued on the line that just ended,
	// starting at from (the index just past the newline), and returns the index
	// of the first character after the last terminator line.
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
			// Single quotes: literal to the next single quote, no escapes.
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
				cur.WriteByte(c) // <<< here-string, or a malformed <<
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
				cur.WriteByte(c) // 2>&1, or a background &: not a split point.
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
	// A heredoc still pending at end of input has no body to consume.
	flush()
}

// parseHeredocOpener reads "<<", optional "-", and the delimiter word at i,
// which may be bare, 'single-quoted' or "double-quoted". It returns the heredoc
// and the index just past the delimiter. ok is false for <<< (a here-string) and
// for a << with no delimiter word.
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

// scanDoubleQuoted copies a double-quoted span into cur, recursing into any
// substitution it contains, and returns the index just past the closing quote.
// A newline inside the quotes is ordinary text, not a split point — that is what
// keeps a multi-line `git commit -m "..."` message from becoming sub-commands.
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

// matchParen returns the index of the ')' matching the '(' at open, counting
// nesting and skipping quoted spans. -1 when unbalanced.
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

// indexUnescaped finds the next unescaped occurrence of want at or after from.
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

// tokenize splits a sub-command into unquoted tokens and drops the leading words
// that are not the command: VAR=value assignments, a bare `env`, a bare `sudo`.
// If a sub-command is nothing BUT those, the raw tokens are kept, because an
// empty token list would silently classify as "never granted".
func tokenize(s string) []string {
	raw := splitWords(s)
	stripped := stripNonCommandPrefix(raw)
	if len(stripped) == 0 {
		return raw
	}
	return stripped
}

// stripNonCommandPrefix removes leading assignments and bare sudo/env wrappers,
// so `CGO_ENABLED=0 go build` and `sudo rm -rf x` are judged on `go` and `rm`
// rather than on an assignment or on `sudo`. Only the BARE wrapper is handled:
// `sudo -u root rm` still yields `-u`, which the splitter text declares.
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

// splitWords splits on unquoted whitespace and strips quoting from each word.
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
// Corpus: run state and graph
// ---------------------------------------------------------------------------

// nodePolicy is state.json's `tool_policies.<id>`: the EFFECTIVE argv the engine
// handed the CLI. DESIGN.md:2198-2199 records why it is the more durable record
// than graph.json — a grant can exist here and be invisible there. This
// measurement classifies on graph.json as briefed, and uses this only to
// cross-check (§6), so the two sources can be seen to agree instead of one of
// them being trusted silently.
type nodePolicy struct {
	AllowedTools    []string `json:"allowed_tools"`
	DisallowedTools []string `json:"disallowed_tools"`
	Tools           []string `json:"tools"`
}

type stateFile struct {
	RunID           string                `json:"run_id"`
	GraphSourcePath string                `json:"graph_source_path"`
	ToolPolicies    map[string]nodePolicy `json:"tool_policies"`
	Graph           struct {
		Name  string `json:"name"`
		Nodes []struct {
			ID           string   `json:"id"`
			AllowedTools []string `json:"allowed_tools"`
		} `json:"nodes"`
	} `json:"graph"`
	Nodes map[string]struct {
		Verdict   string `json:"verdict"`
		SessionID string `json:"session_id"`
	} `json:"nodes"`
}

type graphFile struct {
	Name  string `json:"name"`
	Nodes []struct {
		ID           string   `json:"id"`
		AllowedTools []string `json:"allowed_tools"`
	} `json:"nodes"`
}

// Named skip reasons, carried verbatim from 0213/0218 so the three measurements
// describe the same corpus in the same words.
const (
	skipNoGraph  = "no graph.json (a hand-written run writes none)"
	skipNoParse  = "state.json or graph.json would not parse"
	noteNoRecord = "no record in state.nodes — the run halted before this node was reached"
	noteNoSess   = "record present but session_id is empty — no transcript addressable"
	noteMissing  = "session_id present but no ~/.claude/projects/*/<id>.jsonl matched"
	noteUnread   = "transcript matched but could not be read in full — excluded from both numerator and denominator"
)

// sameFile is the PLANNED test, and the only one used here. COPIED UNCHANGED
// from docs/measurements/0213-tool-grant-predicate.go (commit b1a55ba, branch
// measure/tool-grant-predicate) and docs/measurements/0218-denied-nodes-that-
// passed.go (commit 0736635, branch measure/denied-nodes), where it is
// byte-identical, so the three measurements share a corpus definition rather
// than three definitions that drift: a run is planned exactly when its
// snapshot's `graph_source_path`, after filepath.Clean and symlink resolution,
// is the same file as that run's own graph.json. A hand-written run points at a
// .yaml somewhere else in the filesystem; an auto run points at the graph.json
// the planner wrote into the run directory. os.SameFile compares device+inode,
// so it survives /var vs /private/var and any other path spelling.
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

// inFlight is #218's in-flight test, carried here as a CROSS-CHECK ONLY: this
// measurement's corpus is decided by excludedRunID, not by this function, and
// nothing it returns changes a count. It is evaluated and reported so that a
// reader can reconcile 0213b's corpus against 0218's, and so that an in-flight
// run this measurement did not anticipate does not pass unnoticed.
//
// THE TEST, as #218 established it against this same corpus: at least one node
// in the graph has no entry in state.nodes, AND no record carries FAIL. Under
// `on_fail: halt` a run that stopped early always leaves a FAIL behind, and a
// run still executing has not, so the two cases separate. "Has a node with no
// record" on its own is NOT the test — it would wrongly drop legitimately
// halted runs. An EMPTY verdict is not a FAIL and must not be read as one.
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

// ---------------------------------------------------------------------------
// Transcript records
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

// blockText renders a tool_result's content, which is a bare string in the shapes
// observed on disk but is an array of blocks in the API's own form. Both are
// handled rather than assumed.
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

// errShape is the discriminator for an errored tool_result. is_error alone is
// NOT denial: an ordinary failure is wrapped in <tool_use_error>, and a
// permission denial is unwrapped and opens with a known template. Anything else
// is a wording this program does not know, and lands in a named residual bucket
// rather than being absorbed into one of the shapes above.
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

type parsedTranscript struct {
	uses    map[string]toolUse
	results []toolResult
	lines   int
	bad     int // lines that were not valid JSON
}

func parseTranscript(path string) (*parsedTranscript, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pt := &parsedTranscript{uses: map[string]toolUse{}}
	seq := 0
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
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
		// atis-latch / queue-operation records are interleaved and carry no
		// content array.
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
	return pt, nil
}

// ---------------------------------------------------------------------------
// Path-scope diagnostic (supplementary — see §9 of the report)
// ---------------------------------------------------------------------------

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

// classifyPathScope asks whether every path-shaped argument of the command lies
// under the node's cwd. It is a heuristic on argument SHAPE (a token starting
// with / ~ ./ or ../), not a resolution of what the shell would really open.
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

// within compares PATH SEGMENTS, not string prefixes. A prefix test on ".." is
// wrong, and was wrong here first time round: `go build ./...` yields the
// relative path "...", whose string prefix is "..", and the whole Go package
// wildcard was being reported as escaping the working directory.
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
// Classification
// ---------------------------------------------------------------------------

type bucket string

const (
	bucketA       bucket = "A"
	bucketB       bucket = "B"
	bucketC       bucket = "C"
	bucketNoParse bucket = "D"
	bucketNoGrant bucket = "E"
)

var bucketTitle = map[bucket]string{
	bucketA:       "(A) FIRST SUB-COMMAND GRANTED, A LATER ONE NOT — the #213 compound shape",
	bucketB:       "(B) THE COMMAND'S OWN FIRST WORD WAS NEVER GRANTED — out of scope from the start",
	bucketC:       "(C) EVERY SUB-COMMAND GRANTED, DENIED ANYWAY — unexplained by the grant list",
	bucketNoParse: "(D) COMMAND EMPTY OR UNSPLITTABLE — no sub-command to judge",
	bucketNoGrant: "(E) NODE'S GRANT LIST NOT RECOVERABLE FROM graph.json — cannot classify",
}

var bucketOrder = []bucket{bucketA, bucketB, bucketC, bucketNoParse, bucketNoGrant}

type denial struct {
	runID        string
	nodeID       string
	sessionID    string
	command      string
	grants       []string
	grantsKnown  bool
	policyGrants []string
	policyAgrees bool
	policyKnown  bool
	bashByName   bool // tool_policies denies the bare name Bash: beats every allow
	split        splitResult
	granted      []bool
	bucket       bucket
	offenders    []string // ungranted sub-command TEXTS, in source order
	offenderWord []string // their first words — the tail §8 counts
	compound     bool
	scope        pathScope
	cwd          string
	shape        errShape
}

func classify(d *denial) {
	if !d.grantsKnown {
		d.bucket = bucketNoGrant
		return
	}
	if len(d.split.subs) == 0 {
		d.bucket = bucketNoParse
		return
	}
	d.granted = make([]bool, len(d.split.subs))
	for i, sc := range d.split.subs {
		d.granted[i] = anyGrantMatches(d.grants, sc.tokens)
		if !d.granted[i] {
			d.offenders = append(d.offenders, sc.text)
			d.offenderWord = append(d.offenderWord, sc.firstWord())
		}
	}
	switch {
	case !d.granted[0]:
		d.bucket = bucketB
	case len(d.offenders) > 0:
		d.bucket = bucketA
	default:
		d.bucket = bucketC
	}
}

// ---------------------------------------------------------------------------
// Counters
// ---------------------------------------------------------------------------

type counters struct {
	RunDirs             int `json:"run_directories"`
	ExcludedRuns        int `json:"excluded_own_run"`
	RunsNoStateJSON     int `json:"runs_with_no_state_json"`
	RunsUnreadableState int `json:"runs_with_unparseable_state_json"`
	RunsNoGraphJSON     int `json:"runs_with_no_graph_json"`
	RunsBadGraphJSON    int `json:"runs_with_unparseable_graph_json"`
	UnplannedRuns       int `json:"runs_not_planned"`
	PlannedRuns         int `json:"runs_planned"`

	NodeRecords        int `json:"node_records_in_planned_runs"`
	NodesTranscriptOK  int `json:"nodes_with_transcript_parsed"`
	NodesNoTranscript  int `json:"nodes_with_session_id_but_no_transcript_file"`
	NodesNoSessionID   int `json:"nodes_with_no_session_id"`
	NodesDupSession    int `json:"nodes_sharing_a_session_with_an_earlier_node"`
	NodesNotInGraph    int `json:"node_records_absent_from_graph_json"`
	GraphNodesNeverRan int `json:"graph_nodes_with_no_state_record"`

	TranscriptsParsed     int `json:"transcript_files_parsed"`
	TranscriptsUnreadable int `json:"transcript_files_unreadable"`
	BadJSONLines          int `json:"jsonl_lines_that_were_not_valid_json"`
	BashCalls             int `json:"bash_tool_use_blocks"`
	ToolResults           int `json:"tool_result_blocks"`
	OrphanResults         int `json:"tool_results_with_no_matching_tool_use"`
	OrphanDenials         int `json:"tool_results_with_no_matching_tool_use_that_were_denials"`

	BashDenials       int `json:"denied_bash_calls_policy"`
	BashUserRejects   int `json:"denied_bash_calls_user_rejection"`
	NonBashDenials    int `json:"denials_of_a_non_bash_tool"`
	SidechainDenials  int `json:"denials_inside_a_subagent_sidechain"`
	UnrecognisedWords int `json:"errored_results_with_an_unrecognised_wording"`

	PolicyDisagrees   int `json:"nodes_where_tool_policies_and_graph_json_disagree"`
	PolicyMissing     int `json:"nodes_with_no_tool_policies_entry"`
	DenialsBashByName int `json:"denials_under_a_bare_bash_in_disallowed_tools"`

	HeredocsConsumed int `json:"heredoc_bodies_consumed_as_data"`
	GrammarDropped   int `json:"pieces_dropped_as_pure_shell_grammar"`
}

// ---------------------------------------------------------------------------
// JSON report shapes — named structs so field order is stable across runs
// ---------------------------------------------------------------------------

type jsonSubCommand struct {
	Index     int    `json:"index"`
	Text      string `json:"text"`
	FirstWord string `json:"first_word"`
	Granted   bool   `json:"granted"`
	Depth     int    `json:"substitution_depth"`
}

type jsonDenial struct {
	RunID               string           `json:"run_id"`
	NodeID              string           `json:"node_id"`
	SessionID           string           `json:"session_id"`
	CWD                 string           `json:"cwd"`
	Command             string           `json:"command"`
	Grants              []string         `json:"grants"`
	GrantsSource        string           `json:"grants_source"`
	GrantsKnown         bool             `json:"grants_known"`
	PolicyPresent       bool             `json:"tool_policies_present"`
	PolicyGrants        []string         `json:"tool_policies_allowed_tools"`
	PolicyAgrees        bool             `json:"tool_policies_agrees_with_graph_json"`
	BashDeniedByName    bool             `json:"bare_bash_in_disallowed_tools"`
	Class               string           `json:"class"`
	DenialWording       string           `json:"denial_wording"`
	Compound            bool             `json:"compound"`
	PathScope           string           `json:"path_scope"`
	SubCommands         []jsonSubCommand `json:"sub_commands"`
	OffendingSubCommand []string         `json:"offending_sub_commands"`
	OffendingFirstWords []string         `json:"offending_first_words"`
}

type jsonTailEntry struct {
	FirstWord string `json:"first_word"`
	Count     int    `json:"count"`
}

type jsonMissingData struct {
	NodesWithNoSessionID       []string `json:"nodes_with_no_session_id"`
	NodesWithNoTranscriptFile  []string `json:"nodes_with_session_id_but_no_transcript_file"`
	NodesSharingASession       []string `json:"nodes_sharing_a_session_with_an_earlier_node"`
	NodeRecordsAbsentFromGraph []string `json:"node_records_absent_from_graph_json"`
	GraphNodesThatNeverRan     []string `json:"graph_nodes_with_no_state_record"`
	RunsWithNoGraphJSON        []string `json:"runs_with_no_graph_json"`
	UnreadableOrUnparseable    []string `json:"unreadable_or_unparseable_files"`
	UnrecognisedErrorWordings  []string `json:"unrecognised_error_wordings_sample_max_40_first_120_chars"`
	PolicyDisagreements        []string `json:"tool_policies_vs_graph_json_disagreements"`
	InFlightCrossCheck         []string `json:"in_flight_cross_check_reported_only"`
}

type jsonReport struct {
	Measurement string `json:"measurement"`
	Question    string `json:"question"`
	Snapshot    string `json:"snapshot_taken_at"`

	RunsRoot        string `json:"runs_root"`
	TranscriptsRoot string `json:"transcripts_root"`
	ExcludedRunID   string `json:"excluded_run_id"`
	NewestRunID     string `json:"newest_run_id"`
	ClaudeVersion   string `json:"claude_version_that_produced_the_corpus"`

	Caveat     string `json:"caveat"`
	Assumption string `json:"assumed_grant_matching_rule"`
	Splitter   string `json:"sub_command_splitter"`

	Counts      counters          `json:"counts"`
	Classes     map[string]int    `json:"classes"`
	ClassTitles map[string]string `json:"class_titles"`

	Denominator   int             `json:"denominator_denied_bash_calls_in_planned_nodes"`
	CompoundCount int             `json:"denied_commands_that_were_compound"`
	ClassATail    []jsonTailEntry `json:"class_a_offending_first_words"`

	PathScopeXTab map[string]map[string]int `json:"path_scope_cross_tab"`
	ClassCByScope map[string]int            `json:"class_c_by_path_scope"`

	MissingData jsonMissingData `json:"missing_data"`
	DeniedCalls []jsonDenial    `json:"denied_calls"`
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

const caveatText = "THE DENIAL TEXT CARRIES NO REASON CODE: it is byte-identical for a compound " +
	"call, a simple out-of-scope call, and a sandbox refusal. Every class here is a " +
	"CORRELATION between command shape and denial, never a causal reading of the denial."

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		// Nothing downstream can proceed without a home directory: the runs root
		// and the transcript root are both under it. This is the only condition
		// that aborts before any measuring happens.
		fmt.Fprintln(os.Stderr, "cannot resolve home dir:", err)
		os.Exit(1)
	}
	runsRoot := filepath.Join(home, ".oh-my-graph", "runs")
	if v := os.Getenv("OMG_HOME"); v != "" {
		runsRoot = filepath.Join(v, "runs")
	}
	projectsRoot := filepath.Join(home, ".claude", "projects")

	var c counters
	var denials []denial
	var noSessionNodes []string
	var noTranscriptNodes []string
	var notInGraphNodes []string
	var dupSessionNodes []string
	var neverRanNodes []string
	var noGraphRuns []string
	var unreadable []string
	var unrecognised []string
	var policyDiffs []string
	var inFlightDiag []string
	var corpusNotes []string

	transcriptIndex, idxErr := indexTranscripts(projectsRoot)
	if idxErr != nil {
		corpusNotes = append(corpusNotes, "transcript index walk reported: "+idxErr.Error())
	}

	var runIDs []string
	entries, err := os.ReadDir(runsRoot)
	if err != nil {
		// An absent or unreadable runs root is an EMPTY CORPUS, not a program
		// error. The brief is explicit: exit non-zero only on a genuine
		// programming error, never merely because the corpus is small. Both
		// output files are still written, reporting zero.
		corpusNotes = append(corpusNotes, "runs root could not be read ("+err.Error()+
			") — the corpus is empty and every count below is zero")
	}
	for _, e := range entries {
		if e.IsDir() {
			runIDs = append(runIDs, e.Name())
		}
	}
	sort.Strings(runIDs)
	c.RunDirs = len(runIDs)

	newestRunID := ""
	if len(runIDs) > 0 {
		newestRunID = runIDs[len(runIDs)-1]
	}

	seenSession := map[string]string{} // sessionID -> "runID/nodeID" of first claimant

	for _, runID := range runIDs {
		runDir := filepath.Join(runsRoot, runID)

		if runID == excludedRunID {
			c.ExcludedRuns++
			continue
		}

		raw, err := os.ReadFile(filepath.Join(runDir, "state.json"))
		if err != nil {
			c.RunsNoStateJSON++
			continue
		}
		var st stateFile
		if err := json.Unmarshal(raw, &st); err != nil {
			c.RunsUnreadableState++
			unreadable = append(unreadable, runID+": state.json: "+skipNoParse+": "+err.Error())
			continue
		}

		graphPath := filepath.Join(runDir, "graph.json")
		if _, gstatErr := os.Stat(graphPath); gstatErr != nil {
			// Reported for its own sake: it is the reason a hand-written run can
			// never satisfy the planned predicate, and the brief asks for the
			// count separately from "not planned".
			c.RunsNoGraphJSON++
			noGraphRuns = append(noGraphRuns, runID+": "+skipNoGraph)
		}

		if st.GraphSourcePath == "" || !sameFile(st.GraphSourcePath, graphPath) {
			c.UnplannedRuns++
			continue
		}
		c.PlannedRuns++

		// GRANTS COME FROM THAT RUN'S OWN graph.json, as briefed — not from the
		// state.json copy — so a snapshot that drifted from the graph stays
		// visible rather than being papered over. state.tool_policies is read
		// alongside it purely as a cross-check; see nodePolicy's doc comment.
		grantsByNode := map[string][]string{}
		graphKnown := map[string]bool{}
		graphRaw, gerr := os.ReadFile(graphPath)
		if gerr == nil {
			var gf graphFile
			if json.Unmarshal(graphRaw, &gf) == nil {
				for _, n := range gf.Nodes {
					grantsByNode[n.ID] = n.AllowedTools
					graphKnown[n.ID] = true
				}
			} else {
				c.RunsBadGraphJSON++
				unreadable = append(unreadable, runID+": graph.json: "+skipNoParse)
			}
		}

		var nodeIDs []string
		recordedVerdicts := map[string]string{}
		for id, rec := range st.Nodes {
			nodeIDs = append(nodeIDs, id)
			recordedVerdicts[id] = rec.Verdict
		}
		sort.Strings(nodeIDs)

		graphIDs := sortedKeys(graphKnown)
		for _, id := range graphIDs {
			if _, ran := st.Nodes[id]; !ran {
				c.GraphNodesNeverRan++
				neverRanNodes = append(neverRanNodes, runID+"/"+id+" — "+noteNoRecord)
			}
		}
		// Cross-check only; changes no count. See inFlight's doc comment.
		if running, noRecord := inFlight(graphIDs, recordedVerdicts); running {
			inFlightDiag = append(inFlightDiag, fmt.Sprintf(
				"%s | %d graph nodes, %d recorded | no record: %s",
				runID, len(graphIDs), len(st.Nodes), strings.Join(noRecord, " ")))
		}

		for _, nodeID := range nodeIDs {
			nr := st.Nodes[nodeID]
			c.NodeRecords++

			if !graphKnown[nodeID] {
				c.NodesNotInGraph++
				notInGraphNodes = append(notInGraphNodes, runID+"/"+nodeID)
			}

			policy, policyKnown := st.ToolPolicies[nodeID]
			if !policyKnown {
				c.PolicyMissing++
			} else if graphKnown[nodeID] && !sameGrantSet(grantsByNode[nodeID], policy.AllowedTools) {
				c.PolicyDisagrees++
				policyDiffs = append(policyDiffs, fmt.Sprintf("%s/%s graph.json=%s tool_policies=%s",
					runID, nodeID, formatGrants(grantsByNode[nodeID]), formatGrants(policy.AllowedTools)))
			}
			bashByName := deniesBareBash(policy.DisallowedTools)

			if nr.SessionID == "" {
				c.NodesNoSessionID++
				noSessionNodes = append(noSessionNodes, runID+"/"+nodeID+" — "+noteNoSess)
				continue
			}
			if owner, dup := seenSession[nr.SessionID]; dup {
				c.NodesDupSession++
				dupSessionNodes = append(dupSessionNodes,
					runID+"/"+nodeID+" shares session "+nr.SessionID+" with "+owner)
				continue
			}
			seenSession[nr.SessionID] = runID + "/" + nodeID

			paths := transcriptIndex[nr.SessionID+".jsonl"]
			if len(paths) == 0 {
				c.NodesNoTranscript++
				noTranscriptNodes = append(noTranscriptNodes,
					runID+"/"+nodeID+" (session "+nr.SessionID+") — "+noteMissing)
				continue
			}
			c.NodesTranscriptOK++

			for _, tp := range paths {
				pt, err := parseTranscript(tp)
				if err != nil {
					c.TranscriptsUnreadable++
					unreadable = append(unreadable, runID+"/"+nodeID+": "+noteUnread+": "+err.Error())
					continue
				}
				c.TranscriptsParsed++
				c.BadJSONLines += pt.bad
				for _, u := range pt.uses {
					if u.name == "Bash" {
						c.BashCalls++
					}
				}
				sort.Slice(pt.results, func(i, j int) bool { return pt.results[i].seq < pt.results[j].seq })
				for _, r := range pt.results {
					c.ToolResults++
					shape := classifyError(r)

					u, ok := pt.uses[r.toolUseID]
					if !ok {
						// A tool_result naming no tool_use: a data defect.
						// Counted and reported, never silently dropped.
						c.OrphanResults++
						if shape == shapePolicyDenial || shape == shapeUserRejection {
							c.OrphanDenials++
						}
						continue
					}
					if shape == shapeUnrecognised {
						// A wording this program does not know. It surfaces as a
						// number and a sample instead of being absorbed into
						// "ordinary failure".
						c.UnrecognisedWords++
						if len(unrecognised) < 40 {
							unrecognised = append(unrecognised,
								u.name+": "+firstChars(strings.TrimSpace(r.text), 120))
						}
						continue
					}
					if shape != shapePolicyDenial && shape != shapeUserRejection {
						continue
					}
					if u.name != "Bash" {
						c.NonBashDenials++
						continue
					}
					if u.sidechain || r.sidechain {
						// A sub-agent runs under its own policy, not this
						// node's grant list, so it cannot be judged against it.
						c.SidechainDenials++
						continue
					}
					if shape == shapeUserRejection {
						// An interactive human "no". It cannot happen in an
						// unattended dontAsk node, so it is counted apart from
						// the denominator rather than silently joining it.
						c.BashUserRejects++
					} else {
						c.BashDenials++
					}
					if bashByName {
						c.DenialsBashByName++
					}

					grants, known := grantsByNode[nodeID]
					d := denial{
						runID: runID, nodeID: nodeID, sessionID: nr.SessionID,
						command: u.command, grants: grants,
						grantsKnown:  known && graphKnown[nodeID],
						policyGrants: policy.AllowedTools,
						policyKnown:  policyKnown,
						policyAgrees: policyKnown && sameGrantSet(grants, policy.AllowedTools),
						bashByName:   bashByName,
						split:        splitCommand(u.command),
						cwd:          u.cwd,
						shape:        shape,
					}
					c.HeredocsConsumed += d.split.heredocs
					c.GrammarDropped += d.split.grammarDrops
					d.compound = len(d.split.subs) > 1
					d.scope = classifyPathScope(d.split.subs, u.cwd, home)
					classify(&d)
					denials = append(denials, d)
				}
			}
		}
	}

	sort.Slice(denials, func(i, j int) bool {
		if denials[i].runID != denials[j].runID {
			return denials[i].runID < denials[j].runID
		}
		if denials[i].nodeID != denials[j].nodeID {
			return denials[i].nodeID < denials[j].nodeID
		}
		return denials[i].command < denials[j].command
	})

	missing := jsonMissingData{
		NodesWithNoSessionID:       sortedCopy(noSessionNodes),
		NodesWithNoTranscriptFile:  sortedCopy(noTranscriptNodes),
		NodesSharingASession:       sortedCopy(dupSessionNodes),
		NodeRecordsAbsentFromGraph: sortedCopy(notInGraphNodes),
		GraphNodesThatNeverRan:     sortedCopy(neverRanNodes),
		RunsWithNoGraphJSON:        sortedCopy(noGraphRuns),
		UnreadableOrUnparseable:    sortedCopy(unreadable),
		UnrecognisedErrorWordings:  sortedCopy(unrecognised),
		PolicyDisagreements:        sortedCopy(policyDiffs),
		InFlightCrossCheck:         sortedCopy(inFlightDiag),
	}

	// The .txt and stdout are deterministic; the timestamp lives in the .json
	// alone so a byte-comparison of two runs stays a usable check.
	snapshot := time.Now().UTC().Format(time.RFC3339)

	human := renderHuman(c, denials, newestRunID, runsRoot, projectsRoot, missing, corpusNotes)
	machine, err := renderJSON(snapshot, c, denials, newestRunID, runsRoot, projectsRoot, missing)
	if err != nil {
		// Marshalling our own structs cannot fail on well-formed data; if it
		// does, that is a programming error and the only kind worth exiting on.
		fmt.Fprintln(os.Stderr, "cannot marshal JSON report:", err)
		os.Exit(1)
	}

	dir, dirNote := outputDir()
	if dirNote != "" {
		human = dirNote + "\n" + human
	}
	fmt.Print(human)

	failed := false
	for _, f := range []struct {
		name string
		data []byte
	}{
		{outTextName, []byte(human)},
		{outJSONName, machine},
	} {
		p := filepath.Join(dir, f.name)
		if err := os.WriteFile(p, f.data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "cannot write "+p+":", err)
			failed = true
			continue
		}
		fmt.Fprintln(os.Stderr, "wrote "+p)
	}
	if failed {
		// The program's whole contract is the two files. Not writing them is a
		// real failure, unlike an empty corpus.
		os.Exit(1)
	}
}

// outputDir puts the two result files next to this program when the working
// directory is the repo root (the documented way to run it), and falls back to
// the working directory itself with a printed note rather than creating
// directories somewhere unexpected or failing outright.
func outputDir() (string, string) {
	const want = "docs/measurements"
	if fi, err := os.Stat(want); err == nil && fi.IsDir() {
		return want, ""
	}
	return ".", "NOTE: ./docs/measurements is not a directory from here, so the two result " +
		"files were written into the working directory instead. Run this program from the " +
		"repository root to place them alongside the source."
}

func sortedKeys(m map[string]bool) []string {
	var ids []string
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// sameGrantSet compares two grant lists as sets, so an ordering difference
// between graph.json and state.tool_policies is not reported as a disagreement.
func sameGrantSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := sortedCopy(a), sortedCopy(b)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// deniesBareBash reports whether a disallowed_tools list contains the BARE name
// Bash, which DESIGN.md:113-116 measured as beating every prior allow. A scoped
// entry like Bash(*) enforces nothing and is deliberately not counted.
func deniesBareBash(disallowed []string) bool {
	for _, d := range disallowed {
		if strings.TrimSpace(d) == "Bash" {
			return true
		}
	}
	return false
}

func firstChars(s string, n int) string {
	r := strings.NewReplacer("\n", "\\n", "\t", "\\t", "\r", "\\r")
	s = r.Replace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// indexTranscripts walks ~/.claude/projects once and maps "<session>.jsonl" to
// every path carrying that name. A session id is looked up BY FILENAME because
// the containing directory is the node's cwd slug, which is not derivable from
// the run id — a transcript is filed under the checkout the node ran in, not
// under the runs root, and rebuilding that slug is how a measurement invents a
// missing file. A session id is a uuid, so the name is effectively exact;
// multiple matches are possible in principle and are all scanned. Only *.jsonl
// files count: a tool-results/<id>.txt sidecar holding a spilled oversized
// result is NOT a transcript, and one such file alone carries 324 copies of the
// denial sentence.
func indexTranscripts(root string) (map[string][]string, error) {
	idx := map[string][]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: skip, do not abort the walk
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		idx[d.Name()] = append(idx[d.Name()], p)
		return nil
	})
	for k := range idx {
		sort.Strings(idx[k])
	}
	return idx, err
}

func pct(n, d int) string {
	if d == 0 {
		return "  n/a"
	}
	return fmt.Sprintf("%5.1f%%", 100*float64(n)/float64(d))
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// countTail returns the class-(A) offending first words, most frequent first,
// ties broken alphabetically so the order is deterministic.
func countTail(denials []denial) []jsonTailEntry {
	freq := map[string]int{}
	for _, d := range denials {
		if d.bucket != bucketA {
			continue
		}
		for _, w := range d.offenderWord {
			freq[w]++
		}
	}
	out := make([]jsonTailEntry, 0, len(freq))
	for k, n := range freq {
		out = append(out, jsonTailEntry{FirstWord: k, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].FirstWord < out[j].FirstWord
	})
	return out
}

func bucketCounts(denials []denial) map[bucket][]denial {
	by := map[bucket][]denial{}
	for _, d := range denials {
		by[d.bucket] = append(by[d.bucket], d)
	}
	return by
}

var scopeOrder = []pathScope{scopeReachesOutside, scopeAllInside, scopeNoPathArgs}

func renderJSON(snapshot string, c counters, denials []denial, newestRunID,
	runsRoot, projectsRoot string, missing jsonMissingData) ([]byte, error) {

	by := bucketCounts(denials)
	classes := map[string]int{}
	titles := map[string]string{}
	for _, b := range bucketOrder {
		classes[string(b)] = len(by[b])
		titles[string(b)] = bucketTitle[b]
	}

	xtab := map[string]map[string]int{}
	for _, s := range scopeOrder {
		comp, simp := 0, 0
		for _, d := range denials {
			if d.scope != s {
				continue
			}
			if d.compound {
				comp++
			} else {
				simp++
			}
		}
		xtab[s.String()] = map[string]int{"compound": comp, "simple": simp}
	}
	cByScope := map[string]int{}
	for _, s := range scopeOrder {
		n := 0
		for _, d := range by[bucketC] {
			if d.scope == s {
				n++
			}
		}
		cByScope[s.String()] = n
	}

	compound := 0
	for _, d := range denials {
		if d.compound {
			compound++
		}
	}

	calls := make([]jsonDenial, 0, len(denials))
	for _, d := range denials {
		subs := make([]jsonSubCommand, 0, len(d.split.subs))
		for i, sc := range d.split.subs {
			granted := false
			if i < len(d.granted) {
				granted = d.granted[i]
			}
			subs = append(subs, jsonSubCommand{
				Index: i + 1, Text: sc.text, FirstWord: sc.firstWord(),
				Granted: granted, Depth: sc.depth,
			})
		}
		calls = append(calls, jsonDenial{
			RunID: d.runID, NodeID: d.nodeID, SessionID: d.sessionID, CWD: d.cwd,
			Command:             d.command,
			Grants:              nonNil(d.grants),
			GrantsSource:        "graph.json",
			GrantsKnown:         d.grantsKnown,
			PolicyPresent:       d.policyKnown,
			PolicyGrants:        nonNil(d.policyGrants),
			PolicyAgrees:        d.policyAgrees,
			BashDeniedByName:    d.bashByName,
			Class:               string(d.bucket),
			DenialWording:       d.shape.String(),
			Compound:            d.compound,
			PathScope:           d.scope.String(),
			SubCommands:         subs,
			OffendingSubCommand: nonNil(d.offenders),
			OffendingFirstWords: nonNil(d.offenderWord),
		})
	}

	rep := jsonReport{
		Measurement: "0213b-compound-commands",
		Question: "Does a compound command defeat a prefix grant? (#213) — every DENIED Bash " +
			"call inside a PLANNED node, classified A/B/C.",
		Snapshot:        snapshot,
		RunsRoot:        runsRoot,
		TranscriptsRoot: projectsRoot,
		ExcludedRunID:   excludedRunID,
		NewestRunID:     newestRunID,
		ClaudeVersion:   "UNKNOWN — no run record carries it; see the assumption text",
		Caveat:          caveatText,
		Assumption:      assumptionText,
		Splitter:        splitterText,
		Counts:          c,
		Classes:         classes,
		ClassTitles:     titles,
		Denominator:     len(denials),
		CompoundCount:   compound,
		ClassATail:      countTail(denials),
		PathScopeXTab:   xtab,
		ClassCByScope:   cByScope,
		MissingData:     missing,
		DeniedCalls:     calls,
	}
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func renderHuman(c counters, denials []denial, newestRunID, runsRoot, projectsRoot string,
	missing jsonMissingData, corpusNotes []string) string {

	w := &strings.Builder{}
	line := strings.Repeat("=", 78)
	sub := strings.Repeat("-", 78)
	by := bucketCounts(denials)
	total := len(denials)

	fmt.Fprintf(w, "%s\n", line)
	fmt.Fprintf(w, "0213b — DENIED BASH CALLS IN PLANNED NODES, AND WHY\n")
	fmt.Fprintf(w, "%s\n\n", line)

	fmt.Fprintf(w, "%s\n\n", wrap(caveatText, 78))
	fmt.Fprintf(w, "SNAPSHOT MARKER: %d run directories, newest run id %s\n", c.RunDirs, newestRunID)
	fmt.Fprintf(w, "  runs root      %s\n", runsRoot)
	fmt.Fprintf(w, "  transcripts    %s\n", projectsRoot)
	fmt.Fprintf(w, "  EXCLUDED RUN   %s — this lane's own run (graph\n", excludedRunID)
	fmt.Fprintf(w, "                 `compound-commands-defeat-grants`). It is IN FLIGHT while this\n")
	fmt.Fprintf(w, "                 program runs, so measuring it would measure this measurement.\n")
	fmt.Fprintf(w, "                 It is also the lexically newest run id, which is the trap.\n")
	fmt.Fprintf(w, "  MACHINE COPY   %s carries every count below, one record per denied call, and\n", outJSONName)
	fmt.Fprintf(w, "                 the wall-clock snapshot timestamp. THIS file carries no\n")
	fmt.Fprintf(w, "                 timestamp on purpose: it is byte-deterministic, so re-running\n")
	fmt.Fprintf(w, "                 and diffing it is a real check on the corpus.\n")
	for _, n := range corpusNotes {
		fmt.Fprintf(w, "  NOTE: %s\n", n)
	}

	fmt.Fprintf(w, "\n%s\n1. THE STATED ASSUMPTION\n%s\n\n", sub, sub)
	fmt.Fprintf(w, "%s\n", assumptionText)

	fmt.Fprintf(w, "\n%s\n2. THE SPLITTER\n%s\n\n", sub, sub)
	fmt.Fprintf(w, "Splits on | || ; && newline, $( ) and backticks, with a hand-written scanner that\n")
	fmt.Fprintf(w, "respects single and double quotes and CONSUMES HEREDOC BODIES AS DATA. Without\n")
	fmt.Fprintf(w, "that last part a commit message or a Go file written via <<EOF is shredded into\n")
	fmt.Fprintf(w, "fake sub-commands that all land in bucket (A) — the bucket this measurement\n")
	fmt.Fprintf(w, "exists to size. The full statement of what it does and does NOT handle is in\n")
	fmt.Fprintf(w, "%s under \"sub_command_splitter\".\n\n", outJSONName)
	fmt.Fprintf(w, "  heredoc bodies consumed as data (not as shell) %6d\n", c.HeredocsConsumed)
	fmt.Fprintf(w, "  pieces dropped as pure shell grammar           %6d\n", c.GrammarDropped)

	fmt.Fprintf(w, "\n%s\n3. PLANNED-RUN PREDICATE\n%s\n\n", sub, sub)
	fmt.Fprintf(w, "A run is PLANNED when state.json's graph_source_path, after filepath.Clean and\n")
	fmt.Fprintf(w, "symlink resolution, is the same file as that run's own graph.json by os.SameFile\n")
	fmt.Fprintf(w, "(device+inode, so /var vs /private/var and any other spelling survive).\n\n")
	fmt.Fprintf(w, "PROVENANCE — sameFile() is COPIED UNCHANGED from two files where it is\n")
	fmt.Fprintf(w, "byte-identical, ON TWO DIFFERENT BRANCHES:\n")
	fmt.Fprintf(w, "  docs/measurements/0213-tool-grant-predicate.go     commit b1a55ba (#213)\n")
	fmt.Fprintf(w, "      branch measure/tool-grant-predicate\n")
	fmt.Fprintf(w, "  docs/measurements/0218-denied-nodes-that-passed.go commit 0736635 (#218)\n")
	fmt.Fprintf(w, "      branch measure/denied-nodes\n")
	fmt.Fprintf(w, "Neither commit is an ancestor of this branch's HEAD, which is why a Glob of the\n")
	fmt.Fprintf(w, "working tree reports both files absent; both were read out of the object store.\n")
	fmt.Fprintf(w, "(An earlier draft of this report placed BOTH on measure/denied-nodes. That was\n")
	fmt.Fprintf(w, "false and `git branch -a --contains b1a55ba` disproves it in one command.)\n")

	fmt.Fprintf(w, "\n%s\n4. CORPUS COUNTS\n%s\n\n", sub, sub)
	fmt.Fprintf(w, "  run directories (true directory count)        %6d\n", c.RunDirs)
	fmt.Fprintf(w, "  excluded (this lane's own run)                %6d\n", c.ExcludedRuns)
	fmt.Fprintf(w, "  no state.json at all                         %6d\n", c.RunsNoStateJSON)
	fmt.Fprintf(w, "  state.json present but unparseable           %6d\n", c.RunsUnreadableState)
	fmt.Fprintf(w, "  NOT planned (hand-written `run`)             %6d\n", c.UnplannedRuns)
	fmt.Fprintf(w, "  PLANNED runs (the measured population)       %6d\n", c.PlannedRuns)
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "  node records in those planned runs           %6d\n", c.NodeRecords)
	fmt.Fprintf(w, "  ... with a transcript parsed                 %6d\n", c.NodesTranscriptOK)
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "  transcript files parsed                      %6d\n", c.TranscriptsParsed)
	fmt.Fprintf(w, "  Bash tool_use blocks seen in them            %6d\n", c.BashCalls)
	fmt.Fprintf(w, "  tool_result blocks seen                      %6d\n", c.ToolResults)

	fmt.Fprintf(w, "\n%s\n5. THE DENOMINATOR\n%s\n\n", sub, sub)
	fmt.Fprintf(w, "  DENIED Bash calls in planned nodes           %6d   <- THE DENOMINATOR\n", total)
	fmt.Fprintf(w, "  (a denial is a tool_result with is_error true whose text OPENS WITH a known\n")
	fmt.Fprintf(w, "   denial template, joined to its tool_use by tool_use_id, whose tool is Bash)\n\n")
	fmt.Fprintf(w, "  ... policy denial, unattended                %6d\n", c.BashDenials)
	fmt.Fprintf(w, "        \"%s...\"\n", denialPolicyPrefix)
	fmt.Fprintf(w, "  ... refused by an INTERACTIVE HUMAN          %6d\n", c.BashUserRejects)
	fmt.Fprintf(w, "        \"%s\"\n", userRejectPrefix)
	fmt.Fprintf(w, "        A planned node runs unattended under dontAsk with nobody to press\n")
	fmt.Fprintf(w, "        \"no\", so this should be 0. It is counted apart rather than assumed\n")
	fmt.Fprintf(w, "        away; each record carries its wording in %s.\n\n", outJSONName)
	fmt.Fprintf(w, "  denials of a NON-Bash tool (excluded)        %6d\n", c.NonBashDenials)
	fmt.Fprintf(w, "  denials inside a sub-agent sidechain (excl.) %6d\n", c.SidechainDenials)
	fmt.Fprintf(w, "    a sidechain runs under its own policy, not the node's grant list, so it\n")
	fmt.Fprintf(w, "    cannot be judged against that list.\n")

	fmt.Fprintf(w, "\n%s\n6. MISSING DATA AND DATA DEFECTS — reported, never dropped\n%s\n\n", sub, sub)
	fmt.Fprintf(w, "  nodes with a session_id but NO TRANSCRIPT     %6d\n", c.NodesNoTranscript)
	fmt.Fprintf(w, "  nodes with NO session_id at all               %6d\n", c.NodesNoSessionID)
	fmt.Fprintf(w, "  nodes sharing a session with an earlier node  %6d\n", c.NodesDupSession)
	fmt.Fprintf(w, "  node records absent from their own graph.json %6d   -> bucket (E)\n", c.NodesNotInGraph)
	fmt.Fprintf(w, "  graph nodes with no state record (never ran)  %6d\n", c.GraphNodesNeverRan)
	fmt.Fprintf(w, "  runs with NO graph.json                       %6d   (%s)\n", c.RunsNoGraphJSON, skipNoGraph)
	fmt.Fprintf(w, "  runs whose graph.json would not parse         %6d\n", c.RunsBadGraphJSON)
	fmt.Fprintf(w, "  transcripts that could not be read            %6d\n", c.TranscriptsUnreadable)
	fmt.Fprintf(w, "  JSONL lines that were not valid JSON          %6d\n", c.BadJSONLines)
	fmt.Fprintf(w, "  tool_result with NO matching tool_use         %6d\n", c.OrphanResults)
	fmt.Fprintf(w, "    ... of which were denial-shaped             %6d\n", c.OrphanDenials)
	fmt.Fprintf(w, "    A join by adjacency would have attributed each of these to whatever\n")
	fmt.Fprintf(w, "    command happened to precede it.\n")
	fmt.Fprintf(w, "  errored results with an UNRECOGNISED WORDING  %6d\n", c.UnrecognisedWords)
	fmt.Fprintf(w, "    Neither <tool_use_error>-wrapped nor opening with a known denial template.\n")
	fmt.Fprintf(w, "    A third wording would silently shrink the denominator, so it is counted.\n")
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "  GRANT-RECORD CROSS-CHECK (classification uses graph.json, as briefed):\n")
	fmt.Fprintf(w, "  nodes with no state.tool_policies entry       %6d\n", c.PolicyMissing)
	fmt.Fprintf(w, "  nodes where tool_policies DISAGREES with graph %5d\n", c.PolicyDisagrees)
	fmt.Fprintf(w, "    DESIGN.md:2198-2199 records tool_policies as the durable record — a grant\n")
	fmt.Fprintf(w, "    can be invisible in graph.json. A non-zero figure here means the class of\n")
	fmt.Fprintf(w, "    every affected call was decided on the weaker of the two records.\n")
	fmt.Fprintf(w, "  denied calls under a bare `Bash` in disallowed %5d\n", c.DenialsBashByName)
	fmt.Fprintf(w, "    DESIGN.md:113-116: a bare-name deny BEATS every allow the node holds. Any\n")
	fmt.Fprintf(w, "    such call is explained by the deny, whatever its command shape.\n\n")

	writeList(w, "nodes with a session_id but no transcript file", missing.NodesWithNoTranscriptFile)
	writeList(w, "nodes with no session_id", missing.NodesWithNoSessionID)
	writeList(w, "nodes sharing a session with an earlier node (parsed once, attributed to the first)",
		missing.NodesSharingASession)
	writeList(w, "node records absent from their own graph.json", missing.NodeRecordsAbsentFromGraph)
	writeList(w, "graph nodes that never ran", missing.GraphNodesThatNeverRan)
	writeList(w, "runs with no graph.json", missing.RunsWithNoGraphJSON)
	writeList(w, "unreadable / unparseable files", missing.UnreadableOrUnparseable)
	writeList(w, "unrecognised error wordings (sample of at most 40; first 120 chars each — the "+
		"COUNT above is complete, this list is not)", missing.UnrecognisedErrorWordings)
	writeList(w, "tool_policies vs graph.json disagreements", missing.PolicyDisagreements)
	writeList(w, "CROSS-CHECK, #218's in-flight test (reported only; changes NO count here, "+
		"since this measurement excludes by run id instead)", missing.InFlightCrossCheck)

	fmt.Fprintf(w, "\n%s\n7. CLASSIFICATION\n%s\n\n", sub, sub)
	fmt.Fprintf(w, "  denominator = %d denied Bash calls in planned nodes\n\n", total)
	for _, b := range bucketOrder {
		fmt.Fprintf(w, "  %-6s %5d  %s   %s\n", string(b), len(by[b]), pct(len(by[b]), total), bucketTitle[b])
	}
	sum := 0
	for _, b := range bucketOrder {
		sum += len(by[b])
	}
	fmt.Fprintf(w, "  %-6s %5d  %s   (buckets sum to the denominator)\n", "TOTAL", sum, pct(sum, total))
	compound := 0
	for _, d := range denials {
		if d.compound {
			compound++
		}
	}
	fmt.Fprintf(w, "\n  for reference, independent of bucket: %d of %d denied commands (%s) were\n",
		compound, total, strings.TrimSpace(pct(compound, total)))
	fmt.Fprintf(w, "  COMPOUND (more than one sub-command after splitting).\n")
	if n := len(by[bucketC]); n > 0 {
		noPath := 0
		for _, d := range by[bucketC] {
			if d.scope == scopeNoPathArgs {
				noPath++
			}
		}
		fmt.Fprintf(w, "\n  ON BUCKET (C), the one that would overturn the hypothesis: %d call(s) held a\n", n)
		fmt.Fprintf(w, "  grant for EVERY sub-command and were denied anyway. The obvious suspect is\n")
		fmt.Fprintf(w, "  path scope — a simple, in-grant `ls -d ~/.oh-my-graph/runs` is denied while\n")
		fmt.Fprintf(w, "  the same `ls` inside the working directory is allowed, which this lane\n")
		fmt.Fprintf(w, "  observed first-hand — but it does not cover (C): %d of the %d had NO PATH\n", noPath, n)
		fmt.Fprintf(w, "  ARGUMENTS AT ALL. Those are unexplained by both the grant list and the path\n")
		fmt.Fprintf(w, "  heuristic, and the denial text will never say why. §9 has the cross-tab.\n")
	}

	fmt.Fprintf(w, "\n%s\n8. CLASS (A) OFFENDING SUB-COMMANDS, BY FREQUENCY\n%s\n\n", sub, sub)
	tail := countTail(denials)
	if len(tail) == 0 {
		fmt.Fprintf(w, "  (none — class (A) is empty)\n")
	} else {
		fmt.Fprintf(w, "  %d distinct first word(s) turned an otherwise-granted command into (A):\n\n", len(tail))
		for _, e := range tail {
			fmt.Fprintf(w, "  %5d  %s\n", e.Count, e.FirstWord)
		}
	}

	fmt.Fprintf(w, "\n%s\n9. SUPPLEMENTARY — THE CONFOUND\n%s\n\n", sub, sub)
	fmt.Fprintf(w, "Compound-ness is not the only thing that varies across these denials. A simple,\n")
	fmt.Fprintf(w, "in-grant `ls` is denied when its path argument lies outside the node's working\n")
	fmt.Fprintf(w, "directory and allowed when it lies inside — observed again while this program\n")
	fmt.Fprintf(w, "was being written, where `git branch --contains X` was allowed and\n")
	fmt.Fprintf(w, "`git -C /tmp/... branch --contains X` was denied in the same session. If path\n")
	fmt.Fprintf(w, "scope moves with compound-ness in this corpus, an A-vs-B ratio cannot separate\n")
	fmt.Fprintf(w, "them. This cross-tab is printed so a reader can see whether it does.\n\n")
	fmt.Fprintf(w, "  %-22s %10s %10s\n", "path args", "compound", "simple")
	for _, s := range scopeOrder {
		comp, simp := 0, 0
		for _, d := range denials {
			if d.scope != s {
				continue
			}
			if d.compound {
				comp++
			} else {
				simp++
			}
		}
		fmt.Fprintf(w, "  %-22s %10d %10d\n", s.String(), comp, simp)
	}
	fmt.Fprintf(w, "\n  bucket (C) — every sub-command granted, denied anyway — by path scope:\n")
	for _, s := range scopeOrder {
		n := 0
		for _, d := range by[bucketC] {
			if d.scope == s {
				n++
			}
		}
		fmt.Fprintf(w, "  %-22s %10d\n", s.String(), n)
	}
	fmt.Fprintf(w, "\n  (path scope is a heuristic on argument SHAPE — a token starting with / ~ ./ or\n")
	fmt.Fprintf(w, "   ../ — resolved against the node's cwd, compared by path segment. It is not\n")
	fmt.Fprintf(w, "   what the shell would really open.)\n")

	fmt.Fprintf(w, "\n%s\n10. EXAMPLES (up to 3 per class; ALL %d records are in %s)\n%s\n",
		sub, total, outJSONName, sub)
	for _, b := range bucketOrder {
		fmt.Fprintf(w, "\n  %s\n", bucketTitle[b])
		if len(by[b]) == 0 {
			fmt.Fprintf(w, "    (empty)\n")
			continue
		}
		for i, d := range by[b] {
			if i >= 3 {
				fmt.Fprintf(w, "    ... and %d more in %s\n", len(by[b])-3, outJSONName)
				break
			}
			fmt.Fprintf(w, "    run %s   node %s\n", d.runID, d.nodeID)
			fmt.Fprintf(w, "      grants  : %s\n", formatGrants(d.grants))
			fmt.Fprintf(w, "      command : %s\n", quoteVerbatim(d.command))
			fmt.Fprintf(w, "      shape   : %s, %d sub-command(s), %s\n",
				map[bool]string{true: "COMPOUND", false: "simple"}[d.compound],
				len(d.split.subs), d.scope)
			for j, sc := range d.split.subs {
				mark := "GRANTED    "
				if len(d.granted) == 0 {
					mark = "unjudged   "
				} else if !d.granted[j] {
					mark = "NOT GRANTED"
				}
				depth := ""
				if sc.depth > 0 {
					depth = fmt.Sprintf(" (inside substitution, depth %d)", sc.depth)
				}
				fmt.Fprintf(w, "        %d. %s  first-word=%-12s %s%s\n",
					j+1, mark, sc.firstWord(), quoteVerbatim(sc.text), depth)
			}
		}
	}

	fmt.Fprintf(w, "\n%s\nEND OF SUMMARY — per-call records in %s\n%s\n", line, outJSONName, line)
	return w.String()
}

// writeList prints a named bucket with its count and up to listCap examples, so
// the summary stays short while every item remains available in the JSON.
const listCap = 8

func writeList(w *strings.Builder, title string, items []string) {
	fmt.Fprintf(w, "  %s: %d\n", title, len(items))
	for i, s := range items {
		if i >= listCap {
			fmt.Fprintf(w, "    ... and %d more (all of them in %s)\n", len(items)-listCap, outJSONName)
			break
		}
		fmt.Fprintf(w, "    - %s\n", s)
	}
}

func formatGrants(g []string) string {
	if len(g) == 0 {
		return "(none declared)"
	}
	return "[" + strings.Join(g, ", ") + "]"
}

// quoteVerbatim renders a command for the report without altering it: newlines
// and tabs are escaped so one denial stays on one line, and nothing else changes.
func quoteVerbatim(s string) string {
	r := strings.NewReplacer("\n", "\\n", "\t", "\\t", "\r", "\\r")
	return "`" + r.Replace(s) + "`"
}

// wrap breaks a paragraph at width columns on word boundaries, so the caveat
// reads as a paragraph rather than one very long line.
func wrap(s string, width int) string {
	var out strings.Builder
	col := 0
	for i, word := range strings.Fields(s) {
		if i > 0 {
			if col+1+len(word) > width {
				out.WriteByte('\n')
				col = 0
			} else {
				out.WriteByte(' ')
				col++
			}
		}
		out.WriteString(word)
		col += len(word)
	}
	return out.String()
}
