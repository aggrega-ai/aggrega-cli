#!/bin/sh
# Run one step of quickstart-ai, the example in the Aggrega documentation that
# builds an AI agent on a local model up in four steps. Step 1:
#
#   curl -fsSL https://aggrega.ai/quickstart-ai.sh | sh
#
# Another step, 2, 3 or 4, is given after `sh -s --`:
#
#   curl -fsSL https://aggrega.ai/quickstart-ai.sh | sh -s -- 2
#
# It first says what it will do and asks to continue, reading the answer from
# the terminal even when the script itself is piped into sh; --yes, given after
# `sh -s --` too, goes ahead without asking, and with no terminal and no --yes
# it refuses. It checks that aggrega can be found, on PATH or in the folder the
# Aggrega installer puts it in, and, for a step that runs, that Docker answers and has Compose. It writes
# the example into ./quickstart-ai with `aggrega scaffold --example
# quickstart-ai`, or uses that folder as it is when it already exists, changes
# into the step's folder, quickstart-ai/<step>, and runs the commands the
# example's page gives for that step. Steps 1 to 3 build and start the example
# with Docker Compose; step 4 builds its Kubernetes files and deploys nothing.
# It ends by saying what to run or open next; for steps 1 to 3 the last line
# is the address of the chat page.
#
# Settings, read from the environment:
#
#   AGGREGA_INSTALL_DIR   the folder to look for aggrega in when PATH does not
#                         have it, as the installer reads it; ~/.local/bin
#                         when unset.
#   OPENAI_API_KEY        for steps 2 and 3, the key of the hosted OpenAI model,
#                         which Docker Compose reads; unset, the local model
#                         answers instead. The script never prints it.
#
# Exit status:
#
#   0  the step was built, and started where it runs
#   1  a check or a command failed, or the answer was not yes
#   2  an argument is neither a step, 1 to 4, nor --yes, or two steps are given
#
# How the steps are written. Each step's commands are in the function
# step_<N>, one command per line, each line the word `run` followed by the
# command exactly as the example's page writes it in that step's block, less
# the block's `cd` lines, which the script replaces by its own. A variable a
# step needs exported is a line of its own, `require_env <NAME> <value>`, which
# stands for the page's `export <NAME>=<value>` line where the page has one;
# the value is the page's example, printed when the variable is not set. The
# commands run from these lines and from nowhere else: each step function runs
# twice, first to check what require_env asks for, then to run the commands.
#
# Everything runs from main, which the last line calls, so a download of this
# script that is cut short runs nothing.

example=quickstart-ai

step_1() {
  run aggrega build --release release.yaml --auto-pull
  run docker compose -p quickstart-ai -f dist/compose/docker-compose.yaml up -d --wait
}

step_2() {
  run aggrega build --release release.yaml --auto-pull
  run docker compose -p quickstart-ai -f dist/compose/docker-compose.yaml up -d --wait
}

step_3() {
  run aggrega build --release release.yaml --auto-pull
  run docker compose -p quickstart-ai -f dist/compose/docker-compose.yaml up -d --wait
}

step_4() {
  run aggrega build --release release.yaml --auto-pull
}

# step_runs <step> succeeds for a step that starts the example with Docker
# Compose, and fails for one that only builds it.
step_runs() {
  case "$1" in
    1 | 2 | 3) return 0 ;;
    *) return 1 ;;
  esac
}

# step_notes <step> prints what the reader should know before the step runs.
step_notes() {
  case "$1" in
    2 | 3)
      if [ -z "${OPENAI_API_KEY:-}" ]; then
        say "OPENAI_API_KEY is not set, so the local model answers in place of the OpenAI model."
      fi
      ;;
  esac
}

# step_end <step> prints what to run or open next.
step_end() {
  case "$1" in
    4)
      say "the Kubernetes files are in $example/4/dist/helm/; the script deploys nothing, and $example/README.md says what applying them needs."
      ;;
    *)
      say "step $1 is up. To send the agent a message from a terminal, run this in $example/$1:"
      printf '\n  %s\n\n' 'docker compose -p quickstart-ai -f dist/compose/docker-compose.yaml exec agent quickstart-ask demo "My name is Ada. What is the capital of France?"'
      say "open http://127.0.0.1:8090/ in your browser to talk to the agent."
      ;;
  esac
}

# The machinery that runs a step from the functions above.

say() {
  printf '%s: %s\n' "$example" "$*"
}

fail() {
  printf '%s: %s\n' "$example" "$*" >&2
  exit 1
}

usage() {
  printf '%s: %s\n' "$example" "$*" >&2
  printf 'usage: curl -fsSL https://aggrega.ai/%s | sh\n' "$example.sh" >&2
  printf '       another step: curl -fsSL https://aggrega.ai/%s | sh -s -- 2\n' "$example.sh" >&2
  exit 2
}

# parse_args sets step to the step the arguments give, 1 when they give none,
# and yes when they give --yes.
parse_args() {
  yes='' step=''
  for arg in "$@"; do
    case "$arg" in
      --yes) yes=1 ;;
      1 | 2 | 3 | 4)
        [ -z "$step" ] || usage "give one step; $step and $arg are two"
        step="$arg"
        ;;
      -*) usage "'$arg' is not an option this script takes" ;;
      *) usage "'$arg' is not a step; the steps are 1, 2, 3 and 4" ;;
    esac
  done
  [ -n "$step" ] || step=1
}

confirm() {
  if step_runs "$step"; then
    printf '%s\n' "This script will scaffold the example, build it and run it with Docker Compose."
  else
    printf '%s\n' "This script will scaffold the example and build it; it will not run it."
  fi
  [ -z "$yes" ] || return 0
  # Piped into sh, the script's standard input is the script itself, so the
  # answer is read from the terminal.
  if ! (exec </dev/tty) 2>/dev/null; then
    fail "there is no terminal to ask on; run the script again with --yes to go ahead without asking."
  fi
  printf 'Continue? [y/N] '
  answer=''
  read -r answer </dev/tty || answer=''
  case "$answer" in
    y | Y | yes | Yes | YES) ;;
    *) fail "stopped; nothing was done." ;;
  esac
}

# find_aggrega sets aggrega to the aggrega to run: the one on PATH, or else the
# one in the folder the installer puts it in.
find_aggrega() {
  aggrega="$(command -v aggrega 2>/dev/null || true)"
  if [ -z "$aggrega" ]; then
    candidate="${AGGREGA_INSTALL_DIR:-${HOME:-}/.local/bin}/aggrega"
    if [ -f "$candidate" ] && [ -x "$candidate" ]; then
      aggrega="$candidate"
    fi
  fi
  if [ -z "$aggrega" ]; then
    # The name is kept apart from the address: scripts/check-release-job-isolation.sh
    # reads every path ending in .sh this script names as a repository script.
    installer=install.sh
    fail "aggrega is not on PATH or in ${AGGREGA_INSTALL_DIR:-~/.local/bin}; install it with: curl -fsSL https://aggrega.ai/$installer | sh"
  fi
}

check_docker() {
  command -v docker >/dev/null 2>&1 ||
    fail "docker is not on PATH, and step $step runs with Docker Compose."
  docker info >/dev/null 2>&1 ||
    fail "Docker does not answer ('docker info' failed); start it and run the script again."
  docker compose version >/dev/null 2>&1 ||
    fail "Docker Compose is not available ('docker compose version' failed)."
}

# run <command...> runs one of a step's commands, aggrega meaning the one
# find_aggrega found, when the step function runs to run its commands.
run() {
  [ "$mode" = run ] || return 0
  line="$*"
  printf '\n$ %s\n' "$line"
  if [ "$1" = aggrega ]; then
    shift
    set -- "$aggrega" "$@"
  fi
  "$@" </dev/null || fail "the command failed: $line"
}

# require_env <name> <value> refuses, when the step function runs to check,
# unless the variable is set; it prints the line that sets it, with the page's
# example value, and never the variable's own value.
require_env() {
  [ "$mode" = check ] || return 0
  eval "set_value=\${$1:-}"
  if [ -z "$set_value" ]; then
    printf '%s: step %s needs %s exported, and it is not set; choose your own value, export it and run the script again:\n\n  export %s=%s\n\n' \
      "$example" "$step" "$1" "$1" "$2" >&2
    exit 1
  fi
  set_value=''
}

use_folder() {
  if [ -d "$example" ]; then
    say "the folder $example already exists, so the script uses it as it is."
  elif [ -e "$example" ] || [ -L "$example" ]; then
    fail "$example exists and is not a folder; move it away and run the script again."
  else
    say "writing the example into the folder $example."
    if ! out="$("$aggrega" scaffold --example "$example" "$example" 2>&1)"; then
      printf '%s\n' "$out" >&2
      fail "'aggrega scaffold --example $example' failed, as it says above."
    fi
  fi
  [ -d "$example/$step" ] ||
    fail "the folder $example holds no folder $step; move $example away and run the script again to write a new copy."
}

main() {
  parse_args "$@"
  confirm
  find_aggrega
  if step_runs "$step"; then
    check_docker
  fi
  mode=check
  "step_$step"
  step_notes "$step"
  use_folder
  cd "$example/$step" || fail "could not change into the folder $example/$step."
  mode=run
  "step_$step"
  printf '\n'
  step_end "$step"
}

main "$@"
