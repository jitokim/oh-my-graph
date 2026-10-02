#!/bin/sh
# Is each named file a well-formed changelog fragment (ADR 0042, §2.1)?
#
# A fragment is one unreleased CHANGELOG entry, owned by the pull request that
# adds it, at `changelog.d/<issue>-<slug>.md`. Two PRs then never edit a common
# file, which is what lets backlog-batch's lanes share no files (#304).
#
#   name   ^[0-9]+-[a-z0-9]+(-[a-z0-9]+)*\.md$ — the issue number, then a
#          kebab-case slug
#   shape  the first non-blank line is exactly `### <Section>`, from the list
#          below; then at least one prose line (non-blank, not a heading); no
#          other heading outside a fenced block; every fence closed
#
# This is the one shell statement of that rule. The per-PR gate
# (changelog-entry-check.sh) and the release collector (changelog-collect.sh)
# both ask it, and `TestChangelogFragmentRuleAgreesWithTheScript` holds it to
# the Go statement in cmd/oh-my-graph, so the two cannot drift apart.
#
# The file's own basename is judged, so a caller checking content from git (not
# the working tree) writes it to a temporary file of the same name first.
#
# Usage:  scripts/changelog-fragment-check.sh <file>...
# Exit:   0 every file is a fragment   1 at least one is not (each is named)
#         2 usage
set -eu

if [ $# -eq 0 ]; then
	echo "usage: $0 <file>..." >&2
	exit 2
fi

status=0
for file in "$@"; do
	name=$(basename -- "$file")
	if ! printf '%s\n' "$name" | grep -Eq '^[0-9]+-[a-z0-9]+(-[a-z0-9]+)*\.md$'; then
		echo "$file: not a fragment name — want changelog.d/<issue>-<slug>.md, e.g. 304-changelog-fragments.md" >&2
		status=1
		continue
	fi
	if [ ! -f "$file" ]; then
		echo "$file: not a regular file" >&2
		status=1
		continue
	fi
	# The section list is canonical ORDER as well as membership; the collector
	# carries its own copy of the order and the Go side a third, all held
	# together by tests.
	if ! awk -v file="$file" '
		BEGIN {
			n = split("Added|Changed|Deprecated|Removed|Fixed|Security|Documented|Repository|Known limits", list, "|")
			for (i = 1; i <= n; i++) known[list[i]] = 1
			bad = 0
		}
		function refuse(msg) { print file ": " msg > "/dev/stderr"; bad = 1 }
		{
			line = $0
			sub(/[ \t\r]+$/, "", line)
		}
		!headed {
			if (line == "") next
			headed = 1
			if (line !~ /^### /) { refuse("the first non-blank line must be a ### <Section> heading, got: " line); next }
			section = substr(line, 5)
			if (section == "Documentation") { refuse("unknown section \"Documentation\" — this changelog calls it \"Documented\""); next }
			if (!(section in known)) refuse("unknown section \"" section "\" — want one of Added, Changed, Deprecated, Removed, Fixed, Security, Documented, Repository, Known limits")
			next
		}
		/^```/ { fence = !fence; next }
		fence { next }
		line ~ /^#+([ \t]|$)/ { refuse("a fragment holds exactly one heading; write a second fragment for a second section, not: " line); next }
		line ~ /[^ \t]/ { prose++ }
		END {
			if (fence) refuse("a ``` fence is never closed")
			if (!headed) refuse("empty — a fragment is a ### <Section> heading and the entry under it")
			else if (!prose) refuse("a heading with no entry under it")
			exit bad
		}
	' "$file"; then
		status=1
	fi
done
exit $status
