### Fixed

- **A CLI caught mid auto-update no longer halts the run.** While `claude` or
  `codex` updates itself it can be missing from PATH for a moment, and a node
  spawned then failed with `exec: "claude": executable file not found in $PATH`,
  which halted the run. `runner.CLIRunner` now retries that one error
  (`exec.ErrNotFound` on the runtime binary) up to 5 times, 1 second apart,
  before reporting it as it did before. The preflight "command exists" check
  waits for the same window, so a run started during an update is not refused
  either. Nothing else is retried: a non-zero exit, an unparseable reply, a
  timeout, and any other start failure still fail on the first try. Cancelling
  during a wait stops the retry at once. The env scrub is unchanged and no new
  process-starting code was added. A CLI that really is not installed now takes
  about 4 seconds to be refused; docs/LIMITATIONS.md lists what the retry
  does not cover.
  ([#298](https://github.com/jitokim/oh-my-graph/issues/298))
