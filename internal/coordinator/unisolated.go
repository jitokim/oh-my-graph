package coordinator

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// UnisolatedScan is the plan-time answer to "does this plan work anywhere
// oh-my-graph cannot isolate?" — the disclosure half of the limit SECURITY.md
// states ("Isolation stops at the invocation repository"). It is attached to
// every Plan whose text names a local git checkout outside that boundary, and
// nil otherwise, so a caller can print it unconditionally.
//
// It is a WARNING and never a refusal. A goal spanning two repositories is a
// legitimate thing to ask for; the engine simply provisions no worktree and
// takes no lock outside the one repository it was invoked from, so a node that
// switches a branch in a shared checkout races whatever else is standing in it
// (the collision reported in #103). Refusing the plan would break a working
// use case to fix a disclosure problem.
//
// Root is the boundary itself: the invocation repository's root when
// oh-my-graph was invoked inside a checkout (IsRepo), and the invocation
// directory when it was not — in which case nothing at all is isolated, and
// the printed warning says so.
type UnisolatedScan struct {
	Root   string
	IsRepo bool
	// Paths has one entry per DISTINCT checkout, in the order the plan text
	// first named each: goal mentions before node mentions, nodes in graph
	// order. Never empty — a scan that found nothing is nil.
	Paths []UnisolatedPath
}

// UnisolatedPath is one local git checkout outside the invocation repository
// that this plan's text names by absolute path.
//
// Repo is the checkout's root (the directory holding .git), which is what the
// hazard belongs to: two mentions of two files in the same repository are one
// warning, because they are one HEAD. Mention is the path as it was written,
// kept so the user can find the line they wrote — it may be a file deep inside
// Repo, or a `~/...` spelling of it.
type UnisolatedPath struct {
	Repo    string
	Mention string
	// InGoal records that the user's own goal text named this checkout;
	// NodeIDs lists the planned nodes whose prompt did, in graph order. At
	// least one of the two is always set.
	InGoal  bool
	NodeIDs []string
}

// pathMention matches an absolute filesystem path written in prose or in a
// shell command: a leading `/`, or a `~/` home spelling, followed by at least
// one more character of a conservative path alphabet.
//
// The leading group is what keeps a branch name out: `feat/ingest-hardening`
// contains a slash, but that slash is preceded by a word character, so the
// match never starts there. Go's regexp has no lookbehind, so the preceding
// character is consumed by a non-capturing group and the path itself is the
// capture.
//
// The alphabet deliberately stops at `{`, `"`, backtick, `)` and whitespace,
// which is what makes a templated path degrade safely rather than wrongly:
// `/tmp/shepherd-{{ inputs.pr }}` (graphs/merge-shepherd.yaml) matches as
// `/tmp/shepherd-`, a path that resolves into no checkout, so it is dropped by
// isForeignCheckout rather than reported under a guessed name.
var pathMention = regexp.MustCompile(`(?:^|[^\w~./-])(~?/[\w~./+@-]+)`)

// scanUnisolated reports the checkouts outside root that goal and g's prompts
// name. It reads the filesystem (does this path resolve into a git checkout?)
// and never runs git: a fifth spawner would need its own ADR, and the question
// here — "is there a .git above this path" — is answered by stat.
//
// It is called with the planner's OWN prompts, before skill mapping inlines
// any SKILL.md body into them (attemptPlan). A skill body is the user's local
// documentation and routinely names absolute paths that have nothing to do
// with the goal; scanning after inlining would report those as if the plan had
// chosen them.
func scanUnisolated(root invocationRoot, goal string, g *graph.Graph) *UnisolatedScan {
	byRepo := map[string]*UnisolatedPath{}
	var found []*UnisolatedPath

	record := func(nodeID, text string) {
		for _, mention := range pathMentions(text) {
			repo, ok := root.isForeignCheckout(mention)
			if !ok {
				continue
			}
			entry := byRepo[repo]
			if entry == nil {
				entry = &UnisolatedPath{Repo: repo, Mention: mention}
				byRepo[repo] = entry
				found = append(found, entry)
			}
			if nodeID == "" {
				entry.InGoal = true
				continue
			}
			if !slices.Contains(entry.NodeIDs, nodeID) {
				entry.NodeIDs = append(entry.NodeIDs, nodeID)
			}
		}
	}

	record("", goal)
	for _, node := range g.Nodes {
		record(node.ID, node.Prompt)
	}
	if len(found) == 0 {
		return nil
	}

	paths := make([]UnisolatedPath, 0, len(found))
	for _, entry := range found {
		paths = append(paths, *entry)
	}
	return &UnisolatedScan{Root: root.dir, IsRepo: root.isRepo, Paths: paths}
}

// pathMentions extracts the distinct absolute paths written in text, in the
// order they appear. Trailing separators and sentence punctuation are trimmed,
// so "in /Users/me/repo/." and "/Users/me/repo" are the same mention.
func pathMentions(text string) []string {
	var mentions []string
	for _, match := range pathMention.FindAllStringSubmatch(text, -1) {
		mention := strings.TrimRight(match[1], "./-")
		if mention == "" || slices.Contains(mentions, mention) {
			continue
		}
		mentions = append(mentions, mention)
	}
	return mentions
}

// invocationRoot is the boundary auto's isolation stops at: the invocation
// repository's root, or — when oh-my-graph was not invoked inside a checkout
// at all — the invocation directory, with isRepo false.
type invocationRoot struct {
	dir    string
	isRepo bool
}

// resolveInvocationRoot locates the boundary from the directory planned nodes
// will run in. dir is empty in production, meaning the process's working
// directory, which is exactly where `auto` runs every planned node
// (validatePlannedNodeCwd); tests pass a temp dir.
//
// The second result is false when the working directory cannot be read at all.
// That is not an error worth failing a paid plan over — it only means this
// advisory cannot be computed — so the caller drops the scan and stays silent
// rather than warning about a boundary it could not find.
func resolveInvocationRoot(dir string) (invocationRoot, bool) {
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return invocationRoot{}, false
		}
		dir = wd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return invocationRoot{}, false
	}
	if root, ok := checkoutRootOf(abs); ok {
		return invocationRoot{dir: root, isRepo: true}, true
	}
	return invocationRoot{dir: resolveSymlinks(abs)}, true
}

// isForeignCheckout answers the whole rule for one written path: does it
// resolve into a git checkout, and is that checkout outside the boundary? It
// returns the checkout's root.
//
// Requiring a checkout is what keeps the false-positive rate honest. The
// hazard being warned about is a shared HEAD, so a path that is not in a
// repository cannot have it: a `/tmp/...` scratch path, a `/usr/local/...`
// tool, a documentation URL-ish path, and a file that simply does not exist
// all fire nothing. A path inside the invocation repository fires nothing
// either — that one IS the isolated repository.
func (r invocationRoot) isForeignCheckout(mention string) (string, bool) {
	path, ok := expandHome(mention)
	if !ok || !filepath.IsAbs(path) {
		return "", false
	}
	repo, ok := checkoutRootOf(filepath.Clean(path))
	if !ok {
		return "", false
	}
	if withinDir(r.dir, repo) {
		return "", false
	}
	return repo, true
}

// checkoutRootOf walks up from path looking for a `.git` entry, and returns
// the directory holding it with symlinks resolved. Both shapes count: a `.git`
// directory (an ordinary clone) and a `.git` FILE (a linked worktree, which is
// a checkout with its own HEAD and therefore its own collision).
//
// It walks rather than requiring path itself to exist, so a goal naming a file
// a node has yet to create still resolves to the repository that file would
// land in.
func checkoutRootOf(path string) (string, bool) {
	for dir := path; ; {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return resolveSymlinks(dir), true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// resolveSymlinks canonicalizes an existing path, and returns it unchanged
// when it does not exist. Both sides of the boundary comparison go through it
// so that /tmp and /private/tmp — the macOS pair every temp-dir test lands in
// — are recognized as the same directory.
func resolveSymlinks(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// expandHome resolves a `~/...` mention against the user's home directory —
// the spelling goals are actually written in. The second result is false when
// there is no home directory to expand against, which drops the mention: a
// warning naming a path that was never resolved would be worse than silence.
func expandHome(path string) (string, bool) {
	if !strings.HasPrefix(path, "~/") {
		return path, true
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return filepath.Join(home, path[2:]), true
}

// withinDir reports whether child is dir itself or lies under it.
func withinDir(dir, child string) bool {
	rel, err := filepath.Rel(dir, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
