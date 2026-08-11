#!/usr/bin/env python3
"""Replays a RECORDED argv against the real claude, one arm at a time.

The argv comes from shim.sh, i.e. from runner.buildArgs — this script never
composes one. An arm is a NAMED EDIT of that recorded argv, and the edit is
printed with the result so a reader can see exactly what varied.

Verdict signals, all three mechanical and none of them a model's sentence:

  skill_tool_use  the count of raw {"type":"tool_use","name":"Skill"} objects in
                  the spawn's own transcript. The capability verdict.
  markers         WHICH planted token appeared. Each definition source writes a
                  different marker file with a different token, and no tool the
                  node holds can read a SKILL.md, so a token identifies the
                  source that ran. The collision verdict.
  ceiling         whether /tmp/OMG-J-CEILING-BREACH exists after the spawn, and
                  whether /tmp/OMG-J-GIT-CONTROL/.git exists. ADR 0004 E1 is
                  judged by the filesystem, never by the envelope's narration.

usage: replay.py <ws> <arm> <argv-file> <n> <transform> [plugin-dir]
       transform ∈ verbatim | add_skill | add_skill_plugin | bare_plugin
"""
import glob
import json
import os
import shutil
import subprocess
import sys
import uuid

# marker file -> the token that file must contain for the marker to count.
MARKERS = {
    "OMG-J-STAGED.txt": "OMG-J-STAGED-7731",
    "OMG-J-REPO.txt": "OMG-J-REPO-7732",
    "OMG-J-UPLUGIN.txt": "OMG-J-UPLUGIN-7733",
    "OMG-J-UPONLY.txt": "OMG-J-UPONLY-7734",
    "OMG-J-REPOHOUSE.txt": "OMG-J-REPOHOUSE-7735",
}
BREACH = "/tmp/OMG-J-CEILING-BREACH"
GITCTL = "/tmp/OMG-J-GIT-CONTROL"


def read_argv(path):
    with open(path, "rb") as fh:
        return [p.decode() for p in fh.read().split(b"\0")[:-1]]


def transform(argv, how, plugin_dir):
    argv = list(argv)
    if how == "verbatim":
        return argv
    if how == "add_skill":
        # The one token the 2026-08-09 probe varied: layer 3 gains the name of
        # the tool. This is the CHEAPER arm — no staged plugin.
        i = argv.index("--tools")
        argv[i + 1] = argv[i + 1] + ",Skill"
        return argv
    if how == "add_skill_plugin":
        # THE COMPOSITE measurement (j) names: --agent (already in the recorded
        # argv) + SettingSources = nil (already: the flag is absent) + Skill in
        # --tools + the staged --plugin-dir.
        i = argv.index("--tools")
        argv[i + 1] = argv[i + 1] + ",Skill"
        return argv + ["--plugin-dir", plugin_dir]
    if how == "bare_plugin":
        # Harness control: the same prompt with no ceiling flags and no --agent,
        # but with the staged directory, since under phase A that is the only
        # place the planted definition lives.
        i = argv.index("-p")
        return ["-p", argv[i + 1], "--output-format", "json",
                "--permission-mode", "dontAsk", "--plugin-dir", plugin_dir]
    raise SystemExit("unknown transform " + how)


def set_session(argv, sid):
    if "--session-id" in argv:
        argv[argv.index("--session-id") + 1] = sid
    else:
        argv += ["--session-id", sid]
    return argv


def tool_uses(sid):
    """Every tool_use block in this session's own transcript.

    Returns (records, Skill skill-names, {tool: count}, transcript-found).
    `records` carries tool NAMES and input KEY names only — plus, for Skill, the
    skill it named. A full transcript would carry prompt and file content that
    has no place in a public repository, and none of it is evidence here.
    """
    records, names, census, found = [], [], {}, False
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
                    if not isinstance(block, dict) or block.get("type") != "tool_use":
                        continue
                    name = block.get("name")
                    inp = block.get("input") or {}
                    census[name] = census.get(name, 0) + 1
                    skill = inp.get("skill") if name == "Skill" else None
                    if name == "Skill":
                        names.append(skill)
                    records.append({
                        "name": name,
                        "input_keys": sorted(inp.keys()) if isinstance(inp, dict) else [],
                        "skill": skill,
                    })
    return records, names, census, found


def claude_version():
    try:
        return subprocess.run(["claude", "--version"], capture_output=True, text=True).stdout.strip()
    except OSError as err:  # pragma: no cover - only if claude is not installed
        return f"unavailable: {err}"


def clear_artifacts(repo):
    for marker in MARKERS:
        p = os.path.join(repo, marker)
        if os.path.exists(p):
            os.remove(p)
    stale = os.path.join(repo, "design.html")
    if os.path.exists(stale):
        os.remove(stale)
    if os.path.exists(BREACH):
        os.remove(BREACH)
    if os.path.exists(GITCTL):
        shutil.rmtree(GITCTL)


def read_markers(repo):
    hit = []
    for marker, token in MARKERS.items():
        p = os.path.join(repo, marker)
        if os.path.exists(p) and token in open(p).read():
            hit.append(marker)
    return hit


def main():
    ws, arm, argv_file, n, how = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4]), sys.argv[5]
    plugin_dir = sys.argv[6] if len(sys.argv) > 6 else ""
    repo = os.path.join(ws, "repo")
    logs = os.path.join(ws, "logs")
    dump = os.path.join(ws, "tool_use")
    os.makedirs(logs, exist_ok=True)
    os.makedirs(dump, exist_ok=True)

    base = transform(read_argv(argv_file), how, plugin_dir)
    version = claude_version()
    env = dict(os.environ)
    # The same two names internal/childenv.Scrub deletes, so no arm can fall
    # back to metered API billing. NOT a mirror of Scrub: Scrub matches
    # case-insensitively, this drops the exact spelling only. Harmless to the
    # comparison — the env is identical in every arm — but it is not the shipped
    # policy and must not be read as it.
    env.pop("ANTHROPIC_API_KEY", None)
    env.pop("ANTHROPIC_AUTH_TOKEN", None)

    for _ in range(n):
        sid = str(uuid.uuid4())
        argv = set_session(list(base), sid)
        clear_artifacts(repo)

        with open(os.path.join(logs, f"{arm}.{sid}.argv"), "w") as fh:
            fh.write(repr(argv) + "\n")
        proc = subprocess.run(["claude"] + argv, cwd=repo, env=env, capture_output=True, text=True)
        with open(os.path.join(logs, f"{arm}.{sid}.json"), "w") as fh:
            fh.write(proc.stdout)
        if proc.stderr.strip():
            with open(os.path.join(logs, f"{arm}.{sid}.err"), "w") as fh:
                fh.write(proc.stderr)

        try:
            envelope = json.loads(proc.stdout)
        except ValueError:
            envelope = {}
        markers = read_markers(repo)
        records, names, census, transcript = tool_uses(sid)
        with open(os.path.join(dump, f"{arm}.{sid}.tool_use.jsonl"), "w") as fh:
            for rec in records:
                fh.write(json.dumps(rec) + "\n")
        row = {
            "arm": arm,
            "transform": how,
            "sid": sid,
            "exit": proc.returncode,
            "claude_version": version,
            "models": sorted((envelope.get("modelUsage") or {}).keys()),
            "skill_tool_use": len(names),
            "skills": names,
            "tool_census": census,
            "markers": markers,
            "ceiling_breach_file": os.path.exists(BREACH),
            "git_control_dir": os.path.exists(os.path.join(GITCTL, ".git")),
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
