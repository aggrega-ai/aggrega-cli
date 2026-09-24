#!/usr/bin/env bash
# aggrega-cli/scripts/build.sh — self-contained build for the aggrega CLI.
#
# 🔴 THIS IS NOT THE GATE SCRIPT, AND THE TWO SHARE A FILENAME.
#
# It builds; it does not test. There are no test modes here — no unit battery, no
# integration battery, no drift gates, no lint tiers. Its only mode switch is
# --light. Invoking it and reading a success as "the gate passed" reports a green
# for a battery that never ran.
#
# The gate lives at the umbrella root and is invoked from there:
#
#     ./scripts/build.sh --test-fast aggrega-cli
#
# That script is a different program with the same basename, and the umbrella root
# is where every gate invocation belongs. Anything asking for a build gate wants
# that one, not this one. So any --test… flag given here is answered with that
# pointer and exit 2, rather than with a bare "unknown argument".
#
# Builds the version-stamped aggrega binary with its provider/linter guests
# embedded (default), or the no-embed aggrega-light binary (--light). Runs from a
# sibling checkout — aggrega-cli/ beside its sibling repositories
# (aggrega-schemas, aggrega-pdk, aggrega-abi, and, for the overlay check,
# aggrega-hub) — with no workspace file and no committed go.work: the go.mod
# replace directives (../aggrega-schemas, ../aggrega-pdk/go, ../aggrega-abi/go)
# resolve the siblings from their local working copies, offline, under
# GOWORK=off.
#
# Default (embedded) build:
#   1. guest-freshness gate — a fast mtime check; regeneration is skipped when
#      the embedded guests are already current
#   2. regenerate the embedded guests when they are stale or missing
#   3. overlay-consistency check — when the aggrega-hub sibling is present
#   4. go build the version-stamped binary with the guests embedded -> ./aggrega
#
# --light build:
#   go build -tags light produces the no-embed binary -> ./aggrega-light. The
#   light binary embeds no Wasm guests and resolves every provider through the
#   plugin cascade, so it needs no guest regeneration and no overlay check.
#
# The binary is stamped from git: -X main.version=<git-describe, v-stripped> and
# -X main.commit=<short-sha>. `git describe` yields the tag, the bare commit
# before any tag, or a -dirty suffix on uncommitted changes; a leading v on the
# tag is stripped so the stamped version string is v-less. The CLI's version
# command prefers these -ldflags values over runtime build info, whose VCS
# settings are unreliable for a module built inside a git submodule. Builds are
# deterministic (-trimpath, cleared -buildid); the embedded guests are
# reproducible per commit by the guest builder.
#
# Usage:
#   scripts/build.sh            # embedded aggrega binary (default)
#   scripts/build.sh --light    # no-embed aggrega-light binary
#   scripts/build.sh --help
#   scripts/build.sh --test…    # refused (exit 2): names the workspace build script
#
# Exit non-zero on the first failing step.
set -euo pipefail

CLI_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$CLI_ROOT"

# The sibling modules resolve through the go.mod parent-relative replace
# directives with workspace mode off; a stray go.work in a parent directory
# must not shadow them, so pin GOWORK=off for the whole run. This also mirrors
# the guest builder, which forces GOWORK=off internally.
export GOWORK=off

# The build-provenance version (_ver) and short commit (_sha) come from the one
# shared recipe, so this binary, the embedded guests' describe(), and the
# embedded plugin-entity generator can never derive them by divergent
# invocations. Sourced after the cd above so git resolves against the module root.
. scripts/lib/build-version.sh

BINPATH="./cmd/aggrega"

CYAN='\033[36m'
GREEN='\033[32m'
YELLOW='\033[33m'
RED='\033[31m'
BOLD='\033[1m'
RESET='\033[0m'

step() { printf "${CYAN}${BOLD}==> %s${RESET}\n" "$*"; }
ok()   { printf "${GREEN}✓ %s${RESET}\n" "$*"; }
note() { printf "${YELLOW}  %s${RESET}\n" "$*"; }
fail() { printf "${RED}✗ %s${RESET}\n" "$*" >&2; }

MODE="build"
for arg in "$@"; do
  case "$arg" in
    --light)
      MODE="light"
      ;;
    -h|--help)
      sed -n '2,/^# Exit non-zero on the first failing step\.$/p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    --test*)
      # Answered rather than rejected as unknown: running a test mode from here is
      # the common mistake, and the fix is a different script at a different root.
      fail "$arg: this script builds the CLI and has no test modes."
      fail "  The test modes belong to the umbrella workspace's own build script —"
      fail "  scripts/build.sh at the workspace root, a different script with the same name."
      fail "  Run it from the workspace root:"
      fail "    ./scripts/build.sh $arg aggrega-cli"
      fail "  Outside the umbrella workspace, run this module's tests directly: go test ./..."
      exit 2
      ;;
    *)
      fail "unknown argument: $arg"
      exit 2
      ;;
  esac
done

# stamp_ldflags — emit the -ldflags value that stamps version and commit into the
# binary and clears the build id for reproducibility. The version and commit are
# the shared recipe's values, sourced once above so a --dirty tree stamps the
# current working state.
stamp_ldflags() {
  printf -- '-X main.version=%s -X main.commit=%s -buildid=' "$_ver" "$_sha"
}

if [ "$MODE" = "light" ]; then
  # The light binary embeds no Wasm plugin artifacts (-tags light gates the
  # embed out) and resolves every provider through the plugin cascade, so it
  # needs no guest regeneration and no overlay check.
  step "go build (light, no embed): aggrega-light"
  go build -tags light -trimpath -ldflags "$(stamp_ldflags)" -o aggrega-light "$BINPATH"
  ok "built ./aggrega-light"
  printf "${GREEN}${BOLD}build complete${RESET}\n"
  exit 0
fi

# The embedded (non-light) binary embeds the provider guests and the
# publishing-linter guest. Those .wasm.gz artifacts are reproducible build
# outputs regenerated from guest source, not tracked files, so they are absent
# from a fresh checkout and can predate a later guest-source change. Ask the
# freshness oracle first (a seconds-fast mtime check) and regenerate only when
# the guests are stale or missing.
step "check embedded guest freshness"
if bash scripts/check-embedded-guests.sh; then
  note "embedded guests are fresh — regeneration skipped"
else
  note "embedded guests are stale or missing — regenerating"
  step "regenerate embedded guests"
  bash scripts/build-embedded-plugins.sh
  ok "embedded guests regenerated"
fi

# Read-only consistency check: every declared embedded plugin has an authored
# overlay and a built artifact. It reads the aggrega-hub sibling and never
# writes there. Skipped with a note when that sibling is absent (a
# schemas/pdk/abi-only checkout can still build the binary).
if [ -d "$CLI_ROOT/../aggrega-hub" ]; then
  step "check embedded plugin overlays"
  bash scripts/check-embedded-overlays.sh
  ok "embedded plugin overlays consistent"

  # The full binary embeds a complete Plugin entity for every core guest so a
  # no-local-catalog build resolves core providers offline. Each entity merges the
  # guest's describe() self-report with its authored overlay (from the hub), so
  # generation is hub-coupled like the hub-catalog generator. Ask the entity
  # freshness oracle first (a fast mtime check) and regenerate only when an entity
  # is stale or missing relative to its guest descriptor or overlay.
  step "check embedded plugin-entity freshness"
  if bash scripts/check-embedded-plugin-entities.sh; then
    note "embedded plugin entities are fresh — regeneration skipped"
  else
    note "embedded plugin entities are stale or missing — regenerating"
    step "regenerate embedded plugin entities"
    bash scripts/gen-embedded-plugin-entities.sh
    ok "embedded plugin entities regenerated"
  fi

  # Lay down the gitignored, developer-owned co-located catalog: each core
  # guest's .wasm beside its (byte-identical-to-embedded) Plugin entity. It is
  # opt-in and never referenced by a committed build, so it perturbs neither the
  # full binary here nor determinism; it exists so the aggrega-light dev binary
  # can resolve core providers fully offline when a developer points catalog_dirs
  # at it. Regenerated wholesale from the just-refreshed embed assets.
  step "regenerate co-located plugin catalog (dev, gitignored)"
  bash scripts/gen-colocated-plugin-catalog.sh
  ok "co-located plugin catalog regenerated"
else
  note "aggrega-hub sibling absent — overlay-consistency check and embedded plugin-entity generation skipped"
fi

step "go build (embedded): aggrega"
go build -trimpath -ldflags "$(stamp_ldflags)" -o aggrega "$BINPATH"
ok "built ./aggrega"

printf "${GREEN}${BOLD}build complete${RESET}\n"
