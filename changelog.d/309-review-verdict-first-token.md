### Fixed

- **`self-dev`'s `review-verdict` no longer passes a review whose first token
  merely starts with `CLEAN` (`CLEANUP`, `CLEANLY`).** Its verify step deleted
  every whitespace character before matching `grep -Eq '^(CLEAN|MINOR:)'`, so
  `CLEANUP: renamed the helper` read as `CLEAN` and passed the gate. It now
  strips only the markdown and keeps the spaces between words:
  ``tr -d '*_`\r' | tr '\t\n' '  ' | grep -Eq '^ *(CLEAN([^A-Za-z0-9]|$)|MINOR *:)'``.
  `**CLEAN**`, `` `CLEAN` nothing to change ``, `CLEAN — …`, `**MINOR**:`,
  `MINOR :` and leading blank lines still pass. If you copied the old command
  into your own graph, copy this one.
  ([#309](https://github.com/jitokim/oh-my-graph/issues/309))
