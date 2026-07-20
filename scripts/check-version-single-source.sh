#!/usr/bin/env bash
# aggrega-cli/scripts/check-version-single-source.sh
#
# Guards the single home of the build-provenance version recipe. Three outputs
# must carry the SAME version and short commit so they never disagree: the
# shipped aggrega binary, the embedded guests' describe() self-report, and the
# CLI that generates the embedded plugin entities. If those three derived the
# version by separate `git describe` invocations, a later edit to one invocation
# would silently fork the version and an embedded entity could advertise a
# different version than the binary that carries it.
#
# The recipe therefore lives in exactly one file — scripts/lib/build-version.sh —
# and the three scripts source it instead of invoking git themselves. This guard
# asserts that arrangement holds:
#   1. each of the three scripts sources the shared helper, and
#   2. none of the three re-introduces an inline `git describe` or
#      `git rev-parse --short` version derivation, and
#   3. the working-tree-marked (--dirty) provenance recipe appears in no other
#      build script — only in the shared helper.
#
# Out of scope by design: the committed hub-catalog generator and the
# overlay-consistency check derive a deliberately SEPARATE version without the
# --dirty marker, so committed catalog entities stay clean regardless of an
# edited tree; the container-image builder derives an image tag. Those are
# distinct recipes for distinct outputs, keyed differently from the --dirty
# binary-provenance recipe guarded here, and this check does not fold them in.
#
# Exit 0 when the recipe has one home; exit 1 (naming each offending script and
# line) otherwise. Deterministic and offline.
set -euo pipefail
cd "$(dirname "$0")/.."          # aggrega-cli module root

HELPER="scripts/lib/build-version.sh"
SELF="scripts/check-version-single-source.sh"
GUARDED=(
  scripts/build.sh
  scripts/build-embedded-plugins.sh
  scripts/gen-embedded-plugin-entities.sh
)

declare -a violations=()

[ -f "$HELPER" ] || { echo "check-version-single-source: shared helper missing: $HELPER" >&2; exit 1; }

# inline_recipe_hits FILE — prints "LINE: text" for each non-comment line that
# invokes git to derive a version (a `git describe` or `git rev-parse --short`),
# ignoring the line that sources the shared helper. Prose mentions of the recipe
# in header comments are skipped.
inline_recipe_hits() {
  awk '
    /^[[:space:]]*#/                 { next }   # comment line
    /lib\/build-version\.sh/         { next }   # the source line
    /git[[:space:]]+describe/ || /git[[:space:]]+rev-parse[[:space:]]+--short/ {
      printf "%d: %s\n", NR, $0
    }
  ' "$1"
}

for f in "${GUARDED[@]}"; do
  if [ ! -f "$f" ]; then
    echo "check-version-single-source: guarded script missing: $f" >&2
    exit 1
  fi
  if ! grep -q 'lib/build-version\.sh' "$f"; then
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
# Scan every script under scripts/ except the helper (its home) and this guard,
# skipping comment lines. The clean-tree catalog recipe (no --dirty) and the
# container-image tag do not match this pattern and are intentionally excluded.
while IFS= read -r script; do
  case "$script" in "$HELPER"|"$SELF") continue;; esac
  while IFS= read -r hit; do
    [ -n "$hit" ] || continue
    echo "VIOLATION  $script:$hit — stray binary-provenance recipe; source $HELPER instead"
    violations+=("$script:$hit")
  done < <(
    awk '
      /^[[:space:]]*#/ { next }
      /git[[:space:]]+describe[[:space:]]+--tags[[:space:]]+--always[[:space:]]+--dirty/ {
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
