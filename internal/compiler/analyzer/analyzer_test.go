// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func analyzerCatalog() resolver.CatalogLookup {
	return resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case "core/compute/api-server":
				labels := types.OrderedMap[string]{}
				labels.Set(types.ComponentTypeLabel, "server")
				usesSchema := types.OrderedMap[types.UsesSchemaEntry]{}
				usesSchema.Set("databases", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=stateful-server"})
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:   "core/compute/api-server",
						Labels: labels,
					},
					Spec: types.ComponentSpec{
						UsesSchema: usesSchema,
					},
				}, true
			case "core/datastore/postgres":
				labels2 := types.OrderedMap[string]{}
				labels2.Set(types.ComponentTypeLabel, "stateful-server")
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:   "core/datastore/postgres",
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
	nodes.Set("api", types.IRNode{Component: "core/compute/api-server", Labels: labels})
	nodes.Set("main-db", types.IRNode{Component: "core/datastore/postgres", Labels: labels2})

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
	nodes.Set("api", types.IRNode{Component: "core/compute/api-server", Labels: labels})
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

// TestAnalyze_SelectorFail_ReleaseCreatedEdge pins that the Step-4 selector
// gate is origin-agnostic: a Release-created edge (OriginRelease) violating its
// source socket's selector surfaces the same AnalysisError as a developer
// edge — the analyzer walks all IR edges with no special-casing by origin.
func TestAnalyze_SelectorFail_ReleaseCreatedEdge(t *testing.T) {
	t.Parallel()
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "server")
	labels2 := types.OrderedMap[string]{}
	labels2.Set(types.ComponentTypeLabel, "resource")
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/compute/api-server", Labels: labels})
	nodes.Set("cache", types.IRNode{Component: "core/redis", Labels: labels2})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
			Edges: []types.IREdge{
				// Origin-agnostic: this edge was wired by a Release.
				{Source: "api", Target: "cache", Socket: "databases", OriginRelease: true},
			},
		},
	}

	result := Analyze(graph, analyzerCatalog())
	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 selector error for a Release-created edge, got %d", len(result.Errors))
	}
	e := result.Errors[0]
	if e.Source != "api" || e.Target != "cache" || e.Socket != "databases" {
		t.Errorf("error: %+v", e)
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
	feat := types.Feature{
		Metadata: types.FeatureMetadata{Name: "core/monitoring"},
		Spec: types.FeatureSpec{
			AppliesTo: types.ComponentTypeLabel + "=server",
		},
	}
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "server")
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/compute/api-server", Labels: labels})

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
	feat := types.Feature{
		Metadata: types.FeatureMetadata{Name: "core/monitoring"},
		Spec: types.FeatureSpec{
			AppliesTo: types.ComponentTypeLabel + "=server",
		},
	}
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "stateful-server")
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db", types.IRNode{Component: "core/datastore/postgres", Labels: labels})

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

// appliesToGraph builds the one-node, one-attachment graph the appliesTo
// tests share: node "worker" labeled with the given type value, carrying
// Feature instance "mon" of type "core/monitoring".
func appliesToGraph(typeValue string) *types.IRGraph {
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, typeValue)
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("worker", types.IRNode{Component: "core/compute/api-server", Labels: labels})

	inner := types.OrderedMap[types.IRFeatureEntry]{}
	inner.Set("mon", types.IRFeatureEntry{Type: "core/monitoring"})
	outer := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	outer.Set("worker", inner)

	return &types.IRGraph{
		Spec: types.IRSpec{
			Nodes:                nodes,
			FeatureValuesMapping: outer,
		},
	}
}

// TestAnalyze_AppliesToStringOperators covers the string grammar's
// operator forms at the analyzer level against a node labeled
// type=worker: set membership, negated set, equality, inequality,
// bare-key existence, and !key absence, plus multi-requirement AND.
func TestAnalyze_AppliesToStringOperators(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		appliesTo string
		wantErrs  int
	}{
		{"in set match", types.ComponentTypeLabel + " in (server, worker)", 0},
		{"in set no match", types.ComponentTypeLabel + " in (server, task)", 1},
		{"notin set match", types.ComponentTypeLabel + " notin (server, task)", 0},
		{"equality no match", types.ComponentTypeLabel + "=server", 1},
		{"inequality match", types.ComponentTypeLabel + "!=server", 0},
		{"bare key exists match", types.ComponentTypeLabel, 0},
		{"absent key match", "!compliance.aggrega.ai/data-class", 0},
		{"absent key no match", "!" + types.ComponentTypeLabel, 1},
		{"comma AND one fails", types.ComponentTypeLabel + "=worker, env=prod", 1},
		{"empty selector matches all", "", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			feat := types.Feature{
				Metadata: types.FeatureMetadata{Name: "core/monitoring"},
				Spec:     types.FeatureSpec{AppliesTo: tt.appliesTo},
			}
			result := Analyze(appliesToGraph("worker"), analyzerCatalogWithFeature(feat))
			if len(result.Errors) != tt.wantErrs {
				t.Errorf("appliesTo %q: expected %d errors, got %d: %+v",
					tt.appliesTo, tt.wantErrs, len(result.Errors), result.Errors)
			}
		})
	}
}

// TestAnalyze_AppliesToAnyOf covers the OR surface at the analyzer
// level: polymorphic items (string-form and flat-map), any single
// matching item satisfying the attachment, and the AND across the two
// envelopes when both are declared.
func TestAnalyze_AppliesToAnyOf(t *testing.T) {
	t.Parallel()

	serverMap := types.OrderedMap[string]{}
	serverMap.Set(types.ComponentTypeLabel, "server")

	tests := []struct {
		name     string
		spec     types.FeatureSpec
		wantErrs int
	}{
		{
			"string item matches",
			types.FeatureSpec{AppliesToAnyOf: []types.SelectorTerm{
				types.StringTerm(types.ComponentTypeLabel + " in (worker, task)"),
			}},
			0,
		},
		{
			"map item fails, string item matches",
			types.FeatureSpec{AppliesToAnyOf: []types.SelectorTerm{
				types.MapTerm(serverMap),
				types.StringTerm(types.ComponentTypeLabel + "=worker"),
			}},
			0,
		},
		{
			"no item matches",
			types.FeatureSpec{AppliesToAnyOf: []types.SelectorTerm{
				types.MapTerm(serverMap),
				types.StringTerm(types.ComponentTypeLabel + "=task"),
			}},
			1,
		},
		{
			"both envelopes AND: appliesTo fails, anyOf matches",
			types.FeatureSpec{
				AppliesTo: "env=prod",
				AppliesToAnyOf: []types.SelectorTerm{
					types.StringTerm(types.ComponentTypeLabel + "=worker"),
				},
			},
			1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			feat := types.Feature{
				Metadata: types.FeatureMetadata{Name: "core/monitoring"},
				Spec:     tt.spec,
			}
			result := Analyze(appliesToGraph("worker"), analyzerCatalogWithFeature(feat))
			if len(result.Errors) != tt.wantErrs {
				t.Errorf("expected %d errors, got %d: %+v", tt.wantErrs, len(result.Errors), result.Errors)
			}
		})
	}
}

// TestAnalyze_AppliesToParseError pins the parse-error surfacing the
// appliesTo path shares with the usesSchema selector validator: a
// malformed selector produces an [AnalysisError] with the
// "selector parse error" message and the [ErrSelectorViolation]
// sentinel, never a silent skip.
func TestAnalyze_AppliesToParseError(t *testing.T) {
	t.Parallel()

	feat := types.Feature{
		Metadata: types.FeatureMetadata{Name: "core/monitoring"},
		Spec: types.FeatureSpec{
			AppliesTo: types.ComponentTypeLabel + " in (server", // unclosed set
		},
	}
	result := Analyze(appliesToGraph("worker"), analyzerCatalogWithFeature(feat))
	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d: %+v", len(result.Errors), result.Errors)
	}
	e := result.Errors[0]
	if !strings.Contains(e.Message, "selector parse error") {
		t.Errorf("message: %q, want a selector parse error", e.Message)
	}
	if e.Sentinel == nil || !errors.Is(e.Sentinel, ErrSelectorViolation()) {
		t.Errorf("sentinel: %v, want ErrSelectorViolation", e.Sentinel)
	}
	if e.Source != "worker" || e.Target != "mon" {
		t.Errorf("error fields: %+v", e)
	}

	// The same contract holds for a malformed string item inside
	// appliesToAnyOf.
	feat.Spec = types.FeatureSpec{
		AppliesToAnyOf: []types.SelectorTerm{
			types.StringTerm(types.ComponentTypeLabel + " in (server"),
		},
	}
	result = Analyze(appliesToGraph("worker"), analyzerCatalogWithFeature(feat))
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0].Message, "selector parse error") {
		t.Errorf("anyOf parse error: %+v", result.Errors)
	}
}

// capabilityAxisCatalog mirrors the standard-library shape of a tool
// component whose socket constrains the capability axis: the socket
// selector is a set-membership expression over a per-category capability
// label rather than the operational type label. Targets: one datastore
// carrying an in-set engine value, one carrying an out-of-set value.
func capabilityAxisCatalog() resolver.CatalogLookup {
	return resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case "core/ai-tool/retrieval":
				labels := types.OrderedMap[string]{}
				labels.Set(types.ComponentTypeLabel, "function")
				usesSchema := types.OrderedMap[types.UsesSchemaEntry]{}
				usesSchema.Set("target_datastore", types.UsesSchemaEntry{
					Selector: "datastore.aggrega.ai/engine in (vector, episodic, semantic-memory)",
					Required: true,
				})
				return types.Component{
					Metadata: types.ComponentMetadata{Name: "core/ai-tool/retrieval", Labels: labels},
					Spec:     types.ComponentSpec{UsesSchema: usesSchema},
				}, true
			case "core/datastore/vector-db":
				labels := types.OrderedMap[string]{}
				labels.Set(types.ComponentTypeLabel, "stateful-server")
				labels.Set("datastore.aggrega.ai/engine", "vector")
				return types.Component{
					Metadata: types.ComponentMetadata{Name: "core/datastore/vector-db", Labels: labels},
				}, true
			case "core/datastore/relational-db":
				labels := types.OrderedMap[string]{}
				labels.Set(types.ComponentTypeLabel, "stateful-server")
				labels.Set("datastore.aggrega.ai/engine", "relational")
				return types.Component{
					Metadata: types.ComponentMetadata{Name: "core/datastore/relational-db", Labels: labels},
				}, true
			default:
				return types.Component{}, false
			}
		},
		Feature: func(string) (types.Feature, bool) { return types.Feature{}, false },
	}
}

func capabilityAxisGraph(engineValue string) *types.IRGraph {
	toolLabels := types.OrderedMap[string]{}
	toolLabels.Set(types.ComponentTypeLabel, "function")
	dbLabels := types.OrderedMap[string]{}
	dbLabels.Set(types.ComponentTypeLabel, "stateful-server")
	dbLabels.Set("datastore.aggrega.ai/engine", engineValue)
	dbComponent := "core/datastore/vector-db"
	if engineValue != "vector" {
		dbComponent = "core/datastore/relational-db"
	}
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("tool", types.IRNode{Component: "core/ai-tool/retrieval", Labels: toolLabels})
	nodes.Set("db", types.IRNode{Component: dbComponent, Labels: dbLabels})
	return &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
			Edges: []types.IREdge{
				{Source: "tool", Target: "db", Socket: "target_datastore"},
			},
		},
	}
}

// TestAnalyze_CapabilityAxisSocket_Green pins the passing leg of the
// capability-axis socket contract: a set-membership selector over a
// per-category capability label admits a target whose label value is in
// the set.
func TestAnalyze_CapabilityAxisSocket_Green(t *testing.T) {
	t.Parallel()
	result := Analyze(capabilityAxisGraph("vector"), capabilityAxisCatalog())
	if len(result.Errors) != 0 {
		t.Errorf("expected 0 errors for in-set engine value, got %d: %+v", len(result.Errors), result.Errors)
	}
}

// TestAnalyze_CapabilityAxisSocket_Red pins the failing leg: a target
// whose capability-label value falls outside the selector's set trips
// the Step-4 gate with the ErrSelectorViolation sentinel.
func TestAnalyze_CapabilityAxisSocket_Red(t *testing.T) {
	t.Parallel()
	result := Analyze(capabilityAxisGraph("relational"), capabilityAxisCatalog())
	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 selector error for out-of-set engine value, got %d: %+v", len(result.Errors), result.Errors)
	}
	e := result.Errors[0]
	if e.Sentinel == nil || !errors.Is(e.Sentinel, ErrSelectorViolation()) {
		t.Errorf("sentinel: %v, want ErrSelectorViolation", e.Sentinel)
	}
	if e.Source != "tool" || e.Target != "db" || e.Socket != "target_datastore" {
		t.Errorf("error fields: %+v", e)
	}
	if e.Selector != "datastore.aggrega.ai/engine in (vector, episodic, semantic-memory)" {
		t.Errorf("selector: %q", e.Selector)
	}
}

func TestAnalyze_PathsCalculated(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("gateway", types.IRNode{Component: "core/compute/api-server"})
	nodes.Set("api", types.IRNode{Component: "core/compute/api-server"})
	nodes.Set("db", types.IRNode{Component: "core/datastore/postgres"})

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
	nodes.Set("api", types.IRNode{Component: "core/compute/api-server", ResolvedVersion: "1.0.0", Labels: labels})

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
	// Under b-full the policy input carries the full IR graph; the nodes live at
	// Graph.Spec.Nodes (an OrderedMap), not a narrow projection.
	if result.PolicyInput.Graph.Spec.Nodes.Len() != 1 {
		t.Errorf("expected 1 IR node, got %d", result.PolicyInput.Graph.Spec.Nodes.Len())
	}
	pn, ok := result.PolicyInput.Graph.Spec.Nodes.Get("api")
	if !ok || pn.Component != "core/compute/api-server" || pn.ResolvedVersion != "1.0.0" {
		t.Errorf("IR node: %+v (found=%v)", pn, ok)
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
	errs, _ := validateSelectors(context.Background(), graph, resolver.CatalogLookup{})
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
	errs, _ := validateSelectors(context.Background(), graph, cat)
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
	nodes.Set("api", types.IRNode{Component: "core/compute/api-server", Labels: labels})
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
	errs, _ := validateSelectors(context.Background(), graph, analyzerCatalogWithFeature(feat))
	if len(errs) != 1 {
		t.Fatalf("expected 1 no-match error from Feature-resolved selector; got %d: %v", len(errs), errs)
	}
	if errs[0].Selector != "component.aggrega.ai/type=server" {
		t.Errorf("expected selector resolved via Feature usesSchema; got %q", errs[0].Selector)
	}
}

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
		Component:  "core/compute/api-server",
		Labels:     apiLabels,
		SourceFile: "/workspace/root.yaml",
	})
	nodes.Set("api-sub", types.IRNode{
		Component:  "core/compute/api-server",
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

	errs, _ := validateSelectors(context.Background(), graph, analyzerCatalog())
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
	feat := types.Feature{
		Metadata: types.FeatureMetadata{Name: "core/monitoring"},
		Spec: types.FeatureSpec{
			AppliesTo: types.ComponentTypeLabel + "=server",
		},
	}

	mismatch := types.OrderedMap[string]{}
	mismatch.Set(types.ComponentTypeLabel, "stateful-server")

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db-root", types.IRNode{
		Component:  "core/datastore/postgres",
		Labels:     mismatch,
		SourceFile: "/workspace/root.yaml",
	})
	nodes.Set("db-sub", types.IRNode{
		Component:  "core/datastore/postgres",
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

	errs, _ := validateAppliesTo(context.Background(), graph, analyzerCatalogWithFeature(feat))
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

func TestFormatAppliesTo_SummaryOutput(t *testing.T) {
	t.Parallel()
	spec := types.FeatureSpec{
		AppliesTo: "component.aggrega.ai/type=server, compliance.aggrega.ai/tier=pci-dss",
		AppliesToAnyOf: []types.SelectorTerm{
			types.StringTerm("env=prod"),
			types.MapTerm(types.OrderedMap[string]{}),
		},
	}
	out := formatAppliesTo(spec)
	for _, want := range []string{
		`appliesTo:"component.aggrega.ai/type=server, compliance.aggrega.ai/tier=pci-dss"`,
		"appliesToAnyOf[2]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("formatAppliesTo output missing %q; got: %s", want, out)
		}
	}
	if empty := formatAppliesTo(types.FeatureSpec{}); empty != "" {
		t.Errorf("no-constraint spec should summarize empty; got %q", empty)
	}
}
