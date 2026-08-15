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

// TestChangelogSectionHasSubstance refuses a release whose CHANGELOG section is
// a heading and nothing else.
//
// It guards the release body, which `scripts/release-notes.sh` extracts from
// exactly this section. Before that script existed goreleaser wrote the body
// itself, from commit subjects: v0.8.0 shipped as six SHAs, with the headline of
// the release — this project's first outside contribution — as one unattributed
// line among them. Sourcing the body from the changelog only helps if the
// changelog was written, so an empty section has to fail somewhere, and failing
// in the release PR is far better than failing after a tag is pushed (a tag is
// public the moment it lands; a red PR is not).
//
// Three non-blank lines is a floor, not a standard. It cannot judge whether the
// prose is any good — only a reviewer can — but it does catch the two ways a
// release actually goes out empty: bumping the version and forgetting the
// entries, and promoting an `## [Unreleased]` heading that had nothing under it.
func TestChangelogSectionHasSubstance(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	if err != nil {
		t.Fatalf("read CHANGELOG.md: %v", err)
	}
	heading := "## [v" + Version + "]"
	var collecting bool
	var substantive int
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, heading):
			collecting = true
		case collecting && strings.HasPrefix(line, "## ["):
			collecting = false
		case collecting && strings.TrimSpace(line) != "":
			substantive++
		}
	}
	if !collecting && substantive == 0 {
		t.Fatalf("CHANGELOG.md has no %s section — the release body is built from it", heading)
	}
	if substantive < 3 {
		t.Fatalf("CHANGELOG.md's %s section has %d non-blank line(s); a release body needs more than a heading", heading, substantive)
	}
}

// TestVersionMatchesChangelog pins the Version constant to CHANGELOG.md's
// topmost `## [vX.Y.Z]` release heading. It exists because two releases
// (v0.1.1 and v0.2.0) shipped with `oh-my-graph version` still reporting
// 0.1.0: nothing tied the constant to the release process, so bumping the
// changelog without the constant was invisible to CI. A release PR that
// bumps one without the other now fails here instead of shipping stale.
//
// `## [Unreleased]` is skipped: Keep a Changelog's staging heading names no
// version, so it is not the RELEASE heading this test pins against. Skipping
// it costs the guard nothing — the first real `## [vX.Y.Z]` below it is still
// checked, so a release PR that bumps the constant without promoting the
// Unreleased entries into a version heading still fails here.
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
		if got == "Unreleased" {
			continue
		}
		if want := "v" + Version; got != want {
			t.Fatalf("CHANGELOG.md topmost release heading is %q, want %q — bump cmd/oh-my-graph/version.go and the changelog entry together", got, want)
		}
		return
	}
	t.Fatal("no `## [` release heading found in CHANGELOG.md")
}
