// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// ManifestExtendsChain follows a Manifest's extends chain and returns it in
// walk order — the child first, then the Manifest it extends, and so on to the
// root ancestor — or the failure for the one structural fault that stopped it.
//
// Each reference is resolved against releaseDir the same way the Release's own
// manifest reference resolves: an explicit relative-path file, or a
// same-directory identity. The walk itself is the one every kind that declares
// extends runs, so a Manifest loop, a Manifest chain past the depth bound and a
// Manifest reference naming nothing report the same three conditions a
// Component's or a Feature's chain reports, in the same words. What a Manifest
// supplies of its own is what only a Manifest knows: that a link is a FILE, so
// two files declaring one metadata.name are two links; and that its references
// resolve beside the Release rather than in the catalog, so an author whose
// reference names nothing is sent to the directory it was looked for in,
// carrying the failure that looking produced.
//
// ResolveManifestExtends composes exactly this chain. A caller that needs to
// report the chain rather than fold it reads it here, so no second walk can
// disagree with the composition about what the chain is. workspaceRoot is the
// workspace the Release is read in, which decides the root [ResolveManifest]
// holds each reference to — an explicit file and the same-directory scan alike:
// the workspace, or releaseDir itself where it lies outside the workspace, as the
// directory of a Release or a Manifest named from outside it. origin is the file
// the resolution started at — the Release a command or a request names, or the
// Manifest named on its own — which the same-directory scan does not judge again
// (see [ResolveManifest]).
func ManifestExtendsChain(workspaceRoot, releaseDir, origin string, child ResolvedManifest) ([]ResolvedManifest, error) {
	if child.Manifest == nil {
		return nil, nil
	}
	// Why the reason is captured here rather than returned by the walk: the
	// walk asks only whether a reference resolved, because for a catalog
	// entity there is nothing more to say than that the catalog does not hold
	// it. A Manifest reference fails for reasons an author acts on — a file
	// that is not there, a name the directory scan did not index — so the
	// reason is recorded where it is produced and read back in MissingParent.
	// One variable is enough: the walk stops at the first reference it cannot
	// follow, so at most one failure is ever recorded.
	var resolveFailure error
	// The link whose reference is being followed, captured where the walk asks for
	// that reference: the walk hands Lookup the reference alone, and a refusal names
	// the document the reference is written in. The walk asks for a link's reference
	// immediately before it looks the reference up, so this is always that link.
	var declaring ResolvedManifest
	chain, verr := validator.ResolveExtendsChain(envelope.KindManifest, child,
		validator.ExtendsChainSpec[ResolvedManifest]{
			Key:  func(m ResolvedManifest) string { return manifestLinkKey(m.Path) },
			Name: func(m ResolvedManifest) string { return m.Manifest.Metadata.Name },
			ParentLink: func(m ResolvedManifest) string {
				declaring = m
				return m.Manifest.Spec.Extends
			},
			Lookup: func(ref string) (ResolvedManifest, bool) {
				parent, err := resolveManifest(workspaceRoot, releaseDir, origin, ref, declaring.Path, ".spec.extends")
				if err != nil {
					resolveFailure = err
					return ResolvedManifest{}, false
				}
				return parent, true
			},
			MissingParent: func(from, link string) (string, string) {
				return fmt.Sprintf("%s extends %s, which could not be resolved: %v", from, link, resolveFailure),
					"Verify the extends reference is a relative path or the name of a Manifest in the Release's directory"
			},
		})
	if verr != nil {
		// A reference refused for leading out of its root names a file that may well
		// be there, so it halts with its own condition rather than as a parent the
		// walk could not find: the author's fix is to keep the reference inside, not
		// to correct a name. A link beside the Manifest that the same-directory scan
		// refused halts the same way, for the same reason: the fix is the link.
		if errors.Is(resolveFailure, readscope.ErrReferenceLeadsOutOfWorkspace()) ||
			errors.Is(resolveFailure, readscope.ErrReadLeadsOutOfWorkspace()) {
			return nil, resolveFailure
		}
		return nil, verr
	}
	return chain, nil
}

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
// The chain is the one ManifestExtendsChain walks; composition then folds it
// from the root ancestor down to the child so child keys win.
//
// A Manifest with no extends reference is returned unchanged. A reference that
// cannot be resolved, a chain that revisits a file already in the chain, and a
// chain that exceeds the depth bound each halt with the failure the walk
// reports. origin is the file the resolution started at, as it is for
// [ManifestExtendsChain].
func ResolveManifestExtends(workspaceRoot, releaseDir, origin string, child ResolvedManifest) (*types.Manifest, error) {
	if child.Manifest == nil || child.Manifest.Spec.Extends == "" {
		return child.Manifest, nil
	}

	chain, err := ManifestExtendsChain(workspaceRoot, releaseDir, origin, child)
	if err != nil {
		return nil, err
	}

	links := make([]*types.Manifest, len(chain))
	for i, link := range chain {
		links[i] = link.Manifest
	}
	return composeManifestChain(links), nil
}

// manifestLinkKey is the chain-membership key for one resolved link: its file
// path in a single normal form. Normalising here rather than trusting the
// caller keeps the child's key and every parent's key comparable, since a
// parent's path is always built by joining releaseDir to the reference while
// the child's arrives from whichever surface requested the composition.
func manifestLinkKey(path string) string { return filepath.Clean(path) }

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
// across the component and feature mappings. Keys present in src overwrite
// same-named keys already in dst; keys only in dst survive. The three tiers
// (versioned type, unversioned type, name) merge independently per the
// dictionary shape the recipe lookup reads.
func mergeMappingsInto(dst *types.ManifestMappings, src types.ManifestMappings) {
	dst.Components = mergeMappingTier(dst.Components, src.Components)
	dst.Features = mergeMappingTier(dst.Features, src.Features)
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
