#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# test-microsoft-store-submission.sh — checks microsoft-store-submission.sh and
# the download-address functions of scripts/lib/platform-smoke-common.sh it
# uses, against a local HTTPS stub that serves canned answers and logs each
# request. The stub listens on 127.0.0.1 with a self-signed certificate and
# plays the download address, Microsoft Entra ID's token endpoint and the
# Store's submission API; gh is a stub on PATH that serves a release's
# SHA256SUMS. Nothing else on the network is reached, and every temporary file
# lives in one directory removed on exit.
#
# The download cases cover smoke_download_key and smoke_download_url (the object
# key, the address, the trailing slash of the base, an empty version) and
# smoke_check_download (200 with the right bytes, a redirect, a digest that
# differs, a response without Content-Length), and smoke_header_value (a header's
# value without regard to case, the last of two, none).
#
# The submission cases run the script with every wait set to 0 seconds and
# cover: a real run end to end, each request's method, path, headers and body
# held to the API's documentation; a submission that is published; a dry run,
# which sends nothing that changes the draft, with and without a release; each
# setting missing; a pre-release version on a real run; a malformed version; a
# seller ID that is not a number; a real run with no release; an installer
# whose digest differs from the release's list; a submission already in
# progress or a draft holding a newer version, each left alone with exit status
# 3; a draft holding two packages; a draft already
# holding the version; a package upload error and an error outside the
# packages while the draft gets ready; a draft never ready, before the change
# and after it; a refused update, submit and submission; a submission whose
# status stays unknown; a publishingStatus in any case; a warning among the
# draft's status errors; a 403 with its likely cause; a dry run for a build's
# version that has a release; the settings named in the outcome line; a refused token; a rate-limited read retried; the token
# masked under GitHub Actions; and, over every run, that no credential and no
# token reaches the output and every line names the script.
#
# Exit: 0 when every case passes; 1 with the failing cases named.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORK="$(mktemp -d)"
STUB_PID=""
stub_stop() {
  if [ -n "$STUB_PID" ]; then
    kill "$STUB_PID" 2>/dev/null
    wait "$STUB_PID" 2>/dev/null
    STUB_PID=""
  fi
}
trap 'stub_stop; rm -rf "$WORK"' EXIT

FAIL=0
fail() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$*"; }
pass() { printf 'PASS %s\n' "$*"; }

# shellcheck source-path=SCRIPTDIR source=../../scripts/lib/platform-smoke-common.sh
. "$HERE/../../scripts/lib/platform-smoke-common.sh"

openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=127.0.0.1 \
  -addext subjectAltName=IP:127.0.0.1 -keyout "$WORK/key.pem" -out "$WORK/cert.pem" 2>/dev/null \
  || { echo "FAIL could not make the stub's certificate" >&2; exit 1; }
export CURL_CA_BUNDLE="$WORK/cert.pem"

# The stub: python3 -I stub.py <cert> <key> <routes.json> <log.jsonl> <port-file>.
# routes.json is {"routes": [{"method", "path", "responses": [{"status",
# "headers": {}, one of "body" | "json" | "body_file", "omit_length": false}]}]};
# a route's responses are served in order, the last repeating, and a path
# matches without its query string. Every request appends one line to the log:
# {"method", "path", "query", "headers" (names lower-cased), "body"}. A request
# no route matches is answered 404 with an error body naming it.
cat >"$WORK/stub.py" <<'PY'
import http.server, json, ssl, sys, threading, urllib.parse

cert, key, routes_file, log_file, port_file = sys.argv[1:6]
with open(routes_file) as f:
    ROUTES = json.load(f)["routes"]
COUNTS = {}
LOCK = threading.Lock()


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def handle_any(self):
        parsed = urllib.parse.urlsplit(self.path)
        length = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(length).decode("utf-8", "replace") if length else ""
        entry = {
            "method": self.command,
            "path": parsed.path,
            "query": parsed.query,
            "headers": {k.lower(): v for k, v in self.headers.items()},
            "body": body,
        }
        with LOCK:
            with open(log_file, "a") as f:
                f.write(json.dumps(entry) + "\n")
            response = None
            for index, route in enumerate(ROUTES):
                if route["method"] == self.command and route["path"] == parsed.path:
                    n = COUNTS.get(index, 0)
                    COUNTS[index] = n + 1
                    served = route["responses"]
                    response = served[min(n, len(served) - 1)]
                    break
        if response is None:
            status, headers, payload, omit = 404, {"Content-Type": "application/json"}, json.dumps({
                "isSuccess": False,
                "errors": [{"code": "notfound", "message": "no stub route for %s %s" % (self.command, parsed.path), "target": ""}],
            }).encode(), False
        else:
            status = response["status"]
            headers = dict(response.get("headers", {}))
            omit = response.get("omit_length", False)
            if "body_file" in response:
                with open(response["body_file"], "rb") as f:
                    payload = f.read()
            elif "json" in response:
                payload = json.dumps(response["json"]).encode()
                headers.setdefault("Content-Type", "application/json")
            else:
                payload = response.get("body", "").encode()
        self.send_response(status)
        for name, value in headers.items():
            self.send_header(name, value)
        if not omit:
            self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(payload)
        self.close_connection = True
        if omit:
            # No length to frame the body: end it with a clean TLS shutdown.
            self.wfile.flush()
            try:
                self.connection = self.connection.unwrap()
            except (ssl.SSLError, OSError):
                pass

    do_GET = do_POST = do_PUT = do_DELETE = do_HEAD = do_PATCH = handle_any


server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
context.load_cert_chain(cert, key)
server.socket = context.wrap_socket(server.socket, server_side=True)
with open(port_file + ".tmp", "w") as f:
    f.write(str(server.server_address[1]))
import os
os.replace(port_file + ".tmp", port_file)
server.serve_forever()
PY

# stub_start ROUTES_JSON — starts the stub on routes file ROUTES_JSON, waits for
# its port, and sets STUB_BASE (https://127.0.0.1:<port>), STUB_LOG and
# STUB_PID. Returns 1 when the stub does not come up.
stub_start() {
  local routes="$1" tries=0
  stub_stop
  STUB_LOG="$WORK/stub-log.jsonl"
  : >"$STUB_LOG"
  rm -f "$WORK/stub.port"
  python3 -I "$WORK/stub.py" "$WORK/cert.pem" "$WORK/key.pem" "$routes" "$STUB_LOG" "$WORK/stub.port" \
    >"$WORK/stub.out" 2>&1 &
  STUB_PID=$!
  while [ ! -s "$WORK/stub.port" ]; do
    tries=$((tries + 1))
    if [ "$tries" -gt 100 ] || ! kill -0 "$STUB_PID" 2>/dev/null; then
      echo "the stub did not start:" >&2
      cat "$WORK/stub.out" >&2
      return 1
    fi
    sleep 0.1
  done
  STUB_BASE="https://127.0.0.1:$(cat "$WORK/stub.port")"
}

# expect CASE WANT_STATUS WANT_SUBSTRING COMMAND... — runs the command, capturing
# standard output and standard error together, and holds its exit status to
# WANT_STATUS and its output to containing WANT_SUBSTRING (empty: no check).
expect() {
  local name="$1" want_status="$2" want_text="$3" out status
  shift 3
  out="$("$@" 2>&1)"
  status=$?
  if [ "$status" -ne "$want_status" ]; then
    fail "$name: exit $status, want $want_status; output [$out]"
  elif [ -n "$want_text" ] && [[ $out != *"$want_text"* ]]; then
    fail "$name: output [$out] lacks [$want_text]"
  else
    pass "$name"
  fi
}

KEY="aggrega-cli/v1.2.3/aggrega-1.2.3-windows-amd64-setup.exe"
INSTALLER="$WORK/installer.bin"
printf 'installer bytes for the stub\n' >"$INSTALLER"
SIZE="$(wc -c <"$INSTALLER" | tr -d '[:space:]')"
DIGEST="$(sha256sum <"$INSTALLER")"
DIGEST="${DIGEST%% *}"
WRONG="$(printf 'other bytes\n' | sha256sum)"
WRONG="${WRONG%% *}"

# smoke_curl_config_quote: the " and \ are also proven end to end, through curl,
# by the submission cases' token request; newline and carriage return here.
quoted="$(smoke_curl_config_quote $'a\\b"c\nd\re~')"
if [ "$quoted" = 'a\\b\"c\nd\re~' ]; then
  pass curl_config_quote_escapes
else
  fail "curl_config_quote_escapes: printed [$quoted]"
fi

# smoke_header_value: a dump-header file as curl writes it, CRLF line ends.
printf 'HTTP/2 200\r\nX-Correlation-ID: first\r\nx-correlation-id:   second-id\r\nContent-Length: 7\r\n\r\n' >"$WORK/dumped-headers"
expect header_value_last_without_case 0 "second-id" smoke_header_value "$WORK/dumped-headers" X-CORRELATION-ID
hv="$(smoke_header_value "$WORK/dumped-headers" content-length)"
if [ "$hv" = 7 ]; then pass header_value_strips_cr; else fail "header_value_strips_cr: printed [$hv]"; fi
hv="$(smoke_header_value "$WORK/dumped-headers" x-missing)"
if [ -z "$hv" ]; then pass header_value_absent_prints_nothing; else fail "header_value_absent_prints_nothing: printed [$hv]"; fi

expect download_names_key 0 "$KEY" smoke_download_key 1.2.3
expect download_names_url 0 "https://download.aggrega.ai/$KEY" smoke_download_url 1.2.3
export DOWNLOADS_PUBLIC_BASE="https://example.test/base/"
expect download_names_base_slash 0 "https://example.test/base/$KEY" smoke_download_url 1.2.3
unset DOWNLOADS_PUBLIC_BASE

# Both functions refuse an empty version with status 2 and print nothing.
out_key="$(smoke_download_key "" 2>&1)"; st_key=$?
out_url="$(smoke_download_url "" 2>&1)"; st_url=$?
if [ "$st_key" -ne 2 ] || [ "$st_url" -ne 2 ]; then
  fail "download_names_empty_version: exit $st_key and $st_url, want 2 and 2"
elif [ -n "$out_key$out_url" ]; then
  fail "download_names_empty_version: printed [$out_key$out_url], want nothing"
else
  pass download_names_empty_version
fi

cat >"$WORK/routes-ok.json" <<JSON
{"routes": [{"method": "GET", "path": "/$KEY", "responses": [{"status": 200, "body_file": "$INSTALLER"}]}]}
JSON
stub_start "$WORK/routes-ok.json" || exit 1
expect download_ok 0 "$STUB_BASE/$KEY" smoke_check_download "$STUB_BASE/$KEY" "$DIGEST"
expect download_ok_length 0 "Content-Length $SIZE" smoke_check_download "$STUB_BASE/$KEY" "$DIGEST"
expect download_ok_digest 0 "$DIGEST" smoke_check_download "$STUB_BASE/$KEY" "$DIGEST"

expect download_digest_mismatch 1 "$DIGEST" smoke_check_download "$STUB_BASE/$KEY" "$WRONG"
expect download_digest_mismatch_expected 1 "$WRONG" smoke_check_download "$STUB_BASE/$KEY" "$WRONG"
stub_stop

cat >"$WORK/routes-redirect.json" <<JSON
{"routes": [{"method": "GET", "path": "/$KEY", "responses": [{"status": 302, "headers": {"Location": "https://elsewhere.test/signed"}}]}]}
JSON
stub_start "$WORK/routes-redirect.json" || exit 1
expect download_redirect 1 "HTTP 302, redirecting to https://elsewhere.test/signed" smoke_check_download "$STUB_BASE/$KEY" "$DIGEST"
stub_stop

cat >"$WORK/routes-nolength.json" <<JSON
{"routes": [{"method": "GET", "path": "/$KEY", "responses": [{"status": 200, "body_file": "$INSTALLER", "omit_length": true}]}]}
JSON
stub_start "$WORK/routes-nolength.json" || exit 1
expect download_no_length 1 "no Content-Length" smoke_check_download "$STUB_BASE/$KEY" "$DIGEST"
expect download_no_length_names_address 1 "$STUB_BASE/$KEY" smoke_check_download "$STUB_BASE/$KEY" "$DIGEST"
stub_stop

# ---------------------------------------------------------------------------
# microsoft-store-submission.sh

SCRIPT="$HERE/microsoft-store-submission.sh"
VERSION=0.10.3
TENANT=stub-tenant-id
CLIENT=stub-client-id
# The secret holds a ", a \ and a ~, so the token request's form fields prove
# the quoting of curl's configuration end to end.
SECRET='stub-"secret\value~'
TOKEN=stub-access-token
SELLER=12345678
PRODUCT=9NSTUBPRODUCT1
SUB=1152921505690000001
ONGOING=1152921505690000777
P="/submission/v1/product/$PRODUCT"
TOKEN_PATH="/$TENANT/oauth2/v2.0/token"
NEW_KEY="$(smoke_download_key "$VERSION")"
INSTALLER_NAME="${NEW_KEY##*/}"
PREFIX="microsoft-store-submission: "

export MICROSOFT_STORE_TENANT_ID="$TENANT" MICROSOFT_STORE_CLIENT_ID="$CLIENT"
export MICROSOFT_STORE_CLIENT_SECRET="$SECRET" MICROSOFT_STORE_SELLER_ID="$SELLER"
export MICROSOFT_STORE_PRODUCT_ID="$PRODUCT" MICROSOFT_STORE_POLL_SECONDS=0
export GH_STUB="$WORK/gh" GH_REPO=aggrega-ai/aggrega-cli
unset GITHUB_ACTIONS GITHUB_STEP_SUMMARY
mkdir -p "$WORK/bin" "$GH_STUB"

# The gh stub: "release view <tag>" exits 0 when <tag> is a line of
# $GH_STUB/releases and otherwise exits 1 saying "release not found", as gh
# does; "release download <tag> --dir <dir> --pattern SHA256SUMS" copies
# $GH_STUB/SHA256SUMS into <dir>. Every call is appended to $GH_STUB/calls, and
# the name of each credential setting gh finds in its environment to
# $GH_STUB/inherited.
cat >"$WORK/bin/gh" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$GH_STUB/calls"
for name in MICROSOFT_STORE_TENANT_ID MICROSOFT_STORE_CLIENT_ID MICROSOFT_STORE_CLIENT_SECRET; do
  [ -z "${!name+set}" ] || printf '%s\n' "$name" >>"$GH_STUB/inherited"
done
case "${1:-} ${2:-}" in
  "release view")
    if grep -qxF -- "${3:-}" "$GH_STUB/releases"; then
      printf '{"tagName":"%s"}\n' "$3"
      exit 0
    fi
    echo "release not found" >&2
    exit 1
    ;;
  "release download")
    tag="${3:-}" dir="" pattern=""
    shift 3
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --dir) dir="$2"; shift 2 ;;
        --pattern) pattern="$2"; shift 2 ;;
        *) shift ;;
      esac
    done
    grep -qxF -- "$tag" "$GH_STUB/releases" || { echo "release not found" >&2; exit 1; }
    [ "$pattern" = SHA256SUMS ] || { echo "gh stub: no asset matches $pattern" >&2; exit 1; }
    mkdir -p "$dir" && cp "$GH_STUB/SHA256SUMS" "$dir/SHA256SUMS"
    ;;
  *)
    echo "gh stub: unexpected call: $*" >&2
    exit 2
    ;;
esac
SH
chmod +x "$WORK/bin/gh"

# answer <responseData> [<errors>] — one stub response: HTTP 200 with the
# Store's envelope, isSuccess true.
answer() {
  jq -cn --argjson data "$1" --argjson errors "${2:-[]}" \
    '{status: 200, json: {isSuccess: true, errors: $errors, responseData: $data}}'
}
# refusal <status> <correlation> <errors> — one stub response: the Store's
# envelope with isSuccess false, carrying an X-Correlation-ID.
refusal() {
  jq -cn --argjson status "$1" --arg corr "$2" --argjson errors "$3" \
    '{status: $status, headers: {"X-Correlation-ID": $corr}, json: {isSuccess: false, errors: $errors, responseData: {}}}'
}
STATUS_READY="$(answer '{"isReady": true, "ongoingSubmissionId": ""}')"
STATUS_BUSY="$(answer '{"isReady": false, "ongoingSubmissionId": ""}')"
PACKAGES_DRAFT="$(jq -cn --arg file "$WORK/packages.json" \
  '{status: 200, headers: {"Content-Type": "application/json"}, body_file: $file}')"
WARNING='[{"code": "warning", "target": "packages", "message": "stub: the package keeps its languages"}]'

# begin_case — resets the answers to those of a real run end to end, the
# release v$VERSION to existing with SHA256SUMS listing the stub installer, and
# the draft to 0.10.2's installer. A case then changes the one thing it is about.
begin_case() {
  R_TOKEN="[$(jq -cn --arg token "$TOKEN" '{status: 200, json: {token_type: "Bearer", expires_in: 3599, access_token: $token}}')]"
  R_STATUS="[$STATUS_READY, $STATUS_BUSY, $STATUS_READY]"
  R_PACKAGES="[$PACKAGES_DRAFT]"
  R_PUT="[$(answer "{\"pollingUrl\": \"$P/status\", \"ongoingSubmissionId\": \"\"}" "$WARNING")]"
  R_COMMIT="[$(answer "{\"pollingUrl\": \"$P/status\", \"ongoingSubmissionId\": \"\"}")]"
  R_SUBMIT="[$(answer "{\"submissionId\": \"$SUB\", \"pollingUrl\": \"$P/submission/$SUB/status\", \"ongoingSubmissionId\": \"\"}")]"
  R_SUBMISSION="[$(answer '{"publishingStatus": "INPROGRESS", "hasFailed": false}')]"
  R_ONGOING="[$(answer '{"publishingStatus": "INPROGRESS", "hasFailed": false}')]"
  DRAFT_VERSIONS=(0.10.2)
  printf 'v%s\n' "$VERSION" >"$GH_STUB/releases"
  printf '%s  %s\n%s  %s\n' "$DIGEST" "$INSTALLER_NAME" "$WRONG" "aggrega-$VERSION-linux-amd64" >"$GH_STUB/SHA256SUMS"
  : >"$GH_STUB/calls"
}

# serve_case — starts the stub on the R_* answers and writes the draft, one
# package per DRAFT_VERSIONS entry, each installing from that version's address
# at the stub, as the fixture answer to GET /packages.
serve_case() {
  local version
  jq -n --arg dl "/$NEW_KEY" --arg installer "$INSTALLER" --arg token "$TOKEN_PATH" --arg p "$P" \
    --arg sub "$SUB" --arg ongoing "$ONGOING" --argjson rtoken "$R_TOKEN" --argjson rstatus "$R_STATUS" \
    --argjson rpackages "$R_PACKAGES" --argjson rput "$R_PUT" --argjson rcommit "$R_COMMIT" \
    --argjson rsubmit "$R_SUBMIT" --argjson rsubmission "$R_SUBMISSION" --argjson rongoing "$R_ONGOING" \
    '{routes: [
      {method: "GET", path: $dl, responses: [{status: 200, body_file: $installer}]},
      {method: "POST", path: $token, responses: $rtoken},
      {method: "GET", path: ($p + "/status"), responses: $rstatus},
      {method: "GET", path: ($p + "/packages"), responses: $rpackages},
      {method: "PUT", path: ($p + "/packages"), responses: $rput},
      {method: "POST", path: ($p + "/packages/commit"), responses: $rcommit},
      {method: "POST", path: ($p + "/submit"), responses: $rsubmit},
      {method: "GET", path: ($p + "/submission/" + $sub + "/status"), responses: $rsubmission},
      {method: "GET", path: ($p + "/submission/" + $ongoing + "/status"), responses: $rongoing}
    ]}' >"$WORK/routes.json"
  stub_start "$WORK/routes.json" || exit 1
  export MICROSOFT_STORE_LOGIN_BASE="$STUB_BASE" MICROSOFT_STORE_API_BASE="$STUB_BASE" DOWNLOADS_PUBLIC_BASE="$STUB_BASE"
  local -a urls=()
  for version in "${DRAFT_VERSIONS[@]}"; do
    urls+=("$STUB_BASE/$(smoke_download_key "$version")")
  done
  jq -n '{isSuccess: true, errors: [], responseData: {packages: [$ARGS.positional | to_entries[] | {
      packageId: "pkg-\(.key + 1)", packageUrl: .value, languages: ["en-us"], architectures: ["X64"],
      isSilentInstall: true, installerParameters: "/S",
      genericDocUrl: "https://nsis.sourceforge.io/Docs/AppendixD.html#errorlevels",
      packageType: "exe", extraField: "kept"}]}}' --args "${urls[@]}" >"$WORK/packages.json"
  OLD_URL="${urls[0]}"
  NEW_URL="$STUB_BASE/$NEW_KEY"
}

# run_case [<env argument>...] -- <script argument>... — runs the script on the
# stub's settings, env applying each <env argument> (NAME=value, or -u NAME);
# sets OUT (standard output and standard error together) and RC, and appends
# OUT to the record every run's output is checked over at the end.
run_case() {
  local -a mods=()
  while [ "$1" != -- ]; do
    mods+=("$1")
    shift
  done
  shift
  OUT="$(env ${mods[@]+"${mods[@]}"} PATH="$WORK/bin:$PATH" bash "$SCRIPT" "$@" 2>&1)"
  RC=$?
  printf '%s\n' "$OUT" >>"$WORK/script-output"
  FAULTS=()
}

# The checks a case makes on the last run; each fault is recorded and the case
# fails naming every one. The searches of the request log that a case expects
# to find nothing are the same searches real_run_end_to_end expects to find
# each request with.
requests() { jq -r '"\(.method) \(.path)"' "$STUB_LOG"; }
want_exit() { [ "$RC" -eq "$1" ] || FAULTS+=("exit $RC, want $1"); }
want_text() { [[ $OUT == *"$1"* ]] || FAULTS+=("the output lacks [$1]"); }
want_no_text() { [[ $OUT != *"$1"* ]] || FAULTS+=("the output holds [$1]"); }
# want_outcome <text> — the step summary, which receives the outcome line
# alone, holds <text>.
want_outcome() { grep -qF -- "$1" "$WORK/summary-$CASE.md" 2>/dev/null || FAULTS+=("the outcome line lacks [$1]"); }
want_count() {
  local n
  n="$(requests | grep -Ec -- "$2")"
  [ "$n" -eq "$1" ] || FAULTS+=("$n request(s) match [$2], want $1")
}
want_no_store_request() { want_count 0 "^[A-Z]+ /submission/"; }
want_nothing_sent() {
  [ ! -s "$STUB_LOG" ] || FAULTS+=("the stub received [$(requests | paste -sd, -)]")
  [ ! -s "$GH_STUB/calls" ] || FAULTS+=("gh was called: [$(paste -sd, - <"$GH_STUB/calls")]")
}
want_true() { # want_true <what> <jq filter over the request log, slurped>
  jq -es "$2" "$STUB_LOG" >/dev/null || FAULTS+=("$1")
}
end_case() {
  if [ "${#FAULTS[@]}" -eq 0 ]; then
    pass "$1"
  else
    local fault text=""
    for fault in "${FAULTS[@]}"; do text+="$fault; "; done
    fail "$1: ${text}output [$OUT]"
  fi
  stub_stop
}

begin_case
serve_case
run_case GITHUB_STEP_SUMMARY="$WORK/summary.md" -- --version "$VERSION" --dry-run false
want_exit 0
want_text "is in certification"
want_text "no request that publishes"
want_text "warning / packages / stub: the package keeps its languages"
got="$(requests | paste -sd'|' -)"
want="GET /$NEW_KEY|POST $TOKEN_PATH|GET $P/status|GET $P/packages|PUT $P/packages|POST $P/packages/commit|GET $P/status|GET $P/status|POST $P/submit|GET $P/submission/$SUB/status"
[ "$got" = "$want" ] || FAULTS+=("requests [$got], want [$want]")
want_true "the token request is not a form" \
  "map(select(.path == \"$TOKEN_PATH\")) | length == 1 and all(.headers[\"content-type\"] | startswith(\"application/x-www-form-urlencoded\")) and all(.headers.authorization == null)"
form="$(jq -j --arg p "$TOKEN_PATH" 'select(.path == $p) | .body' "$STUB_LOG" \
  | python3 -I -c 'import json, sys, urllib.parse; print(json.dumps(sorted(urllib.parse.parse_qsl(sys.stdin.read(), keep_blank_values=True, strict_parsing=True)), separators=(",", ":")))')"
want_form="$(jq -cn --arg c "$CLIENT" --arg s "$SECRET" \
  '[["client_id", $c], ["client_secret", $s], ["grant_type", "client_credentials"], ["scope", "https://api.store.microsoft.com/.default"]]')"
[ "$form" = "$want_form" ] || FAULTS+=("the token request's form fields differ from the four documented ones")
want_true "a Store request lacks the bearer token or the seller header" \
  "map(select(.path | startswith(\"/submission/\"))) | length == 8 and all(.headers.authorization == \"Bearer $TOKEN\" and .headers[\"x-seller-account-id\"] == \"$SELLER\")"
want_true "the PUT is not application/json" \
  "map(select(.method == \"PUT\")) | length == 1 and all(.headers[\"content-type\"] == \"application/json\")"
want_true "a Store request without a body carries a body or a Content-Type" \
  "map(select((.path | startswith(\"/submission/\")) and .method != \"PUT\")) | all(.body == \"\" and .headers[\"content-type\"] == null)"
put_body="$(jq -r 'select(.method == "PUT") | .body' "$STUB_LOG" | jq -S . 2>/dev/null)"
want_body="$(jq -Sn --arg url "$NEW_URL" '{packages: [{
    packageId: "pkg-1", packageUrl: $url, languages: ["en-us"], architectures: ["X64"],
    isSilentInstall: true, installerParameters: "/S",
    genericDocUrl: "https://nsis.sourceforge.io/Docs/AppendixD.html#errorlevels",
    packageType: "exe", extraField: "kept"}]}')"
[ "$put_body" = "$want_body" ] || FAULTS+=("the PUT body [$put_body] is not the draft with packageUrl $NEW_URL")
grep -qF "is in certification" "$WORK/summary.md" 2>/dev/null || FAULTS+=("the step summary lacks the outcome line")
end_case real_run_end_to_end

begin_case
R_SUBMISSION="[$(answer '{"publishingStatus": "PUBLISHED", "hasFailed": false}')]"
serve_case
run_case -- --version "$VERSION" --dry-run false
want_exit 0
want_text "is published"
end_case real_run_published

begin_case
R_SUBMISSION="[$(answer '{"publishingStatus": "PUBLISHED", "hasFailed": false}')]"
serve_case
run_case GITHUB_ACTIONS=true -- --version "$VERSION" --dry-run false
want_exit 0
masks="$(grep -c "^::add-mask::$TOKEN\$" <<<"$OUT")"
[ "$masks" -eq 1 ] || FAULTS+=("$masks ::add-mask:: line(s) for the token, want 1")
grep -v '^::add-mask::' <<<"$OUT" | grep -qF "$TOKEN" && FAULTS+=("the token appears outside its ::add-mask:: line")
end_case masks_token_under_actions

begin_case
serve_case
run_case -- --version "$VERSION" --dry-run true
want_exit 0
want_text "dry run"
want_text "$OLD_URL"
want_text "$NEW_URL"
want_text "a real run changes the draft's package from $OLD_URL to $NEW_URL"
want_count 0 "^PUT "
want_count 0 "^POST /submission/"
want_count 1 "^GET $P/packages\$"
end_case dry_run_reads_only

begin_case
: >"$GH_STUB/releases"
serve_case
BUILD_VERSION=0.10.4-3-gabc1234
CASE=dry_run_no_release
run_case GITHUB_STEP_SUMMARY="$WORK/summary-$CASE.md" -- --version "$BUILD_VERSION" --dry-run true
want_exit 0
want_text "does not exist yet"
want_outcome "a real run would refuse, changing nothing: '$BUILD_VERSION' is not a version of the form MAJOR.MINOR.PATCH; the release v$BUILD_VERSION does not exist yet."
want_no_text "a real run changes the draft's package"
want_text "$OLD_URL"
want_text "$STUB_BASE/$(smoke_download_key "$BUILD_VERSION")"
want_count 0 "^GET /aggrega-cli/"
want_count 0 "^PUT "
want_count 0 "^POST /submission/"
grep -q '^release download' "$GH_STUB/calls" && FAULTS+=("gh downloaded from a release that does not exist")
end_case dry_run_no_release

for setting in MICROSOFT_STORE_TENANT_ID MICROSOFT_STORE_CLIENT_ID MICROSOFT_STORE_CLIENT_SECRET \
  MICROSOFT_STORE_SELLER_ID MICROSOFT_STORE_PRODUCT_ID; do
  begin_case
  serve_case
  CASE="refuses_missing_settings_$setting"
  run_case -u "$setting" GITHUB_STEP_SUMMARY="$WORK/summary-$CASE.md" -- --version "$VERSION" --dry-run true
  want_exit 1
  want_text "$setting"
  want_text "is not set"
  want_outcome "not set in the GitHub environment microsoft-store: "
  want_outcome "$setting; every setting is needed on a dry run and a real run alike"
  want_nothing_sent
  end_case "refuses_missing_settings $setting"
done

begin_case
serve_case
run_case MICROSOFT_STORE_SELLER_ID=12x -- --version "$VERSION" --dry-run true
want_exit 1
want_text "MICROSOFT_STORE_SELLER_ID"
want_text "12x"
want_nothing_sent
end_case refuses_seller_id_not_number

begin_case
serve_case
run_case -- --version 0.10.4-rc.1 --dry-run false
want_exit 1
want_text "0.10.4-rc.1"
want_text "MAJOR.MINOR.PATCH"
want_nothing_sent
end_case refuses_prerelease_version

begin_case
serve_case
run_case -- --version "v$VERSION" --dry-run true
want_exit 2
want_text "v$VERSION"
want_nothing_sent
end_case refuses_malformed_version

# The header is not a run's message, so this run stays out of the record of runs.
begin_case
serve_case
OUT="$(PATH="$WORK/bin:$PATH" bash "$SCRIPT" --help 2>&1)"
RC=$?
FAULTS=()
want_exit 0
want_text "microsoft-store-submission.sh — "
want_text "MICROSOFT_STORE_CLIENT_SECRET"
want_text "draft's package is the installer of a newer version, its outcome line naming"
want_nothing_sent
end_case help_prints_header

begin_case
: >"$GH_STUB/releases"
serve_case
run_case -- --version "$VERSION" --dry-run false
want_exit 1
want_text "v$VERSION does not exist"
want_count 0 "."
end_case refuses_real_run_no_release

begin_case
printf '%s  %s\n' "$WRONG" "$INSTALLER_NAME" >"$GH_STUB/SHA256SUMS"
serve_case
run_case -- --version "$VERSION" --dry-run false
want_exit 1
want_text "serves SHA-256 $DIGEST, but $WRONG was expected"
want_count 0 "^POST $TOKEN_PATH\$"
want_no_store_request
end_case refuses_installer_digest

begin_case
R_STATUS="[$(answer "{\"isReady\": false, \"ongoingSubmissionId\": \"$ONGOING\"}")]"
serve_case
CASE=defers_ongoing_submission
run_case GITHUB_STEP_SUMMARY="$WORK/summary-$CASE.md" -- --version "$VERSION" --dry-run false
want_exit 3
want_outcome "submission in progress, $ONGOING (publishingStatus INPROGRESS, hasFailed false), so nothing was changed; dispatch \"Submit to the Microsoft Store\" with $VERSION"
want_text "$ONGOING"
want_text "in progress"
want_text "dispatch \"Submit to the Microsoft Store\" with $VERSION once that submission has ended"
want_text "publishingStatus INPROGRESS, hasFailed false"
want_count 1 "^GET $P/submission/$ONGOING/status\$"
want_count 0 "^PUT "
want_count 0 "^POST /submission/"
end_case defers_ongoing_submission

begin_case
R_STATUS="[$(answer "{\"isReady\": false, \"ongoingSubmissionId\": \"$ONGOING\"}")]"
serve_case
CASE=refuses_ongoing_submission_dry_run_reports
run_case GITHUB_STEP_SUMMARY="$WORK/summary-$CASE.md" -- --version "$VERSION" --dry-run true
want_exit 0
want_text "$ONGOING"
want_text "a real run would refuse"
want_outcome "a real run would refuse, changing nothing: submission $ONGOING is in progress."
want_no_text "a real run changes the draft's package"
want_count 1 "^GET $P/status\$"
want_count 0 "^PUT "
want_count 0 "^POST /submission/"
end_case refuses_ongoing_submission_dry_run_reports

begin_case
DRAFT_VERSIONS=(0.10.2 0.10.1)
serve_case
run_case -- --version "$VERSION" --dry-run false
want_exit 1
want_text "$OLD_URL"
want_text "$STUB_BASE/$(smoke_download_key 0.10.1)"
want_count 0 "^PUT "
want_count 0 "^POST /submission/"
end_case refuses_two_packages

begin_case
DRAFT_VERSIONS=(0.10.5)
serve_case
CASE=defers_newer_draft
run_case GITHUB_STEP_SUMMARY="$WORK/summary-$CASE.md" -- --version "$VERSION" --dry-run false
want_exit 3
want_outcome "is the installer of 0.10.5, newer than $VERSION, so the draft already holds the installer of a newer version and the listing is left unchanged"
want_text "0.10.5"
want_text "newer than $VERSION"
want_no_text "dispatch"
want_count 0 "^PUT "
want_count 0 "^POST /submission/"
end_case defers_newer_draft

begin_case
DRAFT_VERSIONS=(0.10.5)
serve_case
CASE=refuses_newer_draft_dry_run_reports
run_case GITHUB_STEP_SUMMARY="$WORK/summary-$CASE.md" -- --version "$VERSION" --dry-run true
want_exit 0
want_text "newer than $VERSION"
want_text "a real run would refuse"
want_outcome "a real run would refuse, changing nothing: the draft's package is the installer of 0.10.5, newer than $VERSION."
want_no_text "a real run changes the draft's package"
want_count 0 "^PUT "
want_count 0 "^POST /submission/"
end_case refuses_newer_draft_dry_run_reports

begin_case
DRAFT_VERSIONS=("$VERSION")
serve_case
run_case -- --version "$VERSION" --dry-run false
want_exit 0
want_text "already the draft's package"
want_text "is in certification"
want_count 0 "^PUT "
want_count 1 "^POST $P/packages/commit\$"
want_count 1 "^POST $P/submit\$"
end_case draft_already_current

begin_case
R_STATUS="[$STATUS_READY, $(answer '{"isReady": false, "ongoingSubmissionId": ""}' \
  '[{"code": "packageuploaderror", "target": "packages", "message": "stub: the package could not be downloaded"}]')]"
serve_case
run_case -- --version "$VERSION" --dry-run false
want_exit 1
want_text "packageuploaderror / packages / stub: the package could not be downloaded"
want_count 2 "^GET $P/status\$"
want_count 0 "^POST $P/submit\$"
end_case refuses_upload_error

begin_case
R_STATUS="[$STATUS_READY, $(answer '{"isReady": false, "ongoingSubmissionId": ""}' \
  '[{"code": "invalidinput", "target": "listings", "message": "stub: a listing field is missing"}]')]"
serve_case
run_case -- --version "$VERSION" --dry-run false
want_exit 1
want_text "invalidinput / listings / stub: a listing field is missing"
want_count 2 "^GET $P/status\$"
want_count 0 "^POST $P/submit\$"
end_case refuses_status_error_outside_packages

begin_case
R_STATUS="[$STATUS_BUSY]"
serve_case
run_case -- --version "$VERSION" --dry-run false
want_exit 1
want_text "not ready"
want_count 60 "^GET $P/status\$"
want_count 0 "^GET $P/packages\$"
end_case refuses_not_ready_before_change

begin_case
R_STATUS="[$STATUS_READY, $STATUS_BUSY]"
serve_case
run_case -- --version "$VERSION" --dry-run false
want_exit 1
want_text "not ready"
want_count 91 "^GET $P/status\$"
want_count 0 "^POST $P/submit\$"
end_case refuses_not_ready_after_commit

begin_case
# A 503 is an answer curl --retry would ask again after; a write is sent once,
# so the second answer, a success, is never served.
R_PUT="[$(refusal 503 corr-put-1 '[{"code": "serviceunavailable", "target": "packages", "message": "stub: the update is not available"}]'), ${R_PUT#[}"
serve_case
run_case -- --version "$VERSION" --dry-run false
want_exit 1
want_text "serviceunavailable / packages / stub: the update is not available"
want_text "corr-put-1"
want_text "HTTP 503"
want_count 1 "^PUT $P/packages\$"
want_count 0 "^POST $P/packages/commit\$"
end_case refuses_failed_put

begin_case
R_SUBMIT="[$(refusal 200 corr-submit-1 '[{"code": "invalidstate", "target": "submission", "message": "stub: a module is not ready"}]')]"
serve_case
run_case -- --version "$VERSION" --dry-run false
want_exit 1
want_text "invalidstate / submission / stub: a module is not ready"
want_text "corr-submit-1"
want_count 0 "^GET $P/submission/"
end_case refuses_failed_submit

begin_case
R_SUBMISSION="[$(answer '{"publishingStatus": "FAILED", "hasFailed": true}' \
  '[{"code": "certificationfailed", "target": "submission", "message": "stub: the installer did not install silently"}]')]"
serve_case
run_case -- --version "$VERSION" --dry-run false
want_exit 1
want_text "FAILED"
want_text "certificationfailed / submission / stub: the installer did not install silently"
end_case refuses_failed_submission

begin_case
R_SUBMISSION="[$(answer '{"publishingStatus": "UNKNOWN", "hasFailed": false}')]"
serve_case
run_case -- --version "$VERSION" --dry-run false
want_exit 1
want_text "UNKNOWN"
want_text "$SUB"
want_count 20 "^GET $P/submission/$SUB/status\$"
end_case refuses_unknown_submission

begin_case
# Entra ID's description names the client ID and the tenant; the run prints
# each as ***, which no_credential_in_output holds over every run.
R_TOKEN="[$(jq -cn --arg c "$CLIENT" --arg t "$TENANT" '{status: 400, json: {error: "unauthorized_client",
  error_description: "AADSTS700016: Application with identifier \u0027\($c)\u0027 was not found in the directory \u0027\($t)\u0027.\r\nTrace ID: stub-trace"}}')]"
serve_case
run_case -- --version "$VERSION" --dry-run false
want_exit 1
want_text "unauthorized_client"
want_text "AADSTS700016: Application with identifier '***' was not found in the directory '***'."
want_no_store_request
end_case refuses_token_error

begin_case
# A dry run for a build's version that has a release: the release workflow
# publishes the address for a final release alone, so the address is not asked.
printf 'v%s\n' "$BUILD_VERSION" >"$GH_STUB/releases"
serve_case
CASE=dry_run_build_version_with_release
run_case GITHUB_STEP_SUMMARY="$WORK/summary-$CASE.md" -- --version "$BUILD_VERSION" --dry-run true
want_exit 0
want_text "is not a final release's version, so the installer at $STUB_BASE/$(smoke_download_key "$BUILD_VERSION") is not checked"
want_outcome "a real run would refuse, changing nothing: '$BUILD_VERSION' is not a version of the form MAJOR.MINOR.PATCH. Nothing"
want_no_text "does not exist yet"
want_count 0 "^GET /aggrega-cli/"
want_count 0 "^PUT "
want_count 0 "^POST /submission/"
grep -q '^release download' "$GH_STUB/calls" && FAULTS+=("gh downloaded SHA256SUMS for a build's version")
end_case dry_run_build_version_with_release

begin_case
R_STATUS="[$(answer '{"isReady": true, "ongoingSubmissionId": ""}' \
  '[{"code": "warning", "target": "properties", "message": "stub: a property is advisory"}]'), $STATUS_BUSY, $STATUS_READY]"
serve_case
run_case -- --version "$VERSION" --dry-run false
want_exit 0
want_text "warning / properties / stub: a property is advisory"
want_text "is in certification"
want_count 1 "^PUT $P/packages\$"
want_count 1 "^POST $P/submit\$"
end_case status_warning_does_not_refuse

begin_case
R_SUBMISSION="[$(answer '{"publishingStatus": "InProgress", "hasFailed": false}')]"
serve_case
run_case -- --version "$VERSION" --dry-run false
want_exit 0
want_text "is in certification"
want_count 1 "^GET $P/submission/$SUB/status\$"
end_case submission_status_any_case

begin_case
R_STATUS="[$(refusal 403 corr-403-1 '[{"code": "forbidden", "target": "", "message": "stub: the caller has no access"}]')]"
serve_case
CASE=refuses_forbidden_names_cause
run_case GITHUB_STEP_SUMMARY="$WORK/summary-$CASE.md" -- --version "$VERSION" --dry-run true
want_exit 1
want_text "HTTP 403"
want_text "forbidden /  / stub: the caller has no access"
want_text "corr-403-1"
want_text "the Entra app registration lacks the Manager role in Partner Center, or the seller ID is not this account's"
want_outcome "lacks the Manager role in Partner Center"
want_count 0 "^PUT "
end_case refuses_forbidden_names_cause

begin_case
R_STATUS="[$(refusal 500 corr-500-1 '[{"code": "internal", "target": "", "message": "stub: broke"}]')]"
serve_case
run_case -- --version "$VERSION" --dry-run true
want_exit 1
want_text "HTTP 500"
want_no_text "Manager role"
end_case refuses_server_error_names_no_cause

begin_case
R_PACKAGES="[$(refusal 429 corr-busy-1 '[{"code": "toomanyrequests", "target": "", "message": "stub: slow down"}]' \
  | jq -c '.headers["Retry-After"] = "1"'), $PACKAGES_DRAFT]"
serve_case
run_case -- --version "$VERSION" --dry-run true
want_exit 0
want_text "$NEW_URL"
want_count 2 "^GET $P/packages\$"
end_case retries_rate_limited_read

FAULTS=()
OUT="$(cat "$WORK/script-output")"
for value in "$SECRET" "$TENANT" "$CLIENT"; do
  grep -qF -- "$value" "$WORK/script-output" && FAULTS+=("a run printed a credential")
done
grep -v '^::add-mask::' "$WORK/script-output" | grep -qF -- "$TOKEN" && FAULTS+=("a run printed the token outside its ::add-mask:: line")
[ -s "$WORK/script-output" ] || FAULTS+=("no run's output was recorded")
[ ! -s "$GH_STUB/inherited" ] || FAULTS+=("gh inherited [$(sort -u "$GH_STUB/inherited" | paste -sd, -)]")
# The planted match: gh started straight from this suite, whose environment
# holds the settings, records them, so an empty record above means something.
mkdir -p "$WORK/gh-planted"
: >"$WORK/gh-planted/releases"
GH_STUB="$WORK/gh-planted" "$WORK/bin/gh" release view v0 2>/dev/null
[ -s "$WORK/gh-planted/inherited" ] || FAULTS+=("the gh stub records no inherited setting even when it has one")
grep -qF -- "::add-mask::$TOKEN" "$WORK/script-output" || FAULTS+=("the record lacks the masked run, so the token search proves nothing")
OUT="(every run's output)"
end_case no_credential_in_output

FAULTS=()
unprefixed="$(grep -v -e '^$' -e "^$PREFIX" -e '^::add-mask::' "$WORK/script-output" | head -n 5 | paste -sd'|' -)"
[ -z "$unprefixed" ] || FAULTS+=("lines not beginning with [$PREFIX]: [$unprefixed]")
OUT="(every run's output)"
end_case every_line_names_the_script

if [ "$FAIL" -ne 0 ]; then
  printf '%d case(s) failed\n' "$FAIL" >&2
  exit 1
fi
