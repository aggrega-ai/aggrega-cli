#!/usr/bin/env bash
# aggrega-cli/scripts/check-version-single-source.sh
#
# Guards the single home of the build-provenance version recipe. The outputs of
# the scripts in GUARDED below must carry the SAME version and short commit so
# they never disagree: the shipped aggrega binary, the embedded guests'
# describe() self-report, the CLI that generates the embedded plugin entities,
# and the installable MCP bundle a release attaches beside the binaries. If they
# derived the version by separate `git describe` invocations, a later edit to
# one invocation would silently fork the version: an embedded entity could
# advertise a different version than the binary that carries it, or a release's
# bundle a different version than its binaries.
#
# The recipe therefore lives in exactly one file — scripts/lib/build-version.sh —
# and every guarded script sources it instead of invoking git itself. The
# GUARDED list is the only statement of which scripts those are; no count of
# them is written here, because a count beside the list goes stale the next
# time the list grows. This guard asserts that arrangement holds:
#   1. each guarded script sources the shared helper — on a line whose first
#      word is `.` or `source` and whose argument is a literal, unquoted path
#      ending in the helper's path; a comment naming the helper does not count,
#      and
#   2. none of them re-introduces an inline `git describe` or
#      `git rev-parse --short` version derivation — spelled plainly or with
#      git's own options ahead of the subcommand (`git -C <dir> describe`,
#      `git rev-parse --verify --short`), and
#   3. the working-tree-marked (--dirty) provenance recipe appears in no other
#      script under this module's scripts/ — only in the shared helper, and in
#      the one script exempted by name below.
#
# Out of scope by design: the committed hub-catalog generator and the
# overlay-consistency check derive a deliberately SEPARATE version without the
# --dirty marker — distinct recipes for distinct outputs, keyed differently from
# the --dirty binary-provenance recipe guarded here. The container-image builder
# runs the --dirty recipe itself, for the default tag of the image it builds: an
# output none of the guarded scripts stamps. It is exempted from the third
# check by name, so the exemption is read where the check is. This check folds
# none of them in.
#
# Note what that separation does NOT buy, because the obvious reading is wrong:
# it confers no immunity from an edited tree. The generator's version stamps
# only the throwaway binary it builds in a temp directory to run the generation
# with; a committed entity's version — and hence its version directory — is the
# guest's own describe() self-report and does not come from this recipe at all,
# so a guest built from a tree with uncommitted edits still stamps a "-dirty"
# version directory. The overlay check derives its value to COMPARE against a
# committed version directory rather than to stamp anything. Neither feeds the
# binary-provenance chain guarded above, which is why they stay out of scope.
#
# Exit 0 when the recipe has one home; exit 1 (naming each offending script and
# line) otherwise. Deterministic and offline. The release workflow's build job
# (.github/workflows/publish-plugins-sign.yml) runs it before anything is built,
# on the tree the run is about to build.
set -euo pipefail
cd "$(dirname "$0")/.."          # aggrega-cli module root

HELPER="scripts/lib/build-version.sh"
SELF="scripts/check-version-single-source.sh"
# The one script the third check exempts, for the reason the header gives: it
# derives the default tag of the container image, not a stamped version.
IMAGE_TAG_SCRIPT="scripts/build-tooling-image.sh"
GUARDED=(
  scripts/build.sh
  scripts/build-embedded-plugins.sh
  scripts/gen-embedded-plugin-entities.sh
  scripts/build-mcp-bundle.sh
)

declare -a violations=()

# A line that sources the helper: `.` or `source` as its first word, then a
# literal path ending in $HELPER, then the end of the line or a separator.
# Matching the bare path anywhere would let a comment that names the helper
# stand in for a removed source line. The path is unquoted and variable-free
# because that is the only form the guarded scripts use; a script that needs a
# quoted or "$dir"/-prefixed form widens this pattern deliberately. The path is
# derived from $HELPER rather than spelled out again here.
SOURCES_HELPER_RE="^[[:space:]]*(\\.|source)[[:space:]]+([A-Za-z0-9._/-]*/)?${HELPER//./\\.}([[:space:];].*)?\$"

[ -f "$HELPER" ] || { echo "check-version-single-source: shared helper missing: $HELPER" >&2; exit 1; }

# inline_recipe_hits FILE — prints "LINE: text" for each non-comment line that
# invokes git to derive a version (a `git describe` or `git rev-parse --short`),
# ignoring the line that sources the shared helper. Prose mentions of the recipe
# in header comments are skipped. Any words may stand between `git` and the
# subcommand, and between `rev-parse` and `--short`, so a derivation that names
# a directory with -C, sets a configuration value with -c, or puts another
# option first is still found: each of those is a second derivation whose
# abbreviation length, dirty marker or tag match can differ from the helper's.
inline_recipe_hits() {
  awk '
    /^[[:space:]]*#/                 { next }   # comment line
    /lib\/build-version\.sh/         { next }   # the source line
    /git([[:space:]]+[^[:space:]]+)*[[:space:]]+describe/ ||
    /git([[:space:]]+[^[:space:]]+)*[[:space:]]+rev-parse([[:space:]]+[^[:space:]]+)*[[:space:]]+--short/ {
      printf "%d: %s\n", NR, $0
    }
  ' "$1"
}

for f in "${GUARDED[@]}"; do
  if [ ! -f "$f" ]; then
    echo "check-version-single-source: guarded script missing: $f" >&2
    exit 1
  fi
  if ! grep -Eq "$SOURCES_HELPER_RE" "$f"; then
    echo "VIOLATION  $f — does not source the shared version helper ($HELPER)"
    violations+=("$f")
  fi
  while IFS= read -r hit; do
    [ -n "$hit" ] || continue
    echo "VIOLATION  $f:$hit — inline version derivation; source $HELPER instead"
    violations+=("$f:$hit")
  done < <(inline_recipe_hits "$f")
done

# The --dirty binary-provenance recipe must appear in no other build script.
# Scan every script under scripts/ except the helper (its home), this guard and
# the image builder exempted above, skipping comment lines. A line is the recipe
# when it runs `git … describe` with --dirty anywhere among its options, whatever
# git's own options or the other describe options around it. The clean-tree
# catalog recipe carries no --dirty and does not match.
while IFS= read -r script; do
  case "$script" in "$HELPER"|"$SELF"|"$IMAGE_TAG_SCRIPT") continue;; esac
  while IFS= read -r hit; do
    [ -n "$hit" ] || continue
    echo "VIOLATION  $script:$hit — stray binary-provenance recipe; source $HELPER instead"
    violations+=("$script:$hit")
  done < <(
    awk '
      /^[[:space:]]*#/ { next }
      /git([[:space:]]+[^[:space:]]+)*[[:space:]]+describe([[:space:]]+[^[:space:]]+)*[[:space:]]+--dirty/ {
        printf "%d: %s\n", NR, $0
      }
    ' "$script"
  )
done < <(find scripts -maxdepth 2 -name '*.sh' | sort)

if [ "${#violations[@]}" -gt 0 ]; then
  echo
  echo "version recipe is not single-sourced; route the scripts above through $HELPER"
  exit 1
fi

echo "version recipe single-sourced: ${GUARDED[*]} all derive it from $HELPER"
