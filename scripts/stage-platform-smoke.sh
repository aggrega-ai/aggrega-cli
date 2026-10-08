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
#               aggrega-darwin-<arch> and aggrega-light-darwin-<arch>. The
#               Windows ones are unsigned: the smoke run runs the signed copies
#               the release attaches, handed to it apart (its --signed), and
#               holds these as what its signature check must refuse
#   tests/      one test executable for the target per package listed below,
#               cross-compiled here, and tests.txt, which names for each executable
#               its package and the tests it runs there
#   example/    the worked example: input/, its files plus the lockfile an offline
#               pull writes; reference/, that workspace after this machine's
#               validate, pull and build — the bytes the target machine must
#               reproduce
#   chart/      the second worked example, whose workload is a Helm chart its
#               Recipe carries: input/ and reference/, as for example/
#   tools/      registry (registry.exe for windows/amd64), go-containerregistry's
#               in-memory registry server built for the target from the module
#               this repository's go.mod already requires, which the smoke run
#               starts on the target machine's loopback address
#   registry/   publish/<name>/<name>.yaml, one FeatureRecipe of the example in a
#               directory of its own, which the smoke run pushes to that registry;
#               and consumer/, a copy of the example whose catalog lacks that
#               entity and whose Manifest names it by provider, so its pull fetches
#               the entity from the registry and writes it into its entity cache
#   smoke.env   the target, the version every binary must report, the commit the
#               examples and the tests come from, the example's Release, the
#               chart-backed example's Release (chart_release), the staged
#               entity's path (registry_entity) and the reference its pull
#               resolves (registry_reference)
#
# The two worked examples are of two shapes. The first is a test fixture of this
# repository whose helm workload the provider synthesizes from what its Recipes
# declare. The second, scripts/testdata/platform-smoke-chart, renders a Helm chart
# its one Recipe carries, and is laid out as a catalog of one's own must be: the
# Recipe in a directory of its own, its chart beside it, and the entity.lock that
# `aggrega pin` writes, which its pull verifies every chart file against. Each has
# its catalog beside it and its provider embedded in the full binary, so it builds
# with no registry at all: every run of either here is offline and reads no
# configuration of this machine. Each reference is built twice, in two directories
# of different depth, and the two trees compared; a difference refuses, because a
# reference that does not reproduce on this machine would make any comparison
# against it meaningless.
#
# Run it from the release build's checkout after the target's binaries are built:
# it needs ./aggrega (the full binary for this machine), the target's binaries
# beside it, the embedded guests they embed (the test executables embed the same
# guests), and a Go toolchain with the modules go.mod requires. The smoke run
# holds every binary to the version ./aggrega reports, so all of them must come
# from one build of this tree, as the
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
# The Manifest that Release names.
EXAMPLE_MANIFEST="manifest.yaml"

# The chart-backed example and its Release. Its entity.lock is committed beside its
# Recipe, so the pull below verifies the chart against it; a chart file changed
# without running `aggrega pin` on the Recipe again refuses the staging, naming
# that command.
CHART_EXAMPLE="scripts/testdata/platform-smoke-chart"
CHART_RELEASE="release.yaml"

# The entity the smoke run serves from a registry of its own: a FeatureRecipe the
# example's Release builds. In the consumer copy the Manifest's mapping to it is
# rewritten to name its provider, since a reference with no provider is looked for
# only in a catalog directory; the reference a pull then resolves carries a colon,
# which the name of its cache slot must not.
REGISTRY_ENTITY="catalog/base/feature-recipes/k8s-dns-patcher.yaml"
REGISTRY_MAPPING_FROM=": k8s-dns-patcher:1.0.0"
REGISTRY_MAPPING_TO=": helm/k8s-dns-patcher:1.0.0"
REGISTRY_REFERENCE="helm/k8s-dns-patcher:1.0.0"
# The registry program: built from a module go.mod already requires, at the version
# it pins, rather than fetched as a binary from anywhere.
REGISTRY_PROGRAM="github.com/google/go-containerregistry/cmd/registry"

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

# stage_example <fixture> <release> <name> — stage one worked example into
# $OUT/<name>: input/, a copy of <fixture> carrying the lockfile an offline pull of
# <release> writes, so the runs that follow, here and on the target machine, start
# from one lockfile and re-resolve onto it; and reference/, built from input/ and
# checked against a second build of it in a directory of different depth.
stage_example() {
  local fixture="$1" release="$2" name="$3"
  local recheck="$OUT/.smoke-recheck/$name/a/b/c/reference"
  echo "==> the worked example $name/: $fixture, $release"
  mkdir -p "$OUT/$name/input"
  cp -R "$fixture/." "$OUT/$name/input/"
  ( cd "$OUT/$name/input" &&
    "$CLI_ROOT/aggrega" --no-color --plain --workspace-root "$OUT/$name/input" pull --release "$release" )

  mkdir -p "$OUT/$name/reference" "$recheck"
  cp -R "$OUT/$name/input/." "$OUT/$name/reference/"
  cp -R "$OUT/$name/input/." "$recheck/"
  smoke_build_example "$CLI_ROOT/aggrega" "$OUT/$name/reference" "$release" "$OUT/.smoke-logs/$name/reference"
  smoke_build_example "$CLI_ROOT/aggrega" "$recheck" "$release" "$OUT/.smoke-logs/$name/recheck"
  if ! smoke_compare_trees "$OUT/$name/reference" "$recheck"; then
    fail "the $name example does not build to the same bytes in two directories on this machine; a comparison on $TARGET against it would mean nothing"
    exit 1
  fi
}

smoke_isolate "$OUT/.smoke-config"
stage_example "$EXAMPLE_FIXTURE" "$EXAMPLE_RELEASE" example
stage_example "$CHART_EXAMPLE" "$CHART_RELEASE" chart
rm -rf "$OUT/.smoke-recheck" "$OUT/.smoke-config" "$OUT/.smoke-logs"

echo "==> the registry the smoke run starts: $REGISTRY_PROGRAM"
mkdir -p "$OUT/tools"
env GOOS="$GOOS_T" GOARCH="$GOARCH_T" CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' \
  -o "$OUT/tools/registry$EXE_SUFFIX" "$REGISTRY_PROGRAM"
echo "  tools/registry$EXE_SUFFIX"

echo "==> the entity served from it, and the copy of the example that fetches it: $REGISTRY_ENTITY"
entity_name="$(basename "$REGISTRY_ENTITY" .yaml)"
mkdir -p "$OUT/registry/publish/$entity_name" "$OUT/registry/consumer"
cp "$EXAMPLE_FIXTURE/$REGISTRY_ENTITY" "$OUT/registry/publish/$entity_name/$entity_name.yaml"
cp -R "$EXAMPLE_FIXTURE/." "$OUT/registry/consumer/"
rm -f "$OUT/registry/consumer/$REGISTRY_ENTITY" "$OUT/registry/consumer/${EXAMPLE_RELEASE%.yaml}.lock"
manifest="$OUT/registry/consumer/$EXAMPLE_MANIFEST"
content="$(< "$manifest")"
rest="${content//"$REGISTRY_MAPPING_FROM"/}"
if [ $(( (${#content} - ${#rest}) / ${#REGISTRY_MAPPING_FROM} )) != 1 ]; then
  fail "$EXAMPLE_FIXTURE/$EXAMPLE_MANIFEST does not map to the entity as \"$REGISTRY_MAPPING_FROM\" exactly once"
  exit 1
fi
printf '%s\n' "${content//"$REGISTRY_MAPPING_FROM"/"$REGISTRY_MAPPING_TO"}" > "$manifest"
echo "  registry/publish/$entity_name/$entity_name.yaml"
echo "  registry/consumer/, its $EXAMPLE_MANIFEST mapping to $REGISTRY_REFERENCE"

{
  printf 'target=%s\n' "$TARGET"
  printf 'version=%s\n' "$VERSION"
  printf 'commit=%s\n' "$COMMIT"
  printf 'release=%s\n' "$EXAMPLE_RELEASE"
  printf 'chart_release=%s\n' "$CHART_RELEASE"
  printf 'registry_entity=%s\n' "registry/publish/$entity_name/$entity_name.yaml"
  printf 'registry_reference=%s\n' "$REGISTRY_REFERENCE"
} > "$OUT/smoke.env"

echo "==> staged $TARGET into $OUT: version $VERSION, commit $COMMIT"
