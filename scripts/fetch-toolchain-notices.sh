#!/usr/bin/env bash
# aggrega-cli/scripts/fetch-toolchain-notices.sh
#
# Fetches the license texts of what a Rust toolchain links into the embedded Rust
# plugins' wasm32-wasip1 builds, into third-party/toolchain/, pinned to that
# toolchain:
#
#   rust-<channel>/COPYRIGHT        the Rust project's COPYRIGHT file at the tag
#                                   named <channel> (the standard library's notice)
#   wasi-libc-<commit>/...          the license files of wasi-libc at the commit the
#                                   toolchain's release builds its libc.a from
#   toolchains.tsv                  one row per channel: channel, wasi-sdk release,
#                                   wasi-libc commit
#
# Usage:  scripts/fetch-toolchain-notices.sh <channel>     (for example 1.85.1)
#
# Run it from anywhere when a plugin's rust-toolchain.toml moves to a channel
# that third-party/toolchain/ does not hold yet. It resolves, in order:
#
#   1. the wasi-sdk release the Rust release builds its WASI target with, read from
#      src/ci/docker/host-x86_64/dist-various-2/Dockerfile at the Rust tag;
#   2. the wasi-libc commit that wasi-sdk release pins as its src/wasi-libc
#      submodule, read through the GitHub contents API.
#
# It refuses, exit 1 naming what it could not resolve or download, and then
# changes nothing. The files are downloaded as data, never executed. The
# generator (gen-third-party-licenses.sh) works offline from what this writes.
set -euo pipefail

if [ "$#" -ne 1 ] || [ -z "$1" ]; then
  echo "usage: fetch-toolchain-notices.sh <channel>   (for example 1.85.1)" >&2
  exit 2
fi
channel="$1"
case "$channel" in
  *[!0-9A-Za-z._-]*) echo "fetch-toolchain-notices: the channel \"$channel\" is not a release name" >&2; exit 2 ;;
esac

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEST="$ROOT/third-party/toolchain"
RAW="https://raw.githubusercontent.com"
API="https://api.github.com"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

die() { echo "fetch-toolchain-notices: $*" >&2; exit 1; }

get() { # URL OUTFILE
  curl -fsSL "$1" -o "$2" || die "could not download $1"
}

# 1. channel -> wasi-sdk release
get "$RAW/rust-lang/rust/$channel/src/ci/docker/host-x86_64/dist-various-2/Dockerfile" "$WORK/Dockerfile"
sdk="$(sed -nE 's#.*/wasi-sdk/releases/download/(wasi-sdk-[0-9]+)/.*#\1#p' "$WORK/Dockerfile" | sort -u)"
case "$sdk" in
  wasi-sdk-[0-9]*) [ "$(printf '%s\n' "$sdk" | wc -l)" -eq 1 ] || die "the Rust $channel Dockerfile names more than one wasi-sdk release: $(printf '%s' "$sdk" | tr '\n' ' ')" ;;
  *) die "could not resolve the wasi-sdk release of Rust $channel from src/ci/docker/host-x86_64/dist-various-2/Dockerfile" ;;
esac

# 2. wasi-sdk release -> wasi-libc commit
get "$API/repos/WebAssembly/wasi-sdk/contents/src/wasi-libc?ref=$sdk" "$WORK/submodule.json"
commit="$(tr -d '\n' < "$WORK/submodule.json" | sed -nE 's/.*"sha": *"([0-9a-f]{40})".*"type": *"submodule".*/\1/p')"
[ -n "$commit" ] || die "could not resolve the wasi-libc commit pinned by $sdk (src/wasi-libc is not a submodule there)"

# 3. the texts
mkdir -p "$WORK/stage/rust-$channel" "$WORK/stage/wasi-libc-$commit"
get "$RAW/rust-lang/rust/$channel/COPYRIGHT" "$WORK/stage/rust-$channel/COPYRIGHT"
for f in LICENSE LICENSE-MIT LICENSE-APACHE-LLVM \
  libc-top-half/musl/COPYRIGHT libc-bottom-half/cloudlibc/LICENSE fts/musl-fts/COPYING; do
  mkdir -p "$WORK/stage/wasi-libc-$commit/$(dirname "$f")"
  get "$RAW/WebAssembly/wasi-libc/$commit/$f" "$WORK/stage/wasi-libc-$commit/$f"
done
for f in "$WORK/stage/rust-$channel/COPYRIGHT" "$WORK/stage/wasi-libc-$commit/LICENSE"; do
  [ -s "$f" ] || die "the downloaded ${f#"$WORK/stage/"} is empty"
done

# 4. into place
mkdir -p "$DEST"
rm -rf "$DEST/rust-$channel" "$DEST/wasi-libc-$commit"
mv "$WORK/stage/rust-$channel" "$DEST/rust-$channel"
mv "$WORK/stage/wasi-libc-$commit" "$DEST/wasi-libc-$commit"

tsv="$DEST/toolchains.tsv"
{
  if [ -f "$tsv" ]; then
    awk -F'\t' -v c="$channel" '$1 != c' "$tsv"
  else
    printf '%s\n' \
      '# One row per Rust channel an embedded plugin pins, tab-separated:' \
      '#   channel      the release named in the plugin'"'"'s rust-toolchain.toml; also the tag of' \
      '#                rust-lang/rust that rust-<channel>/COPYRIGHT is read from' \
      '#   wasi-sdk     the wasi-sdk release the Rust release builds its WASI targets with, read from' \
      '#                src/ci/docker/host-x86_64/dist-various-2/Dockerfile at that tag' \
      '#   wasi-libc    the commit of wasi-libc that release pins as src/wasi-libc, read through the' \
      '#                GitHub contents API; wasi-libc-<commit>/ holds its license files' \
      '# Written by scripts/fetch-toolchain-notices.sh.'
  fi
  printf '%s\t%s\t%s\n' "$channel" "$sdk" "$commit"
} > "$WORK/toolchains.tsv.new"
{ grep '^#' "$WORK/toolchains.tsv.new" || true; grep -v '^#' "$WORK/toolchains.tsv.new" | sort -t "$(printf '\t')" -k1,1V; } > "$WORK/toolchains.tsv.sorted"
mv "$WORK/toolchains.tsv.sorted" "$tsv"

echo "fetch-toolchain-notices: $channel -> $sdk -> wasi-libc $commit (written under $DEST)"
