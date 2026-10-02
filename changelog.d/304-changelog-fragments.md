### Repository

- **A changelog entry is now a file the pull request owns, so two open PRs no
  longer conflict on `CHANGELOG.md`.** Every PR used to append to the one
  `## [Unreleased]` section, so the second of any two PRs open at once hit a
  merge conflict that *Update branch* could not resolve, and every lane of a
  `backlog-batch` run edited the same file — breaking the graph's own rule 1,
  that lanes share no files. An entry is now `changelog.d/<issue>-<slug>.md`:
  one `### <Section>` heading from a fixed list, then the entry
  (`changelog.d/README.md` has the name and shape). The `changelog` CI job
  (`scripts/changelog-entry-check.sh`) counts a fragment the PR adds, or a
  novel line in one it edits, and refuses a misnamed or malformed file by name.
  A line under `## [Unreleased]` no longer counts, and draws a warning. A
  release PR runs the new `scripts/changelog-collect.sh vX.Y.Z`, which writes
  every fragment into the version's section, each heading once, and deletes
  them. A cut — a new version heading, not an edit to a released one — that
  still leaves a fragment in `changelog.d/` is refused with
  exit 2, which `no-changelog` cannot excuse, so a PR merged after the
  collector ran cannot ship without its entry. `scripts/release-notes.sh` is
  unchanged. Two new tests guard the convention:
  `TestUnreleasedSectionHoldsNoEntries` keeps entries out of Unreleased, and
  `TestChangelogFragmentsAreWellFormed` checks every fragment. The duplicate-heading guard now also covers the
  current release's section. `backlog-batch` rule 1 gains no exception; its
  comment now says a file every lane must edit needs a per-lane design.
  ([ADR 0042](docs/adr/0042-a-changelog-entry-is-a-file-the-pr-owns.md),
  [#304](https://github.com/jitokim/oh-my-graph/issues/304))
