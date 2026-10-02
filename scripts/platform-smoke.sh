#!/usr/bin/env bash
# aggrega-cli/scripts/platform-smoke.sh — run one platform's binaries and tests on
# a machine of that platform, and hold what the full binary builds to the bytes
# the release's own build machine built.
#
# It reads the directory scripts/stage-platform-smoke.sh wrote for the target
# (--inputs) and runs from a checkout of this repository at the commit that
# directory names: under the Git for Windows shell on Windows, under bash on macOS.
# It needs no Go toolchain, no sibling repositories, no credentials and no
# registry; it does need bash 4.3 or later, which macOS does not ship. Before the
# checks it holds the inputs and the machine to the target:
#
#   - the staged inputs name the target given with --target, and bin/ holds
#     exactly the binaries scripts/lib/platform-smoke-common.sh lists for it: the
#     full, light and stripped binaries for windows/amd64; the full and light ones
#     for darwin/arm64 and darwin/amd64
#   - on macOS, the machine is the target's: uname -m reports arm64 for
#     darwin/arm64 and x86_64 for darwin/amd64. A Mac with Apple silicon also runs
#     an amd64 binary, through Rosetta 2, and that binary still reports
#     darwin/amd64, so without this check an Apple silicon machine would pass for
#     an Intel one
#   - on macOS, the binaries and the test executables are made executable again,
#     because a workflow artifact does not keep file modes, and none of them may
#     carry the quarantine mark, under which Gatekeeper would assess a program
#     that is not notarized before it runs
#
# Then, in order, stopping at the first failure with what failed:
#
#   1. versions   each binary reports, as `version` and as `version -o json`, the
#                 version staged with it, and the JSON names the target as its
#                 platform
#   2. handshake  the full binary, run as `aggrega mcp`, answers an initialize
#                 request on standard input with a result on standard output
#   3. example    the full binary validates, pulls and builds the staged worked
#                 example in a fresh directory, and the whole workspace it leaves —
#                 the rendered tree and the lockfile with it — is compared byte for
#                 byte, every file and the set of names, with the reference the
#                 build machine built; then the same comparison runs against a copy
#                 of the reference with one byte changed, and must report that
#                 difference, so the comparison shown passing is one that can fail
#   4. tests      each staged test executable runs, from its package directory, the
#                 tests staged for it with -test.run naming them, -test.count=1,
#                 -test.timeout 10m and -test.v; every named test must report PASS,
#                 and a named test that skips, a run that finds no test to run, or a
#                 count of passes other than the count of names fails the run
#
# Steps 1-3 run with every AGGREGA_ setting of the machine unset, the global
# configuration pointed at an empty directory, and the offline posture on, so they
# read nothing of the machine and reach no network. The tests run in the
# environment the shell was given; their own setup isolates what they read.
#
# Usage:
#   scripts/platform-smoke.sh --target <goos>/<goarch> --inputs <dir>
#   scripts/platform-smoke.sh --help
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

INPUTS=""
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
# The runner names directories in the Windows form; this shell works in its own.
command -v cygpath >/dev/null 2>&1 && INPUTS="$(cygpath -u "$INPUTS")"
[ -f "$INPUTS/smoke.env" ] || { fail "$INPUTS holds no smoke.env; is it the directory stage-platform-smoke.sh wrote?"; exit 2; }
WORK="$INPUTS/run"
[ ! -e "$WORK" ] || { fail "$WORK already exists; give a freshly staged directory"; exit 2; }

TOOLS=(cmp cp dd git head tr uname)
[ "$GOOS_T" = darwin ] && TOOLS+=(chmod xattr)
for tool in "${TOOLS[@]}"; do
  command -v "$tool" >/dev/null 2>&1 || { fail "$tool not found on PATH"; exit 1; }
done

STAGED_TARGET="" VERSION="" COMMIT="" RELEASE=""
while IFS='=' read -r key value; do
  case "$key" in
    target) STAGED_TARGET="$value" ;;
    version) VERSION="$value" ;;
    commit) COMMIT="$value" ;;
    release) RELEASE="$value" ;;
  esac
done < "$INPUTS/smoke.env"
[ -n "$STAGED_TARGET" ] && [ -n "$VERSION" ] && [ -n "$COMMIT" ] && [ -n "$RELEASE" ] ||
  { fail "smoke.env names no target, version, commit or release"; exit 1; }
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

# bin/ holds exactly the target's binaries: one missing, or one the target does
# not carry, is a staging that does not match what the release attaches.
declare -A expected_bin=()
for name in "${BINARY_NAMES[@]}"; do
  expected_bin[$name]=1
  [ -f "$INPUTS/bin/$name" ] || { fail "bin/ holds no $name, which the release carries for $TARGET"; exit 1; }
done
for path in "$INPUTS"/bin/*; do
  name="$(basename "$path")"
  [ -n "${expected_bin[$name]+set}" ] || { fail "bin/ holds $name, which the release does not carry for $TARGET"; exit 1; }
done

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
  executables=()
  declare -A listed=()
  for name in "${BINARY_NAMES[@]}"; do executables+=("$INPUTS/bin/$name"); done
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

FULL="$INPUTS/bin/${BINARY_NAMES[0]}"
BINARIES=()
for name in "${BINARY_NAMES[@]}"; do BINARIES+=("$INPUTS/bin/$name"); done

# The versions, the handshake and the example run isolated from the machine.
smoke_isolated() {
  smoke_isolate "$WORK/config"

  step "1. versions"
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

  step "2. handshake: aggrega mcp over standard input and output"
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

  step "3. example: validate, pull and build $RELEASE, and compare with the reference"
  mkdir -p "$WORK/example"
  cp -R "$INPUTS/example/input/." "$WORK/example/"
  smoke_build_example "$FULL" "$WORK/example" "$RELEASE" || return 1
  if ! smoke_compare_trees "$INPUTS/example/reference" "$WORK/example"; then
    fail "what this machine built differs from what the release's build machine built"
    return 1
  fi
  echo "ok: the $TARGET build is byte-identical to the reference"

  echo "The same comparison against the reference with one byte changed must report it:"
  local planted target=""
  mkdir -p "$WORK/planted"
  cp -R "$INPUTS/example/reference/." "$WORK/planted/"
  local had_globstar=0 entry
  shopt -q globstar && had_globstar=1
  shopt -s globstar
  for entry in "$WORK/planted/dist/"**; do
    if [ -f "$entry" ] && [ -s "$entry" ]; then target="$entry"; break; fi
  done
  [ "$had_globstar" = 1 ] || shopt -u globstar
  [ -n "$target" ] || { fail "the reference holds no rendered file to plant a difference in"; return 1; }
  smoke_plant_one_byte "$target" || return 1
  planted="${target#"$WORK/planted/"}"
  echo "planted: the first byte of $planted"
  if smoke_compare_trees "$WORK/planted" "$WORK/example"; then
    fail "the comparison found no difference after one byte of $planted was changed, so its pass above proves nothing"
    return 1
  fi
  echo "ok: the comparison reports the planted difference"
}
# A subshell, so the isolation ends with it and the tests below run in the
# environment the shell was given. It is not run as the left side of || or &&:
# there the shell would stop honouring set -e inside it, and a failing step would
# no longer end the run.
( smoke_isolated )

step "4. tests"
# One executable at a time, in the order staged, each running every test staged
# for it.
order=()
declare -A names_of=() pkg_of=()
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
[ "${#order[@]}" -gt 0 ] || { fail "tests.txt names no test"; exit 1; }

for exe in "${order[@]}"; do
  pkg="${pkg_of[$exe]}"
  read -r -a names <<<"${names_of[$exe]}"
  pattern="^($(IFS='|'; printf '%s' "${names[*]}"))\$"
  log="$WORK/$exe.log"
  printf '\n--- %s, from %s: -test.run %s\n' "$exe" "$pkg" "$pattern"
  [ -d "$CLI_ROOT/$pkg" ] || { fail "$pkg is not a directory of this checkout"; exit 1; }
  set +e
  (cd "$CLI_ROOT/$pkg" && "$INPUTS/tests/$exe" -test.run "$pattern" -test.count=1 -test.timeout 10m -test.v) > "$log" 2>&1
  rc=$?
  set -e
  tr -d '\r' < "$log"
  if [ "$rc" != 0 ]; then
    fail "$exe exited $rc"
    exit 1
  fi
  passes=0
  declare -A passed=()
  while IFS= read -r line; do
    line="${line%$'\r'}"
    case "$line" in
      *"--- SKIP: "*) fail "$exe: a named test skipped: ${line#*--- SKIP: }"; exit 1 ;;
      *"--- FAIL: "*) fail "$exe: ${line#*--- }"; exit 1 ;;
      *"no tests to run"*) fail "$exe: the run found no test to run"; exit 1 ;;
      "--- PASS: "*)
        passes=$((passes + 1))
        line="${line#--- PASS: }"
        passed["${line%% (*}"]=1
        ;;
    esac
  done < "$log"
  for name in "${names[@]}"; do
    [ -n "${passed[$name]+set}" ] || { fail "$exe: $name did not report PASS"; exit 1; }
  done
  if [ "$passes" != "${#names[@]}" ]; then
    fail "$exe: $passes tests reported PASS, but ${#names[@]} were named"
    exit 1
  fi
  unset passed
  echo "ok: $exe — ${#names[@]} named test(s) passed, none skipped"
done

printf '\nplatform-smoke: %s: every check passed\n' "$TARGET"
