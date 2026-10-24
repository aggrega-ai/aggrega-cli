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
# publishing job attaches the signed Windows binaries only once they match the
# SHA-256 list the Windows smoke run held them to, through the same function,
# smoke_check_sums, so the two checks cannot read the list differently. And the
# folder in which those jobs hand files to each other is listed where files enter
# it by smoke_write_tree_sums, in the form smoke_check_sums reads, and held to
# that list where they leave by smoke_check_tree_sums, which reads it through
# smoke_check_sums. The SHA256SUMS a release attaches is written the same way, by
# smoke_write_tree_sums over the directory holding exactly the files the release
# attaches, and held to them by smoke_check_sums before the release is made.
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
#   smoke_native_path <path>
#     Prints <path> in the form a native program on this system reads: the Windows
#     form under the Git for Windows shell (through cygpath), unchanged elsewhere.
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
#   smoke_run_logged <log-dir> <name> <command> [<arg>...]
#     Runs the command with its standard output and its standard error each
#     redirected to a file of <log-dir>, <name>.out and <name>.err, as a run whose
#     output a log or another program reads, then prints both and holds them to
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
#     is one Windows accepts (smoke_names_windows_accepts). Returns 1, naming what
#     failed.
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
#     and every digest that differs. Needs sha256sum, which the Git for Windows
#     shell and Linux both carry.
#
#   smoke_write_tree_sums <dir>
#     Writes <dir>/SHA256SUMS: the SHA-256 of every file beneath <dir>, by its
#     path from <dir>, in bytewise order, in the form sha256sum writes. It is
#     written under another name and then renamed, so a list present is a list
#     complete. Returns 1, writing no list, when <dir> holds anything that is
#     neither a file nor a directory, since no list can hold it, when it holds no
#     file, or when it already holds a list, since a list is written once, after
#     the last file is in place. Prints how many files and bytes it listed. Needs
#     GNU find, sha256sum and bash 4.4 (mapfile -d), which the Linux machine that
#     builds and publishes a release carries; no smoke run calls it.
#
#   smoke_check_tree_sums <dir>
#     Holds every file beneath <dir> to <dir>/SHA256SUMS through
#     smoke_check_sums: the list must name each file but itself exactly once and
#     nothing else, each with the digest it gives, so a file changed, added or
#     removed since the list was written is refused. Returns 1, naming each, when
#     <dir> holds anything that is neither a file nor a directory, and otherwise
#     as smoke_check_sums does. Needs what smoke_write_tree_sums needs.
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

smoke_native_path() {
  if command -v cygpath >/dev/null 2>&1; then
    cygpath -w "$1"
  else
    printf '%s\n' "$1"
  fi
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

smoke_run_logged() {
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
    return 1
  fi
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
  for slot in "$root"/entities/*/*/blob; do
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
  local re='^([0-9a-f]{64}) [ *](.+)$'
  shift 2
  local -A wanted=() listed=()
  for name in "$@"; do wanted[$name]=1; done
  if [ ! -f "$list" ]; then
    printf 'smoke: there is no SHA-256 list at %s\n' "$list" >&2
    return 1
  fi
  if ! command -v sha256sum >/dev/null 2>&1; then
    printf 'smoke: sha256sum not found on PATH\n' >&2
    return 1
  fi
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line%$'\r'}"
    [ -n "$line" ] || continue
    if [[ ! $line =~ $re ]]; then
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
    actual="$(sha256sum < "$dir/$name")" || { printf 'smoke: could not read %s\n' "$dir/$name" >&2; bad=1; continue; }
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

# smoke_tree_refuse_special <dir> — returns 1, naming each, when <dir> holds
# anything that is neither a file nor a directory: a symbolic link, a device, a
# pipe or a socket, none of which a SHA-256 list can hold.
smoke_tree_refuse_special() {
  local dir="$1" found
  if [ ! -d "$1" ]; then
    printf 'smoke: %s is not a directory\n' "$dir" >&2
    return 1
  fi
  if ! found="$(find "$dir" -mindepth 1 ! -type f ! -type d -printf '  %P\n')"; then
    printf 'smoke: could not read every entry beneath %s\n' "$dir" >&2
    return 1
  fi
  [ -z "$found" ] && return 0
  printf 'smoke: %s holds what is neither a file nor a directory:\n%s\n' "$dir" "$found" >&2
  return 1
}

smoke_write_tree_sums() {
  local dir="$1" bytes
  local -a files=()
  smoke_tree_refuse_special "$dir" || return 1
  if [ -e "$dir/SHA256SUMS" ] || [ -e "$dir/SHA256SUMS.partial" ]; then
    printf 'smoke: %s already holds a SHA-256 list; a list is written once, after the last file is in place\n' "$dir" >&2
    return 1
  fi
  mapfile -d '' -t files < <(cd "$dir" && find . -type f -printf '%P\0' | LC_ALL=C sort -z)
  if [ "${#files[@]}" -eq 0 ]; then
    printf 'smoke: %s holds no file\n' "$dir" >&2
    return 1
  fi
  if ! (cd "$dir" && sha256sum -- "${files[@]}" > SHA256SUMS.partial && mv SHA256SUMS.partial SHA256SUMS); then
    rm -f -- "$dir/SHA256SUMS.partial"
    printf 'smoke: could not write the SHA-256 list of %s\n' "$dir" >&2
    return 1
  fi
  bytes="$(du -sb "$dir")"
  printf 'ok: %s: %d file(s), %s bytes, each listed in SHA256SUMS\n' "$dir" "${#files[@]}" "${bytes%%[[:space:]]*}"
}

smoke_check_tree_sums() {
  local dir="$1"
  local -a files=()
  smoke_tree_refuse_special "$dir" || return 1
  mapfile -d '' -t files < <(cd "$dir" && find . -type f ! -path ./SHA256SUMS -printf '%P\0' | LC_ALL=C sort -z)
  smoke_check_sums "$dir/SHA256SUMS" "$dir" "${files[@]}"
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
