// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — end-to-end proof that a feature attached to a single
// component from a Release carries its `uses` block into the built graph: the
// edge is drawn, it is stamped with the attachment as its origin, and the
// consuming component is injected the target's provides all the way to rendered
// output. The twin case — the same attachment declared for a whole boundary — is
// covered separately; these two surfaces are one feature and are proved to
// behave alike.

package compiler_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const bcFeatureUsesCatalog = "testdata/boundary-component-feature-uses/catalog/base"

// TestBoundaryComponentFeatureUses_EdgeEntersGraph pins the shape of the edge the
// attachment contributes: exactly one edge, from the component the attachment
// sits on to the target it names, on the socket and alias authored, carrying the
// attachment's feature type and instance name as its origin. The Aggregate
// declares no topology at all, so an edge present here can only have come from
// the Release attachment.
func TestBoundaryComponentFeatureUses_EdgeEntersGraph(t *testing.T) {
	t.Parallel()

	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/boundary-component-feature-uses/release.yaml",
		CatalogPaths: []string{bcFeatureUsesCatalog},
	})

	if got := len(result.IRGraph.Spec.Edges); got != 1 {
		t.Fatalf("edge count = %d, want exactly 1; got edges %+v", got, result.IRGraph.Spec.Edges)
	}
	e := result.IRGraph.Spec.Edges[0]
	if e.Source != "api" || e.Target != "db" || e.Socket != "backend" || e.Alias != "primary" {
		t.Errorf("edge = source %q target %q socket %q alias %q; want api/db/backend/primary",
			e.Source, e.Target, e.Socket, e.Alias)
	}
	// Origin stamping is what keeps an attachment-authored edge separable from a
	// component-authored one, and it must match what the boundary-wide surface
	// stamps: the feature type plus the attachment's instance name.
	if e.OriginFeature != "core/shared-backend" {
		t.Errorf("edge OriginFeature = %q, want %q", e.OriginFeature, "core/shared-backend")
	}
	if e.OriginFeatureInstance != "shared_backend" {
		t.Errorf("edge OriginFeatureInstance = %q, want %q", e.OriginFeatureInstance, "shared_backend")
	}
}

// TestBoundaryComponentFeatureUses_RendersInjectedProvides closes the render-level
// loop: the consumer's DB_HOST environment variable carries the target's `host`
// provides. With no bridge FeatureRecipe in this fixture the value is the
// target's plain host provides (its instance id, "db"), so the value proves the
// injection travelled the edge rather than being rewritten per consumer.
//
// The absence half is asserted on the same output and is the invariant under
// test rather than a nicety: per-edge configuration is consumer-scoped, so the
// target's own rendered service must carry no trace of the edge — its output is
// invariant with respect to who consumes it.
func TestBoundaryComponentFeatureUses_RendersInjectedProvides(t *testing.T) {
	t.Parallel()

	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/boundary-component-feature-uses/release.yaml",
		CatalogPaths: []string{bcFeatureUsesCatalog},
	})

	const wantPath = "dist/cluster/compose/docker-compose.yaml"
	body, ok := result.Artifacts[wantPath]
	if !ok {
		paths := make([]string, 0, len(result.Artifacts))
		for p := range result.Artifacts {
			paths = append(paths, p)
		}
		t.Fatalf("expected merged compose at %q; got paths %v", wantPath, paths)
	}
	merged := string(body)

	for _, svcName := range []string{"api:", "db:"} {
		if !strings.Contains(merged, svcName) {
			t.Errorf("merged docker-compose.yaml missing service %q\n--- body ---\n%s", svcName, merged)
		}
	}
	if !strings.Contains(merged, "DB_HOST: db") {
		t.Errorf("expected api's DB_HOST to be injected with db's host provides ('db') via the attachment's uses edge:\n%s", merged)
	}
	// Exactly one consumer receives it: a second occurrence would mean the edge
	// leaked into the target's context too.
	if got := strings.Count(merged, "DB_HOST"); got != 1 {
		t.Errorf("DB_HOST occurrences = %d, want 1 (consumer only; the target's output is invariant w.r.t. its consumers)\n%s", got, merged)
	}
}

// TestBoundaryComponentFeatureUses_DanglingTargetHalts pins that the attachment
// wires an edge and never creates compute: a target naming no component halts
// with the dangling-target sentinel instead of bringing that component into
// being.
func TestBoundaryComponentFeatureUses_DanglingTargetHalts(t *testing.T) {
	t.Parallel()

	_, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  "testdata/boundary-component-feature-uses/release-dangling.yaml",
		CatalogPaths: []string{bcFeatureUsesCatalog},
	})
	if !errors.Is(err, topology.ErrDanglingUsesTarget()) {
		t.Fatalf("want ErrDanglingUsesTarget for an attachment edge naming a non-existent target; got %v", err)
	}
}

// TestBoundaryComponentFeatureUses_TargetNamedByGraphID pins which name the edge
// target is written with when the components live in a sub-module. A Release
// names such a component by the id it carries in the assembled graph — the
// module name and the local name joined with a dot — and the target on an
// attachment the Release declares is read the same way, so the two spellings in
// one document agree. The counterpart case, the module-local name, finds no
// component and halts.
func TestBoundaryComponentFeatureUses_TargetNamedByGraphID(t *testing.T) {
	t.Parallel()

	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/boundary-component-feature-uses/release-subagg-graphid.yaml",
		CatalogPaths: []string{bcFeatureUsesCatalog},
	})
	if got := len(result.IRGraph.Spec.Edges); got != 1 {
		t.Fatalf("edge count = %d, want exactly 1; got edges %+v", got, result.IRGraph.Spec.Edges)
	}
	if e := result.IRGraph.Spec.Edges[0]; e.Source != "data.svc" || e.Target != "data.store" {
		t.Errorf("edge = source %q target %q; want data.svc/data.store", e.Source, e.Target)
	}

	_, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  "testdata/boundary-component-feature-uses/release-subagg-localname.yaml",
		CatalogPaths: []string{bcFeatureUsesCatalog},
	})
	if !errors.Is(err, topology.ErrDanglingUsesTarget()) {
		t.Fatalf("want ErrDanglingUsesTarget for a target written with a module-local name; got %v", err)
	}
}

// TestBoundaryComponentFeatureUses_ReachedThroughRoutingEntry pins the same
// attachment declared on the other axis a boundary names its components by: a
// routing entry, where the component is keyed by the name it carries inside its
// module. The two spellings coexist in one entry — the key is module-local, the
// edge target is a graph id — and the edge that results is the same one the
// directly-named form draws.
//
// It also carries the consumer-scoping half at render level: the consumer's
// service receives the target's host provides, and the target's own service
// declares no environment at all, so a target's rendered output does not move
// when something starts consuming it.
func TestBoundaryComponentFeatureUses_ReachedThroughRoutingEntry(t *testing.T) {
	t.Parallel()

	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/boundary-component-feature-uses/release-subagg-routed.yaml",
		CatalogPaths: []string{bcFeatureUsesCatalog},
	})
	if got := len(result.IRGraph.Spec.Edges); got != 1 {
		t.Fatalf("edge count = %d, want exactly 1; got edges %+v", got, result.IRGraph.Spec.Edges)
	}
	e := result.IRGraph.Spec.Edges[0]
	if e.Source != "data.svc" || e.Target != "data.store" || e.OriginFeature != "core/shared-backend" {
		t.Errorf("edge = source %q target %q origin %q; want data.svc/data.store/core/shared-backend",
			e.Source, e.Target, e.OriginFeature)
	}

	merged := string(result.Artifacts["dist/cluster/compose/docker-compose.yaml"])
	if !strings.Contains(merged, "DB_HOST: data-zdstore") {
		t.Errorf("expected the consumer's DB_HOST to carry the target's host provides:\n%s", merged)
	}
	// The target's own service block runs to the next service key; it must declare
	// no environment mapping, which is the consumer-scoping invariant at render
	// level. The match is on the service-level key at its own indent, so the
	// `aggrega.ai/environment` label nested deeper is not mistaken for one.
	_, after, found := strings.Cut(merged, "\n  data-zdstore:\n")
	if !found {
		t.Fatalf("expected a data-zdstore service in the merged output:\n%s", merged)
	}
	targetBlock, _, _ := strings.Cut(after, "\n  data-zdsvc:\n")
	if strings.Contains(targetBlock, "\n    environment:") {
		t.Errorf("target's rendered service declares an environment; per-edge config must not reach it:\n%s", targetBlock)
	}
	// A positive control on the same probe: the consumer's block, read the same
	// way, does declare one — so an always-false probe cannot pass this test.
	_, afterConsumer, foundConsumer := strings.Cut(merged, "\n  data-zdsvc:\n")
	if !foundConsumer {
		t.Fatalf("expected a data-zdsvc service in the merged output:\n%s", merged)
	}
	if !strings.Contains(afterConsumer, "\n    environment:") {
		t.Errorf("consumer's rendered service declares no environment, so the absence probe above proves nothing:\n%s", merged)
	}
}

// TestBoundaryComponentFeatureUses_MatchesBoundaryWideShape pins the two Release
// attachment surfaces against each other on the property the schema promises of
// both: the same `uses` block produces an edge of the same shape and the same
// origin stamp whether it is declared for one component or for the whole
// boundary. The boundary-wide document holds the consumer and the target in
// sibling boundaries so the wide attachment reaches only the consumer, which is
// what makes the two edge sets comparable; a divergence in that set means the
// surfaces are not one feature.
func TestBoundaryComponentFeatureUses_MatchesBoundaryWideShape(t *testing.T) {
	t.Parallel()

	perComponent := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/boundary-component-feature-uses/release.yaml",
		CatalogPaths: []string{bcFeatureUsesCatalog},
	})
	boundaryWide := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/boundary-component-feature-uses/release-boundary-wide.yaml",
		CatalogPaths: []string{bcFeatureUsesCatalog},
	})

	type edgeShape struct {
		source, target, socket, alias, originFeature, originInstance string
	}
	shapes := func(g *types.IRGraph) []edgeShape {
		out := make([]edgeShape, 0, len(g.Spec.Edges))
		for _, e := range g.Spec.Edges {
			out = append(out, edgeShape{e.Source, e.Target, e.Socket, e.Alias, e.OriginFeature, e.OriginFeatureInstance})
		}
		return out
	}

	want := []edgeShape{{"api", "db", "backend", "primary", "core/shared-backend", "shared_backend"}}
	for _, tc := range []struct {
		label string
		got   []edgeShape
	}{
		{"per-component", shapes(perComponent.IRGraph)},
		{"boundary-wide", shapes(boundaryWide.IRGraph)},
	} {
		if !reflect.DeepEqual(tc.got, want) {
			t.Errorf("%s surface: edges = %+v, want %+v", tc.label, tc.got, want)
		}
	}
}
