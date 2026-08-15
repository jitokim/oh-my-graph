#!/bin/sh
# Build a release body for one tag: CHANGELOG.md's section for it, plus a
# Contributors line derived from git.
#
# Why this exists: without it goreleaser writes the body itself, from the commit
# subjects since the previous tag. v0.8.0 shipped that way and the result was
# six SHAs — with the headline of that release, this project's first outside
# contribution, sitting as one unattributed line among them. A commit list
# cannot say who wrote something or why it matters.
#
# Both halves are DETERMINISTIC, and that is the point of each:
#
#   - the prose is CHANGELOG.md's own section, written by hand in the release PR
#     and reviewed like any other change. `TestChangelogSectionHasSubstance`
#     refuses an empty one before a tag is ever pushed.
#   - the credit is computed from `git log`, never remembered. Squash-merging a
#     pull request keeps the contributor as the commit AUTHOR, so the range
#     carries who wrote what whoever presses merge. Forgetting to thank someone
#     stops being possible.
#
# Author identity is resolved to a GitHub login through the API rather than
# printed from the commit trailer: a commit's email is often a work address, and
# a release page is not the place to publish one.
#
# Usage: scripts/release-notes.sh v0.8.0
set -eu

if [ $# -ne 1 ]; then
	echo "usage: $0 <tag>" >&2
	exit 2
fi

tag=$1
version=${tag#v}
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
changelog="$root/CHANGELOG.md"

# --- the prose ---------------------------------------------------------------
# Everything after the `## [vX.Y.Z]` heading and before the next `## [` one.
# Anchored on "## [" rather than "## " so a section's own `## ` subheading
# cannot end it early.
notes=$(awk -v want="## [v$version]" '
	index($0, want) == 1 { collecting = 1; next }
	collecting && index($0, "## [") == 1 { exit }
	collecting { print }
' "$changelog" | awk 'NF { seen = 1 } seen' |
	awk '{ line[NR] = $0 } END { last = 0; for (i = 1; i <= NR; i++) if (line[i] ~ /[^ \t]/) last = i; for (i = 1; i <= last; i++) print line[i] }')

if [ -z "$notes" ]; then
	echo "$0: CHANGELOG.md has no section for v$version" >&2
	exit 1
fi
printf '%s\n' "$notes"

# --- the credit --------------------------------------------------------------
# Everything below is best-effort: a release must not fail because the API was
# unreachable. A missing Contributors line is a smaller harm than a release that
# does not exist, and the prose above already stands on its own.
previous=$(git -C "$root" describe --tags --abbrev=0 "$tag^" 2>/dev/null) || exit 0
owner=${GITHUB_REPOSITORY%%/*}
repo=${GITHUB_REPOSITORY:-jitokim/oh-my-graph}
command -v gh >/dev/null 2>&1 || exit 0

logins=$(
	git -C "$root" log --format=%H "$previous..$tag" | while read -r sha; do
		gh api "repos/$repo/commits/$sha" --jq '.author.login // empty' 2>/dev/null || true
	done | sort -u | grep -v "^${owner:-jitokim}$" || true
)
[ -n "$logins" ] || exit 0

printf '\n### Contributors\n\n'
printf 'This release includes work from %s.\n' \
	"$(printf '%s\n' "$logins" | sed 's|^|@|' | paste -sd ', ' -)"
