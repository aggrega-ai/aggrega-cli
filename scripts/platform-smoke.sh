#!/usr/bin/env bash
# aggrega-cli/scripts/platform-smoke.sh — run one platform's binaries and tests on
# a machine of that platform, and hold what the full binary builds to the bytes
# the release's own build machine built.
#
# It reads the directory scripts/stage-platform-smoke.sh wrote for the target
# (--inputs) and runs from a checkout of this repository at the commit that
# directory names: under the Git for Windows shell on Windows, under bash on macOS.
# For windows/amd64 it also reads the directory the release's signing wrote
# (--signed): the Windows binaries with their Authenticode signatures, and
# SHA256SUMS, the SHA-256 list of them. The signed binaries are the ones every
# step runs, because they are the ones the release attaches; the unsigned copies
# the build wrote, staged in bin/, run in no step, and step 11 holds them as what
# its checks must refuse. The macOS binaries are not signed by the release, so a
# darwin target runs the staged ones and takes no --signed.
# It needs no Go toolchain, no sibling repositories, no credentials and no
# registry but the one it starts on this machine; it does need bash 4.3 or later,
# which macOS does not ship. Before the checks it holds the inputs and the machine
# to the target:
#
#   - the staged inputs name the target given with --target, and bin/ holds
#     exactly the binaries scripts/lib/platform-smoke-common.sh lists for it: the
#     full, light and stripped binaries, for windows/amd64, darwin/arm64 and
#     darwin/amd64 alike. For windows/amd64 the --signed directory
#     holds exactly those binaries too, and SHA256SUMS beside them
#   - on macOS, the machine is the target's: uname -m reports arm64 for
#     darwin/arm64 and x86_64 for darwin/amd64. A Mac with Apple silicon also runs
#     an amd64 binary, through Rosetta 2, and that binary still reports
#     darwin/amd64, so without this check an Apple silicon machine would pass for
#     an Intel one
#   - on macOS, the binaries, the registry program and the test executables are
#     made executable again, because a workflow artifact does not keep file modes,
#     and none of them may carry the quarantine mark, under which Gatekeeper would
#     assess a program that is not notarized before it runs
#
# Then, in order, stopping at the first failure with what failed:
#
#   1. versions   each binary reports, as `version` and as `version -o json`, the
#                 version staged with it, and the JSON names the target as its
#                 platform
#   2. handshake  the full binary, run as `aggrega mcp`, answers an initialize
#                 request on standard input with a result on standard output
#   3. example    the full binary validates, pulls and builds the staged worked
#                 example in a fresh directory, the pull and the build with
#                 --progress, whose plain progress lines must be printed; the whole
#                 workspace it leaves — the rendered tree and the lockfile with it —
#                 is compared byte for byte, every file and the set of names, with
#                 the reference the build machine built; then the same comparison
#                 runs against a copy of the reference with one byte changed, and
#                 must report that difference, so the comparison shown passing is
#                 one that can fail
#   4. path       steps 5 to 8 work in a directory whose path holds a space and a
#                 letter outside ASCII, as a user's own directories often do; the
#                 same check over a plain path must refuse it
#   5. configuration
#                 `aggrega config init` runs with only the platform's own rule able
#                 to place the global configuration: on Windows with HOME,
#                 XDG_CONFIG_HOME and AGGREGA_CONFIG unset and APPDATA and
#                 LOCALAPPDATA naming directories of step 4's, as a Windows prompt
#                 gives them; on macOS with XDG_CONFIG_HOME, AGGREGA_CONFIG,
#                 APPDATA and LOCALAPPDATA unset and HOME naming one. It must exit 0,
#                 write the file where that rule puts it — %APPDATA%\aggrega\config.yaml
#                 on Windows, $HOME/.config/aggrega/config.yaml on macOS — report
#                 that file, the directory the machine-wide caches are kept in and
#                 the registry it registered, print nothing on standard error and
#                 no warning; the same check against a directory holding no file
#                 must refuse it
#   6. entity cache
#                 the registry program staged with the binaries serves an
#                 in-memory registry on this machine's loopback address; the full
#                 binary pushes the staged FeatureRecipe to it, then pulls a copy of
#                 the example whose catalog lacks that entity and whose Manifest
#                 names it by provider, so the pull fetches it from the registry
#                 and writes it into the workspace's entity cache, under a name
#                 derived from a reference that carries a colon; then it builds
#                 that copy offline, from the cache. The pull must report the
#                 fetch, a slot must hold the entity, every name under the cache
#                 must be one Windows accepts, and the rendered tree must be byte
#                 for byte the one step 3's reference holds; the same cache check
#                 over the reference workspace, which resolved everything from its
#                 own catalog, and the name check over the cache's names with a
#                 colon planted in one, must refuse them
#   7. catalog    a copy of the example whose configuration lists its catalog
#                 directory is pulled with --catalog naming that directory again;
#                 the pull must write the lockfile and print no notice that a
#                 catalog file is shadowed; the same check over its output with
#                 such a notice planted must refuse it
#   8. chart      the full binary validates, pulls and builds the staged
#                 chart-backed example — a Recipe whose workload is the Helm chart
#                 beside it, pinned file by file in the entity.lock beside it — in a
#                 fresh directory beneath step 4's; the whole workspace it leaves is
#                 compared byte for byte with the reference the build machine
#                 built, and the same comparison against a copy of that reference
#                 with one byte changed must report the difference
#   9. escapes    every command steps 3 to 8 ran had its standard output and its
#                 standard error redirected to files, as a run whose output a log
#                 or another program reads, and was refused had either carried an
#                 escape byte; here that check, over a copy of one such file with
#                 an escape byte planted, must refuse it
#  10. tests      each staged test executable runs, from its package directory, the
#                 tests staged for it with -test.run naming them, -test.count=1,
#                 -test.timeout 10m and -test.v; every named test must report PASS,
#                 and a named test that skips, a run that finds no test to run, or a
#                 count of passes other than the count of names fails the run
#  11. signature  windows/amd64 only. Each binary the steps above ran must have
#                 the SHA-256 that SHA256SUMS lists for it, the list naming exactly
#                 those binaries; and Windows' own Get-AuthenticodeSignature, run
#                 through Windows PowerShell on each, must report Status Valid, a
#                 signer certificate whose subject names Cognitio S.r.l. as its
#                 common name, first, and a timestamp countersignature (a
#                 TimeStamperCertificate), which keeps the signature valid after
#                 its certificate, issued for a few days only, expires. Each file's
#                 status, signer and timestamper are printed. Then the same
#                 signature check over the unsigned full binary staged in bin/,
#                 and the same list check over bin/, must refuse them
#
# Each binary started in steps 1-8 has every AGGREGA_ setting of the machine
# unset, the global configuration pointed at an empty directory (XDG_CONFIG_HOME
# and APPDATA both name it), and the offline posture on, so it reads no
# configuration of the machine and reaches no network; steps 4, 9 and 11 start
# none. Step 5 replaces that configuration directory with its own, and step 6
# turns the posture off for its push and its
# pull alone, with the proxy settings naming a closed port on the loopback
# address: a request to the registry it started, on that address, goes direct,
# and any other request fails. The tests run in the environment the shell was
# given; their own setup isolates what they read. Step 11 runs Windows PowerShell
# with -NoProfile, so no profile of the machine shapes what it reports.
#
# Usage:
#   scripts/platform-smoke.sh --target <goos>/<goarch> --inputs <dir> [--signed <dir>] [--binaries <dir>]
#   scripts/platform-smoke.sh --help
#
# --signed <dir> names the directory holding the signed Windows binaries and
# SHA256SUMS. It is required for windows/amd64 and refused for a darwin target.
#
# --binaries <dir> runs every step with the binaries in <dir>, named as bin/ names
# them, in place of the ones it would run otherwise (the --signed ones for
# windows/amd64, the staged ones for macOS): the way to show which checks the
# binaries of another release fail. Each step then runs whatever an earlier one
# reported, and the run ends naming every step that failed. It exits 1 when one
# did and 3 when none did — never 0, since what passed is binaries the release
# does not carry.
#
# <goos>/<goarch> is windows/amd64, darwin/arm64 or darwin/amd64. Writes its working
# files under <dir>/run, which must not exist yet. An argument it cannot use is
# refused with exit 2, before anything runs.
set -euo pipefail

CLI_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$CLI_ROOT"

TARGET=""
fail() { printf 'platform-smoke%s: FAILED: %s\n' "${TARGET:+ $TARGET}" "$*" >&2; }
step() { printf '\n==> %s\n' "$*"; }

. scripts/lib/platform-smoke-common.sh
smoke_require_bash || exit 1

INPUTS="" SIGNED="" SUBSTITUTE=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --target)
      [ -n "${2:-}" ] || { fail "--target needs <goos>/<goarch>"; exit 2; }
      TARGET="$2"
      shift 2
      ;;
    --inputs)
      [ -n "${2:-}" ] || { fail "--inputs needs the staged directory"; exit 2; }
      INPUTS="$2"
      shift 2
      ;;
    --signed)
      [ -n "${2:-}" ] || { fail "--signed needs the directory holding the signed binaries and SHA256SUMS"; exit 2; }
      SIGNED="$2"
      shift 2
      ;;
    --binaries)
      [ -n "${2:-}" ] || { fail "--binaries needs the directory holding the binaries to run"; exit 2; }
      SUBSTITUTE="$2"
      shift 2
      ;;
    -h|--help)
      sed -n '2,/^# refused with exit 2, before anything runs/p' "$CLI_ROOT/scripts/$(basename "$0")" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      fail "unknown argument: $1"
      exit 2
      ;;
  esac
done
[ -n "$TARGET" ] || { fail "--target <goos>/<goarch> is required"; exit 2; }
[ -n "$INPUTS" ] || { fail "--inputs <dir> is required"; exit 2; }
names="$(smoke_target_binaries "$TARGET")" || exit 2
mapfile -t BINARY_NAMES <<<"$names"
GOOS_T="${TARGET%/*}" GOARCH_T="${TARGET#*/}"
EXE_SUFFIX=""
[ "$GOOS_T" = windows ] && EXE_SUFFIX=".exe"
# The release signs the Windows binaries and no others, so the signed directory is
# what a Windows run must be handed and what a macOS run must not be.
case "$GOOS_T" in
  windows) [ -n "$SIGNED" ] || { fail "--signed <dir> is required for $TARGET: the binaries the release attaches are the signed ones"; exit 2; } ;;
  *) [ -z "$SIGNED" ] || { fail "--signed applies to windows/amd64 alone; the release does not sign the $TARGET binaries"; exit 2; } ;;
esac
# The runner names directories in the Windows form; this shell works in its own.
if command -v cygpath >/dev/null 2>&1; then
  INPUTS="$(cygpath -u "$INPUTS")"
  [ -z "$SIGNED" ] || SIGNED="$(cygpath -u "$SIGNED")"
  [ -z "$SUBSTITUTE" ] || SUBSTITUTE="$(cygpath -u "$SUBSTITUTE")"
fi
[ -f "$INPUTS/smoke.env" ] || { fail "$INPUTS holds no smoke.env; is it the directory stage-platform-smoke.sh wrote?"; exit 2; }
if [ -n "$SIGNED" ]; then
  [ -d "$SIGNED" ] || { fail "--signed names $SIGNED, which is not a directory"; exit 2; }
  SIGNED="$(cd "$SIGNED" && pwd)"
fi
if [ -n "$SUBSTITUTE" ]; then
  [ -d "$SUBSTITUTE" ] || { fail "--binaries names $SUBSTITUTE, which is not a directory"; exit 2; }
  SUBSTITUTE="$(cd "$SUBSTITUTE" && pwd)"
fi
WORK="$INPUTS/run"
[ ! -e "$WORK" ] || { fail "$WORK already exists; give a freshly staged directory"; exit 2; }

TOOLS=(cat cmp cp dd git head sleep tr uname)
[ "$GOOS_T" = darwin ] && TOOLS+=(chmod xattr)
[ "$GOOS_T" = windows ] && TOOLS+=(powershell.exe sha256sum)
for tool in "${TOOLS[@]}"; do
  command -v "$tool" >/dev/null 2>&1 || { fail "$tool not found on PATH"; exit 1; }
done

STAGED_TARGET="" VERSION="" COMMIT="" RELEASE="" CHART_RELEASE="" REGISTRY_ENTITY="" REGISTRY_REFERENCE=""
while IFS='=' read -r key value; do
  case "$key" in
    target) STAGED_TARGET="$value" ;;
    version) VERSION="$value" ;;
    commit) COMMIT="$value" ;;
    release) RELEASE="$value" ;;
    chart_release) CHART_RELEASE="$value" ;;
    registry_entity) REGISTRY_ENTITY="$value" ;;
    registry_reference) REGISTRY_REFERENCE="$value" ;;
  esac
done < "$INPUTS/smoke.env"
[ -n "$STAGED_TARGET" ] && [ -n "$VERSION" ] && [ -n "$COMMIT" ] && [ -n "$RELEASE" ] && [ -n "$CHART_RELEASE" ] ||
  { fail "smoke.env names no target, version, commit, release or chart_release"; exit 1; }
[ -n "$REGISTRY_ENTITY" ] && [ -n "$REGISTRY_REFERENCE" ] ||
  { fail "smoke.env names no registry_entity or registry_reference"; exit 1; }
if [ "$STAGED_TARGET" != "$TARGET" ]; then
  fail "the inputs were staged for $STAGED_TARGET, not for $TARGET"
  exit 1
fi

HEAD_COMMIT="$(git rev-parse HEAD)"
if [ "$HEAD_COMMIT" != "$COMMIT" ]; then
  fail "this checkout is at $HEAD_COMMIT, but the tests and the example were staged from $COMMIT"
  exit 1
fi
MACHINE="$(uname -m)"
echo "platform-smoke: $TARGET, version $VERSION, commit $COMMIT, on $(uname -s) $MACHINE, bash $BASH_VERSION"

# The SHA-256 list the signing writes beside the signed binaries.
SUMS_NAME=SHA256SUMS

# A binaries directory holds exactly the target's binaries, and any other file
# named after <dir>: one missing, or one the target does not carry, is a staging
# that does not match what the release attaches.
require_binary_set() { # <dir> [<other file>...]
  local dir="$1" name path
  local -A expected_bin=()
  shift
  for name in "$@"; do
    expected_bin[$name]=1
    [ -f "$dir/$name" ] || { fail "${dir#"$INPUTS/"} holds no $name"; exit 1; }
  done
  for name in "${BINARY_NAMES[@]}"; do
    expected_bin[$name]=1
    [ -f "$dir/$name" ] || { fail "${dir#"$INPUTS/"} holds no $name, which the release carries for $TARGET"; exit 1; }
  done
  for path in "$dir"/*; do
    name="$(basename "$path")"
    [ -n "${expected_bin[$name]+set}" ] || { fail "${dir#"$INPUTS/"} holds $name, which the release does not carry for $TARGET"; exit 1; }
  done
}
require_binary_set "$INPUTS/bin"
BIN_DIR="$INPUTS/bin"
if [ -n "$SIGNED" ]; then
  require_binary_set "$SIGNED" "$SUMS_NAME"
  BIN_DIR="$SIGNED"
  echo "platform-smoke: running the signed binaries in $SIGNED; bin/ holds the unsigned ones the build wrote"
fi
if [ -n "$SUBSTITUTE" ]; then
  require_binary_set "$SUBSTITUTE"
  echo "platform-smoke: running the binaries in $SUBSTITUTE in place of the ones in ${BIN_DIR#"$INPUTS/"}"
  BIN_DIR="$SUBSTITUTE"
fi
REGISTRY_PROGRAM="$INPUTS/tools/registry$EXE_SUFFIX"
[ -f "$REGISTRY_PROGRAM" ] || { fail "tools/ holds no registry$EXE_SUFFIX, the registry step 6 starts"; exit 1; }

case "$GOOS_T" in
  darwin)
    case "$GOARCH_T" in
      arm64) want_machine=arm64 ;;
      amd64) want_machine=x86_64 ;;
    esac
    if [ "$MACHINE" != "$want_machine" ]; then
      fail "this machine reports $MACHINE: the $TARGET binaries are proved only on a Mac reporting $want_machine"
      exit 1
    fi
    ;;
esac

mkdir -p "$WORK"

if [ "$GOOS_T" = darwin ]; then
  # A workflow artifact does not keep file modes, so each executable comes back
  # without its execute bit; and a file written by a program that marks its
  # downloads would carry the quarantine mark, which this run must not meet.
  executables=("$REGISTRY_PROGRAM")
  declare -A listed=()
  for name in "${BINARY_NAMES[@]}"; do executables+=("$BIN_DIR/$name"); done
  while read -r exe _; do
    [ -n "${exe:-}" ] && [ -z "${listed[$exe]+set}" ] || continue
    listed[$exe]=1
    executables+=("$INPUTS/tests/$exe")
  done < "$INPUTS/tests/tests.txt"
  for path in "${executables[@]}"; do
    chmod 0755 "$path"
    if xattr -p com.apple.quarantine "$path" >/dev/null 2>&1; then
      fail "${path#"$INPUTS/"} carries the quarantine mark, so Gatekeeper would assess it before it runs"
      exit 1
    fi
  done
  echo "ok: ${#executables[@]} executable(s) marked executable, none carrying the quarantine mark"
fi

FULL="$BIN_DIR/${BINARY_NAMES[0]}"
BINARIES=()
for name in "${BINARY_NAMES[@]}"; do BINARIES+=("$BIN_DIR/$name"); done
LOGS="$WORK/logs"
# Steps 5 to 8 work beneath this directory. Its last name carries a space and an
# e with an acute accent, written as its UTF-8 bytes so this file stays ASCII.
ODD="$WORK/work space "$'\303\251'

check_versions() {
  local bin text json flat
  for bin in "${BINARIES[@]}"; do
    text="$("$bin" version)" || { fail "$(basename "$bin") version exited non-zero"; return 1; }
    printf '%s\n' "$text"
    case "$text" in
      "aggrega $VERSION ("*) ;;
      *) fail "$(basename "$bin") version does not report aggrega $VERSION"; return 1 ;;
    esac
    json="$("$bin" version -o json)" || { fail "$(basename "$bin") version -o json exited non-zero"; return 1; }
    printf '%s\n' "$json"
    flat="$(printf '%s' "$json" | tr -d '[:space:]')"
    case "$flat" in
      *"\"version\":\"$VERSION\""*) ;;
      *) fail "$(basename "$bin") version -o json does not report version $VERSION"; return 1 ;;
    esac
    case "$flat" in
      *"\"platform\":\"$TARGET\""*) ;;
      *) fail "$(basename "$bin") version -o json does not report the platform $TARGET"; return 1 ;;
    esac
    echo "ok: $(basename "$bin") reports $VERSION on $TARGET"
  done
}

check_handshake() {
  local request response
  request='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"platform-smoke","version":"1"}}}'
  mkdir -p "$WORK/mcp"
  # Standard input closes after the one request, which ends the session once it
  # is answered.
  if ! (cd "$WORK/mcp" && printf '%s\n' "$request" | "$FULL" mcp > "$WORK/mcp.out" 2> "$WORK/mcp.err"); then
    cat "$WORK/mcp.err" >&2
    fail "aggrega mcp exited non-zero"
    return 1
  fi
  echo "stderr:"; cat "$WORK/mcp.err"
  response="$(head -n 1 "$WORK/mcp.out" | tr -d '\r')"
  printf 'first line on standard output: %.400s\n' "$response"
  case "$response" in
    *'"jsonrpc":"2.0"'*) ;;
    *) fail "the first line on standard output is not a JSON-RPC message"; return 1 ;;
  esac
  case "$response" in
    *'"id":1,'* | *'"id":1}'*) ;;
    *) fail "the answer does not carry the request's id 1"; return 1 ;;
  esac
  case "$response" in
    *'"error":'*) fail "the answer to initialize is an error"; return 1 ;;
  esac
  case "$response" in
    *'"result":{'*'"protocolVersion":"2025-06-18"'*) ;;
    *) fail "the answer to initialize is not a result naming protocol version 2025-06-18"; return 1 ;;
  esac
  echo "ok: initialize answered with a result"
}

# compare_with_reference <reference> <workspace> <planted> — compare <workspace>
# with the <reference> the build machine built, then show the comparison can fail:
# a copy of the reference made at <planted>, one byte of its first non-empty
# rendered file changed, must compare unequal with the same workspace.
compare_with_reference() {
  local reference="$1" workspace="$2" planted="$3" target="" had_globstar=0 entry name
  if ! smoke_compare_trees "$reference" "$workspace"; then
    fail "what this machine built differs from what the release's build machine built"
    return 1
  fi
  echo "ok: the $TARGET build is byte-identical to the reference"

  echo "The same comparison against the reference with one byte changed must report it:"
  mkdir -p "$planted"
  cp -R "$reference/." "$planted/"
  shopt -q globstar && had_globstar=1
  shopt -s globstar
  for entry in "$planted/dist/"**; do
    if [ -f "$entry" ] && [ -s "$entry" ]; then target="$entry"; break; fi
  done
  [ "$had_globstar" = 1 ] || shopt -u globstar
  [ -n "$target" ] || { fail "the reference holds no rendered file to plant a difference in"; return 1; }
  smoke_plant_one_byte "$target" || return 1
  name="${target#"$planted/"}"
  echo "planted: the first byte of $name"
  if smoke_compare_trees "$planted" "$workspace"; then
    fail "the comparison found no difference after one byte of $name was changed, so its pass above proves nothing"
    return 1
  fi
  echo "ok: the comparison reports the planted difference"
}

check_example() {
  local log="$LOGS/example"
  mkdir -p "$WORK/example"
  cp -R "$INPUTS/example/input/." "$WORK/example/"
  smoke_build_example "$FULL" "$WORK/example" "$RELEASE" "$log" --progress || return 1
  if [ ! -s "$log/pull.err" ] || [ ! -s "$log/build.err" ]; then
    fail "the pull or the build with --progress printed no progress line, so the escape check over their output read none"
    return 1
  fi
  compare_with_reference "$INPUTS/example/reference" "$WORK/example" "$WORK/planted"
}

check_path() {
  smoke_require_space_and_non_ascii "$ODD" || { fail "the directory steps 5 to 8 work in is not the one they need"; return 1; }
  mkdir -p "$ODD"
  echo "The same check over a plain path must refuse it:"
  if smoke_require_space_and_non_ascii "/plain/path"; then
    fail "the path check accepted a plain path, so its pass above proves nothing"
    return 1
  fi
  echo "ok: the path check refuses a plain path"
}

check_configuration() {
  local log="$LOGS/configuration" base="$ODD/configuration" config_file printed_file printed_cache
  mkdir -p "$base/cwd"
  case "$GOOS_T" in
    windows)
      mkdir -p "$base/AppData/Roaming" "$base/AppData/Local"
      config_file="$base/AppData/Roaming/aggrega/config.yaml"
      printed_file="$(smoke_native_path "$base/AppData/Roaming")\\aggrega\\config.yaml"
      printed_cache="$(smoke_native_path "$base/AppData/Local")\\aggrega\\cache"
      (
        unset HOME XDG_CONFIG_HOME XDG_CACHE_HOME AGGREGA_CONFIG
        APPDATA="$(smoke_native_path "$base/AppData/Roaming")"
        LOCALAPPDATA="$(smoke_native_path "$base/AppData/Local")"
        export APPDATA LOCALAPPDATA
        cd "$base/cwd"
        smoke_run_logged "$log" config-init "$FULL" config init
      ) || { fail "config init with only APPDATA and LOCALAPPDATA to place its files did not succeed"; return 1; }
      ;;
    darwin)
      mkdir -p "$base/home"
      config_file="$base/home/.config/aggrega/config.yaml"
      printed_file="$config_file"
      printed_cache="$base/home/Library/Caches/aggrega"
      (
        unset XDG_CONFIG_HOME XDG_CACHE_HOME AGGREGA_CONFIG APPDATA LOCALAPPDATA
        export HOME="$base/home"
        cd "$base/cwd"
        smoke_run_logged "$log" config-init "$FULL" config init
      ) || { fail "config init with only HOME to place its files did not succeed"; return 1; }
      ;;
  esac
  # The locations are stated here, independently of the product's own rule, and
  # deliberately so: a check that asked the product where the file belongs could
  # not catch the product putting it in the wrong place.
  smoke_check_config_init "$log/config-init.out" "$log/config-init.err" "$config_file" "$printed_file" "$printed_cache" ||
    { fail "config init did not write and report the global configuration where the $GOOS_T rule puts it"; return 1; }

  echo "The same check against a directory holding no configuration file must refuse it:"
  mkdir -p "$base/empty"
  if smoke_check_config_init "$log/config-init.out" "$log/config-init.err" "$base/empty/aggrega/config.yaml" "$printed_file" "$printed_cache"; then
    fail "the configuration check accepted a directory holding no file, so its pass above proves nothing"
    return 1
  fi
  echo "ok: the configuration check refuses a missing file"
}

# The registry program's process, for the trap that stops it when step 6 ends.
REGISTRY_PID=""

check_entity_cache() {
  local log="$LOGS/entity-cache" publish="$ODD/publish" consumer="$ODD/entity cache" entity port="" i text
  local -a names=()
  local -A entries=()
  mkdir -p "$log" "$publish" "$consumer"
  cp -R "$INPUTS/registry/publish/." "$publish/"
  cp -R "$INPUTS/registry/consumer/." "$consumer/"
  entity="$publish/${REGISTRY_ENTITY#registry/publish/}"
  [ -f "$entity" ] || { fail "the staged inputs hold no $REGISTRY_ENTITY"; return 1; }

  # Port 0 has the program take a free port and name it in its log. The log exists
  # before the program starts, since a redirection on a command run in the
  # background is made by that command's own process, after this one goes on.
  : > "$log/registry.log"
  "$REGISTRY_PROGRAM" -port 0 >> "$log/registry.log" 2>&1 &
  REGISTRY_PID=$!
  trap 'kill "$REGISTRY_PID" 2>/dev/null || true' EXIT
  for ((i = 0; i < 200; i++)); do
    text="$(< "$log/registry.log")"
    if [[ $text =~ serving\ on\ port\ ([0-9]+) ]]; then
      port="${BASH_REMATCH[1]}"
      break
    fi
    sleep 0.1
  done
  if [ -z "$port" ]; then
    cat "$log/registry.log" >&2
    fail "the registry program named no port within 20 seconds"
    return 1
  fi
  echo "ok: a registry serves localhost:$port"
  printf 'default_registry: "localhost:%s"\n' "$port" > "$consumer/.aggrega-config.yaml"

  (
    # The push and the pull are the only steps that may reach a network, and only
    # this machine's loopback address: a request to it goes direct, and the proxy
    # these settings name, a closed port on that address, refuses every other.
    export HTTP_PROXY=http://127.0.0.1:9 HTTPS_PROXY=http://127.0.0.1:9 http_proxy=http://127.0.0.1:9 https_proxy=http://127.0.0.1:9
    unset NO_PROXY no_proxy
    cd "$publish"
    smoke_run_logged "$log" push "$FULL" --offline=false --workspace-root "$(smoke_native_path "$publish")" \
      push "$(smoke_native_path "$entity")" --registry "localhost:$port"
    cd "$consumer"
    smoke_run_logged "$log" pull "$FULL" --offline=false --workspace-root "$(smoke_native_path "$consumer")" \
      pull --release "$RELEASE" --detail
  ) || { fail "the push to, or the pull from, the registry on this machine did not succeed"; return 1; }
  smoke_check_fetched "$log/pull.out" "$REGISTRY_REFERENCE" || { fail "the pull did not fetch the entity from the registry"; return 1; }
  smoke_check_entity_cache "$consumer" || { fail "the pull did not write the entity cache under names Windows accepts"; return 1; }

  ( cd "$consumer" && smoke_run_logged "$log" build "$FULL" --workspace-root "$(smoke_native_path "$consumer")" build --release "$RELEASE" ) ||
    { fail "the build, offline, from the entity cache did not succeed"; return 1; }
  if ! smoke_compare_trees "$INPUTS/example/reference/dist" "$consumer/dist"; then
    fail "the build from the entity cache rendered other bytes than the build from the catalog"
    return 1
  fi
  echo "ok: the build from the entity cache rendered the reference's bytes"

  echo "The same cache check over the reference workspace, which resolved everything from its own catalog, must refuse it:"
  if smoke_check_entity_cache "$INPUTS/example/reference"; then
    fail "the cache check accepted a workspace holding no cached entity, so its pass above proves nothing"
    return 1
  fi
  echo "The name check over the cache's names with a colon planted in one must refuse them:"
  smoke_tree_entries "$consumer/.aggrega/cache" entries || return 1
  names=("${!entries[@]}")
  if printf '%s\n' "${names[@]}" "${names[0]%/}:planted" | smoke_names_windows_accepts; then
    fail "the name check accepted a name with a colon, so its pass above proves nothing"
    return 1
  fi
  echo "ok: the cache check refuses a workspace with no cached entity, and the name check a name with a colon"
}

check_catalog() {
  local log="$LOGS/catalog" ws="$ODD/catalog named twice" planted
  mkdir -p "$ws"
  cp -R "$INPUTS/example/input/." "$ws/"
  # The pull must write the lockfile, or its silence would prove nothing.
  rm -f "$ws/${RELEASE%.yaml}.lock"
  printf 'workspace:\n  catalog_dirs:\n    - catalog\n' > "$ws/.aggrega-config.yaml"
  ( cd "$ws" && smoke_run_logged "$log" pull "$FULL" --workspace-root "$(smoke_native_path "$ws")" pull --release "$RELEASE" --catalog catalog ) ||
    { fail "the pull naming the catalog by configuration and by flag did not succeed"; return 1; }
  [ -s "$ws/${RELEASE%.yaml}.lock" ] || { fail "the pull wrote no lockfile, so its silence proves nothing"; return 1; }
  smoke_refuse_shadow_notices "$log/pull.out" "$log/pull.err" ||
    { fail "a catalog directory named by configuration and by flag was read as two"; return 1; }

  echo "The same check over the pull's output with a shadow notice planted must refuse it:"
  planted="$log/pull.err.planted"
  cp "$log/pull.err" "$planted"
  printf '%s\n' "catalog/base/components/web-ui.yaml skipped: duplicate component identity; this file is shadowed" >> "$planted"
  if smoke_refuse_shadow_notices "$log/pull.out" "$planted"; then
    fail "the notice check accepted a planted shadow notice, so its pass above proves nothing"
    return 1
  fi
  echo "ok: the notice check refuses a planted shadow notice"
}

check_chart() {
  local ws="$ODD/chart example"
  mkdir -p "$ws"
  cp -R "$INPUTS/chart/input/." "$ws/"
  smoke_build_example "$FULL" "$ws" "$CHART_RELEASE" "$LOGS/chart" || return 1
  compare_with_reference "$INPUTS/chart/reference" "$ws" "$ODD/chart planted"
}

check_escapes() {
  local source="$LOGS/example/build.err" planted="$LOGS/escapes/build.err.planted"
  mkdir -p "$LOGS/escapes"
  if [ -f "$source" ]; then cp "$source" "$planted"; else : > "$planted"; fi
  printf 'planted \033[2mline\033[0m\n' >> "$planted"
  echo "The escape check over a copy of the example build's standard error with an escape byte planted must refuse it:"
  if smoke_refuse_escapes "$planted"; then
    fail "the escape check accepted a planted escape byte, so its passes in steps 3 to 8 prove nothing"
    return 1
  fi
  echo "ok: the escape check refuses a planted escape byte"
}

check_tests() {
  # One executable at a time, in the order staged, each running every test staged
  # for it.
  local -a order=() names=()
  local -A names_of=() pkg_of=() passed=()
  local exe pkg name pattern log rc passes line
  while read -r exe pkg name; do
    [ -n "${exe:-}" ] || continue
    if [ -z "${pkg_of[$exe]+set}" ]; then
      order+=("$exe")
      pkg_of[$exe]="$pkg"
      names_of[$exe]="$name"
    else
      names_of[$exe]="${names_of[$exe]} $name"
    fi
  done < "$INPUTS/tests/tests.txt"
  [ "${#order[@]}" -gt 0 ] || { fail "tests.txt names no test"; return 1; }

  for exe in "${order[@]}"; do
    pkg="${pkg_of[$exe]}"
    read -r -a names <<<"${names_of[$exe]}"
    pattern="^($(IFS='|'; printf '%s' "${names[*]}"))\$"
    log="$WORK/$exe.log"
    printf '\n--- %s, from %s: -test.run %s\n' "$exe" "$pkg" "$pattern"
    [ -d "$CLI_ROOT/$pkg" ] || { fail "$pkg is not a directory of this checkout"; return 1; }
    rc=0
    (cd "$CLI_ROOT/$pkg" && "$INPUTS/tests/$exe" -test.run "$pattern" -test.count=1 -test.timeout 10m -test.v) > "$log" 2>&1 || rc=$?
    tr -d '\r' < "$log"
    if [ "$rc" != 0 ]; then
      fail "$exe exited $rc"
      return 1
    fi
    passes=0
    passed=()
    while IFS= read -r line; do
      line="${line%$'\r'}"
      case "$line" in
        *"--- SKIP: "*) fail "$exe: a named test skipped: ${line#*--- SKIP: }"; return 1 ;;
        *"--- FAIL: "*) fail "$exe: ${line#*--- }"; return 1 ;;
        *"no tests to run"*) fail "$exe: the run found no test to run"; return 1 ;;
        "--- PASS: "*)
          passes=$((passes + 1))
          line="${line#--- PASS: }"
          passed["${line%% (*}"]=1
          ;;
      esac
    done < "$log"
    for name in "${names[@]}"; do
      [ -n "${passed[$name]+set}" ] || { fail "$exe: $name did not report PASS"; return 1; }
    done
    if [ "$passes" != "${#names[@]}" ]; then
      fail "$exe: $passes tests reported PASS, but ${#names[@]} were named"
      return 1
    fi
    echo "ok: $exe — ${#names[@]} named test(s) passed, none skipped"
  done
}

# The publisher every Windows binary must be signed as. It is stated here rather
# than read from anything the check looks at, and deliberately so, as step 5
# states its locations: a check that took the expected name from the signature it
# checks would accept whichever signer it found.
SIGNER_CN="Cognitio S.r.l."

# authenticode_report <file> <report> — write to <report>, as key=value lines,
# what Windows' own Get-AuthenticodeSignature, in Windows PowerShell, says about
# <file>. The path reaches PowerShell through the environment rather than in the
# command's text, so no character of it can change what PowerShell runs; a
# certificate that is absent reports its fields empty.
authenticode_report() {
  local script
  script="\$ErrorActionPreference = 'Stop'; "
  script+="\$s = Get-AuthenticodeSignature -LiteralPath \$env:SMOKE_SIGNATURE_FILE; "
  script+="\$c = \$s.SignerCertificate; \$t = \$s.TimeStamperCertificate; "
  script+="'status=' + \$s.Status; "
  script+="'status_message=' + (\$s.StatusMessage -replace '[\\r\\n]+', ' '); "
  script+="'signer_subject=' + \$c.Subject; "
  script+="'signer_issuer=' + \$c.Issuer; "
  script+="'signer_thumbprint=' + \$c.Thumbprint; "
  script+="'signer_serial=' + \$c.SerialNumber; "
  script+="'signer_not_after=' + \$(if (\$c) { \$c.NotAfter.ToUniversalTime().ToString('u') }); "
  script+="'timestamper_subject=' + \$t.Subject; "
  script+="'timestamper_thumbprint=' + \$t.Thumbprint"
  # MSYS_NO_PATHCONV keeps the Git for Windows shell from rewriting an argument it
  # takes for a path on its way to a Windows program.
  SMOKE_SIGNATURE_FILE="$(smoke_native_path "$1")" MSYS_NO_PATHCONV=1 \
    powershell.exe -NoLogo -NoProfile -NonInteractive -Command "$script" > "$2"
}

check_signature() {
  local log="$LOGS/signature" name report bad=0
  mkdir -p "$log"
  smoke_check_sums "$SIGNED/$SUMS_NAME" "$BIN_DIR" "${BINARY_NAMES[@]}" || bad=1
  for name in "${BINARY_NAMES[@]}"; do
    report="$log/$name.txt"
    printf -- '--- %s: Get-AuthenticodeSignature\n' "$name"
    if ! authenticode_report "$BIN_DIR/$name" "$report"; then
      fail "Windows PowerShell could not report the signature of $name"
      bad=1
      continue
    fi
    tr -d '\r' < "$report"
    smoke_check_authenticode_report "$report" "$SIGNER_CN" || bad=1
  done
  if [ "$bad" != 0 ]; then
    fail "the binaries are not the listed ones, each signed as $SIGNER_CN with a timestamp Windows reports Valid"
    return 1
  fi

  echo "The same signature check over the unsigned ${BINARY_NAMES[0]} staged in bin/ must refuse it:"
  report="$log/unsigned.txt"
  authenticode_report "$INPUTS/bin/${BINARY_NAMES[0]}" "$report" ||
    { fail "Windows PowerShell could not report the signature of the unsigned ${BINARY_NAMES[0]}"; return 1; }
  tr -d '\r' < "$report"
  if smoke_check_authenticode_report "$report" "$SIGNER_CN"; then
    fail "the signature check accepted the unsigned ${BINARY_NAMES[0]}, so its passes above prove nothing"
    return 1
  fi
  echo "The same list check over the unsigned binaries in bin/ must refuse them:"
  if smoke_check_sums "$SIGNED/$SUMS_NAME" "$INPUTS/bin" "${BINARY_NAMES[@]}"; then
    fail "the list check accepted the unsigned binaries, so its pass above proves nothing"
    return 1
  fi
  echo "ok: the signature check refuses an unsigned binary, and the list check the unsigned binaries"
}

# Each step runs in a subshell, so an environment it changes, and a process it
# starts, end with it. The subshell is not the left side of || or &&: there the
# shell would stop honouring set -e inside it, and a failing command would no
# longer end the step. A failing step ends the run, unless --binaries named the
# binaries, in which case the run goes on and names every failed step at its end.
FAILED=()
run_step() { # <title> <function> [isolated]
  local title="$1" fn="$2" isolated="${3:-}" rc
  step "$title"
  set +e
  (
    set -e
    if [ "$isolated" = isolated ]; then smoke_isolate "$WORK/config"; fi
    "$fn"
  )
  rc=$?
  set -e
  [ "$rc" = 0 ] && return 0
  FAILED+=("$title")
  [ -n "$SUBSTITUTE" ] || exit 1
  printf 'platform-smoke %s: step failed; going on, since --binaries named the binaries\n' "$TARGET" >&2
}

run_step "1. versions" check_versions isolated
run_step "2. handshake: aggrega mcp over standard input and output" check_handshake isolated
run_step "3. example: validate, pull and build $RELEASE, and compare with the reference" check_example isolated
run_step "4. path: a directory whose path holds a space and a letter outside ASCII" check_path
run_step "5. configuration: config init where the $GOOS_T rule alone places it" check_configuration isolated
run_step "6. entity cache: pull $REGISTRY_REFERENCE from a registry on this machine, then build from the cache" check_entity_cache isolated
run_step "7. catalog: the catalog named by configuration and by flag, read once" check_catalog isolated
run_step "8. chart: validate, pull and build the chart-backed $CHART_RELEASE, and compare with the reference" check_chart isolated
run_step "9. escapes: the escape check refuses a planted escape byte" check_escapes
run_step "10. tests" check_tests
if [ "$GOOS_T" = windows ]; then
  run_step "11. signature: each binary signed as $SIGNER_CN, timestamped, and the one SHA256SUMS lists" check_signature
fi

if [ "${#FAILED[@]}" -gt 0 ]; then
  printf '\nplatform-smoke: %s: with the binaries in %s, %d step(s) failed:\n' "$TARGET" "$SUBSTITUTE" "${#FAILED[@]}" >&2
  printf '  %s\n' "${FAILED[@]}" >&2
  exit 1
fi
if [ -n "$SUBSTITUTE" ]; then
  printf '\nplatform-smoke: %s: every step passed with the binaries in %s, which the release does not carry, so the run does not pass\n' \
    "$TARGET" "$SUBSTITUTE" >&2
  exit 3
fi
printf '\nplatform-smoke: %s: every check passed\n' "$TARGET"
