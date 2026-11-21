#!/usr/bin/env bash
# aggrega-cli/scripts/install-guest-rust-toolchains.sh — install the Rust
# toolchains the core guests pin, with a rustup of this script's own, into a
# directory of the caller's. It is called by the release workflow's build job
# and by a local check of the staging steps, so both install them the same way.
#
#   <dir>
#       Runs pinned-rustup.sh install <dir>, which installs rustup at one
#       pinned release into <dir>/rustup (RUSTUP_HOME) and <dir>/cargo
#       (CARGO_HOME) and prints those two settings; <dir> must be absent or
#       empty. It then runs that rustup, with no toolchain named, in each Rust
#       guest's source directory, as the core-plugin list's one reader names
#       them, so the release each directory's rust-toolchain.toml pins is
#       installed with every target the file lists. RUSTUP_TOOLCHAIN is cleared
#       for the call because it outranks every toolchain file. The minimal
#       profile is installed, which is all that compiling the guests needs.
#       No release and no target is spelled here.
#       On success it prints, on standard output and nothing else, the two
#       lines pinned-rustup.sh printed,
#           RUSTUP_HOME=<dir>/rustup
#           CARGO_HOME=<dir>/cargo
#       for the caller to put in the environment of every later command that
#       runs rustup, cargo or rustc, and to put <dir>/cargo/bin ahead of the
#       path. Everything else it reports, rustup's own output included, goes to
#       standard error.
#
# Usage:
#   scripts/install-guest-rust-toolchains.sh <dir>
#   scripts/install-guest-rust-toolchains.sh --help
#
# Exits 0 when it did what it was asked, 1 when it could not, and 2 for an
# argument it cannot use.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

usage_error() { printf 'install-guest-rust-toolchains: %s\n' "$*" >&2; exit 2; }
refuse() { printf 'install-guest-rust-toolchains: %s\n' "$*" >&2; exit 1; }

install_toolchains() {
  local dir="$1" homes line cargo_bin dirs d
  homes="$("$here/pinned-rustup.sh" install "$dir")" || exit 1
  while IFS= read -r line; do
    case "$line" in
      RUSTUP_HOME=?*|CARGO_HOME=?*) export "${line?}" ;;
      *) refuse "pinned-rustup.sh printed $line, not a RUSTUP_HOME or CARGO_HOME setting" ;;
    esac
  done <<< "$homes"
  cargo_bin="$CARGO_HOME/bin"
  . "$here/lib/core-plugins.sh"
  dirs="$(core_plugin_rust_dirs)"
  while IFS= read -r d; do
    [ -n "$d" ] || continue
    echo "rust: installing the toolchain aggrega-cli/$d/rust-toolchain.toml pins, with its targets" >&2
    if ! ( cd "$CORE_PLUGIN_MODULE_ROOT/$d" && env -u RUSTUP_TOOLCHAIN "$cargo_bin/rustup" toolchain install --profile minimal --no-self-update >&2 ); then
      echo "rust: $("$cargo_bin/rustup" --version 2>/dev/null) could not install the toolchain aggrega-cli/$d/rust-toolchain.toml pins" >&2
      exit 1
    fi
  done <<< "$dirs"
  printf '%s\n' "$homes"
}

case "${1:-}" in
  -h|--help) sed -n '2,/^# argument it cannot use\.$/p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
  '') usage_error "name the directory to install rustup into (see --help)" ;;
  -*) usage_error "unknown option: $1 (see --help)" ;;
  *)
    [ "$#" = 1 ] || usage_error "takes one argument, the directory to install rustup into (see --help)"
    install_toolchains "$1"
    ;;
esac
