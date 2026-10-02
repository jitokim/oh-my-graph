### Fixed

- **`self-dev` no longer opens its PR over a review that found something.**
  Its two parallel reviews passed on `CLEAN` and on `FINDINGS:` alike, and
  `pr` depended on them directly, so a finding from either reviewer was
  quoted into a draft PR instead of being fixed. The reviews now fan in to a
  new `review-verdict` node. It accepts only `CLEAN`, and it declares
  `feedback: { rerun: dev, max: 2 }`, so a finding re-runs
  dev → e2e → both reviews with the findings quoted in dev's prompt. Only an
  exhausted loop fails the run, and then no PR opens. When the verdict
  replies `FINDINGS:`, dev's payload is that reply, which carries the
  findings verbatim. A `CLEAN` is not taken on the model's word: its `verify`
  reads both review artifacts itself, and if either did not come back clean
  it fails the verdict and prints the head of each open review, which then
  becomes dev's payload instead. The
  reviews are inside the loop, so a re-run reviewer gets its own previous
  findings through `{{ self.previous }}`. The node has to sit at the fan-in
  because ADR 0010 refuses an arc on either review: an arc on one has a side
  exit through its sibling, and two arcs that both re-run `dev` overlap.
  Worst case is 15 runs per task, or 18 counting `e2e`'s own retry. A run
  that comes back clean first time pays for one extra node.
  `dev-review-pr` keeps the advisory review on purpose and says why in its
  header: it is the everyday template, and its ready PR puts both reviews in
  front of the human who decides the merge.
  ([#293](https://github.com/jitokim/oh-my-graph/issues/293))
