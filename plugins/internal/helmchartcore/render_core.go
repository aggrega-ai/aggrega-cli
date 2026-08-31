// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchartcore

import "github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"

// RenderSourcedChartCore rebuilds the sub-chart's path→bytes map from a source
// projection: each projected file becomes a map entry keyed by its chart-root-
// relative, slash-separated path with its content verbatim. It is the
// filesystem-free render core — it consumes the in-memory projection and never
// reads from disk, which is what lets a provider Wasm guest, which reconstructs
// the projection from the payload, drive this one byte-identical-critical path.
// It is exported so that guest, which lives in a separate package, can drive this
// core rather than carrying its own copy of it. The orchestrator prefixes the
// returned keys with `boundaries/<boundary>/charts/<instance_id>/` when placing
// the sub-chart under the umbrella.
func RenderSourcedChartCore(proj *rendercore.SourceProjection) map[string][]byte {
	out := make(map[string][]byte, len(proj.Files))
	for _, f := range proj.Files {
		out[f.Path] = f.Content
	}
	return out
}
