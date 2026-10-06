#!/usr/bin/env bash
# aggrega-cli/scripts/lib/plugin-publish.sh
#
# The single home for publishing one Plugin entity paired with its .wasm guest,
# SOURCED (never executed) by both plugin publishers: scripts/publish-plugins.sh,
# which discovers the pairs, and scripts/publish-plugin-pairs.sh, which reads a
# pairing written earlier. Whichever one runs, a pair is published by the same
# command, with the same options, and reported the same way.
#
# A pair is published through `aggrega push --plugin`, which pushes the .wasm
# (addressed by its content digest) and the entity (tagged by its version),
# enforces immutability, and cross-checks the guest's describe() against the
# entity's authored claims. Every integrity guarantee lives in the CLI; this file
# only builds the command and reads its answer.
#
# Nothing here derives a guest's filename or builds anything, so a caller that
# only publishes needs the aggrega binary and nothing else: no toolchain and no
# source tree.
#
# The caller sets, before calling:
#   AGGREGA   the aggrega binary to run
#   REGISTRY  the registry to publish to
#   DRYRUN    "--dry-run" to compute and print the references only, else empty
#   SIGN      non-empty to sign each published reference
#   KEY       a private key path for offline keyed signing, or empty
#   IDTOKEN   an identity token for keyless signing, or empty
#
# Sourcing defines three functions and sets no variables:
#
#   plugin_publish_require_signing_material <prefix>
#     Signing is non-interactive only. On a real signed publish with no key and
#     no token source (--key, --identity-token, AGGREGA_IDENTITY_TOKEN or
#     SIGSTORE_ID_TOKEN), says so on standard error, prefixed with <prefix>, and
#     returns 2, so the keyless flow never falls back to a browser prompt. A dry
#     run signs nothing and always passes.
#
#   plugin_publish_one <wasm> <entity>
#     Publishes one pair, prints the push's own output, and returns its status.
#     On success it sets PLUGIN_PUBLISHED_REF to the resolved
#     <registry>/<path>:<tag> reference, empty when the push printed none.
#
#   plugin_publish_reference
#     Reads captured `aggrega push` output on standard input and prints the
#     resolved reference: the third field of a "Push complete:" or "Already
#     published:" line, the fourth of the "aggrega push (dry-run):" line.

plugin_publish_require_signing_material() {
  local prefix="$1"
  if [ -n "$SIGN" ] && [ -z "$DRYRUN" ]; then
    if [ -z "$KEY" ] && [ -z "$IDTOKEN" ] && [ -z "${AGGREGA_IDENTITY_TOKEN:-}" ] && [ -z "${SIGSTORE_ID_TOKEN:-}" ]; then
      echo "$prefix: --sign: no signing material; pass --key <path> for offline keyed signing, or --identity-token / AGGREGA_IDENTITY_TOKEN / SIGSTORE_ID_TOKEN for non-interactive keyless signing" >&2
      return 2
    fi
  fi
  return 0
}

plugin_publish_reference() {
  awk '
    /^Push complete: /            { print $3; exit }
    /^Already published: /        { print $3; exit }
    /^aggrega push \(dry-run\): / { print $4; exit }
  '
}

plugin_publish_one() {
  local wasm="$1" entity="$2" out
  local cmd=("$AGGREGA" push --plugin "$wasm" "$entity" --registry "$REGISTRY")
  if [ -n "$DRYRUN" ]; then
    cmd+=(--dry-run)
  fi
  if [ -n "$SIGN" ]; then
    cmd+=(--sign)
    if [ -n "$KEY" ]; then
      cmd+=(--key "$KEY")
    fi
    if [ -n "$IDTOKEN" ]; then
      cmd+=(--identity-token "$IDTOKEN")
    fi
  fi
  PLUGIN_PUBLISHED_REF=""
  # The output is captured to recover the resolved reference, then echoed so the
  # push's own report stays visible. The `if` keeps a failing push from ending a
  # caller running under set -e before its failure is counted.
  if out="$("${cmd[@]}")"; then
    printf '%s\n' "$out"
    # Read by the scripts that source this file.
    # shellcheck disable=SC2034
    PLUGIN_PUBLISHED_REF="$(printf '%s\n' "$out" | plugin_publish_reference)"
    return 0
  fi
  printf '%s\n' "$out"
  return 1
}
