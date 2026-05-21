// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"context"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// HelmEngine produces flat rendered Kubernetes YAML.
// Dispatches to sourced (Helm SDK on a real chart) or sourceless (generative
// Deployment + Service from validated values dict) based on the Recipe's
// `source` field. Source is passed in via NodeRenderContext.SourceHint below,
// populated by the orchestrator.
//
// Implements renderer.WorkloadSynthesizer via SynthesizeWorkload (defined in
// workload_synth.go) — Phase B.4 dispatches to that method when the bound
// Recipe declares workload:.
type HelmEngine struct{}

var (
	_ renderer.Engine              = (*HelmEngine)(nil)
	_ renderer.WorkloadSynthesizer = (*HelmEngine)(nil)
)

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
		return renderSourceless(values, nodeCtx)
	case string:
		if s == "" {
			return nil, cerrors.Wrap(ErrEmptySource, cerrors.CatalogError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("node %q", nodeCtx.NodeID))
		}
		return renderSourced(ctx, s, values, nodeCtx)
	default:
		// Structured source objects (e.g. {repo: ..., chart: ..., version: ...}) are
		// reserved for future registry integration; the helm provider supports
		// only filesystem paths today.
		return nil, cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q: structured source not supported", nodeCtx.NodeID), nil, nil)
	}
}
