package handoff

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// Warning is one advisory finding from this package's lint sweeps
// (LintPlaceholders, LintSessions): something a valid graph declares that
// will — or may — not behave as written at run time. It is advice, never an
// error, and must never affect whether a graph is valid or what any command
// exits with.
type Warning struct {
	NodeID string
	Field  string // which node field the finding is about, e.g. "prompt", "cwd", "worktree", "retry"
	Detail string
}

func (w Warning) String() string {
	return fmt.Sprintf("node %q: %s: %s", w.NodeID, w.Field, w.Detail)
}

// looseTokenPattern finds every {{ ... }} token, well-formed or not, so the
// inspection can judge tokens the strict placeholderPattern would skip over
// (which is exactly how a one-character typo ships verbatim into a paid
// prompt today).
var looseTokenPattern = regexp.MustCompile(`\{\{[^{}]*\}\}`)

// leadingWordPattern extracts the first identifier of a token body — the word
// that decides whether the token is placeholder-LIKE at all.
var leadingWordPattern = regexp.MustCompile(`^[A-Za-z0-9_]+`)

// placeholderKinds are the body-leading words that mark a token as intended
// interpolation: the three real kinds plus their number typos (singular for
// the plural kinds, plural for feedback), matched case-insensitively so a
// case-variant like {{ Artifacts.x }} is judged rather than shipped
// verbatim. A {{ ... }} whose body starts with anything else is assumed to
// be deliberate literal text and is never warned about.
var placeholderKinds = map[string]bool{
	"inputs":    true,
	"artifacts": true,
	"feedback":  true,
	"input":     true,
	"artifact":  true,
	"feedbacks": true,
}

// LintPlaceholders statically inspects every templated field the Scheduler
// interpolates — a node's prompt and cwd, and a verify block's command and
// cwd — and returns one advisory warning per {{ ... }} token that is
// placeholder-like but will not resolve:
//
//   - the token fails to parse as {{ inputs.<name> }} / {{ artifacts.<id> }}
//     (optional filter: | inline) despite its body starting with one of the
//     placeholder kinds (matched case-insensitively) — an unknown filter, a
//     singular typo, a case-variant like {{ Artifacts.x }}; a dotted
//     reference is NOT that — ids and input names may contain dots, so
//     {{ inputs.repo.name }} is a well-formed reference to "repo.name";
//   - the token parses but names an input the graph does not declare;
//   - the token parses but names an artifact of a node that does not exist,
//     is the referencing node itself, or is not one of its ancestors (so the
//     artifact cannot be guaranteed to exist when the node runs).
//
// It shares placeholderPattern with Interpolate, so what lint calls
// well-formed and what the runtime resolves can never drift apart. Warnings
// are advice only: the runtime passes every unrecognized token through
// verbatim, and LintPlaceholders never affects whether a graph is valid.
// Callers should hand it an already-validated graph — on a broken one
// (e.g. a dependency cycle) the ancestry walk still terminates but the
// findings may be noise on top of the real errors.
func LintPlaceholders(g *graph.Graph) []Warning {
	declared := make(map[string]bool, len(g.Inputs))
	for _, name := range g.Inputs {
		declared[name] = true
	}

	var warnings []Warning
	for _, node := range g.Nodes {
		ancestors := ancestorsOf(g, node.ID)
		fields := []struct{ name, tmpl string }{
			{"prompt", node.Prompt},
			{"cwd", node.Cwd},
		}
		if v := node.SuccessCheck.Verify; v != nil {
			fields = append(fields,
				struct{ name, tmpl string }{"success_check.verify.command", v.Command},
				struct{ name, tmpl string }{"success_check.verify.cwd", v.Cwd},
			)
		}
		for _, field := range fields {
			for _, token := range looseTokenPattern.FindAllString(field.tmpl, -1) {
				if detail := judgeToken(g, node.ID, declared, ancestors, token); detail != "" {
					warnings = append(warnings, Warning{NodeID: node.ID, Field: field.name, Detail: detail})
				}
			}
		}
	}
	return warnings
}

// judgeToken decides what, if anything, is wrong with one {{ ... }} token.
// It returns "" for a token that is either deliberate literal text (its body
// does not start with a placeholder kind) or a well-formed reference to a
// declared input / an ancestor node's artifact.
func judgeToken(g *graph.Graph, nodeID string, declared, ancestors map[string]bool, token string) string {
	body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(token, "{{"), "}}"))
	leading := leadingWordPattern.FindString(body)
	if !placeholderKinds[strings.ToLower(leading)] {
		return "" // deliberate literal text — none of the runtime's business, none of lint's
	}

	// The strict-parse judgment MUST come from placeholderPattern itself — the
	// exact regex Interpolate substitutes with — anchored to the whole token.
	loc := placeholderPattern.FindStringIndex(token)
	if loc == nil || loc[0] != 0 || loc[1] != len(token) {
		if leading != strings.ToLower(leading) {
			return fmt.Sprintf("%s looks like a placeholder but the runtime resolves lowercase kinds only — did you mean lowercase? As written it will reach the prompt verbatim", token)
		}
		return fmt.Sprintf("%s looks like a placeholder but does not match {{ inputs.<name> }}, {{ artifacts.<id> }} (optional filter: | inline) or {{ feedback.<id> }} — it will reach the prompt verbatim", token)
	}

	groups := placeholderPattern.FindStringSubmatch(token)
	kind, ref := groups[1], groups[2]
	if kind == "inputs" {
		if !declared[ref] {
			return fmt.Sprintf("%s references an input the graph does not declare in its inputs list", token)
		}
		return ""
	}
	if kind == "feedback" {
		// The feedback namespace is judged at LOAD, not here: an out-of-body
		// or filtered token is a graph.Validate error (ADR 0010), so on any
		// graph this lint is handed the token is already known-legal —
		// re-reporting it would be noise, and warning about a legal one
		// would contradict the validator.
		return ""
	}

	// kind == "artifacts"
	if ref == nodeID {
		return fmt.Sprintf("%s references the node's own artifact, which cannot exist while the node runs", token)
	}
	if _, ok := g.NodeByID(ref); !ok {
		return fmt.Sprintf("%s references %q, which is not a node in the graph", token, ref)
	}
	if !ancestors[ref] {
		return fmt.Sprintf("%s references node %q, which is not an ancestor of this node — its artifact may not exist when this node runs", token, ref)
	}
	return ""
}

// ancestorsOf walks the depends_on edges up from id and returns every
// transitive parent — the nodes whose artifacts are guaranteed to be
// persisted before id runs. The visited-set walk terminates even on a cyclic
// graph, so lint can run this before validity is settled.
func ancestorsOf(g *graph.Graph, id string) map[string]bool {
	ancestors := make(map[string]bool)
	var walk func(string)
	walk = func(current string) {
		node, ok := g.NodeByID(current)
		if !ok {
			return
		}
		for _, parent := range node.DependsOn {
			if !ancestors[parent] {
				ancestors[parent] = true
				walk(parent)
			}
		}
	}
	walk(id)
	return ancestors
}
