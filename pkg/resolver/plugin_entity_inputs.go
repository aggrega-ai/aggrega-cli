// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// PluginEntityInputs maps a parsed Plugin entity into the neutral cross-check
// inputs the publish-side validation door reads. It is the single home of the
// entity → wasm.EntityInputs assembly; every surface that cross-checks a .wasm
// against its authored entity — the publish path, the standalone validate, the
// in-process test harness — builds these inputs here rather than re-spelling the
// struct, so the set of authored fields carried into the describe() comparison
// cannot drift between them.
//
// The capability claim goes through the resolver's single
// entity → neutral → wasm claim mapping — the exact mapping the load gate
// cross-checks against — so the authored fields (input kinds, boundary assembly,
// delimiters, source filter, reusable-instance, cross-boundary-linking) carried
// into the describe() comparison match the load surface. An entity authoring
// neither exports nor a capabilities block yields a nil claim, mapped here to a
// zero claim that cross-checks as a no-op. The host-function record is the
// entity's runtime block (nil when it declares none). The command mirror and
// curated surface are mechanical field projections preserving declared order.
// The version is the entity's metadata.version, carried in for exact-string
// cross-check against the binary's describe() build-provenance version.
//
// The describe-load memory cap is deliberately not assembled here: it is caller
// policy — the CLI's platform-default headroom versus an entity's declared
// runtime cap — and each caller supplies it on wasm.ValidateOptions directly.
func PluginEntityInputs(entity types.Plugin) *wasm.EntityInputs {
	var claim wasm.EntityContractClaim
	if c := WasmContractClaim(PluginContractClaim(entity)); c != nil {
		claim = *c
	}
	var hostFunctions []string
	if entity.Spec.Runtime != nil {
		hostFunctions = entity.Spec.Runtime.HostFunctions
	}
	return &wasm.EntityInputs{
		Claim:          claim,
		CommandMirror:  entityCommandMirror(entity),
		HostFunctions:  hostFunctions,
		CuratedSurface: ProjectCuratedSurface(entity),
		Version:        entity.Metadata.Version,
	}
}

// entityCommandMirror projects a Plugin entity's machine-owned command record
// into the neutral command shape the mirror comparison reads, preserving
// declared order. An entity that records no commands yields an empty slice,
// which the comparison treats as equal to a binary describing no commands.
func entityCommandMirror(entity types.Plugin) []wasm.PluginCLICommand {
	mirror := make([]wasm.PluginCLICommand, len(entity.Spec.Commands))
	for i, c := range entity.Spec.Commands {
		mirror[i] = wasm.PluginCLICommand{
			Name:        c.Name,
			Description: c.Description,
			Flags:       entityCommandFlags(c.Flags),
		}
	}
	return mirror
}

// entityCommandFlags projects an entity command's authored flag mirror into the
// neutral flag shape the command-mirror comparison reads, preserving declared
// order. A command that records no flags yields a nil slice, which the
// comparison treats as equal to a described command that declares no flags.
func entityCommandFlags(flags []types.PluginCommandFlag) []wasm.PluginCommandFlag {
	if len(flags) == 0 {
		return nil
	}
	out := make([]wasm.PluginCommandFlag, len(flags))
	for i, f := range flags {
		out[i] = wasm.PluginCommandFlag{
			Name:        f.Name,
			Shorthand:   f.Shorthand,
			Description: f.Description,
			Type:        f.Type,
			Default:     f.Default,
		}
	}
	return out
}

// ProjectCuratedSurface projects a Plugin entity's authored, curated surface — its
// export names, plugin-level capabilities, and whether it authors a
// render-behavior contract — into the shape the under-declaration advisory reads.
func ProjectCuratedSurface(entity types.Plugin) wasm.EntityCuratedSurface {
	exportNames := make([]string, len(entity.Spec.Exports))
	for i, e := range entity.Spec.Exports {
		exportNames[i] = e.Name
	}
	hasContract := entity.Spec.Provider != nil && entity.Spec.Provider.Contract != nil
	return wasm.EntityCuratedSurface{
		ExportNames:  exportNames,
		Capabilities: entity.DeclaredCapabilities(),
		HasContract:  hasContract,
	}
}
