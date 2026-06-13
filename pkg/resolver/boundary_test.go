// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// scalarAnnotation returns an AnnotationValue wrapping a scalar string,
// pointer-allocated so it can be stored in an OrderedMap[*AnnotationValue].
// Used to populate annotation fixtures in tests where the author-intent is
// a non-nil explicit scalar value.
func scalarAnnotation(s string) *types.AnnotationValue {
	return &types.AnnotationValue{Scalar: &s}
}

func TestWireBoundaryInfra_BoundaryComponentAnnotationsOverride(t *testing.T) {
	t.Parallel()
	annotations2 := types.OrderedMap[*types.AnnotationValue]{}
	annotations2.Set("aggrega.ai/namespace", scalarAnnotation("backend-api-ns"))
	annotations := types.OrderedMap[*types.AnnotationValue]{}
	annotations.Set("aggrega.ai/namespace", scalarAnnotation("backend-zone"))
	annotations.Set("aggrega.ai/image-registry", scalarAnnotation("registry.example.com"))
	components := types.OrderedMap[types.BoundaryComponent]{}
	components.Set("api", types.BoundaryComponent{Annotations: annotations2})
	components.Set("worker", types.BoundaryComponent{})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set("backend-zone", types.Boundary{Annotations: annotations, Components: components})

	rel := &types.Release{
		Spec: types.ReleaseSpec{
			Boundaries: boundaries,
		},
	}

	bi, _ := resolver.WireBoundaryInfra(rel)

	api := bi["api"]
	if api == nil {
		t.Fatal("missing api BoundaryInfra")
	}
	if got, _ := api.Annotations.Get("aggrega.ai/namespace"); got != "backend-api-ns" {
		t.Errorf("api namespace: want backend-api-ns, got %q", got)
	}
	if got, _ := api.Annotations.Get("aggrega.ai/image-registry"); got != "registry.example.com" {
		t.Errorf("api should inherit image-registry, got %q", got)
	}

	worker := bi["worker"]
	if worker == nil {
		t.Fatal("missing worker BoundaryInfra")
	}
	if got, _ := worker.Annotations.Get("aggrega.ai/namespace"); got != "backend-zone" {
		t.Errorf("worker namespace: want inherited backend-zone, got %q", got)
	}
}

func newComps(names ...string) types.OrderedMap[types.BoundaryComponent] {
	var m types.OrderedMap[types.BoundaryComponent]
	for _, n := range names {
		m.Set(n, types.BoundaryComponent{})
	}
	return m
}

// TestNormalizeReleaseScopes pins the load-time desugaring of the top-level
// components surface into a synthesized `default` boundary.
func TestNormalizeReleaseScopes(t *testing.T) {
	t.Parallel()

	t.Run("components-only synthesizes a marked default holding the components", func(t *testing.T) {
		rel := &types.Release{}
		rel.Spec.Components = newComps("api", "worker")
		resolver.NormalizeReleaseScopes(rel)

		if got := rel.Spec.Boundaries.Len(); got != 1 {
			t.Fatalf("want exactly 1 synthesized boundary; got %d", got)
		}
		if keys := rel.Spec.Boundaries.Keys(); len(keys) == 0 || keys[0] != "default" {
			t.Fatalf("synthesized boundary must be named 'default' and ordered first; keys=%v", keys)
		}
		b, _ := rel.Spec.Boundaries.Get("default")
		if !b.Synthesized {
			t.Error("synthesized boundary must carry Synthesized=true")
		}
		if b.Components.Len() != 2 {
			t.Errorf("synthesized default must hold the 2 top-level components; got %d", b.Components.Len())
		}
		if rel.Spec.Components.Len() != 0 {
			t.Errorf("top-level components must move into the boundary (cleared); got %d", rel.Spec.Components.Len())
		}
	})

	t.Run("a declared boundary is a no-op (never marked synthesized)", func(t *testing.T) {
		rel := &types.Release{}
		var b types.OrderedMap[types.Boundary]
		b.Set("main", types.Boundary{Components: newComps("api")})
		rel.Spec.Boundaries = b
		resolver.NormalizeReleaseScopes(rel)

		if _, ok := rel.Spec.Boundaries.Get("default"); ok {
			t.Error("must not synthesize a default when boundaries are declared")
		}
		main, _ := rel.Spec.Boundaries.Get("main")
		if main.Synthesized {
			t.Error("a declared boundary must never be marked Synthesized")
		}
	})

	t.Run("empty release synthesizes nothing", func(t *testing.T) {
		rel := &types.Release{}
		resolver.NormalizeReleaseScopes(rel)
		if rel.Spec.Boundaries.Len() != 0 {
			t.Errorf("no components and no boundaries must synthesize nothing; got %d", rel.Spec.Boundaries.Len())
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		rel := &types.Release{}
		rel.Spec.Components = newComps("api")
		resolver.NormalizeReleaseScopes(rel)
		resolver.NormalizeReleaseScopes(rel)
		if rel.Spec.Boundaries.Len() != 1 {
			t.Errorf("a second call must be a no-op; got %d boundaries", rel.Spec.Boundaries.Len())
		}
	})
}
