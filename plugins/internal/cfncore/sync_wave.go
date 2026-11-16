// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfncore

import (
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
)

// dependsOnAttribute is the CloudFormation resource attribute that fixes the
// order resources are operated on in. It takes a single logical id or a list of
// them, and it is the only ordering primitive a template expresses, which is why
// a declared deployment wave lands here and nowhere else.
const dependsOnAttribute = "DependsOn"

// applyWaveOrdering records the ordering a declared deployment wave expresses, as
// DependsOn between the resources of the assembled stack.
//
// What the ordering IS. CloudFormation operates on resources in parallel to the
// extent it can, and DependsOn overrides that parallelism to operate on resources
// in a stated order. So a wave orders *provisioning*: the earlier wave's resources
// are created before the later wave's are begun, and — because the attribute
// documents the reverse direction as well — deleted after them, which gives correct
// teardown ordering without further work. It is creation and deletion order, not
// readiness: a template expresses no notion of a resource being healthy, so nothing
// here waits for one to become serviceable.
//
// Reach. A dispatch group assembles into one stack template, and DependsOn is
// resource-to-resource within one template, so this orders every resource in the
// assembled stack and nothing outside it. Contributors that assemble into a
// different stack are unreachable from here and are not approximated.
//
// Fan-out. Resources are grouped by their contributing node's wave, and
// [rendercore.WavePredecessors] chains each to the immediately-preceding populated
// wave, never every earlier wave: CloudFormation resolves the rest transitively
// through the middle wave, so a three-wave group chains rather than fanning out to
// the whole group. Waves need not be contiguous or non-negative, so -1, 0 and 5
// chain exactly as 0, 1 and 2 do. The lowest wave names nothing.
//
// Order. Targets are sorted by logical id before they are recorded: a set of
// same-wave resources carries no author order, so alphabetical is what makes the
// emitted list a function of the input alone. A DependsOn the fragment already
// authored is preserved and extended rather than replaced — its own entries keep
// their author order and the wave's targets follow, so both requirements hold and
// neither hides the other.
//
// A group whose resources all carry one wave records nothing at all, so a dispatch
// group where no boundary declared a wave assembles byte-identical bytes.
func applyWaveOrdering(resources map[string]any, waveByResource map[string]int) {
	// Units are the carried resources by logical id: same-wave resources carry no
	// author order, so the sorted id is what fixes each wave's list.
	ids := make([]string, 0, len(waveByResource))
	for id := range waveByResource {
		// A resource the merge did not carry is not addressable in the assembled
		// stack, so naming it would emit a dependency on nothing.
		if _, carried := resources[id]; carried {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	waves := make([]int, len(ids))
	for i, id := range ids {
		waves[i] = waveByResource[id]
	}

	predecessors := rendercore.WavePredecessors(waves)
	if predecessors == nil {
		return
	}
	for i, id := range ids {
		if len(predecessors[i]) == 0 {
			continue
		}
		body, ok := resources[id].(map[string]any)
		if !ok {
			// A resource body that is not a mapping carries no attribute to
			// set. The fragment is emitted as it was authored rather than
			// rewritten into a shape it did not have.
			continue
		}
		targets := make([]string, 0, len(predecessors[i]))
		for _, j := range predecessors[i] {
			targets = append(targets, ids[j])
		}
		resources[id] = withDependsOn(body, targets)
	}
}

// withDependsOn returns a copy of one resource body carrying targets in its
// DependsOn, merged with whatever the fragment already authored there.
//
// It copies rather than writing through because two logical ids can reach the same
// underlying map when a fragment reuses an anchor: mutating in place would then set
// one resource's dependencies on another. The copy is shallow, which is enough —
// only the top-level attribute is written.
//
// An authored DependsOn may be a single logical id or a list of them, and both
// spellings are read. A target already named is not named twice, and the merged
// value is emitted as a list because that is the one spelling that holds however
// many entries the merge produces.
func withDependsOn(body map[string]any, targets []string) map[string]any {
	merged := make(map[string]any, len(body)+1)
	for k, v := range body {
		merged[k] = v
	}

	existing := authoredDependsOn(body[dependsOnAttribute])
	named := make(map[string]struct{}, len(existing)+len(targets))
	combined := make([]any, 0, len(existing)+len(targets))
	for _, e := range existing {
		if _, seen := named[e]; seen {
			continue
		}
		named[e] = struct{}{}
		combined = append(combined, e)
	}
	for _, t := range targets {
		if _, seen := named[t]; seen {
			continue
		}
		named[t] = struct{}{}
		combined = append(combined, t)
	}

	merged[dependsOnAttribute] = combined
	return merged
}

// authoredDependsOn reads whatever a fragment wrote as a DependsOn into the logical
// ids it names, in the order it named them.
//
// A value that is neither a string nor a list of strings is read as naming nothing:
// this core reads the attribute to extend it and never validates it, so an
// unrecognized spelling is left to the runtime that consumes the template rather
// than refused here.
func authoredDependsOn(raw any) []string {
	switch v := raw.(type) {
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
