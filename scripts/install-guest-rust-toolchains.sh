#!/usr/bin/env bash
# aggrega-cli/scripts/install-guest-rust-toolchains.sh — install the Rust
# toolchains the core guests pin, with a rustup of this script's own, into a
# directory of the caller's. It is called by the release workflow's build job
# and by the heavy checks' workflows, and by a local check of the staging steps,
# so all of them install the toolchains the same way.
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
#   --github-env <dir>
#       The same install into <dir>, for a GitHub Actions step. Instead of
#       printing the two settings it appends them to the file GITHUB_ENV names,
#       so every later step of the job runs rustup, cargo and rustc with them:
#       cargo and rustc find their toolchains through RUSTUP_HOME, and the guest
#       build remaps CARGO_HOME out of the guests' bytes. It also appends
#       <dir>/cargo/bin to the file GITHUB_PATH names, so the cargo and rustc
#       the later steps run are this rustup's. Standard output stays empty.
#       When GITHUB_ENV or GITHUB_PATH is unset or empty it exits 2 before it
#       installs anything. <dir> should lie under the runner's temporary
#       directory, which the runner empties at the start and end of every job,
#       so a run needs no Rust on the runner and installs none into a user's
#       home.
#
# Usage:
#   scripts/install-guest-rust-toolchains.sh <dir>
#   scripts/install-guest-rust-toolchains.sh --github-env <dir>
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
  # shellcheck source=lib/core-plugins.sh
  . "$here/lib/core-plugins.sh" || exit 1
  dirs="$(core_plugin_rust_dirs)" || exit 1
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

github_env() {
  local dir="$1" homes line
  [ -n "${GITHUB_ENV:-}" ] || usage_error "--github-env needs GITHUB_ENV to name a file, and it is unset or empty"
  [ -n "${GITHUB_PATH:-}" ] || usage_error "--github-env needs GITHUB_PATH to name a file, and it is unset or empty"
  homes="$(install_toolchains "$dir")"
  while IFS= read -r line; do
    case "$line" in
      RUSTUP_HOME=?*) printf '%s\n' "$line" >> "$GITHUB_ENV" ;;
      CARGO_HOME=?*) printf '%s\n' "$line" >> "$GITHUB_ENV"; printf '%s\n' "${line#CARGO_HOME=}/bin" >> "$GITHUB_PATH" ;;
    esac
  done <<< "$homes"
}

case "${1:-}" in
  -h|--help) sed -n '2,/^# argument it cannot use\.$/p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
  '') usage_error "name the directory to install rustup into (see --help)" ;;
  --github-env)
    [ "$#" = 2 ] || usage_error "--github-env takes one argument, the directory to install rustup into (see --help)"
    github_env "$2"
    ;;
  -*) usage_error "unknown option: $1 (see --help)" ;;
  *)
    [ "$#" = 1 ] || usage_error "takes one argument, the directory to install rustup into (see --help)"
    install_toolchains "$1"
    ;;
esac
