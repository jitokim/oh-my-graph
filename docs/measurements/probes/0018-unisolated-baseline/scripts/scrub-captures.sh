#!/usr/bin/env bash
# Make the captures publishable. This repository is public and keeps personal
# paths out of tracked files (`git grep Users/` finds nothing), and a node's raw
# session transcript carries the operator's ENTIRE local skill corpus in its
# system prompt — 35 descriptions of private tooling that have nothing to do
# with this measurement.
#
# So, before the archive is committed:
#
#   1. raw `transcripts/*.jsonl` are DELETED. What is kept in their place is
#      `excerpts/<run>--<node>.txt` — every Bash command the node ran, in order,
#      which is the evidence each row was actually decided from — plus
#      `runstate/graph.json`, which holds the node's full planned prompt.
#      Together those are exactly what ADR 0018 asks a sample to preserve: "the
#      node's git commands, the prompt, the invocation root".
#   2. the staged-skill corpus listing is cut out of every `stdout.txt`, and a
#      marker line records how many lines went. The plan printout, the
#      `! not isolated:` population block, the node verdicts and the ledger all
#      stay.
#   3. `$HOME` is rewritten to `<home>` everywhere that remains.
#
# Idempotent.
set -euo pipefail

probe_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$probe_dir"

rm -rf captures/*/transcripts

for out in captures/*/stdout.txt; do
  python3 - "$out" <<'PY'
import re, sys

MARKER_RE = re.compile(r"^    \[\d+ staged-skill description line")
ANCHOR = "    and feedback re-runs)"

path = sys.argv[1]
lines = open(path, encoding="utf-8").read().split("\n")

dropped = 0
kept, skipping = [], False
for line in lines:
    if MARKER_RE.match(line):
        dropped += int(re.search(r"\[(\d+)", line).group(1))
        continue
    # A staged-skill entry is '    <name> (<size>, sha256:…) — "<description>"',
    # whose description may wrap onto continuation lines after it.
    if re.match(r"^    \S+ \(\d[^)]*sha256:", line):
        skipping = True
        dropped += 1
        continue
    if skipping:
        if line.startswith("  Which skill a node uses"):
            skipping = False
        else:
            continue
    kept.append(line)

if dropped:
    marker = (f"    [{dropped} staged-skill description line(s) removed for publication — "
              "the operator's local skill corpus, unrelated to this measurement; "
              "see scripts/scrub-captures.sh]")
    at = kept.index(ANCHOR) + 1 if ANCHOR in kept else 0
    kept.insert(at, marker)

open(path, "w", encoding="utf-8").write("\n".join(kept))
PY
done

# grep -Z / xargs -0 is not portable enough here; a plain loop is.
# $HOME goes through the ENVIRONMENT, never into the perl program text: a home
# path containing `}` would break the s{}{} delimiters, and one containing `$`
# or `@` would be interpolated by perl itself — silently, leaving the operator's
# real path in a capture bound for a public repository.
while IFS= read -r file; do
  HOME_PATH="$HOME" perl -pi -e 's{\Q$ENV{HOME_PATH}\E}{<home>}g' "$file"
done < <(grep -rl "$HOME" . 2>/dev/null || true)

echo "scrubbed: raw transcripts removed, skill corpus cut from stdout, \$HOME rewritten"
# This is the last gate before the captures are committed, so a surviving home
# path has to fail the script — a caller or a hook cannot read a warning.
if grep -rq "$HOME" . 2>/dev/null; then
  echo "WARNING: home path still present"
  grep -rl "$HOME" .
  exit 1
else
  echo "no home path remains"
fi
