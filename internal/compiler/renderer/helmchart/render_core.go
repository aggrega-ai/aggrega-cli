// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchart

import "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"

// RenderSourcedChartCore rebuilds the sub-chart's path→bytes map from a source
// projection: each projected file becomes a map entry keyed by its chart-root-
// relative, slash-separated path with its content verbatim. It is the
// filesystem-free render core — it consumes the in-memory projection and never
// reads from disk, so both the in-tree host engine (which projects the chart from
// a path) and a provider Wasm guest (which reconstructs the projection from the
// payload) share this one byte-identical-critical path. It is exported so a
// provider Wasm guest, which lives in a separate package, can drive the same core
// the in-tree engine runs. The orchestrator prefixes the returned keys with
// `boundaries/<boundary>/charts/<instance_id>/` when placing the sub-chart under
// the umbrella.
func RenderSourcedChartCore(proj *renderer.SourceProjection) map[string][]byte {
	out := make(map[string][]byte, len(proj.Files))
	for _, f := range proj.Files {
		out[f.Path] = f.Content
	}
	return out
}
