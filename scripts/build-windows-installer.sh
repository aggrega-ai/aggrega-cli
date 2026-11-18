#!/usr/bin/env bash
# aggrega-cli/scripts/build-windows-installer.sh — build the Windows installer,
# aggrega-<version>-windows-amd64-setup.exe as a release attaches it, from
# packaging/windows/aggrega.nsi, with NSIS's makensis, on a Linux x86-64 machine.
#
# The installer and every program file it carries are signed, and the signing
# never runs in a job that runs third-party code such as makensis, so the build
# is in two parts, with a signing after each:
#
#   parts      writes the two program files the installer carries beside
#              aggrega.exe, under the names smoke_target_installer_parts gives
#              them: uninstall.exe, the uninstaller, as makensis generates it
#              from the script, and EnVar.dll, the plug-in that edits the user
#              PATH, as its package carries it. The release signs these with the
#              Windows binaries.
#   installer  writes the installer, under the name smoke_target_installer gives
#              it, carrying the aggrega.exe and the parts it is given — the signed
#              ones, or on a run that signs nothing the unsigned ones. makensis
#              generates the uninstaller again, and it must be byte for byte the
#              one parts wrote, given here as --unsigned-parts; the uninstaller
#              given in --parts is then stored in its place, so the signature it
#              carries is a signature of exactly the uninstaller this script
#              generates. The release then signs the installer.
#
# NSIS is Debian's 3.12-1 build, the nsis package (makensis) and the
# nsis-common package (the installer stubs and the plug-ins), fetched from
# snapshot.debian.org, which keeps every package it has archived at a fixed
# address; the plug-in is EnVar, fetched from the NSIS wiki. Each is fetched at
# one version and refused, before anything in it is unpacked or run, unless its
# SHA-256 is the one written below; moving to another version changes both
# lines. The plug-ins nsis-common carries are deleted once it is unpacked, so the
# one plug-in makensis can find is EnVar: a script calling any other fails to
# compile, so the installer carries no plug-in but the EnVar.dll it is given.
#
# The packages are unpacked, and makensis runs, in a container of the Debian
# image written below, pinned by its digest: the Debian release those packages
# were built for, so the C and C++ libraries makensis needs are the ones it was
# built against, whatever the machine runs. The container has no network, a
# read-only root and an empty /tmp, runs as the user running this script, and
# sees four folders of this machine, each at its own path: this script's
# folder and the folder holding the installer's script, read-only, and this
# script's temporary directory and --output-dir, which it writes. Every other
# file it reads is copied into the temporary directory first. So the machine
# needs docker, able to pull that image and to mount this machine's own folders
# into a container that writes them as the user it runs as — a daemon on this
# machine, run as root — and curl, sha256sum and unzip, which fetch and check
# the packages and take the plug-in out of its archive. A machine missing one is
# refused, naming it. When the image is not in docker's image store already,
# this script pulls it, and removes it again when it exits, a failure or a
# refusal included; an image that was there already is left as it is.
#
# The installer is the same bytes whenever it is built from the same inputs:
# the script has makensis record no file times, and makensis records no time of
# its own.
#
# The installer's license page shows the license this repository's LICENSE
# holds, read when the installer is built, and the install places it in the
# install folder as LICENSE. NSIS's reference asks for the text file of such a
# page in DOS format, with Windows line endings, so the copy it is given is
# LICENSE with each line ending turned into a carriage return and a line feed,
# and nothing else changed; before makensis runs, that copy is held to LICENSE,
# line endings aside, and refused unless it matches. A LICENSE that is missing,
# empty, or holds a byte that is not ASCII is refused, naming it: makensis would
# read such a byte in the code page of the machine building the installer. The
# welcome and finish pages, which are license pages too, are given welcome.txt
# and finish.txt as they are written, with Unix line endings: makensis reads
# them from packaging/windows/ itself, and bringing them through this
# conversion too is outside it, which exists for the license page alone.
#
# The install also places the third-party notices file this repository holds,
# THIRD-PARTY-NOTICES.txt, in the install folder under that name. No page shows
# it, so it is installed byte for byte, with no line-ending or code-page
# change; one that is missing, not a file, or empty is refused, naming it.
#
# Usage:
#   scripts/build-windows-installer.sh parts --version <version> --aggrega <aggrega.exe> --output-dir <dir>
#   scripts/build-windows-installer.sh installer --version <version> --aggrega <aggrega.exe> \
#       --parts <dir> --unsigned-parts <dir> --output-dir <dir>
#   scripts/build-windows-installer.sh --help
#
# <version> is the version the aggrega binary reports; the installer records it
# as its version and as the version of the installed app, and the version
# resource takes its first three numbers. parts compiles the whole installer to
# generate the uninstaller, so it also needs an aggrega.exe, which it does not
# keep. <dir> for --output-dir must be empty or not exist. --parts and
# --unsigned-parts are directories holding uninstall.exe and EnVar.dll; the
# EnVar.dll in --unsigned-parts must be the one this script fetches.
#
# Writes into --output-dir and a temporary directory of its own, which it
# removes, and into docker's image store the image it pulls, which it removes;
# writes nothing else. Exits 0 when it wrote what it was asked to, 1
# when it could not, and 2 for an argument it cannot use.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd -P)"
CLI_ROOT="$(cd "$SCRIPT_DIR/.." && pwd -P)"
NSI_DIR="$CLI_ROOT/packaging/windows"
NSI="$NSI_DIR/aggrega.nsi"

# The image makensis runs in: Debian forky, as Docker Hub's library/debian
# tagged it forky-20261020-slim, named by the digest of its image index, of
# which docker takes the linux/amd64 image.
NSIS_IMAGE=docker.io/library/debian@sha256:4cee6ff3ebc49e32d55a857e25164ef8e5f8fecff2a186c6393f7e8bd8c23b53
NSIS_DEB_URL=https://snapshot.debian.org/archive/debian/20260504T084255Z/pool/main/n/nsis/nsis_3.12-1_amd64.deb
NSIS_DEB_SHA256=7df43fb0dee49146089c4c9cde11621305fe6c978e1654c061c19b7451ffa340
NSIS_COMMON_DEB_URL=https://snapshot.debian.org/archive/debian/20260504T084255Z/pool/main/n/nsis/nsis-common_3.12-1_all.deb
NSIS_COMMON_DEB_SHA256=03e1944ea80c1f8f833baafffde085c0c68ccd0dca7ada3a0274fa3d239b9274
# The EnVar plug-in's archive, as the NSIS wiki serves it, and the plug-in the
# installer runs, its build for x86-unicode, held to a digest of its own as well.
ENVAR_ZIP_URL=https://nsis.sourceforge.io/mediawiki/images/7/7f/EnVar_plugin.zip
ENVAR_ZIP_SHA256=e9aa92de351345ed82795251d838f1ae9041ba35af9d381a5780c7843b01f56a
ENVAR_DLL_MEMBER=Plugins/x86-unicode/EnVar.dll
ENVAR_DLL_SHA256=dbb0040cd73c83aac965319eaafe81a962154668eb2e7773d79a6a8040b446b0

say() { printf 'build-windows-installer: %s\n' "$*"; }
refuse() { printf 'build-windows-installer: %s\n' "$*" >&2; exit 1; }
usage_error() { printf 'build-windows-installer: %s\n' "$*" >&2; exit 2; }

sha256_of() {
  local sum
  sum="$(sha256sum < "$1")" || return 1
  printf '%s\n' "${sum%% *}"
}

# finalize-uninstaller <file>: run by makensis, through the script's
# !uninstfinalize, on the uninstaller it generated. With
# AGGREGA_UNINSTALLER_EXPORT set, it copies the uninstaller there; with
# AGGREGA_UNINSTALLER_EXPECTED and AGGREGA_UNINSTALLER_REPLACEMENT set, it holds
# the uninstaller to the first, byte for byte, and puts the second in its place.
finalize_uninstaller() {
  local file="$1"
  [ -f "$file" ] || refuse "makensis named no uninstaller at $file"
  if [ -n "${AGGREGA_UNINSTALLER_EXPORT:-}" ]; then
    cp -- "$file" "$AGGREGA_UNINSTALLER_EXPORT"
    say "kept the uninstaller makensis generated, SHA-256 $(sha256_of "$file")"
    return 0
  fi
  if [ -n "${AGGREGA_UNINSTALLER_EXPECTED:-}" ] && [ -n "${AGGREGA_UNINSTALLER_REPLACEMENT:-}" ]; then
    if ! cmp -s -- "$file" "$AGGREGA_UNINSTALLER_EXPECTED"; then
      refuse "the uninstaller makensis generated, SHA-256 $(sha256_of "$file"), is not the one the parts were built with, SHA-256 $(sha256_of "$AGGREGA_UNINSTALLER_EXPECTED"), so the uninstaller given would not be its signed copy"
    fi
    cp -- "$AGGREGA_UNINSTALLER_REPLACEMENT" "$file"
    say "the uninstaller makensis generated is the one the parts were built with; stored $AGGREGA_UNINSTALLER_REPLACEMENT in its place, SHA-256 $(sha256_of "$file")"
    return 0
  fi
  refuse "finalize-uninstaller runs inside makensis, from this script's own build"
}

# fetch <url> <sha256> <file>: download <url> to <file> and refuse it, deleting
# it, unless its SHA-256 is <sha256>.
fetch() {
  local url="$1" want="$2" file="$3" got
  curl -sSfL --retry 3 -o "$file" "$url" || refuse "could not download $url"
  got="$(sha256_of "$file")"
  if [ "$got" != "$want" ]; then
    rm -f -- "$file"
    refuse "$url has SHA-256 $got, not $want, the digest this script pins; refusing to use it"
  fi
  say "$url, SHA-256 $got, the pinned digest"
}

# pull_image: make NSIS_IMAGE's linux/amd64 image available to docker, pulling
# it when the image store does not hold it, and noting that it did.
pull_image() {
  local platform
  if docker image inspect "$NSIS_IMAGE" >/dev/null 2>&1; then
    say "$NSIS_IMAGE is in docker's image store already, and is left there"
  else
    docker pull --quiet --platform linux/amd64 "$NSIS_IMAGE" >/dev/null ||
      refuse "docker could not pull $NSIS_IMAGE, the image makensis runs in"
    PULLED_IMAGE=1
    say "pulled $NSIS_IMAGE"
  fi
  platform="$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$NSIS_IMAGE")" ||
    refuse "docker cannot read $NSIS_IMAGE after pulling it"
  [ "$platform" = linux/amd64 ] || refuse "docker holds $NSIS_IMAGE as a $platform image, not linux/amd64"
}

# remove_image: remove the image pull_image pulled from docker's image store,
# by its ID, since a pull by digest gives it no tag to remove it by. Where
# docker refuses — the store keeps it under another name too — it says so and
# leaves it.
remove_image() {
  local id
  [ "$PULLED_IMAGE" = 1 ] || return 0
  if id="$(docker image inspect --format '{{.Id}}' "$NSIS_IMAGE" 2>/dev/null)"; then
    if docker image rm "$id" >/dev/null; then
      say "removed $NSIS_IMAGE from docker's image store"
    else
      printf "build-windows-installer: could not remove %s (%s) from docker's image store\n" "$NSIS_IMAGE" "$id" >&2
    fi
  fi
}

clean_up() {
  rm -rf -- "$WORK"
  remove_image
}

# in_image <command> [<argument>...]: run a command in a container of
# NSIS_IMAGE, as the header describes, handing it NSISDIR and the three
# AGGREGA_UNINSTALLER_ variables finalize-uninstaller reads, where they are set.
in_image() {
  docker run --rm --platform linux/amd64 --pull never \
    --network none --read-only --tmpfs /tmp \
    --user "$(id -u):$(id -g)" --workdir "$WORK" \
    --env "NSISDIR=$WORK/nsis/usr/share/nsis" \
    --env AGGREGA_UNINSTALLER_EXPORT \
    --env AGGREGA_UNINSTALLER_EXPECTED \
    --env AGGREGA_UNINSTALLER_REPLACEMENT \
    --mount "type=bind,source=$SCRIPT_DIR,target=$SCRIPT_DIR,readonly" \
    --mount "type=bind,source=$NSI_DIR,target=$NSI_DIR,readonly" \
    --mount "type=bind,source=$WORK,target=$WORK" \
    --mount "type=bind,source=$OUTPUT_DIR,target=$OUTPUT_DIR" \
    "$NSIS_IMAGE" "$@"
}

# unpack_deb <deb> <root>: unpack the files a Debian package installs under
# <root>, with the image's dpkg-deb.
unpack_deb() {
  local deb="$1" root="$2"
  mkdir -p "$root"
  in_image dpkg-deb --extract "$deb" "$root" || refuse "dpkg-deb could not unpack $deb"
  say "unpacked ${deb##*/}"
}

# prepare_tools: fetch NSIS and the plug-in into WORK, and unpack them, setting
# MAKENSIS and PLUGIN (the plug-in DLL).
prepare_tools() {
  local tool machine
  machine="$(uname -s)/$(uname -m)"
  [ "$machine" = Linux/x86_64 ] || refuse "the image makensis runs in is taken for x64 Linux, and this machine is $machine"
  for tool in docker curl sha256sum unzip; do
    command -v "$tool" >/dev/null 2>&1 || refuse "$tool is not on PATH, and building the installer needs it"
  done
  pull_image
  fetch "$NSIS_DEB_URL" "$NSIS_DEB_SHA256" "$WORK/nsis.deb"
  fetch "$NSIS_COMMON_DEB_URL" "$NSIS_COMMON_DEB_SHA256" "$WORK/nsis-common.deb"
  fetch "$ENVAR_ZIP_URL" "$ENVAR_ZIP_SHA256" "$WORK/envar.zip"
  unpack_deb "$WORK/nsis.deb" "$WORK/nsis"
  unpack_deb "$WORK/nsis-common.deb" "$WORK/nsis"
  MAKENSIS="$WORK/nsis/usr/bin/makensis"
  [ -x "$MAKENSIS" ] || refuse "the nsis package holds no usr/bin/makensis"
  [ -d "$WORK/nsis/usr/share/nsis/Stubs" ] || refuse "the nsis-common package holds no usr/share/nsis/Stubs"
  # NSIS's own plug-ins are removed, so makensis can resolve no plug-in but the
  # EnVar.dll handed to it through the script's !addplugindir: a call to any
  # other refuses the compile rather than packing a DLL no step signs.
  [ -d "$WORK/nsis/usr/share/nsis/Plugins" ] || refuse "the nsis-common package holds no usr/share/nsis/Plugins"
  rm -rf -- "$WORK/nsis/usr/share/nsis/Plugins"
  mkdir -p "$WORK/plugin"
  unzip -p "$WORK/envar.zip" "$ENVAR_DLL_MEMBER" > "$WORK/plugin/EnVar.dll" ||
    refuse "$ENVAR_ZIP_URL holds no $ENVAR_DLL_MEMBER"
  [ "$(sha256_of "$WORK/plugin/EnVar.dll")" = "$ENVAR_DLL_SHA256" ] ||
    refuse "$ENVAR_DLL_MEMBER in $ENVAR_ZIP_URL has SHA-256 $(sha256_of "$WORK/plugin/EnVar.dll"), not $ENVAR_DLL_SHA256"
  PLUGIN="$WORK/plugin/EnVar.dll"
  local version
  if ! version="$(in_image "$MAKENSIS" -VERSION 2>&1)"; then
    printf '%s\n' "$version" >&2
    refuse "makensis does not run in $NSIS_IMAGE"
  fi
  say "makensis $version, in $NSIS_IMAGE"
}

# check_license_page <page> <license>: refuse the license page's text unless
# every line of it ends in a carriage return and a line feed, and it is
# <license> byte for byte once the carriage return ending each line is dropped
# from both.
check_license_page() {
  local page="$1" license="$2" crs lfs
  crs="$(LC_ALL=C tr -cd '\r' < "$page" | wc -c)"
  lfs="$(LC_ALL=C tr -cd '\n' < "$page" | wc -c)"
  [ "$crs" -eq "$lfs" ] ||
    refuse "the license page's text, $page, has $lfs line feeds and $crs carriage returns, so not every line of it ends in both"
  cmp -s <(LC_ALL=C tr -d '\r' < "$page") <(LC_ALL=C tr -d '\r' < "$license") ||
    refuse "the license page's text, $page, is not $license once line endings are set aside"
}

# prepare_license: write LICENSE_PAGE, the text the license page shows and the
# install places as LICENSE: this repository's LICENSE with Windows line
# endings, the one change makensis needs, held to LICENSE before it is used.
# This is the one place the license text is transformed.
prepare_license() {
  local license="$CLI_ROOT/LICENSE" non_ascii
  [ -e "$license" ] || refuse "$license is missing, and the installer's license page shows it"
  [ -f "$license" ] || refuse "$license is not a file, and the installer's license page shows it"
  [ -s "$license" ] || refuse "$license is empty, and the installer's license page shows it"
  non_ascii="$(LC_ALL=C tr -d '\000-\177' < "$license" | wc -c)"
  [ "$non_ascii" -eq 0 ] ||
    refuse "$license holds $non_ascii bytes that are not ASCII, which makensis would read in the code page of the machine building the installer"
  LICENSE_PAGE="$WORK/input/LICENSE"
  LC_ALL=C sed 's/\r$//; s/$/\r/' "$license" > "$LICENSE_PAGE"
  check_license_page "$LICENSE_PAGE" "$license"
  say "license page: $license with Windows line endings, which is LICENSE once they are set aside, SHA-256 $(sha256_of "$LICENSE_PAGE")"
}

# prepare_notices: copy this repository's THIRD-PARTY-NOTICES.txt, byte for
# byte, to NOTICES_PAGE in the temporary directory the container sees, for the
# install to place beside aggrega.exe.
prepare_notices() {
  local notices="$CLI_ROOT/THIRD-PARTY-NOTICES.txt"
  [ -e "$notices" ] || refuse "$notices is missing, and the installer places it beside aggrega.exe"
  [ -f "$notices" ] || refuse "$notices is not a file, and the installer places it beside aggrega.exe"
  [ -s "$notices" ] || refuse "$notices is empty, and the installer places it beside aggrega.exe"
  NOTICES_PAGE="$WORK/input/THIRD-PARTY-NOTICES.txt"
  cp -- "$notices" "$NOTICES_PAGE"
  say "third-party notices: $notices, unchanged, SHA-256 $(sha256_of "$NOTICES_PAGE")"
}

# compile <aggrega.exe> <plugin dir> <installer>: compile the script, every
# path given being one the container sees.
compile() {
  local exe="$1" plugin_dir="$2" out="$3" log="$WORK/makensis.log" rc=0
  in_image "$MAKENSIS" -V2 -WX -NOCONFIG \
    "-DVERSION=$VERSION" "-DVI_VERSION=$VI_VERSION" \
    "-DAGGREGA_EXE=$exe" "-DPLUGIN_DIR=$plugin_dir" \
    "-DLICENSE_FILE=$LICENSE_PAGE" "-DNOTICES_FILE=$NOTICES_PAGE" \
    "-DFINALIZER=$SCRIPT_DIR/build-windows-installer.sh" \
    "-DOUTFILE=$out" "$NSI" > "$log" 2>&1 || rc=$?
  cat "$log"
  [ "$rc" = 0 ] || refuse "makensis failed (exit $rc) compiling $NSI"
  [ -f "$out" ] || refuse "makensis wrote no $out"
}

# The version resource holds four numbers; a release version's first three are
# its major, minor and patch numbers. A version past a tag, as a build between
# releases reports it, keeps its three; one with none records 0.0.0.0.
vi_version_of() {
  local re='^([0-9]+)\.([0-9]+)\.([0-9]+)'
  if [[ $1 =~ $re ]]; then
    printf '%s.%s.%s.0\n' "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}" "${BASH_REMATCH[3]}"
  else
    printf '0.0.0.0\n'
  fi
}

case "${1:-}" in
  -h|--help) sed -n '2,/^# when it could not, and 2 for an argument it cannot use\.$/p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
  finalize-uninstaller)
    [ "$#" = 2 ] || usage_error "finalize-uninstaller takes the uninstaller's file"
    finalize_uninstaller "$2"
    exit 0
    ;;
  parts|installer) MODE="$1"; shift ;;
  '') usage_error "name what to build: parts or installer (see --help)" ;;
  *) usage_error "unknown mode: $1 (see --help)" ;;
esac

VERSION="" AGGREGA_EXE="" OUTPUT_DIR="" PARTS="" UNSIGNED_PARTS=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --version|--aggrega|--output-dir|--parts|--unsigned-parts)
      [ -n "${2:-}" ] || usage_error "$1 needs a value"
      case "$1" in
        --version) VERSION="$2" ;;
        --aggrega) AGGREGA_EXE="$2" ;;
        --output-dir) OUTPUT_DIR="$2" ;;
        --parts) PARTS="$2" ;;
        --unsigned-parts) UNSIGNED_PARTS="$2" ;;
      esac
      shift 2
      ;;
    *) usage_error "unknown argument: $1" ;;
  esac
done
[ -n "$VERSION" ] || usage_error "--version is required"
[[ $VERSION =~ ^[A-Za-z0-9][A-Za-z0-9.+-]*$ ]] || usage_error "--version $VERSION is not a version such as 0.8.7"
[ -n "$AGGREGA_EXE" ] || usage_error "--aggrega is required"
[ -f "$AGGREGA_EXE" ] || usage_error "--aggrega names $AGGREGA_EXE, which is not a file"
[ -n "$OUTPUT_DIR" ] || usage_error "--output-dir is required"
if [ "$MODE" = installer ]; then
  [ -n "$PARTS" ] && [ -n "$UNSIGNED_PARTS" ] || usage_error "installer needs --parts and --unsigned-parts"
else
  [ -z "$PARTS$UNSIGNED_PARTS" ] || usage_error "--parts and --unsigned-parts apply to installer alone"
fi
if [ -e "$OUTPUT_DIR" ] && [ -n "$(find "$OUTPUT_DIR" -mindepth 1 -print -quit)" ]; then
  usage_error "--output-dir $OUTPUT_DIR is not empty"
fi
TEMP_ROOT="${RUNNER_TEMP:-${TMPDIR:-/tmp}}"
# The folders the container sees are handed to docker in a comma-separated
# list, and paths in them to makensis in quotes.
for path in "$SCRIPT_DIR" "$NSI_DIR" "$OUTPUT_DIR" "$TEMP_ROOT"; do
  case "$path" in
    *"'"* | *'"'* | *,*) usage_error "a path holding a quote or a comma cannot be handed to docker and makensis: $path" ;;
  esac
done

. "$SCRIPT_DIR/lib/platform-smoke-common.sh"
smoke_require_bash || exit 1
INSTALLER_NAME="$(smoke_target_installer windows/amd64)"
mapfile -t PART_NAMES < <(smoke_target_installer_parts windows/amd64)
[ "${PART_NAMES[*]}" = "uninstall.exe EnVar.dll" ] ||
  refuse "the parts this script builds are uninstall.exe and EnVar.dll, but the smoke library names ${PART_NAMES[*]}"
VI_VERSION="$(vi_version_of "$VERSION")"
AGGREGA_EXE="$(cd "$(dirname "$AGGREGA_EXE")" && pwd -P)/$(basename "$AGGREGA_EXE")"
mkdir -p "$OUTPUT_DIR"
OUTPUT_DIR="$(cd "$OUTPUT_DIR" && pwd -P)"

PULLED_IMAGE=0
WORK="$(mktemp -d "$TEMP_ROOT/aggrega-installer.XXXXXX")"
WORK="$(cd "$WORK" && pwd -P)"
trap clean_up EXIT
# The license page's text, the notices and the binary go into the temporary
# directory, which the container sees; the license and the notices are read
# first, so one refused stops the build before anything is fetched.
mkdir -p "$WORK/input"
prepare_license
prepare_notices
prepare_tools
say "version $VERSION, version resource $VI_VERSION, aggrega.exe $AGGREGA_EXE, SHA-256 $(sha256_of "$AGGREGA_EXE")"
cp -- "$AGGREGA_EXE" "$WORK/input/aggrega.exe"

if [ "$MODE" = parts ]; then
  AGGREGA_UNINSTALLER_EXPORT="$OUTPUT_DIR/uninstall.exe" compile "$WORK/input/aggrega.exe" "$WORK/plugin" "$WORK/discarded-setup.exe"
  [ -f "$OUTPUT_DIR/uninstall.exe" ] || refuse "makensis did not run the script's !uninstfinalize, so no uninstaller was kept"
  cp -- "$PLUGIN" "$OUTPUT_DIR/EnVar.dll"
  say "wrote, for signing:"
  (cd "$OUTPUT_DIR" && sha256sum -- "${PART_NAMES[@]}")
  exit 0
fi

for name in "${PART_NAMES[@]}"; do
  [ -f "$PARTS/$name" ] || usage_error "--parts $PARTS holds no $name"
  [ -f "$UNSIGNED_PARTS/$name" ] || usage_error "--unsigned-parts $UNSIGNED_PARTS holds no $name"
done
[ "$(sha256_of "$UNSIGNED_PARTS/EnVar.dll")" = "$ENVAR_DLL_SHA256" ] ||
  refuse "$UNSIGNED_PARTS/EnVar.dll is not the EnVar.dll this script fetches (SHA-256 $ENVAR_DLL_SHA256)"
PARTS="$(cd "$PARTS" && pwd -P)"
UNSIGNED_PARTS="$(cd "$UNSIGNED_PARTS" && pwd -P)"
# The plug-in the installer runs and places is the one given in --parts, from a
# folder of its own, so that makensis finds it under its own name; it and both
# uninstallers go into the temporary directory, which the container sees.
mkdir -p "$WORK/given-plugin" "$WORK/given-parts" "$WORK/unsigned-parts"
cp -- "$PARTS/EnVar.dll" "$WORK/given-plugin/EnVar.dll"
cp -- "$PARTS/uninstall.exe" "$WORK/given-parts/uninstall.exe"
cp -- "$UNSIGNED_PARTS/uninstall.exe" "$WORK/unsigned-parts/uninstall.exe"
AGGREGA_UNINSTALLER_EXPECTED="$WORK/unsigned-parts/uninstall.exe" \
  AGGREGA_UNINSTALLER_REPLACEMENT="$WORK/given-parts/uninstall.exe" \
  compile "$WORK/input/aggrega.exe" "$WORK/given-plugin" "$OUTPUT_DIR/$INSTALLER_NAME"
say "wrote $OUTPUT_DIR/$INSTALLER_NAME, SHA-256 $(sha256_of "$OUTPUT_DIR/$INSTALLER_NAME"), carrying:"
(cd "$PARTS" && sha256sum -- "${PART_NAMES[@]}")
printf '%s  aggrega.exe\n' "$(sha256_of "$AGGREGA_EXE")"
