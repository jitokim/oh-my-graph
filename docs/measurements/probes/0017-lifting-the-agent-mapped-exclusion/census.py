#!/usr/bin/env python3
"""Re-derives the write-up's per-spawn table from the COMMITTED records.

Two sources, printed side by side so the committed snapshot can be checked
against its origin rather than trusted:

  tool_use/<arm>.<sid>.tool_use.jsonl   committed here — tool names, input KEY
                                        names, and the skill a Skill call named
  ~/.claude/projects/**/<sid>.jsonl     the transcript it was extracted from,
                                        for as long as that directory keeps it

A session whose transcript has aged out prints `gone`; the committed record is
still there, which is the point of committing it. `missing-committed` is the
other way round — a results row with no committed record at all, which is a
hole in the evidence rather than an aged-out transcript, so the two do not
share a word. A disagreement between the two prints as `MISMATCH` and is a
finding, not a rendering detail.

The results file and the tool_use directory are one PAIR: a re-run writes both
under its own workspace (`$WS/logs/results.jsonl` and `$WS/tool_use`), so pass
them together or neither. Defaulting one and not the other would census a
re-run's rows against the committed dump and call every row a hole.

usage: census.py [results.jsonl [tool_use_dir]]
"""
import glob
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))

# The session id replay.py emits, and nothing else. `sid` comes out of a
# results file and is interpolated into a glob and a path: a value like `*`
# would enumerate every session under ~/.claude/projects and aggregate
# unrelated local tool use into this table as if it were the probe's.
SID = re.compile(r"\A[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\Z", re.I)


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


def census_from_committed(dump, arm, sid):
    path = os.path.join(dump, f"{arm}.{sid}.tool_use.jsonl")
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
    dump = sys.argv[2] if len(sys.argv) > 2 else os.path.join(HERE, "tool_use")
    with open(path) as fh:
        rows = [json.loads(line) for line in fh if line.strip()]
    bad = 0
    for row in rows:
        arm, sid = row["arm"], row["sid"]
        if not SID.match(sid):
            bad += 1
            print(f"{arm:<6}{sid[:8]!r:<12}BAD-SID  not the session id replay.py emits; row skipped")
            continue
        committed, skills = census_from_committed(dump, arm, sid)
        live, found = census_from_transcript(sid)
        if committed is None:
            state = "missing-committed"
        elif not found:
            state = "gone"
        elif live == committed:
            state = "transcript"
        else:
            state = "MISMATCH"
        breach = "BREACH" if row["ceiling_breach_file"] else ("git-ok" if row["git_control_dir"] else "-")
        print(f"{arm:<6}{sid[:8]}  {committed!s:<28}{state:<18}{breach:<8}"
              f"{','.join(row['markers']) or '-':<24}{' '.join(skills)}")
    if bad:
        # A row this script could not check is not a row it checked.
        sys.exit(f"census: {bad} row(s) carried an unusable session id")


if __name__ == "__main__":
    main()
