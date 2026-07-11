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
# This script does NOT build the binary. The binary is produced by the
# repository build (`scripts/build.sh` at the workspace root), which regenerates
# the embedded Wasm guests and stamps the git-describe version. Run that first;
# this script verifies the resulting binary is embedded and stamped, then builds
# and (optionally) pushes the image. Keeping the build in one place means the
# image always packages the exact artifact the build scripts produce.
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
#   --binary <path>     Path to the pre-built aggrega binary (default: the
#                       aggrega binary beside this script's parent, i.e. the
#                       build output at <aggrega-cli>/aggrega).
#   --platform <p>      Target platform (default: linux/amd64).
#   --push              Push the image after building (default: build only,
#                       loaded into the local docker image store).
#   --allow-dirty       Permit building/pushing a -dirty tag (refused by
#                       default when --push is set, so a work-in-progress tree
#                       is never published).
#   --dry-run           Print the planned commands without running docker.
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
    --allow-dirty) ALLOW_DIRTY=1; shift ;;
    --dry-run)    DRY_RUN=1; shift ;;
    -h|--help)    sed -n '2,52p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *)            die "unknown argument: $1" ;;
  esac
done

# Default tag from git describe (v-less), matching the binary's stamped form.
if [ -z "$TAG" ]; then
  TAG="$(git -C "$CLI_DIR" describe --tags --always --dirty 2>/dev/null || echo dev)"
  TAG="${TAG#v}"
fi

[ -x "$BINARY" ] || die "aggrega binary not found or not executable at: $BINARY
build it first with the repository build (scripts/build.sh), then re-run."

# Verify the binary is the embedded build: `plugin list` lists guest URIs under
# the embedded built-ins group only when guests are compiled in. A --light
# binary prints the group header with "(none)" and would package a
# non-functional tooling image, so check for an actual guest URI in that
# section rather than just the header.
EMBEDDED_SECTION="$("$BINARY" plugin list 2>/dev/null | awk '/Embedded built-ins:/{f=1;next} /Configured plugins:/{f=0} f')"
if ! printf '%s' "$EMBEDDED_SECTION" | grep -q "oci://"; then
  die "binary at $BINARY is not the embedded build (no embedded guests).
the tooling image needs the embedded binary; rebuild without --light."
fi

# Read the version and ABI version straight from the binary so the image labels
# and the binary's own `aggrega version --json` output cannot diverge.
VERSION_JSON="$("$BINARY" version --json 2>/dev/null)" || die "could not read version from $BINARY"
BIN_VERSION="$(printf '%s' "$VERSION_JSON" | grep -o '"version":"[^"]*"' | head -1 | cut -d'"' -f4)"
BIN_ABI="$(printf '%s' "$VERSION_JSON" | grep -o '"abiVersion":"[^"]*"' | head -1 | cut -d'"' -f4)"

[ -n "$BIN_VERSION" ] || die "binary reported no version — is it stamped? rebuild via scripts/build.sh"
[ -n "$BIN_ABI" ] || die "binary reported no abiVersion — is it up to date? rebuild via scripts/build.sh"

case "$TAG" in
  *dirty*|*-dirty)
    if [ "$PUSH" = 1 ] && [ "$ALLOW_DIRTY" != 1 ]; then
      die "refusing to push a -dirty tag ($TAG); commit the tree or pass --allow-dirty"
    fi
    printf 'warning: building a -dirty tag (%s) from an uncommitted tree\n' "$TAG" >&2
    ;;
esac

IMAGE_REF="$REGISTRY/$IMAGE_NAME:$TAG"

# docker buildx build with the binary's version + ABI stamped as labels via
# build args. --push publishes; otherwise --load leaves the image in the local
# store for inspection or a separate push.
declare -a BUILD_CMD=(
  docker buildx build
  --platform "$PLATFORM"
  --build-arg "VERSION=$BIN_VERSION"
  --build-arg "ABI_VERSION=$BIN_ABI"
  --tag "$IMAGE_REF"
  --file "$CLI_DIR/Dockerfile"
)
if [ "$PUSH" = 1 ]; then
  BUILD_CMD+=(--push)
else
  BUILD_CMD+=(--load)
fi
BUILD_CMD+=("$CLI_DIR")

printf 'image:     %s\n' "$IMAGE_REF"
printf 'version:   %s\n' "$BIN_VERSION"
printf 'abi:       %s\n' "$BIN_ABI"
printf 'platform:  %s\n' "$PLATFORM"
printf 'push:      %s\n' "$([ "$PUSH" = 1 ] && echo yes || echo 'no (local load)')"

if [ "$DRY_RUN" = 1 ]; then
  printf 'dry-run — would run:\n  %s\n' "${BUILD_CMD[*]}"
  if [ "$PUSH" = 1 ]; then
    printf 'then sign by reference:\n  aggrega sign %s\n' "$IMAGE_REF"
  fi
  exit 0
fi

command -v docker >/dev/null 2>&1 || die "docker not found on PATH"

"${BUILD_CMD[@]}"

if [ "$PUSH" = 1 ]; then
  printf '\npushed %s\n' "$IMAGE_REF"
  printf 'sign it by reference:\n  aggrega sign %s\n' "$IMAGE_REF"
fi
