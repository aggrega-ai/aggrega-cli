// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestDeprecateSignerAmbientSelfMints proves a keyless deprecate in a simulated
// GitHub Actions environment — no explicit token, non-interactive — resolves the
// ambient OIDC endpoint as a live source and self-mints, exactly as a push does,
// rather than refusing. Before the token unification the string-only resolver
// had no ambient tier, so a non-TTY deprecate without an exported token refused;
// that this path now returns a signer whose live source fetches a fresh token
// from the ambient endpoint is the intended behavioral change.
func TestDeprecateSignerAmbientSelfMints(t *testing.T) {
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

	rw, err := workspaceconfig.Resolve(t.TempDir(), "", func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	env := map[string]string{
		testActionsIDTokenRequestURL:   srv.URL,
		testActionsIDTokenRequestToken: "ambient-req-token",
	}

	signer, err := deprecateSigner(DeprecateConfig{
		Reference:   "reg.example.com/x:1.0.0",
		Workspace:   rw,
		EnvLookup:   lookMap(env),
		IsTTY:       func() bool { return false },
		TokenGetter: noAcquire(t),
	})
	if err != nil {
		t.Fatalf("ambient deprecate refused rather than self-minting: %v", err)
	}
	ks, ok := signer.(*signing.KeylessSigner)
	if !ok {
		t.Fatalf("want a keyless signer, got %T", signer)
	}
	if ks.TokenSource == nil {
		t.Fatal("want a live ambient token source on the keyless signer, got nil")
	}
	got, ferr := ks.TokenSource(context.Background())
	if ferr != nil {
		t.Fatalf("ambient source fetch: %v", ferr)
	}
	if got != jwt {
		t.Fatalf("ambient fetch returned %q, want the served token %q", got, jwt)
	}
}
