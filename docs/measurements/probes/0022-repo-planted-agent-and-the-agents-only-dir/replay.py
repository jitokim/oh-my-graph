#!/usr/bin/env python3
"""Replays a RECORDED argv against the real claude, one arm at a time.

The argv comes from shim.sh, i.e. from runner.buildArgs — this script never
composes one. ADR 0022 has SHIPPED, so the default transform is `verbatim`:
the thing under measurement is the argv this build really emits, not an edit of
it. The one edit, `v060`, is a named counterfactual and prints as one.

Verdict signals, all mechanical and none of them a model's sentence:

  exit / stderr   an unresolvable `--agent` exits 1 having written nothing to
                  stdout (ADR 0004: it does NOT fall back to plain claude), so
                  the exit code is the resolution verdict's first half.
  markers         WHICH planted token appeared. The repository copy and the
                  user copy of the SAME agent name carry different tokens, and
                  the node that stamps one holds `Write` and nothing else, so a
                  token identifies the definition whose system prompt ran.
  ceiling         whether /tmp/OMG-L-CEILING-BREACH exists after the spawn, and
                  whether /tmp/OMG-L-GIT-CONTROL/.git exists. ADR 0004 E1 is
                  judged by the filesystem, never by the envelope's narration.

usage: replay.py <ws> <arm> <argv-file> <n> [verbatim|v060]
"""
import glob
import json
import os
import shutil
import subprocess
import sys
import uuid

# (label, marker file, the token that file must contain for the marker to
# count). Both entries share a filename on purpose: the two definitions of the
# one agent name stamp `OMG-L-AGENT.txt` with different tokens, so the file says
# WHICH ONE resolved.
MARKERS = [
    ("AGENT-REPO", "OMG-L-AGENT.txt", "OMG-L-AGENT-REPO-9101"),
    ("AGENT-USER", "OMG-L-AGENT.txt", "OMG-L-AGENT-USER-9102"),
]
BREACH = "/tmp/OMG-L-CEILING-BREACH"
GITCTL = "/tmp/OMG-L-GIT-CONTROL"


def read_argv(path):
    with open(path, "rb") as fh:
        return [p.decode() for p in fh.read().split(b"\0")[:-1]]


def transform(argv, how):
    argv = list(argv)
    if how == "verbatim":
        return argv
    if how == "v060":
        # THE COUNTERFACTUAL, and the only edit in this probe. v0.6.0's mapped
        # argv omitted --setting-sources entirely (applyAgentMapping set
        # SettingSources = nil) and staged nothing, so its --agent resolved by
        # CLI DISCOVERY out of <cwd>/.claude/agents. Removing exactly those two
        # flag pairs reconstructs it; nothing else moves.
        for flag in ("--setting-sources", "--plugin-dir"):
            if flag not in argv:
                raise SystemExit("v060: recorded argv carries no " + flag)
            i = argv.index(flag)
            del argv[i:i + 2]
        return argv
    raise SystemExit("unknown transform " + how)


def set_session(argv, sid):
    if "--session-id" in argv:
        argv[argv.index("--session-id") + 1] = sid
    else:
        argv += ["--session-id", sid]
    return argv


def tool_uses(sid):
    """Every tool_use block in this session's own transcript.

    Returns (records, {tool: count}, transcript-found). `records` carries tool
    NAMES and input KEY names only. A full transcript would carry prompt and
    file content that has no place in a public repository, and none of it is
    evidence here.
    """
    records, census, found = [], {}, False
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
                    records.append({
                        "name": name,
                        "input_keys": sorted(inp.keys()) if isinstance(inp, dict) else [],
                    })
    return records, census, found


def claude_version():
    try:
        return subprocess.run(["claude", "--version"], capture_output=True, text=True).stdout.strip()
    except OSError as err:  # pragma: no cover - only if claude is not installed
        return f"unavailable: {err}"


def marker_roots(repo):
    """(root, max depth) pairs a marker may land under. PREREG addendum 1.

    A node whose entire tool set is `Write` holds no Read, no Bash, no Glob and
    no Grep, so it cannot determine its own cwd — (j) caught a spawn writing its
    marker to $HOME on a guess, (k) caught one writing to /tmp, and this probe's
    first two wrote to `/mnt/user-data/outputs/` and `/tmp/outputs/`. WHERE the
    file lands is a cwd-guessing artifact; WHETHER a token appears is the
    evidence, since a token exists only inside one agent definition and no tool
    the node holds can read one.

    So this is a bounded WALK rather than a list of the guesses already seen: an
    undetected marker here is not a missing datum but a wrong one, because the
    arm that matters claims a token is ABSENT. $HOME is depth 1 because depth 2
    under a home directory is unbounded work.
    """
    return [(repo, 3), ("/tmp", 2), (os.path.expanduser("~"), 1), ("/mnt/user-data/outputs", 1)]


def find_files(repo, names):
    """Every path under the roots whose basename is one of `names`."""
    found = []
    for root, depth in marker_roots(repo):
        if not os.path.isdir(root):
            continue
        base_depth = root.rstrip("/").count("/")
        for dirpath, dirnames, filenames in os.walk(root):
            if dirpath.rstrip("/").count("/") - base_depth >= depth:
                dirnames[:] = []
            for name in filenames:
                if name in names:
                    found.append(os.path.join(dirpath, name))
    return found


def clear_artifacts(repo):
    names = {m[1] for m in MARKERS} | {"design.html"}
    for p in find_files(repo, names):
        try:
            os.remove(p)
        except OSError:
            pass
    if os.path.exists(BREACH):
        os.remove(BREACH)
    if os.path.exists(GITCTL):
        shutil.rmtree(GITCTL)


def read_markers(repo):
    hit = []
    for path in find_files(repo, {m[1] for m in MARKERS}):
        try:
            body = open(path).read()
        except OSError:
            continue
        for label, marker, token in MARKERS:
            if os.path.basename(path) == marker and token in body:
                hit.append(label if os.path.dirname(path) == repo else f"{label}@{os.path.dirname(path)}")
    return hit


def main():
    if len(sys.argv) not in (5, 6):
        sys.exit(__doc__)
    ws, arm, argv_file, n = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
    how = sys.argv[5] if len(sys.argv) > 5 else "verbatim"
    repo = os.path.join(ws, "repo")
    logs = os.path.join(ws, "logs")
    dump = os.path.join(ws, "tool_use")
    os.makedirs(logs, exist_ok=True)
    os.makedirs(dump, exist_ok=True)

    base = transform(read_argv(argv_file), how)
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
        records, census, transcript = tool_uses(sid)
        with open(os.path.join(dump, f"{arm}.{sid}.tool_use.jsonl"), "w") as fh:
            for rec in records:
                fh.write(json.dumps(rec) + "\n")
        row = {
            "arm": arm,
            "transform": how,
            "sid": sid,
            "exit": proc.returncode,
            "stderr_head": proc.stderr.strip()[:220],
            "claude_version": version,
            "models": sorted((envelope.get("modelUsage") or {}).keys()),
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
