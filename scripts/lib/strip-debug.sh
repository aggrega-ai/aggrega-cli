#!/usr/bin/env bash
# aggrega-cli/scripts/lib/strip-debug.sh
#
# The single home for the stripped-copy recipe, SOURCED (never executed) by every
# script that writes a copy of a built aggrega binary without its debug
# information: the tooling image's build (scripts/build-tooling-image.sh), whose
# copy a release also attaches as the Linux stripped binary, and
# `scripts/build.sh --stripped`, which writes the stripped binary with it for a
# Linux target and for the machine it runs on. Sourcing one recipe means a
# stripped copy is made the same way whichever script made it. A Windows binary
# is not stripped here: scripts/build.sh has the linker write the stripped
# Windows binary instead, and says why where it decides how each target's
# stripped binary is made.
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
