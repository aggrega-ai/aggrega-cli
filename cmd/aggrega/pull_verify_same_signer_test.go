// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// TestEnforcePluginSameSigner_HaltsOnMismatch proves the same-signer
// comparison directly: a plugin whose entity and .wasm artifact carry valid,
// individually-trusted signatures from DIFFERENT identities halts with
// ErrPluginSignerMismatch. The two signatures verify on their own (each row
// carries a Signature fact); it is the comparison of the matched signers — not
// "both verified under the policy" — that must reject the split signer. A
// keyless multi-identity policy where each signature matches a different
// trusted identity is exactly the case "both verified" would wrongly admit, so
// the comparison is fed two distinct matched identities here.
func TestEnforcePluginSameSigner_HaltsOnMismatch(t *testing.T) {
	const entityURI = "host.example.com/wasm/pipeline/patch-server"
	const wasmURI = "oci://host.example.com/wasm/pipeline/patch-server@sha256:abc"

	lf := &lockfile.Lockfile{
		Entities: []lockfile.Entity{
			{
				URI:       entityURI,
				Signature: &lockfile.VerifiedSignature{Mode: "keyless", Identity: "alice@example.com", Issuer: "https://accounts.google.com"},
			},
		},
		Plugins: []lockfile.Plugin{
			{
				URI:            wasmURI,
				ReferencedFrom: []string{entityURI},
				Signature:      &lockfile.VerifiedSignature{Mode: "keyless", Identity: "bob@example.com", Issuer: "https://accounts.google.com"},
			},
		},
	}

	err := enforcePluginSameSigner(lf)
	if !errors.Is(err, signing.ErrPluginSignerMismatch) {
		t.Fatalf("want ErrPluginSignerMismatch on a split signer, got %v", err)
	}
}

// TestEnforcePluginSameSigner_PassesOnMatch proves the comparison admits a
// plugin whose entity and .wasm carry signatures from the SAME matched signer.
func TestEnforcePluginSameSigner_PassesOnMatch(t *testing.T) {
	const entityURI = "host.example.com/wasm/pipeline/patch-server"
	const wasmURI = "oci://host.example.com/wasm/pipeline/patch-server@sha256:abc"

	sig := func() *lockfile.VerifiedSignature {
		return &lockfile.VerifiedSignature{Mode: "keyless", Identity: "alice@example.com", Issuer: "https://accounts.google.com"}
	}
	lf := &lockfile.Lockfile{
		Entities: []lockfile.Entity{{URI: entityURI, Signature: sig()}},
		Plugins:  []lockfile.Plugin{{URI: wasmURI, ReferencedFrom: []string{entityURI}, Signature: sig()}},
	}

	if err := enforcePluginSameSigner(lf); err != nil {
		t.Fatalf("want no error when both sides share a signer, got %v", err)
	}
}

// TestEnforcePluginSameSigner_KeyedMismatch proves the keyed leg: two keyed
// signatures whose pinned-key fingerprints differ are a signer mismatch, even
// though keyed signatures carry no certificate identity.
func TestEnforcePluginSameSigner_KeyedMismatch(t *testing.T) {
	const entityURI = "host.example.com/wasm/pipeline/patch-server"
	const wasmURI = "oci://host.example.com/wasm/pipeline/patch-server@sha256:abc"

	lf := &lockfile.Lockfile{
		Entities: []lockfile.Entity{
			{URI: entityURI, Signature: &lockfile.VerifiedSignature{Mode: "keyed", Identity: "keyed:sha256:aaaa"}},
		},
		Plugins: []lockfile.Plugin{
			{URI: wasmURI, ReferencedFrom: []string{entityURI}, Signature: &lockfile.VerifiedSignature{Mode: "keyed", Identity: "keyed:sha256:bbbb"}},
		},
	}

	if err := enforcePluginSameSigner(lf); !errors.Is(err, signing.ErrPluginSignerMismatch) {
		t.Fatalf("want ErrPluginSignerMismatch on differing keyed fingerprints, got %v", err)
	}
}

// TestEnforcePluginSameSigner_NoHaltWhenOneSideUnsigned proves the comparison
// runs only when BOTH sides carry a signature. A registry that does not require
// signatures may leave one or both rows unsigned; there is no signer to
// mismatch, so the same-signer check is a no-op. The policy gate
// (RequiresSignatures) — not this comparison — is what demands a signature be
// present.
func TestEnforcePluginSameSigner_NoHaltWhenOneSideUnsigned(t *testing.T) {
	const entityURI = "host.example.com/wasm/pipeline/patch-server"
	const wasmURI = "oci://host.example.com/wasm/pipeline/patch-server@sha256:abc"

	// Entity signed, .wasm unsigned.
	lf := &lockfile.Lockfile{
		Entities: []lockfile.Entity{
			{URI: entityURI, Signature: &lockfile.VerifiedSignature{Mode: "keyless", Identity: "alice@example.com", Issuer: "https://accounts.google.com"}},
		},
		Plugins: []lockfile.Plugin{
			{URI: wasmURI, ReferencedFrom: []string{entityURI}, Signature: nil},
		},
	}
	if err := enforcePluginSameSigner(lf); err != nil {
		t.Fatalf("want no error when the .wasm is unsigned, got %v", err)
	}

	// .wasm signed, entity unsigned.
	lf2 := &lockfile.Lockfile{
		Entities: []lockfile.Entity{{URI: entityURI, Signature: nil}},
		Plugins: []lockfile.Plugin{
			{URI: wasmURI, ReferencedFrom: []string{entityURI}, Signature: &lockfile.VerifiedSignature{Mode: "keyless", Identity: "alice@example.com", Issuer: "https://accounts.google.com"}},
		},
	}
	if err := enforcePluginSameSigner(lf2); err != nil {
		t.Fatalf("want no error when the entity is unsigned, got %v", err)
	}
}
