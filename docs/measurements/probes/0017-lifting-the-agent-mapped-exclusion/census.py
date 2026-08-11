#!/usr/bin/env python3
"""Re-derives the write-up's per-spawn table from the COMMITTED records.

Two sources, printed side by side so the committed snapshot can be checked
against its origin rather than trusted:

  tool_use/<arm>.<sid>.tool_use.jsonl   committed here — tool names, input KEY
                                        names, and the skill a Skill call named
  ~/.claude/projects/**/<sid>.jsonl     the transcript it was extracted from,
                                        for as long as that directory keeps it

A session whose transcript has aged out prints `gone`; the committed record is
still there, which is the point of committing it. A disagreement between the
two prints as `MISMATCH` and is a finding, not a rendering detail.

usage: census.py [results.jsonl]
"""
import glob
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))


def census_from_transcript(sid):
    census, found = {}, False
    for path in glob.glob(os.path.expanduser(f"~/.claude/projects/*/{sid}.jsonl")):
        found = True
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
                    if isinstance(block, dict) and block.get("type") == "tool_use":
                        name = block.get("name")
                        census[name] = census.get(name, 0) + 1
    return census, found


def census_from_committed(arm, sid):
    path = os.path.join(HERE, "tool_use", f"{arm}.{sid}.tool_use.jsonl")
    census, skills = {}, []
    if not os.path.exists(path):
        return None, []
    with open(path) as fh:
        for line in fh:
            rec = json.loads(line)
            census[rec["name"]] = census.get(rec["name"], 0) + 1
            if rec["name"] == "Skill":
                skills.append(rec.get("skill"))
    return census, skills


def main():
    path = sys.argv[1] if len(sys.argv) > 1 else os.path.join(HERE, "results.jsonl")
    with open(path) as fh:
        rows = [json.loads(line) for line in fh if line.strip()]
    for row in rows:
        arm, sid = row["arm"], row["sid"]
        committed, skills = census_from_committed(arm, sid)
        live, found = census_from_transcript(sid)
        if not found:
            state = "gone"
        elif live == committed:
            state = "transcript"
        else:
            state = "MISMATCH"
        breach = "BREACH" if row["ceiling_breach_file"] else ("git-ok" if row["git_control_dir"] else "-")
        print(f"{arm:<6}{sid[:8]}  {str(committed):<28}{state:<11}{breach:<8}"
              f"{','.join(row['markers']) or '-':<24}{' '.join(skills)}")


if __name__ == "__main__":
    main()
