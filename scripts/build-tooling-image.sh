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
# separate signing pass is needed. A --save archive carries both references the
# same way, for a publish that happens later.
#
# This script does NOT build the binary. The binary is produced by this
# repository's build, `scripts/build.sh`, which regenerates the embedded Wasm
# guests, stamps the git-describe version, and writes ./aggrega, the binary
# without its debug information, beside ./aggrega-debug, the same binary with
# it. Run that first; this script verifies the resulting binary is embedded and
# stamped and carries no debug information, then builds and (optionally) pushes
# the image. Keeping the build in one place means the image always packages the
# artifact the build script produces: the binary a release attaches as
# aggrega-<version>-linux-amd64 is the very file the image carries.
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
# PATH) lists them — naming ./aggrega as the binary to give it. It copies the
# binary as it is, under the name `aggrega`, into a build context of its own, a
# temporary directory holding only that copy, and builds the image from it; it
# strips nothing, so the bytes the image carries are the bytes the build wrote.
# The binary given by --binary is never modified.
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
#                       information (default: the aggrega binary beside this
#                       script's parent, i.e. the build output at
#                       <aggrega-cli>/aggrega).
#   --platform <p>      Target platform (default: linux/amd64).
#   --push              Push the image after building (default: build only,
#                       loaded into the local docker image store).
#   --save <dir>        Build the image and write it, instead of pushing or
#                       loading it, as an archive in <dir> (created when
#                       missing): image.tar, which `docker load` reads, carrying
#                       both the version reference and the moving :stable one,
#                       and references, those two references one per line. The
#                       archive is published later, possibly on another
#                       machine, by scripts/publish-tooling-image.sh, which only
#                       loads and pushes it, so the machine that publishes need
#                       not build anything. It does not combine with --push.
#   --allow-dirty       Permit building/pushing a -dirty tag (refused by
#                       default when --push or --save is set, so a
#                       work-in-progress tree is never published).
#   --dry-run           Check the binary as a real run does, then print the
#                       planned commands without running docker.
#   -h, --help          Show this help.
#
# Exit non-zero on the first failing step.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CLI_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

REGISTRY="hub.aggrega.ai"
IMAGE_NAME="tools/aggrega"
PLATFORM="linux/amd64"
BINARY="$CLI_DIR/aggrega"
TAG=""
SAVE_DIR=""
PUSH=0
ALLOW_DIRTY=0
DRY_RUN=0

die() { printf 'error: %s\n' "$*" >&2; exit 1; }

while [ "$#" -gt 0 ]; do
  case "$1" in
    --tag)        TAG="${2:-}"; shift 2 ;;
    --registry)   REGISTRY="${2:-}"; shift 2 ;;
    --binary)     BINARY="${2:-}"; shift 2 ;;
    --platform)   PLATFORM="${2:-}"; shift 2 ;;
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
  die "--push and --save do not combine: --save writes the archive a later publish pushes"
fi
if [ -n "$SAVE_DIR" ]; then
  case "$SAVE_DIR" in /*) ;; *) SAVE_DIR="$PWD/$SAVE_DIR" ;; esac
fi

# Default tag from git describe (v-less), matching the binary's stamped form.
if [ -z "$TAG" ]; then
  TAG="$(git -C "$CLI_DIR" describe --tags --always --dirty 2>/dev/null || echo dev)"
  TAG="${TAG#v}"
fi

[ -x "$BINARY" ] || die "aggrega binary not found or not executable at: $BINARY
build it first with the repository build (scripts/build.sh), then re-run."

# Verify the binary is the embedded build: `plugin list -o json` reports the
# guests compiled into the binary as a machine-readable array. A --light binary
# reports an empty array and would package a non-functional tooling image. The
# check reads the JSON rather than the human table, so it does not depend on the
# table's display format (which lists bare `<socket>/<name>` identities, not
# URIs). Whitespace is stripped so the non-empty test (`"embedded":[{`) is
# insensitive to the pretty-printer, and it needs no jq.
if ! "$BINARY" plugin list -o json 2>/dev/null | tr -d '[:space:]' | grep -q '"embedded":\[{'; then
  die "binary at $BINARY is not the embedded build (no embedded guests).
the tooling image needs the embedded binary; rebuild without --light."
fi

# Read the version and ABI version straight from the binary so the image labels
# and the binary's own `aggrega version -o json` output cannot diverge.
# Whitespace is stripped before the match so the extraction is insensitive to
# the pretty-printer, and it needs no jq. Stripping the whole payload is safe
# only where the extracted value contains no whitespace, which holds for the
# version and ABI version strings read here. An absent field yields an empty
# value rather than a failed pipeline, so the refusals below name what is missing
# instead of the script exiting silently.
json_field() { printf '%s' "$1" | tr -d '[:space:]' | grep -o "\"$2\":\"[^\"]*\"" | head -1 | cut -d'"' -f4 || true; }

VERSION_JSON="$("$BINARY" version -o json 2>/dev/null)" || die "could not read version from $BINARY"
BIN_VERSION="$(json_field "$VERSION_JSON" version)"
BIN_ABI="$(json_field "$VERSION_JSON" abiVersion)"

[ -n "$BIN_VERSION" ] || die "binary reported no version — is it stamped? rebuild via scripts/build.sh"
[ -n "$BIN_ABI" ] || die "binary reported no abiVersion — is it up to date? rebuild via scripts/build.sh"

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

# Refuse a binary that carries debug information (see the header for why), on
# every run, a dry run included: the check is local, and a dry run exists to
# show that the real run would work. readelf lists the binary's sections; a
# section whose name begins .debug_ or .zdebug_ holds debug information, written
# compressed or not.
command -v readelf >/dev/null 2>&1 || die "readelf not found on PATH.
the tooling image refuses a binary that carries debug information, which readelf
reads (see --help for why); install GNU binutils, which provides readelf, and re-run."
SECTIONS="$(readelf --section-headers --wide "$BINARY" 2>&1)" || die "readelf could not read the sections of $BINARY:
$SECTIONS"
case "$SECTIONS" in
  *" .debug_"* | *" .zdebug_"*)
    die "binary at $BINARY carries debug information.
the tooling image carries the binary without it, because the registry refuses a
single upload above 100,000,000 bytes and the binary's layer is one upload (see
--help); give it the ./aggrega that scripts/build.sh writes, not ./aggrega-debug."
    ;;
esac

# Copy the binary, as it is, into a build context of its own: the Dockerfile
# copies the file named aggrega from it, and nothing else is in it.
CONTEXT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/aggrega-tooling-image.XXXXXX")" || die "could not create a temporary build context"
trap 'rm -rf "$CONTEXT_DIR"' EXIT
cp "$BINARY" "$CONTEXT_DIR/aggrega" || die "could not copy $BINARY into the build context"
chmod 0755 "$CONTEXT_DIR/aggrega"

IMAGE_REF="$REGISTRY/$IMAGE_NAME:$TAG"
STABLE_REF="$REGISTRY/$IMAGE_NAME:stable"

# docker buildx build with the binary's version + ABI stamped as labels via
# build args. --push publishes; --save writes a docker archive for a later
# publish; otherwise --load leaves the image in the local store for inspection
# or a separate push. On a push or a save the same build carries the moving
# :stable tag as a second --tag, so both references come from one build and
# resolve to the identical digest. :stable rides on those two only — a local
# --load never fabricates a moving tag.
declare -a BUILD_CMD=(
  docker buildx build
  --platform "$PLATFORM"
  --build-arg "VERSION=$BIN_VERSION"
  --build-arg "ABI_VERSION=$BIN_ABI"
  --tag "$IMAGE_REF"
  --file "$CLI_DIR/Dockerfile"
)
if [ "$PUSH" = 1 ]; then
  BUILD_CMD+=(--tag "$STABLE_REF" --push)
elif [ -n "$SAVE_DIR" ]; then
  BUILD_CMD+=(--tag "$STABLE_REF" --output "type=docker,dest=$SAVE_DIR/image.tar")
else
  BUILD_CMD+=(--load)
fi
BUILD_CMD+=("$CONTEXT_DIR")

printf 'image:     %s\n' "$IMAGE_REF"
if [ "$PUSH" = 1 ] || [ -n "$SAVE_DIR" ]; then
  printf 'stable:    %s\n' "$STABLE_REF"
fi
printf 'version:   %s\n' "$BIN_VERSION"
printf 'abi:       %s\n' "$BIN_ABI"
printf 'platform:  %s\n' "$PLATFORM"
if [ "$PUSH" = 1 ]; then
  printf 'push:      yes\n'
elif [ -n "$SAVE_DIR" ]; then
  printf 'push:      no (archive written to %s)\n' "$SAVE_DIR"
else
  printf 'push:      no (local load)\n'
fi
printf 'binary:    %s (%s bytes, no debug information; copied as it is)\n' "$BINARY" "$(wc -c < "$BINARY" | tr -d '[:space:]')"
printf 'context:   %s (the copy only; removed on exit)\n' "$CONTEXT_DIR"

if [ "$DRY_RUN" = 1 ]; then
  printf 'dry-run — would run:\n  %s\n' "${BUILD_CMD[*]}"
  if [ "$PUSH" = 1 ]; then
    printf 'then sign by reference:\n  aggrega sign %s\n' "$IMAGE_REF"
  fi
  exit 0
fi

command -v docker >/dev/null 2>&1 || die "docker not found on PATH"

if [ -n "$SAVE_DIR" ]; then
  # An archive or a reference list left from an earlier run must not pass for
  # this build's when this build fails.
  mkdir -p "$SAVE_DIR" || die "could not create $SAVE_DIR"
  rm -f "$SAVE_DIR/image.tar" "$SAVE_DIR/references"
fi

"${BUILD_CMD[@]}"

if [ -n "$SAVE_DIR" ]; then
  [ -s "$SAVE_DIR/image.tar" ] || die "the build wrote no archive at $SAVE_DIR/image.tar"
  printf '%s\n%s\n' "$IMAGE_REF" "$STABLE_REF" > "$SAVE_DIR/references"
  printf '\nsaved %s (%s bytes), carrying:\n  %s\n  %s\n' "$SAVE_DIR/image.tar" \
    "$(wc -c < "$SAVE_DIR/image.tar" | tr -d '[:space:]')" "$IMAGE_REF" "$STABLE_REF"
  printf 'publish it with scripts/publish-tooling-image.sh --archive-dir %s, then sign by reference:\n  aggrega sign %s\n' \
    "$SAVE_DIR" "$IMAGE_REF"
fi

if [ "$PUSH" = 1 ]; then
  printf '\npushed %s\n' "$IMAGE_REF"
  printf 'pushed %s (moving tag, same digest)\n' "$STABLE_REF"
  printf 'sign it by reference:\n  aggrega sign %s\n' "$IMAGE_REF"
  printf 'the signature resolves to the shared digest, so it also covers %s\n' "$STABLE_REF"
fi
