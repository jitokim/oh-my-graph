#!/bin/sh
# Collect changelog.d/'s fragments into a new release section of CHANGELOG.md,
# and delete them (ADR 0042, §2.3).
#
# Run by the maintainer in the release PR, before writing the section's opening
# prose. It writes `## [vX.Y.Z] - <date>` directly under the `## [Unreleased]`
# block, then each section heading ONCE, in the canonical order, with every
# fragment's entry for that heading under it — fragments ordered by issue number
# (numeric), then slug. One heading per section by construction is what spares
# a release the v0.14.0 node that merged eleven subheadings back into four.
#
# It writes no intro prose and no footnote: both are already the maintainer's
# (CONTRIBUTING.md, "Releasing"), and TestChangelogSectionHasSubstance and
# TestChangelogHasFootnoteForThisVersion guard them. What it writes is a draft
# to edit before the tag, not the release body. scripts/release-notes.sh reads
# the section it writes exactly as it read a hand-promoted one.
#
# Every refusal comes before the first write, so a refusal leaves CHANGELOG.md
# and changelog.d/ byte-identical:
#
#   - a file git tracks under changelog.d/ (other than README.md) that is not
#     a well-formed fragment — named, by scripts/changelog-fragment-check.sh,
#     or as nested below changelog.d/. Untracked and ignored files (a
#     .DS_Store, an editor's swap file) are not entries and are left alone
#   - a fragment the checker passed that the section did not take exactly once:
#     the checker and the extractor disagree, and deleting it would drop it
#   - no fragments at all: a release with nothing to collect is the empty body
#     TestChangelogSectionHasSubstance exists to stop
#   - a `## [vX.Y.Z]` heading already in CHANGELOG.md, so a second run cannot
#     write the section twice
#
# Staging is the maintainer's, like every other release-PR edit.
#
# Usage:  scripts/changelog-collect.sh <vX.Y.Z> [<YYYY-MM-DD>]   (date: today)
# Exit:   0 collected   1 refused   2 usage
set -eu

usage() {
	echo "usage: $0 <vX.Y.Z> [<YYYY-MM-DD>]" >&2
	exit 2
}

[ $# -ge 1 ] && [ $# -le 2 ] || usage
tag=$1
date=${2:-$(date +%Y-%m-%d)}
printf '%s\n' "$tag" | grep -Eqx 'v[0-9]+\.[0-9]+\.[0-9]+' || usage
printf '%s\n' "$date" | grep -Eqx '[0-9]{4}-[0-9]{2}-[0-9]{2}' || usage

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
changelog="$root/CHANGELOG.md"
dir="$root/changelog.d"

if [ ! -f "$changelog" ]; then
	echo "$0: no CHANGELOG.md at $root" >&2
	exit 1
fi
if [ ! -d "$dir" ]; then
	echo "$0: no changelog.d/ at $root — nothing to collect" >&2
	exit 1
fi

# Every file git tracks there but README.md, as names relative to changelog.d/.
# Tracked, because that is what the per-PR gate judged and what the release
# commit ships: a .DS_Store or an editor's swap file is local noise, not an
# entry, and a fragment nobody committed was never in a pull request. A
# misnamed or nested tracked file is not skipped: skipping it is how an entry
# would be dropped from a release without anyone being told.
if ! tracked=$(git -C "$root" ls-files -- changelog.d/); then
	echo "$0: cannot list changelog.d/ with git ls-files; nothing was written" >&2
	exit 1
fi
names=$(printf '%s\n' "$tracked" | sed -n 's|^changelog\.d/||p' | grep -vxF README.md || true)

if [ -z "$names" ]; then
	echo "$0: changelog.d/ holds no fragments — a release with nothing to collect has no body" >&2
	exit 1
fi

# Sorted by issue number, numerically, then by slug. A name that is not a
# fragment sorts anywhere; it is refused below before the order matters.
names=$(printf '%s\n' "$names" | LC_ALL=C sort -t- -k1,1n -k2)

# One argument per name. Fragment names carry no whitespace or glob characters
# (the name rule forbids them), and anything else is refused by the checker
# under its own name either way.
nested=$(printf '%s\n' "$names" | grep / || true)
if [ -n "$nested" ]; then
	printf '%s\n' "$nested" | sed 's|^|changelog.d/|; s|$|: a fragment lives directly in changelog.d/, not below it|' >&2
	echo "$0: refused — every file in changelog.d/ but README.md must be a fragment (see changelog.d/README.md); nothing was written" >&2
	exit 1
fi
set -f
# shellcheck disable=SC2086
if ! (cd "$dir" && sh "$root/scripts/changelog-fragment-check.sh" $names); then
	set +f
	echo "$0: refused — every file in changelog.d/ but README.md must be a fragment (see changelog.d/README.md); nothing was written" >&2
	exit 1
fi
set +f

if awk -v want="## [$tag]" 'index($0, want) == 1 { found = 1 } END { exit !found }' "$changelog"; then
	echo "$0: CHANGELOG.md already has a ## [$tag] section; nothing was written" >&2
	exit 1
fi
if ! grep -q '^## \[Unreleased\]' "$changelog"; then
	echo "$0: CHANGELOG.md has no ## [Unreleased] heading to write the section under; nothing was written" >&2
	exit 1
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# The section, built whole before CHANGELOG.md is touched. Each fragment's
# entry is everything after its heading, with blank lines trimmed at both ends.
# A line's trailing \r is dropped first and \r counts as whitespace, as it does
# to the checker, so a CRLF fragment's blank lines are blank here too and
# CHANGELOG.md gains no \r. Each fragment whose heading matched is recorded in
# $tmp/emitted, for the postcondition below.
: >"$tmp/emitted"
{
	printf '## [%s] - %s\n' "$tag" "$date"
	for section in Added Changed Deprecated Removed Fixed Security Documented Repository "Known limits"; do
		body=$(printf '%s\n' "$names" | while IFS= read -r name; do
			awk -v want="### $section" -v name="$name" -v emitted="$tmp/emitted" '
				{ sub(/\r+$/, "") }
				!headed && /[^ \t\r]/ {
					headed = 1
					line = $0
					sub(/[ \t\r]+$/, "", line)
					if (line != want) exit
					print name >>emitted
					next
				}
				headed { body[++n] = $0 }
				END {
					first = 1; last = n
					while (first <= n && body[first] !~ /[^ \t\r]/) first++
					while (last >= first && body[last] !~ /[^ \t\r]/) last--
					for (i = first; i <= last; i++) print body[i]
				}
			' "$dir/$name"
		done)
		[ -n "$body" ] || continue
		printf '\n### %s\n\n%s\n' "$section" "$body"
	done
} >"$tmp/section"

# Every fragment the checker passed was written exactly once. The checker and
# the extractor above are two readings of one shape; if they ever disagree, a
# fragment would be deleted below without reaching the section, so a mismatch
# refuses, loudly, before anything is written.
printf '%s\n' "$names" | LC_ALL=C sort >"$tmp/want"
LC_ALL=C sort "$tmp/emitted" >"$tmp/got"
if ! cmp -s "$tmp/want" "$tmp/got"; then
	echo "$0: refused — these fragments passed the checker but were not each collected exactly once:" >&2
	cat "$tmp/want" "$tmp/got" | LC_ALL=C sort | uniq -u | sed 's|^|  changelog.d/|' >&2
	LC_ALL=C sort "$tmp/got" | uniq -d | sed 's|^|  changelog.d/|' >&2
	echo "This is a bug in scripts/changelog-collect.sh, not in the fragment; nothing was written." >&2
	exit 1
fi

# Spliced in directly under the Unreleased block: before the first `## [`
# heading after it, or before the link footnotes if Unreleased is the last
# section. Fences are tracked, as release-notes.sh tracks them, so a quoted
# heading inside Unreleased cannot pull the section up into it.
awk -v section="$tmp/section" '
	function emit() {
		while ((getline line < section) > 0) print line
		print ""
		done = 1
	}
	/^```/ { fence = !fence }
	!fence && !done && seen && (/^## \[/ || /^\[(Unreleased|v?[0-9]+\.[0-9]+\.[0-9]+)\]:/) { emit() }
	!fence && /^## \[Unreleased\]/ { seen = 1 }
	{ print }
	END { if (!done) { print ""; emit() } }
' "$changelog" >"$tmp/CHANGELOG.md"

cat "$tmp/CHANGELOG.md" >"$changelog"
printf '%s\n' "$names" | while IFS= read -r name; do
	rm -- "$dir/$name"
done

count=$(printf '%s\n' "$names" | wc -l | tr -d ' ')
echo "collected $count fragment(s) into ## [$tag] - $date in CHANGELOG.md, and deleted them."
echo "Next: write the section's opening prose, and the [$tag] link footnote."
