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

// ResolvedRelease is the Release counterpart of [ResolvedManifest]: the loaded
// entity together with the file it was read from.
//
// The path travels with the entity because it is the link's identity along an
// extends chain, not merely provenance. Two Release files may legally declare one
// metadata.name — an environment pair naming the deployed system rather than the
// environment is the ordinary shape — so a chain keyed on the name would fold two
// distinct links into one and report a loop nobody wrote.
//
// Digest is the content digest of the bytes the Release was decoded from, set by
// LoadResolvedRelease from the same read. It is what a lockfile records for the
// document, and it is empty for a value assembled from a Release already in hand.
type ResolvedRelease struct {
	Release *types.Release
	Path    string
	Digest  string
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
//
// Each link's reference is resolved beside the Release that writes it — the
// directory holding that link's file — so a parent kept in another directory
// names its own parent as its author sees it, exactly as its parameters import
// is resolved beside it. Every reference along the chain is held to one root,
// the one readscope.CheckReference picks for releaseDir, the directory holding
// child: the workspace at workspaceRoot, or releaseDir itself where child was
// named from outside the workspace. A reference leading out of that root halts
// with readscope.ErrReferenceLeadsOutOfWorkspace, and the Release it names is
// not read.
func ReleaseExtendsChain(workspaceRoot, releaseDir string, child ResolvedRelease) ([]ResolvedRelease, error) {
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
	// The link whose reference is being followed, captured where the walk asks for
	// that reference: the walk hands Lookup the reference alone, and a refusal names
	// the document the reference is written in. The walk asks for a link's reference
	// immediately before it looks the reference up, so this is always that link.
	var declaring ResolvedRelease
	chain, verr := validator.ResolveExtendsChain(envelope.KindRelease, child,
		validator.ExtendsChainSpec[ResolvedRelease]{
			Key:  func(r ResolvedRelease) string { return releaseLinkKey(r.Path) },
			Name: func(r ResolvedRelease) string { return r.Release.Metadata.Name },
			ParentLink: func(r ResolvedRelease) string {
				declaring = r
				return r.Release.Spec.Extends
			},
			Lookup: func(ref string) (ResolvedRelease, bool) {
				parent, err := resolveReleaseExtendsRef(workspaceRoot, releaseDir, declaring, ref)
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
		// A reference refused for leading out of its root names a file that may well
		// be there, so it halts with its own condition rather than as a parent the
		// walk could not find: the author's fix is to keep the reference inside, not
		// to correct a path.
		if errors.Is(resolveFailure, readscope.ErrReferenceLeadsOutOfWorkspace()) {
			return nil, resolveFailure
		}
		return nil, verr
	}
	return chain, nil
}

// resolveReleaseExtendsRef resolves one extends reference, written in the Release
// declaring, to the Release it names: relative to the directory holding
// declaring's file, and held to the root readscope.CheckReference picks for
// anchor, the directory of the Release the chain starts at, given the workspace
// at workspaceRoot.
//
// The document itself is read by [LoadResolvedRelease], so a parent along a chain
// is loaded by the one function that loads a Release anywhere else: the envelope
// gate it runs, the normalization it applies and the failure it reports for a
// file that cannot be read are the same for a parent as for the Release a caller
// named directly, and the parent carries the digest of the bytes it was decoded
// from. What this function decides is which file the reference points at, which
// is the only part a chain step has of its own.
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
func resolveReleaseExtendsRef(workspaceRoot, anchor string, declaring ResolvedRelease, ref string) (ResolvedRelease, error) {
	if !isRelativePathRef(ref) {
		return ResolvedRelease{}, &ResolverError{
			Entity: "Release",
			Field:  ref,
			Message: "a Release extends reference addresses a file beside the Release, so it is " +
				"written as a relative path beginning ./ or ../",
			Err: ErrYAMLLoad(),
		}
	}
	from := filepath.Dir(declaring.Path)
	path := filepath.Join(from, ref)
	if err := readscope.CheckReference(workspaceRoot, readscope.Reference{
		Document: declaring.Path, Anchor: anchor, From: from, Key: ".spec.extends", Value: ref, Target: path,
	}); err != nil {
		return ResolvedRelease{}, err
	}
	return LoadResolvedRelease(path)
}

// releaseLinkKey is the chain-membership key for one resolved link: its file path
// in a single normal form. Normalising here rather than trusting the caller keeps
// the child's key and every parent's key comparable, since a parent's path is
// always built by joining the directory of the link that names it to the
// reference while the child's arrives from whichever surface requested the
// resolution.
func releaseLinkKey(path string) string { return filepath.Clean(path) }
