// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// PluginContractClaim maps a catalog Plugin entity's authored
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
func PluginContractClaim(entity types.Plugin) *pluginpath.PluginContractClaim {
	contract := entity.Spec.Provider.Contract
	if len(entity.Spec.Exports) == 0 && contract.IsZero() {
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
	return &pluginpath.PluginContractClaim{
		Exports:  exports,
		Contract: pluginContractClaim(contract),
	}
}

// pluginContractClaim maps a Plugin entity's authored plugin-level
// capabilities block into the neutral claim shape, or nil when the entity
// authors none. "Authors none" is asked of the contract itself rather than of a
// pointer, so the entity's own definition of an absent contract is the one that
// decides.
func pluginContractClaim(c types.ProviderContract) *pluginpath.ProviderContractClaim {
	if c.IsZero() {
		return nil
	}
	claim := &pluginpath.ProviderContractClaim{
		// Assigned whole, not copied field-by-field: the neutral claim's input-kind
		// type is an alias of the entity's, so this carries every kind the home type
		// declares and a new one needs no edit here. A field-by-field copy would
		// re-introduce a site that silently drops a kind.
		InputKinds:           c.InputKinds,
		BoundaryAssembly:     c.BoundaryAssembly,
		SourceFilter:         c.SourceFilter,
		ReusableInstance:     c.ReusableInstance,
		CrossBoundaryLinking: c.CrossBoundaryLinking,
		NestSegment:          c.NestSegment,
		AttachmentSegment:    c.AttachmentSegment,
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

// PluginCommandsMirror maps a catalog Plugin entity's machine-owned CLI-command
// record into the neutral command mirror the plugin entity-front attaches to a
// resolved plugin, so the load gate verifies it against the plugin's describe()
// commands. It is the single home of the entity → neutral-mirror mapping; the
// plugin sockets that front the cascade with the entity translation call it
// rather than each re-spelling the field copy.
//
// An entity that carries no commands field authors no mirror and yields nil — the
// load gate then skips the command check. An entity that records the field, even
// an explicit empty list, yields a non-nil pointer so the load gate runs the
// full-equality check (an empty recorded mirror matches a plugin that describes
// no commands and diverges from one that describes some).
func PluginCommandsMirror(entity types.Plugin) *[]pluginpath.PluginCommandMirror {
	if entity.Spec.Commands == nil {
		return nil
	}
	mirror := make([]pluginpath.PluginCommandMirror, 0, len(entity.Spec.Commands))
	for _, c := range entity.Spec.Commands {
		mirror = append(mirror, pluginpath.PluginCommandMirror{
			Name:        c.Name,
			Description: c.Description,
			Flags:       pluginCommandFlagsMirror(c.Flags),
		})
	}
	return &mirror
}

// pluginCommandFlagsMirror maps a catalog command's machine-owned flags into the
// neutral flag-mirror shape, preserving declared order. A command with no flags
// yields a nil slice.
func pluginCommandFlagsMirror(flags []types.PluginCommandFlag) []pluginpath.PluginCommandFlagMirror {
	if len(flags) == 0 {
		return nil
	}
	out := make([]pluginpath.PluginCommandFlagMirror, len(flags))
	for i, f := range flags {
		out[i] = pluginpath.PluginCommandFlagMirror{
			Name:        f.Name,
			Shorthand:   f.Shorthand,
			Description: f.Description,
			Type:        f.Type,
			Default:     f.Default,
		}
	}
	return out
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
