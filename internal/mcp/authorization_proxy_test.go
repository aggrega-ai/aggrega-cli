// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// Every proof here drives the reader the handler SHIPS with for a deployment
// that reads its caller from a proxy in front, and every confinement proof
// drives the entry point the command layer calls. Nothing installs the
// accept-all authenticator: what is under test is which credential this
// deployment reads and where it may read it.

// The two headers the proofs here configure. They are the spellings a proxy
// commonly uses, and nothing in the product reads them by name — the deployment
// names them, which is the property the last proof in this file asserts.
const (
	testPrincipalHeader = "X-Forwarded-User"
	testScopesHeader    = "X-Forwarded-Scopes"

	// testAssertedPrincipal is who the proxy says is calling. It is compared to
	// nothing by this deployment, so it exists only so a proof can read back
	// what arrived.
	testAssertedPrincipal = "asserted-caller@example.test"
)

// testTrustedProxy is the proxy declaration every accepting proof here runs
// under: both headers named, which is what a deployment behind a proxy that
// asserts scopes writes.
func testTrustedProxy() TrustedProxyConfig {
	return TrustedProxyConfig{PrincipalHeader: testPrincipalHeader, ScopesHeader: testScopesHeader}
}

// proxyTransport is the deployment shape these proofs run as: the caps every
// other proof in this package uses, this deployment's own declared addresses,
// and the proxy declaration and egress gate the row supplies.
func proxyTransport(proxy TrustedProxyConfig, gate NetworkEgress) HTTPConfig {
	cfg := defaultTransportConfig()
	cfg.Authorization.TrustedProxy = proxy
	cfg.Egress = gate
	return cfg
}

// seenPrincipals collects the callers the reader produced. Requests arrive on
// the server's own goroutines and the proof reads from its own, so the two are
// separated by a lock rather than by hope.
type seenPrincipals struct {
	mu  sync.Mutex
	all []*principal
}

func (s *seenPrincipals) record(p *principal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.all = append(s.all, p)
}

func (s *seenPrincipals) last() *principal {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.all) == 0 {
		return nil
	}
	return s.all[len(s.all)-1]
}

// recordingAuthenticator passes every request through the reader the handler
// installed and copies out the caller that reader attached.
//
// It is TEST-ONLY and it decides nothing: the production reader admits or
// refuses, and this only reads what it produced. It exists because nothing in
// the handler consumes the caller yet, so there is no production surface a
// proof could read one back from — and calling the reader directly with a
// synthetic request would leave the transport out of the drive entirely, which
// is what the rest of this package's proofs deliberately do not do.
type recordingAuthenticator struct {
	inner httpAuthenticator
	seen  *seenPrincipals
}

func (a *recordingAuthenticator) authenticate(r *http.Request) (*http.Request, *authRefusal) {
	admitted, refusal := a.inner.authenticate(r)
	if admitted != nil {
		a.seen.record(principalFrom(admitted.Context()))
	}
	return admitted, refusal
}

// proxyFixture is one listener reading its caller from a proxy's headers, plus
// what the reader the handler shipped with produced for each request.
type proxyFixture struct {
	*httpFixture
	seen *seenPrincipals
}

// newProxyFixture starts a listener for the given deployment and wraps whatever
// reader the handler installed, so a proof reads the real reader's output.
func newProxyFixture(t *testing.T, transport HTTPConfig) *proxyFixture {
	t.Helper()
	s := newCommandToolServer(t, nil)
	s.logw = io.Discard
	h, err := newHTTPHandler(context.Background(), s, transport)
	if err != nil {
		t.Fatalf("newHTTPHandler: %v", err)
	}
	if _, isProxy := h.auth.(*proxyAssertion); !isProxy {
		t.Fatalf("the handler installed %T rather than the proxy reader, so nothing below is about "+
			"proxy mode", h.auth)
	}
	seen := &seenPrincipals{}
	h.auth = &recordingAuthenticator{inner: h.auth, seen: seen}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return &proxyFixture{httpFixture: &httpFixture{ts: ts, h: h}, seen: seen}
}

// callWith posts one well-formed request in the per-request revision carrying
// the given headers, so a row states only the headers it is about.
func (f *proxyFixture) callWith(t *testing.T, headers map[string]string) answer {
	t.Helper()
	sent := modernHeaders("tools/list", "")
	for name, value := range headers {
		sent[name] = value
	}
	return f.send(t, http.MethodPost, endpointPath, sent, modernRequest(t, 1, "tools/list", nil))
}

// A deployment reading its caller from a proxy is refused on an address the
// network can reach, and the refusal is made before anything binds.
//
// The three rows are one table because the accepting two are the refusing one's
// controls: without them a build that refused every address would satisfy the
// first row and look correct.
//
// "Nothing bound" is read from the seam rather than inferred from an error
// having been returned. The seam runs after the health address has opened and
// before the endpoint binds, so a launch that reached it has already bound
// something; a launch that never reaches it has bound nothing at all. The
// accepting rows read the health socket from inside the seam, which is what
// makes the refusing row's silence an absence rather than a seam that never
// works.
func TestTrustedProxyConfinesTheListenAddressBeforeAnythingBinds(t *testing.T) {
	certDir := t.TempDir()
	certFile, keyFile, _ := testCertificate(t, certDir)

	for _, row := range []struct {
		name    string
		address func(t *testing.T) string
		cert    bool
		refused bool
	}{
		{
			name:    "a network address, with a certificate so the plaintext confinement is not what refuses",
			address: func(*testing.T) string { return "0.0.0.0:0" },
			cert:    true,
			refused: true,
		},
		{
			name:    "loopback",
			address: func(*testing.T) string { return "127.0.0.1:0" },
		},
		{
			name: "a Unix socket",
			address: func(t *testing.T) string {
				address, _ := socketAddress(t)
				return address
			},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Chdir(aDirectoryNoWorkspaceResolvesFrom(t))
			healthAddress, healthSocket := healthSocketAddress(t)

			listen := ListenConfig{Address: row.address(t), HealthAddress: healthAddress}
			if row.cert {
				listen.CertFile, listen.KeyFile = certFile, keyFile
			}

			reached := false
			healthOpenAtTheSeam := false
			err := listenAndServe(context.Background(), Config{Stderr: io.Discard},
				proxyTransport(testTrustedProxy(), nil),
				listen,
				func() {
					reached = true
					_, statErr := os.Stat(healthSocket)
					healthOpenAtTheSeam = statErr == nil
				})

			if err == nil {
				t.Fatal("the launch served rather than returning a refusal, so nothing below is readable")
			}
			if !row.refused {
				if !reached {
					t.Fatalf("the launch never reached the workspace question, so the address was refused "+
						"before it: %v", err)
				}
				if !healthOpenAtTheSeam {
					t.Errorf("%s had not opened at the seam, so this row cannot act as the control for a "+
						"refusal that never reaches the seam", healthSocket)
				}
				if !strings.Contains(err.Error(), "--workspace-root") {
					t.Errorf("the refusal is not the workspace one, so this address was refused for some "+
						"other reason: %v", err)
				}
				return
			}

			if reached {
				t.Error("the launch reached the workspace question, so it had already bound its first " +
					"address with a request header as its whole credential on an address the network reaches")
			}
			if _, statErr := os.Stat(healthSocket); statErr == nil {
				t.Errorf("%s exists, so the launch bound before the refusal", healthSocket)
			}
			for _, setting := range []string{"mcp.authorization.trusted_proxy", "mcp.listen"} {
				if !strings.Contains(err.Error(), setting) {
					t.Errorf("the refusal does not name %s, so a reader is not told which two settings "+
						"conflict: %v", setting, err)
				}
			}
		})
	}
}

// The caller is the value of the configured header, and the scopes are the
// other header's value split on spaces; both reach the handler.
//
// What arrived is read back rather than inferred from a 200: a reader that
// admitted every request while attaching nothing would answer 200 just as
// readily, and nothing else in the handler consumes the caller yet.
func TestTheProxyAssertionCarriesTheCallerAndTheScopesItWasGiven(t *testing.T) {
	f := newProxyFixture(t, proxyTransport(testTrustedProxy(), nil))

	got := f.callWith(t, map[string]string{
		testPrincipalHeader: testAssertedPrincipal,
		testScopesHeader:    scopeRead + " " + scopeWorkspaceWrite,
	})
	wantReachedTheToolSurface(t, got)

	arrived := f.seen.last()
	if arrived == nil {
		t.Fatal("the request was served with no caller attached to it")
	}
	if arrived.Subject != testAssertedPrincipal {
		t.Errorf("caller = %q, want %q — the value of %s", arrived.Subject, testAssertedPrincipal,
			testPrincipalHeader)
	}
	if len(arrived.Scopes) != 2 || arrived.Scopes[0] != scopeRead || arrived.Scopes[1] != scopeWorkspaceWrite {
		t.Errorf("scopes = %+v, want the two the %s header listed, in that order",
			arrived.Scopes, testScopesHeader)
	}
	if arrived.Issuer != "" {
		t.Errorf("issuer = %q, want empty: this deployment read no token, so it knows of no issuer",
			arrived.Issuer)
	}
}

// A request carrying no principal header is refused, and the refusal is the
// challenge every caller with no credential receives.
func TestAProxyDeploymentRefusesARequestCarryingNoPrincipalHeader(t *testing.T) {
	f := newProxyFixture(t, proxyTransport(testTrustedProxy(), nil))

	got := f.callWith(t, map[string]string{testScopesHeader: scopeRead})
	if got.status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body %q)", got.status, got.raw)
	}
	if got.header.Get(headerAuthenticate) == "" {
		t.Error("the refusal carries no challenge, so a caller is not told where to obtain a credential")
	}
	if f.seen.last() != nil {
		t.Error("a caller was attached to a request that carried no principal header")
	}
}

// Only the two configured headers are read. A deployment naming no scopes
// header asserts no scopes, whatever else the request carries.
func TestAProxyDeploymentReadsOnlyTheHeadersItNamed(t *testing.T) {
	f := newProxyFixture(t, proxyTransport(
		TrustedProxyConfig{PrincipalHeader: testPrincipalHeader}, nil))

	got := f.callWith(t, map[string]string{
		testPrincipalHeader: testAssertedPrincipal,
		// Named by no setting in this deployment. A reader consulting a header
		// the deployment did not name would make a second thing trustable that
		// the confinement does not cover.
		testScopesHeader:  scopeRegistryWrite,
		"X-Forwarded-For": "203.0.113.7",
	})
	wantReachedTheToolSurface(t, got)

	arrived := f.seen.last()
	if arrived == nil {
		t.Fatal("the request was served with no caller attached to it")
	}
	if len(arrived.Scopes) != 0 {
		t.Errorf("scopes = %+v, want none: this deployment named no scopes header", arrived.Scopes)
	}
}

// No token is processed in proxy mode, and the pair of rows below is what shows
// it rather than asserting it.
//
// A token the validator would refuse outright — issued for another deployment —
// is served when the request carries a valid principal header; a token the
// validator WOULD admit is refused when the request carries none. Neither row
// shows this alone: the first could be a validator that admits everything and
// the second a validator that refuses everything, and only together do they
// show that the answer turns on the header and never on the token.
//
// The first row's control is in the same proof: the same token, against the
// same deployment shape with no proxy declared, is refused.
func TestNoTokenIsProcessedWhereAProxyAssertsTheCaller(t *testing.T) {
	issuer := newAuthorizationServerDouble(t)
	issuedForSomebodyElse := issuer.mint(t, tokenClaimSet{audience: []string{"https://elsewhere.example.test"}})
	issuedForThisDeployment := issuer.mint(t, tokenClaimSet{})

	validating := newTokenFixture(t, transportFor(issuer.authorizationConfig(), issuer.egress()))
	if got := validating.call(t, bearer(issuedForSomebodyElse)); got.status != http.StatusUnauthorized {
		t.Fatalf("precondition: a token issued for another deployment was answered %d rather than 401, "+
			"so the row below would prove nothing (body %q)", got.status, got.raw)
	}
	if got := validating.call(t, bearer(issuedForThisDeployment)); got.status != http.StatusOK {
		t.Fatalf("precondition: a token issued for this deployment was answered %d rather than 200, "+
			"so the row below would prove nothing (body %q)", got.status, got.raw)
	}

	proxied := proxyTransport(testTrustedProxy(), issuer.egress())
	proxied.Authorization.Issuer = issuer.authorizationConfig().Issuer
	proxied.Authorization.Resource = issuer.authorizationConfig().Resource
	f := newProxyFixture(t, proxied)

	served := f.callWith(t, map[string]string{
		headerAuthorization: bearer(issuedForSomebodyElse),
		testPrincipalHeader: testAssertedPrincipal,
	})
	wantReachedTheToolSurface(t, served)

	refused := f.callWith(t, map[string]string{headerAuthorization: bearer(issuedForThisDeployment)})
	if refused.status != http.StatusUnauthorized {
		t.Errorf("a token this deployment's own authorization server issued for it was answered %d with "+
			"no principal header, want 401: the token is not what admits a caller here (body %q)",
			refused.status, refused.raw)
	}
}

// A proxy deployment reaches no authorization server at all, at startup or
// afterwards, and the pair of rows is what shows it.
//
// The gate that refuses every outbound request is the same one an offline
// workspace hands over. A token-validating deployment is refused before it
// serves anything, because it has nowhere to read its verification keys from; a
// proxy deployment builds and serves, because it was never going to read any.
func TestAProxyDeploymentNeverReachesForVerificationKeys(t *testing.T) {
	offline := errors.New("this workspace permits no outbound request")

	validating := defaultTransportConfig()
	validating.Egress = refusingEgress(offline)
	if _, err := newHTTPHandler(context.Background(), newCommandToolServer(t, nil), validating); err == nil {
		t.Fatal("precondition: a token-validating deployment was built with nowhere to read its keys " +
			"from, so the row below would prove nothing")
	}

	gate := &countingEgress{}
	f := newProxyFixture(t, proxyTransport(testTrustedProxy(), gate.gate()))
	got := f.callWith(t, map[string]string{testPrincipalHeader: testAssertedPrincipal})
	wantReachedTheToolSurface(t, got)

	if asked := gate.asked.Load(); asked != 0 {
		t.Errorf("the egress gate was asked %d times by a deployment that validates no token", asked)
	}
}

// A deployment naming a scopes header and no principal header is refused rather
// than read as one that asked for no proxy at all.
//
// The two are one setting in two halves, exactly as the certificate and its key
// are: reading the half-declared form as the absent one would leave every
// request validated against a token the proxy in front has already consumed.
func TestAHalfDeclaredProxyIsRefusedRatherThanIgnored(t *testing.T) {
	transport := proxyTransport(TrustedProxyConfig{ScopesHeader: testScopesHeader}, nil)
	_, err := newHTTPHandler(context.Background(), newCommandToolServer(t, nil), transport)
	if err == nil {
		t.Fatal("a scopes header with no principal header was accepted, so the deployment quietly " +
			"validates tokens the proxy in front has already consumed")
	}
	if !strings.Contains(err.Error(), "mcp.authorization.trusted_proxy.principal_header") {
		t.Errorf("the refusal does not name the key that resolves it: %v", err)
	}
}
