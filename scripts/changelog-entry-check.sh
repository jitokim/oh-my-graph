#!/bin/sh
# Does this diff add a changelog entry a reader would actually find?
#
# Since ADR 0042 (#304) an unreleased entry is a FILE the pull request owns,
# `changelog.d/<issue>-<slug>.md`, not a line in CHANGELOG.md's
# `## [Unreleased]` section: every PR appending to that one section made any two
# PRs open at once conflict, and made backlog-batch's rule 1 (lanes share no
# files) false by construction. The release PR collects the fragments into a
# `## [vX.Y.Z]` section (scripts/changelog-collect.sh) and deletes them.
#
# The question this asks is the one the `changelog` job always meant, moved to
# where the entry now lives:
#
#   1. An entry is a fragment the diff adds, or an existing fragment that gains
#      a novel prose line — whose name and shape are well-formed
#      (scripts/changelog-fragment-check.sh). A line that only restates one the
#      diff took away from changelog.d/ is a reflow, not an entry (#208), so a
#      reindented bullet or a renamed fragment buys nothing. Diffs are read with
#      rename detection OFF: a fragment renamed and edited in one PR is then a
#      deletion plus an addition, and the new path is judged whole.
#   2. A release cut is still the entry: an added `## [vX.Y.Z]` heading in
#      CHANGELOG.md for a version the base did not have. An edited released
#      heading (its date, a typo) names a version the base had, so it is not a
#      cut and is judged by rules 1 and 4 like any other edit.
#      A cut is REFUSED (exit 2) while changelog.d/ at HEAD still holds a
#      fragment. HEAD's tree, not the diff, on purpose: a fragment merged to
#      main after the collector ran reaches the release PR through
#      Update branch, which moves the merge-base past it, so it is never in
#      base...HEAD — and unrefused it would ship in this tag's code and in the
#      NEXT release's notes.
#   3. A line added under `## [Unreleased]` is not counted either way. It draws
#      a warning naming changelog.d/, and nothing else; keeping entries out of
#      Unreleased is TestUnreleasedSectionHoldsNoEntries' job.
#   4. A misnamed or malformed file under changelog.d/ is refused by name: the
#      collector would refuse it at release, and refusing it here is cheap.
#      changelog.d/README.md is exempt — neither an entry nor a refusal.
#
# Usage:  scripts/changelog-entry-check.sh <base-sha>
# Exit:   0 an entry was added   1 nothing a reader would find, or a misnamed
#         or malformed fragment   2 a release cut that leaves a fragment behind
#         (no PR body excuses it), or usage
#
# It runs locally on any branch, which is the point — a guard nobody can run
# outside CI is a guard nobody falsifies:
#
#     scripts/changelog-entry-check.sh "$(git merge-base main HEAD)"
#
# It reads committed history, not the working tree, because that is what CI
# judges. Uncommitted edits are invisible to it — commit first, or it will tell
# you nothing was added while you are looking at it.
set -eu

if [ $# -ne 1 ]; then
	echo "usage: $0 <base-sha>" >&2
	exit 2
fi
base=$1
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
file=CHANGELOG.md
dir=changelog.d

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# Added lines of CHANGELOG.md, numbered as they land in the file at HEAD. With
# --unified=0 a hunk's removed lines carry no new-file number, so advancing the
# counter on '+' alone is the correct numbering.
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

# --- rule 2: a release cut ----------------------------------------------------
# A cut is an added `## [vX.Y.Z]` heading whose version CHANGELOG.md at the base
# did not already have. Editing a released heading — a date, a typo — re-adds a
# version that is already there: that is not a cut, and falls through to rules
# 1 and 4 like any other edit, where `no-changelog` can excuse it.
git show "$base:$file" 2>/dev/null | awk '
	/^## \[v?[0-9]+\.[0-9]+\.[0-9]+\]/ { v = $0; sub(/^## \[/, "", v); sub(/\].*/, "", v); print v }
' >"$tmp/released"
cut=$(printf '%s\n' "$added" | awk -v released="$tmp/released" '
	BEGIN { while ((getline v < released) > 0) old[v] = 1 }
	{ line = $0; sub(/^[0-9]+\t/, "", line) }
	line ~ /^## \[v?[0-9]+\.[0-9]+\.[0-9]+\]/ {
		v = line; sub(/^## \[/, "", v); sub(/\].*/, "", v)
		if (!(v in old)) print v
	}
')
if [ -n "$cut" ]; then
	left=$(git ls-tree -r --name-only HEAD -- "$dir/" | grep -vxF "$dir/README.md" || true)
	if [ -n "$left" ]; then
		echo "::error::a release section was cut, but $dir/ at HEAD still holds:" >&2
		printf '%s\n' "$left" | sed 's/^/  /' >&2
		echo "Each is an entry for a change this tag would ship, which would otherwise wait for the NEXT release's notes." >&2
		echo "Move its text into the new section and delete it, or remove the section and run scripts/changelog-collect.sh again." >&2
		echo "'no-changelog' does not excuse this (ADR 0042)." >&2
		exit 2
	fi
	# shellcheck disable=SC2086
	echo "a release section was cut:" $cut "— that is the entry"
	exit 0
fi

# --- rule 3: Unreleased is not counted, only pointed away from ---------------
if [ -f "$file" ]; then
	bounds=$(awk '
		/^## \[Unreleased\]/ { start = NR; next }
		start && !end && /^## / { end = NR - 1 }
		END { if (start) print start "\t" (end ? end : NR) }
	' "$file")
	if [ -n "$bounds" ]; then
		start=$(printf '%s' "$bounds" | cut -f1)
		end=$(printf '%s' "$bounds" | cut -f2)
		if printf '%s\n' "$added" | awk -F '\t' -v s="$start" -v e="$end" '
			$1 > s && $1 <= e && $2 ~ /[^ \t]/ { found = 1 }
			END { exit !found }
		'; then
			echo "::warning::lines were added under ## [Unreleased] in $file; they are not changelog entries." >&2
			echo "Since ADR 0042 an entry is its own file: $dir/<issue>-<slug>.md (see $dir/README.md)." >&2
		fi
	fi
fi

# --- rules 1 and 4: fragments -------------------------------------------------
# Everything the diff took away from a fragment, whitespace-normalised: the
# lines an added one must not merely restate.
git diff --no-renames --unified=0 "$base"...HEAD -- "$dir/" ":(exclude)$dir/README.md" | awk '
	/^---/ { next }
	/^-/ {
		line = substr($0, 2)
		gsub(/[[:space:]]+/, " ", line)
		sub(/^ /, "", line)
		sub(/ $/, "", line)
		print line
	}
' >"$tmp/removed"

changes=$(git diff --no-renames --name-status "$base"...HEAD -- "$dir/")
mkdir "$tmp/$dir"
entries=""
bad=""
while IFS='	' read -r status path; do
	[ -n "$path" ] || continue
	[ "$path" != "$dir/README.md" ] || continue
	case $status in D*) continue ;; esac
	case $path in
	"$dir"/*/*)
		echo "$path: a fragment lives directly in $dir/, not below it" >&2
		bad=yes
		continue
		;;
	esac
	name=${path#"$dir"/}
	if ! git show "HEAD:$path" >"$tmp/$dir/$name" 2>/dev/null; then
		echo "$path: unreadable at HEAD" >&2
		bad=yes
		continue
	fi
	if ! (cd "$tmp" && sh "$here/changelog-fragment-check.sh" "$path"); then
		bad=yes
		continue
	fi
	# A novel PROSE line: a re-headed fragment has said nothing new.
	if git diff --no-renames --unified=0 "$base"...HEAD -- "$path" | awk -v removed="$tmp/removed" '
		BEGIN { while ((getline line < removed) > 0) seen[line] = 1 }
		/^\+\+\+/ { next }
		/^\+/ {
			line = substr($0, 2)
			gsub(/[[:space:]]+/, " ", line)
			sub(/^ /, "", line)
			sub(/ $/, "", line)
			if (line == "" || line ~ /^#/ || line ~ /^```/) next
			if (!(line in seen)) found = 1
		}
		END { exit !found }
	'; then
		entries="$entries $path"
	fi
done <<EOF
$changes
EOF

if [ -n "$bad" ]; then
	echo "::error::$dir/ gained a file that is not a well-formed fragment (named above)." >&2
	echo "The release collector would refuse it; see $dir/README.md for the name and shape." >&2
	exit 1
fi

if [ -n "$entries" ]; then
	echo "an entry was added:$entries"
	exit 0
fi

echo "::error::nothing in this diff is a changelog entry." >&2
echo "Write one as $dir/<issue>-<slug>.md — a ### <Section> heading and the entry under it (see $dir/README.md)." >&2
echo "A fragment that only restates lines this diff removed from $dir/ is a reflow or a rename, not an entry." >&2
exit 1
