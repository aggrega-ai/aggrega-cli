// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestSignAmbientSelfMints proves a keyless sign batch in a simulated GitHub
// Actions environment — no explicit token, non-interactive — resolves the
// ambient OIDC endpoint as a live source and self-mints, exactly as a push does,
// rather than refusing. The batch resolves once (prepare) and the per-reference
// signer reuses that live source, so every signature mints a fresh token from
// the ambient endpoint. Before the token unification a non-TTY sign without an
// exported token refused; self-minting here is the intended behavioral change.
func TestSignAmbientSelfMints(t *testing.T) {
	t.Parallel()
	jwt := subjectfulIDToken(t)
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

	b := &batchSigner{cfg: signConfig{
		References:  []string{"reg.example.com/x:1.0.0"},
		ws:          rw,
		EnvLookup:   lookMap(env),
		IsTTY:       func() bool { return false },
		TokenGetter: noAcquire(t),
	}}
	if err := b.prepare(); err != nil {
		t.Fatalf("ambient sign refused rather than self-minting: %v", err)
	}
	if b.tokenSource == nil {
		t.Fatal("want the ambient live source stored on the batch, got nil")
	}
	if b.idToken != "" {
		t.Fatalf("want an empty static token on the ambient path, got %q", b.idToken)
	}

	signer, err := b.buildRefSigner(b.cfg.References[0])
	if err != nil {
		t.Fatalf("buildRefSigner: %v", err)
	}
	ks, ok := signer.(*signing.KeylessSigner)
	if !ok {
		t.Fatalf("want a keyless signer, got %T", signer)
	}
	if ks.TokenSource == nil {
		t.Fatal("want the live ambient source threaded to the per-reference signer, got nil")
	}
	got, ferr := ks.TokenSource(context.Background())
	if ferr != nil {
		t.Fatalf("ambient source fetch: %v", ferr)
	}
	if got != jwt {
		t.Fatalf("ambient fetch returned %q, want the served token %q", got, jwt)
	}
}
