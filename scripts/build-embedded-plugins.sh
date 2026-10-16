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
# The build is reproducible: the same commit yields the same bytes wherever it is
# built, in any directory and on any machine. Four things make it so, and each is
# held here rather than left to the building machine.
#
#   The toolchain. The compiler's own code reaches a guest's bytes, so a guest
#   built with another toolchain is another module. A go guest is compiled with
#   the module's own Go toolchain (go_guest_toolchain below), the one the release
#   workflow sets Go up from, so a newer local Go does not compile it. A rust
#   guest is compiled with the one exact release its rust-toolchain.toml pins, and
#   is refused when the rustc that would compile it reports another.
#
#   The paths. Go guests use GOWORK=off, -trimpath, -buildid= and
#   -buildvcs=false to strip build-path, build-id and ambient VCS stamps; rust
#   guests use --remap-path-prefix, -C strip=symbols and CARGO_INCREMENTAL=0 for
#   the analogous effect, and both use gzip -n. The rust remap (rust_guest_flags
#   below) covers every directory a rust guest would otherwise name: the
#   directory holding this module and the kits beside it, the cargo home, and the
#   toolchain's standard-library sources, so a guest carries no path of the
#   machine that built it.
#
#   The identity cargo gives a path dependency, which reaches a rust guest's
#   bytes. cargo derives it from the dependency's absolute location unless the
#   dependency lies under the workspace root, so a rust guest is compiled from a
#   staged view in which every crate it depends on by path lies under one root
#   (rust_guest_stage below).
#
#   The stamps. The compiler version and commit are injected explicitly (via
#   -ldflags -X for go, via the AGGREGA_VERSION and AGGREGA_COMMIT compile-time
#   environment variables for rust) from the shared recipe, whose commit has a
#   fixed length, so the uncompressed guest digest advances only when the source
#   or the commit does.
#
# What the building machine can still change is what it chooses to add: flags
# given through RUSTFLAGS, CARGO_ENCODED_RUSTFLAGS or the Go environment (GOFLAGS,
# GOEXPERIMENT, GOWASM), and a cargo configuration of its own that alters the
# release profile. A build meant to reproduce a published guest adds none.
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
# The directory rust guests are staged under (rust_guest_stage). Its path is
# fixed for a checkout and a user, so the rustflags that remap it are the same
# from one build to the next and cargo reuses what it compiled before; each
# guest's stage is laid out afresh, and the whole of it is removed when the build
# ends. Removing it touches only links and the files written there: rm does not
# follow a link into the directory it points at.
GUEST_STAGE_ROOT="${TMPDIR:-/tmp}/aggrega-guest-stage-$(id -u)-$(pwd -P | cksum | cut -d' ' -f1)"
trap 'rm -rf "$TMP" "$GUEST_STAGE_ROOT"' EXIT

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

# go_guest_toolchain — print the Go toolchain every go guest is compiled with: the
# module's go.mod toolchain line, or go<version> from its go line where it has
# none. That is the file the release workflow sets Go up from, so a guest built
# there and one built here are compiled by the same release. Handed to the build
# as GOTOOLCHAIN, it holds the build to that release exactly: the go command's
# default takes a newer local toolchain in its place.
go_guest_toolchain() {
  local json
  json="$(GOWORK=off go mod edit -json)" || return 1
  awk -F'"' '
    $2 == "Go"        { go = $4 }
    $2 == "Toolchain" { toolchain = $4 }
    END {
      if (toolchain != "") print toolchain
      else if (go != "") print "go" go
      else { print "build-embedded-plugins: go.mod declares neither a toolchain nor a go line" > "/dev/stderr"; exit 1 }
    }
  ' <<< "$json"
}

# build_go NAME SOURCEPATH DIR [DIGEST_OUT] — compile the wasip1 guest from
# SOURCEPATH with the module's Go toolchain, gzip it to DIR/NAME.wasm.gz, and
# extract its describe() descriptor beside it; DIGEST_OUT is handed to
# compress_and_describe.
build_go() {
  local name="$1" src="$2" dir="$3" digest_out="${4:-}"
  local embed="$dir/$name.wasm.gz" wasm="$TMP/$name.wasm"
  echo "=== build $name (go $GO_GUEST_TOOLCHAIN: GOOS=wasip1 GOARCH=wasm) ==="
  GOTOOLCHAIN="$GO_GUEST_TOOLCHAIN" GOWORK=off GOOS=wasip1 GOARCH=wasm go build -trimpath \
    -ldflags "-X github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega.Version=$_ver -X github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega.Commit=$_sha -buildid=" \
    -buildvcs=false -buildmode=c-shared -o "$wasm" "$src"
  compress_and_describe "$name" "$wasm" "$embed" "$digest_out"
}

# rust_guest_flags WORKSPACE STAGE — print, encoded for CARGO_ENCODED_RUSTFLAGS
# (one flag per field, separated by the unit separator), the rustflags a rust
# guest is compiled with, for the crate in the working directory.
#
# A rust guest keeps the source path of every panic location it can report — in
# its own code, its dependencies and the standard library — as a string in the
# .wasm. Each directory those paths start in is remapped to a fixed name:
#
#   WORKSPACE, the directory holding this module and the     .
#     kits its crates reference beside it
#   STAGE, the view the guest is compiled from                .
#     (rust_guest_stage), which lays the same directories
#     out at the same relative positions
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
# the cargo home and the toolchain, and their names must win; STAGE follows it,
# so its own name wins should the stage lie inside the checkout.
#
# cargo hands the compiler most crates under the stage by their path relative to
# it, but not every location: generic code of one crate compiled into another is
# recorded with its file joined to the working directory, which lies under the
# stage. So the stage is remapped like the checkout. Its path is fixed for a
# checkout (GUEST_STAGE_ROOT), so these flags are the same from one build to the
# next, which is what lets cargo reuse what it compiled before.
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
  local workspace="$1" stage="$2"
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
    "--remap-path-prefix=$stage=."
    "--remap-path-prefix=${CARGO_HOME:-$HOME/.cargo}=cargo-home"
    "--remap-path-prefix=$sysroot/lib/rustlib/src/rust=/rustc/${commit:-unknown}"
    "-Cstrip=symbols"
  )
  # "${flags[*]}" joins with the first character of IFS, the unit separator.
  printf '%s' "${flags[*]}"
}

# rust_guest_stage WORKSPACE MODULE CRATE STAGE — lay out under STAGE the view the
# rust guest at MODULE/CRATE is compiled from: WORKSPACE, the directory holding
# this module and the kits beside it, with STAGE as the root of a generated
# workspace whose one member is the guest.
#
# cargo identifies a path dependency by its path relative to the workspace root
# when it lies under the root, and by its absolute location otherwise, and that
# identity reaches the guest's bytes: it decides the order of the guest's
# functions in the module. A guest crate is a workspace root of its own, and every
# crate it depends on by path — the kits beside this module, the shared
# governance core beside the guest — lies outside it, so built in place the same
# commit yields different bytes in each directory it is checked out in. Under
# STAGE each of those crates lies beneath the root, at the same relative position
# wherever the checkout is.
#
# The view is links to the real files, apart from three things STAGE holds
# itself: the root manifest, which names the guest its one member, excludes every
# other directory under the root (an excluded path dependency is still a
# dependency, it just does not join the workspace) and carries the guest's own
# [profile.*] tables, since cargo reads profiles from the root alone; the guest's
# manifest, copied without its [workspace] and [profile.*] tables; and the
# guest's Cargo.lock, which cargo reads at the root. The module is laid out as
# aggrega-cli whatever its checkout is called, because the guest's own path under
# the root is part of its identity too.
rust_guest_stage() {
  local workspace="$1" module="$2" crate="$3" stage="$4"
  local here="$module" there="$stage/aggrega-cli" rest="$crate" part e base
  local excluded="" sep="" resolver
  mkdir -p "$there" || return 1
  for e in "$workspace"/* "$workspace"/.[!.]* "$workspace"/..?*; do
    [ -e "$e" ] || [ -L "$e" ] || continue
    [ "$e" -ef "$module" ] && continue
    base="${e##*/}"
    case "$base" in aggrega-cli|Cargo.toml|Cargo.lock) continue ;; esac
    ln -s "$e" "$stage/$base" || return 1
  done
  # Down the crate's path, every entry beside each directory on the way.
  while :; do
    part="${rest%%/*}"
    for e in "$here"/* "$here"/.[!.]* "$here"/..?*; do
      [ -e "$e" ] || [ -L "$e" ] || continue
      [ "${e##*/}" = "$part" ] && continue
      ln -s "$e" "$there/${e##*/}" || return 1
    done
    here="$here/$part"
    there="$there/$part"
    mkdir "$there" || return 1
    [ "$rest" = "$part" ] && break
    rest="${rest#*/}"
  done
  for e in "$here"/* "$here"/.[!.]* "$here"/..?*; do
    [ -e "$e" ] || [ -L "$e" ] || continue
    case "${e##*/}" in Cargo.toml|Cargo.lock|target) continue ;; esac
    ln -s "$e" "$there/${e##*/}" || return 1
  done
  awk '/^[[:space:]]*\[/ { skip = ($0 ~ /^[[:space:]]*\[[[:space:]]*(workspace|profile)[].[:space:]]/) } !skip' \
    "$here/Cargo.toml" > "$there/Cargo.toml" || return 1
  if [ -f "$here/Cargo.lock" ]; then
    cp "$here/Cargo.lock" "$stage/Cargo.lock" || return 1
  fi
  for e in "$stage"/* "$stage"/.[!.]* "$stage"/..?*; do
    [ -d "$e" ] || continue
    base="${e##*/}"
    base="${base//\\/\\\\}"
    base="${base//\"/\\\"}"
    excluded="$excluded$sep\"$base\""
    sep=", "
  done
  # The resolver a root package would get: one the manifest names, otherwise the
  # one its edition implies. A generated root that named none would fall back to
  # the oldest, and resolve the guest's features differently.
  resolver="$(awk -F'"' '
    /^[[:space:]]*\[/                                          { table = $0 }
    /^[[:space:]]*resolver[[:space:]]*=/                       { named = $2 }
    /^[[:space:]]*edition[[:space:]]*=/ && table ~ /^[[:space:]]*\[[[:space:]]*package[[:space:]]*\]/ { edition = $2 }
    END {
      if (named != "") print named
      else if (edition == "2024") print "3"
      else if (edition == "2021") print "2"
      else print "1"
    }
  ' "$here/Cargo.toml")" || return 1
  {
    printf '[workspace]\n'
    printf 'members = ["aggrega-cli/%s"]\n' "$crate"
    printf 'exclude = [%s]\n' "$excluded"
    printf 'resolver = "%s"\n\n' "$resolver"
    awk '/^[[:space:]]*\[/ { keep = ($0 ~ /^[[:space:]]*\[[[:space:]]*profile\./) } keep' "$here/Cargo.toml"
  } > "$stage/Cargo.toml"
}

# rust_guest_toolchain_check NAME CRATE CHANNEL — refuse, naming both, when the
# rustc that would compile the guest in the working directory does not report
# CHANNEL, the release CRATE/rust-toolchain.toml pins. rustup selects the
# toolchain from that file, so the two differ only where something overrides it
# (RUSTUP_TOOLCHAIN, RUSTC) or where the file names a moving channel such as
# stable rather than one release; either way the guest would be compiled by a
# release its source does not name, and its bytes would differ from the same
# commit built anywhere else.
rust_guest_toolchain_check() {
  local name="$1" crate="$2" channel="$3"
  local rustc="${RUSTC:-rustc}" version line release=""
  if ! version="$("$rustc" -vV)"; then
    echo "build-embedded-plugins: $rustc did not report its version for $name ($crate/rust-toolchain.toml pins rust $channel; install it with every target that file lists: (cd $crate && rustup toolchain install))" >&2
    return 1
  fi
  while IFS= read -r line; do
    case "$line" in "release: "*) release="${line#release: }" ;; esac
  done <<< "$version"
  if [ "$release" != "$channel" ]; then
    echo "build-embedded-plugins: $crate/rust-toolchain.toml pins rust $channel for $name, but $rustc reports ${release:-no release}" >&2
    echo "  a core guest is compiled by the one release its toolchain file pins and no other: unset RUSTUP_TOOLCHAIN and RUSTC, or pin one release rather than a channel" >&2
    return 1
  fi
}

# build_rust NAME SOURCEPATH DIR [DIGEST_OUT] — compile the wasm32-wasip1 crate
# at SOURCEPATH with the toolchain it pins, from a staged view of the workspace
# (rust_guest_stage), gzip it to DIR/NAME.wasm.gz, and extract its describe()
# descriptor beside it; DIGEST_OUT is handed to compress_and_describe. The built
# artifact name is the crate directory basename with dashes mapped to underscores
# (cargo's library-name convention).
#
# cargo runs in the crate's staged directory, which holds a link to the crate's
# rust-toolchain.toml. It matters because rustup selects the toolchain from a
# rust-toolchain.toml found in the WORKING DIRECTORY and never from
# --manifest-path: run from elsewhere, a crate that pins its own toolchain is
# silently compiled by whichever toolchain the invoking directory selects. The
# toolchain check and the rustflags are computed there too, so both read the
# rustc that compiles the crate. The build directory stays the crate's own target
# directory in the checkout, so the artifact lands where it always has and cargo's
# cache outlives the stage; the workspace prefix of the remap covers it. Where
# cargo rewrites the lock because the manifest moved past it, the rewritten lock
# is copied back to the crate, as cargo would have written it there. A guest
# whose bytes still name the stage is refused: the remap rewrites the paths the
# compiler records, and a path reaching the guest any other way — as a string
# value, say — would make its bytes depend on where it was built.
build_rust() {
  local name="$1" crate="${2#./}" dir="$3" digest_out="${4:-}"
  local embed="$dir/$name.wasm.gz" module workspace channel stage
  module="$(pwd -P)"
  workspace="$(cd .. && pwd -P)"
  channel="$(core_plugin_rust_toolchain "$crate")" || exit 1
  stage="$GUEST_STAGE_ROOT/$name"
  rm -rf "$stage"
  rust_guest_stage "$workspace" "$module" "$crate" "$stage" || exit 1
  local wasm
  wasm="$crate/target/wasm32-wasip1/release/$(basename "$crate" | tr - _).wasm"
  echo "=== build $name (rust $channel: cargo --target wasm32-wasip1) ==="
  (
    cd "$stage/aggrega-cli/$crate" || exit 1
    rust_guest_toolchain_check "$name" "$crate" "$channel" || exit 1
    rustflags="$(rust_guest_flags "$workspace" "$stage")" || exit 1
    CARGO_TARGET_DIR="$module/$crate/target" CARGO_INCREMENTAL=0 \
    AGGREGA_VERSION="$_ver" AGGREGA_COMMIT="$_sha" \
    CARGO_ENCODED_RUSTFLAGS="$rustflags" \
      cargo build --release --target wasm32-wasip1
  )
  if [ -f "$stage/Cargo.lock" ] && ! cmp -s "$stage/Cargo.lock" "$crate/Cargo.lock"; then
    cp "$stage/Cargo.lock" "$crate/Cargo.lock"
  fi
  if LC_ALL=C grep -qF -- "$stage" "$wasm"; then
    echo "build-embedded-plugins: $name names the directory it was staged in ($stage), which no remap rewrote, so the guest's bytes would depend on where it was built" >&2
    exit 1
  fi
  compress_and_describe "$name" "$wasm" "$embed" "$digest_out"
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

GO_GUEST_TOOLCHAIN="$(go_guest_toolchain)" || exit 1

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
