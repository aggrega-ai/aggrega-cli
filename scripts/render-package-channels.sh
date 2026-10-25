#!/usr/bin/env bash
# aggrega-cli/scripts/render-package-channels.sh — write the Homebrew formula, the
# Scoop manifest and the winget manifest for one release, from that release's
# own files.
#
# The formula installs the default binary for macOS on Apple silicon and on
# Intel and for Linux on 64-bit ARM and 64-bit x86; the Scoop manifest installs
# the default binary for Windows on 64-bit x86 as aggrega.exe; the winget
# manifest installs Aggrega on Windows on 64-bit x86 with the Windows installer,
# for the current user. They are rendered from the templates
# packaging/homebrew/aggrega.rb.in, packaging/scoop/aggrega.json.in and the
# three in packaging/windows/winget/, which carry every line of them; this
# script fills in the version, each file's name, each file's SHA-256, and the
# one-line description of Aggrega the three channels share, which it states
# once, below.
#
# It reads a folder laid out as a release attaches its files: the binaries and
# the installer under the names the release gives them, and the release's
# SHA256SUMS. Before it reads a digest, it holds every file in the folder to
# SHA256SUMS through the smoke library's smoke_check_tree_sums, which refuses
# unless the list names each file exactly once, with the digest of its bytes,
# and nothing else. Every digest the files carry is then read from that list,
# by smoke_listed_digest; nothing here computes one of its own. The file names
# are smoke_release_asset_name's, the namer the release step attaches the files
# with, the installer's name the one smoke_target_installer gives it, and the
# name the Scoop manifest's autoupdate builds from Scoop's own $version is that
# namer's output for the version "$version".
#
# The output is a function of the templates, the version and the list alone,
# so the same release folder renders the same bytes every time.
#
# Usage:
#   scripts/render-package-channels.sh [--channel <channel>]... <release-folder> <version> <output-folder>
#
# <channel> is homebrew, scoop or winget; given once or more, only those
# channels are rendered, and the folder need hold only the files they install
# (each must still be the one SHA256SUMS lists, and SHA256SUMS must list exactly
# the files the folder holds). With no --channel, all three are rendered.
#
# <version> is the version the files' names carry, without a leading v, as
# the release step names them: the release's tag is v<version>. Writes
# <output-folder>/aggrega.rb (homebrew), <output-folder>/aggrega.json (scoop),
# and <output-folder>/winget/ holding Cognitio.Aggrega.yaml,
# Cognitio.Aggrega.installer.yaml and Cognitio.Aggrega.locale.en-US.yaml
# (winget), the three files a pull request to the winget community repository
# adds under manifests/c/Cognitio/Aggrega/<version>/. Each is written under
# another name first and then renamed, replacing an earlier copy, and only once
# every template has been filled: a refusal writes nothing.
#
# Exits 0 when every file is written; 1, naming what it refused, when the
# folder does not match its list, a file a channel installs is missing from it
# or from the list, or a template does not carry a value this script fills in
# or carries one it does not; 2 on a usage error.
set -euo pipefail

usage() {
  echo "usage: render-package-channels.sh [--channel homebrew|scoop|winget]... <release-folder> <version> <output-folder>" >&2
  exit 2
}

declare -A WANT=()
ARGS=()
while [ "$#" -gt 0 ]; do
  case "$1" in
    -h|--help) sed -n '2,/^# or carries one it does not; 2 on a usage error\.$/p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    --channel)
      [ -n "${2:-}" ] || usage
      case "$2" in
        homebrew | scoop | winget) WANT[$2]=1 ;;
        *) echo "render-package-channels: '$2' is not a channel this script renders: homebrew, scoop or winget" >&2; exit 2 ;;
      esac
      shift 2
      ;;
    --) shift; ARGS+=("$@"); break ;;
    -*) echo "render-package-channels: unknown option $1" >&2; usage ;;
    *) ARGS+=("$1"); shift ;;
  esac
done
[ "${#ARGS[@]}" -eq 3 ] || usage
[ "${#WANT[@]}" -gt 0 ] || WANT=([homebrew]=1 [scoop]=1 [winget]=1)
RELEASE="${ARGS[0]}" VERSION="${ARGS[1]}" OUT="${ARGS[2]}"
[ -d "$RELEASE" ] || { echo "render-package-channels: no release folder at $RELEASE" >&2; exit 2; }
[ -d "$OUT" ] || { echo "render-package-channels: no output folder at $OUT" >&2; exit 2; }
case "$VERSION" in
  v*) echo "render-package-channels: give the version without its leading v, as the binaries' names carry it: ${VERSION#v}" >&2; exit 2 ;;
esac
if [[ ! "$VERSION" =~ ^[0-9][0-9A-Za-z.-]*$ ]]; then
  echo "render-package-channels: '$VERSION' is not a version a release names its binaries with" >&2
  exit 2
fi

# The one line every channel describes Aggrega with: the formula's desc, the Scoop
# manifest's description and the winget manifest's ShortDescription, each
# template carrying @DESCRIPTION@ where it goes. It is written into a Ruby
# string, a JSON string and a YAML scalar as it is, so it holds no character any
# of the three would read otherwise.
DESCRIPTION="Deterministic ahead-of-time compiler for deployment artifacts"
case "$DESCRIPTION" in
  *[\"\\#:]* | *$'\n'*)
    echo "render-package-channels: the description holds a character a Ruby string, a JSON string or a YAML scalar would read otherwise" >&2
    exit 1
    ;;
esac

CLI_ROOT="$(cd "$(dirname "$0")/.." && pwd -P)"
# shellcheck source-path=SCRIPTDIR source=lib/platform-smoke-common.sh
. "$CLI_ROOT/scripts/lib/platform-smoke-common.sh"
smoke_require_bash

if ! smoke_check_tree_sums "$RELEASE"; then
  echo "render-package-channels: the files in $RELEASE do not match its SHA256SUMS; nothing was written" >&2
  exit 1
fi

# The one list of what each channel installs: the file, by the target and the
# name the build gives it, and the key its values take in the templates.
declare -A ASSET=() SHA=()
FILES=()
[ -z "${WANT[homebrew]+set}" ] || FILES+=(darwin/arm64:aggrega darwin/amd64:aggrega linux/arm64:aggrega linux/amd64:aggrega)
[ -z "${WANT[scoop]+set}" ] || FILES+=(windows/amd64:aggrega)
[ -z "${WANT[winget]+set}" ] || FILES+=("windows/amd64:$(smoke_target_installer windows/amd64)")
for file in "${FILES[@]}"; do
  target="${file%%:*}" name="${file#*:}"
  key="$(printf '%s' "${target/\//_}" | tr '[:lower:]' '[:upper:]')"
  case "$name" in
    *-setup.exe) key+=_SETUP ;;
  esac
  [ -z "${ASSET[$key]+set}" ] || continue
  ASSET[$key]="$(smoke_release_asset_name "$target" "$VERSION" "$name")"
  if ! SHA[$key]="$(smoke_listed_digest "$RELEASE/SHA256SUMS" "${ASSET[$key]}")"; then
    echo "render-package-channels: the release's SHA256SUMS gives no single digest for ${ASSET[$key]}; nothing was written" >&2
    exit 1
  fi
  printf 'render-package-channels: %s %s\n' "${SHA[$key]}" "${ASSET[$key]}"
done

# fill <template> <key>=<value>... — prints <template> with each @<key>@
# replaced by its value, refusing a key the template does not carry and a
# @KEY@ left over that none of them fills.
fill() {
  local template="$1" text pair key value
  shift
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
  printf '%s\n' "$text"
}

# Every file is filled before any is written, so a template refused leaves the
# output folder as it was.
OUTPUTS=() TEXTS=()
add() { # <output> <template> <key>=<value>...
  local output="$1" text
  shift
  text="$(fill "$@")" || { echo "render-package-channels: nothing was written" >&2; exit 1; }
  OUTPUTS+=("$output") TEXTS+=("$text")
}
if [ -n "${WANT[homebrew]+set}" ]; then
  add "$OUT/aggrega.rb" "$CLI_ROOT/packaging/homebrew/aggrega.rb.in" \
    "VERSION=$VERSION" "DESCRIPTION=$DESCRIPTION" \
    "ASSET_DARWIN_ARM64=${ASSET[DARWIN_ARM64]}" "SHA256_DARWIN_ARM64=${SHA[DARWIN_ARM64]}" \
    "ASSET_DARWIN_AMD64=${ASSET[DARWIN_AMD64]}" "SHA256_DARWIN_AMD64=${SHA[DARWIN_AMD64]}" \
    "ASSET_LINUX_ARM64=${ASSET[LINUX_ARM64]}" "SHA256_LINUX_ARM64=${SHA[LINUX_ARM64]}" \
    "ASSET_LINUX_AMD64=${ASSET[LINUX_AMD64]}" "SHA256_LINUX_AMD64=${SHA[LINUX_AMD64]}"
fi
if [ -n "${WANT[scoop]+set}" ]; then
  # shellcheck disable=SC2016 # $version is Scoop's own variable, written as it is.
  add "$OUT/aggrega.json" "$CLI_ROOT/packaging/scoop/aggrega.json.in" \
    "VERSION=$VERSION" "DESCRIPTION=$DESCRIPTION" \
    "ASSET_WINDOWS_AMD64=${ASSET[WINDOWS_AMD64]}" "SHA256_WINDOWS_AMD64=${SHA[WINDOWS_AMD64]}" \
    "ASSET_WINDOWS_AMD64_AUTOUPDATE=$(smoke_release_asset_name windows/amd64 '$version' aggrega)"
fi
if [ -n "${WANT[winget]+set}" ]; then
  WINGET_TEMPLATES="$CLI_ROOT/packaging/windows/winget"
  add "$OUT/winget/Cognitio.Aggrega.yaml" "$WINGET_TEMPLATES/Cognitio.Aggrega.yaml.in" \
    "VERSION=$VERSION"
  add "$OUT/winget/Cognitio.Aggrega.installer.yaml" "$WINGET_TEMPLATES/Cognitio.Aggrega.installer.yaml.in" \
    "VERSION=$VERSION" \
    "ASSET_WINDOWS_AMD64_SETUP=${ASSET[WINDOWS_AMD64_SETUP]}" "SHA256_WINDOWS_AMD64_SETUP=${SHA[WINDOWS_AMD64_SETUP]}"
  add "$OUT/winget/Cognitio.Aggrega.locale.en-US.yaml" "$WINGET_TEMPLATES/Cognitio.Aggrega.locale.en-US.yaml.in" \
    "VERSION=$VERSION" "DESCRIPTION=$DESCRIPTION"
  mkdir -p -- "$OUT/winget"
fi

for i in "${!OUTPUTS[@]}"; do
  printf '%s\n' "${TEXTS[$i]}" > "${OUTPUTS[$i]}.partial"
  mv -f -- "${OUTPUTS[$i]}.partial" "${OUTPUTS[$i]}"
  echo "render-package-channels: wrote ${OUTPUTS[$i]}"
done
