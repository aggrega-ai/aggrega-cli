// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// verdictGateVerify drives a host-less verdict-socket gate plugin (a linter or
// policy plugin configured cache-not-pin) through the exact pull-time
// verification path cacheVerdictSocketPlugins uses for such a gate:
// verifyCachedVerdictSocketPlugin -> verifyFetchedPlugin. A verdict gate never
// enters the closure, so this is the ONLY pass that verifies it — the closure
// signature pass the pinned tier runs never sees it.
//
// The gate's URI is a host-less bare identity; the harness sets DefaultRegistry
// so verifyCachedVerdictSocketPlugin binds the identity to a registry location
// (via catalog.BindEntityLocation) and discovers signature referrers there, the
// same binding the pinned plugin-entity signature uses. The LazyLookup is built
// with an empty cache dir so PluginFromCacheByURI is a clean miss: the
// host-function inputs stay zero and this pass isolates the registry-signature
// verdict on the verdict tier.
func verdictGateVerify(t *testing.T, store *memory.Store, wsCfg workspaceconfig.Config, uri, manifestDigest string) error {
	t.Helper()
	lazy := catalog.NewLazyLookup(t.TempDir(), "", nil, nil)
	_, err := verifyCachedVerdictSocketPlugin(
		context.Background(), wsCfg, lazy, uri, manifestDigest,
		t.TempDir(), "cache", pluginTargetFor(store, "1.0.0"), io.Discard,
	)
	return err
}

// keyedVerdictConfig builds a require-signatures workspace config that binds the
// gate's host-less identity to the given registry host and pins pubPEM as the
// trusted key. DefaultRegistry drives the identity->location binding the verdict
// verify performs.
func keyedVerdictConfig(host string, pubPEM []byte, required bool) workspaceconfig.Config {
	return workspaceconfig.Config{
		DefaultRegistry: host,
		Registries: []workspaceconfig.RegistryConfig{
			{Host: host, Signing: workspaceconfig.SigningConfig{Required: required}, PublicKey: string(pubPEM)},
		},
	}
}

// TestVerdictGateSignatureHaltsAtPull is the load-bearing proof for the
// cache-not-pin verdict tier: a verdict-socket gate resolved through
// cacheVerdictSocketPlugins / verifyCachedVerdictSocketPlugin (NOT placed in the
// closure) is signature-verified at pull against the resolving registry's trust
// policy, and a tampered or unsigned gate HALTS the pull with the same Loud
// Guardrail the pinned plugin path produces. Previously a verdict gate reached
// build having had only its content digest checked; this proves the full
// registry-signature verdict now fires on the verdict tier at pull.
func TestVerdictGateSignatureHaltsAtPull(t *testing.T) {
	const uri = "linter/publishing-linter"

	t.Run("wrong_key_halts", func(t *testing.T) {
		store, dg, _, _, _ := signedRealLinterStore(t)
		_, _, _, otherPub, _ := signedRealLinterStore(t)
		wsCfg := keyedVerdictConfig("keyed.example.com", otherPub, true)

		err := verdictGateVerify(t, store, wsCfg, uri, dg)
		if !errors.Is(err, signing.ErrSignatureVerificationFailed()) {
			t.Fatalf("a verdict gate signed with the wrong key must halt with ErrSignatureVerificationFailed; got %v", err)
		}
	})

	t.Run("unsigned_halts", func(t *testing.T) {
		ctx := context.Background()
		store := memory.New()
		artifact := []byte("\x00asm\x01\x00\x00\x00")
		dg, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, artifact, nil)
		if err != nil {
			t.Fatalf("Push: %v", err)
		}
		// No signature referrer is attached: an unsigned gate under a
		// require-signatures registry must halt before any key comparison.
		wsCfg := keyedVerdictConfig("req.example.com", []byte("irrelevant"), true)

		err = verdictGateVerify(t, store, wsCfg, uri, dg)
		if !errors.Is(err, signing.ErrNoSignatureFound()) {
			t.Fatalf("an unsigned verdict gate under a required-signing registry must halt with ErrNoSignatureFound; got %v", err)
		}
	})

	t.Run("correct_key_passes", func(t *testing.T) {
		store, dg, _, pubPEM, _ := signedRealLinterStore(t)
		wsCfg := keyedVerdictConfig("keyed.example.com", pubPEM, true)

		if err := verdictGateVerify(t, store, wsCfg, uri, dg); err != nil {
			t.Fatalf("a correctly-signed verdict gate must pass verification; got %v", err)
		}
	})
}

// TestVerdictGateHostFunctionStampHaltsAtSharedHome proves the host-function
// stamp re-derivation fires on the verdict tier: the verdict gate's verification
// home (verifyFetchedPlugin — the callee verifyCachedVerdictSocketPlugin routes
// every gate through, locked by the source guard) HALTS with
// ErrWasmHostFunctionsStampMismatch when a gate's committed
// spec.runtime.hostFunctions stamp diverges from the imports of its
// content-verified .wasm, and passes when they match. The workspace is offline
// so the signature branch is skipped and the host-function check is isolated.
//
// The frp-step-go fixture imports no custom host functions, so its derived set
// is empty; a stamped [aggrega_log] is a divergence the digest pin alone would
// not catch.
func TestVerdictGateHostFunctionStampHaltsAtSharedHome(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	wasmBytes, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	derived, err := wasm.DeriveHostFunctions("linter/gate", wasmBytes)
	if err != nil {
		t.Fatalf("derive host functions: %v", err)
	}

	base := fetchedPlugin{
		URI:         "linter/gate",
		EntityURI:   "linter/gate",
		ArtifactURI: "linter/gate",
		WasmBytes:   wasmBytes,
	}
	offline := workspaceconfig.Config{Offline: true}

	t.Run("divergent_stamp_halts", func(t *testing.T) {
		in := base
		in.Entity = pluginEntity("linter/gate", []string{"aggrega_log"})
		_, err := verifyFetchedPlugin(context.Background(), in, offline, nil, io.Discard)
		if !errors.Is(err, wasm.ErrWasmHostFunctionsStampMismatch()) {
			t.Fatalf("a verdict gate whose stamp diverges from its .wasm imports must halt with ErrWasmHostFunctionsStampMismatch; got %v", err)
		}
	})

	t.Run("matching_stamp_passes", func(t *testing.T) {
		in := base
		in.Entity = pluginEntity("linter/gate", derived)
		if _, err := verifyFetchedPlugin(context.Background(), in, offline, nil, io.Discard); err != nil {
			t.Fatalf("a verdict gate whose stamp matches its .wasm imports must pass; got %v", err)
		}
	})
}
