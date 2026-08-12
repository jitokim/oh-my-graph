#!/bin/bash
# Builds the probe workspace. Spawns no claude.
#
#   <ws>/repo/                              a REAL git repository — the node's
#                                           cwd, because "repository-supplied"
#                                           means a definition that arrives
#                                           with a checkout
#   <ws>/repo/.claude/agents/omg-probe-writer.md
#                                           the PLANTED definition, committed
#   <ws>/user-agents/omg-probe-writer.md    the same name at user scope; stands
#                                           in for ~/.claude/agents, which this
#                                           probe never touches
#   <ws>/run-pre/, <ws>/run-fix/            what BindAgentStaging materializes
#   <ws>/argv-pre/, <ws>/argv-fix/          runner.buildArgs' output, per node
#
# Nothing under ~/.claude is written, read-modify-written or removed by this
# probe.
set -eu
WS="${1:-/tmp/omg-repo-agent}"
HERE="$(cd "$(dirname "$0")" && pwd)"
. "$HERE/agents.sh"

# $WS is deleted recursively below, and it is whatever the caller typed:
# `setup.sh "$HOME"` would take the caller's home directory with it. So delete
# only a workspace this probe owns — one that does not exist yet, or one
# carrying the marker a previous run of THIS script left in it.
MARKER=".omg-probe-workspace"
case "$WS" in
  "" | / | "$HOME" | "$HOME"/) echo "setup.sh: refusing $WS as a workspace" >&2; exit 2 ;;
esac
if [ -e "$WS" ] && [ ! -f "$WS/$MARKER" ]; then
  echo "setup.sh: $WS exists and carries no $MARKER — refusing to rm -rf a" >&2
  echo "          directory this probe did not create. Pass a fresh path." >&2
  exit 2
fi

rm -rf "$WS"
mkdir -p "$WS/repo/.claude/agents" "$WS/user-agents" "$WS/logs" "$WS/tool_use"
printf 'created by %s\n' "$HERE/setup.sh" >"$WS/$MARKER"

git -C "$WS/repo" init -q
git -C "$WS/repo" config user.email probe@example.invalid
git -C "$WS/repo" config user.name "omg probe"

plain_agent "$WS/repo/.claude/agents"
plain_agent "$WS/user-agents"

git -C "$WS/repo" add -A
git -C "$WS/repo" commit -qm "probe fixture repository, with a planted agent definition"

chmod +x "$HERE/shim.sh"
echo "workspace: $WS"
find "$WS/repo/.claude" "$WS/user-agents" -type f | sort
