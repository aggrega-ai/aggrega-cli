#!/usr/bin/env bash
# aggrega-cli/scripts/stage-platform-smoke.sh — stage, on the machine that built
# the release, everything one platform's smoke run needs, in one directory.
#
# The smoke run (scripts/platform-smoke.sh) runs on a machine of the target
# platform, a Windows or a macOS machine, that has none of what building needs: no
# sibling repositories, no toolchains, no credentials, no registry. So the
# machine that built the release hands it, in the directory this script writes:
#
#   bin/        the binaries the build wrote for the target, as
#               scripts/lib/platform-smoke-common.sh lists them: for windows/amd64
#               aggrega-windows-amd64.exe, aggrega-light-windows-amd64.exe and
#               aggrega-stripped-windows-amd64.exe; for darwin/<arch>
#               aggrega-darwin-<arch> and aggrega-light-darwin-<arch>
#   tests/      one test executable for the target per package listed below,
#               cross-compiled here, and tests.txt, which names for each executable
#               its package and the tests it runs there
#   example/    the worked example: input/, its files plus the lockfile an offline
#               pull writes; reference/, that workspace after this machine's
#               validate, pull and build — the bytes the target machine must
#               reproduce
#   smoke.env   the target, the version every binary must report, the commit the
#               example and the tests come from, and the example's Release
#
# The worked example is a test fixture of this repository whose catalog sits beside
# it and whose providers the full binary embeds, so it builds with no registry at
# all: every run of it here is offline and reads no configuration of this machine.
# The reference is built twice, in two directories of different depth, and the two
# trees compared; a difference refuses, because a reference that does not reproduce
# on this machine would make any comparison against it meaningless.
#
# Run it from the release build's checkout after the target's binaries are built:
# it needs ./aggrega (the full binary for this machine), the target's binaries
# beside it, the embedded guests they embed (the test executables embed the same
# guests), and a Go toolchain. The smoke run holds every binary to the version
# ./aggrega reports, so all of them must come from one build of this tree, as the
# release build's --expect-version already holds them. Like scripts/build.sh it
# builds with GOWORK=off, the go.mod replace directives resolving the sibling
# repositories beside this one.
#
# Usage:
#   scripts/stage-platform-smoke.sh --target <goos>/<goarch> --output-dir <dir>
#   scripts/stage-platform-smoke.sh --help
#
# <goos>/<goarch> is windows/amd64, darwin/arm64 or darwin/amd64. <dir> must not
# exist or be empty. An argument it cannot use is refused with exit 2, before
# anything is staged. Exit non-zero on the first failing step.
set -euo pipefail

CLI_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$CLI_ROOT"
export GOWORK=off

. scripts/lib/platform-smoke-common.sh
smoke_require_bash || exit 1

# The worked example: a fixture directory of this repository and the Release in it
# that is built. Its catalog is its own catalog/ directory and its one provider is
# the embedded helm guest; it carries no executable file, whose execute bit a
# Windows checkout could not reproduce.
EXAMPLE_FIXTURE="internal/compiler/testdata/fullstack-app"
EXAMPLE_RELEASE="release.yaml"

# The tests the smoke run executes, as <systems> <package> <test>, where <systems>
# names, comma-separated, the operating systems the test is staged for. Each
# proves a platform split behaves there, and each runs with -test.run naming it; a
# named test the run sees skipped, or not run at all, fails the run. A test
# declared in a file built only for Windows is listed for windows alone, since a
# macOS build does not contain it; the staging names each test it leaves out.
TESTS=(
  # Cross-process locking of the shared compile cache: one process waits for, or
  # is refused by, a lock another process holds, and never both write.
  "windows,darwin internal/wasm TestCompileLockMakesASecondProcessWait"
  "windows,darwin internal/wasm TestCompileCacheReclaimLockRefusesASecondProcess"
  "windows,darwin internal/wasm TestCompileCacheReclaimLockIsExclusiveAndNonBlocking"
  # The file lock every cache lock is built on. The last three are the Windows
  # lock's own tests, declared in a Windows-only file.
  "windows,darwin internal/wasm/filelock TestAcquireSerializesHolders"
  "windows,darwin internal/wasm/filelock TestAcquireDegradesWhenLockPathUnusable"
  "windows,darwin internal/wasm/filelock TestAcquireReleasesForReuse"
  "windows internal/wasm/filelock TestLockAndTryLockCreateNoDirectory"
  "windows internal/wasm/filelock TestTryLockRefusesWhileHeld"
  "windows internal/wasm/filelock TestLockSharedAdmitsReadersTogetherAndExcludesAWriter"
  # A file another process holds open: removal is refused on Windows, and the
  # cache verbs leave such a file in place and say so rather than failing; on
  # macOS removal succeeds, and the same tests hold the verbs to that. The index
  # tests are the Windows read lock's own, declared in Windows-only files.
  "windows,darwin internal/wasm/filelock TestHeldOpenNamesTheRefusalToRemoveAnOpenFile"
  "windows,darwin internal/wasm TestReclaimCompileCacheLeavesAnEntryAnotherProcessHasOpen"
  "windows internal/wasm/attribindex TestLoadWaitsForAWriterHoldingTheIndexLock"
  "windows internal/wasm/attribindex TestAWriterReplacesTheIndexOnceItsReadersHaveClosedIt"
  "windows internal/wasm/provindex TestLoadWaitsForAWriterHoldingTheIndexLock"
  "windows internal/wasm/provindex TestAWriterReplacesTheIndexOnceItsReadersHaveClosedIt"
  "windows,darwin internal/cacheops TestRemoveAllLeavingHeldOpenLeavesOnlyTheOpenFile"
  "windows,darwin internal/cacheops TestClearLeavesWhatACompileHoldsAndClearsEverythingElse"
  "windows,darwin cmd/aggrega TestCachePruneLeavesWhatAnotherProcessHasOpen"
  "windows,darwin cmd/aggrega TestCacheEvictLeavesALeafAnotherProcessHasOpen"
  "windows,darwin cmd/aggrega TestCacheClearLeavesTheLockACompileHolds"
  # Paths the compiler writes or answers with are spelled with forward slashes.
  "windows,darwin internal/compiler/renderer TestComputeFinalPathIsSlashSeparated"
  "windows,darwin internal/compiler/trace TestMemoryWriter_CollectsEntriesUnderRelativePaths"
  "windows,darwin pkg/catalog TestRelativeToWorkspaceSpellsRelativePathsWithSlashes"
  "windows,darwin internal/serve TestReleasePathCompletionsAreSlashSeparated"
  # A Unix-socket listen address: served, removed on close, and an address in
  # neither form refused.
  "windows,darwin internal/mcp TestListenerServesPlaintextOnAUnixSocket"
  "windows,darwin internal/mcp TestAUnixSocketIsRemovedWhenTheListenerCloses"
  "windows,darwin internal/mcp TestListenerRefusesAnAddressInNeitherForm"
)

fail() { printf 'stage-platform-smoke: %s\n' "$*" >&2; }

TARGET="" OUT=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --target)
      [ -n "${2:-}" ] || { fail "--target needs <goos>/<goarch>"; exit 2; }
      TARGET="$2"
      shift 2
      ;;
    --output-dir)
      [ -n "${2:-}" ] || { fail "--output-dir needs a directory"; exit 2; }
      OUT="$2"
      shift 2
      ;;
    -h|--help)
      sed -n '2,/^# anything is staged/p' "$CLI_ROOT/scripts/$(basename "$0")" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      fail "unknown argument: $1"
      exit 2
      ;;
  esac
done
[ -n "$TARGET" ] || { fail "--target <goos>/<goarch> is required"; exit 2; }
[ -n "$OUT" ] || { fail "--output-dir <dir> is required"; exit 2; }
names="$(smoke_target_binaries "$TARGET")" || exit 2
mapfile -t BINARIES <<<"$names"
GOOS_T="${TARGET%/*}" GOARCH_T="${TARGET#*/}"
EXE_SUFFIX=""
[ "$GOOS_T" = windows ] && EXE_SUFFIX=".exe"
if [ -e "$OUT" ] && [ -n "$(ls -A "$OUT")" ]; then
  fail "$OUT is not empty; stage into a new or empty directory"
  exit 2
fi
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"

# What this stages from must already be built.
[ -x ./aggrega ] || { fail "./aggrega is not built; run scripts/build.sh first"; exit 1; }
for bin in "${BINARIES[@]}"; do
  [ -f "$bin" ] || { fail "./$bin is not built; run scripts/build.sh --target $TARGET (and --light) first"; exit 1; }
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

echo "==> the $TARGET binaries"
mkdir -p "$OUT/bin"
for bin in "${BINARIES[@]}"; do
  cp "$bin" "$OUT/bin/$bin"
  echo "  bin/$bin"
done

echo "==> the $TARGET test executables"
mkdir -p "$OUT/tests"
: > "$OUT/tests/tests.txt"
built=" "
for entry in "${TESTS[@]}"; do
  read -r systems pkg name <<<"$entry"
  if [[ ",$systems," != *",$GOOS_T,"* ]]; then
    echo "  not staged for $TARGET: $pkg $name (listed for $systems)"
    continue
  fi
  exe="${pkg//\//-}.test$EXE_SUFFIX"
  if [[ "$built" != *" $exe "* ]]; then
    # A compile, not a test run: nothing executes here, and nothing could, since
    # the executable is for another system. -s -w leaves out the symbol table and
    # the debug information, which no test reads, and keeps what is handed over
    # smaller; a failing test still names its functions and lines. For
    # darwin/arm64 the linker still signs the executable ad hoc, which a Mac with
    # Apple silicon requires before it runs one.
    env GOOS="$GOOS_T" GOARCH="$GOARCH_T" CGO_ENABLED=0 go test -c -ldflags='-s -w' -o "$OUT/tests/$exe" "./$pkg"
    built="$built$exe "
    echo "  tests/$exe  <- ./$pkg"
  fi
  printf '%s %s %s\n' "$exe" "$pkg" "$name" >> "$OUT/tests/tests.txt"
done
[ -s "$OUT/tests/tests.txt" ] || { fail "no listed test is staged for $TARGET"; exit 1; }

echo "==> the worked example: $EXAMPLE_FIXTURE, $EXAMPLE_RELEASE"
smoke_isolate "$OUT/.smoke-config"
mkdir -p "$OUT/example/input"
cp -R "$EXAMPLE_FIXTURE/." "$OUT/example/input/"
# The input carries the lockfile an offline pull writes, so the runs that follow,
# here and on the target machine, start from one lockfile and re-resolve onto it.
( cd "$OUT/example/input" &&
  "$CLI_ROOT/aggrega" --no-color --plain --workspace-root "$OUT/example/input" pull --release "$EXAMPLE_RELEASE" )

mkdir -p "$OUT/example/reference" "$OUT/.smoke-recheck/a/b/c/reference"
cp -R "$OUT/example/input/." "$OUT/example/reference/"
cp -R "$OUT/example/input/." "$OUT/.smoke-recheck/a/b/c/reference/"
smoke_build_example "$CLI_ROOT/aggrega" "$OUT/example/reference" "$EXAMPLE_RELEASE"
smoke_build_example "$CLI_ROOT/aggrega" "$OUT/.smoke-recheck/a/b/c/reference" "$EXAMPLE_RELEASE"
if ! smoke_compare_trees "$OUT/example/reference" "$OUT/.smoke-recheck/a/b/c/reference"; then
  fail "the example does not build to the same bytes in two directories on this machine; a comparison on $TARGET against it would mean nothing"
  exit 1
fi
rm -rf "$OUT/.smoke-recheck" "$OUT/.smoke-config"

{
  printf 'target=%s\n' "$TARGET"
  printf 'version=%s\n' "$VERSION"
  printf 'commit=%s\n' "$COMMIT"
  printf 'release=%s\n' "$EXAMPLE_RELEASE"
} > "$OUT/smoke.env"

echo "==> staged $TARGET into $OUT: version $VERSION, commit $COMMIT"
