### Changed

- **`auto --max-cycles` now stops when a done-list command cannot be run by
  any planned node.** The final check node names each such command on an
  `UNRUNNABLE: <command>` line of its FAIL reply. The goal loop reads that
  line from the failed check node's reply file (`failed/<node-id>.out`) —
  a reply that is never shown to the assessor or to another node. Those
  lines are copied to the front of that file, so they survive both the
  256 KiB cut when it is written and the 64 KiB cut when the goal loop
  reads it, wherever they sit in a long reply. The loop then ends
  after that cycle with the new stop reason `unrunnable_done_item` instead
  of planning cycles that could never run it. The goal summary lists the
  commands and suggests checking them with `--verify-cmd` or a
  configuration that allows them; the exit code is the usual unmet-goal 1.
  ([#333](https://github.com/jitokim/oh-my-graph/issues/333))
