// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"testing"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// signedPluginStore publishes a .wasm artifact to an in-memory store, signs
// its manifest digest with a fresh held key, attaches the bundle as a
// signature referrer, and returns the store, the manifest digest, and the
// pinned public-key PEM.
func signedPluginStore(t *testing.T) (store *memory.Store, manifestDigest string, pubPEM []byte) {
	t.Helper()
	ctx := context.Background()
	store = memory.New()

	artifact := []byte("\x00asm\x01\x00\x00\x00") // a minimal Wasm header stand-in
	dg, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, artifact)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	bundle, artifactType, err := signer.Sign(ctx, dg)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := oci.AttachReferrer(ctx, store, "1.0.0", artifactType, bundle); err != nil {
		t.Fatalf("AttachReferrer: %v", err)
	}
	return store, dg, signer.PublicKeyPEM()
}

func pluginTargetFor(store oras.ReadOnlyTarget, subjectRef string) pullverify.TargetFor {
	return func(pullverify.Entity) (oras.ReadOnlyTarget, string, error) {
		return store, subjectRef, nil
	}
}

// TestVerifyClosurePluginSignatureRecordsFact proves a signed Wasm plugin in
// the closure verifies through the SAME pull-verify pass as catalog entities —
// against the same per-registry trust policy — and the verified fact folds
// into the lockfile WasmPlugin row.
func TestVerifyClosurePluginSignatureRecordsFact(t *testing.T) {
	t.Parallel()
	store, dg, pubPEM := signedPluginStore(t)

	const uri = "keyed.example.com/wasm/linter/publishing-linter"
	cl := closure.Closure{
		WasmPlugins: []closure.ResolvedRef{
			{URI: uri, Digest: dg, ResolutionPath: "tier-3"},
		},
	}
	lf := &lockfile.Lockfile{
		WasmPlugins: []lockfile.WasmPlugin{{URI: uri, Digest: dg}},
	}
	wsCfg := workspaceconfig.Config{
		Registries: []workspaceconfig.RegistryConfig{
			{Host: "keyed.example.com", RequiresSignatures: true, PublicKey: string(pubPEM)},
		},
	}

	if err := verifyClosureSignatures(context.Background(), lf, cl, wsCfg, t.TempDir(), nil, pluginTargetFor(store, "1.0.0")); err != nil {
		t.Fatalf("verifyClosureSignatures: %v", err)
	}
	sig := lf.WasmPlugins[0].Signature
	if sig == nil {
		t.Fatal("plugin signature fact was not recorded on the lockfile row")
	}
	if sig.Mode != "keyed" {
		t.Errorf("mode = %q, want keyed", sig.Mode)
	}
	if sig.BundleDigest == "" {
		t.Error("recorded fact carries no cached bundle digest")
	}
}

// TestVerifyClosurePluginWrongKeyHalts proves a signature-requiring registry
// that pins the wrong key halts the pull for a plugin — the same Loud Guardrail
// the entity path produces.
func TestVerifyClosurePluginWrongKeyHalts(t *testing.T) {
	t.Parallel()
	store, dg, _ := signedPluginStore(t)
	_, _, otherPub := signedPluginStore(t)

	const uri = "keyed.example.com/wasm/linter/p"
	cl := closure.Closure{
		WasmPlugins: []closure.ResolvedRef{{URI: uri, Digest: dg, ResolutionPath: "tier-3"}},
	}
	lf := &lockfile.Lockfile{WasmPlugins: []lockfile.WasmPlugin{{URI: uri, Digest: dg}}}
	wsCfg := workspaceconfig.Config{
		Registries: []workspaceconfig.RegistryConfig{
			{Host: "keyed.example.com", RequiresSignatures: true, PublicKey: string(otherPub)},
		},
	}

	err := verifyClosureSignatures(context.Background(), lf, cl, wsCfg, t.TempDir(), nil, pluginTargetFor(store, "1.0.0"))
	if !errors.Is(err, signing.ErrSignatureVerificationFailed) {
		t.Fatalf("want ErrSignatureVerificationFailed, got %v", err)
	}
}

// TestVerifyClosurePluginMissingSignatureHalts proves a signature-requiring
// registry halts the pull when a plugin carries no signature referrer.
func TestVerifyClosurePluginMissingSignatureHalts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	dg, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, []byte("\x00asm\x01\x00\x00\x00"))
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	const uri = "req.example.com/wasm/linter/unsigned"
	cl := closure.Closure{
		WasmPlugins: []closure.ResolvedRef{{URI: uri, Digest: dg, ResolutionPath: "tier-3"}},
	}
	lf := &lockfile.Lockfile{WasmPlugins: []lockfile.WasmPlugin{{URI: uri, Digest: dg}}}
	wsCfg := workspaceconfig.Config{
		Registries: []workspaceconfig.RegistryConfig{
			{Host: "req.example.com", RequiresSignatures: true, PublicKey: "irrelevant"},
		},
	}

	err = verifyClosureSignatures(ctx, lf, cl, wsCfg, t.TempDir(), nil, pluginTargetFor(store, "1.0.0"))
	if !errors.Is(err, signing.ErrNoSignatureFound) {
		t.Fatalf("want ErrNoSignatureFound, got %v", err)
	}
}
