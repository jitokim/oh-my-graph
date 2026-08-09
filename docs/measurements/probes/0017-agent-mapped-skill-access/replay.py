#!/usr/bin/env python3
"""Replays a RECORDED argv against the real claude, one arm at a time.

The argv comes from shim.sh, i.e. from runner.buildArgs — this script never
composes one. An arm is a named single-token edit of that recorded argv, and
the edit is printed with the result so a reader can see exactly what varied.

Verdict signal: the count of raw {"type":"tool_use","name":"Skill"} objects in
the spawn's own session transcript. Corroborating signal: the planted skill's
marker file, whose token appears nowhere the node's tool set can read.
A model's sentence about itself is stored and never counted.

usage: replay.py <ws> <arm> <argv-file> <n> <transform>
       transform ∈ verbatim | add_skill | bare
"""
import glob
import json
import os
import subprocess
import sys
import uuid

MARKER = "OMG-PROBE-AGENTMAP-FIRED.txt"
TOKEN = "OMG-AGENTMAP-4417-ZK"


def read_argv(path):
    with open(path, "rb") as fh:
        return [p.decode() for p in fh.read().split(b"\0")[:-1]]


def transform(argv, how):
    argv = list(argv)
    if how == "verbatim":
        return argv
    if how == "add_skill":
        # The ONE token this probe varies: layer 3 gains the name of the tool.
        i = argv.index("--tools")
        argv[i + 1] = argv[i + 1] + ",Skill"
        return argv
    if how == "bare":
        # Harness control: the same prompt with no ceiling flags and no --agent.
        i = argv.index("-p")
        return ["-p", argv[i + 1], "--output-format", "json", "--permission-mode", "dontAsk"]
    raise SystemExit("unknown transform " + how)


def set_session(argv, sid):
    if "--session-id" in argv:
        argv[argv.index("--session-id") + 1] = sid
    else:
        argv += ["--session-id", sid]
    return argv


def skill_calls(sid):
    """Every Skill tool_use in this session's own transcript, by skill name."""
    names, found = [], False
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
                    if isinstance(block, dict) and block.get("type") == "tool_use" and block.get("name") == "Skill":
                        names.append(block.get("input", {}).get("skill"))
    return names, found


def main():
    ws, arm, argv_file, n, how = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4]), sys.argv[5]
    work = os.path.join(ws, "work")
    logs = os.path.join(ws, "logs")
    os.makedirs(logs, exist_ok=True)

    base = transform(read_argv(argv_file), how)
    env = dict(os.environ)
    # internal/childenv.Scrub's policy, at the probe layer.
    env.pop("ANTHROPIC_API_KEY", None)
    env.pop("ANTHROPIC_AUTH_TOKEN", None)

    for _ in range(n):
        sid = str(uuid.uuid4())
        argv = set_session(list(base), sid)
        marker = os.path.join(work, MARKER)
        for stale in (marker, os.path.join(work, "design.html")):
            if os.path.exists(stale):
                os.remove(stale)

        with open(os.path.join(logs, f"{arm}.{sid}.argv"), "w") as fh:
            fh.write(repr(argv) + "\n")
        proc = subprocess.run(["claude"] + argv, cwd=work, env=env, capture_output=True, text=True)
        with open(os.path.join(logs, f"{arm}.{sid}.json"), "w") as fh:
            fh.write(proc.stdout)
        if proc.stderr.strip():
            with open(os.path.join(logs, f"{arm}.{sid}.err"), "w") as fh:
                fh.write(proc.stderr)

        try:
            envelope = json.loads(proc.stdout)
        except ValueError:
            envelope = {}
        marker_ok = os.path.exists(marker) and TOKEN in open(marker).read()
        names, transcript = skill_calls(sid)
        row = {
            "arm": arm,
            "transform": how,
            "sid": sid,
            "exit": proc.returncode,
            "skill_tool_use": len(names),
            "skills": names,
            "marker": marker_ok,
            "transcript_found": transcript,
            "cost_usd": envelope.get("total_cost_usd"),
            "permission_denials": envelope.get("permission_denials"),
            "num_turns": envelope.get("num_turns"),
            "is_error": envelope.get("is_error"),
            "result_head": (envelope.get("result") or "")[:220],
        }
        print(json.dumps(row))
        with open(os.path.join(logs, "results.jsonl"), "a") as fh:
            fh.write(json.dumps(row) + "\n")


if __name__ == "__main__":
    main()
