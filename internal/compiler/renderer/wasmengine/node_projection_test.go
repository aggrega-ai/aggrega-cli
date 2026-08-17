// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasmengine

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestNodeProjection_CarriesAttachmentsAndWithholdsTheRest pins both halves of
// the per-node render surface's attachment contract in one place: the node's
// attachments reach the guest in author order with their derived sensitivity
// marking intact, and the three fields deliberately kept off that surface stay
// off it. The second half matters as much as the first — each of those three has
// its own reason for being withheld, so a projection that started carrying one of
// them would widen the surface silently, with no rendered byte moving to show it.
func TestNodeProjection_CarriesAttachmentsAndWithholdsTheRest(t *testing.T) {
	t.Parallel()

	attachments := types.OrderedMap[types.IRFeatureEntry]{}
	attachments.Set("zeta-cache", types.IRFeatureEntry{Type: "core/cache"})
	attachments.Set("alpha-db", types.IRFeatureEntry{Type: "core/referenced-secret", Sensitive: true})

	nodeCtx := renderer.NodeRenderContext{
		NodeID:      "app",
		Attachments: attachments,
	}

	proj := (&Engine{}).nodeProjection(nodeCtx, types.OrderedMap[any]{}, nil)

	want := []string{"zeta-cache", "alpha-db"}
	got := proj.Features.Keys()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("projected attachment aliases = %v, want %v (author order, not sorted)", got, want)
	}
	entry, ok := proj.Features.Get("alpha-db")
	if !ok {
		t.Fatal("projection carries no alpha-db attachment")
	}
	if !entry.Sensitive {
		t.Error("alpha-db reached the projection without its sensitivity marking")
	}

	if proj.Type != "" {
		t.Errorf("projection carries type %q; the component type stays off the render surface", proj.Type)
	}
	if proj.ResolvedRecipe != "" {
		t.Errorf("projection carries resolved recipe %q; the resolved URI stays off the render surface", proj.ResolvedRecipe)
	}
	if proj.Uses.Len() != 0 {
		t.Errorf("projection carries %d uses edge set(s); outbound edges stay off the render surface", proj.Uses.Len())
	}
}
