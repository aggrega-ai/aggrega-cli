#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# test-microsoft-store-submission.sh — checks the download-address functions of
# scripts/lib/platform-smoke-common.sh against a local HTTPS stub that serves canned answers and logs each
# request. The stub listens on 127.0.0.1 with a self-signed certificate; nothing
# else on the network is reached, and every temporary file lives in one
# directory removed on exit.
#
# The cases cover smoke_download_key and smoke_download_url (the object key, the
# address, the trailing slash of the base, an empty version) and
# smoke_check_download (200 with the right bytes, a redirect, a digest that
# differs, a response without Content-Length).
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

if [ "$FAIL" -ne 0 ]; then
  printf '%d case(s) failed\n' "$FAIL" >&2
  exit 1
fi
