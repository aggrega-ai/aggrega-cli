#!/usr/bin/env bash
# aggrega-cli/scripts/build-embedded-terraform-provider.sh
#
# Rebuilds the embedded terraform provider Wasm guest from source, writes it
# gzip-compressed to the embed path, and prints the uncompressed content digest.
# The build is reproducible per commit: the recipe below (GOWORK=off, -trimpath,
# -buildid=, -buildvcs=false, and gzip -n) strips build-path, build-id, and
# ambient VCS stamps, while the compiler version and short commit are injected
# explicitly via -ldflags -X, so the uncompressed guest digest is deterministic
# for a given commit — identical source at the same commit yields identical
# bytes, and the digest advances only when the source or the commit does.
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
_ver="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"; _ver="${_ver#v}"
_sha="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
GOWORK=off GOOS=wasip1 GOARCH=wasm go build -trimpath -ldflags "-X main.version=$_ver -X main.commit=$_sha -buildid=" -buildvcs=false -buildmode=c-shared -o "$WASM" ./plugins/terraform-provider

echo "=== compress to $EMBED ==="
gzip -n -9 -c "$WASM" > "$EMBED"

DIGEST="sha256:$(sha256sum "$WASM" | cut -d' ' -f1)"
echo "=== raw $(stat -c%s "$WASM") bytes / gz $(stat -c%s "$EMBED") bytes ==="
echo "uncompressed content digest: $DIGEST"
