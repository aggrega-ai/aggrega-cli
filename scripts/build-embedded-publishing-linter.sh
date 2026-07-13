#!/usr/bin/env bash
# aggrega-cli/scripts/build-embedded-publishing-linter.sh
#
# Rebuilds the embedded publishing-linter Wasm guest from its Rust source, writes
# it gzip-compressed to the embed path, and prints the uncompressed content
# digest. Unlike the Go provider guests (go build), this guest is a Rust crate
# built with cargo to the wasm32-wasip1 target. The recipe strips the analogous
# non-reproducible stamps: --remap-path-prefix removes absolute build paths,
# -C strip=symbols drops symbol/debug tables, CARGO_INCREMENTAL=0 disables the
# incremental cache, and gzip -n omits the archive timestamp, while the compiler
# version and short commit are injected explicitly through the AGGREGA_VERSION and
# AGGREGA_COMMIT compile-time environment variables the crate reads via option_env!,
# so the uncompressed guest digest is deterministic for a given commit from a clean
# checkout.
#
# The embedded linter registers under a digest-free repository reference (see
# PublishingLinterURI in internal/wasm/embedded/embedded.go); the printed digest
# is reported for reproducibility confirmation only.
set -euo pipefail
cd "$(dirname "$0")/.."

CRATE="plugins/publishing-linter"
EMBED="internal/wasm/embedded/plugins/publishing.wasm.gz"
BUILT="$CRATE/target/wasm32-wasip1/release/publishing_linter.wasm"

echo "=== build guest (cargo --target wasm32-wasip1) ==="
_ver="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"; _ver="${_ver#v}"
_sha="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
CARGO_INCREMENTAL=0 \
AGGREGA_VERSION="$_ver" AGGREGA_COMMIT="$_sha" \
RUSTFLAGS="--remap-path-prefix=$PWD=. -C strip=symbols" \
	cargo build --release --manifest-path "$CRATE/Cargo.toml" --target wasm32-wasip1

echo "=== compress to $EMBED ==="
gzip -n -9 -c "$BUILT" > "$EMBED"

echo "=== extract describe() descriptor ==="
# Extract the guest's describe() descriptor once, here, so the plugin listing can
# report its static metadata without instantiating the guest. Written alongside
# the .wasm.gz and captured by the embed directive at CLI compile time; run on the
# host toolchain, unlike the wasm32-wasip1 guest build above.
DESC="${EMBED%.wasm.gz}.describe.json"
GOWORK=off go run ./internal/buildtools/plugindescribe -wasm "$BUILT" -name "$(basename "${EMBED%.wasm.gz}")" -out "$DESC"
echo "wrote $DESC"

DIGEST="sha256:$(sha256sum "$BUILT" | cut -d' ' -f1)"
echo "=== raw $(stat -c%s "$BUILT") bytes / gz $(stat -c%s "$EMBED") bytes ==="
echo "uncompressed content digest: $DIGEST"
