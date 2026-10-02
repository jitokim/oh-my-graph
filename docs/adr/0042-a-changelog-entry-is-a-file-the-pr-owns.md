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

Put together: **every lane of every batch run on this repo edits
`CHANGELOG.md`**, and always
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
  collision and is loud, as it should be. The number is the one this repo's
  commit subjects already carry (`(#304)`); a change with no issue opens one
  first, as the larger changes here already do.
- **Shape.** The first non-blank line is exactly one `### <Section>` heading,
  where `<Section>` is one of a fixed list, in this canonical order: `Added`,
  `Changed`, `Deprecated`, `Removed`, `Fixed`, `Security`, `Documented`,
  `Repository`, `Known limits`. Then at least one prose line (a non-blank line
  that is not a heading). No other `#`-heading line outside a fenced block. A
  PR whose change is both an addition and a fix writes two fragments.
- **Why that list, against the names actually used.** The released sections
  hold 13 distinct `###` names. The list keeps every name used more than once
  in the current spelling, folds one on purpose, adds one, and leaves the rest
  to the maintainer:
  - **`Documentation` folds into `Documented`.** `Documentation` is the more
    frequent (10 against 4), but it is the older habit: every use is in
    v0.3.0–v0.6.0, and every release from v0.9.0 on says `Documented`. The
    list follows the current name. A fragment headed `### Documentation` is
    refused with a message that names `Documented`, so the old habit meets
    an instruction rather than a bare "unknown section".
  - **`Known limits` is added**, last, so a known-issues note has a home.
    It is the one name of the single-use set that describes a recurring kind
    of entry (v0.7.0 used it).
  - **The other single-use headings stay out**: `Deferred (tracked, not in
    v0.1)`, `Consumer contract (docs/RUN-FEED.md)`, `Not fixed, and not
    claimed`, `Not automated, deliberately — merge-shepherd`. Each was one
    release's own framing prose, not a kind of change. Writing one is the
    maintainer's edit to the collected section at the cut (§2.3), where the
    release's opening prose is already written by hand. They are not part of
    a PR's entry.
- **Body.** Exactly what would have gone under that heading in Unreleased
  before: the bullet, the bold lead, the issue link. Nothing about the prose
  convention changes.
- **`changelog.d/README.md`** is the one non-fragment file in the directory.
  It states the name and shape above, and it is what GitHub renders when
  someone opens the directory.

### 2.2 The per-PR gate asks about fragments

`scripts/changelog-entry-check.sh <base-sha>` keeps its contract — exit 0 an
entry was added, 1 nothing a reader would find — and its local-run usage, and
gains one code: exit 2, a refusal no PR body excuses (rule 2 below). Its
question becomes:

1. **An entry** is a file under `changelog.d/` that the diff adds (or an
   existing fragment that gains a novel non-blank line, the #208 reflow rule
   applied per file), whose name matches §2.1, and which has a prose line.
   "Adds" is read with rename detection **off** (`git diff --no-renames`):
   `git diff` detects renames by default, and a fragment renamed to fix its
   name *and* edited in the same PR would otherwise show as `R`, matching
   neither "added" nor "existing fragment" cleanly. With renames off it is a
   deletion of the old path plus an addition of the new one, so the new path
   is judged whole, by its own name and shape. The reflow rule then compares
   the added file's lines against the fragments the same diff deletes, so a
   pure rename adds nothing novel and is not an entry, while a rename that
   also adds a novel line is.
2. **The release-cut exemption narrows its trigger and gains one refusal.**
   A cut is the entry, but a cut is now an added `## [vX.Y.Z]` heading for a
   version `CHANGELOG.md` at the base did not have — not, as before, any
   added `## [` line (`changelog-entry-check.sh:62-67`). Under the old trigger
   an edit to a released heading (its date, a typo) was a free pass; with the
   refusal below it would have become a hard exit 2 between releases, when
   `changelog.d/` almost always holds fragments, with no excuse and the wrong
   advice. Such an edit names a version the base already had, so it is not a
   cut, and falls through to rules 1 and 4 like any other edit — a fragment
   or `no-changelog` settles it. A cut adds the version heading and
   deletes the fragments, so it still passes. But the cut is now refused
   (exit 2) if `changelog.d/` **at HEAD** still holds any file other than
   `README.md`, naming each. The check reads HEAD's tree, not the diff, on
   purpose: a fragment merged to `main` *after* the maintainer ran the
   collector reaches the release PR through *Update branch*, which moves the
   merge-base past it, so it never appears in `base...HEAD`. It does appear in
   HEAD's tree. *Update branch* re-runs CI, and `main` requires the branch to
   be up to date (CONTRIBUTING.md:92), so the late fragment cannot reach the
   tag silently: the release PR turns red, and the maintainer moves the late
   fragment's entry into the section by hand and deletes the fragment. See §6
   for the failure this closes.
3. **A line added under `## [Unreleased]` is not counted, and draws a hint.**
   Not counted means it neither makes an entry nor refuses one: the verdict
   is decided by rules 1, 2 and 4 alone, so a PR with a valid fragment that
   also edits the Unreleased pointer paragraph (§2.4) — as this ADR's own PR
   does — exits 0. When Unreleased gained lines the script prints a warning
   that names `changelog.d/` and this ADR, so the habit of the old rule meets
   an instruction either way; if nothing else counted, that warning sits
   beside the ordinary exit 1. Keeping entries *out* of Unreleased is
   enforced in one place, `TestUnreleasedSectionHoldsNoEntries` (§2.4), which
   reads the result rather than the diff and so also covers a `no-changelog`
   PR the gate excused.
4. **A misnamed file under `changelog.d/`** (`304_foo.md`, `foo.txt`) fails
   by name. The collector refuses the whole release over a tracked file that
   is not a fragment (§2.3, item 1), so a misnamed file merged today is a
   blocked release later; refusing it at the PR that adds it is where it is
   cheap. **`changelog.d/README.md` is
   exempt by name in the gate**, exactly as in §2.1 and §2.3: adding or
   editing it is neither an entry nor a refusal.

The workflow step (`test.yml:65-86`) changes in three places only: the
"nothing outside the changelog changed" short-circuit (`test.yml:72`) treats
`changelog.d/` like `CHANGELOG.md`; the error text names `changelog.d/`
instead of `## [Unreleased]`; and the `no-changelog` excuse (`test.yml:79`)
applies to exit 1 only, so an exit 2 fails the step whatever the body says.
`no-changelog` otherwise still excuses, and still loudly. A release PR has no
reason to carry `no-changelog` (the cut *is* its entry), and an excuse must
not open a path for a late fragment to reach the tag. The short-circuit
cannot skip rule 2 on a real cut: a cut bumps the version in
`cmd/oh-my-graph` (`TestVersionMatchesChangelog`), which is outside the
changelog.

**This PR under its own gate.** It adds `changelog.d/README.md` (exempt,
rule 4), moves three entries and adds its own as fragments (entries, rule 1),
and rewrites Unreleased into the pointer paragraph (not counted, a warning,
rule 3). Exit 0, without `no-changelog`; a later edit to the pointer
paragraph that ships with a fragment passes the same way.

### 2.3 The release cut collects

A new `scripts/changelog-collect.sh <vX.Y.Z> [<date>]`, run by the maintainer
in the release PR:

1. Refuses if any file git tracks under `changelog.d/` other than `README.md`
   is not a well-formed fragment (§2.1), naming each one. Nothing is written.
   Tracked, as the gate judges it and the release commit ships it: an
   untracked or ignored file (a `.DS_Store`, an editor's swap file, a
   fragment never committed) is not an entry, and is neither refused nor
   collected. `TestChangelogFragmentsAreWellFormed` lists the same way.
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
   like every other release-PR edit) — but first checks that every fragment
   the checker passed was written into the section exactly once, and refuses,
   writing nothing, if not. The checker and the extractor are two readings of
   one shape (a CRLF fragment once read as a fragment to the first and as
   headless to the second); the postcondition makes any future disagreement
   loud instead of a silently deleted entry.

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
the duplicate-heading guard (§2.5) fails without it, and the
`[Unreleased]:` compare footnote needs it. Under it sits one fixed paragraph
pointing to `changelog.d/`.

A new guard, **`TestUnreleasedSectionHoldsNoEntries`**, refuses any line
inside it other than that paragraph and blank lines — a heading of any depth,
a list item of any marker, a fenced block, bare prose. Listing entry shapes to
refuse would leave the unlisted ones (`+ `, `1. `, a paragraph) to be
stranded; admitting the one fixed paragraph leaves none. Without it a
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
the collected section that re-adds one, before the tag. With its scope no
longer Unreleased alone, it is renamed
`TestChangelogSectionsHaveNoDuplicateHeadings`. Released sections
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

### 2.7 `backlog-batch` rule 1 gains no exception, and its prompts stay generic

`graphs/backlog-batch.yaml` is an embedded example graph that runs against
**any** repo (`--input repo="$PWD"`), and no shipped graph mentions a
changelog today. So the fragment convention does not go into it:

- **Its prompts are not changed**, and neither are the golden
  `*.resolved.json` files. Telling both lanes to write
  `changelog.d/<issue>-<slug>.md` would have every user's lane create this
  repo's directory in a repo with no such convention — and with no issue
  number to put in the name, because a lane's input is free text
  (`--input task_a="implement <feature>"`).
- **The instruction lives where this repo's conventions already live**:
  CONTRIBUTING.md's merge-rule row and `changelog.d/README.md` (§6,
  Compatibility). A lane's dev reads those like any other repo rule when the
  graph runs here, and the gate's own message (§2.2) names `changelog.d/` if
  it does not.
- **Rule 1's comment stays exceptionless and gains one generic sentence**: a
  file every lane must edit needs a per-lane design, like this repo's
  `changelog.d/` (ADR 0042). That is true in any repo, and it is the shape
  rule 1's own remedy lacked — not "serialize", but "give each lane its own
  file". Rule 1 is a YAML comment, so the edit moves no golden file.

Issue #304 asks for rule 1 to be updated "accordingly"; that is satisfied by
the comment, not by making the shipped prompts oh-my-graph-specific.

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
- **No shipped graph's behaviour.** `backlog-batch`'s prompts and every golden
  `*.resolved.json` are untouched; only rule 1's comment gains a sentence
  (§2.7).

## 4. Alternatives considered

### 4.1 (a) Name `CHANGELOG.md` as the rule-1 exception, resolved by `merge=union`

The cheapest option: one `.gitattributes` line, `CHANGELOG.md merge=union`,
and one sentence in rule 1. Git's built-in union driver keeps both sides'
lines on a conflict, so two appended bullets rebase cleanly. It keeps the
single file everybody already knows, keeps Unreleased readable on `main`, and
needs no collector.

It loses, on four counts:

1. **It may resolve only where a local git runs it — unverified.** The
   conflict in §1.2 is met on GitHub, at *Update branch* and at merge, and
   whether GitHub's server-side merge honours `.gitattributes` merge drivers
   has not been measured in this repository. If it does not, every second PR
   would still be checked out, rebased locally and force-pushed — the step
   the issue asks to remove. This count is a risk, not a finding, and the
   rejection does not rest on it: counts 2 and 3 reject (a) on their own,
   whichever way the measurement falls (§7, 1).
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
   there is nothing to merge. That silence has one place it would be wrong —
   the release PR, where today's conflict is what tells the maintainer a PR
   merged after the cut — and §2.2's rule 2 puts a refusal there instead.

What (a) gets right — one file, readable Unreleased — is the cost (b) pays,
and §6 states it.

### 4.2 Others

- **Serialize every lane on the changelog (rule 1's own remedy).** Loses: every
  lane edits the changelog, so every batch collapses to one lane.
- **`no-changelog` on every lane PR, one changelog PR after the batch.** Loses
  the gate's whole reason (`test.yml:43-45`): the entry would be written after
  the merge, by someone not looking at the change. That is the shape that cost
  five round trips in v0.9.0.
- **Generate entries from commit subjects.** Loses on v0.8.0's lesson
  (`release-notes.sh:5-9`): a commit list cannot say why a change matters.
- **Generate entries from a changelog section of each PR body.** That prose
  is still hand-written, so v0.8.0's lesson does not apply. It loses on two
  other counts. The release cut would need the network, to fetch every
  merged PR's body from GitHub, where today it reads only the tree. And the
  entry would not be in the diff: a PR body is edited outside review, after
  approval, with no check re-run, so the text that ships is not the text that
  was reviewed. A fragment is a file, so it is reviewed in the diff and
  collected offline.
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
   **Renames:** a fragment renamed with no other change exits 1 (nothing
   novel); renamed *and* given a novel line exits 0.
4. **Gate: a line under Unreleased is not counted.** Alone, it exits 1 and
   stderr names `changelog.d/`. **With a well-formed fragment in the same
   diff**, an edit to the Unreleased paragraph exits 0 and still prints the
   warning.
5. **Gate: a misnamed file is refused by name.** `changelog.d/304_x.md` exits
   1 and is named. A well-formed fragment renamed to `304_x.md` is refused by
   its new name the same way. **`README.md` is exempt**: adding
   `changelog.d/README.md` beside a fragment exits 0, and alone exits 1
   without naming it as misnamed.
6. **Gate: the release cut still passes.** A diff that adds `## [v9.9.9]` and
   deletes every fragment exits 0.
7. **Gate: a cut with a fragment left at HEAD is refused.** The same cut,
   with a fragment committed on `main` after it and merged in (so it is in
   HEAD's tree but not in `base...HEAD`), exits **2**, not 1, and names the
   fragment. Exit 2 is what keeps `no-changelog` from excusing it; that the
   workflow excuses exit 1 only is a one-line `test.yml` change, checked in
   review like the rest of that file.
8. **Collector: grouping and order.** Three fragments — two `Fixed`, one
   `Added`, issue numbers out of order — produce one `### Added` then one
   `### Fixed`, fragments in numeric issue order, under `## [vX] - <date>`
   placed directly below the Unreleased block; the fragments are deleted.
9. **Collector: refusals write nothing.** A malformed fragment, no fragments,
   and an already-present version heading each exit non-zero with
   `CHANGELOG.md` and `changelog.d/` byte-identical.
10. **Collector output feeds `release-notes.sh` unchanged.** On test 8's
    result, `release-notes.sh vX` prints the collected section (the credit
    half is skipped: no previous tag).
11. **`TestChangelogFragmentsAreWellFormed`** over the real `changelog.d/`:
    every file git tracks there but `README.md` matches §2.1's name and shape, with a known
    section name. Its own failure cases are table-driven, and include
    `### Documentation` refused with a message naming `Documented`, and
    `### Known limits` accepted.
12. **`TestUnreleasedSectionHoldsNoEntries`**, with a failing case for a
    `### ` and a `#####` heading, a `- `, `+ ` and `1. ` item, a prose
    paragraph, a fenced block and a reworded pointer; the pointer alone
    passes.
13. **The widened duplicate-heading guard** fails on a `## [v<Version>]`
    section with two `### Fixed`, still ignores fenced headings, and still
    ignores sections below the current one.
14. **The two scan lists**: `changelog.d/` is in `historyExcluded` and is
    reached by the `docsclaims` walk.
15. **Gate: an edited released heading is not a cut.** Changing a released
    heading's date with a fragment pending in `changelog.d/` exits 1, not 2
    (so `no-changelog` excuses it), and 0 beside a fragment of its own. An
    edit that makes an existing fragment malformed (`M` status) exits 1 and
    names it.
16. **Collector: what it reads.** A CRLF fragment with a leading blank line
    beside another fragment is collected, with no `\r` in `CHANGELOG.md`;
    untracked and ignored files are neither refused nor collected nor
    deleted; a checker that passes what the extractor cannot place is
    refused by the postcondition, writing nothing; a `CHANGELOG.md` with no
    `## [Unreleased]` heading is refused, writing nothing.

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
  forgetting to promote Unreleased today.
- **A fragment is left in `changelog.d/` at the cut** — a manual cut that
  skipped the collector, or a fragment that merged to `main` after the
  collector ran and before the release PR merged. Unguarded, the tag would
  ship that PR's code while its fragment waited in `changelog.d/`, to be
  collected into the *next* release's notes, describing a change that had
  already shipped. Today the second case is loud by accident: *Update
  branch* on the release PR conflicts on Unreleased. Fragments remove that
  conflict, so §2.2's rule 2 replaces it on purpose: a cut whose HEAD still
  holds a fragment exits 2, *Update branch* re-runs the check, and `main`'s
  up-to-date rule means the release PR cannot merge without it. The
  collector refuses a second write of the section (§2.3, 3), so the fix is
  to move the late fragment's body into the section by hand and delete the
  fragment. Removing the section and collecting again is *not* a fix: the
  first run already deleted the fragments it collected, so the second would
  write a section holding only the late one, and nothing would notice the
  rest were gone.
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
  change in the same PR — the row names `changelog.d/`, the fragment name and
  shape (pointing to `changelog.d/README.md`), and the local gate command; the
  checklist gains "run `scripts/changelog-collect.sh vX.Y.Z` first, then
  write the section's opening prose". These two files are the only place the
  convention is taught, so they are also what a `backlog-batch` lane's dev
  reads when the graph runs on this repo (§2.7).
- **DESIGN.md**'s repo layout gains `changelog.d/`.
- **Users of oh-my-graph**: nothing. The binary, the graph schema, and the
  release page are unchanged.

## 7. Falsification

1. **GitHub's server-side merge is shown to honour `merge=union`, and a
   release cycle shows no stranded or contradictory entries under it.** Then
   §4.1's first count falls and counts 2 and 3 are the whole argument; the
   trade would be worth re-weighing against the reader cost in §6.
2. **Fragment conflicts are observed between ordinary PRs** (not same-issue
   lanes). Then the naming scheme does not make names disjoint, and it, not
   the mechanism, needs to change.
3. **A release is cut whose collected section needed more manual
   restructuring than the old Unreleased merge did** (v0.14.0 needed a whole
   graph node to merge eleven subheadings into four). Then
   one-heading-per-fragment is the wrong grain.
