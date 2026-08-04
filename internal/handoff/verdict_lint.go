package handoff

import (
	"strings"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// verdictDemands are the phrasings a shipped prompt uses to demand a verdict
// token as the first characters of the reply. Matched case-insensitively
// against the prompt, they are deliberately narrow: each one names the REPLY
// and where a token goes in it, so ordinary prose about starting work does not
// trip the sweep. A graph that words the demand some other way is simply not
// warned about — this is an advisory, and a missed warning costs less than a
// warning the author learns to ignore.
var verdictDemands = []string{
	"start your reply with",
	"start the reply with",
	"begin your reply with",
	"your reply must start",
	"your reply is exactly",
	"first characters of the reply",
	"reply with exactly",
}

// LintVerdicts inspects every node for the two ways a declared verdict ends up
// unchecked, and returns one advisory warning per finding:
//
//   - the prompt demands a verdict token but `success_check` has no
//     `result_matches` — the prompt asks for an answer nothing reads. This is
//     the merge-shepherd failure recorded in DESIGN.md ("Verdict patterns"):
//     the node was asked for a merge commit SHA, replied with a promise to
//     merge once a review concluded, exited 0, and wrote a PASS row into the
//     ledger with nothing merged. A prompt is not a mechanism;
//   - `result_matches` is set but `exit_zero` is not. Exit-zero is only the
//     default while a node declares NO check at all (`SuccessCheck.IsZero`),
//     so adding a result predicate to a node that had no check silently
//     deletes its exit-code guard — a fix that removes a guard while adding
//     one. Any node that wants both must name both.
//
// Both stay warnings rather than load errors because both are expressible on
// purpose: a node may demand a token purely so a DOWNSTREAM node can read it
// out of the artifact, and a node may genuinely not care about its exit code.
// Neither judges the pattern's quality — whether a payload is shaped tightly
// enough to reject a promise is a question no static check can answer.
func LintVerdicts(g *graph.Graph) []Warning {
	var warnings []Warning
	for _, node := range g.Nodes {
		check := node.SuccessCheck
		if check.ResultMatches == "" && demandsVerdict(node.Prompt) {
			warnings = append(warnings, Warning{
				NodeID: node.ID,
				Field:  "success_check",
				Detail: "prompt demands a verdict token but success_check has no result_matches — nothing reads the answer, so a reply that promises the work instead of doing it passes (DESIGN.md \"Verdict patterns\")",
			})
		}
		if check.ResultMatches != "" && !check.ExitZero {
			warnings = append(warnings, Warning{
				NodeID: node.ID,
				Field:  "success_check",
				Detail: "result_matches without exit_zero — exit-zero is the default only for a node with NO success_check, so this node's exit code is now unchecked; add `exit_zero: true` to keep it",
			})
		}
	}
	return warnings
}

// demandsVerdict reports whether a prompt asks for a verdict token at the head
// of the reply, by the phrasings in verdictDemands. Case-insensitive, since
// these prompts shout the demand ("START your reply with") as often as not.
func demandsVerdict(prompt string) bool {
	lowered := strings.ToLower(prompt)
	for _, demand := range verdictDemands {
		if strings.Contains(lowered, demand) {
			return true
		}
	}
	return false
}
