// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// The proofs here read what a serving process WROTE about the requests it
// answered, rather than what it answered them with. Every one of them drives a
// real listener with the authenticator the handler ships with, so the outcome a
// line reports is the outcome a caller actually received.

// metaKeyLogLevel is the per-request name the protocol defines for a client
// asking to be sent log notifications.
//
// It is spelled in this file and nowhere in the server, and that is the property
// under test rather than an oversight: this server reads the name for nothing
// and emits no notification whatever a request carries, so a constant for it in
// the production source would be a name something consults.
const metaKeyLogLevel = "io.modelcontextprotocol/logLevel"

// newAuditFixture starts a listener with the token validator installed and its
// error stream captured, and with the lint seam a proof can hold a call inside.
//
// It is the token fixture plus that seam: the outcomes under test include one
// that can only be reached while a call is still running, and the shipped
// authenticator has to stay installed because two of the outcomes are its.
func newAuditFixture(t *testing.T, transport HTTPConfig, hold lintRunner) *tokenFixture {
	t.Helper()
	log := &lockedBuffer{}
	s := newCommandToolServer(t, nil)
	s.logw = log
	s.runLint = hold
	h, err := newHTTPHandler(context.Background(), s, transport)
	if err != nil {
		t.Fatalf("newHTTPHandler: %v", err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return &tokenFixture{httpFixture: &httpFixture{ts: ts, h: h}, log: log}
}

// callTool posts one tools/call in the handshake revision, carrying the given
// token. The handshake revision is used because it asks for no mirror headers,
// and none of these proofs is about the headers.
func (f *tokenFixture) callTool(t *testing.T, token, tool string, arguments map[string]any) answer {
	t.Helper()
	params := map[string]any{"name": tool}
	if arguments != nil {
		params["arguments"] = arguments
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params,
	})
	if err != nil {
		t.Fatalf("compose tools/call: %v", err)
	}
	headers := map[string]string{}
	if token != "" {
		headers[headerAuthorization] = bearer(token)
	}
	return f.send(t, http.MethodPost, endpointPath, headers, string(body))
}

// callMethod posts one bare request in the handshake revision, carrying the
// given token and the given params verbatim.
func (f *tokenFixture) callMethod(t *testing.T, token, method string, params map[string]any) answer {
	t.Helper()
	body := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		body["params"] = params
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("compose %s: %v", method, err)
	}
	headers := map[string]string{}
	if token != "" {
		headers[headerAuthorization] = bearer(token)
	}
	return f.send(t, http.MethodPost, endpointPath, headers, string(encoded))
}

// oneAuditLine parses what the serving process wrote during one request and
// insists it was exactly one line that is a JSON object.
//
// The count is asserted here rather than beside each proof, because "one line
// per request" is the property every one of them rests on: a proof reading the
// first line of two would pass while the stream carried a duplicate of every
// record in it.
func oneAuditLine(t *testing.T, written string) map[string]json.RawMessage {
	t.Helper()
	lines := auditLines(t, written)
	if len(lines) != 1 {
		t.Fatalf("one request wrote %d lines, want 1:\n%s", len(lines), written)
	}
	return lines[0]
}

// auditLines parses every line written, and fails on any that is not a JSON
// object — which is what a prose line reaching a collected stream looks like.
func auditLines(t *testing.T, written string) []map[string]json.RawMessage {
	t.Helper()
	trimmed := strings.TrimSuffix(written, "\n")
	if trimmed == "" {
		return nil
	}
	raw := strings.Split(trimmed, "\n")
	out := make([]map[string]json.RawMessage, 0, len(raw))
	for _, line := range raw {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &fields); err != nil {
			t.Fatalf("a line on the error stream does not parse as a JSON object: %q: %v", line, err)
		}
		out = append(out, fields)
	}
	return out
}

// auditString reads one field as a string, reporting an absent field and an
// empty one as the different things they are.
func auditString(t *testing.T, line map[string]json.RawMessage, key string) string {
	t.Helper()
	raw, present := line[key]
	if !present {
		t.Fatalf("the line carries no %q key: %v", key, keysOf(line))
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("the %q key is not a string: %s", key, raw)
	}
	return value
}

// keysOf is the line's key set, sorted so two reports of one difference read the
// same way.
func keysOf(line map[string]json.RawMessage) []string {
	out := make([]string, 0, len(line))
	for key := range line {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}

// wantExactKeys asserts a line's key set in BOTH directions, naming what is
// missing and what is unexpected.
//
// Both directions are the point. A proof asserting only that the keys it expects
// are present passes unchanged the day a key is added, which is exactly the
// change a record of who did what must not absorb quietly: a field appearing in
// a collected stream with nobody having decided to publish it is the same class
// of event as one disappearing.
func wantExactKeys(t *testing.T, line map[string]json.RawMessage, want []string) {
	t.Helper()
	got := keysOf(line)
	if missing := missingFrom(want, got); len(missing) > 0 {
		t.Errorf("the line is missing %v\n got: %v\nwant: %v", missing, got, want)
	}
	if unexpected := missingFrom(got, want); len(unexpected) > 0 {
		t.Errorf("the line carries %v, which nothing decided to publish\n got: %v\nwant: %v",
			unexpected, got, want)
	}
}

// A request this deployment authenticated leaves exactly one line, that line is
// a JSON object, and its key set is pinned in both directions.
//
// The key set is the assertion clause by clause: adding a field to the record or
// removing one from it reddens here, which is what makes the set something
// somebody decided rather than whatever the struct happened to carry. The values
// are read beside it, because a line carrying every key and nothing in them
// would satisfy a key-set assertion perfectly.
//
// Planted defect: remove any field from the encoded record, or add one, and this
// fails naming it.
func TestOneAuthenticatedRequestLeavesOneLineWithAFixedKeySet(t *testing.T) {
	issuer := newAuthorizationServerDouble(t)
	f := newAuditFixture(t, transportFor(issuer.authorizationConfig(), issuer.egress()), nil)

	from := f.log.mark()
	token := issuer.mint(t, tokenClaimSet{scope: scopeRead, clientID: "a-client"})
	got := f.callTool(t, token, describeCapabilitiesName, nil)
	if got.status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", got.status, got.raw)
	}

	line := oneAuditLine(t, f.log.since(from))
	wantExactKeys(t, line, []string{
		"time", "request_id", "transport", "workspace_root",
		"subject", "issuer", "client_id",
		"method", "target", "reach", "outcome", "duration_ms",
	})

	if got := auditString(t, line, "transport"); got != transportNameHTTP {
		t.Errorf("transport = %q, want %q", got, transportNameHTTP)
	}
	if got := auditString(t, line, "subject"); got != testSubject {
		t.Errorf("subject = %q, want %q", got, testSubject)
	}
	if got := auditString(t, line, "issuer"); got != issuer.issuer() {
		t.Errorf("issuer = %q, want %q", got, issuer.issuer())
	}
	if got := auditString(t, line, "client_id"); got != "a-client" {
		t.Errorf("client_id = %q, want %q", got, "a-client")
	}
	if got := auditString(t, line, "method"); got != "tools/call" {
		t.Errorf("method = %q, want %q", got, "tools/call")
	}
	if got := auditString(t, line, "target"); got != describeCapabilitiesName {
		t.Errorf("target = %q, want %q", got, describeCapabilitiesName)
	}
	if got := auditString(t, line, "outcome"); got != outcomeOK {
		t.Errorf("outcome = %q, want %q", got, outcomeOK)
	}
	if got := auditString(t, line, "request_id"); got == "" {
		t.Error("the line carries an empty request identifier")
	}
	if _, err := time.Parse(auditTimeFormat, auditString(t, line, "time")); err != nil {
		t.Errorf("the time does not parse: %v", err)
	}

	// Two requests are recorded under two identifiers. One identifier for two
	// requests would satisfy every assertion above and make the whole record
	// unusable for following a single call.
	second := f.log.mark()
	f.callTool(t, token, describeCapabilitiesName, nil)
	if first, next := auditString(t, line, "request_id"),
		auditString(t, oneAuditLine(t, f.log.since(second)), "request_id"); first == next {
		t.Errorf("two requests were recorded under one identifier %q", first)
	}
}

// Every way a request can end carries its own outcome, and no two of them agree.
//
// The rows are driven against one listener and read one at a time, so each reads
// only the line its own request produced. They are asserted as a SET at the end
// rather than only row by row: each row passing its own expectation would still
// leave two outcomes spelled the same way, and a stream in which a refusal and a
// success are indistinguishable is one nothing can be counted from.
//
// Planted defect: give any two of the rows the same outcome value and the
// distinctness check names the pair.
func TestEveryOutcomeIsDistinguishable(t *testing.T) {
	issuer := newAuthorizationServerDouble(t)

	// The held tool is the vehicle for the row that can only be reached while a
	// call is still running. It releases as soon as the call's context ends.
	entered := make(chan struct{}, 1)
	hold := func(ctx context.Context, _ lintRequest, _, _ io.Writer) error {
		entered <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}

	capped := transportFor(issuer.authorizationConfig(), issuer.egress())
	capped.RequestsPerMinute = 1
	ordinary := transportFor(issuer.authorizationConfig(), issuer.egress())

	readOnly := issuer.mint(t, tokenClaimSet{scope: scopeRead})
	writer := issuer.mint(t, tokenClaimSet{scope: scopeRead + " " + scopeWorkspaceWrite})

	seen := map[string]string{}
	record := func(t *testing.T, row, outcome string) {
		t.Helper()
		if other, clash := seen[outcome]; clash {
			t.Errorf("the %q row and the %q row both report the outcome %q, so the two are "+
				"indistinguishable on the stream", row, other, outcome)
		}
		seen[outcome] = row
	}

	read := func(t *testing.T, f *tokenFixture, from int, row string) string {
		t.Helper()
		outcome := auditString(t, oneAuditLine(t, f.log.since(from)), "outcome")
		record(t, row, outcome)
		return outcome
	}

	t.Run("a call that ran and succeeded", func(t *testing.T) {
		f := newAuditFixture(t, ordinary, nil)
		from := f.log.mark()
		if got := f.callTool(t, readOnly, describeCapabilitiesName, nil); got.status != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", got.status, got.raw)
		}
		if got := read(t, f, from, "success"); got != outcomeOK {
			t.Errorf("outcome = %q, want %q", got, outcomeOK)
		}
	})

	t.Run("a tool result flagged as a failure", func(t *testing.T) {
		f := newAuditFixture(t, ordinary, nil)
		from := f.log.mark()
		// The tool runs and refuses its own arguments, which is a served call
		// whose result reports a failure rather than a refusal of the request.
		got := f.callTool(t, writer, "build", map[string]any{"release": ""})
		if got.status != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", got.status, got.raw)
		}
		if outcome := read(t, f, from, "tool failure"); outcome != outcomeToolFailure {
			t.Errorf("outcome = %q, want %q (body %q)", outcome, outcomeToolFailure, got.raw)
		}
	})

	t.Run("a JSON-RPC error envelope", func(t *testing.T) {
		f := newAuditFixture(t, ordinary, nil)
		from := f.log.mark()
		got := f.callMethod(t, readOnly, "no/such/method", nil)
		if got.frame.Error == nil {
			t.Fatalf("no error envelope in %q", got.raw)
		}
		if outcome := read(t, f, from, "error envelope"); outcome != outcomeError {
			t.Errorf("outcome = %q, want %q", outcome, outcomeError)
		}
	})

	t.Run("a credential this deployment would not accept", func(t *testing.T) {
		f := newAuditFixture(t, ordinary, nil)
		from := f.log.mark()
		expired := issuer.mint(t, tokenClaimSet{expiry: time.Now().Add(-time.Hour)})
		if got := f.callTool(t, expired, describeCapabilitiesName, nil); got.status != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 (body %q)", got.status, got.raw)
		}
		line := oneAuditLine(t, f.log.since(from))
		outcome := auditString(t, line, "outcome")
		record(t, "401", outcome)
		if outcome != outcomeUnauthenticated {
			t.Errorf("outcome = %q, want %q", outcome, outcomeUnauthenticated)
		}
		// The check that refused it reaches the line, which is where it now goes
		// rather than onto a line of its own.
		if got := auditString(t, line, "refusal"); got != string(reasonExpired) {
			t.Errorf("refusal = %q, want %q", got, string(reasonExpired))
		}
	})

	t.Run("a credential that did not admit what was asked for", func(t *testing.T) {
		f := newAuditFixture(t, ordinary, nil)
		from := f.log.mark()
		// A read-only credential admits the build tool, which declares no reach,
		// and does not admit the fill this call's own argument escalates it to.
		got := f.callTool(t, readOnly, "build",
			map[string]any{"release": "a.yaml", "auto_pull": true})
		if got.status != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body %q)", got.status, got.raw)
		}
		line := oneAuditLine(t, f.log.since(from))
		outcome := auditString(t, line, "outcome")
		record(t, "403", outcome)
		if outcome != outcomeForbidden {
			t.Errorf("outcome = %q, want %q", outcome, outcomeForbidden)
		}
	})

	t.Run("a caller that sent more than the deployment accepts", func(t *testing.T) {
		f := newAuditFixture(t, capped, nil)
		f.callTool(t, readOnly, describeCapabilitiesName, nil)
		from := f.log.mark()
		if got := f.callTool(t, readOnly, describeCapabilitiesName, nil); got.status != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429 (body %q)", got.status, got.raw)
		}
		if outcome := read(t, f, from, "429"); outcome != outcomeRateCapped {
			t.Errorf("outcome = %q, want %q", outcome, outcomeRateCapped)
		}
	})

	t.Run("a caller that closed its connection during the call", func(t *testing.T) {
		f := newAuditFixture(t, ordinary, hold)
		from := f.log.mark()

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost,
				f.ts.URL+endpointPath, strings.NewReader(callHeldToolBody(1)))
			if err != nil {
				return
			}
			req.Header.Set(headerAuthorization, bearer(readOnly))
			resp, err := f.ts.Client().Do(req)
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
		<-done

		// The handler records its line as it returns, which is after the client
		// goroutine has already seen its connection close, so the line is waited
		// for rather than assumed present.
		var outcome string
		deadline := time.Now().Add(runReturnCeiling)
		for time.Now().Before(deadline) {
			if written := f.log.since(from); strings.Contains(written, `"outcome"`) {
				outcome = auditString(t, oneAuditLine(t, written), "outcome")
				break
			}
			time.Sleep(time.Millisecond)
		}
		record(t, "cancelled", outcome)
		if outcome != outcomeCancelled {
			t.Errorf("outcome = %q, want %q", outcome, outcomeCancelled)
		}
	})
}

// The class an operation declares and the class a call escalated to both reach
// the line, and a call that escalated nothing carries no escalation key.
//
// The pair is the point. A record carrying only the declared class would say a
// read-scoped build reached nothing, when what it was refused for was the fill
// its own argument asked for; and a record carrying the escalation on every line
// would report an escalation for every call that never had one.
func TestTheDeclaredAndEscalatedReachBothReachTheLine(t *testing.T) {
	issuer := newAuthorizationServerDouble(t)
	f := newAuditFixture(t, transportFor(issuer.authorizationConfig(), issuer.egress()), nil)
	readOnly := issuer.mint(t, tokenClaimSet{scope: scopeRead})

	t.Run("a call that escalated", func(t *testing.T) {
		from := f.log.mark()
		f.callTool(t, readOnly, "build", map[string]any{"release": "a.yaml", "auto_pull": true})
		line := oneAuditLine(t, f.log.since(from))
		if got := auditString(t, line, "reach"); got != string(opsurface.ReachNone) {
			t.Errorf("reach = %q, want %q", got, opsurface.ReachNone)
		}
		if got := auditString(t, line, "reach_escalated_to"); got != string(opsurface.ReachWorkspace) {
			t.Errorf("reach_escalated_to = %q, want %q", got, opsurface.ReachWorkspace)
		}
	})

	t.Run("a call sent write escalated", func(t *testing.T) {
		from := f.log.mark()
		f.callTool(t, readOnly, "build", map[string]any{"release": "a.yaml", "write": true})
		line := oneAuditLine(t, f.log.since(from))
		if got := auditString(t, line, "reach"); got != string(opsurface.ReachNone) {
			t.Errorf("reach = %q, want %q", got, opsurface.ReachNone)
		}
		if got := auditString(t, line, "reach_escalated_to"); got != string(opsurface.ReachWorkspace) {
			t.Errorf("reach_escalated_to = %q, want %q", got, opsurface.ReachWorkspace)
		}
	})

	t.Run("a call that did not escalate carries no escalation", func(t *testing.T) {
		from := f.log.mark()
		f.callTool(t, readOnly, describeCapabilitiesName, nil)
		line := oneAuditLine(t, f.log.since(from))
		if got := auditString(t, line, "reach"); got != string(opsurface.ReachNone) {
			t.Errorf("reach = %q, want %q", got, opsurface.ReachNone)
		}
		if _, present := line["reach_escalated_to"]; present {
			t.Errorf("a call that reached exactly what it declares reports an escalation: %v", keysOf(line))
		}
	})
}

// No token, no part of one, and no authorization header a caller sent reaches
// any line — and the instrument that says so is shown to fire before its silence
// is read as absence.
//
// The needle is the whole of the proof's standing. A search for a token in a
// stream that never carried one passes whatever the server does, so every one of
// these searches is first run against a line the proof has deliberately made
// carry what it looks for, and only then against the lines the server wrote.
//
// What is searched for is the credential and the header that carried it, not the
// word "authorization": the sentences naming the check that refused a token say
// which header they are about, in prose, and that is the diagnosis an operator
// is owed rather than a leak.
//
// The bound, stated because it is not obvious: where a deployment reads its
// caller from a proxy in front, the principal header's VALUE is that caller's
// subject and is recorded as one — clause by clause the same field a token's
// subject claim fills. That is the deployment having declared the header
// trustworthy, not a credential reaching a log, and nothing here asserts
// otherwise.
func TestNoTokenReachesAnyLine(t *testing.T) {
	issuer := newAuthorizationServerDouble(t)
	f := newAuditFixture(t, transportFor(issuer.authorizationConfig(), issuer.egress()), nil)

	token := issuer.mint(t, tokenClaimSet{scope: scopeRead})
	wrongAudience := issuer.mint(t, tokenClaimSet{audience: []string{"https://elsewhere.example.test/mcp"}})
	presented := []string{bearer(token), "Basic " + token}

	secrets := credentialFragments(token, wrongAudience)
	secrets = append(secrets, presented...)

	// The needle: the same searches, run against a line carrying what they look
	// for. Every one must fire, or the searches below prove nothing.
	planted := fmt.Sprintf(`{"refusal":%q,"detail":%q,"note":%q}`,
		strings.Join(secrets, "|"), bearer(token), wrongAudience)
	for _, secret := range secrets {
		if !strings.Contains(planted, secret) {
			t.Fatalf("the needle does not fire: a planted line does not carry %q", secret)
		}
	}

	const body = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	from := f.log.mark()
	// Every way a credential reaches this server, in one stream: accepted,
	// refused on its claims, refused on how it was presented, and sent in the
	// address rather than on the header.
	f.callTool(t, token, describeCapabilitiesName, nil)
	f.callTool(t, wrongAudience, describeCapabilitiesName, nil)
	f.send(t, http.MethodPost, endpointPath, map[string]string{headerAuthorization: presented[1]}, body)
	f.send(t, http.MethodPost, endpointPath+"?"+queryTokenParameter+"="+token, nil, body)

	written := f.log.since(from)
	if len(auditLines(t, written)) != 4 {
		t.Fatalf("four requests wrote %d lines, so this proof is not reading what it thinks:\n%s",
			len(auditLines(t, written)), written)
	}
	for _, secret := range secrets {
		if strings.Contains(written, secret) {
			t.Errorf("the error stream carries %q:\n%s", secret, written)
		}
	}
}

// credentialFragments is everything a caller presented, in every piece a reader
// could recognise it by. A token is three dot-separated parts, and a stream
// carrying any one of them has leaked a credential as surely as one carrying the
// whole.
func credentialFragments(tokens ...string) []string {
	var out []string
	for _, token := range tokens {
		out = append(out, token)
		for _, part := range strings.Split(token, ".") {
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// A caller that presented nothing leaves no line, and that silence is the
// difference between being challenged and being refused.
//
// Being challenged is how a client discovers where to obtain a token, so it is
// the first exchange every well-behaved client has with this deployment. The
// control beside it is a caller that presented something and was refused, which
// does leave a line: without it a server that wrote nothing at all would pass
// the first half.
func TestACallerThatPresentedNothingLeavesNoLine(t *testing.T) {
	issuer := newAuthorizationServerDouble(t)
	f := newAuditFixture(t, transportFor(issuer.authorizationConfig(), issuer.egress()), nil)

	from := f.log.mark()
	if got := f.callTool(t, "", describeCapabilitiesName, nil); got.status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body %q)", got.status, got.raw)
	}
	if written := f.log.since(from); written != "" {
		t.Errorf("a caller that presented nothing wrote %q", written)
	}

	control := f.log.mark()
	refused := issuer.mint(t, tokenClaimSet{expiry: time.Now().Add(-time.Hour)})
	if got := f.callTool(t, refused, describeCapabilitiesName, nil); got.status != http.StatusUnauthorized {
		t.Fatalf("control: status = %d, want 401 (body %q)", got.status, got.raw)
	}
	if written := f.log.since(control); written == "" {
		t.Error("a caller whose credential was refused wrote nothing, so the silence above is not a decision")
	}
}

// A path this deployment does not serve, and a method this endpoint does not
// take, leave no line at all.
//
// Both are settled before anything about a caller is read, so neither is a
// request to the protocol surface — and recording them would let anything that
// can reach the address drive an unbounded write to whatever collects the
// stream, which is the thing the challenge path above is careful not to do.
func TestARequestThatNeverReachedTheProtocolSurfaceLeavesNoLine(t *testing.T) {
	issuer := newAuthorizationServerDouble(t)
	f := newAuditFixture(t, transportFor(issuer.authorizationConfig(), issuer.egress()), nil)

	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{"a path this deployment does not serve", http.MethodPost, "/somewhere-else"},
		{"a method this endpoint does not take", http.MethodGet, endpointPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			from := f.log.mark()
			f.send(t, tc.method, tc.path, nil, "")
			if written := f.log.since(from); written != "" {
				t.Errorf("wrote %q", written)
			}
		})
	}
}

// A trace context a caller sends is copied into the line exactly as it was sent,
// and a request that sends none, or sends one that does not parse, produces a
// line without the key.
//
// Verbatim is the requirement rather than a preference: the value exists so a
// collector can join this stream to the caller's own, and a value altered on the
// way through joins the two records to nothing. A value that does not parse is
// omitted rather than copied, because a malformed identifier recorded as this
// request's trace context would attribute the request to a trace it was never
// part of.
func TestTheTraceContextIsCopiedVerbatimOrOmitted(t *testing.T) {
	issuer := newAuthorizationServerDouble(t)
	f := newAuditFixture(t, transportFor(issuer.authorizationConfig(), issuer.egress()), nil)
	token := issuer.mint(t, tokenClaimSet{scope: scopeRead})

	const wellFormed = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	for _, tc := range []struct {
		name string
		meta map[string]any
		want map[string]string
		// absent names the keys the line must NOT carry.
		absent []string
	}{
		{
			name:   "no trace context at all",
			meta:   nil,
			absent: []string{"traceparent", "tracestate", "baggage"},
		},
		{
			name: "a well-formed parent alone",
			meta: map[string]any{metaKeyTraceParent: wellFormed},
			want: map[string]string{"traceparent": wellFormed},
			// The two list members are absent because the caller sent neither,
			// which is a different statement from having sent an empty one.
			absent: []string{"tracestate", "baggage"},
		},
		{
			name: "a parent with both list members",
			meta: map[string]any{
				metaKeyTraceParent: wellFormed,
				metaKeyTraceState:  "vendor=t61rcWkgMzE,other=abc",
				metaKeyBaggage:     "userId=alice,serverNode=DF28",
			},
			want: map[string]string{
				"traceparent": wellFormed,
				"tracestate":  "vendor=t61rcWkgMzE,other=abc",
				"baggage":     "userId=alice,serverNode=DF28",
			},
		},
		{
			name:   "a parent that is not the right length",
			meta:   map[string]any{metaKeyTraceParent: "00-4bf92f3577b34da6-00f067aa0ba902b7-01"},
			absent: []string{"traceparent"},
		},
		{
			name:   "a parent carrying something that is not hexadecimal",
			meta:   map[string]any{metaKeyTraceParent: "00-4bf92f3577b34da6a3ce929d0e0e473g-00f067aa0ba902b7-01"},
			absent: []string{"traceparent"},
		},
		{
			name:   "a parent whose trace identifier is the absent one",
			meta:   map[string]any{metaKeyTraceParent: "00-00000000000000000000000000000000-00f067aa0ba902b7-01"},
			absent: []string{"traceparent"},
		},
		{
			name:   "a parent whose parent identifier is the absent one",
			meta:   map[string]any{metaKeyTraceParent: "00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01"},
			absent: []string{"traceparent"},
		},
		{
			name:   "the reserved version",
			meta:   map[string]any{metaKeyTraceParent: "ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"},
			absent: []string{"traceparent"},
		},
		{
			name:   "a parent that is not a string",
			meta:   map[string]any{metaKeyTraceParent: 17},
			absent: []string{"traceparent"},
		},
		{
			name: "list members that are not member lists are dropped, and the parent stays",
			meta: map[string]any{
				metaKeyTraceParent: wellFormed,
				metaKeyTraceState:  "not-a-member-list",
				metaKeyBaggage:     "",
			},
			want:   map[string]string{"traceparent": wellFormed},
			absent: []string{"tracestate", "baggage"},
		},
		{
			name: "list members without a parent are not copied on their own",
			meta: map[string]any{
				metaKeyTraceState: "vendor=t61rcWkgMzE",
				metaKeyBaggage:    "userId=alice",
			},
			absent: []string{"traceparent", "tracestate", "baggage"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]any{"name": describeCapabilitiesName}
			if tc.meta != nil {
				params["_meta"] = tc.meta
			}
			from := f.log.mark()
			body, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params,
			})
			if err != nil {
				t.Fatalf("compose request: %v", err)
			}
			got := f.send(t, http.MethodPost, endpointPath,
				map[string]string{headerAuthorization: bearer(token)}, string(body))
			if got.status != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %q)", got.status, got.raw)
			}

			line := oneAuditLine(t, f.log.since(from))
			for key, want := range tc.want {
				if got := auditString(t, line, key); got != want {
					t.Errorf("%s = %q, want %q verbatim", key, got, want)
				}
			}
			for _, key := range tc.absent {
				if _, present := line[key]; present {
					t.Errorf("the line carries %q, which this request did not send in a form to copy: %v",
						key, keysOf(line))
				}
			}
		})
	}
}

// The serving process records its own life on the same stream, each line naming
// the event instead of a caller.
//
// A launch that serves and then stops is driven end to end, because the events
// are the steps of one life rather than five independent writes: a proof that
// called each writer directly would pass on a launch that never reached three of
// them.
func TestTheServingProcessRecordsItsOwnLifecycle(t *testing.T) {
	t.Run("a launch that binds, serves and stops", func(t *testing.T) {
		log := &lockedBuffer{}
		// The established furnished root: it anchors a workspace, so the launch
		// passes the question it refuses an unfurnished directory at, and it
		// declares the workspace offline, so the documentation acquisition this
		// launch performs reaches no registry.
		root := rootedWorkspace(t)

		runCtx, endRun := context.WithCancel(context.Background())
		served := make(chan error, 1)
		go func() {
			served <- listenAndServe(runCtx,
				Config{Stderr: log, WorkspaceRoot: root},
				defaultTransportConfig(),
				ListenConfig{Address: "127.0.0.1:0"}, nil)
		}()

		// The listening line is what says the endpoint is up; waiting for it is
		// what makes the stop below happen to a process that was serving.
		waitForEvent(t, log, eventListening)
		endRun()
		select {
		case err := <-served:
			if err != nil {
				t.Fatalf("the run ended with %v", err)
			}
		case <-time.After(runReturnCeiling):
			t.Fatal("the run never ended within the liveness ceiling")
		}

		events := eventsWritten(t, log.String())
		for _, want := range []string{eventStartup, eventListening, eventShutdown} {
			if !slices.Contains(events, want) {
				t.Errorf("the stream carries no %q line; it carries %v", want, events)
			}
		}
		// The bound address reaches the line, which is the one thing a
		// deployment that asked for any free port cannot learn anywhere else.
		for _, line := range auditLines(t, log.String()) {
			if auditString(t, line, "event") != eventListening {
				continue
			}
			if got := auditString(t, line, "address"); got == "" || strings.HasSuffix(got, ":0") {
				t.Errorf("the listening line reports the address %q rather than the one that was bound", got)
			}
		}
	})

	t.Run("a launch refused before it bound", func(t *testing.T) {
		log := &lockedBuffer{}
		// A plaintext listener on an address the network reaches is refused
		// before anything binds, which is one of the refusals under test.
		err := listenAndServe(context.Background(),
			Config{Stderr: log, WorkspaceRoot: t.TempDir()},
			defaultTransportConfig(),
			ListenConfig{Address: "0.0.0.0:0"}, nil)
		if err == nil {
			t.Fatal("the launch was not refused")
		}

		events := eventsWritten(t, log.String())
		if !slices.Contains(events, eventStartupRefused) {
			t.Fatalf("a refused launch wrote no %q line; it wrote %v", eventStartupRefused, events)
		}
		if slices.Contains(events, eventListening) {
			t.Errorf("a refused launch reported that it was listening: %v", events)
		}
		// The reason reaches the line, because a refusal an operator cannot read
		// leaves them with a process that exited and nothing saying why.
		for _, line := range auditLines(t, log.String()) {
			if auditString(t, line, "event") != eventStartupRefused {
				continue
			}
			if got := auditString(t, line, "detail"); !strings.Contains(got, "plaintext") {
				t.Errorf("the refusal line does not carry the reason: %q", got)
			}
		}
	})
}

// eventsWritten is the event of every line on the stream, in order. It fails on
// a line that is not a JSON object, which is what a prose line reaching a
// collected stream looks like.
func eventsWritten(t *testing.T, written string) []string {
	t.Helper()
	lines := auditLines(t, written)
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, auditString(t, line, "event"))
	}
	return out
}

// waitForEvent blocks until the named event appears on the stream, within the
// liveness ceiling. The ceiling separates "arrived" from "never will" and is the
// only thing a clock decides here.
func waitForEvent(t *testing.T, log *lockedBuffer, event string) {
	t.Helper()
	deadline := time.Now().Add(runReturnCeiling)
	for time.Now().Before(deadline) {
		if strings.Contains(log.String(), `"event":"`+event+`"`) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("the %q line never appeared within the liveness ceiling; the stream carries:\n%s",
		event, log.String())
}

// proseLines is every line a stream carries, with the framing asserted: each
// line the server writes ends in a newline, so a stream that does not end in one
// carries a line the server did not finish.
func proseLines(t *testing.T, written string) []string {
	t.Helper()
	if written == "" {
		return nil
	}
	if !strings.HasSuffix(written, "\n") {
		t.Fatalf("the stream does not end in a newline, so it carries an unfinished line: %q", written)
	}
	return strings.Split(strings.TrimSuffix(written, "\n"), "\n")
}

// A process speaking its own streams writes the prose it has always written,
// byte for byte, and writes no machine-readable line at all.
//
// The whole stream is compared rather than searched, which is what makes this a
// non-regression proof rather than a presence check: a search for each sentence
// would pass on a run that also wrote a structured line beside it, and the
// structured line is exactly what must not appear here. The comparison is exact
// in both directions and line by line — a sentence the server stopped writing
// fails it, a sentence it writes twice fails it, and a line it writes as well as
// these fails it, each line compared byte for byte.
//
// The ORDER of the three is deliberately not asserted, and must not be asserted
// later: each sentence below is written by a different message, a request is
// answered on its own goroutine, and a notification is handled on the loop's, so
// which of the three reaches the stream first is whatever the scheduler chose on
// that run. Sequence across separate messages is not something this server
// promises, and a comparison that pins it fails on runs where nothing about the
// prose has changed.
//
// Planted defect: change one character of any sentence below, delete one, or
// make a stdio session write a structured line, and the comparison names the
// difference.
func TestStdioWritesTheProseItAlwaysHasAndNothingElse(t *testing.T) {
	log := &lockedBuffer{}
	s := newCommandToolServer(t, nil)
	s.logw = log

	messages := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"a-client","version":"1.2.3"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"initialize","params":"not an object"}`,
		`{"jsonrpc":"2.0","method":"notifications/nothing-this-server-knows"}`,
	}, "\n") + "\n"

	if err := s.serve(context.Background(), strings.NewReader(messages)); err != nil {
		t.Fatalf("serve: %v", err)
	}

	want := []string{
		`mcp: initialize from client "a-client" version "1.2.3" (protocol "2025-06-18")`,
		`mcp: initialize params parse error: json: cannot unmarshal string into Go value of type mcp.initializeParams`,
		`mcp: ignoring unrecognized notification "notifications/nothing-this-server-knows"`,
	}

	got := proseLines(t, log.String())
	inAnyOrder := slices.Sorted(slices.Values(want))
	if !slices.Equal(slices.Sorted(slices.Values(got)), inAnyOrder) {
		t.Errorf("the stdio error stream is not what it was.\n got: %q\nwant, in any order: %q", got, want)
	}
	if s.structuredLog() {
		t.Error("a session speaking the process's own streams reports that its lines are machine-readable")
	}
}

// The server sends no message notification on either transport, including to a
// request that asked to be sent them.
//
// The negative is driven rather than asserted: a request carrying the per-request
// log-level name is sent on both transports, and everything the server wrote to
// the client is read back and searched for a notification. A source census sits
// beside it, because the behavioural half can only speak for the requests it
// sent and the census speaks for every request there could be.
func TestNoMessageNotificationOnEitherTransport(t *testing.T) {
	const notificationMethod = "notifications/message"

	t.Run("stdio, with the log-level name in the request metadata", func(t *testing.T) {
		out := &lockedBuffer{}
		s := newCommandToolServer(t, nil)
		s.out = out

		body, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/list",
			"params": map[string]any{"_meta": map[string]any{metaKeyLogLevel: "debug"}},
		})
		if err != nil {
			t.Fatalf("compose request: %v", err)
		}
		if err := s.serve(context.Background(), strings.NewReader(string(body)+"\n")); err != nil {
			t.Fatalf("serve: %v", err)
		}

		framed := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
		if len(framed) != 1 {
			t.Fatalf("one request produced %d messages on the protocol stream:\n%s", len(framed), out.String())
		}
		wantNoNotification(t, framed[0], notificationMethod)
	})

	t.Run("http, with the log-level name in the request metadata", func(t *testing.T) {
		issuer := newAuthorizationServerDouble(t)
		f := newAuditFixture(t, transportFor(issuer.authorizationConfig(), issuer.egress()), nil)
		token := issuer.mint(t, tokenClaimSet{scope: scopeRead})

		body, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/list",
			"params": map[string]any{"_meta": map[string]any{metaKeyLogLevel: "debug"}},
		})
		if err != nil {
			t.Fatalf("compose request: %v", err)
		}
		got := f.send(t, http.MethodPost, endpointPath,
			map[string]string{headerAuthorization: bearer(token)}, string(body))
		if got.status != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", got.status, got.raw)
		}
		wantNoNotification(t, got.raw, notificationMethod)
	})

	// The census: nothing in this package's own source names the notification,
	// so there is nothing that could send one to any request at all. It is
	// needled first, against a string this file is known to contain, so its zero
	// is a reading rather than a claim about the search.
	t.Run("nothing in the package names it", func(t *testing.T) {
		sources := packageSources(t)
		if len(sources) == 0 {
			t.Fatal("no sources were read, so the census below would hold whatever the package contained")
		}
		needle := "notifications/initialized"
		var fires bool
		for _, src := range sources {
			if strings.Contains(src.body, needle) {
				fires = true
			}
			if strings.Contains(src.body, notificationMethod) {
				t.Errorf("%s names %q", src.path, notificationMethod)
			}
		}
		if !fires {
			t.Fatalf("the census does not fire: no source carries %q, so its silence about %q "+
				"measures the search rather than the package", needle, notificationMethod)
		}
	})
}

// wantNoNotification asserts one framed message is an answer to a request rather
// than a notification: it carries an identifier, and it names no method.
func wantNoNotification(t *testing.T, framed, notificationMethod string) {
	t.Helper()
	var message struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.Unmarshal([]byte(framed), &message); err != nil {
		t.Fatalf("the message does not parse: %q: %v", framed, err)
	}
	if message.Method != "" {
		t.Errorf("the server sent a message naming the method %q, which an answer does not carry: %q",
			message.Method, framed)
	}
	if len(message.ID) == 0 {
		t.Errorf("the server sent a message with no identifier, which is a notification: %q", framed)
	}
	if strings.Contains(framed, notificationMethod) {
		t.Errorf("the server sent %q", framed)
	}
}

// source is one of this package's own files and its contents.
type source struct {
	path string
	body string
}

// packageSources reads this package's non-test Go files, for the census above.
func packageSources(t *testing.T) []source {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}
	out := make([]source, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		out = append(out, source{path: name, body: string(body)})
	}
	return out
}
