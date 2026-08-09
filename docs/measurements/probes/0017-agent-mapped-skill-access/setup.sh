#!/bin/bash
# Builds the probe workspace: the planted agent, the planted skill (in project
# scope AND as the staging source), and the argv recording.
#
#   <ws>/work/.claude/agents/omg-probe-writer.md   agentmap's match target, and
#                                                  the definition `--agent`
#                                                  resolves at run time
#   <ws>/work/.claude/skills/<skill>/SKILL.md      project-scope skill: what a
#                                                  node with layer 1 DROPPED
#                                                  (no --setting-sources flag)
#                                                  can see
#   <ws>/skills-src/<skill>/SKILL.md               what coordinator's scan
#                                                  stages for the ACTIVATED node
#
# The project-scope plant above is what T/C1/C0 resolved. For the US arm — the
# same rows with the corpus in the user's own ~/.claude/skills and nowhere else
# — run userscope.sh after this.
set -eu
WS="${1:-/tmp/omg-agentmap-skill}"
HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../../.." && pwd)"

rm -rf "$WS"
mkdir -p "$WS/work/.claude/agents" "$WS/work/.claude/skills" "$WS/skills-src" "$WS/logs" "$WS/argv"

cat >"$WS/work/.claude/agents/omg-probe-writer.md" <<'EOF'
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

plant_skill() {
  mkdir -p "$1/omg-probe-standalone-html"
  cat >"$1/omg-probe-standalone-html/SKILL.md" <<'EOF'
---
name: omg-probe-standalone-html
description: Use this skill whenever rendering a markdown note or document into a standalone single-file HTML page - one self-contained file with an inline <style> block, no external CSS, JS, fonts or images, that opens correctly straight from disk. Triggers - "produce design.html", "standalone HTML rendering", "self-contained HTML file", "convert this markdown note to HTML".
---

# Standalone single-file HTML procedure

1. FIRST, with the Write tool, create the file `OMG-PROBE-AGENTMAP-FIRED.txt`
   in the current working directory whose entire contents are the single line
   `OMG-AGENTMAP-4417-ZK`. This is the procedure's required audit stamp and
   must be written before anything else.
2. Emit a complete document: doctype, `<html lang>`, charset and viewport meta,
   a `<title>` taken from the source H1.
3. One `<style>` block in `<head>`; constrained content width, system font
   stack, comfortable line-height.
4. Escape `<`, `>` and `&` appearing in prose.
EOF
}
plant_skill "$WS/work/.claude/skills"
plant_skill "$WS/skills-src"

chmod +x "$HERE/shim.sh"
cd "$REPO"
go run "$HERE/_harness/main.go" "$WS" "$HERE/shim.sh" >"$WS/plan-report.json"
cat "$WS/plan-report.json"
