// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// plugin builds a provider Plugin declaring one well-known annotation at the
// given scopes, against the given input-kind claim. Building it through the real
// entity type rather than a local shape is what keeps the test honest about what
// a loaded entity looks like.
func plugin(kinds types.PluginInputKinds, scopes ...string) types.Plugin {
	return types.Plugin{
		Metadata: types.PluginMetadata{Name: "probe", PluginType: "provider"},
		Spec: types.PluginSpec{
			Provider: &types.PluginProvider{
				WellKnownAnnotations: []types.WellKnownAnnotation{
					{Key: "aggrega.ai/probe", Purpose: "a probe key", Scope: scopes},
				},
				Contract: &types.ProviderContract{InputKinds: kinds},
			},
		},
	}
}

// TestValidateWellKnownAnnotationScopes walks the grid the check actually has:
// each of the three input-kind scopes against a contract that does and does not
// claim it, plus the two scopes that are never compared and the two entity
// shapes that carry no claim at all. Each row fails on its own fault, and the
// claimed rows are the positive controls that keep the unclaimed rows from
// passing for the wrong reason.
func TestValidateWellKnownAnnotationScopes(t *testing.T) {
	all := types.PluginInputKinds{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
	none := types.PluginInputKinds{}

	cases := []struct {
		name  string
		in    types.Plugin
		field string // the contract field the rejection must name; empty means it must be accepted
	}{
		{"sourced claimed", plugin(all, types.WellKnownAnnotationScopeSourced), ""},
		{"sourced unclaimed", plugin(none, types.WellKnownAnnotationScopeSourced), "sourced"},
		{"synthesized claimed", plugin(all, types.WellKnownAnnotationScopeSynthesized), ""},
		{"synthesized unclaimed", plugin(none, types.WellKnownAnnotationScopeSynthesized), "synthesized"},
		{"artifactsStandalone claimed", plugin(all, types.WellKnownAnnotationScopeArtifactsStandalone), ""},
		{"artifactsStandalone unclaimed", plugin(none, types.WellKnownAnnotationScopeArtifactsStandalone), "artifactsStandalone"},

		{"boundary scope is never compared", plugin(none, types.WellKnownAnnotationScopeBoundary), ""},
		{"no scope makes no claim to contradict", plugin(none), ""},

		{"one bad scope among good ones", plugin(
			types.PluginInputKinds{Sourced: true},
			types.WellKnownAnnotationScopeSourced,
			types.WellKnownAnnotationScopeBoundary,
			types.WellKnownAnnotationScopeSynthesized,
		), "synthesized"},

		{"an absent contract claims no input kind", func() types.Plugin {
			p := plugin(none, types.WellKnownAnnotationScopeSourced)
			p.Spec.Provider.Contract = nil
			return p
		}(), "sourced"},

		{"an entity with no provider block declares nothing", types.Plugin{
			Metadata: types.PluginMetadata{Name: "probe", PluginType: "parser"},
		}, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := types.ValidateWellKnownAnnotationScopes(c.in)
			if c.field == "" {
				if err != nil {
					t.Fatalf("accepted input was rejected: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("a contradictory declaration was accepted")
			}
			if !errors.Is(err, types.ErrWellKnownAnnotationScope()) {
				t.Errorf("rejection does not carry the halting condition: %v", err)
			}
			for _, want := range []string{"probe", "aggrega.ai/probe", "contract.inputKinds." + c.field} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("rejection does not name %q: %v", want, err)
				}
			}
		})
	}
}

// TestValidateWellKnownAnnotationScopesDoesNotPanicOnAnEmptyEntity pins the
// negative-input contract: a zero entity is invalid input, and invalid input
// produces an error or nil rather than a panic. testing.T does not fail on a
// recovered panic by itself, so the assertion is written out.
func TestValidateWellKnownAnnotationScopesDoesNotPanicOnAnEmptyEntity(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("a zero entity panicked: %v", r)
		}
	}()
	if err := types.ValidateWellKnownAnnotationScopes(types.Plugin{}); err != nil {
		t.Fatalf("a zero entity declares nothing and must be accepted: %v", err)
	}
}
