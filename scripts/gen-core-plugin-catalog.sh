#!/usr/bin/env bash
# aggrega-cli/scripts/gen-core-plugin-catalog.sh
#
# Regenerates the committed core-plugin catalog entities from the compiler
# binary's embedded guests and the authored per-plugin overlays. This is a
# release/maintenance-time step, NOT part of the ordinary build or test cycle:
# it writes into the sibling aggrega-hub catalog and is run deliberately when the
# embedded guests or the overlays change and the committed entities need to catch
# up. The tag-driven publish CI performs the same generation before signing and
# publishing the core plugins.
#
# The generated entity's contract fields (pluginType, abiVersion, targets,
# exports, capabilities) come from each guest's describe() self-report; its
# identity, runtime limits, and provider block come from the matching overlay;
# its version is the compiler's own build version. Regenerating over unchanged
# inputs yields byte-identical files (idempotent).
#
# The build version is derived without the working-tree --dirty marker, so a
# regeneration performed with uncommitted edits in the tree still stamps a clean
# version-directory rather than a "-dirty" one; the release CI regenerates at the
# tagged commit where the tree is clean regardless.
set -euo pipefail
cd "$(dirname "$0")/.."          # aggrega-cli
CLI_ROOT="$(pwd)"
UMBRELLA="$(cd ../ && pwd)"
HUB="$UMBRELLA/aggrega-hub"
OVERLAY_DIR="$HUB/plugin-overlays"
OUTPUT_DIR="$HUB/aggrega-catalog/plugin"

[ -d "$OVERLAY_DIR" ] || { echo "gen-core-plugin-catalog: overlay dir not found: $OVERLAY_DIR" >&2; exit 1; }

echo "=== build aggrega (clean version stamp) ==="
_ver="$(git describe --tags --always 2>/dev/null || echo dev)"; _ver="${_ver#v}"
_sha="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
BIN="$(mktemp -d)/aggrega"
GOWORK=off go build -ldflags "-X main.version=$_ver -X main.commit=$_sha" -o "$BIN" ./cmd/aggrega
echo "built $BIN (version $_ver)"

# Remove only the generated socket directories so stale version-dirs do not
# accumulate. plugin-overlays/ (the authored input) and plugin/example-search/
# (an out-of-scope entity) are never touched.
echo "=== clear generated socket dirs ==="
rm -rf "$OUTPUT_DIR/provider" "$OUTPUT_DIR/linter"

echo "=== generate core plugin catalog entities ==="
"$BIN" plugin gen-catalog --from-embedded \
  --overlay "$OVERLAY_DIR" \
  --output "$OUTPUT_DIR"

echo "=== done: entities under $OUTPUT_DIR/{provider,linter} ==="
