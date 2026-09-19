// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// ResolvedRelease is the Release counterpart of [ResolvedManifest]: the loaded
// entity together with the file it was read from.
//
// The path travels with the entity because it is the link's identity along an
// extends chain, not merely provenance. Two Release files may legally declare one
// metadata.name — an environment pair naming the deployed system rather than the
// environment is the ordinary shape — so a chain keyed on the name would fold two
// distinct links into one and report a loop nobody wrote.
type ResolvedRelease struct {
	Release *types.Release
	Path    string
}

// ReleaseExtendsChain follows a Release's extends chain and returns it in walk
// order — the child first, then the Release it extends, and so on to the root
// ancestor — or the failure for the one structural fault that stopped it.
//
// The walk itself is the one every kind that declares extends runs, so a Release
// loop, a Release chain past the depth bound and a Release reference naming
// nothing report the same three conditions a Component's or a Manifest's chain
// reports, in the same words. What a Release supplies of its own is what only a
// Release knows: that a link is a FILE, so two files declaring one metadata.name
// are two links; and that its references resolve beside the Release rather than
// in the catalog, so an author whose reference names nothing is sent to the
// directory it was looked for in, carrying the failure that looking produced.
//
// It returns the chain and nothing folded. Composing the chain into a single
// Release — which field a parent contributes and which a child overrides — is a
// separate job with its own contract, and keeping the two apart is what lets the
// three structural conditions be decided once, here, for every kind.
//
// A Release declaring no extends reference yields a chain of one: itself.
func ReleaseExtendsChain(releaseDir string, child ResolvedRelease) ([]ResolvedRelease, error) {
	if child.Release == nil {
		return nil, nil
	}
	// Why the reason is captured here rather than returned by the walk: the walk
	// asks only whether a reference resolved, because for a catalog entity there
	// is nothing more to say than that the catalog does not hold it. A Release
	// reference fails for reasons an author acts on — a file that is not there, a
	// document that did not load — so the reason is recorded where it is produced
	// and read back in MissingParent. One variable is enough: the walk stops at
	// the first reference it cannot follow, so at most one failure is ever
	// recorded.
	var resolveFailure error
	chain, verr := validator.ResolveExtendsChain(envelope.KindRelease, child,
		validator.ExtendsChainSpec[ResolvedRelease]{
			Key:        func(r ResolvedRelease) string { return releaseLinkKey(r.Path) },
			Name:       func(r ResolvedRelease) string { return r.Release.Metadata.Name },
			ParentLink: func(r ResolvedRelease) string { return r.Release.Spec.Extends },
			Lookup: func(ref string) (ResolvedRelease, bool) {
				parent, err := resolveReleaseExtendsRef(releaseDir, ref)
				if err != nil {
					resolveFailure = err
					return ResolvedRelease{}, false
				}
				return parent, true
			},
			MissingParent: func(from, link string) (string, string) {
				return fmt.Sprintf("%s extends %s, which could not be resolved: %v", from, link, resolveFailure),
					"Verify the extends reference is a relative path to a Release file, such as " +
						"./base-release.yaml, resolved from the directory holding this Release"
			},
		})
	if verr != nil {
		return nil, verr
	}
	return chain, nil
}

// resolveReleaseExtendsRef resolves one extends reference to the Release it
// names, relative to releaseDir.
//
// The document itself is read by [LoadRelease], so a parent along a chain is
// loaded by the one function that loads a Release anywhere else: the envelope
// gate it runs, the normalization it applies and the failure it reports for a
// file that cannot be read are the same for a parent as for the Release a caller
// named directly. What this function decides is which file the reference points
// at, which is the only part a chain step has of its own.
//
// It admits the explicit relative-path form alone, and the narrowness is a
// limit of what a Release reference can address today rather than a choice taken
// here. The same-directory scan that lets an aggregate: or manifest: reference
// name an entity by identity indexes Aggregates and Manifests and skips every
// other kind, so a bare token naming a Release matches nothing it holds — and
// widening that scan would make two same-named Releases beside one another a
// halt, on directories that build today. So a bare token is refused with the form
// that does resolve, rather than silently searched for somewhere it cannot be
// found.
func resolveReleaseExtendsRef(releaseDir, ref string) (ResolvedRelease, error) {
	if !isRelativePathRef(ref) {
		return ResolvedRelease{}, &ResolverError{
			Entity: "Release",
			Field:  ref,
			Message: "a Release extends reference addresses a file beside the Release, so it is " +
				"written as a relative path beginning ./ or ../",
			Err: ErrYAMLLoad(),
		}
	}
	path := filepath.Join(releaseDir, ref)
	rel, err := LoadRelease(path)
	if err != nil {
		return ResolvedRelease{}, err
	}
	return ResolvedRelease{Release: rel, Path: path}, nil
}

// releaseLinkKey is the chain-membership key for one resolved link: its file path
// in a single normal form. Normalising here rather than trusting the caller keeps
// the child's key and every parent's key comparable, since a parent's path is
// always built by joining releaseDir to the reference while the child's arrives
// from whichever surface requested the resolution.
func releaseLinkKey(path string) string { return filepath.Clean(path) }
