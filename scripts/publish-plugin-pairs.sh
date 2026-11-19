#!/usr/bin/env bash
# aggrega-cli/scripts/publish-plugin-pairs.sh — publish a pairing of Plugin
# entities and their .wasm guests, written earlier, through the aggrega binary
# alone.
#
# The pairing is the file `scripts/publish-plugins.sh … --pairs-out <file>`
# writes: one line per plugin, the guest's path, the entity's path, the guest's
# notices file and its SBOM file, separated by tabs, each relative to the
# directory the pairing sits in. Working out which guest
# belongs to which entity needs the Go toolchain and this module's source; this
# script needs neither. It reads the pairing and publishes each pair with the
# same command, options and signing refusal as scripts/publish-plugins.sh, from
# the one shared home in scripts/lib/plugin-publish.sh. It builds nothing and
# runs no program but the aggrega binary, which is what lets the release
# workflow publish and sign from a job that runs no build.
#
# Usage:
#   scripts/publish-plugin-pairs.sh --pairs <file> [options]
#
# Options:
#   --pairs <file>            The pairing to publish (required).
#   --registry <host[:port]>  The registry to publish to (default 127.0.0.1:5000).
#   --dry-run                 Compute and print each proposed reference, with no
#                             network call and no signing.
#   --sign                    Sign each published reference. Non-interactive
#                             only: refused on a real publish unless --key,
#                             --identity-token, AGGREGA_IDENTITY_TOKEN or
#                             SIGSTORE_ID_TOKEN supplies the material.
#   --key <path>              Offline keyed signing with this private key.
#   --identity-token <token>  Keyless signing with this identity token.
#   -h, --help                Show this help.
#
# Env: AGGREGA_BIN names the aggrega binary (default: aggrega on PATH).
#
# It refuses, before publishing anything, a pairing that is missing, empty, or
# holds a line that is not four tab-separated paths naming existing files. It
# publishes every pair even after one fails, then exits 1 when any failed; it
# exits 2 on a usage error and when signing material is missing.
#
# Exit non-zero when any pair was not published.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=lib/plugin-publish.sh
. "$SCRIPT_DIR/lib/plugin-publish.sh"

REGISTRY="127.0.0.1:5000"
DRYRUN=""
SIGN=""
KEY=""
IDTOKEN=""
PAIRS_FILE=""
AGGREGA="${AGGREGA_BIN:-aggrega}"

usage() { printf 'publish-plugin-pairs: %s\n' "$*" >&2; exit 2; }
die()   { printf 'publish-plugin-pairs: %s\n' "$*" >&2; exit 1; }

while [ "$#" -gt 0 ]; do
  case "$1" in
    --pairs)          [ -n "${2:-}" ] || usage "--pairs needs a file"; PAIRS_FILE="$2"; shift 2 ;;
    --registry)       [ -n "${2:-}" ] || usage "--registry needs a host"; REGISTRY="$2"; shift 2 ;;
    --dry-run)        DRYRUN="--dry-run"; shift ;;
    --sign)           SIGN="1"; shift ;;
    --key)            [ -n "${2:-}" ] || usage "--key needs a path"; KEY="$2"; shift 2 ;;
    --identity-token) [ -n "${2:-}" ] || usage "--identity-token needs a token"; IDTOKEN="$2"; shift 2 ;;
    # Ends at the header's own closing line rather than at a line number, so the
    # help follows the header as it grows.
    -h|--help)        sed -n '2,/^# Exit non-zero when any pair was not published\.$/p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *)                usage "unknown argument: $1" ;;
  esac
done

[ -n "$PAIRS_FILE" ] || usage "--pairs <file> is required"
[ -f "$PAIRS_FILE" ] || die "pairing not found: $PAIRS_FILE"
plugin_publish_require_signing_material publish-plugin-pairs || exit 2

PAIRS_DIR="$(cd "$(dirname "$PAIRS_FILE")" && pwd -P)"

# Read and check every line before publishing any, so a malformed pairing
# publishes nothing rather than half of itself.
WASMS=() ENTITIES=() NOTICES=() SBOMS=()
lineno=0
while IFS= read -r line || [ -n "$line" ]; do
  lineno=$((lineno + 1))
  [ -n "$line" ] || die "$PAIRS_FILE:$lineno: an empty line; each line is a guest path, a tab and an entity path"
  rest="$line"
  fields=()
  while :; do
    case "$rest" in
      *$'\t'*) fields+=("${rest%%$'\t'*}"); rest="${rest#*$'\t'}" ;;
      *) fields+=("$rest"); break ;;
    esac
  done
  [ "${#fields[@]}" -eq 4 ] || die "$PAIRS_FILE:$lineno: ${#fields[@]} tab-separated field(s); each line is a guest path, an entity path, a notices path and an SBOM path"
  for k in 0 1 2 3; do
    [ -n "${fields[$k]}" ] || die "$PAIRS_FILE:$lineno: field $((k + 1)) is empty; each line is a guest path, an entity path, a notices path and an SBOM path"
    case "${fields[$k]}" in /*) ;; *) fields[k]="$PAIRS_DIR/${fields[$k]}" ;; esac
  done
  wasm="${fields[0]}" entity="${fields[1]}" notices="${fields[2]}" sbom="${fields[3]}"
  [ -f "$wasm" ] || die "$PAIRS_FILE:$lineno: guest not found: $wasm"
  [ -f "$entity" ] || die "$PAIRS_FILE:$lineno: entity not found: $entity"
  [ -f "$notices" ] || die "$PAIRS_FILE:$lineno: notices not found: $notices"
  [ -f "$sbom" ] || die "$PAIRS_FILE:$lineno: SBOM not found: $sbom"
  WASMS+=("$wasm")
  ENTITIES+=("$entity")
  NOTICES+=("$notices")
  SBOMS+=("$sbom")
done < "$PAIRS_FILE"
[ "${#WASMS[@]}" -gt 0 ] || die "$PAIRS_FILE holds no pair; refusing to report a publish of nothing as a success"

published=0 failed=0
REFS=()
for i in "${!WASMS[@]}"; do
  if plugin_publish_one "${WASMS[$i]}" "${ENTITIES[$i]}" "${NOTICES[$i]}" "${SBOMS[$i]}"; then
    published=$((published + 1))
    if [ -n "$PLUGIN_PUBLISHED_REF" ]; then
      REFS+=("$PLUGIN_PUBLISHED_REF")
    fi
  else
    echo "publish-plugin-pairs: FAILED: ${ENTITIES[$i]}" >&2
    failed=$((failed + 1))
  fi
done

echo "publish-plugin-pairs: $published published, $failed failed -> $REGISTRY${DRYRUN:+ (dry run)}"
if [ "${#REFS[@]}" -gt 0 ]; then
  echo "publish-plugin-pairs: references:"
  printf '  %s\n' "${REFS[@]}"
fi

[ "$failed" -eq 0 ]
