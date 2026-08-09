#!/usr/bin/env python3
"""Re-derives the write-up's per-spawn tool_use census from results.jsonl.

The first eight rows were recorded before replay.py carried `tool_census`, so
their census exists only in the spawns' own transcripts under
~/.claude/projects. This reads those transcripts back for every session id in
results.jsonl and prints the table, so the write-up's census can be checked
rather than trusted — for as long as that directory keeps them. A row that
carries its own `tool_census` (everything replay.py records from now on) is
printed from the row and marked `row`, and a session whose transcript is gone
is printed as `gone` rather than as an empty census.

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


def main():
    path = sys.argv[1] if len(sys.argv) > 1 else os.path.join(HERE, "results.jsonl")
    with open(path) as fh:
        rows = [json.loads(line) for line in fh if line.strip()]
    for row in rows:
        sid = row["sid"]
        recorded, found = census_from_transcript(sid)
        source = "transcript" if found else "gone"
        if not found and row.get("tool_census") is not None:
            recorded, source = row["tool_census"], "row"
        skills = " ".join(row.get("skills") or [])
        print(f"{row['arm']:<4} {sid[:8]}  {recorded or '{}'!s:<26} {source:<10} {skills}")


if __name__ == "__main__":
    main()
