# ADR 0042 — A changelog entry is a file the pull request owns, not a line in a shared one

**Status:** Accepted. Written before its code; §5 lists the tests the
implementation owes, and each lands in the same PR.

**Where the addresses point.** Written against `be6da77`, the `main` this
branch (`feat/304-changelog-fragments`) left. Every `file:line` below was read
at that commit. Code that does not exist yet is named by file **and** function
or step, because the line goes stale when the diff lands and the name does not.

**Date:** 2026-10-02

**Issue:** [#304](https://github.com/jitokim/oh-my-graph/issues/304).
`backlog-batch` rule 1 (lanes share no files) collides with the per-PR
changelog gate, and any two PRs that land close together conflict on
`CHANGELOG.md`'s `## [Unreleased]` section.

## 1. Context

### 1.1 Two rules that are each right, and cannot both hold

**Rule 1 of `backlog-batch`** (`graphs/backlog-batch.yaml:17-33`): lanes must
not share files, because two lanes touching one file merge-conflict at PR time
and can silently undo each other. The rule is PROSE: which files a lane owns
arrives at run time in `--input task_a`/`task_b`, so no checker can read it
(`docs/measurements/0034-lane-file-ownership-predicate.md`, verdict
DO-NOT-SHIP). Its only remedy for a genuine overlap is to serialize the two
tasks into one lane.

**The changelog gate** (`.github/workflows/test.yml:54-86`, deciding through
`scripts/changelog-entry-check.sh`): a PR that changes anything must add a
non-blank, non-reflow line inside `## [Unreleased]` as it stands at HEAD, or
say `no-changelog` in its body. The gate exists because the question is
answerable *before* the merge, by the person who knows what changed
(`test.yml:34-45`).

Put together: **every lane of every batch edits `CHANGELOG.md`**, and always
the same dozen lines of it. Rule 1 is violated by construction, and its remedy
— serialize the overlapping lanes — serializes the whole batch, which is the
one thing the graph exists not to do.

### 1.2 The conflict is not specific to batches

`main` requires the branch to be up to date before merge (CONTRIBUTING.md,
"What `main` enforces"). So whenever two PRs are open at once, the second to
merge must take the first in. Both appended a bullet at the same place in the
same section, so the textual merge conflicts, and GitHub's *Update branch*
cannot resolve it. Someone checks the branch out, resolves by hand, pushes,
and every check reruns. The most recent instance is #303, which sat blocked
on a merge conflict while #302 was open beside it, both carrying an
Unreleased entry.

### 1.3 What already depends on `CHANGELOG.md`, and must keep working

| consumer | what it reads | `file:line` |
| --- | --- | --- |
| `scripts/release-notes.sh` | the `## [vX.Y.Z]` section, verbatim, on the tag push | `release-notes.sh:56-63` |
| `TestChangelogSectionHasSubstance` | ≥ 3 prose lines in `## [v<Version>]`; headings do not count | `cmd/oh-my-graph/version_test.go:45-70` |
| `TestUnreleasedSectionHasNoDuplicateHeadings` | no repeated `### X` inside `## [Unreleased]` | `version_test.go:242-276` |
| `TestVersionMatchesChangelog`, `TestChangelogHasFootnoteForThisVersion` | the topmost `## [v…]` heading; the `[Unreleased]:` footnote | `version_test.go:84-108`, `:151-172` |
| `scripts/changelog-entry-check.sh` | added lines inside Unreleased; an added `## [` line as the release-cut exemption | `changelog-entry-check.sh:46-120`, `:62-67` |
| `internal/docsclaims` | walks `CHANGELOG.md` among the scanned roots | `internal/docsclaims/claims_test.go:141-160` |
| `internal/invariants` prose-count guard | excludes `CHANGELOG.md` as history | `internal/invariants/prose_count_test.go:120-123` |

The important observation is that **the release-time consumers all read a
`## [vX.Y.Z]` section, never `## [Unreleased]`**. Only the per-PR gate and
the duplicate-heading guard read Unreleased. That is the seam this ADR cuts on.

## 2. Decision

**Option (b): every PR writes its entry as its own file under `changelog.d/`.
The release PR collects those files into a new `## [vX.Y.Z]` section of
`CHANGELOG.md` and deletes them. `## [Unreleased]` stops holding entries.**

Two PRs then never touch a common file, rule 1 holds without an exception, and
everything that reads a released section reads exactly what it reads today.

### 2.1 The fragment

One file per entry, at `changelog.d/<issue>-<slug>.md`:

- **Name.** `^[0-9]+-[a-z0-9]+(-[a-z0-9]+)*\.md$` — the issue number first,
  then a kebab-case slug. The issue number makes two lanes' names disjoint in
  the ordinary case; two PRs for one issue pick different slugs. Two PRs that
  pick the *same* name have an add/add conflict, which is a real rule-1
  collision and is loud, as it should be.
- **Shape.** The first non-blank line is exactly one `### <Section>` heading,
  where `<Section>` is one of the names this changelog already uses, in this
  canonical order: `Added`, `Changed`, `Deprecated`, `Removed`, `Fixed`,
  `Security`, `Documented`, `Repository`. Then at least one prose line (a
  non-blank line that is not a heading). No other `#`-heading line outside a
  fenced block. A PR whose change is both an addition and a fix writes two
  fragments.
- **Body.** Exactly what would have gone under that heading in Unreleased
  before: the bullet, the bold lead, the issue link. Nothing about the prose
  convention changes.
- **`changelog.d/README.md`** is the one non-fragment file in the directory.
  It states the name and shape above, and it is what GitHub renders when
  someone opens the directory.

### 2.2 The per-PR gate asks about fragments

`scripts/changelog-entry-check.sh <base-sha>` keeps its contract — exit 0 an
entry was added, 1 nothing a reader would find — and its local-run usage. Its
question becomes:

1. **An entry** is a file under `changelog.d/` that the diff adds (or an
   existing fragment that gains a novel non-blank line, the #208 reflow rule
   applied per file), whose name matches §2.1, and which has a prose line.
2. **The release-cut exemption is unchanged**: an added `## [` line in
   `CHANGELOG.md` is the entry (`changelog-entry-check.sh:62-67`). A cut adds
   the version heading and deletes the fragments, so it still passes.
3. **A line added under `## [Unreleased]` is no longer an entry.** It fails
   with a message that names `changelog.d/` and this ADR, so the habit of the
   old rule meets an instruction, not a bare red.
4. **A misnamed file under `changelog.d/`** (`304_foo.md`, `foo.txt`) fails
   by name. The collector skips anything that is not a fragment, so a misnamed
   file is an entry that would be silently dropped at release; refusing it at
   the PR is the only place that is cheap.

The workflow step (`test.yml:65-86`) changes in two places only: the
"nothing outside the changelog changed" short-circuit (`test.yml:72`) treats
`changelog.d/` like `CHANGELOG.md`, and the error text names `changelog.d/`
instead of `## [Unreleased]`. `no-changelog` in the PR body still excuses, and
still loudly.

### 2.3 The release cut collects

A new `scripts/changelog-collect.sh <vX.Y.Z> [<date>]`, run by the maintainer
in the release PR:

1. Refuses if any file under `changelog.d/` other than `README.md` is not a
   well-formed fragment (§2.1), naming each one. Nothing is written.
2. Refuses if there are no fragments: a release with nothing to collect is the
   empty body `TestChangelogSectionHasSubstance` exists to stop, and failing
   in the script says so one step earlier.
3. Refuses if `CHANGELOG.md` already has a `## [<vX.Y.Z>]` heading, so a
   second run cannot write the section twice.
4. Writes `## [<vX.Y.Z>] - <date>` directly under the `## [Unreleased]` block,
   then **each section heading once**, in the canonical order, with every
   fragment body for that heading under it, fragments ordered by issue number
   (numeric) then slug.
5. Deletes the collected fragments (plain `rm`; staging is the maintainer's,
   like every other release-PR edit).

It writes no intro prose and no footnote. The release checklist already makes
the section's opening prose the maintainer's job ("Write the release's
CHANGELOG section as prose"), and `TestChangelogHasFootnoteForThisVersion`
already guards the footnote. The collected entries are a draft the maintainer
may reorder or edit before the tag; what is collected is the floor, not the
release body.

`scripts/release-notes.sh` is **not changed**. It reads `## [vX.Y.Z]`, and in
the release PR that section exists in `CHANGELOG.md` exactly as before.

### 2.4 `## [Unreleased]` keeps its heading and loses its entries

The heading stays: Keep a Changelog readers look for it,
`TestUnreleasedSectionHasNoDuplicateHeadings` fails without it, and the
`[Unreleased]:` compare footnote needs it. Under it sits one fixed paragraph
pointing to `changelog.d/`.

A new guard, **`TestUnreleasedSectionHoldsNoEntries`**, refuses any `### `
heading or any `- ` list item inside it (outside fences). Without it a
`no-changelog` PR, or a hand edit, could still put an entry there, and the
collector — which reads only `changelog.d/` — would leave it stranded under
Unreleased at the cut, where it would never reach a release body. There must
be one home for an unreleased entry, and this makes it one.

### 2.5 The duplicate-heading guard follows the body it protects

`TestUnreleasedSectionHasNoDuplicateHeadings` exists because "whatever the
Unreleased block looks like at the cut IS the release body"
(`version_test.go:218-226`). Under this ADR that is no longer the block that
becomes the body: the collector writes the version section directly. So the
guard's scope **widens** to also cover the `## [v<Version>]` section — the one
`TestChangelogSectionHasSubstance` already reads, and the one that is in
review in the release PR. Its Unreleased half stays (trivially green under
§2.4, and still correct). The collector emits each heading once by
construction; the widened guard is what catches a maintainer's hand edit of
the collected section that re-adds one, before the tag. Released sections
below the current one stay out of scope, for the reason the guard already
gives: they are settled history.

### 2.6 A fragment is changelog text that has not been placed yet

Every repo rule that reads `CHANGELOG.md` treats `changelog.d/` the same way,
or excludes it for the same reason:

- `internal/invariants` prose-count guard: `changelog.d/` joins
  `historyExcluded`. A fragment records what a change claimed when it landed,
  exactly as the changelog does.
- `internal/docsclaims`: `changelog.d` joins `walkedSubtrees`, because
  `CHANGELOG.md` is scanned; a claim must not escape the scan by being written
  before the release instead of after it.

### 2.7 `backlog-batch` rule 1 gains no exception

Rule 1's text stays exceptionless and gains one sentence of explanation: the
changelog entry is a per-lane fragment under `changelog.d/` named by the
lane's own issue, so every lane writes a changelog entry and no two lanes
share the file. Both lanes' dev prompts are told to write the entry as a
fragment, since a lane that follows the old habit fails the gate (§2.2, 3).
The golden `*.resolved.json` files move with any prompt edit.

## 3. What does not change

- **The release body.** `release-notes.sh`, the release workflow, its 200-byte
  body check, and the Contributors line are untouched.
- **`TestChangelogSectionHasSubstance`, `TestVersionMatchesChangelog`,
  `TestChangelogHasFootnoteForThisVersion`** are untouched and keep their
  meaning: each reads the released section or the footnotes.
- **The gate's two escape hatches**: the release-cut exemption and
  `no-changelog`.
- **No engine code.** Nothing under `internal/` changes except the two scan
  lists in §2.6. No exec seam, no `childenv`, no graph schema.

## 4. Alternatives considered

### 4.1 (a) Name `CHANGELOG.md` as the rule-1 exception, resolved by `merge=union`

The cheapest option: one `.gitattributes` line, `CHANGELOG.md merge=union`,
and one sentence in rule 1. Git's built-in union driver keeps both sides'
lines on a conflict, so two appended bullets rebase cleanly. It keeps the
single file everybody already knows, keeps Unreleased readable on `main`, and
needs no collector.

It loses, on four counts:

1. **It resolves only where a local git runs it.** The conflict in §1.2 is
   met on GitHub, at *Update branch* and at merge, and whether GitHub's
   server-side merge honours `.gitattributes` merge drivers is unmeasured in
   this repository. The design would be correct only on the path that does not
   need it, and every second PR would still be checked out, rebased locally
   and force-pushed — the step the issue asks to remove.
2. **Union is not correct, only mechanical.** It concatenates the two sides of
   a hunk without reading them. After a release cut leaves Unreleased empty,
   two PRs that each add `### Fixed` produce two `### Fixed` headings, and the
   duplicate-heading guard turns the rebased PR red — a manual fix, again. Two
   PRs that both amend one bullet produce both versions of it, and **no guard
   catches that**: it ships in the release body as a duplicated, contradictory
   entry.
3. **It puts an exception into the one rule that has no checker.** Rule 1 is
   prose because nothing can verify it (§1.1). An exception list is the first
   thing such a rule grows, and the measurement behind rule 1 already found
   its only lexical hit was noise from `CONTRIBUTING.md`, another file every
   lane cites. A rule whose shape is "no shared files, except these" invites
   the next "except".
4. **Fragments make the conflict impossible rather than resolvable.** With
   (b), *Update branch* succeeds on GitHub with no checkout at all, because
   there is nothing to merge.

What (a) gets right — one file, readable Unreleased — is the cost (b) pays,
and §6 states it.

### 4.2 Others

- **Serialize every lane on the changelog (rule 1's own remedy).** Loses: every
  lane edits the changelog, so every batch collapses to one lane.
- **`no-changelog` on every lane PR, one changelog PR after the batch.** Loses
  the gate's whole reason (`test.yml:43-45`): the entry would be written after
  the merge, by someone not looking at the change. That is the shape that cost
  five round trips in v0.9.0.
- **Generate entries from commit subjects or PR bodies.** Loses on v0.8.0's
  lesson (`release-notes.sh:5-9`): a commit list cannot say why a change
  matters. Fragments keep the prose hand-written.
- **`towncrier` or another fragment tool.** Same idea, but a Python toolchain
  in a Go repo's release path and CI, a config format, and its own section
  vocabulary — for a collector that is one `awk` pass. The house scripts are
  POSIX `sh` (and must run under dash, `release-notes.sh:76-80`); the
  collector follows them.
- **Fragments plus still accepting Unreleased lines.** Loses: two homes for an
  unreleased entry, so the collector must parse and merge Unreleased's own
  `###` blocks with the fragments' — the duplicate-heading problem, moved into
  the collector. §2.4 refuses the second home instead.
- **A fragment per PR rather than per entry, with several `###` blocks.**
  Workable, but the validator and collector then parse a multi-section file;
  "one heading per file" makes the collector's grouping a sort and makes a
  duplicate heading impossible by construction. A two-kind PR writes two
  files.

## 5. Tests the implementation owes

Scripts are exercised from Go tests in a temporary git repository, so the
cases run in `make test` with no network.

1. **Gate: a fragment is an entry.** A diff adding a well-formed
   `changelog.d/304-x.md` exits 0.
2. **Gate: a blank or heading-only fragment is not.** Exits 1.
3. **Gate: a reflow of an existing fragment is not.** Re-indenting a bullet
   in an existing fragment exits 1; adding a novel line to it exits 0.
4. **Gate: a line under Unreleased is not an entry.** Exits 1, and stderr
   names `changelog.d/`.
5. **Gate: a misnamed file is refused by name.** `changelog.d/304_x.md` exits
   1 and is named.
6. **Gate: the release cut still passes.** A diff that adds `## [v9.9.9]` and
   deletes every fragment exits 0.
7. **Collector: grouping and order.** Three fragments — two `Fixed`, one
   `Added`, issue numbers out of order — produce one `### Added` then one
   `### Fixed`, fragments in numeric issue order, under `## [vX] - <date>`
   placed directly below the Unreleased block; the fragments are deleted.
8. **Collector: refusals write nothing.** A malformed fragment, no fragments,
   and an already-present version heading each exit non-zero with
   `CHANGELOG.md` and `changelog.d/` byte-identical.
9. **Collector output feeds `release-notes.sh` unchanged.** On test 7's
   result, `release-notes.sh vX` prints the collected section (the credit
   half is skipped: no previous tag).
10. **`TestChangelogFragmentsAreWellFormed`** over the real `changelog.d/`:
    every file but `README.md` matches §2.1's name and shape, with a known
    section name. Its own failure cases are table-driven.
11. **`TestUnreleasedSectionHoldsNoEntries`**, with a failing case for a
    `### ` heading and for a `- ` item, and a fenced example that passes.
12. **The widened duplicate-heading guard** fails on a `## [v<Version>]`
    section with two `### Fixed`, still ignores fenced headings, and still
    ignores sections below the current one.
13. **The two scan lists**: `changelog.d/` is in `historyExcluded` and is
    reached by the `docsclaims` walk.

## 6. Failure modes and compatibility

**Failure modes.**

- **A reader of `CHANGELOG.md` on `main` no longer sees what is unreleased.**
  This is the real cost and it is accepted. The Unreleased paragraph points at
  `changelog.d/`, whose README GitHub renders. Released sections, which are
  what users install, are unaffected.
- **A misnamed fragment would be silently dropped at release.** Refused twice:
  by the gate at the PR (§2.2, 4) and by `TestChangelogFragmentsAreWellFormed`
  on every `go test`; the collector refuses again before writing.
- **An entry written straight into Unreleased would be stranded.**
  `TestUnreleasedSectionHoldsNoEntries` refuses it, including on a
  `no-changelog` PR that the gate excused.
- **The maintainer forgets to run the collector.** The version heading is
  missing, so `TestVersionMatchesChangelog` and
  `TestChangelogSectionHasSubstance` fail the release PR — the same failure as
  forgetting to promote Unreleased today. Fragments left behind after a manual
  cut would be collected again into the next release; the collector deletes
  what it collects so the normal path cannot do that, and the checklist names
  the script so the normal path is the one taken.
- **Two PRs pick the same fragment name.** An add/add conflict, loud at
  *Update branch*. It is a genuine rule-1 collision and rename is the fix.
- **A PR edits another PR's unreleased fragment** (a follow-up fix to an
  unreleased feature). Allowed and counted (§2.2, 1); it is a shared file only
  if both are open at once, which is rule 1's ordinary territory.

**Compatibility.**

- **In-flight PRs that wrote under Unreleased** (at this writing #302) fail
  the new gate and `TestUnreleasedSectionHoldsNoEntries` once rebased. The fix
  is to move the bullet into a fragment; the gate's message says so.
- **The three entries already under Unreleased** (#293, #298, #294) move
  verbatim into `changelog.d/293-…`, `298-…` and `294-…` in this PR, so the
  next release collects them. This PR's own entry is a fourth fragment,
  `changelog.d/304-changelog-fragments.md`; it is what satisfies the gate for
  this PR under the rule it introduces, independent of the moved three.
- **Contributors**: CONTRIBUTING.md's merge-rule row and its release checklist
  change in the same PR — the row names `changelog.d/` and the local gate
  command; the checklist gains "run `scripts/changelog-collect.sh vX.Y.Z`
  first, then write the section's opening prose".
- **DESIGN.md**'s repo layout gains `changelog.d/`.
- **Users of oh-my-graph**: nothing. The binary, the graph schema, and the
  release page are unchanged.

## 7. Falsification

1. **GitHub's server-side merge is shown to honour `merge=union`, and a
   release cycle shows no stranded or contradictory entries under it.** Then
   §4.1's first count falls and its second becomes the whole argument; the
   trade would be worth re-weighing against the reader cost in §6.
2. **Fragment conflicts are observed between ordinary PRs** (not same-issue
   lanes). Then the naming scheme does not make names disjoint, and it, not
   the mechanism, needs to change.
3. **A release is cut whose collected section needed more manual
   restructuring than the old Unreleased merge did** (v0.14.0 needed a whole
   graph node to merge eleven subheadings into four). Then
   one-heading-per-fragment is the wrong grain.
