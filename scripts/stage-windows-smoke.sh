#!/usr/bin/env bash
# aggrega-cli/scripts/stage-windows-smoke.sh — stage, on the machine that built the
# release, everything the Windows smoke run needs, in one directory.
#
# The Windows smoke run (scripts/windows-smoke.sh) runs on a Windows machine that
# has none of what building needs: no sibling repositories, no toolchains, no
# credentials, no registry. So the machine that built the release hands it, in the
# directory this script writes:
#
#   bin/        the three Windows binaries the build wrote: aggrega-windows-amd64.exe,
#               aggrega-light-windows-amd64.exe and aggrega-stripped-windows-amd64.exe
#   tests/      one Windows test executable per package listed below, cross-compiled
#               here, and tests.txt, which names for each executable its package
#               and the tests it runs there
#   example/    the worked example: input/, its files plus the lockfile an offline
#               pull writes; reference/, that workspace after this machine's
#               validate, pull and build — the bytes the Windows run must reproduce
#   smoke.env   the version every binary must report, the commit the example and
#               the tests come from, and the example's Release
#
# The worked example is a test fixture of this repository whose catalog sits beside
# it and whose providers the full binary embeds, so it builds with no registry at
# all: every run of it here is offline and reads no configuration of this machine.
# The reference is built twice, in two directories of different depth, and the two
# trees compared; a difference refuses, because a reference that does not reproduce
# on this machine would make any comparison against it meaningless.
#
# Run it from the release build's checkout after the Windows binaries are built:
# it needs ./aggrega (the full binary for this machine), the three Windows binaries
# beside it, the embedded guests they embed (the test executables embed the same
# guests), and a Go toolchain. The Windows run holds every binary to the version
# ./aggrega reports, so all four must come from one build of this tree, as the
# release build's --expect-version already holds them. Like scripts/build.sh it builds with GOWORK=off, the
# go.mod replace directives resolving the sibling repositories beside this one.
#
# Usage:
#   scripts/stage-windows-smoke.sh --output-dir <dir>
#   scripts/stage-windows-smoke.sh --help
#
# <dir> must not exist or be empty. An argument it cannot use is refused with exit
# 2, before anything is staged. Exit non-zero on the first failing step.
set -euo pipefail

CLI_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$CLI_ROOT"
export GOWORK=off

. scripts/lib/smoke-example.sh

# The worked example: a fixture directory of this repository and the Release in it
# that is built. Its catalog is its own catalog/ directory and its one provider is
# the embedded helm guest; it carries no executable file, whose execute bit a
# Windows checkout could not reproduce.
EXAMPLE_FIXTURE="internal/compiler/testdata/fullstack-app"
EXAMPLE_RELEASE="release.yaml"

# The tests the Windows run executes, as <package> <test>. Each proves a platform
# split behaves on Windows, and each runs there with -test.run naming it; a named
# test the Windows run sees skipped, or not run at all, fails the run.
TESTS=(
  # Cross-process locking of the shared compile cache: one process waits for, or
  # is refused by, a lock another process holds, and never both write.
  "internal/wasm TestCompileLockMakesASecondProcessWait"
  "internal/wasm TestCompileCacheReclaimLockRefusesASecondProcess"
  "internal/wasm TestCompileCacheReclaimLockIsExclusiveAndNonBlocking"
  # The file lock every cache lock is built on.
  "internal/wasm/filelock TestAcquireSerializesHolders"
  "internal/wasm/filelock TestAcquireDegradesWhenLockPathUnusable"
  "internal/wasm/filelock TestAcquireReleasesForReuse"
  "internal/wasm/filelock TestLockAndTryLockCreateNoDirectory"
  "internal/wasm/filelock TestTryLockRefusesWhileHeld"
  "internal/wasm/filelock TestLockSharedAdmitsReadersTogetherAndExcludesAWriter"
  # A file another process holds open: removal is refused on Windows, and the
  # cache verbs leave such a file in place and say so rather than failing.
  "internal/wasm/filelock TestHeldOpenNamesTheRefusalToRemoveAnOpenFile"
  "internal/wasm TestReclaimCompileCacheLeavesAnEntryAnotherProcessHasOpen"
  "internal/wasm/attribindex TestLoadWaitsForAWriterHoldingTheIndexLock"
  "internal/wasm/attribindex TestAWriterReplacesTheIndexOnceItsReadersHaveClosedIt"
  "internal/wasm/provindex TestLoadWaitsForAWriterHoldingTheIndexLock"
  "internal/wasm/provindex TestAWriterReplacesTheIndexOnceItsReadersHaveClosedIt"
  "internal/cacheops TestRemoveAllLeavingHeldOpenLeavesOnlyTheOpenFile"
  "internal/cacheops TestClearLeavesWhatACompileHoldsAndClearsEverythingElse"
  "cmd/aggrega TestCachePruneLeavesWhatAnotherProcessHasOpen"
  "cmd/aggrega TestCacheEvictLeavesALeafAnotherProcessHasOpen"
  "cmd/aggrega TestCacheClearLeavesTheLockACompileHolds"
  # Paths the compiler writes or answers with are spelled with forward slashes.
  "internal/compiler/renderer TestComputeFinalPathIsSlashSeparated"
  "internal/compiler/trace TestMemoryWriter_CollectsEntriesUnderRelativePaths"
  "pkg/catalog TestRelativeToWorkspaceSpellsRelativePathsWithSlashes"
  "internal/serve TestReleasePathCompletionsAreSlashSeparated"
  # A Unix-socket listen address: served, removed on close, and an address in
  # neither form refused.
  "internal/mcp TestListenerServesPlaintextOnAUnixSocket"
  "internal/mcp TestAUnixSocketIsRemovedWhenTheListenerCloses"
  "internal/mcp TestListenerRefusesAnAddressInNeitherForm"
)

WINDOWS_BINARIES=(aggrega-windows-amd64.exe aggrega-light-windows-amd64.exe aggrega-stripped-windows-amd64.exe)

fail() { printf 'stage-windows-smoke: %s\n' "$*" >&2; }

OUT=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output-dir)
      [ -n "${2:-}" ] || { fail "--output-dir needs a directory"; exit 2; }
      OUT="$2"
      shift 2
      ;;
    -h|--help)
      sed -n '2,/^# 2, before anything is staged/p' "$CLI_ROOT/scripts/$(basename "$0")" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      fail "unknown argument: $1"
      exit 2
      ;;
  esac
done
[ -n "$OUT" ] || { fail "--output-dir <dir> is required"; exit 2; }
if [ -e "$OUT" ] && [ -n "$(ls -A "$OUT")" ]; then
  fail "$OUT is not empty; stage into a new or empty directory"
  exit 2
fi
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"

# What this stages from must already be built.
[ -x ./aggrega ] || { fail "./aggrega is not built; run scripts/build.sh first"; exit 1; }
for exe in "${WINDOWS_BINARIES[@]}"; do
  [ -f "$exe" ] || { fail "./$exe is not built; run scripts/build.sh --target windows/amd64 (--stripped, and --light) first"; exit 1; }
done
if ! bash scripts/check-embedded-guests.sh >/dev/null 2>&1; then
  fail "the embedded guests are stale or missing; run scripts/build.sh first, so the test executables embed what the binaries embed"
  exit 1
fi

VERSION_JSON="$(./aggrega version -o json)"
if [[ "$VERSION_JSON" =~ \"version\":\ *\"([^\"]+)\" ]]; then
  VERSION="${BASH_REMATCH[1]}"
else
  fail "./aggrega version -o json reported no version"
  exit 1
fi
COMMIT="$(git rev-parse HEAD)"

echo "==> the Windows binaries"
mkdir -p "$OUT/bin"
for exe in "${WINDOWS_BINARIES[@]}"; do
  cp "$exe" "$OUT/bin/$exe"
  echo "  bin/$exe"
done

echo "==> the Windows test executables"
mkdir -p "$OUT/tests"
: > "$OUT/tests/tests.txt"
built=" "
for entry in "${TESTS[@]}"; do
  read -r pkg name <<<"$entry"
  exe="${pkg//\//-}.test.exe"
  if [[ "$built" != *" $exe "* ]]; then
    # A compile, not a test run: nothing executes here, and nothing could, since
    # the executable is for Windows. -s -w leaves out the symbol table and the
    # debug information, which no test reads, and keeps what is handed over
    # smaller; a failing test still names its functions and lines.
    env GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -ldflags='-s -w' -o "$OUT/tests/$exe" "./$pkg"
    built="$built$exe "
    echo "  tests/$exe  <- ./$pkg"
  fi
  printf '%s %s %s\n' "$exe" "$pkg" "$name" >> "$OUT/tests/tests.txt"
done

echo "==> the worked example: $EXAMPLE_FIXTURE, $EXAMPLE_RELEASE"
smoke_isolate "$OUT/.smoke-config"
mkdir -p "$OUT/example/input"
cp -R "$EXAMPLE_FIXTURE/." "$OUT/example/input/"
# The input carries the lockfile an offline pull writes, so the runs that follow,
# here and on Windows, start from one lockfile and re-resolve onto it.
( cd "$OUT/example/input" &&
  "$CLI_ROOT/aggrega" --no-color --plain --workspace-root "$OUT/example/input" pull --release "$EXAMPLE_RELEASE" )

mkdir -p "$OUT/example/reference" "$OUT/.smoke-recheck/a/b/c/reference"
cp -R "$OUT/example/input/." "$OUT/example/reference/"
cp -R "$OUT/example/input/." "$OUT/.smoke-recheck/a/b/c/reference/"
smoke_build_example "$CLI_ROOT/aggrega" "$OUT/example/reference" "$EXAMPLE_RELEASE"
smoke_build_example "$CLI_ROOT/aggrega" "$OUT/.smoke-recheck/a/b/c/reference" "$EXAMPLE_RELEASE"
if ! smoke_compare_trees "$OUT/example/reference" "$OUT/.smoke-recheck/a/b/c/reference"; then
  fail "the example does not build to the same bytes in two directories on this machine; a Windows comparison against it would mean nothing"
  exit 1
fi
rm -rf "$OUT/.smoke-recheck" "$OUT/.smoke-config"

{
  printf 'version=%s\n' "$VERSION"
  printf 'commit=%s\n' "$COMMIT"
  printf 'release=%s\n' "$EXAMPLE_RELEASE"
} > "$OUT/smoke.env"

echo "==> staged into $OUT: version $VERSION, commit $COMMIT"
