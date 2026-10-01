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
# separate signing pass is needed.
#
# This script does NOT build the binary. The binary is produced by the
# repository build (`scripts/build.sh` at the workspace root), which regenerates
# the embedded Wasm guests and stamps the git-describe version. Run that first;
# this script verifies the resulting binary is embedded and stamped, then builds
# and (optionally) pushes the image. Keeping the build in one place means the
# image always packages the artifact the build scripts produce, stripped of its
# debug information as described next.
#
# WHY the image carries a stripped copy of the binary: the hub registry, the
# default --registry, refuses a single upload request above 100,000,000 bytes.
# It runs as a Cloudflare Worker, whose request body is capped by the hosting
# account's plan, and it advertises the same figure as the largest upload chunk
# it accepts. docker buildx sends each layer in one request, and the binary is
# one file, so its layer cannot be split; with its debug information that layer
# comes close to the ceiling, and it grows with every release. So this script
# writes a copy of the binary with its debug information removed
# (`strip --strip-debug`; the symbol table stays) into a build context of its
# own, a temporary directory holding only that copy under the name `aggrega`,
# and builds the image from it. The recipe is the shared one in
# scripts/lib/strip-debug.sh, which `scripts/build.sh --stripped` also uses, so
# every stripped binary a release carries is made the same way. The binary given
# by --binary is never modified.
# The copy must report the same `version` and `abiVersion` in
# `aggrega version -o json` as the original, or the script refuses. It strips on
# every run, a dry run included: stripping is local, and a dry run exists to
# show that the real run would work. `strip` (GNU binutils) must be on PATH.
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
#   --keep-stripped <path>
#                       Also keep the stripped copy of the binary at <path>. It
#                       is the same file the image carries, byte for byte, so a
#                       caller can publish exactly what the image holds. Without
#                       it the stripped copy exists only in the temporary build
#                       context, which is removed when the script exits. <path>
#                       must not name the --binary file itself.
#   --push              Push the image after building (default: build only,
#                       loaded into the local docker image store).
#   --allow-dirty       Permit building/pushing a -dirty tag (refused by
#                       default when --push is set, so a work-in-progress tree
#                       is never published).
#   --dry-run           Strip and check the copy as a real run does, then print
#                       the planned commands without running docker.
#   -h, --help          Show this help.
#
# Exit non-zero on the first failing step.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CLI_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# The stripped-copy recipe (strip_debug_copy) has one home, shared with
# scripts/build.sh; see the header.
# shellcheck source-path=SCRIPTDIR source=lib/strip-debug.sh
. "$SCRIPT_DIR/lib/strip-debug.sh"

REGISTRY="hub.aggrega.ai"
IMAGE_NAME="tools/aggrega"
PLATFORM="linux/amd64"
BINARY="$CLI_DIR/aggrega"
TAG=""
KEEP_STRIPPED=""
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
    --keep-stripped)
      [ -n "${2:-}" ] || die "--keep-stripped needs a path"
      KEEP_STRIPPED="$2"; shift 2 ;;
    --push)       PUSH=1; shift ;;
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
# version and ABI version strings read here. The stripped copy below is read
# through the same function, so the two identities are compared field for field.
# An absent field yields an empty value rather than a failed pipeline, so the
# refusals below name what is missing instead of the script exiting silently.
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
    printf 'warning: building a -dirty tag (%s) from an uncommitted tree\n' "$TAG" >&2
    ;;
esac

# Strip a copy of the binary into a build context of its own (see the header
# for why), through the shared recipe, which writes the copy and leaves the
# input untouched.
command -v strip >/dev/null 2>&1 || die "strip not found on PATH.
the tooling image carries a copy of the binary stripped of its debug information
(see --help for why); install GNU binutils, which provides strip, and re-run."
if [ -n "$KEEP_STRIPPED" ] && [ -e "$KEEP_STRIPPED" ] && [ "$KEEP_STRIPPED" -ef "$BINARY" ]; then
  die "--keep-stripped names the --binary file itself ($KEEP_STRIPPED); the binary must stay as built"
fi
if [ -n "$KEEP_STRIPPED" ] && [ -d "$KEEP_STRIPPED" ]; then
  die "--keep-stripped names a directory ($KEEP_STRIPPED); give the path of the file to write"
fi

CONTEXT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/aggrega-tooling-image.XXXXXX")" || die "could not create a temporary build context"
trap 'rm -rf "$CONTEXT_DIR"' EXIT
STRIPPED="$CONTEXT_DIR/aggrega"
strip_debug_copy "$BINARY" "$STRIPPED" || die "could not write the stripped copy of $BINARY"

STRIPPED_JSON="$("$STRIPPED" version -o json 2>/dev/null)" || die "the stripped copy of $BINARY does not run: could not read its version"
STRIPPED_VERSION="$(json_field "$STRIPPED_JSON" version)"
STRIPPED_ABI="$(json_field "$STRIPPED_JSON" abiVersion)"
if [ "$STRIPPED_VERSION" != "$BIN_VERSION" ] || [ "$STRIPPED_ABI" != "$BIN_ABI" ]; then
  die "the stripped copy reports version '$STRIPPED_VERSION' and abiVersion '$STRIPPED_ABI',
but $BINARY reports version '$BIN_VERSION' and abiVersion '$BIN_ABI';
refusing to build an image whose binary does not report the original's identity"
fi

if [ -n "$KEEP_STRIPPED" ]; then
  cp "$STRIPPED" "$KEEP_STRIPPED" || die "could not keep the stripped copy at $KEEP_STRIPPED"
  chmod 0755 "$KEEP_STRIPPED"
fi

IMAGE_REF="$REGISTRY/$IMAGE_NAME:$TAG"
STABLE_REF="$REGISTRY/$IMAGE_NAME:stable"

# docker buildx build with the binary's version + ABI stamped as labels via
# build args. --push publishes; otherwise --load leaves the image in the local
# store for inspection or a separate push. On a push the same build carries the
# moving :stable tag as a second --tag, so both references are published from
# one build and resolve to the identical digest. :stable rides on push only —
# a local --load never fabricates a moving tag.
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
else
  BUILD_CMD+=(--load)
fi
BUILD_CMD+=("$CONTEXT_DIR")

printf 'image:     %s\n' "$IMAGE_REF"
if [ "$PUSH" = 1 ]; then
  printf 'stable:    %s\n' "$STABLE_REF"
fi
printf 'version:   %s\n' "$BIN_VERSION"
printf 'abi:       %s\n' "$BIN_ABI"
printf 'platform:  %s\n' "$PLATFORM"
printf 'push:      %s\n' "$([ "$PUSH" = 1 ] && echo yes || echo 'no (local load)')"
printf 'binary:    %s (%s bytes, left as built)\n' "$BINARY" "$(wc -c < "$BINARY" | tr -d '[:space:]')"
printf 'stripped:  %s bytes, debug information removed; same version and abiVersion\n' "$(wc -c < "$STRIPPED" | tr -d '[:space:]')"
printf 'context:   %s (the stripped copy only; removed on exit)\n' "$CONTEXT_DIR"
if [ -n "$KEEP_STRIPPED" ]; then
  printf 'kept:      %s (the same file the image carries)\n' "$KEEP_STRIPPED"
fi

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
  printf 'pushed %s (moving tag, same digest)\n' "$STABLE_REF"
  printf 'sign it by reference:\n  aggrega sign %s\n' "$IMAGE_REF"
  printf 'the signature resolves to the shared digest, so it also covers %s\n' "$STABLE_REF"
fi
