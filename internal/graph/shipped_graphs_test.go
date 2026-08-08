package graph

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/graphs"
)

// shippedTemplateNames is the set of graph templates the binary carries, as
// slash-separated payload paths. It walks rather than ReadDir's the root
// because the payload is nested — `graphs/fragments/*.yaml` ships alongside
// `graphs/*.yaml` (ADR 0013) — and skips those fragment files: a fragment is a
// single-node definition, not a graph, and only loads through a template that
// cites it.
func shippedTemplateNames(t *testing.T) []string {
	t.Helper()
	var names []string
	err := fs.WalkDir(graphs.FS, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && !strings.Contains(path, "/") {
			names = append(names, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read embedded graphs: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("no graphs are embedded — the //go:embed patterns in graphs/embed.go matched nothing")
	}
	return names
}

// TestShippedGraphsLoad guards the example graphs as they exist in the repo
// checkout: they must load and pass validation, so a broken example is caught
// in CI rather than at a maintainer's next `oh-my-graph run graphs/...`.
//
// The set is read from the embed FS rather than listed by hand: the embed
// patterns are globs, so a template dropped into graphs/ ships to users
// immediately and must be guarded immediately. The previous hardcoded list had
// silently fallen two files behind.
//
// Loading goes through LoadFile — the path-aware seam `run` itself uses
// (ADR 0013) — rather than Parse over bytes, because a migrated template's
// `use:` resolves against its file's graphs/fragments/ sibling on disk. A
// fragment-free file loads identically either way.
func TestShippedGraphsLoad(t *testing.T) {
	for _, name := range shippedTemplateNames(t) {
		if _, err := LoadFile(filepath.Join("..", "..", "graphs", name)); err != nil {
			t.Errorf("shipped graph %s failed to load: %v", name, err)
		}
	}
}

// TestEmbeddedGraphsLoadFromTheBinarysOwnPayload is the same guarantee for the
// bytes a `go install` user actually receives, which is a different claim: the
// repo checkout has a graphs/fragments/ directory whether or not the binary
// embeds it. Unpacking the embed FS into a scratch directory and loading from
// THERE is what proves the payload is self-sufficient — that every `use:` in a
// shipped template finds its fragment in what the binary carries.
//
// Without this, `//go:embed *.yaml` (which does not descend into
// subdirectories) shipped two templates that died at load with "no fragment
// file at the fragment location", and no test in the repo noticed.
func TestEmbeddedGraphsLoadFromTheBinarysOwnPayload(t *testing.T) {
	root := t.TempDir()
	err := fs.WalkDir(graphs.FS, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := graphs.FS.ReadFile(path)
		if err != nil {
			return err
		}
		target := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("unpack the embedded payload: %v", err)
	}

	names := shippedTemplateNames(t)
	for _, name := range names {
		if _, err := LoadFile(filepath.Join(root, name)); err != nil {
			t.Errorf("embedded graph %s failed to load from the binary's own payload: %v", name, err)
		}
	}
	// A template that cites a fragment is the only reason this test can catch
	// anything the checkout test cannot, so at least one must exist — otherwise
	// the assertion above is satisfiable by a payload with no fragments at all.
	if !anyTemplateCitesAFragment(t, root, names) {
		t.Error("no embedded template cites a fragment — this test can no longer detect a missing fragments/ payload")
	}
}

// TestMergeVerdictRejectsThePromiseFamily pins the one shipped verdict pattern
// whose false PASS is irreversible: `merge-shepherd`'s `merge`, which once
// ended a real run green with nothing merged (ADR 0019 §1).
//
// The pattern is read out of the shipped graph rather than restated here, so
// this test judges what users actually get. It asserts two things at once:
//
//  1. the shipped `^` anchor rejects every promise reply below, and
//  2. the SAME pattern with `(?m)` prefixed ACCEPTS them.
//
// The second assertion is the point. The relaxation to `(?m)` was proposed on
// the theory that the SHA payload is "a fact a promise cannot produce", and it
// is not: seven hex characters are typeable, and `merge-shepherd.yaml` hands
// the model `MERGED 4f2a1c9` as its format example. What `^` actually rejects
// is the preamble a promise cannot do without. If a future change makes these
// replies pass, that theory has been re-adopted by accident.
func TestMergeVerdictRejectsThePromiseFamily(t *testing.T) {
	g, err := LoadFile(filepath.Join("..", "..", "graphs", "merge-shepherd.yaml"))
	if err != nil {
		t.Fatalf("load merge-shepherd: %v", err)
	}
	var pattern string
	for _, n := range g.Graph.Nodes {
		if n.ID == "merge" {
			pattern = n.SuccessCheck.ResultMatches
		}
	}
	if pattern == "" {
		t.Fatal("merge-shepherd's merge node declares no result_matches — the verdict is unchecked")
	}
	if strings.HasPrefix(pattern, "(?m)") {
		t.Fatalf("merge's verdict is line-anchored (%q); ADR 0019 refused that, and the promises below are why", pattern)
	}
	anchored := regexp.MustCompile(pattern)
	perLine := regexp.MustCompile("(?m)" + pattern)

	// Nothing merged in any of these. Each puts a well-formed verdict on a
	// line of its own, which is all a line anchor asks for.
	promises := map[string]string{
		"quotes the reply it will give later":             "CodeRabbit's re-review is mid-flight, so I will merge when it concludes.\nMy final reply will be:\n`MERGED 4f2a1c9 — ADR 0018`",
		"lists both verdicts as plan bullets":             "Plan:\n- `MERGED 4f2a1c9` — the squash merge has landed\n- `WITHHELD <reason>` — I did not merge\nI cannot pick one yet.",
		"quotes the instruction as a code block":          "The instructions say the reply must be:\n\n    MERGED 4f2a1c9\n\nand I cannot produce that until the squash lands.",
		"a to-do list with the SHA filled in later":       "Steps remaining: 1. gh pr merge --squash\nThen:\nMERGED 83edfad — I will fill in the real SHA.",
		"the PR head SHA, which exists before the squash": "Once I squash it the answer will be:\nMERGED 2be7c58 will be the result once I squash it.",
		"a bare WITHHELD with the reason stripped off":    "I have not decided yet.\nWITHHELD:",
	}
	for name, reply := range promises {
		if anchored.MatchString(reply) {
			t.Errorf("merge's verdict pattern ACCEPTS a promise (%s):\n%s", name, reply)
		}
		if name == "a bare WITHHELD with the reason stripped off" {
			continue // rejected for its missing payload, not for its position
		}
		if !perLine.MatchString(reply) {
			t.Errorf("(?m) no longer accepts %q — the counterfactual ADR 0019 rests on has drifted; re-measure before trusting §3", name)
		}
	}

	// The other half: a verdict that opens the reply still passes, both ways.
	for name, reply := range map[string]string{
		"MERGED with a short SHA": "MERGED 52a7373 — ADR 0015 lands.",
		"MERGED with a full SHA":  "MERGED 83edfad11db1ba0281cab9b615c4acadded38512 — ADR 0018 shipped.",
		"WITHHELD with a reason":  "WITHHELD CodeRabbit's re-review has not concluded.",
	} {
		if !anchored.MatchString(reply) {
			t.Errorf("merge's verdict pattern REJECTS a real verdict (%s): %s", name, reply)
		}
	}
}

// anyTemplateCitesAFragment reports whether any unpacked template resolved at
// least one `use:`, which is what makes the payload's fragments/ directory
// load-bearing.
func anyTemplateCitesAFragment(t *testing.T, root string, names []string) bool {
	t.Helper()
	for _, name := range names {
		loaded, err := LoadFile(filepath.Join(root, name))
		if err == nil && len(loaded.Resolutions) > 0 {
			return true
		}
	}
	return false
}
