// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// What a deployment declares about itself is read back here from the wire, on a
// listener that has the authenticator it ships with installed, because what is
// under test is the pair of answers a caller with no token receives: the
// document, and the challenge that points at it.
//
// The two expectations below are written out rather than derived, deliberately.
// Deriving the path and the address from the configured resource the way the
// production code derives them would compare that derivation with itself and
// pass whatever it did; a literal is an independent statement of where a client
// will look.

const (
	// testIssuer and testResource are the deployment every proof in this package
	// runs as. Both sit under a name reserved for testing, so neither names
	// anything a proof here could reach.
	testIssuer   = "https://login.example.test"
	testResource = "https://mcp.example.test/mcp"

	// testMetadataPath is where a client asks for the document first: the
	// well-known path with the resource's own path after it.
	testMetadataPath = "/.well-known/oauth-protected-resource/mcp"

	// testMetadataAddress is the whole address the challenge names.
	testMetadataAddress = "https://mcp.example.test/.well-known/oauth-protected-resource/mcp"
)

// defaultAuthorizationConfig is what a deployment declares in every proof that
// is not about declaring it.
func defaultAuthorizationConfig() AuthorizationConfig {
	return AuthorizationConfig{Issuer: testIssuer, Resource: testResource}
}

// parseChallenge reads a challenge into its scheme and its parameters.
//
// It parses rather than matching a substring: a parameter is asserted by name,
// so a challenge that carried the right characters in the wrong place — an
// address inside the scope, a parameter spelled as part of another one — fails
// here instead of passing a contains check.
func parseChallenge(t *testing.T, value string) (scheme string, params map[string]string) {
	t.Helper()
	params = map[string]string{}
	scheme, rest, found := strings.Cut(value, " ")
	if !found {
		return scheme, params
	}
	for _, part := range strings.Split(rest, ",") {
		name, raw, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			t.Errorf("the challenge carries %q, which is not a parameter: %q", part, value)
			continue
		}
		params[name] = strings.Trim(raw, `"`)
	}
	return scheme, params
}

// wantPublishedNames asserts one list in the document, by name and in order.
func wantPublishedNames(t *testing.T, doc map[string]any, key string, want ...string) {
	t.Helper()
	raw, ok := doc[key].([]any)
	if !ok {
		t.Errorf("%s is %#v, which is not a list of names", key, doc[key])
		return
	}
	got := make([]string, 0, len(raw))
	for _, entry := range raw {
		name, isName := entry.(string)
		if !isName {
			t.Errorf("%s carries %#v, which is not a name", key, entry)
			return
		}
		got = append(got, name)
	}
	if !slices.Equal(got, want) {
		t.Errorf("%s = %q, want %q", key, got, want)
	}
}

// wantPublishedDocument asserts one answer is the document this deployment
// publishes about itself, and returns its raw bytes.
func wantPublishedDocument(t *testing.T, got answer, where string) string {
	t.Helper()
	if got.status != http.StatusOK {
		t.Fatalf("%s: status = %d, want 200 (body %q)", where, got.status, got.raw)
	}
	if contentType := got.header.Get(headerContentType); contentType != contentTypeJSON {
		t.Errorf("%s: %s = %q, want %q", where, headerContentType, contentType, contentTypeJSON)
	}
	if cacheControl := got.header.Get(headerCacheControl); cacheControl != "public, max-age=3600" {
		t.Errorf("%s: %s = %q, want a public hour", where, headerCacheControl, cacheControl)
	}
	if challenge := got.header.Get(headerAuthenticate); challenge != "" {
		t.Errorf("%s: the document was answered with the challenge %q, so a caller is asked for a token "+
			"in the one answer it reads in order to find out where to obtain one", where, challenge)
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(got.raw), &doc); err != nil {
		t.Fatalf("%s: the answer is not a document: %v (body %q)", where, err, got.raw)
	}
	if doc["resource"] != testResource {
		t.Errorf("%s: resource = %#v, want %q", where, doc["resource"], testResource)
	}
	wantPublishedNames(t, doc, "authorization_servers", testIssuer)
	wantPublishedNames(t, doc, "scopes_supported",
		"aggrega:read", "aggrega:workspace-write", "aggrega:registry-write")
	wantPublishedNames(t, doc, "bearer_methods_supported", "header")

	published := make([]string, 0, len(doc))
	for key := range doc {
		published = append(published, key)
	}
	slices.Sort(published)
	want := []string{"authorization_servers", "bearer_methods_supported", "resource", "scopes_supported"}
	if !slices.Equal(published, want) {
		t.Errorf("%s: the document publishes %q, want %q", where, published, want)
	}
	return got.raw
}

// A caller that has nothing gets the document, at both places a client looks,
// and gets it without presenting anything.
//
// The listener here has the authenticator the handler SHIPS with installed, so
// what is read is what a deployment answers rather than what a test-only bypass
// allows. The endpoint row at the end is the control: without it this would pass
// against a listener that had started serving everyone.
func TestTheDocumentIsPublishedAtBothPlacesAClientLooksWithNoCredential(t *testing.T) {
	f := newUnauthenticatedHTTPFixture(t, context.Background(), defaultTransportConfig(), nil)

	inserted := wantPublishedDocument(t, f.send(t, http.MethodGet, testMetadataPath, nil, ""),
		"the path a client asks for first")
	bare := wantPublishedDocument(t, f.send(t, http.MethodGet, wellKnownProtectedResource, nil, ""),
		"the path a client falls back to")
	if inserted != bare {
		t.Errorf("the two paths answer different documents:\n first: %s\n  then: %s", inserted, bare)
	}

	endpoint := f.post(t, nil, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if endpoint.status != http.StatusUnauthorized {
		t.Errorf("the endpoint answered %d; the document being served is only meaningful while the "+
			"protocol surface behind it still is not", endpoint.status)
	}
}

// Reading is all that path does, so anything else is refused the way the
// protocol endpoint refuses a method it does not serve.
func TestTheDocumentIsReadOnly(t *testing.T) {
	f := newUnauthenticatedHTTPFixture(t, context.Background(), defaultTransportConfig(), nil)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			got := f.send(t, method, testMetadataPath, nil, "")
			if got.status != http.StatusMethodNotAllowed {
				t.Errorf("status = %d, want 405 (body %q)", got.status, got.raw)
			}
			if allow := got.header.Get(headerAllow); allow != http.MethodGet {
				t.Errorf("%s = %q, want %q", headerAllow, allow, http.MethodGet)
			}
		})
	}
}

// A caller with no token is told where to go, by address and by scope.
//
// The address is the one the deployment declared and not the one the request
// arrived on, and this row reads that rather than assuming it: the request below
// reaches a listener on loopback and carries loopback as its own host, so an
// address built from the request's host would name that loopback address. The
// declared name is what comes back.
//
// The last step is what makes this more than a string comparison: the path out
// of the address the challenge named is fetched from this same listener, and it
// answers the same document. A challenge naming an address nothing serves would
// pass every assertion above it and fail there.
func TestARequestWithNoTokenIsChallengedWithThePublishedAddress(t *testing.T) {
	f := newUnauthenticatedHTTPFixture(t, context.Background(), defaultTransportConfig(), nil)

	got := f.post(t, nil, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if got.status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body %q)", got.status, got.raw)
	}
	scheme, params := parseChallenge(t, got.header.Get(headerAuthenticate))
	if scheme != "Bearer" {
		t.Errorf("the challenge scheme is %q, want %q", scheme, "Bearer")
	}
	if params["resource_metadata"] != testMetadataAddress {
		t.Errorf("resource_metadata = %q, want %q", params["resource_metadata"], testMetadataAddress)
	}
	if params["scope"] != "aggrega:read" {
		t.Errorf("scope = %q, want %q", params["scope"], "aggrega:read")
	}

	named, err := url.Parse(params["resource_metadata"])
	if err != nil {
		t.Fatalf("the challenge names %q, which is not an address: %v", params["resource_metadata"], err)
	}
	wantPublishedDocument(t, f.send(t, http.MethodGet, named.Path, nil, ""),
		"the path out of the address the challenge named")
}

// A listener is refused before it binds until the deployment says who issues
// its tokens and what it is called as the resource they are for.
//
// Each row reads the absence of the socket file afterwards rather than the
// error alone: a refusal that returned an error after binding would satisfy an
// error-only assertion while the address was already accepting.
func TestAListenerIsRefusedUntilTheDeploymentDeclaresItself(t *testing.T) {
	for _, tc := range []struct {
		name          string
		authorization AuthorizationConfig
		names         []string
	}{
		{
			"neither key set",
			AuthorizationConfig{},
			[]string{"mcp.authorization.issuer", "mcp.authorization.resource"},
		},
		{
			"only the issuer set",
			AuthorizationConfig{Issuer: testIssuer},
			[]string{"mcp.authorization.resource"},
		},
		{
			"only the resource set",
			AuthorizationConfig{Resource: testResource},
			[]string{"mcp.authorization.issuer"},
		},
		{
			"an issuer carrying no scheme and host",
			AuthorizationConfig{Issuer: "login.example.test", Resource: testResource},
			[]string{"mcp.authorization.issuer"},
		},
		{
			"a resource carrying no scheme and host",
			AuthorizationConfig{Issuer: testIssuer, Resource: "mcp.example.test/mcp"},
			[]string{"mcp.authorization.resource"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			address, path := socketAddress(t)
			transport := defaultTransportConfig()
			transport.Authorization = tc.authorization

			// The launch runs on a goroutine of its own, under a context this test
			// can end, because a refusal that did not happen does not fail — it
			// serves, and a launch called in line would hang until the whole package
			// timed out.
			ctx, endRun := context.WithCancel(context.Background())
			t.Cleanup(endRun)
			launched := make(chan error, 1)
			go func() {
				launched <- ListenAndServe(ctx,
					Config{WorkspaceRoot: rootedWorkspace(t), Stderr: io.Discard},
					transport, ListenConfig{Address: address})
			}()

			var err error
			select {
			case err = <-launched:
			case <-time.After(runReturnCeiling):
				t.Fatal("the launch neither refused nor returned within the liveness ceiling, so it is " +
					"serving for a deployment that has declared nothing")
			}
			if err == nil {
				t.Fatal("the listener served for a deployment that has declared nothing")
			}
			for _, name := range tc.names {
				if !strings.Contains(err.Error(), name) {
					t.Errorf("the refusal does not name %s, so an operator cannot act on it; it reads %q",
						name, err)
				}
			}
			if _, statErr := os.Stat(path); statErr == nil {
				t.Errorf("%s exists, so the address was bound before the refusal", path)
			}
		})
	}
}

// The positive control the refusals need: a deployment that has declared itself
// binds, and the document it publishes is readable over the address it bound.
//
// Without this every row above would pass against a listener that refused every
// launch it was given.
func TestAListenerThatDeclaresItselfBindsAndPublishesTheDocument(t *testing.T) {
	address, path := socketAddress(t)
	ctx, endRun := context.WithCancel(context.Background())
	t.Cleanup(endRun)

	served := make(chan error, 1)
	go func() {
		served <- ListenAndServe(ctx,
			Config{WorkspaceRoot: rootedWorkspace(t), Stderr: io.Discard},
			defaultTransportConfig(), ListenConfig{Address: address})
	}()
	awaitSocket(t, path, served)

	resp, err := unixClient(path).Get("http://socket" + testMetadataPath)
	if err != nil {
		t.Fatalf("read the document over the bound address: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read the document's body: %v", err)
	}
	wantPublishedDocument(t, answer{status: resp.StatusCode, header: resp.Header, raw: string(raw)},
		"the document over a bound listener")

	endRun()
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("serving returned %v, want a clean end", err)
		}
	case <-time.After(runReturnCeiling):
		t.Fatal("the listener never stopped serving within the liveness ceiling")
	}
}
