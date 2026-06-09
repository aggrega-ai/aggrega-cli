// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchart

import (
	"context"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// HelmEngine produces sub-chart contents for one node by copying the user's
// local Helm chart verbatim. Sourced mode is the only supported path: the
// helm-chart provider's role is umbrella + sub-chart composition for
// sourced charts. Workload synthesis is the helm provider's concern
// (renderer/helm/HelmEngine.SynthesizeWorkload) and is not implemented at
// the helm-chart provider level — chapter 16 assigns synthesis exclusively
// to helm.
type HelmEngine struct{}

var (
	_ renderer.Engine                   = (*HelmEngine)(nil)
	_ renderer.BoundaryAssembler        = (*HelmEngine)(nil)
	_ renderer.ExternalArtifactsHandler = (*HelmEngine)(nil)
	_ renderer.ArtifactsPathProvider    = (*HelmEngine)(nil)
	_ renderer.ArtifactsTemplateDelims  = (*HelmEngine)(nil)
)

// Capabilities reports helm-chart's input-kind support: sourced only. It
// produces an umbrella chart from a chart source; it does not synthesise from a
// Workload (no WorkloadSynthesizer) and does not render artifacts-only (as an
// ExternalArtifactsHandler it injects artifacts into the umbrella chart, which
// an artifacts-only node lacks). The describe() analog for this engine.
func (e *HelmEngine) Capabilities() renderer.ProviderCapabilities {
	return renderer.ProviderCapabilities{Sourced: true}
}

// HandlesArtifactsExternally satisfies renderer.ExternalArtifactsHandler.
// The helm-chart engine merges $import:-loaded artifact entries from the
// Recipe into the chart's templates/ directory rather than emitting
// standalone files; Phase B.3 dispatches to the external-handler branch
// when this marker is satisfied. Inline-content artifact entries are
// rejected at the same point — Helm's chart structure requires templates
// as physical files.
func (e *HelmEngine) HandlesArtifactsExternally() {}

// PathForArtifact satisfies renderer.ArtifactsPathProvider. Each
// $import:-loaded artifact file lands at templates/<basename> within the
// per-node output; the orchestrator's per-boundary nesting layers
// boundaries/<bp>/charts/<nodeID>/ on top, so the emitted file is
// positioned alongside the sourced chart's templates and participates in
// Helm's downstream render pass.
func (e *HelmEngine) PathForArtifact(_ renderer.NodeRenderContext, basename string) string {
	return "templates/" + basename
}

// ArtifactDelimiters satisfies renderer.ArtifactsTemplateDelims. helm-chart
// $import:-loaded artifact files use "[[", "]]" for Aggrega's pre-templating
// pass so Helm's "{{ ... }}" template syntax inside the file survives
// untouched into Helm's downstream render. Authors writing $import:-loaded
// artifact files for helm-chart use [[ .pillar ]] for Aggrega context and
// {{ .Values.x }} for Helm context.
func (e *HelmEngine) ArtifactDelimiters() (string, string) {
	return "[[", "]]"
}

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
func (e *HelmEngine) Render(_ context.Context, _ map[string]any, nodeCtx renderer.NodeRenderContext) (map[string][]byte, error) {
	s, ok := nodeCtx.Source.(string)
	if !ok || s == "" {
		// Defensive: the central input-kind gate (orchestrator) dispatches
		// engine.Render only for sourced mode and declines helm-chart's
		// synthesised + artifacts-only modes at the gate (Capabilities: Sourced
		// only), so a nil/empty source never reaches here in a valid build.
		return nil, cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID},
			fmt.Sprintf("helmchart: node %q: internal error — engine.Render dispatched with no source; the orchestrator gate restricts helm-chart to sourced mode",
				nodeCtx.NodeID),
			nil, nil)
	}
	return copySourcedChart(s)
}
