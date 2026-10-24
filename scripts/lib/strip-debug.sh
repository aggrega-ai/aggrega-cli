#!/usr/bin/env bash
# aggrega-cli/scripts/lib/strip-debug.sh
#
# The single home for the recipe that copies a built aggrega binary without its
# debug information, SOURCED (never executed) by scripts/build.sh, which makes
# ./aggrega with it from ./aggrega-debug in a build for the machine it runs on.
# That ./aggrega is the binary a release attaches for linux/amd64 under the plain
# name and the one the tooling image carries, which copies it as it is: this is
# the one place a binary is stripped. A binary built for another system with
# --target is not stripped here: scripts/build.sh has the linker write those
# without their debug information instead, and says why where it decides how
# each build's ./aggrega is made.
#
# Sourcing defines one function and sets no variables:
#   strip_debug_copy <binary> <copy>
#     Writes <copy>: <binary> with its debug information removed
#     (`strip --strip-debug`; the symbol table and the program stay), with mode
#     0755. <binary> is never modified. Returns non-zero, after saying why on
#     standard error, when strip is not on PATH or fails.
#
# strip runs with SOURCE_DATE_EPOCH=0 so that a copy never depends on the time it
# was made: GNU strip writes the current time into the header time stamp of a
# format that carries one unless SOURCE_DATE_EPOCH names a time. A Linux (ELF)
# binary carries no such stamp, and strip writes the same bytes for it with or
# without the variable.
#
# strip comes from GNU binutils. It does not read the macOS (Mach-O) format.
strip_debug_copy() {
  local binary="$1" copy="$2"
  if ! command -v strip >/dev/null 2>&1; then
    printf 'strip not found on PATH; install GNU binutils, which provides it\n' >&2
    return 1
  fi
  if ! SOURCE_DATE_EPOCH=0 strip --strip-debug -o "$copy" "$binary"; then
    printf 'strip --strip-debug failed on %s\n' "$binary" >&2
    return 1
  fi
  # strip -o creates the copy under the umask, so the mode is set explicitly.
  chmod 0755 "$copy"
}
