package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The changelog scripts of ADR 0042, exercised in a temporary git repository:
// the per-PR gate (changelog-entry-check.sh), the fragment rule
// (changelog-fragment-check.sh), the release collector (changelog-collect.sh),
// and release-notes.sh reading what the collector wrote. Real `git` and `sh`
// only — no network, no model CLI — so every case runs under `make test`.

const baseChangelog = `# Changelog

## [Unreleased]

Unreleased entries live in changelog.d/ until a release collects them.

## [v1.0.0] - 2026-01-01

The first release, in prose.

### Fixed

- **An old fix.**

[Unreleased]: https://example.com/compare/v1.0.0...HEAD
[v1.0.0]: https://example.com/releases/v1.0.0
`

const fragmentReadme = "# changelog.d\n\nOne file per unreleased entry.\n"

// scriptRepo is a scratch repository carrying a copy of the real scripts/.
type scriptRepo struct {
	t   *testing.T
	dir string
}

func newScriptRepo(t *testing.T) *scriptRepo {
	t.Helper()
	r := &scriptRepo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.git("config", "user.email", "test@example.com")
	r.git("config", "user.name", "test")
	r.git("config", "commit.gpgsign", "false")

	scripts, err := filepath.Glob(filepath.Join("..", "..", "scripts", "*.sh"))
	if err != nil || len(scripts) == 0 {
		t.Fatalf("find scripts/*.sh: %v (%d found)", err, len(scripts))
	}
	for _, s := range scripts {
		body, err := os.ReadFile(s)
		if err != nil {
			t.Fatalf("read %s: %v", s, err)
		}
		r.write(filepath.Join("scripts", filepath.Base(s)), string(body))
	}
	r.write("CHANGELOG.md", baseChangelog)
	r.write("changelog.d/README.md", fragmentReadme)
	r.commit("base")
	return r
}

func (r *scriptRepo) cmd(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Dir = r.dir
	// The developer's own git configuration (hooks, signing, a default branch)
	// must not reach a scratch repository.
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "LC_ALL=C")
	return cmd
}

func (r *scriptRepo) git(args ...string) string {
	r.t.Helper()
	out, err := r.cmd("git", args...).CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *scriptRepo) write(rel, body string) {
	r.t.Helper()
	path := filepath.Join(r.dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *scriptRepo) read(rel string) string {
	r.t.Helper()
	body, err := os.ReadFile(filepath.Join(r.dir, rel))
	if err != nil {
		r.t.Fatal(err)
	}
	return string(body)
}

func (r *scriptRepo) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(r.dir, rel))
	return err == nil
}

func (r *scriptRepo) commit(msg string) {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "--allow-empty", "-m", msg)
}

// branch starts the pull request under test, off main as it stands.
func (r *scriptRepo) branch() {
	r.t.Helper()
	r.git("checkout", "-q", "-b", "pr")
}

// posixShell is dash where it is installed — /bin/sh on the CI and release
// runners, and stricter than a bash-as-sh about what POSIX permits — and sh
// otherwise.
func posixShell() string {
	if p, err := exec.LookPath("dash"); err == nil {
		return p
	}
	return "sh"
}

// run executes a script and returns its exit code, stdout and stderr.
func (r *scriptRepo) run(script string, args ...string) (int, string, string) {
	r.t.Helper()
	cmd := r.cmd(posixShell(), append([]string{filepath.Join("scripts", script)}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, stdout.String(), stderr.String()
	case errors.As(err, &exit):
		return exit.ExitCode(), stdout.String(), stderr.String()
	}
	r.t.Fatalf("sh scripts/%s: %v", script, err)
	return 0, "", ""
}

// gate runs the per-PR check against the merge-base with main, as CI does.
func (r *scriptRepo) gate() (int, string, string) {
	r.t.Helper()
	return r.run("changelog-entry-check.sh", r.git("merge-base", "main", "HEAD"))
}

func (r *scriptRepo) wantGate(code int, stderrHas ...string) string {
	r.t.Helper()
	got, stdout, stderr := r.gate()
	if got != code {
		r.t.Fatalf("gate exit = %d, want %d\nstdout:\n%s\nstderr:\n%s", got, code, stdout, stderr)
	}
	for _, s := range stderrHas {
		if !strings.Contains(stderr, s) {
			r.t.Errorf("gate stderr does not mention %q:\n%s", s, stderr)
		}
	}
	return stderr
}

const (
	fixedFragment = "### Fixed\n\n- **A fix.** ([#304](https://example.com/304))\n"
	addedFragment = "### Added\n\n- **A feature.**\n"
)

// --- the gate ----------------------------------------------------------------

func TestChangelogGateCountsAFragment(t *testing.T) {
	r := newScriptRepo(t)
	r.branch()
	r.write("main.go", "package main\n")
	r.write("changelog.d/304-x.md", fixedFragment)
	r.commit("fix")
	r.wantGate(0)
}

func TestChangelogGateRefusesAnEmptyFragment(t *testing.T) {
	for name, body := range map[string]string{
		"blank":        "\n\n",
		"heading only": "### Fixed\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			r := newScriptRepo(t)
			r.branch()
			r.write("changelog.d/304-x.md", body)
			r.commit("empty")
			r.wantGate(1, "changelog.d/304-x.md")
		})
	}
}

func TestChangelogGateRefusesAMalformedFragmentByName(t *testing.T) {
	for name, tc := range map[string]struct{ path, body, says string }{
		"underscore":      {"changelog.d/304_x.md", fixedFragment, "not a fragment name"},
		"not markdown":    {"changelog.d/304-x.txt", fixedFragment, "not a fragment name"},
		"nested":          {"changelog.d/sub/304-x.md", fixedFragment, "directly in changelog.d/"},
		"old section":     {"changelog.d/304-x.md", "### Documentation\n\n- x\n", "Documented"},
		"two sections":    {"changelog.d/304-x.md", fixedFragment + "\n" + addedFragment, "exactly one heading"},
		"unknown section": {"changelog.d/304-x.md", "### Misc\n\n- x\n", "unknown section"},
		// Quoted by git, it was refused as unreadable instead of by the rule.
		"a name git would quote": {"changelog.d/304-café.md", fixedFragment, "changelog.d/304-café.md: not a fragment name"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newScriptRepo(t)
			r.branch()
			r.write(tc.path, tc.body)
			// A good fragment beside it does not buy the bad one a pass: the
			// collector would refuse the release over it.
			r.write("changelog.d/305-good.md", addedFragment)
			r.commit("bad")
			r.wantGate(1, tc.path, tc.says)
		})
	}
}

func TestChangelogGateRefusesANameWithANewline(t *testing.T) {
	r := newScriptRepo(t)
	r.branch()
	r.write("changelog.d/304-x\n.md", fixedFragment)
	r.write("changelog.d/305-good.md", addedFragment)
	r.commit("bad")
	r.wantGate(1, "a name holding a newline")
}

func TestChangelogGateRefusesAFragmentRenamedToABadName(t *testing.T) {
	r := newScriptRepo(t)
	r.write("changelog.d/304-x.md", fixedFragment)
	r.commit("fragment")
	r.branch()
	r.git("mv", "changelog.d/304-x.md", "changelog.d/304_x.md")
	r.commit("rename")
	r.wantGate(1, "changelog.d/304_x.md", "not a fragment name")
}

func TestChangelogGateReflowIsNotAnEntry(t *testing.T) {
	setup := func(t *testing.T) *scriptRepo {
		r := newScriptRepo(t)
		r.write("changelog.d/304-x.md", "### Fixed\n\n- **A fix** that wraps\n  onto a second line.\n")
		r.commit("fragment")
		r.branch()
		return r
	}
	t.Run("reindented", func(t *testing.T) {
		r := setup(t)
		r.write("changelog.d/304-x.md", "### Fixed\n\n-   **A fix**   that wraps\n    onto a second line.  \n")
		r.commit("reflow")
		r.wantGate(1, "changelog.d/")
	})
	t.Run("a novel line", func(t *testing.T) {
		r := setup(t)
		r.write("changelog.d/304-x.md", "### Fixed\n\n- **A fix** that wraps\n  onto a second line, and now says why.\n")
		r.commit("novel")
		r.wantGate(0)
	})
	t.Run("renamed only", func(t *testing.T) {
		r := setup(t)
		r.git("mv", "changelog.d/304-x.md", "changelog.d/304-better-name.md")
		r.commit("rename")
		r.wantGate(1)
	})
	t.Run("renamed and re-headed", func(t *testing.T) {
		r := setup(t)
		r.git("rm", "-q", "changelog.d/304-x.md")
		r.write("changelog.d/304-y.md", "### Changed\n\n- **A fix** that wraps\n  onto a second line.\n")
		r.commit("reclassify")
		r.wantGate(1)
	})
	t.Run("renamed with a novel line", func(t *testing.T) {
		r := setup(t)
		r.git("rm", "-q", "changelog.d/304-x.md")
		r.write("changelog.d/304-better-name.md", "### Fixed\n\n- **A fix** that wraps\n  onto a second line.\n- And a second one.\n")
		r.commit("rename and add")
		r.wantGate(0)
	})
}

func TestChangelogGateDoesNotCountUnreleased(t *testing.T) {
	unreleased := strings.Replace(baseChangelog,
		"Unreleased entries live in changelog.d/ until a release collects them.\n",
		"Unreleased entries live in changelog.d/ until a release collects them.\n\n### Fixed\n\n- **A fix, in the old place.**\n", 1)

	t.Run("alone", func(t *testing.T) {
		r := newScriptRepo(t)
		r.branch()
		r.write("CHANGELOG.md", unreleased)
		r.commit("old habit")
		stderr := r.wantGate(1, "changelog.d/", "::warning::", "ADR 0042")
		if !strings.Contains(stderr, "::error::") {
			t.Errorf("the warning sits beside the ordinary refusal, want both:\n%s", stderr)
		}
	})
	t.Run("beside a fragment", func(t *testing.T) {
		r := newScriptRepo(t)
		r.branch()
		r.write("CHANGELOG.md", strings.Replace(baseChangelog, "until a release collects them.", "until a release collects them; see changelog.d/README.md.", 1))
		r.write("changelog.d/304-x.md", fixedFragment)
		r.commit("pointer edit")
		stderr := r.wantGate(0, "::warning::")
		if strings.Contains(stderr, "::error::") {
			t.Errorf("an Unreleased line must not refuse a PR that has an entry:\n%s", stderr)
		}
	})
	// The added lines are numbered against HEAD, so Unreleased's bounds must be
	// too: an uncommitted edit that shifts the working tree's copy down must not
	// move the section away from the committed line.
	t.Run("with an uncommitted edit above it", func(t *testing.T) {
		r := newScriptRepo(t)
		r.branch()
		r.write("CHANGELOG.md", strings.Replace(baseChangelog, "until a release collects them.", "until a release collects them; see changelog.d/README.md.", 1))
		r.write("changelog.d/304-x.md", fixedFragment)
		r.commit("pointer edit")
		r.write("CHANGELOG.md", "\n\n\n\n\n"+r.read("CHANGELOG.md"))
		r.wantGate(0, "::warning::")
	})
}

// TestChangelogGatePassesTheMigration is ADR 0042's own PR under its own gate
// (§2.2): entries move out of Unreleased into fragments, the README and this
// change's own fragment are added, and Unreleased becomes the pointer
// paragraph. It passes without `no-changelog`, and warns about Unreleased.
func TestChangelogGatePassesTheMigration(t *testing.T) {
	r := newScriptRepo(t)
	old := strings.Replace(baseChangelog,
		"Unreleased entries live in changelog.d/ until a release collects them.\n",
		"### Fixed\n\n- **Fix 293.**\n- **Fix 298.**\n", 1)
	r.write("CHANGELOG.md", old)
	r.git("rm", "-q", "-r", "changelog.d")
	r.commit("before ADR 0042")
	r.branch()
	r.write("CHANGELOG.md", baseChangelog)
	r.write("changelog.d/README.md", fragmentReadme)
	r.write("changelog.d/293-a.md", "### Fixed\n\n- **Fix 293.**\n")
	r.write("changelog.d/298-b.md", "### Fixed\n\n- **Fix 298.**\n")
	r.write("changelog.d/304-changelog-fragments.md", "### Repository\n\n- **Entries are files.**\n")
	r.write("main.go", "package main\n")
	r.commit("ADR 0042")
	stderr := r.wantGate(0, "::warning::")
	if strings.Contains(stderr, "::error::") {
		t.Errorf("the migration must pass its own gate:\n%s", stderr)
	}
}

func TestChangelogGateExemptsTheReadme(t *testing.T) {
	t.Run("beside a fragment", func(t *testing.T) {
		r := newScriptRepo(t)
		r.branch()
		r.write("changelog.d/README.md", fragmentReadme+"\nMore guidance.\n")
		r.write("changelog.d/304-x.md", fixedFragment)
		r.commit("docs")
		r.wantGate(0)
	})
	t.Run("alone", func(t *testing.T) {
		r := newScriptRepo(t)
		r.branch()
		r.write("changelog.d/README.md", fragmentReadme+"\nMore guidance.\n")
		r.commit("docs")
		stderr := r.wantGate(1)
		if strings.Contains(stderr, "README.md:") || strings.Contains(stderr, "not a fragment name") {
			t.Errorf("README.md is exempt and must not be named as a bad fragment:\n%s", stderr)
		}
	})
}

// TestChangelogGateRefusesAnEditThatBreaksAFragment is the M-status path: an
// existing, well-formed fragment edited into a malformed one is judged whole at
// HEAD and named, exactly as an added one is.
func TestChangelogGateRefusesAnEditThatBreaksAFragment(t *testing.T) {
	r := newScriptRepo(t)
	r.write("changelog.d/304-x.md", fixedFragment)
	r.commit("fragment")
	r.branch()
	r.write("changelog.d/304-x.md", fixedFragment+"\n### Added\n\n- **A novel second section.**\n")
	r.commit("break it")
	if status := r.git("diff", "--name-status", "main...HEAD"); !strings.HasPrefix(status, "M") {
		t.Fatalf("test setup: want an M-status edit, got %q", status)
	}
	r.wantGate(1, "changelog.d/304-x.md", "exactly one heading")
}

// TestChangelogGateAReleasedHeadingEditIsNotACut: editing a released heading
// (its date, a typo) re-adds a `## [` line for a version the base already had.
// That is not a cut — it neither counts as the entry nor trips rule 2's exit 2
// over the fragments that sit in changelog.d/ between releases. It is judged
// like any other edit, so `no-changelog` (exit 1) or a fragment settles it.
func TestChangelogGateAReleasedHeadingEditIsNotACut(t *testing.T) {
	setup := func(t *testing.T) *scriptRepo {
		r := newScriptRepo(t)
		r.write("changelog.d/298-pending.md", fixedFragment)
		r.commit("an entry waiting for the next release")
		r.branch()
		r.write("CHANGELOG.md", strings.Replace(baseChangelog, "## [v1.0.0] - 2026-01-01", "## [v1.0.0] - 2026-01-02", 1))
		r.write("main.go", "package main\n")
		return r
	}
	t.Run("alone", func(t *testing.T) {
		r := setup(t)
		r.commit("fix the release date")
		stderr := r.wantGate(1, "nothing in this diff is a changelog entry")
		if strings.Contains(stderr, "release section was cut") {
			t.Errorf("a heading edit is not a cut:\n%s", stderr)
		}
	})
	t.Run("beside a fragment", func(t *testing.T) {
		r := setup(t)
		r.write("changelog.d/310-date.md", "### Documented\n\n- **v1.0.0's date is corrected.**\n")
		r.commit("fix the release date")
		r.wantGate(0)
	})
}

func TestChangelogGateDeletionIsNotAnEntry(t *testing.T) {
	r := newScriptRepo(t)
	r.write("changelog.d/304-x.md", fixedFragment)
	r.commit("fragment")
	r.branch()
	r.git("rm", "-q", "changelog.d/304-x.md")
	r.commit("drop")
	r.wantGate(1)
}

func TestChangelogGateUsage(t *testing.T) {
	r := newScriptRepo(t)
	if code, _, stderr := r.run("changelog-entry-check.sh"); code != 2 || !strings.Contains(stderr, "usage") {
		t.Fatalf("no argument: exit %d, stderr %q; want 2 and a usage line", code, stderr)
	}
}

// releaseCut is the release PR: the collector has run on branch pr.
func releaseCut(t *testing.T) *scriptRepo {
	t.Helper()
	r := newScriptRepo(t)
	r.write("changelog.d/298-a.md", fixedFragment)
	r.write("changelog.d/304-b.md", addedFragment)
	r.commit("two entries")
	r.branch()
	if code, out, stderr := r.run("changelog-collect.sh", "v9.9.9", "2026-10-02"); code != 0 {
		t.Fatalf("collect: exit %d\n%s%s", code, out, stderr)
	}
	r.write("version.go", "package main\n\nconst Version = \"9.9.9\"\n")
	r.commit("release v9.9.9")
	return r
}

func TestChangelogGateCountsAReleaseCut(t *testing.T) {
	r := releaseCut(t)
	r.wantGate(0)
}

func TestChangelogGateRefusesACutThatLeavesAFragment(t *testing.T) {
	r := releaseCut(t)
	// A PR merges to main after the collector ran on the release branch...
	r.git("checkout", "-q", "main")
	r.write("changelog.d/310-late.md", fixedFragment)
	r.commit("late fix")
	// ...and reaches the release PR through Update branch, which moves the
	// merge-base past it: it is in HEAD's tree, never in base...HEAD.
	r.git("checkout", "-q", "pr")
	r.git("merge", "-q", "--no-edit", "--no-ff", "main")
	if diff := r.git("diff", "--name-only", r.git("merge-base", "main", "HEAD")+"...HEAD"); strings.Contains(diff, "310-late.md") {
		t.Fatalf("test setup: the late fragment is in base...HEAD, so this does not test HEAD's tree:\n%s", diff)
	}
	stderr := r.wantGate(2, "changelog.d/310-late.md", "no-changelog", "by hand")
	// Re-collecting would find only the late fragment — the first run deleted
	// the rest — so the refusal must not steer the maintainer there.
	if strings.Contains(stderr, "run scripts/changelog-collect.sh again") {
		t.Errorf("the refusal advises re-collecting, which drops every fragment the first run took:\n%s", stderr)
	}
}

func TestChangelogGateRefusesAManualCutThatSkippedTheCollector(t *testing.T) {
	r := newScriptRepo(t)
	r.write("changelog.d/298-a.md", fixedFragment)
	r.commit("entry")
	r.branch()
	r.write("CHANGELOG.md", strings.Replace(baseChangelog, "## [v1.0.0]", "## [v9.9.9] - 2026-10-02\n\nBy hand.\n\n## [v1.0.0]", 1))
	r.commit("manual cut")
	r.wantGate(2, "changelog.d/298-a.md")
}

// --- the fragment rule, stated twice -----------------------------------------

// TestChangelogFragmentRuleAgreesWithTheScript runs fragmentCases through the
// shell checker the gate and the collector ask, and holds its verdict to
// checkFragment's. Two statements of one rule drift apart unless something
// compares them.
func TestChangelogFragmentRuleAgreesWithTheScript(t *testing.T) {
	r := newScriptRepo(t)
	for i, tc := range fragmentCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join("cases", strings.Repeat("x", i+1))
			rel := filepath.Join(dir, tc.file)
			r.write(rel, tc.body)
			code, _, stderr := r.run("changelog-fragment-check.sh", rel)
			goErr := checkFragment(tc.file, tc.body)
			if (code == 0) != (goErr == nil) {
				t.Fatalf("the rule's two statements disagree on %s: script exit %d (%s), Go %v", tc.file, code, strings.TrimSpace(stderr), goErr)
			}
			if tc.wantErr != "" && !strings.Contains(stderr, tc.wantErr) {
				t.Errorf("script refusal does not mention %q:\n%s", tc.wantErr, stderr)
			}
		})
	}
}

func TestChangelogFragmentCheckUsage(t *testing.T) {
	r := newScriptRepo(t)
	if code, _, _ := r.run("changelog-fragment-check.sh"); code != 2 {
		t.Fatalf("no argument: exit %d, want 2", code)
	}
	if code, _, stderr := r.run("changelog-fragment-check.sh", "changelog.d/404-missing.md"); code != 1 || !strings.Contains(stderr, "404-missing.md") {
		t.Fatalf("a missing file: exit %d, stderr %q; want 1 naming it", code, stderr)
	}
}

// TestChangelogSectionListIsOneList holds the scripts' copies of the section
// list to changelogSections, order included: the collector writes headings in
// that order, and the checker admits exactly those names.
func TestChangelogSectionListIsOneList(t *testing.T) {
	read := func(name string) string {
		body, err := os.ReadFile(filepath.Join("..", "..", "scripts", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	if want := strings.Join(changelogSections, "|"); !strings.Contains(read("changelog-fragment-check.sh"), `"`+want+`"`) {
		t.Errorf("scripts/changelog-fragment-check.sh does not carry the section list %q", want)
	}
	quoted := make([]string, len(changelogSections))
	for i, s := range changelogSections {
		quoted[i] = s
		if strings.Contains(s, " ") {
			quoted[i] = `"` + s + `"`
		}
	}
	if want := "for section in " + strings.Join(quoted, " ") + ";"; !strings.Contains(read("changelog-collect.sh"), want) {
		t.Errorf("scripts/changelog-collect.sh does not carry the section list in order: want %q", want)
	}
}

// --- the collector -------------------------------------------------------------

func TestChangelogCollectGroupsAndOrders(t *testing.T) {
	r := newScriptRepo(t)
	r.write("changelog.d/310-later-fix.md", "### Fixed\n\n- **Fix 310.**\n")
	r.write("changelog.d/42-early-fix.md", "\n### Fixed\n\n- **Fix 42**, wrapped\n  over two lines.\n\n")
	r.write("changelog.d/300-feature.md", "### Added\n\n- **Feature 300.**\n")
	r.write("changelog.d/42-another.md", "### Fixed\n- **Fix 42, the other slug.**\n")
	r.commit("fragments")

	code, stdout, stderr := r.run("changelog-collect.sh", "v9.9.9", "2026-10-02")
	if code != 0 {
		t.Fatalf("collect: exit %d\n%s%s", code, stdout, stderr)
	}
	want := strings.Replace(baseChangelog, "## [v1.0.0] - 2026-01-01", `## [v9.9.9] - 2026-10-02

### Added

- **Feature 300.**

### Fixed

- **Fix 42, the other slug.**
- **Fix 42**, wrapped
  over two lines.
- **Fix 310.**

## [v1.0.0] - 2026-01-01`, 1)
	if got := r.read("CHANGELOG.md"); got != want {
		t.Fatalf("CHANGELOG.md after collecting:\n%s\nwant:\n%s", got, want)
	}
	for _, f := range []string{"310-later-fix.md", "42-early-fix.md", "300-feature.md", "42-another.md"} {
		if r.exists("changelog.d/" + f) {
			t.Errorf("changelog.d/%s was collected but not deleted", f)
		}
	}
	if !r.exists("changelog.d/README.md") {
		t.Error("changelog.d/README.md was deleted; it is not a fragment")
	}

	// The section feeds release-notes.sh unchanged. With no previous tag its
	// credit half is skipped, so stdout is the section alone.
	code, notes, stderr := r.run("release-notes.sh", "v9.9.9")
	if code != 0 {
		t.Fatalf("release-notes.sh: exit %d\n%s", code, stderr)
	}
	wantNotes := "### Added\n\n- **Feature 300.**\n\n### Fixed\n\n- **Fix 42, the other slug.**\n- **Fix 42**, wrapped\n  over two lines.\n- **Fix 310.**\n"
	if notes != wantNotes {
		t.Fatalf("release-notes.sh v9.9.9 printed:\n%s\nwant:\n%s", notes, wantNotes)
	}
}

// TestChangelogCollectTakesACRLFFragment: a CRLF fragment with a leading blank
// line is a fragment to the checker, so the extractor must read it the same
// way — a "\r" line is blank, not a heading — or it is deleted unwritten. The
// other fragment beside it is what kept the empty-section guard from catching
// the drop.
func TestChangelogCollectTakesACRLFFragment(t *testing.T) {
	r := newScriptRepo(t)
	r.write("changelog.d/1-a.md", "\r\n### Fixed\r\n\r\n- **Fix 1**, written\r\n  on Windows.\r\n\r\n")
	r.write("changelog.d/2-b.md", addedFragment)
	r.commit("fragments")
	if code, out, stderr := r.run("changelog-collect.sh", "v9.9.9", "2026-10-02"); code != 0 {
		t.Fatalf("collect: exit %d\n%s%s", code, out, stderr)
	}
	want := strings.Replace(baseChangelog, "## [v1.0.0] - 2026-01-01",
		"## [v9.9.9] - 2026-10-02\n\n### Added\n\n- **A feature.**\n\n### Fixed\n\n- **Fix 1**, written\n  on Windows.\n\n## [v1.0.0] - 2026-01-01", 1)
	if got := r.read("CHANGELOG.md"); got != want {
		t.Fatalf("CHANGELOG.md:\n%q\nwant:\n%q", got, want)
	}
}

// TestChangelogCollectReadsOnlyWhatGitTracks: a .DS_Store, an editor's swap
// file and a fragment nobody committed are not entries. The collector neither
// refuses the release over them nor collects (and deletes) them.
func TestChangelogCollectReadsOnlyWhatGitTracks(t *testing.T) {
	r := newScriptRepo(t)
	r.write(".gitignore", ".DS_Store\n")
	r.write("changelog.d/304-x.md", fixedFragment)
	r.commit("fragment")
	r.write("changelog.d/.DS_Store", "\x00\x01binary")
	r.write("changelog.d/.304-x.md.swp", "swap")
	r.write("changelog.d/304-x.md~", "backup")
	r.write("changelog.d/999-stray.md", "### Added\n\n- **Never committed.**\n")

	code, out, stderr := r.run("changelog-collect.sh", "v9.9.9", "2026-10-02")
	if code != 0 {
		t.Fatalf("collect: exit %d\n%s%s", code, out, stderr)
	}
	if !strings.Contains(out, "collected 1 fragment(s)") {
		t.Errorf("want exactly the tracked fragment collected:\n%s", out)
	}
	got := r.read("CHANGELOG.md")
	if !strings.Contains(got, "- **A fix.**") || strings.Contains(got, "Never committed") {
		t.Errorf("want the tracked fragment and not the stray one in CHANGELOG.md:\n%s", got)
	}
	for _, f := range []string{".DS_Store", ".304-x.md.swp", "304-x.md~", "999-stray.md"} {
		if !r.exists("changelog.d/" + f) {
			t.Errorf("changelog.d/%s is not tracked, and was deleted", f)
		}
	}
	if r.exists("changelog.d/304-x.md") {
		t.Error("changelog.d/304-x.md was collected but not deleted")
	}
}

// TestChangelogCollectRefusesWhenTheExtractorDisagrees breaks the checker on
// purpose — it passes everything — so a fragment the extractor cannot place
// reaches the postcondition. It must refuse there, with nothing written and
// nothing deleted, rather than delete an entry it never wrote.
func TestChangelogCollectRefusesWhenTheExtractorDisagrees(t *testing.T) {
	r := newScriptRepo(t)
	r.write("scripts/changelog-fragment-check.sh", "exit 0\n")
	r.write("changelog.d/304-ok.md", fixedFragment)
	r.write("changelog.d/305-misc.md", "### Misc\n\n- **No such section.**\n")
	r.commit("a checker that disagrees")
	before := r.read("CHANGELOG.md")

	code, _, stderr := r.run("changelog-collect.sh", "v9.9.9", "2026-10-02")
	if code != 1 || !strings.Contains(stderr, "changelog.d/305-misc.md") || strings.Contains(stderr, "changelog.d/304-ok.md") {
		t.Fatalf("exit %d, stderr:\n%s\nwant 1 naming 305-misc.md alone", code, stderr)
	}
	if r.read("CHANGELOG.md") != before {
		t.Error("CHANGELOG.md changed on a refusal")
	}
	if status := r.git("status", "--porcelain", "--untracked-files=all"); status != "" {
		t.Errorf("the tree changed on a refusal:\n%s", status)
	}
}

func TestChangelogCollectWhenUnreleasedIsTheLastSection(t *testing.T) {
	r := newScriptRepo(t)
	r.write("CHANGELOG.md", "# Changelog\n\n## [Unreleased]\n\nSee changelog.d/.\n\n[Unreleased]: https://example.com/compare/HEAD\n")
	r.write("changelog.d/1-first.md", addedFragment)
	r.commit("first")
	if code, out, stderr := r.run("changelog-collect.sh", "v0.1.0", "2026-10-02"); code != 0 {
		t.Fatalf("collect: exit %d\n%s%s", code, out, stderr)
	}
	want := "# Changelog\n\n## [Unreleased]\n\nSee changelog.d/.\n\n## [v0.1.0] - 2026-10-02\n\n### Added\n\n- **A feature.**\n\n[Unreleased]: https://example.com/compare/HEAD\n"
	if got := r.read("CHANGELOG.md"); got != want {
		t.Fatalf("CHANGELOG.md:\n%q\nwant:\n%q", got, want)
	}
}

func TestChangelogCollectRefusalsWriteNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		files map[string]string
		args  []string
		code  int
		says  string
	}{
		"a malformed fragment": {
			files: map[string]string{"changelog.d/304-ok.md": fixedFragment, "changelog.d/305-bad.md": "### Fixed\n"},
			args:  []string{"v9.9.9", "2026-10-02"}, code: 1, says: "305-bad.md",
		},
		"a misnamed file": {
			files: map[string]string{"changelog.d/304-ok.md": fixedFragment, "changelog.d/notes.txt": fixedFragment},
			args:  []string{"v9.9.9", "2026-10-02"}, code: 1, says: "notes.txt",
		},
		// git quotes a non-ASCII name unless asked not to; quoted, it stopped
		// starting with changelog.d/ and was neither refused nor collected.
		"a name git would quote": {
			files: map[string]string{"changelog.d/304-ok.md": fixedFragment, "changelog.d/305-café.md": fixedFragment},
			args:  []string{"v9.9.9", "2026-10-02"}, code: 1, says: "changelog.d/305-café.md: not a fragment name",
		},
		// Refused whole and by its full path, not as `305` and `x.md`.
		"a name with a space": {
			files: map[string]string{"changelog.d/304-ok.md": fixedFragment, "changelog.d/305 x.md": fixedFragment},
			args:  []string{"v9.9.9", "2026-10-02"}, code: 1, says: "changelog.d/305 x.md: not a fragment name",
		},
		"a name with a newline": {
			files: map[string]string{"changelog.d/304-ok.md": fixedFragment, "changelog.d/305-x\n.md": fixedFragment},
			args:  []string{"v9.9.9", "2026-10-02"}, code: 1, says: "holds a newline",
		},
		"a directory": {
			files: map[string]string{"changelog.d/304-ok.md": fixedFragment, "changelog.d/sub/305-x.md": fixedFragment},
			args:  []string{"v9.9.9", "2026-10-02"}, code: 1, says: "sub",
		},
		"no fragments": {
			args: []string{"v9.9.9", "2026-10-02"}, code: 1, says: "no fragments",
		},
		"no Unreleased heading": {
			files: map[string]string{
				"changelog.d/304-ok.md": fixedFragment,
				"CHANGELOG.md":          strings.Replace(baseChangelog, "## [Unreleased]\n\nUnreleased entries live in changelog.d/ until a release collects them.\n\n", "", 1),
			},
			args: []string{"v9.9.9", "2026-10-02"}, code: 1, says: "no ## [Unreleased] heading",
		},
		"the version is already there": {
			files: map[string]string{"changelog.d/304-ok.md": fixedFragment},
			args:  []string{"v1.0.0", "2026-10-02"}, code: 1, says: "already has",
		},
		"a version with no v": {
			files: map[string]string{"changelog.d/304-ok.md": fixedFragment},
			args:  []string{"9.9.9"}, code: 2, says: "usage",
		},
		"a malformed date": {
			files: map[string]string{"changelog.d/304-ok.md": fixedFragment},
			args:  []string{"v9.9.9", "02/10/2026"}, code: 2, says: "usage",
		},
		"no arguments": {
			files: map[string]string{"changelog.d/304-ok.md": fixedFragment},
			code:  2, says: "usage",
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newScriptRepo(t)
			for path, body := range tc.files {
				r.write(path, body)
			}
			r.commit("state")
			before := r.git("ls-files", "-s")
			beforeChangelog := r.read("CHANGELOG.md")

			code, _, stderr := r.run("changelog-collect.sh", tc.args...)
			if code != tc.code || !strings.Contains(stderr, tc.says) {
				t.Fatalf("exit %d, stderr:\n%s\nwant exit %d mentioning %q", code, stderr, tc.code, tc.says)
			}
			if got := r.read("CHANGELOG.md"); got != beforeChangelog {
				t.Errorf("CHANGELOG.md changed on a refusal:\n%s", got)
			}
			if status := r.git("status", "--porcelain", "--untracked-files=all"); status != "" {
				t.Errorf("the tree changed on a refusal:\n%s", status)
			}
			if after := r.git("ls-files", "-s"); after != before {
				t.Errorf("the index changed on a refusal")
			}
		})
	}
}
