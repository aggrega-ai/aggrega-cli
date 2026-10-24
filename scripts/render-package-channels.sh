#!/usr/bin/env bash
# aggrega-cli/scripts/render-package-channels.sh — write the Homebrew formula and
# the Scoop manifest for one release, from that release's own files.
#
# The formula installs the default binary for macOS on Apple silicon and on
# Intel and for Linux on 64-bit ARM and 64-bit x86; the manifest installs the
# default binary for Windows on 64-bit x86 as aggrega.exe. Both are rendered
# from the templates packaging/homebrew/aggrega.rb.in and
# packaging/scoop/aggrega.json.in, which carry every line of them; this script
# fills in the version, each binary's file name and each binary's SHA-256.
#
# It reads a folder laid out as a release attaches its files: the binaries
# under the names the release gives them, and the release's SHA256SUMS. Before
# it reads a digest, it holds every file in the folder to SHA256SUMS through the
# smoke library's smoke_check_tree_sums, which refuses unless the list names
# each file exactly once, with the digest of its bytes, and nothing else. Every
# digest the formula and the manifest carry is then read from that list, by
# smoke_listed_digest; nothing here computes one of its own. The file names are
# smoke_release_asset_name's, the namer the release step attaches the binaries
# with, and the name the Scoop manifest's autoupdate builds from Scoop's own
# $version is that namer's output for the version "$version".
#
# The output is a function of the templates, the version and the list alone,
# so the same release folder renders the same bytes every time.
#
# Usage:
#   scripts/render-package-channels.sh <release-folder> <version> <output-folder>
#
# <version> is the version the binaries' names carry, without a leading v, as
# the release step names them: the release's tag is v<version>. Writes
# <output-folder>/aggrega.rb and <output-folder>/aggrega.json, each under
# another name first and then renamed, replacing an earlier copy.
#
# Exits 0 when both are written; 1, naming what it refused, when the folder
# does not match its list, a binary is missing from it or from the list, or a
# template does not carry a value this script fills in or carries one it does
# not; 2 on a usage error.
set -euo pipefail

case "${1:-}" in
  -h|--help) sed -n '2,/^# not; 2 on a usage error\.$/p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
esac

usage() {
  echo "usage: render-package-channels.sh <release-folder> <version> <output-folder>" >&2
  exit 2
}
[ "$#" -eq 3 ] || usage
RELEASE="$1" VERSION="$2" OUT="$3"
[ -d "$RELEASE" ] || { echo "render-package-channels: no release folder at $RELEASE" >&2; exit 2; }
[ -d "$OUT" ] || { echo "render-package-channels: no output folder at $OUT" >&2; exit 2; }
case "$VERSION" in
  v*) echo "render-package-channels: give the version without its leading v, as the binaries' names carry it: ${VERSION#v}" >&2; exit 2 ;;
esac
if [[ ! "$VERSION" =~ ^[0-9][0-9A-Za-z.-]*$ ]]; then
  echo "render-package-channels: '$VERSION' is not a version a release names its binaries with" >&2
  exit 2
fi

CLI_ROOT="$(cd "$(dirname "$0")/.." && pwd -P)"
# shellcheck source-path=SCRIPTDIR source=lib/platform-smoke-common.sh
. "$CLI_ROOT/scripts/lib/platform-smoke-common.sh"
smoke_require_bash

if ! smoke_check_tree_sums "$RELEASE"; then
  echo "render-package-channels: the files in $RELEASE do not match its SHA256SUMS; nothing was written" >&2
  exit 1
fi

# The one list of what the two files install: the target, and the key its
# values take in the templates.
declare -A ASSET=() SHA=()
for target in darwin/arm64 darwin/amd64 linux/arm64 linux/amd64 windows/amd64; do
  key="$(printf '%s' "${target/\//_}" | tr '[:lower:]' '[:upper:]')"
  ASSET[$key]="$(smoke_release_asset_name "$target" "$VERSION" aggrega)"
  if ! SHA[$key]="$(smoke_listed_digest "$RELEASE/SHA256SUMS" "${ASSET[$key]}")"; then
    echo "render-package-channels: the release's SHA256SUMS gives no single digest for ${ASSET[$key]}; nothing was written" >&2
    exit 1
  fi
  printf 'render-package-channels: %s %s\n' "${SHA[$key]}" "${ASSET[$key]}"
done
# shellcheck disable=SC2016 # $version is Scoop's own variable, written as it is.
AUTOUPDATE_ASSET="$(smoke_release_asset_name windows/amd64 '$version' aggrega)"

# render <template> <output> <key>=<value>... — writes <output> from
# <template> with each @<key>@ replaced by its value, refusing a key the
# template does not carry and a @KEY@ left over that none of them fills.
render() {
  local template="$1" output="$2" text pair key value
  shift 2
  if [ ! -f "$template" ]; then
    echo "render-package-channels: no template at $template" >&2
    return 1
  fi
  text="$(cat "$template")"
  for pair in "$@"; do
    key="${pair%%=*}" value="${pair#*=}"
    if [[ "$text" != *"@$key@"* ]]; then
      echo "render-package-channels: $template carries no @$key@" >&2
      return 1
    fi
    text="${text//"@$key@"/"$value"}"
  done
  if [[ "$text" =~ @[A-Z][A-Z0-9_]*@ ]]; then
    echo "render-package-channels: $template carries ${BASH_REMATCH[0]}, which this script does not fill in" >&2
    return 1
  fi
  printf '%s\n' "$text" > "$output.partial"
  mv -f -- "$output.partial" "$output"
  echo "render-package-channels: wrote $output"
}

render "$CLI_ROOT/packaging/homebrew/aggrega.rb.in" "$OUT/aggrega.rb" \
  "VERSION=$VERSION" \
  "ASSET_DARWIN_ARM64=${ASSET[DARWIN_ARM64]}" "SHA256_DARWIN_ARM64=${SHA[DARWIN_ARM64]}" \
  "ASSET_DARWIN_AMD64=${ASSET[DARWIN_AMD64]}" "SHA256_DARWIN_AMD64=${SHA[DARWIN_AMD64]}" \
  "ASSET_LINUX_ARM64=${ASSET[LINUX_ARM64]}" "SHA256_LINUX_ARM64=${SHA[LINUX_ARM64]}" \
  "ASSET_LINUX_AMD64=${ASSET[LINUX_AMD64]}" "SHA256_LINUX_AMD64=${SHA[LINUX_AMD64]}" \
  || exit 1

render "$CLI_ROOT/packaging/scoop/aggrega.json.in" "$OUT/aggrega.json" \
  "VERSION=$VERSION" \
  "ASSET_WINDOWS_AMD64=${ASSET[WINDOWS_AMD64]}" "SHA256_WINDOWS_AMD64=${SHA[WINDOWS_AMD64]}" \
  "ASSET_WINDOWS_AMD64_AUTOUPDATE=$AUTOUPDATE_ASSET" \
  || exit 1
