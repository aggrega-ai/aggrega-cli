// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// maxManifestExtendsDepth bounds the Manifest extends chain so a
// pathological author cannot force unbounded parent resolution. The bound
// matches the entity-inheritance walker's depth ceiling.
const maxManifestExtendsDepth = 10

// ResolveManifestExtends composes a Manifest's extends chain into a single
// resolved Manifest. A Manifest may declare a single extends reference to a
// base Manifest whose mappings it inherits; composition follows shallow
// per-tier key replacement — the child's type and name sub-dictionaries
// inside each mapping section merge independently with the parent's
// corresponding tier, and within each tier a child key overwrites the
// parent key of the same name. The merge does not deep-merge individual
// Recipe URI values: a tier key present in both parent and child resolves
// to the child's value. This keeps the resolved mappings a flat per-key
// dictionary, preserving the constant-time per-tier lookup the recipe
// cascade relies on.
//
// The chain is walked iteratively from child toward root ancestor. Each
// extends reference is resolved against releaseDir the same way the
// Release's own manifest reference resolves — an explicit relative-path
// file or a same-directory identity. Composition then folds the chain from
// the root ancestor down to the child so child keys win.
//
// A Manifest with no extends reference is returned unchanged. A reference
// that cannot be resolved, a chain that revisits a Manifest already in the
// chain, and a chain that exceeds the depth bound each halt with a
// diagnostic naming the offending reference.
func ResolveManifestExtends(releaseDir string, child *types.Manifest) (*types.Manifest, error) {
	if child == nil || child.Spec.Extends == "" {
		return child, nil
	}

	chain := []*types.Manifest{child}
	visited := map[string]bool{child.Metadata.Name: true}
	current := child

	for current.Spec.Extends != "" {
		if len(chain) >= maxManifestExtendsDepth {
			return nil, fmt.Errorf("%w: Manifest %q extends chain exceeds maximum depth of %d; reduce the chain length",
				validator.ErrInheritanceMerge(), child.Metadata.Name, maxManifestExtendsDepth)
		}

		parentRef := current.Spec.Extends
		if visited[parentRef] {
			return nil, fmt.Errorf("%w: Manifest %q extends %q, which is already in the chain; remove the circular reference",
				validator.ErrCyclicExtension(), current.Metadata.Name, parentRef)
		}

		parentRes, err := ResolveManifest(releaseDir, parentRef)
		if err != nil {
			return nil, fmt.Errorf("%w: Manifest %q extends %q, which could not be resolved: %v; verify the extends reference is a relative path or the name of a Manifest in the Release's directory",
				validator.ErrInheritanceMerge(), current.Metadata.Name, parentRef, err)
		}
		parent := parentRes.Manifest

		visited[parentRef] = true
		visited[parent.Metadata.Name] = true
		chain = append(chain, parent)
		current = parent
	}

	return composeManifestChain(chain), nil
}

// composeManifestChain folds an extends chain (child first, root ancestor
// last) into a single Manifest by shallow per-tier key replacement. It
// starts from the root ancestor and applies each descendant in turn, so the
// child — applied last — wins on any key collision. Identity (apiVersion,
// kind, metadata) comes from the child; only the mappings are composed.
func composeManifestChain(chain []*types.Manifest) *types.Manifest {
	child := chain[0]
	out := &types.Manifest{
		APIVersion: child.APIVersion,
		Kind:       child.Kind,
		Metadata:   child.Metadata,
		Defs:       child.Defs,
		Spec:       types.ManifestSpec{},
	}

	for i := len(chain) - 1; i >= 0; i-- {
		mergeMappingsInto(&out.Spec.Mappings, chain[i].Spec.Mappings)
	}

	return out
}

// mergeMappingsInto folds src into dst by shallow per-tier key replacement
// across the flat-shape component / feature tiers and the legacy
// per-provider tiers. Keys present in src overwrite same-named keys already
// in dst; keys only in dst survive. The three tiers (versioned type,
// unversioned type, name) merge independently per the flat dictionary shape
// the recipe cascade reads.
func mergeMappingsInto(dst *types.ManifestMappings, src types.ManifestMappings) {
	dst.Components = mergeMappingTier(dst.Components, src.Components)
	dst.Features = mergeMappingTier(dst.Features, src.Features)

	for prov, srcPM := range src.Legacy.All() {
		existing, ok := dst.Legacy.Get(prov)
		if !ok {
			existing = types.ProviderMappings{}
		}
		existing.Components = mergeMappingTier(existing.Components, srcPM.Components)
		existing.Features = mergeMappingTier(existing.Features, srcPM.Features)
		dst.Legacy.Set(prov, existing)
	}
}

// mergeMappingTier folds the src tier's type and name dictionaries into dst
// by key replacement, returning the merged tier. A nil dst with a non-nil
// src yields a fresh tier populated from src; a nil src leaves dst
// unchanged. The type and name dictionaries are merged independently. The
// annotation bindings are replaced wholesale when src declares them, since
// an annotation registry is consumed as a unit rather than per-key composed.
func mergeMappingTier(dst, src *types.MappingTier) *types.MappingTier {
	if src == nil {
		return dst
	}
	if dst == nil {
		dst = &types.MappingTier{}
	}
	for k, v := range src.Type.All() {
		dst.Type.Set(k, v)
	}
	for k, v := range src.Name.All() {
		dst.Name.Set(k, v)
	}
	if src.Annotations != nil {
		dst.Annotations = src.Annotations
	}
	return dst
}
