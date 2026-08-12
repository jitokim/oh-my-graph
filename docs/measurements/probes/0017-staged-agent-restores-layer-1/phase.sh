#!/bin/bash
# Switches the workspace between the phases. What varies is only WHICH
# DEFINITION SOURCES are loadable; the argv and the prompts never change.
#
#   A  clean composite   staged corpus only. No repo skill, no project plugin.
#                        Answers: does the candidate hold the ceiling, and is
#                        Skill live under it.
#   B  collision         staged + repo-project-scope + project plugin, all three
#                        carrying a skill of the SAME NAME and different tokens.
#                        Answers: which one resolves under the candidate.
#   C  repository        one repository-supplied skill and nothing else.
#                        Answers: can a SKILL.md committed to the target repo
#                        still become invocable procedure text.
#   R  resolution        phase A, with BOTH agent definitions stamped, each with
#                        its own token. Answers: does --agent resolve under
#                        `--setting-sources ""`, and WHICH definition.
#   F  frontmatter       phase A, with the STAGED agent declaring
#                        `tools: Bash, Write` against a node whose --tools is
#                        Write. Answers: does layer 3 still bind a staged
#                        agent's frontmatter.
#
# git state is part of the fixture in phases B and C: the skill is COMMITTED,
# because "repository-supplied" means a definition that arrives with a checkout.
set -eu
PHASE="${1:?usage: phase.sh A|B|C|R|F [ws]}"
WS="${2:-/tmp/omg-staged-agent}"
HERE="$(cd "$(dirname "$0")" && pwd)"
. "$HERE/skills.sh"
. "$HERE/agents.sh"

rm -rf "$WS/repo/.claude/skills"
mkdir -p "$WS/repo/.claude/skills"
rm -f "$WS/repo/.claude/settings.json" "$WS/repo/.claude/settings.local.json"
plain_agent "$WS/repo/.claude/agents"
bash "$HERE/stage.sh" "$WS" plain >/dev/null

case "$PHASE" in
  A)
    ;;
  B)
    plant_repo_collide "$WS/repo/.claude/skills"
    # Declared at PROJECT scope, in the repo the node runs in. The SHIPPED
    # mapped node omits --setting-sources entirely, so the CLI's own default
    # (user + project + local) loads this. Whether the CANDIDATE still does is
    # exactly what arms K-COLLIDE and K-UPONLY ask.
    ( cd "$WS/repo" \
      && claude plugin marketplace add "$WS/user-mkt" --scope project \
      && claude plugin enable omg-probe-user-plugin@omg-probe-user-mkt --scope project )
    ;;
  C)
    plant_repo_house "$WS/repo/.claude/skills"
    ;;
  R)
    # Both copies stamped, different tokens. The REPO copy is the one an
    # agent-mapped node resolves today; the STAGED copy is the candidate's.
    stamped_agent "$WS/repo/.claude/agents" OMG-K-AGENT-REPO-8801
    bash "$HERE/stage.sh" "$WS" stamped >/dev/null
    ;;
  F)
    bash "$HERE/stage.sh" "$WS" widening >/dev/null
    ;;
  *)
    echo "unknown phase $PHASE" >&2; exit 2 ;;
esac

git -C "$WS/repo" add -A
# Phase A stages nothing new after the first time, and "nothing to commit" is
# the one commit failure that means the fixture is already correct. Every other
# one means phase B/C cannot claim the repository skill arrived in a CHECKOUT,
# so it has to be fatal rather than swallowed by a blanket `|| true`.
if ! git -C "$WS/repo" diff --cached --quiet; then
  git -C "$WS/repo" commit -qm "phase $PHASE fixture"
fi
echo "phase $PHASE:"
find "$WS/repo/.claude" -name SKILL.md -o -name settings.json -o -name '*.md' -path '*agents*' | sort
find "$WS/run/skills-plugin-agents" -type f | sort
