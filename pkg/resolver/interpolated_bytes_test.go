// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestResolveResult_CarriesInterpolatedAggregateBytes reads the
// post-interpolation Aggregate text at the later stage — from the resolve
// result, downstream of the interpolation that produced it — and holds it to
// being the document the resolve actually parsed rather than the raw text the
// caller supplied.
//
// The two are held apart on the evidence that distinguishes them: the raw text
// still carries its parameter reference expression and the interpolated text
// carries the substituted value, so a single "the Aggregate's bytes" fact could
// not satisfy both readers. That distinction is load-bearing for the
// unread-parameter advisory, which reads the raw text on purpose, and the
// assertion below would fail if the two were ever collapsed onto one field.
func TestResolveResult_CarriesInterpolatedAggregateBytes(t *testing.T) {
	t.Parallel()

	raw := []byte("apiVersion: aggrega.ai/v1beta1\n" +
		"kind: Aggregate\n" +
		"metadata:\n" +
		"  name: a\n" +
		"  version: \"1.0.0\"\n" +
		"spec:\n" +
		"  parameters:\n" +
		"    tier:\n" +
		"      type: string\n" +
		"  components:\n" +
		"    api:\n" +
		"      type: core/web-server\n" +
		"      values:\n" +
		"        tier: \"{{ .parameters.tier }}\"\n")

	rel := &types.Release{}
	var b types.OrderedMap[types.Boundary]
	b.Set("default", types.Boundary{Synthesized: true})
	rel.Spec.Boundaries = b
	var inline types.OrderedMap[any]
	inline.Set("tier", "gold")
	rel.Spec.Parameters.Inline = inline

	var interpolated []byte
	err := resolver.ResolveAndUse(
		context.Background(),
		resolver.ResolveInputs{Release: rel, Manifest: &types.Manifest{}, AggregateRawBytes: raw, AggregatePath: "a.yaml"},
		func(rr *resolver.ResolveResult) error {
			interpolated = rr.AggregateInterpolatedBytes
			return nil
		},
	)
	if err != nil {
		t.Fatalf("ResolveAndUse: %v", err)
	}

	if len(interpolated) == 0 {
		t.Fatal("resolve result carries no post-interpolation Aggregate bytes")
	}
	if !bytes.Contains(interpolated, []byte("gold")) {
		t.Errorf("post-interpolation bytes do not carry the fulfilled parameter value:\n%s", interpolated)
	}
	if bytes.Contains(interpolated, []byte(".parameters.tier")) {
		t.Errorf("post-interpolation bytes still carry the reference expression, so these are the raw bytes:\n%s", interpolated)
	}
	if bytes.Equal(interpolated, raw) {
		t.Error("post-interpolation bytes are byte-identical to the raw input, so the two facts have been collapsed into one")
	}
	// The raw text the caller supplied is unchanged — interpolation does not
	// rewrite it in place, which is what keeps the reference expression readable
	// for the advisory that needs it.
	if !bytes.Contains(raw, []byte(".parameters.tier")) {
		t.Error("the caller's raw bytes were mutated; the pre-interpolation reference expression is gone")
	}
}
