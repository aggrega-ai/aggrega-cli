// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// WasmPluginCapabilityClaim maps a catalog WasmPlugin entity's authored
// capability declaration into the neutral claim the plugin entity-front attaches
// to a resolved plugin, so the load gate can cross-check it against the plugin's
// self-report. It is the single home of the entity → neutral-claim mapping; both
// plugin sockets that front the cascade with the entity translation (the render
// pipeline's per-step load and the publishing-linter's workspace-config path)
// call it rather than each re-spelling the field copy.
//
// Two authored surfaces cross the boundary: the per-export operates/scope (the
// export name keys the cross-check; the selector is free-form documentation and
// is not carried), and the optional runtime.declaredHostFunctions list the gate
// cross-checks against the host functions derived from the .wasm imports. An
// entity that authors neither yields nil — no claim, so both cross-checks are
// skipped for that plugin.
func WasmPluginCapabilityClaim(entity types.WasmPlugin) *pluginpath.PluginCapabilityClaim {
	var declaredHostFunctions []string
	if entity.Spec.Runtime != nil {
		declaredHostFunctions = entity.Spec.Runtime.DeclaredHostFunctions
	}
	if len(entity.Spec.Exports) == 0 && len(declaredHostFunctions) == 0 {
		return nil
	}
	exports := make([]pluginpath.PluginExportClaim, 0, len(entity.Spec.Exports))
	for _, e := range entity.Spec.Exports {
		exports = append(exports, pluginpath.PluginExportClaim{
			Name:     e.Name,
			Operates: e.Operates,
			Scope:    e.Scope,
		})
	}
	return &pluginpath.PluginCapabilityClaim{
		Exports:               exports,
		DeclaredHostFunctions: declaredHostFunctions,
	}
}
