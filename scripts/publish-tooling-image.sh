#!/usr/bin/env bash
# aggrega-cli/scripts/publish-tooling-image.sh — publish the tooling image from
# the archive `scripts/build-tooling-image.sh --save <dir>` wrote, building
# nothing.
#
# <dir> holds image.tar, a docker archive, and references, the references the
# image was built under, one per line: the version reference first, then the
# moving :stable one. This script loads the archive into the local docker image
# store, checks that every listed reference is now present there and that all of
# them name one image, and pushes each. Loading points those references at the
# loaded image, so before it loads, the script records which image each of them
# names and which images the store holds, and however it ends, a refusal or an
# interrupt included, it puts the store back: a reference that named an image
# names that image again, a reference the load added is removed, and the loaded
# image is removed unless the store held it before or a tag still names it. An
# image pulled under :stable before the run is still there under :stable after
# it, and a machine that publishes release after release keeps no copy. One
# case is not put back: docker removes an image with its last tag, so an
# untagged image the store held before is removed when the archive carries it
# and none of these references named an image before. The bytes published are
# the ones the build wrote: nothing here runs a Dockerfile, so the machine that
# publishes runs no build. Sign the version reference afterwards; the signature
# resolves to the shared digest, so it covers :stable too:
#   aggrega sign <the first reference>
#
# Registry login is the caller's: run `docker login <host>` first.
#
# Usage:
#   scripts/publish-tooling-image.sh --archive-dir <dir> --registry <host> [--dry-run]
#
# Options:
#   --archive-dir <dir>  The directory build-tooling-image.sh --save wrote.
#   --registry <host>    The registry this run publishes to. Every reference must
#                        name it, so an archive built for another registry is
#                        refused before anything is pushed.
#   --dry-run            Load the archive and check its references as a real
#                        run does, then print what would be pushed, pushing
#                        nothing. The store is put back as on a real run.
#   -h, --help           Show this help.
#
# Exit non-zero on the first failing step.
set -euo pipefail

die()   { printf 'publish-tooling-image: %s\n' "$*" >&2; exit 1; }
usage() { printf 'publish-tooling-image: %s\n' "$*" >&2; exit 2; }

ARCHIVE_DIR=""
REGISTRY=""
DRY_RUN=0

while [ "$#" -gt 0 ]; do
  case "$1" in
    --archive-dir) [ -n "${2:-}" ] || usage "--archive-dir needs a directory"; ARCHIVE_DIR="$2"; shift 2 ;;
    --registry)    [ -n "${2:-}" ] || usage "--registry needs a host"; REGISTRY="$2"; shift 2 ;;
    --dry-run)     DRY_RUN=1; shift ;;
    # Ends at the header's own closing line, so the help follows the header as it
    # grows.
    -h|--help)     sed -n '2,/^# Exit non-zero on the first failing step\.$/p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *)             usage "unknown argument: $1" ;;
  esac
done

[ -n "$ARCHIVE_DIR" ] || usage "--archive-dir <dir> is required"
[ -n "$REGISTRY" ] || usage "--registry <host> is required"
ARCHIVE="$ARCHIVE_DIR/image.tar"
REFS_FILE="$ARCHIVE_DIR/references"
[ -s "$ARCHIVE" ] || die "no archive at $ARCHIVE"
[ -s "$REFS_FILE" ] || die "no reference list at $REFS_FILE"

REFS=()
while IFS= read -r ref || [ -n "$ref" ]; do
  [ -n "$ref" ] || continue
  case "$ref" in
    "$REGISTRY"/*) ;;
    *) die "$REFS_FILE names $ref, which is not under the registry this run publishes to ($REGISTRY)" ;;
  esac
  REFS+=("$ref")
done < "$REFS_FILE"
[ "${#REFS[@]}" -gt 0 ] || die "$REFS_FILE lists no reference"

command -v docker >/dev/null 2>&1 || die "docker not found on PATH"

# What the store holds before the load, which restore_image_store puts back: the
# image each reference names, empty where it names none, and every image present.
PRESENT_BEFORE="$(docker image ls --all --quiet --no-trunc)" || die "could not list the images in the local docker store"
BEFORE_IDS=()
for ref in "${REFS[@]}"; do
  id="$(docker image inspect --format '{{.Id}}' "$ref" 2>/dev/null)" || id=""
  BEFORE_IDS+=("$id")
done

# Puts the store back as the script found it, however the script ends, a refusal
# or an interrupt included; a further interrupt does not stop it part way. The
# references the load added are removed before the others are pointed back,
# because docker removes an image with its last tag: in this order an untagged
# image the store held before keeps a tag until a reference moves off it, which
# leaves it untagged rather than removed. Each image a reference names after the
# load is then removed by ID, unless the store held it before or a tag still
# names it.
restore_image_store() {
  local i ref id named=""
  set +e
  trap '' INT TERM HUP
  for ref in "${REFS[@]}"; do
    id="$(docker image inspect --format '{{.Id}}' "$ref" 2>/dev/null)" && named="$named $id"
  done
  for i in "${!REFS[@]}"; do
    [ -n "${BEFORE_IDS[$i]}" ] || docker image rm "${REFS[$i]}" >/dev/null 2>&1
  done
  for i in "${!REFS[@]}"; do
    [ -z "${BEFORE_IDS[$i]}" ] || docker tag "${BEFORE_IDS[$i]}" "${REFS[$i]}" >/dev/null 2>&1
  done
  for id in $named; do
    case "$PRESENT_BEFORE" in *"$id"*) continue ;; esac
    [ "$(docker image inspect --format '{{len .RepoTags}}' "$id" 2>/dev/null)" = 0 ] || continue
    docker image rm "$id" >/dev/null 2>&1
  done
  return 0
}
trap restore_image_store EXIT

docker load --input "$ARCHIVE"

image_id=""
for ref in "${REFS[@]}"; do
  id="$(docker image inspect --format '{{.Id}}' "$ref" 2>/dev/null)" || die "the archive $ARCHIVE does not carry $ref"
  if [ -z "$image_id" ]; then
    image_id="$id"
  elif [ "$id" != "$image_id" ]; then
    die "$ref names image $id but ${REFS[0]} names $image_id; the references must name one image"
  fi
done

if [ "$DRY_RUN" = 1 ]; then
  printf 'dry-run — %s loads as image %s; would push:\n' "$ARCHIVE" "$image_id"
  printf '  %s\n' "${REFS[@]}"
  printf 'then sign by reference:\n  aggrega sign %s\n' "${REFS[0]}"
  exit 0
fi

for ref in "${REFS[@]}"; do
  docker push "$ref"
done

printf '\npushed %s\n' "${REFS[@]}"
printf 'sign it by reference:\n  aggrega sign %s\n' "${REFS[0]}"
printf 'the signature resolves to the shared digest, so it also covers the other references above\n'
