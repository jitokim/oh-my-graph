package graph

import (
	"io/fs"
	"testing"

	"github.com/jitokim/oh-my-graph/graphs"
)

// TestShippedGraphsLoad guards the example graphs that ship in graphs/: they
// must parse and pass validation, so a broken example is caught in CI rather
// than at a user's first run — which since `oh-my-graph init` means the copy
// unpacked out of the binary, not just the one in a repo checkout.
//
// The set is read from the embed FS rather than listed by hand: the embed
// pattern is a glob, so a template dropped into graphs/ ships to users
// immediately and must be guarded immediately. The previous hardcoded list had
// silently fallen two files behind.
func TestShippedGraphsLoad(t *testing.T) {
	entries, err := fs.ReadDir(graphs.FS, ".")
	if err != nil {
		t.Fatalf("read embedded graphs: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no graphs are embedded — the //go:embed pattern in graphs/embed.go matched nothing")
	}
	for _, entry := range entries {
		data, err := graphs.FS.ReadFile(entry.Name())
		if err != nil {
			t.Errorf("read embedded graph %s: %v", entry.Name(), err)
			continue
		}
		if _, err := Parse(data); err != nil {
			t.Errorf("shipped graph %s failed to load: %v", entry.Name(), err)
		}
	}
}
