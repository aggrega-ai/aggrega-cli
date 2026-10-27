#!/usr/bin/env bash
# aggrega-cli/scripts/platform-smoke.sh — run one platform's binaries and tests on
# a machine of that platform, and hold what the default binary builds to the bytes
# the release's own build machine built.
#
# It reads the directory scripts/stage-platform-smoke.sh wrote for the target
# (--inputs) and runs from a checkout of this repository at the commit that
# directory names: under the Git for Windows shell on Windows, under bash on macOS
# and on Linux.
# For windows/amd64, darwin/arm64 and darwin/amd64 it also reads the directory
# the release's signing wrote (--signed): the binaries with their signatures —
# Authenticode for Windows, a Developer ID signature Apple has notarized for
# macOS — and SHA256SUMS, the SHA-256 list of them. The signed binaries are the
# ones every step runs, because they are the ones the release attaches; the
# unsigned copies the build wrote, staged in bin/, run in no step, and step 12
# holds them as what its checks must refuse. On a run that signs nothing,
# --unsigned takes the place of --signed: every step runs the unsigned copies in
# bin/, and step 12, with no signature to check, is not applicable. On a run
# whose release attaches the macOS binaries unsigned, --ships-unsigned takes its
# place for a darwin target: the same, but the run is held as a signed one is. The
# Linux binaries are not signed by the release, so a linux target runs the staged
# ones and takes none of the three.
# It needs no Go toolchain, no sibling repositories, no credentials and no
# registry or server but the ones it starts on this machine — save GitHub, for
# step 14 on a Windows machine that carries no winget; it does need bash 4.3
# or later, which macOS does not ship, and Python 3.7 or later, as python3 or
# python, which step 11 serves a release folder with; on Windows, step 14 needs
# winget, or else curl and GitHub, from which it installs the WinGet CLI it pins.
# Before the checks it holds the inputs and the machine to the target:
#
#   - the staged inputs name the target given with --target, and bin/ holds
#     exactly the binaries scripts/lib/platform-smoke-common.sh lists for it: the
#     default binary, without its debug information, and the debug and light
#     ones, for windows/amd64, darwin/arm64, darwin/amd64 and linux/arm64 alike.
#     For windows/amd64 and the darwin targets the --signed directory holds
#     exactly those binaries too, and SHA256SUMS beside them
#   - on macOS and Linux, the machine is the target's: uname -m reports arm64 for
#     darwin/arm64, x86_64 for darwin/amd64 and aarch64 for linux/arm64. A Mac
#     with Apple silicon also runs an amd64 binary, through Rosetta 2, and that
#     binary still reports darwin/amd64, so without this check an Apple silicon
#     machine would pass for an Intel one; a Linux machine with an emulator
#     registered runs a binary of another architecture the same way
#   - on macOS and Linux, the binaries, the registry program and the test
#     executables are made executable again, because a workflow artifact does not
#     keep file modes; and on macOS none of them may carry the quarantine mark,
#     under which Gatekeeper would assess a program that is not notarized before
#     it runs
#
# Then, in order, stopping at the first failure with what failed unless
# --unsigned or --binaries is given (below):
#
#   1. versions   each binary reports, as `version` and as `version -o json`, the
#                 version staged with it, and the JSON names the target as its
#                 platform
#   2. handshake  the default binary, run as `aggrega mcp`, answers an initialize
#                 request on standard input with a result on standard output
#   3. example    the default binary validates, pulls and builds the staged worked
#                 example in a fresh directory, the pull and the build with
#                 --progress, whose plain progress lines must be printed; the whole
#                 workspace it leaves — the rendered tree and the lockfile with it —
#                 is compared byte for byte, every file and the set of names, with
#                 the reference the build machine built; then the same comparison
#                 runs against a copy of the reference with one byte changed, and
#                 must report that difference, so the comparison shown passing is
#                 one that can fail
#   4. path       steps 5 to 8 and 11 work in a directory whose path holds a
#                 space and a letter outside ASCII, as a user's own directories
#                 often do; the same check over a plain path must refuse it
#   5. configuration
#                 `aggrega config init` runs with only the platform's own rule able
#                 to place the global configuration: on Windows with HOME,
#                 XDG_CONFIG_HOME and AGGREGA_CONFIG unset and APPDATA and
#                 LOCALAPPDATA naming directories of step 4's, as a Windows prompt
#                 gives them; on macOS and Linux with XDG_CONFIG_HOME,
#                 XDG_CACHE_HOME, AGGREGA_CONFIG, APPDATA and LOCALAPPDATA unset
#                 and HOME naming one. It must exit 0, write the file where that
#                 rule puts it — %APPDATA%\aggrega\config.yaml on Windows,
#                 $HOME/.config/aggrega/config.yaml on macOS and Linux — report
#                 that file, the directory the machine-wide caches are kept in and
#                 the registry it registered, print nothing on standard error and
#                 no warning; the same check against a directory holding no file
#                 must refuse it
#   6. entity cache
#                 the registry program staged with the binaries serves an
#                 in-memory registry on this machine's loopback address; the default
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
#   8. chart      the default binary validates, pulls and builds the staged
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
#  11. install    the install scripts this checkout carries, packaging/install/,
#                 install the default binary from a folder laid out as a release
#                 is: the binaries this run runs, under the names the release
#                 gives them, on Windows the installer step 13 runs as well, the
#                 two scripts, and SHA256SUMS over them, written by the functions
#                 the release step writes its own with. The folder
#                 is served on this machine's loopback address by
#                 scripts/lib/serve-release-folder.py, which answers
#                 latest/download/ with a redirect to the release, as GitHub does,
#                 and is named to the scripts as their mirror, AGGREGA_MIRROR; a
#                 server that names no port within 20 seconds fails the step with
#                 its log printed, whether it exited or still ran. On
#                 macOS and Linux, `curl -fsSL <url>/install.sh | sh` runs with HOME
#                 naming a directory beneath step 4's; on Windows,
#                 `irm <url>/install.ps1 | iex` runs under Windows PowerShell
#                 (powershell.exe) and under PowerShell 7 (pwsh), each with
#                 LOCALAPPDATA naming one. Each must install the served default
#                 binary, byte for byte, as $HOME/.local/bin/aggrega or
#                 %LOCALAPPDATA%\Programs\aggrega\aggrega.exe, the folder holding
#                 nothing else, and the installed binary must report the version
#                 staged; on Windows the folder must then be on the user PATH
#                 once, and the user PATH is put back as it was. Then one byte of
#                 the served default binary is changed, and each script, run the
#                 same way into a fresh directory, must refuse it with the sentence
#                 naming the checksum it computed and the one SHA256SUMS lists,
#                 install nothing, and on Windows leave the user PATH as it was
#  12. signature  windows/amd64, darwin/arm64 and darwin/amd64; on windows/amd64:
#                 each binary the steps above ran must have
#                 the SHA-256 that SHA256SUMS lists for it, the list naming exactly
#                 those binaries; and Windows' own Get-AuthenticodeSignature, run
#                 through Windows PowerShell on each, must report Status Valid, a
#                 signer certificate whose subject names Cognitio S.r.l. as its
#                 common name, first, and a timestamp countersignature (a
#                 TimeStamperCertificate), which keeps the signature valid after
#                 its certificate, issued for a few days only, expires. Each file's
#                 status, signer and timestamper are printed. Then the same
#                 signature check over the unsigned default binary staged in bin/,
#                 and the same list check over bin/, must refuse them. Given
#                 --unsigned it checks nothing: it prints that it is not
#                 applicable, and the run's last lines list it so, never as
#                 passed.
#                 darwin/arm64 and darwin/amd64 hold the same list check, and
#                 macOS's own `codesign --verify --strict` must accept each
#                 binary, while `codesign --display --verbose=2 --entitlements -`
#                 must report a first authority of "Developer ID Application:
#                 Cognitio S.r.l. (<team>)", that team as its TeamIdentifier,
#                 the hardened runtime among the code directory's flags, a
#                 secure timestamp, and exactly the entitlements this
#                 checkout's packaging/macos/aggrega.entitlements declares,
#                 the file the signing signed with — none beyond them, none of
#                 them missing — and never get-task-allow. Then
#                 Gatekeeper: a copy of each binary is given the quarantine
#                 mark a browser gives a download, and `spctl --assess --type
#                 execute -vvv` must report it accepted, its source Notarized
#                 Developer ID — the ticket a bare executable cannot carry
#                 stapled, which Gatekeeper finds online. Each report is
#                 printed. Then the same codesign and Gatekeeper checks over a
#                 quarantined copy of the unsigned default binary staged in
#                 bin/, and the same list check over bin/, must refuse them.
#                 Given --unsigned or --ships-unsigned it is not applicable,
#                 as for Windows
#  13. installer  windows/amd64 only. The installer in the --installer directory
#                 must have the SHA-256 the SHA256SUMS beside it lists. Run as
#                 `<installer> /S`, it must exit 0 within five minutes — a silent
#                 install that waits is one showing a window — and leave the
#                 folder Windows names as the user's local application data,
#                 under Programs\aggrega, holding aggrega.exe, byte for byte the
#                 default binary the installer was built from (the signed one, or
#                 with --unsigned the one in bin/), uninstall.exe and EnVar.dll,
#                 and nothing else; the user PATH holding that folder once; an
#                 uninstall entry in the user's list of installed apps naming
#                 Aggrega, the version staged, Cognitio S.r.l. as its publisher,
#                 the folder, and the uninstaller in it, with /S as its quiet
#                 form; and a new PowerShell, whose PATH is the machine's and the
#                 user's as Windows keeps them, must find aggrega in that folder
#                 by name and run it to report the version staged. Run again with
#                 /S over that install, as an upgrade in place is, the same must
#                 hold, the folder still on the PATH once. Unless --unsigned, the
#                 installer and each program file it installed must pass step
#                 12's signature check. Then `uninstall.exe /S` must leave, within
#                 five minutes, no folder, no uninstall entry, and a user PATH
#                 holding the entries it held before the install, in their order,
#                 empty entries set aside; and the same checks that passed after
#                 the install must refuse: no new PowerShell finds aggrega, the
#                 PATH does not hold the folder, there is no uninstall entry. The
#                 folder must not exist before the step, and the user PATH is put
#                 back as it was when the step ends, whatever ended it
#  14. winget     windows/amd64 only. In a fresh folder laid out as step 11's is,
#                 the installer among its files, scripts/render-package-channels.sh
#                 --channel winget renders the winget manifest the release renders
#                 from its own files: three files, Cognitio.Aggrega.yaml,
#                 Cognitio.Aggrega.installer.yaml and
#                 Cognitio.Aggrega.locale.en-US.yaml, and nothing else. The
#                 winget is the one a new Windows PowerShell finds, by name or
#                 where Windows places it for the user; when it finds none, the
#                 step installs, for the current user, the WinGet CLI release
#                 this script pins — its App Installer bundle and the x64
#                 packages it depends on, fetched from that release on GitHub,
#                 each refused unless it has the SHA-256 pinned for it —
#                 through Add-AppxPackage, and uses that one; the step says
#                 which ran. Then `winget validate --manifest <folder>
#                 --disable-interactivity`, run through Windows PowerShell,
#                 must exit 0, which it does only when the manifest has neither
#                 an error nor a warning. Then the same validation over a copy
#                 of the manifest whose installer type is one winget does not
#                 know must refuse it
#
# Each binary started in steps 1-8, 11 and 13 has every AGGREGA_ setting of the
# machine unset, the global configuration pointed at an empty directory
# (XDG_CONFIG_HOME and APPDATA both name it), and the offline posture on, so it
# reads no configuration of the machine and reaches no network; steps 4, 9, 12
# and 14 start none. Step 5 replaces that configuration directory with its own, and step
# 6 turns the posture off for its push and its
# pull alone, with the proxy settings naming a closed port on the loopback
# address: a request to the registry it started, on that address, goes direct,
# and any other request fails. Step 11 sets AGGREGA_MIRROR alone, and runs
# the install scripts with those proxy settings and NO_PROXY naming 127.0.0.1, so
# curl and PowerShell 7 reach the folder it serves and nothing else; Windows
# PowerShell reads the system's proxy settings rather than these. The tests run
# in the environment the shell was given; their own setup isolates what they
# read. Every PowerShell steps 11 to 14 start, Windows PowerShell and
# PowerShell 7 alike, runs with -NoProfile, so no profile of the machine shapes
# what it does.
#
# Usage:
#   scripts/platform-smoke.sh --target <goos>/<goarch> --inputs <dir> [--signed <dir> | --unsigned | --ships-unsigned] [--installer <dir>] [--binaries <dir>]
#   scripts/platform-smoke.sh --help
#
# --signed <dir> names the directory holding the signed binaries and SHA256SUMS.
# It is required for windows/amd64, darwin/arm64 and darwin/amd64, unless
# --unsigned is given, and refused for a linux target.
#
# --installer <dir> names the directory holding the Windows installer, under the
# name the build writes it, and SHA256SUMS: the signed installer, or with
# --unsigned the one built from the unsigned binary. It is required for
# windows/amd64 and refused for a darwin or linux target.
#
# --unsigned runs the windows/amd64 or darwin binaries staged in bin/, unsigned as
# the build wrote them: the way to run the checks on a run that signs nothing. It
# is refused with --signed and for a linux target. Each step then runs whatever an
# earlier one reported; step 12 checks nothing and is reported not applicable,
# never passed; step 13 checks no signature, saying so; and the run ends naming
# every step that failed and the step that was not applicable. It exits 1 when a step failed and 3 when none did — never
# 0, since what ran is not the signed binaries the release attaches.
#
# --ships-unsigned runs the darwin/arm64 or darwin/amd64 binaries staged in bin/,
# unsigned as the build wrote them, on a run whose release attaches them so: the
# way to run the checks while macOS signing is switched off. It is refused with
# --signed or --unsigned and for any other target. The run stops at the first
# failure, as with --signed; step 12 checks nothing and is reported not
# applicable, never passed; and the run exits 0 when every other step passed,
# since what ran is what the release attaches.
#
# --binaries <dir> runs every step with the binaries in <dir>, named as bin/ names
# them, in place of the ones it would run otherwise (the --signed ones for
# windows/amd64 and macOS, the staged ones for Linux and with --unsigned): the way to show
# which checks the binaries of another release fail. Each step then runs whatever
# an earlier one reported, and the run ends naming every step that failed. It
# exits 1 when one did and 3 when none did — never 0, since what passed is
# binaries the release does not carry. With --unsigned as well, step 12 is not
# applicable, as above.
#
# <goos>/<goarch> is windows/amd64, darwin/arm64, darwin/amd64 or linux/arm64. Writes its working
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

INPUTS="" SIGNED="" UNSIGNED="" SHIPS_UNSIGNED="" SUBSTITUTE="" INSTALLER=""
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
    --unsigned)
      UNSIGNED=1
      shift
      ;;
    --ships-unsigned)
      SHIPS_UNSIGNED=1
      shift
      ;;
    --installer)
      [ -n "${2:-}" ] || { fail "--installer needs the directory holding the installer and SHA256SUMS"; exit 2; }
      INSTALLER="$2"
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
# The release signs the Windows and macOS binaries and no others, so the signed
# directory is what a Windows or macOS run must be handed and what a Linux run must
# not be. A run that signs nothing says so with --unsigned in its place, and a
# Linux run takes neither.
[ -z "$SIGNED" ] || [ -z "$UNSIGNED" ] ||
  { fail "--signed and --unsigned cannot both be given: --signed runs the signed binaries in its directory, --unsigned the unsigned ones in bin/"; exit 2; }
# A release that attaches the macOS binaries unsigned: they run as --unsigned runs
# them, and the run is held as a signed one is, since they are what ships.
if [ -n "$SHIPS_UNSIGNED" ]; then
  [ -z "$SIGNED" ] && [ -z "$UNSIGNED" ] ||
    { fail "--ships-unsigned takes the place of --signed and --unsigned: it runs the unsigned binaries in bin/, which the release attaches as they are"; exit 2; }
  [ "${TARGET%/*}" = darwin ] ||
    { fail "--ships-unsigned applies to darwin/arm64 and darwin/amd64 alone, the binaries a release may attach unsigned"; exit 2; }
  UNSIGNED=1
fi
case "$GOOS_T" in
  windows | darwin)
    [ -n "$SIGNED" ] || [ -n "$UNSIGNED" ] || { fail "--signed <dir> is required for $TARGET: the binaries the release attaches are the signed ones; on a run that signs nothing, give --unsigned instead"; exit 2; }
    ;;
  *)
    [ -z "$SIGNED" ] || { fail "--signed applies to windows/amd64, darwin/arm64 and darwin/amd64; the release does not sign the $TARGET binaries"; exit 2; }
    [ -z "$UNSIGNED" ] || { fail "--unsigned applies to windows/amd64, darwin/arm64 and darwin/amd64; the release does not sign the $TARGET binaries, so every run of them runs them as staged"; exit 2; }
    ;;
esac
case "$GOOS_T" in
  windows)
    [ -n "$INSTALLER" ] || { fail "--installer <dir> is required for $TARGET: the release attaches an installer for it"; exit 2; }
    ;;
  *)
    [ -z "$INSTALLER" ] || { fail "--installer applies to windows/amd64 alone; the release carries no installer for $TARGET"; exit 2; }
    ;;
esac
# The runner names directories in the Windows form; this shell works in its own.
if command -v cygpath >/dev/null 2>&1; then
  INPUTS="$(cygpath -u "$INPUTS")"
  [ -z "$SIGNED" ] || SIGNED="$(cygpath -u "$SIGNED")"
  [ -z "$SUBSTITUTE" ] || SUBSTITUTE="$(cygpath -u "$SUBSTITUTE")"
  [ -z "$INSTALLER" ] || INSTALLER="$(cygpath -u "$INSTALLER")"
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
if [ -n "$INSTALLER" ]; then
  [ -d "$INSTALLER" ] || { fail "--installer names $INSTALLER, which is not a directory"; exit 2; }
  INSTALLER="$(cd "$INSTALLER" && pwd)"
fi
WORK="$INPUTS/run"
[ ! -e "$WORK" ] || { fail "$WORK already exists; give a freshly staged directory"; exit 2; }

TOOLS=(cat cmp cp dd find git head sleep sort tr uname)
[ "$GOOS_T" = darwin ] && TOOLS+=(chmod codesign curl date sh spctl uuidgen xattr)
[ "$GOOS_T" = linux ] && TOOLS+=(chmod curl sh)
[ "$GOOS_T" = windows ] && TOOLS+=(cygpath powershell.exe pwsh sha256sum timeout)
for tool in "${TOOLS[@]}"; do
  command -v "$tool" >/dev/null 2>&1 || { fail "$tool not found on PATH"; exit 1; }
done
# Step 11 serves its release folder with Python. Which name a machine gives it
# differs — Windows may carry only python — so each is tried, and held to the
# version the server needs by running it rather than by finding it.
PYTHON=""
for candidate in python3 python; do
  if command -v "$candidate" >/dev/null 2>&1 &&
    "$candidate" -c 'import sys; sys.exit(0 if sys.version_info >= (3, 7) else 1)' >/dev/null 2>&1; then
    PYTHON="$candidate"
    break
  fi
done
[ -n "$PYTHON" ] || { fail "neither python3 nor python on PATH runs Python 3.7 or later, which step 11 serves the release folder with"; exit 1; }

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
if [ -n "$SHIPS_UNSIGNED" ]; then
  echo "platform-smoke: --ships-unsigned: the release attaches the $TARGET binaries unsigned, so the binaries it runs are the unsigned ones in bin/, as the build wrote them, and step 12 is not applicable"
elif [ -n "$UNSIGNED" ]; then
  echo "platform-smoke: --unsigned: this run signs nothing, so the binaries it runs are the unsigned ones in bin/, as the build wrote them, and step 12 is not applicable"
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
  linux)
    case "$GOARCH_T" in
      arm64) want_machine=aarch64 ;;
    esac
    if [ "$MACHINE" != "$want_machine" ]; then
      fail "this machine reports $MACHINE: the $TARGET binaries are proved only on a Linux machine reporting $want_machine"
      exit 1
    fi
    ;;
esac

mkdir -p "$WORK"

if [ "$GOOS_T" = darwin ] || [ "$GOOS_T" = linux ]; then
  # A workflow artifact does not keep file modes, so each executable comes back
  # without its execute bit; and on macOS a file written by a program that marks
  # its downloads would carry the quarantine mark, which this run must not meet.
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
    if [ "$GOOS_T" = darwin ] && xattr -p com.apple.quarantine "$path" >/dev/null 2>&1; then
      fail "${path#"$INPUTS/"} carries the quarantine mark, so Gatekeeper would assess it before it runs"
      exit 1
    fi
  done
  if [ "$GOOS_T" = darwin ]; then
    echo "ok: ${#executables[@]} executable(s) marked executable, none carrying the quarantine mark"
  else
    echo "ok: ${#executables[@]} executable(s) marked executable"
  fi
fi

DEFAULT_BIN="$BIN_DIR/${BINARY_NAMES[0]}"
BINARIES=()
for name in "${BINARY_NAMES[@]}"; do BINARIES+=("$BIN_DIR/$name"); done
LOGS="$WORK/logs"
# Steps 5 to 8 and 11 work beneath this directory. Its last name carries a space and an
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
  if ! (cd "$WORK/mcp" && printf '%s\n' "$request" | "$DEFAULT_BIN" mcp > "$WORK/mcp.out" 2> "$WORK/mcp.err"); then
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
  smoke_build_example "$DEFAULT_BIN" "$WORK/example" "$RELEASE" "$log" --progress || return 1
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
        smoke_run_logged "$log" config-init "$DEFAULT_BIN" config init
      ) || { fail "config init with only APPDATA and LOCALAPPDATA to place its files did not succeed"; return 1; }
      ;;
    darwin | linux)
      mkdir -p "$base/home"
      config_file="$base/home/.config/aggrega/config.yaml"
      printed_file="$config_file"
      if [ "$GOOS_T" = darwin ]; then
        printed_cache="$base/home/Library/Caches/aggrega"
      else
        printed_cache="$base/home/.cache/aggrega"
      fi
      (
        unset XDG_CONFIG_HOME XDG_CACHE_HOME AGGREGA_CONFIG APPDATA LOCALAPPDATA
        export HOME="$base/home"
        cd "$base/cwd"
        smoke_run_logged "$log" config-init "$DEFAULT_BIN" config init
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
    smoke_run_logged "$log" push "$DEFAULT_BIN" --offline=false --workspace-root "$(smoke_native_path "$publish")" \
      push "$(smoke_native_path "$entity")" --registry "localhost:$port"
    cd "$consumer"
    smoke_run_logged "$log" pull "$DEFAULT_BIN" --offline=false --workspace-root "$(smoke_native_path "$consumer")" \
      pull --release "$RELEASE" --detail
  ) || { fail "the push to, or the pull from, the registry on this machine did not succeed"; return 1; }
  smoke_check_fetched "$log/pull.out" "$REGISTRY_REFERENCE" || { fail "the pull did not fetch the entity from the registry"; return 1; }
  smoke_check_entity_cache "$consumer" || { fail "the pull did not write the entity cache under names Windows accepts"; return 1; }

  ( cd "$consumer" && smoke_run_logged "$log" build "$DEFAULT_BIN" --workspace-root "$(smoke_native_path "$consumer")" build --release "$RELEASE" ) ||
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
  ( cd "$ws" && smoke_run_logged "$log" pull "$DEFAULT_BIN" --workspace-root "$(smoke_native_path "$ws")" pull --release "$RELEASE" --catalog catalog ) ||
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
  smoke_build_example "$DEFAULT_BIN" "$ws" "$CHART_RELEASE" "$LOGS/chart" || return 1
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

# user_path <save|holds|unchanged|same-entries|restore> — what Windows keeps as
# the user PATH, read and written through Windows PowerShell, with the folder
# step 11 or 13 installs into in SMOKE_INSTALL_FOLDER and the file the PATH is
# kept in in SMOKE_USER_PATH_SAVED, both in the Windows form. save keeps the PATH
# as it is now — its value, unexpanded, and its kind, or that there is none —
# refusing one that already holds the folder; holds refuses a PATH that does not
# hold the folder exactly once; unchanged refuses a PATH that is not the one
# kept; same-entries refuses a PATH whose entries, empty ones set aside, are not
# the kept one's in their order, or whose kind is not the kept one's when one was
# kept; restore writes the kept one back and refuses when what is then read
# differs from it.
# Each reports in a line of its own. The paths reach PowerShell through the
# environment rather than in the command's text, so no character of them can
# change what PowerShell runs, and are compared there, so no conversion between
# the shell's encoding and Windows' can change them either. Its strings are in
# single quotes, as authenticode_report's are, so the command's text carries no
# double quote for the passing of it to a Windows program to escape.
user_path() {
  local script
  script="\$ErrorActionPreference = 'Stop'; "
  script+="\$key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment'); "
  script+="\$options = [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames; "
  script+="\$value = \$key.GetValue('Path', \$null, \$options); "
  script+="if (\$null -eq \$value) { \$now = [pscustomobject]@{ Kind = 'Absent'; Value = '' } } "
  script+="else { \$now = [pscustomobject]@{ Kind = \$key.GetValueKind('Path').ToString(); Value = [string]\$value } }; "
  script+="\$folder = \$env:SMOKE_INSTALL_FOLDER; "
  script+="\$count = @(\$now.Value.Split(';') | Where-Object { \$_ -and \$_.TrimEnd('\\') -eq \$folder }).Count; "
  script+="switch (\$env:SMOKE_USER_PATH_ACTION) { "
  script+="'save' { if (\$count -ne 0) { throw ('the user PATH already holds ' + \$folder) }; "
  script+="\$now | Export-Clixml -LiteralPath \$env:SMOKE_USER_PATH_SAVED; "
  script+="'ok: kept the user PATH, a value of kind ' + \$now.Kind } "
  script+="'holds' { if (\$count -ne 1) { throw ('the user PATH holds ' + \$folder + ' ' + \$count + ' time(s), not once: ' + \$now.Value) }; "
  script+="'ok: the user PATH holds ' + \$folder + ' once' } "
  script+="'unchanged' { \$kept = Import-Clixml -LiteralPath \$env:SMOKE_USER_PATH_SAVED; "
  script+="if (\$kept.Kind -cne \$now.Kind -or \$kept.Value -cne \$now.Value) { throw ('the user PATH is not the one kept: ' + \$now.Kind + ' ' + \$now.Value) }; "
  script+="'ok: the user PATH is the one kept' } "
  script+="'same-entries' { \$kept = Import-Clixml -LiteralPath \$env:SMOKE_USER_PATH_SAVED; "
  script+="\$was = @(\$kept.Value.Split(';') | Where-Object { \$_ }) -join ';'; "
  script+="\$is = @(\$now.Value.Split(';') | Where-Object { \$_ }) -join ';'; "
  script+="if (\$was -cne \$is) { throw ('the user PATH does not hold the entries kept, in their order: ' + \$now.Value) }; "
  script+="if (\$kept.Kind -ne 'Absent' -and \$kept.Kind -cne \$now.Kind) { throw ('the user PATH is a value of kind ' + \$now.Kind + ', not ' + \$kept.Kind) }; "
  script+="'ok: the user PATH holds the entries kept, in their order, as a value of kind ' + \$now.Kind } "
  script+="'restore' { \$kept = Import-Clixml -LiteralPath \$env:SMOKE_USER_PATH_SAVED; "
  script+="if (\$kept.Kind -eq 'Absent') { \$key.DeleteValue('Path', \$false) } "
  script+="else { \$key.SetValue('Path', \$kept.Value, [Microsoft.Win32.RegistryValueKind]\$kept.Kind) }; "
  script+="\$back = \$key.GetValue('Path', \$null, \$options); "
  script+="if (\$kept.Kind -eq 'Absent') { if (\$null -ne \$back) { throw 'the user PATH was absent and could not be removed again' } } "
  script+="elseif (\$back -cne \$kept.Value -or \$key.GetValueKind('Path').ToString() -cne \$kept.Kind) { throw 'the user PATH could not be put back as it was' }; "
  script+="'ok: put the user PATH back as it was' } "
  script+="default { throw ('unknown action ' + \$env:SMOKE_USER_PATH_ACTION) } }"
  SMOKE_USER_PATH_ACTION="$1" MSYS_NO_PATHCONV=1 \
    powershell.exe -NoLogo -NoProfile -NonInteractive -Command "$script"
}

# The server step 11 starts and the user PATH it keeps, for the trap that stops
# the one and puts the other back when step 11 ends, whatever ended it.
INSTALL_SERVER_PID="" INSTALL_PATH_KEPT=""
end_install() {
  [ -z "$INSTALL_SERVER_PID" ] || kill "$INSTALL_SERVER_PID" 2>/dev/null || true
  if [ -n "$INSTALL_PATH_KEPT" ]; then
    user_path restore || printf 'platform-smoke: could not put the user PATH back as it was\n' >&2
  fi
}

# install_run <log-dir> <url> <name> <sh | the PowerShell to run> <home> — run an
# install script the way a person does, the one for Linux and macOS through the
# shell, the one for Windows through the PowerShell named, reading the release
# folder <url> serves, into the directory <home> stands for: HOME on Linux and
# macOS, LOCALAPPDATA on Windows, with the temporary folder beside it. Logged as
# <name>; returns the script's exit status.
#
# The script reaches the folder through the one setting naming a mirror of the
# releases, AGGREGA_MIRROR, and nothing else: a closed port on the loopback address
# is the proxy for every other address, as in step 6, and NO_PROXY lets a request
# to the folder's address go direct. Step 6 needs no such list, since Go never
# sends a request for a loopback address through a proxy; curl and PowerShell 7
# would. Windows PowerShell reads the system's proxy settings instead.
install_run() {
  local log="$1" url="$2" name="$3" shell="$4" home="$5" native_home native_temp
  local -a reach=(
    AGGREGA_MIRROR="$url"
    HTTP_PROXY=http://127.0.0.1:9 HTTPS_PROXY=http://127.0.0.1:9 http_proxy=http://127.0.0.1:9 https_proxy=http://127.0.0.1:9
    NO_PROXY=127.0.0.1 no_proxy=127.0.0.1
  )
  case "$shell" in
    sh)
      (
        cd "$home"
        # shellcheck disable=SC2016 # $1 is the inner shell's: the URL, passed as an argument.
        smoke_run_captured "$log" "$name" env "${reach[@]}" HOME="$home" TMPDIR="$home.tmp" \
          bash -c 'set -o pipefail; curl -fsSL "$1/latest/download/install.sh" | sh' install "$url"
      )
      ;;
    *)
      native_home="$(smoke_native_path "$home")"
      native_temp="$(smoke_native_path "$home.tmp")"
      (
        cd "$home"
        # MSYS_NO_PATHCONV keeps the Git for Windows shell from rewriting the
        # command's text, which it could take for a path, on its way to PowerShell.
        export MSYS_NO_PATHCONV=1
        smoke_run_captured "$log" "$name" env "${reach[@]}" LOCALAPPDATA="$native_home" TEMP="$native_temp" TMP="$native_temp" \
          "$shell" -NoLogo -NoProfile -NonInteractive -Command "irm $url/latest/download/install.ps1 | iex"
      )
      ;;
  esac
}

# lay_out_release_folder <folder> — make <folder> and lay it out as the release
# attaches its files for this target: the binaries this run runs, under the names
# the release gives them; on Windows the installer --installer names, under its
# own; and the two install scripts this checkout carries, under theirs. Then
# SHA256SUMS over them, by the writer the release step writes its own with. Steps
# 11 and 14 read folders it lays out.
lay_out_release_folder() {
  local folder="$1" sources="$CLI_ROOT/packaging/install" name
  for name in install.sh install.ps1; do
    [ -f "$sources/$name" ] || { fail "this checkout holds no packaging/install/$name"; return 1; }
  done
  mkdir -p "$folder"
  for name in "${BINARY_NAMES[@]}"; do
    cp "$BIN_DIR/$name" "$folder/$(smoke_release_asset_name "$TARGET" "$VERSION" "$name")"
  done
  if [ -n "$INSTALLER" ]; then
    name="$(smoke_target_installer "$TARGET")" || return 1
    cp "$INSTALLER/$name" "$folder/$(smoke_release_asset_name "$TARGET" "$VERSION" "$name")"
  fi
  cp "$sources/install.sh" "$sources/install.ps1" "$folder/"
  smoke_write_tree_sums "$folder"
}

check_install() {
  local log="$LOGS/install" base="$ODD/install" tag="v$VERSION"
  local folder asset="" listed_sum="" port="" url run shell label home dir installed text
  local -a shells=()
  folder="$base/release/download/$tag"
  mkdir -p "$log"

  lay_out_release_folder "$folder" || return 1
  cat "$folder/SHA256SUMS"
  asset="$(smoke_release_asset_name "$TARGET" "$VERSION" "${BINARY_NAMES[0]}")"
  listed_sum="$(smoke_listed_digest "$folder/SHA256SUMS" "$asset")" ||
    { fail "the release folder's SHA256SUMS lists no $asset"; return 1; }

  : > "$log/server.log"
  smoke_serve_release_folder "$PYTHON" "$base/release" "$tag" "$base/port" "$log/server.log" ||
    { fail "the release folder's server named no port; its log is printed above"; return 1; }
  INSTALL_SERVER_PID="$SMOKE_SERVER_PID"
  trap end_install EXIT
  port="$SMOKE_SERVER_PORT"
  url="http://127.0.0.1:$port"
  echo "ok: $url serves the release folder, its latest release $tag"

  if [ "$GOOS_T" = windows ]; then
    shells=("powershell.exe:windows-powershell" "pwsh:powershell-7")
  else
    shells=("sh:sh")
  fi

  for run in "${shells[@]}"; do
    shell="${run%%:*}" label="${run#*:}"
    home="$base/$label"
    mkdir -p "$home" "$home.tmp"
    if [ "$GOOS_T" = windows ]; then
      dir="$home/Programs/aggrega" installed="$home/Programs/aggrega/aggrega.exe"
      export SMOKE_INSTALL_FOLDER SMOKE_USER_PATH_SAVED
      SMOKE_INSTALL_FOLDER="$(smoke_native_path "$dir")"
      SMOKE_USER_PATH_SAVED="$(smoke_native_path "$base/$label.user-path.xml")"
      user_path save || { fail "could not keep the user PATH before $label runs install.ps1"; return 1; }
      INSTALL_PATH_KEPT=1
    else
      dir="$home/.local/bin" installed="$home/.local/bin/aggrega"
    fi
    printf -- '--- %s: the latest release from %s\n' "$label" "$url"
    install_run "$log" "$url" "$label" "$shell" "$home" || { fail "the install script under $label did not succeed"; return 1; }
    [ -f "$installed" ] || { fail "the install script under $label left no ${installed#"$home/"}"; return 1; }
    cmp -s "$BIN_DIR/${BINARY_NAMES[0]}" "$installed" ||
      { fail "the aggrega installed under $label is not the default binary the folder served"; return 1; }
    echo "ok: ${installed#"$home/"} is byte for byte the served $asset"
    smoke_check_only_entries "$dir" "$(basename "$installed")" ||
      { fail "the install folder holds more than aggrega after $label installed it"; return 1; }
    smoke_run_logged "$log" "$label-version" "$installed" version || { fail "the installed aggrega did not run"; return 1; }
    text="$(head -n 1 "$log/$label-version.out" | tr -d '\r')"
    case "$text" in
      "aggrega $VERSION ("*) echo "ok: the installed aggrega reports $VERSION" ;;
      *) fail "the aggrega installed under $label reports '$text', not aggrega $VERSION"; return 1 ;;
    esac
    if [ "$GOOS_T" = windows ]; then
      user_path holds || { fail "install.ps1 under $label did not put its folder on the user PATH once"; return 1; }
      user_path restore || { fail "could not put the user PATH back as it was after $label"; return 1; }
      INSTALL_PATH_KEPT=""
    else
      smoke_check_only_entries "$home.tmp" || { fail "install.sh left files in its temporary folder"; return 1; }
    fi
  done

  echo "The same install, with one byte of the served $asset changed, must be refused with nothing installed:"
  smoke_plant_one_byte "$folder/$asset" || return 1
  echo "planted: the first byte of download/$tag/$asset; SHA256SUMS still lists $listed_sum"
  for run in "${shells[@]}"; do
    shell="${run%%:*}" label="${run#*:}"
    home="$base/$label planted"
    mkdir -p "$home" "$home.tmp"
    if [ "$GOOS_T" = windows ]; then
      dir="$home/Programs/aggrega"
      SMOKE_INSTALL_FOLDER="$(smoke_native_path "$dir")"
      SMOKE_USER_PATH_SAVED="$(smoke_native_path "$base/$label planted.user-path.xml")"
      user_path save || { fail "could not keep the user PATH before $label runs install.ps1"; return 1; }
      INSTALL_PATH_KEPT=1
    else
      dir="$home/.local/bin"
    fi
    if install_run "$log" "$url" "$label-planted" "$shell" "$home"; then
      fail "the install script under $label installed a binary whose checksum is not the one SHA256SUMS lists"
      return 1
    fi
    smoke_check_checksum_refusal "$asset" "$listed_sum" "$log/$label-planted.out" "$log/$label-planted.err" ||
      { fail "the install script under $label did not refuse the planted binary for its checksum"; return 1; }
    [ ! -e "$dir" ] || { fail "the install script under $label refused the planted binary but left ${dir#"$home/"} behind"; return 1; }
    echo "ok: nothing was installed under $label"
    if [ "$GOOS_T" = windows ]; then
      user_path unchanged || { fail "install.ps1 under $label changed the user PATH although it installed nothing"; return 1; }
      user_path restore || { fail "could not put the user PATH back as it was after $label"; return 1; }
      INSTALL_PATH_KEPT=""
    else
      smoke_check_only_entries "$home.tmp" || { fail "install.sh did not delete the refused download"; return 1; }
    fi
  done
}

# The publisher every Windows and macOS binary must be signed as. It is stated here rather
# than read from anything the check looks at, and deliberately so, as step 5
# states its locations: a check that took the expected name from the signature it
# checks would accept whichever signer it found.
SIGNER_CN="Cognitio S.r.l."

# The entitlements every macOS binary must claim, and no other: the ones the
# property list in this checkout declares, the file the signing job signed with,
# since the checkout is at the commit the build that handed it over built.
ENTITLEMENTS="$CLI_ROOT/$(smoke_macos_entitlements)"

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

# codesign_report <file> <report> — write to <report> what macOS's own codesign
# says about the signature of <file> and the entitlements it claims, its standard
# output and its standard error together, as smoke_check_codesign_report reads it.
codesign_report() {
  codesign --display --verbose=2 --entitlements - --xml "$1" > "$2" 2>&1
}

# quarantine_copy <file> <copy> — copy <file> to <copy>, executable, carrying the
# quarantine mark a browser gives what it downloads, which is what makes
# Gatekeeper assess a program before it first runs.
quarantine_copy() {
  cp "$1" "$2"
  chmod 0755 "$2"
  xattr -w com.apple.quarantine "0081;$(printf '%08x' "$(date +%s)");Safari;$(uuidgen)" "$2"
}

# gatekeeper_report <file> <report> — write to <report> Gatekeeper's assessment
# of <file> as a program to run, its standard output and its standard error
# together, and return spctl's exit status.
gatekeeper_report() {
  local rc=0
  spctl --assess --type execute -vvv "$1" > "$2" 2>&1 || rc=$?
  return "$rc"
}

check_macos_signature() {
  local log="$LOGS/signature" name report copy rc bad=0
  mkdir -p "$log/quarantined"
  smoke_check_sums "$SIGNED/$SUMS_NAME" "$BIN_DIR" "${BINARY_NAMES[@]}" || bad=1
  echo "Gatekeeper on this machine: $(spctl --status 2>&1)"
  for name in "${BINARY_NAMES[@]}"; do
    printf -- '--- %s: codesign --verify --strict\n' "$name"
    if ! codesign --verify --strict --verbose=2 "$BIN_DIR/$name"; then
      fail "codesign does not accept the signature of $name"
      bad=1
    fi
    report="$log/$name.codesign.txt"
    printf -- '--- %s: codesign --display\n' "$name"
    if ! codesign_report "$BIN_DIR/$name" "$report"; then
      cat "$report"
      fail "codesign could not display the signature of $name"
      bad=1
      continue
    fi
    cat "$report"
    smoke_check_codesign_report "$report" "$SIGNER_CN" "$ENTITLEMENTS" || bad=1
    copy="$log/quarantined/$name"
    quarantine_copy "$BIN_DIR/$name" "$copy" || { fail "could not make a quarantined copy of $name"; bad=1; continue; }
    report="$log/$name.gatekeeper.txt"
    printf -- '--- %s, quarantined: spctl --assess --type execute\n' "$name"
    rc=0
    gatekeeper_report "$copy" "$report" || rc=$?
    cat "$report"
    if [ "$rc" != 0 ]; then
      fail "Gatekeeper refused the quarantined $name (spctl exited $rc)"
      bad=1
    fi
    smoke_check_gatekeeper_report "$report" || bad=1
  done
  if [ "$bad" != 0 ]; then
    fail "the binaries are not the listed ones, each signed with the Developer ID of $SIGNER_CN, hardened, timestamped, claiming the entitlements $(smoke_macos_entitlements) declares, and accepted by Gatekeeper as notarized"
    return 1
  fi

  echo "The same checks over a quarantined copy of the unsigned ${BINARY_NAMES[0]} staged in bin/ must refuse it:"
  report="$log/unsigned.codesign.txt"
  codesign_report "$INPUTS/bin/${BINARY_NAMES[0]}" "$report" || true
  cat "$report"
  if smoke_check_codesign_report "$report" "$SIGNER_CN" "$ENTITLEMENTS"; then
    fail "the signature check accepted the unsigned ${BINARY_NAMES[0]}, so its passes above prove nothing"
    return 1
  fi
  copy="$log/quarantined/unsigned-${BINARY_NAMES[0]}"
  quarantine_copy "$INPUTS/bin/${BINARY_NAMES[0]}" "$copy" ||
    { fail "could not make a quarantined copy of the unsigned ${BINARY_NAMES[0]}"; return 1; }
  report="$log/unsigned.gatekeeper.txt"
  rc=0
  gatekeeper_report "$copy" "$report" || rc=$?
  cat "$report"
  if [ "$rc" = 0 ] || smoke_check_gatekeeper_report "$report"; then
    fail "Gatekeeper accepted the quarantined unsigned ${BINARY_NAMES[0]}, so its acceptances above prove nothing"
    return 1
  fi
  echo "The same list check over the unsigned binaries in bin/ must refuse them:"
  if smoke_check_sums "$SIGNED/$SUMS_NAME" "$INPUTS/bin" "${BINARY_NAMES[@]}"; then
    fail "the list check accepted the unsigned binaries, so its pass above proves nothing"
    return 1
  fi
  echo "ok: the signature and Gatekeeper checks refuse an unsigned binary, and the list check the unsigned binaries"
}

# How long step 13 gives the installer, and the uninstaller, to finish.
INSTALLER_SECONDS=300

# installed_app <present|absent> — what the user's list of installed apps holds
# under the installer's uninstall entry, read through Windows PowerShell, with
# the install folder in SMOKE_INSTALL_FOLDER and the version staged in
# SMOKE_VERSION. present prints the entry's values and refuses one that is
# missing or does not name Aggrega, the version, Cognitio S.r.l., the folder,
# and the uninstaller in it, quoted, with /S as its quiet form; absent refuses
# an entry that is there. Its strings are in single quotes and the quote around
# the uninstaller's path is [char]34, as user_path's and authenticode_report's
# text carries no double quote.
installed_app() {
  local script
  script="\$ErrorActionPreference = 'Stop'; "
  script+="\$key = 'HKCU:\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\aggrega'; "
  script+="\$q = [string][char]34; "
  script+="\$uninstaller = Join-Path \$env:SMOKE_INSTALL_FOLDER 'uninstall.exe'; "
  script+="\$want = [ordered]@{ DisplayName = 'Aggrega'; DisplayVersion = \$env:SMOKE_VERSION; Publisher = 'Cognitio S.r.l.'; "
  script+="InstallLocation = \$env:SMOKE_INSTALL_FOLDER; UninstallString = \$q + \$uninstaller + \$q; QuietUninstallString = \$q + \$uninstaller + \$q + ' /S' }; "
  script+="switch (\$env:SMOKE_INSTALLED_APP) { "
  script+="'present' { if (-not (Test-Path -LiteralPath \$key)) { throw 'the list of installed apps holds no entry for aggrega' }; "
  script+="\$entry = Get-ItemProperty -LiteralPath \$key; "
  script+="foreach (\$name in \$want.Keys) { \$value = [string]\$entry.\$name; 'entry: ' + \$name + ' = ' + \$value; "
  script+="if (\$value -cne \$want[\$name]) { throw ('the entry' + [char]39 + 's ' + \$name + ' is ' + \$value + ', not ' + \$want[\$name]) } }; "
  script+="'ok: the list of installed apps holds the entry for aggrega' } "
  script+="'absent' { if (Test-Path -LiteralPath \$key) { throw 'the list of installed apps still holds an entry for aggrega' }; "
  script+="'ok: the list of installed apps holds no entry for aggrega' } "
  script+="default { throw ('unknown action ' + \$env:SMOKE_INSTALLED_APP) } }"
  SMOKE_INSTALLED_APP="$1" MSYS_NO_PATHCONV=1 \
    powershell.exe -NoLogo -NoProfile -NonInteractive -Command "$script"
}

# new_shell_version — run aggrega by name in a new Windows PowerShell whose PATH
# is the machine's and the user's, as Windows keeps them and gives a new
# session, rather than the PATH this shell inherited: it must find aggrega.exe in
# SMOKE_INSTALL_FOLDER, first, and run it as `aggrega version`. Prints where it
# found it and what it printed; returns non-zero when it finds none, finds
# another first, or the command fails.
new_shell_version() {
  local script
  script="\$ErrorActionPreference = 'Stop'; "
  script+="\$env:Path = [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' + [Environment]::GetEnvironmentVariable('Path', 'User'); "
  script+="\$found = @(Get-Command aggrega -CommandType Application -ErrorAction SilentlyContinue); "
  script+="if (\$found.Count -eq 0) { throw 'a new shell finds no aggrega on its PATH' }; "
  script+="\$want = Join-Path \$env:SMOKE_INSTALL_FOLDER 'aggrega.exe'; "
  script+="if (\$found[0].Source -ne \$want) { throw ('a new shell finds ' + \$found[0].Source + ' first, not ' + \$want) }; "
  script+="'found: ' + \$found[0].Source; "
  script+="& aggrega version; "
  script+="if (\$LASTEXITCODE -ne 0) { throw ('aggrega version exited ' + \$LASTEXITCODE) }"
  MSYS_NO_PATHCONV=1 powershell.exe -NoLogo -NoProfile -NonInteractive -Command "$script"
}

check_installer() {
  local log="$LOGS/installer" name installer built_from local_app native_folder folder run file report waited text
  mkdir -p "$log"
  name="$(smoke_target_installer "$TARGET")" || return 1
  smoke_check_sums "$INSTALLER/$SUMS_NAME" "$INSTALLER" "$name" ||
    { fail "the installer is not the one the SHA256SUMS beside it lists"; return 1; }
  installer="$INSTALLER/$name"
  # The installer carries the default binary as the release attaches it: signed,
  # or on a run that signs nothing as the build wrote it. --binaries changes what
  # the other steps run, not what the installer was built from.
  if [ -n "$UNSIGNED" ]; then
    built_from="$INPUTS/bin/${BINARY_NAMES[0]}"
  else
    built_from="$SIGNED/${BINARY_NAMES[0]}"
  fi

  # Where the installer puts aggrega: the folder Windows names as the user's local
  # application data, which the installer asks Windows for rather than reading
  # LOCALAPPDATA, under Programs\aggrega.
  local_app="$(MSYS_NO_PATHCONV=1 powershell.exe -NoLogo -NoProfile -NonInteractive -Command "[Environment]::GetFolderPath('LocalApplicationData')")" ||
    { fail "Windows PowerShell could not name the user's local application data folder"; return 1; }
  local_app="${local_app%$'\r'}"
  [ -n "$local_app" ] || { fail "Windows names no local application data folder for this user"; return 1; }
  native_folder="$local_app\\Programs\\aggrega"
  folder="$(cygpath -u "$native_folder")"
  if [ -e "$folder" ]; then
    fail "$native_folder exists before the installer runs, so what the installer leaves there could not be told from what was there"
    return 1
  fi
  export SMOKE_INSTALL_FOLDER SMOKE_USER_PATH_SAVED SMOKE_VERSION
  SMOKE_INSTALL_FOLDER="$native_folder"
  SMOKE_USER_PATH_SAVED="$(smoke_native_path "$log/user-path.xml")"
  SMOKE_VERSION="$VERSION"
  user_path save || { fail "could not keep the user PATH before the installer runs"; return 1; }
  INSTALL_PATH_KEPT=1
  trap end_install EXIT

  for run in install upgrade; do
    printf -- '--- %s /S (%s)\n' "$name" "$run"
    # MSYS_NO_PATHCONV keeps the Git for Windows shell from rewriting /S, which it
    # could take for a path, on its way to the installer.
    if ! smoke_run_captured "$log" "$run" env MSYS_NO_PATHCONV=1 timeout "$INSTALLER_SECONDS" "$installer" /S; then
      fail "$name /S ($run) did not exit 0 within $INSTALLER_SECONDS seconds; a silent install that does not finish is one waiting at a window"
      return 1
    fi
    smoke_check_only_entries "$folder" aggrega.exe uninstall.exe EnVar.dll ||
      { fail "after $name /S ($run), $native_folder does not hold exactly aggrega.exe, uninstall.exe and EnVar.dll"; return 1; }
    cmp -s "$built_from" "$folder/aggrega.exe" ||
      { fail "the aggrega.exe installed ($run) is not byte for byte ${built_from#"$INPUTS/"}, the default binary the installer was built from"; return 1; }
    echo "ok: aggrega.exe installed ($run) is byte for byte the default binary the installer was built from"
    user_path holds || { fail "after $name /S ($run), the user PATH does not hold $native_folder once"; return 1; }
    installed_app present || { fail "after $name /S ($run), the uninstall entry is not the one the installer writes"; return 1; }
    if ! new_shell_version > "$log/$run-new-shell.out" 2> "$log/$run-new-shell.err"; then
      cat "$log/$run-new-shell.out" "$log/$run-new-shell.err" >&2
      fail "after $name /S ($run), a new shell does not run the installed aggrega by name"
      return 1
    fi
    tr -d '\r' < "$log/$run-new-shell.out"
    text="$(tr -d '\r' < "$log/$run-new-shell.out")"
    case $'\n'"$text" in
      *$'\n'"aggrega $VERSION ("*) echo "ok: a new shell runs the installed aggrega by name, and it reports $VERSION" ;;
      *) fail "the aggrega a new shell runs ($run) does not report aggrega $VERSION"; return 1 ;;
    esac
  done

  if [ -n "$UNSIGNED" ]; then
    echo "not applicable: --unsigned: the installer and what it installed are unsigned on a run that signs nothing, so their signatures are not checked"
  else
    for file in "$installer" "$folder/aggrega.exe" "$folder/uninstall.exe" "$folder/EnVar.dll"; do
      report="$log/$(basename "$file").signature.txt"
      printf -- '--- %s: Get-AuthenticodeSignature\n' "${file##*/}"
      authenticode_report "$file" "$report" ||
        { fail "Windows PowerShell could not report the signature of ${file##*/}"; return 1; }
      tr -d '\r' < "$report"
      smoke_check_authenticode_report "$report" "$SIGNER_CN" ||
        { fail "${file##*/} is not signed as $SIGNER_CN with a timestamp Windows reports Valid"; return 1; }
    done
  fi

  printf -- '--- uninstall.exe /S\n'
  if ! smoke_run_captured "$log" uninstall env MSYS_NO_PATHCONV=1 timeout "$INSTALLER_SECONDS" "$folder/uninstall.exe" /S; then
    fail "uninstall.exe /S did not exit 0 within $INSTALLER_SECONDS seconds"
    return 1
  fi
  # The uninstaller copies itself to a temporary folder and finishes from there,
  # so that the copy in the install folder can be removed: the command above
  # returns before the uninstall is done.
  for ((waited = 0; waited < INSTALLER_SECONDS; waited++)); do
    [ -e "$folder" ] || break
    sleep 1
  done
  if [ -e "$folder" ]; then
    ls -la "$folder" >&2 || true
    fail "$native_folder is still there $INSTALLER_SECONDS seconds after uninstall.exe /S"
    return 1
  fi
  echo "ok: uninstall.exe /S removed $native_folder within $waited second(s)"
  installed_app absent || { fail "uninstall.exe /S left the uninstall entry"; return 1; }
  user_path same-entries || { fail "uninstall.exe /S did not leave the user PATH holding the entries it held before the install"; return 1; }

  echo "The checks that passed after the install must now refuse:"
  if new_shell_version > "$log/uninstalled-new-shell.out" 2> "$log/uninstalled-new-shell.err"; then
    fail "a new shell still runs an aggrega by name after the uninstall, so the check above proves nothing"
    return 1
  fi
  if user_path holds; then
    fail "the PATH check still finds $native_folder after the uninstall, so its pass above proves nothing"
    return 1
  fi
  if installed_app present; then
    fail "the uninstall entry check still passes after the uninstall, so its pass above proves nothing"
    return 1
  fi
  echo "ok: after the uninstall no new shell finds aggrega, the PATH does not hold the folder, and there is no uninstall entry"
  user_path restore || { fail "could not put the user PATH back as it was"; return 1; }
  INSTALL_PATH_KEPT=""
}

# The files of the winget manifest the release renders, as the winget community
# repository names them.
WINGET_FILES=(Cognitio.Aggrega.yaml Cognitio.Aggrega.installer.yaml Cognitio.Aggrega.locale.en-US.yaml)

# The WinGet CLI step 14 installs for the current user when this machine carries
# none: release v1.29.380 of microsoft/winget-cli, its App Installer bundle and
# the archive of the packages that bundle depends on, each held to the SHA-256 the
# release publishes beside it (in its .txt file of the same name, and as GitHub's
# digest of the asset), and the three x64 packages that archive holds, which its
# DesktopAppInstaller_Dependencies.json names, each held to a SHA-256 of its own.
# Moving to another release changes every line below together.
WINGET_RELEASE_URL=https://github.com/microsoft/winget-cli/releases/download/v1.29.380
WINGET_BUNDLE=Microsoft.DesktopAppInstaller_8wekyb3d8bbwe.msixbundle
WINGET_BUNDLE_SHA256=65dea9c01ce08ee7b763366b27c0e651f97db857c11ca9b9c301826c10092f2e
WINGET_DEPENDENCIES=DesktopAppInstaller_Dependencies.zip
WINGET_DEPENDENCIES_SHA256=ba875afe9d190f61218985ac0292a99d1db710bf93e13c68944ca9d89f0d82d1
WINGET_DEPENDENCIES_X64=(
  "9c17b521f9d690a1f504da5108ed6eec5669eb3a8fd1331eef43e40d84e74283  Microsoft.VCLibs.140.00_14.0.33519.0_x64.appx"
  "077a3d1a5d0622bd3004dca85f5e192d6e98ec79b83d4aa06766759ea6c09c3d  Microsoft.VCLibs.140.00.UWPDesktop_14.0.33728.0_x64.appx"
  "a31595cc4b5aebc18466ec24e8d4b566fe0fcafb52d833b6d139b8691d0e5177  Microsoft.WindowsAppRuntime.1.8_8000.616.304.0_x64.appx"
)

# winget_find — print the path of the winget this machine carries, and return 0;
# return 1, printing nothing, when it carries none. A new Windows PowerShell looks
# it up by name first, then where Windows places the command for the current user,
# %LOCALAPPDATA%\Microsoft\WindowsApps\winget.exe, which a PATH may leave out.
winget_find() {
  local script found
  script="\$found = @(Get-Command winget -CommandType Application -ErrorAction SilentlyContinue); "
  script+="if (\$found.Count -gt 0) { \$found[0].Source; exit 0 }; "
  script+="\$alias = Join-Path \$env:LOCALAPPDATA 'Microsoft\\WindowsApps\\winget.exe'; "
  script+="if (Test-Path -LiteralPath \$alias) { \$alias; exit 0 }; "
  script+="exit 1"
  found="$(MSYS_NO_PATHCONV=1 powershell.exe -NoLogo -NoProfile -NonInteractive -Command "$script")" || return 1
  found="${found%$'\r'}"
  [ -n "$found" ] || return 1
  printf '%s\n' "$found"
}

# winget_install <dir> — install the WinGet CLI pinned above for the current user:
# fetch the bundle and the archive into <dir>, hold each to its pinned SHA-256
# through the smoke library's one reader of SHA-256 lists, take the archive's x64
# packages out with Windows PowerShell's Expand-Archive and hold them to theirs the
# same way, and register the bundle with those packages as its dependencies through
# Add-AppxPackage. Refuses, before anything is installed, a download whose digest
# is not the pinned one.
winget_install() {
  local dir="$1" pair script file
  command -v curl >/dev/null 2>&1 || { fail "curl not found on PATH, which fetches the pinned WinGet CLI"; return 1; }
  mkdir -p "$dir/x64"
  for file in "$WINGET_BUNDLE" "$WINGET_DEPENDENCIES"; do
    curl -sSfL --retry 3 -o "$dir/$file" "$WINGET_RELEASE_URL/$file" || { fail "could not download $WINGET_RELEASE_URL/$file"; return 1; }
  done
  printf '%s  %s\n' "$WINGET_BUNDLE_SHA256" "$WINGET_BUNDLE" "$WINGET_DEPENDENCIES_SHA256" "$WINGET_DEPENDENCIES" > "$dir/pinned.sums"
  smoke_check_sums "$dir/pinned.sums" "$dir" "$WINGET_BUNDLE" "$WINGET_DEPENDENCIES" ||
    { fail "the WinGet CLI downloaded is not the release this script pins"; return 1; }
  script="\$ErrorActionPreference = 'Stop'; \$ProgressPreference = 'SilentlyContinue'; "
  script+="Expand-Archive -LiteralPath \$env:SMOKE_WINGET_ARCHIVE -DestinationPath \$env:SMOKE_WINGET_EXPANDED"
  SMOKE_WINGET_ARCHIVE="$(smoke_native_path "$dir/$WINGET_DEPENDENCIES")" SMOKE_WINGET_EXPANDED="$(smoke_native_path "$dir/expanded")" \
    MSYS_NO_PATHCONV=1 powershell.exe -NoLogo -NoProfile -NonInteractive -Command "$script" ||
    { fail "Windows PowerShell could not expand $WINGET_DEPENDENCIES"; return 1; }
  : > "$dir/x64.sums"
  for pair in "${WINGET_DEPENDENCIES_X64[@]}"; do
    printf '%s\n' "$pair" >> "$dir/x64.sums"
  done
  smoke_check_sums "$dir/x64.sums" "$dir/expanded/x64" "${WINGET_DEPENDENCIES_X64[@]#*  }" ||
    { fail "the x64 packages in $WINGET_DEPENDENCIES are not the ones this script pins"; return 1; }
  script="\$ErrorActionPreference = 'Stop'; \$ProgressPreference = 'SilentlyContinue'; "
  script+="\$dependencies = @(Get-ChildItem -LiteralPath \$env:SMOKE_WINGET_DEPENDENCIES -Filter *.appx | ForEach-Object { \$_.FullName }); "
  script+="Add-AppxPackage -Path \$env:SMOKE_WINGET_BUNDLE -DependencyPath \$dependencies; "
  script+="'registered: ' + (Get-AppxPackage -Name Microsoft.DesktopAppInstaller | ForEach-Object { \$_.PackageFullName })"
  SMOKE_WINGET_BUNDLE="$(smoke_native_path "$dir/$WINGET_BUNDLE")" SMOKE_WINGET_DEPENDENCIES="$(smoke_native_path "$dir/expanded/x64")" \
    MSYS_NO_PATHCONV=1 powershell.exe -NoLogo -NoProfile -NonInteractive -Command "$script"
}

# winget_validate <manifest folder> — run `winget validate` over <manifest
# folder> in Windows PowerShell, with the winget at SMOKE_WINGET, print the exit
# code winget gave, and return 0 when it is 0 — which winget gives only when the
# manifest has neither an error nor a warning — and 1 otherwise. winget's other
# codes are 32-bit values whose low byte, all this shell would see of them, could
# be 0. The folder and the winget reach PowerShell through the environment, as
# authenticode_report's file does.
winget_validate() {
  local script
  script="\$ErrorActionPreference = 'Continue'; "
  script+="'winget: ' + \$env:SMOKE_WINGET; "
  script+="& \$env:SMOKE_WINGET --version; "
  script+="& \$env:SMOKE_WINGET validate --manifest \$env:SMOKE_WINGET_MANIFEST --disable-interactivity; "
  script+="\$code = \$LASTEXITCODE; "
  script+="'winget validate exited ' + \$code + ' (0x' + ('{0:X8}' -f \$code) + ')'; "
  script+="if (\$code -eq 0) { exit 0 } else { exit 1 }"
  SMOKE_WINGET_MANIFEST="$(smoke_native_path "$1")" MSYS_NO_PATHCONV=1 \
    powershell.exe -NoLogo -NoProfile -NonInteractive -Command "$script"
}

check_winget() {
  local log="$LOGS/winget" base="$WORK/winget" folder manifest planted text file
  folder="$base/release" manifest="$base/channels/winget" planted="$base/planted"
  mkdir -p "$log" "$base/channels" "$planted"
  # The winget the machine carries, or else the pinned one, installed here.
  export SMOKE_WINGET
  if SMOKE_WINGET="$(winget_find)"; then
    echo "winget: the one this machine carries, $SMOKE_WINGET"
  else
    echo "winget: this machine carries none, so the pinned WinGet CLI from $WINGET_RELEASE_URL is installed for this user"
    smoke_run_captured "$log" winget-install winget_install "$base/winget-cli" ||
      { fail "the pinned WinGet CLI could not be installed"; return 1; }
    SMOKE_WINGET="$(winget_find)" ||
      { fail "the pinned WinGet CLI was installed, but no new Windows PowerShell finds winget"; return 1; }
    echo "winget: the pinned WinGet CLI this step installed, $SMOKE_WINGET"
  fi
  lay_out_release_folder "$folder" || return 1
  smoke_run_captured "$log" render bash "$CLI_ROOT/scripts/render-package-channels.sh" --channel winget "$folder" "$VERSION" "$base/channels" ||
    { fail "scripts/render-package-channels.sh --channel winget did not render the winget manifest from the release folder"; return 1; }
  smoke_check_only_entries "$manifest" "${WINGET_FILES[@]}" ||
    { fail "the winget manifest rendered is not the three files the winget community repository takes"; return 1; }
  for file in "${WINGET_FILES[@]}"; do
    printf -- '--- %s\n' "$file"
    cat "$manifest/$file"
  done
  smoke_run_captured "$log" validate winget_validate "$manifest" ||
    { fail "winget validate does not accept the winget manifest rendered from the release folder"; return 1; }
  echo "ok: winget validate accepts the winget manifest rendered from the release folder, with neither an error nor a warning"

  echo "The same validation over a copy whose installer type is one winget does not know must refuse it:"
  cp "$manifest"/* "$planted/"
  text="$(cat "$planted/Cognitio.Aggrega.installer.yaml")"
  [[ "$text" == *"InstallerType: nullsoft"* ]] ||
    { fail "the installer manifest carries no 'InstallerType: nullsoft' to plant over"; return 1; }
  printf '%s\n' "${text/InstallerType: nullsoft/InstallerType: planted-unknown}" > "$planted/Cognitio.Aggrega.installer.yaml"
  if smoke_run_captured "$log" validate-planted winget_validate "$planted"; then
    fail "winget validate accepted an installer type it does not know, so its acceptance above proves nothing"
    return 1
  fi
  echo "ok: winget validate refuses the manifest with an installer type it does not know"
}

# Each step runs in a subshell, so an environment it changes, and a process it
# starts, end with it. The subshell is not the left side of || or &&: there the
# shell would stop honouring set -e inside it, and a failing command would no
# longer end the step. A failing step ends the run, unless --binaries named the
# binaries or --unsigned (not --ships-unsigned) was given, in which case the run goes on and names every
# failed step at its end. GO_ON says why a run goes on; RAN and NOT_CARRIED name,
# for its last lines, the binaries it ran in place of the ones the release
# attaches, and why those are not them.
FAILED=() NOT_APPLICABLE=()
GO_ON="" RAN="" NOT_CARRIED=""
if [ -n "$SUBSTITUTE" ]; then
  GO_ON="--binaries named the binaries" RAN="the binaries in $SUBSTITUTE" NOT_CARRIED="which the release does not carry"
elif [ -n "$UNSIGNED" ] && [ -z "$SHIPS_UNSIGNED" ]; then
  GO_ON="--unsigned has every step report its verdict" RAN="the unsigned binaries in $INPUTS/bin" NOT_CARRIED="which the release attaches only signed"
fi
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
  [ -n "$GO_ON" ] || exit 1
  printf 'platform-smoke %s: step failed; going on, since %s\n' "$TARGET" "$GO_ON" >&2
}

# A step that cannot apply to the binaries this run runs: it is headed like any
# other, says why it checks nothing, and is listed at the end as not applicable,
# neither among the steps that passed nor among those that failed.
not_applicable() { # <title> <why>
  step "$1"
  printf 'not applicable: %s\n' "$2"
  NOT_APPLICABLE+=("$1")
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
  run_step "11. install: irm <url>/install.ps1 | iex under Windows PowerShell and PowerShell 7, from a release folder on this machine" check_install isolated
else
  run_step "11. install: curl -fsSL <url>/install.sh | sh, from a release folder on this machine" check_install isolated
fi
if [ "$GOOS_T" = windows ] || [ "$GOOS_T" = darwin ]; then
  if [ "$GOOS_T" = windows ]; then
    SIGNATURE_STEP="12. signature: each binary signed as $SIGNER_CN, timestamped, and the one SHA256SUMS lists"
    SIGNATURE_CHECK=check_signature
  else
    SIGNATURE_STEP="12. signature: each binary signed with the Developer ID of $SIGNER_CN, hardened, timestamped, accepted by Gatekeeper as notarized, and the one SHA256SUMS lists"
    SIGNATURE_CHECK=check_macos_signature
  fi
  if [ -n "$SHIPS_UNSIGNED" ]; then
    not_applicable "$SIGNATURE_STEP" \
      "--ships-unsigned: the release attaches these binaries unsigned and not notarized, so there is no signature and no SHA256SUMS of a signing to hold them to; this step neither passed nor failed"
  elif [ -n "$UNSIGNED" ]; then
    not_applicable "$SIGNATURE_STEP" \
      "--unsigned: this run signs nothing, so there is no signature and no SHA256SUMS to hold the binaries to; this step neither passed nor failed"
  else
    run_step "$SIGNATURE_STEP" "$SIGNATURE_CHECK"
  fi
fi
if [ "$GOOS_T" = windows ]; then
  run_step "13. installer: $(smoke_target_installer "$TARGET") /S, an upgrade over it, and uninstall.exe /S" check_installer isolated
  run_step "14. winget: the winget manifest rendered from a release folder on this machine, and winget validate over it" check_winget
fi

if [ "${#FAILED[@]}" -gt 0 ]; then
  printf '\nplatform-smoke: %s: with %s, %d step(s) failed:\n' "$TARGET" "$RAN" "${#FAILED[@]}" >&2
  printf '  %s\n' "${FAILED[@]}" >&2
fi
if [ "${#NOT_APPLICABLE[@]}" -gt 0 ]; then
  printf '\nplatform-smoke: %s: not applicable, so neither passed nor failed:\n' "$TARGET" >&2
  printf '  %s\n' "${NOT_APPLICABLE[@]}" >&2
fi
[ "${#FAILED[@]}" -eq 0 ] || exit 1
if [ -n "$RAN" ]; then
  which_passed="every step"
  [ "${#NOT_APPLICABLE[@]}" -eq 0 ] || which_passed="every other step"
  printf '\nplatform-smoke: %s: %s passed with %s, %s, so the run does not pass\n' \
    "$TARGET" "$which_passed" "$RAN" "$NOT_CARRIED" >&2
  exit 3
fi
if [ "${#NOT_APPLICABLE[@]}" -gt 0 ]; then
  printf '\nplatform-smoke: %s: every applicable check passed\n' "$TARGET"
else
  printf '\nplatform-smoke: %s: every check passed\n' "$TARGET"
fi
