// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/internal/sourceref"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// frDocumentLookup returns a RecipeLookup that reports documentPath as the file
// every FeatureRecipe was declared in. It is the half of the lookup a render
// needs in order to anchor what a source template produced and to position the
// diagnostics its sub-passes raise.
func frDocumentLookup(documentPath string) RecipeLookup {
	return RecipeLookup{
		FeatureRecipeDocument: func(*types.FeatureRecipe) (string, bool) {
			return documentPath, true
		},
	}
}

// boundaryScopeFRWithSource builds a boundary-scope FeatureRecipe carrying the
// given source and locals block, plus the graph and release a boundary dispatch
// reads around it.
func boundaryScopeFRWithSource(source any, locals types.OrderedMap[string]) (*types.FeatureRecipe, *types.IRGraph, *types.Release) {
	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "compose-bridge-fr", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Capability:     "stub",
			Implements:     "core/compose-bridge:1.0.0",
			ExecutionScope: "boundary",
			Source:         source,
			Locals:         locals,
			ValuesMapping:  types.ValuesMapping{Inline: "{}"},
		},
	}
	clusterBF := types.OrderedMap[types.IRBoundaryFeature]{}
	clusterBF.Set("bridge", types.IRBoundaryFeature{Type: "core/compose-bridge:1.0.0", Infra: types.OrderedMap[any]{}})
	graph := &types.IRGraph{Metadata: types.IRMetadata{AggregateName: "bridge-app"}}
	graph.Spec.BoundaryFeatures.Set("cluster", clusterBF)
	graph.Spec.Nodes.Set("api", types.IRNode{Component: "core/api", BoundaryPath: "cluster"})
	release := &types.Release{}
	release.Spec.Boundaries.Set("cluster", types.Boundary{Description: "cluster"})
	return fr, graph, release
}

// TestDispatchBoundaryScopeFR_TemplatedSourceIsEvaluatedAndAnchored drives the
// real boundary-scope dispatch for a FeatureRecipe whose source: is a template
// producing a RELATIVE path, and asserts the provider was handed the path that
// template produced, resolved against the document the FeatureRecipe was declared
// in.
//
// The two halves are asserted together on purpose. An evaluation that produced
// the right text and anchored it nowhere hands a provider a path relative to
// whatever directory the build happens to run from, which is a different source on
// every machine; an anchoring with no evaluation hands it the template's own
// spelling joined to a directory. Only the pair is the capability.
func TestDispatchBoundaryScopeFR_TemplatedSourceIsEvaluatedAndAnchored(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	document := filepath.Join(root, "catalog", "compose-bridge", "featurerecipe.yaml")

	locals := types.OrderedMap[string]{}
	locals.Set("chart_dir", "./charts/bridge")
	fr, graph, release := boundaryScopeFRWithSource("{{ .locals.chart_dir }}", locals)

	var captured any
	factory := func(string) (Provider, error) { return sourceCaptureEngine{captured: &captured}, nil }
	key := boundaryFRKey{BoundaryPath: "cluster", FRInstance: "bridge"}

	if err := dispatchBoundaryScopeFR(
		context.Background(), fr, "bridge", key, &PhaseBState{}, NewBoundaryFRState(),
		NodeRenderContext{}, factory, frDocumentLookup(document), graph, release, nil, nil,
		RendererInput{Lockfile: &lockfile.Lockfile{Version: 1}, WorkspaceRoot: root, CacheDir: ".aggrega/cache"},
	); err != nil {
		t.Fatalf("dispatchBoundaryScopeFR (templated boundary-scope FR source): %v", err)
	}

	want := filepath.Join(root, "catalog", "compose-bridge", "charts", "bridge")
	if captured != want {
		t.Errorf("boundary-scope FR templated source resolved to %#v; want %q — an unevaluated template reaches the provider as literal text and stats nothing", captured, want)
	}
}

// TestDispatchBoundaryScopeFR_LiteralRelativeSourceIsNotAnchored is the negative
// control for the test above, and it is sharper than the existing
// verbatim-forward lock because the document IS wired here: a plain relative
// literal must still reach the provider exactly as written even when there is a
// document to anchor against.
//
// That is not symmetry for its own sake. A literal relative source was already
// anchored once, at load, against the same document — so anchoring it a second
// time here would join the directory twice and name a path no author wrote.
func TestDispatchBoundaryScopeFR_LiteralRelativeSourceIsNotAnchored(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	document := filepath.Join(root, "catalog", "compose-bridge", "featurerecipe.yaml")

	fr, graph, release := boundaryScopeFRWithSource("./charts/local-bridge", types.OrderedMap[string]{})

	var captured any
	factory := func(string) (Provider, error) { return sourceCaptureEngine{captured: &captured}, nil }
	key := boundaryFRKey{BoundaryPath: "cluster", FRInstance: "bridge"}

	if err := dispatchBoundaryScopeFR(
		context.Background(), fr, "bridge", key, &PhaseBState{}, NewBoundaryFRState(),
		NodeRenderContext{}, factory, frDocumentLookup(document), graph, release, nil, nil,
		RendererInput{Lockfile: &lockfile.Lockfile{Version: 1}, WorkspaceRoot: root, CacheDir: ".aggrega/cache"},
	); err != nil {
		t.Fatalf("dispatchBoundaryScopeFR (literal relative source): %v", err)
	}

	if captured != "./charts/local-bridge" {
		t.Errorf("literal relative boundary-scope FR source reached the provider as %#v; want it untouched — the load already anchored it", captured)
	}
}

// TestDispatchBoundaryScopeFR_DiagnosticNamesTheFeatureRecipeDocument drives a
// FeatureRecipe render to a halt inside the workload sub-pass and asserts the
// diagnostic names the file the FeatureRecipe was declared in.
//
// The sub-passes an attachment's render runs are the component path's own, and
// they position every diagnostic they raise at the document the render state
// carries. An attachment render that carries no document positions all of them at
// no file, which is a diagnostic naming a fault an author cannot locate.
func TestDispatchBoundaryScopeFR_DiagnosticNamesTheFeatureRecipeDocument(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	document := filepath.Join(root, "catalog", "compose-bridge", "featurerecipe.yaml")

	fr, graph, release := boundaryScopeFRWithSource(nil, types.OrderedMap[string]{})
	// A workload template whose OUTPUT is not a YAML document: the sub-pass parses
	// it, refuses, and positions the refusal at the declaring file.
	fr.Spec.Workload = types.WorkloadTemplate{Inline: "not: [a, valid\nworkload: {{"}

	var captured any
	factory := func(string) (Provider, error) { return sourceCaptureEngine{captured: &captured}, nil }
	key := boundaryFRKey{BoundaryPath: "cluster", FRInstance: "bridge"}

	err := dispatchBoundaryScopeFR(
		context.Background(), fr, "bridge", key, &PhaseBState{}, NewBoundaryFRState(),
		NodeRenderContext{}, factory, frDocumentLookup(document), graph, release, nil, nil,
		RendererInput{},
	)
	if err == nil {
		t.Fatal("expected the malformed FeatureRecipe workload to halt")
	}
	if !strings.Contains(err.Error(), document) {
		t.Errorf("FeatureRecipe diagnostic does not name its declaring document %q; got:\n%v", document, err)
	}
}

// TestAnchorSourceToDocument covers the one home of the step that resolves what a
// source template PRODUCED against the document that declared it, across every
// shape reaching it.
//
// The authored value and the produced value are separate arguments, and the rows
// below are why: the decision is a property of what the author wrote. A produced
// value that happens to carry no template action is still owed the anchoring where
// the authored text had one, and a produced value that happens to look like a
// template is owed nothing where the authored text was a literal.
func TestAnchorSourceToDocument(t *testing.T) {
	t.Parallel()
	const document = "/catalog/features/mon/featurerecipe.yaml"
	tests := []struct {
		name     string
		authored any
		produced any
		document string
		want     any
	}{
		{"templated scalar producing a relative path anchors", "{{ .locals.dir }}", "./charts/mon", document, "/catalog/features/mon/charts/mon"},
		{"templated scalar producing a transport reference passes through", "{{ .locals.url }}", "oci://hub.aggrega.ai/charts/mon", document, "oci://hub.aggrega.ai/charts/mon"},
		{"templated scalar producing nothing leaves the node sourceless", "{{ .locals.dir }}", "", document, ""},
		{"literal relative path is not anchored a second time", "./charts/mon", "./charts/mon", document, "./charts/mon"},
		{"no declaring document leaves the produced value as produced", "{{ .locals.dir }}", "./charts/mon", "", "./charts/mon"},
		{"a structured source written out in full passes through", map[string]any{"url": "oci://x"}, map[string]any{"url": "oci://x"}, document, map[string]any{"url": "oci://x"}},
		{"a structured source's literal url anchored at load is not anchored again", map[string]any{"url": "/catalog/features/mon/charts/mon"}, map[string]any{"url": "/catalog/features/mon/charts/mon"}, document, map[string]any{"url": "/catalog/features/mon/charts/mon"}},
		{"a templated structured url producing a relative path anchors", map[string]any{"url": "{{ .locals.dir }}"}, map[string]any{"url": "./charts/mon"}, document, map[string]any{"url": "/catalog/features/mon/charts/mon"}},
		{"a templated structured url producing a transport reference passes through", map[string]any{"url": "{{ .locals.url }}"}, map[string]any{"url": "oci://x"}, document, map[string]any{"url": "oci://x"}},
		{"a sourceless entity passes through", nil, nil, document, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := anchorSourceToDocument(tc.authored, tc.produced, tc.document)
			if err != nil {
				t.Fatalf("anchorSourceToDocument: %v", err)
			}
			if gotMap, ok := got.(map[string]any); ok {
				wantMap, _ := tc.want.(map[string]any)
				if len(gotMap) != len(wantMap) || gotMap["url"] != wantMap["url"] {
					t.Errorf("anchorSourceToDocument = %#v; want %#v", got, tc.want)
				}
				return
			}
			if got != tc.want {
				t.Errorf("anchorSourceToDocument = %#v; want %#v", got, tc.want)
			}
		})
	}
}

// TestAnchorSourceToDocumentRefusesAProducedPathOutsideTheDocumentDirectory is the
// render's half of the bound on a source's path. A template names nothing until it
// runs, so this is the one moment its value can be judged; each row produces a path
// that leaves the declaring document's directory, in each shape a source is
// authored in, and each is refused with the condition every content path meets.
func TestAnchorSourceToDocumentRefusesAProducedPathOutsideTheDocumentDirectory(t *testing.T) {
	t.Parallel()
	const document = "/catalog/features/mon/featurerecipe.yaml"
	for _, tc := range []struct {
		name     string
		authored any
		produced any
		document string
	}{
		{"a templated scalar climbing out", "{{ .locals.dir }}", "../../shared/charts/mon", document},
		{"a templated scalar producing an absolute path", "{{ .locals.dir }}", "/opt/charts/mon", document},
		{"a templated structured url climbing out", map[string]any{"url": "{{ .locals.dir }}"}, map[string]any{"url": "../charts/mon"}, document},
		{"a templated structured url producing an absolute path", map[string]any{"url": "{{ .locals.dir }}"}, map[string]any{"url": "/opt/charts/mon"}, document},
		{"a templated scalar climbing out with no declaring document", "{{ .locals.dir }}", "../charts/mon", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := anchorSourceToDocument(tc.authored, tc.produced, tc.document)
			if err == nil {
				t.Fatal("a produced path outside the declaring directory was accepted")
			}
			if !errors.Is(err, sourceref.ErrContentOutsideEntityDirectory()) {
				t.Errorf("refused for another reason: %v", err)
			}
		})
	}
}

// TestEvaluateFeatureRecipeSource_RefusesAnUndeclaredLocal proves the attachment
// source surface halts on a local its block does not declare rather than
// rendering the key away — the same refusal a locals entry gets for the same
// mistake, and the reason the reference check is on this surface at all.
func TestEvaluateFeatureRecipeSource_RefusesAnUndeclaredLocal(t *testing.T) {
	t.Parallel()
	locals := types.OrderedMap[string]{}
	locals.Set("chart_dir", "./charts/mon")

	if _, err := EvaluateFeatureRecipeSource(
		"{{ .locals.chart_dir }}", "mon", "api", "",
		types.OrderedMap[any]{}, types.OrderedMap[any]{}, locals, redact.Keys{}); err != nil {
		t.Fatalf("a declared local must resolve: %v", err)
	}

	_, err := EvaluateFeatureRecipeSource(
		"{{ .locals.chart_directory }}", "mon", "api", "",
		types.OrderedMap[any]{}, types.OrderedMap[any]{}, locals, redact.Keys{})

	if err == nil {
		t.Fatal("a FeatureRecipe source naming an undeclared local must halt, not render the key away")
	}
}

// TestEvaluateFeatureRecipeSource_AdmitsTheAttachmentName proves the attachment
// source surface admits .attachment_name, which the base Recipe's source surface
// refuses. The difference is the reason the two surfaces carry different scopes:
// an attachment IS being resolved here, so the name of the instance filling the
// slot is a fact, and a source template may compose a path from it.
func TestEvaluateFeatureRecipeSource_AdmitsTheAttachmentName(t *testing.T) {
	t.Parallel()
	got, err := EvaluateFeatureRecipeSource(
		"./charts/{{ .attachment_name }}", "mon", "api", "",
		types.OrderedMap[any]{}, types.OrderedMap[any]{}, types.OrderedMap[string]{}, redact.Keys{})

	if err != nil {
		t.Fatalf("EvaluateFeatureRecipeSource: %v", err)
	}
	if got != "./charts/mon" {
		t.Errorf("attachment source = %#v; want %q", got, "./charts/mon")
	}

	if _, err := EvaluateRecipeSource(
		"./charts/{{ .attachment_name }}", "api",
		types.OrderedMap[any]{}, types.OrderedMap[any]{}, types.OrderedMap[string]{}, redact.Keys{}); err == nil {
		t.Error("a base Recipe source must still refuse .attachment_name — its source resolves for a node, not for an attachment")
	}
}
