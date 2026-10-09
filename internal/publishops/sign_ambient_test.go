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

// TestSignAmbientSelfMints proves a keyless sign batch in a simulated GitHub
// Actions environment — no explicit token, non-interactive — resolves the
// ambient OIDC endpoint as a live source and self-mints, exactly as a push does,
// rather than refusing. The batch's credential is resolved once and every
// reference's signer carries that live source, so every signature mints a fresh
// token from the ambient endpoint.
func TestSignAmbientSelfMints(t *testing.T) {
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

	cfg := SignConfig{
		References:  []string{"reg.example.com/x:1.0.0", "other.example.com/y:1.0.0"},
		Workspace:   rw,
		EnvLookup:   lookMap(env),
		IsTTY:       func() bool { return false },
		TokenGetter: noAcquire(t),
	}
	cred, err := resolveCredential(cfg.signerRequest(), cfg.References, nil)
	if err != nil {
		t.Fatalf("ambient sign refused rather than self-minting: %v", err)
	}
	if cred.tokenSource == nil {
		t.Fatal("want the ambient live source held by the batch's credential, got nil")
	}
	if cred.idToken != "" {
		t.Fatalf("want an empty static token on the ambient path, got %q", cred.idToken)
	}

	for _, ref := range cfg.References {
		signer, serr := cred.signerFor(ref)
		if serr != nil {
			t.Fatalf("signerFor %s: %v", ref, serr)
		}
		ks, ok := signer.(*signing.KeylessSigner)
		if !ok {
			t.Fatalf("%s: want a keyless signer, got %T", ref, signer)
		}
		if ks.TokenSource == nil {
			t.Fatalf("%s: want the live ambient source threaded to the reference's signer, got nil", ref)
		}
		got, ferr := ks.TokenSource(context.Background())
		if ferr != nil {
			t.Fatalf("%s: ambient source fetch: %v", ref, ferr)
		}
		if got != jwt {
			t.Fatalf("%s: ambient fetch returned %q, want the served token %q", ref, got, jwt)
		}
	}
}
