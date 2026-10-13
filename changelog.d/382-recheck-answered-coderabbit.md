### Fixed

- **`merge-shepherd`'s `recheck` no longer halts on a CodeRabbit review that
  `triage` already answered.** When triage replied to a `CHANGES_REQUESTED`
  without pushing, the bot re-reviewed the same commit seconds later, but
  `recheck` had already answered `LATCHED` and halted the run. Triage now ends
  its report with `coderabbit-answered-through: <T>`, which is the
  `submittedAt` of the newest bot review it answered, copied from gh. If it
  answered none, it writes `none`. A bot `CHANGES_REQUESTED` on head with `at`
  ≤ `<T>` is now waited out. A newer `APPROVED` is green, a newer
  `CHANGES_REQUESTED` is `LATCHED`, and a `COMMENTED` never settles it. If
  neither arrives within the budget, the answer is `UNSETTLED`, never
  `RECHECKED`. A `<T>` that matches no real bot review counts as absent. A
  review newer than `<T>`, or one with no answered-through line, still latches
  at once. `result_matches` is unchanged.
  ([#382](https://github.com/jitokim/oh-my-graph/issues/382))
