package handoff

import (
	"github.com/jitokim/oh-my-graph/internal/graph"
)

// LintToolGrants inspects every claude node for the one shape in which a tool
// denial is unobservable, and returns one advisory warning per finding: the
// node declares NO `allowed_tools` and NO `success_check.verify`.
//
// `allowed_tools` is the node's own grant, not a hint. A node that omits it
// inherits the user's Claude Code settings, so a tool that is not
// pre-authorised there is a tool the node cannot use — and nothing fails
// loudly: the subprocess explains the denial in prose, exits 0, and a
// `result_matches` written for the happy path passes on that prose. The node
// is then paid for, marked PASS in the ledger, and the work it was asked to do
// never happened. That is issue #154.
//
// The predicate is structural rather than a reading of the prompt: a node with
// a grant has authorised what it needs, and a node with a `success_check.verify`
// has a check the ENGINE runs itself, outside the node's own reply, so a denial
// that stops the work also fails the node. A node with neither has no
// mechanism of either kind. It was measured before it shipped, over the shipped
// graphs plus a 30-lane operator corpus — 164 claude nodes, counted after
// fragment resolution because that is the population the predicate is
// evaluated over: 62 hits, of which 61 were nodes whose prompts demand pushes,
// `gh` calls, builds and file writes they never declared. The one node that was
// not — a pure judgment node — is the shipped `review-loop.yaml::review`, which
// now declares the read tools it reads the working tree with, because declaring
// is what this advisory asks of everyone else.
//
// Two structural exemptions, both because the warning's premise is false there
// rather than to quiet it:
//
//   - a gate node spawns no subprocess at all, so it has no tools to grant;
//   - `permission_mode: bypassPermissions` grants every tool regardless of
//     `allowed_tools`, so nothing the node reaches for can be denied.
//
// The 61 were nodes that had never yet FAILED for want of a grant, because the
// machine that ran them pre-authorises broadly (`Bash(*)`); the hit is that
// none of them could have told you if it had.
//
// It stays a warning rather than a load error for the standing reason every
// sweep in this package does: a hand-written graph is the user's own reviewed
// artifact. Turning the advice down costs one line of YAML: a node that only
// summarises its inputs and reaches for nothing declares that with an EMPTY
// grant, `allowed_tools: []`, which is read here as the author saying so —
// while an absent key is no declaration at all. The two shapes are
// indistinguishable at run time (the runner emits `--allowedTools` only for a
// non-empty grant, so both inherit the user's settings), which is exactly why
// `[]` is safe to spend on saying it, and also why it must not be read as a
// sandbox: on the hand-written path the ceiling is the user's own settings by
// design (ADR 0004).
func LintToolGrants(g *graph.Graph) []Warning {
	var warnings []Warning
	for _, node := range g.Nodes {
		if node.Type == graph.TypeGate || node.PermissionMode == graph.PermissionBypass {
			continue
		}
		// Non-nil is the test, not non-empty: yaml.v3 leaves an absent key nil
		// and an explicit `[]` empty-but-present, so the declaration of needing
		// nothing survives as something this sweep can see.
		if node.AllowedTools != nil || node.SuccessCheck.Verify != nil {
			continue
		}
		warnings = append(warnings, Warning{
			NodeID: node.ID,
			Field:  "allowed_tools",
			Detail: "no allowed_tools and no success_check.verify — a tool your settings do not pre-authorise is denied silently, and the node's own result_matches passes on the prose explaining it; name the tools this node needs (`[]` if none), or add a verify command the engine runs itself",
		})
	}
	return warnings
}
