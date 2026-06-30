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
// dropped), and the authored runtime.declaredHostFunctions threads through for
// the load gate's authored-vs-derived cross-check.
func TestPluginCapabilityClaim(t *testing.T) {
	t.Parallel()

	t.Run("maps exports and host functions", func(t *testing.T) {
		t.Parallel()
		entity := types.Plugin{}
		entity.Spec.Exports = []types.PluginExport{
			{Name: "step", Operates: "patch", Scope: "own", Selector: "dropped"},
			{Name: "render_node", Operates: "emit", Scope: "host"},
		}
		entity.Spec.Runtime = &types.PluginRuntime{
			DeclaredHostFunctions: []string{"aggrega_log"},
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
		if !reflect.DeepEqual(got.DeclaredHostFunctions, []string{"aggrega_log"}) {
			t.Errorf("declared host functions = %v; want [aggrega_log]", got.DeclaredHostFunctions)
		}
	})

	t.Run("host functions only (no exports) still yields a claim", func(t *testing.T) {
		t.Parallel()
		entity := types.Plugin{}
		entity.Spec.Runtime = &types.PluginRuntime{
			DeclaredHostFunctions: []string{"aggrega_log"},
		}
		got := PluginCapabilityClaim(entity)
		if got == nil {
			t.Fatal("an entity authoring only host functions should yield a claim")
		}
		if len(got.Exports) != 0 {
			t.Errorf("expected no exports; got %+v", got.Exports)
		}
	})

	t.Run("no authored capability yields nil", func(t *testing.T) {
		t.Parallel()
		if got := PluginCapabilityClaim(types.Plugin{}); got != nil {
			t.Errorf("an entity with no authored exports or host functions should yield nil; got %+v", got)
		}
	})
}
