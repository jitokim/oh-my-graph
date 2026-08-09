#!/usr/bin/env bash
# Take ONE `auto` run and capture everything the population is defined by,
# AT RUN TIME. This is the whole point of the script: `Unisolated` reaches no
# runstate field and no runfeed event, and a single-cycle `auto` persists
# neither the invocation root nor the goal, so a population reconstructed later
# from state.json alone is not this population (ADR 0018 §Falsification).
#
#   capture-run.sh <label> <A-repo> <B-repo> <goal-file>
#
# A is the repository oh-my-graph is invoked from; B is the foreign checkout the
# goal names. Both are throwaway fixtures under /tmp — see setup-fixtures.sh.
set -euo pipefail

label="${1:?usage: capture-run.sh <label> <A-repo> <B-repo> <goal-file>}"
repo_a="${2:?missing A repo}"
repo_b="${3:?missing B repo}"
goal_file="${4:?missing goal file}"

probe_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
bin="${OMG_BIN:?set OMG_BIN to the ABSOLUTE path of the binary built from this worktree}"
export OMG_HOME="${OMG_HOME:-/tmp/omg-0018/omghome}"

cap="$probe_dir/captures/$label"
rm -rf "$cap"
mkdir -p "$cap/runstate" "$cap/transcripts"

goal="$(cat "$goal_file")"
cp "$goal_file" "$cap/goal.txt"

# ---- 1. what the snapshot will not record -------------------------------
{
  echo "label:            $label"
  echo "date:             $(date -Iseconds)"
  echo "binary:           $bin"
  echo "binary version:   $("$bin" version 2>&1 | head -1)"
  echo "binary sha256:    $(shasum -a 256 "$bin" | awk '{print $1}')"
  echo "claude version:   $(claude --version 2>&1 | head -1)"
  echo "OMG_HOME:         $OMG_HOME"
  echo "invocation dir:   $(cd "$repo_a" && pwd -P)"
  echo "foreign checkout: $(cd "$repo_b" && pwd -P)"
  echo "argv:             auto <goal> --no-web --continue-on-fail"
  echo "goal file:        $goal_file"
} >"$cap/meta.txt"

git_state() {
  for repo in "$repo_a" "$repo_b"; do
    echo "=== $repo"
    echo "-- HEAD:      $(git -C "$repo" rev-parse HEAD 2>&1)"
    echo "-- branch:    $(git -C "$repo" rev-parse --abbrev-ref HEAD 2>&1)"
    echo "-- branches:"
    git -C "$repo" branch -a 2>&1 | sed 's/^/     /'
    echo "-- worktrees:"
    git -C "$repo" worktree list 2>&1 | sed 's/^/     /'
    echo "-- log:"
    git -C "$repo" log --oneline --all --decorate 2>&1 | sed 's/^/     /'
    echo "-- status:"
    git -C "$repo" status --porcelain 2>&1 | sed 's/^/     /'
    echo
  done
}
git_state >"$cap/git-before.txt"

# ---- 2. the run ---------------------------------------------------------
before_runs="$(ls -1 "$OMG_HOME/runs" 2>/dev/null | sort || true)"

set +e
( cd "$repo_a" && "$bin" auto "$goal" --no-web --continue-on-fail ) \
  >"$cap/stdout.txt" 2>&1
status=$?
set -e
echo "$status" >"$cap/exit-code.txt"

git_state >"$cap/git-after.txt"

# ---- 3. the run directory ----------------------------------------------
after_runs="$(ls -1 "$OMG_HOME/runs" 2>/dev/null | sort || true)"
run_id="$(comm -13 <(echo "$before_runs") <(echo "$after_runs") | tail -1)"
echo "${run_id:-<none>}" >"$cap/run-id.txt"

if [[ -n "$run_id" ]]; then
  run_dir="$OMG_HOME/runs/$run_id"
  for f in state.json graph.json events.jsonl; do
    [[ -f "$run_dir/$f" ]] && cp "$run_dir/$f" "$cap/runstate/$f"
  done

  # ---- 4. the transcripts, located via NodeRecord.SessionID ------------
  python3 - "$run_dir/state.json" "$cap/transcripts" <<'PY'
import json, os, pathlib, shutil, sys

state_path, out_dir = sys.argv[1], sys.argv[2]
state = json.load(open(state_path))
projects = pathlib.Path.home() / ".claude" / "projects"
index = {}
if projects.is_dir():
    for path in projects.rglob("*.jsonl"):
        index.setdefault(path.stem, path)

rows = []
for node_id, record in sorted((state.get("nodes") or {}).items()):
    sid = record.get("session_id") or ""
    src = index.get(sid)
    dest = ""
    if src is not None:
        dest = os.path.join(out_dir, f"{node_id}--{sid}.jsonl")
        shutil.copyfile(src, dest)
    rows.append((node_id, record.get("verdict", ""), sid, str(src or "<not found>")))

with open(os.path.join(out_dir, "..", "sessions.txt"), "w") as fh:
    for node_id, verdict, sid, src in rows:
        fh.write(f"{node_id}\t{verdict}\t{sid}\t{src}\n")
        print(f"  {node_id}\t{verdict}\t{sid}")
PY
fi

echo "captured $label (exit $status, run ${run_id:-<none>}) -> $cap"
