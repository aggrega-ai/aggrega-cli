// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// craftJWT builds a syntactically valid JWT whose payload is the given JSON
// claims. The signature segment is a fixed placeholder; no consumer here
// verifies it.
func craftJWT(claimsJSON string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(claimsJSON))
	return header + "." + payload + ".sig"
}

// lookFrom returns an environment lookup backed by a fixed map.
func lookFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// TestKeylessSignFetchesTokenPerSign proves the token source is consulted once
// per Sign — two Sign calls trigger two fetches, so a long-running signing job
// never reuses a token that expired since it began. The source returns an
// empty token, so each Sign refuses at the guard before any network contact;
// the call count is the observable that proves per-sign fetching.
func TestKeylessSignFetchesTokenPerSign(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var calls int
	signer := &KeylessSigner{
		TokenSource: func(context.Context) (string, error) {
			calls++
			return "", nil
		},
	}
	for i := 0; i < 2; i++ {
		if _, _, err := signer.Sign(ctx, "sha256:abc"); !errors.Is(err, ErrKeylessLive) {
			t.Fatalf("Sign #%d: want ErrKeylessLive, got %v", i+1, err)
		}
	}
	if calls != 2 {
		t.Fatalf("token source called %d times across two Sign calls, want 2 (fresh per sign, not cached)", calls)
	}
}

// TestKeylessSignNilSourceRefuses proves a signer constructed with no token
// source refuses loudly rather than dereferencing a nil source.
func TestKeylessSignNilSourceRefuses(t *testing.T) {
	t.Parallel()
	signer := &KeylessSigner{}
	if _, _, err := signer.Sign(context.Background(), "sha256:abc"); !errors.Is(err, ErrKeylessLive) {
		t.Fatalf("want ErrKeylessLive for nil source, got %v", err)
	}
}

// TestStaticIdentityToken proves the static source yields its token unchanged
// on every call.
func TestStaticIdentityToken(t *testing.T) {
	t.Parallel()
	src := StaticIdentityToken("the-token")
	for i := 0; i < 2; i++ {
		got, err := src(context.Background())
		if err != nil {
			t.Fatalf("static source call %d: unexpected error %v", i+1, err)
		}
		if got != "the-token" {
			t.Fatalf("static source call %d: got %q, want %q", i+1, got, "the-token")
		}
	}
}

// TestGitHubActionsAmbientAvailable proves the detector is true only when both
// ambient environment variables are present.
func TestGitHubActionsAmbientAvailable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"both present", map[string]string{envActionsIDTokenRequestURL: "https://x", envActionsIDTokenRequestToken: "tok"}, true},
		{"url only", map[string]string{envActionsIDTokenRequestURL: "https://x"}, false},
		{"token only", map[string]string{envActionsIDTokenRequestToken: "tok"}, false},
		{"neither", map[string]string{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := GitHubActionsAmbientAvailable(lookFrom(tc.env)); got != tc.want {
				t.Fatalf("GitHubActionsAmbientAvailable = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestAmbientGitHubActionsSourceFetch proves the ambient source issues the
// documented request — the Sigstore audience in the query and the request
// token as a bearer credential — and returns the JSON body's value field. The
// injected validator runs against the fetched token.
func TestAmbientGitHubActionsSourceFetch(t *testing.T) {
	t.Parallel()
	jwt := craftJWT(`{"email":"dev@example.com","email_verified":true}`)

	var gotAudience, gotAuth, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAudience = r.URL.Query().Get("audience")
		gotAuth = r.Header.Get("Authorization")
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":"` + jwt + `"}`))
	}))
	defer srv.Close()

	var validated string
	src := AmbientGitHubActionsSource(
		lookFrom(map[string]string{
			envActionsIDTokenRequestURL:   srv.URL,
			envActionsIDTokenRequestToken: "req-token-123",
		}),
		srv.Client(),
		func(tok string) error { validated = tok; return nil },
	)

	got, err := src(context.Background())
	if err != nil {
		t.Fatalf("ambient source: unexpected error %v", err)
	}
	if got != jwt {
		t.Fatalf("token = %q, want the body's value field %q", got, jwt)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("request method = %q, want GET", gotMethod)
	}
	if gotAudience != "sigstore" {
		t.Errorf("audience query = %q, want %q", gotAudience, "sigstore")
	}
	if gotAuth != "Bearer req-token-123" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer req-token-123")
	}
	if validated != jwt {
		t.Errorf("validator saw %q, want the fetched token %q", validated, jwt)
	}
}

// TestAmbientGitHubActionsSourceValidatorRejects proves a validator error
// propagates and suppresses the token.
func TestAmbientGitHubActionsSourceValidatorRejects(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"value":"` + craftJWT(`{}`) + `"}`))
	}))
	defer srv.Close()

	wantErr := errors.New("no usable subject")
	src := AmbientGitHubActionsSource(
		lookFrom(map[string]string{
			envActionsIDTokenRequestURL:   srv.URL,
			envActionsIDTokenRequestToken: "req-token",
		}),
		srv.Client(),
		func(string) error { return wantErr },
	)
	if _, err := src(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("want the validator error to propagate, got %v", err)
	}
}

// TestAmbientGitHubActionsSourceNon2xx proves a non-2xx response is a
// descriptive error, not a silent empty token.
func TestAmbientGitHubActionsSourceNon2xx(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()

	src := AmbientGitHubActionsSource(
		lookFrom(map[string]string{
			envActionsIDTokenRequestURL:   srv.URL,
			envActionsIDTokenRequestToken: "req-token",
		}),
		srv.Client(),
		nil,
	)
	_, err := src(context.Background())
	if err == nil {
		t.Fatal("want an error for a non-2xx response, got nil")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error %q does not name the status code", err.Error())
	}
}

// TestAmbientGitHubActionsSourceMissingEnv proves a missing environment
// variable is a descriptive error rather than a request to an empty URL.
func TestAmbientGitHubActionsSourceMissingEnv(t *testing.T) {
	t.Parallel()
	src := AmbientGitHubActionsSource(lookFrom(map[string]string{}), nil, nil)
	if _, err := src(context.Background()); err == nil {
		t.Fatal("want an error when the ambient environment is absent, got nil")
	}
}
