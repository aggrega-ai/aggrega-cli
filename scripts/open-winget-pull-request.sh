#!/usr/bin/env bash
# aggrega-cli/scripts/open-winget-pull-request.sh — open the pull request that
# adds one release's winget manifest to the winget community repository,
# microsoft/winget-pkgs, from a fork of it.
#
# The manifest is the one the release renders with render-package-channels.sh:
# three files, the version, installer and default-locale files, which this script
# adds unchanged under the folder winget-pkgs keeps them in. That folder is
# manifests/<first letter of the identifier, lower case>/<the identifier, a
# folder for each part between its dots>/<version>/, so Cognitio.Aggrega 0.8.7
# goes under manifests/c/Cognitio/Aggrega/0.8.7/; the identifier and the version
# are read from the files themselves, which must all carry the same ones.
#
# On a real publish, and only when the repository variable
# AGGREGA_WINGET_PULL_REQUEST is exactly true, it:
#   1. syncs the fork's master branch with winget-pkgs' master branch;
#   2. reads that branch's newest commit from the fork into a repository of its
#      own, every tree of it and none of its files (a partial fetch), and stops,
#      opening nothing, when winget-pkgs already holds that version's folder;
#   3. writes one commit on top of it adding the three files, building only the
#      trees on the folder's path, and pushes it to the fork as the branch
#      <identifier>-<version>, never forced; a branch of that name already on the
#      fork is refused rather than replaced;
#   4. opens the pull request from that branch onto winget-pkgs' master branch,
#      titled as winget-pkgs' pull request template asks: "New package:
#      <identifier> version <version>" when winget-pkgs holds no version of the
#      package yet, else "Update: <identifier> to <version>".
# The token in WINGET_PULL_REQUEST_TOKEN does all of it: a personal access token
# (classic) with the public_repo scope, of an account that may push to the fork.
# It reaches gh through gh's own GH_TOKEN, and git as an Authorization header
# through git's environment configuration (GIT_CONFIG_COUNT), so it is never
# placed on a command line or written to disk. The pull request is opened
# through GitHub's REST API (gh api), because gh pr create does not take an
# organization's fork as the head of a pull request.
#
# With AGGREGA_WINGET_PULL_REQUEST unset or anything but true, a real publish
# opens nothing and says the gate is off. A dry run opens nothing, pushes nothing
# and syncs nothing, whatever the variable is: it prints the fork, the branch,
# the files' paths and the title the pull request would have, and what the
# variable would do on a real publish. It then checks the token, whatever the
# variable is, with one read-only request to GitHub's API (so a dry run with a
# token set reaches the network): GitHub must accept the token, and it must be
# a personal access token (classic) carrying the public_repo scope, which GitHub
# reports in the X-OAuth-Scopes header of its answer; a fine-grained or App
# token carries no such header and is refused. The dry run names the account and
# the scopes the token carries. With no token set, a dry run refuses when the
# variable is true, since a real publish would have none to push with, and
# otherwise says no token is set and passes. A real publish with the gate on
# runs the same check before it syncs anything.
#
# Usage:
#   scripts/open-winget-pull-request.sh --pack <manifest-folder>
#   scripts/open-winget-pull-request.sh --manifest <file> --version <version>
#     --tag <tag> --fork <owner>/<repository> --dry-run true|false
#
# --pack prints the manifest folder's files, one line each, as "<file name>
# <its bytes in base64>": the form the release hands the manifest on in, from
# the job that renders it to the job that opens the pull request, and the form
# --manifest reads. The folder must hold nothing but files whose names end in
# .yaml.
#
# --manifest <file> holds those lines, exactly three; <version> is the version
# the files carry, which on a real publish must be MAJOR.MINOR.PATCH, and
# <tag> the release's tag, which on a real publish must be v<version>;
# <owner>/<repository> is the fork of microsoft/winget-pkgs the branch is pushed
# to and the pull request opened from; --dry-run true prints what would be
# opened and opens nothing. A dry run may build a commit no release tag names, whose binaries
# carry a version such as 1.2.3-4-gabc1234, so on a dry run the version need
# only start with a digit and hold letters, digits, dots, - and _; the dry run
# then says a real publish files the manifest under its tag's version instead.
#
# Programs: bash, git 2.31 or later (it reads the token from GIT_CONFIG_COUNT),
# gh, base64, mktemp and the shell's own utilities; the network on a real
# publish with the gate on, and on a dry run with a token set; on no other path.
#
# Exits 0 when the pull request is opened, on a dry run, when the gate is off,
# and when winget-pkgs already holds the version; 1, naming what it refused or
# what failed, when the manifest is not three files carrying one identifier and
# the given version, the tag is not v<version>, no token is set where one is
# needed, GitHub does not accept the token or the token is not a personal access
# token (classic) with the public_repo scope, the fork already has the branch, or
# a step against GitHub fails; 2 on a usage error.
set -euo pipefail
# The manifest's files are listed in one order on every machine.
export LC_ALL=C

UPSTREAM=microsoft/winget-pkgs
UPSTREAM_BRANCH=master

usage() {
  echo "usage: open-winget-pull-request.sh --pack <manifest-folder>" >&2
  echo "       open-winget-pull-request.sh --manifest <file> --version <version> --tag <tag> --fork <owner>/<repository> --dry-run true|false" >&2
  exit 2
}
say() { printf 'open-winget-pull-request: %s\n' "$*"; }
refuse() { printf 'open-winget-pull-request: %s\n' "$*" >&2; exit 1; }

# A file name the manifest may carry: no path, no leading dot, ending in .yaml.
NAME_PATTERN='^[A-Za-z0-9][A-Za-z0-9._-]*\.yaml$'

if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
  sed -n '2,/^# a step against GitHub fails; 2 on a usage error\.$/p' "$0" | sed 's/^# \{0,1\}//'
  exit 0
fi

if [ "${1:-}" = "--pack" ]; then
  [ "$#" -eq 2 ] || usage
  dir="$2"
  [ -d "$dir" ] || { echo "open-winget-pull-request: no manifest folder at $dir" >&2; exit 2; }
  shopt -s nullglob dotglob
  entries=("$dir"/*)
  shopt -u nullglob dotglob
  [ "${#entries[@]}" -gt 0 ] || refuse "the manifest folder $dir is empty"
  for entry in "${entries[@]}"; do
    name="${entry##*/}"
    [ -f "$entry" ] && [ ! -L "$entry" ] || refuse "$entry is not a plain file"
    [[ "$name" =~ $NAME_PATTERN ]] || refuse "$name is not a manifest file name"
  done
  for entry in "${entries[@]}"; do
    printf '%s %s\n' "${entry##*/}" "$(base64 -w0 < "$entry")"
  done
  exit 0
fi

MANIFEST="" VERSION="" TAG="" FORK="" DRY=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --manifest | --version | --tag | --fork | --dry-run)
      [ "$#" -ge 2 ] || usage
      case "$1" in
        --manifest) MANIFEST="$2" ;;
        --version) VERSION="$2" ;;
        --tag) TAG="$2" ;;
        --fork) FORK="$2" ;;
        --dry-run) DRY="$2" ;;
      esac
      shift 2
      ;;
    *) echo "open-winget-pull-request: unknown argument $1" >&2; usage ;;
  esac
done
[ -n "$MANIFEST" ] && [ -n "$VERSION" ] && [ -n "$FORK" ] && [ -n "$DRY" ] || usage
case "$DRY" in
  true | false) ;;
  *) echo "open-winget-pull-request: --dry-run is '$DRY'; it takes true or false" >&2; exit 2 ;;
esac
# The version names a folder and a branch, so it is held to the characters those
# take here on every run; a release publishes only MAJOR.MINOR.PATCH.
[[ "$VERSION" =~ ^[0-9][0-9A-Za-z._-]*$ ]] || { echo "open-winget-pull-request: '$VERSION' is not a version this script names a folder and a branch with" >&2; exit 2; }
RELEASE_FORM=false
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] && RELEASE_FORM=true
[ "$DRY" = "true" ] || [ "$RELEASE_FORM" = "true" ] || { echo "open-winget-pull-request: '$VERSION' is not a version of the form MAJOR.MINOR.PATCH" >&2; exit 2; }
[[ "$FORK" =~ ^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$ ]] || { echo "open-winget-pull-request: '$FORK' is not <owner>/<repository>" >&2; exit 2; }
[ -f "$MANIFEST" ] || { echo "open-winget-pull-request: no manifest file at $MANIFEST" >&2; exit 2; }

WORK="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/winget-pull-request.XXXXXX")"
trap 'rm -rf -- "$WORK"' EXIT
mkdir "$WORK/files"

# The manifest, unpacked into this run's own folder: exactly three files, each
# named as a manifest file is and named once.
count=0
while IFS= read -r line || [ -n "$line" ]; do
  [ -n "$line" ] || continue
  name="${line%% *}" data="${line#* }"
  [ "$name" != "$line" ] || refuse "a line of $MANIFEST is not '<file name> <base64>'"
  [[ "$name" =~ $NAME_PATTERN ]] || refuse "$MANIFEST names '$name', which is not a manifest file name"
  [ ! -e "$WORK/files/$name" ] || refuse "$MANIFEST names $name twice"
  printf '%s' "$data" | base64 -d > "$WORK/files/$name" 2>/dev/null || refuse "$MANIFEST holds $name in a form base64 does not read"
  count=$((count + 1))
done < "$MANIFEST"
[ "$count" -eq 3 ] || refuse "$MANIFEST holds $count file(s); a winget manifest here is three: the version, installer and default-locale files"

# field <file> <key> — the value of a top-level key, without the quotes YAML
# may put around it.
field() {
  awk -v key="$2:" '$1 == key { v = $2; gsub(/^["'\'']|["'\'']$/, "", v); print v; exit }' "$1"
}
ID=""
for file in "$WORK"/files/*; do
  id="$(field "$file" PackageIdentifier)" version="$(field "$file" PackageVersion)"
  [ -n "$id" ] || refuse "${file##*/} carries no PackageIdentifier"
  [ -z "$ID" ] || [ "$id" = "$ID" ] || refuse "the manifest's files carry two identifiers, $ID and $id"
  ID="$id"
  [ "$version" = "$VERSION" ] || refuse "${file##*/} carries the version '$version', not $VERSION"
done
# winget's identifiers are Publisher.Package, two to eight parts; these also name
# folders and a branch here, so each part is held to letters, digits, - and _.
[[ "$ID" =~ ^[A-Za-z0-9][A-Za-z0-9_-]{0,31}(\.[A-Za-z0-9][A-Za-z0-9_-]{0,31}){1,7}$ ]] \
  || refuse "the identifier '$ID' is not one this script files under winget-pkgs' folders"
for name in "$ID.yaml" "$ID.installer.yaml"; do
  [ -f "$WORK/files/$name" ] || refuse "the manifest has no $name"
done

first="${ID:0:1}"
PACKAGE_PATH="manifests/${first,,}/${ID//.//}"
FOLDER="$PACKAGE_PATH/$VERSION"
BRANCH="$ID-$VERSION"
OWNER="${FORK%%/*}"
TITLE_NEW="New package: $ID version $VERSION"
TITLE_UPDATE="Update: $ID to $VERSION"
GATE="${AGGREGA_WINGET_PULL_REQUEST-}"
TOKEN="${WINGET_PULL_REQUEST_TOKEN-}"
if [ -z "$GATE" ]; then
  gate_state="unset"
else
  gate_state="'$GATE'"
fi

# check_token — asks GitHub, with one request, whose token WINGET_PULL_REQUEST_TOKEN
# is and which scopes it carries, and refuses unless GitHub accepts it and it is
# a personal access token (classic) with the public_repo scope. It sets
# ACCOUNT_ID, LOGIN and SCOPES. gh reads the token from GH_TOKEN; nothing else
# here holds one, and only the scope names and the login are ever printed.
check_token() {
  local response headers scope found=false
  export GH_TOKEN="$TOKEN"
  if ! response="$(gh api --include user --jq '"\(.id) \(.login)"')"; then
    refuse "GitHub did not accept the token in WINGET_PULL_REQUEST_TOKEN; nothing was opened"
  fi
  response="${response//$'\r'/}"
  # The headers end at the first blank line; the filtered body is the last line.
  headers="$(printf '%s\n' "$response" | awk 'NF == 0 { exit } { print }')"
  account="$(printf '%s\n' "$response" | awk 'blank && NF { line = $0 } NF == 0 { blank = 1 } END { print line }')"
  ACCOUNT_ID="${account%% *}" LOGIN="${account#* }"
  [ -n "$ACCOUNT_ID" ] && [ -n "$LOGIN" ] && [ "$ACCOUNT_ID" != "$account" ] || refuse "GitHub named no account for the token; nothing was opened"
  if ! printf '%s\n' "$headers" | awk -F: 'tolower($1) == "x-oauth-scopes" { found = 1 } END { exit !found }'; then
    refuse "the token in WINGET_PULL_REQUEST_TOKEN carries no X-OAuth-Scopes header, so it is not a personal access token (classic); the winget pull request needs a personal access token (classic) with the public_repo scope, of an account that may push to $FORK; nothing was opened"
  fi
  SCOPES="$(printf '%s\n' "$headers" | awk -F: 'tolower($1) == "x-oauth-scopes" { sub(/^[^:]*:[ \t]*/, ""); print; exit }')"
  local -a listed
  IFS=, read -r -a listed <<<"$SCOPES"
  for scope in "${listed[@]}"; do
    scope="${scope#"${scope%%[![:space:]]*}"}"
    scope="${scope%"${scope##*[![:space:]]}"}"
    [ "$scope" = "public_repo" ] && found=true
  done
  [ "$found" = "true" ] || refuse "the token in WINGET_PULL_REQUEST_TOKEN lacks the public_repo scope: it carries the scopes: ${SCOPES:-none}; the winget pull request needs a personal access token (classic) with the public_repo scope; nothing was opened"
}

if [ "$DRY" = "true" ]; then
  say "dry run: would sync the $UPSTREAM_BRANCH branch of the fork https://github.com/$FORK with that of https://github.com/$UPSTREAM, push the branch $BRANCH to the fork, and open a pull request from $OWNER:$BRANCH onto the $UPSTREAM_BRANCH branch of $UPSTREAM, adding:"
  for file in "$WORK"/files/*; do
    say "  $FOLDER/${file##*/}"
  done
  say "dry run: titled \"$TITLE_NEW\", or \"$TITLE_UPDATE\" when winget-pkgs already holds a version of $ID"
  if [ "$RELEASE_FORM" != "true" ]; then
    say "dry run: $VERSION is this build's version, not a release's MAJOR.MINOR.PATCH, as when no release tag names the commit built; a real publish, from the tag v<MAJOR.MINOR.PATCH>, files the manifest under that version"
  fi
  if [ "$GATE" = "true" ]; then
    say "dry run: the repository variable AGGREGA_WINGET_PULL_REQUEST is true, so a real publish opens it; nothing is opened on a dry run"
  else
    say "dry run: the repository variable AGGREGA_WINGET_PULL_REQUEST is $gate_state, not true, so a real publish opens nothing; nothing is opened on a dry run"
  fi
  if [ -n "$TOKEN" ]; then
    check_token
    say "dry run: GitHub accepts the token in WINGET_PULL_REQUEST_TOKEN: the account $LOGIN, with the scopes: $SCOPES"
  elif [ "$GATE" = "true" ]; then
    refuse "dry run: the repository variable AGGREGA_WINGET_PULL_REQUEST is true but WINGET_PULL_REQUEST_TOKEN is empty, so a real publish could push nothing to $FORK or open anything on $UPSTREAM; nothing was opened"
  else
    say "dry run: no token is set in WINGET_PULL_REQUEST_TOKEN, so the token is not checked"
  fi
  exit 0
fi

if [ "$GATE" != "true" ]; then
  say "the winget pull request is off: the repository variable AGGREGA_WINGET_PULL_REQUEST is $gate_state, not true, so nothing was opened on $UPSTREAM for $ID $VERSION; set it to true to open it on a real publish"
  exit 0
fi

[ "$TAG" = "v$VERSION" ] || refuse "the release tag is '$TAG', but the manifest carries the version $VERSION; nothing was opened"
[ -n "$TOKEN" ] || refuse "the gate is on but WINGET_PULL_REQUEST_TOKEN is empty, so nothing can be pushed to $FORK or opened on $UPSTREAM; nothing was opened"
probe="$(GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=wingetpullrequest.probe GIT_CONFIG_VALUE_0=read git config --get wingetpullrequest.probe || true)"
[ "$probe" = read ] || refuse "this machine's $(git --version) does not read configuration from GIT_CONFIG_COUNT, which is how the token reaches git without being written to disk; git 2.31 or later does. Nothing was opened."

check_token

auth="$(printf '%s:%s' "$LOGIN" "$TOKEN" | base64 -w0)"
echo "::add-mask::$auth"
export GIT_CONFIG_COUNT=1
export GIT_CONFIG_KEY_0="http.https://github.com/.extraheader"
export GIT_CONFIG_VALUE_0="AUTHORIZATION: basic $auth"
export GIT_TERMINAL_PROMPT=0
# Every tree is fetched up front and no file is ever read, so a git that would
# otherwise fetch a missing object on demand is told not to: a need for one
# fails here rather than downloading winget-pkgs one object at a time.
export GIT_NO_LAZY_FETCH=1

say "syncing the $UPSTREAM_BRANCH branch of https://github.com/$FORK with that of $UPSTREAM"
gh repo sync "$FORK" --source "$UPSTREAM" --branch "$UPSTREAM_BRANCH" \
  || refuse "could not sync the $UPSTREAM_BRANCH branch of $FORK with that of $UPSTREAM; nothing was opened"

REMOTE="https://github.com/$FORK.git"
G="$WORK/winget-pkgs.git"
git init -q --bare "$G"
git --git-dir="$G" remote add fork "$REMOTE"
git --git-dir="$G" fetch -q --depth=1 --filter=blob:none --no-tags fork \
  "refs/heads/$UPSTREAM_BRANCH:refs/remotes/fork/$UPSTREAM_BRANCH" \
  || refuse "could not read $REMOTE's $UPSTREAM_BRANCH branch; nothing was opened"
BASE="$(git --git-dir="$G" rev-parse "refs/remotes/fork/$UPSTREAM_BRANCH^{commit}")"

if git --git-dir="$G" cat-file -e "$BASE:$FOLDER" 2>/dev/null; then
  say "$UPSTREAM already holds $FOLDER (at $BASE); nothing was opened"
  exit 0
fi
if git --git-dir="$G" cat-file -e "$BASE:$PACKAGE_PATH" 2>/dev/null; then
  TITLE="$TITLE_UPDATE"
else
  TITLE="$TITLE_NEW"
fi
if ! heads="$(git --git-dir="$G" ls-remote --heads fork "refs/heads/$BRANCH")"; then
  refuse "could not read $REMOTE's branches; nothing was opened"
fi
[ -z "$heads" ] || refuse "$FORK already has a branch $BRANCH, which this script does not replace; open the pull request from $OWNER:$BRANCH by hand, titled \"$TITLE\", once that branch holds the manifest. Nothing was opened."

# with_entry <tree, or empty for none> <name> <tree> — prints a tree that is the
# first with its entry <name> replaced by, or added as, the folder <tree>. Every
# other entry is copied as it is, by its object name, so none of the files the
# partial fetch left out is needed.
with_entry() {
  local base="$1" name="$2" tree="$3" record
  {
    if [ -n "$base" ]; then
      while IFS= read -r -d '' record; do
        [ "${record#*$'\t'}" = "$name" ] || printf '%s\0' "$record"
      done < <(git --git-dir="$G" ls-tree -z "$base")
    fi
    printf '040000 tree %s\t%s\0' "$tree" "$name"
  } | git --git-dir="$G" mktree -z --missing
}

leaf="$(
  for file in "$WORK"/files/*; do
    printf '100644 blob %s\t%s\0' "$(git --git-dir="$G" hash-object -w -- "$file")" "${file##*/}"
  done | git --git-dir="$G" mktree -z
)"
IFS=/ read -r -a parts <<<"$FOLDER"
tree="$leaf"
for ((i = ${#parts[@]} - 1; i >= 0; i--)); do
  parent="$(IFS=/; printf '%s' "${parts[*]:0:i}")"
  base=""
  if git --git-dir="$G" cat-file -e "$BASE:$parent" 2>/dev/null; then
    base="$BASE:$parent"
  fi
  tree="$(with_entry "$base" "${parts[$i]}" "$tree")"
done

export GIT_AUTHOR_NAME="$LOGIN" GIT_COMMITTER_NAME="$LOGIN"
export GIT_AUTHOR_EMAIL="$ACCOUNT_ID+$LOGIN@users.noreply.github.com" GIT_COMMITTER_EMAIL="$ACCOUNT_ID+$LOGIN@users.noreply.github.com"
COMMIT="$(git --git-dir="$G" commit-tree "$tree" -p "$BASE" -m "$TITLE")"
say "pushing $COMMIT, which adds $FOLDER to $BASE, to https://github.com/$FORK as $BRANCH"
git --git-dir="$G" push -q fork "$COMMIT:refs/heads/$BRANCH" \
  || refuse "could not push the branch $BRANCH to $FORK; nothing was opened"

BODY="$(
  printf '## Description\n\n'
  printf '%s %s, the manifest the Aggrega release %s writes from the Windows installer it attaches and the SHA-256 its SHA256SUMS lists for it.\n\n' "$ID" "$VERSION" "$TAG"
  printf '## Manifest Checklist\n\n'
  printf -- '- [x] This PR only modifies one (1) manifest\n'
  # shellcheck disable=SC2016 # the backquotes are Markdown's, written as they are.
  printf -- '- [x] Validated manifest locally with `winget validate --manifest <path>`: the release validates this manifest, rendered from the same installer, on Windows before it publishes\n'
)"
if ! url="$(gh api --method POST "repos/$UPSTREAM/pulls" \
  -f "title=$TITLE" -f "head=$OWNER:$BRANCH" -f "base=$UPSTREAM_BRANCH" -f "body=$BODY" --jq .html_url)"; then
  refuse "the branch $BRANCH is on $FORK, but GitHub did not open the pull request onto $UPSTREAM; open it by hand from $OWNER:$BRANCH, titled \"$TITLE\""
fi
say "opened $url: \"$TITLE\", from $OWNER:$BRANCH"
