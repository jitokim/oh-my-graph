#!/bin/sh
# Print a release body for one tag: CHANGELOG.md's section for it, plus a
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
# THIS RUNS UNATTENDED ON A TAG PUSH, so whatever it prints is public and cannot
# be un-published. Everything it emits is therefore validated rather than
# trusted, and the failure policy is split deliberately: a missing changelog
# section FAILS (a tag with no written notes should not become an installable
# release), while every step of the credit half is best-effort (a release must
# not fail because an API call did not answer).
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
#
# Anchored on "## [" rather than "## " so a section's own `## ` subheading
# cannot end it early — and fenced blocks are tracked, because the mirror hazard
# is real for this changelog: it quotes code and headings constantly, and a
# fence containing a line that looks like a heading would otherwise truncate the
# release body mid-fence and drop everything after it, silently and with exit 0.
#
# The link-reference footer at the end of the file is excluded the same way a
# heading is: it is not prose, and only the oldest section could ever reach it.
notes=$(awk -v want="## [v$version]" '
	/^```/ { fence = !fence }
	!fence && index($0, want) == 1 { collecting = 1; next }
	collecting && !fence && index($0, "## [") == 1 { exit }
	collecting && !fence && /^\[v?[0-9]+\.[0-9]+\.[0-9]+\]:|^\[Unreleased\]:/ { exit }
	collecting { print }
' "$changelog" | awk 'NF { seen = 1 } seen' |
	awk '{ line[NR] = $0 } END { last = 0; for (i = 1; i <= NR; i++) if (line[i] ~ /[^ \t]/) last = i; for (i = 1; i <= last; i++) print line[i] }')

if [ -z "$notes" ]; then
	echo "$0: CHANGELOG.md has no section for v$version" >&2
	exit 1
fi
printf '%s\n' "$notes"

# --- the credit --------------------------------------------------------------
# Best-effort from here down. Nothing below may fail the release: a missing
# Contributors line is a smaller harm than a release that does not exist, and
# the prose above already stands on its own.
#
# `repo` is defaulted BEFORE `owner` is derived from it. The other order looks
# equivalent and is not: `${GITHUB_REPOSITORY%%/*}` on an unset variable is a
# fatal expansion under `set -u` in a POSIX shell, and /bin/sh on the release
# runner is dash, which enforces it. bash-as-sh permits it, so the bug would
# have been invisible in local testing and fatal on the runner.
repo=${GITHUB_REPOSITORY:-jitokim/oh-my-graph}
owner=${repo%%/*}

if ! previous=$(git -C "$root" describe --tags --abbrev=0 "$tag^" 2>/dev/null); then
	# The genuine first-tag case looks identical to a shallow clone that dropped
	# the history. Say which is suspected rather than vanishing: this feature
	# exists because credit went missing once, so silence is the wrong bias.
	echo "$0: no tag before $tag; skipping the contributors line (a shallow clone looks like this too)" >&2
	exit 0
fi
command -v gh >/dev/null 2>&1 || {
	echo "$0: gh not found; skipping the contributors line" >&2
	exit 0
}

# `.author` is the GitHub ACCOUNT, so `.type` distinguishes a bot from a person
# without pattern-matching a name. Both filters are kept: this repo already has
# dependabot commits in its history, and `[bot]` catches an account the API
# types as a User but which is plainly one (some app identities do this).
#
# A login is then validated against GitHub's own charset before it can be
# printed. That is not belt-and-braces: `gh api` writes an HTTP error body to
# STDOUT and does not apply --jq on the error path, so a 403 or a 5xx would
# otherwise put a JSON blob on the release page as somebody's name.
logins=$(
	git -C "$root" log --format=%H "$previous..$tag" | while read -r sha; do
		gh api "repos/$repo/commits/$sha" \
			--jq 'select(.author.type != "Bot") | .author.login // empty' 2>/dev/null || true
	done | grep -E '^[A-Za-z0-9][A-Za-z0-9-]{0,38}$' | grep -v '\[bot\]$' | sort -u || true
)
[ -n "$logins" ] || exit 0

# Nobody is excluded by name. An earlier draft dropped the repo owner, which is
# right only while the repo is user-owned: transfer it to an org and `owner`
# matches no author, so the maintainer silently starts appearing and the line
# inverts meaning. The suppression below keeps releases quiet in the ordinary
# case, and its worst failure is printing one extra true name.
if [ "$logins" = "$owner" ]; then
	exit 0
fi

# `paste -sd ', '` is wrong here and looks right: -d takes a delimiter LIST that
# it CYCLES, so three names render "@a,@b @c" on both BSD and GNU paste.
printf '\n### Contributors\n\n'
printf 'This release includes work from %s.\n' \
	"$(printf '%s\n' "$logins" | awk 'NR > 1 { printf ", " } { printf "@%s", $0 } END { print "" }')"
