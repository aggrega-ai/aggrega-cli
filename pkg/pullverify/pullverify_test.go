// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullverify_test

import (
	"context"
	"errors"
	"testing"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// signedStore publishes an entity to an in-memory store, signs its
// manifest digest with a fresh held key, attaches the bundle as a
// signature referrer, and returns the store, the manifest digest, and the
// pinned public-key PEM.
func signedStore(t *testing.T) (store *memory.Store, manifestDigest string, pubPEM []byte) {
	t.Helper()
	ctx := context.Background()
	store = memory.New()

	artifact := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/api-server\n  version: 1.0.0\n")
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

func targetForStore(store oras.ReadOnlyTarget, subjectRef string) pullverify.TargetFor {
	return func(pullverify.Entity) (oras.ReadOnlyTarget, string, error) {
		return store, subjectRef, nil
	}
}

// TestVerifyKeyedRecordsFact proves the pull verify pass discovers a keyed
// signature referrer in an in-memory target, verifies it against the
// pinned key, and records a keyed verified fact with the cached bundle.
func TestVerifyKeyedRecordsFact(t *testing.T) {
	t.Parallel()
	store, dg, pubPEM := signedStore(t)

	cfg := workspaceconfig.Config{
		Registries: []workspaceconfig.RegistryConfig{
			{Host: "keyed.example.com", Signing: workspaceconfig.SigningConfig{Required: true}, PublicKey: string(pubPEM)},
		},
	}
	ent := pullverify.Entity{URI: "keyed.example.com/component/core/api-server", ManifestDigest: dg, Host: "keyed.example.com"}

	results, err := pullverify.Verify(context.Background(), []pullverify.Entity{ent}, pullverify.Options{
		Config: cfg,
		Target: targetForStore(store, "1.0.0"),
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	r := results[0]
	if r.Mode != "keyed" {
		t.Errorf("mode = %q, want keyed", r.Mode)
	}
	if r.BundleDigest == "" || len(r.Bundle) == 0 {
		t.Error("verified fact should carry a cached bundle + digest")
	}
	if r.URI != ent.URI {
		t.Errorf("result URI = %q, want %q", r.URI, ent.URI)
	}
	// A keyed verified fact records a pinned-key fingerprint as its signer
	// identity (a keyed bundle carries no certificate), which the same-signer
	// check compares across a plugin's entity and .wasm. The fingerprint is
	// stable across runs so two artifacts signed under the one pinned key
	// compare equal.
	if r.Identity == "" {
		t.Error("keyed verified fact should record a pinned-key fingerprint as Identity")
	}
	stable, err := pullverify.Verify(context.Background(), []pullverify.Entity{ent}, pullverify.Options{
		Config: cfg,
		Target: targetForStore(store, "1.0.0"),
	})
	if err != nil {
		t.Fatalf("Verify (stable): %v", err)
	}
	if stable[0].Identity != r.Identity {
		t.Errorf("keyed fingerprint not stable across runs: %q vs %q", stable[0].Identity, r.Identity)
	}
}

// TestVerifyKeyedRejectsWrongKey proves a registry that pins the wrong key
// fails the (non-advisory) verify and halts.
func TestVerifyKeyedRejectsWrongKey(t *testing.T) {
	t.Parallel()
	store, dg, _ := signedStore(t)
	_, _, otherPub := signedStore(t)

	cfg := workspaceconfig.Config{
		Registries: []workspaceconfig.RegistryConfig{
			{Host: "keyed.example.com", Signing: workspaceconfig.SigningConfig{Required: true}, PublicKey: string(otherPub)},
		},
	}
	ent := pullverify.Entity{URI: "keyed.example.com/x", ManifestDigest: dg, Host: "keyed.example.com"}
	_, err := pullverify.Verify(context.Background(), []pullverify.Entity{ent}, pullverify.Options{
		Config: cfg,
		Target: targetForStore(store, "1.0.0"),
	})
	if !errors.Is(err, signing.ErrSignatureVerificationFailed) {
		t.Fatalf("want ErrSignatureVerificationFailed, got %v", err)
	}
}

// TestVerifyMissingSignatureHaltsWhenRequired proves a registry that
// requires signatures halts with ErrNoSignatureFound when the artifact
// carries no signature referrer.
func TestVerifyMissingSignatureHaltsWhenRequired(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	dg, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, []byte("kind: Component\n"))
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	cfg := workspaceconfig.Config{
		Registries: []workspaceconfig.RegistryConfig{
			{Host: "req.example.com", Signing: workspaceconfig.SigningConfig{Required: true}, PublicKey: "irrelevant"},
		},
	}
	ent := pullverify.Entity{URI: "req.example.com/x", ManifestDigest: dg, Host: "req.example.com"}
	_, verr := pullverify.Verify(ctx, []pullverify.Entity{ent}, pullverify.Options{
		Config: cfg,
		Target: targetForStore(store, "1.0.0"),
	})
	if !errors.Is(verr, signing.ErrNoSignatureFound) {
		t.Fatalf("want ErrNoSignatureFound, got %v", verr)
	}
}

// TestVerifyMissingSignatureToleratedWhenUnknown proves an unknown,
// non-mandatory registry with no signature is tolerated (verify-if-present
// default) and produces no fact.
func TestVerifyMissingSignatureToleratedWhenUnknown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	dg, _ := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, []byte("kind: Component\n"))
	ent := pullverify.Entity{URI: "unknown.example.com/x", ManifestDigest: dg, Host: "unknown.example.com"}
	results, verr := pullverify.Verify(ctx, []pullverify.Entity{ent}, pullverify.Options{
		Config: workspaceconfig.Config{},
		Target: targetForStore(store, "1.0.0"),
	})
	if verr != nil {
		t.Fatalf("unknown registry with no signature should not halt: %v", verr)
	}
	if len(results) != 0 {
		t.Errorf("expected no verified facts, got %d", len(results))
	}
}

// TestVerifyOrgPolicyRequiresSignatureForKind proves the org layer: a registry
// with NO per-registry signing.required still rejects an unsigned artifact
// of a kind the workspace signing policy scopes in — the org policy, not the
// registry, forces the requirement.
func TestVerifyOrgPolicyRequiresSignatureForKind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	dg, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, []byte("kind: Component\n"))
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	cfg := workspaceconfig.Config{
		Signing: workspaceconfig.SigningConfig{Required: true, Kinds: []string{"Component"}},
	}
	ent := pullverify.Entity{URI: "org.example.com/component/x", ManifestDigest: dg, Host: "org.example.com", Kind: "Component"}
	_, verr := pullverify.Verify(ctx, []pullverify.Entity{ent}, pullverify.Options{
		Config: cfg,
		Target: targetForStore(store, "1.0.0"),
	})
	if !errors.Is(verr, signing.ErrNoSignatureFound) {
		t.Fatalf("an unsigned in-scope kind under the org policy must halt with ErrNoSignatureFound, got %v", verr)
	}
}

// TestVerifyOrgPolicyOutOfScopeToleratesMissing proves the org policy is
// kind-scoped: an artifact whose kind is not in signing.kinds is tolerated when
// unsigned even with the policy on.
func TestVerifyOrgPolicyOutOfScopeToleratesMissing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	dg, _ := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, []byte("kind: Component\n"))
	cfg := workspaceconfig.Config{
		Signing: workspaceconfig.SigningConfig{Required: true, Kinds: []string{"Plugin"}},
	}
	ent := pullverify.Entity{URI: "org.example.com/component/x", ManifestDigest: dg, Host: "org.example.com", Kind: "Component"}
	results, verr := pullverify.Verify(ctx, []pullverify.Entity{ent}, pullverify.Options{
		Config: cfg,
		Target: targetForStore(store, "1.0.0"),
	})
	if verr != nil {
		t.Fatalf("an out-of-scope kind must not halt, got %v", verr)
	}
	if len(results) != 0 {
		t.Errorf("out-of-scope unsigned kind should record no fact, got %d", len(results))
	}
}

// TestVerifyOrgPolicyFailSafeEmptyKind proves the fail-safe: when the org policy
// is active but an artifact's kind cannot be classified, the pass does not
// silently exempt it — it requires a signature and halts on a missing one.
func TestVerifyOrgPolicyFailSafeEmptyKind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	dg, _ := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, []byte("kind: Component\n"))
	cfg := workspaceconfig.Config{
		Signing: workspaceconfig.SigningConfig{Required: true},
	}
	ent := pullverify.Entity{URI: "org.example.com/x", ManifestDigest: dg, Host: "org.example.com", Kind: ""}
	_, verr := pullverify.Verify(ctx, []pullverify.Entity{ent}, pullverify.Options{
		Config: cfg,
		Target: targetForStore(store, "1.0.0"),
	})
	if !errors.Is(verr, signing.ErrNoSignatureFound) {
		t.Fatalf("an unclassifiable artifact under an active org policy must halt with ErrNoSignatureFound, got %v", verr)
	}
}

// TestVerifyAdvisoryDowngradesFailure proves an advisory registry
// downgrades a verification failure to a warning and continues.
func TestVerifyAdvisoryDowngradesFailure(t *testing.T) {
	t.Parallel()
	store, dg, _ := signedStore(t)
	_, _, otherPub := signedStore(t)
	var warned bool
	cfg := workspaceconfig.Config{
		Registries: []workspaceconfig.RegistryConfig{
			{Host: "adv.example.com", Signing: workspaceconfig.SigningConfig{Required: true}, Advisory: true, PublicKey: string(otherPub)},
		},
	}
	ent := pullverify.Entity{URI: "adv.example.com/x", ManifestDigest: dg, Host: "adv.example.com"}
	results, err := pullverify.Verify(context.Background(), []pullverify.Entity{ent}, pullverify.Options{
		Config: cfg,
		Target: targetForStore(store, "1.0.0"),
		Warn:   func(string) { warned = true },
	})
	if err != nil {
		t.Fatalf("advisory failure should not halt: %v", err)
	}
	if !warned {
		t.Error("advisory failure should warn")
	}
	if len(results) != 0 {
		t.Errorf("advisory failure should record no verified fact, got %d", len(results))
	}
}

// TestVerifyGlobalSkipIsLoud proves the global opt-out disables
// verification and emits a loud warning.
func TestVerifyGlobalSkipIsLoud(t *testing.T) {
	t.Parallel()
	store, dg, pubPEM := signedStore(t)
	var warning string
	cfg := workspaceconfig.Config{
		SkipSignatureVerification: true,
		Registries: []workspaceconfig.RegistryConfig{
			{Host: "keyed.example.com", Signing: workspaceconfig.SigningConfig{Required: true}, PublicKey: string(pubPEM)},
		},
	}
	ent := pullverify.Entity{URI: "keyed.example.com/x", ManifestDigest: dg, Host: "keyed.example.com"}
	results, err := pullverify.Verify(context.Background(), []pullverify.Entity{ent}, pullverify.Options{
		Config: cfg,
		Target: targetForStore(store, "1.0.0"),
		Warn:   func(s string) { warning = s },
	})
	if err != nil {
		t.Fatalf("global skip should not error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("global skip should record nothing, got %d", len(results))
	}
	if warning == "" {
		t.Error("global skip must be loud (warn)")
	}
}

// TestVerifyPerRegistrySkip proves a per-registry skip disables
// verification for that host with a warning.
func TestVerifyPerRegistrySkip(t *testing.T) {
	t.Parallel()
	store, dg, pubPEM := signedStore(t)
	var warned bool
	cfg := workspaceconfig.Config{
		Registries: []workspaceconfig.RegistryConfig{
			{Host: "keyed.example.com", Signing: workspaceconfig.SigningConfig{Required: true}, SkipSignatureVerification: true, PublicKey: string(pubPEM)},
		},
	}
	ent := pullverify.Entity{URI: "keyed.example.com/x", ManifestDigest: dg, Host: "keyed.example.com"}
	results, err := pullverify.Verify(context.Background(), []pullverify.Entity{ent}, pullverify.Options{
		Config: cfg,
		Target: targetForStore(store, "1.0.0"),
		Warn:   func(string) { warned = true },
	})
	if err != nil {
		t.Fatalf("per-registry skip should not error: %v", err)
	}
	if !warned || len(results) != 0 {
		t.Errorf("per-registry skip should warn and record nothing (warned=%v results=%d)", warned, len(results))
	}
}

// TestVerifyPerRegistryRequiredRefusesUnsigned proves the per-host half of the
// one trust stance on the verify side: a host whose own signing block sets
// required refuses an unsigned in-scope artifact, exactly as the top-level
// policy does — one name, one job, scope from placement.
func TestVerifyPerRegistryRequiredRefusesUnsigned(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	dg, _ := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, []byte("kind: Component\n"))
	cfg := workspaceconfig.Config{
		Registries: []workspaceconfig.RegistryConfig{
			{Host: "reg.example.com", Signing: workspaceconfig.SigningConfig{Required: true, Kinds: []string{"Component"}}},
		},
	}
	ent := pullverify.Entity{URI: "reg.example.com/component/x", ManifestDigest: dg, Host: "reg.example.com", Kind: "Component"}
	_, verr := pullverify.Verify(ctx, []pullverify.Entity{ent}, pullverify.Options{
		Config: cfg,
		Target: targetForStore(store, "1.0.0"),
	})
	if !errors.Is(verr, signing.ErrNoSignatureFound) {
		t.Fatalf("per-registry required must halt an unsigned in-scope artifact with ErrNoSignatureFound, got %v", verr)
	}
}

// TestVerifyOrgRequiredImmuneToPerHostSkip proves the top-level required policy
// is immune to a per-host break-glass skip: a host that sets
// skip_signature_verification still has its in-scope artifact verified when the
// org policy requires a signature for that kind. The host pins the CORRECT key,
// so verification runs and records the fact rather than being skipped silently.
func TestVerifyOrgRequiredImmuneToPerHostSkip(t *testing.T) {
	t.Parallel()
	store, dg, pubPEM := signedStore(t)
	var warned bool
	cfg := workspaceconfig.Config{
		Signing: workspaceconfig.SigningConfig{Required: true},
		Registries: []workspaceconfig.RegistryConfig{
			{Host: "keyed.example.com", SkipSignatureVerification: true, PublicKey: string(pubPEM)},
		},
	}
	ent := pullverify.Entity{URI: "keyed.example.com/x", ManifestDigest: dg, Host: "keyed.example.com", Kind: "Component"}
	results, err := pullverify.Verify(context.Background(), []pullverify.Entity{ent}, pullverify.Options{
		Config: cfg,
		Target: targetForStore(store, "1.0.0"),
		Warn:   func(string) { warned = true },
	})
	if err != nil {
		t.Fatalf("org-required verification of a correctly-signed artifact must succeed despite per-host skip: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("org-required host must be verified (not skipped) despite per-host skip; got %d results", len(results))
	}
	if warned {
		t.Error("a successful org-required verification should not emit the per-host skip warning")
	}
}

// TestVerifyOrgRequiredEnforcedDespitePerHostSkip proves the immunity actually
// ENFORCES: with the org policy on, a host that sets skip_signature_verification
// but pins the WRONG key still HALTS the pull, because verification runs rather
// than being skipped. Were the per-host skip able to defeat the org policy, this
// would return no error.
func TestVerifyOrgRequiredEnforcedDespitePerHostSkip(t *testing.T) {
	t.Parallel()
	store, dg, _ := signedStore(t)
	_, _, wrongPub := signedStore(t)
	cfg := workspaceconfig.Config{
		Signing: workspaceconfig.SigningConfig{Required: true},
		Registries: []workspaceconfig.RegistryConfig{
			{Host: "keyed.example.com", SkipSignatureVerification: true, PublicKey: string(wrongPub)},
		},
	}
	ent := pullverify.Entity{URI: "keyed.example.com/x", ManifestDigest: dg, Host: "keyed.example.com", Kind: "Component"}
	_, err := pullverify.Verify(context.Background(), []pullverify.Entity{ent}, pullverify.Options{
		Config: cfg,
		Target: targetForStore(store, "1.0.0"),
	})
	if err == nil {
		t.Fatal("org-required verification must halt on a wrong-key host despite per-host skip; got nil error")
	}
}

// TestVerifyGlobalSkipDefeatsOrgRequired proves the ONLY switch that can disable
// an org-required verification is the global skip_signature_verification: with it
// set, even an org-required in-scope artifact from a wrong-key host is not
// verified, and the disablement is loud.
func TestVerifyGlobalSkipDefeatsOrgRequired(t *testing.T) {
	t.Parallel()
	store, dg, _ := signedStore(t)
	_, _, wrongPub := signedStore(t)
	var warning string
	cfg := workspaceconfig.Config{
		SkipSignatureVerification: true,
		Signing:                   workspaceconfig.SigningConfig{Required: true},
		Registries: []workspaceconfig.RegistryConfig{
			{Host: "keyed.example.com", PublicKey: string(wrongPub)},
		},
	}
	ent := pullverify.Entity{URI: "keyed.example.com/x", ManifestDigest: dg, Host: "keyed.example.com", Kind: "Component"}
	results, err := pullverify.Verify(context.Background(), []pullverify.Entity{ent}, pullverify.Options{
		Config: cfg,
		Target: targetForStore(store, "1.0.0"),
		Warn:   func(s string) { warning = s },
	})
	if err != nil {
		t.Fatalf("global skip must short-circuit even org-required verification: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("global skip should record nothing, got %d", len(results))
	}
	if warning == "" {
		t.Error("global skip must be loud")
	}
}
