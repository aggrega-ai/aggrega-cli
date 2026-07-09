#!/usr/bin/env bash
# aggrega-cli/scripts/build-embedded-publishing-linter.sh
#
# Rebuilds the embedded publishing-linter Wasm guest from its Rust source, writes
# it gzip-compressed to the embed path, and prints the uncompressed content
# digest. Unlike the Go provider guests (go build), this guest is a Rust crate
# built with cargo to the wasm32-wasip1 target. The recipe strips the analogous
# non-reproducible stamps: --remap-path-prefix removes absolute build paths,
# -C strip=symbols drops symbol/debug tables, CARGO_INCREMENTAL=0 disables the
# incremental cache, and gzip -n omits the archive timestamp, so the uncompressed
# guest digest is stable across rebuilds from a clean checkout.
#
# The embedded linter registers under a digest-free repository reference (see
# PublishingLinterURI in internal/wasm/embedded/embedded.go), so the printed
# digest does not need to be transcribed into any URI or pin — it is reported for
# reproducibility confirmation only.
set -euo pipefail
cd "$(dirname "$0")/.."

CRATE="plugins/publishing-linter"
EMBED="internal/wasm/embedded/plugins/publishing-linter.wasm.gz"
BUILT="$CRATE/target/wasm32-wasip1/release/publishing_linter.wasm"

echo "=== build guest (cargo --target wasm32-wasip1) ==="
CARGO_INCREMENTAL=0 \
RUSTFLAGS="--remap-path-prefix=$PWD=. -C strip=symbols" \
	cargo build --release --manifest-path "$CRATE/Cargo.toml" --target wasm32-wasip1

echo "=== compress to $EMBED ==="
gzip -n -9 -c "$BUILT" > "$EMBED"

DIGEST="sha256:$(sha256sum "$BUILT" | cut -d' ' -f1)"
echo "=== raw $(stat -c%s "$BUILT") bytes / gz $(stat -c%s "$EMBED") bytes ==="
echo "uncompressed content digest: $DIGEST"
