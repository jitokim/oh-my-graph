package handoff

import (
	"fmt"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// LintVerifyInlining inspects every `success_check.verify.command` for the one
// placeholder shape that puts a MODEL's free-form text into a shell command
// line, and returns one advisory warning per token.
//
// A verify command interpolates exactly like a prompt —
// `schedule.resolveVerification` calls the same `Handoff.Interpolate` — and the
// result is then handed to `verify.ShellVerifier`, the second exec seam, which
// runs it as `sh -c <command>` (`cmd /c` on Windows) under the user's own
// shell. Two token shapes carry model-written text into that string:
//
//   - `{{ artifacts.<id> | inline }}` — the node's own reply. The DEFAULT is
//     not this: with no filter an artifacts token resolves to the persisted
//     `.out` FILE PATH, which the engine computes from the run directory and a
//     sanitized node id, so the plain token is inert and the hazard is the
//     filter specifically. That is why the message names `| inline` and offers
//     the default as the fix;
//   - `{{ feedback.<id> }}` — the declarer's feedback payload, which is its
//     result text when the failing execution produced one (ADR 0010). This one
//     ALWAYS inlines and takes no filter — `graph.Validate` refuses one — so
//     there is no default to fall back to, and its message says something
//     different: the payload belongs in a prompt.
//
// A token LintPlaceholders condemns as UNRESOLVABLE is skipped here — an
// artifacts token naming the referencing node itself, or a node that is not in
// the graph. Nothing is spliced by a token that never substitutes, so the
// warning's own sentence would be false, and its fix ("drop the filter") is
// advice the author cannot use: the filterless form of a reference that names
// nothing does not resolve either. That keeps the two sweeps' subjects
// disjoint on the tokens that can never work, and the boundary stated in
// LintPlaceholders' docstring true of the code. A reference to a node that
// exists but is not an ancestor is NOT skipped: LintPlaceholders warns that it
// may not resolve, but it still can — and if it does, a model's reply is on
// that command line — so it earns a line from each sweep, for two different
// reasons.
//
// `{{ inputs.<name> }}` is deliberately NOT warned about. An input is bound at
// invocation from the user's own `--input k=v`, so it has exactly the standing
// the command line itself has — and it is a shape this repo ships:
// `backlog-batch.yaml`'s two e2e nodes run `{{ inputs.checks_command }}`,
// which is the author parameterising their own check, not a model choosing it.
//
// Only `command` is swept, not `verify.cwd`. The exemption is that a cwd is
// not SHELL-INTERPRETED: it becomes `exec.Cmd.Dir`, so no part of a reply
// there is parsed as syntax and nothing in it can become a second command.
// (It is not a claim that a reply in a cwd always fails: a node asked for a
// path replies with a path, and then the author's own command runs somewhere a
// model chose. That is worth knowing, but it is not this sweep's subject —
// this sweep is about the command LINE, the place where a reply becomes
// executable text.)
//
// It was measured before it shipped and it fires on NOTHING: over this repo's
// shipped graphs plus a 20-graph operator lane corpus — 93 nodes and 34 verify
// blocks after fragment resolution — zero verify commands inline a model's
// text, and the only tokens in any verify command at all are the two
// `{{ inputs.checks_command }}` above, which this predicate correctly leaves
// alone. So this is documentation with a test attached, and it ships as that
// on purpose: it is the one advisory in this package whose subject is not a
// run that comes out wrong but a run that executes text nobody wrote, it is
// invisible by construction (the token is well-formed, the graph is valid, the
// command runs), and the test is what keeps the documentation true — if the
// default filter ever changes, or a second inlining filter is added,
// TestLintVerifyInlining_TheDefaultFilterIsAPath fails.
//
// It stays a warning rather than a load error for the standing reason every
// sweep in this package does: a hand-written graph is the user's own reviewed
// artifact. A planned graph cannot reach this at all —
// `coordinator.validatePlannedNodeVerify` refuses a planner-authored
// `verify:`, and the only other way one appears is
// `coordinator.attachVerifyCommand`, which sets the user's own `--verify-cmd`
// string verbatim — so an author is the only person who can write this, which
// is exactly what makes advice the right severity.
func LintVerifyInlining(g *graph.Graph) []Warning {
	var warnings []Warning
	for _, node := range g.Nodes {
		v := node.SuccessCheck.Verify
		if v == nil {
			continue
		}
		for _, groups := range placeholderPattern.FindAllStringSubmatch(v.Command, -1) {
			token, kind, ref, filter := groups[0], groups[1], groups[2], groups[3]
			if kind == "artifacts" && !canEverSubstitute(g, node.ID, ref) {
				continue // LintPlaceholders' finding, and nothing this one could say is true of it
			}
			detail := judgeInlinedToken(token, kind, ref, filter)
			if detail == "" {
				continue
			}
			warnings = append(warnings, Warning{
				NodeID: node.ID,
				Field:  "success_check.verify.command",
				Detail: detail,
			})
		}
	}
	return warnings
}

// canEverSubstitute reports whether an artifacts reference written in nodeID's
// own verify command could ever resolve to anything at run time. A node cannot
// reference its own artifact (it does not exist while the node runs) and a
// reference to a node the graph does not have never names an artifact at all;
// both are already LintPlaceholders findings. It deliberately does NOT ask
// about ancestry — a non-ancestor's artifact may well exist by the time this
// node runs, and then the splice is real.
func canEverSubstitute(g *graph.Graph, nodeID, ref string) bool {
	if ref == nodeID {
		return false
	}
	_, exists := g.NodeByID(ref)
	return exists
}

// judgeInlinedToken returns what to say about one already-well-formed token in
// a verify command, or "" for a token that carries no model text — a
// user-supplied input, or an artifacts token resolving to its file path.
func judgeInlinedToken(token, kind, ref, filter string) string {
	switch {
	case kind == "artifacts" && filter == "inline":
		return fmt.Sprintf(
			"%s splices node %q's reply into a command line the engine runs through your shell — a model's free-form text, not a value your graph chose. Drop the filter: with no filter the token is the artifact's FILE PATH, so the command reads the file itself (grep -q '^PASS' \"{{ artifacts.%s }}\")",
			token, ref, ref)
	case kind == "feedback":
		return fmt.Sprintf(
			"%s splices node %q's feedback payload — its own reply text — into a command line the engine runs through your shell. A feedback placeholder always inlines and has no path form to fall back on: quote the payload in the node's PROMPT, and let the verify command observe the tree instead",
			token, ref)
	}
	return ""
}
