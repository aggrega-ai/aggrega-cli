// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Structural guarantee for the secret-parameter purge: the only exported
// producer of a ResolveResult is ResolveAndUse, which scopes the result to a
// callback and purges the parameter map in a defer it owns. A caller cannot
// obtain a live result outside that scope, so the purge holds by construction
// rather than by every caller remembering a deferred PurgeParameters.

package resolver_test

import (
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestResolveAndUse_PurgesParametersAfterCallback proves the producer-owned
// purge fires after the callback returns: the result captured inside the
// callback has an empty parameter map once ResolveAndUse has returned.
func TestResolveAndUse_PurgesParametersAfterCallback(t *testing.T) {
	t.Parallel()

	rel := &types.Release{}
	var b types.OrderedMap[types.Boundary]
	b.Set("default", types.Boundary{Synthesized: true})
	rel.Spec.Boundaries = b
	agg := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: a\n  version: \"1.0.0\"\nspec:\n  parameters:\n    secret_token:\n      type: string\n  components: {}\n")
	var inline types.OrderedMap[any]
	inline.Set("secret_token", "s3cr3t")
	rel.Spec.Parameters.Inline = inline

	var captured *resolver.ResolveResult
	var sawValueInScope bool
	err := resolver.ResolveAndUse(
		context.Background(),
		resolver.ResolveInputs{Release: rel, Manifest: &types.Manifest{}, AggregateRawBytes: agg},
		func(rr *resolver.ResolveResult) error {
			captured = rr
			// Parameters are live inside the scope.
			if _, ok := rr.Parameters["secret_token"]; ok {
				sawValueInScope = true
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("ResolveAndUse: %v", err)
	}
	if !sawValueInScope {
		t.Fatalf("expected the merged parameter to be live inside the callback scope")
	}
	// After ResolveAndUse returns, the producer-owned defer has purged the map.
	if captured == nil {
		t.Fatalf("callback did not run")
	}
	if len(captured.Parameters) != 0 {
		t.Fatalf("parameters not purged after ResolveAndUse returned: %v", captured.Parameters)
	}
}

// TestResolveAndUse_PurgesEvenWhenCallbackErrors confirms the purge fires on the
// callback-error path too — the deferred purge does not depend on a nil error.
func TestResolveAndUse_PurgesEvenWhenCallbackErrors(t *testing.T) {
	t.Parallel()

	rel := &types.Release{}
	var b types.OrderedMap[types.Boundary]
	b.Set("default", types.Boundary{Synthesized: true})
	rel.Spec.Boundaries = b
	agg := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: a\n  version: \"1.0.0\"\nspec:\n  parameters:\n    secret_token:\n      type: string\n  components: {}\n")
	var inline types.OrderedMap[any]
	inline.Set("secret_token", "s3cr3t")
	rel.Spec.Parameters.Inline = inline

	var captured *resolver.ResolveResult
	_ = resolver.ResolveAndUse(
		context.Background(),
		resolver.ResolveInputs{Release: rel, Manifest: &types.Manifest{}, AggregateRawBytes: agg},
		func(rr *resolver.ResolveResult) error {
			captured = rr
			return errSentinelForTest
		},
	)
	if captured == nil {
		t.Fatalf("callback did not run")
	}
	if len(captured.Parameters) != 0 {
		t.Fatalf("parameters not purged on the callback-error path: %v", captured.Parameters)
	}
}

var errSentinelForTest = &simpleErr{"callback failed"}

type simpleErr struct{ s string }

func (e *simpleErr) Error() string { return e.s }
