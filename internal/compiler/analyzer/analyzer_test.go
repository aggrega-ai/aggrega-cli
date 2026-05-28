// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func analyzerCatalog() resolver.CatalogLookup {
	return resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case "core/server/api-server":
				labels := types.OrderedMap[string]{}
				labels.Set(types.ComponentTypeLabel, "server")
				usesSchema := types.OrderedMap[types.UsesSchemaEntry]{}
				usesSchema.Set("databases", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=stateful-server"})
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:   "core/server/api-server",
						Labels: labels,
					},
					Spec: types.ComponentSpec{
						UsesSchema: usesSchema,
					},
				}, true
			case "core/database/postgres":
				labels2 := types.OrderedMap[string]{}
				labels2.Set(types.ComponentTypeLabel, "stateful-server")
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:   "core/database/postgres",
						Labels: labels2,
					},
				}, true
			case "core/redis":
				labels3 := types.OrderedMap[string]{}
				labels3.Set(types.ComponentTypeLabel, "resource")
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:   "core/redis",
						Labels: labels3,
					},
				}, true
			default:
				return types.Component{}, false
			}
		},
		Feature: func(typeURI string) (types.Feature, bool) {
			return types.Feature{}, false
		},
	}
}

func TestAnalyze_SelectorPass(t *testing.T) {
	t.Parallel()
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "server")
	labels2 := types.OrderedMap[string]{}
	labels2.Set(types.ComponentTypeLabel, "stateful-server")
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/server/api-server", Labels: labels})
	nodes.Set("main-db", types.IRNode{Component: "core/database/postgres", Labels: labels2})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
			Edges: []types.IREdge{
				{Source: "api", Target: "main-db", Socket: "databases"},
			},
		},
	}

	result := Analyze(graph, analyzerCatalog())
	if len(result.Errors) != 0 {
		t.Errorf("expected 0 selector errors, got %d: %v", len(result.Errors), result.Errors[0].Message)
	}
}

func TestAnalyze_SelectorFail(t *testing.T) {
	t.Parallel()
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "server")
	labels2 := types.OrderedMap[string]{}
	labels2.Set(types.ComponentTypeLabel, "resource")
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/server/api-server", Labels: labels})
	nodes.Set("cache", types.IRNode{Component: "core/redis", Labels: labels2})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
			Edges: []types.IREdge{
				{Source: "api", Target: "cache", Socket: "databases"},
			},
		},
	}

	result := Analyze(graph, analyzerCatalog())
	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 selector error, got %d", len(result.Errors))
	}
	e := result.Errors[0]
	if e.Source != "api" || e.Target != "cache" || e.Socket != "databases" {
		t.Errorf("error: %+v", e)
	}
	if e.Selector != "component.aggrega.ai/type=stateful-server" {
		t.Errorf("selector: %q", e.Selector)
	}
}

func analyzerCatalogWithFeature(feat types.Feature) resolver.CatalogLookup {
	base := analyzerCatalog()
	return resolver.CatalogLookup{
		Component: base.Component,
		Feature: func(typeURI string) (types.Feature, bool) {
			if typeURI == feat.Metadata.Name {
				return feat, true
			}
			return types.Feature{}, false
		},
	}
}

func TestAnalyze_AppliesToPass(t *testing.T) {
	t.Parallel()
	appliesTo := types.OrderedMap[string]{}
	appliesTo.Set(types.ComponentTypeLabel, "server")

	feat := types.Feature{
		Metadata: types.FeatureMetadata{Name: "core/monitoring"},
		Spec: types.FeatureSpec{
			AppliesTo: appliesTo,
		},
	}
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "server")
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/server/api-server", Labels: labels})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
			FeatureValuesMapping: func() types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]] {
				inner := types.OrderedMap[types.IRFeatureEntry]{}
				inner.Set("monitoring", types.IRFeatureEntry{Type: "core/monitoring"})
				outer := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
				outer.Set("api", inner)
				return outer
			}(),
		},
	}

	result := Analyze(graph, analyzerCatalogWithFeature(feat))
	if len(result.Errors) != 0 {
		t.Errorf("expected 0 errors, got %d: %+v", len(result.Errors), result.Errors)
	}
}

func TestAnalyze_AppliesToFail(t *testing.T) {
	t.Parallel()
	appliesTo := types.OrderedMap[string]{}
	appliesTo.Set(types.ComponentTypeLabel, "server")

	feat := types.Feature{
		Metadata: types.FeatureMetadata{Name: "core/monitoring"},
		Spec: types.FeatureSpec{
			AppliesTo: appliesTo,
		},
	}
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "stateful-server")
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db", types.IRNode{Component: "core/database/postgres", Labels: labels})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
			FeatureValuesMapping: func() types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]] {
				inner := types.OrderedMap[types.IRFeatureEntry]{}
				inner.Set("monitoring", types.IRFeatureEntry{Type: "core/monitoring"})
				outer := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
				outer.Set("db", inner)
				return outer
			}(),
		},
	}

	result := Analyze(graph, analyzerCatalogWithFeature(feat))
	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d: %+v", len(result.Errors), result.Errors)
	}
	e := result.Errors[0]
	if e.Source != "db" || e.Target != "monitoring" {
		t.Errorf("error fields: %+v", e)
	}
	if !strings.Contains(e.Message, "appliesTo constraint not satisfied") {
		t.Errorf("message: %q", e.Message)
	}
	if !strings.Contains(e.Selector, "component.aggrega.ai/type=server") {
		t.Errorf("selector: %q", e.Selector)
	}
}

func TestAnalyze_AppliesToExpressions(t *testing.T) {
	t.Parallel()

	feat := types.Feature{
		Metadata: types.FeatureMetadata{Name: "core/monitoring"},
		Spec: types.FeatureSpec{
			AppliesToExpressions: []types.LabelSelectorRequirement{
				{Key: types.ComponentTypeLabel, Operator: "In", Values: []string{"server", "worker"}},
			},
		},
	}
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "worker")
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("worker", types.IRNode{Component: "core/server/api-server", Labels: labels})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
			FeatureValuesMapping: func() types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]] {
				inner := types.OrderedMap[types.IRFeatureEntry]{}
				inner.Set("mon", types.IRFeatureEntry{Type: "core/monitoring"})
				outer := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
				outer.Set("worker", inner)
				return outer
			}(),
		},
	}

	result := Analyze(graph, analyzerCatalogWithFeature(feat))
	if len(result.Errors) != 0 {
		t.Errorf("expected 0 errors (worker matches In [server, worker]), got %d: %+v", len(result.Errors), result.Errors)
	}
}

func TestAnalyze_PathsCalculated(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("gateway", types.IRNode{Component: "core/server/api-server"})
	nodes.Set("api", types.IRNode{Component: "core/server/api-server"})
	nodes.Set("db", types.IRNode{Component: "core/database/postgres"})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
			Edges: []types.IREdge{
				{Source: "api", Target: "db", Socket: "databases"},
				{Source: "gateway", Target: "api", Socket: "apis"},
			},
		},
	}

	result := Analyze(graph, analyzerCatalog())
	if len(result.Paths) < 2 {
		t.Errorf("expected at least 2 paths, got %d", len(result.Paths))
	}
	// gateway→api→db should be one path
	found := false
	for _, p := range result.Paths {
		if len(p.Nodes) == 3 && p.Nodes[0] == "api" && p.Nodes[1] == "db" {
			// api→db is a 2-node path (wrong)
		}
		if len(p.Nodes) == 3 && p.Nodes[0] == "gateway" {
			found = true
		}
	}
	if !found {
		t.Error("expected gateway→api→db path")
	}
}

func TestAnalyze_PolicyInputAssembled(t *testing.T) {
	t.Parallel()
	labels := types.OrderedMap[string]{}
	labels.Set("a", "b")
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/server/api-server", ResolvedVersion: "1.0.0", Labels: labels})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
			Edges: nil,
		},
	}

	result := Analyze(graph, analyzerCatalog())
	if result.PolicyInput == nil {
		t.Fatal("expected policy input")
	}
	if len(result.PolicyInput.Graph.Nodes) != 1 {
		t.Errorf("expected 1 policy node, got %d", len(result.PolicyInput.Graph.Nodes))
	}
	pn := result.PolicyInput.Graph.Nodes["api"]
	if pn.Component != "core/server/api-server" || pn.ResolvedVersion != "1.0.0" {
		t.Errorf("policy node: %+v", pn)
	}
}

func TestAnalyze_EmptyGraph(t *testing.T) {
	t.Parallel()

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: types.OrderedMap[types.IRNode]{},
			Edges: nil,
		},
	}

	result := Analyze(graph, analyzerCatalog())
	if len(result.Errors) != 0 {
		t.Errorf("expected 0 errors, got %d", len(result.Errors))
	}
	if len(result.Paths) != 0 {
		t.Errorf("expected 0 paths, got %d", len(result.Paths))
	}
}

// TestValidateSelectors_NilCatalogLookupReturnsNil pins the early-return
// guard when the catalog-component lookup function is nil. Without a
// catalog source the validator cannot resolve usesSchema entries and
// returns nil rather than panic.
func TestValidateSelectors_NilCatalogLookupReturnsNil(t *testing.T) {
	t.Parallel()
	graph := &types.IRGraph{Spec: types.IRSpec{}}
	errs := validateSelectors(graph, resolver.CatalogLookup{})
	if errs != nil {
		t.Errorf("expected nil errors with nil catalog lookup; got %v", errs)
	}
}

// TestValidateSelectors_MalformedSelectorProducesParseError pins the
// selector-parse-error branch. A usesSchema entry whose selector string
// fails the selector parser surfaces an AnalysisError carrying
// "selector parse error" framing.
func TestValidateSelectors_MalformedSelectorProducesParseError(t *testing.T) {
	t.Parallel()
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "server")
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "test/with-bad-selector", Labels: labels})
	nodes.Set("target", types.IRNode{Component: "core/redis", Labels: labels})
	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
			Edges: []types.IREdge{{Source: "api", Target: "target", Socket: "databases"}},
		},
	}
	uses := types.OrderedMap[types.UsesSchemaEntry]{}
	uses.Set("databases", types.UsesSchemaEntry{Selector: "!"})
	cat := resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == "test/with-bad-selector" {
				return types.Component{Spec: types.ComponentSpec{UsesSchema: uses}}, true
			}
			return types.Component{}, false
		},
	}
	errs := validateSelectors(graph, cat)
	if len(errs) != 1 {
		t.Fatalf("expected 1 parse-error; got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Message, "selector parse error") {
		t.Errorf("missing parse-error framing; got: %s", errs[0].Message)
	}
}

// TestValidateSelectors_FeatureOriginatedEdgeReadsFeatureUsesSchema pins
// the OriginFeature branch — when an edge declares it originated from a
// Feature, the selector resolution reads the Feature's usesSchema, not
// the consuming Component's.
func TestValidateSelectors_FeatureOriginatedEdgeReadsFeatureUsesSchema(t *testing.T) {
	t.Parallel()
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "stateful-server")
	targetLabels := types.OrderedMap[string]{}
	targetLabels.Set(types.ComponentTypeLabel, "resource")
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/server/api-server", Labels: labels})
	nodes.Set("backing", types.IRNode{Component: "core/redis", Labels: targetLabels})
	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
			Edges: []types.IREdge{{
				Source: "api", Target: "backing", Socket: "metrics_target",
				OriginFeature: "core/metrics-exporter",
			}},
		},
	}
	featUses := types.OrderedMap[types.UsesSchemaEntry]{}
	featUses.Set("metrics_target", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=server"})
	feat := types.Feature{
		Metadata: types.FeatureMetadata{Name: "core/metrics-exporter"},
		Spec:     types.FeatureSpec{UsesSchema: featUses},
	}
	errs := validateSelectors(graph, analyzerCatalogWithFeature(feat))
	if len(errs) != 1 {
		t.Fatalf("expected 1 no-match error from Feature-resolved selector; got %d: %v", len(errs), errs)
	}
	if errs[0].Selector != "component.aggrega.ai/type=server" {
		t.Errorf("expected selector resolved via Feature usesSchema; got %q", errs[0].Selector)
	}
}

// TestFormatAppliesTo_MultiLabelOutput pins the multi-label branch of
// formatAppliesTo's diagnostic shaping. Two or more labels render in
// stable alphabetical order joined by comma. The function also surfaces
// optional count-form suffixes for appliesToExpressions and
// appliesToAnyOf when those structured-form lists are non-empty.
func TestValidateSelectors_PopulatesOriginFileFromSourceNode(t *testing.T) {
	t.Parallel()
	// Two nodes authored in different files, both fail the same selector
	// against their target. The analyzer must stamp each violation's
	// OriginFile from its source node's SourceFile, so a downstream
	// per-file filter can partition the diagnostics cleanly.
	apiLabels := types.OrderedMap[string]{}
	apiLabels.Set(types.ComponentTypeLabel, "server")
	cacheLabels := types.OrderedMap[string]{}
	cacheLabels.Set(types.ComponentTypeLabel, "resource")

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api-root", types.IRNode{
		Component:  "core/server/api-server",
		Labels:     apiLabels,
		SourceFile: "/workspace/root.yaml",
	})
	nodes.Set("api-sub", types.IRNode{
		Component:  "core/server/api-server",
		Labels:     apiLabels,
		SourceFile: "/workspace/sub.yaml",
	})
	nodes.Set("cache", types.IRNode{
		Component: "core/redis",
		Labels:    cacheLabels,
	})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
			Edges: []types.IREdge{
				{Source: "api-root", Target: "cache", Socket: "databases"},
				{Source: "api-sub", Target: "cache", Socket: "databases"},
			},
		},
	}

	errs := validateSelectors(graph, analyzerCatalog())
	if len(errs) != 2 {
		t.Fatalf("expected 2 selector violations, got %d", len(errs))
	}

	got := map[string]string{}
	for _, e := range errs {
		got[e.Source] = e.OriginFile
	}
	want := map[string]string{
		"api-root": "/workspace/root.yaml",
		"api-sub":  "/workspace/sub.yaml",
	}
	for source, wantFile := range want {
		if got[source] != wantFile {
			t.Errorf("source %q: OriginFile = %q; want %q", source, got[source], wantFile)
		}
	}
}

func TestValidateAppliesTo_PopulatesOriginFileFromAttachingNode(t *testing.T) {
	t.Parallel()
	appliesTo := types.OrderedMap[string]{}
	appliesTo.Set(types.ComponentTypeLabel, "server")

	feat := types.Feature{
		Metadata: types.FeatureMetadata{Name: "core/monitoring"},
		Spec: types.FeatureSpec{
			AppliesTo: appliesTo,
		},
	}

	mismatch := types.OrderedMap[string]{}
	mismatch.Set(types.ComponentTypeLabel, "stateful-server")

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db-root", types.IRNode{
		Component:  "core/database/postgres",
		Labels:     mismatch,
		SourceFile: "/workspace/root.yaml",
	})
	nodes.Set("db-sub", types.IRNode{
		Component:  "core/database/postgres",
		Labels:     mismatch,
		SourceFile: "/workspace/sub.yaml",
	})

	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	for _, node := range []string{"db-root", "db-sub"} {
		inner := types.OrderedMap[types.IRFeatureEntry]{}
		inner.Set("monitoring", types.IRFeatureEntry{Type: "core/monitoring"})
		fvm.Set(node, inner)
	}

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes:                nodes,
			FeatureValuesMapping: fvm,
		},
	}

	errs := validateAppliesTo(graph, analyzerCatalogWithFeature(feat))
	if len(errs) != 2 {
		t.Fatalf("expected 2 appliesTo violations, got %d", len(errs))
	}

	got := map[string]string{}
	for _, e := range errs {
		got[e.Source] = e.OriginFile
	}
	want := map[string]string{
		"db-root": "/workspace/root.yaml",
		"db-sub":  "/workspace/sub.yaml",
	}
	for source, wantFile := range want {
		if got[source] != wantFile {
			t.Errorf("source %q: OriginFile = %q; want %q", source, got[source], wantFile)
		}
	}
}

func TestFormatAppliesTo_MultiLabelOutput(t *testing.T) {
	t.Parallel()
	appliesTo := types.OrderedMap[string]{}
	appliesTo.Set("component.aggrega.ai/type", "server")
	appliesTo.Set("compliance.aggrega.ai/tier", "pci-dss")
	spec := types.FeatureSpec{
		AppliesTo:            appliesTo,
		AppliesToExpressions: []types.LabelSelectorRequirement{{Key: "k", Operator: "In", Values: []string{"v"}}},
		AppliesToAnyOf:       []types.SelectorTerm{{}, {}},
	}
	out := formatAppliesTo(spec)
	for _, want := range []string{
		"component.aggrega.ai/type=server",
		"compliance.aggrega.ai/tier=pci-dss",
		"appliesToExpressions[1]",
		"appliesToAnyOf[2]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("formatAppliesTo output missing %q; got: %s", want, out)
		}
	}
}
