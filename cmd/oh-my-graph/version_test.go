package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
// Three lines is a floor, not a standard: it cannot judge whether the prose is
// any good — only a reviewer can. What it does catch is the two ways a release
// actually goes out empty: bumping the version and forgetting the entries, and
// promoting an `## [Unreleased]` heading whose Keep-a-Changelog subheadings had
// nothing under them.
//
// The second is why HEADING LINES DO NOT COUNT. A section of
// `### Added` / `### Fixed` / `### Changed` and nothing else is three non-blank
// lines, so counting them made this test pass on precisely the emptiness it
// claimed to catch — and the release body would then have been three
// subheadings. A line only counts if it is prose.
func TestChangelogSectionHasSubstance(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	if err != nil {
		t.Fatalf("read CHANGELOG.md: %v", err)
	}
	heading := "## [v" + Version + "]"
	var found, collecting bool
	var prose int
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, heading):
			found, collecting = true, true
		case collecting && strings.HasPrefix(line, "## ["):
			collecting = false
		case collecting && trimmed != "" && !strings.HasPrefix(trimmed, "#"):
			prose++
		}
	}
	if !found {
		t.Fatalf("CHANGELOG.md has no %s section — the release body is built from it", heading)
	}
	if prose < 3 {
		t.Fatalf("CHANGELOG.md's %s section has %d line(s) of prose (headings do not count); a release body needs more than a heading", heading, prose)
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

// TestPluginManifestsMatchVersion pins the two plugin manifests to the Version
// constant.
//
// It exists because the release checklist said, in as many words, that "CI does
// not guard these two — the checklist is the only thing that does" — and then
// v0.8.0 shipped with both manifests still reading 0.7.0. A checklist item that
// depends on a person reading it is not a guard; it is a note about a guard
// nobody wrote. This is the same argument that produced
// TestVersionMatchesChangelog after two releases went out reporting a stale
// version, and it has now been made twice by the same failure.
//
// The manifests are read as text rather than decoded: the assertion is about
// one field, and a JSON round-trip would let an unrelated schema change here
// fail a test whose subject is the version string.
func TestPluginManifestsMatchVersion(t *testing.T) {
	want := `"version": "` + Version + `"`
	for _, rel := range []string{
		filepath.Join("..", "..", "plugin", ".claude-plugin", "plugin.json"),
		filepath.Join("..", "..", ".claude-plugin", "marketplace.json"),
	} {
		data, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if !strings.Contains(string(data), want) {
			t.Errorf("%s does not carry %s — bump it with cmd/oh-my-graph/version.go", rel, want)
		}
	}
}

// TestChangelogHasFootnoteForThisVersion and TestLimitationsStampMatchesVersion
// exist because the release checklist never called either place, and both went
// stale in the same way for three releases running: `[Unreleased]` still
// compared against v0.10.0 while v0.11.0, v0.12.0 and v0.13.0 shipped with no
// footnote at all, and docs/LIMITATIONS.md still stamped itself "as of v0.11.0"
// after two releases — in a file whose most recent commit had been titled
// "five sentences v0.11.0 made false".
//
// CONTRIBUTING says why a checklist item was never going to be enough:
// "A checklist item that depends on someone reading it is a note about a guard
// nobody wrote." So this is the guard, not another line to read.
func TestChangelogHasFootnoteForThisVersion(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	if err != nil {
		t.Fatalf("read CHANGELOG.md: %v", err)
	}
	text := string(body)

	footnote := fmt.Sprintf("[v%s]: https://", Version)
	if !strings.Contains(text, footnote) {
		t.Errorf("CHANGELOG.md has no link footnote for the current version: want a line starting %q.\n"+
			"Every released heading needs one, or the version's compare link resolves to nothing.", footnote)
	}

	// `[Unreleased]` must compare against the version that shipped, not against
	// whatever it compared against three releases ago.
	unreleased := fmt.Sprintf("[Unreleased]: https://github.com/jitokim/oh-my-graph/compare/v%s...HEAD", Version)
	if !strings.Contains(text, unreleased) {
		t.Errorf("CHANGELOG.md's [Unreleased] footnote does not compare against v%s.\n"+
			"want the line: %s\n"+
			"An [Unreleased] that points at an older tag silently claims the releases between them do not exist.", Version, unreleased)
	}
}

// TestLimitationsStampMatchesVersion pins the "as of" stamps in the honest-gaps
// document. A gaps file that stamps an older version is claiming its list was
// last checked against code that has since moved — which is the one thing that
// file exists not to do.
func TestLimitationsStampMatchesVersion(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "docs", "LIMITATIONS.md"))
	if err != nil {
		t.Fatalf("read docs/LIMITATIONS.md: %v", err)
	}
	text := string(body)

	// Assert PRESENCE of this version's stamp rather than the absence of older
	// ones: a file that dropped every stamp would pass an absence check while
	// telling the reader nothing.
	//
	// Presence is derived from the SAME regex the staleness loop uses, and that
	// is the whole point of this shape. The first version of this test read
	// `strings.Contains(text, "v"+Version)`, which is presence of the version
	// STRING, not of a stamp — and this file names its version in prose too
	// ("In v0.13.0, and on `main` today, a limit…"). Delete every `as of` stamp
	// and that check stayed satisfied by the prose while the loop below, having
	// nothing to iterate, said nothing either. The comment above promised a
	// guard the code did not implement; a release's meta-review found it.
	stale := regexp.MustCompile(`as of \*{0,2}v(\d+\.\d+\.\d+)`)
	stamps := stale.FindAllStringSubmatch(text, -1)
	if len(stamps) == 0 {
		t.Fatalf("docs/LIMITATIONS.md carries no 'as of vX' stamp at all — the gaps list no longer says which build it was checked against")
	}

	// And no stamp may name a version other than this one.
	for _, match := range stamps {
		if match[1] != Version {
			t.Errorf("docs/LIMITATIONS.md stamps %q while this build is v%s — the gaps list claims a check against code that has moved", match[0], Version)
		}
	}
}
