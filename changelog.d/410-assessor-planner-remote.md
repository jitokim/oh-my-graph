### Added

- **The goal assessor sees the run's remote changes.** In `auto`, the
  assessment prompt gains an `ENGINE-OBSERVED remote changes` block right
  after the run outcome, with or without `--forbid-remote-changes`, built
  from the run's remote change record: the entry counts and which parts were
  compared (and, on a guarded run, guarded) are the engine's own lines; every
  entry, with its leg and its label ("observed during the run; points at a
  commit this run made" or "observed, not attributable"), and every
  not-compared reason sits inside a nonce fence, each line cut to 300 bytes,
  at most 20 entries and 10 not-compared items with the rest counted. A part
  not compared, or a run with no record, reads as unknown, never as "no
  changes". The printed summary and the block share one wording (ADR 0046).
  ([#410](https://github.com/jitokim/oh-my-graph/issues/410))
- **`auto --forbid-remote-changes` tells the planner.** Every planner prompt
  (first, repair and later cycles) gains one sentence: the operator forbade
  remote changes and the engine observes the remote itself, so no planned
  node pushes, adds a remote, or opens or edits a pull request. Without the
  flag the planner prompt is byte-identical. A guarded `auto` now exits 6,
  its last line naming the change, when its final cycle's run made a
  forbidden one, and its guard lines list branches and pull requests under
  not guarded (`auto: no run branches`), since a planned node's own push is
  never attributable.
  ([#410](https://github.com/jitokim/oh-my-graph/issues/410))
- **A guarded leg still compares branches when its end snapshot cannot list
  the remotes.** A node that pushed and then broke `git remote -v` (a
  corrupted `.git/config`) used to leave the branches part not compared and
  its push unseen; a `--forbid-remote-changes` leg now reads each remote it
  had at the start through that start's fetch URL, so the push is recorded,
  while the remotes part stays not compared. Once the repository's own
  config changed or cannot be read, those reads run from an empty directory
  outside the repository, so an `insteadOf` or a broken `.git/config` cannot
  redirect them. The commit check is not moved outside: it still reads that
  config, so a broken one can leave the node's own push `not_attributable`,
  and a guarded `run` leg then fails closed — a `not_attributable` new branch
  or PR fails it too — so that push exits 6; an `auto` leg, with no run
  branches, does not. Unguarded runs are unchanged. If the leg's start could
  not list the remotes, the branches part is still not compared, as ADR 0046
  already lists.
  ([#410](https://github.com/jitokim/oh-my-graph/issues/410))
