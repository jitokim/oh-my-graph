// Package childenv owns the one environment policy every process oh-my-graph
// spawns must obey: the variables that silently switch the claude CLI from the
// user's logged-in subscription (OAuth) to metered API-key billing are DELETED
// from the child's environment.
//
// It is a dependency-free leaf package because there are FOUR spawners —
// runner.ClaudeCLIRunner (a claude node), verify.ShellVerifier (a
// success_check.verify command), worktree.GitManager (the git worktree
// commands behind a node's worktree:) and browser.ExecOpener (the open/xdg-open
// launch of the serve URL) — and they must not be able to disagree about the
// rule. Each of the last three can reach claude without meaning to: a
// verification command may legitimately BE a claude invocation
// (`verify: { command: "claude -p ..." }`), a repo's git hooks may invoke it,
// and the launcher dispatches to a user-configured URL handler that inherits
// the child environment (ADR 0006). A scrub that lived only in the runner
// would leave exactly those cases billed to the API.
//
// The set of spawners is not a matter of prose: internal/invariants/
// exec_seam_test.go enforces it, both by refusing an os/exec import outside the
// four seams and by requiring each seam's call site to route its child env
// through Scrub. A fifth spawner needs its own ADR (see docs/adr/0002, 0005,
// 0006), and that test is what will notice.
//
// This is the load-bearing subscription-auth guarantee of the whole project.
// It is asserted here (the policy) and again on each spawner (the call site).
package childenv

import "strings"

// scrubbedVars are the environment variables deleted from every child process.
// Both are read by the claude CLI as "use API-key billing instead of the
// logged-in subscription", so leaving either in place would silently spend
// metered credits for a run the user expects to be inside their plan.
var scrubbedVars = []string{
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_AUTH_TOKEN",
	"OPENAI_API_KEY",
	"CODEX_API_KEY",
}

// Scrub returns parent with every scrubbed variable removed, leaving everything
// else untouched. Matching is on the WHOLE KEY (the text before the first '='),
// so a variable whose VALUE happens to mention one of the names, or whose key
// merely starts with one (ANTHROPIC_API_KEY_BACKUP), survives.
//
// The key comparison is CASE-INSENSITIVE, and unconditionally so on every
// platform. Windows env lookups are case-insensitive, so a child there reads
// `anthropic_api_key` as the billing switch just the same; an exact-case scrub
// would hand it straight through and bill a run the user was told is inside
// their plan. The alternative — a GOOS-tagged case-insensitive variant — would
// put the load-bearing guarantee on a code path this project's Linux CI can
// never execute, which is how the hole got here in the first place. One rule,
// exercised by every test run on every platform, is worth more than a
// platform-exact one nobody runs. The cost on unix, where case does matter, is
// that a variable deliberately named as a case-variant of a billing key
// (`anthropic_api_key`) is also deleted — nothing reads it there, so nothing
// breaks. Prefix and value behaviour are unchanged everywhere.
//
// The input is never modified; the result is a fresh slice safe to hand to
// exec.Cmd.Env.
func Scrub(parent []string) []string {
	out := make([]string, 0, len(parent))
	for _, kv := range parent {
		if isScrubbed(kv) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// isScrubbed reports whether a "KEY=value" entry names a scrubbed variable,
// comparing the whole key without regard to case (see Scrub).
func isScrubbed(kv string) bool {
	key, _, _ := strings.Cut(kv, "=")
	for _, scrubbed := range scrubbedVars {
		if strings.EqualFold(key, scrubbed) {
			return true
		}
	}
	return false
}
