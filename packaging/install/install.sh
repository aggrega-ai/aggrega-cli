#!/bin/sh
# Install the aggrega command for the current user on Linux or macOS:
#
#   curl -fsSL https://aggrega.ai/install.sh | sh
#
# It downloads the default aggrega binary for this operating system and
# processor from a release, checks it against the release's SHA256SUMS list,
# and installs it as ~/.local/bin/aggrega, replacing an earlier copy there in
# one step. Then it checks, printing nothing, that the installed binary runs.
# What it prints, on success:
#
#   Installing aggrega <version> (<os>-<arch>)
#     Downloaded and checked against its SHA-256 checksum
#     Installed <folder>/aggrega
#
# then, when the folder is not on PATH, the line that adds it to your shell's
# profile; then the installed binary's own version, printed by running it by
# its full path; and last, under "Next:", one line each for running the AI
# quickstart, adding the Aggrega skill to your AI assistants, and the page
# that shows how to set up shell completion and connect an AI client to the
# Aggrega MCP server. It never uses sudo, and it edits a shell profile only
# when --completion sets up completion for zsh.
#
# While the binary downloads, and only when standard error is a terminal, a
# progress bar there shows how much of it has arrived: curl's, or wget's where
# wget offers one. Run any other way, as with its output captured, it shows
# none.
#
# A refusal or a failed step is written to standard error as one line:
#
#   aggrega install: error: <what went wrong> - <what to do>
#
# Beyond the standard system utilities it needs curl or wget, uname, and
# sha256sum or shasum.
#
# Options, written after `sh -s --` when the script is piped into sh:
#
#   curl -fsSL https://aggrega.ai/install.sh | sh -s -- --verbose
#
#   --verbose              also print the address of the checksum list, the
#                          name of the release file and the address it is
#                          downloaded from.
#   --completion           also set up shell completion, for you alone, for the
#                          shell your SHELL setting names, using the installed
#                          aggrega's own `aggrega completion` script, and say
#                          which file it wrote or changed:
#                            bash  writes the script into the folder the
#                                  bash-completion package reads your own
#                                  completions from,
#                                  ~/.local/share/bash-completion/completions;
#                                  bash completion needs that package
#                            zsh   writes _aggrega into
#                                  ~/.local/share/zsh/site-functions and adds
#                                  the lines that load it to ~/.zshrc
#                            fish  writes aggrega.fish into
#                                  ~/.config/fish/completions
#                          XDG_DATA_HOME, XDG_CONFIG_HOME,
#                          BASH_COMPLETION_USER_DIR and ZDOTDIR move those
#                          folders as each shell reads them. Run again, it
#                          rewrites a script only when it changed and adds the
#                          lines to ~/.zshrc only once.
#   --skills               also install the Aggrega skill, in every project of
#                          yours, for the AI assistants on this machine, by
#                          running the installed aggrega's own
#                          `aggrega skills install --global`. It prints each
#                          assistant it found and what found it. At a terminal
#                          it then asks before writing: press Enter to install
#                          for those, or type the identifiers of others to add
#                          them. With no terminal it installs for those it
#                          found. It prints each skill folder it wrote, and
#                          the line about the skill is left out of "Next:".
#   --skills <list>        install the skill for the assistants the
#                          comma-separated list names instead, such as
#                          --skills claude-code,cursor, without looking for
#                          others; `aggrega skills list` prints the
#                          identifiers. --skills=<list> is the same. Run again,
#                          either form replaces the copies an earlier run wrote,
#                          which is how a newer aggrega brings the skill up to
#                          its own version.
#
# Exit status:
#
#   0  aggrega is installed, and every step an option asked for succeeded
#   1  nothing was installed, or the installed aggrega did not run
#   2  an option the script does not take, or a list naming no assistant;
#      nothing was done
#   3  aggrega is installed and runs, but setting up shell completion failed
#   4  aggrega is installed and runs, but installing the skill failed
#   5  aggrega is installed and runs, but both of those failed
#
# A failed step says why in its error line, written when it fails.
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

# item prints one line of what the install did, indented under its first line.
item() {
  printf '  %s\n' "$*"
}

# refuse <what went wrong> <what to do> reports why nothing more can be done
# and stops with exit status 1.
refuse() {
  printf 'aggrega install: error: %s - %s\n' "$1" "$2" >&2
  exit 1
}

# complain <what went wrong> <what to do> reports a step that failed without
# stopping the script.
complain() {
  printf 'aggrega install: error: %s - %s\n' "$1" "$2" >&2
}

# complain_completion <why> reports that shell completion was not set up.
complain_completion() {
  complain "could not set up shell completion: $1" "aggrega is installed and runs; https://aggrega.ai/docs/install/ shows how to set completion up later"
}

# usage_refusal reports an option the script cannot use and stops with exit
# status 2, before anything is done.
usage_refusal() {
  printf "aggrega install: error: %s - the options are --verbose, --completion and --skills [<list>], written after 'sh -s --' when the script is piped into sh\n" "$*" >&2
  exit 2
}

# Sets verbose, want_completion, want_skills and skills_list from the options,
# or refuses with exit status 2 before anything is done. A value after --skills
# that does not start with a dash is its list of assistants.
parse_options() {
  verbose='' want_completion='' want_skills='' skills_list=''
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --verbose) verbose=1 ;;
      --completion) want_completion=1 ;;
      --skills=*)
        want_skills=1 skills_list="${1#--skills=}"
        [ -n "$skills_list" ] || usage_refusal "--skills= names no assistant"
        ;;
      --skills)
        want_skills=1
        case "${2:-}" in
          '' | -*) ;;
          *)
            skills_list="$2"
            shift
            ;;
        esac
        ;;
      *) usage_refusal "'$1' is not an option install.sh takes" ;;
    esac
    shift
  done
  case "$skills_list" in
    '' | *[a-z0-9]*) ;;
    *) usage_refusal "--skills names '$skills_list', which names no assistant" ;;
  esac
  case "$skills_list" in
    *[!a-z0-9,-]*) usage_refusal "--skills names '$skills_list', which is not a comma-separated list of assistant identifiers such as claude-code,cursor" ;;
  esac
}

# sh_quote <text> prints text quoted for a POSIX shell, quotes left off when
# none are needed.
sh_quote() {
  case "$1" in
    '' | *[!A-Za-z0-9_./:@%+=,-]*)
      printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"
      ;;
    *) printf '%s' "$1" ;;
  esac
}

# Sets os and arch to the names release files carry, or refuses.
detect_platform() {
  kernel="$(uname -s)" ||
    refuse "could not run uname to read the operating system's name" "install.sh needs uname, one of the standard system utilities"
  machine="$(uname -m)" ||
    refuse "could not run uname to read the processor's name" "install.sh needs uname, one of the standard system utilities"
  case "$kernel" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    MINGW* | MSYS* | CYGWIN* | Windows_NT)
      refuse "this is Windows ($kernel), which install.sh does not install on" "from PowerShell, run: irm $base/latest/download/install.ps1 | iex"
      ;;
    *)
      refuse "the operating system is $kernel, for which no release carries a binary" "install on Linux, macOS or Windows, which releases carry binaries for"
      ;;
  esac
  case "$machine" in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *)
      refuse "the processor is $machine, for which no release carries a $kernel binary" "install on 64-bit x86 (x86_64) or 64-bit ARM (aarch64, arm64), which releases carry binaries for"
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
    refuse "downloading needs curl or wget, and neither is on PATH" "install one of them and run the installer again"
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    hasher=sha256sum
  elif command -v shasum >/dev/null 2>&1; then
    hasher=shasum
  else
    refuse "checking the download needs sha256sum or shasum, and neither is on PATH" "install one of them and run the installer again"
  fi
}

# fetch <url> <file> [progress] downloads the url into the file, showing no
# progress. Given progress, and with standard error a terminal, it shows the
# download's progress there instead: curl's progress bar, which --silent would
# hide, or wget's, where the wget offers --show-progress, as the wget built
# into BusyBox does not.
fetch() {
  if [ "${3:-}" = progress ] && [ -t 2 ]; then
    case "$fetcher" in
      curl)
        curl -fSL --progress-bar -o "$2" "$1"
        return
        ;;
      *)
        # wget labels its bar with the file's name as -O gives it, so the
        # name is given from the file's own folder.
        if wget --help 2>&1 | grep -e --show-progress >/dev/null; then
          (cd -- "${2%/*}" && wget -q --show-progress -O "${2##*/}" "$1")
          return
        fi
        ;;
    esac
  fi
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
  other_release="set AGGREGA_VERSION to another release and run the installer again"
  if [ "$matches" -eq 0 ]; then
    refuse "the checksum list at $sums_url names no aggrega binary for $os/$arch" "$other_release"
  fi
  if [ "$matches" -gt 1 ]; then
    refuse "the checksum list at $sums_url names $matches aggrega binaries for $os/$arch, where it should name one" "$other_release"
  fi
  case "$asset_sum" in
    *[!0-9a-f]*) refuse "the checksum list at $sums_url lists '$asset_sum' for $asset, which is not a SHA-256 digest" "$other_release" ;;
  esac
  if [ "${#asset_sum}" -ne 64 ]; then
    refuse "the checksum list at $sums_url lists '$asset_sum' for $asset, which is not a SHA-256 digest" "$other_release"
  fi
  version="${asset#aggrega-}"
  version="${version%-"$os-$arch"}"
}

# shellcheck disable=SC2329 # run by the EXIT trap main sets
cleanup() {
  if [ -n "${staged:-}" ]; then
    rm -f -- "$staged"
  fi
  if [ -n "${tmp:-}" ]; then
    rm -rf -- "$tmp"
  fi
}

# path_hint prints the command that adds dir to PATH in the profile of the
# user's login shell.
path_hint() {
  # The folder is written inside single quotes in the command below, so a
  # single quote in it is closed, escaped and reopened.
  quoted_dir="$(printf '%s' "$dir" | sed "s/'/'\\\\''/g")"
  # shellcheck disable=SC2016 # $PATH is the profile's to expand, when the shell reads it.
  case "${SHELL:-}" in
    */zsh) command="echo 'export PATH=\"$quoted_dir:\$PATH\"' >> ~/.zshrc" ;;
    */bash)
      if [ "$os" = darwin ]; then
        command="echo 'export PATH=\"$quoted_dir:\$PATH\"' >> ~/.bash_profile"
      else
        command="echo 'export PATH=\"$quoted_dir:\$PATH\"' >> ~/.bashrc"
      fi
      ;;
    */fish) command="echo 'fish_add_path \"$quoted_dir\"' >> ~/.config/fish/config.fish" ;;
    *) command="echo 'export PATH=\"$quoted_dir:\$PATH\"' >> ~/.profile" ;;
  esac
  printf '\n%s is not on your PATH. Add it, then open a new terminal:\n\n  %s\n' "$dir" "$command"
}

# write_completion <shell> <file> writes the installed aggrega's completion
# script for the shell to the file, replacing it in one step, unless the file
# already holds exactly that script.
write_completion() {
  if ! "$dest" completion "$1" >"$tmp/completion.$1"; then
    complain_completion "running '$dest completion $1' failed"
    return 1
  fi
  if [ -f "$2" ] && cmp -s "$tmp/completion.$1" "$2"; then
    item "$2 already holds aggrega's $1 completion; left it unchanged"
    return 0
  fi
  if ! mkdir -p -- "${2%/*}"; then
    complain_completion "could not create the folder ${2%/*}"
    return 1
  fi
  staged="${2%/*}/.aggrega.completion.$$"
  if ! cp -- "$tmp/completion.$1" "$staged" || ! mv -f -- "$staged" "$2"; then
    complain_completion "could not write $2"
    return 1
  fi
  staged=''
  item "Wrote aggrega's $1 completion to $2"
}

# file_has_line <file> <line> succeeds when the file holds the line exactly.
file_has_line() {
  [ -f "$1" ] || return 1
  while IFS= read -r existing || [ -n "$existing" ]; do
    if [ "$existing" = "$2" ]; then
      return 0
    fi
  done <"$1"
  return 1
}

completion_bash() {
  if [ -n "${BASH_COMPLETION_USER_DIR:-}" ]; then
    folder="${BASH_COMPLETION_USER_DIR%%:*}/completions"
  else
    folder="${XDG_DATA_HOME:-$HOME/.local/share}/bash-completion/completions"
  fi
  write_completion bash "$folder/aggrega" || return 1
  item "bash loads it through the bash-completion package, from a new terminal on"
}

completion_zsh() {
  folder="${XDG_DATA_HOME:-$HOME/.local/share}/zsh/site-functions"
  write_completion zsh "$folder/_aggrega" || return 1
  rc="${ZDOTDIR:-$HOME}/.zshrc"
  marker='# Shell completion for aggrega, added by the aggrega installer.'
  if file_has_line "$rc" "$marker"; then
    item "$rc already loads it; left it unchanged"
    return 0
  fi
  # A file whose last line has no newline gets one first, so the lines start
  # on their own.
  if [ -s "$rc" ] && [ -n "$(tail -c 1 "$rc")" ]; then
    lead='
'
  else
    lead=''
  fi
  # compdef exists once compinit has run; where nothing ran it, the lines run
  # it, as zsh completion needs.
  # shellcheck disable=SC2016 # the third line is zsh's to expand, when zsh reads it.
  if ! printf '%s%s\n%s\n%s\n' "$lead" "$marker" \
    "fpath=($(sh_quote "$folder") \$fpath)" \
    'if (( $+functions[compdef] )); then autoload -Uz _aggrega && compdef _aggrega aggrega; else autoload -Uz compinit && compinit; fi' \
    >>"$rc"; then
    complain_completion "could not add the lines that load it to $rc"
    return 1
  fi
  item "Added the lines that load it to $rc; zsh reads them from a new terminal on"
}

completion_fish() {
  write_completion fish "${XDG_CONFIG_HOME:-$HOME/.config}/fish/completions/aggrega.fish" || return 1
  item "fish loads it from a new terminal on"
}

# setup_completion sets up completion for the shell SHELL names, or reports
# why it did not.
setup_completion() {
  if [ -z "${HOME:-}" ]; then
    complain_completion "HOME is not set, so there is no folder of yours to write it into"
    return 1
  fi
  case "${SHELL:-}" in
    */bash | bash) completion_bash ;;
    */zsh | zsh) completion_zsh ;;
    */fish | fish) completion_fish ;;
    '')
      complain_completion "SHELL is not set, so the shell to set it up for is not known; install.sh sets it up for bash, zsh and fish"
      return 1
      ;;
    *)
      complain_completion "SHELL names $SHELL, and install.sh sets it up for bash, zsh and fish"
      return 1
      ;;
  esac
}

# install_skills runs the installed aggrega's own skill installation: for the
# assistants the --skills list names, or for the ones it finds on this machine.
# Finding them, asking at a terminal and writing are all the command's own, so
# what it prints is what it did. A person at a terminal is asked through it even
# when the script itself is read from a pipe, as under curl | sh; with no
# terminal to ask through, the command reads nothing and installs for the
# assistants it found.
install_skills() {
  set -- skills install --global
  if [ -n "$skills_list" ]; then
    saved_ifs="$IFS"
    IFS=,
    for id in $skills_list; do
      if [ -n "$id" ]; then
        set -- "$@" --agent "$id"
      fi
    done
    IFS="$saved_ifs"
    "$dest" "$@" </dev/null
  elif (exec </dev/tty) 2>/dev/null; then
    "$dest" "$@" </dev/tty
  else
    "$dest" "$@" </dev/null
  fi || {
    complain "could not install the Aggrega skill: 'aggrega skills install' did not succeed, as it says above" "aggrega is installed and runs; run 'aggrega skills install --global' again once that is fixed"
    return 1
  }
}

# next_steps prints one line for each thing to do next: the AI quickstart, the
# skill when --skills did not install it, and the page on shell completion and
# the MCP server.
next_steps() {
  # The name is kept apart from the address on purpose:
  # scripts/check-release-job-isolation.sh reads every path ending in .sh that
  # this script names as a repository script, and refuses one it cannot find.
  quickstart='quickstart-ai.sh'
  printf '\nNext:\n'
  printf '  %-30s%s\n' 'Run the AI quickstart:' "curl -fsSL https://aggrega.ai/$quickstart | sh"
  if [ "$skills_done" != 1 ]; then
    printf '  %-30s%s\n' 'Add the skill to your AI:' 'aggrega skills install --global'
  fi
  printf '  %-30s%s\n' 'Shell completion and MCP:' 'https://aggrega.ai/docs/install/'
}

main() {
  parse_options "$@"
  base="${AGGREGA_MIRROR:-https://github.com/aggrega-ai/aggrega-cli/releases}"
  base="${base%/}"
  detect_platform

  requested="${AGGREGA_VERSION:-}"
  requested="${requested#v}"
  if [ -n "${AGGREGA_VERSION:-}" ]; then
    case "$requested" in
      '' | *[!A-Za-z0-9._+-]*)
        refuse "AGGREGA_VERSION is '$AGGREGA_VERSION', which is not a release version such as 0.8.7 or v0.8.7" "set it to a release version, or unset it to install the latest release"
        ;;
    esac
  fi

  if [ -n "${AGGREGA_INSTALL_DIR:-}" ]; then
    dir="$AGGREGA_INSTALL_DIR"
  elif [ -n "${HOME:-}" ]; then
    dir="$HOME/.local/bin"
  else
    refuse "HOME is not set, so there is no default folder to install into" "set AGGREGA_INSTALL_DIR to the folder to install into"
  fi
  case "$dir" in
    /*) ;;
    *) dir="$(pwd)/$dir" ;;
  esac
  while [ "$dir" != / ] && [ "${dir%/}" != "$dir" ]; do
    dir="${dir%/}"
  done
  dest="$dir/aggrega"
  elsewhere="check that you can write there, or set AGGREGA_INSTALL_DIR to another folder"
  if [ -d "$dest" ]; then
    refuse "$dest is a folder, so aggrega cannot be installed under that name" "move that folder away, or set AGGREGA_INSTALL_DIR to another folder"
  fi

  pick_tools

  staged='' tmp=''
  trap cleanup EXIT
  trap 'exit 129' HUP
  trap 'exit 130' INT
  trap 'exit 143' TERM
  tmp="$(mktemp -d 2>/dev/null || mktemp -d -t aggrega-install)" ||
    refuse "could not create a temporary folder to download into" "check that the temporary folder, TMPDIR when it is set, can be written to"

  if [ -n "$requested" ]; then
    sums_url="$base/download/v$requested/SHA256SUMS"
  else
    sums_url="$base/latest/download/SHA256SUMS"
  fi
  if ! fetch "$sums_url" "$tmp/SHA256SUMS"; then
    if [ -n "$requested" ]; then
      refuse "could not download $sums_url" "check that $requested is a published release"
    fi
    refuse "could not download $sums_url" "check your network connection and run the installer again"
  fi
  find_asset "$tmp/SHA256SUMS"

  url="$base/download/v$version/$asset"
  printf 'Installing aggrega %s (%s-%s)\n' "$version" "$os" "$arch"
  if [ -n "$verbose" ]; then
    item "Checksum list:  $sums_url"
    item "Release file:   $asset"
    item "Downloaded from $url"
  fi
  fetch "$url" "$tmp/$asset" progress ||
    refuse "could not download $url" "check your network connection and run the installer again"
  actual="$(digest "$tmp/$asset")" ||
    refuse "could not compute the SHA-256 checksum of the downloaded $asset" "run the installer again"
  if [ "$actual" != "$asset_sum" ]; then
    rm -f -- "$tmp/$asset"
    refuse "the SHA-256 checksum of the downloaded $asset is $actual, but SHA256SUMS lists $asset_sum; the download was deleted and nothing was installed" "run the installer again"
  fi
  item "Downloaded and checked against its SHA-256 checksum"

  # The new binary is written beside the old one under a temporary name and
  # then renamed over it, so the folder holds either the earlier aggrega or
  # the new one at every moment, never a partial file.
  mkdir -p -- "$dir" || refuse "could not create the folder $dir" "$elsewhere"
  staged="$dir/.aggrega.install.$$"
  cp -- "$tmp/$asset" "$staged" || refuse "could not write into $dir" "$elsewhere"
  chmod 755 "$staged" || refuse "could not mark $staged executable" "$elsewhere"
  mv -f -- "$staged" "$dest" || refuse "could not put the new aggrega in place at $dest" "$elsewhere"
  staged=''

  item "Installed $dest"
  # Checked before completion or the skill is set up, which run it too; the
  # version it prints is shown below.
  if ! "$dest" version >/dev/null 2>&1; then
    refuse "installed $dest, but running '$dest version' failed" "run '$dest version' to see why"
  fi

  status=0 skills_done=''
  if [ -n "$want_completion" ]; then
    setup_completion || status=3
  fi

  case ":${PATH:-}:" in
    *":$dir:"* | *":$dir/:"*)
      found="$(command -v aggrega 2>/dev/null || true)"
      if [ -n "$found" ] && [ "$found" != "$dest" ]; then
        printf '\nNote: the aggrega that runs by name is %s, which comes before %s on your PATH.\n' "$found" "$dir"
      fi
      ;;
    *) path_hint ;;
  esac

  if [ -n "$want_skills" ]; then
    printf '\n'
    if install_skills; then
      skills_done=1
    elif [ "$status" = 3 ]; then
      status=5
    else
      status=4
    fi
  fi

  printf '\n'
  if ! "$dest" version; then
    refuse "installed $dest, but running '$dest version' failed" "run '$dest version' to see why"
  fi
  next_steps
  exit "$status"
}

main "$@"
