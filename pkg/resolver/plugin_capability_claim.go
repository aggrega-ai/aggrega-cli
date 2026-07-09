// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// PluginCapabilityClaim maps a catalog Plugin entity's authored
// capability declaration into the neutral claim the plugin entity-front attaches
// to a resolved plugin, so the load gate can cross-check it against the plugin's
// self-report. It is the single home of the entity → neutral-claim mapping; both
// plugin sockets that front the cascade with the entity translation (the render
// pipeline's per-step load and the publishing-linter's workspace-config path)
// call it rather than each re-spelling the field copy.
//
// Two authored surfaces cross the boundary: the per-export operates/scope (the
// export name keys the cross-check; the selector is free-form documentation and
// is not carried) and the plugin-level capabilities block. The host-function
// surface is machine-derived and stamped, not authored, so it never participates
// in a load-time claim. An entity that authors neither exports nor a
// capabilities block yields nil — no claim, so the cross-check is skipped for
// that plugin.
func PluginCapabilityClaim(entity types.Plugin) *pluginpath.PluginCapabilityClaim {
	if len(entity.Spec.Exports) == 0 && entity.Spec.Capabilities == nil {
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
		Exports:      exports,
		Capabilities: pluginCapabilitiesClaim(entity.Spec.Capabilities),
	}
}

// pluginCapabilitiesClaim maps a Plugin entity's authored plugin-level
// capabilities block into the neutral claim shape, or nil when the entity
// authors none.
func pluginCapabilitiesClaim(c *types.PluginCapabilities) *pluginpath.PluginCapabilitiesClaim {
	if c == nil {
		return nil
	}
	claim := &pluginpath.PluginCapabilitiesClaim{
		InputKinds: pluginpath.PluginInputKindsClaim{
			Sourced:             c.InputKinds.Sourced,
			Synthesized:         c.InputKinds.Synthesized,
			ArtifactsStandalone: c.InputKinds.ArtifactsStandalone,
		},
		BoundaryAssembly:     c.BoundaryAssembly,
		SourceFilter:         c.SourceFilter,
		ReusableInstance:     c.ReusableInstance,
		CrossBoundaryLinking: c.CrossBoundaryLinking,
	}
	if c.ArtifactDelimiters != nil {
		claim.ArtifactDelimiters = &pluginpath.PluginArtifactDelimitersClaim{
			Left:  c.ArtifactDelimiters.Left,
			Right: c.ArtifactDelimiters.Right,
		}
	}
	if c.AssemblyNameAnnotation != nil {
		claim.AssemblyNameAnnotation = &pluginpath.PluginAssemblyNameAnnotationClaim{
			Annotation:    c.AssemblyNameAnnotation.Annotation,
			RequireUnique: c.AssemblyNameAnnotation.RequireUnique,
		}
	}
	return claim
}

// PluginRuntimeLimits maps a catalog Plugin entity's authored runtime
// resource limits into the neutral limits the plugin entity-front attaches to a
// resolved plugin, so the loader caps the plugin's memory and per-call wall time.
// It is the single home of the entity → neutral-limits mapping; both plugin
// sockets that front the cascade with the entity translation call it rather than
// each re-spelling the field copy.
//
// An entity with no runtime block authors no limits and yields nil; the loader
// then applies the platform default cap, so a load is never unbounded. Limits are
// a sibling of the capability claim, not part of it: a plugin does not
// self-report limits, so there is no describe()-vs-entity cross-check to feed.
func PluginRuntimeLimits(entity types.Plugin) *pluginpath.PluginRuntimeLimits {
	if entity.Spec.Runtime == nil {
		return nil
	}
	return &pluginpath.PluginRuntimeLimits{
		MaxMemoryPages: entity.Spec.Runtime.MaxMemoryPages,
		TimeoutMs:      entity.Spec.Runtime.TimeoutMs,
	}
}
