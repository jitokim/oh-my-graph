package graph

import (
	"io/fs"
	"os"
	"path/filepath"
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
