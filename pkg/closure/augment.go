// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import (
	"fmt"
	"sort"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// LibrarySource is the resolved-closure read surface the library-discovery
// producers use to fetch each in-closure entity body by URI. Every method
// returns a pkg/types value plus a found flag; a false found return means the
// closure does not reach that URI through the source. Implementations resolve
// the body from a locally-authored catalog or from the content cache (a remote
// pinned entity, via the verified lazy library walk); the producers stay
// decoupled from that resolution machinery.
type LibrarySource interface {
	Component(uri string) (types.Component, bool)
	Feature(uri string) (types.Feature, bool)
	SchemaLibrary(uri string) (types.SchemaLibrary, bool)
	RecipeByURI(uri string) (types.Recipe, bool)
	FeatureRecipeByURI(uri string) (types.FeatureRecipe, bool)
	MappingLibrary(uri string) (types.MappingLibrary, bool)
}

// AugmentClosure resolves the cross-document library axes the entity walk does
// not descend — the SchemaLibrary axis first, then the MappingLibrary axis —
// merging each into the closure. Both discoveries read the resolved closure
// through the same source, so a pull and a build of the same Release compute the
// identical library closure. It routes libraries only; the plugin,
// sub-aggregate, and source axes are reconstructed by their own passes.
func AugmentClosure(cl *Closure, src LibrarySource, lookup Lookup) error {
	if err := discoverClosureLibraries(cl, src, lookup); err != nil {
		return fmt.Errorf("schema-library discovery: %w", err)
	}
	if err := discoverClosureMappingLibraries(cl, src, lookup); err != nil {
		return fmt.Errorf("mapping-library discovery: %w", err)
	}
	return nil
}

// discoverClosureLibraries is the single producer of the closure's SchemaLibrary
// axis, shared by pull's pin flow and build's drift-verify flow. Both resolve the
// closure through the same lazy lookup, so they compute the identical library
// closure: the lockfile pull writes and the closure build verifies against it
// agree by construction, because pull and build read the same resolved closure
// rather than two independently-built catalog views. It scans every in-closure
// Component and Feature schema for cross-document $ref:s, resolves the discovered libraries
// (minimum-version selection + the injected lookup), and descends them
// transitively to a fixpoint, merging the result into cl.SchemaLibraries.
//
// Scanning is CLOSURE-SCOPED: only the Components and Features the Release reaches
// (cl.Entities) are scanned for $ref:s, matching the closure-scoped Pass-2 gate —
// so a library referenced only by an out-of-closure entity is neither pinned nor
// resolution-fatal, and an out-of-closure entity with a dangling $ref never fails
// the build/pull. Transitive descent into a discovered library's own $ref:s is
// unscoped (those libraries are in-closure by construction). Iteration is
// sorted-name for deterministic output.
func discoverClosureLibraries(cl *Closure, src LibrarySource, lookup Lookup) error {
	// Closure-scoped consumer scan over the lazy lookup: resolve each closure
	// entity and collect its schema — components first, then features, each sorted
	// by name. This preserves the deterministic consumer order the eager-map scan
	// produced, now reading the resolved closure rather than the bridged catalog.
	// Both build (computeBuildClosure) and pull pass their own lazy lookup, so
	// there is one producer and no fork.
	var compConsumers, featConsumers []SchemaConsumer
	compSeen, featSeen := map[string]struct{}{}, map[string]struct{}{}
	for _, e := range cl.Entities {
		if comp, ok := src.Component(e.URI); ok {
			if _, dup := compSeen[comp.Metadata.Name]; dup {
				continue
			}
			compSeen[comp.Metadata.Name] = struct{}{}
			c := comp
			compConsumers = append(compConsumers, SchemaConsumer{URI: c.Metadata.Name, Schema: &c.Spec.Schema})
			continue
		}
		if feat, ok := src.Feature(e.URI); ok {
			if _, dup := featSeen[feat.Metadata.Name]; dup {
				continue
			}
			featSeen[feat.Metadata.Name] = struct{}{}
			f := feat
			featConsumers = append(featConsumers, SchemaConsumer{URI: f.Metadata.Name, Schema: &f.Spec.Schema})
		}
	}
	sort.Slice(compConsumers, func(i, j int) bool { return compConsumers[i].URI < compConsumers[j].URI })
	sort.Slice(featConsumers, func(i, j int) bool { return featConsumers[i].URI < featConsumers[j].URI })
	consumers := append(compConsumers, featConsumers...)

	// Transitive body accessor: a discovered library's own entry bodies, for
	// descending its cross-document $ref:s, resolved through the lazy library walk
	// (Tier-1 + pinned Tier-2). A library the closure does not reach is not found
	// here and its transitive descent lands with the lookup's Tier-3 path.
	body := func(lib ResolvedLibrary) ([]*yaml.Node, bool, error) {
		sl, ok := src.SchemaLibrary(lib.URI)
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

// discoverClosureMappingLibraries is the mapping-axis mirror of
// discoverClosureLibraries: the single producer of the closure's MappingLibrary
// axis, shared by pull's pin flow and build's drift-verify flow. It scans every
// Recipe and FeatureRecipe valuesMapping in the loaded catalog for a field-root
// `$import:` library reference, resolves the discovered libraries (minimum-version
// selection + the injected lookup), and descends them transitively to a fixpoint,
// merging the result into cl.MappingLibraries.
//
// A valuesMapping is presented to the walker in its authored `$import: <ref>`
// directive form; a Mode-1 inline values template and a Mode-2 local-file import
// carry no library reference and are skipped by the walker's identity gate.
// Iteration is sorted-name for deterministic output.
func discoverClosureMappingLibraries(cl *Closure, src LibrarySource, lookup Lookup) error {
	// Closure-scoped: resolve each closure recipe / feature-recipe via the lazy
	// lookup and collect its `$import:` valuesMapping consumer — recipes first,
	// then feature-recipes, each sorted by URI for deterministic output. A Mode-1
	// inline template or a Mode-2 local-file import carries no library reference
	// and is skipped by the directive gate.
	var recipeConsumers, frConsumers []MappingConsumer
	rSeen, frSeen := map[string]struct{}{}, map[string]struct{}{}
	for _, e := range cl.Entities {
		if r, ok := src.RecipeByURI(e.URI); ok {
			if _, dup := rSeen[e.URI]; dup {
				continue
			}
			rSeen[e.URI] = struct{}{}
			if tmpl, ok := valuesMappingImportDirective(r.Spec.ValuesMapping); ok {
				recipeConsumers = append(recipeConsumers, MappingConsumer{URI: e.URI, Template: tmpl})
			}
			continue
		}
		if fr, ok := src.FeatureRecipeByURI(e.URI); ok {
			if _, dup := frSeen[e.URI]; dup {
				continue
			}
			frSeen[e.URI] = struct{}{}
			if tmpl, ok := valuesMappingImportDirective(fr.Spec.ValuesMapping); ok {
				frConsumers = append(frConsumers, MappingConsumer{URI: e.URI, Template: tmpl})
			}
		}
	}
	sort.Slice(recipeConsumers, func(i, j int) bool { return recipeConsumers[i].URI < recipeConsumers[j].URI })
	sort.Slice(frConsumers, func(i, j int) bool { return frConsumers[i].URI < frConsumers[j].URI })
	consumers := append(recipeConsumers, frConsumers...)

	// Transitive body accessor: a discovered MappingLibrary's own entry bodies, for
	// descending their field-root `$import:`s, resolved through the lazy library
	// walk. A library the closure does not reach lands with the lookup's Tier-3 path.
	body := func(lib ResolvedLibrary) ([]string, bool, error) {
		ml, ok := src.MappingLibrary(lib.URI)
		if !ok {
			return nil, false, nil
		}
		bodies := make([]string, 0, ml.Spec.Mappings.Len())
		for _, k := range ml.Spec.Mappings.Keys() {
			entry, _ := ml.Spec.Mappings.Get(k)
			bodies = append(bodies, entry.Body)
		}
		return bodies, true, nil
	}

	return cl.DiscoverMappingLibraries(lookup, consumers, body)
}

// valuesMappingImportDirective renders a valuesMapping that carries an `$import:`
// target back into its authored directive form (`$import: <target>`) for the
// discovery walker, which scans a template body for a field-root `$import:`. A
// Mode-1 inline values template carries no import and yields ok=false.
func valuesMappingImportDirective(vm types.ValuesMapping) (string, bool) {
	if vm.Import == "" {
		return "", false
	}
	return "$import: " + vm.Import, true
}
