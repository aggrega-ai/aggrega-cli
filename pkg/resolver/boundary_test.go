// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver_test

import (
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestWireBoundaryInfra_BoundaryComponentAnnotationsOverride(t *testing.T) {
	t.Parallel()
	annotations2 := types.OrderedMap[string]{}
	annotations2.Set("aggrega.ai/namespace", "backend-api-ns")
	annotations := types.OrderedMap[string]{}
	annotations.Set("aggrega.ai/namespace", "backend-zone")
	annotations.Set("aggrega.ai/image-registry", "registry.example.com")
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
