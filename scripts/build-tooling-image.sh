#!/usr/bin/env bash
# build-tooling-image.sh — build (and optionally push) the aggrega tooling
# image to the registry at the reserved tools/ namespace.
#
# The image packages the embedded, version-stamped aggrega binary so consumer
# CI can run the CLI without a Go toolchain. It is pushed with standard OCI
# tooling (docker buildx) to <registry>/tools/aggrega:<tag> — NOT via
# `aggrega push`, which is entity-kind-dispatched and cannot carry an image.
# It is NOT pushed to GHCR.
#
# On a real --push the same build is also published as the moving tag
# <registry>/tools/aggrega:stable, pointing at the identical image (same
# digest) as the version tag. Consumer CI that tracks :stable therefore always
# resolves to the most recently published tooling image. Because :stable and
# the version tag share one digest, a signature made against the version
# reference (which resolves to that digest) already covers :stable — no
# separate signing pass is needed. --save records both references beside its
# archives, for a publish that happens later: that publish pushes one index
# under both, so they share its digest the same way.
#
# This script does NOT build the binary. The binary is produced by this
# repository's build, `scripts/build.sh`, which regenerates the embedded Wasm
# guests, stamps the git-describe version, and writes ./aggrega, the binary
# without its debug information, beside ./aggrega-debug, the same binary with
# it; `scripts/build.sh --target linux/arm64` writes ./aggrega-linux-arm64 the
# same way. Run that first; this script verifies the resulting binary is
# embedded and stamped and carries no debug information, then builds and
# (optionally) pushes the image. Keeping the build in one place means the image
# always packages the artifact the build script produces: the binary a release
# attaches as aggrega-<version>-linux-<arch> is the very file the image carries
# for that platform.
#
# ONE IMAGE PER PLATFORM, ONE TAG FOR ALL OF THEM. The image is published for
# linux/amd64 and linux/arm64 under one tag, so `docker pull` on either kind of
# machine gets the image carrying its own binary. Each platform's image is built
# from the same Dockerfile and its own copy of that platform's binary, which is
# why --platform names the binary beside the platform. Building for a platform
# other than this machine's runs the Dockerfile's RUN step as that platform's
# code, which needs emulation for it registered on this machine, so before it
# builds anything the script refuses unless `docker buildx inspect --bootstrap`
# lists every platform asked for. Several platforms are written with --save,
# one archive each, and published by scripts/publish-tooling-image.sh, which
# pushes each platform's image and then the index joining them under the tag.
#
# WHY the image carries the binary without its debug information: the hub
# registry, the default --registry, refuses a single upload request above
# 100,000,000 bytes. It runs as a Cloudflare Worker, whose request body is capped
# by the hosting account's plan, and it advertises the same figure as the largest
# upload chunk it accepts. docker buildx sends each layer in one request, and the
# binary is one file, so its layer cannot be split; with its debug information
# that layer comes close to the ceiling, and it grows with every release. So this
# script refuses a binary that carries debug information — an ELF section whose
# name begins .debug_ or .zdebug_, as `readelf` (GNU binutils, which must be on
# PATH) lists them — naming ./aggrega as the binary to give it; and it refuses a
# binary built for another processor than the platform it is given for, as the
# machine `readelf --file-header` reports. It copies each binary as it is, under
# the name `aggrega`, into a build context of its own, a temporary directory
# holding only that copy, and builds that platform's image from it; it strips
# nothing, so the bytes the image carries are the bytes the build wrote. No
# binary given to it is ever modified.
#
# After a push, sign the image by reference:
#   aggrega sign <registry>/tools/aggrega:<tag>
# (reference-based signing is kind-agnostic; the image signs like any other
# artifact once it is in the registry).
#
# Usage:
#   scripts/build-tooling-image.sh [options]
#
# Options:
#   --tag <tag>         Image tag (default: git describe --tags --always
#                       --dirty, with any leading v stripped).
#   --registry <host>   Registry host (default: hub.aggrega.ai).
#   --binary <path>     Path to the pre-built aggrega binary, without its debug
#                       information, for a --platform that names none (default:
#                       the aggrega binary beside this script's parent, i.e. the
#                       build output at <aggrega-cli>/aggrega).
#   --platform <os>/<arch>[=<binary>]
#                       A platform to build the image for, linux/amd64 or
#                       linux/arm64, and the binary that platform's image
#                       carries (default: --binary's). Repeat it to build
#                       several, each from its own binary; several combine only
#                       with --save or --dry-run. Without it the one platform is
#                       linux/amd64.
#   --push              Push the image after building (default: build only,
#                       loaded into the local docker image store). One platform
#                       only, pushed under the version tag and :stable.
#   --save <dir>        Build the images and write them, instead of pushing or
#                       loading them, into <dir> (created when missing): one
#                       archive per platform, image-<os>-<arch>.tar, which
#                       `docker load` reads, each carrying the version
#                       reference; platforms, one line per platform naming the
#                       platform and its archive, in the order given; and
#                       references, the version reference and the moving
#                       :stable one, one per line, the references the joined
#                       image is published under. The archives are published
#                       later, possibly on another machine, by
#                       scripts/publish-tooling-image.sh, which loads and pushes
#                       them and joins them under those references, so the
#                       machine that publishes need not build anything. It does
#                       not combine with --push.
#   --allow-dirty       Permit building/pushing a -dirty tag (refused by
#                       default when --push or --save is set, so a
#                       work-in-progress tree is never published).
#   --dry-run           Check the binaries as a real run does, then print the
#                       planned commands without running docker.
#   -h, --help          Show this help.
#
# Exit non-zero on the first failing step.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CLI_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

REGISTRY="hub.aggrega.ai"
IMAGE_NAME="tools/aggrega"
BINARY="$CLI_DIR/aggrega"
TAG=""
SAVE_DIR=""
PUSH=0
ALLOW_DIRTY=0
DRY_RUN=0
# The platforms asked for, in order, and the binary each names; an empty binary
# takes --binary's, read once the arguments are all in.
PLATFORMS=()
PLATFORM_BINARIES=()

die() { printf 'error: %s\n' "$*" >&2; exit 1; }

while [ "$#" -gt 0 ]; do
  case "$1" in
    --tag)        TAG="${2:-}"; shift 2 ;;
    --registry)   REGISTRY="${2:-}"; shift 2 ;;
    --binary)     BINARY="${2:-}"; shift 2 ;;
    --platform)
      [ -n "${2:-}" ] || die "--platform needs <os>/<arch>, optionally followed by =<binary>"
      case "$2" in
        *=*) PLATFORMS+=("${2%%=*}"); PLATFORM_BINARIES+=("${2#*=}") ;;
        *)   PLATFORMS+=("$2"); PLATFORM_BINARIES+=("") ;;
      esac
      shift 2 ;;
    --push)       PUSH=1; shift ;;
    --save)
      [ -n "${2:-}" ] || die "--save needs a directory"
      SAVE_DIR="$2"; shift 2 ;;
    --allow-dirty) ALLOW_DIRTY=1; shift ;;
    --dry-run)    DRY_RUN=1; shift ;;
    # Ends at the header's own closing line rather than at a line NUMBER, which
    # is how the previous form came to print `set -euo pipefail` to the reader:
    # a fixed range cannot follow a header that grows. Same sentinel, and same
    # two-stage form, as scripts/build.sh.
    -h|--help)    sed -n '2,/^# Exit non-zero on the first failing step\.$/p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *)            die "unknown argument: $1" ;;
  esac
done

if [ "$PUSH" = 1 ] && [ -n "$SAVE_DIR" ]; then
  die "--push and --save do not combine: --save writes the archives a later publish pushes"
fi
if [ -n "$SAVE_DIR" ]; then
  case "$SAVE_DIR" in /*) ;; *) SAVE_DIR="$PWD/$SAVE_DIR" ;; esac
fi

if [ "${#PLATFORMS[@]}" -eq 0 ]; then
  PLATFORMS=(linux/amd64)
  PLATFORM_BINARIES=("")
fi
for i in "${!PLATFORMS[@]}"; do
  [ -n "${PLATFORM_BINARIES[$i]}" ] || PLATFORM_BINARIES[i]="$BINARY"
done

# The processor `readelf --file-header` names for each platform's binary. A
# platform with no line here has no image: the Dockerfile's base and the
# release's binaries exist for these two.
elf_machine_of() {
  case "$1" in
    linux/amd64) printf 'Advanced Micro Devices X86-64\n' ;;
    linux/arm64) printf 'AArch64\n' ;;
    *) return 1 ;;
  esac
}

for i in "${!PLATFORMS[@]}"; do
  platform="${PLATFORMS[$i]}"
  elf_machine_of "$platform" >/dev/null || die "no tooling image is built for $platform: the platforms are linux/amd64 and linux/arm64"
  for ((j = 0; j < i; j++)); do
    [ "${PLATFORMS[$j]}" != "$platform" ] || die "--platform $platform is given twice"
  done
done
if [ "${#PLATFORMS[@]}" -gt 1 ] && [ -z "$SAVE_DIR" ] && [ "$DRY_RUN" != 1 ]; then
  if [ "$PUSH" = 1 ]; then
    die "--push builds one platform; for several, write them with --save and publish them with scripts/publish-tooling-image.sh, which joins them under one tag"
  fi
  die "the local image store holds one platform's image under a reference; build several with --save, or one at a time"
fi

# Default tag from git describe (v-less), matching the binary's stamped form.
if [ -z "$TAG" ]; then
  TAG="$(git -C "$CLI_DIR" describe --tags --always --dirty 2>/dev/null || echo dev)"
  TAG="${TAG#v}"
fi

# Each binary's checks, the ones reading its file before the ones running it:
# a binary for another processor runs on this machine only where emulation for
# that processor is registered, so a binary refused on its file is refused
# without needing it.
command -v readelf >/dev/null 2>&1 || die "readelf not found on PATH.
the tooling image refuses a binary that carries debug information, or one built
for another processor than its platform, both of which readelf reads (see
--help for why); install GNU binutils, which provides readelf, and re-run."

# Read the version and ABI version straight from the binary so the image labels
# and the binary's own `aggrega version -o json` output cannot diverge.
# Whitespace is stripped before the match so the extraction is insensitive to
# the pretty-printer, and it needs no jq. Stripping the whole payload is safe
# only where the extracted value contains no whitespace, which holds for the
# version and ABI version strings read here. An absent field yields an empty
# value rather than a failed pipeline, so the refusals below name what is missing
# instead of the script exiting silently.
json_field() { printf '%s' "$1" | tr -d '[:space:]' | grep -o "\"$2\":\"[^\"]*\"" | head -1 | cut -d'"' -f4 || true; }

# check_binary <platform> <binary>: refuses a binary that is not the embedded,
# stamped build without debug information for <platform>, and sets BIN_VERSION
# and BIN_ABI from it.
check_binary() {
  local platform="$1" binary="$2" header want machine sections version_json
  [ -x "$binary" ] || die "aggrega binary for $platform not found or not executable at: $binary
build it first with the repository build (scripts/build.sh, with --target $platform
for a platform other than this machine's), then re-run."

  header="$(readelf --file-header --wide "$binary" 2>&1)" || die "readelf could not read $binary as an ELF executable:
$header"
  want="$(elf_machine_of "$platform")"
  machine="$(printf '%s\n' "$header" | sed -n 's/^[[:space:]]*Machine:[[:space:]]*//p')"
  [ "$machine" = "$want" ] || die "binary at $binary is built for ${machine:-an unnamed machine}, not for $platform ($want).
give each --platform the binary built for it: ./aggrega for this machine's own
platform, ./aggrega-<os>-<arch> for one built with --target."

  # Refuse a binary that carries debug information (see the header for why), on
  # every run, a dry run included: the check is local, and a dry run exists to
  # show that the real run would work. readelf lists the binary's sections; a
  # section whose name begins .debug_ or .zdebug_ holds debug information,
  # written compressed or not.
  sections="$(readelf --section-headers --wide "$binary" 2>&1)" || die "readelf could not read the sections of $binary:
$sections"
  case "$sections" in
    *" .debug_"* | *" .zdebug_"*)
      die "binary at $binary carries debug information.
the tooling image carries the binary without it, because the registry refuses a
single upload above 100,000,000 bytes and the binary's layer is one upload (see
--help); give it the ./aggrega that scripts/build.sh writes, not ./aggrega-debug."
      ;;
  esac

  # The first run of the binary reads its version, so a binary this machine
  # cannot run is refused here, saying why.
  version_json="$("$binary" version -o json 2>/dev/null)" || die "could not run $binary, built for $platform, to read its version.
a binary for another processor than this machine's runs here only where
emulation for that processor is registered."

  # Verify the binary is the embedded build: `plugin list -o json` reports the
  # guests compiled into the binary as a machine-readable array. A --light
  # binary reports an empty array and would package a non-functional tooling
  # image. The check reads the JSON rather than the human table, so it does not
  # depend on the table's display format (which lists bare `<socket>/<name>`
  # identities, not URIs). Whitespace is stripped so the non-empty test
  # (`"embedded":[{`) is insensitive to the pretty-printer, and it needs no jq.
  if ! "$binary" plugin list -o json 2>/dev/null | tr -d '[:space:]' | grep -q '"embedded":\[{'; then
    die "binary at $binary is not the embedded build (no embedded guests).
the tooling image needs the embedded binary; rebuild without --light."
  fi
  BIN_VERSION="$(json_field "$version_json" version)"
  BIN_ABI="$(json_field "$version_json" abiVersion)"
  [ -n "$BIN_VERSION" ] || die "binary at $binary reported no version — is it stamped? rebuild via scripts/build.sh"
  [ -n "$BIN_ABI" ] || die "binary at $binary reported no abiVersion — is it up to date? rebuild via scripts/build.sh"
}

# Every platform's image carries the same version and ABI labels under one tag,
# so every binary must report the same two.
VERSION_ALL=""
ABI_ALL=""
for i in "${!PLATFORMS[@]}"; do
  check_binary "${PLATFORMS[$i]}" "${PLATFORM_BINARIES[$i]}"
  if [ -z "$VERSION_ALL" ]; then
    VERSION_ALL="$BIN_VERSION"
    ABI_ALL="$BIN_ABI"
  elif [ "$BIN_VERSION" != "$VERSION_ALL" ] || [ "$BIN_ABI" != "$ABI_ALL" ]; then
    die "binary at ${PLATFORM_BINARIES[$i]} reports version $BIN_VERSION, ABI $BIN_ABI, but ${PLATFORM_BINARIES[0]} reports version $VERSION_ALL, ABI $ABI_ALL; the platforms of one tag carry one build"
  fi
done

case "$TAG" in
  *dirty*)
    if [ "$PUSH" = 1 ] && [ "$ALLOW_DIRTY" != 1 ]; then
      die "refusing to push a -dirty tag ($TAG); commit the tree or pass --allow-dirty"
    fi
    if [ -n "$SAVE_DIR" ] && [ "$ALLOW_DIRTY" != 1 ]; then
      die "refusing to save a -dirty tag ($TAG) for publishing; commit the tree or pass --allow-dirty"
    fi
    printf 'warning: building a -dirty tag (%s) from an uncommitted tree\n' "$TAG" >&2
    ;;
esac

IMAGE_REF="$REGISTRY/$IMAGE_NAME:$TAG"
STABLE_REF="$REGISTRY/$IMAGE_NAME:stable"

# archive_of <platform>: the file name of that platform's archive in --save's
# directory. The platforms list written beside the archives names each, so the
# publish reads the names rather than composing them a second time.
archive_of() { printf 'image-%s.tar\n' "${1//\//-}"; }

# Copy each binary, as it is, into a build context of its own: the Dockerfile
# copies the file named aggrega from it, and nothing else is in it.
CONTEXT_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/aggrega-tooling-image.XXXXXX")" || die "could not create a temporary build context"
trap 'rm -rf "$CONTEXT_ROOT"' EXIT
CONTEXTS=()
for i in "${!PLATFORMS[@]}"; do
  context="$CONTEXT_ROOT/${PLATFORMS[$i]//\//-}"
  mkdir "$context" || die "could not create a build context in $CONTEXT_ROOT"
  cp "${PLATFORM_BINARIES[$i]}" "$context/aggrega" || die "could not copy ${PLATFORM_BINARIES[$i]} into the build context"
  chmod 0755 "$context/aggrega"
  CONTEXTS+=("$context")
done

# compose_build <index>: sets BUILD_CMD to that platform's docker buildx build,
# with the binaries' version + ABI stamped as labels via build args, so the dry
# run prints exactly what a real run runs. --push publishes, carrying the moving
# :stable tag as a second --tag so both references come from one build and
# resolve to the identical digest; --save writes one docker archive per
# platform carrying the version reference, which the publish pushes in turn
# before the index joining them goes up under the version reference and
# :stable; otherwise --load leaves the image in the local store for inspection
# or a separate push. :stable rides on a push or a publish only — a local --load
# never fabricates a moving tag.
compose_build() {
  local i="$1"
  BUILD_CMD=(
    docker buildx build
    --platform "${PLATFORMS[$i]}"
    --build-arg "VERSION=$VERSION_ALL"
    --build-arg "ABI_VERSION=$ABI_ALL"
    --tag "$IMAGE_REF"
    --file "$CLI_DIR/Dockerfile"
  )
  if [ "$PUSH" = 1 ]; then
    BUILD_CMD+=(--tag "$STABLE_REF" --push)
  elif [ -n "$SAVE_DIR" ]; then
    BUILD_CMD+=(--output "type=docker,dest=$SAVE_DIR/$(archive_of "${PLATFORMS[$i]}")")
  else
    BUILD_CMD+=(--load)
  fi
  BUILD_CMD+=("${CONTEXTS[$i]}")
}

printf 'image:     %s\n' "$IMAGE_REF"
if [ "$PUSH" = 1 ] || [ -n "$SAVE_DIR" ]; then
  printf 'stable:    %s\n' "$STABLE_REF"
fi
printf 'version:   %s\n' "$VERSION_ALL"
printf 'abi:       %s\n' "$ABI_ALL"
for i in "${!PLATFORMS[@]}"; do
  printf 'platform:  %s, binary %s (%s bytes, no debug information; copied as it is)\n' \
    "${PLATFORMS[$i]}" "${PLATFORM_BINARIES[$i]}" "$(wc -c < "${PLATFORM_BINARIES[$i]}" | tr -d '[:space:]')"
done
if [ "$PUSH" = 1 ]; then
  printf 'push:      yes\n'
elif [ -n "$SAVE_DIR" ]; then
  printf 'push:      no (archives written to %s)\n' "$SAVE_DIR"
else
  printf 'push:      no (local load)\n'
fi
printf 'context:   %s (the copies only; removed on exit)\n' "$CONTEXT_ROOT"

if [ "$DRY_RUN" = 1 ]; then
  printf 'dry-run — would run:\n  docker buildx inspect --bootstrap (refusing unless it lists %s)\n' "${PLATFORMS[*]}"
  for i in "${!PLATFORMS[@]}"; do
    compose_build "$i"
    printf '  %s\n' "${BUILD_CMD[*]}"
  done
  if [ "$PUSH" = 1 ]; then
    printf 'then sign by reference:\n  aggrega sign %s\n' "$IMAGE_REF"
  fi
  exit 0
fi

command -v docker >/dev/null 2>&1 || die "docker not found on PATH"

# The builder must be able to build every platform asked for before anything is
# built. `docker buildx inspect --bootstrap` starts the current builder and
# lists, on its Platforms: line, what it can build; a platform other than this
# machine's appears there only when emulation for it is registered. A platform
# is listed bare or with a variant (linux/arm64/v8), and one the builder was
# configured with carries a trailing *.
INSPECT="$(docker buildx inspect --bootstrap 2>&1)" || die "docker buildx inspect --bootstrap failed:
$INSPECT"
LISTED="$(printf '%s\n' "$INSPECT" | sed -n 's/^[[:space:]]*Platforms:[[:space:]]*//p' | tr ',' '\n' | tr -d ' *')"
for platform in "${PLATFORMS[@]}"; do
  found=0
  while IFS= read -r listed; do
    case "$listed" in "$platform" | "$platform"/*) found=1 ;; esac
  done <<< "$LISTED"
  [ "$found" = 1 ] || die "the image builder cannot build for $platform: docker buildx inspect --bootstrap lists $(printf '%s' "$LISTED" | tr '\n' ' ').
building for a platform other than this machine's runs the image's RUN step as
that platform's code, which needs emulation for it registered on this machine
before the builder starts."
done

if [ -n "$SAVE_DIR" ]; then
  # Archives and lists left from an earlier run must not pass for this build's
  # when this build fails.
  mkdir -p "$SAVE_DIR" || die "could not create $SAVE_DIR"
  rm -f "$SAVE_DIR/image.tar" "$SAVE_DIR"/image-*.tar "$SAVE_DIR/references" "$SAVE_DIR/platforms"
fi

for i in "${!PLATFORMS[@]}"; do
  compose_build "$i"
  "${BUILD_CMD[@]}"
done

if [ -n "$SAVE_DIR" ]; then
  for platform in "${PLATFORMS[@]}"; do
    archive="$SAVE_DIR/$(archive_of "$platform")"
    [ -s "$archive" ] || die "the build for $platform wrote no archive at $archive"
  done
  for platform in "${PLATFORMS[@]}"; do
    printf '%s %s\n' "$platform" "$(archive_of "$platform")"
  done > "$SAVE_DIR/platforms"
  printf '%s\n%s\n' "$IMAGE_REF" "$STABLE_REF" > "$SAVE_DIR/references"
  printf '\nsaved in %s, under %s and then %s:\n' "$SAVE_DIR" "$IMAGE_REF" "$STABLE_REF"
  for platform in "${PLATFORMS[@]}"; do
    archive="$SAVE_DIR/$(archive_of "$platform")"
    printf '  %s  %s (%s bytes)\n' "$platform" "$archive" "$(wc -c < "$archive" | tr -d '[:space:]')"
  done
  printf 'publish them with scripts/publish-tooling-image.sh --archive-dir %s, then sign by reference:\n  aggrega sign %s\n' \
    "$SAVE_DIR" "$IMAGE_REF"
fi

if [ "$PUSH" = 1 ]; then
  printf '\npushed %s\n' "$IMAGE_REF"
  printf 'pushed %s (moving tag, same digest)\n' "$STABLE_REF"
  printf 'sign it by reference:\n  aggrega sign %s\n' "$IMAGE_REF"
  printf 'the signature resolves to the shared digest, so it also covers %s\n' "$STABLE_REF"
fi
