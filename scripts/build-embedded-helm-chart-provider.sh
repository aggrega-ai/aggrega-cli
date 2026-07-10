#!/usr/bin/env bash
# aggrega-cli/scripts/build-embedded-helm-chart-provider.sh
#
# Rebuilds the embedded helm-chart provider Wasm guest from source, writes it
# gzip-compressed to the embed path, and prints the uncompressed content digest.
# The build is reproducible per commit: the recipe below (GOWORK=off, -trimpath,
# -buildid=, -buildvcs=false, and gzip -n) strips build-path, build-id, and
# ambient VCS stamps, while the compiler version and short commit are injected
# explicitly via -ldflags -X, so the uncompressed guest digest is deterministic
# for a given commit — identical source at the same commit yields identical
# bytes, and the digest advances only when the source or the commit does.
# The embedded provider registers under a digest-free repository reference (see
# helmChartProviderURI in internal/wasm/embedded/embedded.go, mirrored by the
# spec.plugin reference in the helm-chart-renderer Plugin entity), and the
# resolution test derives its expected digest from the embedded bytes at test
# time, so the printed digest does not need to be transcribed into any URI or pin
# — it is reported for reproducibility confirmation only.
set -euo pipefail
cd "$(dirname "$0")/.."

EMBED="internal/wasm/embedded/plugins/helmchart-provider.wasm.gz"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
WASM="$TMP/helmchart-provider.wasm"

echo "=== build guest (GOOS=wasip1 GOARCH=wasm) ==="
_ver="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"; _ver="${_ver#v}"
_sha="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
GOWORK=off GOOS=wasip1 GOARCH=wasm go build -trimpath -ldflags "-X main.version=$_ver -X main.commit=$_sha -buildid=" -buildvcs=false -buildmode=c-shared -o "$WASM" ./plugins/helmchart-provider

echo "=== compress to $EMBED ==="
gzip -n -9 -c "$WASM" > "$EMBED"

echo "=== extract describe() descriptor ==="
# Extract the guest's describe() descriptor once, here, so the plugin listing can
# report its static metadata without instantiating the guest. Written alongside
# the .wasm.gz and captured by the embed directive at CLI compile time; run on the
# host toolchain (no GOOS/GOARCH override), unlike the wasip1 guest build above.
DESC="${EMBED%.wasm.gz}.describe.json"
GOWORK=off go run ./internal/buildtools/plugindescribe -wasm "$WASM" -name "$(basename "${EMBED%.wasm.gz}")" -out "$DESC"
echo "wrote $DESC"

DIGEST="sha256:$(sha256sum "$WASM" | cut -d' ' -f1)"
echo "=== raw $(stat -c%s "$WASM") bytes / gz $(stat -c%s "$EMBED") bytes ==="
echo "uncompressed content digest: $DIGEST"
