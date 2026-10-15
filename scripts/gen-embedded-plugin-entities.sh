#!/usr/bin/env bash
# aggrega-cli/scripts/gen-embedded-plugin-entities.sh
#
# Generates the complete Plugin entity for every core guest and writes it beside
# the guest, in its row's artifact directory: under
# internal/wasm/embedded/plugins/<name>.plugin.yaml for a guest the binary
# embeds, so the full binary embeds a complete catalog view of its own core
# plugins, and under internal/wasm/embedded/published-only/<name>.plugin.yaml for
# one it does not, where the co-located dev catalog and the entity check read it.
# A no-local-catalog build resolves core providers (compose, helm, terraform, …)
# from the embedded entities at render time, with no registry round-trip.
#
# The embedded guests' entities come from `plugin gen-catalog --from-embedded`;
# the others' from core_plugin_generate_unembedded (scripts/lib/core-plugins.sh),
# the one generation of a core plugin the binary does not embed, which the
# core-plugin catalog generator and the release workflow call as well.
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
SIBLINGS_DIR="$(cd ../ && pwd)"
HUB="$SIBLINGS_DIR/aggrega-hub"
OVERLAY_DIR="$HUB/plugin-overlays"

# The core-plugin list's one reader: core_plugin_rows, the artifact directories
# and the generation of the rows the binary does not embed.
. scripts/lib/core-plugins.sh
rows="$(core_plugin_rows)" || exit 1
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
  --output-dir "$OUT"
echo "=== generate the entities of the core plugins the binary does not embed ==="
core_plugin_generate_unembedded "$BIN" "$OVERLAY_DIR" "$OUT"

# Relocate each generated per-coordinate entity to the flat, name-based layout
# beside its guest. gen-catalog writes <out>/<pluginType>/<name>/<ver>/
# plugin.yaml; the layout beside the guest is <dir>/<name>.plugin.yaml. The bytes
# are copied verbatim, so the entity beside each guest is byte-identical to what
# the hub generator writes for the same commit.
declare -a written=()
while IFS=$'\x1f' read -r socket name sourcePath toolchain extraRoots embed dir; do
  [ -n "$socket" ] || continue
  src="$(echo "$OUT/$socket/$name"/*/plugin.yaml)"
  if [ ! -f "$src" ]; then
    echo "gen-embedded-plugin-entities: no generated entity for $socket/$name at $OUT/$socket/$name/*/plugin.yaml" >&2
    exit 1
  fi
  mkdir -p "$dir"
  cp "$src" "$dir/$name.plugin.yaml"
  written+=("$name")
done <<< "$rows"

echo "=== wrote core plugin entities: ${written[*]} ==="
