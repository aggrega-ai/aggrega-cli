// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchart

import (
	"context"
	"fmt"

	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
)

// HelmEngine produces sub-chart contents for one node by copying the user's
// local Helm chart verbatim. Sourced mode is the only supported path: the
// helm-chart provider's role is umbrella + sub-chart composition for
// sourced charts. Workload synthesis is the helm provider's concern
// (renderer/helm/HelmEngine.SynthesizeWorkload) and is not implemented at
// the helm-chart provider level — chapter 16 assigns synthesis exclusively
// to helm.
type HelmEngine struct{}

var _ renderer.Engine = (*HelmEngine)(nil)

// Render produces the sub-chart contents for one node.
//
// Sourced mode (nodeCtx.Source is a non-empty string): copy the user's local
// Helm chart verbatim. The umbrella builder emits a dependency entry using
// the user's chart name with `alias: <instance_id>` so values propagation
// works while allowing multiple instances of the same source chart.
//
// The legacy sourceless path (generative rendering of Chart.yaml +
// templates/*.yaml from a values dict) was removed when chapter 16's
// three-input-kinds model assigned workload synthesis to helm exclusively.
// A nil/empty source is rejected with a clear error.
func (e *HelmEngine) Render(_ context.Context, _ map[string]interface{}, nodeCtx renderer.NodeRenderContext) (map[string][]byte, error) {
	s, ok := nodeCtx.Source.(string)
	if !ok || s == "" {
		return nil, fmt.Errorf("helmchart: node %q: source is required (helm-chart provider supports sourced mode only; for synthesised K8s manifests use the helm provider with workload:)", nodeCtx.NodeID)
	}
	return copySourcedChart(s)
}
