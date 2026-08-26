// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// LintAnnotationBindingMaps walks every annotation binding on a
// Manifest and reports one advisory per dotted-path subkey that
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
// the deterministic tie-break will silently route to infra.
//
// Each finding carries [ErrAnnotationBindingPillarOverlap] and its own
// locator, and is returned rather than printed, so it joins the one advisory
// collection every other advisory flows through: the build renders that
// collection to a terminal and the compiler-as-service daemon maps the same
// findings to editor warnings positioned on the offending binding key. A
// finding written straight to a stream reaches only the first of those two,
// and reaches it under a shape no consumer can match.
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
// finding sequence across runs of identical input.
//
// Returns nil when the manifest is nil or carries no overlapping
// paths.
func LintAnnotationBindingMaps(manifest *types.Manifest) []*ResolverError {
	if manifest == nil {
		return nil
	}
	var findings []*ResolverError

	check := func(providerTier string, annotations *types.OrderedMap[types.AnnotationBinding]) {
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
				findings = append(findings, &ResolverError{
					Entity: "Manifest",
					Field:  annotationBindingField(providerTier, key),
					Message: fmt.Sprintf(
						"annotation binding %q on Feature %q declares path %q in both maps.infra and maps.values; the routing tie-break sends this subkey through the infra pillar, which makes it unreachable from the Aggregate side — declare two bindings under two annotation keys if both pillars are meant to route it",
						key, binding.Feature, p,
					),
					Err: ErrAnnotationBindingPillarOverlap(),
				})
			}
		}
	}

	if manifest.Spec.Mappings.Features != nil {
		check("", manifest.Spec.Mappings.Features.Annotations)
	}
	for _, prov := range manifest.Spec.Mappings.Legacy.Keys() {
		tier, _ := manifest.Spec.Mappings.Legacy.Get(prov)
		if tier.Features != nil {
			check(prov, tier.Features.Annotations)
		}
	}
	return findings
}
