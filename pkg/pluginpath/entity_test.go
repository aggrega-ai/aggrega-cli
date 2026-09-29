// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginpath

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
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
	// sibling field so the loader caps the plugin's memory and execution time.
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
	if !errors.Is(err, ErrPluginEntityNotFound()) {
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

// TestEntityLookup_MalformedPin proves the entity-front judges the pin of an
// entity reference it could not translate, on the entity-only surface and nowhere
// else. A malformed pin is reported as that malformation; a well-formed pin the
// catalog does not hold keeps the not-an-entity condition; and a permissive
// surface keeps the not-an-entity condition for a malformed pin too, because its
// callers fall back on that condition to resolve artifact references. The
// translated reference is the positive control.
func TestEntityLookup_MalformedPin(t *testing.T) {
	cascade := func(uri string) (ResolvedPlugin, error) {
		return ResolvedPlugin{Bytes: []byte("\x00asm"), URI: uri}, nil
	}
	translate := func(uri string) (string, *PluginContractClaim, *PluginRuntimeLimits, *[]PluginCommandMirror, bool) {
		if uri == "mutator/probe:1.0.0" {
			return "mutator/probe", nil, nil, nil, true
		}
		return "", nil, nil, nil, false
	}

	for _, tc := range []struct {
		name   string
		policy SurfacePolicy
		ref    string
		want   error
	}{
		{"entity-only: a reference the catalog translates", SurfaceEntityOnly, "mutator/probe:1.0.0", nil},
		{"entity-only: a caret pin whose range does not parse", SurfaceEntityOnly, "mutator/probe:^garbage", types.ErrMalformedVersionPin()},
		{"entity-only: an exact pin that is not a semantic version", SurfaceEntityOnly, "mutator/probe:1.x", types.ErrMalformedVersionPin()},
		{"entity-only: a well-formed pin the catalog does not hold", SurfaceEntityOnly, "mutator/probe:9.9.9", ErrPluginEntityNotFound()},
		{"permissive: a malformed pin keeps the fallback condition", SurfacePermissive, "mutator/probe:^garbage", ErrPluginEntityNotFound()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := EntityLookup(translate, cascade, tc.policy)(tc.ref)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("control: %q must translate; got %v", tc.ref, err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("front(%q) = %v; want it to match %v", tc.ref, err, tc.want)
			}
			malformed := errors.Is(tc.want, types.ErrMalformedVersionPin())
			if malformed == errors.Is(err, ErrPluginEntityNotFound()) {
				t.Errorf("front(%q) = %v; exactly one of the malformed-pin and not-an-entity conditions applies", tc.ref, err)
			}
			if malformed && !strings.Contains(err.Error(), `reference "`+tc.ref+`"`) {
				t.Errorf("front(%q) = %v; the refusal must name the reference", tc.ref, err)
			}
		})
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
	if !errors.Is(err, ErrPluginEntityNotFound()) {
		t.Errorf("nil translator should halt with ErrPluginEntityNotFound; got: %v", err)
	}
}
