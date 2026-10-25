#!/usr/bin/env bash
# aggrega-cli/scripts/sign-windows-files.sh — sign Windows program files with
# Authenticode, as Cognitio S.r.l., through Azure Artifact Signing: the one
# signing every Windows file of a release goes through: the binaries and the
# program files the installer carries, in the release workflow's sign-windows
# job, and the installer, in its sign-windows-installer job.
#
#   check-settings
#       Refuses, naming each, any of the five settings below that is not set,
#       before any request, so a missing one is named rather than surfacing as a
#       refusal from the token exchange or the signing service.
#   fetch-tools <dir>
#       Fetches jsign and the Java runtime it runs under into <dir>, each at one
#       version and refused, before it is unpacked or run, unless its SHA-256 is
#       the one this script pins, so no Java the machine carries is run. The
#       runtime is built for x64 Linux, and another machine is refused, naming
#       what it is.
#   sign <dir> <file>...
#       Signs the files in place, in one jsign run with the tools fetch-tools
#       put in <dir>, every signature carrying an RFC 3161 timestamp from
#       Microsoft's timestamp service.
#
# The signing service keeps its keys in its own hardware: jsign sends each file's
# digest to it, and it signs with a certificate it issues from the account's
# certificate profile, valid for three days, which is why every signature is
# timestamped: Windows checks a timestamped signature's certificate as of the
# time the timestamp attests, so the signature stays valid after the
# certificate expires.
#
# sign signs as an Azure identity without holding a secret. It mints the run's
# OIDC token, which only a job granted id-token: write can mint, exchanges it
# with Microsoft Entra ID for a token scoped to the signing service, and signs
# with that, all in one process, so the signing token lives only in its shell
# and in jsign's environment, never in a file or in the environment of a later
# step. Neither token stands on a command line: each reaches curl on its
# standard input, and the signing token reaches jsign through --storepass env:,
# which names the variable it is read from. Each is masked in the log the moment
# it is read. A refused exchange fails, naming the HTTP status, Entra ID's error
# and its description, and the run's ref: the identity's federated credentials
# in Azure decide which refs may sign.
#
# jsign signs every file given in one run, because it asks the service for the
# certificate chain once per run. --tsaurl and --tsmode are both given: jsign
# takes the timestamp's form from --tsmode alone, so the address without it
# would ask for the older Authenticode timestamp rather than an RFC 3161 one.
#
# It reads, from the environment: the five settings, AZURE_TENANT_ID,
# AZURE_CLIENT_ID, ARTIFACT_SIGNING_ENDPOINT, ARTIFACT_SIGNING_ACCOUNT and
# ARTIFACT_SIGNING_PROFILE, each an identifier and none a secret; and, for sign,
# what GitHub Actions gives a job that may mint the OIDC token,
# ACTIONS_ID_TOKEN_REQUEST_URL and ACTIONS_ID_TOKEN_REQUEST_TOKEN, with
# GITHUB_REF and GITHUB_WORKFLOW_REF for the refusal's message. It runs curl,
# tar, sha256sum and the fetched Java and jsign, and nothing else beside the
# shell's own utilities.
#
# Usage:
#   scripts/sign-windows-files.sh check-settings
#   scripts/sign-windows-files.sh fetch-tools <dir>
#   scripts/sign-windows-files.sh sign <dir> <file>...
#   scripts/sign-windows-files.sh --help
#
# Exits 0 when it did what it was asked, 1 when it could not, and 2 for an
# argument it cannot use.
set -euo pipefail

# jsign 7.5, from Maven Central, and the SHA-256 of that jar, which is also the
# digest GitHub publishes for the same jar on jsign's 7.5 release. Whatever the
# server sends, a jar with any other digest is refused before it runs; moving to
# another version changes both lines.
JSIGN_URL=https://repo1.maven.org/maven2/net/jsign/jsign/7.5/jsign-7.5.jar
JSIGN_SHA256=602a51c3545a6dc4fb99bd2ea7152b26d1345916d0c93ddfbd5936cb735af91c
# The Java runtime jsign runs under: Eclipse Temurin 21.0.12.1+1, the JRE for x64
# Linux, from Adoptium's release on GitHub, and the SHA-256 of that archive,
# which is also the digest GitHub publishes for it, the release's own
# .sha256.txt and Adoptium's API. An archive with any other digest is refused
# before it is unpacked; moving to another version changes both lines.
JAVA_URL=https://github.com/adoptium/temurin21-binaries/releases/download/jdk-21.0.12.1%2B1/OpenJDK21U-jre_x64_linux_hotspot_21.0.12.1_1.tar.gz
JAVA_SHA256=2413149700df0f7d440500a84a8f764c535f21e5a5e87d38328b64eec2c5b500

SETTINGS=(AZURE_TENANT_ID AZURE_CLIENT_ID ARTIFACT_SIGNING_ENDPOINT ARTIFACT_SIGNING_ACCOUNT ARTIFACT_SIGNING_PROFILE)

refuse() { printf 'sign-windows-files: %s\n' "$*" >&2; exit 1; }
usage_error() { printf 'sign-windows-files: %s\n' "$*" >&2; exit 2; }

check_settings() {
  local name missing=0
  for name in "${SETTINGS[@]}"; do
    if [ -z "${!name:-}" ]; then
      printf 'sign-windows-files: the repository variable %s is not set\n' "$name" >&2
      missing=1
    fi
  done
  [ "$missing" = 0 ] || exit 1
  echo "sign-windows-files: signing with the profile $ARTIFACT_SIGNING_PROFILE of the account $ARTIFACT_SIGNING_ACCOUNT at $ARTIFACT_SIGNING_ENDPOINT"
}

fetch_tools() {
  local dir="$1" machine archive runtime jar actual
  machine="$(uname -s)/$(uname -m)"
  if [ "$machine" != Linux/x86_64 ]; then
    refuse "the Java runtime this script pins is for x64 Linux, and this machine is $machine"
  fi
  mkdir -p "$dir"
  archive="$dir/java-runtime.tar.gz"
  runtime="$dir/java"
  curl -sSfL --retry 3 -o "$archive" "$JAVA_URL"
  actual="$(sha256sum < "$archive")"
  actual="${actual%% *}"
  if [ "$actual" != "$JAVA_SHA256" ]; then
    rm -f -- "$archive"
    refuse "$JAVA_URL has SHA-256 $actual, not $JAVA_SHA256, the digest this script pins; refusing to unpack it"
  fi
  mkdir -p "$runtime"
  tar -xzf "$archive" -C "$runtime" --strip-components=1
  rm -f -- "$archive"
  echo "java: $JAVA_URL, SHA-256 $actual, the pinned digest, unpacked at $runtime"
  "$runtime/bin/java" -version

  jar="$dir/jsign.jar"
  curl -sSfL --retry 3 -o "$jar" "$JSIGN_URL"
  actual="$(sha256sum < "$jar")"
  actual="${actual%% *}"
  if [ "$actual" != "$JSIGN_SHA256" ]; then
    rm -f -- "$jar"
    refuse "$JSIGN_URL has SHA-256 $actual, not $JSIGN_SHA256, the digest this script pins; refusing to run it"
  fi
  echo "jsign: $JSIGN_URL, SHA-256 $actual, the pinned digest"
}

sign_files() {
  local dir="$1" oidc_json re oidc_token token_url response status body error description access_token file
  shift
  [ -x "$dir/java/bin/java" ] && [ -f "$dir/jsign.jar" ] ||
    refuse "$dir holds no Java runtime and jsign; run fetch-tools $dir first"
  for file in "$@"; do
    [ -f "$file" ] || refuse "there is no file to sign at $file"
  done
  check_settings >/dev/null

  if ! oidc_json="$(printf 'Authorization: bearer %s\n' "${ACTIONS_ID_TOKEN_REQUEST_TOKEN:-}" |
      curl -sSf -H @- "${ACTIONS_ID_TOKEN_REQUEST_URL:-}&audience=api://AzureADTokenExchange")"; then
    refuse "the Actions runtime did not issue this run's OIDC token"
  fi
  re='"value":"([^"]+)"'
  if ! [[ $oidc_json =~ $re ]]; then
    refuse "the Actions runtime returned no OIDC token"
  fi
  oidc_token="${BASH_REMATCH[1]}"
  echo "::add-mask::$oidc_token"
  # The request credentials are not needed again, so they are taken out of the
  # environment jsign inherits. That narrows what jsign is handed; it is no
  # barrier, since a program running as this job's user can still read them
  # from the process that started it.
  unset ACTIONS_ID_TOKEN_REQUEST_TOKEN ACTIONS_ID_TOKEN_REQUEST_URL

  token_url="https://login.microsoftonline.com/$AZURE_TENANT_ID/oauth2/v2.0/token"
  if ! response="$(printf '%s' "$oidc_token" | curl -sS \
      --data-urlencode "client_id=$AZURE_CLIENT_ID" \
      --data-urlencode "scope=https://codesigning.azure.net/.default" \
      --data-urlencode "client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer" \
      --data-urlencode "client_assertion@-" \
      --data-urlencode "grant_type=client_credentials" \
      -w '\n%{http_code}' "$token_url")"; then
    refuse "the token request to $token_url did not complete"
  fi
  status="${response##*$'\n'}"
  body="${response%$'\n'*}"
  if [ "$status" != 200 ]; then
    error="" description=""
    re='"error":"([^"]*)"'
    if [[ $body =~ $re ]]; then error="${BASH_REMATCH[1]}"; fi
    re='"error_description":"(([^"\\]|\\.)*)"'
    if [[ $body =~ $re ]]; then description="${BASH_REMATCH[1]}"; fi
    printf 'sign-windows-files: Microsoft Entra ID refused to exchange this run'"'"'s OIDC token: HTTP %s, %s: %s\n' \
      "$status" "${error:-no error code}" "${description:-no description}" >&2
    refuse "this run's ref is ${GITHUB_REF:-unknown} (workflow ${GITHUB_WORKFLOW_REF:-unknown}); the federated credentials of the identity AZURE_CLIENT_ID names decide which refs may sign"
  fi
  re='"access_token":"([^"]+)"'
  if ! [[ $body =~ $re ]]; then
    refuse "Microsoft Entra ID answered HTTP 200 without an access token"
  fi
  access_token="${BASH_REMATCH[1]}"
  echo "::add-mask::$access_token"
  unset oidc_json oidc_token response body

  ARTIFACT_SIGNING_TOKEN="$access_token" "$dir/java/bin/java" -jar "$dir/jsign.jar" \
    --storetype TRUSTEDSIGNING \
    --keystore "$ARTIFACT_SIGNING_ENDPOINT" \
    --storepass env:ARTIFACT_SIGNING_TOKEN \
    --alias "$ARTIFACT_SIGNING_ACCOUNT/$ARTIFACT_SIGNING_PROFILE" \
    --tsaurl http://timestamp.acs.microsoft.com/ \
    --tsmode RFC3161 \
    "$@"
  unset access_token
}

case "${1:-}" in
  -h|--help) sed -n '2,/^# argument it cannot use\.$/p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
  check-settings)
    [ "$#" = 1 ] || usage_error "check-settings takes no argument"
    check_settings
    ;;
  fetch-tools)
    [ "$#" = 2 ] && [ -n "$2" ] || usage_error "fetch-tools takes the directory to fetch the tools into"
    fetch_tools "$2"
    ;;
  sign)
    [ "$#" -ge 3 ] && [ -n "$2" ] || usage_error "sign takes the tools' directory and at least one file"
    shift
    sign_files "$@"
    ;;
  '') usage_error "name what to do: check-settings, fetch-tools or sign (see --help)" ;;
  *) usage_error "unknown command: $1 (see --help)" ;;
esac
