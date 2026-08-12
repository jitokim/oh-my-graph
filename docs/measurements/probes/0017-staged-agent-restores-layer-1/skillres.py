#!/usr/bin/env python3
"""Derives, per spawn, WHAT EACH `Skill` CALL RETURNED.

The `tool_use` dump answers "was the tool called, and what name did it name".
It cannot answer "did that definition exist", and arms K-UPONLY and K-REPO-N
turn on exactly that difference: the model NAMED a repository-supplied skill
and the CLI answered `Unknown skill: <name>`. A call that errored is not a
definition that reached the model.

Only two CLI-generated strings are recorded — `Unknown skill: ...` and
`Launching skill: ...` — plus the is_error flag. Neither carries prompt text,
file content or anything the user wrote, which is the same bound the tool_use
dump keeps.

usage: skillres.py <results.jsonl> [out.jsonl]
"""
import glob
import json
import os
import sys


def outcomes(sid):
    """Pair each Skill tool_use with the tool_result that answered it."""
    pending, rows = {}, []
    for path in sorted(glob.glob(os.path.expanduser(f"~/.claude/projects/*/{sid}.jsonl"))):
        with open(path) as fh:
            for line in fh:
                try:
                    obj = json.loads(line)
                except ValueError:
                    continue
                content = obj.get("message", {}).get("content")
                if not isinstance(content, list):
                    continue
                for block in content:
                    if not isinstance(block, dict):
                        continue
                    if block.get("type") == "tool_use" and block.get("name") == "Skill":
                        pending[block.get("id")] = (block.get("input") or {}).get("skill")
                    elif block.get("type") == "tool_result" and block.get("tool_use_id") in pending:
                        named = pending.pop(block.get("tool_use_id"))
                        text = block.get("content")
                        if not isinstance(text, str):
                            text = json.dumps(text)
                        verdict = "other"
                        for known in ("Unknown skill:", "Launching skill:"):
                            if known in text:
                                verdict = known.rstrip(":")
                                break
                        rows.append({
                            "skill": named,
                            "is_error": bool(block.get("is_error")),
                            "result": verdict,
                        })
    for named in pending.values():
        rows.append({"skill": named, "is_error": None, "result": "no-result-recorded"})
    return rows


def main():
    if len(sys.argv) not in (2, 3):
        sys.exit(__doc__)
    src = sys.argv[1]
    out = sys.argv[2] if len(sys.argv) > 2 else None
    lines = []
    for line in open(src):
        if not line.strip():
            continue
        row = json.loads(line)
        rec = {"arm": row["arm"], "sid": row["sid"], "skill_calls": outcomes(row["sid"])}
        lines.append(json.dumps(rec))
        print(f"{rec['arm']:<12}{rec['sid'][:8]}  " +
              "; ".join(f"{c['skill']} -> {c['result']}" + (" ERROR" if c["is_error"] else "")
                        for c in rec["skill_calls"]) or "-")
    if out:
        with open(out, "w") as fh:
            fh.write("\n".join(lines) + "\n")


if __name__ == "__main__":
    main()
