package docsclaims

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// A falsified is one absolute the code has already made false, plus the way a
// document is allowed to keep saying it: conditioned.
//
//   - absolute is the phrasing that is false on its own.
//   - qualifier is what must appear close after it for the sentence to be true.
//     A document that never says the absolute at all is never a finding — this
//     guard has no opinion about what a document must contain.
//   - address is the code that falsifies it, carried into the failure message
//     so whoever reads the failure can retrace it instead of trusting it.
//   - anchors is one line of code per `path:first-last` span the address names,
//     in the order the address names them. Each must appear exactly once in the
//     file its span names, on that span's first line — which is what turns a
//     promised coordinate into something a test can re-resolve rather than
//     something a reader has to trust. See TestClaimAddressesResolve.
type falsified struct {
	name      string
	absolute  *regexp.Regexp
	qualifier *regexp.Regexp
	address   string
	anchors   []string
}

// How far after the absolute the qualifier may sit, in whitespace-normalised
// bytes. Wide enough for a qualifier pushed onto the next wrapped line, narrow
// enough that a mention elsewhere in the paragraph does not launder it.
const qualifierWindow = 200

// optIn is the flag ADR 0032 shipped; naming it is what conditions both claims.
var optIn = regexp.MustCompile(`--accept-loaded-user-config`)

// The two absolutes ADR 0032 falsified. d537739 conditioned both where they
// stood in docs/EXAMPLES.md: the `--strict-mcp-config` parenthetical, and the
// paragraph on what `--no-agent <name>` buys. They are encoded here so that no
// document — that one or a new one — can state either as an absolute again.
//
// The sentences are named, not numbered, on purpose: line numbers into that
// document have rotted twice inside the file whose whole job is stopping rot,
// and each claim's own name below is the phrase to search for.
var falsifiedByADR0032 = []falsified{
	{
		name:      `"no planned node gets that any more"`,
		absolute:  regexp.MustCompile(`no planned node gets that any more`),
		qualifier: optIn,
		address: "internal/coordinator/coordinator.go:868-871 — toolPolicyFor sets " +
			"policy.SettingSources = nil when the run typed --accept-loaded-user-config, " +
			"so every planned node of such a run does get the operator's configuration",
		anchors: []string{"if loadedUserConfig {"},
	},
	{
		name:      "\"as every planned node's has\", of `--strict-mcp-config`",
		absolute:  regexp.MustCompile(`as every planned node('s)? (always )?has`),
		qualifier: optIn,
		address: "internal/coordinator/coordinator.go:868-871 sets policy.StrictMCPConfig = false " +
			"under --accept-loaded-user-config, and internal/runner/claude_protocol.go:65-67 " +
			"emits --strict-mcp-config only when it is true, so such a node's argv carries none",
		anchors: []string{"if loadedUserConfig {", "if policy.StrictMCPConfig {"},
	},
}

// A stated is the other direction of the same drift, and the direction the
// falsified list above cannot see: not a document asserting something the code
// made false, but a document DENYING something the code shipped.
//
// The guard above is deliberately blind to it — "silence is not a finding" is
// what keeps it from demanding that every document mention every feature. A
// stated is narrower than that on purpose: it does not ask a document to cover
// a subject, it pins one named sentence that a shipped behaviour has already
// re-written once, in the document that was left denying it while a sibling
// document was corrected in the same lane.
//
//   - doc is the one document that must carry it; other documents are not asked.
//   - phrases are ALL required, matched against the whitespace-normalised text,
//     so a sentence wrapped across lines still reads as one sentence.
//   - address and anchors work exactly as they do for a falsified, and
//     TestClaimAddressesResolve re-resolves them the same way: the code a
//     document is pinned to moves, and the pin has to say where it went.
type stated struct {
	name    string
	doc     string
	phrases []*regexp.Regexp
	address string
	anchors []string
}

// The two sentences DESIGN.md was still denying after the behaviour shipped.
// Both landed in 456d374, which corrected docs/LIMITATIONS.md and did not
// touch DESIGN.md at all — one claim living in two places with only one of
// them moving, which is the whole failure mode this package exists for.
var statedByShippedBehaviour = []stated{
	{
		name: "a verify FAIL names the scrubbed variable when both halves hold",
		doc:  "DESIGN.md",
		phrases: []*regexp.Regexp{
			regexp.MustCompile(`the failure text says which when it can`),
			regexp.MustCompile(`trigger is a conjunction and both halves are load-bearing`),
			regexp.MustCompile(`judged against the full output rather than the truncated tail`),
			regexp.MustCompile(`Either signal alone is noise`),
			regexp.MustCompile(`scrubHint`),
		},
		address: "internal/schedule/scheduler.go:1554-1565 — scrubHint returns the sentence " +
			"only for a name that is BOTH in result.ScrubbedFromEnv and contained in the " +
			"full result.Output, and returns the first such name in childenv's list order",
		anchors: []string{"func scrubHint(result verify.Result) string {"},
	},
	{
		name: "the fourth whole-reply pin's caveat goes in the FAIL branch",
		doc:  "DESIGN.md",
		phrases: []*regexp.Regexp{
			regexp.MustCompile(`For three of the four the answer to "where does the caveat go" is still "nowhere`),
			regexp.MustCompile(`The fourth now has one, and it is that same FAIL branch`),
			regexp.MustCompile(`branchEvidenceRule`),
			regexp.MustCompile(`The pattern itself is unchanged`),
		},
		address: "internal/coordinator/coordinator.go:1812-1819 — branchEvidenceRule reserves PASS " +
			"for the assertion holding and nothing a reader would act on differently, and sends " +
			"anything else into the FAIL branch; internal/coordinator/coordinator.go:1890 — the " +
			"pattern the same paragraph hands out is unchanged, anchored at both ends",
		anchors: []string{
			"markdown. Anything the node does need to report goes in the FAIL branch,",
			"const plannedVerdictPattern = ",
		},
	},
}

// expectedRoots is asserted PRESENT in the walk — it is never used to select
// what gets scanned. Scanning follows the walk, so a document that lands under
// docs/ or plugin/ tomorrow is scanned tomorrow with no edit here; this list
// only fails the day the walk stops reaching a document it used to reach.
var expectedRoots = []string{
	"README.md",
	"README.ko.md",
	"DESIGN.md",
	"SECURITY.md",
	"CONTRIBUTING.md",
	"CHANGELOG.md",
	"docs/EXAMPLES.md",
	"docs/LIMITATIONS.md",
	"docs/INSTALL.md",
	"docs/adr/0032-a-planned-node-may-carry-the-operators-configuration.md",
	"plugin/README.md",
	"plugin/commands/graph.md",
	"plugin/agents/oh-my-graph.md",
	"plugin/skills/run-graph/SKILL.md",
}

// walkedSubtrees is the scope, not a file list: every Markdown file at the
// repository root, and every Markdown file anywhere under these.
var walkedSubtrees = []string{"docs", "plugin"}

type finding struct {
	line  int
	claim falsified
	quote string
}

func TestDocSetIsWalkedAndReachesEveryExpectedRoot(t *testing.T) {
	root := repoRoot(t)
	docs := docSet(t, root)

	found := make(map[string]bool, len(docs))
	for _, rel := range docs {
		found[rel] = true
	}
	for _, want := range expectedRoots {
		if !found[want] {
			t.Errorf("the walk no longer reaches %s (%d documents found). "+
				"A guard that scans nothing passes; fix the walk, do not shrink this list.",
				want, len(docs))
		}
	}
}

func TestScanFiresOnTheAbsoluteAndNotOnTheConditionedForm(t *testing.T) {
	cases := []struct {
		name string
		text string
		want int
	}{
		{
			// docs/EXAMPLES.md:457-458 as it stood before d537739, verbatim.
			name: "the absolute, as it shipped",
			text: "what declining buys: it does **not** hand the node your environment back,\n" +
				"because no planned node gets that any more. `--no-agent-mapping` remains the\n",
			want: 1,
		},
		{
			name: "the same claim, conditioned",
			text: "because no planned node gets that any more — unless the run typed\n" +
				"`--accept-loaded-user-config`, in which case every planned node has it\n",
			want: 0,
		},
		{
			name: "the strict-mcp-config absolute, wrapped across lines",
			text: "(Its argv also carries `--strict-mcp-config`, as every planned node's\n" +
				"always has; whether that closes MCP is unmeasured.)\n",
			want: 1,
		},
		{
			name: "the same claim, conditioned",
			text: "(Its argv also carries `--strict-mcp-config`, as every planned node's always\n" +
				"has unless the run typed `--accept-loaded-user-config`, which drops it.)\n",
			want: 0,
		},
		{
			// docs/EXAMPLES.md:469-472 as it stands today, with its `unless`
			// clause cut out: the sentence the shipped one would regress to.
			// The pattern used to demand an "always" this form never had, which
			// is exactly the shape of miss the vacuity floor now catches.
			name: "the strict-mcp-config absolute without the adverb",
			text: "declares one and that is the more specific choice (ADR 0037). (Its argv also carries\n" +
				"`--strict-mcp-config`, as every planned node's has; whether\n" +
				"it closes MCP is unmeasured, so read it as a flag rather than a result.)\n",
			want: 1,
		},
		{
			// The conditioned branch of the same paragraph, EXAMPLES.md:480.
			// "every planned node has it" states no absolute — it is inside the
			// `--accept-loaded-user-config` case — and the qualifier is behind
			// it, not ahead of it, so only the missing "as" keeps this silent.
			name: "the conditioned branch, which names no qualifier after itself",
			text: "because no planned node gets that any more — unless the run typed\n" +
				"`--accept-loaded-user-config`, in which case every planned node has it and there\n" +
				"is no agent mapping left to decline\n",
			want: 0,
		},
		{
			// Requirement 3: absence is never a failure. This document states
			// neither claim, mentions neither flag, and must be silent.
			name: "a document that claims nothing at all",
			text: "# Install\n\n`go install github.com/jitokim/oh-my-graph/cmd/oh-my-graph@latest`\n",
			want: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := scan([]byte(tc.text))
			if len(got) != tc.want {
				t.Fatalf("scan found %d finding(s), want %d: %+v", len(got), tc.want, got)
			}
		})
	}
}

func TestNoDocumentStatesAnAbsoluteADR0032Falsified(t *testing.T) {
	root := repoRoot(t)
	stated := make([]int, len(falsifiedByADR0032))
	for _, rel := range docSet(t, root) {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		findings, counts := scan(raw)
		for i, n := range counts {
			stated[i] += n
		}
		for _, f := range findings {
			t.Errorf("%s:%d states %s unconditionally.\n  quoted: %q\n  falsified by: %s\n"+
				"  Condition it on --accept-loaded-user-config, or drop it.",
				rel, f.line, f.claim.name, f.quote, f.claim.address)
		}
	}

	// The floor. A claim that matches nothing at all cannot fail, so it is
	// indistinguishable from a passing corpus — which is how a regexp demanding
	// a word the documents never wrote survived here unnoticed.
	for i, claim := range falsifiedByADR0032 {
		if stated[i] == 0 {
			t.Errorf("no document states %s any more, conditioned or not — this test now asserts nothing for that claim, and the absolute it exists to stop is demonstrated nowhere in the walked corpus.\n  pattern: /%s/\n  Re-word the pattern to the sentence the documents actually carry, or retire the claim.",
				claim.name, claim.absolute)
		}
	}
}

// TestDocumentStatesWhatTheCodeShipped is the presence half of this guard.
//
// Its subject is the drift TestNoDocumentStatesAnAbsoluteADR0032Falsified
// cannot reach: a sentence that was true when it was written, that a shipped
// change made false in the direction of DENIAL, and that survives because
// nothing red goes off when a document merely stops short of what the code
// does. Both claims below were corrected in one document and left standing in
// another, so the pin is on the document that was missed.
//
// A missing phrase names itself in the failure, because "DESIGN.md no longer
// states this claim" is not actionable and "DESIGN.md no longer contains
// /Either signal alone is noise/" is.
func TestDocumentStatesWhatTheCodeShipped(t *testing.T) {
	root := repoRoot(t)
	for _, claim := range statedByShippedBehaviour {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(claim.doc)))
		if err != nil {
			t.Errorf("claim %q is pinned to %s, which cannot be opened: %v", claim.name, claim.doc, err)
			continue
		}
		text, _ := normalize(raw)
		for _, phrase := range claim.phrases {
			if phrase.MatchString(text) {
				continue
			}
			t.Errorf("%s no longer states %q.\n  missing: /%s/\n  the code that makes it true: %s\n"+
				"  The behaviour shipped, so the document may not go back to denying it — reword the phrase here only if you reworded the sentence there.",
				claim.doc, claim.name, phrase, claim.address)
		}
	}
}

// citationRe finds the coordinates an address promises: a path carrying a
// source extension, a first line, and optionally a last one. Requiring both the
// extension and the `:digits` is what keeps it off the rest of the prose —
// `policy.SettingSources = nil` names no line, `ADR 0032` names no file.
var citationRe = regexp.MustCompile(`([A-Za-z0-9_./-]+\.(?:go|sh|md)):(\d+)(?:-(\d+))?`)

// TestClaimAddressesResolve opens every coordinate the claims promise and
// checks it against the tree as it stands today.
//
// The address is the one part of this guard that is printed rather than
// evaluated: a reader who sees TestNoDocumentStatesAnAbsoluteADR0032Falsified
// go red is handed a file:line and told to retrace it, so the address is
// trusted exactly as far as it is accurate — and a line number rots the moment
// somebody inserts a line above it, silently, with nothing failing. Each
// coordinate therefore carries an anchor: a line of code that must occur
// exactly once in the file and must sit on the coordinate's first line. When
// the code moves, this test finds where it moved to and the failure names the
// address to write instead, so the fix is an edit rather than an investigation.
//
// A span's last line is bounded rather than anchored, because the line that
// closes a block is usually `}` and `}` anchors nothing.
//
// It covers both claim kinds, because both print an address for the same
// reason: a stated's failure hands its reader the code that shipped the
// behaviour the document stopped denying, and that coordinate rots exactly as
// readily as a falsified's.
func TestClaimAddressesResolve(t *testing.T) {
	root := repoRoot(t)
	for _, claim := range falsifiedByADR0032 {
		checkAddressResolves(t, root, claim.name, claim.address, claim.anchors)
	}
	for _, claim := range statedByShippedBehaviour {
		checkAddressResolves(t, root, claim.name, claim.address, claim.anchors)
	}
}

// checkAddressResolves re-resolves every coordinate one claim's address
// promises, against the anchors it declares for them.
func checkAddressResolves(t *testing.T, root, name, address string, anchors []string) {
	t.Helper()

	cites := citationRe.FindAllStringSubmatch(address, -1)
	switch {
	case len(cites) == 0:
		t.Errorf("claim %s promises no file:line at all, so its address cannot be retraced.\n  address: %s",
			name, address)
	case len(cites) != len(anchors):
		t.Errorf("claim %s promises %d coordinate(s) but declares %d anchor(s).\n"+
			"  Every file:line an address names needs one anchor, in the order the address names them.\n"+
			"  address: %s", name, len(cites), len(anchors), address)
	default:
		for i, cite := range cites {
			checkCitationResolves(t, root, name, anchors[i], cite)
		}
	}
}

// checkCitationResolves re-resolves one `path:first[-last]` against the file it
// names. cite is a citationRe submatch: whole, path, first, last ("" if none).
func checkCitationResolves(t *testing.T, root string, name, anchor string, cite []string) {
	t.Helper()

	whole, path := cite[0], cite[1]
	first, err := strconv.Atoi(cite[2])
	if err != nil {
		t.Errorf("claim %s: address coordinate %s has an unreadable line number: %v", name, whole, err)
		return
	}

	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Errorf("claim %s: address names %s, which cannot be opened — the file moved or went away: %v",
			name, whole, err)
		return
	}
	lines := strings.Split(string(raw), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}

	var hits []int
	for n, line := range lines {
		if strings.Contains(line, anchor) {
			hits = append(hits, n+1)
		}
	}
	switch {
	case len(hits) == 0:
		t.Errorf("claim %s: address %s is anchored on %q, which is nowhere in %s any more.\n"+
			"  The code the address names was deleted or re-worded; re-read the file, then rewrite the address and its anchor together.",
			name, whole, anchor, path)
		return
	case len(hits) > 1:
		t.Errorf("claim %s: anchor %q occurs %d times in %s, at lines %v, so it cannot say which line %s means.\n"+
			"  Lengthen the anchor until exactly one line carries it.",
			name, anchor, len(hits), path, hits, whole)
		return
	}

	if hits[0] != first {
		t.Errorf("claim %s: address says %s, but its anchor %q sits at %s:%d today.\n"+
			"  Update the address to %s.",
			name, whole, anchor, path, hits[0], shiftedCitation(path, first, cite[3], hits[0]))
		return
	}

	if cite[3] == "" {
		return
	}
	last, err := strconv.Atoi(cite[3])
	if err != nil {
		t.Errorf("claim %s: address coordinate %s has an unreadable last line: %v", name, whole, err)
		return
	}
	if last < first || last > len(lines) {
		t.Errorf("claim %s: address %s ends at line %d, which %s does not reach (it has %d lines).\n"+
			"  Re-read the block and give the address the line it really ends on.",
			name, whole, last, path, len(lines))
	}
}

// shiftedCitation is the address to write instead: the anchor's new line, and a
// span end moved by the same distance the anchor moved.
func shiftedCitation(path string, first int, rawLast string, found int) string {
	if rawLast == "" {
		return path + ":" + strconv.Itoa(found)
	}
	last, err := strconv.Atoi(rawLast)
	if err != nil {
		return path + ":" + strconv.Itoa(found)
	}
	return path + ":" + strconv.Itoa(found) + "-" + strconv.Itoa(last+found-first)
}

// scan reports every absolute stated in raw with nothing conditioning it, and
// beside it how many times each claim's absolute was stated at all, conditioned
// or not. That second count is what the vacuity floor adds up: a suppressed
// match still proves the pattern reaches the sentence it was written for, and a
// claim with no matches of either kind is a guard that has stopped guarding.
func scan(raw []byte) ([]finding, []int) {
	text, offsets := normalize(raw)
	var findings []finding
	stated := make([]int, len(falsifiedByADR0032))
	for i, claim := range falsifiedByADR0032 {
		for _, m := range claim.absolute.FindAllStringIndex(text, -1) {
			stated[i]++
			end := m[1] + qualifierWindow
			if end > len(text) {
				end = len(text)
			}
			if claim.qualifier.MatchString(text[m[1]:end]) {
				continue
			}
			quoteEnd := m[1] + 60
			if quoteEnd > len(text) {
				quoteEnd = len(text)
			}
			findings = append(findings, finding{
				line:  lineOf(raw, offsets[m[0]]),
				claim: claim,
				quote: text[m[0]:quoteEnd],
			})
		}
	}
	return findings, stated
}

// normalize collapses every run of whitespace to one space, so a claim the
// author wrapped across two lines still reads as one sentence. It returns the
// offset in raw of each byte it kept, which is what turns a match back into a
// line number.
func normalize(raw []byte) (string, []int) {
	var b strings.Builder
	offsets := make([]int, 0, len(raw))
	prevSpace := false
	for i, c := range raw {
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			if prevSpace {
				continue
			}
			b.WriteByte(' ')
			offsets = append(offsets, i)
			prevSpace = true
			continue
		}
		b.WriteByte(c)
		offsets = append(offsets, i)
		prevSpace = false
	}
	return b.String(), offsets
}

func lineOf(raw []byte, offset int) int {
	return 1 + strings.Count(string(raw[:offset]), "\n")
}

// docSet derives the documents to scan: every *.md at the repository root, and
// every *.md under each walked subtree. Nothing is excluded, because an
// exclusion is the shape of the miss this guard exists to stop.
func docSet(t *testing.T, root string) []string {
	t.Helper()

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read repository root %s: %v", root, err)
	}
	var docs []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			docs = append(docs, e.Name())
		}
	}

	for _, sub := range walkedSubtrees {
		err := filepath.WalkDir(filepath.Join(root, sub), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			docs = append(docs, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", sub, err)
		}
	}
	return docs
}

func repoRoot(t *testing.T) string {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("no go.mod at %s — this guard is scanning the wrong tree: %v", root, err)
	}
	return root
}
