// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package terraformcore

import "github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"

// dependsOnMetaArgument is the Terraform meta-argument that fixes the order the
// engine performs actions in. It is accepted on a `module` block and takes a
// list of references, which is why a declared deployment wave lands here and
// nowhere else: it is the only ordering primitive the root configuration can
// express between the components it composes.
const dependsOnMetaArgument = "depends_on"

// waveDependencies returns, per contributing component, the module names that
// component's `module` block declares in its depends_on — the ordering a
// declared deployment wave expresses, in the terms the root configuration can
// state it.
//
// What the ordering IS. Terraform builds a dependency graph and works through it
// in parallel to the extent the graph allows; depends_on adds an edge to that
// graph, so the engine completes its actions on the named modules before it
// begins the ones on the module declaring it. So a wave orders the actions the
// engine takes: the earlier wave's components are acted on before the later
// wave's are begun. It is not readiness ordering — a configuration expresses no
// notion of a resource being serviceable, so nothing here waits for one to
// become so.
//
// Reach. A boundary's components are child modules of one root configuration, and
// depends_on names a module inside the configuration that declares it, so this
// orders every component in that root and nothing outside it. Components composed
// into a different root are unreachable from here and are not approximated.
//
// Fan-out. Components are grouped by their contributing node's wave, and
// [rendercore.WavePredecessors] chains each to the immediately-preceding populated
// wave, never every earlier wave: Terraform resolves the rest transitively through
// the middle wave, so a three-wave boundary chains rather than fanning out to the
// whole boundary. Waves need be neither contiguous nor non-negative, so -5, 0 and 7
// chain exactly as 0, 1 and 2 do. The lowest wave names nothing.
//
// Order. Within a wave the names are emitted in contributor order, which is the
// order the module blocks themselves appear in, so a list reads in the same order
// as the file above it. That order is the caller's and is fixed deterministically,
// which is what makes the emitted list a function of the input alone.
//
// A boundary with fewer than two populated waves yields nothing at all, so a
// boundary where no wave was declared — every component carrying the field's
// default — emits the root configuration it would emit if the wave expressed no
// ordering.
func waveDependencies(components []RootComponent, moduleNames []string) [][]string {
	waves := make([]int, len(components))
	for i, c := range components {
		waves[i] = c.SyncWave
	}
	predecessors := rendercore.WavePredecessors(waves)
	if predecessors == nil {
		return nil
	}

	deps := make([][]string, len(components))
	for i, before := range predecessors {
		if len(before) == 0 {
			continue
		}
		names := make([]string, 0, len(before))
		for _, j := range before {
			names = append(names, moduleNames[j])
		}
		deps[i] = names
	}
	return deps
}
