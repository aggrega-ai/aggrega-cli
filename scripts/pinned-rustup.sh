#!/usr/bin/env bash
# aggrega-cli/scripts/pinned-rustup.sh — install rustup at one pinned release,
# checked against the SHA-256 pinned here, into a directory of the caller's, so
# the release workflow's build job installs the Rust toolchains the guests pin
# with this rustup and never with one the machine happens to carry.
#
#   install <dir>
#       Fetches rustup-init for this machine's platform at the release below,
#       from the Rust project's archive of rustup releases, and refuses it,
#       before it runs, unless its SHA-256 is the one pinned here for that
#       platform, naming both digests. It then runs it to install rustup, with
#       no toolchain and the minimal profile, into <dir>/rustup (RUSTUP_HOME)
#       and <dir>/cargo (CARGO_HOME), changing no shell profile and nothing in
#       the home directory, and refuses an installed rustup that does not report
#       the pinned release. <dir> must be absent or empty, so whatever is used
#       from it is what this run installed. A platform with no pinned digest is
#       refused, naming it, before anything is fetched. On success it prints, on
#       standard output and nothing else, two lines,
#           RUSTUP_HOME=<dir>/rustup
#           CARGO_HOME=<dir>/cargo
#       for the caller to put in the environment of every later command that
#       runs rustup, cargo or rustc: the rustup it installed, and the cargo and
#       rustc it puts in <dir>/cargo/bin, find their toolchains through them.
#       Everything else it reports goes to standard error.
#
# Which toolchains are installed is not decided here: the caller runs the
# installed rustup in each Rust guest's directory, and the guest's
# rust-toolchain.toml names the release and its targets. Installing from a
# toolchain file that way needs rustup 1.28.0 or newer.
#
# It runs curl, sha256sum, uname and the fetched rustup-init, and nothing else
# beside the shell's own utilities.
#
# Usage:
#   scripts/pinned-rustup.sh install <dir>
#   scripts/pinned-rustup.sh --help
#
# Exits 0 when it did what it was asked, 1 when it could not, and 2 for an
# argument it cannot use.
set -euo pipefail

# rustup 1.29.2, from the Rust project's archive, which keeps every release at
# its own path; the dist location beside it moves with each new release. One
# SHA-256 per platform the release workflow's build runs on, each the digest of
# that release's rustup-init and the one the Rust project publishes beside it,
# in rustup-init.sha256. A rustup-init with any other digest is refused before it
# runs; moving to another release changes the version and every digest.
RUSTUP_VERSION=1.29.2
RUSTUP_ARCHIVE=https://static.rust-lang.org/rustup/archive

# rustup_platform MACHINE — print the target triple the rustup-init for MACHINE
# (uname -s/uname -m) is published under, and the SHA-256 pinned for it, or
# fail for a machine with none pinned.
rustup_platform() {
  case "$1" in
    Linux/x86_64)
      echo "x86_64-unknown-linux-gnu 5be191e8c70a5007074254cb74d78dbfdd25cd5a61c27de7b8e5410122ec24f4" ;;
    *) return 1 ;;
  esac
}

refuse() { printf 'pinned-rustup: %s\n' "$*" >&2; exit 1; }
usage_error() { printf 'pinned-rustup: %s\n' "$*" >&2; exit 2; }

install_rustup() {
  local dir="$1" machine platform triple pinned url init actual reported
  machine="$(uname -s)/$(uname -m)"
  if ! platform="$(rustup_platform "$machine")"; then
    refuse "rustup $RUSTUP_VERSION is pinned here for Linux/x86_64 only, and this machine is $machine: no digest is pinned for its rustup-init, so none is fetched"
  fi
  triple="${platform%% *}"
  pinned="${platform#* }"
  if [ -e "$dir" ] && [ -n "$(find "$dir" -mindepth 1 -print -quit)" ]; then
    refuse "$dir is not empty: rustup is installed only into an absent or empty directory, so nothing found there is used"
  fi
  mkdir -p "$dir"
  dir="$(cd "$dir" && pwd -P)"

  url="$RUSTUP_ARCHIVE/$RUSTUP_VERSION/$triple/rustup-init"
  init="$dir/rustup-init"
  curl -sSfL --proto '=https' --tlsv1.2 --retry 3 -o "$init" "$url"
  actual="$(sha256sum < "$init")"
  actual="${actual%% *}"
  if [ "$actual" != "$pinned" ]; then
    rm -f -- "$init"
    refuse "$url has SHA-256 $actual, not $pinned, the digest this script pins for $triple; refusing to run it"
  fi
  echo "rustup: $url, SHA-256 $actual, the pinned digest" >&2
  chmod +x "$init"
  RUSTUP_HOME="$dir/rustup" CARGO_HOME="$dir/cargo" \
    "$init" -y --no-modify-path --profile minimal --default-toolchain none >&2
  rm -f -- "$init"

  reported="$(RUSTUP_HOME="$dir/rustup" CARGO_HOME="$dir/cargo" "$dir/cargo/bin/rustup" --version 2>/dev/null)" ||
    refuse "the rustup installed at $dir/cargo/bin did not report its version"
  case "$reported" in
    "rustup $RUSTUP_VERSION "*|"rustup $RUSTUP_VERSION") ;;
    *) refuse "the rustup installed at $dir/cargo/bin reports ${reported:-nothing}, not $RUSTUP_VERSION" ;;
  esac
  echo "rustup: $reported, installed at $dir/cargo/bin" >&2
  printf 'RUSTUP_HOME=%s\nCARGO_HOME=%s\n' "$dir/rustup" "$dir/cargo"
}

case "${1:-}" in
  -h|--help) sed -n '2,/^# argument it cannot use\.$/p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
  install)
    [ "$#" = 2 ] && [ -n "$2" ] || usage_error "install takes the directory to install rustup into"
    install_rustup "$2"
    ;;
  '') usage_error "name what to do: install (see --help)" ;;
  *) usage_error "unknown command: $1 (see --help)" ;;
esac
