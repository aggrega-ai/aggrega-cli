// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// advertisedTrustServer stands up an httptest server serving the given body at
// the well-known trust path and a 404 elsewhere, returning its loopback host
// (so the trust fetch speaks plain HTTP) and client. No live network is used.
func advertisedTrustServer(t *testing.T, body string) (host string, client *http.Client) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/aggrega-trust" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), srv.Client()
}

// TestRegistryAddFetchesAndPinsTrust proves trust-on-first-use: with no manual
// flags, `registry add` fetches the registry's advertised trust document and
// pins requires_signatures, both identity OR-sets, and the public key into the
// written config — round-tripped through the config resolver.
func TestRegistryAddFetchesAndPinsTrust(t *testing.T) {
	t.Parallel()
	host, client := advertisedTrustServer(t, `{
  "requires_signatures": true,
  "trusted_identities": [
    {"identity": "dev@acme.example", "issuer": "https://accounts.google.com"},
    {"identity_regexp": "^https://github.com/acme/", "issuer": "https://token.actions.githubusercontent.com"}
  ],
  "trusted_deprecation_identities": [
    {"identity": "security@acme.example", "issuer": "https://accounts.google.com"}
  ]
}`)

	rw := resolveTempWorkspace(t)
	var out bytes.Buffer
	if err := registryAddRun(registryAddConfig{
		Host:        host,
		FetchTrust:  true,
		ws:          rw,
		Out:         &out,
		trustClient: client,
	}); err != nil {
		t.Fatalf("registryAddRun: %v", err)
	}

	reloaded, err := workspaceconfig.Resolve(rw.Root(), "")
	if err != nil {
		t.Fatalf("re-resolve: %v", err)
	}
	pol, found := reloaded.Config().RegistryTrustPolicy(host)
	if !found {
		t.Fatal("registry should be found after add")
	}
	if !pol.RequiresSignatures {
		t.Error("requires_signatures should be pinned from the advertised document")
	}
	if len(pol.TrustedIdentities) != 2 {
		t.Fatalf("trusted_identities = %v, want 2 pinned", pol.TrustedIdentities)
	}
	if pol.TrustedIdentities[0].Identity != "dev@acme.example" {
		t.Errorf("identity[0] = %q; want the advertised identity", pol.TrustedIdentities[0].Identity)
	}
	if pol.TrustedIdentities[1].IdentityRegexp != "^https://github.com/acme/" {
		t.Errorf("identity[1] regexp = %q; want the advertised regexp", pol.TrustedIdentities[1].IdentityRegexp)
	}
	if len(pol.TrustedDeprecationIdentities) != 1 || pol.TrustedDeprecationIdentities[0].Identity != "security@acme.example" {
		t.Errorf("trusted_deprecation_identities = %v; want the advertised deprecation identity", pol.TrustedDeprecationIdentities)
	}
	if !strings.Contains(out.String(), "Pinned advertised trust policy") {
		t.Errorf("expected a pin notice; got %q", out.String())
	}
}

// TestRegistryAddManualIdentityOverridesFetch proves a manual --identity
// overrides (skips) the fetched advertised set: the served document is NOT
// pinned, only the flag identity is written. The served body would otherwise
// pin different identities, so a pinned-from-fetch result would be visible.
func TestRegistryAddManualIdentityOverridesFetch(t *testing.T) {
	t.Parallel()
	host, client := advertisedTrustServer(t, `{
  "requires_signatures": true,
  "trusted_identities": [
    {"identity": "fetched@acme.example", "issuer": "https://accounts.google.com"}
  ]
}`)

	rw := resolveTempWorkspace(t)
	var out bytes.Buffer
	if err := registryAddRun(registryAddConfig{
		Host:        host,
		Identities:  []string{"manual@acme.example=https://accounts.google.com"},
		FetchTrust:  true, // on, but a manual policy must override it
		ws:          rw,
		Out:         &out,
		trustClient: client,
	}); err != nil {
		t.Fatalf("registryAddRun: %v", err)
	}

	reloaded, _ := workspaceconfig.Resolve(rw.Root(), "")
	pol, found := reloaded.Config().RegistryTrustPolicy(host)
	if !found {
		t.Fatal("registry should be found")
	}
	if len(pol.TrustedIdentities) != 1 {
		t.Fatalf("trusted_identities = %v; want exactly the one manual identity (fetch overridden)", pol.TrustedIdentities)
	}
	if pol.TrustedIdentities[0].Identity != "manual@acme.example" {
		t.Errorf("identity = %q; want the manual identity, not the fetched one", pol.TrustedIdentities[0].Identity)
	}
	// The fetched requires_signatures must NOT have been pinned, since the
	// fetch was skipped in favor of the manual policy.
	if pol.RequiresSignatures {
		t.Error("requires_signatures should be false: the manual override skipped the fetch")
	}
	if strings.Contains(out.String(), "Pinned advertised trust policy") {
		t.Errorf("a manual override must not pin from the fetch; got %q", out.String())
	}
}

// TestRegistryAddNoTrustAdvertisedFallsBack proves a registry that serves no
// trust document (404) does not hard-fail: the add succeeds, prints the
// manual-pinning notice, and writes an empty policy.
func TestRegistryAddNoTrustAdvertisedFallsBack(t *testing.T) {
	t.Parallel()
	// A server that 404s every path — including the well-known trust path.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	rw := resolveTempWorkspace(t)
	var out bytes.Buffer
	if err := registryAddRun(registryAddConfig{
		Host:        host,
		FetchTrust:  true,
		ws:          rw,
		Out:         &out,
		trustClient: srv.Client(),
	}); err != nil {
		t.Fatalf("registryAddRun should not hard-fail on an unadvertised registry: %v", err)
	}

	reloaded, _ := workspaceconfig.Resolve(rw.Root(), "")
	pol, found := reloaded.Config().RegistryTrustPolicy(host)
	if !found {
		t.Fatal("registry should still be registered after a fallback add")
	}
	if len(pol.TrustedIdentities) != 0 || pol.RequiresSignatures {
		t.Errorf("fallback should write an empty policy; got %+v", pol)
	}
	if !strings.Contains(out.String(), "advertises no trust config") {
		t.Errorf("expected the manual-pinning notice; got %q", out.String())
	}
}

// TestRegistryAddUnreachableEndpointFallsBack proves a transport failure (the
// endpoint is unreachable) is tolerated like a 404 — the add succeeds with a
// notice — rather than hard-failing registration on a network error.
func TestRegistryAddUnreachableEndpointFallsBack(t *testing.T) {
	t.Parallel()
	// Stand up then immediately close a server so its port is unbound: the
	// fetch fails at connect (a transport error, not a 404). No live DNS.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	client := srv.Client()
	host := strings.TrimPrefix(srv.URL, "http://")
	srv.Close()

	rw := resolveTempWorkspace(t)
	var out bytes.Buffer
	if err := registryAddRun(registryAddConfig{
		Host:        host,
		FetchTrust:  true,
		ws:          rw,
		Out:         &out,
		trustClient: client,
	}); err != nil {
		t.Fatalf("an unreachable trust endpoint must not hard-fail the add: %v", err)
	}
	reloaded, _ := workspaceconfig.Resolve(rw.Root(), "")
	if _, found := reloaded.Config().RegistryTrustPolicy(host); !found {
		t.Fatal("registry should still be registered after a transport-failure fallback")
	}
	if !strings.Contains(out.String(), "advertises no trust config") {
		t.Errorf("expected the manual-pinning notice on transport failure; got %q", out.String())
	}
}

// TestRegistryAddNoFetchTrustSkipsFetch proves --no-fetch-trust (FetchTrust
// false) writes the flag-derived policy without contacting the endpoint. The
// server would pin an identity if fetched; a nil trustClient with FetchTrust
// off proves the fetch path is never entered.
func TestRegistryAddNoFetchTrustSkipsFetch(t *testing.T) {
	t.Parallel()
	rw := resolveTempWorkspace(t)
	var out bytes.Buffer
	if err := registryAddRun(registryAddConfig{
		Host:       "no-fetch.example.com",
		FetchTrust: false,
		// trustClient deliberately nil: if the fetch path were entered it would
		// build a default client and attempt a live GET, which this test forbids.
		ws:  rw,
		Out: &out,
	}); err != nil {
		t.Fatalf("registryAddRun: %v", err)
	}
	reloaded, _ := workspaceconfig.Resolve(rw.Root(), "")
	pol, found := reloaded.Config().RegistryTrustPolicy("no-fetch.example.com")
	if !found {
		t.Fatal("registry should be registered")
	}
	if len(pol.TrustedIdentities) != 0 {
		t.Errorf("no-fetch add should write an empty policy; got %+v", pol)
	}
	if strings.Contains(out.String(), "Pinned advertised trust policy") {
		t.Errorf("--no-fetch-trust must not pin from a fetch; got %q", out.String())
	}
}

// TestRegistryAddMalformedTrustHardFails proves a registry that serves a
// malformed trust document (fetches but does not decode) fails the add — the
// endpoint answered with unusable content, a real misconfiguration.
func TestRegistryAddMalformedTrustHardFails(t *testing.T) {
	t.Parallel()
	host, client := advertisedTrustServer(t, `{"trusted_identities": [{"identity": "no-issuer-here"}]}`)
	rw := resolveTempWorkspace(t)
	var out bytes.Buffer
	err := registryAddRun(registryAddConfig{
		Host:        host,
		FetchTrust:  true,
		ws:          rw,
		Out:         &out,
		trustClient: client,
	})
	if err == nil {
		t.Fatal("a malformed advertised trust document should fail the add")
	}
	if !strings.Contains(err.Error(), "malformed") {
		t.Errorf("err = %v; want a malformed-trust failure", err)
	}
}
