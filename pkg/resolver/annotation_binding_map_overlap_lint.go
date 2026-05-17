// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"sort"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// LintAnnotationBindingMaps walks every annotation binding on a
// Manifest and emits one advisory warning per dotted-path subkey that
// the binding declares in BOTH Maps.Infra and Maps.Values lists.
//
// The overlap pattern is an authoring smell rather than a structural
// error: declaring "this subkey routes through both pillars" is
// incoherent because annotation subkeys fire exactly one pillar's
// contribution per authoring side (per the Aggregate-as-Authoring-
// Surface invariant's pillar partition). The runtime path
// [types.AnnotationBindingMaps.Lookup] tie-breaks deterministically
// to "infra", which makes the overlapped subkey Aggregate-inaccessible
// — the Aggregate side then halts via side-vs-pillar enforcement at
// expansion time, despite the subkey appearing in Maps.Values.
//
// Surfacing the smell at the manifest layer (right after Resolve,
// before expansion) gives operators a clear preview of which subkeys
// the deterministic tie-break will silently route to infra. The lint
// runs alongside the print site for catalog-level lint warnings
// ([catalog.Catalog.LintWarnings]) so warning output forms one
// coherent advisory stream the operator scans before reading
// compiled output.
//
// The lint never halts. Authors who deliberately accept the overlap
// (or who run with the warning ignored) still get the deterministic
// tie-break behavior; the runtime side-vs-pillar halt remains the
// load-bearing safety net.
//
// Walk order. The flat-features tier is walked first, then each
// legacy-provider tier in OrderedMap author order. Within a tier,
// annotation keys are walked in OrderedMap author order. Within a
// binding, overlapping paths are emitted in [sort.Strings] order so
// multiple overlaps on one binding produce a stable, deterministic
// warning sequence across runs of identical input.
//
// Returns nil when the manifest is nil or carries no overlapping
// paths.
func LintAnnotationBindingMaps(manifest *types.Manifest) []string {
	if manifest == nil {
		return nil
	}
	var warnings []string

	check := func(annotations *types.OrderedMap[types.AnnotationBinding]) {
		if annotations == nil {
			return
		}
		for _, key := range annotations.Keys() {
			binding, _ := annotations.Get(key)
			infraSet := make(map[string]struct{}, len(binding.Maps.Infra))
			for _, p := range binding.Maps.Infra {
				infraSet[p] = struct{}{}
			}
			var overlapping []string
			for _, p := range binding.Maps.Values {
				if _, has := infraSet[p]; has {
					overlapping = append(overlapping, p)
				}
			}
			sort.Strings(overlapping)
			for _, p := range overlapping {
				warnings = append(warnings, fmt.Sprintf(
					"LINT: Manifest annotation binding %q on Feature %q declares path %q in both Maps.Infra and Maps.Values; the runtime deterministically routes this subkey through the infra pillar, making it Aggregate-inaccessible (side-vs-pillar enforcement halts the Aggregate side at expansion time)",
					key, binding.Feature, p,
				))
			}
		}
	}

	if manifest.Spec.Mappings.Features != nil {
		check(manifest.Spec.Mappings.Features.Annotations)
	}
	for _, prov := range manifest.Spec.Mappings.Legacy.Keys() {
		tier, _ := manifest.Spec.Mappings.Legacy.Get(prov)
		if tier.Features != nil {
			check(tier.Features.Annotations)
		}
	}
	return warnings
}
