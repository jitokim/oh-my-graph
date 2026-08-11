#!/bin/bash
# Switches the workspace between the three phases. What varies is only WHICH
# DEFINITION SOURCES are loadable; the argv and the prompts never change.
#
#   A  clean composite   staged corpus only. No repo skill, no project plugin.
#                        Answers: does the composite deliver, and what does it
#                        do to the ceiling.
#   B  collision         staged + repo-project-scope + project plugin, all three
#                        carrying a skill of the SAME NAME and different tokens.
#                        Answers: which one resolves.
#   C  repository        one repository-supplied skill and nothing else, under
#                        the CHEAPER arm (no staged plugin). Answers: can a
#                        SKILL.md committed to the target repo become invocable
#                        procedure text on an agent-mapped node.
#
# git state is part of the fixture in phase C: the skill is COMMITTED, because
# "repository-supplied" means a definition that arrives with a checkout.
set -eu
PHASE="${1:?usage: phase.sh A|B|C [ws]}"
WS="${2:-/tmp/omg-lift-exclusion}"
HERE="$(cd "$(dirname "$0")" && pwd)"
. "$HERE/skills.sh"

rm -rf "$WS/repo/.claude/skills"
mkdir -p "$WS/repo/.claude/skills"
rm -f "$WS/repo/.claude/settings.json" "$WS/repo/.claude/settings.local.json"

case "$PHASE" in
  A)
    ;;
  B)
    plant_repo_collide "$WS/repo/.claude/skills"
    # Declared at PROJECT scope, in the repo the node runs in. An agent-mapped
    # node omits --setting-sources entirely, so the CLI's own default
    # (user + project + local) loads this exactly as it loads the user's own.
    ( cd "$WS/repo" \
      && claude plugin marketplace add "$WS/user-mkt" --scope project \
      && claude plugin enable omg-probe-user-plugin@omg-probe-user-mkt --scope project )
    ;;
  C)
    plant_repo_house "$WS/repo/.claude/skills"
    ;;
  *)
    echo "unknown phase $PHASE" >&2; exit 2 ;;
esac

git -C "$WS/repo" add -A
git -C "$WS/repo" commit -qm "phase $PHASE fixture" || true
echo "phase $PHASE:"
find "$WS/repo/.claude" -name SKILL.md -o -name settings.json | sort
