// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The promoted-label tests drive the real render over two nodes that share one
// recipe, because that is the only shape in which the promotion's central risk
// is observable: a promoted value belongs to the node whose component declares
// it, and a recipe binds to many nodes.
//
// The lookup below reproduces how a build hands a recipe to the render. The
// catalog stores one recipe and returns it BY VALUE, so each node receives its
// own *types.Recipe whose outputLabels field is nonetheless the same underlying
// map as the stored entity's. A promotion that merged into that map in place
// would leave one node's identity on the recipe every later node reads, and no
// single-node test could see it.

const olPromotionWorkload = `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: app:latest
    ports:
      web: 8080
`

// olpLabels builds a component label set from key/value pairs.
func olpLabels(pairs ...string) types.OrderedMap[string] {
	m := types.OrderedMap[string]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i], pairs[i+1])
	}
	return m
}

// olpInput builds a two-node graph whose nodes carry the supplied component
// label sets. Both nodes are the same component type, so one recipe serves both.
func olpInput(firstLabels, secondLabels types.OrderedMap[string]) renderer.RendererInput {
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("first", types.IRNode{
		Component: "core/compute/api-server", Labels: firstLabels,
		ResolvedVersion: "1.0.0", BoundaryPath: "local/default",
	})
	nodes.Set("second", types.IRNode{
		Component: "core/compute/api-server", Labels: secondLabels,
		ResolvedVersion: "1.0.0", BoundaryPath: "local/default",
	})
	valuesMapping := types.OrderedMap[json.RawMessage]{}
	valuesMapping.Set("first", json.RawMessage(`{"port":8080}`))
	valuesMapping.Set("second", json.RawMessage(`{"port":8080}`))
	return renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "test-app"},
			Spec: types.IRSpec{
				Nodes:         nodes,
				ValuesMapping: valuesMapping,
				InfraMapping:  types.OrderedMap[json.RawMessage]{},
				Provenance:    types.IRProvenance{CompilerVersion: "0.1.0", AggregateVersion: "1.0.0"},
			},
		},
		FeatureOrder: ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test-app"}},
			Release: &types.Release{
				Metadata: types.ReleaseMetadata{Name: "test-release", Version: "1.0.0", Environment: "local"},
			},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
	}
}

// olpSharedRecipeLookup serves both nodes from one stored recipe, handed out by
// value exactly as the catalog hands one out. The stored entity is returned so a
// test can assert the render left it untouched.
func olpSharedRecipeLookup(literal map[string]string, promoted []string) (renderer.RecipeLookup, *types.Recipe) {
	stored := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "shared-recipe", Version: "1.0.0"},
		Spec: types.RecipeSpec{
			Capability:            "helm",
			Implements:            "core/compute/api-server",
			Workload:              types.WorkloadTemplate{Inline: olPromotionWorkload},
			OutputLabels:          literal,
			OutputComponentLabels: promoted,
		},
	}
	return renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType != "core/compute/api-server" {
				return nil, false
			}
			// By value, then addressed — the catalog's own shape. The struct is a
			// fresh copy per call; its map fields are not.
			perNode := *stored
			return &perNode, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}, stored
}

// olpRenderedFor returns the rendered body of every file whose path names the
// given node, joined — the surface a promoted label has to appear on.
func olpRenderedFor(t *testing.T, result *renderer.RenderResult, nodeID string) string {
	t.Helper()
	var b strings.Builder
	for path, file := range result.Files {
		if strings.Contains(path, nodeID) {
			b.Write(file.Content)
			b.WriteString("\n")
		}
	}
	if b.Len() == 0 {
		var paths []string
		for p := range result.Files {
			paths = append(paths, p)
		}
		t.Fatalf("no rendered file for node %q; files: %v", nodeID, paths)
	}
	return b.String()
}

// TestOutputComponentLabels_PerNodeValue_NoCrossNodeLeak is the property that
// proves a promoted value stays with the node it came from. One recipe serves
// two nodes whose components declare DIFFERENT values for the promoted key; each
// node's output must carry its own value and not the other's.
func TestOutputComponentLabels_PerNodeValue_NoCrossNodeLeak(t *testing.T) {
	t.Parallel()
	lookup, stored := olpSharedRecipeLookup(
		map[string]string{"example.io/team": "platform"},
		[]string{"component.aggrega.ai/hosting"},
	)
	input := olpInput(
		olpLabels("component.aggrega.ai/hosting", "managed"),
		olpLabels("component.aggrega.ai/hosting", "self-hosted"),
	)
	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	firstBody := olpRenderedFor(t, result, "first")
	secondBody := olpRenderedFor(t, result, "second")

	if !strings.Contains(firstBody, "component.aggrega.ai/hosting: managed") {
		t.Errorf("first node must carry its own promoted value:\n%s", firstBody)
	}
	if strings.Contains(firstBody, "component.aggrega.ai/hosting: self-hosted") {
		t.Errorf("first node must NOT carry the second node's promoted value:\n%s", firstBody)
	}
	if !strings.Contains(secondBody, "component.aggrega.ai/hosting: self-hosted") {
		t.Errorf("second node must carry its own promoted value:\n%s", secondBody)
	}
	if strings.Contains(secondBody, "component.aggrega.ai/hosting: managed") {
		t.Errorf("second node must NOT carry the first node's promoted value:\n%s", secondBody)
	}
	// The literal entry reaches both, which is what distinguishes a promoted key
	// from a literal one.
	if !strings.Contains(firstBody, "example.io/team: platform") ||
		!strings.Contains(secondBody, "example.io/team: platform") {
		t.Errorf("the literal output label must reach both nodes")
	}

	// The recipe the render was handed must be exactly as it was stored: a
	// promoted key that reached it would travel to every node bound to it.
	if _, promotedLeaked := stored.Spec.OutputLabels["component.aggrega.ai/hosting"]; promotedLeaked {
		t.Errorf("the render wrote a promoted key back onto the recipe: %v", stored.Spec.OutputLabels)
	}
	if len(stored.Spec.OutputLabels) != 1 {
		t.Errorf("the recipe's literal output labels must be unchanged, got %v", stored.Spec.OutputLabels)
	}
}

// TestOutputComponentLabels_AbsentKey_ElidesAndWarns proves a promoted key the
// bound component does not declare stamps nothing, raises a non-fatal advisory
// naming the node, the recipe and the key, and leaves the build succeeding.
func TestOutputComponentLabels_AbsentKey_ElidesAndWarns(t *testing.T) {
	t.Parallel()
	lookup, _ := olpSharedRecipeLookup(nil, []string{"component.aggrega.ai/hosting"})
	input := olpInput(
		olpLabels("component.aggrega.ai/type", "server"),
		olpLabels("component.aggrega.ai/type", "server"),
	)
	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err != nil {
		t.Fatalf("an absent promoted key must not halt the build: %v", err)
	}

	body := olpRenderedFor(t, result, "first")
	if strings.Contains(body, "component.aggrega.ai/hosting") {
		t.Errorf("an undeclared promoted key must stamp nothing:\n%s", body)
	}

	var advisory string
	var raised renderer.Diagnostic
	for _, d := range result.Warnings {
		w := renderer.RenderDiagnosticText(d)
		if strings.Contains(w, "component.aggrega.ai/hosting") {
			advisory, raised = w, d
			break
		}
	}
	if advisory == "" {
		t.Fatalf("expected an advisory naming the missing promoted key; warnings: %v", result.Warnings)
	}
	// The node and the promoted key are fields as well as words, so a consumer
	// reading no terminal can point at the right node and the right key.
	if raised.Location == nil || raised.Location.Instance != "first" {
		t.Errorf("the advisory is not located at the node: %+v", raised.Location)
	}
	if raised.SourceRef != "component.aggrega.ai/hosting" {
		t.Errorf("the promoted key did not survive as the source reference: %+v", raised)
	}
	for _, want := range []string{`"first"`, `"shared-recipe"`, `"component.aggrega.ai/hosting"`} {
		if !strings.Contains(advisory, want) {
			t.Errorf("advisory must name %s; got: %s", want, advisory)
		}
	}
}

// TestOutputComponentLabels_AbsentOnOneNodeOnly_DoesNotInherit is the sharpest
// form of the shared-map property. One node declares the promoted key and the
// other does not; the node that does not must stamp nothing, even though a node
// bound to the same recipe already resolved a value for that key.
func TestOutputComponentLabels_AbsentOnOneNodeOnly_DoesNotInherit(t *testing.T) {
	t.Parallel()
	lookup, _ := olpSharedRecipeLookup(
		map[string]string{"example.io/team": "platform"},
		[]string{"component.aggrega.ai/hosting"},
	)
	// The declaring node renders first, so a value exists by the time the
	// non-declaring node is populated.
	input := olpInput(
		olpLabels("component.aggrega.ai/hosting", "self-hosted"),
		olpLabels("component.aggrega.ai/type", "server"),
	)
	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	if body := olpRenderedFor(t, result, "first"); !strings.Contains(body, "component.aggrega.ai/hosting: self-hosted") {
		t.Errorf("the declaring node must carry the promoted value:\n%s", body)
	}
	if body := olpRenderedFor(t, result, "second"); strings.Contains(body, "component.aggrega.ai/hosting") {
		t.Errorf("a node whose component omits the key must stamp nothing, not inherit:\n%s", body)
	}
}

// TestOutputComponentLabels_PromotedOnly_NoLiteralBlock proves a recipe that
// promotes without declaring any literal output label renders the promoted key.
// The recipe's outputLabels map is nil in this shape, which the resolution has
// to allocate around rather than write into.
func TestOutputComponentLabels_PromotedOnly_NoLiteralBlock(t *testing.T) {
	t.Parallel()
	lookup, stored := olpSharedRecipeLookup(nil, []string{"component.aggrega.ai/hosting"})
	input := olpInput(
		olpLabels("component.aggrega.ai/hosting", "managed"),
		olpLabels("component.aggrega.ai/hosting", "managed"),
	)
	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	if body := olpRenderedFor(t, result, "first"); !strings.Contains(body, "component.aggrega.ai/hosting: managed") {
		t.Errorf("a promoted key must render without a literal block present:\n%s", body)
	}
	if stored.Spec.OutputLabels != nil {
		t.Errorf("the recipe's absent literal block must stay absent, got %v", stored.Spec.OutputLabels)
	}
}
