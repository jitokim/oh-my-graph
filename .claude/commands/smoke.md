---
description: Build oh-my-graph and run the haiku-smoke graph against a real claude subscription (costs a few cents; manual only, never CI).
allowed-tools: Bash(make smoke), Bash(make build)
---

Run `make smoke` via Bash. This builds the `oh-my-graph` binary and runs
`graphs/haiku-smoke.yaml` against a real, logged-in `claude` subscription —
it costs a few cents and requires the user to already be authenticated.

When it finishes, report the RunLedger output back to the user (session id,
cost, verdict, duration per node, plus total cost). If the run fails, surface
the failing node and its reason rather than re-running automatically.

This is a manual verification step, not something to run unprompted or wire
into any automated check — see CONTRIBUTING.md's "Build, test, smoke"
section.
