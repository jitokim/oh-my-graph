#!/bin/bash
# The recording claude: writes its own argv, NUL-separated, into
# $OMG_ARGV_DIR/argv.<pid>.<n>, prints a minimal envelope so
# runner.parseEnvelope is satisfied, and spawns nothing.
#
# This is what makes the measured argv runner.buildArgs' output rather than a
# transcription of it.
#
# errexit, not just nounset: if the capture below cannot be written, this must
# NOT go on to print a success envelope. A spawn recorded as successful with no
# argv beside it is the one failure this probe cannot detect after the fact.
set -eu
out="$OMG_ARGV_DIR/argv.$$.$RANDOM"
for a in "$@"; do printf '%s\0' "$a"; done >"$out"
printf '{"session_id":"00000000-0000-4000-8000-000000000000","result":"shim","total_cost_usd":0}\n'
