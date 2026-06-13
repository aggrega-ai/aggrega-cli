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

	bi, _ := resolver.WireBoundaryInfra(types.NormalizeRelease(rel))

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

// NormalizeRelease's non-mutating contract is unit-tested in pkg/types
// (TestNormalizeRelease), symmetric to TestNormalize for the Aggregate side.
