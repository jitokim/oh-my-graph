### Fixed

- **A `--conventions` path swapped for a FIFO no longer blocks the launch.**
  Each path was validated and then opened, so a path replaced by a FIFO in
  between left `auto` waiting on an open no writer would ever satisfy, before
  any node started. The file is now opened first — non-blocking where the
  platform has `O_NONBLOCK`, so opening a FIFO cannot hang — and the opened
  handle is checked: it must be a regular file and the same file the launch
  validated, or the path is refused with the reason. The limit is gone from
  docs/LIMITATIONS.md. No new process-starting code was added.
  ([#296](https://github.com/jitokim/oh-my-graph/issues/296))
