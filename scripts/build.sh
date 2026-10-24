#!/usr/bin/env bash
# aggrega-cli/scripts/build.sh — self-contained build for the aggrega CLI.
#
# 🔴 THIS SCRIPT BUILDS; IT DOES NOT TEST.
#
# There are no test modes here — no unit battery, no integration battery, no drift
# gates, no lint tiers. Its switches choose what to build (--light, --target),
# and --expect-version checks the version the build stamped. Invoking
# it and reading a success as "the tests passed" reports a green for a battery that
# never ran.
#
# This module's tests run through the Go toolchain directly (README.md, "Testing"):
#
#     go test ./...
#
# So any --test… flag given here is answered with that pointer and exit 2, rather
# than with a bare "unknown argument".
#
# Builds the version-stamped aggrega binary with its provider/linter guests
# embedded (default) — written twice, as ./aggrega without its debug information
# and as ./aggrega-debug with it — or the no-embed aggrega-light binary (--light).
# Runs from a sibling checkout — aggrega-cli/ beside its sibling repositories
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
#   4. go build the version-stamped binary with the guests embedded, with its
#      debug information -> ./aggrega-debug
#   5. the same binary without its debug information -> ./aggrega
#
# --light build:
#   go build -tags light produces the no-embed binary -> ./aggrega-light. The
#   light binary embeds no Wasm guests and resolves every provider through the
#   plugin cascade, so it needs no guest regeneration and no overlay check.
#
# --target <goos>/<goarch> builds either variant for another system — any pair
# `go tool dist list` names, as in windows/amd64 or darwin/arm64 — through the
# same steps: the guests, the overlay check and the entity generation stay
# host-side (the guests are WebAssembly, the same for every system), and only the
# final go build is given the target, with cgo off. Its outputs are named for the
# target, so they never overwrite the host binaries: ./aggrega-<goos>-<goarch>,
# ./aggrega-debug-<goos>-<goarch> and ./aggrega-light-<goos>-<goarch>, with .exe
# appended for windows. Without --target the build is for this machine, under
# the names above.
#
# The embedded build always writes both of its binaries. ./aggrega-debug is the
# binary as the linker writes it, debug information included, for a debugger, a
# profiler or anything else that reads it. ./aggrega is that same binary without
# its debug information (the symbol table and the program stay), so it runs the
# same, reports the same version, and is the smaller file. How ./aggrega is made
# depends on whether the build is for this machine, and one place in this script
# decides it and says why. For this machine it is a copy of ./aggrega-debug
# stripped by the one recipe in scripts/lib/strip-debug.sh, and `strip` (GNU
# binutils) must be on PATH. For any --target the linker writes it, with
# ./aggrega-debug's own flags and -w added. However it is made, the script
# refuses an ./aggrega whose stamped version differs from ./aggrega-debug's. The
# light build writes one binary, ./aggrega-light, as the linker writes it.
#
# --expect-version <version> refuses, after building, unless every binary the run
# wrote carries <version> as its stamped version. The stamp is read from each
# binary's own symbol table and data rather than by running it, so a binary built
# for another system is checked on this one: `go version -m` cannot show it,
# because Go leaves the linker flags out of the build information of a -trimpath
# build. The read is one build tool, internal/buildtools/stampedversion, run with
# `go run`; it reads Linux (ELF), Windows (PE) and macOS (Mach-O) binaries through
# the Go standard library, so it needs nothing beyond the toolchain the build
# already uses.
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
#   scripts/build.sh            # embedded binary: aggrega and aggrega-debug
#   scripts/build.sh --light    # no-embed aggrega-light binary
#   scripts/build.sh --target windows/amd64
#                               # aggrega-windows-amd64.exe and
#                               # aggrega-debug-windows-amd64.exe
#   scripts/build.sh --light --target windows/amd64
#                               # aggrega-light-windows-amd64.exe
#   scripts/build.sh --target darwin/arm64
#                               # aggrega-darwin-arm64 and
#                               # aggrega-debug-darwin-arm64
#   scripts/build.sh --target linux/arm64
#                               # aggrega-linux-arm64 and
#                               # aggrega-debug-linux-arm64
#   scripts/build.sh --light --target darwin/arm64
#                               # aggrega-light-darwin-arm64
#   scripts/build.sh … --expect-version <version>
#                               # refuse unless what was built stamps <version>
#   scripts/build.sh --help
#   scripts/build.sh --test…    # refused (exit 2): points at go test ./...
#
# An argument it cannot use is refused with exit 2, before anything is built.
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

# The recipe that copies a binary without its debug information
# (strip_debug_copy): how ./aggrega is made from ./aggrega-debug for any target
# but Windows and macOS.
. scripts/lib/strip-debug.sh

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
TARGET=""
EXPECT_VERSION=""
while [ "$#" -gt 0 ]; do
  arg="$1"
  case "$arg" in
    --light)
      MODE="light"
      shift
      ;;
    --target)
      [ -n "${2:-}" ] || { fail "--target needs <goos>/<goarch>, as in windows/amd64"; exit 2; }
      TARGET="$2"
      shift 2
      ;;
    --expect-version)
      [ -n "${2:-}" ] || { fail "--expect-version needs the version the build must stamp"; exit 2; }
      EXPECT_VERSION="$2"
      shift 2
      ;;
    -h|--help)
      # Read from CLI_ROOT, not "$0": the cd above has already moved away from
      # wherever a relative "$0" was resolved.
      sed -n '2,/^# Exit non-zero on the first failing step\.$/p' "$CLI_ROOT/scripts/$(basename "$0")" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    --test*)
      # Answered rather than rejected as unknown: running a test mode from here is
      # the common mistake, and the fix is to run the tests through Go directly.
      fail "$arg: this script builds the CLI and has no test modes."
      fail "  Run this module's tests directly: go test ./..."
      exit 2
      ;;
    *)
      fail "unknown argument: $arg"
      exit 2
      ;;
  esac
done

# How ./aggrega is made from ./aggrega-debug depends on whether the build is for
# this machine, and this is the one place that decides it: the step that writes
# ./aggrega below follows DEFAULT_MADE_BY and decides nothing. However it is
# made, it is ./aggrega-debug without its debug information, the symbol table
# and the program kept.
#
#   strip   A build for this machine, without --target: ./aggrega-debug is
#           linked, then copied without its debug information by
#           strip_debug_copy, the recipe in scripts/lib/strip-debug.sh.
#   linker  Any --target: the linker writes ./aggrega itself, from
#           ./aggrega-debug's own build with -w added, which leaves the debug
#           information out — the same sources, embedded guests, flags and
#           version stamp. GNU strip reads only the executables of the
#           architectures it was built for, so an x86-64 machine's strip refuses
#           a Linux arm64 binary as one whose architecture it cannot recognise;
#           and two formats cannot be stripped after they are linked at all.
#           Windows (PE): the Go linker places its debug sections in the middle
#           of a Windows image, before the import table, the relocations and the
#           symbol table, and removing them afterwards cannot move the sections
#           that follow, so the image is left with a gap in its addresses where
#           they were; the Windows format requires an image's sections to be
#           adjacent, and Windows refuses to run one that leaves such a gap.
#           macOS (Mach-O): GNU strip cannot read the format, and the Go linker
#           signs an Apple silicon binary ad hoc, with a signature whose hashes
#           cover the file's pages, so a binary changed after linking carries a
#           signature that no longer matches its bytes, which a Mac with Apple
#           silicon refuses to run until the binary is signed again. Linked with
#           -w, the binary is signed by the linker over the bytes it writes, as
#           ./aggrega-debug is.
if [ -n "$TARGET" ]; then
  DEFAULT_MADE_BY="linker"
else
  DEFAULT_MADE_BY="strip"
fi

# The output names, and the go build environment for a --target build. A target
# is checked against the toolchain's own list of the systems it builds for, so a
# misspelt pair is refused by name before anything is built rather than by the
# compiler after the guests are regenerated.
OUT_SUFFIX=""
TARGET_ENV=()
if [ -n "$TARGET" ]; then
  targets="$(go tool dist list)" || { fail "go tool dist list failed; cannot check --target $TARGET"; exit 1; }
  case $'\n'"$targets"$'\n' in
    *$'\n'"$TARGET"$'\n'*) ;;
    *)
      fail "--target $TARGET: not a <goos>/<goarch> pair this Go toolchain builds for"
      fail "  (go tool dist list names them, as in windows/amd64)"
      exit 2
      ;;
  esac
  TARGET_OS="${TARGET%/*}"
  OUT_SUFFIX="-${TARGET_OS}-${TARGET#*/}"
  [ "$TARGET_OS" = "windows" ] && OUT_SUFFIX="$OUT_SUFFIX.exe"
  # cgo off: no package of this module uses it, and a binary for another system
  # cannot link this machine's C library.
  TARGET_ENV=(GOOS="$TARGET_OS" GOARCH="${TARGET#*/}" CGO_ENABLED=0)
fi
DEFAULT_OUT="aggrega$OUT_SUFFIX"
DEBUG_OUT="aggrega-debug$OUT_SUFFIX"
LIGHT_OUT="aggrega-light$OUT_SUFFIX"

# stamp_ldflags — emit the -ldflags value that stamps version and commit into the
# binary and clears the build id for reproducibility. The version and commit are
# the shared recipe's values, sourced once above so a --dirty tree stamps the
# current working state.
stamp_ldflags() {
  printf -- '-X main.version=%s -X main.commit=%s -buildid=' "$_ver" "$_sha"
}

# go_build <output> <more linker flags> [go build flags…] — the one final go build
# every binary this script writes runs: trimmed paths and the version stamp, then
# <more linker flags> when it is not empty (the -w that leaves the debug
# information out of an ./aggrega the linker writes), and for a --target build
# the target's environment. An empty <more
# linker flags> leaves the linker flags exactly the stamp's. The guards keep an
# empty TARGET_ENV from expanding under set -u in older bash.
go_build() {
  local out="$1" more="$2" ldflags
  shift 2
  ldflags="$(stamp_ldflags)"
  [ -z "$more" ] || ldflags="$ldflags $more"
  env ${TARGET_ENV[@]+"${TARGET_ENV[@]}"} go build "$@" -trimpath -ldflags "$ldflags" -o "$out" "$BINPATH"
}

# stamped_version <binary> — print the version the linker stamped into
# <binary>'s main.version, read from the binary itself rather than by running it,
# so it reads a binary built for another system. The reading lives in one place,
# the build tool internal/buildtools/stampedversion, for ELF, PE and Mach-O alike;
# it fails, saying why on standard error, when the binary carries no stamp. The
# tool itself runs here, so a GOOS or GOARCH in the caller's environment is not
# handed to it.
stamped_version() {
  env -u GOOS -u GOARCH go run ./internal/buildtools/stampedversion -binary "$1"
}

# check_stamp <binary> <expected> <what> — refuse unless <binary>'s stamped
# version is <expected>; <what> names the comparison in the refusal.
check_stamp() {
  local bin="$1" expected="$2" what="$3" got
  if ! got="$(stamped_version "$bin")" || [ -z "$got" ]; then
    fail "could not read the stamped version of ./$bin from its symbol table and data"
    exit 1
  fi
  if [ "$got" != "$expected" ]; then
    fail "./$bin stamps version '$got', but $what is '$expected'"
    exit 1
  fi
  ok "./$bin stamps version $got"
}

if [ "$MODE" = "light" ]; then
  # The light binary embeds no Wasm plugin artifacts (-tags light gates the
  # embed out) and resolves every provider through the plugin cascade, so it
  # needs no guest regeneration and no overlay check.
  step "go build (light, no embed): $LIGHT_OUT${TARGET:+ for $TARGET}"
  go_build "$LIGHT_OUT" "" -tags light
  ok "built ./$LIGHT_OUT"
  if [ -n "$EXPECT_VERSION" ]; then
    check_stamp "$LIGHT_OUT" "$EXPECT_VERSION" "--expect-version"
  fi
  printf '%b\n' "${GREEN}${BOLD}build complete${RESET}"
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

  # The embedded binary embeds a complete Plugin entity for every core guest so a
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
  # embedded binary here nor determinism; it exists so the aggrega-light dev binary
  # can resolve core providers fully offline when a developer points catalog_dirs
  # at it. Regenerated wholesale from the just-refreshed embed assets.
  step "regenerate co-located plugin catalog (dev, gitignored)"
  bash scripts/gen-colocated-plugin-catalog.sh
  ok "co-located plugin catalog regenerated"
else
  note "aggrega-hub sibling absent — overlay-consistency check and embedded plugin-entity generation skipped"
fi

step "go build (embedded) with its debug information: $DEBUG_OUT${TARGET:+ for $TARGET}"
go_build "$DEBUG_OUT" ""
ok "built ./$DEBUG_OUT"
# With --expect-version the debug binary is held to it here, and that is its
# stamp; otherwise its stamp is read here, for ./aggrega to be held to.
debug_version="$EXPECT_VERSION"
if [ -n "$debug_version" ]; then
  check_stamp "$DEBUG_OUT" "$debug_version" "--expect-version"
elif ! debug_version="$(stamped_version "$DEBUG_OUT")" || [ -z "$debug_version" ]; then
  fail "could not read the stamped version of ./$DEBUG_OUT from its symbol table and data"
  exit 1
fi

# ./aggrega is ./aggrega-debug without its debug information, made the way
# DEFAULT_MADE_BY names; why each target takes its way is written where
# DEFAULT_MADE_BY is set. Built for another system it cannot be run to check it,
# so its stamped version is read the same way as ./aggrega-debug's and held to
# that: one that lost or changed the stamp is refused.
case "$DEFAULT_MADE_BY" in
  linker)
    step "go build (embedded) without its debug information: $DEFAULT_OUT for $TARGET"
    # Written under its own name, not renamed into place: the linker names an
    # Apple silicon binary's ad-hoc signature after the file it writes.
    go_build "$DEFAULT_OUT" "-w"
    check_stamp "$DEFAULT_OUT" "$debug_version" "./$DEBUG_OUT's"
    ;;
  strip)
    step "copy ./$DEBUG_OUT without its debug information: $DEFAULT_OUT"
    # Written beside it and then renamed over it, so a program that runs
    # ./aggrega meanwhile finds the previous binary or this one, never a part.
    partial="$DEFAULT_OUT.partial"
    trap 'rm -f -- "$partial"' EXIT
    strip_debug_copy "$DEBUG_OUT" "$partial" || { fail "could not write ./$DEFAULT_OUT"; exit 1; }
    check_stamp "$partial" "$debug_version" "./$DEBUG_OUT's"
    mv -f -- "$partial" "$DEFAULT_OUT"
    ;;
esac
ok "wrote ./$DEFAULT_OUT ($(wc -c < "$DEFAULT_OUT" | tr -d '[:space:]') bytes; ./$DEBUG_OUT is $(wc -c < "$DEBUG_OUT" | tr -d '[:space:]') bytes)"

printf '%b\n' "${GREEN}${BOLD}build complete${RESET}"
