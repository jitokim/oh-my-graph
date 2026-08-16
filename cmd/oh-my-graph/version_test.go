package main

import (
	"bytes"
	"os"
	"os/exec"
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

// TestEveryMergedPRIsInTheChangelog refuses a release that leaves a merged PR
// UNMENTIONED.
//
// Read that as written, because it is weaker than it first sounds and the
// weakness was found by mutating it rather than by reasoning: it asserts the
// number appears somewhere in the release's window, not that an entry describes
// it. A version heading that enumerates its PRs — as v0.9.0's does — satisfies
// this test for all of them at once. Deleting the link from #182's entry left
// it green, because the heading still named #182.
//
// That is still worth having: the failure it targets is a PR nobody remembered,
// and remembering it in a list is a visible act. It is NOT a guarantee that
// each change is described. Do not upgrade this comment without upgrading the
// check — claiming more than a test checks is how a green suite stops meaning
// anything.
//
// The release body IS the changelog's section for the tag
// (`scripts/release-notes.sh`), so an entry nobody wrote is a user-visible
// change nobody is told about — and no existing guard can see it.
// TestChangelogSectionHasSubstance only asks whether the section is empty; a
// section with two entries out of eight passes it comfortably. That is exactly
// what happened while cutting this release: #181, #182, #184, #176 and #177
// were all merged and all missing, and the omission was caught by hand.
//
// Every squash-merged PR carries `(#NNN)` in its subject, so the set of numbers
// since the last tag is computable. An entry is not owed for every PR — a
// refactor with no user-visible effect owes none — so a number may be excused,
// but only OUT LOUD: put it in the `## [Unreleased]` section as
// `<!-- no-changelog: 123 reason -->`. You may skip it; you may not skip it
// silently.
//
// Skipped when git or the tag history is unavailable (a source tarball, a
// shallow clone) rather than failing: this asserts about a repository, and a
// checkout without history is not a broken repository.
// isReleaseCut reports whether a commit is the release-prep commit — the one
// that writes the version heading and bumps the constant.
//
// It owes no changelog entry because it IS the changelog entry, and without
// this the check fails on every release, at the worst moment: green on the
// release PR (its own number is not merged yet) and red on `main` the instant
// it lands. That happened on v0.9.0.
//
// Recognised by what it touched, not by how its subject is worded: only a
// release cut changes CHANGELOG.md and version.go together. A wording rule
// would be a second thing to keep in sync with a habit.
// It also exempts a commit that changed NOTHING BUT the changelog. Such a
// commit has no change to describe — it IS the description — and demanding an
// entry for it makes the check eat its own tail: the PR that adds a missing
// entry is itself missing one, and so is the PR that adds THAT. Three PRs into
// v0.9.0 before the regress was recognised as structural rather than another
// edge case.
func isReleaseCut(sha string) bool {
	out, err := exec.Command("git", "show", "--name-only", "--format=", sha).Output()
	if err != nil {
		return false
	}
	files := strings.Fields(string(out))
	if len(files) == 0 {
		return false
	}
	var changelog, version, other bool
	for _, f := range files {
		switch f {
		case "CHANGELOG.md":
			changelog = true
		case "cmd/oh-my-graph/version.go":
			version = true
		default:
			other = true
		}
	}
	// A release cut writes the heading and bumps the constant together, which
	// nothing else does — it may carry other files and still be one.
	if changelog && version {
		return true
	}
	// Changelog-only maintenance.
	return changelog && !other
}

func TestEveryMergedPRIsInTheChangelog(t *testing.T) {
	last, err := exec.Command("git", "describe", "--tags", "--abbrev=0").Output()
	if err != nil {
		t.Skip("no tag history here; nothing to compare against")
	}
	rng := strings.TrimSpace(string(last)) + "..HEAD"
	out, err := exec.Command("git", "log", "--format=%H %s", rng).Output()
	if err != nil {
		t.Skip("git log unavailable")
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	if err != nil {
		t.Fatalf("read CHANGELOG.md: %v", err)
	}
	// Two sections count, and both are needed: an entry lives under
	// `## [Unreleased]` while the work is merged, and moves under
	// `## [vX.Y.Z]` the moment the release is cut. Reading only the first
	// makes every entry vanish at the cut — which is precisely when this test
	// matters most, and is what the first version of it did.
	//
	// A number under an OLDER heading is history, not this release's entry,
	// so the window stops at the end of the current version's section.
	body := string(data)
	start := strings.Index(body, "## [Unreleased]")
	if start < 0 {
		t.Fatal("CHANGELOG.md has no ## [Unreleased] section")
	}
	rest := body[start:]
	cut := "## [v" + Version + "]"
	if i := strings.Index(rest, cut); i >= 0 {
		// Take through the end of the current version's section.
		after := rest[i+len(cut):]
		if end := strings.Index(after, "\n## ["); end >= 0 {
			rest = rest[:i+len(cut)+end]
		}
	} else if end := strings.Index(rest[len("## [Unreleased]"):], "\n## ["); end >= 0 {
		rest = rest[:len("## [Unreleased]")+end]
	}

	prRef := regexp.MustCompile(`\(#(\d+)\)\s*$`)
	var missing []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		sha, subject := parts[0], parts[1]
		m := prRef.FindStringSubmatch(subject)
		if m == nil {
			continue
		}
		if strings.Contains(rest, "#"+m[1]) || isReleaseCut(sha) {
			continue
		}
		missing = append(missing, subject)
	}
	if len(missing) > 0 {
		t.Fatalf("merged since %s but absent from ## [Unreleased] (%d):\n  %s\n\n"+
			"Write the entry, or excuse it out loud with `<!-- no-changelog: N reason -->`.",
			strings.TrimSpace(string(last)), len(missing), strings.Join(missing, "\n  "))
	}
}
