### Fixed

- **`auto`'s check nodes no longer FAIL a goal for state it forbids changing.**
  Under a goal that says not to push, not to open or edit a pull request, or
  not to create a remote, the final check node asserted an open pull request
  and replied FAIL on work that had landed. It now asserts an open pull
  request only when the goal's own nodes push the branch and open it, and
  otherwise — including a goal that says not to push or not to open or edit
  a pull request — asserts the local branch ref. Forbidding only a new remote
  keeps the open-PR assertion for a goal whose own nodes push to an existing
  remote and open the pull request. Every planned check node asserts
  only what the run's own nodes produce or change, never the absence of
  something the goal only forbids creating (a remote that already existed is
  not a violation), and a forbidden action is the operator's next step, never
  a reason to reply FAIL. Planner prompt only, with and without
  `--verify-cmd`. ([#388](https://github.com/jitokim/oh-my-graph/issues/388))
