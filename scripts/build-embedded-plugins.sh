#!/usr/bin/env bash
# aggrega-cli/scripts/build-embedded-plugins.sh
#
# Rebuilds the core Wasm guests from source, writes each gzip-compressed to its
# row's artifact directory, extracts its describe() descriptor, and prints the
# uncompressed content digest. This is the single build entry point for every
# core plugin: it reads the core-plugin list (the one declarative source of the
# core plugins and of whether the binary embeds each) through
# scripts/lib/core-plugins.sh and branches on each row's toolchain, so the set of
# core guests is authored exactly once and cannot drift from the registry table
# or the freshness and drift guards. A guest the binary embeds is written under
# internal/wasm/embedded/plugins/, which the binary embeds whole; one it does not
# embed is built and stamped the same way and written under
# internal/wasm/embedded/published-only/, which nothing embeds, and the content
# digest of that guest is written under internal/wasm/embedded/published-selection/,
# which the binary embeds, so the binary can name the one module it accepts for a
# plugin whose bytes it does not carry.
#
# The build is reproducible per commit. Go guests use GOWORK=off, -trimpath,
# -buildid=, -buildvcs=false, and gzip -n to strip build-path, build-id, and
# ambient VCS stamps; rust guests use --remap-path-prefix, -C strip=symbols,
# CARGO_INCREMENTAL=0, and gzip -n for the analogous effect. The rust remap
# (rust_guest_flags below) covers every directory a rust guest would otherwise
# name: the directory holding this module and the kits beside it, the cargo
# home, and the toolchain's standard-library sources, so a guest carries no path
# of the machine that built it. In both cases the compiler version and short
# commit are injected explicitly (via -ldflags -X for go, via the
# AGGREGA_VERSION and AGGREGA_COMMIT compile-time environment variables for
# rust), so the uncompressed guest digest is deterministic for a given commit —
# identical source at the same commit yields identical bytes, and the digest
# advances only when the source or the commit does. For a rust guest this holds
# for builds in one directory: cargo derives the identity of a path dependency
# outside the crate's own workspace, such as the kits beside this module, from
# its absolute location, and that identity reaches the guest's bytes, so the
# same commit built in another directory yields a different digest.
#
# Each embedded guest registers under its digest-free bare identity
# ({socket}/{name}); the resolution tests derive the expected digest from the
# embedded bytes at test time, so for an embedded guest the printed digest is
# reported for reproducibility confirmation only. For a guest the binary does not
# embed, the same digest is also the one the binary carries for it.
#
# Usage:
#   build-embedded-plugins.sh            # rebuild every core-plugin list entry
#   build-embedded-plugins.sh NAME...    # rebuild only the named entries
set -euo pipefail
cd "$(dirname "$0")/.."          # aggrega-cli module root

# The core-plugin list's one reader: core_plugin_rows and the artifact directories.
. scripts/lib/core-plugins.sh
rows="$(core_plugin_rows)" || exit 1

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

# build_go NAME SOURCEPATH DIR [DIGEST_OUT] — compile the wasip1 guest from
# SOURCEPATH, gzip it to DIR/NAME.wasm.gz, and extract its describe() descriptor
# beside it; DIGEST_OUT is handed to compress_and_describe.
build_go() {
  local name="$1" src="$2" dir="$3" digest_out="${4:-}"
  local embed="$dir/$name.wasm.gz" wasm="$TMP/$name.wasm"
  echo "=== build $name (go: GOOS=wasip1 GOARCH=wasm) ==="
  GOWORK=off GOOS=wasip1 GOARCH=wasm go build -trimpath \
    -ldflags "-X github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega.Version=$_ver -X github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega.Commit=$_sha -buildid=" \
    -buildvcs=false -buildmode=c-shared -o "$wasm" "$src"
  compress_and_describe "$name" "$wasm" "$embed" "$digest_out"
}

# rust_guest_flags WORKSPACE — print, encoded for CARGO_ENCODED_RUSTFLAGS (one
# flag per field, separated by the unit separator), the rustflags a rust guest
# is compiled with, for the crate in the working directory.
#
# A rust guest keeps the source path of every panic location it can report — in
# its own code, its dependencies and the standard library — as a string in the
# .wasm. Each directory those paths start in is remapped to a fixed name:
#
#   WORKSPACE, the directory holding this module and the     .
#     kits its crates reference beside it
#   the cargo home (CARGO_HOME, or ~/.cargo)                  cargo-home
#   the toolchain's standard-library sources                  /rustc/<commit-hash>
#
# The last name is the one the toolchain's prebuilt standard library already
# carries, so the bytes do not depend on whether the rust-src component is
# installed. rustup selects the toolchain from the working directory, so the
# sysroot and commit are read from the rustc that will compile this crate.
#
# rustc applies the LAST --remap-path-prefix whose prefix matches a path, so
# WORKSPACE is given first: a checkout straight under a home directory contains
# the cargo home and the toolchain, and their names must win.
#
# CARGO_ENCODED_RUSTFLAGS is read by cargo in preference to RUSTFLAGS and is not
# split on spaces, so a directory whose name holds a space is remapped whole. A
# caller's own flags — CARGO_ENCODED_RUSTFLAGS when it is set, otherwise
# RUSTFLAGS — are kept and come first, so a caller can still add a flag but can
# neither bring a path back nor undo the symbol strip. While either variable is
# set, cargo reads no rustflags from a configuration file.
#
# The plugins repository's rust guest builds carry the same remap in their own
# lib/build-paths.sh. The copy is deliberate: each repository builds from its
# own checkout alone, so neither sources a file from the other.
rust_guest_flags() {
  local workspace="$1"
  local rustc="${RUSTC:-rustc}" sysroot version line commit=""
  if ! sysroot="$("$rustc" --print sysroot)" || ! version="$("$rustc" -vV)"; then
    echo "build-embedded-plugins: $rustc did not report its sysroot and version from $PWD" >&2
    return 1
  fi
  while IFS= read -r line; do
    case "$line" in "commit-hash: "*) commit="${line#commit-hash: }" ;; esac
  done <<< "$version"

  local IFS=$'\x1f' flags=()
  if [ -n "${CARGO_ENCODED_RUSTFLAGS+set}" ]; then
    if [ -n "$CARGO_ENCODED_RUSTFLAGS" ]; then
      read -r -a flags <<< "$CARGO_ENCODED_RUSTFLAGS"
    fi
  elif [ -n "${RUSTFLAGS:-}" ]; then
    IFS=' ' read -r -a flags <<< "$RUSTFLAGS"
  fi
  flags+=(
    "--remap-path-prefix=$workspace=."
    "--remap-path-prefix=${CARGO_HOME:-$HOME/.cargo}=cargo-home"
    "--remap-path-prefix=$sysroot/lib/rustlib/src/rust=/rustc/${commit:-unknown}"
    "-Cstrip=symbols"
  )
  # "${flags[*]}" joins with the first character of IFS, the unit separator.
  printf '%s' "${flags[*]}"
}

# build_rust NAME SOURCEPATH DIR [DIGEST_OUT] — compile the wasm32-wasip1 crate
# at SOURCEPATH, gzip it to DIR/NAME.wasm.gz, and extract its describe()
# descriptor beside it; DIGEST_OUT is handed to compress_and_describe. The built
# artifact name is the crate directory basename with dashes mapped to underscores
# (cargo's library-name convention).
#
# cargo runs with the crate directory as its working directory, which is how a
# developer builds one of these crates by hand. It matters because rustup selects
# the toolchain from a rust-toolchain.toml found in the WORKING DIRECTORY and
# never from --manifest-path: run from elsewhere, a crate that pins its own
# toolchain is silently compiled by whichever toolchain the invoking directory
# selects, and a crate whose dependencies need the pinned one then fails to
# resolve. The rustflags are computed there too, so the standard-library remap
# names the toolchain that compiles the crate; the workspace prefix is the
# physical parent of the module root, where cargo resolves the kits, so it is
# unchanged by where cargo runs.
build_rust() {
  local name="$1" crate="$2" dir="$3" digest_out="${4:-}"
  local embed="$dir/$name.wasm.gz" workspace
  workspace="$(cd .. && pwd -P)"
  local built="$crate/target/wasm32-wasip1/release/$(basename "$crate" | tr - _).wasm"
  echo "=== build $name (rust: cargo --target wasm32-wasip1) ==="
  (
    cd "$crate" || exit 1
    rustflags="$(rust_guest_flags "$workspace")" || exit 1
    CARGO_INCREMENTAL=0 \
    AGGREGA_VERSION="$_ver" AGGREGA_COMMIT="$_sha" \
    CARGO_ENCODED_RUSTFLAGS="$rustflags" \
      cargo build --release --target wasm32-wasip1
  )
  compress_and_describe "$name" "$built" "$embed" "$digest_out"
}

# compress_and_describe NAME WASM EMBED [DIGEST_OUT] — gzip the built guest to
# EMBED, extract its describe() descriptor alongside, and print the uncompressed
# content digest. The descriptor is extracted once here, on the host toolchain
# (no GOOS/GOARCH override), so the plugin listing can report a guest's static
# metadata without instantiating it. When DIGEST_OUT is given — the guest of a
# plugin the binary does not embed — the digest is also written there, one line,
# for the binary to carry.
compress_and_describe() {
  local name="$1" wasm="$2" embed="$3" digest_out="${4:-}"
  echo "=== compress to $embed ==="
  gzip -n -9 -c "$wasm" > "$embed"
  local desc="${embed%.wasm.gz}.describe.json"
  echo "=== extract describe() descriptor ==="
  GOWORK=off go run ./internal/buildtools/plugindescribe -wasm "$wasm" -name "$name" -out "$desc"
  echo "wrote $desc"
  local digest; digest="sha256:$(sha256sum "$wasm" | cut -d' ' -f1)"
  echo "=== raw $(stat -c%s "$wasm") bytes / gz $(stat -c%s "$embed") bytes ==="
  echo "uncompressed content digest: $digest"
  if [ -n "$digest_out" ]; then
    printf '%s\n' "$digest" > "$digest_out"
    echo "wrote $digest_out"
  fi
}

while IFS=$'\x1f' read -r socket name sourcePath toolchain extraRoots embed dir; do
  [ -n "$socket" ] || continue
  want "$name" || continue
  mkdir -p "$dir"
  digest_out=""
  if [ "$embed" = no ]; then
    mkdir -p "$CORE_PLUGIN_SELECTION_DIR"
    digest_out="$CORE_PLUGIN_SELECTION_DIR/$name.digest"
  fi
  case "$toolchain" in
    go)   build_go   "$name" "$sourcePath" "$dir" "$digest_out" ;;
    rust) build_rust "$name" "$sourcePath" "$dir" "$digest_out" ;;
    *)    echo "build-embedded-plugins: unknown toolchain '$toolchain' for $name" >&2; exit 1 ;;
  esac
  built+=("$name")
done <<< "$rows"

if [ "${#SELECT[@]}" -gt 0 ] && [ "${#built[@]}" -ne "${#SELECT[@]}" ]; then
  echo "build-embedded-plugins: some requested entries were not in the core-plugin list: ${SELECT[*]}" >&2
  exit 1
fi
echo "=== rebuilt core guests: ${built[*]} ==="
