#!/bin/sh
# Does this diff add a CHANGELOG entry a reader would actually find?
#
# The `changelog` job used to ask only whether CHANGELOG.md appeared in the
# changed-file list, so a trailing space passed, and so did an entry filed under
# an already-released heading — the two ways of touching the file that leave a
# reader with nothing (#208). It named `## [Unreleased]` in its error text and
# never looked at it.
#
# This asks the narrower question the job always meant: did the diff add a
# non-blank line INSIDE the `## [Unreleased]` section as it stands at HEAD?
#
# One exemption, and it is not a loophole: a release cut moves entries out of
# Unreleased into a new `## [vX.Y.Z]` heading and legitimately leaves Unreleased
# empty (the v0.10.0 cut, #203, is exactly this shape). An added version heading
# is therefore accepted as an entry.
#
# Usage:  scripts/changelog-entry-check.sh <base-sha>
# Exit:   0 an entry was added   1 nothing a reader would find
#
# It runs locally on any branch, which is the point — a guard nobody can run
# outside CI is a guard nobody falsifies:
#
#     scripts/changelog-entry-check.sh "$(git merge-base main HEAD)"
#
# It reads committed history, not the working tree, because that is what CI
# judges. Uncommitted edits to CHANGELOG.md are invisible to it — commit first,
# or it will tell you the file gained no lines while you are looking at them.
set -eu

if [ $# -ne 1 ]; then
	echo "usage: $0 <base-sha>" >&2
	exit 2
fi
base=$1
file=CHANGELOG.md

if [ ! -f "$file" ]; then
	echo "no $file in the working tree" >&2
	exit 1
fi

# Added lines, numbered as they land in the file at HEAD. With --unified=0 a
# hunk's removed lines carry no new-file number, so advancing the counter on
# '+' alone is the correct numbering.
added=$(git diff --unified=0 "$base"...HEAD -- "$file" | awk '
	/^\+\+\+/ { next }
	/^@@/ {
		# @@ -a,b +c,d @@ — c is where this hunk lands in the new file.
		split($3, hunk, ",")
		n = substr(hunk[1], 2) + 0
		next
	}
	/^\+/ { print n "\t" substr($0, 2); n++ }
')

if [ -z "$added" ]; then
	echo "$file gained no lines in this diff" >&2
	exit 1
fi

# A release cut: entries move into a new version heading, so Unreleased is
# legitimately left empty.
if printf '%s\n' "$added" | grep -q '^[0-9][0-9]*	## \['; then
	echo "a release section was cut — that is the entry"
	exit 0
fi

# The Unreleased section's bounds in the file as it stands now.
bounds=$(awk '
	/^## \[Unreleased\]/ { start = NR; next }
	start && !end && /^## / { end = NR - 1 }
	END { if (start) print start "\t" (end ? end : NR) }
' "$file")

if [ -z "$bounds" ]; then
	echo "::error::$file has no '## [Unreleased]' heading to file an entry under." >&2
	exit 1
fi

start=$(printf '%s' "$bounds" | cut -f1)
end=$(printf '%s' "$bounds" | cut -f2)

# What the diff took away, whitespace-normalised. An added line that only
# restates a removed one is a reflow, not an entry — reindenting a bullet or
# putting a space at the end of it must not buy a green check, which is the
# case #208 names.
removed=$(git diff --unified=0 "$base"...HEAD -- "$file" | awk '
	/^---/ { next }
	/^-/ {
		line = substr($0, 2)
		gsub(/[[:space:]]+/, " ", line)
		sub(/^ /, "", line)
		sub(/ $/, "", line)
		print line
	}
')

novel=""
printf '%s\n' "$added" | while IFS= read -r row; do
	num=$(printf '%s' "$row" | cut -f1)
	[ "$num" -gt "$start" ] 2>/dev/null || continue
	[ "$num" -le "$end" ] || continue
	text=$(printf '%s' "$row" | cut -f2- | tr -s '[:space:]' ' ' | sed 's/^ //; s/ $//')
	[ -n "$text" ] || continue
	# `--` is load-bearing: every changelog entry starts with `-`, which grep
	# reads as a flag without it, and the comparison then silently never runs.
	printf '%s\n' "$removed" | grep -qxF -- "$text" && continue
	exit 42 # a line that is genuinely new, inside Unreleased
done || novel=yes

if [ -n "$novel" ]; then
	echo "an entry was added under ## [Unreleased] (lines $start-$end)"
	exit 0
fi

echo "::error::$file changed, but nothing was added under '## [Unreleased]'." >&2
echo "Lines $start-$end are the Unreleased section; the additions in this diff all fall outside it," >&2
echo "or are blank. An entry filed under an already-released heading is not an entry for this change." >&2
exit 1
