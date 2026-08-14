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
	WorkloadLibrary(uri string) (types.WorkloadLibrary, bool)
	ArtifactsLibrary(uri string) (types.ArtifactsLibrary, bool)
}

// AugmentClosure resolves the cross-document library axes the entity walk does not
// descend, merging each into the closure. Every discovery reads the resolved
// closure through the same source, so a pull and a build of the same Release
// compute the identical library closure. It routes libraries only; the plugin,
// sub-aggregate, and source axes are reconstructed by their own passes.
//
// The axes run in declared order, and each reports failure under its own
// operator-facing label rather than a hand-written string, so a label cannot
// disagree with the one the lockfile uses for the same axis.
func AugmentClosure(cl *Closure, src LibrarySource, lookup Lookup) error {
	for _, d := range libraryDiscoverers() {
		if err := d.discover(cl, src, lookup); err != nil {
			return fmt.Errorf("%s discovery: %w", d.axis.Label, err)
		}
	}
	return nil
}

// libraryDiscoverer binds one axis to the producer that knows where that axis's
// references live in an entity. That binding is the one thing a shared driver
// cannot supply: which field of a Recipe or Component carries the reference is new
// information per axis, not a copy of another axis's.
type libraryDiscoverer struct {
	axis     LibraryAxis
	discover func(*Closure, LibrarySource, Lookup) error
}

// libraryDiscoverers returns the per-axis producers in declared order. It is the
// single place the axis set is bound to its producers; the coverage assertion
// beside it requires every declared axis to appear, so an axis added to the set
// cannot silently go undiscovered.
func libraryDiscoverers() []libraryDiscoverer {
	return []libraryDiscoverer{
		{LibraryAxisNamed("SchemaLibraries"), discoverClosureLibraries},
		{LibraryAxisNamed("MappingLibraries"), discoverClosureMappingLibraries},
		{LibraryAxisNamed("WorkloadLibraries"), discoverClosureWorkloadLibraries},
		{LibraryAxisNamed("ArtifactsLibraries"), discoverClosureArtifactsLibraries},
	}
}

// collectTemplateConsumers walks the closure's entities once and emits the
// consumers a template axis contributes, recipes first and then feature-recipes,
// each sorted by URI for deterministic output. Each entity is visited once per
// axis; a duplicate URI contributes nothing further.
//
// The traversal, the deduplication and the ordering are identical on every
// template axis, so they are written here once; the caller supplies only the
// extraction that differs — which field of the entity carries the reference.
func collectTemplateConsumers(
	cl *Closure,
	src LibrarySource,
	fromRecipe func(uri string, r types.Recipe) []TemplateConsumer,
	fromFeatureRecipe func(uri string, fr types.FeatureRecipe) []TemplateConsumer,
) []TemplateConsumer {
	var recipeConsumers, frConsumers []TemplateConsumer
	rSeen, frSeen := map[string]struct{}{}, map[string]struct{}{}
	for _, e := range cl.Entities {
		if r, ok := src.RecipeByURI(e.URI); ok {
			if _, dup := rSeen[e.URI]; dup {
				continue
			}
			rSeen[e.URI] = struct{}{}
			recipeConsumers = append(recipeConsumers, fromRecipe(e.URI, r)...)
			continue
		}
		if fr, ok := src.FeatureRecipeByURI(e.URI); ok {
			if _, dup := frSeen[e.URI]; dup {
				continue
			}
			frSeen[e.URI] = struct{}{}
			frConsumers = append(frConsumers, fromFeatureRecipe(e.URI, fr)...)
		}
	}
	sort.SliceStable(recipeConsumers, func(i, j int) bool { return recipeConsumers[i].URI < recipeConsumers[j].URI })
	sort.SliceStable(frConsumers, func(i, j int) bool { return frConsumers[i].URI < frConsumers[j].URI })
	return append(recipeConsumers, frConsumers...)
}

// importConsumer renders a field that carries an `$import:` target back into its
// authored directive form for the discovery walker, which scans a template body
// for a field-root `$import:`. An inline body carries no import and yields none.
func importConsumer(uri, importTarget string) []TemplateConsumer {
	if importTarget == "" {
		return nil
	}
	return []TemplateConsumer{{URI: uri, Template: "$import: " + importTarget}}
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
// (cl.Entities) are scanned for $ref:s, matching the closure-scoped pillar-validation gate —
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

// discoverClosureMappingLibraries is the single producer of the closure's
// MappingLibrary axis. It scans every in-closure Recipe and FeatureRecipe
// valuesMapping for a field-root `$import:` reference and descends the discovered
// libraries transitively.
//
// A valuesMapping is presented to the walker in its authored `$import: <ref>`
// directive form; an inline values template and a local-file import carry no
// library reference and are skipped by the walker's identity gate.
func discoverClosureMappingLibraries(cl *Closure, src LibrarySource, lookup Lookup) error {
	consumers := collectTemplateConsumers(cl, src,
		func(uri string, r types.Recipe) []TemplateConsumer {
			return importConsumer(uri, r.Spec.ValuesMapping.Import)
		},
		func(uri string, fr types.FeatureRecipe) []TemplateConsumer {
			return importConsumer(uri, fr.Spec.ValuesMapping.Import)
		})

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

	return cl.DiscoverTemplateLibraries(LibraryAxisNamed("MappingLibraries"), lookup, consumers, body)
}

// discoverClosureWorkloadLibraries is the single producer of the closure's
// WorkloadLibrary axis. It scans every in-closure Recipe and FeatureRecipe workload
// for a field-root `$import:` reference and descends the discovered libraries
// transitively.
//
// A FeatureRecipe workload library reference is refused at envelope validation, so
// an attachment consumer contributes no edge here; scanning it is harmless and
// keeps the producer symmetric with the mapping axis.
func discoverClosureWorkloadLibraries(cl *Closure, src LibrarySource, lookup Lookup) error {
	consumers := collectTemplateConsumers(cl, src,
		func(uri string, r types.Recipe) []TemplateConsumer {
			return importConsumer(uri, r.Spec.Workload.Import)
		},
		func(uri string, fr types.FeatureRecipe) []TemplateConsumer {
			return importConsumer(uri, fr.Spec.Workload.Import)
		})

	body := func(lib ResolvedLibrary) ([]string, bool, error) {
		wl, ok := src.WorkloadLibrary(lib.URI)
		if !ok {
			return nil, false, nil
		}
		bodies := make([]string, 0, wl.Spec.Workloads.Len())
		for _, k := range wl.Spec.Workloads.Keys() {
			entry, _ := wl.Spec.Workloads.Get(k)
			bodies = append(bodies, entry.Body)
		}
		return bodies, true, nil
	}

	return cl.DiscoverTemplateLibraries(LibraryAxisNamed("WorkloadLibraries"), lookup, consumers, body)
}

// discoverClosureArtifactsLibraries is the single producer of the closure's
// ArtifactsLibrary axis. Unlike the workload and mapping axes — where an entity
// carries a single field — artifacts is a MAP, so one entity may contribute several
// edges, one per entry that imports a bundle. Entry author order is preserved.
func discoverClosureArtifactsLibraries(cl *Closure, src LibrarySource, lookup Lookup) error {
	consumers := collectTemplateConsumers(cl, src,
		func(uri string, r types.Recipe) []TemplateConsumer {
			return artifactsImportConsumers(uri, r.Spec.Artifacts)
		},
		func(uri string, fr types.FeatureRecipe) []TemplateConsumer {
			return artifactsImportConsumers(uri, fr.Spec.Artifacts)
		})

	// A discovered bundle's own file bodies, flattened across every bundle entry.
	body := func(lib ResolvedLibrary) ([]string, bool, error) {
		al, ok := src.ArtifactsLibrary(lib.URI)
		if !ok {
			return nil, false, nil
		}
		var bodies []string
		for _, k := range al.Spec.Artifacts.Keys() {
			entry, _ := al.Spec.Artifacts.Get(k)
			for _, fk := range entry.Files.Keys() {
				fb, _ := entry.Files.Get(fk)
				bodies = append(bodies, fb)
			}
		}
		return bodies, true, nil
	}

	return cl.DiscoverTemplateLibraries(LibraryAxisNamed("ArtifactsLibraries"), lookup, consumers, body)
}

// artifactsImportConsumers renders every artifacts-map entry that carries an
// `$import:` target into a consumer in authored directive form. An inline body
// yields no consumer. Author order of the entries is preserved.
func artifactsImportConsumers(uri string, artifacts types.OrderedMap[types.ArtifactEntry]) []TemplateConsumer {
	var out []TemplateConsumer
	for _, k := range artifacts.Keys() {
		entry, _ := artifacts.Get(k)
		out = append(out, importConsumer(uri, entry.Import)...)
	}
	return out
}
