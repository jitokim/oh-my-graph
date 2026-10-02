### Fixed

- **`e2e-verify`'s optional stress step now has to fit inside the node's
  timeout.** An e2e node once picked `go test -race -count=300` on its own,
  ran past its 20-minute timeout and was killed with no verdict, though the
  code was fine. The fragment now names the bound `verify_command` — the
  command the engine runs as evidence — as the required check that runs first, caps stress at a quarter of the timeout
  (5 of the default 20 minutes) in one `go test` call bounded by
  `-timeout 10m`, treats a named repetition count as a ceiling, and lets the
  node skip stress when the required check already used the time.
  `TestE2EVerifyStressFitsItsTimeout` holds those numbers against every
  graph that splices the fragment.
  ([#292](https://github.com/jitokim/oh-my-graph/issues/292))
