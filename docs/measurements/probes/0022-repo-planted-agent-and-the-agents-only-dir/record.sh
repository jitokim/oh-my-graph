#!/bin/bash
# Sets the phase and records the argv for one scan order.
#
#   usage: record.sh <ws> R|A pre|fix
#
#   R  resolution   both definitions STAMPED, each with its own token. Answers:
#                   which definition does the CLI resolve from the staged dir.
#   A  ceiling      both definitions PLAIN. The ceiling arms run --tools Bash
#                   with no Write, so a stamp instruction they cannot obey
#                   would be a second variable.
#
#   pre  the scan order DefaultAgentDirs() returns at 3ea7355 (user, project)
#   fix  user scope only — the proposed fix
#
# The repository copy is COMMITTED after every phase change: "repository-
# supplied" means a definition that arrives with a checkout, and a fixture that
# only ever existed in the working tree would not be that.
set -eu
WS="${1:?usage: record.sh <ws> R|A pre|fix}"
PHASE="${2:?usage: record.sh <ws> R|A pre|fix}"
SCOPE="${3:?usage: record.sh <ws> R|A pre|fix}"
# $SCOPE is a path component below (`argv-$SCOPE`, `run-$SCOPE`) and one of them
# is `rm -rf`'d, so it gets the same closed check $PHASE gets: `foo/../..` is a
# traversal out of $WS, not a scan order.
case "$SCOPE" in
  pre|fix) ;;
  *) echo "record.sh: unknown scope $SCOPE" >&2; exit 2 ;;
esac
HERE="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$HERE/../../../.." && pwd)"
. "$HERE/agents.sh"

case "$PHASE" in
  R)
    stamped_agent "$WS/repo/.claude/agents" OMG-L-AGENT-REPO-9101
    stamped_agent "$WS/user-agents"         OMG-L-AGENT-USER-9102
    ;;
  A)
    plain_agent "$WS/repo/.claude/agents"
    plain_agent "$WS/user-agents"
    ;;
  *) echo "record.sh: unknown phase $PHASE" >&2; exit 2 ;;
esac

git -C "$WS/repo" add -A
if ! git -C "$WS/repo" diff --cached --quiet; then
  git -C "$WS/repo" commit -qm "phase $PHASE fixture"
fi

rm -rf "$WS/argv-$SCOPE" "$WS/run-$SCOPE"
cd "$REPO_ROOT"
go run "$HERE/_harness/main.go" "$WS" "$HERE/shim.sh" "$SCOPE" >"$WS/plan-report-$PHASE-$SCOPE.json"
cat "$WS/plan-report-$PHASE-$SCOPE.json"
echo "staged directory:"
find "$WS/run-$SCOPE" -type f | sort
