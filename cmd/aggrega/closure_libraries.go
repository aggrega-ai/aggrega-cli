// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// discoverClosureLibraries is the single producer of the closure's SchemaLibrary
// axis, shared by pull's pin flow and build's drift-verify flow so both compute
// the identical library closure — the lockfile pull writes and the closure build
// verifies against it cannot diverge. It scans every Component and Feature schema
// in the loaded catalog for cross-document $ref:s, resolves the discovered
// libraries (minimum-version selection + the injected lookup), and descends them
// transitively to a fixpoint, merging the result into cl.SchemaLibraries.
//
// Scanning the whole loaded catalog (not just the entity closure) matches what
// Load's Pass-2 pillar validation expands — every catalog Component/Feature
// schema — so every library a loaded entity references is pinned and verified,
// and the build's bridge has every library Pass-2 will need. Iteration is
// sorted-name for deterministic output.
func discoverClosureLibraries(cl *closure.Closure, cat *catalog.Catalog, lookup closure.Lookup) error {
	var consumers []closure.SchemaConsumer

	compNames := make([]string, 0, len(cat.Components))
	for name := range cat.Components {
		compNames = append(compNames, name)
	}
	sort.Strings(compNames)
	for _, name := range compNames {
		comp := cat.Components[name]
		consumers = append(consumers, closure.SchemaConsumer{URI: name, Schema: &comp.Spec.Schema})
	}

	featNames := make([]string, 0, len(cat.Features))
	for name := range cat.Features {
		featNames = append(featNames, name)
	}
	sort.Strings(featNames)
	for _, name := range featNames {
		feat := cat.Features[name]
		consumers = append(consumers, closure.SchemaConsumer{URI: name, Schema: &feat.Spec.Schema})
	}

	// Transitive body accessor: a discovered library's own entry bodies, for
	// descending its cross-document $ref:s. A local or bridged library is in the
	// catalog; a library present only in the cache (a remote pin not bridged into
	// THIS catalog) is not found here and its transitive descent lands with the
	// pull-side Tier-3 path.
	body := func(lib closure.ResolvedLibrary) ([]*yaml.Node, bool, error) {
		sl, ok := cat.SchemaLibraries[lib.URI]
		if !ok {
			return nil, false, nil
		}
		keys := sl.Spec.Schemas.Keys()
		bodies := make([]*yaml.Node, 0, len(keys))
		for _, k := range keys {
			entry, _ := sl.Spec.Schemas.Get(k)
			node := entry.Content
			bodies = append(bodies, &node)
		}
		return bodies, true, nil
	}

	return cl.DiscoverSchemaLibraries(lookup, consumers, body)
}
