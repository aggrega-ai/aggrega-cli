// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// entityCommandMirror maps a Plugin entity's machine-owned command record into
// the neutral command shape the mirror comparison reads. An entity that records
// no commands field yields an empty slice, which the comparison treats as equal
// to a plugin that describes no commands.
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

// canonicalHostFunctionsEqual reports whether two host-function sets are equal
// as sets (order and duplicates ignored), treating an absent record (nil) and an
// explicit empty set as equal. It delegates to the single set-equality home in
// the wasm package so the record-sync tooling and the publication validation door
// compare a host-function record by exactly the same rule.
func canonicalHostFunctionsEqual(a, b []string) bool {
	return wasm.HostFunctionsEqual(a, b)
}
