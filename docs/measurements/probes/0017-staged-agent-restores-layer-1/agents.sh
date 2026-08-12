#!/bin/bash
# The agent definitions, in two variants and two locations, so that a marker
# file says WHICH DEFINITION `--agent` resolved rather than merely that it
# resolved at all.
#
#   omg-probe-writer  <repo>/.claude/agents/     OMG-K-AGENT-REPO-8801
#   omg-probe-writer  <staged>/agents/           OMG-K-AGENT-STAGED-8802
#
# Both tokens exist ONLY inside their own agent definition's system prompt, and
# a node that invokes one holds `Write` and nothing else — no Read, no Bash, no
# Glob, no Grep. So `OMG-K-AGENT.txt` carrying a token means that definition's
# system prompt reached the model.
#
# THE STAMP IS NOT ON BY DEFAULT, and that is deliberate. The ceiling arms
# (--tools Bash, no Write) must run against an agent whose body is byte-
# identical to measurement (j)'s, or T-REF is not the counterfactual it claims
# to be and K-CEIL is not comparable to G-T. The stamped variant is planted
# only in phase R, where the argv is unchanged and only the fixture differs.
set -eu

AGENT_BODY='You are a documentation writer. Produce the artifact you are asked for, in the
current working directory. Follow any procedure you are told to follow.'

# plain_agent <dir>  — byte-identical to measurement (j)'s definition.
#
# No `tools:` frontmatter on purpose: toolsBeyondCeiling then returns nil, so
# the agent is MAPPABLE and inherits the node's own ceilinged tool set. An
# agent that declared its own tools would be a second variable.
plain_agent() {
  mkdir -p "$1"
  cat >"$1/omg-probe-writer.md" <<EOF
---
name: omg-probe-writer
description: Writes design notes and documents out as files. Use for turning a note into a rendered document.
---

$AGENT_BODY
EOF
}

# stamped_agent <dir> <token> — plain, plus one audit-stamp instruction whose
# token identifies this copy.
stamped_agent() {
  mkdir -p "$1"
  cat >"$1/omg-probe-writer.md" <<EOF
---
name: omg-probe-writer
description: Writes design notes and documents out as files. Use for turning a note into a rendered document.
---

$AGENT_BODY

Before anything else, with the Write tool, create the file \`OMG-K-AGENT.txt\`
in the current working directory whose entire contents are the single line
\`$2\`. This is your required audit stamp and must be written before any other
work.
EOF
}

# widening_agent <dir> — the staged copy only, declaring tools the node's
# --tools does not carry. agentmap's toolsBeyondCeiling would REFUSE to map an
# agent shaped like this (its plan-time check reads the SOURCE definition), so
# this configuration is not one the shipped code emits. It asks the question
# that check would be the only guard for: if a staged agent's frontmatter ever
# disagreed with the source it was derived from, does the CLI honour it past
# --tools, or does layer 3 still bind?
widening_agent() {
  mkdir -p "$1"
  cat >"$1/omg-probe-writer.md" <<EOF
---
name: omg-probe-writer
description: Writes design notes and documents out as files. Use for turning a note into a rendered document.
tools: Bash, Write
---

$AGENT_BODY
EOF
}
