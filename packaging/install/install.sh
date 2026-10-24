#!/bin/sh
# Install the aggrega command for the current user on Linux or macOS:
#
#   curl -fsSL https://aggrega.ai/install.sh | sh
#
# It downloads the default aggrega binary for this operating system and
# processor from a release, checks it against the release's SHA256SUMS list,
# and installs it as ~/.local/bin/aggrega, replacing an earlier copy there in
# one step. Then it runs the installed binary to print its version, and, when
# the folder is not on PATH, prints the line that puts it there. It never uses
# sudo and edits no shell profile.
#
# Beyond the standard system utilities it needs curl or wget, uname, and
# sha256sum or shasum.
#
# Settings, read from the environment:
#
#   AGGREGA_VERSION        the release to install, such as 0.8.7 or v0.8.7.
#                          The latest release when unset.
#   AGGREGA_INSTALL_DIR    the folder to install into. ~/.local/bin when unset.
#   AGGREGA_MIRROR         the address of a mirror of the Aggrega releases,
#                          laid out as GitHub lays them out:
#                          <mirror>/latest/download/<file> for the latest
#                          release and <mirror>/download/v<version>/<file> for
#                          a given one. The releases on GitHub,
#                          https://github.com/aggrega-ai/aggrega-cli/releases,
#                          when unset.
#
# Everything runs from main, which the last line calls, so a download of this
# script that is cut short runs nothing.

say() {
  printf 'aggrega install: %s\n' "$*"
}

refuse() {
  printf 'aggrega install: %s\n' "$*" >&2
  exit 1
}

# Sets os and arch to the names release files carry, or refuses.
detect_platform() {
  kernel="$(uname -s)" || refuse "could not run uname to read the operating system's name"
  machine="$(uname -m)" || refuse "could not run uname to read the processor's name"
  case "$kernel" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    MINGW* | MSYS* | CYGWIN* | Windows_NT)
      refuse "this is Windows ($kernel), which install.sh does not install on; from PowerShell, run: irm $base/latest/download/install.ps1 | iex"
      ;;
    *)
      refuse "the operating system is $kernel, for which no release carries a binary; releases carry binaries for Linux, macOS and Windows"
      ;;
  esac
  case "$machine" in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *)
      refuse "the processor is $machine, for which no release carries a $kernel binary; releases carry binaries for 64-bit x86 (x86_64) and 64-bit ARM (aarch64, arm64)"
      ;;
  esac
  # On a Mac with Apple silicon, a shell running through Rosetta 2 reports
  # x86_64; the Apple silicon binary is the one built for that Mac.
  if [ "$os" = darwin ] && [ "$arch" = amd64 ] &&
    [ "$(sysctl -n sysctl.proc_translated 2>/dev/null)" = 1 ]; then
    arch=arm64
  fi
}

pick_tools() {
  if command -v curl >/dev/null 2>&1; then
    fetcher=curl
  elif command -v wget >/dev/null 2>&1; then
    fetcher=wget
  else
    refuse "downloading needs curl or wget, and neither is on PATH"
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    hasher=sha256sum
  elif command -v shasum >/dev/null 2>&1; then
    hasher=shasum
  else
    refuse "checking the download needs sha256sum or shasum, and neither is on PATH"
  fi
}

# fetch <url> <file>
fetch() {
  case "$fetcher" in
    curl) curl -fsSL -o "$2" "$1" ;;
    *) wget -q -O "$2" "$1" ;;
  esac
}

# digest <file> prints the file's SHA-256 digest in lowercase hexadecimal.
digest() {
  case "$hasher" in
    sha256sum) digest_out="$(sha256sum <"$1")" ;;
    *) digest_out="$(shasum -a 256 <"$1")" ;;
  esac || return 1
  printf '%s\n' "${digest_out%% *}" | tr 'A-F' 'a-f'
}

# find_asset <checksum list> sets asset and asset_sum to the one default binary
# the list names for this platform, and version to the version it carries, or
# refuses. The default binary is aggrega-<version>-<os>-<arch>, where the
# version starts with a digit; that is what sets it apart from the other
# variants a release carries, aggrega-debug-<version>-<os>-<arch> and
# aggrega-light-<version>-<os>-<arch>.
find_asset() {
  asset='' asset_sum='' matches=0 cr="$(printf '\r')"
  while read -r line_sum line_name || [ -n "$line_sum" ]; do
    line_name="${line_name%"$cr"}"
    line_name="${line_name#\*}"
    case "$line_name" in
      aggrega-[0-9]*-"$os-$arch") ;;
      *) continue ;;
    esac
    if [ -n "$requested" ] && [ "$line_name" != "aggrega-$requested-$os-$arch" ]; then
      continue
    fi
    matches=$((matches + 1))
    asset="$line_name"
    asset_sum="$(printf '%s\n' "$line_sum" | tr 'A-F' 'a-f')"
  done <"$1"
  if [ "$matches" -eq 0 ]; then
    refuse "the checksum list at $sums_url names no aggrega binary for $os/$arch"
  fi
  if [ "$matches" -gt 1 ]; then
    refuse "the checksum list at $sums_url names $matches aggrega binaries for $os/$arch, where it should name one"
  fi
  case "$asset_sum" in
    *[!0-9a-f]*) refuse "the checksum list at $sums_url lists '$asset_sum' for $asset, which is not a SHA-256 digest" ;;
  esac
  if [ "${#asset_sum}" -ne 64 ]; then
    refuse "the checksum list at $sums_url lists '$asset_sum' for $asset, which is not a SHA-256 digest"
  fi
  version="${asset#aggrega-}"
  version="${version%-"$os-$arch"}"
}

cleanup() {
  if [ -n "${staged:-}" ]; then
    rm -f -- "$staged"
  fi
  if [ -n "${tmp:-}" ]; then
    rm -rf -- "$tmp"
  fi
}

# path_hint prints the line that puts dir on PATH for the user's login shell.
path_hint() {
  line="export PATH=\"$dir:\$PATH\""
  case "${SHELL:-}" in
    */zsh) profile=.zshrc ;;
    */bash)
      if [ "$os" = darwin ]; then
        profile=.bash_profile
      else
        profile=.bashrc
      fi
      ;;
    */fish) profile=.config/fish/config.fish line="fish_add_path \"$dir\"" ;;
    *) profile=.profile ;;
  esac
  say "$dir is not on your PATH. To run aggrega by name, add this line to ${HOME:-~}/$profile:"
  printf '\n  %s\n\n' "$line"
  say "then open a new terminal."
}

main() {
  base="${AGGREGA_MIRROR:-https://github.com/aggrega-ai/aggrega-cli/releases}"
  base="${base%/}"
  detect_platform

  requested="${AGGREGA_VERSION:-}"
  requested="${requested#v}"
  if [ -n "${AGGREGA_VERSION:-}" ]; then
    case "$requested" in
      '' | *[!A-Za-z0-9._+-]*)
        refuse "AGGREGA_VERSION is '$AGGREGA_VERSION', which is not a release version such as 0.8.7 or v0.8.7"
        ;;
    esac
  fi

  if [ -n "${AGGREGA_INSTALL_DIR:-}" ]; then
    dir="$AGGREGA_INSTALL_DIR"
  elif [ -n "${HOME:-}" ]; then
    dir="$HOME/.local/bin"
  else
    refuse "HOME is not set, so there is no default folder to install into; set AGGREGA_INSTALL_DIR to one"
  fi
  case "$dir" in
    /*) ;;
    *) dir="$(pwd)/$dir" ;;
  esac
  while [ "$dir" != / ] && [ "${dir%/}" != "$dir" ]; do
    dir="${dir%/}"
  done
  dest="$dir/aggrega"
  if [ -d "$dest" ]; then
    refuse "$dest is a folder, so aggrega cannot be installed under that name"
  fi

  pick_tools

  staged='' tmp=''
  trap cleanup EXIT
  trap 'exit 129' HUP
  trap 'exit 130' INT
  trap 'exit 143' TERM
  tmp="$(mktemp -d 2>/dev/null || mktemp -d -t aggrega-install)" ||
    refuse "could not create a temporary folder to download into"

  if [ -n "$requested" ]; then
    sums_url="$base/download/v$requested/SHA256SUMS"
  else
    sums_url="$base/latest/download/SHA256SUMS"
  fi
  say "reading the checksum list $sums_url"
  if ! fetch "$sums_url" "$tmp/SHA256SUMS"; then
    if [ -n "$requested" ]; then
      refuse "could not download $sums_url; check that $requested is a published release"
    fi
    refuse "could not download $sums_url"
  fi
  find_asset "$tmp/SHA256SUMS"

  url="$base/download/v$version/$asset"
  say "downloading $url"
  fetch "$url" "$tmp/$asset" || refuse "could not download $url"
  actual="$(digest "$tmp/$asset")" || refuse "could not compute the SHA-256 checksum of the downloaded $asset"
  if [ "$actual" != "$asset_sum" ]; then
    rm -f -- "$tmp/$asset"
    refuse "the SHA-256 checksum of the downloaded $asset is $actual, but SHA256SUMS lists $asset_sum; the download was deleted and nothing was installed"
  fi
  say "$asset matches its SHA-256 checksum in SHA256SUMS"

  # The new binary is written beside the old one under a temporary name and
  # then renamed over it, so the folder holds either the earlier aggrega or
  # the new one at every moment, never a partial file.
  mkdir -p -- "$dir" || refuse "could not create the folder $dir"
  staged="$dir/.aggrega.install.$$"
  cp -- "$tmp/$asset" "$staged" || refuse "could not write into $dir"
  chmod 755 "$staged" || refuse "could not mark $staged executable"
  mv -f -- "$staged" "$dest" || refuse "could not put the new aggrega in place at $dest"
  staged=''

  say "installed $dest"
  if ! "$dest" version; then
    refuse "installed $dest, but running '$dest version' failed"
  fi

  case ":${PATH:-}:" in
    *":$dir:"* | *":$dir/:"*)
      found="$(command -v aggrega 2>/dev/null || true)"
      if [ -n "$found" ] && [ "$found" != "$dest" ]; then
        say "note: the aggrega that runs by name is $found, which comes before $dir on your PATH"
      fi
      ;;
    *) path_hint ;;
  esac
}

main "$@"
