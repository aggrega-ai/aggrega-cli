#!/usr/bin/env bash
# aggrega-cli/scripts/check-third-party-licenses.sh
#
# Fails when the committed third-party attribution is stale against the inputs
# it is generated from.
#
# scripts/gen-third-party-licenses.sh records, in the DocumentComment of
# THIRD-PARTY.spdx, a digest of its inputs: go.mod, go.sum, the Cargo.lock of
# each embedded Rust guest, the embedded rows of the core-plugin list, and the
# generator itself (scripts/lib/third-party-inputs.sh defines the set and the
# digest, for both scripts). This check recomputes the digest and compares. It
# only hashes: no go list, no cargo, no network.
#
# Exit 0 when the recorded digest is the computed one; exit 1 when it differs,
# naming both digests, the inputs read and the command that regenerates; exit 2
# when the check could not run (the SBOM is missing or records no digest, an
# input or the core-plugin list is unreadable), which is never reported as
# current.
set -euo pipefail
cd "$(dirname "$0")/.."

. scripts/lib/core-plugins.sh
. scripts/lib/third-party-inputs.sh

SBOM="THIRD-PARTY.spdx"
REGEN="bash scripts/gen-third-party-licenses.sh"

if [ ! -f "$SBOM" ]; then
  echo "check-third-party-licenses: $SBOM is missing; generate it from the module root with: $REGEN" >&2
  exit 2
fi

recorded="$(awk -v p="$THIRD_PARTY_DIGEST_PREFIX" '
  /^DocumentComment:/ {
    i = index($0, p)
    if (i > 0) {
      s = substr($0, i + length(p))
      if (match(s, /^sha256:[0-9a-f]{64}/)) { print substr(s, 1, RLENGTH); exit }
    }
  }' "$SBOM")"
if [ -z "$recorded" ]; then
  echo "check-third-party-licenses: $SBOM records no inputs digest in its DocumentComment; regenerate it from the module root with: $REGEN" >&2
  exit 2
fi

computed="$(third_party_inputs_digest)" || {
  echo "check-third-party-licenses: could not read the inputs the digest covers" >&2
  exit 2
}
labels="$(third_party_input_labels)" || exit 2

if [ "$recorded" = "$computed" ]; then
  exit 0
fi

{
  echo "check-third-party-licenses: the third-party attribution is stale against its inputs."
  echo "  recorded in $SBOM: $recorded"
  echo "  computed now:                 $computed"
  echo "  inputs read:"
  printf '%s\n' "$labels" | sed 's/^/    /'
  echo "  regenerate from the module root with: $REGEN"
} >&2
exit 1
