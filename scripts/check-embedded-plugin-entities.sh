#!/usr/bin/env bash
# aggrega-cli/scripts/check-embedded-plugin-entities.sh
#
# Fails when any core plugin's generated entity beside its guest — under
# internal/wasm/embedded/plugins/<name>.plugin.yaml for a plugin the binary
# embeds, under internal/wasm/embedded/published-only/ for one it does not — is
# missing, older than an input it is generated from — the guest's describe() descriptor (the
# machine-derived contract half) or the authored overlay (the identity / runtime
# / provider half) — or declaring a version its guest does not report. The
# embedded entity is what a no-local-catalog build resolves a core provider from
# at render time, so a stale or missing entity would route a core provider off an
# out-of-date contract or fail to resolve it at all.
#
# The embedded entities are reproducible build outputs regenerated from the
# guests and overlays (see gen-embedded-plugin-entities.sh, run by
# scripts/build.sh after the guests are current), not tracked files. This guard
# is the entity-axis counterpart of check-embedded-guests.sh: run it after a
# build to assert the embedded entities are current with their inputs.
#
# The check has two halves, and they answer different questions:
#
#   - MODIFICATION TIME — an entity older than an input is stale. This is the
#     cheap regeneration trigger, the same posture the guest check uses. It is
#     deliberately not a whole-file digest comparison: the entity carries a
#     build-provenance version stamp, so a digest test would report false
#     staleness on any tree whose version string moved without an input change.
#
#   - VERSION AGREEMENT — the entity's metadata.version must be the version its
#     guest's describe() descriptor reports. This is the property that actually
#     matters, asserted directly rather than inferred from file ordering, and it
#     is what a resolution keyed on the entity's version depends on. Comparing
#     exactly this one field is immune to the false staleness a whole-file digest
#     would suffer, because the entity does not stamp a version of its own: the
#     generator copies the guest's self-reported version verbatim. So the two can
#     disagree only when the entity was generated against a different build of
#     the guest than the one now on disk — which modification time alone does not
#     detect whenever the entity is the newer file.
#
# For a plugin the binary does not embed, the binary carries a copy of its
# entity (internal/wasm/embedded/published-selection/<name>.plugin.yaml) in place
# of the guest, so a copy that is missing or differs by a byte from the entity
# beside the guest is stale too.
#
# The overlays live in the sibling aggrega-hub. When that sibling is absent (a
# clone of this repository alone, which cannot generate entities anyway), the
# overlay half of the freshness check is skipped with a note and only presence +
# descriptor currency is asserted.
#
# Exit 0 when every entity is fresh; exit 1 (naming each stale or missing entity
# and the rebuild command) otherwise. Deterministic and offline.
set -euo pipefail
cd "$(dirname "$0")/.."          # aggrega-cli module root
SIBLINGS_DIR="$(cd ../ && pwd)"
OVERLAY_DIR="$SIBLINGS_DIR/aggrega-hub/plugin-overlays"

# The core-plugin list's one reader: core_plugin_rows and the artifact directories.
. scripts/lib/core-plugins.sh
rows="$(core_plugin_rows)" || exit 1
REBUILD_HINT="rebuild with: ./scripts/gen-embedded-plugin-entities.sh   (or ./scripts/build.sh, which regenerates when stale)"

have_overlays=1
[ -d "$OVERLAY_DIR" ] || have_overlays=0

declare -a stale=()

# newer_input ENTITY INPUT — echoes INPUT when it is newer than ENTITY (a
# staleness trigger), nothing otherwise. A missing INPUT is not a staleness
# trigger here (the descriptor's own presence is guarded by the guest check; a
# missing overlay is the overlay-consistency check's concern).
newer_input() {
  local entity="$1" input="$2"
  [ -f "$input" ] || return 0
  find "$input" -newer "$entity" -print -quit 2>/dev/null
}

# entity_version ENTITY — echoes the entity's metadata.version. The entity is a
# generated file with one such key at a fixed indent, so the field is read
# directly rather than through a YAML parser the other build scripts do not need.
entity_version() {
  sed -n 's/^  version:[[:space:]]*//p' "$1" | head -1
}

# guest_version DESCRIPTOR — echoes the version the guest's describe() descriptor
# reports. Read for the diagnostic only; the assertion itself is a fixed-string
# containment test, so a version carrying regex metacharacters cannot widen it.
guest_version() {
  sed -n 's/.*"version":"\([^"]*\)".*/\1/p' "$1" | head -1
}

while IFS=$'\x1f' read -r socket name sourcePath toolchain extraRoots embed dir; do
  [ -n "$socket" ] || continue
  entity="$dir/$name.plugin.yaml"
  if [ ! -f "$entity" ]; then
    echo "STALE  $socket/$name — missing entity $entity"
    stale+=("$name")
    continue
  fi
  newer=""
  desc="$dir/$name.describe.json"
  newer="$(newer_input "$entity" "$desc")"
  if [ -z "$newer" ] && [ "$have_overlays" = 1 ]; then
    newer="$(newer_input "$entity" "$OVERLAY_DIR/$socket/$name.yaml")"
  fi
  if [ -n "$newer" ]; then
    echo "STALE  $socket/$name — input newer than entity: $newer"
    stale+=("$name")
    continue
  fi
  # Version agreement: the entity must declare the version its guest reports.
  # Checked even when the entity is the newer file, which is exactly the case
  # modification time cannot see.
  ev="$(entity_version "$entity")"
  if [ -z "$ev" ]; then
    echo "STALE  $socket/$name — entity declares no metadata.version: $entity"
    stale+=("$name")
    continue
  fi
  if [ -f "$desc" ] && ! grep -qF "\"version\":\"$ev\"" "$desc"; then
    echo "STALE  $socket/$name — entity declares version $ev but its guest reports $(guest_version "$desc")"
    stale+=("$name")
    continue
  fi
  # The copy the binary carries of the entity of a plugin it does not embed must
  # be the entity beside the guest, byte for byte.
  if [ "$embed" = no ]; then
    carried="$CORE_PLUGIN_SELECTION_DIR/$name.plugin.yaml"
    if [ ! -f "$carried" ]; then
      echo "STALE  $socket/$name — missing carried entity $carried"
      stale+=("$name")
      continue
    fi
    if ! cmp -s "$entity" "$carried"; then
      echo "STALE  $socket/$name — carried entity $carried differs from $entity"
      stale+=("$name")
      continue
    fi
  fi
  echo "fresh  $socket/$name"
done <<< "$rows"

if [ "$have_overlays" = 0 ]; then
  echo "note: aggrega-hub sibling absent (no overlays at $OVERLAY_DIR) — overlay-currency half of the entity check skipped"
fi

if [ "${#stale[@]}" -gt 0 ]; then
  echo
  echo "core plugin entities stale or missing: ${stale[*]}"
  echo "$REBUILD_HINT"
  exit 1
fi

echo
echo "all core plugin entities fresh"
