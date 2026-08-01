package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrintVersion(t *testing.T) {
	var buf bytes.Buffer
	printVersion(&buf)
	want := "oh-my-graph " + Version + "\n"
	if got := buf.String(); got != want {
		t.Errorf("printVersion output = %q, want %q", got, want)
	}
}

// TestVersionMatchesChangelog pins the Version constant to CHANGELOG.md's
// topmost `## [vX.Y.Z]` release heading. It exists because two releases
// (v0.1.1 and v0.2.0) shipped with `oh-my-graph version` still reporting
// 0.1.0: nothing tied the constant to the release process, so bumping the
// changelog without the constant was invisible to CI. A release PR that
// bumps one without the other now fails here instead of shipping stale.
func TestVersionMatchesChangelog(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	if err != nil {
		t.Fatalf("read CHANGELOG.md: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "## [") {
			continue
		}
		rest := strings.TrimPrefix(line, "## [")
		end := strings.IndexByte(rest, ']')
		if end < 0 {
			t.Fatalf("malformed release heading in CHANGELOG.md: %q", line)
		}
		got := rest[:end]
		if want := "v" + Version; got != want {
			t.Fatalf("CHANGELOG.md topmost release heading is %q, want %q — bump cmd/oh-my-graph/version.go and the changelog entry together", got, want)
		}
		return
	}
	t.Fatal("no `## [` release heading found in CHANGELOG.md")
}
