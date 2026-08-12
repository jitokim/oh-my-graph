#!/bin/bash
# Builds THE CANDIDATE plugin directory: a copy of what SkillStaging really
# materialized, plus `agents/`.
#
#   <ws>/run/skills-plugin/            written by SkillStaging.BindTo — the
#                                      shipped corpus, untouched here
#   <ws>/run/skills-plugin-agents/     the copy + agents/<matched agent>.md
#
# WHY A COPY AND NOT AN EDIT OF THE REAL ONE. `SkillStaging.Materialize` runs
# before EVERY node spawn and DELETES every path its manifest does not name
# (ADR 0017 §5). An `agents/` directory dropped into the staged dir would
# therefore be pruned by the shipped code. That is a real implementation
# consequence of the candidate — a fix would have to teach the manifest about
# agent files — and it is recorded here rather than worked around silently.
#
# usage: stage.sh <ws> plain|stamped|widening [manifest]
#        `manifest` additionally declares the agent file in plugin.json, for
#        the conditional K-RES-MAN arm: auto-discovery of `agents/` is an
#        assumption, not a contract.
set -eu
WS="${1:?usage: stage.sh <ws> plain|stamped|widening [manifest]}"
VARIANT="${2:-plain}"
DECLARE="${3:-}"
HERE="$(cd "$(dirname "$0")" && pwd)"
. "$HERE/agents.sh"

SRC="$WS/run/skills-plugin"
DST="$WS/run/skills-plugin-agents"
[ -d "$SRC" ] || { echo "stage.sh: $SRC does not exist — run setup.sh first" >&2; exit 2; }

rm -rf "$DST"
mkdir -p "$DST"
cp -R "$SRC/." "$DST/"

case "$VARIANT" in
  plain)    plain_agent   "$DST/agents" ;;
  stamped)  stamped_agent "$DST/agents" OMG-K-AGENT-STAGED-8802 ;;
  widening) widening_agent "$DST/agents" ;;
  *) echo "stage.sh: unknown variant $VARIANT" >&2; exit 2 ;;
esac

if [ "$DECLARE" = manifest ]; then
  cat >"$DST/.claude-plugin/plugin.json" <<'EOF'
{
  "name": "oh-my-graph-staged-skills",
  "version": "0.0.0",
  "description": "Skill definitions oh-my-graph staged for this run's planned nodes (ADR 0017).",
  "agents": ["./agents/omg-probe-writer.md"]
}
EOF
fi

echo "staged plugin dir: $DST (agent variant: $VARIANT${DECLARE:+, $DECLARE})"
find "$DST" -type f | sort
