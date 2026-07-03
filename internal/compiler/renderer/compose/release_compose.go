// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1

// Host adapter for the compose provider's per-(path, provider) merge.
// Implements renderer.BoundaryAssembler (dispatched once per contributor group).
// The merge itself lives in the tag-free assembleBoundaryCore (assemble_core.go),
// which both this host adapter and the provider Wasm guest call so the two
// assembly surfaces stay byte-for-byte identical. This adapter's only job is to
// project the host BoundaryInfo onto the core's consumed-inputs signature.

package compose

import (
	"context"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// AssembleBoundary implements renderer.BoundaryAssembler. Reads per-node
// fragments from info.IntermediatesByNode and emits a single
// docker-compose.yaml at the dispatch path. nestPerNode is always false
// for compose — the assembled file is flat at the dispatch root.
func (e *ComposeEngine) AssembleBoundary(_ context.Context, info renderer.BoundaryInfo) (map[string][]byte, bool, error) {
	files, err := assembleBoundaryCore(info.IntermediatesByNode, info.NodeAnnotations, info.Edges, info.BoundaryPath, info.YAMLIndent)
	if err != nil {
		return nil, false, err
	}
	return files, false, nil
}
