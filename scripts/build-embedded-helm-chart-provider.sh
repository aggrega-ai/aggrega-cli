#!/usr/bin/env bash
# aggrega-cli/scripts/build-embedded-helm-chart-provider.sh
#
# Rebuilds the embedded helm-chart provider Wasm guest from source, writes it
# gzip-compressed to the embed path, and prints the uncompressed content digest.
# When the guest source changes, run this and update the new digest at its three
# coupled sites: the registration URI in internal/wasm/embedded/embedded.go, the
# artifact reference (spec.plugin) in the helm-chart-renderer Plugin entity, and
# the capstone resolution test's expected pin. The script does not rewrite those
# sites — Wasm is not byte-reproducible, so a maintainer confirms the new digest
# deliberately.
set -euo pipefail
cd "$(dirname "$0")/.."

EMBED="internal/wasm/embedded/plugins/helmchart-provider.wasm.gz"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
WASM="$TMP/helmchart-provider.wasm"

echo "=== build guest (GOOS=wasip1 GOARCH=wasm) ==="
GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o "$WASM" ./plugins/helmchart-provider

echo "=== compress to $EMBED ==="
gzip -9 -c "$WASM" > "$EMBED"

DIGEST="sha256:$(sha256sum "$WASM" | cut -d' ' -f1)"
echo "=== raw $(stat -c%s "$WASM") bytes / gz $(stat -c%s "$EMBED") bytes ==="
echo "uncompressed content digest: $DIGEST"
