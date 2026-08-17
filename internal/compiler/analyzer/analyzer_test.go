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
