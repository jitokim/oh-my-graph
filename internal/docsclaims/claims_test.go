package docsclaims

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
type falsified struct {
	name      string
	absolute  *regexp.Regexp
	qualifier *regexp.Regexp
	address   string
}

// How far after the absolute the qualifier may sit, in whitespace-normalised
// bytes. Wide enough for a qualifier pushed onto the next wrapped line, narrow
// enough that a mention elsewhere in the paragraph does not launder it.
const qualifierWindow = 200

// optIn is the flag ADR 0032 shipped; naming it is what conditions both claims.
var optIn = regexp.MustCompile(`--accept-loaded-user-config`)

// The two absolutes ADR 0032 falsified. d537739 conditioned both where they
// stood in docs/EXAMPLES.md (:451, :460 today); they are encoded here so that
// no document — that one or a new one — can state either as an absolute again.
var falsifiedByADR0032 = []falsified{
	{
		name:      `"no planned node gets that any more"`,
		absolute:  regexp.MustCompile(`no planned node gets that any more`),
		qualifier: optIn,
		address: "internal/coordinator/coordinator.go:763-764 — toolPolicyFor sets " +
			"policy.SettingSources = nil when the run typed --accept-loaded-user-config, " +
			"so every planned node of such a run does get the operator's configuration",
	},
	{
		name:      "\"as every planned node's always has\", of `--strict-mcp-config`",
		absolute:  regexp.MustCompile(`every planned node('s)? always has`),
		qualifier: optIn,
		address: "internal/coordinator/coordinator.go:765 sets policy.StrictMCPConfig = false " +
			"under --accept-loaded-user-config, and internal/runner/claude_protocol.go:55-56 " +
			"emits --strict-mcp-config only when it is true, so such a node's argv carries none",
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
			// Requirement 3: absence is never a failure. This document states
			// neither claim, mentions neither flag, and must be silent.
			name: "a document that claims nothing at all",
			text: "# Install\n\n`go install github.com/jitokim/oh-my-graph/cmd/oh-my-graph@latest`\n",
			want: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scan([]byte(tc.text))
			if len(got) != tc.want {
				t.Fatalf("scan found %d finding(s), want %d: %+v", len(got), tc.want, got)
			}
		})
	}
}

func TestNoDocumentStatesAnAbsoluteADR0032Falsified(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range docSet(t, root) {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		for _, f := range scan(raw) {
			t.Errorf("%s:%d states %s unconditionally.\n  quoted: %q\n  falsified by: %s\n"+
				"  Condition it on --accept-loaded-user-config, or drop it.",
				rel, f.line, f.claim.name, f.quote, f.claim.address)
		}
	}
}

// scan reports every absolute stated in raw with nothing conditioning it.
func scan(raw []byte) []finding {
	text, offsets := normalize(raw)
	var findings []finding
	for _, claim := range falsifiedByADR0032 {
		for _, m := range claim.absolute.FindAllStringIndex(text, -1) {
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
	return findings
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
