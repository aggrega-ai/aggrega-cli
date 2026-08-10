// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// ResolveProviderSocketEntities resolves the workspace provider-socket bindings
// (.aggrega-config.yaml plugins.providers) to their Plugin entities on the
// catalog-entity axis and merges them into the closure. It runs as a pull-time
// pass, separate from ComputeClosure: ComputeClosure stays hermetic and
// config-blind to the provider socket, and build / serve reconstruct these
// provider Plugin entities from the lockfile rather than from live config, so a
// build's entity axis is invariant to a workspace-config edit made after pull.
//
// For each binding with a non-empty URI, the plugin-kinded entity is resolved
// via the Lookup and merged into cl.Entities: a fresh URI appends a new
// ResolvedRef carrying the plugin kind hint and a referenced-from path naming the
// providers socket; a URI already present unions the socket referenced-from into
// the existing entry. The merged slice stays sorted by URI with sorted
// referenced-from paths, so two runs over the same input produce identical
// output. A missing entity halts loudly with ErrUnresolvableURI.
//
// Registry-only pinning: an entity is pinned only when it is a pulled artifact
// (the registry-backed tiers). A provider entity already present on the machine is
// left unpinned — it needs no lockfile pin because the build reconstructs the
// binding from the lockfile and then resolves the serving plugin from where it
// already is, through the local catalog walk for a locally-authored provider or
// from the binary itself for a core provider that ships inside it. Pinning either
// would leave a lockfile entry the hermetic build cannot reconstruct. This applies
// the same entity-axis rule the pipeline-plugin entity descent does; every binding
// this pass sees is a provider-socket binding, so the from-the-binary case is
// provider-scoped by construction here.
//
// A capability-only binding (URI == "") names a render-engine target but
// delegates the choice of serving plugin to capability resolution; it pins no
// entity here and does not halt the pass.
func ResolveProviderSocketEntities(cl *Closure, providerRefs []workspaceconfig.PluginRef, lookup Lookup) error {
	if cl == nil {
		return nil
	}
	const refPath = ".aggrega-config.yaml.plugins.providers"

	byURI := map[string]int{}
	for i := range cl.Entities {
		byURI[cl.Entities[i].URI] = i
	}

	for _, ref := range providerRefs {
		if ref.URI == "" {
			continue
		}
		res, err := lookup.ResolveCatalogEntity(ref.URI, pluginKindHint)
		if err != nil {
			return fmt.Errorf("resolve: resolve entity %q: %w", ref.URI, err)
		}
		if res.NotFound {
			return fmt.Errorf("%w: entity %q (referenced from %s)",
				ErrUnresolvableURI(), ref.URI, refPath)
		}
		// Registry-only pinning: a provider already present on the machine resolves
		// at build from where it already is — the local catalog walk, or the binary's
		// own copy of a core provider — and is never lockfile-pinned, so pinning it
		// here would strand a lockfile entry the hermetic build cannot reconstruct.
		if entityNeedsNoPin(res.ResolutionPath) {
			continue
		}
		if idx, ok := byURI[ref.URI]; ok {
			cl.Entities[idx].ReferencedFrom = unionReferencedFrom(cl.Entities[idx].ReferencedFrom, refPath)
			continue
		}
		cl.Entities = append(cl.Entities, ResolvedRef{
			URI:             ref.URI,
			ResolvedVersion: res.ResolvedVersion,
			Digest:          res.Digest,
			ManifestDigest:  res.ManifestDigest,
			ResolutionPath:  res.ResolutionPath,
			ReferencedFrom:  []string{refPath},
			KindHint:        pluginKindHint,
		})
		byURI[ref.URI] = len(cl.Entities) - 1
	}

	canonicaliseClosure(cl)
	return nil
}

// unionReferencedFrom appends refPath to refs when absent, leaving the slice
// unchanged when it is already present. canonicaliseClosure performs the final
// sort; this helper only guarantees no duplicate entries.
func unionReferencedFrom(refs []string, refPath string) []string {
	for _, r := range refs {
		if r == refPath {
			return refs
		}
	}
	return append(refs, refPath)
}
