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
# The generated entity's contract fields (pluginType, abiVersion, capabilities,
# exports, contract) come from each guest's describe() self-report; its
# identity, runtime limits, and provider block come from the matching overlay;
# its version is the compiler build that produced that guest, which the guest
# reports through describe(). Regenerating over unchanged inputs yields
# byte-identical files (idempotent).
#
# That version names the version directory each entity is written under, and it
# is the guest's own describe() self-report rather than the version stamped into
# the binary this script builds below. The two agree only when the guests were
# rebuilt at the current commit, so regenerating against older guests stamps the
# older version. Nothing here confers immunity from a dirty working tree either:
# a guest built from a tree with uncommitted edits reports a "-dirty" version and
# therefore stamps a "-dirty" version directory. To stamp a clean version,
# rebuild the guests from a clean tree and then regenerate; the release CI does
# exactly that at the tagged commit.
set -euo pipefail
cd "$(dirname "$0")/.."          # aggrega-cli
CLI_ROOT="$(pwd)"
UMBRELLA="$(cd ../ && pwd)"
HUB="$UMBRELLA/aggrega-hub"
OVERLAY_DIR="$HUB/plugin-overlays"
OUTPUT_DIR="$HUB/aggrega-catalog/plugin"
MANIFEST="internal/wasm/embedded/embedded-plugins.tsv"

[ -d "$OVERLAY_DIR" ] || { echo "gen-core-plugin-catalog: overlay dir not found: $OVERLAY_DIR" >&2; exit 1; }
[ -f "$MANIFEST" ] || { echo "gen-core-plugin-catalog: manifest not found: $MANIFEST" >&2; exit 1; }

# The socket directories this run owns are derived from the embedded-plugin
# manifest's socket column — the same declarative source the --from-embedded
# selection below draws its guests from — so the set cleared and the set reported
# cannot fall behind the set actually written. A list kept by hand here would be
# precisely the parallel list that manifest exists to prevent.
mapfile -t SOCKETS < <(awk -F'\t' '$1 != "" && $1 !~ /^#/ { print $1 }' "$MANIFEST" | sort -u)
[ "${#SOCKETS[@]}" -gt 0 ] || { echo "gen-core-plugin-catalog: no plugin sockets declared in $MANIFEST" >&2; exit 1; }

echo "=== build aggrega (generator binary) ==="
_ver="$(git describe --tags --always 2>/dev/null || echo dev)"; _ver="${_ver#v}"
_sha="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
BIN="$(mktemp -d)/aggrega"
GOWORK=off go build -ldflags "-X main.version=$_ver -X main.commit=$_sha" -o "$BIN" ./cmd/aggrega
echo "built $BIN (version $_ver)"

# Remove only the generated socket directories so stale version-dirs do not
# accumulate. plugin-overlays/ (the authored input) is never touched. Both names
# are expanded with :? so an empty one aborts rather than widening the removal to
# the catalog root — the derivation above already excludes blank fields, and this
# makes that structural instead of merely true today.
echo "=== clear generated socket dirs ==="
for _socket in "${SOCKETS[@]}"; do
  rm -rf "${OUTPUT_DIR:?}/${_socket:?}"
done

echo "=== generate core plugin catalog entities ==="
"$BIN" plugin gen-catalog --from-embedded \
  --overlay "$OVERLAY_DIR" \
  --output-dir "$OUTPUT_DIR"

_socket_list="$(IFS=,; echo "${SOCKETS[*]}")"
echo "=== done: entities under $OUTPUT_DIR/{$_socket_list} ==="
