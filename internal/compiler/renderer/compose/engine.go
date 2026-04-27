// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compose

import (
	"context"
	"fmt"

	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
)

// ComposeEngine is the compose provider rendering engine. It implements:
//
//   - renderer.Engine — Render dispatches by source classification:
//     synthesised-mode (no source: → handled at Phase B.4 via
//     SynthesizeWorkload), sourced-mode (string source → loads a base
//     compose file and overlays valuesMapping output deep-merged), error
//     for the bare-values shape (no source, no workload — unsupported).
//   - renderer.WorkloadSynthesizer — per-node Workload → compose-fragment
//     translation; emitted as Intermediate=true so the orchestrator never
//     ships fragments to disk.
//   - renderer.ReleaseComposer — Release-level merge of every node's
//     fragment into a single docker-compose.yaml at the Release output
//     root, with per-boundary networks, depends_on inference from IR uses
//     edges, and cross-boundary collision detection.
type ComposeEngine struct{}

var (
	_ renderer.Engine              = (*ComposeEngine)(nil)
	_ renderer.WorkloadSynthesizer = (*ComposeEngine)(nil)
	_ renderer.ReleaseComposer     = (*ComposeEngine)(nil)
)

// Render produces a map of relative file paths → YAML bytes for the compose
// provider. File keys are flat (no subdirectory prefix); the orchestrator
// prefixes them with `boundaries/<boundary>/`. The Release-level merger
// (Track 2) replaces per-node fragments with a single docker-compose.yaml at
// the Release output root.
//
// Track 1 dispatch: Workload-mode arrives via Phase B.4 dispatching to
// SynthesizeWorkload; Render itself is reached only when no Workload is
// declared. For the current compose surface every supported Recipe
// declares Workload, so this entry point is a defensive stub that errors
// on the empty case (matching helm.HelmEngine.Render's
// source-classification structure for symmetry).
func (e *ComposeEngine) Render(
	ctx context.Context,
	values map[string]interface{},
	nodeCtx renderer.NodeRenderContext,
) (map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Classify source (forwarded by the orchestrator from recipe.spec.source).
	switch s := nodeCtx.Source.(type) {
	case nil:
		// Sourceless without a Workload is an unsupported authoring shape for
		// the compose provider — Recipes targeting compose either declare
		// `workload:` (synthesised-mode, dispatched via SynthesizeWorkload) or
		// `source:` pointing at a compose file (sourced-mode, lands in
		// Track 2).
		return nil, fmt.Errorf("compose: node %q: Recipe must declare workload: or source: for the compose provider; bare values-only mode is not supported", nodeCtx.NodeID)
	case string:
		if s == "" {
			return nil, fmt.Errorf("%w: node %q", ErrEmptySource, nodeCtx.NodeID)
		}
		return renderSourced(s, values, nodeCtx)
	default:
		return nil, fmt.Errorf("compose: node %q: structured source not supported", nodeCtx.NodeID)
	}
}
