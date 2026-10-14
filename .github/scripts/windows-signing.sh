#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# windows-signing.sh — whether the release workflow signs the Windows binaries in
# this run, and the refusal of a run that would leave them unsigned and publish.
#
#   decide  signs, unless the run is a manual dry run launched from a ref the
#           signing identity is taken not to admit: then the Windows binaries are
#           left as the build wrote them, sign-windows is skipped, and the Windows
#           smoke run runs them unsigned, its signature check not applicable;
#   guard   refuses a decision to leave them unsigned on any run but a manual dry
#           run, so a tag push and a dispatch that publishes always sign, and a
#           signing that fails always fails the run.
#
# The decision reads the event, the dry_run input and the ref, and nothing else,
# before anything is signed: it is never taken from a signing that failed.
#
# Which refs may sign is decided by the signing identity's federated credentials
# in Microsoft Entra ID, which this repository cannot read. The refs this script
# takes the identity to admit are written once, in ADMITTED_REFS below: the main
# branch, from which a dry run proves the signing before a tag exists, and every
# tag, the refs a release is signed from. Any other ref is taken not to be
# admitted; Entra ID refuses the token of a run from a branch its credentials do
# not name, naming that branch. A dry run from a ref the list names but the
# identity does not admit still signs, and fails in the signing, naming the ref;
# a dry run from a ref the identity admits but the list leaves out is left
# unsigned, and publishes nothing either way.
#
# The guard reads the event and the input itself rather than asking decide, and
# never consults ADMITTED_REFS: it checks the one property that keeps an unsigned
# binary from a release, so a decide that went wrong cannot also pass it. The two
# are kept apart deliberately; merging them would leave the guard checking
# nothing decide had not already concluded.
#
# Run with --help for the arguments.

set -euo pipefail

usage() {
  cat <<'EOF'
usage:
  windows-signing.sh decide --event <name> --dry-run <value> --ref <ref>
  windows-signing.sh guard --signing <decision> --event <name> --dry-run <value>
  windows-signing.sh --help

decide
  Prints the decision, sign or skip, and why. Under GitHub Actions it is also
  written to the step's outputs as signing=<decision>. It is skip only when the
  event is workflow_dispatch, the dry_run input is true, and the ref is not one
  the signing identity is taken to admit: refs/heads/main, or any ref under
  refs/tags/. Every other run signs, a tag push and a dispatch that publishes
  among them.

guard
  Passes a decision of sign on any run, and a decision of skip on a
  workflow_dispatch whose dry_run input is true. It refuses skip on any other
  run, naming the event and the input, and refuses a decision that is neither
  sign nor skip, an empty one included.

Arguments:
  --event     the run's event name, as github.event_name gives it: push,
              workflow_dispatch
  --dry-run   the dry_run input as the run reads it: true, false, or empty on
              an event that carries no inputs
  --ref       the ref the run was launched from, as github.ref gives it:
              refs/heads/main, refs/tags/v1.2.3
  --signing   the decision decide wrote, as a later step reads it

Exit status: 0 when the decision is written or passes, 1 when the guard
refuses, 2 for a usage error.
EOF
}

# The refs the signing identity is taken to admit, as shell patterns. This is
# the one statement of them in the repository.
ADMITTED_REFS=('refs/heads/main' 'refs/tags/*')

fail() {
  echo "windows-signing: $*" >&2
  exit 1
}

usage_error() {
  echo "windows-signing: $*" >&2
  echo "windows-signing: run with --help for the arguments" >&2
  exit 2
}

if [ "$#" -eq 0 ]; then
  usage_error "name a command: decide or guard"
fi

case "$1" in
  -h|--help)
    usage
    exit 0
    ;;
  decide|guard)
    command="$1"
    shift
    ;;
  *)
    usage_error "unknown command '$1'"
    ;;
esac

event=""
event_set=false
dry_run=""
dry_run_set=false
ref=""
ref_set=false
signing=""
signing_set=false

while [ "$#" -gt 0 ]; do
  case "$1" in
    -h|--help)
      usage
      exit 0
      ;;
    --event|--dry-run|--ref|--signing)
      [ "$#" -ge 2 ] || usage_error "$1 needs a value"
      case "$1" in
        --event) event="$2"; event_set=true ;;
        --dry-run) dry_run="$2"; dry_run_set=true ;;
        --ref) ref="$2"; ref_set=true ;;
        --signing) signing="$2"; signing_set=true ;;
      esac
      shift 2
      ;;
    *)
      usage_error "unknown argument '$1'"
      ;;
  esac
done

"$event_set" || usage_error "$command needs --event"
[ -n "$event" ] || usage_error "$command needs a non-empty --event"
"$dry_run_set" || usage_error "$command needs --dry-run, empty on an event that carries no inputs"
if [ "$command" = decide ]; then
  "$ref_set" || usage_error "decide needs --ref"
  [ -n "$ref" ] || usage_error "decide needs a non-empty --ref"
  ! "$signing_set" || usage_error "decide takes no --signing"
else
  "$signing_set" || usage_error "guard needs --signing, the decision decide wrote"
  ! "$ref_set" || usage_error "guard takes no --ref: it judges the event and the input alone"
fi

admitted() {
  local pattern
  for pattern in "${ADMITTED_REFS[@]}"; do
    # shellcheck disable=SC2053 # the right-hand side is matched as a pattern
    if [[ $1 == $pattern ]]; then
      return 0
    fi
  done
  return 1
}

decide() {
  local decision reason
  if [ "$event" != workflow_dispatch ]; then
    decision=sign
    reason="a $event run signs the Windows binaries"
  elif [ "$dry_run" != true ]; then
    decision=sign
    reason="a dispatch with dry_run=${dry_run:-<empty>} is not a dry run, so it signs the Windows binaries"
  elif admitted "$ref"; then
    decision=sign
    reason="a dry run from $ref, a ref the signing identity is taken to admit, signs the Windows binaries"
  else
    decision=skip
    reason="a dry run from $ref, a ref the signing identity is taken not to admit, leaves the Windows binaries unsigned: sign-windows is skipped, and the Windows smoke run runs them as the build wrote them, its signature check not applicable"
  fi
  echo "windows-signing: $decision: $reason"
  if [ -n "${GITHUB_OUTPUT:-}" ]; then
    printf 'signing=%s\n' "$decision" >> "$GITHUB_OUTPUT"
  fi
}

guard() {
  case "$signing" in
    sign)
      echo "windows-signing: this run signs the Windows binaries"
      ;;
    skip)
      if [ "$event" = workflow_dispatch ] && [ "$dry_run" = true ]; then
        echo "windows-signing: this run leaves the Windows binaries unsigned, which only a manual dry run may do, and it is one: it publishes nothing"
      else
        fail "refused: the decision is to leave the Windows binaries unsigned, but this is a $event run with dry_run=${dry_run:-<empty>}, a run that publishes; only a workflow_dispatch with dry_run true may leave them unsigned, so the run stops here"
      fi
      ;;
    *)
      fail "refused: the decision '$signing' is neither sign nor skip, so whether this run signs the Windows binaries is unknown, and the run stops here"
      ;;
  esac
}

case "$command" in
  decide) decide ;;
  guard) guard ;;
esac
