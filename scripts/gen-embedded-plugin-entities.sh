#!/usr/bin/env bash
# aggrega-cli/scripts/gen-embedded-plugin-entities.sh
#
# Generates the complete Plugin entity for every embedded guest and writes it
# beside the guest under internal/wasm/embedded/plugins/<name>.plugin.yaml, so
# the full binary embeds a complete catalog view of its own core plugins. A
# no-local-catalog build then resolves core providers (compose, helm, terraform,
# …) from the embedded entities at render time, with no registry round-trip.
#
# The entity is emitted through the SAME path the committed hub catalog uses —
# `plugin gen-catalog --from-embedded` merging each guest's describe()
# self-report with its authored overlay (identity, runtime limits, provider
# block) — so the embedded <name>.plugin.yaml is BYTE-IDENTICAL to the entity
# scripts/gen-core-plugin-catalog.sh writes to the hub for the same commit. The
# single emit is not forked; this script relocates gen-catalog's per-coordinate
# output to the flat, name-based embed layout beside the guest's describe.json.
#
# The build version is single-sourced with the shipped binary's provenance stamp
# (scripts/lib/build-version.sh), so a generated entity's version always matches
# the binary that embeds it. Committed embedded entities stay clean by
# regenerating from a clean working tree — not by stripping the working-tree
# --dirty marker. The overlays are sourced from the sibling aggrega-hub, exactly
# as gen-core-plugin-catalog.sh does; a hub-less checkout cannot generate the
# entities and this script fails loudly (the embedded guests still build; only
# the offline entity view is unavailable, the same coupling the hub-catalog
# generator already carries).
#
# Usage:
#   gen-embedded-plugin-entities.sh
#
# Exit non-zero on the first failing step.
set -euo pipefail
cd "$(dirname "$0")/.."          # aggrega-cli module root
CLI_ROOT="$(pwd)"
UMBRELLA="$(cd ../ && pwd)"
HUB="$UMBRELLA/aggrega-hub"
OVERLAY_DIR="$HUB/plugin-overlays"

MANIFEST="internal/wasm/embedded/embedded-plugins.tsv"
EMBED_DIR="internal/wasm/embedded/plugins"
[ -f "$MANIFEST" ] || { echo "gen-embedded-plugin-entities: manifest not found: $MANIFEST" >&2; exit 1; }
[ -d "$OVERLAY_DIR" ] || { echo "gen-embedded-plugin-entities: overlay dir not found: $OVERLAY_DIR (the sibling aggrega-hub is required to generate embedded entities)" >&2; exit 1; }

echo "=== build aggrega (provenance version stamp) ==="
. scripts/lib/build-version.sh
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
BIN="$TMP/aggrega"
GOWORK=off go build -ldflags "-X main.version=$_ver -X main.commit=$_sha" -o "$BIN" ./cmd/aggrega
echo "built $BIN (version $_ver)"

OUT="$TMP/catalog"
echo "=== generate embedded plugin entities ==="
"$BIN" plugin gen-catalog --from-embedded \
  --overlay "$OVERLAY_DIR" \
  --output "$OUT"

# Relocate each generated per-coordinate entity to the flat, name-based embed
# layout beside its guest. gen-catalog writes <out>/<pluginType>/<name>/<ver>/
# plugin.yaml; the embed layout is plugins/<name>.plugin.yaml. The bytes are
# copied verbatim, so the embedded entity is byte-identical to what the hub
# generator writes for the same commit.
declare -a written=()
while IFS=$'\t' read -r socket name sourcePath toolchain extraRoots; do
  case "$socket" in ''|'#'*) continue;; esac
  src="$(echo "$OUT/$socket/$name"/*/plugin.yaml)"
  if [ ! -f "$src" ]; then
    echo "gen-embedded-plugin-entities: no generated entity for $socket/$name at $OUT/$socket/$name/*/plugin.yaml" >&2
    exit 1
  fi
  cp "$src" "$EMBED_DIR/$name.plugin.yaml"
  written+=("$name")
done < "$MANIFEST"

echo "=== wrote embedded plugin entities: ${written[*]} ==="
