// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestBuild_UnnormalizedAggregateFailsLoud pins the loud guardrail: an Aggregate
// reaching flatten with a non-empty top-level components map AND a `default`
// subsystem is a normalization bypass (impossible through types.Normalize, and
// the authored-both shape is envelope-rejected), so topology fails loud with
// ErrUnnormalizedAggregate rather than silently flattening a half-normalized
// graph. Defense-in-depth behind the NormalizedAggregate newtype.
func TestBuild_UnnormalizedAggregateFailsLoud(t *testing.T) {
	t.Parallel()
	agg := &types.Aggregate{}
	agg.Spec.Components.Set("api", types.ComponentInstance{Type: "core/server"})
	var subs types.OrderedMap[types.Subsystem]
	subs.Set("default", types.Subsystem{Synthesized: true})
	agg.Spec.Subsystems = subs
	rr := &resolver.ResolveResult{Aggregate: agg}

	if _, err := Build(BuildConfig{ResolveResult: rr}); err == nil || !errors.Is(err, ErrUnnormalizedAggregate) {
		t.Fatalf("non-empty root alongside a default subsystem must fail loud with ErrUnnormalizedAggregate; got %v", err)
	}
}

// TestBuild_NormalizedAggregateFlattensClean is the green control: the
// normalized shape (root folded into the default subsystem) flattens without the
// assert, and the node carries the synthesized-default provenance.
func TestBuild_NormalizedAggregateFlattensClean(t *testing.T) {
	t.Parallel()
	agg := &types.Aggregate{}
	agg.Spec.Components.Set("api", types.ComponentInstance{Type: "core/server"})
	na := types.Normalize(agg) // folds api into the synthesized default subsystem
	rr := &resolver.ResolveResult{Aggregate: na.Aggregate()}

	result, err := Build(BuildConfig{ResolveResult: rr})
	if err != nil {
		t.Fatalf("a normalized aggregate must flatten clean; got %v", err)
	}
	node, ok := result.Nodes.Get("api")
	if !ok {
		t.Fatalf("node 'api' missing; nodes=%v", result.Nodes.Keys())
	}
	if node.SubsystemPath != "default" || !node.SynthesizedSubsystem {
		t.Errorf("normalized node must carry SubsystemPath=default + SynthesizedSubsystem=true; got SubsystemPath=%q Synthesized=%v", node.SubsystemPath, node.SynthesizedSubsystem)
	}
}
