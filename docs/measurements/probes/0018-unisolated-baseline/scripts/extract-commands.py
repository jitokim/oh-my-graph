#!/usr/bin/env python3
"""Pull every shell command a node actually ran out of its session transcript.

The transcript is the evidence for ADR 0018's baseline; a node's own summary of
what it did is not. This prints, in order, each `Bash` tool_use's command with
the session cwd the record carries and a *tracked* directory that follows bare
`cd` calls (Claude Code's Bash tool keeps one shell, so a `cd` persists across
calls) and one-shot `cd X && ...` prefixes (which do not).

    extract-commands.py <transcript.jsonl> [--git-only]
"""
import json
import re
import sys

GIT_ONLY = "--git-only" in sys.argv[1:]
path = [a for a in sys.argv[1:] if not a.startswith("--")][0]

CD_ONLY = re.compile(r"^\s*cd\s+(?P<dir>'[^']*'|\"[^\"]*\"|\S+)\s*$")
CD_PREFIX = re.compile(r"^\s*cd\s+(?P<dir>'[^']*'|\"[^\"]*\"|\S+)\s*&&\s*(?P<rest>.*)$", re.S)


def unquote(text):
    if len(text) >= 2 and text[0] == text[-1] and text[0] in "'\"":
        return text[1:-1]
    return text


tracked = None
index = 0
for line in open(path, encoding="utf-8"):
    line = line.strip()
    if not line:
        continue
    try:
        record = json.loads(line)
    except json.JSONDecodeError:
        continue
    if tracked is None and record.get("cwd"):
        tracked = record["cwd"]
    message = record.get("message") or {}
    content = message.get("content")
    if not isinstance(content, list):
        continue
    for block in content:
        if not isinstance(block, dict) or block.get("type") != "tool_use":
            continue
        name = block.get("name", "")
        params = block.get("input") or {}
        if name != "Bash":
            continue
        command = (params.get("command") or "").strip()
        index += 1
        here = tracked
        # A `cd` on its own line changes the one shell the Bash tool keeps, so
        # it persists into later calls. A multi-line command may hold more than
        # one, and it is the LAST of them the next call inherits — so `tracked`
        # follows every line, while `here` (what THIS command is attributed to)
        # still moves only for a `cd` that opens the command. A `cd X && …`
        # prefix does not persist.
        lines = command.split("\n")
        opening = CD_ONLY.match(lines[0]) or CD_PREFIX.match(command)
        if opening:
            here = unquote(opening.group("dir"))
        for command_line in lines:
            match = CD_ONLY.match(command_line)
            if match:
                tracked = unquote(match.group("dir"))
        if GIT_ONLY and "git" not in command:
            continue
        print(f"--- #{index}  [dir: {here}]")
        print(command)
        print()
