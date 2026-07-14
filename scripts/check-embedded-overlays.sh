#!/usr/bin/env bash
# aggrega-cli/scripts/check-embedded-overlays.sh
#
# Asserts that every embedded plugin declared in the embedded-plugin manifest is
# consistent with the sibling aggrega-hub catalog inputs and outputs. For each
# manifest entry it checks:
#   (a) a matching authored overlay is present under plugin-overlays/,
#   (b) its embedded assets (the gzip-compressed guest and its describe()
#       descriptor) are present, and
#   (c) the committed core-plugin catalog carries a version directory matching
#       the current compiler build version.
#
# This guard is READ-ONLY on aggrega-hub: it inspects overlay and catalog
# presence only and never writes there, and it never regenerates the catalog.
# The catalog regeneration is a separate, deliberate release/maintenance step.
#
# Checks (a) and (b) are hard failures in every mode: a manifest entry with no
# overlay or no built artifact is a real drift between the declarative source and
# its inputs/outputs. Check (c) — catalog version currency — is a
# release/publish-time concern: the committed catalog is regenerated deliberately
# at publish points and legitimately trails HEAD during ordinary development, so
# it is a hard failure only under --release and an informational note otherwise.
#
# Usage:
#   check-embedded-overlays.sh            # (a)/(b) hard; (c) informational note
#   check-embedded-overlays.sh --release  # (a)/(b)/(c) all hard
#
# Exit 0 when consistent; exit 1 (naming each offending entry) otherwise.
# Deterministic and offline.
set -euo pipefail
cd "$(dirname "$0")/.."          # aggrega-cli module root
UMBRELLA="$(cd ../ && pwd)"
HUB="$UMBRELLA/aggrega-hub"

RELEASE=0
for arg in "$@"; do
  case "$arg" in
    --release) RELEASE=1 ;;
    *) echo "check-embedded-overlays: unknown argument: $arg" >&2; exit 2 ;;
  esac
done

MANIFEST="internal/wasm/embedded/embedded-plugins.tsv"
EMBED_DIR="internal/wasm/embedded/plugins"
OVERLAY_DIR="$HUB/plugin-overlays"
CATALOG_DIR="$HUB/aggrega-catalog/plugin"
[ -f "$MANIFEST" ] || { echo "check-embedded-overlays: manifest not found: $MANIFEST" >&2; exit 1; }
[ -d "$HUB" ] || { echo "check-embedded-overlays: aggrega-hub not found at $HUB" >&2; exit 1; }

# The catalog version is the compiler build version without the working-tree
# --dirty marker, matching how the catalog generator stamps its version
# directories: uncommitted edits do not change the expected version.
BUILD_VER="$(git describe --tags --always 2>/dev/null || echo dev)"; BUILD_VER="${BUILD_VER#v}"

declare -a missing=()
declare -a stale_catalog=()

while IFS=$'\t' read -r socket name sourcePath toolchain extraRoots; do
  case "$socket" in ''|'#'*) continue;; esac
  entry_ok=1

  # (a) authored overlay present.
  overlay="$OVERLAY_DIR/$socket/$name.yaml"
  if [ ! -f "$overlay" ]; then
    echo "MISSING  $socket/$name — no overlay at plugin-overlays/$socket/$name.yaml"
    missing+=("$socket/$name"); entry_ok=0
  fi

  # (b) embedded assets present.
  wasmgz="$EMBED_DIR/$name.wasm.gz"
  desc="$EMBED_DIR/$name.describe.json"
  if [ ! -f "$wasmgz" ]; then
    echo "MISSING  $socket/$name — no embedded artifact $wasmgz"
    missing+=("$socket/$name"); entry_ok=0
  fi
  if [ ! -f "$desc" ]; then
    echo "MISSING  $socket/$name — no describe() descriptor $desc"
    missing+=("$socket/$name"); entry_ok=0
  fi

  # (c) committed catalog carries a version directory for the current build.
  # Absence is loud only under --release; between releases it is expected.
  catver="$CATALOG_DIR/$socket/$name/$BUILD_VER/plugin.yaml"
  if [ ! -f "$catver" ]; then
    stale_catalog+=("$socket/$name")
    [ "$RELEASE" = 1 ] && { echo "STALE    $socket/$name — no committed catalog entry for version $BUILD_VER"; entry_ok=0; }
  fi

  [ "$entry_ok" = 1 ] && echo "ok       $socket/$name"
done < "$MANIFEST"

status=0
if [ "${#missing[@]}" -gt 0 ]; then
  echo
  echo "embedded plugins missing an overlay or artifact: ${missing[*]}"
  echo "author the overlay under aggrega-hub/plugin-overlays/, and rebuild the"
  echo "embedded guests (go generate ./internal/wasm/embedded/... or ./scripts/build.sh)"
  status=1
fi

if [ "${#stale_catalog[@]}" -gt 0 ]; then
  echo
  if [ "$RELEASE" = 1 ]; then
    echo "committed core-plugin catalog is not current for build $BUILD_VER: ${stale_catalog[*]}"
    echo "regenerate the catalog before publishing: scripts/gen-core-plugin-catalog.sh"
    status=1
  else
    echo "note: committed core-plugin catalog trails build $BUILD_VER (${stale_catalog[*]})"
    echo "note: this is expected between releases; the catalog is regenerated at publish time."
  fi
fi

if [ "$status" -eq 0 ]; then
  echo
  echo "embedded plugins consistent with overlays and catalog"
fi
exit "$status"
