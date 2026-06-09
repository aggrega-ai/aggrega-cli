// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compose

import (
	"context"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
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
//   - renderer.BoundaryAssembler — per-(evaluated_path, provider) merge
//     of every contributor's fragment into a single docker-compose.yaml
//     at the dispatch path, with per-boundary networks, depends_on
//     inference from IR uses edges, and cross-boundary service-name
//     collision detection.
type ComposeEngine struct{}

var (
	_ renderer.Engine              = (*ComposeEngine)(nil)
	_ renderer.WorkloadSynthesizer = (*ComposeEngine)(nil)
	_ renderer.BoundaryAssembler   = (*ComposeEngine)(nil)
)

// Capabilities reports compose's input-kind support: sourced (a compose file)
// and synthesised (Workload → service fragment via SynthesizeWorkload). compose
// does NOT render artifacts-only: as a BoundaryAssembler it merges per-node
// output into docker-compose.yaml services, and an artifacts-only node has none
// to contribute. The describe() analog for this engine.
func (e *ComposeEngine) Capabilities() renderer.ProviderCapabilities {
	return renderer.ProviderCapabilities{Sourced: true, Synthesized: true}
}

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
	values map[string]any,
	nodeCtx renderer.NodeRenderContext,
) (map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Classify source (forwarded by the orchestrator from recipe.spec.source).
	switch s := nodeCtx.Source.(type) {
	case nil:
		// Defensive: the central input-kind gate (orchestrator) dispatches
		// engine.Render only for sourced mode (hasSource). compose's synthesised
		// mode renders in Phase B.4 via SynthesizeWorkload, and compose declines
		// artifacts-only at the gate (Capabilities: no ArtifactsStandalone), so a
		// nil source never reaches here in a valid build. Mirrors the default:
		// arm.
		return nil, cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID},
			fmt.Sprintf("compose: node %q: internal error — engine.Render dispatched with no source; the orchestrator gate routes synthesised mode to Phase B.4", nodeCtx.NodeID), nil, nil)
	case string:
		if s == "" {
			return nil, cerrors.Wrap(ErrEmptySource, cerrors.CatalogError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("node %q", nodeCtx.NodeID))
		}
		return renderSourced(s, values, nodeCtx)
	default:
		// Defensive arm: a non-string Source here means the orchestrator
		// did not resolve spec.source via resolveSourceForNode before
		// dispatch. Engines do not consume structured sources directly.
		return nil, cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID},
			fmt.Sprintf("compose: node %q: internal error — orchestrator should have resolved spec.source to a filesystem path; received %T (source: %s)",
				nodeCtx.NodeID, nodeCtx.Source, renderer.SourceForDiagnostic(nodeCtx)),
			nil, nil)
	}
}
