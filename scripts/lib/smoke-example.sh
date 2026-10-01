#!/usr/bin/env bash
# aggrega-cli/scripts/lib/smoke-example.sh
#
# The single home for how the platform smoke run builds its worked example and
# compares two built trees, SOURCED (never executed) by both of its scripts:
# scripts/stage-windows-smoke.sh, which builds the reference on the machine that
# built the release, and scripts/windows-smoke.sh, which builds the same example on
# Windows and compares the result with that reference. The two sides run one
# procedure and one comparison, so a difference between their trees is a
# difference between the platforms rather than between two scripts.
#
# Sourcing defines five functions and sets no variables:
#
#   smoke_native_path <path>
#     Prints <path> in the form a native program on this system reads: the Windows
#     form under the Git for Windows shell (through cygpath), unchanged elsewhere.
#
#   smoke_isolate <config-dir>
#     Prepares this shell's environment for every aggrega run that follows: unsets
#     every AGGREGA_ variable it inherited, so no setting of the machine reaches the
#     run; points the global configuration at <config-dir>, an empty directory, so
#     no configuration file of the machine is read either; and sets AGGREGA_OFFLINE,
#     so a run refuses every network reach rather than fetching what the example
#     does not carry.
#
#   smoke_build_example <aggrega> <workspace> <release>
#     Runs `validate --release`, `pull --release` and `build --release` for
#     <release> with the binary <aggrega>, from inside <workspace>, which it names as
#     the workspace root so nothing above it is read as part of the workspace. The
#     pull re-resolves the dependencies into the lockfile the workspace already
#     carries; when the result is unchanged it keeps that lockfile's generated_at,
#     so the whole workspace, lockfile included, is comparable byte for byte with a
#     second machine's. Returns non-zero, naming the command, on the first that
#     fails.
#
#   smoke_compare_trees <expected> <actual>
#     Compares two directory trees: the set of names beneath each (a directory is
#     an entry of its own), then every file present in both, byte for byte. Prints
#     each name present on one side only and each file whose bytes differ, with
#     where they first differ, then one summary line. Returns 0 when the trees are
#     identical and 1 otherwise. Needs cmp (GNU diffutils), which the Git for Windows
#     shell carries.
#
#   smoke_plant_one_byte <file>
#     Overwrites the first byte of <file> with a different byte, leaving its size
#     unchanged: the planted difference that shows smoke_compare_trees can fail,
#     and that a comparison of sizes alone would miss.

smoke_native_path() {
  if command -v cygpath >/dev/null 2>&1; then
    cygpath -w "$1"
  else
    printf '%s\n' "$1"
  fi
}

smoke_isolate() {
  local config_dir="$1" var
  mkdir -p "$config_dir"
  for var in $(compgen -e); do
    case "$var" in AGGREGA_*) unset "$var" ;; esac
  done
  XDG_CONFIG_HOME="$(smoke_native_path "$config_dir")"
  export XDG_CONFIG_HOME
  export AGGREGA_OFFLINE=true
}

smoke_build_example() {
  local bin="$1" workspace="$2" release="$3" root
  root="$(smoke_native_path "$workspace")"
  (
    cd "$workspace" || exit 1
    "$bin" --no-color --plain --workspace-root "$root" validate --release "$release" ||
      { printf 'smoke: validate --release %s failed in %s\n' "$release" "$workspace" >&2; exit 1; }
    "$bin" --no-color --plain --workspace-root "$root" pull --release "$release" --detail ||
      { printf 'smoke: pull --release %s failed in %s\n' "$release" "$workspace" >&2; exit 1; }
    "$bin" --no-color --plain --workspace-root "$root" build --release "$release" ||
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
    printf 'compare: cmp not found on PATH; GNU diffutils provides it\n' >&2
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
  local file="$1" first replacement=A
  if [ ! -f "$file" ] || [ ! -s "$file" ]; then
    printf 'plant: %s is not a non-empty file\n' "$file" >&2
    return 1
  fi
  first="$(head -c 1 "$file")"
  [ "$first" = A ] && replacement=B
  printf '%s' "$replacement" | dd of="$file" bs=1 count=1 conv=notrunc status=none
}
