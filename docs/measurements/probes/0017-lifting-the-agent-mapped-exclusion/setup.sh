#!/bin/bash
# Builds the probe workspace and records the argv. Spawns no claude.
#
#   <ws>/repo/                              a REAL git repository — the node's
#                                           cwd, because "repository-supplied
#                                           SKILL.md" is what one arm measures
#   <ws>/repo/.claude/agents/omg-probe-writer.md
#                                           agentmap's match target, and what
#                                           --agent resolves at run time
#   <ws>/repo/.claude/skills/               phase-dependent; see phase.sh
#   <ws>/repo/.claude/settings.json         phase-dependent; where a PROJECT-scope
#                                           plugin is declared (phase B)
#   <ws>/skills-src/                        what the coordinator's scan stages
#   <ws>/user-mkt/                          a local marketplace holding the
#                                           colliding "user" plugin
#   <ws>/argv/<node-id>/                    runner.buildArgs' output, per node
#
# Nothing under ~/.claude is written, read-modify-written or removed by this
# probe. The colliding plugin is declared at PROJECT scope, which an
# agent-mapped node's nil layer 1 loads exactly as it loads the user's own.
set -eu
WS="${1:-/tmp/omg-lift-exclusion}"
HERE="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$HERE/../../../.." && pwd)"
. "$HERE/skills.sh"

rm -rf "$WS"
mkdir -p "$WS/repo/.claude/agents" "$WS/repo/.claude/skills" "$WS/skills-src" "$WS/logs" "$WS/argv"

git -C "$WS/repo" init -q
git -C "$WS/repo" config user.email probe@example.invalid
git -C "$WS/repo" config user.name "omg probe"

cat >"$WS/repo/.claude/agents/omg-probe-writer.md" <<'EOF'
---
name: omg-probe-writer
description: Writes design notes and documents out as files. Use for turning a note into a rendered document.
---

You are a documentation writer. Produce the artifact you are asked for, in the
current working directory. Follow any procedure you are told to follow.
EOF

# No `tools:` frontmatter on purpose: toolsBeyondCeiling then returns nil, so
# the agent is MAPPABLE and inherits the node's own ceilinged tool set. An
# agent that declared its own tools would be a second variable.

plant_staged "$WS/skills-src"

# The "user" plugin, in a local marketplace. Two skills: one whose NAME
# collides with the staged corpus, one unique — the unique one is the load
# control, without which a collision result cannot be told from "the plugin
# never loaded".
mkdir -p "$WS/user-mkt/.claude-plugin" "$WS/user-mkt/omg-probe-user-plugin/.claude-plugin"
cat >"$WS/user-mkt/.claude-plugin/marketplace.json" <<'EOF'
{
  "name": "omg-probe-user-mkt",
  "owner": { "name": "omg probe" },
  "plugins": [
    {
      "name": "omg-probe-user-plugin",
      "source": "./omg-probe-user-plugin",
      "description": "Probe stand-in for one of the user's own installed plugins."
    }
  ]
}
EOF
cat >"$WS/user-mkt/omg-probe-user-plugin/.claude-plugin/plugin.json" <<'EOF'
{
  "name": "omg-probe-user-plugin",
  "version": "0.0.1",
  "description": "Probe stand-in for one of the user's own installed plugins."
}
EOF
plant_uplugin_collide "$WS/user-mkt/omg-probe-user-plugin/skills"
plant_uplugin_only "$WS/user-mkt/omg-probe-user-plugin/skills"

git -C "$WS/repo" add -A
git -C "$WS/repo" commit -qm "probe fixture repository"

chmod +x "$HERE/shim.sh"
cd "$REPO_ROOT"
go run "$HERE/_harness/main.go" "$WS" "$HERE/shim.sh" >"$WS/plan-report.json"
cat "$WS/plan-report.json"
