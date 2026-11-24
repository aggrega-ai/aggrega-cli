#!/usr/bin/env bash
# aggrega-cli/scripts/lib/first-line.sh
#
# The one home for reading the first line a command prints, SOURCED (never
# executed) by the scripts that need it.
#
# `<command> | head -n1` stops reading after the first line. A command still
# writing at that moment meets a closed pipe on its next write and SIGPIPE ends
# it with status 141; under `set -o pipefail` the pipeline then fails although
# the line was read. Whether the command has finished writing by then depends on
# timing, so the failure comes and goes. Reading the whole output before taking
# its first line lets the command finish on its own.
#
# Sourcing defines one function and sets no variables:
#   first_line_of <command> [<argument>…]
#     Runs <command> with its arguments, reads everything it writes to standard
#     output, and prints the first line of that, followed by a newline (an empty
#     line when the command printed nothing). Returns the command's own status;
#     when the command fails it prints nothing. Standard input and standard error
#     are the caller's.
first_line_of() {
  local out
  out="$("$@")" || return
  printf '%s\n' "${out%%$'\n'*}"
}
