// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package provider

import (
	"context"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// Engine renders one deployment scope of an IR graph into on-disk-style artifacts. Keys are
// relative paths (for example "compose.yaml", "templates/deployment.yaml"); values are file
// contents.
type Engine interface {
	Render(ctx context.Context, graph *types.IRGraph, release *types.Release, boundary string) (map[string][]byte, error)
}
