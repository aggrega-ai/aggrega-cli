#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# release-changes.sh — prints the text of one release's section of CHANGELOG.md,
# for the "Changes" part of the release page.
#
#   release-changes.sh TAG CHANGELOG_FILE
#
# The section is the lines after the heading "## [X.Y.Z] - ..." (TAG without its
# leading v) up to the next line starting "## ", with blank lines at either end
# dropped. The heading matches the version exactly, so the section of 0.10.3 is
# never taken for the tag v0.10.30.
#
# Exit 0 with nothing printed when the file or the section is absent: the release
# page then carries no Changes part. Exit 1, naming the tag and the offending line
# on standard error, when the section holds an HTML comment.
#
# The comment check is a deliberate one-line cross-check of a rule the repository's
# other gates already hold: a released section never carries an HTML comment, so
# one reaching the release page would be text that was never meant to be public.

set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "usage: release-changes.sh TAG CHANGELOG_FILE" >&2
  exit 2
fi
tag="$1"
file="$2"

[ -f "$file" ] || exit 0

section="$(awk -v head="## [${tag#v}] - " '
  index($0, head) == 1 { in_section = 1; next }
  in_section && index($0, "## ") == 1 { exit }
  in_section { print }
' "$file")"

# Drop the blank lines at either end.
section="$(printf '%s\n' "$section" | sed -e '/./,$!d' | sed -e ':a' -e '/^[[:space:]]*$/{$d;N;ba' -e '}')"
[ -n "$section" ] || exit 0

if line="$(printf '%s\n' "$section" | grep -m1 -F -e '<!--')"; then
  echo "release-changes: the CHANGELOG.md section of $tag holds an HTML comment, which a released section never carries: $line" >&2
  exit 1
fi

printf '%s\n' "$section"
