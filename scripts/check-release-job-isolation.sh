#!/usr/bin/env bash
# aggrega-cli/scripts/check-release-job-isolation.sh — refuse a release workflow
# in which third-party code can reach the credentials that sign and publish.
#
# Building runs code this repository does not control: packages fetched from a
# package registry, a toolchain's and its dependencies' build logic, a base
# image. Every step of a GitHub Actions job can read what that job holds — its
# environment, a token a checkout leaves in a repository's git configuration,
# and, in a job granted id-token: write, the request credentials that mint an
# OIDC identity token, which is the identity a job signs as, keyless or through a
# token exchanged for it. So the release workflow keeps every job that signs free
# of build code, and this script reads the workflow and refuses it, naming the
# job and the step, when:
#
#   1. the file-level permissions grant id-token: write (or write-all), which
#      every job declaring no permissions of its own inherits;
#   2. a job that may mint the token — its permissions grant id-token: write or
#      write-all, or it declares none and the file level grants it — has a step
#      that runs a build or pack command, in its run: body or in a repository
#      script that body names, read through every script that one sources or
#      names in turn; or a step that runs any action but actions/checkout,
#      actions/download-artifact and actions/upload-artifact (GitHub's own
#      artifact actions, the last so that a job which signs files can hand them
#      on); or calls a reusable workflow; or declares a container: or services:,
#      whose images run beside its steps;
#   3. an actions/checkout step, in any job, does not set
#      persist-credentials: false;
#   4. a job that may not mint the token but runs third-party code — any step
#      that is not a checkout, an artifact download or upload, or a run: step
#      free of build commands — carries a secret or github.token in an env: or a
#      run: body, or names anywhere a secret that a job minting the token also
#      names. A secret reaches such a job only through an action's with:.
#
# The build and pack commands are npx, npm, pnpm, yarn, cargo, make, tinygo;
# go build, go run, go test, go generate, go install, go get; and docker build,
# docker buildx build, buildx build, docker image build, docker compose build. A
# command counts wherever it stands on a line that is not a comment, inside a
# string too: this reads text, so where a line merely names one it refuses
# rather than guesses. A repository script is any token ending in .sh, with a
# shell variable before it ("$SCRIPT_DIR/lib/x.sh") dropped, looked up against
# the module root, its scripts/ and .github/scripts/ directories and the
# directory of the script naming it, a leading aggrega-cli/ standing for the
# module root; a token found in none of them is refused, since a script that
# cannot be read cannot be cleared.
#
# What it does not see: a program in another language that a minting job runs
# (only .sh references are followed, and node, python or java are not
# themselves refused); what a binary the job runs does once running, the aggrega
# binary among them; a command assembled at run time from variables. It reads the
# workflow's structure with yq (mikefarah, version 4), the YAML reader this
# workspace's record gates use, and needs nothing else.
#
# Usage:
#   scripts/check-release-job-isolation.sh [<workflow.yml>]
#
# <workflow.yml> defaults to .github/workflows/publish-plugins-sign.yml under
# the module root. It exits 0 when every rule holds, printing what it read; 1,
# naming each violation; and 2 when the workflow cannot be read or yq is not
# mikefarah's version 4.
#
# Exit non-zero when any rule is broken.
set -uo pipefail

CLI_ROOT="$(cd "$(dirname "$0")/.." && pwd -P)"
LABEL_ROOT="$(dirname "$CLI_ROOT")"

case "${1:-}" in
  -h|--help) sed -n '2,/^# Exit non-zero when any rule is broken\.$/p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
esac
[ "$#" -le 1 ] || { echo "check-release-job-isolation: one workflow file at most" >&2; exit 2; }
WORKFLOW="${1:-$CLI_ROOT/.github/workflows/publish-plugins-sign.yml}"

command -v yq >/dev/null 2>&1 || { echo "check-release-job-isolation: yq is not on PATH" >&2; exit 2; }
yq_version="$(yq --version 2>&1)"
case "$yq_version" in
  *mikefarah*" v4."*) ;;
  *) echo "check-release-job-isolation: yq is not mikefarah's version 4 ($yq_version)" >&2; exit 2 ;;
esac
[ -f "$WORKFLOW" ] || { echo "check-release-job-isolation: no such file: $WORKFLOW" >&2; exit 2; }
if ! yq -e '.' "$WORKFLOW" >/dev/null 2>&1; then
  echo "check-release-job-isolation: $WORKFLOW does not parse as YAML" >&2
  exit 2
fi
if [ "$(yq -r '.jobs | tag' "$WORKFLOW")" != '!!map' ]; then
  echo "check-release-job-isolation: $WORKFLOW has no jobs: map" >&2
  exit 2
fi

# The command patterns, each a bash extended regular expression whose second
# group is the command. A command is not part of a longer word: a letter, digit,
# underscore, dot or hyphen on either side makes it something else
# (build-mcp-bundle.sh, scripts/make-x.sh), while a slash before it does not
# (/usr/bin/npx).
B='(^|[^A-Za-z0-9_.-])'
E='([^A-Za-z0-9_-]|$)'
COMMAND_RES=(
  "${B}(npx|npm|pnpm|yarn|cargo|make|tinygo)${E}"
  "${B}(go[[:space:]]+(build|run|test|generate|install|get))${E}"
  "${B}(docker[[:space:]]+(buildx[[:space:]]+|image[[:space:]]+|compose[[:space:]]+)?build|buildx[[:space:]]+build)${E}"
)
SCRIPT_RE='([A-Za-z0-9_./-]*[A-Za-z0-9_-]\.sh)([^A-Za-z0-9_.-]|$)'
VAR_RE='\$\{?[A-Za-z_][A-Za-z0-9_]*\}?'

violations=()
violate() { violations+=("$1"); }

label_of() {
  case "$1" in
    "$LABEL_ROOT"/*) printf '%s\n' "${1#"$LABEL_ROOT"/}" ;;
    *) printf '%s\n' "$1" ;;
  esac
}

# resolve_script <token> <dir of the text naming it>: prints the file a script
# token names, or nothing.
resolve_script() {
  local tok="${1#./}" dir="$2" rel cand
  rel="${tok#/}"
  local -a cands=()
  case "$tok" in
    aggrega-cli/*) cands+=("$CLI_ROOT/${tok#aggrega-cli/}") ;;
  esac
  cands+=("$CLI_ROOT/$rel" "$CLI_ROOT/scripts/$rel" "$CLI_ROOT/.github/scripts/$rel" "$dir/$rel")
  for cand in "${cands[@]}"; do
    if [ -f "$cand" ]; then
      (cd "$(dirname "$cand")" && printf '%s/%s\n' "$(pwd -P)" "$(basename "$cand")")
      return 0
    fi
  done
  return 0
}

# scan_text <text> <label> <dir>: appends to HITS each build command found on a
# line of <text> that is not a comment, as "<label>:<line> runs `<command>`";
# follows each script token into scan_script; records a token it cannot find.
declare -A SEEN=()
HITS=()
READ_SCRIPTS=()
scan_text() {
  local text="$1" label="$2" dir="$3" line trimmed lineno=0 re rest tok file
  while IFS= read -r line || [ -n "$line" ]; do
    lineno=$((lineno + 1))
    trimmed="${line#"${line%%[![:space:]]*}"}"
    [ -n "$trimmed" ] || continue
    case "$trimmed" in \#*) continue ;; esac
    for re in "${COMMAND_RES[@]}"; do
      if [[ "$line" =~ $re ]]; then
        HITS+=("$label:$lineno runs \`${BASH_REMATCH[2]}\`")
      fi
    done
    # A variable standing before a path ("$SCRIPT_DIR/lib/x.sh") is dropped, so
    # the path is looked up from the places above rather than read as part of
    # the name.
    rest="$line"
    while [[ "$rest" =~ $VAR_RE ]]; do
      rest="${rest/"${BASH_REMATCH[0]}"/}"
    done
    while [[ "$rest" =~ $SCRIPT_RE ]]; do
      tok="${BASH_REMATCH[1]}"
      rest="${rest#*"$tok"}"
      file="$(resolve_script "$tok" "$dir")"
      if [ -z "$file" ]; then
        HITS+=("$label:$lineno names $tok, which is no file this check can read")
        continue
      fi
      scan_script "$file"
    done
  done <<< "$text"
}

scan_script() {
  local file="$1"
  [ -z "${SEEN[$file]:-}" ] || return 0
  SEEN[$file]=1
  READ_SCRIPTS+=("$(label_of "$file")")
  scan_text "$(cat "$file")" "$(label_of "$file")" "$(dirname "$file")"
}

# scan_step_run <run body>: resets the per-step state and scans the body as the
# workflow runs it, from the directory above the module root.
scan_step_run() {
  SEEN=()
  HITS=()
  READ_SCRIPTS=()
  scan_text "$1" "run" "$LABEL_ROOT"
}

# grants_id_token <yq path of a permissions node>: yes when it grants
# id-token: write, directly or through write-all.
grants_id_token() {
  local node="$1" tag
  tag="$(yq -r "$node | tag" "$WORKFLOW")"
  case "$tag" in
    '!!str') [ "$(yq -r "$node" "$WORKFLOW")" = write-all ] && echo yes || echo no ;;
    '!!map') [ "$(yq -r "$node.\"id-token\" // \"\"" "$WORKFLOW")" = write ] && echo yes || echo no ;;
    *) echo no ;;
  esac
}

# secrets_in <text>: prints each secrets.<NAME> and github.token the text names,
# one per line.
SECRET_RE='(secrets\.[A-Za-z_][A-Za-z0-9_]*|github\.token)'
secrets_in() {
  local rest="$1"
  while [[ "$rest" =~ $SECRET_RE ]]; do
    printf '%s\n' "${BASH_REMATCH[1]}"
    rest="${rest#*"${BASH_REMATCH[1]}"}"
  done
}

TOP_GRANTS="$(grants_id_token '.permissions')"
if [ "$TOP_GRANTS" = yes ]; then
  violate "file level: permissions grant id-token: write, which every job declaring no permissions of its own inherits; grant it only in the jobs that sign"
fi

mapfile -t JOBS < <(yq -r '.jobs | keys | .[]' "$WORKFLOW")
declare -A HOLDS=() THIRD_PARTY=()
checkouts=0
holder_steps=0
declare -A HOLDER_SECRETS=()
HOLDER_READ=()

# Pass 1: which jobs may mint the token, the steps of those jobs, and every
# checkout.
for job in "${JOBS[@]}"; do
  export J="$job"
  if [ "$(yq -r '.jobs[strenv(J)] | has("permissions")' "$WORKFLOW")" = true ]; then
    HOLDS[$job]="$(grants_id_token '.jobs[strenv(J)].permissions')"
  else
    HOLDS[$job]="$TOP_GRANTS"
  fi
  THIRD_PARTY[$job]=""
  holds="${HOLDS[$job]}"

  if [ "$holds" = yes ]; then
    for key in container services uses; do
      if [ "$(KEY="$key" yq -r '.jobs[strenv(J)] | has(strenv(KEY))' "$WORKFLOW")" = true ]; then
        case "$key" in
          uses) violate "job $job: calls a reusable workflow, whose steps this check cannot read, while it may mint the OIDC token" ;;
          *) violate "job $job: declares $key:, whose images run beside its steps, while it may mint the OIDC token" ;;
        esac
      fi
    done
    while IFS= read -r ref; do
      [ -n "$ref" ] && HOLDER_SECRETS[$ref]="$job"
    done < <(secrets_in "$(yq -o=json '.jobs[strenv(J)]' "$WORKFLOW")")
  elif [ "$(yq -r '.jobs[strenv(J)] | has("uses")' "$WORKFLOW")" = true ]; then
    THIRD_PARTY[$job]="it calls a reusable workflow"
  fi

  nsteps="$(yq -r '.jobs[strenv(J)].steps | length' "$WORKFLOW")"
  case "$nsteps" in ''|null) nsteps=0 ;; esac
  for ((i = 0; i < nsteps; i++)); do
    export I="$i"
    name="$(yq -r '.jobs[strenv(J)].steps[env(I)].name // ""' "$WORKFLOW")"
    [ -n "$name" ] || name="$(yq -r '.jobs[strenv(J)].steps[env(I)].id // ""' "$WORKFLOW")"
    [ -n "$name" ] || name="step $((i + 1))"
    uses="$(yq -r '.jobs[strenv(J)].steps[env(I)].uses // ""' "$WORKFLOW")"
    run="$(yq -r '.jobs[strenv(J)].steps[env(I)].run // ""' "$WORKFLOW")"
    where="job $job, step \"$name\""

    case "$uses" in
      actions/checkout@*|actions/checkout)
        checkouts=$((checkouts + 1))
        persist="$(yq -r '.jobs[strenv(J)].steps[env(I)].with."persist-credentials"' "$WORKFLOW")"
        if [ "$persist" != false ]; then
          violate "$where: actions/checkout without persist-credentials: false (found: $persist), which leaves its token in the checkout's git configuration for every later step"
        fi
        ;;
    esac

    if [ -n "$uses" ]; then
      case "$uses" in
        actions/checkout@*|actions/checkout|actions/download-artifact@*|actions/upload-artifact@*) ;;
        *)
          if [ "$holds" = yes ]; then
            violate "$where: uses $uses in a job that may mint the OIDC token; only actions/checkout, actions/download-artifact and actions/upload-artifact run there"
          elif [ -z "${THIRD_PARTY[$job]}" ]; then
            THIRD_PARTY[$job]="step \"$name\" runs $uses"
          fi
          ;;
      esac
    fi

    if [ -n "$run" ]; then
      scan_step_run "$run"
      if [ "$holds" = yes ]; then
        holder_steps=$((holder_steps + 1))
        for s in ${READ_SCRIPTS[@]+"${READ_SCRIPTS[@]}"}; do HOLDER_READ+=("$s"); done
        for hit in ${HITS[@]+"${HITS[@]}"}; do
          violate "$where: $hit, in a job that may mint the OIDC token"
        done
      elif [ "${#HITS[@]}" -gt 0 ] && [ -z "${THIRD_PARTY[$job]}" ]; then
        THIRD_PARTY[$job]="step \"$name\": ${HITS[0]}"
      fi
    elif [ "$holds" = yes ] && [ -n "$uses" ]; then
      holder_steps=$((holder_steps + 1))
    fi
  done
done

# Pass 2: what a job running third-party code can reach.
TP_REPORT=()
for job in "${JOBS[@]}"; do
  [ "${HOLDS[$job]}" = yes ] && continue
  [ -n "${THIRD_PARTY[$job]}" ] || continue
  export J="$job"
  TP_REPORT+=("$job (${THIRD_PARTY[$job]})")
  # The environment every step of the job sees, and each step's own.
  exposed="$(yq -o=json '.env // {}' "$WORKFLOW")"$'\n'"$(yq -o=json '.jobs[strenv(J)].env // {}' "$WORKFLOW")"
  while IFS= read -r ref; do
    [ -n "$ref" ] && violate "job $job: an env: at the file or job level carries $ref into a job that runs third-party code (${THIRD_PARTY[$job]})"
  done < <(secrets_in "$exposed")
  nsteps="$(yq -r '.jobs[strenv(J)].steps | length' "$WORKFLOW")"
  case "$nsteps" in ''|null) nsteps=0 ;; esac
  for ((i = 0; i < nsteps; i++)); do
    export I="$i"
    name="$(yq -r '.jobs[strenv(J)].steps[env(I)].name // ""' "$WORKFLOW")"
    [ -n "$name" ] || name="step $((i + 1))"
    step_exposed="$(yq -o=json '.jobs[strenv(J)].steps[env(I)].env // {}' "$WORKFLOW")"$'\n'"$(yq -r '.jobs[strenv(J)].steps[env(I)].run // ""' "$WORKFLOW")"
    while IFS= read -r ref; do
      [ -n "$ref" ] && violate "job $job, step \"$name\": its env: or run: carries $ref into a job that runs third-party code (${THIRD_PARTY[$job]}); a secret reaches such a job only through an action's with:"
    done < <(secrets_in "$step_exposed")
  done
  # A secret a minting job holds, named anywhere in this one.
  while IFS= read -r ref; do
    [ -n "$ref" ] || continue
    if [ -n "${HOLDER_SECRETS[$ref]:-}" ] && [ "$ref" != github.token ]; then
      violate "job $job: names $ref, which job ${HOLDER_SECRETS[$ref]} holds to sign or publish; a publishing credential stays out of a job that runs third-party code (${THIRD_PARTY[$job]})"
    fi
  done < <(secrets_in "$(yq -o=json '.jobs[strenv(J)]' "$WORKFLOW")" | sort -u)
done

if [ "${#violations[@]}" -gt 0 ]; then
  for v in "${violations[@]}"; do
    printf 'VIOLATION  %s\n' "$v"
  done
  printf 'release job isolation is broken in %s: %d violation(s)\n' "$(label_of "$WORKFLOW")" "${#violations[@]}"
  RC=1
  exit $RC
fi

holders=()
for job in "${JOBS[@]}"; do
  [ "${HOLDS[$job]}" = yes ] && holders+=("$job")
done
read_uniq=()
if [ "${#HOLDER_READ[@]}" -gt 0 ]; then
  mapfile -t read_uniq < <(printf '%s\n' "${HOLDER_READ[@]}" | sort -u)
fi
printf 'release job isolation holds in %s:\n' "$(label_of "$WORKFLOW")"
printf '  may mint the OIDC token: %s — %d step(s), none a build or pack step, no action but checkout, download-artifact and upload-artifact\n' \
  "${holders[*]:-no job}" "$holder_steps"
printf '  scripts read for those steps: %s\n' "${read_uniq[*]:-none}"
printf '  actions/checkout steps: %d, each with persist-credentials: false\n' "$checkouts"
printf '  jobs running third-party code, each reaching a secret only through an action'"'"'s with: — %s\n' "${TP_REPORT[*]:-none}"
RC=0
exit $RC
