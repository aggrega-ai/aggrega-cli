#!/usr/bin/env bash
# aggrega-cli/scripts/build-embedded-plugins.sh
#
# Rebuilds the embedded Wasm guests from source, writes each gzip-compressed to
# its embed path, extracts its describe() descriptor, and prints the uncompressed
# content digest. This is the single build entry point for every embedded plugin:
# it reads the embedded-plugin manifest (the one declarative source of which
# plugins ship inside the binary) and branches on each row's toolchain, so the
# set of embedded guests is authored exactly once and cannot drift from the
# registry table or the freshness and drift guards.
#
# The build is reproducible per commit. Go guests use GOWORK=off, -trimpath,
# -buildid=, -buildvcs=false, and gzip -n to strip build-path, build-id, and
# ambient VCS stamps; rust guests use --remap-path-prefix, -C strip=symbols,
# CARGO_INCREMENTAL=0, and gzip -n for the analogous effect. In both cases the
# compiler version and short commit are injected explicitly (via -ldflags -X for
# go, via the AGGREGA_VERSION and AGGREGA_COMMIT compile-time environment
# variables for rust), so the uncompressed guest digest is deterministic for a
# given commit — identical source at the same commit yields identical bytes, and
# the digest advances only when the source or the commit does.
#
# Each embedded guest registers under a digest-free repository reference
# (oci://hub.aggrega.ai/plugin/{socket}/{name}); the resolution tests derive the
# expected digest from the embedded bytes at test time, so the printed digest is
# reported for reproducibility confirmation only.
#
# Usage:
#   build-embedded-plugins.sh            # rebuild every manifest entry
#   build-embedded-plugins.sh NAME...    # rebuild only the named entries
set -euo pipefail
cd "$(dirname "$0")/.."          # aggrega-cli module root

MANIFEST="internal/wasm/embedded/embedded-plugins.tsv"
EMBED_DIR="internal/wasm/embedded/plugins"
[ -f "$MANIFEST" ] || { echo "build-embedded-plugins: manifest not found: $MANIFEST" >&2; exit 1; }

# Build-provenance stamps (_ver, _sha) from the one shared recipe, computed once
# and shared by every entry so a single rebuild pass stamps a consistent version
# across all guests — the same version the shipped binary and the plugin-entity
# generator derive.
. scripts/lib/build-version.sh

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

declare -a SELECT=("$@")
declare -a built=()

# want NAME — with entry-name arguments present, true only for a requested entry;
# with no arguments, true for every entry.
want() {
  local target="$1" n
  [ "${#SELECT[@]}" -eq 0 ] && return 0
  for n in "${SELECT[@]}"; do [ "$n" = "$target" ] && return 0; done
  return 1
}

# build_go NAME SOURCEPATH — compile the wasip1 guest from SOURCEPATH, gzip it to
# the embed path, and extract its describe() descriptor.
build_go() {
  local name="$1" src="$2"
  local embed="$EMBED_DIR/$name.wasm.gz" wasm="$TMP/$name.wasm"
  echo "=== build $name (go: GOOS=wasip1 GOARCH=wasm) ==="
  GOWORK=off GOOS=wasip1 GOARCH=wasm go build -trimpath \
    -ldflags "-X github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega.Version=$_ver -X github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega.Commit=$_sha -buildid=" \
    -buildvcs=false -buildmode=c-shared -o "$wasm" "$src"
  compress_and_describe "$name" "$wasm" "$embed"
}

# build_rust NAME SOURCEPATH — compile the wasm32-wasip1 crate at SOURCEPATH,
# gzip it to the embed path, and extract its describe() descriptor. The built
# artifact name is the crate directory basename with dashes mapped to
# underscores (cargo's library-name convention).
#
# cargo runs with the crate directory as its working directory, which is how a
# developer builds one of these crates by hand. It matters because rustup selects
# the toolchain from a rust-toolchain.toml found in the WORKING DIRECTORY and
# never from --manifest-path: run from elsewhere, a crate that pins its own
# toolchain is silently compiled by whichever toolchain the invoking directory
# selects, and a crate whose dependencies need the pinned one then fails to
# resolve. The path-remap prefix stays anchored to the module root so the
# reproducibility stamping is unchanged by where cargo runs.
build_rust() {
  local name="$1" crate="$2"
  local embed="$EMBED_DIR/$name.wasm.gz" root="$PWD"
  local built="$crate/target/wasm32-wasip1/release/$(basename "$crate" | tr - _).wasm"
  echo "=== build $name (rust: cargo --target wasm32-wasip1) ==="
  (
    cd "$crate" || exit 1
    CARGO_INCREMENTAL=0 \
    AGGREGA_VERSION="$_ver" AGGREGA_COMMIT="$_sha" \
    RUSTFLAGS="--remap-path-prefix=$root=. -C strip=symbols" \
      cargo build --release --target wasm32-wasip1
  )
  compress_and_describe "$name" "$built" "$embed"
}

# compress_and_describe NAME WASM EMBED — gzip the built guest to EMBED, extract
# its describe() descriptor alongside, and print the uncompressed content digest.
# The descriptor is extracted once here, on the host toolchain (no GOOS/GOARCH
# override), so the plugin listing can report a guest's static metadata without
# instantiating it.
compress_and_describe() {
  local name="$1" wasm="$2" embed="$3"
  echo "=== compress to $embed ==="
  gzip -n -9 -c "$wasm" > "$embed"
  local desc="${embed%.wasm.gz}.describe.json"
  echo "=== extract describe() descriptor ==="
  GOWORK=off go run ./internal/buildtools/plugindescribe -wasm "$wasm" -name "$name" -out "$desc"
  echo "wrote $desc"
  local digest; digest="sha256:$(sha256sum "$wasm" | cut -d' ' -f1)"
  echo "=== raw $(stat -c%s "$wasm") bytes / gz $(stat -c%s "$embed") bytes ==="
  echo "uncompressed content digest: $digest"
}

while IFS=$'\t' read -r socket name sourcePath toolchain extraRoots; do
  case "$socket" in ''|'#'*) continue;; esac
  want "$name" || continue
  case "$toolchain" in
    go)   build_go   "$name" "$sourcePath" ;;
    rust) build_rust "$name" "$sourcePath" ;;
    *)    echo "build-embedded-plugins: unknown toolchain '$toolchain' for $name" >&2; exit 1 ;;
  esac
  built+=("$name")
done < "$MANIFEST"

if [ "${#SELECT[@]}" -gt 0 ] && [ "${#built[@]}" -ne "${#SELECT[@]}" ]; then
  echo "build-embedded-plugins: some requested entries were not in the manifest: ${SELECT[*]}" >&2
  exit 1
fi
echo "=== rebuilt embedded guests: ${built[*]} ==="
