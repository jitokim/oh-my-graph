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
awk '/^=== /{keep=0} /shared-config|chart-lib/{keep=1} keep' "$cap/git-after.txt" | sed 's/^/    /'
