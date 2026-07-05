// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registrytrust

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// schemeCapture is an http.RoundTripper that records the scheme of the request
// URL and answers with a canned trust document, so Fetch's plain-HTTP-vs-HTTPS
// decision is observable without a live endpoint or DNS.
type schemeCapture struct{ scheme string }

func (c *schemeCapture) RoundTrip(r *http.Request) (*http.Response, error) {
	c.scheme = r.URL.Scheme
	const body = `{"requires_signatures": false, "trusted_identities": [{"identity": "dev@example.com", "issuer": "https://accounts.google.com"}]}`
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

// TestFetchLoopbackHostsUsePlainHTTP proves the plain-HTTP gate covers the full
// loopback set — reserved ".localhost" names and the unspecified 0.0.0.0
// address alongside literal loopback IPs and "localhost" — while a public host
// that merely contains "localhost" as a substring keeps HTTPS.
func TestFetchLoopbackHostsUsePlainHTTP(t *testing.T) {
	t.Parallel()
	cases := []struct {
		host       string
		wantScheme string
	}{
		{"localhost:5000", "http"},
		{"127.0.0.1:5000", "http"},
		{"app.localhost:5000", "http"},
		{"0.0.0.0:5000", "http"},
		{"localhost.evil.com:5000", "https"},
		{"registry.example.com", "https"},
	}
	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			t.Parallel()
			cap := &schemeCapture{}
			if _, err := Fetch(context.Background(), &http.Client{Transport: cap}, tc.host); err != nil {
				t.Fatalf("Fetch(%q): %v", tc.host, err)
			}
			if cap.scheme != tc.wantScheme {
				t.Errorf("Fetch(%q) used scheme %q; want %q", tc.host, cap.scheme, tc.wantScheme)
			}
		})
	}
}

// TestDecodeValidDocument proves a well-formed advertised trust document
// decodes into the expected struct across all four field families.
func TestDecodeValidDocument(t *testing.T) {
	t.Parallel()
	const body = `{
  "requires_signatures": true,
  "trusted_identities": [
    {"identity": "dev@example.com", "issuer": "https://accounts.google.com"},
    {"identity_regexp": "^https://github.com/acme/", "issuer": "https://token.actions.githubusercontent.com"}
  ],
  "trusted_deprecation_identities": [
    {"identity": "security@example.com", "issuer": "https://accounts.google.com"}
  ],
  "public_key": "-----BEGIN PUBLIC KEY-----\nABC\n-----END PUBLIC KEY-----\n"
}`
	got, err := Decode([]byte(body))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !got.RequiresSignatures {
		t.Error("requires_signatures should decode true")
	}
	if len(got.TrustedIdentities) != 2 {
		t.Fatalf("trusted_identities = %d, want 2", len(got.TrustedIdentities))
	}
	if got.TrustedIdentities[0].Identity != "dev@example.com" {
		t.Errorf("identity[0] = %q", got.TrustedIdentities[0].Identity)
	}
	if got.TrustedIdentities[1].IdentityRegexp == "" {
		t.Error("identity[1] regexp should decode")
	}
	if len(got.TrustedDeprecationIdentities) != 1 {
		t.Errorf("trusted_deprecation_identities = %d, want 1", len(got.TrustedDeprecationIdentities))
	}
	if !strings.Contains(got.PublicKey, "BEGIN PUBLIC KEY") {
		t.Errorf("public_key = %q", got.PublicKey)
	}
}

// TestDecodeMalformed proves both a JSON parse failure and a semantically
// invalid identity entry map to ErrMalformedTrust so the caller can hard-fail
// on a broken advertised document.
func TestDecodeMalformed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
	}{
		{"not_json", `not json at all`},
		{"unknown_field", `{"trusted_identiteez": []}`},
		{"identity_missing_issuer", `{"trusted_identities": [{"identity": "dev@example.com"}]}`},
		{"identity_empty", `{"trusted_identities": [{"issuer": "https://accounts.google.com"}]}`},
		{"deprecation_missing_issuer", `{"trusted_deprecation_identities": [{"identity": "x"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Decode([]byte(tc.body))
			if !errors.Is(err, ErrMalformedTrust) {
				t.Fatalf("Decode(%s) err = %v; want errors.Is(ErrMalformedTrust)", tc.name, err)
			}
		})
	}
}

// TestFetchPinsAdvertisedDocument proves Fetch GETs the well-known path from
// the httptest server and decodes the served body. No live network is used.
func TestFetchPinsAdvertisedDocument(t *testing.T) {
	t.Parallel()
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"requires_signatures": true, "trusted_identities": [{"identity": "dev@example.com", "issuer": "https://accounts.google.com"}]}`))
	}))
	t.Cleanup(srv.Close)

	host := strings.TrimPrefix(srv.URL, "http://") // loopback → Fetch speaks plain HTTP
	adv, err := Fetch(context.Background(), srv.Client(), host)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotPath != WellKnownPath {
		t.Errorf("server saw path %q; want %q", gotPath, WellKnownPath)
	}
	if !adv.RequiresSignatures || len(adv.TrustedIdentities) != 1 {
		t.Errorf("advertised = %+v", adv)
	}
}

// TestFetch404IsNoTrustAdvertised proves a registry that does not serve the
// well-known path maps to the soft ErrNoTrustAdvertised outcome, not a hard
// error.
func TestFetch404IsNoTrustAdvertised(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	_, err := Fetch(context.Background(), srv.Client(), host)
	if !errors.Is(err, ErrNoTrustAdvertised) {
		t.Fatalf("Fetch 404 err = %v; want errors.Is(ErrNoTrustAdvertised)", err)
	}
}

// TestFetchMalformedBodyIsHardError proves a body that fetches but does not
// decode surfaces ErrMalformedTrust — the endpoint answered with unusable
// content, which the caller hard-fails on.
func TestFetchMalformedBodyIsHardError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"trusted_identities": [{"identity": "no-issuer"}]}`))
	}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	_, err := Fetch(context.Background(), srv.Client(), host)
	if !errors.Is(err, ErrMalformedTrust) {
		t.Fatalf("Fetch malformed err = %v; want errors.Is(ErrMalformedTrust)", err)
	}
}

// TestFetchTransportErrorTolerated proves an unreachable endpoint returns a
// transport error that is neither ErrNoTrustAdvertised nor ErrMalformedTrust,
// so the caller tolerates it as "advertises nothing" while distinguishing it
// from a malformed document.
func TestFetchTransportError(t *testing.T) {
	t.Parallel()
	// A server that is created then immediately closed: the port is unbound,
	// so the GET fails at connect with a transport error (no live DNS needed).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	client := srv.Client()
	host := strings.TrimPrefix(srv.URL, "http://")
	srv.Close()

	_, err := Fetch(context.Background(), client, host)
	if err == nil {
		t.Fatal("Fetch against a closed endpoint should error")
	}
	if errors.Is(err, ErrMalformedTrust) {
		t.Errorf("transport error must not be ErrMalformedTrust: %v", err)
	}
	if errors.Is(err, ErrNoTrustAdvertised) {
		t.Errorf("a connect failure is a transport error, not a 404: %v", err)
	}
}

func TestFetchRequiresHost(t *testing.T) {
	t.Parallel()
	if _, err := Fetch(context.Background(), http.DefaultClient, ""); err == nil {
		t.Fatal("Fetch with empty host should error")
	}
}
