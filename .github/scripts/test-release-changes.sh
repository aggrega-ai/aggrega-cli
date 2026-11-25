#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# test-release-changes.sh — checks release-changes.sh against fixture changelogs:
# a tag with a section, a tag without one, a section holding an HTML comment, a
# tag that is only a prefix of another, and an absent file.
#
# Exit: 0 when every assertion holds; 1 with the failing assertions named.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$HERE/release-changes.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

FAIL=0
fail() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$*"; }
pass() { printf 'PASS %s\n' "$*"; }

# expect NAME WANT_STATUS WANT_STDOUT WANT_STDERR TAG FILE
expect() {
  local name="$1" want_status="$2" want_out="$3" want_err="$4" tag="$5" file="$6"
  local out err status
  out="$("$SCRIPT" "$tag" "$file" 2>"$WORK/err")"
  status=$?
  err="$(cat "$WORK/err")"
  if [ "$status" -ne "$want_status" ]; then
    fail "$name: exit $status, want $want_status"
  elif [ "$out" != "$want_out" ]; then
    fail "$name: stdout [$out], want [$want_out]"
  elif [ "$err" != "$want_err" ]; then
    fail "$name: stderr [$err], want [$want_err]"
  else
    pass "$name"
  fi
}

cat >"$WORK/section.md" <<'MD'
# Changelog

## [0.10.30] - 2026-12-01

- Thirty.

## [0.10.3] - 2026-11-24


- Added one thing.
- Fixed another.

  Indented detail.


## [0.10.2] - 2026-11-20

- Older.
MD

cat >"$WORK/marker.md" <<'MD'
# Changelog

## [0.11.0] - 2026-12-05

- Added a thing.
- Leaked <!-- aggrega-cli abc1234 --> marker.

## [0.10.3] - 2026-11-24

- Older.
MD

expect "section" 0 \
  $'- Added one thing.\n- Fixed another.\n\n  Indented detail.' "" \
  v0.10.3 "$WORK/section.md"
expect "section, longer tag" 0 "- Thirty." "" v0.10.30 "$WORK/section.md"
expect "no section" 0 "" "" v0.9.9 "$WORK/section.md"
expect "section with a comment" 1 "" \
  "release-changes: the CHANGELOG.md section of v0.11.0 holds an HTML comment, which a released section never carries: - Leaked <!-- aggrega-cli abc1234 --> marker." \
  v0.11.0 "$WORK/marker.md"
expect "other section unaffected by a comment elsewhere" 0 "- Older." "" v0.10.3 "$WORK/marker.md"
expect "absent file" 0 "" "" v0.10.3 "$WORK/missing.md"

if [ "$FAIL" -ne 0 ]; then
  printf '%s assertion(s) failed\n' "$FAIL"
  exit 1
fi
echo "test-release-changes: passed"
