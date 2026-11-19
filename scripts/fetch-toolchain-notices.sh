#!/usr/bin/env bash
# aggrega-cli/scripts/fetch-toolchain-notices.sh
#
# Fetches, into third-party/toolchain/, the license texts of what a toolchain
# builds into a WebAssembly plugin, pinned to that toolchain. Two forms:
#
#   scripts/fetch-toolchain-notices.sh <channel>          (for example 1.85.1)
#   scripts/fetch-toolchain-notices.sh extism-js <tag>    (for example v1.5.1)
#
# A Rust channel: what a Rust toolchain links into a wasm32-wasip1 build.
#
#   rust-<channel>/COPYRIGHT        the Rust project's COPYRIGHT file at the tag
#                                   named <channel> (the standard library's notice)
#   rust-<channel>/LICENSE-APACHE   the Rust project's LICENSE-APACHE file at that
#                                   tag (the text of its Apache-2.0 alternative)
#   wasi-libc-<commit>/...          the license files of wasi-libc at the commit the
#                                   toolchain's release builds its libc.a from
#   toolchains.tsv                  one row per channel: channel, wasi-sdk release,
#                                   wasi-libc commit
#
# Run it when a plugin's rust-toolchain.toml moves to a channel that
# third-party/toolchain/ does not hold yet. It resolves, in order:
#
#   1. the wasi-sdk release the Rust release builds its WASI target with, read from
#      src/ci/docker/host-x86_64/dist-various-2/Dockerfile at the Rust tag;
#   2. the wasi-libc commit that wasi-sdk release pins as its src/wasi-libc
#      submodule, read through the GitHub contents API.
#
# An extism-js release: the JavaScript runtime the extism-js compiler of that
# release of github.com/extism/js-pdk merges into every TypeScript plugin's .wasm.
#
#   js-pdk-<tag>/LICENSE            the Extism JS PDK's LICENSE at the tag
#   quickjs-ng-<commit>/LICENSE     the LICENSE of QuickJS-NG, the JavaScript engine,
#                                   at the commit the runtime's rquickjs release
#                                   builds it from
#   rquickjs-<version>/LICENSE      the LICENSE of rquickjs, the bindings that build
#                                   the engine into the runtime, at its tag v<version>
#   core-js-<version>/LICENSE       the LICENSEs of core-js and urlpattern-polyfill,
#   urlpattern-polyfill-<version>/LICENSE
#                                   which the runtime's JavaScript prelude bundles,
#                                   at the commit npm records for that version
#   extism-js.tsv                   one row per extism-js binary the release
#                                   publishes, one per platform, with the sha256 of
#                                   its bytes and what its runtime carries
#
# It resolves, in order:
#
#   1. the release's binaries: each extism-js-<platform>-<tag>.gz asset is
#      downloaded, decompressed and hashed, as data;
#   2. the commit the tag names, and the versions of core-js and
#      urlpattern-polyfill in crates/core/src/prelude/package-lock.json at the tag;
#   3. the rquickjs version the runtime is built with: the one rquickjs-core-<version>
#      source path every binary carries;
#   4. the Rust release the runtime is built with: the one /rustc/<commit> source
#      path the x86_64-linux binary carries, which its `rustc version` line names
#      and the Rust release tag confirms;
#   5. the QuickJS-NG commit rquickjs v<version> pins as its sys/quickjs submodule,
#      and the version its quickjs.h states;
#   6. the commit and GitHub repository the npm registry records for each of the two
#      prelude packages.
#
# The Rust release of step 4 is attributed through its row of toolchains.tsv; when
# it has none, the form prints the command that fetches it.
#
# Either form refuses, exit 1 naming what it could not resolve or download, and
# then changes nothing. Everything is downloaded as data and never executed. The
# generator (gen-third-party-licenses.sh) works offline from what this writes. The
# extism-js form needs jq.
set -euo pipefail

usage() {
  echo "usage: fetch-toolchain-notices.sh <channel>          (for example 1.85.1)" >&2
  echo "       fetch-toolchain-notices.sh extism-js <tag>    (for example v1.5.1)" >&2
  exit 2
}

if [ "$#" -eq 2 ] && [ "$1" = extism-js ] && [ -n "$2" ]; then
  form=extism-js
  tag="$2"
  case "$tag" in
    *[!0-9A-Za-z._-]*) echo "fetch-toolchain-notices: the extism-js tag \"$tag\" is not a release tag" >&2; exit 2 ;;
  esac
elif [ "$#" -eq 1 ] && [ -n "$1" ] && [ "$1" != extism-js ]; then
  form=rust
  channel="$1"
  case "$channel" in
    *[!0-9A-Za-z._-]*) echo "fetch-toolchain-notices: the channel \"$channel\" is not a release name" >&2; exit 2 ;;
  esac
else
  usage
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEST="$ROOT/third-party/toolchain"
RAW="https://raw.githubusercontent.com"
API="https://api.github.com"
NPM="https://registry.npmjs.org"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

die() { echo "fetch-toolchain-notices: $*" >&2; exit 1; }

get() { # URL OUTFILE
  curl -fsSL --retry 3 "$1" -o "$2" || die "could not download $1"
}

# tag_commit OWNER/REPO TAG : the commit the tag names, through an annotated tag.
tag_commit() {
  local type sha
  get "$API/repos/$1/git/ref/tags/$2" "$WORK/ref.json"
  type="$(jq -r '.object.type // empty' "$WORK/ref.json")"
  sha="$(jq -r '.object.sha // empty' "$WORK/ref.json")"
  if [ "$type" = tag ]; then
    get "$API/repos/$1/git/tags/$sha" "$WORK/tag.json"
    type="$(jq -r '.object.type // empty' "$WORK/tag.json")"
    sha="$(jq -r '.object.sha // empty' "$WORK/tag.json")"
  fi
  case "$type:$sha" in
    commit:[0-9a-f]*) [ "${#sha}" -eq 40 ] || die "could not resolve the commit the tag $2 of $1 names" ;;
    *) die "could not resolve the commit the tag $2 of $1 names" ;;
  esac
  printf '%s\n' "$sha"
}

# one_match FILE ERE WHAT : the one distinct match of ERE in the bytes of FILE;
# refuses naming WHAT when there is none or more than one.
one_match() {
  local m
  m="$(LC_ALL=C grep -aoE "$2" "$1" | sort -u || true)"
  [ -n "$m" ] || die "$3: none found in $(basename "$1")"
  [ "$(printf '%s\n' "$m" | wc -l)" -eq 1 ] || die "$3: more than one in $(basename "$1"): $(printf '%s' "$m" | tr '\n' ' ')"
  printf '%s\n' "$m"
}

fetch_extism_js() {
  command -v jq > /dev/null || die "the extism-js form needs jq"
  local repo=extism/js-pdk asset platform sha rq="" r f name ver json url head gh dir
  local stage="$WORK/stage"
  mkdir -p "$WORK/bin" "$stage"

  # 1. the release's binaries
  get "$API/repos/$repo/releases/tags/$tag" "$WORK/release.json"
  jq -r --arg t "$tag" '.assets[] | select(.name | test("^extism-js-.+-" + ($t | gsub("\\."; "\\.")) + "\\.gz$")) | .name' \
    "$WORK/release.json" | LC_ALL=C sort > "$WORK/assets.txt"
  [ -s "$WORK/assets.txt" ] || die "the release $tag of $repo publishes no extism-js-<platform>-$tag.gz binary"
  grep -qx "extism-js-x86_64-linux-$tag.gz" "$WORK/assets.txt" \
    || die "the release $tag of $repo publishes no x86_64-linux binary, which the runtime's Rust release is read from"
  : > "$WORK/binaries.tsv"
  while IFS= read -r asset; do
    platform="${asset#extism-js-}"
    platform="${platform%-"$tag".gz}"
    get "https://github.com/$repo/releases/download/$tag/$asset" "$WORK/$asset"
    gunzip -c "$WORK/$asset" > "$WORK/bin/$platform" || die "could not decompress $asset"
    sha="$(sha256sum < "$WORK/bin/$platform" | awk '{ print $1 }')"
    # 3. every binary carries the same runtime, built with one rquickjs release
    r="$(one_match "$WORK/bin/$platform" 'rquickjs-core-[0-9]+\.[0-9]+\.[0-9]+[0-9A-Za-z.+-]*/' "the rquickjs-core source path of the $platform binary")"
    r="${r#rquickjs-core-}"
    r="${r%/}"
    [ -z "$rq" ] || [ "$rq" = "$r" ] || die "the $tag binaries carry different rquickjs releases: $rq and $r ($platform)"
    rq="$r"
    printf '%s\t%s\n' "$platform" "$sha" >> "$WORK/binaries.tsv"
  done < "$WORK/assets.txt"

  # 2. the tag's commit and its prelude's bundled packages
  local pdk_commit
  pdk_commit="$(tag_commit "$repo" "$tag")" || exit 1
  mkdir -p "$stage/js-pdk-$tag"
  get "$RAW/$repo/$pdk_commit/LICENSE" "$stage/js-pdk-$tag/LICENSE"
  get "$RAW/$repo/$pdk_commit/crates/core/src/prelude/package-lock.json" "$WORK/prelude-lock.json"
  local cj up
  cj="$(jq -r '.packages["node_modules/core-js"].version // empty' "$WORK/prelude-lock.json")"
  up="$(jq -r '.packages["node_modules/urlpattern-polyfill"].version // empty' "$WORK/prelude-lock.json")"
  [ -n "$cj" ] || die "the prelude package-lock.json of $repo $tag names no core-js"
  [ -n "$up" ] || die "the prelude package-lock.json of $repo $tag names no urlpattern-polyfill"

  # 4. the Rust release the runtime is built with
  local linux="$WORK/bin/x86_64-linux" rust_commit rust_line rust
  rust_commit="$(one_match "$linux" '/rustc/[0-9a-f]{40}/' "the /rustc/<commit> source path of the x86_64-linux binary")"
  rust_commit="${rust_commit#/rustc/}"
  rust_commit="${rust_commit%/}"
  rust_line="$(one_match "$linux" 'rustc version [0-9]+\.[0-9]+\.[0-9]+ \([0-9a-f]+ ' "the rustc version line of the x86_64-linux binary")"
  rust="$(printf '%s\n' "$rust_line" | awk '{ print $3 }')"
  case "$rust_commit" in
    "$(printf '%s\n' "$rust_line" | awk '{ print substr($4, 2) }')"*) ;;
    *) die "the x86_64-linux binary of $tag names Rust $rust, whose short commit is not its /rustc/$rust_commit path" ;;
  esac
  [ "$(tag_commit rust-lang/rust "$rust")" = "$rust_commit" ] \
    || die "the Rust release tag $rust does not name the commit $rust_commit the $tag runtime is built with"

  # 5. rquickjs and the QuickJS-NG commit it pins
  mkdir -p "$stage/rquickjs-$rq"
  get "$RAW/DelSkayn/rquickjs/v$rq/LICENSE" "$stage/rquickjs-$rq/LICENSE"
  get "$API/repos/DelSkayn/rquickjs/contents/sys/quickjs?ref=v$rq" "$WORK/submodule.json"
  local qc qv
  [ "$(jq -r '.type // empty' "$WORK/submodule.json")" = submodule ] \
    || die "sys/quickjs is not a submodule at rquickjs v$rq"
  case "$(jq -r '.submodule_git_url // empty' "$WORK/submodule.json")" in
    https://github.com/quickjs-ng/quickjs|https://github.com/quickjs-ng/quickjs.git) ;;
    *) die "sys/quickjs at rquickjs v$rq is not github.com/quickjs-ng/quickjs" ;;
  esac
  qc="$(jq -r '.sha // empty' "$WORK/submodule.json")"
  case "$qc" in
    *[!0-9a-f]*|"") die "could not resolve the QuickJS-NG commit rquickjs v$rq pins" ;;
  esac
  [ "${#qc}" -eq 40 ] || die "could not resolve the QuickJS-NG commit rquickjs v$rq pins"
  get "$RAW/quickjs-ng/quickjs/$qc/quickjs.h" "$WORK/quickjs.h"
  qv="$(awk '
    $1 == "#define" && $2 == "QJS_VERSION_MAJOR" { ma = $3 }
    $1 == "#define" && $2 == "QJS_VERSION_MINOR" { mi = $3 }
    $1 == "#define" && $2 == "QJS_VERSION_PATCH" { pa = $3 }
    $1 == "#define" && $2 == "QJS_VERSION_SUFFIX" { su = $3; gsub(/"/, "", su) }
    END { if (ma != "" && mi != "" && pa != "") print ma "." mi "." pa (su == "" ? "" : "-" su) }' "$WORK/quickjs.h")"
  [ -n "$qv" ] || die "the quickjs.h of QuickJS-NG $qc states no QJS_VERSION"
  mkdir -p "$stage/quickjs-ng-$qc"
  get "$RAW/quickjs-ng/quickjs/$qc/LICENSE" "$stage/quickjs-ng-$qc/LICENSE"

  # 6. the prelude's bundled packages
  local commits=()
  for name in core-js urlpattern-polyfill; do
    if [ "$name" = core-js ]; then ver="$cj"; else ver="$up"; fi
    json="$WORK/npm-$name.json"
    get "$NPM/$name/$ver" "$json"
    head="$(jq -r '.gitHead // empty' "$json")"
    url="$(jq -r 'if (.repository | type) == "object" then .repository.url else (.repository // empty) end' "$json")"
    gh="$(printf '%s\n' "$url" | sed -nE 's#^(git\+)?(https|git|ssh)://(git@)?github\.com/([^/]+/[^/.]+)(\.git)?$#\4#p')"
    [ -n "$gh" ] || die "the npm registry names no GitHub repository for $name $ver"
    case "$head" in
      *[!0-9a-f]*|"") die "the npm registry records no commit for $name $ver" ;;
    esac
    [ "${#head}" -eq 40 ] || die "the npm registry records no commit for $name $ver"
    dir="$stage/$name-$ver"
    mkdir -p "$dir"
    get "$RAW/$gh/$head/LICENSE" "$dir/LICENSE"
    commits+=("$head")
  done

  for f in "js-pdk-$tag/LICENSE" "rquickjs-$rq/LICENSE" "quickjs-ng-$qc/LICENSE" \
    "core-js-$cj/LICENSE" "urlpattern-polyfill-$up/LICENSE"; do
    [ -s "$stage/$f" ] || die "the downloaded $f is empty"
  done

  # into place
  mkdir -p "$DEST"
  for f in "js-pdk-$tag" "rquickjs-$rq" "quickjs-ng-$qc" "core-js-$cj" "urlpattern-polyfill-$up"; do
    rm -rf "${DEST:?}/$f"
    mv "$stage/$f" "$DEST/$f"
  done

  local tsv="$DEST/extism-js.tsv"
  {
    if [ -f "$tsv" ]; then
      awk -F'\t' -v t="$tag" '$1 != t' "$tsv"
    else
      printf '%s\n' \
        '# One row per extism-js binary a release of github.com/extism/js-pdk publishes, tab-separated:' \
        '#   tag            the release tag; js-pdk-<tag>/LICENSE is read at its commit' \
        '#   platform       the binary'"'"'s platform, from its asset name extism-js-<platform>-<tag>.gz' \
        '#   sha256         the sha256 of the decompressed binary, which identifies it' \
        '#   commit         the commit the tag names' \
        '#   rquickjs       the rquickjs release the runtime is built with, read from the binaries;' \
        '#                  rquickjs-<version>/LICENSE is read at its tag v<version>' \
        '#   quickjs-ng     the QuickJS-NG commit that rquickjs release pins as sys/quickjs;' \
        '#                  quickjs-ng-<commit>/LICENSE is read at it' \
        '#   quickjs-ng-version  the version the quickjs.h of that commit states' \
        '#   core-js, core-js-commit, urlpattern-polyfill, urlpattern-polyfill-commit' \
        '#                  the version crates/core/src/prelude/package-lock.json pins at the tag,' \
        '#                  and the commit the npm registry records for it, which' \
        '#                  <name>-<version>/LICENSE is read at' \
        '#   rust           the Rust release the runtime is built with, read from the x86_64-linux' \
        '#                  binary; toolchains.tsv holds its row' \
        '# Written by scripts/fetch-toolchain-notices.sh extism-js <tag>.'
    fi
    while IFS=$'\t' read -r platform sha; do
      printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$tag" "$platform" "$sha" "$pdk_commit" \
        "$rq" "$qc" "$qv" "$cj" "${commits[0]}" "$up" "${commits[1]}" "$rust"
    done < "$WORK/binaries.tsv"
  } > "$WORK/extism-js.tsv.new"
  { grep '^#' "$WORK/extism-js.tsv.new" || true; grep -v '^#' "$WORK/extism-js.tsv.new" | sort -t "$(printf '\t')" -k1,1V -k2,2; } > "$WORK/extism-js.tsv.sorted"
  mv "$WORK/extism-js.tsv.sorted" "$tsv"

  echo "fetch-toolchain-notices: extism-js $tag -> rquickjs $rq, QuickJS-NG $qv ($qc), core-js $cj, urlpattern-polyfill $up, Rust $rust; $(wc -l < "$WORK/binaries.tsv" | tr -d ' ') binaries (written under $DEST)"
  if ! awk -F'\t' -v c="$rust" '$1 == c { f = 1 } END { exit !f }' "$DEST/toolchains.tsv" 2> /dev/null; then
    echo "fetch-toolchain-notices: the $tag runtime is built with Rust $rust, which toolchains.tsv has no row for; fetch it with: scripts/fetch-toolchain-notices.sh $rust"
  fi
}

if [ "$form" = extism-js ]; then
  fetch_extism_js
  exit 0
fi

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
get "$RAW/rust-lang/rust/$channel/LICENSE-APACHE" "$WORK/stage/rust-$channel/LICENSE-APACHE"
for f in LICENSE LICENSE-MIT LICENSE-APACHE-LLVM \
  libc-top-half/musl/COPYRIGHT libc-bottom-half/cloudlibc/LICENSE fts/musl-fts/COPYING; do
  mkdir -p "$WORK/stage/wasi-libc-$commit/$(dirname "$f")"
  get "$RAW/WebAssembly/wasi-libc/$commit/$f" "$WORK/stage/wasi-libc-$commit/$f"
done
for f in "$WORK/stage/rust-$channel/COPYRIGHT" "$WORK/stage/rust-$channel/LICENSE-APACHE" "$WORK/stage/wasi-libc-$commit/LICENSE"; do
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
