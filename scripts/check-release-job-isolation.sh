#!/usr/bin/env bash
# aggrega-cli/scripts/check-release-job-isolation.sh — refuse a release workflow
# in which third-party code can reach a credential: the ones that sign and
# publish, or any secret the workflow names.
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
#   4. a job that may not mint the token but runs third-party code — a step that
#      runs any action but actions/checkout, actions/download-artifact,
#      actions/upload-artifact and actions/create-github-app-token (GitHub's own
#      action that mints an App installation token from the secrets its with:
#      is handed), a run: step that runs a build or pack command (read as in
#      rule 2), or a call to a reusable workflow — names a secret or github.token
#      in any string of the job: a step's with:, env: or run: body, the job's
#      env:, container:, services: or outputs:, any other field; or hands a
#      reusable workflow secrets:; or the file-level env: names one, which every
#      job sees. So a secret is named only in a job that runs nothing but those
#      four actions and run: steps free of build commands, or in a job rule 2 or
#      rule 5 holds;
#   5. the file-level permissions grant actions: write (or write-all); or a job
#      that may delete the run's artifacts — its permissions grant actions:
#      write or write-all — is granted anything besides actions: write, runs
#      any action at all, calls a reusable workflow, declares a container: or
#      services:, has a step that runs a build or pack command (read as in rule
#      2), or names a secret anywhere. Such a job holds the run's own token,
#      github.token, which it may name, and nothing else;
#   6. a secret whose name begins APPLE_ — the identity that signs the macOS
#      binaries and the key that has them notarized — is named in any job but
#      sign-macos, or in the file-level env:; or sign-macos has a step that
#      runs actions/checkout, a build or pack command (read as in rule 2) or a
#      repository script, runs any action but actions/download-artifact and
#      actions/upload-artifact, or one of those not pinned by a full 40-digit
#      commit; or sign-macos calls a reusable workflow, declares a container:
#      or services:, is granted any permission, names a secret not beginning
#      APPLE_ or github.token, or runs-on anything but a literal label of a
#      GitHub-hosted macOS image (macos-…), which no variable can turn into the
#      machine the build ran on. So the Apple secrets share a job with Apple's
#      own programs, the shell's utilities and GitHub's artifact actions alone.
#
# The build and pack commands are npx, npm, pnpm, yarn, cargo, make, tinygo,
# makensis (which compiles the Windows installer from a script and the plug-ins
# and stubs it fetches); go build, go run, go test, go generate, go install, go
# get; and docker build, docker buildx build, buildx build, docker image build,
# docker compose build. A
# command counts wherever it stands on a line that is not a comment, inside a
# string too: this reads text, so where a line merely names one it refuses
# rather than guesses. A repository script is any token ending in .sh, with a
# shell variable before it ("$SCRIPT_DIR/lib/x.sh") dropped, looked up against
# the root of the repository the script naming it sits in (a directory beside
# the module root, the module root itself for this repository's own), then the
# module root, its scripts/ and .github/scripts/ directories and the directory
# of the script naming it, a leading aggrega-cli/ standing for the module root;
# a token found in none of them is refused, since a script that cannot be read
# cannot be cleared.
#
# What it does not see: a program in another language that a minting job runs
# (only .sh references are followed, and node, python or java are not
# themselves refused); what a binary the job runs does once running, the aggrega
# binary among them; a command assembled at run time from variables; a
# credential handed on as a value rather than named — a token minted in one job
# and passed to another through a job output or a file it leaves behind. It
# reads the workflow's structure with yq (mikefarah, version 4), the YAML reader
# this workspace's record gates use, and needs nothing else.
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
  "${B}(npx|npm|pnpm|yarn|cargo|make|tinygo|makensis)${E}"
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
  # A script in a repository checked out beside the module root names others
  # from that repository's root, as aggrega-hub's scripts name their own.
  case "$dir" in
    "$LABEL_ROOT"/*)
      local repo="${dir#"$LABEL_ROOT"/}"
      cands+=("$LABEL_ROOT/${repo%%/*}/$rel")
      ;;
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

# grants_write <yq path of a permissions node> <scope>: yes when it grants
# <scope>: write, directly or through write-all.
grants_write() {
  local node="$1" tag
  tag="$(yq -r "$node | tag" "$WORKFLOW")"
  case "$tag" in
    '!!str') [ "$(yq -r "$node" "$WORKFLOW")" = write-all ] && echo yes || echo no ;;
    '!!map') [ "$(SCOPE="$2" yq -r "${node}[strenv(SCOPE)] // \"\"" "$WORKFLOW")" = write ] && echo yes || echo no ;;
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

TOP_GRANTS="$(grants_write '.permissions' id-token)"
if [ "$TOP_GRANTS" = yes ]; then
  violate "file level: permissions grant id-token: write, which every job declaring no permissions of its own inherits; grant it only in the jobs that sign"
fi
TOP_DELETES="$(grants_write '.permissions' actions)"
if [ "$TOP_DELETES" = yes ]; then
  violate "file level: permissions grant actions: write, which every job declaring no permissions of its own inherits; grant it only in the job that deletes the run's artifacts"
fi

mapfile -t JOBS < <(yq -r '.jobs | keys | .[]' "$WORKFLOW")
declare -A HOLDS=() DELETES=() THIRD_PARTY=()
checkouts=0
holder_steps=0
deleter_steps=0
HOLDER_READ=()

# Pass 1: which jobs may mint the token and which may delete the run's
# artifacts, the steps of those jobs, and every checkout.
for job in "${JOBS[@]}"; do
  export J="$job"
  if [ "$(yq -r '.jobs[strenv(J)] | has("permissions")' "$WORKFLOW")" = true ]; then
    HOLDS[$job]="$(grants_write '.jobs[strenv(J)].permissions' id-token)"
    DELETES[$job]="$(grants_write '.jobs[strenv(J)].permissions' actions)"
  else
    HOLDS[$job]="$TOP_GRANTS"
    DELETES[$job]="$TOP_DELETES"
  fi
  THIRD_PARTY[$job]=""
  holds="${HOLDS[$job]}"
  deletes="${DELETES[$job]}"

  if [ "$deletes" = yes ]; then
    # Granted actions: write and nothing else: no other scope at any level, and
    # not write-all.
    case "$(yq -r '.jobs[strenv(J)].permissions | tag' "$WORKFLOW")" in
      '!!map')
        while IFS= read -r grant; do
          [ -n "$grant" ] && violate "job $job: may delete the run's artifacts (actions: write) and is granted $grant as well; that job is granted actions: write and nothing else"
        done < <(yq -r '.jobs[strenv(J)].permissions | to_entries | .[] | select(.key != "actions") | .key + ": " + (.value | tostring)' "$WORKFLOW")
        ;;
      '!!str') violate "job $job: may delete the run's artifacts and is granted $(yq -r '.jobs[strenv(J)].permissions' "$WORKFLOW"); that job is granted actions: write and nothing else" ;;
      *) violate "job $job: may delete the run's artifacts through the file-level permissions; that job declares actions: write and nothing else of its own" ;;
    esac
    for key in container services uses; do
      if [ "$(KEY="$key" yq -r '.jobs[strenv(J)] | has(strenv(KEY))' "$WORKFLOW")" = true ]; then
        case "$key" in
          uses) violate "job $job: calls a reusable workflow, whose steps this check cannot read, while it may delete the run's artifacts" ;;
          *) violate "job $job: declares $key:, whose images run beside its steps, while it may delete the run's artifacts" ;;
        esac
      fi
    done
    while IFS= read -r ref; do
      case "$ref" in
        secrets.*) violate "job $job: names $ref, while it may delete the run's artifacts; that job holds the run's own token and no secret" ;;
      esac
    done < <(secrets_in "$(yq -o=json '.jobs[strenv(J)]' "$WORKFLOW")" | sort -u)
  fi

  if [ "$holds" = yes ]; then
    for key in container services uses; do
      if [ "$(KEY="$key" yq -r '.jobs[strenv(J)] | has(strenv(KEY))' "$WORKFLOW")" = true ]; then
        case "$key" in
          uses) violate "job $job: calls a reusable workflow, whose steps this check cannot read, while it may mint the OIDC token" ;;
          *) violate "job $job: declares $key:, whose images run beside its steps, while it may mint the OIDC token" ;;
        esac
      fi
    done
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

    if [ -n "$uses" ] && [ "$deletes" = yes ]; then
      violate "$where: uses $uses in a job that may delete the run's artifacts; no action runs there"
    fi
    if [ -n "$uses" ]; then
      case "$uses" in
        actions/checkout@*|actions/checkout|actions/download-artifact@*|actions/upload-artifact@*) ;;
        *)
          if [ "$holds" = yes ]; then
            violate "$where: uses $uses in a job that may mint the OIDC token; only actions/checkout, actions/download-artifact and actions/upload-artifact run there"
          elif [ -z "${THIRD_PARTY[$job]}" ]; then
            # GitHub's own token action is trusted as the checkout and the
            # artifact actions are, so it does not make a job one that runs
            # third-party code; rules 2 and 5 still refuse it in the jobs they
            # hold.
            case "$uses" in
              actions/create-github-app-token@*) ;;
              *) THIRD_PARTY[$job]="step \"$name\" runs $uses" ;;
            esac
          fi
          ;;
      esac
    fi

    if [ -n "$run" ]; then
      scan_step_run "$run"
      if [ "$deletes" = yes ]; then
        deleter_steps=$((deleter_steps + 1))
        for hit in ${HITS[@]+"${HITS[@]}"}; do
          violate "$where: $hit, in a job that may delete the run's artifacts"
        done
      fi
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

# Pass 2: what a job running third-party code names. Every string of the job is
# read wherever it stands — a step's with:, env: or run: body, the job's env:,
# container:, services:, outputs: or any other field — each reference reported
# with the path it stands at; and the file-level env:, which every job sees.
# NAMED_AT prints "<path from the job>\t<reference>" for each, in document order.
# shellcheck disable=SC2016 # $v and $p are yq's own variables, not the shell's.
NAMED_AT='.jobs[strenv(J)] | .. | select(tag == "!!str") | . as $v | (path | .[2:] | map(tostring) | join(".")) as $p | $v | match("secrets[.][A-Za-z_][A-Za-z0-9_]*|github[.]token"; "g") | $p + "\t" + .string'

# where_in_job <path from the job>: the path, a step's index replaced by its name.
where_in_job() {
  local at="$1" index rest name
  case "$at" in
    steps.*)
      index="${at#steps.}"
      rest="${index#*.}"
      index="${index%%.*}"
      [ "$rest" = "$index" ] && rest=""
      name="$(I="$index" yq -r '.jobs[strenv(J)].steps[env(I)] | .name // .id // ""' "$WORKFLOW")"
      [ -n "$name" ] || name="step $((index + 1))"
      printf 'step "%s"%s\n' "$name" "${rest:+, $rest}"
      ;;
    *) printf '%s\n' "$at" ;;
  esac
}

TP_REPORT=()
NAMING_REPORT=()
TOP_ENV="$(yq -o=json '.env // {}' "$WORKFLOW")"
for job in "${JOBS[@]}"; do
  export J="$job"
  # Every job naming a secret, for the summary.
  named="$(secrets_in "$(yq -o=json '.jobs[strenv(J)]' "$WORKFLOW")" | sort -u)"
  named_secrets=""
  while IFS= read -r ref; do
    case "$ref" in secrets.*) named_secrets="${named_secrets:+$named_secrets }$ref" ;; esac
  done <<< "$named"
  [ -z "$named_secrets" ] || NAMING_REPORT+=("$job ($named_secrets)")

  [ "${HOLDS[$job]}" = yes ] && continue
  [ -n "${THIRD_PARTY[$job]}" ] || continue
  TP_REPORT+=("$job (${THIRD_PARTY[$job]})")
  while IFS= read -r ref; do
    [ -n "$ref" ] && violate "job $job: the file-level env: carries $ref into a job that runs third-party code (${THIRD_PARTY[$job]})"
  done < <(secrets_in "$TOP_ENV")
  if [ "$(yq -r '.jobs[strenv(J)] | has("secrets")' "$WORKFLOW")" = true ]; then
    violate "job $job: hands secrets: to the reusable workflow it calls, which this check cannot read (${THIRD_PARTY[$job]})"
  fi
  while IFS=$'\t' read -r at ref; do
    [ -n "$ref" ] || continue
    violate "job $job, $(where_in_job "$at"): names $ref in a job that runs third-party code (${THIRD_PARTY[$job]}); no secret and no github.token is named in such a job"
  done < <(yq -r "$NAMED_AT" "$WORKFLOW")
done

# Pass 3: the Apple secrets and the one job that may name them. Every
# secrets.APPLE_ reference, wherever it stands, outside that job is refused, and
# that job is held to what rule 6 lets it run.
APPLE_JOB=sign-macos
APPLE_SECRET_RE='secrets\.APPLE_[A-Za-z0-9_]*'
SHA_PIN_RE='^actions/(download-artifact|upload-artifact)@[0-9a-f]{40}$'
apple_jobs=()
while IFS= read -r ref; do
  [[ $ref =~ ^$APPLE_SECRET_RE$ ]] || continue
  violate "file level: env: names $ref, which every job sees; the Apple secrets are named in $APPLE_JOB alone"
done < <(secrets_in "$TOP_ENV" | sort -u)
for job in "${JOBS[@]}"; do
  export J="$job"
  [ "$job" = "$APPLE_JOB" ] && continue
  while IFS=$'\t' read -r at ref; do
    [ -n "$ref" ] || continue
    [[ $ref =~ ^$APPLE_SECRET_RE$ ]] || continue
    violate "job $job, $(where_in_job "$at"): names $ref; the Apple secrets are named in $APPLE_JOB alone"
  done < <(yq -r "$NAMED_AT" "$WORKFLOW")
done
if [ "$(J="$APPLE_JOB" yq -r '.jobs | has(strenv(J))' "$WORKFLOW")" = true ]; then
  export J="$APPLE_JOB"
  apple_jobs+=("$APPLE_JOB")
  apple_steps=0
  for key in container services uses; do
    if [ "$(KEY="$key" yq -r '.jobs[strenv(J)] | has(strenv(KEY))' "$WORKFLOW")" = true ]; then
      violate "job $APPLE_JOB: declares $key:, which runs what this check cannot read beside the Apple secrets"
    fi
  done
  case "$(yq -r '.jobs[strenv(J)] | has("permissions")' "$WORKFLOW")" in
    true)
      while IFS= read -r grant; do
        [ -n "$grant" ] && violate "job $APPLE_JOB: is granted $grant; the job that holds the Apple secrets is granted nothing (permissions: {})"
      done < <(yq -r '.jobs[strenv(J)].permissions | select(tag == "!!map") | to_entries | .[] | .key + ": " + (.value | tostring)' "$WORKFLOW")
      if [ "$(yq -r '.jobs[strenv(J)].permissions | tag' "$WORKFLOW")" != '!!map' ]; then
        violate "job $APPLE_JOB: is granted $(yq -r '.jobs[strenv(J)].permissions' "$WORKFLOW"); the job that holds the Apple secrets is granted nothing (permissions: {})"
      fi
      ;;
    *) violate "job $APPLE_JOB: declares no permissions of its own, so it takes the file level's; the job that holds the Apple secrets is granted nothing (permissions: {})" ;;
  esac
  runs_on="$(yq -r '.jobs[strenv(J)]."runs-on" | select(tag == "!!str")' "$WORKFLOW")"
  if ! [[ $runs_on =~ ^macos-[0-9A-Za-z.-]+$ ]]; then
    violate "job $APPLE_JOB: runs on $(yq -o=json -I=0 '.jobs[strenv(J)]."runs-on"' "$WORKFLOW"); the job that holds the Apple secrets runs on a GitHub-hosted macOS image it names by a literal label (macos-…)"
  fi
  while IFS=$'\t' read -r at ref; do
    [ -n "$ref" ] || continue
    [[ $ref =~ ^$APPLE_SECRET_RE$ ]] && continue
    violate "job $APPLE_JOB, $(where_in_job "$at"): names $ref; the job that holds the Apple secrets names no other secret and not the run's token"
  done < <(yq -r "$NAMED_AT" "$WORKFLOW")
  nsteps="$(yq -r '.jobs[strenv(J)].steps | length' "$WORKFLOW")"
  case "$nsteps" in ''|null) nsteps=0 ;; esac
  for ((i = 0; i < nsteps; i++)); do
    export I="$i"
    apple_steps=$((apple_steps + 1))
    name="$(yq -r '.jobs[strenv(J)].steps[env(I)] | .name // .id // ""' "$WORKFLOW")"
    [ -n "$name" ] || name="step $((i + 1))"
    where="job $APPLE_JOB, step \"$name\""
    uses="$(yq -r '.jobs[strenv(J)].steps[env(I)].uses // ""' "$WORKFLOW")"
    run="$(yq -r '.jobs[strenv(J)].steps[env(I)].run // ""' "$WORKFLOW")"
    if [ -n "$uses" ]; then
      case "$uses" in
        actions/checkout@*|actions/checkout)
          violate "$where: checks out a repository beside the Apple secrets; $APPLE_JOB checks nothing out"
          ;;
        actions/download-artifact@*|actions/upload-artifact@*)
          [[ $uses =~ $SHA_PIN_RE ]] ||
            violate "$where: uses $uses, not pinned by its full commit; beside the Apple secrets every action is pinned by commit"
          ;;
        *)
          violate "$where: uses $uses beside the Apple secrets; only actions/download-artifact and actions/upload-artifact run there"
          ;;
      esac
    fi
    if [ -n "$run" ]; then
      scan_step_run "$run"
      for hit in ${HITS[@]+"${HITS[@]}"}; do
        violate "$where: $hit, beside the Apple secrets"
      done
      for s in ${READ_SCRIPTS[@]+"${READ_SCRIPTS[@]}"}; do
        violate "$where: runs the repository script $s beside the Apple secrets; $APPLE_JOB checks nothing out and runs none"
      done
    fi
  done
fi

if [ "${#violations[@]}" -gt 0 ]; then
  for v in "${violations[@]}"; do
    printf 'VIOLATION  %s\n' "$v"
  done
  printf 'release job isolation is broken in %s: %d violation(s)\n' "$(label_of "$WORKFLOW")" "${#violations[@]}"
  RC=1
  exit $RC
fi

holders=()
deleters=()
for job in "${JOBS[@]}"; do
  [ "${HOLDS[$job]}" = yes ] && holders+=("$job")
  [ "${DELETES[$job]}" = yes ] && deleters+=("$job")
done
read_uniq=()
if [ "${#HOLDER_READ[@]}" -gt 0 ]; then
  mapfile -t read_uniq < <(printf '%s\n' "${HOLDER_READ[@]}" | sort -u)
fi
printf 'release job isolation holds in %s:\n' "$(label_of "$WORKFLOW")"
printf '  may mint the OIDC token: %s — %d step(s), none a build or pack step, no action but checkout, download-artifact and upload-artifact\n' \
  "${holders[*]:-no job}" "$holder_steps"
printf '  scripts read for those steps: %s\n' "${read_uniq[*]:-none}"
printf '  may delete the run'"'"'s artifacts: %s — %d step(s), granted actions: write alone, no action, none a build or pack step, no secret\n' \
  "${deleters[*]:-no job}" "$deleter_steps"
printf '  actions/checkout steps: %d, each with persist-credentials: false\n' "$checkouts"
printf '  jobs running third-party code, each naming no secret and no github.token: %s\n' "${TP_REPORT[*]:-none}"
printf '  jobs naming a secret, none of them running third-party code: %s\n' "${NAMING_REPORT[*]:-none}"
if [ "${#apple_jobs[@]}" -gt 0 ]; then
  printf '  the Apple secrets: named in %s alone — %d step(s), no checkout, no build or pack step and no repository script, no action but download-artifact and upload-artifact pinned by commit, no permission, no other secret, on a GitHub-hosted macOS image\n' \
    "$APPLE_JOB" "$apple_steps"
else
  printf '  the Apple secrets: no job named %s, and no job names one\n' "$APPLE_JOB"
fi
RC=0
exit $RC
