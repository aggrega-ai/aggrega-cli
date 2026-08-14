// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Structural bound on the resolved view's reachability: the only exported
// producer of a ResolveResult is ResolveAndUse, which scopes the result to a
// callback rather than returning it. A caller cannot obtain a resolved view —
// and so cannot reach the merged parameter map — outside that scope, and the
// bound holds by construction rather than by any caller's discipline.
//
// The positive half is what the path-template carve-out depends on: the merged
// map is live for the whole callback, so orchestrator-time path templates read
// `.parameters.<key>` while artifact paths are being emitted.

package resolver_test

import (
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestResolveAndUse_ParametersLiveForTheWholeCallback proves the merged
// parameter map is readable throughout the callback the producer scopes it to,
// which is what path-template evaluation relies on.
func TestResolveAndUse_ParametersLiveForTheWholeCallback(t *testing.T) {
	t.Parallel()

	rel := &types.Release{}
	var b types.OrderedMap[types.Boundary]
	b.Set("default", types.Boundary{Synthesized: true})
	rel.Spec.Boundaries = b
	agg := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: a\n  version: \"1.0.0\"\nspec:\n  parameters:\n    region:\n      type: string\n  components: {}\n")
	var inline types.OrderedMap[any]
	inline.Set("region", "eu-west-1")
	rel.Spec.Parameters.Inline = inline

	var sawValue any
	var callbackRan bool
	err := resolver.ResolveAndUse(
		context.Background(),
		resolver.ResolveInputs{Release: rel, Manifest: &types.Manifest{}, AggregateRawBytes: agg},
		func(rr *resolver.ResolveResult) error {
			callbackRan = true
			sawValue = rr.Parameters["region"]
			return nil
		},
	)
	if err != nil {
		t.Fatalf("ResolveAndUse: %v", err)
	}
	if !callbackRan {
		t.Fatalf("callback did not run")
	}
	if sawValue != "eu-west-1" {
		t.Fatalf("merged parameter not readable inside the callback scope: got %v", sawValue)
	}
}

// TestResolveAndUse_CallbackErrorPropagates pins the producer's error contract:
// the callback's error is the call's error, so a consumer doing its
// parameter-dependent work inside the scope can fail the resolve from there.
func TestResolveAndUse_CallbackErrorPropagates(t *testing.T) {
	t.Parallel()

	rel := &types.Release{}
	var b types.OrderedMap[types.Boundary]
	b.Set("default", types.Boundary{Synthesized: true})
	rel.Spec.Boundaries = b
	agg := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: a\n  version: \"1.0.0\"\nspec:\n  parameters:\n    region:\n      type: string\n  components: {}\n")
	var inline types.OrderedMap[any]
	inline.Set("region", "eu-west-1")
	rel.Spec.Parameters.Inline = inline

	err := resolver.ResolveAndUse(
		context.Background(),
		resolver.ResolveInputs{Release: rel, Manifest: &types.Manifest{}, AggregateRawBytes: agg},
		func(rr *resolver.ResolveResult) error {
			return errSentinelForTest
		},
	)
	if err != errSentinelForTest {
		t.Fatalf("expected the callback's error to propagate, got %v", err)
	}
}

var errSentinelForTest = &simpleErr{"callback failed"}

type simpleErr struct{ s string }

func (e *simpleErr) Error() string { return e.s }
