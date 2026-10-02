# changelog.d — unreleased changelog entries

Every pull request that changes something writes its CHANGELOG entry here, as
**its own file**, instead of appending to `CHANGELOG.md`'s `## [Unreleased]`
section. Two PRs then never edit a common file, so they never conflict on the
changelog, and `backlog-batch`'s rule 1 (lanes share no files) holds
([ADR 0042](../docs/adr/0042-a-changelog-entry-is-a-file-the-pr-owns.md),
[#304](https://github.com/jitokim/oh-my-graph/issues/304)).

## Name

`changelog.d/<issue>-<slug>.md` — the issue number, then a kebab-case slug:

```
304-changelog-fragments.md
```

The name must match `^[0-9]+-[a-z0-9]+(-[a-z0-9]+)*\.md$`. Two PRs for one
issue pick different slugs. A change with no issue opens one first.

## Shape

The first non-blank line is exactly one `### <Section>` heading, from this list
(in the order a release prints them):

`Added`, `Changed`, `Deprecated`, `Removed`, `Fixed`, `Security`,
`Documented`, `Repository`, `Known limits`

Then the entry, written exactly as it would have been under that heading in
`CHANGELOG.md`: the bullet, the bold lead, the issue link. No other heading
outside a fenced block. A change that is both an addition and a fix writes two
fragments.

```markdown
### Fixed

- **What a reader notices, in one bold line.** Why it matters, and what
  changed. ([#304](https://github.com/jitokim/oh-my-graph/issues/304))
```

## What checks it

- **The `changelog` CI job** (`scripts/changelog-entry-check.sh`) counts a
  fragment the PR adds, or a novel line in one it edits, as the PR's entry,
  and refuses a misnamed or malformed file here by name. Run it before
  pushing:

  ```sh
  scripts/changelog-entry-check.sh "$(git merge-base main HEAD)"
  ```

  A line added under `## [Unreleased]` is not an entry; it draws a warning.
- **`go test`**: `TestChangelogFragmentsAreWellFormed` holds every file here to
  the name and shape above; `TestUnreleasedSectionHoldsNoEntries` keeps
  `## [Unreleased]` free of entries.
- **The release PR** runs `scripts/changelog-collect.sh vX.Y.Z`, which writes
  every fragment into a new `## [vX.Y.Z]` section of `CHANGELOG.md`, each
  heading once, and deletes the fragments. This README is the one file it
  leaves.
