// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginpath

import (
	"errors"
	"strings"
	"testing"
)

// TestEntityLookup_TranslatesEntityToArtifact proves the entity-front translates a
// Plugin entity URI to its spec.plugin artifact reference and runs the cascade
// on the ARTIFACT, not the entity URI — the entity → spec.plugin → cascade two-step.
func TestEntityLookup_TranslatesEntityToArtifact(t *testing.T) {
	const entityURI = "core/frp/patch:1.0.0"
	const artifactURI = "oci://hub.aggrega.ai/plugin/frp/patch@sha256:0000000000000000000000000000000000000000000000000000000000000000"

	var sawArtifact string
	cascade := func(uri string) (ResolvedPlugin, error) {
		sawArtifact = uri
		return ResolvedPlugin{Bytes: []byte("\x00asm-bytes"), Tier: TierColocated, URI: uri}, nil
	}
	claim := &PluginContractClaim{Exports: []PluginExportClaim{{Name: "step", Operates: "patch", Scope: "own"}}}
	limits := &PluginRuntimeLimits{MaxMemoryPages: 64, TimeoutMs: 1500}
	front := EntityLookup(func(uri string) (string, *PluginContractClaim, *PluginRuntimeLimits, *[]PluginCommandMirror, bool) {
		if uri == entityURI {
			return artifactURI, claim, limits, nil, true
		}
		return "", nil, nil, nil, false
	}, cascade, SurfaceEntityOnly)

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
	// The entity-front attaches the entity's authored capability claim to the
	// cascade's ResolvedPlugin so the load gate can cross-check it.
	if res.Contract != claim {
		t.Errorf("resolved Contract = %v; want the entity's claim attached", res.Contract)
	}
	// The entity-front attaches the entity's authored runtime limits as a
	// sibling field so the loader caps the plugin's memory and wall time.
	if res.Limits != limits {
		t.Errorf("resolved Limits = %v; want the entity's limits attached", res.Limits)
	}
}

// TestEntityLookup_BareWasmHaltsLoudly proves the named gate: a reference that
// matches no Plugin entity (a bare-.wasm or artifact reference) halts with
// ErrPluginEntityNotFound, naming the offending reference, and never reaches
// the cascade.
func TestEntityLookup_BareWasmHaltsLoudly(t *testing.T) {
	cascadeRan := false
	cascade := func(uri string) (ResolvedPlugin, error) {
		cascadeRan = true
		return ResolvedPlugin{}, nil
	}
	// An entity lookup that finds nothing — every reference is a miss.
	front := EntityLookup(func(uri string) (string, *PluginContractClaim, *PluginRuntimeLimits, *[]PluginCommandMirror, bool) {
		return "", nil, nil, nil, false
	}, cascade, SurfaceEntityOnly)

	const bareWasm = "frp-step-patch-rust.wasm"
	_, err := front(bareWasm)
	if err == nil {
		t.Fatalf("expected a loud halt for a bare-.wasm reference; got nil")
	}
	if !errors.Is(err, ErrPluginEntityNotFound) {
		t.Errorf("error should wrap ErrPluginEntityNotFound; got: %v", err)
	}
	if !strings.Contains(err.Error(), bareWasm) {
		t.Errorf("error should name the offending reference %q; got: %v", bareWasm, err)
	}
	if !strings.Contains(err.Error(), "Plugin entity URI") {
		t.Errorf("error should state a Plugin entity URI is required; got: %v", err)
	}
	if cascadeRan {
		t.Errorf("cascade must not run when the reference is not a Plugin entity")
	}
}

// TestEntityLookup_NilTranslatorHaltsLoudly proves a nil entity translator (a
// catalog-less path) halts every reference as not-an-entity rather than panicking.
func TestEntityLookup_NilTranslatorHaltsLoudly(t *testing.T) {
	front := EntityLookup(nil, func(uri string) (ResolvedPlugin, error) {
		t.Fatalf("cascade must not run with a nil translator")
		return ResolvedPlugin{}, nil
	}, SurfaceEntityOnly)
	_, err := front("core/frp/patch:1.0.0")
	if !errors.Is(err, ErrPluginEntityNotFound) {
		t.Errorf("nil translator should halt with ErrPluginEntityNotFound; got: %v", err)
	}
}
