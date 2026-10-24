#!/usr/bin/env bash
# aggrega-cli/scripts/publish-tooling-image.sh — publish the tooling image from
# the archives `scripts/build-tooling-image.sh --save <dir>` wrote, building
# nothing.
#
# <dir> holds one docker archive per platform, each carrying the version
# reference; platforms, one line per platform naming the platform and its
# archive, `<os>/<arch> <file>`; and references, the references the image is
# published under, one per line: the version reference first, then the moving
# :stable one. This script loads every archive into the local docker image
# store, checks that each carries the version reference, naming an image of the
# platform its line names and a different image from every other archive's; and
# only then pushes. It pushes each platform's image under the version reference
# in turn, reading the digest the registry stored it under from the push, and
# then, once every platform's image is stored, joins them in one index — a
# Docker manifest list, written with `docker manifest create` and pushed with
# `docker manifest push` — under every listed reference. The index goes last
# because the registry accepts an index only when every image it names is
# already stored by digest; the version reference names the index once it is
# pushed, and no reference is left naming a single platform's image. Every
# listed reference must be in one repository, since the index names its images
# by digest in the repository it is pushed to.
#
# Loading points the version reference at each loaded image in turn, so before
# it loads, the script records which image each listed reference names and
# which images the store holds, and however it ends, a refusal or an interrupt
# included, it puts the store back: a reference that named an image names that
# image again, a reference the load added is removed, and every image it loaded
# is removed unless the store held it before or a tag still names it. An image
# pulled under :stable before the run is still there under :stable after it,
# and a machine that publishes release after release keeps no copy. One case is
# not put back: docker removes an image with its last tag, so an untagged image
# the store held before is removed when an archive carries it and none of these
# references named an image before. The index it creates is removed from
# docker's own list of them once pushed. The bytes published are the ones the
# build wrote: nothing here runs a Dockerfile, so the machine that publishes
# runs no build, and the docker commands it runs — load, image inspect, tag,
# push, image rm and manifest — are docker's own. Sign the version reference
# afterwards; it names the index, whose digest every listed reference shares, so
# the signature covers :stable too:
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
#   --dry-run            Load the archives and check them as a real run does,
#                        then print what would be pushed, pushing nothing. The
#                        store is put back as on a real run.
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
REFS_FILE="$ARCHIVE_DIR/references"
PLATFORMS_FILE="$ARCHIVE_DIR/platforms"
[ -s "$REFS_FILE" ] || die "no reference list at $REFS_FILE"
[ -s "$PLATFORMS_FILE" ] || die "no platform list at $PLATFORMS_FILE"

# repository_of <reference>: the reference without its tag. A tag follows the
# last colon only when that colon comes after the last slash, since a registry
# host may carry a port.
repository_of() {
  local ref="$1" last="${1##*/}"
  case "$last" in
    *:*) printf '%s\n' "${ref%:*}" ;;
    *) printf '%s\n' "$ref" ;;
  esac
}

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
REPOSITORY="$(repository_of "${REFS[0]}")"
for ref in "${REFS[@]}"; do
  [ "$(repository_of "$ref")" = "$REPOSITORY" ] || die "$REFS_FILE names $ref, outside the repository of ${REFS[0]} ($REPOSITORY); the index is pushed under every reference, so they share one repository"
done

PLATFORMS=()
ARCHIVES=()
while IFS= read -r line || [ -n "$line" ]; do
  [ -n "$line" ] || continue
  platform="${line%% *}"
  archive="${line#* }"
  [[ "$platform" =~ ^[a-z0-9]+/[a-z0-9]+$ ]] || die "$PLATFORMS_FILE: '$line' does not begin with a platform written <os>/<arch>"
  [[ "$archive" =~ ^[A-Za-z0-9._-]+$ ]] || die "$PLATFORMS_FILE: '$line' does not name an archive file in $ARCHIVE_DIR"
  [ -s "$ARCHIVE_DIR/$archive" ] || die "$PLATFORMS_FILE names $archive for $platform, which is not in $ARCHIVE_DIR"
  for i in "${!PLATFORMS[@]}"; do
    [ "${PLATFORMS[$i]}" != "$platform" ] || die "$PLATFORMS_FILE names $platform twice"
    [ "${ARCHIVES[$i]}" != "$archive" ] || die "$PLATFORMS_FILE names $archive twice"
  done
  PLATFORMS+=("$platform")
  ARCHIVES+=("$archive")
done < "$PLATFORMS_FILE"
[ "${#PLATFORMS[@]}" -gt 0 ] || die "$PLATFORMS_FILE lists no platform"

command -v docker >/dev/null 2>&1 || die "docker not found on PATH"
# The index is written by docker's own manifest command; a docker without it is
# refused here, before anything is loaded or pushed, rather than after every
# platform's image is already up.
docker manifest create --help >/dev/null 2>&1 || die "this docker has no manifest command, which joins the platforms' images in one index; nothing was pushed"

# What the store holds before the load, which restore_image_store puts back: the
# image each reference names, empty where it names none, and every image present.
PRESENT_BEFORE="$(docker image ls --all --quiet --no-trunc)" || die "could not list the images in the local docker store"
BEFORE_IDS=()
for ref in "${REFS[@]}"; do
  id="$(docker image inspect --format '{{.Id}}' "$ref" 2>/dev/null)" || id=""
  BEFORE_IDS+=("$id")
done
LOADED_IDS=()

# Puts the store back as the script found it, however the script ends, a refusal
# or an interrupt included; a further interrupt does not stop it part way. The
# references the load added are removed before the others are pointed back,
# because docker removes an image with its last tag: in this order an untagged
# image the store held before keeps a tag until a reference moves off it, which
# leaves it untagged rather than removed. Each image a reference names after the
# load, and each image the loads brought in, is then removed by ID, unless the
# store held it before or a tag still names it.
restore_image_store() {
  local i ref id named=""
  set +e
  trap '' INT TERM HUP
  for ref in "${REFS[@]}"; do
    id="$(docker image inspect --format '{{.Id}}' "$ref" 2>/dev/null)" && named="$named $id"
  done
  for id in ${LOADED_IDS[@]+"${LOADED_IDS[@]}"}; do
    named="$named $id"
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

# Load and check every archive before anything is pushed. Each load points the
# version reference at the image it brings in, so each image is recorded by ID
# as it arrives.
for i in "${!PLATFORMS[@]}"; do
  platform="${PLATFORMS[$i]}"
  archive="$ARCHIVE_DIR/${ARCHIVES[$i]}"
  docker load --input "$archive"
  id="$(docker image inspect --format '{{.Id}}' "${REFS[0]}" 2>/dev/null)" || die "the archive $archive does not carry ${REFS[0]}"
  LOADED_IDS+=("$id")
  carried="$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$id")" || die "could not read the platform of the image $archive carries"
  [ "$carried" = "$platform" ] || die "$PLATFORMS_FILE lists $archive for $platform, but the image it carries is for $carried"
  for ((j = 0; j < i; j++)); do
    [ "${LOADED_IDS[$j]}" != "$id" ] || die "$archive and ${ARCHIVES[$j]} carry the same image $id; each platform has an image of its own"
  done
done

if [ "$DRY_RUN" = 1 ]; then
  printf 'dry-run — loaded and checked:\n'
  for i in "${!PLATFORMS[@]}"; do
    printf '  %s  %s  image %s\n' "${PLATFORMS[$i]}" "${ARCHIVES[$i]}" "${LOADED_IDS[$i]}"
  done
  printf 'would push each image under %s in turn, in the order above, then join them by digest in one index pushed under:\n' "${REFS[0]}"
  printf '  %s\n' "${REFS[@]}"
  printf 'then sign by reference:\n  aggrega sign %s\n' "${REFS[0]}"
  exit 0
fi

# Each platform's image under the version reference, in turn. The registry
# keeps every image by digest when the reference moves on, so the index below
# can name each one; the digest is the one the push reports the registry stored.
DIGESTS=()
for i in "${!PLATFORMS[@]}"; do
  docker tag "${LOADED_IDS[$i]}" "${REFS[0]}"
  out="$(docker push "${REFS[0]}")" || { printf '%s\n' "$out"; die "the push of the ${PLATFORMS[$i]} image under ${REFS[0]} failed; no index was pushed"; }
  printf '%s\n' "$out"
  [[ "$out" =~ digest:\ (sha256:[0-9a-f]{64}) ]] || die "the push of the ${PLATFORMS[$i]} image reported no digest; the index cannot name it, and no index was pushed"
  DIGESTS+=("${BASH_REMATCH[1]}")
done

# The index, under every listed reference, the version reference first. Each
# create starts from no list of that name, so nothing an earlier run left in
# docker's list of them is carried into this one.
MEMBERS=()
for digest in "${DIGESTS[@]}"; do
  MEMBERS+=("$REPOSITORY@$digest")
done
INDEX_DIGEST=""
for ref in "${REFS[@]}"; do
  docker manifest rm "$ref" >/dev/null 2>&1 || true
  docker manifest create "$ref" "${MEMBERS[@]}"
  out="$(docker manifest push --purge "$ref")" || { printf '%s\n' "$out"; die "the push of the index under $ref failed"; }
  printf '%s\n' "$out"
  pushed="$(printf '%s\n' "$out" | sed -n 's/^\(sha256:[0-9a-f]\{64\}\)$/\1/p' | tail -n 1)"
  [ -n "$pushed" ] || die "the push of the index under $ref reported no digest"
  if [ -z "$INDEX_DIGEST" ]; then
    INDEX_DIGEST="$pushed"
  elif [ "$pushed" != "$INDEX_DIGEST" ]; then
    die "the index under $ref is $pushed but under ${REFS[0]} it is $INDEX_DIGEST; a signature of ${REFS[0]} would not cover $ref"
  fi
done

printf '\npushed the index %s, joining:\n' "$INDEX_DIGEST"
for i in "${!PLATFORMS[@]}"; do
  printf '  %s  %s@%s\n' "${PLATFORMS[$i]}" "$REPOSITORY" "${DIGESTS[$i]}"
done
printf 'under:\n'
printf '  %s\n' "${REFS[@]}"
printf 'sign it by reference:\n  aggrega sign %s\n' "${REFS[0]}"
printf 'the signature resolves to the index digest, which every reference above shares, so it covers them all\n'
