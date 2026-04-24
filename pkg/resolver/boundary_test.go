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

	rel := &types.Release{
		Spec: types.ReleaseSpec{
			Boundaries: func() types.OrderedMap[types.Boundary] { _m := types.OrderedMap[types.Boundary]{}; _m.Set("backend-zone", types.Boundary{
					Annotations: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set("aggrega.ai/namespace", "backend-zone"); _m.Set("aggrega.ai/image-registry", "registry.example.com"); return _m }(),
					Components: func() types.OrderedMap[types.BoundaryComponent] { _m := types.OrderedMap[types.BoundaryComponent]{}; _m.Set("api", types.BoundaryComponent{
							Annotations: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set("aggrega.ai/namespace", "backend-api-ns"); return _m }(),
						}); _m.Set("worker", types.BoundaryComponent{
							
						}); return _m }(),
				}); return _m }(),
		},
	}

	bi := resolver.WireBoundaryInfra(rel)

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
