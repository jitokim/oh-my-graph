package handoff

import (
	"fmt"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// LintSessions statically inspects every session-handoff node and returns one
// advisory warning per way its `--resume` may quietly not deliver the parent
// conversation it was written for:
//
//   - the child's directory differs from its session-parent's — claude's
//     session lookup is project-directory-scoped, so a child running
//     somewhere else may resume cold or attach to the wrong project. Worktree
//     names are compared exactly (a set-vs-unset difference counts: a managed
//     checkout is never the parent's plain cwd); cwd strings are compared as
//     written, after trimming — no path resolution, since two spellings of
//     one directory are knowable only at run time;
//   - the node also declares a retry — a retried attempt never resumes the
//     parent session (the scheduler clears ResumeSession), so the prompt must
//     work cold, or the retry should go.
//
// Both stay warnings rather than load errors because each is expressible on
// purpose: a deliberate scope switch, or a retry whose prompt was written
// cold-safe. Callers should hand LintSessions an already-validated graph; a
// session node without exactly one real parent is skipped here, since
// validation owns those errors.
func LintSessions(g *graph.Graph) []Warning {
	var warnings []Warning
	for _, node := range g.Nodes {
		if node.Handoff != graph.HandoffSession || len(node.DependsOn) != 1 {
			continue
		}
		parent, ok := g.NodeByID(node.DependsOn[0])
		if !ok {
			continue
		}
		if w, mismatch := sessionScopeMismatch(node, parent); mismatch {
			warnings = append(warnings, w)
		}
		if node.Retry != nil && node.Retry.Max > 0 {
			warnings = append(warnings, Warning{
				NodeID: node.ID,
				Field:  "retry",
				Detail: "handoff: session with retry — a retried attempt does not resume the parent session; make the prompt work cold, or drop retry",
			})
		}
	}
	return warnings
}

// sessionScopeMismatch judges whether node runs in a different directory than
// its session-parent. Worktrees are judged first: when either side names one,
// only an exactly matching name on the other side means the same checkout.
// Otherwise the two declared cwds are compared as written, after trimming.
func sessionScopeMismatch(node, parent graph.Node) (Warning, bool) {
	if node.Worktree != "" || parent.Worktree != "" {
		if node.Worktree != parent.Worktree {
			return Warning{
				NodeID: node.ID,
				Field:  "worktree",
				Detail: fmt.Sprintf("handoff: session but worktree %q is not session-parent %q's worktree %q — claude's session lookup is project-directory-scoped, so the resume may start cold or attach to the wrong project", node.Worktree, parent.ID, parent.Worktree),
			}, true
		}
		return Warning{}, false
	}
	if strings.TrimSpace(node.Cwd) != strings.TrimSpace(parent.Cwd) {
		return Warning{
			NodeID: node.ID,
			Field:  "cwd",
			Detail: fmt.Sprintf("handoff: session but cwd %q is not session-parent %q's cwd %q — claude's session lookup is project-directory-scoped, so the resume may start cold or attach to the wrong project", node.Cwd, parent.ID, parent.Cwd),
		}, true
	}
	return Warning{}, false
}
