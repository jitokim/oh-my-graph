#!/bin/sh
# Does the repository still meet the requirements its owner wrote down (#386)?
#
# This is the deterministic half of a drift review: everything about it that
# needs no model. It answers in four steps, stopping at the first that fails.
#
#   a. HEAD        only when head is given: the checked-out commit is head.
#                  The invariants run on the working tree, so a review of any
#                  other commit would judge code it never ran.
#   b. LOCK        the lock file pins, by sha256, the design and the
#                  requirements file the review is judged against, and must
#                  have an entry for both. A missing lock, a file it does not
#                  pin (`LOCK <file> not pinned`), or a pinned file that is
#                  missing or changed, stops the review: the yardstick moved,
#                  and no one said so.
#   c. FORMAT      every requirement is a well-formed entry (below).
#   d. INVARIANTS  every `invariant` entry's check command passes.
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
# The paths in the lock are compared literally with the arguments, so pass the
# design and the requirements file here exactly as the owner passed them.
#
# The requirements file holds one entry per line; blank lines and lines whose
# first non-blank character is `#` are ignored. An entry is exactly five
# `|`-separated fields, each trimmed of surrounding whitespace:
#
#   ID | measure | source | check | text
#
#   ID       unique within the file
#   measure  `invariant` or `llm`
#   source   where the requirement comes from, e.g. DESIGN.md:12
#   check    invariant: a shell command, run from the repository root, that
#            exits 0 when the requirement holds; llm: a short check name
#   text     free text
#
# No field may hold `|`, so an entry with any other count is refused as
#
#   <ID> has N fields, want 5 ('|' is not allowed inside a check; put a pipeline in a script file)
#
# A bad entry is named by its ID, or by its line number when the ID is empty.
#
# Usage:  scripts/drift-baseline.sh <design> <requirements-file> <lock-file> [head]
#         (run from the target repository's root)
# Exit:   0 every step passed   1 a head, lock, format or invariant finding
#         (each is printed, one per line)   2 usage
set -eu

if [ $# -lt 3 ] || [ $# -gt 4 ]; then
	echo "usage: $0 <design> <requirements-file> <lock-file> [head]" >&2
	exit 2
fi
design=$1
reqs=$2
lock=$3

trim() {
	printf '%s' "$1" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//'
}

# parse splits one entry into id, measure, source, check and text, and sets
# fields to its number of |-separated fields.
parse() {
	fields=$(($(printf '%s' "$1" | tr -cd '|' | wc -c) + 1))
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

# lockpath sets path to the file a lock line $1 pins: a sha256sum line
# `<hash>  <path>` (text mode) or `<hash> *<path>` (binary mode).
lockpath() {
	path=${1#* }
	path=${path#[ *]}
}

# pinned is true when the lock has an entry for the path $1.
pinned() {
	while IFS= read -r entry || [ -n "$entry" ]; do
		lockpath "$entry"
		[ "$path" != "$1" ] || return 0
	done <"$lock"
	return 1
}

# missing prints `LOCK <path> missing` for each well-formed lock line whose
# file is not a regular file. It is decided here, not from the checker's
# output: GNU sha256sum reports a missing file on stdout, macOS's only on
# stderr. A malformed line is left for the checker to call unreadable.
missing() {
	while IFS= read -r entry || [ -n "$entry" ]; do
		hash=${entry%% *}
		[ "${#hash}" -eq 64 ] || continue
		case "$hash" in *[!0-9a-fA-F]*) continue ;; esac
		case "$entry" in "$hash  "?* | "$hash *"?*) ;; *) continue ;; esac
		lockpath "$entry"
		[ -f "$path" ] || echo "LOCK $path missing"
	done <"$lock"
}

# --- a. HEAD ------------------------------------------------------------------

if [ $# -eq 4 ]; then
	head=$4
	if ! here=$(git rev-parse --verify -q HEAD); then
		echo "HEAD the checked-out tree is not a git commit; cannot compare it with head $head"
		exit 1
	fi
	if ! want=$(git rev-parse --verify -q "$head^{commit}"); then
		echo "HEAD $here is not head $head: $head does not resolve to a commit"
		exit 1
	fi
	if [ "$here" != "$want" ]; then
		echo "HEAD $here is not head $want; invariants run on the checked-out tree"
		exit 1
	fi
fi

# --- b. LOCK ------------------------------------------------------------------

if [ ! -f "$lock" ]; then
	echo "LOCK $lock missing"
	exit 1
fi
unpinned=0
for f in "$design" "$reqs"; do
	if ! pinned "$f"; then
		echo "LOCK $f not pinned"
		unpinned=1
	fi
done
[ "$unpinned" -eq 0 ] || exit 1
if command -v sha256sum >/dev/null 2>&1; then
	sha256check() { sha256sum -c "$1"; }
elif command -v shasum >/dev/null 2>&1; then
	sha256check() { shasum -a 256 -c "$1"; }
else
	echo "LOCK cannot verify $lock: neither sha256sum nor shasum is installed"
	exit 1
fi
gone=$(missing)
if ! verdict=$(LC_ALL=C sha256check "$lock" 2>/dev/null) || [ -n "$gone" ]; then
	changed=$(printf '%s\n' "$verdict" | sed -n 's/^\(.*\): FAILED$/LOCK \1 changed/p')
	found=$(printf '%s\n%s\n' "$gone" "$changed" | sed '/^$/d')
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

# --- c. FORMAT ----------------------------------------------------------------

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
	if [ "$fields" -ne 5 ]; then
		problems="has $fields fields, want 5 ('|' is not allowed inside a check; put a pipeline in a script file)"
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

# --- d. INVARIANTS ------------------------------------------------------------

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
