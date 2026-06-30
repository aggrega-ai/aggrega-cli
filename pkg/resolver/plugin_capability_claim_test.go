// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestPluginCapabilityClaim proves the single-source entity → neutral-claim
// mapping: authored exports map to the per-export operates/scope claim (selector
// dropped). The host-function surface is machine-derived and stamped, not
// authored, so it never enters the load-time claim.
func TestPluginCapabilityClaim(t *testing.T) {
	t.Parallel()

	t.Run("maps exports", func(t *testing.T) {
		t.Parallel()
		entity := types.Plugin{}
		entity.Spec.Exports = []types.PluginExport{
			{Name: "step", Operates: "patch", Scope: "own", Selector: "dropped"},
			{Name: "render_node", Operates: "emit", Scope: "host"},
		}

		got := PluginCapabilityClaim(entity)
		if got == nil {
			t.Fatal("expected a non-nil claim")
		}
		wantExports := []pluginpath.PluginExportClaim{
			{Name: "step", Operates: "patch", Scope: "own"},
			{Name: "render_node", Operates: "emit", Scope: "host"},
		}
		if !reflect.DeepEqual(got.Exports, wantExports) {
			t.Errorf("exports = %+v; want %+v (selector excluded)", got.Exports, wantExports)
		}
	})

	t.Run("a stamped host-function record does not, on its own, yield a claim", func(t *testing.T) {
		t.Parallel()
		entity := types.Plugin{}
		entity.Spec.Runtime = &types.PluginRuntime{
			HostFunctions: []string{"aggrega_log"},
		}
		if got := PluginCapabilityClaim(entity); got != nil {
			t.Errorf("the machine-derived host-function record is not an authored claim; an entity with no exports should yield nil, got %+v", got)
		}
	})

	t.Run("no authored exports yields nil", func(t *testing.T) {
		t.Parallel()
		if got := PluginCapabilityClaim(types.Plugin{}); got != nil {
			t.Errorf("an entity with no authored exports should yield nil; got %+v", got)
		}
	})
}
