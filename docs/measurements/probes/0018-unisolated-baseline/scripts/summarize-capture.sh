#!/usr/bin/env bash
# Print one capture's population determination and the corroborating git state,
# so a reader can see what a row was built from without opening five files.
#
#   summarize-capture.sh <capture-dir>
set -euo pipefail
cap="${1:?usage: summarize-capture.sh <capture-dir>}"

echo "### $(basename "$cap")"
sed -n 's/^/    /p' "$cap/meta.txt"
echo
echo "-- population (scanUnisolated's own verdict, printed at run time):"
grep -A1 '! not isolated' "$cap/stdout.txt" | sed 's/^/    /' || echo "    (none printed)"
echo
echo "-- planned nodes:"
sed -n '/^Planned graph/,/^  skill scan/p' "$cap/stdout.txt" | grep '^  - ' | sed 's/^/    /' || true
echo
echo "-- node verdicts:"
grep -E '^[✓✖] ' "$cap/stdout.txt" | sed 's/^/    /' || true
echo
echo "-- sessions (node / verdict / session id):"
[ -f "$cap/sessions.txt" ] && sed 's/^/    /' "$cap/sessions.txt" || echo "    (none)"
echo
echo "-- foreign checkout AFTER the run (corroboration only):"
# Read the checkout from meta.txt rather than hard-coding pair 1 and 2's names:
# pairs 3 and 4 use brand-assets and proto-defs, and a name filter prints an
# empty block for exactly the runs the sample was widened with.
# Compared by last path component: meta.txt resolves the path (`pwd -P`, so
# /private/tmp on macOS) while git-after.txt prints the argument as given.
foreign="$(basename "$(sed -n 's/^foreign checkout: *//p' "$cap/meta.txt" | head -1)")"
awk -v want="$foreign" '/^=== /{n = split($2, p, "/"); keep = (p[n] == want)} keep' \
  "$cap/git-after.txt" | sed 's/^/    /'
