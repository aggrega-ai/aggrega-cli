#!/usr/bin/env bash
# aggrega-cli/scripts/gen-core-plugin-catalog.sh
#
# Generates the core-plugin catalog entities from the core guests — the ones the
# compiler binary embeds and the ones the core-plugin list builds without
# embedding — and the authored per-plugin overlays, into the sibling aggrega-hub
# catalog; NOT part of the ordinary build or test cycle. The release step runs it
# in a scratch checkout, to check the release's overlays and render docs pages.
# The copy aggrega-hub commits comes from the tag-driven publish CI, which runs the
# same generation before signing and publishing the core plugins and records it on
# the hub's published branch, from which the sync commit takes it into main.
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
SIBLINGS_DIR="$(cd ../ && pwd)"
HUB="$SIBLINGS_DIR/aggrega-hub"
OVERLAY_DIR="$HUB/plugin-overlays"
OUTPUT_DIR="$HUB/aggrega-catalog/plugin"

[ -d "$OVERLAY_DIR" ] || { echo "gen-core-plugin-catalog: overlay dir not found: $OVERLAY_DIR (this script reads the overlays from, and writes the entities into, the aggrega-hub repository checked out beside this one at $HUB; check it out there first)" >&2; exit 1; }

# The core-plugin list's one reader: core_plugin_rows and the generation of the
# rows the binary does not embed.
. scripts/lib/core-plugins.sh
rows="$(core_plugin_rows)" || exit 1

# The socket directories this run owns are derived from the core-plugin list's
# socket column — the same declarative source the --from-embedded selection and
# the generation of the rows the binary does not embed draw their guests from —
# so the set cleared and the set reported cannot fall behind the set actually
# written. A list kept by hand here would be precisely the parallel list the core
# plugin list exists to prevent.
mapfile -t SOCKETS < <(while IFS=$'\x1f' read -r socket _; do [ -n "$socket" ] && printf '%s\n' "$socket"; done <<< "$rows" | sort -u)
[ "${#SOCKETS[@]}" -gt 0 ] || { echo "gen-core-plugin-catalog: no plugin sockets declared in $CORE_PLUGIN_LIST" >&2; exit 1; }

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
echo "=== generate the entities of the core plugins the binary does not embed ==="
core_plugin_generate_unembedded "$BIN" "$OVERLAY_DIR" "$OUTPUT_DIR"

_socket_list="$(IFS=,; echo "${SOCKETS[*]}")"
echo "=== done: entities under $OUTPUT_DIR/{$_socket_list} ==="
