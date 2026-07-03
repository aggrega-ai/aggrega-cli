// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package abtest

import (
	"context"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/compose"
)

// TestComposeProviderNumericOverlayABByteIdentity proves the sourced render path
// keeps numeric overlay values byte-identical to the in-tree compose engine when
// those values cross the provider Wasm ABI.
//
// The oracle receives the overlay as Go-native numerics (int and float64) — the
// shape a YAML scalar parse yields — so go-yaml renders them unquoted. The
// candidate receives the same overlay as an ordered map that is JSON-marshalled
// onto the render wire and decoded by the guest: the wire codec decodes JSON
// numerics to json.Number (a string-kind type), which go-yaml would render as a
// quoted scalar unless the guest renormalizes it back to the Go numeric type the
// oracle holds. A byte-identical merged fragment proves the guest performs that
// renormalization; a quoted-vs-unquoted divergence proves it does not.
//
// The overlay carries a top-level integer, a nested integer, and a non-integral
// float so both the int64 and the float64 renormalization branches are exercised.
func TestComposeProviderNumericOverlayABByteIdentity(t *testing.T) {
	ctx := context.Background()
	wasmPath := buildPluginWasm(t)

	baseCompose := absPath(t, "../../../internal/compiler/testdata/fullstack-app/catalog/recipes/compose-sourced/sources/api.yaml")
	nodeID := "api"

	// ORACLE overlay — Go-native numerics, as a yaml scalar parse yields: a
	// top-level int, a nested int, and a non-integral float64.
	oracleValues := map[string]any{
		"replicas": 3,
		"services": map[string]any{
			"api": map[string]any{
				"ports":  map[string]any{"published": 8080},
				"weight": 1.5,
			},
		},
	}

	// CANDIDATE overlay — the SAME structure decoded through the OrderedMap[any]
	// JSON decode the guest performs off the wire, so its numerics land as
	// json.Number and the wire round-trip is what introduces the string-kind type.
	valuesOrdered := valuesFromJSON(t, `{"replicas":3,"services":{"api":{"ports":{"published":8080},"weight":1.5}}}`)

	const indent = 2

	// ORACLE — the in-tree Go compose engine, fed real provenance and Go-native
	// numeric values.
	oracleCtx := renderer.NodeRenderContext{NodeID: nodeID, Source: baseCompose, YAMLIndent: indent}
	stampOracleProvenance(&oracleCtx)
	oracle, err := (&compose.ComposeEngine{}).Render(ctx, oracleValues, oracleCtx)
	if err != nil {
		t.Fatalf("oracle Render: %v", err)
	}

	// Non-vacuity guard: the oracle must render the numerics UNQUOTED, or a
	// byte-identical candidate proves nothing about json.Number renormalization.
	fragment := oracleFragment(t, oracle)
	for _, want := range []string{"replicas: 3", "published: 8080", "weight: 1.5"} {
		if !strings.Contains(fragment, want) {
			t.Fatalf("oracle fragment missing unquoted %q — the A/B would be vacuous:\n%s", want, fragment)
		}
	}
	for _, quoted := range []string{`replicas: "3"`, `published: "8080"`, `weight: "1.5"`} {
		if strings.Contains(fragment, quoted) {
			t.Fatalf("oracle fragment quoted a numeric (%q) — the oracle is not the unquoted reference:\n%s", quoted, fragment)
		}
	}

	// CANDIDATE — the Wasm guest through the production loader (firewall ON). The
	// base compose file arrives in the payload's SourceProjection, NOT a mount.
	proj, err := compose.ProjectComposeSource(baseCompose)
	if err != nil {
		t.Fatalf("ProjectComposeSource: %v", err)
	}
	node := renderer.IRNodeProjection{ID: nodeID, Values: valuesOrdered}
	stampCandidateProvenance(&node)
	input := renderer.RenderNodeInput{
		Node:             node,
		Context:          renderctx.RenderContext{YAMLIndent: indent},
		SourceProjection: proj,
	}
	payload, err := input.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	rawOut := invokeGuest(t, ctx, wasmPath, payload)

	var out renderer.RenderNodeOutput
	if err := out.UnmarshalJSON(rawOut); err != nil {
		t.Fatalf("decode RenderNodeOutput: %v (raw: %s)", err, rawOut)
	}
	candidate := make(map[string][]byte, len(out.Files))
	for _, f := range out.Files {
		candidate[f.Path] = f.Content
	}

	assertByteIdentical(t, oracle, candidate)
}

// oracleFragment returns the single rendered fragment's content as a string,
// failing the test if the oracle did not produce exactly one file.
func oracleFragment(t *testing.T, oracle map[string][]byte) string {
	t.Helper()
	if len(oracle) != 1 {
		t.Fatalf("oracle produced %d files; expected a single compose fragment", len(oracle))
	}
	for _, b := range oracle {
		return string(b)
	}
	return ""
}
