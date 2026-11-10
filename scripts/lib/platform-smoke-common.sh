#!/usr/bin/env bash
# aggrega-cli/scripts/lib/platform-smoke-common.sh
#
# The single home for what the platform smoke run's two scripts share, SOURCED
# (never executed) by both: scripts/stage-platform-smoke.sh, which stages a
# target's binaries and builds the reference on the machine that built the
# release, and scripts/platform-smoke.sh, which runs those binaries on a machine
# of the target platform and compares what it builds with that reference. The two
# sides read one table of targets, run one procedure and one comparison, so a
# difference between their trees is a difference between the platforms rather
# than between two scripts. The assertions the smoke run makes about what a
# command printed or wrote live here too, each a function of what it is given, so
# each can be run on its own over a captured output or a planted fault.
#
# The release workflow's jobs on its self-hosted runner source it as well. The
# publishing job attaches the signed Windows and macOS binaries only once they
# match the SHA-256 lists the Windows and macOS smoke runs held them to, through
# the same function, smoke_check_sums, so the two checks cannot read a list
# differently. The macOS signing job, which checks nothing out, writes its lists
# in the form that function reads, with shasum -a 256. And the
# folder in which those jobs hand files to each other is listed where files enter
# it by smoke_write_tree_sums, in the form smoke_check_sums reads, and held to
# that list where they leave by smoke_check_tree_sums, which reads it through
# smoke_check_sums. The SHA256SUMS a release attaches is written the same way, by
# smoke_write_tree_sums over the directory holding exactly the files the release
# attaches, and held to them by smoke_check_sums before the release is made; the
# names the release gives its binaries and its Windows installer are
# smoke_release_asset_name's. The
# Homebrew formula and the Scoop manifest each release writes for its default
# binaries take every digest from that list, through smoke_listed_digest, once
# smoke_check_tree_sums has held the list to the files. The smoke
# run's install step lays out a release the same way, with the same two
# functions, so the install scripts are proven against the names and the list a
# release carries.
#
# Sourcing defines these functions and sets no variables:
#
#   smoke_require_bash
#     Returns non-zero, saying which bash this is, unless it is bash 4.3 or later:
#     the functions below use associative arrays, name references and globstar,
#     none of which the bash 3.2 that macOS ships provides.
#
#   smoke_target_binaries <goos>/<goarch>
#     Prints, one per line, the file names of the binaries the release carries for
#     the target, as the build writes them: the default binary, without its debug
#     information, first, then the debug and the light ones; for windows/amd64,
#     darwin/arm64, darwin/amd64 and linux/arm64 alike. A target the smoke run
#     does not cover is refused with exit 2, naming the targets it does cover.
#
#   smoke_target_installer <goos>/<goarch>
#     Prints the file name of the installer the release carries for the target,
#     as the build writes it: aggrega-windows-amd64-setup.exe for windows/amd64,
#     the one target the release carries an installer for. Any other target is
#     refused with exit 2, naming it.
#
#   smoke_target_installer_parts <goos>/<goarch>
#     Prints, one per line, the program files the target's installer carries
#     beside the default binary, under the names the build writes them for
#     signing, which is done before the installer is built: uninstall.exe, the
#     uninstaller, and EnVar.dll, the plug-in that edits the user PATH, which the
#     installer runs and places beside aggrega.exe for the uninstaller to run.
#     Refused with exit 2 as smoke_target_installer is.
#
#   smoke_release_asset_name <goos>/<goarch> <version> <name>
#     Prints the name a release gives the binary the build wrote as <name> for the
#     target: <variant>-<version>-<goos>-<goarch>, with .exe for windows, where
#     <variant> is <name> without the target the build may have appended to it
#     and without .exe — so aggrega, as the build machine's own build writes it,
#     and aggrega-debug-darwin-arm64, as a --target build writes it, become
#     aggrega-<version>-linux-amd64 and aggrega-debug-<version>-darwin-arm64. An
#     installer, written as <variant>-<goos>-<goarch>-setup.exe, keeps -setup
#     after the target: aggrega-windows-amd64-setup.exe becomes
#     aggrega-<version>-windows-amd64-setup.exe. Returns 2, printing nothing, when
#     an argument is empty.
#
#   smoke_native_path <path>
#     Prints <path> in the form a native program on this system reads: the Windows
#     form under the Git for Windows shell (through cygpath), unchanged elsewhere.
#
#   smoke_serve_release_folder <python> <root> <tag> <port-file> <log-file>
#     Starts scripts/lib/serve-release-folder.py in the background with the Python
#     <python>, as `<python> -I -u`, serving <root> as a release's download folder
#     holding <tag>, every path given it in smoke_native_path's form, its standard
#     output and standard error appended to <log-file>; then waits up to 20 seconds
#     for it to write its port into <port-file>. Sets SMOKE_SERVER_PID to the
#     server's process and, once the port is written, SMOKE_SERVER_PORT to the
#     port, and returns 0; the caller stops the server. When the server exits
#     before writing the port, it stops waiting at once; when 20 seconds pass
#     without one, it asks the server, where the platform can, to write each of its
#     threads' stacks into <log-file>, and stops it. Either way it returns 1,
#     having printed whether the server exited, and with which status, or was
#     still running, then <log-file> under a line giving its size, so an empty
#     log is shown as empty rather than as nothing.
#
#   smoke_isolate <config-dir>
#     Prepares this shell's environment for every aggrega run that follows: unsets
#     every AGGREGA_ variable it inherited, so no setting of the machine reaches the
#     run; points the global configuration at <config-dir>, an empty directory,
#     through the variable each platform's rule reads first — XDG_CONFIG_HOME on
#     macOS and Linux, APPDATA on Windows — so no configuration file of the machine
#     is read either; and sets AGGREGA_OFFLINE, so a run refuses every network
#     reach rather than fetching what the example does not carry.
#
#   smoke_run_captured <log-dir> <name> <command> [<arg>...]
#     Runs the command with its standard output and its standard error each
#     redirected to a file of <log-dir>, <name>.out and <name>.err, as a run whose
#     output a log or another program reads, then prints both. Returns the
#     command's exit status, saying so when it is not 0.
#
#   smoke_run_logged <log-dir> <name> <command> [<arg>...]
#     Runs the command through smoke_run_captured, then holds both files to
#     smoke_refuse_escapes. Returns non-zero, saying which, when the command exits
#     non-zero or either stream carries an escape byte.
#
#   smoke_build_example <aggrega> <workspace> <release> <log-dir> [<arg>...]
#     Runs `validate --release`, `pull --release` and `build --release` for
#     <release> with the binary <aggrega>, from inside <workspace>, which it names as
#     the workspace root so nothing above it is read as part of the workspace, each
#     through smoke_run_logged into <log-dir>; each <arg> is passed to the pull and
#     the build. The pull re-resolves the dependencies into the lockfile the
#     workspace already carries; when the result is unchanged it keeps that
#     lockfile's generated_at, so the whole workspace, lockfile included, is
#     comparable byte for byte with a second machine's. Returns non-zero, naming
#     the command, on the first that fails.
#
#   smoke_compare_trees <expected> <actual>
#     Compares two directory trees: the set of names beneath each (a directory is
#     an entry of its own), then every file present in both, byte for byte. Prints
#     each name present on one side only and each file whose bytes differ, with
#     where they first differ, then one summary line. Returns 0 when the trees are
#     identical and 1 otherwise. Needs cmp, which the Git for Windows shell and
#     macOS both carry.
#
#   smoke_plant_one_byte <file>
#     Overwrites the first byte of <file> with a different byte, leaving its size
#     unchanged: the planted difference that shows smoke_compare_trees can fail,
#     and that a comparison of sizes alone would miss.
#
#   smoke_refuse_escapes <file>...
#     Returns 1, naming the file and the line, when any of the files holds the
#     escape byte (0x1b) that opens a terminal control sequence, which in a file
#     or a pipe is nothing but noise; 0, saying so, when none does.
#
#   smoke_refuse_shadow_notices <file>...
#     Returns 1, printing what it found, when a line of any of the files says that
#     a catalog file is shadowed — the notice a catalog directory read twice prints
#     about its own files; 0, saying so, when none does.
#
#   smoke_check_config_init <stdout> <stderr> <config-file> <printed-file> <printed-cache>
#     Holds what `aggrega config init` wrote and printed: a non-empty configuration
#     file at <config-file>, in this shell's form; on standard output the three
#     lines that report it — "Created the global configuration file at
#     <printed-file>", "Machine-wide caches are kept in <printed-cache>" and
#     "Registered registry hub.aggrega.ai in <printed-file>", each path in the
#     form the binary prints it; nothing on standard error; and no line in either
#     stream mentioning a warning. Returns 1, naming the first thing missing.
#
#   smoke_check_fetched <stdout> <reference>
#     Returns 1 unless the `pull --detail` report in <stdout> says that <reference>
#     was fetched fresh from a registry.
#
#   smoke_check_entity_cache <workspace>
#     Holds the entity cache of <workspace>, under .aggrega/cache, the directory a
#     workspace keeps it in when its configuration names no other: at least one
#     entity slot holds the entity it cached, and every name beneath .aggrega/cache
#     is one Windows accepts (smoke_names_windows_accepts). A slot is the documented
#     directory, entities/<kind>/<reference>/<version>/, and holds the entity in a
#     file named blob; only a blob at exactly that depth counts, so a binary that
#     lays a slot out at any other depth is refused. Returns 1, naming what failed.
#
#   smoke_names_windows_accepts
#     Reads relative paths, one per line, on standard input, and returns 1, naming
#     each, when a part of one carries a character Windows refuses in a file
#     name — < > : " \ | ? * or a control character — or ends in a space or a dot.
#
#   smoke_require_space_and_non_ascii <path>
#     Returns 1, saying what is missing, unless <path> holds both a space and a
#     byte outside ASCII.
#
#   smoke_check_sums <list> <dir> <name>...
#     Holds the files <name>... in <dir> to <list>, a SHA-256 list in the form
#     sha256sum writes ("<digest>  <name>", or "<digest> *<name>", one per line):
#     the list must name each <name> exactly once and nothing else, and each file
#     must be in <dir> with the digest the list gives it. Prints each file's
#     verdict; returns 1, naming every name missing from the list, every line that
#     names something else or is not in that form, every file missing from <dir>
#     and every digest that differs. Digests with sha256sum, which the Git for
#     Windows shell and Linux both carry, or with shasum -a 256 where sha256sum is
#     absent, as on macOS.
#
#   smoke_listed_digest <list> <name>
#     Prints the SHA-256 digest <list>, a list in the form smoke_check_sums
#     reads, gives <name>, reading each line through the same statement of that
#     form. Returns 1, printing nothing on standard output, when the list is
#     missing, holds a line not in that form, or names <name> other than exactly
#     once. It reads the list and nothing else: what holds the list to the bytes
#     it describes is smoke_check_sums.
#
#   smoke_write_tree_sums <dir>
#     Writes <dir>/SHA256SUMS: the SHA-256 of every file beneath <dir>, by its
#     path from <dir>, in bytewise order, in the text form sha256sum writes,
#     "<digest>  <name>", on every machine: where sha256sum writes its binary
#     form, "<digest> *<name>", as on Windows, each line is rewritten. It is
#     written under another name and then renamed, so a list present is a list
#     complete. Returns 1, writing no list, when <dir> holds anything that is
#     neither a file nor a directory, since no list can hold it, when it holds no
#     file, or when it already holds a list, since a list is written once, after
#     the last file is in place. Prints how many files it listed, and how many
#     bytes where du reports bytes. Digests with sha256sum, or with shasum -a 256
#     where sha256sum is absent, as on macOS; needs find, sort -z and mv beside
#     it, which the Linux machine that builds and publishes a release, the Git for
#     Windows shell and macOS all carry.
#
#   smoke_check_tree_sums <dir>
#     Holds every file beneath <dir> to <dir>/SHA256SUMS through
#     smoke_check_sums: the list must name each file but itself exactly once and
#     nothing else, each with the digest it gives, so a file changed, added or
#     removed since the list was written is refused. Returns 1, naming each, when
#     <dir> holds anything that is neither a file nor a directory, and otherwise
#     as smoke_check_sums does. Needs find and sort -z, and what smoke_check_sums
#     needs.
#
#   smoke_check_checksum_refusal <asset> <listed-digest> <file>...
#     Returns 0, saying so, when the files, read as one text with every space,
#     tab, carriage return and line feed taken out, hold the sentence the install
#     scripts refuse a download with when its digest is not the one SHA256SUMS
#     lists: "the SHA-256 checksum of the downloaded <asset> is <64 hexadecimal
#     digits>, but SHA256SUMS lists <listed-digest>; the download was deleted and
#     nothing was installed". The whitespace is taken out because a shell may
#     wrap an error message it prints. Returns 1, saying so, otherwise.
#
#   smoke_check_only_entries <dir> [<name>...]
#     Returns 1, naming each, unless <dir> is a directory and what lies beneath
#     it, hidden names included, is exactly the names given: none missing and
#     nothing else, at any depth. With no name, <dir> must be empty.
#
#   smoke_check_winget_warnings <report> <manifest-folder>
#     Holds what `winget validate --manifest <manifest-folder>` printed on standard
#     output, in <report>, when it exited with the code for a validation that
#     succeeded with warnings (APPINSTALLER_CLI_ERROR_MANIFEST_VALIDATION_WARNING,
#     0x8A150028). Returns 0, saying so, only when every finding winget printed —
#     each a line beginning "Manifest Warning: " or "Manifest Error: " — is the
#     warning a winget gives for a schema header naming a manifest version newer
#     than any it carries: "The schema header URL does not match the expected
#     pattern.", its value exactly the header line of the file it names, that
#     header exactly "# yaml-language-server:
#     $schema=https://aka.ms/winget-manifest.<ManifestType>.<ManifestVersion>.schema.json"
#     for that file's own ManifestType and ManifestVersion, and no file named
#     twice. Such a winget compares the header with the schema of the newest
#     version it carries, and so finds a correct header for a newer version not
#     matching; a winget that carries the version finds no such warning. Returns
#     1, naming each, on any error, any other warning, a header that is not that
#     exact line, a file not in <manifest-folder>, a finding line it cannot read,
#     or no finding at all. A carriage return ending a line, and any terminal
#     colour sequence in it, are set aside.
#
#   smoke_check_authenticode_report <report> <common-name>
#     Holds a report of what Windows' Get-AuthenticodeSignature says about one
#     file, written as key=value lines (a trailing carriage return set aside), to
#     a signature Windows accepts from the publisher <common-name>: status=Valid;
#     signer_subject naming <common-name> as its common name, first — exactly
#     "CN=<common-name>" or beginning "CN=<common-name>, ", so a longer name that
#     merely starts the same way is refused; and timestamper_subject not empty,
#     since a signature without a timestamp countersignature stops validating
#     when its certificate expires. Returns 1, naming each that fails; prints the
#     status, the signer and the timestamper when all hold. Other keys in the
#     report are read by nobody here and only printed by the caller.
#
#   smoke_macos_entitlements
#     Prints the path, from the module root, of the property list that declares
#     the entitlements every macOS binary the release carries is signed with:
#     packaging/macos/aggrega.entitlements. It declares one,
#     com.apple.security.cs.allow-unsigned-executable-memory, the hardened
#     runtime's exception that lets the WebAssembly runtime the binaries embed
#     make the memory it compiles plugins into executable. The build job hands
#     that file to the macOS signing job beside the binaries, and the macOS
#     smoke run holds what each signed binary claims to it.
#
#   smoke_macos_team
#     Prints the Apple team ID every macOS binary the release carries is signed
#     under: N4DSTL8KLH.
#
#   smoke_macos_developer_id
#     Prints the common name of the Developer ID Application certificate every
#     macOS binary the release carries is signed with, as Apple issues it, to
#     the registered legal name and the team smoke_macos_team prints:
#     "Developer ID Application: COGNITIO SRL (N4DSTL8KLH)". These two are the
#     one statement of the macOS signing identity the checks hold a signature
#     to; the release workflow's notarization-check job, which may run no
#     repository script, states the same two values in its env:, and a change
#     here is made there too.
#
#   smoke_plist_entitlements <file>
#     Prints the entitlements the property list in <file> declares, one
#     "<key>=true" or "<key>=false" line each, in bytewise order of their keys.
#     It reads <file> with every space, tab, carriage return and line feed taken
#     out, as one text, and from it what lies between the first "<plist …>" and
#     the last "</plist>": a dictionary, "<dict/>" or "<dict>…</dict>", holding
#     nothing but "<key>…</key>" entries each followed by "<true/>" or
#     "<false/>", no key twice — the standard XML form an entitlements file is
#     written in, and the form codesign prints the entitlements a signature
#     claims in when given --xml. Returns 2, printing nothing, when <file> holds
#     no property list, as codesign's report on a signature that claims no
#     entitlement holds none; and 1, saying why, when what it holds is anything
#     else, a value other than a boolean included, since an entitlement this
#     reader cannot read is one it cannot hold to anything.
#
#   smoke_check_codesign_report <report> <authority> <team> <entitlements>
#     Holds what macOS's codesign says about one file — `codesign --display
#     --verbose=2 --entitlements - --xml <file>`, its standard output and its
#     standard error together — to a Developer ID signature a Mac accepts for
#     distribution from one identity: the first Authority line is exactly
#     <authority>, as smoke_macos_developer_id prints it, so a longer name that
#     merely starts the same way is refused, and <authority> must end in
#     "(<team>)"; <team>, as smoke_macos_team prints it, is ten letters or
#     digits, and the TeamIdentifier line names exactly it; the CodeDirectory line's flags
#     include runtime, the hardened runtime notarization requires; a Timestamp
#     line is present, the secure timestamp notarization requires, where a
#     signature without one carries "Signed Time" instead; the entitlements the
#     report claims are exactly the ones the property list <entitlements>
#     declares, each with the same value, both read through
#     smoke_plist_entitlements, so an entitlement claimed beyond them and one of
#     them not claimed are each refused; and nothing in the report names the
#     com.apple.security.get-task-allow entitlement, which notarization refuses,
#     whatever <entitlements> declares. Returns 1, naming each that fails;
#     prints the authority, the team, the timestamp and the entitlements when
#     all hold.
#
#   smoke_check_notarization_report <report>
#     Holds what macOS's codesign says when asked whether one file is notarized
#     — `codesign --verify --strict -vvvv -R=notarized --check-notarization
#     <file>`, its standard output and its standard error together, the check
#     Apple's developer support gives for code that is neither an app, a disk
#     image nor an installer package, a bare executable among it — to a
#     notarized file: a line ending ": explicit requirement satisfied", and no
#     line saying the code failed to satisfy the requirement. codesign looks
#     the file's ticket up online. Gatekeeper's spctl --assess judges an app,
#     a disk image or an installer package, and refuses a bare executable as
#     not an app whatever its notarization, so it is not asked here. The
#     caller also holds codesign's exit status to 0. Returns 1, printing what
#     was found instead, otherwise. The release workflow's notarization-check
#     job holds its own reports to the same lines in a copy written inline,
#     deliberately: that job holds the notary key and so may run no repository
#     script, this library included. A change to what counts as notarized here
#     is made there too.

smoke_require_bash() {
  if [ "${BASH_VERSINFO[0]}" -gt 4 ] || { [ "${BASH_VERSINFO[0]}" -eq 4 ] && [ "${BASH_VERSINFO[1]}" -ge 3 ]; }; then
    return 0
  fi
  printf 'smoke: needs bash 4.3 or later (associative arrays, name references, globstar); this is bash %s at %s\n' \
    "$BASH_VERSION" "${BASH:-?}" >&2
  return 1
}

smoke_target_binaries() {
  case "$1" in
    windows/amd64)
      printf '%s\n' aggrega-windows-amd64.exe aggrega-debug-windows-amd64.exe aggrega-light-windows-amd64.exe
      ;;
    darwin/arm64 | darwin/amd64 | linux/arm64)
      printf '%s\n' "aggrega-${1%/*}-${1#*/}" "aggrega-debug-${1%/*}-${1#*/}" "aggrega-light-${1%/*}-${1#*/}"
      ;;
    *)
      printf 'smoke: no smoke run covers the target %s; it covers windows/amd64, darwin/arm64, darwin/amd64 and linux/arm64\n' "$1" >&2
      return 2
      ;;
  esac
}

smoke_target_installer() {
  case "$1" in
    windows/amd64) printf '%s\n' aggrega-windows-amd64-setup.exe ;;
    *)
      printf 'smoke: the release carries no installer for %s; it carries one for windows/amd64\n' "$1" >&2
      return 2
      ;;
  esac
}

smoke_target_installer_parts() {
  smoke_target_installer "$1" >/dev/null || return 2
  printf '%s\n' uninstall.exe EnVar.dll
}

smoke_release_asset_name() {
  local target="$1" version="$2" name="$3" goos goarch ext="" kind="" variant
  [ -n "$target" ] && [ -n "$version" ] && [ -n "$name" ] || return 2
  goos="${target%/*}" goarch="${target#*/}"
  [ "$goos" = windows ] && ext=.exe
  variant="${name%"$ext"}"
  case "$variant" in
    *"-$goos-$goarch-setup") kind=-setup variant="${variant%-setup}" ;;
  esac
  variant="${variant%"-$goos-$goarch"}"
  printf '%s-%s-%s-%s%s%s\n' "$variant" "$version" "$goos" "$goarch" "$kind" "$ext"
}

smoke_native_path() {
  if command -v cygpath >/dev/null 2>&1; then
    cygpath -w "$1"
  else
    printf '%s\n' "$1"
  fi
}

smoke_serve_release_folder() {
  local python="$1" root="$2" tag="$3" port_file="$4" log_file="$5" server i status="" state port
  server="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/serve-release-folder.py"
  SMOKE_SERVER_PID="" SMOKE_SERVER_PORT=""
  "$python" -I -u "$(smoke_native_path "$server")" "$(smoke_native_path "$root")" "$tag" \
    "$(smoke_native_path "$port_file")" >> "$log_file" 2>&1 &
  SMOKE_SERVER_PID=$!
  for ((i = 0; i < 200; i++)); do
    if [ -s "$port_file" ]; then break; fi
    if ! kill -0 "$SMOKE_SERVER_PID" 2>/dev/null; then
      wait "$SMOKE_SERVER_PID" || status=$?
      state="exited ${status:-0} before writing its port"
      break
    fi
    sleep 0.1
  done
  if [ -z "${state:-}" ] && [ -s "$port_file" ]; then
    read -r port < "$port_file"
    # shellcheck disable=SC2034 # read by the script that sourced this file
    SMOKE_SERVER_PORT="${port%$'\r'}"
    return 0
  fi
  if [ -z "${state:-}" ]; then
    state="was still running after 20 seconds without writing its port"
    # SIGUSR1 has the server write every thread's stack into its log. The Git for
    # Windows shell cannot deliver it to a native program, so it is not sent there.
    if ! command -v cygpath >/dev/null 2>&1; then
      if kill -USR1 "$SMOKE_SERVER_PID" 2>/dev/null; then sleep 1; fi
    fi
    kill "$SMOKE_SERVER_PID" 2>/dev/null || true
    wait "$SMOKE_SERVER_PID" 2>/dev/null || true
  fi
  printf 'smoke: the release folder server, %s %s, %s; its log, %s, holds %s bytes:\n' \
    "$python" "$server" "$state" "$log_file" "$(wc -c < "$log_file" | tr -d '[:space:]')" >&2
  cat "$log_file" >&2
  printf 'smoke: end of the release folder server log\n' >&2
  return 1
}

smoke_isolate() {
  local config_dir="$1" var native
  mkdir -p "$config_dir"
  for var in $(compgen -e); do
    case "$var" in AGGREGA_*) unset "$var" ;; esac
  done
  native="$(smoke_native_path "$config_dir")"
  XDG_CONFIG_HOME="$native"
  APPDATA="$native"
  export XDG_CONFIG_HOME APPDATA
  export AGGREGA_OFFLINE=true
}

smoke_run_captured() {
  local log_dir="$1" name="$2" status=0
  shift 2
  mkdir -p "$log_dir" || return 1
  "$@" > "$log_dir/$name.out" 2> "$log_dir/$name.err" || status=$?
  printf -- '--- %s: standard output\n' "$name"
  cat "$log_dir/$name.out"
  printf -- '--- %s: standard error\n' "$name"
  cat "$log_dir/$name.err"
  if [ "$status" != 0 ]; then
    printf 'smoke: %s exited %s\n' "$name" "$status" >&2
  fi
  return "$status"
}

smoke_run_logged() {
  local log_dir="$1" name="$2"
  smoke_run_captured "$@" || return 1
  smoke_refuse_escapes "$log_dir/$name.out" "$log_dir/$name.err"
}

smoke_build_example() {
  local bin="$1" workspace="$2" release="$3" log_dir="$4" root
  shift 4
  root="$(smoke_native_path "$workspace")"
  (
    cd "$workspace" || exit 1
    smoke_run_logged "$log_dir" validate "$bin" --workspace-root "$root" validate --release "$release" ||
      { printf 'smoke: validate --release %s failed in %s\n' "$release" "$workspace" >&2; exit 1; }
    smoke_run_logged "$log_dir" pull "$bin" --workspace-root "$root" pull --release "$release" --detail "$@" ||
      { printf 'smoke: pull --release %s failed in %s\n' "$release" "$workspace" >&2; exit 1; }
    smoke_run_logged "$log_dir" build "$bin" --workspace-root "$root" build --release "$release" "$@" ||
      { printf 'smoke: build --release %s failed in %s\n' "$release" "$workspace" >&2; exit 1; }
  )
}

# smoke_tree_entries <dir> <array-name> — fill the associative array named
# <array-name> with every entry beneath <dir>, relative to it, a directory
# suffixed with /. Hidden names are included. The comparison reads the entries as a
# set, so neither the order a directory lists them in nor the order a shell sorts
# them in can make two equal trees compare unequal.
# shellcheck disable=SC2034 # into is a name reference: its writes fill the caller's array.
smoke_tree_entries() {
  local dir="$1" entry
  local -n into="$2"
  local had_globstar=0 had_dotglob=0 had_nullglob=0
  shopt -q globstar && had_globstar=1
  shopt -q dotglob && had_dotglob=1
  shopt -q nullglob && had_nullglob=1
  shopt -s globstar dotglob nullglob
  pushd "$dir" >/dev/null || return 1
  for entry in **; do
    if [ -d "$entry" ] && [ ! -L "$entry" ]; then
      into["$entry/"]=1
    else
      into["$entry"]=1
    fi
  done
  popd >/dev/null || return 1
  [ "$had_globstar" = 1 ] || shopt -u globstar
  [ "$had_dotglob" = 1 ] || shopt -u dotglob
  [ "$had_nullglob" = 1 ] || shopt -u nullglob
}

smoke_compare_trees() {
  local expected="$1" actual="$2" entry where differences=0 files=0
  local -A in_expected=() in_actual=()
  if [ ! -d "$expected" ] || [ ! -d "$actual" ]; then
    printf 'compare: both trees must be directories: %s, %s\n' "$expected" "$actual" >&2
    return 1
  fi
  if ! command -v cmp >/dev/null 2>&1; then
    printf 'compare: cmp not found on PATH\n' >&2
    return 1
  fi
  smoke_tree_entries "$expected" in_expected || return 1
  smoke_tree_entries "$actual" in_actual || return 1
  for entry in "${!in_expected[@]}"; do
    if [ -z "${in_actual[$entry]+set}" ]; then
      printf 'compare: only in %s: %s\n' "$expected" "$entry"
      differences=$((differences + 1))
    fi
  done
  for entry in "${!in_actual[@]}"; do
    if [ -z "${in_expected[$entry]+set}" ]; then
      printf 'compare: only in %s: %s\n' "$actual" "$entry"
      differences=$((differences + 1))
      continue
    fi
    case "$entry" in */) continue ;; esac
    files=$((files + 1))
    if ! cmp -s "$expected/$entry" "$actual/$entry"; then
      where="$(cmp "$expected/$entry" "$actual/$entry" 2>&1)"
      printf 'compare: bytes differ: %s (%s)\n' "$entry" "${where##*: }"
      differences=$((differences + 1))
    fi
  done
  if [ "$differences" -gt 0 ]; then
    printf 'compare: %s and %s DIFFER: %d difference(s)\n' "$expected" "$actual" "$differences"
    return 1
  fi
  printf 'compare: %s and %s are identical: %d entries, %d files compared byte for byte\n' \
    "$expected" "$actual" "${#in_expected[@]}" "$files"
}

smoke_plant_one_byte() {
  local file="$1" first replacement=A out
  if [ ! -f "$file" ] || [ ! -s "$file" ]; then
    printf 'plant: %s is not a non-empty file\n' "$file" >&2
    return 1
  fi
  first="$(head -c 1 "$file")"
  [ "$first" = A ] && replacement=B
  # dd reports what it copied on standard error, and silencing it takes an operand
  # outside the set POSIX defines, so the report is captured instead and shown
  # only when the write fails.
  if ! out="$(printf '%s' "$replacement" | dd of="$file" bs=1 count=1 conv=notrunc 2>&1)"; then
    printf 'plant: could not write the first byte of %s: %s\n' "$file" "$out" >&2
    return 1
  fi
}

# The checks below read a file a line at a time with the C locale, so a byte is
# matched as a byte whatever the machine's locale makes of the bytes around it.

smoke_refuse_escapes() {
  local LC_ALL=C file line number found=0
  for file in "$@"; do
    number=0
    while IFS= read -r line || [ -n "$line" ]; do
      number=$((number + 1))
      case "$line" in
        *$'\033'*)
          printf 'smoke: %s, line %d, carries an escape byte: %s\n' "$file" "$number" "${line//$'\033'/\\033}" >&2
          found=1
          break
          ;;
      esac
    done < "$file"
  done
  [ "$found" = 0 ] || return 1
  printf 'ok: no escape byte in'
  for file in "$@"; do printf ' %s' "${file##*/}"; done
  printf '\n'
}

smoke_refuse_shadow_notices() {
  local file line found=0
  for file in "$@"; do
    while IFS= read -r line || [ -n "$line" ]; do
      case "$line" in
        *shadowed*)
          found=$((found + 1))
          [ "$found" -le 5 ] && printf 'smoke: %s says a catalog file is shadowed: %s\n' "${file##*/}" "${line%$'\r'}" >&2
          ;;
      esac
    done < "$file"
  done
  if [ "$found" -gt 0 ]; then
    printf 'smoke: %d line(s) say a catalog file is shadowed\n' "$found" >&2
    return 1
  fi
  printf 'ok: no line says a catalog file is shadowed\n'
}

# smoke_has_line <file> <line> — whether <file> holds <line> as a whole line, a
# trailing carriage return set aside.
smoke_has_line() {
  local line
  while IFS= read -r line || [ -n "$line" ]; do
    [ "${line%$'\r'}" = "$2" ] && return 0
  done < "$1"
  return 1
}

smoke_check_config_init() {
  local out="$1" err="$2" file="$3" printed_file="$4" printed_cache="$5" want line stream missing=0
  if [ ! -s "$file" ]; then
    printf 'smoke: config init left no configuration file at %s\n' "$file" >&2
    return 1
  fi
  for want in "Created the global configuration file at $printed_file" \
    "Machine-wide caches are kept in $printed_cache" \
    "Registered registry hub.aggrega.ai in $printed_file"; do
    if ! smoke_has_line "$out" "$want"; then
      printf 'smoke: config init did not report: %s\n' "$want" >&2
      missing=1
    fi
  done
  [ "$missing" = 0 ] || return 1
  if [ -s "$err" ]; then
    printf 'smoke: config init wrote to its standard error\n' >&2
    return 1
  fi
  for stream in "$out" "$err"; do
    while IFS= read -r line || [ -n "$line" ]; do
      case "$line" in
        *[Ww][Aa][Rr][Nn]*)
          printf 'smoke: config init printed a warning: %s\n' "${line%$'\r'}" >&2
          return 1
          ;;
      esac
    done < "$stream"
  done
  printf 'ok: config init wrote %s and reported it, the cache directory and the registry, with no warning\n' "$file"
}

smoke_check_fetched() {
  local line
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line%$'\r'}"
    case "$line" in
      *fetched-fresh*" $2" | *fetched-fresh*" $2 "*)
        printf 'ok: the pull fetched %s from the registry\n' "$2"
        return 0
        ;;
    esac
  done < "$1"
  printf 'smoke: the pull did not report fetching %s from a registry\n' "$2" >&2
  return 1
}

smoke_check_entity_cache() {
  local root="$1/.aggrega/cache" slot slots=0
  local -A entries=()
  if [ ! -d "$root/entities" ]; then
    printf 'smoke: %s holds no entity cache at %s\n' "$1" "$root/entities" >&2
    return 1
  fi
  # The depth is a deliberate copy of the documented slot layout,
  # entities/<kind>/<reference>/<version>/: a shell script cannot read the layout
  # the binary builds slots from, and the check stays independent of the binary on
  # purpose, since asking the binary where it wrote would let it grade itself. A
  # binary whose slots sit at another depth leaves this glob empty and is refused;
  # a change to the documented layout changes this line with it.
  for slot in "$root"/entities/*/*/*/blob; do
    [ -f "$slot" ] && slots=$((slots + 1))
  done
  if [ "$slots" = 0 ]; then
    printf 'smoke: no entity slot under %s holds a cached entity\n' "$root/entities" >&2
    return 1
  fi
  smoke_tree_entries "$root" entries || return 1
  printf '%s\n' "${!entries[@]}" | smoke_names_windows_accepts || return 1
  printf 'ok: %d entity slot(s) under %s, and each of the %d names beneath it is one Windows accepts\n' \
    "$slots" "$root" "${#entries[@]}"
}

smoke_names_windows_accepts() {
  local LC_ALL=C name part bad=0
  local -a parts
  while IFS= read -r name || [ -n "$name" ]; do
    name="${name%/}"
    [ -n "$name" ] || continue
    IFS='/' read -r -a parts <<<"$name"
    for part in "${parts[@]}"; do
      case "$part" in
        *'<'* | *'>'* | *':'* | *'"'* | *"\\"* | *'|'* | *'?'* | *'*'* | *[$'\001'-$'\037']* | *' ' | *'.')
          printf 'smoke: Windows refuses the name %s, in %s\n' "$part" "$name" >&2
          bad=1
          ;;
      esac
    done
  done
  return "$bad"
}

smoke_require_space_and_non_ascii() {
  local LC_ALL=C
  case "$1" in
    *' '*) ;;
    *) printf 'smoke: %s holds no space\n' "$1" >&2; return 1 ;;
  esac
  case "$1" in
    *[$'\200'-$'\377']*) ;;
    *) printf 'smoke: %s holds no byte outside ASCII\n' "$1" >&2; return 1 ;;
  esac
  printf 'ok: %s holds a space and a letter outside ASCII\n' "$1"
}

smoke_check_sums() {
  local LC_ALL=C list="$1" dir="$2" line digest name actual bad=0
  shift 2
  local -A wanted=() listed=()
  for name in "$@"; do wanted[$name]=1; done
  if [ ! -f "$list" ]; then
    printf 'smoke: there is no SHA-256 list at %s\n' "$list" >&2
    return 1
  fi
  local -a digest_command=()
  smoke_sha256_command digest_command || return 1
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line%$'\r'}"
    [ -n "$line" ] || continue
    if ! smoke_sums_line "$line"; then
      printf 'smoke: %s holds a line that is not "<SHA-256>  <name>": %s\n' "${list##*/}" "$line" >&2
      bad=1
      continue
    fi
    digest="${BASH_REMATCH[1]}"
    name="${BASH_REMATCH[2]}"
    if [ -z "${wanted[$name]+set}" ]; then
      printf 'smoke: %s lists %s, which is not one of the files it must list\n' "${list##*/}" "$name" >&2
      bad=1
    elif [ -n "${listed[$name]+set}" ]; then
      printf 'smoke: %s lists %s twice\n' "${list##*/}" "$name" >&2
      bad=1
    else
      listed[$name]="$digest"
    fi
  done < "$list"
  for name in "$@"; do
    if [ -z "${listed[$name]+set}" ]; then
      printf 'smoke: %s does not list %s\n' "${list##*/}" "$name" >&2
      bad=1
      continue
    fi
    if [ ! -f "$dir/$name" ]; then
      printf 'smoke: %s holds no %s, which %s lists\n' "$dir" "$name" "${list##*/}" >&2
      bad=1
      continue
    fi
    # Read from standard input, so the output carries no file name to unescape.
    actual="$("${digest_command[@]}" < "$dir/$name")" || { printf 'smoke: could not read %s\n' "$dir/$name" >&2; bad=1; continue; }
    actual="${actual%% *}"
    if [ "$actual" != "${listed[$name]}" ]; then
      printf 'smoke: %s has SHA-256 %s, but %s lists %s\n' "$name" "$actual" "${list##*/}" "${listed[$name]}" >&2
      bad=1
    else
      printf 'ok: %s has SHA-256 %s, as %s lists\n' "$name" "$actual" "${list##*/}"
    fi
  done
  [ "$bad" = 0 ] || return 1
  printf 'ok: %s lists exactly the %d file(s), and each in %s matches it\n' "${list##*/}" "$#" "$dir"
}

# smoke_sha256_command <array-name> — fill the indexed array named <array-name>
# with the command that digests SHA-256 on this machine: sha256sum, or shasum -a
# 256 where sha256sum is absent, as on macOS. Both write the lines a SHA-256 list
# holds, "<digest>  <name>", and both read standard input when given no file.
# Returns 1, saying so, when neither is on PATH. The one choice of the command,
# which smoke_check_sums and smoke_write_tree_sums both make through it.
# shellcheck disable=SC2034 # command_into is a name reference: its writes fill the caller's array.
smoke_sha256_command() {
  local -n command_into="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    command_into=(sha256sum)
  elif command -v shasum >/dev/null 2>&1; then
    command_into=(shasum -a 256)
  else
    printf 'smoke: neither sha256sum nor shasum is on PATH, so no SHA-256 can be computed\n' >&2
    return 1
  fi
}

# smoke_sums_line <line> — returns 0 when <line> is a line of a SHA-256 list in
# the form sha256sum writes, leaving the digest in BASH_REMATCH[1] and the name
# in BASH_REMATCH[2], and 1 otherwise. The one statement of that form, which
# smoke_check_sums and smoke_listed_digest both read lines through.
smoke_sums_line() {
  local LC_ALL=C re='^([0-9a-f]{64}) [ *](.+)$'
  [[ $1 =~ $re ]]
}

smoke_listed_digest() {
  local list="$1" want="$2" line digest="" count=0
  if [ ! -f "$list" ]; then
    printf 'smoke: there is no SHA-256 list at %s\n' "$list" >&2
    return 1
  fi
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line%$'\r'}"
    [ -n "$line" ] || continue
    if ! smoke_sums_line "$line"; then
      printf 'smoke: %s holds a line that is not "<SHA-256>  <name>": %s\n' "${list##*/}" "$line" >&2
      return 1
    fi
    if [ "${BASH_REMATCH[2]}" = "$want" ]; then
      count=$((count + 1))
      digest="${BASH_REMATCH[1]}"
    fi
  done < "$list"
  if [ "$count" -ne 1 ]; then
    printf 'smoke: %s lists %s %d time(s); it must list it exactly once\n' "${list##*/}" "$want" "$count" >&2
    return 1
  fi
  printf '%s\n' "$digest"
}

# smoke_tree_refuse_special <dir> — returns 1, naming each, when <dir> holds
# anything that is neither a file nor a directory: a symbolic link, a device, a
# pipe or a socket, none of which a SHA-256 list can hold.
smoke_tree_refuse_special() {
  local dir="$1" found
  if [ ! -d "$1" ]; then
    printf 'smoke: %s is not a directory\n' "$dir" >&2
    return 1
  fi
  if ! found="$(cd "$dir" && find . -mindepth 1 ! -type f ! -type d)"; then
    printf 'smoke: could not read every entry beneath %s\n' "$dir" >&2
    return 1
  fi
  [ -z "$found" ] && return 0
  printf 'smoke: %s holds what is neither a file nor a directory:\n%s\n' "$dir" "$found" >&2
  return 1
}

# smoke_tree_files <dir> <array-name> [<find-test>...] — fill the indexed array
# named <array-name> with every file beneath <dir> that the find tests given
# also select, by its path from <dir>, in bytewise order. It reads find's
# -print0 rather than a format of GNU find's own, and read -d '' rather than
# mapfile -d, so it runs with the find of macOS and with bash 4.3.
# shellcheck disable=SC2034 # files_into is a name reference: its writes fill the caller's array.
smoke_tree_files() {
  local dir="$1" path
  local -n files_into="$2"
  shift 2
  files_into=()
  while IFS= read -r -d '' path; do
    files_into+=("${path#./}")
  done < <(cd "$dir" && find . -type f "$@" -print0 | LC_ALL=C sort -z)
}

smoke_write_tree_sums() {
  local dir="$1" bytes listed line odd=""
  local -a files=() digest=()
  smoke_tree_refuse_special "$dir" || return 1
  if [ -e "$dir/SHA256SUMS" ] || [ -e "$dir/SHA256SUMS.partial" ]; then
    printf 'smoke: %s already holds a SHA-256 list; a list is written once, after the last file is in place\n' "$dir" >&2
    return 1
  fi
  smoke_tree_files "$dir" files
  if [ "${#files[@]}" -eq 0 ]; then
    printf 'smoke: %s holds no file\n' "$dir" >&2
    return 1
  fi
  smoke_sha256_command digest || return 1
  if ! listed="$(cd "$dir" && "${digest[@]}" -- "${files[@]}")"; then
    printf 'smoke: could not write the SHA-256 list of %s\n' "$dir" >&2
    return 1
  fi
  # sha256sum on Windows writes each line in its binary form, "<digest> *<name>";
  # every line is written here in the text form, "<digest>  <name>", read through
  # the one statement of a list's lines, so a list is the same on every machine
  # as the one a release attaches.
  while IFS= read -r line; do
    line="${line%$'\r'}"
    if ! smoke_sums_line "$line"; then
      odd=1
      break
    fi
    printf '%s  %s\n' "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}"
  done <<<"$listed" > "$dir/SHA256SUMS.partial"
  if [ -n "$odd" ]; then
    rm -f -- "$dir/SHA256SUMS.partial"
    printf 'smoke: %s wrote a line that is not "<SHA-256>  <name>" for %s: %s\n' "${digest[0]}" "$dir" "$line" >&2
    return 1
  fi
  if ! mv -- "$dir/SHA256SUMS.partial" "$dir/SHA256SUMS"; then
    rm -f -- "$dir/SHA256SUMS.partial"
    printf 'smoke: could not write the SHA-256 list of %s\n' "$dir" >&2
    return 1
  fi
  if bytes="$(du -sb "$dir" 2>/dev/null)"; then
    printf 'ok: %s: %d file(s), %s bytes, each listed in SHA256SUMS\n' "$dir" "${#files[@]}" "${bytes%%[[:space:]]*}"
  else
    printf 'ok: %s: %d file(s), each listed in SHA256SUMS\n' "$dir" "${#files[@]}"
  fi
}

smoke_check_tree_sums() {
  local dir="$1"
  local -a files=()
  smoke_tree_refuse_special "$dir" || return 1
  smoke_tree_files "$dir" files ! -path ./SHA256SUMS
  smoke_check_sums "$dir/SHA256SUMS" "$dir" "${files[@]}"
}

smoke_check_checksum_refusal() {
  local asset="$1" listed="$2" text="" file
  shift 2
  for file in "$@"; do
    [ -f "$file" ] || { printf 'smoke: there is no output to read at %s\n' "$file" >&2; return 1; }
    text+="$(tr -d ' \t\r\n' < "$file")"
  done
  # The parts in quotes are matched as they are written; only the digest the
  # script computed, which nobody here knows, is a pattern.
  if [[ $text =~ "theSHA-256checksumofthedownloaded${asset}is"[0-9a-f]{64}",butSHA256SUMSlists${listed};thedownloadwasdeletedandnothingwasinstalled" ]]; then
    printf 'ok: the download of %s was refused for a checksum other than the %s SHA256SUMS lists\n' "$asset" "$listed"
    return 0
  fi
  printf 'smoke: no refusal of %s for a checksum other than the %s SHA256SUMS lists\n' "$asset" "$listed" >&2
  return 1
}

smoke_check_only_entries() {
  local dir="$1" name bad=0
  local -A wanted=() found=()
  shift
  if [ ! -d "$dir" ]; then
    printf 'smoke: %s is not a directory\n' "$dir" >&2
    return 1
  fi
  for name in "$@"; do wanted[$name]=1; done
  smoke_tree_entries "$dir" found || return 1
  for name in "${!found[@]}"; do
    if [ -z "${wanted[${name%/}]+set}" ]; then
      printf 'smoke: %s holds %s, which it should not\n' "$dir" "$name" >&2
      bad=1
    fi
  done
  for name in "$@"; do
    if [ -z "${found[$name]+set}" ] && [ -z "${found[$name/]+set}" ]; then
      printf 'smoke: %s holds no %s\n' "$dir" "$name" >&2
      bad=1
    fi
  done
  [ "$bad" = 0 ] || return 1
  printf 'ok: %s holds %s\n' "$dir" "${*:-nothing}"
}

smoke_check_winget_warnings() {
  local report="$1" folder="$2" line rest value file name header type version expected bad=0 count=0
  local message="The schema header URL does not match the expected pattern."
  local -A seen=()
  if [ ! -f "$report" ]; then
    printf 'smoke: there is no winget validate report at %s\n' "$report" >&2
    return 1
  fi
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line%$'\r'}"
    # A terminal colour sequence winget may write around a line is taken out, so
    # the line is read for its words alone.
    while [[ "$line" =~ $'\e'\[[0-9\;]*[A-Za-z] ]]; do
      line="${line/"${BASH_REMATCH[0]}"/}"
    done
    case "$line" in
      "Manifest Warning: "*) ;;
      "Manifest Error: "*)
        printf 'smoke: winget reports an error: %s\n' "$line" >&2
        bad=1
        continue
        ;;
      *"Manifest Warning:"* | *"Manifest Error:"*)
        printf 'smoke: winget reports a finding this check cannot read: %s\n' "$line" >&2
        bad=1
        continue
        ;;
      *) continue ;;
    esac
    rest="${line#"Manifest Warning: "}"
    if [[ "$rest" != "$message Value: "* ]]; then
      printf 'smoke: winget warns of something other than a schema header: %s\n' "$line" >&2
      bad=1
      continue
    fi
    rest="${rest#"$message Value: "}"
    if [[ ! "$rest" =~ ^(.*)\ File:\ (.+)$ ]]; then
      printf 'smoke: winget'"'"'s schema header warning names no file: %s\n' "$line" >&2
      bad=1
      continue
    fi
    value="${BASH_REMATCH[1]}" file="${BASH_REMATCH[2]}"
    if [[ "$value" =~ ^(.*)\ Line:\ [0-9]+,\ Column:\ [0-9]+$ ]]; then
      value="${BASH_REMATCH[1]}"
    fi
    name="${file##*[\\/]}"
    if [ ! -f "$folder/$name" ]; then
      printf 'smoke: winget warns of a schema header in %s, which %s does not hold\n' "$file" "$folder" >&2
      bad=1
      continue
    fi
    if [ -n "${seen[$name]+set}" ]; then
      printf 'smoke: winget warns twice of the schema header in %s\n' "$name" >&2
      bad=1
      continue
    fi
    seen[$name]=1
    header="" type="" version=""
    while IFS= read -r rest || [ -n "$rest" ]; do
      rest="${rest%$'\r'}"
      case "$rest" in
        "# yaml-language-server: "*) [ -n "$header" ] || header="$rest" ;;
        "ManifestType: "*) type="${rest#ManifestType: }" ;;
        "ManifestVersion: "*) version="${rest#ManifestVersion: }" ;;
      esac
    done < "$folder/$name"
    expected="# yaml-language-server: \$schema=https://aka.ms/winget-manifest.$type.$version.schema.json"
    if [ -z "$type" ] || [ -z "$version" ] || [ "$header" != "$expected" ]; then
      printf 'smoke: the schema header of %s is "%s", not "%s"\n' "$name" "$header" "$expected" >&2
      bad=1
      continue
    fi
    if [ "$value" != "$header" ]; then
      printf 'smoke: winget warns of the schema header "%s" in %s, whose header is "%s"\n' "$value" "$name" "$header" >&2
      bad=1
      continue
    fi
    printf 'warned of: the schema header of %s, %s\n' "$name" "$header"
    count=$((count + 1))
  done < "$report"
  if [ "$count" = 0 ] && [ "$bad" = 0 ]; then
    printf 'smoke: winget reports warnings, but %s holds none this check reads\n' "$report" >&2
    bad=1
  fi
  [ "$bad" = 0 ] || return 1
  printf 'ok: every warning winget gives is for a correct schema header naming a manifest version newer than any it carries (%s file(s))\n' "$count"
}

smoke_check_authenticode_report() {
  local report="$1" cn="$2" line status="" subject="" stamper="" bad=0
  if [ -z "$cn" ]; then
    printf 'smoke: no publisher was named to hold the signature to\n' >&2
    return 1
  fi
  if [ ! -f "$report" ]; then
    printf 'smoke: there is no signature report at %s\n' "$report" >&2
    return 1
  fi
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line%$'\r'}"
    case "$line" in
      status=*) status="${line#status=}" ;;
      signer_subject=*) subject="${line#signer_subject=}" ;;
      timestamper_subject=*) stamper="${line#timestamper_subject=}" ;;
    esac
  done < "$report"
  if [ "$status" != Valid ]; then
    printf 'smoke: Windows reports the signature as %s, not Valid\n' "${status:-nothing}" >&2
    bad=1
  fi
  case "$subject" in
    "CN=$cn" | "CN=$cn, "*) ;;
    *)
      printf 'smoke: the signer certificate'"'"'s subject is %s, which does not name %s as its common name first\n' \
        "${subject:-empty}" "$cn" >&2
      bad=1
      ;;
  esac
  if [ -z "$stamper" ]; then
    printf 'smoke: the signature carries no timestamp countersignature\n' >&2
    bad=1
  fi
  [ "$bad" = 0 ] || return 1
  printf 'ok: Valid, signed by %s, timestamped by %s\n' "$subject" "$stamper"
}

smoke_macos_entitlements() {
  printf '%s\n' packaging/macos/aggrega.entitlements
}

smoke_macos_team() {
  printf '%s\n' N4DSTL8KLH
}

smoke_macos_developer_id() {
  printf 'Developer ID Application: COGNITIO SRL (%s)\n' "$(smoke_macos_team)"
}

smoke_plist_entitlements() {
  local LC_ALL=C file="$1" text rest key value
  local plist_re='<plist[^>]*>(.*)</plist>'
  local pair_re='^<key>([^<]+)</key><(true|false)/>(.*)$'
  local -A seen=()
  local -a declared_pairs=()
  if [ ! -f "$file" ]; then
    printf 'smoke: there is no file at %s to read entitlements from\n' "$file" >&2
    return 1
  fi
  text="$(tr -d ' \t\r\n' < "$file")"
  [[ $text =~ $plist_re ]] || return 2
  rest="${BASH_REMATCH[1]}"
  case "$rest" in
    '<dict/>') return 0 ;;
    '<dict>'*'</dict>')
      rest="${rest#<dict>}"
      rest="${rest%</dict>}"
      ;;
    *)
      printf 'smoke: the property list in %s holds no dictionary of entitlements, but: %s\n' "$file" "$rest" >&2
      return 1
      ;;
  esac
  while [ -n "$rest" ]; do
    if ! [[ $rest =~ $pair_re ]]; then
      printf 'smoke: the entitlements in %s hold something other than a key and a boolean value: %s\n' "$file" "$rest" >&2
      return 1
    fi
    key="${BASH_REMATCH[1]}" value="${BASH_REMATCH[2]}" rest="${BASH_REMATCH[3]}"
    if [ -n "${seen[$key]+set}" ]; then
      printf 'smoke: the entitlements in %s hold %s twice\n' "$file" "$key" >&2
      return 1
    fi
    seen[$key]=1
    declared_pairs+=("$key=$value")
  done
  [ "${#declared_pairs[@]}" -eq 0 ] || printf '%s\n' "${declared_pairs[@]}" | sort
}

smoke_check_codesign_report() {
  local LC_ALL=C report="$1" expected="$2" team="$3" entitlements="$4" line authority="" listed_team="" flags="" stamp="" bad=0
  local declared="" claimed="" rc entry
  local -A declared_set=() claimed_set=()
  local flags_re='^CodeDirectory .*flags=0x[0-9a-fA-F]+\(([^)]*)\)'
  local team_re='^[A-Z0-9]{10}$'
  if [ -z "$expected" ] || ! [[ $team =~ $team_re ]]; then
    printf 'smoke: no certificate name, or no team of ten letters or digits, was named to hold the signature to (%s, %s)\n' \
      "${expected:-none}" "${team:-none}" >&2
    return 1
  fi
  case "$expected" in
    "Developer ID Application: "*" ($team)") ;;
    *)
      printf 'smoke: %s is not a Developer ID Application certificate name ending in the team (%s)\n' "$expected" "$team" >&2
      return 1
      ;;
  esac
  if [ -z "$entitlements" ]; then
    printf 'smoke: no entitlements file was named to hold the signature'"'"'s entitlements to\n' >&2
    return 1
  fi
  if [ ! -f "$report" ]; then
    printf 'smoke: there is no signature report at %s\n' "$report" >&2
    return 1
  fi
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line%$'\r'}"
    case "$line" in
      Authority=*) [ -n "$authority" ] || authority="${line#Authority=}" ;;
      TeamIdentifier=*) listed_team="${line#TeamIdentifier=}" ;;
      Timestamp=*) stamp="${line#Timestamp=}" ;;
    esac
    if [[ $line =~ $flags_re ]]; then
      flags="${BASH_REMATCH[1]}"
    fi
    case "$line" in
      *com.apple.security.get-task-allow*)
        printf 'smoke: the signature claims the com.apple.security.get-task-allow entitlement, which notarization refuses: %s\n' "$line" >&2
        bad=1
        ;;
    esac
  done < "$report"
  if [ "$authority" != "$expected" ]; then
    printf 'smoke: the signing certificate is %s, not %s\n' "${authority:-absent}" "$expected" >&2
    bad=1
  fi
  if [ "$listed_team" != "$team" ]; then
    printf 'smoke: the signature names the team %s, not %s\n' "${listed_team:-nothing}" "$team" >&2
    bad=1
  fi
  case ",$flags," in
    *,runtime,*) ;;
    *)
      printf 'smoke: the code directory'"'"'s flags are %s, without runtime: the hardened runtime is not on\n' "${flags:-absent}" >&2
      bad=1
      ;;
  esac
  if [ -z "$stamp" ]; then
    printf 'smoke: the signature carries no secure timestamp\n' >&2
    bad=1
  fi
  if ! declared="$(smoke_plist_entitlements "$entitlements")"; then
    printf 'smoke: %s is not a property list of entitlements this check can read, so the entitlements the signature claims are held to nothing\n' "$entitlements" >&2
    bad=1
  else
    rc=0
    claimed="$(smoke_plist_entitlements "$report")" || rc=$?
    case "$rc" in
      0 | 2)
        while IFS= read -r entry; do
          [ -z "$entry" ] || declared_set[$entry]=1
        done <<<"$declared"
        while IFS= read -r entry; do
          [ -z "$entry" ] || claimed_set[$entry]=1
        done <<<"$claimed"
        # Each list is walked in its own sorted order, so what is refused is
        # named in the same order on every run.
        while IFS= read -r entry; do
          if [ -n "$entry" ] && [ -z "${declared_set[$entry]+set}" ]; then
            printf 'smoke: the signature claims the entitlement %s, which %s does not declare\n' "$entry" "$entitlements" >&2
            bad=1
          fi
        done <<<"$claimed"
        while IFS= read -r entry; do
          if [ -n "$entry" ] && [ -z "${claimed_set[$entry]+set}" ]; then
            printf 'smoke: the signature does not claim the entitlement %s, which %s declares\n' "$entry" "$entitlements" >&2
            bad=1
          fi
        done <<<"$declared"
        ;;
      *)
        printf 'smoke: the entitlements the signature claims cannot be read, so they cannot be held to %s\n' "$entitlements" >&2
        bad=1
        ;;
    esac
  fi
  [ "$bad" = 0 ] || return 1
  printf 'ok: signed by %s, team %s, hardened runtime, timestamped %s, claiming exactly the entitlements %s declares: %s\n' \
    "$authority" "$team" "$stamp" "$entitlements" "$(printf '%s' "$claimed" | tr '\n' ' ')"
}

smoke_check_notarization_report() {
  local report="$1" line satisfied=0 failed=0
  if [ ! -f "$report" ]; then
    printf 'smoke: there is no notarization report at %s\n' "$report" >&2
    return 1
  fi
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line%$'\r'}"
    case "$line" in
      *": explicit requirement satisfied") satisfied=1 ;;
      *"failed to satisfy"*) failed=1 ;;
    esac
  done < "$report"
  if [ "$satisfied" = 1 ] && [ "$failed" = 0 ]; then
    printf 'ok: codesign reports it notarized\n'
    return 0
  fi
  [ "$satisfied" = 1 ] || printf 'smoke: codesign does not report the notarized requirement satisfied\n' >&2
  [ "$failed" = 0 ] || printf 'smoke: codesign reports the code failed to satisfy the notarized requirement\n' >&2
  return 1
}
