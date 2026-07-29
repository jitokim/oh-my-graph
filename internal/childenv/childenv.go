// Package childenv owns the one environment policy every process oh-my-graph
// spawns must obey: the variables that silently switch the claude CLI from the
// user's logged-in subscription (OAuth) to metered API-key billing are DELETED
// from the child's environment.
//
// It is a dependency-free leaf package because there is now more than one
// spawner — runner.ClaudeCLIRunner (a claude node) and verify.ShellVerifier (a
// success_check.verify command) — and the two must not be able to disagree
// about the rule. A verification command may legitimately BE a claude
// invocation (`verify: { command: "claude -p ..." }`), so a scrub that lived
// only in the runner would leave exactly that case billed to the API.
//
// This is the load-bearing subscription-auth guarantee of the whole project.
// It is asserted here (the policy) and again on each spawner (the call site).
package childenv

import "strings"

// scrubbedVars are the environment variables deleted from every child process.
// Both are read by the claude CLI as "use API-key billing instead of the
// logged-in subscription", so leaving either in place would silently spend
// metered credits for a run the user expects to be inside their plan.
var scrubbedVars = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"}

// Scrub returns parent with every scrubbed variable removed, leaving everything
// else untouched. Matching is on the KEY (the text before the first '='), so a
// variable whose VALUE happens to mention one of the names, or whose key merely
// starts with one (ANTHROPIC_API_KEY_BACKUP), survives.
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

// isScrubbed reports whether a "KEY=value" entry names a scrubbed variable.
func isScrubbed(kv string) bool {
	key, _, _ := strings.Cut(kv, "=")
	for _, scrubbed := range scrubbedVars {
		if key == scrubbed {
			return true
		}
	}
	return false
}
