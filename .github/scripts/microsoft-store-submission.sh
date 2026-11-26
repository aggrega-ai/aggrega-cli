#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# microsoft-store-submission.sh — submit one release's Windows installer to
# Aggrega's Microsoft Store listing, through the Store's submission API for
# MSI/EXE apps.
#
# The listing holds one package: the installer, which the Store downloads from
# its address and installs silently. This script points that package at the
# installer of the release <version>, at the address the release workflow
# publishes it to, smoke_download_url's in scripts/lib/platform-smoke-common.sh:
# https://download.aggrega.ai/aggrega-cli/v<version>/aggrega-<version>-windows-amd64-setup.exe.
# It sets the English listing's "What's new in this version", the listing's
# whatsNew, to the version's section of CHANGELOG.md. Then it submits the
# listing for certification. Every other field of the listing, its images and
# its markets stay as Partner Center holds them.
#
# A run, in order:
#   1. refuses, naming each and where it lives, a setting below that is not
#      set (its outcome line names them too), a seller ID that is not a number and
#      a product ID that is not a GUID, before any request;
#   2. on a real run, refuses a version that is not MAJOR.MINOR.PATCH: a
#      pre-release or a build is never submitted;
#   3. takes the What's new text from CHANGELOG.md at the root of the checkout
#      this script is in: the section of v<version>, as release-changes.sh
#      prints it for the release page, written as plain lines. A heading
#      "### Fixed" becomes the line "Fixed", after a blank line unless it comes
#      first; each "- " entry stays a line, its inline-code backticks dropped;
#      blank lines go. The listing takes at most 1,500 characters, counted here
#      in UTF-16 code units, never fewer than the characters: a longer text
#      keeps the whole lines that fit, drops a heading left with no entry, and
#      ends with a blank line and the line "More changes in this version:
#      https://aggrega.ai/docs/changelog/", the documentation's Changelog page.
#      A real run refuses a version whose section is absent or empty, naming
#      the version and the file, before any request; a dry run says a real run
#      would refuse and goes on. A section holding an HTML comment is refused
#      on every run, as release-changes.sh refuses it;
#   4. reads the release v<version> with gh and holds the installer's address to
#      the SHA-256 the release's SHA256SUMS lists, through smoke_check_download:
#      one GET of it in full that answers 200 itself, not a redirect, with those
#      bytes and a Content-Length equal to their number. A real run requires the
#      release; a dry run for a version with no release says so and checks
#      nothing, as does a dry run for a version that is not MAJOR.MINOR.PATCH,
#      whose address the release workflow never publishes;
#   5. asks Microsoft Entra ID for an access token for the Store API;
#   6. reads the draft's status. When a submission is in progress, it reads that
#      submission's status and prints both; a real run then changes
#      nothing and exits 3, and a dry run says a real run would refuse and goes
#      on. When the draft is not ready and no submission is in progress, it asks again every
#      10 seconds, 60 tries in all. An entry of the status's errors whose code is
#      warning is printed and never refuses, here and at steps 11 and 12;
#   7. reads the draft's packages, and refuses unless there is exactly one,
#      naming each package's URL. When the package's URL ends in the object key
#      smoke_download_key gives a version, that version is the draft's: one
#      newer than <version>, by MAJOR.MINOR.PATCH in sort -V order, is left
#      alone on a real run, which exits 3, and reported on a dry run. A package
#      already at the installer's address is reported as already the draft's package, and the
#      run goes on to the commit and the submit without updating it, so a run
#      that stopped after its update is completed by running it again;
#   8. reads the draft's listings, one per language, and refuses unless exactly
#      one is English, its language en or en-<region> in any case, naming each
#      listing's language; a listing in another language is named and left as
#      it is. An English listing whose whatsNew already equals the text is
#      reported, and its whatsNew is not sent again;
#   9. prints the change: the English listing's whatsNew before and after, the
#      package's URL before and after, and every other field of the package,
#      unchanged;
#  10. stops here on a dry run;
#  11. unless it already holds the text, sends the English listing with its
#      whatsNew replaced and every other field as the draft holds it, then
#      reads the draft's status every 10 seconds, 60 tries in all, until it is
#      ready, refusing an error entry as step 12 does;
#  12. replaces the draft's packages with that one package, its URL changed and
#      every other field sent as the draft holds it, since the update replaces
#      the whole package set and resets a field it is not sent; commits the
#      draft's packages; and reads the draft's status every 10 seconds, 90 tries
#      in all, until it is ready, refusing on an error whose code is
#      packageuploaderror or whose target is anything but packages;
#  13. submits the draft; the submission is the answer's submissionId, or its
#      ongoingSubmissionId when it names none;
#  14. reads the submission's status every 30 seconds, 20 tries in all; the
#      publishingStatus is read without regard to case.
#      INPROGRESS: the submission is in certification, and the run passes. The
#      submission API has no request that publishes a certified submission, and
#      a submission reads INPROGRESS from certification until it is live; Partner
#      Center shows whether a certified submission goes live by itself or waits
#      for its Publish button. PUBLISHED: the run passes, saying so. FAILED, or
#      hasFailed true: refused, naming each error. UNKNOWN at the last try:
#      refused, naming the submission.
# The run's outcome line also goes to the file GITHUB_STEP_SUMMARY names, when
# it is set.
#
# The requests. The token: POST <login>/<tenant ID>/oauth2/v2.0/token, form
# encoded, with grant_type=client_credentials, client_id, client_secret and
# scope=https://api.store.microsoft.com/.default. The Store's, each under
# <api>/submission/v1/product/<product ID>, with the headers Authorization:
# Bearer <token> and X-Seller-Account-Id: <seller ID>, and Content-Type:
# application/json on the two that carry a body:
#   GET   /status                          the draft's isReady and ongoingSubmissionId
#   GET   /submission/<id>/status          a submission's publishingStatus and hasFailed
#   GET   /packages                        the draft's packages
#   GET   /metadata/listings               the draft's listings, {"listings": [...]}
#   PATCH /metadata                        {"listings": <the English listing>}
#   PUT   /packages                        {"packages": [<the package>]}
#   POST  /packages/commit                 no body
#   POST  /submit                          no body
# The PATCH sends the listing whole, as the draft holds it with whatsNew alone
# replaced, the form in which Microsoft's own samples send a listing back; a
# PATCH keeps a field it is not sent, where a PUT of the metadata resets it.
# The Store refuses a metadata update while a submission is in progress, which
# step 6 has already ruled out on a real run.
# Every Store answer is {isSuccess, errors: [{code, target, message}],
# responseData}. An answer with an HTTP status outside 2xx, or with isSuccess
# not true, is refused, naming each error's code, target and message, and the
# answer's X-Correlation-ID header, the ID by which Microsoft's support finds a
# request; on HTTP 401 or 403 it adds the likely cause, an Entra app without the
# Manager role in Partner Center or a seller ID that is not the account's. The
# errors of a successful answer are its warnings, and are printed. A read is
# retried by curl --retry 3, which waits as a rate-limited answer's Retry-After
# header asks; a PUT, a PATCH or a POST is sent once, never retried.
#
# Settings, all read from the environment. In the release workflow and the
# dispatch workflow they come from this repository's GitHub environment
# microsoft-store:
#   MICROSOFT_STORE_TENANT_ID      secret: the Microsoft Entra tenant of the
#                                  Partner Center account
#   MICROSOFT_STORE_CLIENT_ID      secret: the client ID of the Entra app
#                                  registration Partner Center lists with the
#                                  Manager role
#   MICROSOFT_STORE_CLIENT_SECRET  secret: a client secret of that registration
#   MICROSOFT_STORE_SELLER_ID      variable: the seller ID, a number, from Partner
#                                  Center's Account settings, Legal info
#   MICROSOFT_STORE_PRODUCT_ID     variable: the app's Partner Center product
#                                  ID, a GUID, shown in the app's address in
#                                  Partner Center
#                                  (partner.microsoft.com/.../products/<GUID>/overview);
#                                  the Store ID that starts XP is a different
#                                  identifier
# gh reads the release with GH_TOKEN, the run's own token, and GH_REPO, this
# repository. The tenant ID, the client ID and the client secret reach curl on
# its standard input, as lines of curl's configuration, as does the access
# token; none of them is placed on a command line, written to a file, or
# printed, and a message from either service that holds one prints it as ***.
# The child processes this script starts do not inherit the three. Under GitHub
# Actions the token is masked in the log with ::add-mask::. The seller ID and
# the product ID are identifiers, and messages name them.
#
# Overrides, for a test against a local stand-in: MICROSOFT_STORE_LOGIN_BASE
# (https://login.microsoftonline.com when unset), MICROSOFT_STORE_API_BASE
# (https://api.store.microsoft.com), DOWNLOADS_PUBLIC_BASE (the download
# address's base, read by smoke_download_url), and MICROSOFT_STORE_POLL_SECONDS,
# which, when set, makes every wait between two tries that many seconds and
# leaves the numbers of tries as they are. Every address must be https://.
#
# Usage:
#   .github/scripts/microsoft-store-submission.sh --version <version> --dry-run true|false
#
# <version> is the release's version, without the tag's v. On a dry run it may
# also be a build's version, such as 1.2.3-4-gabc1234: a digit first, then
# letters, digits, dots, - and _. A dry run reads CHANGELOG.md, the release,
# the installer's address, the draft's status, packages and listings, and
# prints the change; the only request it sends that is not a read is the
# token's, and it changes nothing. Where a real run would refuse — a version
# that is not MAJOR.MINOR.PATCH, a version with no section in CHANGELOG.md, a
# release that does not exist, a submission in progress, a newer draft — the
# dry run says so where it finds it and goes on, and its outcome line names
# every such reason instead of the change.
#
# Programs: bash 4.3 or later, curl, jq, gh, sha256sum (or shasum), sort and the
# shell's own utilities; the network on every run that passes the settings.
#
# Exits 0 when a dry run has printed the change, and when the submission is in
# certification or published; 1, naming what it refused or what failed, when a
# setting is missing or malformed, the version is not one a real run submits,
# CHANGELOG.md gives a real run no text for the version or its section holds
# an HTML comment, the release or the installer's address does not hold, a
# request fails, the Store refuses an answer, the draft does not hold exactly
# one package or exactly one English listing, the draft does not become
# ready, or the submission fails or stays unknown; 2 on a
# usage error; 3 when a real run changed nothing because a submission is in
# progress, its outcome line naming that submission and saying to dispatch
# "Submit to the Microsoft Store" with this version once it ends, or because the
# draft's package is the installer of a newer version, its outcome line naming
# that version and saying the listing is left unchanged.
set -euo pipefail
# The credentials and the token must never reach the log, so tracing stays off
# even when the caller turned it on.
set +x
# The credentials are read before any program is started and taken out of the
# environment, so no child process inherits them; the token joins them once it
# is issued.
store_tenant="${MICROSOFT_STORE_TENANT_ID:-}"
store_client_id="${MICROSOFT_STORE_CLIENT_ID:-}"
store_client_secret="${MICROSOFT_STORE_CLIENT_SECRET:-}"
store_token=""
export -n store_tenant store_client_id store_client_secret store_token
unset MICROSOFT_STORE_TENANT_ID MICROSOFT_STORE_CLIENT_ID MICROSOFT_STORE_CLIENT_SECRET

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PREFIX="microsoft-store-submission: "
SCOPE="https://api.store.microsoft.com/.default"

if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
  sed -n '/^# microsoft-store-submission\.sh — /,/^# that version and saying the listing is left unchanged\.$/p' \
    "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
  exit 0
fi

usage() {
  printf '%s%s\n' "$PREFIX" "usage: microsoft-store-submission.sh --version <version> --dry-run true|false" >&2
  printf '%s%s\n' "$PREFIX" "       microsoft-store-submission.sh --help" >&2
  exit 2
}
usage_error() {
  printf '%s%s\n' "$PREFIX" "$1" >&2
  exit 2
}

VERSION="" DRY=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --version | --dry-run)
      [ "$#" -ge 2 ] || usage
      case "$1" in
        --version) VERSION="$2" ;;
        --dry-run) DRY="$2" ;;
      esac
      shift 2
      ;;
    *) printf '%sunknown argument %s\n' "$PREFIX" "$1" >&2; usage ;;
  esac
done
[ -n "$VERSION" ] && [ -n "$DRY" ] || usage
case "$DRY" in
  true | false) ;;
  *) usage_error "--dry-run is '$DRY'; it takes true or false" ;;
esac
# The version names a tag and an address, so it is held to the characters those
# take here on every run; a real run takes MAJOR.MINOR.PATCH alone, below.
[[ "$VERSION" =~ ^[0-9][0-9A-Za-z._-]*$ ]] \
  || usage_error "'$VERSION' is not a version: a digit first, then letters, digits, dots, - and _"

POLL="${MICROSOFT_STORE_POLL_SECONDS:-}"
[ -z "$POLL" ] || [[ "$POLL" =~ ^[0-9]+$ ]] \
  || usage_error "MICROSOFT_STORE_POLL_SECONDS is '$POLL'; it takes a whole number of seconds"
LOGIN_BASE="${MICROSOFT_STORE_LOGIN_BASE:-https://login.microsoftonline.com}"
API_BASE="${MICROSOFT_STORE_API_BASE:-https://api.store.microsoft.com}"
for base in LOGIN_BASE API_BASE; do
  case "${!base}" in
    https://*) ;;
    *) usage_error "MICROSOFT_STORE_$base is '${!base}', not an https:// address" ;;
  esac
done
LOGIN_BASE="${LOGIN_BASE%/}"
API_BASE="${API_BASE%/}"

# shellcheck source-path=SCRIPTDIR source=../../scripts/lib/platform-smoke-common.sh
. "$HERE/../../scripts/lib/platform-smoke-common.sh"
if ! smoke_require_bash 2>/dev/null; then
  printf '%sneeds bash 4.3 or later; this is bash %s\n' "$PREFIX" "$BASH_VERSION" >&2
  exit 1
fi

WORK="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/microsoft-store-submission.XXXXXX")"
trap 'rm -rf -- "$WORK"' EXIT

# clean <text> — sets CLEAN to <text> on one line, each credential and the token
# in it written ***.
clean() {
  local value
  CLEAN="$1"
  for value in "$store_tenant" "$store_client_id" "$store_client_secret" "$store_token"; do
    if [ -n "$value" ]; then
      CLEAN="${CLEAN//"$value"/***}"
    fi
  done
  CLEAN="${CLEAN//[$'\r\n']/ }"
}
summary() {
  if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
    printf '%s\n' "$1" >>"$GITHUB_STEP_SUMMARY" || true
  fi
}
say() { clean "$*"; printf '%s%s\n' "$PREFIX" "$CLEAN"; }
warn() { clean "$*"; printf '%s%s\n' "$PREFIX" "$CLEAN" >&2; }
outcome() { clean "$*"; printf '%s%s\n' "$PREFIX" "$CLEAN"; summary "$PREFIX$CLEAN"; }
refuse() { clean "$*"; printf '%s%s\n' "$PREFIX" "$CLEAN" >&2; summary "$PREFIX$CLEAN"; exit 1; }
# defer <message> — a real run that changed nothing and leaves the submission
# to a later run: the message, then exit status 3.
defer() { clean "$*"; printf '%s%s\n' "$PREFIX" "$CLEAN" >&2; summary "$PREFIX$CLEAN"; exit 3; }

# relay <file> <label> — prints each line of <file>, a program's own output, as
# a message: "<label><line>", a library function's "smoke: " dropped.
relay() {
  local line
  while IFS= read -r line || [ -n "$line" ]; do
    say "$2${line#smoke: }"
  done <"$1"
}

# pause <seconds> — waits between two tries: <seconds>, or
# MICROSOFT_STORE_POLL_SECONDS when that is set.
pause() { sleep "${POLL:-$1}"; }
seconds() { printf '%s' "${POLL:-$1}"; }

# uri <value> — <value> percent-encoded for one segment of a path.
uri() { printf '%s' "$1" | jq -sRr @uri; }

# --- 1. Settings -------------------------------------------------------------

SELLER="${MICROSOFT_STORE_SELLER_ID:-}"
PRODUCT="${MICROSOFT_STORE_PRODUCT_ID:-}"
missing=()
[ -n "$store_tenant" ] || missing+=("secret MICROSOFT_STORE_TENANT_ID")
[ -n "$store_client_id" ] || missing+=("secret MICROSOFT_STORE_CLIENT_ID")
[ -n "$store_client_secret" ] || missing+=("secret MICROSOFT_STORE_CLIENT_SECRET")
[ -n "$SELLER" ] || missing+=("variable MICROSOFT_STORE_SELLER_ID")
[ -n "$PRODUCT" ] || missing+=("variable MICROSOFT_STORE_PRODUCT_ID")
if [ "${#missing[@]}" -gt 0 ]; then
  names=""
  for setting in "${missing[@]}"; do
    warn "the $setting of the GitHub environment microsoft-store is not set"
    names+="${names:+, }$setting"
  done
  refuse "nothing was sent: not set in the GitHub environment microsoft-store: $names; every setting is needed on a dry run and a real run alike"
fi
[[ "$SELLER" =~ ^[0-9]+$ ]] \
  || refuse "the variable MICROSOFT_STORE_SELLER_ID is '$SELLER', not a seller ID: Partner Center gives it as a number, under Account settings, Legal info; nothing was sent"
[[ "$PRODUCT" =~ ^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$ ]] \
  || refuse "the variable MICROSOFT_STORE_PRODUCT_ID is '$PRODUCT', not a product ID: it holds the Partner Center product ID, a GUID shown in the app's address in Partner Center, not the Store ID; nothing was sent"

# --- 2. Version --------------------------------------------------------------

# The reasons a real run would refuse for, which a dry run reports and goes on
# past; its outcome line names them all.
WOULD_REFUSE=()

RELEASE_FORM=false
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] && RELEASE_FORM=true
if [ "$RELEASE_FORM" = false ]; then
  if [ "$DRY" = false ]; then
    refuse "'$VERSION' is not a version of the form MAJOR.MINOR.PATCH: a real run submits a final release only, never a pre-release or a build; nothing was sent"
  fi
  say "dry run: '$VERSION' is not a version of the form MAJOR.MINOR.PATCH; a real run would refuse it"
  WOULD_REFUSE+=("'$VERSION' is not a version of the form MAJOR.MINOR.PATCH")
fi
TAG="v$VERSION"

# --- 3. The What's new text --------------------------------------------------

CHANGELOG="$(cd "$HERE/../.." && pwd)/CHANGELOG.md"
WHATS_NEW_LIMIT=1500
MORE_CHANGES="More changes in this version: https://aggrega.ai/docs/changelog/"

# whats_new_text — prints the What's new text of the CHANGELOG.md section on
# standard input, as step 3 of the header gives it.
whats_new_text() {
  jq -Rrs --arg more "$MORE_CHANGES" --argjson limit "$WHATS_NEW_LIMIT" '
    def units: [explode[] | if . > 65535 then 2 else 1 end] | add // 0;
    def text: map(.line) | join("\n");
    [split("\n")[] | select(test("\\S"))] as $raw
    | [range(0; $raw | length) as $i | $raw[$i]
        | if startswith("### ") then
            (if $i > 0 then {line: "", kind: "blank"} else empty end), {line: .[4:], kind: "heading"}
          else {line: gsub("`"; ""), kind: "entry"} end] as $lines
    | if ($lines | text | units) <= $limit then $lines | text
      else
        [{line: "", kind: "blank"}, {line: $more, kind: "more"}] as $tail
        | ([range(0; ($lines | length) + 1) | select(($lines[0:.] + $tail | text | units) <= $limit)] | max) as $fit
        | $lines[0:$fit]
        | until(length == 0 or (.[-1].kind == "entry"); .[0:-1])
        | if length == 0 then [$tail[1]] else . + $tail end
        | text
      end'
}

WHATS_NEW=""
rc=0
section="$(bash "$HERE/release-changes.sh" "$TAG" "$CHANGELOG" 2>"$WORK/changes.err")" || rc=$?
if [ "$rc" -ne 0 ]; then
  relay "$WORK/changes.err" "" >&2
  refuse "the section of $VERSION in $CHANGELOG cannot be the listing's What's new text: release-changes.sh exited $rc; nothing was sent"
fi
if [ -n "$section" ]; then
  WHATS_NEW="$(printf '%s\n' "$section" | whats_new_text)"
else
  if [ -f "$CHANGELOG" ]; then
    absent="$CHANGELOG has no section for $VERSION, or an empty one"
  else
    absent="$CHANGELOG does not exist, so it has no section for $VERSION"
  fi
  if [ "$DRY" = false ]; then
    refuse "$absent: the listing's What's new text is that section; nothing was sent"
  fi
  say "dry run: $absent; a real run would refuse"
  WOULD_REFUSE+=("$absent")
fi

# --- 4. The installer's address ----------------------------------------------

INSTALLER="$(smoke_release_asset_name windows/amd64 "$VERSION" "$(smoke_target_installer windows/amd64)")"
NEW_URL="$(smoke_download_url "$VERSION")"

# release_exists — returns 0 when the release $TAG exists and 1 when gh says it
# does not; refuses on any other answer from gh.
release_exists() {
  local rc=0
  gh release view "$TAG" --json tagName >/dev/null 2>"$WORK/gh.err" || rc=$?
  [ "$rc" -ne 0 ] || return 0
  if grep -q 'release not found' "$WORK/gh.err"; then
    return 1
  fi
  relay "$WORK/gh.err" "gh: " >&2
  refuse "gh could not read the release $TAG (exit $rc); nothing was sent to the Store"
}

release_found=false
release_exists && release_found=true
if [ "$release_found" = true ] && [ "$RELEASE_FORM" = false ]; then
  # Only a dry run gets here: a real run refused the version above. The release
  # workflow publishes the installer's address for a final release alone, so
  # the address of this version is not checked.
  say "dry run: '$VERSION' is not a final release's version, so the installer at $NEW_URL is not checked: the release workflow publishes that address for a final release alone"
elif [ "$release_found" = true ]; then
  rc=0
  gh release download "$TAG" --dir "$WORK/release" --pattern SHA256SUMS >/dev/null 2>"$WORK/gh.err" || rc=$?
  if [ "$rc" -ne 0 ]; then
    relay "$WORK/gh.err" "gh: " >&2
    refuse "gh could not download SHA256SUMS from the release $TAG (exit $rc); nothing was sent to the Store"
  fi
  if ! listed_sha256="$(smoke_listed_digest "$WORK/release/SHA256SUMS" "$INSTALLER" 2>"$WORK/digest.err")"; then
    relay "$WORK/digest.err" "" >&2
    refuse "the release $TAG's SHA256SUMS gives no single digest for $INSTALLER; nothing was sent to the Store"
  fi
  rc=0
  smoke_check_download "$NEW_URL" "$listed_sha256" >"$WORK/check.out" 2>&1 || rc=$?
  if [ "$rc" -ne 0 ]; then
    relay "$WORK/check.out" "" >&2
    refuse "the installer's address does not serve the release $TAG's $INSTALLER as the Store needs it; nothing was sent to the Store"
  fi
  relay "$WORK/check.out" ""
elif [ "$DRY" = true ]; then
  say "dry run: the release $TAG does not exist yet, so the installer at $NEW_URL is not checked; a real run would refuse"
  WOULD_REFUSE+=("the release $TAG does not exist yet")
else
  refuse "the release $TAG does not exist: a real run submits a released version's installer; nothing was sent to the Store"
fi

# --- 5. The access token -----------------------------------------------------

token_config() {
  printf 'url = "%s"\n' "$(smoke_curl_config_quote "$LOGIN_BASE/$(uri "$store_tenant")/oauth2/v2.0/token")"
  printf 'data-urlencode = "%s"\n' "grant_type=client_credentials"
  printf 'data-urlencode = "%s"\n' "$(smoke_curl_config_quote "client_id=$store_client_id")"
  printf 'data-urlencode = "%s"\n' "$(smoke_curl_config_quote "client_secret=$store_client_secret")"
  printf 'data-urlencode = "%s"\n' "scope=$SCOPE"
}
# The answer stays in this shell's memory: it holds the token, so it is never
# written to a file.
rc=0
answer="$(token_config | curl --config - --silent --show-error --proto '=https' \
  --connect-timeout 30 --max-time 60 --output - --write-out '\n%{http_code}' 2>"$WORK/curl.err")" || rc=$?
if [ "$rc" -ne 0 ]; then
  relay "$WORK/curl.err" "" >&2
  refuse "the token request to Microsoft Entra ID could not be completed: curl exited $rc; nothing was sent to the Store"
fi
code="${answer##*$'\n'}"
body="${answer%$'\n'*}"
unset answer
if [[ "$code" != 2?? ]]; then
  if printf '%s' "$body" | jq -e 'type == "object"' >/dev/null 2>&1; then
    entra="$(printf '%s' "$body" | jq -r '"\(.error // "") / \(.error_description // "")"')"
    refuse "Microsoft Entra ID refused the token request with HTTP $code: $entra; nothing was sent to the Store"
  fi
  refuse "Microsoft Entra ID answered the token request with HTTP $code and no JSON; nothing was sent to the Store"
fi
store_token="$(printf '%s' "$body" | jq -r 'if type == "object" then .access_token // "" else "" end' 2>/dev/null)" \
  || store_token=""
unset body
[ -n "$store_token" ] || refuse "Microsoft Entra ID answered the token request with HTTP $code but no access_token; nothing was sent to the Store"
if [ "${GITHUB_ACTIONS:-}" = true ]; then
  printf '::add-mask::%s\n' "$store_token"
fi
say "Microsoft Entra ID issued an access token for the Store API"

# --- The Store's requests ----------------------------------------------------

PRODUCT_PATH="$API_BASE/submission/v1/product/$(uri "$PRODUCT")"
CORRELATION=""

auth_config() {
  printf 'header = "%s"\n' "$(smoke_curl_config_quote "Authorization: Bearer $store_token")"
}

# entries <label> — prints each entry of the answer's errors as one message,
# "  <label>: <code> / <target> / <message>".
entries() {
  local line
  while IFS= read -r line; do
    say "  $1: $line"
  done < <(jq -r '(.errors // [])[] | objects
    | [(.code // ""), (.target // ""), (.message // "")]
    | map(tostring | gsub("[\r\n]+"; " ")) | join(" / ")' "$WORK/answer")
}

# auth_hint <http status> — prints the likely cause of a 401 or a 403 from the
# Store, and nothing for another status.
auth_hint() {
  case "$1" in
    401 | 403)
      printf '%s' "; likely cause: the Entra app registration lacks the Manager role in Partner Center, or the seller ID is not this account's"
      ;;
  esac
}

# refuse_answer <headline> — refuses with <headline>, then each error of the
# answer and its X-Correlation-ID.
refuse_answer() {
  warn "$1"
  entries error >&2
  [ -z "$CORRELATION" ] || warn "  X-Correlation-ID: $CORRELATION"
  clean "$1"
  summary "$PREFIX$CLEAN"
  exit 1
}

# store <method> <path> <what> [<body file>] — sends one request to the Store
# under the product's path and leaves the answer in $WORK/answer, refusing,
# with <what> named, when it cannot be sent, the answer is not JSON, its HTTP
# status is outside 2xx, or its isSuccess is not true. A GET is retried by
# curl; a PUT or a PATCH carries <body file> as JSON; a POST carries no body.
store() {
  local method="$1" path="$2" what="$3" body="${4:-}" code rc=0
  local -a args=(--config - --silent --show-error --proto '=https' --connect-timeout 30 --max-time 120
    --header "X-Seller-Account-Id: $SELLER"
    --dump-header "$WORK/headers" --output "$WORK/answer" --write-out '%{http_code}')
  case "$method" in
    GET) args+=(--retry 3) ;;
    PUT | PATCH) args+=(--request "$method" --header "Content-Type: application/json" --data-binary "@$body") ;;
    POST) args+=(--data-binary "" --header "Content-Type:") ;;
  esac
  rm -f -- "$WORK/headers" "$WORK/answer"
  CORRELATION=""
  code="$(auth_config | curl "${args[@]}" "$PRODUCT_PATH$path" 2>"$WORK/curl.err")" || rc=$?
  if [ -f "$WORK/headers" ]; then
    CORRELATION="$(smoke_header_value "$WORK/headers" x-correlation-id)"
  fi
  if [ "$rc" -ne 0 ]; then
    relay "$WORK/curl.err" "" >&2
    refuse "$what ($method $path) could not be completed: curl exited $rc"
  fi
  if ! jq -e 'type == "object"' "$WORK/answer" >/dev/null 2>&1; then
    : >"$WORK/answer"
    refuse_answer "the Store answered $what ($method $path) with HTTP $code and no JSON object$(auth_hint "$code")"
  fi
  if [[ "$code" != 2?? ]] || ! jq -e '.isSuccess == true' "$WORK/answer" >/dev/null; then
    refuse_answer "the Store refused $what ($method $path): HTTP $code, isSuccess $(jq -c '.isSuccess' "$WORK/answer")$(auth_hint "$code")"
  fi
}

# warnings <what> — prints the errors of a successful answer, its warnings.
warnings() { entries "warning from the Store on $1"; }

# draft_ready <when> — judges the draft's status in $WORK/answer: refuses on an
# error whose code is packageuploaderror or whose target is not packages, an
# entry whose code is warning excepted, prints the other entries, and returns 0
# when the draft is ready.
draft_ready() {
  if jq -e '[(.errors // [])[] | objects | select(.code != "warning" and (.target != "packages" or .code == "packageuploaderror"))] | length > 0' \
    "$WORK/answer" >/dev/null; then
    refuse_answer "the draft cannot become ready $1: the Store reports"
  fi
  warnings "the draft's status"
  jq -e '.responseData.isReady == true' "$WORK/answer" >/dev/null
}

# wait_ready <tries> <seconds> <when> — the draft's status read first is in
# $WORK/answer; reads it again every <seconds> until it is ready, <tries> reads
# in all, and refuses when it is not ready at the last.
wait_ready() {
  local tries="$1" interval="$2" when="$3" try=1
  until draft_ready "$when"; do
    if [ "$try" -ge "$tries" ]; then
      refuse_answer "the draft is still not ready $when after $tries reads of its status, $(seconds "$interval") seconds apart; nothing more was sent"
    fi
    say "the draft is not ready $when (read $try of $tries); reading it again in $(seconds "$interval") seconds"
    pause "$interval"
    try=$((try + 1))
    store GET /status "the read of the draft's status"
  done
}

# --- 6. The draft's status ---------------------------------------------------

store GET /status "the read of the draft's status"
ongoing="$(jq -r '.responseData.ongoingSubmissionId // "" | tostring' "$WORK/answer")"
if [ -n "$ongoing" ]; then
  warnings "the draft's status"
  store GET "/submission/$(uri "$ongoing")/status" "the read of the submission in progress"
  warnings "the submission in progress"
  state="$(jq -r '"publishingStatus \(.responseData.publishingStatus), hasFailed \(.responseData.hasFailed)"' "$WORK/answer")"
  if [ "$DRY" = true ]; then
    say "dry run: the listing has a submission in progress, $ongoing ($state); a real run would refuse, changing nothing"
    WOULD_REFUSE+=("submission $ongoing is in progress")
  else
    defer "the listing has a submission in progress, $ongoing ($state), so nothing was changed; dispatch \"Submit to the Microsoft Store\" with $VERSION once that submission has ended"
  fi
else
  wait_ready 60 10 "before the change"
fi

# --- 7. The draft's package --------------------------------------------------

store GET /packages "the read of the draft's packages"
warnings "the draft's packages"
cp -- "$WORK/answer" "$WORK/packages.json"
count="$(jq '(.responseData.packages // []) | length' "$WORK/packages.json")"
if [ "$count" -ne 1 ]; then
  while IFS= read -r url; do
    warn "  the draft holds a package installing from $url"
  done < <(jq -r '(.responseData.packages // [])[] | .packageUrl // "(no packageUrl)" | tostring' "$WORK/packages.json")
  refuse "the draft holds $count packages; this replaces the package of a listing that holds exactly one, so nothing was changed"
fi
OLD_URL="$(jq -r '.responseData.packages[0].packageUrl // "" | tostring' "$WORK/packages.json")"

# core <version> — the version's MAJOR.MINOR.PATCH, or nothing.
core() {
  if [[ "$1" =~ ^([0-9]+\.[0-9]+\.[0-9]+) ]]; then
    printf '%s' "${BASH_REMATCH[1]}"
  fi
}
draft_version=""
if [[ "$OLD_URL" =~ /aggrega-cli/v([^/]+)/[^/]+$ ]]; then
  candidate="${BASH_REMATCH[1]}"
  if key="$(smoke_download_key "$candidate")" && [[ "$OLD_URL" == *"/$key" ]]; then
    draft_version="$candidate"
  fi
fi
CURRENT=false
if [ "$OLD_URL" = "$NEW_URL" ]; then
  CURRENT=true
  say "$NEW_URL is already the draft's package"
elif [ -n "$draft_version" ]; then
  draft_core="$(core "$draft_version")" want_core="$(core "$VERSION")"
  if [ -n "$draft_core" ] && [ -n "$want_core" ] && [ "$draft_core" != "$want_core" ] \
    && [ "$(printf '%s\n%s\n' "$draft_core" "$want_core" | LC_ALL=C sort -V | tail -n 1)" = "$draft_core" ]; then
    if [ "$DRY" = true ]; then
      say "dry run: the draft's package is the installer of $draft_version, newer than $VERSION; a real run would refuse, changing nothing"
      WOULD_REFUSE+=("the draft's package is the installer of $draft_version, newer than $VERSION")
    else
      defer "the draft's package, $OLD_URL, is the installer of $draft_version, newer than $VERSION, so the draft already holds the installer of a newer version and the listing is left unchanged"
    fi
  fi
fi

# --- 8. The draft's English listing ------------------------------------------

store GET /metadata/listings "the read of the draft's listings"
warnings "the draft's listings"
cp -- "$WORK/answer" "$WORK/listings.json"
ENGLISH='[(.responseData.listings // [])[] | objects
  | select(.language // "" | tostring | ascii_downcase | test("^en(-|$)"))]'
count="$(jq "$ENGLISH | length" "$WORK/listings.json")"
if [ "$count" -ne 1 ]; then
  languages="$(jq -r '[(.responseData.listings // [])[] | objects | .language // "(no language)" | tostring] | join(", ")' \
    "$WORK/listings.json")"
  refuse "the draft holds $count English listings (its listings' languages: ${languages:-none}); this sets the What's new text of a draft that holds exactly one, so nothing was changed"
fi
jq "$ENGLISH | .[0]" "$WORK/listings.json" >"$WORK/listing.json"
LANGUAGE="$(jq -r '.language | tostring' "$WORK/listing.json")"
while IFS= read -r other; do
  say "the draft's $other listing is left as it is"
done < <(jq -r --arg lang "$LANGUAGE" '(.responseData.listings // [])[] | objects
  | .language // "(no language)" | tostring | select(. != $lang)' "$WORK/listings.json")
OLD_WHATS_NEW="$(jq -r '.whatsNew // "" | tostring' "$WORK/listing.json")"
LISTING_CURRENT=false
if [ -n "$WHATS_NEW" ] && [ "$OLD_WHATS_NEW" = "$WHATS_NEW" ]; then
  LISTING_CURRENT=true
  say "the $LANGUAGE listing's whatsNew already holds the section of $VERSION in CHANGELOG.md"
fi

# --- 9. The change -----------------------------------------------------------

# text_lines <text> — prints each line of <text> as a message, "  | <line>"
# ("  |" for a blank line), or "  (empty)" for an empty text.
text_lines() {
  local line
  if [ -z "$1" ]; then
    say "  (empty)"
    return
  fi
  while IFS= read -r line || [ -n "$line" ]; do
    say "  |${line:+ $line}"
  done <<<"$1"
}

if [ "$LISTING_CURRENT" = true ]; then
  say "the $LANGUAGE listing's whatsNew (unchanged):"
  text_lines "$OLD_WHATS_NEW"
else
  say "the $LANGUAGE listing's whatsNew now:"
  text_lines "$OLD_WHATS_NEW"
  if [ -n "$WHATS_NEW" ]; then
    say "the $LANGUAGE listing's whatsNew after, from the section of $VERSION in CHANGELOG.md:"
    text_lines "$WHATS_NEW"
  else
    say "the $LANGUAGE listing's whatsNew after: none, since CHANGELOG.md gives no text for $VERSION"
  fi
fi
say "the draft's package $(jq -r '.responseData.packages[0].packageId // "(no packageId)" | tostring' "$WORK/packages.json"):"
if [ "$CURRENT" = true ]; then
  say "  packageUrl: $OLD_URL (unchanged)"
else
  say "  packageUrl: $OLD_URL -> $NEW_URL"
fi
while IFS= read -r field; do
  say "  $field (unchanged)"
done < <(jq -r '.responseData.packages[0] | to_entries[] | select(.key != "packageUrl") | "\(.key): \(.value | tojson)"' \
  "$WORK/packages.json")

# --- 10. A dry run stops here ------------------------------------------------

if [ "$DRY" = true ]; then
  if [ "$LISTING_CURRENT" = true ]; then
    listing_change="leaves the $LANGUAGE listing's whatsNew, already the section of $VERSION in CHANGELOG.md, as it is"
  else
    listing_change="sets the $LANGUAGE listing's whatsNew to the section of $VERSION in CHANGELOG.md"
  fi
  if [ "${#WOULD_REFUSE[@]}" -gt 0 ]; then
    reasons=""
    for reason in "${WOULD_REFUSE[@]}"; do
      reasons+="${reasons:+; }$reason"
    done
    outcome "dry run for $VERSION: a real run would refuse, changing nothing: $reasons. Nothing that changes the draft was sent."
  elif [ "$CURRENT" = true ]; then
    outcome "dry run for $VERSION: the draft's package is already $NEW_URL; a real run $listing_change, then commits and submits the draft. Nothing that changes the draft was sent."
  else
    outcome "dry run for $VERSION: a real run $listing_change, changes the draft's package from $OLD_URL to $NEW_URL and submits it. Nothing that changes the draft was sent."
  fi
  exit 0
fi

# --- 11. The listing's What's new --------------------------------------------

if [ "$LISTING_CURRENT" = true ]; then
  say "the $LANGUAGE listing's whatsNew is not sent again"
else
  jq --arg text "$WHATS_NEW" '{listings: (.whatsNew = $text)}' "$WORK/listing.json" >"$WORK/patch.json"
  store PATCH /metadata "the update of the $LANGUAGE listing's whatsNew" "$WORK/patch.json"
  warnings "the update of the $LANGUAGE listing's whatsNew"
  say "the $LANGUAGE listing's whatsNew now holds the section of $VERSION in CHANGELOG.md"
  store GET /status "the read of the draft's status"
  wait_ready 60 10 "after the listing's update"
fi

# --- 12. Update, commit, wait until ready ------------------------------------

if [ "$CURRENT" = true ]; then
  say "the draft's package is not updated; the draft is committed and submitted as it stands"
else
  jq --arg url "$NEW_URL" '{packages: [.responseData.packages[0] | .packageUrl = $url]}' \
    "$WORK/packages.json" >"$WORK/put.json"
  store PUT /packages "the update of the draft's package" "$WORK/put.json"
  warnings "the update of the draft's package"
  say "the draft's package now installs from $NEW_URL"
fi
store POST /packages/commit "the commit of the draft's packages"
warnings "the commit of the draft's packages"
store GET /status "the read of the draft's status"
wait_ready 90 10 "after the commit"
say "the draft is ready"

# --- 13. Submit --------------------------------------------------------------

store POST /submit "the submit"
warnings "the submit"
SUBMISSION="$(jq -r '(.responseData.submissionId // "" | tostring) as $id
  | if $id != "" then $id else (.responseData.ongoingSubmissionId // "" | tostring) end' "$WORK/answer")"
[ -n "$SUBMISSION" ] || refuse_answer "the Store's answer to the submit names no submission"
say "the Store created submission $SUBMISSION for $VERSION"

# --- 14. The submission's status ---------------------------------------------

tries=20
for ((try = 1; try <= tries; try++)); do
  store GET "/submission/$(uri "$SUBMISSION")/status" "the read of submission $SUBMISSION's status"
  status="$(jq -r '.responseData.publishingStatus // "UNKNOWN" | tostring | ascii_upcase' "$WORK/answer")"
  failed="$(jq -r '.responseData.hasFailed // false | tostring' "$WORK/answer")"
  if [ "$status" = FAILED ] || [ "$failed" = true ]; then
    refuse_answer "submission $SUBMISSION of $VERSION failed: publishingStatus $status, hasFailed $failed"
  fi
  warnings "submission $SUBMISSION's status"
  case "$status" in
    PUBLISHED)
      outcome "submission $SUBMISSION of $VERSION is published: the Store listing now installs $NEW_URL"
      exit 0
      ;;
    INPROGRESS)
      outcome "submission $SUBMISSION of $VERSION is in certification. The submission API has no request that publishes a certified submission: Partner Center shows whether it goes live by itself or waits for its Publish button."
      exit 0
      ;;
  esac
  if [ "$try" -lt "$tries" ]; then
    say "submission $SUBMISSION reads publishingStatus $status (read $try of $tries); reading it again in $(seconds 30) seconds"
    pause 30
  fi
done
refuse "submission $SUBMISSION of $VERSION still reads publishingStatus $status after $tries reads, $(seconds 30) seconds apart; Partner Center shows where it stands"
