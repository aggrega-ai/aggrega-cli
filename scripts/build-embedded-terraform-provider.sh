#!/usr/bin/env bash
# aggrega-cli/scripts/build-embedded-terraform-provider.sh
#
# Rebuilds the embedded terraform provider Wasm guest from source, writes it
# gzip-compressed to the embed path, and prints the uncompressed content digest.
# The build is reproducible: the recipe below (GOWORK=off, -trimpath,
# -ldflags=-buildid=, -buildvcs=false, and gzip -n) strips build-path, build-id,
# and VCS stamps so the uncompressed guest digest is stable across rebuilds.
# When the guest source changes, run this and update the new digest at its four
# coupled sites: the registration URI in internal/wasm/embedded/embedded.go, the
# artifact reference (spec.plugin) in the terraform-renderer Plugin entity, the
# capstone resolution test's expected pin, and the route-test's inline Plugin copy
# in internal/compiler/renderer/enginefactory/hybrid_route_test.go. The script
# does not rewrite those sites — a maintainer confirms the new digest deliberately.
set -euo pipefail
cd "$(dirname "$0")/.."

EMBED="internal/wasm/embedded/plugins/terraform-provider.wasm.gz"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
WASM="$TMP/terraform-provider.wasm"

echo "=== build guest (GOOS=wasip1 GOARCH=wasm) ==="
GOWORK=off GOOS=wasip1 GOARCH=wasm go build -trimpath -ldflags=-buildid= -buildvcs=false -buildmode=c-shared -o "$WASM" ./plugins/terraform-provider

echo "=== compress to $EMBED ==="
gzip -n -9 -c "$WASM" > "$EMBED"

DIGEST="sha256:$(sha256sum "$WASM" | cut -d' ' -f1)"
echo "=== raw $(stat -c%s "$WASM") bytes / gz $(stat -c%s "$EMBED") bytes ==="
echo "uncompressed content digest: $DIGEST"
