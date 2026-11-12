// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// metadataGraph is a graph compiled from a Release and an Aggregate: it records
// the versions it was compiled from and the Aggregate's name.
func metadataGraph() *types.IRGraph {
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/api", ResolvedVersion: "1.0.0"})
	return &types.IRGraph{
		Metadata: types.IRMetadata{AggregateName: "shop"},
		Spec: types.IRSpec{
			Nodes: nodes,
			Provenance: types.IRProvenance{
				AggregateVersion: "1.0.0",
				ReleaseVersion:   "2.0.0",
			},
		},
	}
}

// metadataRelease is the Release the graph above was compiled from.
func metadataRelease() *types.Release {
	return &types.Release{Metadata: types.ReleaseMetadata{
		Name:        "shop-prod",
		Version:     "2.0.0",
		Environment: "production",
	}}
}

// TestSelfInjectMetadata_IsWhatTheRecipeTemplatesRead asserts a component-level
// self-inject value: template reads the same .metadata block as the node's Recipe
// templates: the block the Recipe's context is assembled with, field for field.
func TestSelfInjectMetadata_IsWhatTheRecipeTemplatesRead(t *testing.T) {
	t.Parallel()
	graph, release := metadataGraph(), metadataRelease()

	recipeCtx := AssembleNodeContext("api", graph, nil, nil, types.InjectPipe{}, nil, nil, nil)
	AssembleNodeContextWithRelease(recipeCtx, release)
	if recipeCtx.Metadata == nil {
		t.Fatal("the Recipe template context carries no metadata block")
	}

	got := selfInjectMetadata(graph, release)
	if got != *recipeCtx.Metadata {
		t.Errorf("self-inject metadata = %+v, want the Recipe context's %+v", got, *recipeCtx.Metadata)
	}
	want := types.TemplateMetadata{
		ReleaseName:    "shop-prod",
		ReleaseVersion: "2.0.0",
		Environment:    "production",
		AggregateName:  "shop",
	}
	if got != want {
		t.Errorf("self-inject metadata = %+v, want %+v", got, want)
	}
}

// TestSelfInjectValue_ReadsTheReleaseMetadata evaluates a self-inject value:
// template reading each .metadata field, and the whole block, against the
// metadata a build supplies.
func TestSelfInjectValue_ReadsTheReleaseMetadata(t *testing.T) {
	t.Parallel()
	ctx := SelfInjectContext{
		Context:  SelfInjectContextBlock{InstanceID: "api"},
		Metadata: selfInjectMetadata(metadataGraph(), metadataRelease()),
	}
	for _, row := range []struct {
		template string
		want     string
	}{
		{`{{ .metadata.release_name }}`, "shop-prod"},
		{`{{ .metadata.release_version }}`, "2.0.0"},
		{`{{ .metadata.environment }}`, "production"},
		{`{{ .metadata.aggregate_name }}`, "shop"},
		{`{{ toJson .metadata }}`,
			`{"aggregate_name":"shop","environment":"production","release_name":"shop-prod","release_version":"2.0.0"}`},
	} {
		got, err := evalSelfInjectValue("t", row.template, ctx, redact.Keys{})
		if err != nil {
			t.Errorf("%s: %v", row.template, err)
			continue
		}
		if got != row.want {
			t.Errorf("%s rendered %q, want %q", row.template, got, row.want)
		}
	}
}

// TestSelfInjectValue_MetadataWithoutAReleaseRendersEmpty asserts the block is
// present even where the render was handed no Release: a field read renders
// nothing rather than stopping on an absent block.
func TestSelfInjectValue_MetadataWithoutAReleaseRendersEmpty(t *testing.T) {
	t.Parallel()
	ctx := SelfInjectContext{
		Context:  SelfInjectContextBlock{InstanceID: "api"},
		Metadata: selfInjectMetadata(&types.IRGraph{}, nil),
	}
	got, err := evalSelfInjectValue("t", `{{ .metadata.release_name }}`, ctx, redact.Keys{})
	if err != nil {
		t.Fatalf("reading a field of the metadata block with no Release stopped: %v", err)
	}
	if got != "" {
		t.Errorf("rendered %q, want empty", got)
	}
}
