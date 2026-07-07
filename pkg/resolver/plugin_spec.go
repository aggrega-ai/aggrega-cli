// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// PluginSpec builds the loader input for a resolved plugin: the bytes to
// compile under the given diagnostic name, plus the entity's neutral capability
// claim (mapped onto the load gate's cross-check shape) and runtime limits
// (mapped onto the loader's caps). It is the single home of the
// neutral → wasm.PluginSpec mapping; every socket that loads a resolved plugin
// (the render pipeline's per-step load and the publishing-linter dispatch) calls
// it rather than re-spelling the field copy.
//
// A resolution that is not entity-fronted carries a nil Capability and nil
// Limits — the bare cascade, the linter's --plugin override, the publish path.
// A nil claim maps to a nil spec.Capability (the load gate skips the
// cross-check); nil limits map to zero caps (the loader applies the platform
// default cap, so a load is never unbounded). This preserves the non-entity load
// behaviour while the entity-fronted path gains both the cross-check and the
// entity's explicit caps.
//
// It is the sibling of PluginCapabilityClaim / PluginRuntimeLimits (the
// entity → neutral mapping): those translate an authored Plugin entity into the
// neutral shapes the resolution layer attaches to a resolved plugin; this
// translates those neutral shapes into the loader's spec. Keeping both mappings
// in this package leaves pkg/pluginpath free of any internal/wasm dependency and
// the loader free of any resolver dependency.
func PluginSpec(name string, resolved pluginpath.ResolvedPlugin) wasm.PluginSpec {
	spec := wasm.PluginSpec{Name: name, Bytes: resolved.Bytes}
	if resolved.Capability != nil {
		exports := make([]wasm.EntityExportClaim, 0, len(resolved.Capability.Exports))
		for _, e := range resolved.Capability.Exports {
			exports = append(exports, wasm.EntityExportClaim{
				Name:     e.Name,
				Operates: e.Operates,
				Scope:    e.Scope,
			})
		}
		spec.Capability = &wasm.EntityCapabilityClaim{
			Exports:      exports,
			Capabilities: wasmCapabilities(resolved.Capability.Capabilities),
		}
	}
	if resolved.Limits != nil {
		spec.MaxMemoryPages = resolved.Limits.MaxMemoryPages
		spec.TimeoutMs = resolved.Limits.TimeoutMs
	}
	return spec
}

// wasmCapabilities maps the neutral plugin-level capability claim onto the
// load gate's cross-check shape, or nil when the resolution carries none.
func wasmCapabilities(c *pluginpath.PluginCapabilitiesClaim) *wasm.Capabilities {
	if c == nil {
		return nil
	}
	caps := &wasm.Capabilities{
		InputKinds: wasm.InputKinds{
			Sourced:             c.InputKinds.Sourced,
			Synthesized:         c.InputKinds.Synthesized,
			ArtifactsStandalone: c.InputKinds.ArtifactsStandalone,
		},
		BoundaryAssembly: c.BoundaryAssembly,
		SourceFilter:     c.SourceFilter,
		ReusableInstance: c.ReusableInstance,
	}
	if c.ArtifactDelimiters != nil {
		caps.ArtifactDelimiters = &wasm.ArtifactDelimiters{
			Left:  c.ArtifactDelimiters.Left,
			Right: c.ArtifactDelimiters.Right,
		}
	}
	if c.AssemblyNameAnnotation != nil {
		caps.AssemblyNameAnnotation = &wasm.AssemblyNameAnnotation{
			Annotation:    c.AssemblyNameAnnotation.Annotation,
			RequireUnique: c.AssemblyNameAnnotation.RequireUnique,
		}
	}
	return caps
}
