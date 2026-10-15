#!/bin/sh
# Does the repository still meet the requirements its owner wrote down (#386)?
#
# This is the deterministic half of a drift review: everything about it that
# needs no model. It answers in three steps, stopping at the first that fails.
#
#   a. LOCK        the lock file pins, by sha256, the design and the
#                  requirements file the review is judged against. A missing
#                  lock, or a pinned file that is missing or changed, stops
#                  the review: the yardstick moved, and no one said so.
#   b. FORMAT      every requirement is a well-formed entry (below).
#   c. INVARIANTS  every `invariant` entry's check command passes.
#
# On success it prints `OK <n> invariant, <m> llm`; the `llm` entries are left
# for a model to judge, never by this script.
#
# THE LOCK IS NEVER WRITTEN HERE. Creating or updating it is the owner's
# decision — not this script's, and not a model's. A person runs, from the
# repository root:
#
#   sha256sum <design> <requirements-file> > <lock-file>
#
# e.g. `sha256sum DESIGN.md docs/requirements.txt > docs/requirements.lock`.
# Re-running that after an edit is how the owner says the new text is the spec.
#
# The requirements file holds one entry per line; blank lines and lines whose
# first non-blank character is `#` are ignored. An entry is five `|`-separated
# fields, each trimmed of surrounding whitespace:
#
#   ID | measure | source | check | text
#
#   ID       unique within the file
#   measure  `invariant` or `llm`
#   source   where the requirement comes from, e.g. DESIGN.md:12
#   check    invariant: a shell command, run from the repository root, that
#            exits 0 when the requirement holds; llm: a short check name
#   text     free text — everything after the fourth `|`, so it may hold `|`
#
# A bad entry is named by its ID, or by its line number when the ID is empty.
#
# Usage:  scripts/drift-baseline.sh <requirements-file> <lock-file>
#         (run from the target repository's root)
# Exit:   0 every step passed   1 a lock, format or invariant finding (each
#         is printed, one per line)   2 usage
set -eu

if [ $# -ne 2 ]; then
	echo "usage: $0 <requirements-file> <lock-file>" >&2
	exit 2
fi
reqs=$1
lock=$2

trim() {
	printf '%s' "$1" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//'
}

# parse splits one entry into id, measure, source, check and text. fields is
# 0 when the line has fewer than five fields.
parse() {
	case "$1" in
	*'|'*'|'*'|'*'|'*) fields=1 ;;
	*) fields=0 ;;
	esac
	IFS='|' read -r id measure source check text <<EOF
$1
EOF
	id=$(trim "$id")
	measure=$(trim "$measure")
	source=$(trim "$source")
	check=$(trim "$check")
	text=$(trim "$text")
}

# skip is true for a blank line or a comment.
skip() {
	case "$(trim "$1")" in
	'' | '#'*) return 0 ;;
	esac
	return 1
}

# --- a. LOCK ------------------------------------------------------------------

if [ ! -f "$lock" ]; then
	echo "LOCK $lock missing"
	exit 1
fi
if ! verdict=$(LC_ALL=C sha256sum -c "$lock" 2>/dev/null); then
	found=$(printf '%s\n' "$verdict" | sed -n \
		-e 's/^\(.*\): FAILED open or read$/LOCK \1 missing/p' \
		-e 's/^\(.*\): FAILED$/LOCK \1 changed/p')
	if [ -z "$found" ]; then
		found="LOCK $lock unreadable — want lines from sha256sum <design> <requirements-file>"
	fi
	printf '%s\n' "$found"
	exit 1
fi
if [ ! -f "$reqs" ]; then
	echo "LOCK $reqs missing"
	exit 1
fi

# --- b. FORMAT ----------------------------------------------------------------

bad=0
seen=$(mktemp)
trap 'rm -f "$seen"' EXIT
n=0
while IFS= read -r line || [ -n "$line" ]; do
	n=$((n + 1))
	if skip "$line"; then
		continue
	fi
	parse "$line"
	problems=
	if [ "$fields" -eq 0 ]; then
		problems="fewer than five |-separated fields"
	else
		[ -n "$id" ] || problems="${problems:+$problems; }empty ID"
		case "$measure" in
		invariant | llm) ;;
		*) problems="${problems:+$problems; }unknown measure \"$measure\" — want invariant or llm" ;;
		esac
		[ -n "$source" ] || problems="${problems:+$problems; }empty source"
		[ -n "$check" ] || problems="${problems:+$problems; }empty check"
		if [ -n "$id" ]; then
			first=$(awk -F'\t' -v id="$id" '$1 == id { print $2; exit }' "$seen")
			if [ -n "$first" ]; then
				problems="${problems:+$problems; }duplicate ID (first on line $first)"
			else
				printf '%s\t%s\n' "$id" "$n" >>"$seen"
			fi
		fi
	fi
	if [ -n "$problems" ]; then
		echo "${id:-$n} $problems"
		bad=1
	fi
done <"$reqs"
[ "$bad" -eq 0 ] || exit 1

# --- c. INVARIANTS ------------------------------------------------------------

invariants=0
llms=0
while IFS= read -r line || [ -n "$line" ]; do
	if skip "$line"; then
		continue
	fi
	parse "$line"
	if [ "$measure" = llm ]; then
		llms=$((llms + 1))
		continue
	fi
	invariants=$((invariants + 1))
	# stdin is the requirements file here; a check must not read it.
	if ! out=$(sh -c "$check" 2>&1 </dev/null); then
		first=$(printf '%s\n' "$out" | sed -n '/[^[:space:]]/{p;q;}')
		echo "$id $source ${first:-check failed}"
		bad=1
	fi
done <"$reqs"
[ "$bad" -eq 0 ] || exit 1

echo "OK $invariants invariant, $llms llm"
