// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
)

// The ambient GitHub Actions OIDC endpoint is discovered through these
// ecosystem environment variables. They are named as literals here because the
// signing package keeps them unexported.
const (
	testActionsIDTokenRequestURL   = "ACTIONS_ID_TOKEN_REQUEST_URL"
	testActionsIDTokenRequestToken = "ACTIONS_ID_TOKEN_REQUEST_TOKEN"
)

// resolveIdentityTokenSource preserves the call shape these precedence cases are
// written against — terminal check and token getter as separate arguments — by
// composing them into the ladder's optional interactive rung exactly as a surface
// adapter does, then calling the shared ladder. It therefore exercises the real
// composition rather than standing in for it.
//
// The caller's vocabulary is left zero: these cases are about which RUNG resolves,
// and the vocabulary reaches only the refusal's wording, so supplying one would
// add a variable no case here varies.
func resolveIdentityTokenSource(flagToken string, look func(string) string, tty func() bool, acquire func() (string, error), validate func(string) error, client *http.Client) (string, signing.IdentityTokenSource, error) {
	return signing.ResolveIdentityToken(flagToken, look, validate, client, interactiveAcquirer(acquire, tty), signing.IdentityTokenSupply{})
}

func lookMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// noAcquire fails the test if the interactive acquisition path is reached; the
// precedence cases above the TTY tier must never fall through to it.
func noAcquire(t *testing.T) func() (string, error) {
	return func() (string, error) {
		t.Helper()
		t.Error("interactive acquisition reached; a higher-precedence source should have won")
		return "", nil
	}
}

// TestResolveIdentityTokenSourceStaticPrecedence covers the static tiers of the
// source precedence: the explicit flag and AGGREGA_IDENTITY_TOKEN both outrank
// an available ambient endpoint, SIGSTORE_ID_TOKEN is consulted below ambient,
// and each static tier resolves to a token string (source stays nil).
func TestResolveIdentityTokenSourceStaticPrecedence(t *testing.T) {
	t.Parallel()

	ambientEnv := map[string]string{
		testActionsIDTokenRequestURL:   "https://actions.example/oidc",
		testActionsIDTokenRequestToken: "req-token",
	}
	withAmbient := func(extra map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range ambientEnv {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	tests := []struct {
		name      string
		flagToken string
		env       map[string]string
		wantToken string
	}{
		{
			name:      "flag beats ambient",
			flagToken: "flag-token",
			env:       withAmbient(map[string]string{signing.EnvIdentityToken: "aggrega-token", signing.EnvSigstoreIDToken: "sigstore-token"}),
			wantToken: "flag-token",
		},
		{
			name:      "AGGREGA beats ambient",
			env:       withAmbient(map[string]string{signing.EnvIdentityToken: "aggrega-token", signing.EnvSigstoreIDToken: "sigstore-token"}),
			wantToken: "aggrega-token",
		},
		{
			name:      "SIGSTORE when no flag, no AGGREGA, no ambient",
			env:       map[string]string{signing.EnvSigstoreIDToken: "sigstore-token"},
			wantToken: "sigstore-token",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			idToken, source, err := resolveIdentityTokenSource(
				tc.flagToken,
				lookMap(tc.env),
				func() bool { return false },
				noAcquire(t),
				checkIDTokenSubject,
				nil,
			)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if source != nil {
				t.Fatalf("want a static resolution (nil source), got a live source")
			}
			if idToken != tc.wantToken {
				t.Fatalf("idToken = %q, want %q", idToken, tc.wantToken)
			}
		})
	}
}

// TestResolveIdentityTokenSourceAmbientOverSigstore proves that with the
// ambient endpoint available and no flag or AGGREGA_IDENTITY_TOKEN, the
// resolved value is the ambient live source — selected over a frozen
// SIGSTORE_ID_TOKEN — and that the source fetches a fresh token wired with the
// subject validator.
func TestResolveIdentityTokenSourceAmbientOverSigstore(t *testing.T) {
	t.Parallel()
	jwt := signingtest.SubjectfulIDToken(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("audience"); got != "sigstore" {
			t.Errorf("audience = %q, want sigstore", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer ambient-req-token" {
			t.Errorf("Authorization = %q, want Bearer ambient-req-token", got)
		}
		_, _ = w.Write([]byte(`{"value":"` + jwt + `"}`))
	}))
	defer srv.Close()

	env := map[string]string{
		testActionsIDTokenRequestURL:   srv.URL,
		testActionsIDTokenRequestToken: "ambient-req-token",
		signing.EnvSigstoreIDToken:     "frozen-sigstore-token",
	}
	idToken, source, err := resolveIdentityTokenSource(
		"",
		lookMap(env),
		func() bool { return false },
		noAcquire(t),
		checkIDTokenSubject,
		srv.Client(),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if idToken != "" {
		t.Fatalf("idToken = %q, want empty (ambient resolves to a source, not a string)", idToken)
	}
	if source == nil {
		t.Fatal("want the ambient live source, got nil")
	}
	got, ferr := source(context.Background())
	if ferr != nil {
		t.Fatalf("ambient source fetch: %v", ferr)
	}
	if got != jwt {
		t.Fatalf("ambient fetch returned %q, want the served token %q", got, jwt)
	}
}

// TestResolveIdentityTokenSourceInteractiveFallback proves that with none of
// the higher tiers set and an interactive terminal, one token is acquired once
// and returned as a static string.
func TestResolveIdentityTokenSourceInteractiveFallback(t *testing.T) {
	t.Parallel()
	idToken, source, err := resolveIdentityTokenSource(
		"",
		lookMap(map[string]string{}),
		func() bool { return true },
		func() (string, error) { return "acquired-token", nil },
		checkIDTokenSubject,
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if source != nil {
		t.Fatalf("want a static resolution from interactive acquisition, got a live source")
	}
	if idToken != "acquired-token" {
		t.Fatalf("idToken = %q, want acquired-token", idToken)
	}
}

// TestResolveIdentityTokenSourceLoudRefusal proves that with no token available
// and no interactive terminal, resolution refuses loudly rather than returning
// an empty token.
func TestResolveIdentityTokenSourceLoudRefusal(t *testing.T) {
	t.Parallel()
	idToken, source, err := resolveIdentityTokenSource(
		"",
		lookMap(map[string]string{}),
		func() bool { return false },
		func() (string, error) { return "", fmt.Errorf("acquire should not be called") },
		checkIDTokenSubject,
		nil,
	)
	if err == nil {
		t.Fatal("want a loud refusal error, got nil")
	}
	if idToken != "" || source != nil {
		t.Fatalf("want empty resolution on refusal, got idToken=%q source!=nil=%v", idToken, source != nil)
	}
}
