package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The changelog-fragment rule of ADR 0042, §2.1, stated in Go.
//
// An unreleased CHANGELOG entry is a file the pull request owns,
// `changelog.d/<issue>-<slug>.md`, so two PRs never edit a common file (#304).
// scripts/changelog-fragment-check.sh is the shell statement of the same rule,
// asked by the per-PR gate and by the release collector;
// TestChangelogFragmentRuleAgreesWithTheScript runs both over one table so they
// cannot drift apart.

// changelogSections is the fixed list a fragment's heading is drawn from, in
// the canonical order the collector writes them in.
var changelogSections = []string{
	"Added", "Changed", "Deprecated", "Removed", "Fixed",
	"Security", "Documented", "Repository", "Known limits",
}

var (
	fragmentName      = regexp.MustCompile(`^[0-9]+-[a-z0-9]+(-[a-z0-9]+)*\.md$`)
	markdownHeading   = regexp.MustCompile(`^#+([ \t]|$)`)
	errNotAFragment   = errors.New("not a well-formed changelog fragment")
	fragmentDirectory = filepath.Join("..", "..", "changelog.d")
)

// checkFragment reports why a file named name with contents body is not a
// fragment, or nil if it is one.
func checkFragment(name, body string) error {
	if !fragmentName.MatchString(name) {
		return fmt.Errorf("%w: %q is not a fragment name — want <issue>-<slug>.md", errNotAFragment, name)
	}
	known := map[string]bool{}
	for _, s := range changelogSections {
		known[s] = true
	}
	var headed, fenced bool
	prose := 0
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		if !headed {
			if line == "" {
				continue
			}
			headed = true
			section, ok := strings.CutPrefix(line, "### ")
			switch {
			case !ok:
				return fmt.Errorf("%w: the first non-blank line must be a ### <Section> heading, got %q", errNotAFragment, line)
			case section == "Documentation":
				return fmt.Errorf("%w: unknown section %q — this changelog calls it \"Documented\"", errNotAFragment, section)
			case !known[section]:
				return fmt.Errorf("%w: unknown section %q — want one of %s", errNotAFragment, section, strings.Join(changelogSections, ", "))
			}
			continue
		}
		if strings.HasPrefix(raw, "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if markdownHeading.MatchString(line) {
			return fmt.Errorf("%w: a fragment holds exactly one heading; write a second fragment for a second section, not %q", errNotAFragment, line)
		}
		if strings.TrimSpace(line) != "" {
			prose++
		}
	}
	switch {
	case fenced:
		return fmt.Errorf("%w: a ``` fence is never closed", errNotAFragment)
	case !headed:
		return fmt.Errorf("%w: empty — a fragment is a ### <Section> heading and the entry under it", errNotAFragment)
	case prose == 0:
		return fmt.Errorf("%w: a heading with no entry under it", errNotAFragment)
	}
	return nil
}

// fragmentCase is one row of the table both statements of the rule are run
// over.
type fragmentCase struct {
	name, file, body string
	wantErr          string // "" means a fragment; otherwise a substring of the refusal
}

var fragmentCases = []fragmentCase{
	{name: "a bullet under a known section", file: "304-changelog-fragments.md",
		body: "### Fixed\n\n- **A thing is fixed.** ([#304](https://example))\n"},
	{name: "known limits is a section", file: "12-a.md",
		body: "### Known limits\n\n- Not yet measured.\n"},
	{name: "leading blank lines and a trailing space on the heading", file: "1-x.md",
		body: "\n\n### Added \n- one\n"},
	{name: "a fenced heading is an example, not a second heading", file: "7-fence.md",
		body: "### Documented\n\n- quotes this:\n\n```\n### Added\n```\n"},
	{name: "a multi-part slug", file: "293-self-dev-review-verdict.md",
		body: "### Changed\n- x\n"},
	{name: "CRLF line endings and a leading blank line", file: "1-crlf.md",
		body: "\r\n### Fixed\r\n\r\n- x\r\n"},

	{name: "underscore in the name", file: "304_x.md",
		body: "### Fixed\n- x\n", wantErr: "not a fragment name"},
	{name: "no issue number", file: "fix-thing.md",
		body: "### Fixed\n- x\n", wantErr: "not a fragment name"},
	{name: "not markdown", file: "304-x.txt",
		body: "### Fixed\n- x\n", wantErr: "not a fragment name"},
	{name: "upper case slug", file: "304-Fix.md",
		body: "### Fixed\n- x\n", wantErr: "not a fragment name"},
	{name: "blank", file: "304-x.md",
		body: "\n  \n", wantErr: "empty"},
	{name: "heading only", file: "304-x.md",
		body: "### Fixed\n\n", wantErr: "no entry under it"},
	{name: "prose before the heading", file: "304-x.md",
		body: "- x\n### Fixed\n", wantErr: "first non-blank line"},
	{name: "a level-two heading", file: "304-x.md",
		body: "## Fixed\n- x\n", wantErr: "first non-blank line"},
	{name: "the old Documentation name", file: "304-x.md",
		body: "### Documentation\n- x\n", wantErr: "Documented"},
	{name: "an unknown section", file: "304-x.md",
		body: "### Not fixed, and not claimed\n- x\n", wantErr: "unknown section"},
	{name: "two sections in one file", file: "304-x.md",
		body: "### Added\n- x\n\n### Fixed\n- y\n", wantErr: "exactly one heading"},
	{name: "an unclosed fence", file: "304-x.md",
		body: "### Added\n- x\n```\ncode\n", wantErr: "never closed"},
}

func TestCheckFragment(t *testing.T) {
	for _, tc := range fragmentCases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkFragment(tc.file, tc.body)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("checkFragment(%q) = %v, want a fragment", tc.file, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("checkFragment(%q) = %v, want a refusal mentioning %q", tc.file, err, tc.wantErr)
			}
			if !errors.Is(err, errNotAFragment) {
				t.Fatalf("checkFragment(%q) = %v, want it to wrap errNotAFragment", tc.file, err)
			}
		})
	}
}

// TestChangelogFragmentsAreWellFormed holds the real changelog.d/ to the rule.
//
// The collector skips nothing tracked — it refuses a release while any file
// there is not a fragment — but the cheapest place to learn of a misnamed or
// malformed one is the PR that added it. The per-PR gate refuses it too; this
// also catches one a `no-changelog` PR brought in, which the gate excused.
//
// It reads what git tracks, as the gate and the collector do, not the
// directory: a .DS_Store or an editor's swap file there is local noise, and
// failing `make test` over it would test the developer's machine.
func TestChangelogFragmentsAreWellFormed(t *testing.T) {
	out, err := exec.Command("git", "-C", filepath.Join("..", ".."), "ls-files", "-z", "--", "changelog.d/").Output()
	if err != nil {
		t.Fatalf("git ls-files changelog.d/: %v", err)
	}
	var readme bool
	for _, path := range strings.Split(string(out), "\x00") {
		name, ok := strings.CutPrefix(path, "changelog.d/")
		switch {
		case !ok:
			continue
		case name == "README.md":
			readme = true
			continue
		case strings.Contains(name, "/"):
			t.Errorf("changelog.d/%s is below changelog.d/; a fragment lives directly in it", name)
			continue
		}
		body, err := os.ReadFile(filepath.Join(fragmentDirectory, name))
		if err != nil {
			t.Fatalf("read changelog.d/%s: %v", name, err)
		}
		if err := checkFragment(name, string(body)); err != nil {
			t.Errorf("changelog.d/%s: %v (see changelog.d/README.md)", name, err)
		}
	}
	if !readme {
		t.Error("changelog.d/README.md is missing — it is what tells a contributor the fragment name and shape")
	}
}

// unreleasedPointer is the one paragraph `## [Unreleased]` holds (ADR 0042,
// §2.4). Rewording it in CHANGELOG.md means rewording it here.
const unreleasedPointer = "Unreleased entries are not written here. Each pull request writes its entry\n" +
	"as its own file in [`changelog.d/`](changelog.d/), and the release PR collects\n" +
	"them into the version's section with `scripts/changelog-collect.sh`, so two\n" +
	"pull requests never edit a common file\n" +
	"([ADR 0042](docs/adr/0042-a-changelog-entry-is-a-file-the-pr-owns.md)). What\n" +
	"has landed since the last release is the list of files in that directory.\n"

// unreleasedEntries returns every non-blank line of text's `## [Unreleased]`
// section that is not part of the pointer paragraph — fenced or not, a list
// item of any marker, a heading of any depth, or plain prose: whatever it is,
// the collector would leave it stranded there. The bool reports whether the
// section exists at all.
func unreleasedEntries(text, pointer string) ([]string, bool) {
	var found, collecting, fenced bool
	var section []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
		}
		switch {
		case !fenced && strings.HasPrefix(line, "## [Unreleased]"):
			found, collecting = true, true
			continue
		case !fenced && collecting && strings.HasPrefix(line, "## ["):
			collecting = false
		}
		if collecting {
			section = append(section, line)
		}
	}
	rest := strings.Replace(strings.Join(section, "\n")+"\n", pointer, "", 1)
	var entries []string
	for _, line := range strings.Split(rest, "\n") {
		if strings.TrimSpace(line) != "" {
			entries = append(entries, line)
		}
	}
	return entries, found
}

// TestUnreleasedSectionHoldsNoEntries keeps `## [Unreleased]` to its pointer
// paragraph (ADR 0042, §2.4): that paragraph and blank lines, nothing else.
//
// The collector reads only changelog.d/, so an entry written under Unreleased
// — by a `no-changelog` PR the gate excused, or by a hand edit — would sit there
// through the release cut and never reach a release body. There is one home for
// an unreleased entry, and this is what makes it one. Admitting only the fixed
// paragraph, rather than refusing the entry shapes one can list, is what leaves
// no `+ ` item, numbered item or bare prose paragraph to slip past.
func TestUnreleasedSectionHoldsNoEntries(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	if err != nil {
		t.Fatalf("read CHANGELOG.md: %v", err)
	}
	entries, found := unreleasedEntries(string(data), unreleasedPointer)
	if !found {
		t.Fatal("CHANGELOG.md has no ## [Unreleased] section — Keep a Changelog readers, and its compare footnote, look for it")
	}
	for _, line := range entries {
		t.Errorf("CHANGELOG.md's ## [Unreleased] section holds a line that is not its pointer paragraph: %q.\n"+
			"Since ADR 0042 an unreleased entry is its own file, changelog.d/<issue>-<slug>.md (see changelog.d/README.md); "+
			"the release collector reads only that directory, so a line here would never reach a release body. "+
			"If you reworded the pointer paragraph itself, reword unreleasedPointer to match.", line)
	}
}

func TestUnreleasedEntries(t *testing.T) {
	const pointer = "Entries live in [changelog.d/](changelog.d/)\nuntil a release collects them.\n"
	for _, tc := range []struct {
		name, text string
		want       int
		found      bool
	}{
		{name: "the pointer paragraph alone", text: "## [Unreleased]\n\n" + pointer + "\n## [v1.0.0]\n\n### Fixed\n\n- old\n", found: true},
		{name: "the pointer as the last section", text: "## [Unreleased]\n\n" + pointer, found: true},
		{name: "a heading", text: "## [Unreleased]\n\n### Fixed\n\n## [v1.0.0]\n", want: 1, found: true},
		{name: "a deep heading", text: "## [Unreleased]\n\n" + pointer + "\n##### Fixed\n\n## [v1.0.0]\n", want: 1, found: true},
		{name: "a list item", text: "## [Unreleased]\n\n" + pointer + "- **a fix.**\n\n## [v1.0.0]\n", want: 1, found: true},
		{name: "a plus item", text: "## [Unreleased]\n\n" + pointer + "+ a fix.\n\n## [v1.0.0]\n", want: 1, found: true},
		{name: "a numbered item", text: "## [Unreleased]\n\n" + pointer + "1. a fix.\n\n## [v1.0.0]\n", want: 1, found: true},
		{name: "a prose paragraph", text: "## [Unreleased]\n\n" + pointer + "\nA fix, in prose,\nover two lines.\n\n## [v1.0.0]\n", want: 2, found: true},
		{name: "a fenced block", text: "## [Unreleased]\n\n" + pointer + "```\n### Fixed\n- x\n```\n\n## [v1.0.0]\n", want: 4, found: true},
		{name: "a reworded pointer", text: "## [Unreleased]\n\nEntries live elsewhere.\n\n## [v1.0.0]\n", want: 1, found: true},
		{name: "released sections are not Unreleased", text: "## [Unreleased]\n\n" + pointer + "## [v1.0.0]\n\n### Added\n\n- x\n", found: true},
		{name: "no section", text: "## [v1.0.0]\n\n- x\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, found := unreleasedEntries(tc.text, pointer)
			if len(got) != tc.want || found != tc.found {
				t.Fatalf("unreleasedEntries = %q, %v; want %d entr(ies), found=%v", got, found, tc.want, tc.found)
			}
		})
	}
}
