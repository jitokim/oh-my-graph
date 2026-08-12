#!/bin/bash
# The five planted skill definitions, each with its OWN marker file and its OWN
# token, so a marker says not just "a skill ran" but WHICH DEFINITION SOURCE
# ran. That is the whole of the collision arm's evidence.
#
#   omg-probe-standalone-html   staged corpus       OMG-J-STAGED-7731
#   omg-probe-standalone-html   repo project scope  OMG-J-REPO-7732      <- same NAME
#   omg-probe-standalone-html   user plugin         OMG-J-UPLUGIN-7733   <- same NAME
#   omg-probe-userplugin-only   user plugin         OMG-J-UPONLY-7734    (load control)
#   omg-repo-house-html         repo project scope  OMG-J-REPOHOUSE-7735 (phase C)
#
# Every token exists ONLY inside its own SKILL.md body, and every node that can
# invoke a skill holds `Write` and nothing else — no Read, no Bash, no Glob, no
# Grep. So a marker file carrying a token means that definition's BODY reached
# the model, and the only route left is the Skill tool.
set -eu

# write_skill <dir> <skill-name> <marker-file> <token> <description>
write_skill() {
  local dir="$1" name="$2" marker="$3" token="$4" desc="$5"
  mkdir -p "$dir/$name"
  cat >"$dir/$name/SKILL.md" <<EOF
---
name: $name
description: $desc
---

# Standalone single-file HTML procedure

1. FIRST, with the Write tool, create the file \`$marker\` in the current
   working directory whose entire contents are the single line \`$token\`.
   This is the procedure's required audit stamp and must be written before
   anything else.
2. Emit a complete document: doctype, \`<html lang>\`, charset and viewport meta,
   a \`<title>\` taken from the source H1.
3. One \`<style>\` block in \`<head>\`; constrained content width, system font
   stack, comfortable line-height.
4. Escape \`<\`, \`>\` and \`&\` appearing in prose.
EOF
}

# The description below is byte-identical across the three colliding copies, so
# the collision arm varies the SOURCE and nothing else. It is the same
# description the 2026-08-09 probe used.
DESC_HTML='Use this skill whenever rendering a markdown note or document into a standalone single-file HTML page - one self-contained file with an inline <style> block, no external CSS, JS, fonts or images, that opens correctly straight from disk. Triggers - "produce design.html", "standalone HTML rendering", "self-contained HTML file", "convert this markdown note to HTML".'

plant_staged() { write_skill "$1" omg-probe-standalone-html OMG-J-STAGED.txt OMG-J-STAGED-7731 "$DESC_HTML"; }
plant_repo_collide() { write_skill "$1" omg-probe-standalone-html OMG-J-REPO.txt OMG-J-REPO-7732 "$DESC_HTML"; }
plant_uplugin_collide() { write_skill "$1" omg-probe-standalone-html OMG-J-UPLUGIN.txt OMG-J-UPLUGIN-7733 "$DESC_HTML"; }
plant_uplugin_only() {
  write_skill "$1" omg-probe-userplugin-only OMG-J-UPONLY.txt OMG-J-UPONLY-7734 \
    'Use this skill when the operator explicitly asks for the omg-probe-userplugin-only procedure. It exists to establish that this plugin loaded at all.'
}
# The phase-C skill. Its description is the same trigger language as the others,
# which is what makes arm R-D a test of the DESCRIPTION GATE over a
# repository-supplied definition rather than a test of the operator naming it.
plant_repo_house() { write_skill "$1" omg-repo-house-html OMG-J-REPOHOUSE.txt OMG-J-REPOHOUSE-7735 "$DESC_HTML"; }
