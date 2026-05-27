// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile

import (
	"os"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// LoadExternalEntities reads the Release's referenced Aggregate and
// Manifest files as raw bytes and returns them keyed by the URI the
// closure walker uses to reference them (the Release's spec.aggregate
// / spec.manifest values). The bytes are passed verbatim into
// ClosureLookupWithExternals so the walker hits these URIs at the
// "external" tier instead of falling through to Tier 3.
//
// Aggregate and Manifest are deployment-side entities loaded by
// resolver.LoadAggregate / resolver.LoadManifest and are not part of
// the in-memory catalog (catalog.Load silently skips Manifest kinds).
// Reading the raw bytes here keeps the digest computation consistent
// with the Tier-1 catalog path (sha256 over the on-disk YAML).
//
// Shared by the CLI's pull/build paths and the daemon's closure
// resolution so both ends seed the external tier identically.
func LoadExternalEntities(rel *types.Release, aggPath, manifestPath string) map[string][]byte {
	out := map[string][]byte{}
	if data, err := os.ReadFile(aggPath); err == nil {
		out[rel.Spec.Aggregate] = data
	}
	if data, err := os.ReadFile(manifestPath); err == nil {
		out[rel.Spec.Manifest] = data
	}
	return out
}
