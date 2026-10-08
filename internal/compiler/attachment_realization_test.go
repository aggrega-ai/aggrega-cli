// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// The realization each attachment entry carries on the build graph: the
// FeatureRecipe the Manifest binds to it, recorded when the graph is finalised
// through the lookup the render binds by. These tests hold it over the real
// pipeline, on every kind of attachment the graph carries — a node's own, one
// written on a connection, and a Release boundary's — beside an attachment the
// Manifest binds to nothing, and they hold the emitted graph carrying it to the
// IR schema.
package compiler_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// realizationOf returns the realization one node's attachment carries, failing
// the test when the graph holds no such attachment — an absent entry would read
// as an absent realization and pass every negative assertion below.
func realizationOf(t *testing.T, graph *types.IRGraph, node, attachment string) string {
	t.Helper()
	bucket, ok := graph.Spec.FeatureValuesMapping.Get(node)
	if !ok {
		t.Fatalf("the graph carries no attachments for node %q; nodes with attachments: %v",
			node, graph.Spec.FeatureValuesMapping.Keys())
	}
	entry, ok := bucket.Get(attachment)
	if !ok {
		t.Fatalf("node %q carries no attachment %q; it carries %v", node, attachment, bucket.Keys())
	}
	return entry.Realization
}

// boundaryRealizationOf is realizationOf for a Release boundary's own
// attachment, under the boundary path the graph keys it by.
func boundaryRealizationOf(t *testing.T, graph *types.IRGraph, boundary, attachment string) string {
	t.Helper()
	bucket, ok := graph.Spec.BoundaryFeatures.Get(boundary)
	if !ok {
		t.Fatalf("the graph carries no boundary attachments under %q; boundaries: %v",
			boundary, graph.Spec.BoundaryFeatures.Keys())
	}
	entry, ok := bucket.Get(attachment)
	if !ok {
		t.Fatalf("boundary %q carries no attachment %q; it carries %v", boundary, attachment, bucket.Keys())
	}
	return entry.Realization
}

// TestAttachmentRealization_EveryAttachmentKindCarriesItsBinding drives a
// workspace through the entry the validation surfaces take, and reads the
// realization off each kind of attachment the graph carries.
//
// Every row discriminates. The node's own attachment and the boundary's are
// bound by their Feature type; the connection's is bound by its qualified name
// to a different FeatureRecipe from the one its type maps to, so a graph that
// bound it by type — or by the slot name rather than the name the render binds
// by — names the wrong one; and an attachment whose type the Manifest maps
// nowhere carries none, so a graph that filled every entry with one value fails
// too. The boundary-level attachment also reaches each component of its
// boundary, where it carries the same realization as on the boundary itself.
//
// The validation entry is the one taken because it builds the graph and stops
// short of the render, which an attachment bound to nothing would halt — the
// graph must carry the binding without the render having run.
func TestAttachmentRealization_EveryAttachmentKindCarriesItsBinding(t *testing.T) {
	t.Parallel()
	tier4, err := pipelinetest.Tier4E(t, pipelinetest.Input{
		ReleasePath: "testdata/attachment-realization/release.yaml",
		CatalogPaths: []string{
			"testdata/connection-attachment/catalog/base",
			"testdata/attachment-realization/catalog",
		},
	})
	if err != nil {
		t.Fatalf("the validation entry refused the fixture: %v", err)
	}
	if tier4 == nil || tier4.IRGraph == nil {
		t.Fatal("the validation entry produced no graph")
	}
	if tier4.Analysis != nil && len(tier4.Analysis.Errors) > 0 {
		t.Fatalf("the fixture raised %d violation(s), so its graph is one no surface hands out; first: %s",
			len(tier4.Analysis.Errors), tier4.Analysis.Errors[0].Message)
	}
	graph := tier4.IRGraph

	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{"a node's own attachment, bound by its Feature type",
			realizationOf(t, graph, "gateway", "db_password"), "conn-token-compose:1.0.0"},
		{"an attachment written on a connection, bound by its qualified name",
			realizationOf(t, graph, "gateway", "gpt.credential"), "conn-token-pinned:2.0.0"},
		{"an attachment the Manifest binds to nothing",
			realizationOf(t, graph, "gateway", "audit"), ""},
		{"a Release boundary's own attachment, bound by its Feature type",
			boundaryRealizationOf(t, graph, "cluster", "trace"), "conn-token-compose:1.0.0"},
		{"the boundary's attachment where it reaches a component",
			realizationOf(t, graph, "model", "trace"), "conn-token-compose:1.0.0"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: realization = %q, want %q", tc.name, tc.got, tc.want)
		}
	}

	// The graph carrying the field on all three entry kinds is held to the IR
	// schema both as built and as a command emits it, so a field the schema
	// does not declare — on either attachment shape — fails here.
	assertIRConforms(t, "attachment-realization as built", graph)
	assertIRConforms(t, "attachment-realization as a command emits it", redact.Graph(graph))
}

// TestAttachmentRealization_TheBuildRecordsTheBindingItRenders drives a
// workspace through the build entry, which renders, and reads the realization
// the graph it hands back carries for a node's own attachment and for one
// written on a connection. Both are bound by their Feature type to the
// FeatureRecipe the build renders them with, so the graph and the render name
// one FeatureRecipe.
func TestAttachmentRealization_TheBuildRecordsTheBindingItRenders(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/connection-attachment/release.yaml",
		CatalogPaths: []string{"testdata/connection-attachment/catalog/base"},
	})
	if result.IRGraph == nil {
		t.Fatal("the build produced no graph")
	}
	for _, attachment := range []string{"db_password", "gpt.credential"} {
		if got := realizationOf(t, result.IRGraph, "gateway", attachment); got != "conn-token-compose:1.0.0" {
			t.Errorf("%s: realization = %q, want %q", attachment, got, "conn-token-compose:1.0.0")
		}
	}
}
