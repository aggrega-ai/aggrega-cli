// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginpath

import (
	"errors"
	"strings"
	"testing"
)

// TestEntityLookup_TranslatesEntityToArtifact proves the entity-front translates a
// WasmPlugin entity URI to its spec.plugin artifact reference and runs the cascade
// on the ARTIFACT, not the entity URI — the entity → spec.plugin → cascade two-step.
func TestEntityLookup_TranslatesEntityToArtifact(t *testing.T) {
	const entityURI = "core/frp/patch:1.0.0"
	const artifactURI = "oci://hub.aggrega.ai/wasm/frp/patch@sha256:0000000000000000000000000000000000000000000000000000000000000000"

	var sawArtifact string
	cascade := func(uri string) (ResolvedPlugin, error) {
		sawArtifact = uri
		return ResolvedPlugin{Bytes: []byte("\x00asm-bytes"), Tier: TierSidecar, URI: uri}, nil
	}
	front := EntityLookup(func(uri string) (string, bool) {
		if uri == entityURI {
			return artifactURI, true
		}
		return "", false
	}, cascade)

	res, err := front(entityURI)
	if err != nil {
		t.Fatalf("front(%q): %v", entityURI, err)
	}
	if sawArtifact != artifactURI {
		t.Errorf("cascade ran on %q; want the translated artifact %q", sawArtifact, artifactURI)
	}
	if string(res.Bytes) != "\x00asm-bytes" {
		t.Errorf("resolved bytes = %q; want the cascade's bytes", res.Bytes)
	}
}

// TestEntityLookup_BareWasmHaltsLoudly proves the named gate: a reference that
// matches no WasmPlugin entity (a bare-.wasm or artifact reference) halts with
// ErrWasmPluginEntityNotFound, naming the offending reference, and never reaches
// the cascade.
func TestEntityLookup_BareWasmHaltsLoudly(t *testing.T) {
	cascadeRan := false
	cascade := func(uri string) (ResolvedPlugin, error) {
		cascadeRan = true
		return ResolvedPlugin{}, nil
	}
	// An entity lookup that finds nothing — every reference is a miss.
	front := EntityLookup(func(uri string) (string, bool) { return "", false }, cascade)

	const bareWasm = "frp-step-patch-rust.wasm"
	_, err := front(bareWasm)
	if err == nil {
		t.Fatalf("expected a loud halt for a bare-.wasm reference; got nil")
	}
	if !errors.Is(err, ErrWasmPluginEntityNotFound) {
		t.Errorf("error should wrap ErrWasmPluginEntityNotFound; got: %v", err)
	}
	if !strings.Contains(err.Error(), bareWasm) {
		t.Errorf("error should name the offending reference %q; got: %v", bareWasm, err)
	}
	if !strings.Contains(err.Error(), "WasmPlugin entity URI") {
		t.Errorf("error should state a WasmPlugin entity URI is required; got: %v", err)
	}
	if cascadeRan {
		t.Errorf("cascade must not run when the reference is not a WasmPlugin entity")
	}
}

// TestEntityLookup_NilTranslatorHaltsLoudly proves a nil entity translator (a
// catalog-less path) halts every reference as not-an-entity rather than panicking.
func TestEntityLookup_NilTranslatorHaltsLoudly(t *testing.T) {
	front := EntityLookup(nil, func(uri string) (ResolvedPlugin, error) {
		t.Fatalf("cascade must not run with a nil translator")
		return ResolvedPlugin{}, nil
	})
	_, err := front("core/frp/patch:1.0.0")
	if !errors.Is(err, ErrWasmPluginEntityNotFound) {
		t.Errorf("nil translator should halt with ErrWasmPluginEntityNotFound; got: %v", err)
	}
}
