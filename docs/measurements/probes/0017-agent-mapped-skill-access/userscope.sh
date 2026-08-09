#!/bin/bash
# The US arm: the same recorded argv, with the planted skill in the USER's own
# ~/.claude/skills and NOWHERE else.
#
# Why this exists. setup.sh plants the skill in PROJECT scope
# (<ws>/work/.claude/skills) as well as in the staging source, so T/C1/C0
# resolved a project-scope definition. That is a fine corpus for the capability
# question — a node with no `Skill` tool invokes nothing from any scope — but it
# is NOT the sentence ADR 0017 wrote ("such a node already sees the user's real
# skills"), and it is not what the C1 arm needs in order to be quoted as
# "measured to work over the user's own corpus". This runs the row with the
# corpus genuinely in user scope.
#
#   plant   moves the definition to ~/.claude/skills/<skill> and REMOVES the
#           project-scope copy from <ws>/work/.claude/skills
#   clean   removes ~/.claude/skills/<skill> again
#
# The user's own skills directory is written to and then restored; the planted
# name is unique to this probe and nothing else in it is touched.
set -eu

SKILL="omg-probe-standalone-html"
USER_SKILLS="$HOME/.claude/skills"
MODE="${1:-plant}"
WS="${2:-/tmp/omg-agentmap-skill}"
HERE="$(cd "$(dirname "$0")" && pwd)"

case "$MODE" in
clean)
  rm -rf "${USER_SKILLS:?}/$SKILL"
  echo "removed $USER_SKILLS/$SKILL"
  ;;
plant)
  [ -d "$WS/work/.claude/agents" ] || {
    echo "no probe workspace at $WS — run setup.sh first" >&2
    exit 1
  }
  [ -d "$USER_SKILLS/$SKILL" ] && {
    echo "$USER_SKILLS/$SKILL already exists; refusing to overwrite" >&2
    exit 1
  }
  mkdir -p "$USER_SKILLS"
  cp -R "$WS/skills-src/$SKILL" "$USER_SKILLS/$SKILL"
  # The point of the arm: project scope must NOT also carry it, or the
  # resolution source is ambiguous again.
  rm -rf "$WS/work/.claude/skills"
  echo "planted $USER_SKILLS/$SKILL; project-scope copy removed from $WS/work"
  echo "now:"
  echo "  AM=\$(ls $WS/argv/omg-probe-writer/* | head -1)"
  echo "  python3 $HERE/replay.py $WS TU  \"\$AM\" 1 verbatim"
  echo "  python3 $HERE/replay.py $WS C1U \"\$AM\" 1 add_skill"
  echo "  bash $HERE/userscope.sh clean"
  ;;
*)
  echo "usage: userscope.sh [plant|clean] [ws]" >&2
  exit 2
  ;;
esac
