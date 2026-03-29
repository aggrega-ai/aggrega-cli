package helm

import (
	"context"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// HelmEngine renders IR graphs into Helm chart files.
type HelmEngine struct{}

// Render produces Helm chart bytes for the given scope. Stub: returns an empty artifact map.
//
// Future implementation notes:
//   - Consult release.Policies.StrictNetworking (and nil = schema default true) to decide
//     whether to inject mTLS sidecars and restricted network policies.
//   - Read per-node, per-feature configuration from graph.Spec.FeatureValuesMapping only
//     after nil checks; do not assume a node or feature key exists.
func (e *HelmEngine) Render(ctx context.Context, graph *types.IRGraph, release *types.Release, scope types.Scope) (map[string][]byte, error) {
	_ = ctx
	_ = graph
	_ = release
	_ = scope
	return map[string][]byte{}, nil
}
