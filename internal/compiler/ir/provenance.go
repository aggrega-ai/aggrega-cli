// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// buildProvenance assembles the IRGraph.Spec.Provenance block — the audit
// trail that records which compiler binary produced this IR and which
// authoring artifacts it was built from. Five fields cover the chain:
//
//   - SchemaVersion — pinned to types.IRAPIVersion. Identifies the IR
//     schema the emitted graph conforms to so downstream consumers can
//     evolve their own version-handling without out-of-band coordination.
//   - CompilerVersion — supplied by the caller via BuildIRConfig and
//     plumbed through here. The command layer derives the CLI binary's
//     version string and threads it in (directly for pull/inspect, via
//     the pipeline's Inputs for build and the daemon); the builder pipes
//     it through without deriving it, so the value stays injectable for
//     deterministic tests.
//   - AggregateVersion / ManifestVersion / ReleaseVersion — read from
//     the resolved entities' metadata. Conditionally populated: a nil
//     entity in ResolveResult leaves its version field at the zero value.
//     This lets partial pipelines (a build with no Manifest, an inspect
//     against an Aggregate alone) emit valid provenance without
//     fabricating absent versions: the key is still emitted, carrying the
//     empty string.
//
// The function never returns an error; provenance generation is a flat
// metadata copy with no failure modes.
func buildProvenance(res *resolver.ResolveResult, compilerVersion string) types.IRProvenance {
	prov := types.IRProvenance{
		SchemaVersion:   types.IRAPIVersion,
		CompilerVersion: compilerVersion,
	}
	if res.Aggregate != nil {
		prov.AggregateVersion = res.Aggregate.Metadata.Version
	}
	if res.Manifest != nil {
		prov.ManifestVersion = res.Manifest.Metadata.Version
	}
	if res.Release != nil {
		prov.ReleaseVersion = res.Release.Metadata.Version
	}
	return prov
}
