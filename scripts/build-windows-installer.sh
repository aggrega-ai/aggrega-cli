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
# NSIS is Debian's 3.08-3+deb12u1 build, the nsis package (makensis) and the
# nsis-common package (the installer stubs and the plug-ins), fetched from
# snapshot.debian.org, which keeps every package it has archived at a fixed
# address; the plug-in is EnVar, fetched from the NSIS wiki. Each is fetched at
# one version and refused, before anything in it is unpacked or run, unless its
# SHA-256 is the one written below; moving to another version changes both
# lines. This makensis needs the GNU C library 2.34 or later, the GNU C++
# library from GCC 11 or later (GLIBCXX_3.4.29) and zlib on the machine; ar and
# xz unpack the packages and unzip the plug-in's archive. A machine missing one
# is refused, naming it.
#
# The installer is the same bytes whenever it is built from the same inputs:
# the script has makensis record no file times, and makensis records no time of
# its own.
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
# removes; writes nothing else. Exits 0 when it wrote what it was asked to, 1
# when it could not, and 2 for an argument it cannot use.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd -P)"
CLI_ROOT="$(cd "$SCRIPT_DIR/.." && pwd -P)"
NSI="$CLI_ROOT/packaging/windows/aggrega.nsi"

NSIS_DEB_URL=https://snapshot.debian.org/archive/debian/20240617T204135Z/pool/main/n/nsis/nsis_3.08-3%2Bdeb12u1_amd64.deb
NSIS_DEB_SHA256=b9ca8de84341753dd1c071a3f65453dc06bd7b6f2230140a36755d7e402827c2
NSIS_COMMON_DEB_URL=https://snapshot.debian.org/archive/debian/20240617T204135Z/pool/main/n/nsis/nsis-common_3.08-3%2Bdeb12u1_all.deb
NSIS_COMMON_DEB_SHA256=f1c9e63389c947442fddd5ca446e22966e8f03fc10663f998cf7df58642f9b52
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

# unpack_deb <deb> <root>: unpack the files a Debian package installs under
# <root>, with ar and tar.
unpack_deb() {
  local deb="$1" root="$2" members member data=""
  local dir
  dir="$(mktemp -d "$WORK/deb.XXXXXX")"
  (cd "$dir" && ar x "$deb") || refuse "ar could not read $deb"
  for member in "$dir"/data.tar.*; do
    [ -f "$member" ] && data="$member"
  done
  [ -n "$data" ] || refuse "$deb holds no data archive"
  mkdir -p "$root"
  tar -xf "$data" -C "$root" || refuse "tar could not unpack $data from $deb"
  members="$(cd "$dir" && printf '%s ' *)"
  rm -rf -- "$dir"
  say "unpacked ${deb##*/} (${members% })"
}

# prepare_tools: fetch and unpack NSIS and the plug-in into WORK, setting
# MAKENSIS, NSIS_DATA and PLUGIN (the plug-in DLL).
prepare_tools() {
  local tool machine
  machine="$(uname -s)/$(uname -m)"
  [ "$machine" = Linux/x86_64 ] || refuse "the makensis this script pins is built for x64 Linux, and this machine is $machine"
  for tool in curl sha256sum ar tar xz unzip cmp; do
    command -v "$tool" >/dev/null 2>&1 || refuse "$tool is not on PATH, and building the installer needs it"
  done
  fetch "$NSIS_DEB_URL" "$NSIS_DEB_SHA256" "$WORK/nsis.deb"
  fetch "$NSIS_COMMON_DEB_URL" "$NSIS_COMMON_DEB_SHA256" "$WORK/nsis-common.deb"
  fetch "$ENVAR_ZIP_URL" "$ENVAR_ZIP_SHA256" "$WORK/envar.zip"
  unpack_deb "$WORK/nsis.deb" "$WORK/nsis"
  unpack_deb "$WORK/nsis-common.deb" "$WORK/nsis"
  MAKENSIS="$WORK/nsis/usr/bin/makensis"
  NSIS_DATA="$WORK/nsis/usr/share/nsis"
  [ -x "$MAKENSIS" ] || refuse "the nsis package holds no usr/bin/makensis"
  [ -d "$NSIS_DATA/Stubs" ] || refuse "the nsis-common package holds no usr/share/nsis/Stubs"
  mkdir -p "$WORK/plugin"
  unzip -p "$WORK/envar.zip" "$ENVAR_DLL_MEMBER" > "$WORK/plugin/EnVar.dll" ||
    refuse "$ENVAR_ZIP_URL holds no $ENVAR_DLL_MEMBER"
  [ "$(sha256_of "$WORK/plugin/EnVar.dll")" = "$ENVAR_DLL_SHA256" ] ||
    refuse "$ENVAR_DLL_MEMBER in $ENVAR_ZIP_URL has SHA-256 $(sha256_of "$WORK/plugin/EnVar.dll"), not $ENVAR_DLL_SHA256"
  PLUGIN="$WORK/plugin/EnVar.dll"
  local version
  if ! version="$(NSISDIR="$NSIS_DATA" "$MAKENSIS" -VERSION 2>&1)"; then
    printf '%s\n' "$version" >&2
    refuse "makensis does not run on this machine; it needs the GNU C library 2.34 or later, libstdc++ with GLIBCXX_3.4.29 and zlib (libz.so.1)"
  fi
  say "makensis $version"
}

# compile <aggrega.exe> <plugin dir> <installer>: compile the script.
compile() {
  local exe="$1" plugin_dir="$2" out="$3" log="$WORK/makensis.log" rc=0
  NSISDIR="$NSIS_DATA" "$MAKENSIS" -V2 -WX -NOCONFIG \
    "-DVERSION=$VERSION" "-DVI_VERSION=$VI_VERSION" \
    "-DAGGREGA_EXE=$exe" "-DPLUGIN_DIR=$plugin_dir" \
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
for path in "$SCRIPT_DIR" "$OUTPUT_DIR" "$AGGREGA_EXE" "$PARTS" "$UNSIGNED_PARTS"; do
  case "$path" in
    *"'"* | *'"'*) usage_error "a path holding a quote cannot be handed to makensis: $path" ;;
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

WORK="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/aggrega-installer.XXXXXX")"
trap 'rm -rf -- "$WORK"' EXIT
prepare_tools
say "version $VERSION, version resource $VI_VERSION, aggrega.exe $AGGREGA_EXE, SHA-256 $(sha256_of "$AGGREGA_EXE")"

if [ "$MODE" = parts ]; then
  AGGREGA_UNINSTALLER_EXPORT="$OUTPUT_DIR/uninstall.exe" compile "$AGGREGA_EXE" "$WORK/plugin" "$WORK/discarded-setup.exe"
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
# folder of its own, so that makensis finds it under its own name.
mkdir -p "$WORK/given-plugin"
cp -- "$PARTS/EnVar.dll" "$WORK/given-plugin/EnVar.dll"
AGGREGA_UNINSTALLER_EXPECTED="$UNSIGNED_PARTS/uninstall.exe" \
  AGGREGA_UNINSTALLER_REPLACEMENT="$PARTS/uninstall.exe" \
  compile "$AGGREGA_EXE" "$WORK/given-plugin" "$OUTPUT_DIR/$INSTALLER_NAME"
say "wrote $OUTPUT_DIR/$INSTALLER_NAME, SHA-256 $(sha256_of "$OUTPUT_DIR/$INSTALLER_NAME"), carrying:"
(cd "$PARTS" && sha256sum -- "${PART_NAMES[@]}")
printf '%s  aggrega.exe\n' "$(sha256_of "$AGGREGA_EXE")"
