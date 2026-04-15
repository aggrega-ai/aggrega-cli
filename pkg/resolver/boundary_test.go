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
			Boundaries: map[string]types.Boundary{
				"backend-zone": {
					Annotations: map[string]string{
						"aggrega.ai/namespace":      "backend-zone",
						"aggrega.ai/image-registry": "registry.example.com",
					},
					Components: map[string]types.BoundaryComponent{
						"api": {
							Annotations: map[string]string{
								// overrides namespace per component
								"aggrega.ai/namespace": "backend-api-ns",
							},
						},
						"worker": {
							// no overrides — inherits
						},
					},
				},
			},
		},
	}

	bi := resolver.WireBoundaryInfra(rel)

	api := bi["api"]
	if api == nil {
		t.Fatal("missing api BoundaryInfra")
	}
	if got := api.Annotations["aggrega.ai/namespace"]; got != "backend-api-ns" {
		t.Errorf("api namespace: want backend-api-ns, got %q", got)
	}
	if got := api.Annotations["aggrega.ai/image-registry"]; got != "registry.example.com" {
		t.Errorf("api should inherit image-registry, got %q", got)
	}

	worker := bi["worker"]
	if worker == nil {
		t.Fatal("missing worker BoundaryInfra")
	}
	if got := worker.Annotations["aggrega.ai/namespace"]; got != "backend-zone" {
		t.Errorf("worker namespace: want inherited backend-zone, got %q", got)
	}
}
