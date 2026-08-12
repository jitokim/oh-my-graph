#!/usr/bin/env python3
"""Replays a RECORDED argv against the real claude, one arm at a time.

The argv comes from shim.sh, i.e. from runner.buildArgs — this script never
composes one. An arm is a NAMED EDIT of that recorded argv, and the edit is
printed with the result so a reader can see exactly what varied.

Verdict signals, all mechanical and none of them a model's sentence:

  exit / stderr   an unresolvable `--agent` exits 1 having written nothing to
                  stdout (ADR 0004: it does NOT fall back to plain claude), so
                  the exit code is the first half of the resolution verdict.
  markers         WHICH planted token appeared. Each definition source — skill
                  AND agent — writes its own token, and no tool the node holds
                  can read a SKILL.md or an agent file, so a token identifies
                  the definition that ran. The collision and resolution verdict.
  skill_tool_use  the count of raw {"type":"tool_use","name":"Skill"} objects in
                  the spawn's own transcript. The capability verdict.
  ceiling         whether /tmp/OMG-K-CEILING-BREACH exists after the spawn, and
                  whether /tmp/OMG-K-GIT-CONTROL/.git exists. ADR 0004 E1 is
                  judged by the filesystem, never by the envelope's narration.

usage: replay.py <ws> <arm> <argv-file> <n> <transform> <plugin-dir> [agent-name]
       transform ∈ verbatim | candidate | candidate_skill

  <plugin-dir>  the directory --plugin-dir is given. Passing the candidate dir
                (skills-plugin-agents) and the shipped one (skills-plugin) is
                how arm K-NEG differs from K-RES: same transform, one carries
                agents/ and the other does not.
  [agent-name]  replaces the --agent VALUE, for the conditional namespaced arm.
"""
import glob
import json
import os
import shutil
import subprocess
import sys
import uuid

# (label, marker file, the token that file must contain for the marker to
# count). Two entries share a filename on purpose: the repo agent definition
# and the staged agent definition both stamp `OMG-K-AGENT.txt`, with different
# tokens, so the file says WHICH ONE resolved.
MARKERS = [
    ("STAGED-SKILL", "OMG-J-STAGED.txt", "OMG-J-STAGED-7731"),
    ("REPO-SKILL", "OMG-J-REPO.txt", "OMG-J-REPO-7732"),
    ("UPLUGIN-SKILL", "OMG-J-UPLUGIN.txt", "OMG-J-UPLUGIN-7733"),
    ("UPONLY-SKILL", "OMG-J-UPONLY.txt", "OMG-J-UPONLY-7734"),
    ("REPOHOUSE-SKILL", "OMG-J-REPOHOUSE.txt", "OMG-J-REPOHOUSE-7735"),
    ("AGENT-REPO", "OMG-K-AGENT.txt", "OMG-K-AGENT-REPO-8801"),
    ("AGENT-STAGED", "OMG-K-AGENT.txt", "OMG-K-AGENT-STAGED-8802"),
]
BREACH = "/tmp/OMG-K-CEILING-BREACH"
GITCTL = "/tmp/OMG-K-GIT-CONTROL"


def read_argv(path):
    with open(path, "rb") as fh:
        return [p.decode() for p in fh.read().split(b"\0")[:-1]]


def transform(argv, how, plugin_dir):
    argv = list(argv)
    if how == "verbatim":
        return argv
    if how in ("candidate", "candidate_skill"):
        # THE CANDIDATE. The shipped agent-mapped argv OMITS --setting-sources
        # entirely (applyAgentMapping sets SettingSources = nil), so layer 1 is
        # ADDED here rather than changed, and the plugin directory carrying the
        # matched agent is appended. Nothing else moves.
        if "--setting-sources" in argv:
            raise SystemExit("candidate: recorded argv already carries --setting-sources")
        if how == "candidate_skill":
            i = argv.index("--tools")
            argv[i + 1] = argv[i + 1] + ",Skill"
        return argv + ["--setting-sources", "", "--plugin-dir", plugin_dir]
    raise SystemExit("unknown transform " + how)


def set_agent(argv, name):
    if "--agent" not in argv:
        raise SystemExit("agent override: recorded argv carries no --agent")
    argv[argv.index("--agent") + 1] = name
    return argv


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


def marker_dirs(repo):
    """Where a marker may land.

    A node whose entire tool set is `Write` (plus `Skill`) holds no Read, no
    Bash, no Glob and no Grep, so it cannot determine its own cwd — measurement
    (j) caught one spawn writing its marker to $HOME on a guess, and this
    probe's first spawn wrote both of its files into `/tmp`. WHERE the file
    lands is a cwd-guessing artifact; WHETHER a token appears is the evidence,
    since a token exists only inside one SKILL.md or one agent definition and no
    tool the node holds can read either. So all three locations are cleared
    before a spawn and all three are searched after, and the location is
    recorded rather than assumed.

    `/tmp` was added after the first K-RES spawn, whose marker this function
    missed and which was recovered by hand; the registered n ran under the
    fixed version. The write-up says so rather than re-running it into
    agreement.
    """
    return [repo, os.path.expanduser("~"), "/tmp"]


def clear_artifacts(repo):
    names = {m[1] for m in MARKERS} | {"design.html"}
    for base in marker_dirs(repo):
        for name in names:
            p = os.path.join(base, name)
            if os.path.exists(p):
                os.remove(p)
    if os.path.exists(BREACH):
        os.remove(BREACH)
    if os.path.exists(GITCTL):
        shutil.rmtree(GITCTL)


def read_markers(repo):
    hit = []
    for base in marker_dirs(repo):
        for label, marker, token in MARKERS:
            p = os.path.join(base, marker)
            if os.path.exists(p) and token in open(p).read():
                hit.append(label if base == repo else f"{label}@HOME")
    return hit


def main():
    if len(sys.argv) not in (7, 8):
        sys.exit(__doc__)
    ws, arm, argv_file, n, how = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4]), sys.argv[5]
    plugin_dir = sys.argv[6]
    agent_name = sys.argv[7] if len(sys.argv) > 7 else ""
    repo = os.path.join(ws, "repo")
    logs = os.path.join(ws, "logs")
    dump = os.path.join(ws, "tool_use")
    os.makedirs(logs, exist_ok=True)
    os.makedirs(dump, exist_ok=True)

    base = transform(read_argv(argv_file), how, plugin_dir)
    if agent_name:
        base = set_agent(base, agent_name)
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
            "plugin_dir": os.path.basename(plugin_dir),
            "agent_override": agent_name,
            "sid": sid,
            "exit": proc.returncode,
            "stderr_head": proc.stderr.strip()[:220],
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
