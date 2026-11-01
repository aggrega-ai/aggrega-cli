#!/bin/sh
# Install the aggrega command for the current user on Linux or macOS:
#
#   curl -fsSL https://aggrega.ai/install.sh | sh
#
# It downloads the default aggrega binary for this operating system and
# processor from a release, checks it against the release's SHA256SUMS list,
# and installs it as ~/.local/bin/aggrega, replacing an earlier copy there in
# one step. Then it runs the installed binary to print its version, and, when
# the folder is not on PATH, prints the line that puts it there. It ends by
# printing the next steps: setting up shell completion, installing the Aggrega
# skill for an AI assistant, and connecting an AI client to the Aggrega MCP
# server. It never uses sudo, and it edits a shell profile only when
# --completion sets up completion for zsh.
#
# Beyond the standard system utilities it needs curl or wget, uname, and
# sha256sum or shasum.
#
# Options, written after `sh -s --` when the script is piped into sh:
#
#   curl -fsSL https://aggrega.ai/install.sh | sh -s -- --completion --skills
#
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
#                          found. It prints each skill folder it wrote.
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
# A failed step says why in the message before the next steps.
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

# complain reports a step that failed without stopping the script.
complain() {
  printf 'aggrega install: %s\n' "$*" >&2
}

# usage_refusal reports an option the script cannot use and stops with exit
# status 2, before anything is done.
usage_refusal() {
  printf "aggrega install: %s; it takes --completion and --skills [<list>], written after 'sh -s --' when the script is piped into sh\n" "$*" >&2
  exit 2
}

# Sets want_completion, want_skills and skills_list from the options, or
# refuses with exit status 2 before anything is done. A value after --skills
# that does not start with a dash is its list of assistants.
parse_options() {
  want_completion='' want_skills='' skills_list=''
  while [ "$#" -gt 0 ]; do
    case "$1" in
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

# shellcheck disable=SC2329 # run by the EXIT trap main sets
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

# write_completion <shell> <file> writes the installed aggrega's completion
# script for the shell to the file, replacing it in one step, unless the file
# already holds exactly that script.
write_completion() {
  if ! "$dest" completion "$1" >"$tmp/completion.$1"; then
    complain "could not set up shell completion: running '$dest completion $1' failed"
    return 1
  fi
  if [ -f "$2" ] && cmp -s "$tmp/completion.$1" "$2"; then
    say "$2 already holds aggrega's $1 completion; left it unchanged"
    return 0
  fi
  if ! mkdir -p -- "${2%/*}"; then
    complain "could not set up shell completion: could not create the folder ${2%/*}"
    return 1
  fi
  staged="${2%/*}/.aggrega.completion.$$"
  if ! cp -- "$tmp/completion.$1" "$staged" || ! mv -f -- "$staged" "$2"; then
    complain "could not set up shell completion: could not write $2"
    return 1
  fi
  staged=''
  say "wrote aggrega's $1 completion to $2"
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
  say "bash loads it through the bash-completion package, from a new terminal on"
}

completion_zsh() {
  folder="${XDG_DATA_HOME:-$HOME/.local/share}/zsh/site-functions"
  write_completion zsh "$folder/_aggrega" || return 1
  rc="${ZDOTDIR:-$HOME}/.zshrc"
  marker='# Shell completion for aggrega, added by the aggrega installer.'
  if file_has_line "$rc" "$marker"; then
    say "$rc already loads it; left it unchanged"
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
    complain "could not set up shell completion: could not add the lines that load it to $rc"
    return 1
  fi
  say "added the lines that load it to $rc; zsh reads them from a new terminal on"
}

completion_fish() {
  write_completion fish "${XDG_CONFIG_HOME:-$HOME/.config}/fish/completions/aggrega.fish" || return 1
  say "fish loads it from a new terminal on"
}

# setup_completion sets up completion for the shell SHELL names, or reports
# why it did not.
setup_completion() {
  if [ -z "${HOME:-}" ]; then
    complain "could not set up shell completion: HOME is not set, so there is no folder of yours to write it into"
    return 1
  fi
  case "${SHELL:-}" in
    */bash | bash) completion_bash ;;
    */zsh | zsh) completion_zsh ;;
    */fish | fish) completion_fish ;;
    '')
      complain "could not set up shell completion: SHELL is not set, so the shell to set it up for is not known; install.sh sets it up for bash, zsh and fish"
      return 1
      ;;
    *)
      complain "could not set up shell completion: SHELL names $SHELL, and install.sh sets it up for bash, zsh and fish"
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
    complain "could not install the Aggrega skill: 'aggrega skills install' did not succeed, as it says above"
    return 1
  }
}

# next_steps prints one command for each thing left to set up.
next_steps() {
  settings=''
  if [ -n "${AGGREGA_MIRROR:-}" ]; then
    settings="${settings}AGGREGA_MIRROR=$(sh_quote "$base") "
  fi
  if [ -n "$requested" ]; then
    settings="${settings}AGGREGA_VERSION=$(sh_quote "$requested") "
  fi
  if [ -n "${AGGREGA_INSTALL_DIR:-}" ]; then
    settings="${settings}AGGREGA_INSTALL_DIR=$(sh_quote "$dir") "
  fi
  shell_name="${SHELL##*/}"
  # The command below fetches this script again under the name the release
  # publishes it as. The name is kept apart from the address on purpose:
  # scripts/check-release-job-isolation.sh reads every path ending in .sh that
  # this script names as a repository script, and refuses one it cannot find.
  installer=install.sh
  say "next steps:"
  printf '\n'
  if [ "$completion_done" = 1 ]; then
    printf '  Shell completion for %s is set up; a new terminal loads it.\n' "$shell_name"
  else
    case "$shell_name" in
      bash | zsh | fish)
        printf '  Set up shell completion for %s:\n' "$shell_name"
        printf '    curl -fsSL %s | %ssh -s -- --completion\n' "$(sh_quote "$base/latest/download/$installer")" "$settings"
        ;;
      *)
        printf '  Set up shell completion, for one of the shells aggrega writes it for:\n'
        printf '    aggrega completion --help\n'
        ;;
    esac
  fi
  if [ "$skills_done" = 1 ]; then
    printf '  The Aggrega skill is installed; a new session of each assistant reads it.\n'
  else
    printf '  Install the Aggrega skill for the AI assistants on this machine:\n'
    printf '    aggrega skills install --global\n'
  fi
  printf '  Connect your AI client to the Aggrega MCP server, as this page describes:\n'
  printf '    https://aggrega.ai/docs/install/#connect-an-mcp-client\n'
  printf '\n'
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

  status=0 completion_done='' skills_done=''
  if [ -n "$want_completion" ]; then
    if setup_completion; then
      completion_done=1
    else
      status=3
    fi
  fi
  if [ -n "$want_skills" ]; then
    if install_skills; then
      skills_done=1
    elif [ "$status" = 3 ]; then
      status=5
    else
      status=4
    fi
  fi
  next_steps
  exit "$status"
}

main "$@"
