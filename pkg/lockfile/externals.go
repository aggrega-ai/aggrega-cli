// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ExternalEntitiesFromBytes returns the Release's referenced Aggregate and
// Manifest raw bytes keyed by the URI the closure walker uses to reference them
// (the Release's spec.aggregate / spec.manifest values). The bytes are passed
// verbatim into ClosureLookupWithExternals so the walker hits these URIs at the
// "external" tier instead of falling through to Tier 3.
//
// The bytes come from the triad resolver (resolver.ResolveAggregate /
// ResolveManifest), which holds the exact bytes it resolved — whether from a
// same-directory file, a ./ or ../ file, or the catalog tier — so the digest
// computation stays consistent with the Tier-1 catalog path (sha256 over the
// authored YAML) without re-reading a file that, for a catalog-resolved
// Aggregate, has no release-relative path.
//
// Shared by the CLI's pull/build paths and the daemon's closure resolution so
// both ends seed the external tier identically.
func ExternalEntitiesFromBytes(rel *types.Release, aggBytes, manBytes []byte) map[string][]byte {
	out := map[string][]byte{}
	if len(aggBytes) > 0 {
		out[rel.Spec.Aggregate] = aggBytes
	}
	if len(manBytes) > 0 {
		out[rel.Spec.Manifest] = manBytes
	}
	return out
}
