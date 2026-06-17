// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func bcomps(names ...string) types.OrderedMap[types.BoundaryComponent] {
	var m types.OrderedMap[types.BoundaryComponent]
	for _, n := range names {
		m.Set(n, types.BoundaryComponent{})
	}
	return m
}

// TestResolveFromInputs_UnnormalizedReleaseFailsLoud pins the loud guardrail at
// the resolve entry: a Release carrying BOTH a non-empty top-level components
// map AND a `default` boundary is a normalization bypass (impossible via
// types.NormalizeRelease, and the authored-both shape is envelope-rejected). The
// assert fires at the resolve entry — before WireBoundaryInfra and the release
// guards consume the Release — so a components-only bypass cannot be silently
// missed. Exercised through ResolveAndUse, the single exported producer.
func TestResolveFromInputs_UnnormalizedReleaseFailsLoud(t *testing.T) {
	t.Parallel()
	rel := &types.Release{}
	rel.Spec.Components = bcomps("api")
	var b types.OrderedMap[types.Boundary]
	b.Set("default", types.Boundary{Synthesized: true})
	rel.Spec.Boundaries = b

	err := resolver.ResolveAndUse(resolver.ResolveInputs{Release: rel, Manifest: &types.Manifest{}}, func(*resolver.ResolveResult) error { return nil })
	if !errors.Is(err, resolver.ErrUnnormalizedRelease) {
		t.Fatalf("a bypass release (raw components + default boundary) must fail loud with ErrUnnormalizedRelease; got %v", err)
	}
}

// TestResolveFromInputs_NormalizedReleaseClearsAssert is the green control: a
// normalized Release (boundaries only, no raw components) does NOT trip the
// assert — any downstream error is unrelated to ErrUnnormalizedRelease.
func TestResolveFromInputs_NormalizedReleaseClearsAssert(t *testing.T) {
	t.Parallel()
	rel := &types.Release{}
	var b types.OrderedMap[types.Boundary]
	b.Set("default", types.Boundary{Synthesized: true, Components: bcomps("api")})
	rel.Spec.Boundaries = b
	agg := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: a\n  version: \"1.0.0\"\nspec:\n  components: {}\n")

	err := resolver.ResolveAndUse(resolver.ResolveInputs{Release: rel, Manifest: &types.Manifest{}, AggregateRawBytes: agg}, func(*resolver.ResolveResult) error { return nil })
	if errors.Is(err, resolver.ErrUnnormalizedRelease) {
		t.Fatalf("a normalized release must not trip the un-normalized assert; got %v", err)
	}
}
