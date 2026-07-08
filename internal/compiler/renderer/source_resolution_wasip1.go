// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// resolveSourceForNode forwards the Recipe's authored source verbatim in the
// Wasm guest build. The guest renders a node whose source artifact was already
// acquired and projected into the render input ahead of time; it performs no
// source acquisition itself, so there is no cache to consult here. This stub
// carries the same signature as the host implementation and satisfies the
// package's compile-time call graph; the guest render path never invokes it at
// runtime.
func resolveSourceForNode(input RendererInput, recipe *types.Recipe, nodeCtx *NodeRenderContext) error {
	if recipe == nil || recipe.Spec.Source == nil {
		return nil
	}
	nodeCtx.OriginalSource = recipe.Spec.Source
	nodeCtx.Source = recipe.Spec.Source
	return nil
}

// resolveEntitySource forwards the entity's authored source verbatim in the
// Wasm guest build, mirroring resolveSourceForNode's no-acquisition contract.
// Carries the same signature as the host implementation for the package's
// compile-time call graph; the guest render path never invokes it at runtime.
func resolveEntitySource(input RendererInput, source any, locals types.OrderedMap[string], uri string, values, infra map[string]any, srcPins []types.SourceDigest) (any, error) {
	return source, nil
}
