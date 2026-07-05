// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1

package helm

import (
	"context"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// HelmEngine produces flat rendered Kubernetes YAML. Render handles the sourced
// input kind — the Helm SDK rendering a real chart at the resolved source path.
// Source is passed in via NodeRenderContext.Source, populated by the
// orchestrator's central input-kind gate, which dispatches Render only for
// sourced recipes.
//
// Synthesised recipes (workload:) render through renderer.WorkloadSynthesizer
// via SynthesizeWorkload (defined in workload_synth.go) — Phase B.4 dispatches
// to that method when the bound Recipe declares workload:. Artifacts-only
// recipes emit in Phase B.3. A nil source reaching Render is therefore an
// internal-error guard, not a render mode.
type HelmEngine struct{}

var (
	_ renderer.Engine              = (*HelmEngine)(nil)
	_ renderer.WorkloadSynthesizer = (*HelmEngine)(nil)
	_ renderer.SourceProjector     = (*HelmEngine)(nil)
	_ renderer.HostCapable         = (*HelmEngine)(nil)
)

// hostCapabilities reports helm's input-kind support: sourced (chart source),
// synthesised (Workload via SynthesizeWorkload), and artifacts-only (flat
// per-node manifest emission). The describe() analog for this engine, and the
// single home the engine method and the standalone HostCaps holder both read.
func hostCapabilities() renderer.ProviderCapabilities {
	return renderer.ProviderCapabilities{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

// HostCaps returns helm's host-side capability holder, built from the provider
// package's standalone functions rather than methods on HelmEngine — so the
// host can resolve helm's host capabilities independently of the engine's
// identity.
func HostCaps() renderer.HostCapabilities {
	return renderer.HostCapabilities{
		Caps:          hostCapabilities(),
		ProjectSource: ProjectChartSource,
	}
}

// Capabilities reports helm's input-kind support. See hostCapabilities.
func (e *HelmEngine) Capabilities() renderer.ProviderCapabilities {
	return hostCapabilities()
}

// HostCapabilities satisfies renderer.HostCapable, exposing helm's host-side
// capability holder.
func (e *HelmEngine) HostCapabilities() renderer.HostCapabilities {
	return HostCaps()
}

// Render produces a map of relative file paths → YAML bytes.
// File keys are flat (no subdirectory prefix) — e.g., "<nodeID>-deployment.yaml".
// The orchestrator prefixes them with `boundaries/<boundary>/`.
func (e *HelmEngine) Render(
	ctx context.Context,
	values map[string]any,
	nodeCtx renderer.NodeRenderContext,
) (map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Classify source (forwarded by the orchestrator from recipe.spec.source)
	switch s := nodeCtx.Source.(type) {
	case nil:
		// Defensive: the central input-kind gate (orchestrator) dispatches
		// engine.Render only for sourced mode (hasSource), so a nil source never
		// reaches here in a valid build — synthesised renders in Phase B.4,
		// artifacts-only emits in Phase B.3. Mirrors the default: arm. (The
		// legacy renderSourceless synth-from-bare-values path was removed: it
		// invented compute the developer never declared — Invariant 4.)
		return nil, cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID},
			fmt.Sprintf("helm: node %q: internal error — engine.Render dispatched with no source; the orchestrator gate routes synthesised mode to Phase B.4 and artifacts-only to Phase B.3", nodeCtx.NodeID),
			nil, nil)
	case string:
		if s == "" {
			return nil, cerrors.Wrap(ErrEmptySource, cerrors.CatalogError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("node %q", nodeCtx.NodeID))
		}
		return renderSourced(ctx, s, values, nodeCtx)
	default:
		// Structured source objects arriving at this engine indicate the
		// orchestrator did not run the source-artifact resolver before
		// dispatch (or its lockfile lookup returned a non-string value).
		// The orchestrator's resolveSourceForNode is the authoritative
		// resolver; reaching this arm is a defensive guard against
		// orchestrator regressions. Error references the OriginalSource
		// (operator-authored) rather than the unresolved cache shape.
		return nil, cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID},
			fmt.Sprintf("helm: node %q: internal error — orchestrator should have resolved spec.source to a filesystem path; received %T (source: %s)",
				nodeCtx.NodeID, nodeCtx.Source, renderer.SourceForDiagnostic(nodeCtx)),
			nil, nil)
	}
}
