package handoff

import (
	"strings"
	"testing"
)

// TestLintSessions_ScopeAndRetry drives every LintSessions class through one
// two-node graph shape: child resumes parent's session, and the case decides
// how the two nodes' scopes and the child's retry are declared.
func TestLintSessions_ScopeAndRetry(t *testing.T) {
	cases := []struct {
		name       string
		parent     string // YAML fields spliced into the parent node
		child      string // YAML fields spliced into the session child
		wantField  string // Field of the single expected warning; "" = expect silence
		wantDetail string // substring the warning's Detail must carry
	}{
		// --- warning cases ---------------------------------------------------
		{
			name:       "cwd mismatch warns",
			parent:     `cwd: /work/app`,
			child:      `cwd: /work/other`,
			wantField:  "cwd",
			wantDetail: "session lookup is project-directory-scoped",
		},
		{
			name:       "unset child cwd against a set parent cwd warns",
			parent:     `cwd: /work/app`,
			child:      `type: claude-run`, // no cwd at all
			wantField:  "cwd",
			wantDetail: `session-parent "parent"`,
		},
		{
			name:       "worktree names differing warns",
			parent:     `worktree: lane-a`,
			child:      `worktree: lane-b`,
			wantField:  "worktree",
			wantDetail: "session lookup is project-directory-scoped",
		},
		{
			name:       "parent worktree against a plain child warns",
			parent:     `worktree: lane`,
			child:      `type: claude-run`,
			wantField:  "worktree",
			wantDetail: `worktree "" is not session-parent "parent"'s worktree "lane"`,
		},
		{
			name:       "retry on a session child warns",
			parent:     `cwd: /work/app`,
			child:      "cwd: /work/app\n    retry: { max: 1, on: [nonzero_exit] }",
			wantField:  "retry",
			wantDetail: "make the prompt work cold, or drop retry",
		},
		// --- silent cases ----------------------------------------------------
		{
			name:   "matching cwds stay silent",
			parent: `cwd: /work/app`,
			child:  `cwd: /work/app`,
		},
		{
			name:   "cwds matching after trimming stay silent",
			parent: `cwd: /work/app`,
			child:  `cwd: " /work/app "`,
		},
		{
			name:   "shared worktree pair stays silent",
			parent: `worktree: lane`,
			child:  `worktree: lane`,
		},
		{
			name:   "both scopes unset stay silent",
			parent: `type: claude-run`,
			child:  `type: claude-run`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := parseGraph(t, `
name: session-scope
nodes:
  - id: parent
    prompt: parent
    `+tc.parent+`
  - id: child
    prompt: child
    depends_on: [parent]
    handoff: session
    `+tc.child+`
`)
			warnings := LintSessions(g)
			if tc.wantField == "" {
				if len(warnings) != 0 {
					t.Fatalf("expected silence, got %v", warnings)
				}
				return
			}
			if len(warnings) != 1 {
				t.Fatalf("expected exactly one warning, got %v", warnings)
			}
			w := warnings[0]
			if w.NodeID != "child" || w.Field != tc.wantField {
				t.Fatalf("warning = %+v, want node child field %q", w, tc.wantField)
			}
			if !strings.Contains(w.Detail, tc.wantDetail) {
				t.Fatalf("detail %q should contain %q", w.Detail, tc.wantDetail)
			}
		})
	}
}

// TestLintSessions_ArtifactNodesAreNoneOfItsBusiness pins the sweep's scope:
// an artifact-handoff child may run anywhere and retry freely — no session is
// being resumed, so LintSessions must stay silent about it.
func TestLintSessions_ArtifactNodesAreNoneOfItsBusiness(t *testing.T) {
	g := parseGraph(t, `
name: artifact-scope
nodes:
  - { id: parent, prompt: parent, cwd: /work/app }
  - id: child
    prompt: child
    depends_on: [parent]
    cwd: /work/other
    retry: { max: 2, on: [nonzero_exit] }
`)
	if warnings := LintSessions(g); len(warnings) != 0 {
		t.Fatalf("artifact handoff should warn nothing, got %v", warnings)
	}
}
