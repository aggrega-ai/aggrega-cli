#!/usr/bin/env bash
# aggrega-cli/scripts/lib/strip-debug.sh
#
# The single home for the stripped-copy recipe, SOURCED (never executed) by every
# script that writes a copy of a built aggrega binary without its debug
# information: the tooling image's build (scripts/build-tooling-image.sh), whose
# copy a release also attaches as the Linux stripped binary, and
# `scripts/build.sh --stripped`, which writes the stripped binary for any target,
# Windows included. Sourcing one recipe means a stripped binary is made the same
# way whichever script made it.
#
# Sourcing defines one function and sets no variables:
#   strip_debug_copy <binary> <copy>
#     Writes <copy>: <binary> with its debug information removed
#     (`strip --strip-debug`; the symbol table and the program stay), with mode
#     0755. <binary> is never modified. Returns non-zero, after saying why on
#     standard error, when strip is not on PATH or fails.
#
# strip runs with SOURCE_DATE_EPOCH=0, and that matters for one format only. GNU
# strip writes the current time into a Windows (PE) executable's header time stamp
# unless SOURCE_DATE_EPOCH names a time, so two copies stripped from one binary a
# second apart would differ. The Go linker writes 0 there, so pinning it to 0
# keeps the copy's stamp the original's and makes the copy reproducible. A Linux
# (ELF) binary carries no such stamp, and strip writes the same bytes for it with
# or without the variable.
#
# strip comes from GNU binutils; its support for the PE format is what strips a
# Windows binary on a Linux machine.
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
