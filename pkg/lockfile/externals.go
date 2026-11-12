// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package lockfile

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ExternalEntitiesFromBytes returns the Release's referenced Aggregate and
// Manifest raw bytes keyed by the identity the closure walker gives them: the
// Release's spec.aggregate as an Aggregate, and its spec.manifest as a Manifest.
// The bytes are passed verbatim into ClosureLookupWithExternals so the walker hits
// these references at the "external" tier instead of falling through to Tier 3.
//
// The kind is part of the key because the two may share a name — every Release an
// importer drafts names its Aggregate and its Manifest alike — and each is pinned
// by a row of its own, so each must resolve to its own bytes rather than one to
// the other's.
//
// The bytes come from the triad resolver (resolver.ResolveAggregate /
// ResolveManifest), which holds the exact bytes it resolved — whether from a
// same-directory file, a ./ or ../ file, or the catalog tier — so the digest
// computation stays consistent with the Tier-1 catalog path (sha256 over the
// authored YAML) without re-reading a file that, for a catalog-resolved
// Aggregate, has no release-relative path.
//
// Shared by the CLI's pull/build paths and the daemon's closure resolution so
// both ends seed the external tier identically. Which entities those are, and the
// identity each is keyed by, is closure.ReleaseExternals; an entity whose bytes
// are empty is not registered.
func ExternalEntitiesFromBytes(rel *types.Release, aggBytes, manBytes []byte) map[closure.EntityKey][]byte {
	documents := map[string][]byte{closure.KindAggregate: aggBytes, closure.KindManifest: manBytes}
	out := map[closure.EntityKey][]byte{}
	for _, key := range closure.ReleaseExternals(rel) {
		if b := documents[key.Kind]; len(b) > 0 {
			out[key] = b
		}
	}
	return out
}
