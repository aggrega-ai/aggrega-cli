// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Every proof in this file drives a real listener over a real TCP connection.
// Nothing here calls ServeHTTP directly with a synthetic recorder: what is under
// test is statuses, headers and a closed connection, and a handler invoked
// in-process would let half of it pass without a transport existing.
//
// # The test-only authenticator
//
// acceptAllAuthenticator admits every request. It is TEST-ONLY and it is
// declared in this file rather than beside the handler, so nothing outside a
// test can reach it: the production value refuses every request, the field
// holding it is unexported, and the exported constructor installs the refusing
// value. The bypass exists because the feature under test in this file is the
// transport — routing, header mirroring, status mapping, the caps and
// cancellation — and a credential check standing in front of all of it would
// leave every one of those behaviours unreachable.
//
// Two proofs deliberately do NOT install it: the ordering proof, which shows an
// unknown path answered before the credential is looked at, and the default
// proof, which shows what an untouched handler does.
type acceptAllAuthenticator struct{}

// authenticate admits every request and attaches a caller whose credential
// carried every scope this deployment publishes.
//
// Attaching one is not incidental to admitting the request: what a caller may
// reach is read off the scopes its credential asserted, and a request admitted
// with nothing attached reaches nothing at all. So a stub that returned the
// request untouched would leave every proof in this package that is not about
// scopes measuring an empty surface — which is a different thing from what each
// of them is about, and would read as those proofs failing rather than as the
// stub being wrong.
//
// Every scope is the honest value for a stub that admits everything. A proof
// about what a narrower credential reaches mints its own token instead, so
// nothing here decides that question by default.
func (acceptAllAuthenticator) authenticate(r *http.Request) (*http.Request, *authRefusal) {
	return withPrincipal(r, &principal{
		Subject: "accept-all@example.test",
		Scopes:  []string{scopeRead, scopeWorkspaceWrite, scopeRegistryWrite},
	}), nil
}

// httpFixture is one running listener with the handler behind it.
type httpFixture struct {
	ts *httptest.Server
	h  *httpHandler
}

// defaultTransportConfig is the shape every proof that is not about the caps
// runs under: a body cap far above any fixture here, room for more requests at
// once than any of them issues, an allowance far above what any of them spends,
// and the deployment every proof here runs as.
//
// The rate allowance is deliberately enormous rather than merely generous.
// Every proof in this package that is not about the rate cap shares one caller,
// and several of them issue a burst; an allowance sized to the busiest of them
// would make an unrelated proof start failing the day it grew one more request.
//
// The egress gate it carries opens and is never used. A handler consults the
// gate once, while it is built, and reads nothing from the network until a
// token actually arrives naming a key — which no proof outside the token file
// sends — so a fixture here reaches the authorization server this deployment
// declares exactly never.
func defaultTransportConfig() HTTPConfig {
	return HTTPConfig{
		MaxBodyBytes:          1 << 20,
		MaxConcurrentRequests: 8,
		RequestsPerMinute:     100_000,
		Authorization:         defaultAuthorizationConfig(),
		Egress:                func() (*http.Client, error) { return http.DefaultClient, nil },
	}
}

// newHTTPFixture starts a listener over the fixture command surface, with the
// test-only authenticator installed, under a run that never ends.
//
// hold is the seam a proof that needs a call to sit inside the handler installs.
// It is the lint seam rather than the command runner because a session reached
// over HTTP is composed with no command surface at all, so a call naming a
// generated tool is refused before anything runs and would hold nothing. The
// server is still built over the fixture command surface, because what the
// composition does with one is the thing several proofs here are about.
func newHTTPFixture(t *testing.T, transport HTTPConfig, hold lintRunner) *httpFixture {
	t.Helper()
	return newHTTPFixtureUnderRun(t, context.Background(), transport, hold)
}

// newHTTPFixtureUnderRun is newHTTPFixture with the run's own context supplied by
// the caller, so a proof can end the run while a call is in flight. Every other
// proof here runs under a context that never ends, which is the shape a serving
// process holds for as long as it serves.
func newHTTPFixtureUnderRun(t *testing.T, runCtx context.Context,
	transport HTTPConfig, hold lintRunner) *httpFixture {
	t.Helper()
	f := newUnauthenticatedHTTPFixture(t, runCtx, transport, hold)
	f.h.auth = acceptAllAuthenticator{}
	return f
}

// newUnauthenticatedHTTPFixture starts a listener with the authenticator the
// handler ships with, so what it serves is what a deployment serves.
func newUnauthenticatedHTTPFixture(t *testing.T, runCtx context.Context,
	transport HTTPConfig, hold lintRunner) *httpFixture {
	t.Helper()
	s := newCommandToolServer(t, nil)
	s.runLint = hold
	h, err := newHTTPHandler(runCtx, s, transport)
	if err != nil {
		t.Fatalf("newHTTPHandler: %v", err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return &httpFixture{ts: ts, h: h}
}

// answer is what one exchange produced: the status, the response headers, the
// raw body, and the body parsed as a JSON-RPC frame when it was one.
type answer struct {
	status int
	header http.Header
	raw    string
	frame  rpcResponse
}

// post sends one body to the protocol endpoint with the given headers.
func (f *httpFixture) post(t *testing.T, headers map[string]string, body string) answer {
	t.Helper()
	return f.send(t, http.MethodPost, endpointPath, headers, body)
}

// send drives one exchange against an arbitrary method and path, so the routing
// proofs can reach what the endpoint does not serve.
func (f *httpFixture) send(t *testing.T, method, path string, headers map[string]string, body string) answer {
	t.Helper()
	got, err := f.exchange(method, path, headers, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return got
}

// exchange is send without a testing handle, for the proofs that issue a request
// from a goroutine of their own: a failure there is reported by the test's own
// goroutine, because the testing package's failure calls belong to it alone.
func (f *httpFixture) exchange(method, path string, headers map[string]string, body string) (answer, error) {
	req, err := http.NewRequest(method, f.ts.URL+path, strings.NewReader(body))
	if err != nil {
		return answer{}, fmt.Errorf("build request: %w", err)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := f.ts.Client().Do(req)
	if err != nil {
		return answer{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return answer{}, fmt.Errorf("read response body: %w", err)
	}
	out := answer{status: resp.StatusCode, header: resp.Header, raw: string(raw)}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out.frame); err != nil {
			return answer{}, fmt.Errorf("response body is not a JSON-RPC frame: %q: %w", raw, err)
		}
	}
	return out, nil
}

// exchangeResult carries one exchange back from the goroutine that issued it.
// The answer travels rather than being asserted on where it was produced: the
// testing package's failure calls belong to the test's own goroutine and nowhere
// else.
type exchangeResult struct {
	got answer
	err error
}

// issue posts one body to the endpoint from a goroutine of its own and hands the
// result back on the channel it returns, for the proofs that need a request left
// in flight while they do something else to the listener.
func (f *httpFixture) issue(body string) <-chan exchangeResult {
	out := make(chan exchangeResult, 1)
	go func() {
		got, err := f.exchange(http.MethodPost, endpointPath, nil, body)
		out <- exchangeResult{got: got, err: err}
	}()
	return out
}

// awaitExchange reads back an exchange issued above, within the liveness
// ceiling. The ceiling separates "returned" from "hung forever" and is the only
// thing a clock decides anywhere in this file.
func awaitExchange(t *testing.T, results <-chan exchangeResult, what string) answer {
	t.Helper()
	select {
	case result := <-results:
		if result.err != nil {
			t.Fatalf("%s: %v", what, result.err)
		}
		return result.got
	case <-time.After(runReturnCeiling):
		t.Fatalf("%s never returned within the liveness ceiling", what)
		return answer{}
	}
}

// awaitEntered waits for a blocking seam to report it has been reached, within
// the same liveness ceiling. Reaching the ceiling means the request never got
// there at all, which is a different failure from whatever the proof went on to
// assert, so it is reported as its own.
func awaitEntered(t *testing.T, entered <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(runReturnCeiling):
		t.Fatalf("%s never reached the tool within the liveness ceiling", what)
	}
}

// awaitCause reads back what a blocking seam recorded on its way out: the cause
// its context carried, or nil where it left on its own release instead. A nil is
// therefore a reading rather than an absence of one, which is what lets a proof
// assert that nothing interrupted a call.
func awaitCause(t *testing.T, ended <-chan error, what string) error {
	t.Helper()
	select {
	case cause := <-ended:
		return cause
	case <-time.After(runReturnCeiling):
		t.Fatalf("%s never left the tool within the liveness ceiling", what)
		return nil
	}
}

// wantRefusal asserts one status and one error code, and reports both sides when
// either misses. Each negative fixture below carries exactly one fault, so the
// code it asserts is the one that fault pins: a fixture asserting merely "some
// error" would keep passing once an earlier gate started rejecting it for an
// unrelated reason, and would stop covering its own subject without saying so.
func wantRefusal(t *testing.T, got answer, wantStatus, wantCode int, what string) {
	t.Helper()
	if got.status != wantStatus {
		t.Errorf("%s: status = %d, want %d (body %q)", what, got.status, wantStatus, got.raw)
	}
	if got.frame.Error == nil {
		t.Fatalf("%s: no error envelope in %q", what, got.raw)
	}
	if got.frame.Error.Code != wantCode {
		t.Errorf("%s: error code = %d (%q), want %d",
			what, got.frame.Error.Code, got.frame.Error.Message, wantCode)
	}
}

// modernRequest composes a body in the per-request revision: the two reserved
// metadata names that revision requires of every request, plus the params the
// method takes. The names come from the constants the era reader matches on, so
// a change to either moves these fixtures with it instead of leaving them
// silently in the other revision.
func modernRequest(t *testing.T, id int, method string, params map[string]any) string {
	t.Helper()
	return modernRequestAtRevision(t, id, method, params, perRequestRevision)
}

// modernRequestAtRevision is modernRequest with the stated revision left to the
// caller, for the fixtures whose fault is the revision itself.
func modernRequestAtRevision(t *testing.T, id int, method string, params map[string]any, revision string) string {
	t.Helper()
	body := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	withMeta := map[string]any{}
	for k, v := range params {
		withMeta[k] = v
	}
	withMeta["_meta"] = map[string]any{
		metaKeyProtocolVersion:    revision,
		metaKeyClientCapabilities: map[string]any{},
	}
	body["params"] = withMeta
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("compose request: %v", err)
	}
	return string(encoded)
}

// modernHeaders composes the mirror headers a per-request-revision request
// carries. An empty name leaves the name header off, which is the shape every
// method that addresses nothing by name takes.
func modernHeaders(method, name string) map[string]string {
	headers := map[string]string{
		headerProtocolVersion: perRequestRevision,
		headerMethod:          method,
	}
	if name != "" {
		headers[headerName] = name
	}
	return headers
}

// base64Sentinel wraps a name in the form the revision defines for one whose
// bytes do not travel safely in a header field.
func base64Sentinel(name string) string {
	return nameSentinelPrefix + base64.StdEncoding.EncodeToString([]byte(name)) + nameSentinelSuffix
}

// A well-formed request in the per-request revision is answered with one JSON
// object carrying that revision's result envelope.
func TestHTTPModernRequestIsAnsweredWithAStampedResult(t *testing.T) {
	f := newHTTPFixture(t, defaultTransportConfig(), nil)

	got := f.post(t, modernHeaders("tools/list", ""), modernRequest(t, 1, "tools/list", nil))

	if got.status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", got.status, got.raw)
	}
	if ct := got.header.Get(headerContentType); ct != contentTypeJSON {
		t.Errorf("%s = %q, want %q", headerContentType, ct, contentTypeJSON)
	}
	if got.frame.Error != nil {
		t.Fatalf("error envelope: %+v", got.frame.Error)
	}
	var result struct {
		ResultType string `json:"resultType"`
	}
	if err := json.Unmarshal(got.frame.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.ResultType != resultTypeComplete {
		t.Errorf("resultType = %q, want %q; the result reached the wire undecorated",
			result.ResultType, resultTypeComplete)
	}
}

// The mirror headers are checked against the body, and the check runs before
// anything is routed.
//
// The method table is a grid rather than a list: era times header-present is
// four cells, and all four are reachable. The two per-request cells are here;
// the two handshake-revision cells are in the legacy proof below, where the
// header is optional and the other two headers belong to a revision the request
// is not speaking.
func TestHTTPMirrorHeadersAreCheckedAgainstTheBody(t *testing.T) {
	f := newHTTPFixture(t, defaultTransportConfig(), nil)

	t.Run("method header differs from the body", func(t *testing.T) {
		got := f.post(t, modernHeaders("resources/list", ""), modernRequest(t, 1, "tools/list", nil))
		wantRefusal(t, got, http.StatusBadRequest, codeHeaderMismatch, "method header mismatch")
	})

	t.Run("version header absent", func(t *testing.T) {
		headers := modernHeaders("tools/list", "")
		delete(headers, headerProtocolVersion)
		got := f.post(t, headers, modernRequest(t, 1, "tools/list", nil))
		wantRefusal(t, got, http.StatusBadRequest, codeHeaderMismatch, "version header absent")
	})

	t.Run("version header differs from the body", func(t *testing.T) {
		headers := modernHeaders("tools/list", "")
		headers[headerProtocolVersion] = handshakeRevision
		got := f.post(t, headers, modernRequest(t, 1, "tools/list", nil))
		wantRefusal(t, got, http.StatusBadRequest, codeHeaderMismatch, "version header mismatch")
	})
}

// The name header, on the methods that address a target by name.
//
// Every row below carries the same well-formed tools/call body naming a tool the
// server does not serve, and differs only in the name header. The positive
// control is the last row: with the header mirroring the body, the header gate
// lets the request through and the METHOD refuses it with a different code. That
// is what makes the two refusals above facts about the header rather than about
// the request being malformed in some other way.
func TestHTTPNameHeaderMirrorsTheAddressedTarget(t *testing.T) {
	f := newHTTPFixture(t, defaultTransportConfig(), nil)
	body := modernRequest(t, 1, "tools/call", map[string]any{
		"name":      "no-such-tool",
		"arguments": map[string]any{},
	})

	t.Run("name header absent", func(t *testing.T) {
		got := f.post(t, modernHeaders("tools/call", ""), body)
		wantRefusal(t, got, http.StatusBadRequest, codeHeaderMismatch, "name header absent")
	})

	t.Run("name header differs from the body", func(t *testing.T) {
		got := f.post(t, modernHeaders("tools/call", "some-other-tool"), body)
		wantRefusal(t, got, http.StatusBadRequest, codeHeaderMismatch, "name header mismatch")
	})

	t.Run("name header mirrors the body", func(t *testing.T) {
		got := f.post(t, modernHeaders("tools/call", "no-such-tool"), body)
		// Past the header gate and refused by the method it reached, which is a
		// different code. A run in which this row reported the mismatch code would
		// mean the gate above refuses everything and the two rows prove nothing.
		wantRefusal(t, got, http.StatusBadRequest, codeInvalidParams, "name header mirrors the body")
	})
}

// A name carried in the Base64 sentinel form is decoded before it is compared.
//
// The control is the first row: the same request with the name carried
// literally is served, so the second row's success is a fact about the decoding
// rather than about the name check having been skipped.
func TestHTTPNameHeaderSentinelIsDecodedBeforeComparison(t *testing.T) {
	f := newHTTPFixture(t, defaultTransportConfig(), nil)
	const uri = "schema://component"
	body := modernRequest(t, 1, "resources/read", map[string]any{"uri": uri})

	t.Run("literal name", func(t *testing.T) {
		got := f.post(t, modernHeaders("resources/read", uri), body)
		if got.status != http.StatusOK || got.frame.Error != nil {
			t.Fatalf("status = %d, error = %+v; want the request served (body %q)",
				got.status, got.frame.Error, got.raw)
		}
	})

	t.Run("sentinel name", func(t *testing.T) {
		got := f.post(t, modernHeaders("resources/read", base64Sentinel(uri)), body)
		if got.status != http.StatusOK || got.frame.Error != nil {
			t.Fatalf("status = %d, error = %+v; want the sentinel decoded and matched (body %q)",
				got.status, got.frame.Error, got.raw)
		}
	})

	t.Run("sentinel carrying a name the body does not address", func(t *testing.T) {
		got := f.post(t, modernHeaders("resources/read", base64Sentinel("schema://recipe")), body)
		wantRefusal(t, got, http.StatusBadRequest, codeHeaderMismatch, "decoded sentinel mismatch")
	})

	t.Run("sentinel wrapper carrying something that is not Base64", func(t *testing.T) {
		got := f.post(t, modernHeaders("resources/read", nameSentinelPrefix+"!!!"+nameSentinelSuffix), body)
		wantRefusal(t, got, http.StatusBadRequest, codeHeaderMismatch, "malformed sentinel")
	})
}

// The three protocol outcomes that are not header mismatches, each with the
// status its answer travels on.
func TestHTTPProtocolRefusalsCarryTheirOwnStatus(t *testing.T) {
	f := newHTTPFixture(t, defaultTransportConfig(), nil)

	t.Run("unsupported revision", func(t *testing.T) {
		headers := modernHeaders("tools/list", "")
		headers[headerProtocolVersion] = "1999-01-01"
		got := f.post(t, headers,
			modernRequestAtRevision(t, 1, "tools/list", nil, "1999-01-01"))
		wantRefusal(t, got, http.StatusBadRequest, codeUnsupportedProtocolVersion, "unsupported revision")

		var data unsupportedRevisionData
		if err := json.Unmarshal(got.frame.Error.Data, &data); err != nil {
			t.Fatalf("unmarshal refusal data: %v", err)
		}
		// Named rather than counted: two lists both short by one still agree on
		// length, so the miss has to say which revision is missing.
		for _, want := range []string{perRequestRevision, handshakeRevision} {
			if !slices.Contains(data.Supported, want) {
				t.Errorf("refusal lists %v, missing the spoken revision %q", data.Supported, want)
			}
		}
	})

	t.Run("required metadata key missing", func(t *testing.T) {
		// The capabilities key alone: enough to place the request in the
		// per-request revision, and short of what that revision requires.
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"` +
			metaKeyClientCapabilities + `":{}}}}`
		got := f.post(t, modernHeaders("tools/list", ""), body)
		wantRefusal(t, got, http.StatusBadRequest, codeInvalidParams, "missing metadata key")
	})

	t.Run("method the server does not serve", func(t *testing.T) {
		got := f.post(t, modernHeaders("does/not/exist", ""),
			modernRequest(t, 1, "does/not/exist", nil))
		wantRefusal(t, got, http.StatusNotFound, codeMethodNotFound, "unknown method")
	})
}

// A notification is accepted with no body; a JSON-RPC response is refused.
//
// The first row's control is the second: the same listener answering a request
// with a body. Without it an empty body would be indistinguishable from a
// listener that had stopped answering anything at all.
func TestHTTPNotificationIsAcceptedWithNoBody(t *testing.T) {
	f := newHTTPFixture(t, defaultTransportConfig(), nil)

	notification := f.post(t, nil, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if notification.status != http.StatusAccepted {
		t.Errorf("notification status = %d, want 202 (body %q)", notification.status, notification.raw)
	}
	if notification.raw != "" {
		t.Errorf("notification answered with a body: %q", notification.raw)
	}

	control := f.post(t, nil, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if control.status != http.StatusOK {
		t.Fatalf("control status = %d, want 200 (body %q)", control.status, control.raw)
	}
	if control.raw == "" {
		t.Fatal("the control was answered with no body either, so the assertion above " +
			"would pass for a listener that answers nothing")
	}
}

func TestHTTPResponseBodyIsRefused(t *testing.T) {
	f := newHTTPFixture(t, defaultTransportConfig(), nil)

	got := f.post(t, nil, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	wantRefusal(t, got, http.StatusBadRequest, codeInvalidRequest, "JSON-RPC response body")
}

// A handshake-revision client is served over this transport exactly as it is
// over the other one, statelessly.
func TestHTTPHandshakeRevisionIsServedWithoutASession(t *testing.T) {
	f := newHTTPFixture(t, defaultTransportConfig(), nil)

	initialize := f.post(t, nil,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+
			handshakeRevision+`","capabilities":{},"clientInfo":{"name":"probe"}}}`)
	if initialize.status != http.StatusOK {
		t.Fatalf("initialize status = %d, want 200 (body %q)", initialize.status, initialize.raw)
	}
	var res initializeResult
	if err := json.Unmarshal(initialize.frame.Result, &res); err != nil {
		t.Fatalf("unmarshal initialize result: %v", err)
	}
	if res.ProtocolVersion != handshakeRevision {
		t.Errorf("negotiated revision = %q, want %q", res.ProtocolVersion, handshakeRevision)
	}
	if id := initialize.header.Get("Mcp-Session-Id"); id != "" {
		t.Errorf("a session identifier was minted: %q", id)
	}
	// Control for the absence above: the reader does find a header that IS set,
	// so the empty session header is a fact about the answer rather than about
	// the way it was read.
	if initialize.header.Get(headerContentType) == "" {
		t.Fatal("no content type on the answer either, so the session-header assertion " +
			"above would pass for a response carrying no headers at all")
	}
}

// The version header on a handshake-revision request: the two cells this era
// has — header present and header absent — plus the two distinct faults a
// present one can carry.
func TestHTTPHandshakeRevisionVersionHeader(t *testing.T) {
	f := newHTTPFixture(t, defaultTransportConfig(), nil)
	const body = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

	t.Run("header absent", func(t *testing.T) {
		got := f.post(t, nil, body)
		if got.status != http.StatusOK || got.frame.Error != nil {
			t.Fatalf("status = %d, error = %+v; want the request served (body %q)",
				got.status, got.frame.Error, got.raw)
		}
	})

	t.Run("header names the handshake revision", func(t *testing.T) {
		got := f.post(t, map[string]string{headerProtocolVersion: handshakeRevision}, body)
		if got.status != http.StatusOK || got.frame.Error != nil {
			t.Fatalf("status = %d, error = %+v; want the request served (body %q)",
				got.status, got.frame.Error, got.raw)
		}
	})

	t.Run("header names a revision the server does not speak", func(t *testing.T) {
		got := f.post(t, map[string]string{headerProtocolVersion: "1999-01-01"}, body)
		wantRefusal(t, got, http.StatusBadRequest, codeUnsupportedProtocolVersion, "unspoken revision")
	})

	t.Run("header names the other revision the server does speak", func(t *testing.T) {
		// A different fault from the row above: the server speaks this revision,
		// and this body is not in it. The header does not mirror the body.
		got := f.post(t, map[string]string{headerProtocolVersion: perRequestRevision}, body)
		wantRefusal(t, got, http.StatusBadRequest, codeHeaderMismatch, "revision the body is not in")
	})
}

// A session or stream-resumption header is answered as if it were absent.
//
// The proof is a byte comparison against the same request without them, which is
// the only form that distinguishes "ignored" from "happened to also succeed".
func TestHTTPSessionAndResumptionHeadersAreIgnored(t *testing.T) {
	f := newHTTPFixture(t, defaultTransportConfig(), nil)
	const body = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

	plain := f.post(t, nil, body)
	if plain.status != http.StatusOK {
		t.Fatalf("plain status = %d, want 200 (body %q)", plain.status, plain.raw)
	}
	withHeaders := f.post(t, map[string]string{
		"Mcp-Session-Id": "a-session-this-server-never-minted",
		"Last-Event-ID":  "17",
	}, body)

	if withHeaders.status != plain.status {
		t.Errorf("status with the headers = %d, without = %d", withHeaders.status, plain.status)
	}
	if withHeaders.raw != plain.raw {
		t.Errorf("the answers differ:\n with: %s\n without: %s", withHeaders.raw, plain.raw)
	}
}

// What the endpoint does not serve, and what is not the endpoint.
func TestHTTPRoutingRefusesEverythingButPostOnTheEndpoint(t *testing.T) {
	f := newHTTPFixture(t, defaultTransportConfig(), nil)

	for _, method := range []string{http.MethodGet, http.MethodDelete, http.MethodPut, http.MethodHead} {
		got := f.send(t, method, endpointPath, nil, "")
		if got.status != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: status = %d, want 405", method, endpointPath, got.status)
		}
		if allow := got.header.Get(headerAllow); allow != http.MethodPost {
			t.Errorf("%s %s: %s = %q, want %q", method, endpointPath, headerAllow, allow, http.MethodPost)
		}
	}

	// A path that is not the endpoint is not the endpoint however close it looks,
	// and the two metadata paths are not it either: those are read, and a request
	// to one that is not a read is refused as a method rather than answered.
	for _, path := range []string{"/", "/mcpx", "/mcp/"} {
		got := f.send(t, http.MethodPost, path, nil, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
		if got.status != http.StatusNotFound {
			t.Errorf("POST %s: status = %d, want 404 (body %q)", path, got.status, got.raw)
		}
	}
	for _, path := range []string{testMetadataPath, wellKnownProtectedResource} {
		got := f.send(t, http.MethodPost, path, nil, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
		if got.status != http.StatusMethodNotAllowed {
			t.Errorf("POST %s: status = %d, want 405 (body %q)", path, got.status, got.raw)
		}
	}
}

// The path is settled before the credential is looked at.
//
// This one runs against the authenticator the handler SHIPS with, so the three
// answers below are what a deployment gives, and together they are the whole
// ordering: a path that does not exist is not-found whether or not a credential
// would have been accepted, the document is answered with no credential asked
// for, and the endpoint itself challenges. Each is the others' control — without
// the third, a listener refusing everything with not-found would pass the first;
// without the first, one challenging everywhere would pass the third.
func TestHTTPUnknownPathIsAnsweredBeforeAuthentication(t *testing.T) {
	f := newUnauthenticatedHTTPFixture(t, context.Background(), defaultTransportConfig(), nil)

	unknown := f.send(t, http.MethodPost, "/not-the-endpoint", nil, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if unknown.status != http.StatusNotFound {
		t.Errorf("unknown path: status = %d, want 404", unknown.status)
	}
	if challenge := unknown.header.Get(headerAuthenticate); challenge != "" {
		t.Errorf("unknown path carried a challenge %q, so a probe learns the credential scheme "+
			"from a path that does not exist", challenge)
	}

	document := f.send(t, http.MethodGet, testMetadataPath, nil, "")
	if document.status != http.StatusOK {
		t.Errorf("the document: status = %d, want 200 (body %q)", document.status, document.raw)
	}

	endpoint := f.post(t, nil, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if endpoint.status != http.StatusUnauthorized {
		t.Fatalf("endpoint: status = %d, want 401; the control that makes the assertions above "+
			"mean something is that this listener DOES challenge somewhere", endpoint.status)
	}
	scheme, params := parseChallenge(t, endpoint.header.Get(headerAuthenticate))
	if scheme != "Bearer" {
		t.Errorf("endpoint: the challenge scheme is %q, want %q", scheme, "Bearer")
	}
	if params["resource_metadata"] != testMetadataAddress {
		t.Errorf("endpoint: resource_metadata = %q, want %q", params["resource_metadata"], testMetadataAddress)
	}
	// The refusal says it is one and nothing more: no result, and no id, which
	// is read out of a body this server never parsed.
	if endpoint.frame.Error == nil {
		t.Errorf("the refusal carries no error envelope: %q", endpoint.raw)
	}
	if endpoint.frame.Result != nil {
		t.Errorf("the refusal carries a result, so something behind the challenge answered: %q", endpoint.raw)
	}
	if endpoint.frame.ID != nil {
		t.Errorf("the refusal names an id, so the body was read before the credential was: %q", endpoint.raw)
	}
}

// An origin is validated against the configured list.
func TestHTTPOriginIsValidatedAgainstTheConfiguredList(t *testing.T) {
	transport := defaultTransportConfig()
	transport.AllowedOrigins = []string{"https://listed.example"}
	f := newHTTPFixture(t, transport, nil)
	const body = `{"jsonrpc":"2.0","id":1,"method":"ping"}`

	t.Run("unlisted origin", func(t *testing.T) {
		got := f.post(t, map[string]string{headerOrigin: "https://unlisted.example"}, body)
		if got.status != http.StatusForbidden {
			t.Errorf("status = %d, want 403 (body %q)", got.status, got.raw)
		}
		if got.frame.Error == nil {
			t.Errorf("the refusal carried no error envelope: %q", got.raw)
		}
	})

	t.Run("listed origin", func(t *testing.T) {
		got := f.post(t, map[string]string{headerOrigin: "https://listed.example"}, body)
		if got.status != http.StatusOK {
			t.Errorf("status = %d, want 200 (body %q)", got.status, got.raw)
		}
	})

	t.Run("no origin header", func(t *testing.T) {
		got := f.post(t, nil, body)
		if got.status != http.StatusOK {
			t.Errorf("status = %d, want 200 (body %q)", got.status, got.raw)
		}
	})
}

// With no origins configured, a request carrying an origin is refused and one
// carrying none is served. That is the documented shape of an empty list rather
// than an accident of it, so it is pinned.
func TestHTTPEmptyOriginListAcceptsOnlyRequestsCarryingNoOrigin(t *testing.T) {
	f := newHTTPFixture(t, defaultTransportConfig(), nil)
	const body = `{"jsonrpc":"2.0","id":1,"method":"ping"}`

	refused := f.post(t, map[string]string{headerOrigin: "https://anything.example"}, body)
	if refused.status != http.StatusForbidden {
		t.Errorf("status with an origin = %d, want 403", refused.status)
	}
	served := f.post(t, nil, body)
	if served.status != http.StatusOK {
		t.Errorf("status without an origin = %d, want 200", served.status)
	}
}

// The body cap, enforced before the body is decoded.
//
// The control is the first row: a body under the cap on the same listener is
// served, so the refusal below is a fact about the size rather than about the
// listener refusing everything.
func TestHTTPBodyCapRefusesWhatItCannotAccept(t *testing.T) {
	transport := defaultTransportConfig()
	transport.MaxBodyBytes = 512
	f := newHTTPFixture(t, transport, nil)

	under := f.post(t, nil, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if under.status != http.StatusOK {
		t.Fatalf("a body under the cap was answered %d (body %q); the refusal below would "+
			"then prove nothing about the cap", under.status, under.raw)
	}

	oversized := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{"pad":%q}}`,
		strings.Repeat("x", 2000))
	if int64(len(oversized)) <= transport.MaxBodyBytes {
		t.Fatalf("the oversized fixture is %d bytes and the cap is %d; it does not exceed it",
			len(oversized), transport.MaxBodyBytes)
	}
	over := f.post(t, nil, oversized)
	if over.status != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413 (body %q)", over.status, over.raw)
	}
	if over.frame.Error == nil {
		t.Errorf("the refusal carried no error envelope: %q", over.raw)
	}
}

// heldCall is a seam that reports when it has been entered and then waits to be
// released, so a proof can hold a request inside the handler for as long as it
// needs the slot occupied.
func heldCall(entered chan<- struct{}, release <-chan struct{}) lintRunner {
	return func(ctx context.Context, _ lintRequest, _, _ io.Writer) error {
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	}
}

// callHeldToolBody is a tools/call on the tool whose seam the fixtures hold, in
// the handshake revision so it carries no mirror headers.
//
// The tool is one this server runs itself, and that is what makes it the right
// vehicle here rather than an incidental choice: a session reached over HTTP
// offers no tool that runs a command in a separate process, so a call naming one
// is refused before any seam is entered and would hold nothing at all. The name
// is a package constant, so a rename moves this with it instead of leaving a
// request no tool answers.
func callHeldToolBody(id int) string {
	return fmt.Sprintf(
		`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":{"entity":"held.yaml"}}}`,
		id, heldToolName)
}

// heldToolName is the tool the held-call proofs drive, found by the operation it
// answers rather than by a name spelled here. A tool's advertised name is set
// from the operation table, so spelling one would be a second place it lives and
// a rename would leave these proofs calling a tool that no longer exists.
var heldToolName = func() string {
	spec, registered := curatedSibling("lint")
	if !registered {
		panic("no tool answers the lint operation; the held-call proofs have no vehicle")
	}
	return spec.name
}()

// The concurrency cap, driven with a blocking seam.
//
// The cap is one, so the held request occupies the only slot. The slot is taken
// before the body is read, so by the time the tool reports it has been entered
// the slot is held and the second request meets a listener with nothing free —
// no timing assumption is made anywhere.
//
// A planted defect that acquires the slot with a blocking send instead of a
// non-blocking one turns the second request into a wait rather than a refusal,
// and this proof reaches its liveness ceiling instead of passing. The excess
// request is therefore issued on a goroutine and read back against that ceiling
// like every other request here: read inline, a defect that made it wait would
// hang the whole package and be reported by the runner's own timeout, with no
// assertion message naming what failed.
func TestHTTPConcurrencyCapRefusesTheExcess(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	// Released once, whichever path gets there first: the test releases it after
	// the refusal is read, and a failure before that point still has to release it
	// or the listener's shutdown would wait on a request nothing will finish.
	releaseHeld := sync.OnceFunc(func() { close(release) })
	defer releaseHeld()

	transport := defaultTransportConfig()
	transport.MaxConcurrentRequests = 1
	f := newHTTPFixture(t, transport, heldCall(entered, release))

	held := f.issue(callHeldToolBody(1))
	awaitEntered(t, entered, "the held request")

	excess := awaitExchange(t, f.issue(callHeldToolBody(2)), "the excess request")
	if excess.status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (body %q)", excess.status, excess.raw)
	}
	if excess.frame.Error == nil {
		t.Fatalf("the refusal carried no error envelope: %q", excess.raw)
	}
	if len(excess.frame.ID) != 0 && string(excess.frame.ID) != "null" {
		t.Errorf("the refusal carried an id %s; it is raised before any body is read",
			excess.frame.ID)
	}

	releaseHeld()
	// Control: the slot the excess request could not take was held by a request
	// that was itself served. Without this the assertion above would pass for a
	// listener refusing everything.
	if got := awaitExchange(t, held, "the held request"); got.status != http.StatusOK {
		t.Errorf("the held request was answered %d, want 200 (body %q)", got.status, got.raw)
	}
}

// Closing the connection during a call ends that call's context, and nothing
// further is written for it.
//
// Two things are observed, and both are needed. The seam records the error that
// ended its context, which is what shows the cancellation reached the work rather
// than only releasing the caller. And a recorder wrapped around the response
// writer shows the handler wrote nothing afterwards — an assertion the client
// side cannot make, because a caller that has hung up cannot tell a server that
// stayed silent from one that wrote into a closed connection.
//
// The control is the second half: the same listener, the same request, released
// instead of cancelled, where the recorder does see a write. Without it a
// recorder that never records anything would pass the first half.
//
// A planted defect that answers regardless of the request's context — dropping
// the two conditions guarding the write — fails the first half while leaving the
// control green.
func TestHTTPDisconnectEndsTheCallAndWritesNothing(t *testing.T) {
	t.Run("connection closed during the call", func(t *testing.T) {
		entered := make(chan struct{}, 1)
		ended := make(chan error, 1)
		run := func(ctx context.Context, _ lintRequest, _, _ io.Writer) error {
			entered <- struct{}{}
			<-ctx.Done()
			ended <- ctx.Err()
			return ctx.Err()
		}
		rec, ts := newRecordingHTTPListener(t, run)

		ctx, cancel := context.WithCancel(context.Background())
		requestDone := make(chan struct{})
		go func() {
			defer close(requestDone)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost,
				ts.URL+endpointPath, strings.NewReader(callHeldToolBody(1)))
			if err != nil {
				return
			}
			resp, err := ts.Client().Do(req)
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		}()

		select {
		case <-entered:
		case <-time.After(runReturnCeiling):
			t.Fatal("the request never reached the tool within the liveness ceiling")
		}
		cancel()

		select {
		case err := <-ended:
			if err == nil {
				t.Error("the seam recorded no context error, so the call ended for some other reason")
			}
		case <-time.After(runReturnCeiling):
			t.Fatal("the tool's context never ended within the liveness ceiling")
		}
		<-requestDone
		rec.waitUntilHandlerReturned(t)

		if status, wrote := rec.observed(); wrote {
			t.Errorf("the handler wrote a %d response for a request whose caller had gone", status)
		}
	})

	t.Run("control: the same call released rather than cancelled", func(t *testing.T) {
		entered := make(chan struct{}, 1)
		release := make(chan struct{})
		releaseHeld := sync.OnceFunc(func() { close(release) })
		defer releaseHeld()
		rec, ts := newRecordingHTTPListener(t, heldCall(entered, release))

		answered := make(chan int, 1)
		go func() {
			resp, err := ts.Client().Post(ts.URL+endpointPath, contentTypeJSON,
				strings.NewReader(callHeldToolBody(1)))
			if err != nil {
				answered <- 0
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			answered <- resp.StatusCode
		}()

		select {
		case <-entered:
		case <-time.After(runReturnCeiling):
			t.Fatal("the request never reached the tool within the liveness ceiling")
		}
		releaseHeld()

		select {
		case status := <-answered:
			if status != http.StatusOK {
				t.Errorf("the control was answered %d, want 200", status)
			}
		case <-time.After(runReturnCeiling):
			t.Fatal("the control never returned within the liveness ceiling")
		}
		rec.waitUntilHandlerReturned(t)

		if _, wrote := rec.observed(); !wrote {
			t.Error("the recorder saw no write for a request that WAS answered, so the " +
				"assertion in the cancelled case would pass for a recorder that records nothing")
		}
	})
}

// Ending the run reaches a call that is already in flight, and the cause it
// records says which of the two channels ended it.
//
// The cause is the assertion rather than a done context, because a done context
// proves nothing about what ended it: the connection closing ends the same
// context, and the two are answered differently. Reading the cause is the only
// thing that tells them apart.
//
// That is what establishes run end reaching an in-flight request here with no
// entry recorded by id anywhere: the watcher on the run's own context is the
// whole of the path, and this drives it.
//
// The control runs first, on the same listener and through the same seam: a call
// nobody interrupts is answered and records no cause at all. Without it the
// assertions below would pass for a listener that ends every call it accepts.
//
// A planted defect that drops the run watcher leaves the second half at its
// liveness ceiling — nothing ends the seam's context and the call never returns.
func TestHTTPRunEndReachesACallAlreadyInFlight(t *testing.T) {
	runCtx, endRun := context.WithCancel(context.Background())
	defer endRun()

	entered := make(chan struct{}, 2)
	ended := make(chan error, 2)
	// One token, taken by the control. The call after it finds the channel empty
	// and stays in the seam until something ends its context, which is the state
	// the run's end has to reach into.
	release := make(chan struct{}, 1)
	run := func(ctx context.Context, _ lintRequest, _, _ io.Writer) error {
		entered <- struct{}{}
		select {
		case <-release:
			ended <- nil
		case <-ctx.Done():
			ended <- context.Cause(ctx)
		}
		return nil
	}
	f := newHTTPFixtureUnderRun(t, runCtx, defaultTransportConfig(), run)

	control := f.issue(callHeldToolBody(1))
	awaitEntered(t, entered, "the control request")
	release <- struct{}{}
	if got := awaitExchange(t, control, "the control request"); got.status != http.StatusOK {
		t.Fatalf("the control was answered %d, want 200 (body %q); a listener answering "+
			"nothing would pass the assertions below", got.status, got.raw)
	}
	if cause := awaitCause(t, ended, "the control request"); cause != nil {
		t.Fatalf("the control's context ended with %v; it was released, not interrupted", cause)
	}

	held := f.issue(callHeldToolBody(2))
	awaitEntered(t, entered, "the held request")
	endRun()

	if cause := awaitCause(t, ended, "the held request"); !errors.Is(cause, errRunEnded) {
		t.Errorf("the seam recorded %v as the cause of its context ending; want the "+
			"run-end cause, which is what separates the run ending from a caller "+
			"that has gone", cause)
	}

	// The run ending is not a disconnect. The caller is still connected and still
	// reading, so the request that was in flight is still answered.
	if got := awaitExchange(t, held, "the held request"); got.status != http.StatusOK {
		t.Errorf("the held request was answered %d, want 200 (body %q); a run that ends "+
			"still answers the request it was answering", got.status, got.raw)
	}
}

// No request over this transport is ever recorded in the shared cancel registry.
//
// The count is read while a call is blocked inside the tool, which is the one
// moment an entry would exist if this transport recorded one. Read before or
// after, a zero would say nothing at all: a transport that tracked every request
// would report zero then too.
//
// The control is the synthetic entry below. A zero is a claim about the reader
// until something shows the reader can see a one — without it this would pass
// equally for a count taken from a registry this handler does not hold.
func TestHTTPRequestsAreNotRecordedInTheCancelRegistry(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	releaseHeld := sync.OnceFunc(func() { close(release) })
	defer releaseHeld()

	f := newHTTPFixture(t, defaultTransportConfig(), heldCall(entered, release))
	held := f.issue(callHeldToolBody(1))
	awaitEntered(t, entered, "the held request")

	if n := f.h.srv.cancels.Len(); n != 0 {
		t.Errorf("the registry holds %d entries while a call over this transport is in "+
			"flight; it records none, because the connection is what cancels a call here", n)
	}

	const synthetic = `"an-id-this-proof-tracked-itself"`
	f.h.srv.cancels.Track(json.RawMessage(synthetic), func() {})
	if n := f.h.srv.cancels.Len(); n != 1 {
		t.Fatalf("the registry reports %d after one entry was tracked in it, so the zero "+
			"above is a fact about the reader rather than about the transport", n)
	}
	f.h.srv.cancels.Untrack(json.RawMessage(synthetic))

	releaseHeld()
	if got := awaitExchange(t, held, "the held request"); got.status != http.StatusOK {
		t.Errorf("the held request was answered %d, want 200 (body %q); the count above was "+
			"read on the understanding that this request was in flight", got.status, got.raw)
	}
}

// A cancellation notification posted over this transport is accepted and ends
// nothing.
//
// It names the id of a request this listener is holding open, so a transport
// recording requests by id would end that request here. Two things say it did
// not, and they fail independently: the seam reports it left on its own release
// rather than on a cancelled context, and the request is answered with a body
// rather than with the empty one a suppressed write leaves behind.
//
// This is the no-op the protocol permits for an id nothing is working on, and it
// is what stands in place of an id-keyed cancellation on a transport where the
// connection is the cancellation channel.
//
// A planted defect that records this transport's requests in the registry fails
// the cause half and leaves the body half standing, which is measured rather
// than assumed: the write guard reads only whether the connection has gone, so a
// call ended by id is still answered. The body half is what would catch a defect
// that restored a cause-reading guard alongside the tracking, and it is kept for
// that reason rather than as a second reading of the first.
func TestHTTPCancellationNotificationEndsNothing(t *testing.T) {
	entered := make(chan struct{}, 1)
	ended := make(chan error, 1)
	release := make(chan struct{})
	releaseHeld := sync.OnceFunc(func() { close(release) })
	defer releaseHeld()

	run := func(ctx context.Context, _ lintRequest, _, _ io.Writer) error {
		entered <- struct{}{}
		select {
		case <-release:
			ended <- nil
		case <-ctx.Done():
			ended <- context.Cause(ctx)
		}
		return nil
	}
	f := newHTTPFixture(t, defaultTransportConfig(), run)

	held := f.issue(callHeldToolBody(1))
	awaitEntered(t, entered, "the held request")

	cancelled := f.post(t, nil,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`)
	if cancelled.status != http.StatusAccepted {
		t.Errorf("the cancellation was answered %d, want 202 (body %q)",
			cancelled.status, cancelled.raw)
	}
	if cancelled.raw != "" {
		t.Errorf("the cancellation was answered with a body: %q", cancelled.raw)
	}

	releaseHeld()
	if cause := awaitCause(t, ended, "the request the cancellation named"); cause != nil {
		t.Errorf("the named request's context ended with %v; the cancellation was supposed "+
			"to reach a registry holding nothing for it", cause)
	}
	got := awaitExchange(t, held, "the request the cancellation named")
	if got.status != http.StatusOK {
		t.Errorf("the request the cancellation named was answered %d, want 200 (body %q)",
			got.status, got.raw)
	}
	if got.frame.Error != nil {
		t.Errorf("the request the cancellation named was answered with an error: %+v",
			got.frame.Error)
	}
	if got.raw == "" {
		t.Error("the request the cancellation named was answered with an empty body, which " +
			"is what a suppressed write leaves behind")
	}
}

// writeRecorder reports whether the handler wrote anything through it. Only what
// the handler writes passes through here: what the HTTP server itself emits when
// a handler returns having written nothing goes to its own response object and is
// invisible to this, which is exactly the distinction the cancellation proof
// needs.
type writeRecorder struct {
	mu       sync.Mutex
	wrote    bool
	status   int
	returned chan struct{}
}

func (rec *writeRecorder) record(status int) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if !rec.wrote {
		rec.wrote, rec.status = true, status
	}
}

func (rec *writeRecorder) observed() (status int, wrote bool) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.status, rec.wrote
}

// waitUntilHandlerReturned removes the race between the handler finishing and
// this goroutine looking. It asserts nothing; the comparison that follows does.
func (rec *writeRecorder) waitUntilHandlerReturned(t *testing.T) {
	t.Helper()
	select {
	case <-rec.returned:
	case <-time.After(runReturnCeiling):
		t.Fatal("the handler never returned within the liveness ceiling")
	}
}

// recordingWriter forwards to the real response writer and tells the recorder
// what went through it.
type recordingWriter struct {
	http.ResponseWriter
	rec *writeRecorder
}

func (w *recordingWriter) WriteHeader(status int) {
	w.rec.record(status)
	w.ResponseWriter.WriteHeader(status)
}

func (w *recordingWriter) Write(b []byte) (int, error) {
	w.rec.record(http.StatusOK)
	return w.ResponseWriter.Write(b)
}

// newRecordingHTTPListener starts a listener whose handler writes through a
// recorder, for the proofs that turn on whether anything was written at all.
func newRecordingHTTPListener(t *testing.T, hold lintRunner) (*writeRecorder, *httptest.Server) {
	t.Helper()
	s := newCommandToolServer(t, nil)
	s.runLint = hold
	h, err := newHTTPHandler(context.Background(), s, defaultTransportConfig())
	if err != nil {
		t.Fatalf("newHTTPHandler: %v", err)
	}
	h.auth = acceptAllAuthenticator{}
	rec := &writeRecorder{returned: make(chan struct{})}
	// Closed once however many requests reach this listener. Each proof that uses
	// it issues one, and a transport that retried would otherwise end the run in a
	// panic rather than in the comparison being made.
	handlerReturned := sync.OnceFunc(func() { close(rec.returned) })
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer handlerReturned()
		h.ServeHTTP(&recordingWriter{ResponseWriter: w, rec: rec}, r)
	}))
	t.Cleanup(ts.Close)
	return rec, ts
}

// A cap the configuration surface cannot produce is refused at construction
// rather than clamped, because a listener that silently served one request at a
// time — or refused every request — would look like a working deployment.
//
// Every row declares the deployment, so the one thing wrong with it is its cap.
// A row that left that out would still be refused, and would stop covering the
// cap the moment it started being refused for the other reason.
func TestHTTPHandlerRefusesANonPositiveCap(t *testing.T) {
	declared := defaultAuthorizationConfig()
	for _, row := range []struct {
		name      string
		transport HTTPConfig
	}{
		{
			"no body cap",
			HTTPConfig{MaxBodyBytes: 0, MaxConcurrentRequests: 4, RequestsPerMinute: 60, Authorization: declared},
		},
		{
			"negative body cap",
			HTTPConfig{MaxBodyBytes: -1, MaxConcurrentRequests: 4, RequestsPerMinute: 60, Authorization: declared},
		},
		{
			"no concurrency cap",
			HTTPConfig{MaxBodyBytes: 1024, MaxConcurrentRequests: 0, RequestsPerMinute: 60, Authorization: declared},
		},
		{
			"negative concurrency cap",
			HTTPConfig{MaxBodyBytes: 1024, MaxConcurrentRequests: -1, RequestsPerMinute: 60, Authorization: declared},
		},
		{
			"no rate cap",
			HTTPConfig{MaxBodyBytes: 1024, MaxConcurrentRequests: 4, RequestsPerMinute: 0, Authorization: declared},
		},
		{
			"negative rate cap",
			HTTPConfig{MaxBodyBytes: 1024, MaxConcurrentRequests: 4, RequestsPerMinute: -1, Authorization: declared},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			s := newCommandToolServer(t, nil)
			if _, err := newHTTPHandler(context.Background(), s, row.transport); err == nil {
				t.Error("a handler was built over a cap the configuration surface cannot produce")
			}
		})
	}
}
