// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
)

// writeKeyPEM generates a fresh keyed PEM and writes it to a temp file,
// returning the path and the matching public-key PEM for verification.
func writeKeyPEM(t *testing.T) (keyPath string, pubPEM []byte) {
	t.Helper()
	priv, pub, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	keyPath = filepath.Join(t.TempDir(), "signing-key.pem")
	if err := os.WriteFile(keyPath, priv, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return keyPath, pub
}

// TestPushKeyedEndToEndSignsBeforePush exercises the REAL keyed production
// path: a --key push to a signature-requiring registry constructs a real
// keyed signer from the flag (no FakeSigner injection), signs the proposed
// digest before push, pushes, and attaches a verifiable referrer.
func TestPushKeyedEndToEndSignsBeforePush(t *testing.T) {
	ctx := context.Background()
	entityPath, wsRoot := writeSigningWorkspace(t, "secure.example.com", true)
	store := memory.New()
	keyPath, pubPEM := writeKeyPEM(t)

	sign := true
	if err := pushRun(pushConfig{
		ws:         resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot}),
		EntityPath: entityPath,
		Registry:   "secure.example.com",
		PushTarget: store,
		Sign:       &sign,
		KeyPath:    keyPath,
		// The keyed path must never reach token acquisition: a TokenGetter
		// that fails the test proves the keyed branch skips it entirely.
		TokenGetter: func() (string, error) { t.Fatal("keyed path must not acquire an identity token"); return "", nil },
		// Signer left nil: production constructs it via buildSigner.
	}); err != nil {
		t.Fatalf("pushRun: %v", err)
	}

	// The artifact is published.
	digest, exists, err := oci.Resolve(ctx, store, "1.0.0")
	if err != nil || !exists {
		t.Fatalf("Resolve: exists=%v err=%v", exists, err)
	}

	// A signature referrer is attached and verifies against the public key
	// for the published digest — proving the pre-push signature commits to
	// the bytes that landed.
	refs, err := oci.ListReferrers(ctx, store, "1.0.0", signing.BundleMediaType)
	if err != nil || len(refs) != 1 {
		t.Fatalf("ListReferrers: refs=%d err=%v", len(refs), err)
	}
	payload, err := oci.FetchReferrerPayload(ctx, store, refs[0])
	if err != nil {
		t.Fatalf("FetchReferrerPayload: %v", err)
	}
	if err := signing.VerifyKeyedBundle(payload, pubPEM, digest); err != nil {
		t.Fatalf("attached bundle does not verify against the published digest: %v", err)
	}
}

// TestPushSignerErrorLeavesZeroBytes is the no-unsigned-leak regression: a
// signer that errors must refuse the push BEFORE any bytes reach the target.
func TestPushSignerErrorLeavesZeroBytes(t *testing.T) {
	ctx := context.Background()
	entityPath, wsRoot := writeSigningWorkspace(t, "secure.example.com", true)
	store := memory.New()

	sign := true
	err := pushRun(pushConfig{
		ws:         resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot}),
		EntityPath: entityPath,
		Registry:   "secure.example.com",
		PushTarget: store,
		Sign:       &sign,
		Signer:     &signingtest.FakeSigner{Err: errors.New("boom")},
	})
	if !errors.Is(err, signing.ErrUnsignedPushRefused()) {
		t.Fatalf("want ErrUnsignedPushRefused, got %v", err)
	}
	if _, exists, _ := oci.Resolve(ctx, store, "1.0.0"); exists {
		t.Error("signer failure let bytes through: the tag exists after a refused push")
	}
}

// TestPushKeyedMissingKeyLeavesZeroBytes proves a --key push whose key file
// does not exist refuses before push (signer construction fails) with zero
// bytes pushed.
func TestPushKeyedMissingKeyLeavesZeroBytes(t *testing.T) {
	ctx := context.Background()
	entityPath, wsRoot := writeSigningWorkspace(t, "secure.example.com", true)
	store := memory.New()

	sign := true
	err := pushRun(pushConfig{
		ws:         resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot}),
		EntityPath: entityPath,
		Registry:   "secure.example.com",
		PushTarget: store,
		Sign:       &sign,
		KeyPath:    filepath.Join(t.TempDir(), "does-not-exist.pem"),
	})
	if !errors.Is(err, signing.ErrUnsignedPushRefused()) {
		t.Fatalf("want ErrUnsignedPushRefused, got %v", err)
	}
	if _, exists, _ := oci.Resolve(ctx, store, "1.0.0"); exists {
		t.Error("construct failure let bytes through: the tag exists after a refused push")
	}
}

// TestPushKeylessNoTokenNoTTYLeavesZeroBytes proves a keyless --sign push with
// no identity token and no interactive terminal refuses BEFORE push (the
// command-layer loud refuse), so zero bytes land — consistent with the
// sign-before-push guarantee. No browser is ever opened: the TokenGetter seam
// fails the test if invoked.
func TestPushKeylessNoTokenNoTTYLeavesZeroBytes(t *testing.T) {
	ctx := context.Background()
	entityPath, wsRoot := writeSigningWorkspace(t, "secure.example.com", true)
	store := memory.New()

	sign := true
	err := pushRun(pushConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot}),
		EntityPath:  entityPath,
		Registry:    "secure.example.com",
		PushTarget:  store,
		Sign:        &sign,
		EnvLookup:   func(string) string { return "" },
		IsTTY:       func() bool { return false },
		TokenGetter: func() (string, error) { t.Fatal("TokenGetter must not be called without a TTY"); return "", nil },
		// No KeyPath and no IDToken, no TTY → loud refuse before push.
	})
	if !errors.Is(err, signing.ErrUnsignedPushRefused()) {
		t.Fatalf("want ErrUnsignedPushRefused, got %v", err)
	}
	if _, exists, _ := oci.Resolve(ctx, store, "1.0.0"); exists {
		t.Error("keyless-no-token let bytes through: the tag exists after a refused push")
	}
}

// TestBuildSignerDefaultsToPublicGood proves that with no URL flags or
// config, buildSigner produces a keyless signer that defaults Fulcio and Rekor
// to the public-good Sigstore instances. The RFC3161 timestamp authority is
// NOT defaulted — it is opt-in, so TimestampURL stays empty unless supplied;
// the keyless trusted timestamp comes from Rekor's transparency-log inclusion.
func TestBuildSignerDefaultsToPublicGood(t *testing.T) {
	t.Parallel()
	signer, err := buildSigner(signerParams{IDToken: subjectfulIDToken(t)})
	if err != nil {
		t.Fatalf("buildSigner: %v", err)
	}
	keyless, ok := signer.(*signing.KeylessSigner)
	if !ok {
		t.Fatalf("buildSigner returned %T, want *KeylessSigner", signer)
	}
	if keyless.FulcioURL != signing.DefaultFulcioURL {
		t.Errorf("FulcioURL = %q, want default %q", keyless.FulcioURL, signing.DefaultFulcioURL)
	}
	if keyless.RekorURL != signing.DefaultRekorURL {
		t.Errorf("RekorURL = %q, want default %q", keyless.RekorURL, signing.DefaultRekorURL)
	}
	if keyless.TimestampURL != "" {
		t.Errorf("TimestampURL = %q, want empty (timestamp authority is opt-in, not defaulted)", keyless.TimestampURL)
	}
}

// TestBuildSignerTimestampOptIn proves the RFC3161 timestamp authority is
// honored when supplied (the opt-in path), while Fulcio and Rekor still take
// their public-good defaults for the unset fields.
func TestBuildSignerTimestampOptIn(t *testing.T) {
	t.Parallel()
	signer, err := buildSigner(signerParams{
		IDToken:      subjectfulIDToken(t),
		TimestampURL: "https://tsa.private/api/v1/timestamp",
	})
	if err != nil {
		t.Fatalf("buildSigner: %v", err)
	}
	keyless := signer.(*signing.KeylessSigner)
	if keyless.TimestampURL != "https://tsa.private/api/v1/timestamp" {
		t.Errorf("TimestampURL = %q, want the explicitly-supplied endpoint", keyless.TimestampURL)
	}
	if keyless.FulcioURL != signing.DefaultFulcioURL {
		t.Errorf("FulcioURL = %q, want default for the unset field", keyless.FulcioURL)
	}
	if keyless.RekorURL != signing.DefaultRekorURL {
		t.Errorf("RekorURL = %q, want default for the unset field", keyless.RekorURL)
	}
}

// TestBuildSignerFlagOverridesDefault proves an explicit URL beats the
// public-good default in buildSigner.
func TestBuildSignerFlagOverridesDefault(t *testing.T) {
	t.Parallel()
	signer, err := buildSigner(signerParams{
		IDToken:   subjectfulIDToken(t),
		FulcioURL: "https://fulcio.private",
	})
	if err != nil {
		t.Fatalf("buildSigner: %v", err)
	}
	keyless := signer.(*signing.KeylessSigner)
	if keyless.FulcioURL != "https://fulcio.private" {
		t.Errorf("FulcioURL = %q, want explicit override", keyless.FulcioURL)
	}
	if keyless.RekorURL != signing.DefaultRekorURL {
		t.Errorf("RekorURL = %q, want default for the unset field", keyless.RekorURL)
	}
}

// TestSignAndPushShareOneSigningPath is the single-source guard: both `sign`
// and `push`, given the same keyed key path, produce a signature referrer
// that verifies against the same public key and the same digest. Both verbs
// route through the one buildSigner construction and the one sign atom — the
// old per-verb signer constructors are deleted, so there is no second path
// either could diverge onto. (ECDSA signatures are randomised, so the bundle
// bytes are not identical; the binding to key+digest is the invariant.)
func TestSignAndPushShareOneSigningPath(t *testing.T) {
	ctx := context.Background()
	keyPath, pubPEM := writeKeyPEM(t)

	// Push a keyed-signed artifact; capture its digest and signature bytes.
	pushEntity, pushWSRoot := writeSigningWorkspace(t, "secure.example.com", true)
	pushStore := memory.New()
	sign := true
	if err := pushRun(pushConfig{
		ws:         resolveTempWorkspace(t, tempWorkspaceOptions{Root: pushWSRoot}),
		EntityPath: pushEntity,
		Registry:   "secure.example.com",
		PushTarget: pushStore,
		Sign:       &sign,
		KeyPath:    keyPath,
	}); err != nil {
		t.Fatalf("pushRun: %v", err)
	}
	pushDigest, _, err := oci.Resolve(ctx, pushStore, "1.0.0")
	if err != nil {
		t.Fatalf("Resolve push: %v", err)
	}

	// Sign an already-published artifact whose digest equals pushDigest,
	// through the sign verb's construction path, against the same key.
	signStore := memory.New()
	// Re-push the identical bytes into a second store so the digest matches.
	if err := pushRun(pushConfig{
		ws:         resolveTempWorkspace(t, tempWorkspaceOptions{Root: pushWSRoot}),
		EntityPath: pushEntity,
		Registry:   "example.com",
		PushTarget: signStore,
	}); err != nil {
		t.Fatalf("seed push: %v", err)
	}
	signDigestVal, _, err := oci.Resolve(ctx, signStore, "1.0.0")
	if err != nil {
		t.Fatalf("Resolve seed: %v", err)
	}
	if signDigestVal != pushDigest {
		t.Fatalf("seed digest %q != push digest %q", signDigestVal, pushDigest)
	}
	if err := signRun(signConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: pushWSRoot}),
		References:  []string{"secure.example.com/component/core/api-server:1.0.0"},
		SubjectRefs: map[string]string{"secure.example.com/component/core/api-server:1.0.0": "1.0.0"},
		Target:      signStore,
		KeyPath:     keyPath,
	}); err != nil {
		t.Fatalf("signRun: %v", err)
	}

	// Both attached bundles verify against the same key and digest — proof
	// both verbs signed the identical digest through one keyed construction.
	verifyOne := func(store *memory.Store, label string) {
		refs, err := oci.ListReferrers(ctx, store, "1.0.0", signing.BundleMediaType)
		if err != nil || len(refs) != 1 {
			t.Fatalf("%s ListReferrers: refs=%d err=%v", label, len(refs), err)
		}
		payload, err := oci.FetchReferrerPayload(ctx, store, refs[0])
		if err != nil {
			t.Fatalf("%s FetchReferrerPayload: %v", label, err)
		}
		if err := signing.VerifyKeyedBundle(payload, pubPEM, pushDigest); err != nil {
			t.Fatalf("%s bundle does not verify: %v", label, err)
		}
	}
	verifyOne(pushStore, "push")
	verifyOne(signStore, "sign")
}
