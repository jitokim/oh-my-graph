### Added

- **`auto --interview` asks you up to five questions about the goal before it
  plans, and gives your answers to the planner.** Off by default: without the
  flag `auto` sends the planner exactly the prompt it did before, and a script
  or an agent never waits on a keyboard. With it, stdin must be a terminal (if
  it isn't, the run is refused before any model call), and each question is
  one read-only interviewer call with no tools. Type `/skip` to skip a question
  (it still counts toward the five) or `/done` to plan with what you have
  answered. An answer over 2000 bytes is refused, never cut, and input that
  ends before any answer is refused. The answers reach the planner only, as
  fenced data under a per-call nonce. No planned node receives them, and they
  change no setting, grant or tool. The interview is asked once per goal, not
  per cycle. Its cost counts in cycle 1's planning cost, the goal's spend and
  `--max-goal-budget-usd`. Each run directory keeps the answers as
  `interview.md`, and `state.json` records their hash, the counts, why the
  interview ended and its cost, never the text. `resume` refuses a run whose
  copy is missing or altered, and asks nothing. It works with `--plan-only`,
  which keeps the answers beside the saved spec.
  ([#319](https://github.com/jitokim/oh-my-graph/issues/319))
- **`oh-my-graph design "<goal>" --out <file>` interviews you, plans once,
  writes the graph as YAML and lints it, and never runs it.** It is for when
  you would rather review the graph and launch it yourself with `run`. No node
  spawns and no run directory is created. Before anything is spent, it refuses
  an `--out` that already exists (it never overwrites a file) and
  `--conventions`, which nothing it runs could carry. A file that does not lint
  is kept, named in the error, and the command exits non-zero. It prints the
  interview's cost and the planner's separately, then the total.
  ([#319](https://github.com/jitokim/oh-my-graph/issues/319))
