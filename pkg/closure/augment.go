// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import (
	"context"
	"fmt"
	"sort"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// LibrarySource is the resolved-closure read surface the library-discovery
// producers use to fetch each in-closure entity body by URI. Every read method
// returns a pkg/types value plus a found flag; a false found return means the
// closure does not reach that URI through the source. Implementations resolve
// the body from a locally-authored catalog or from the content cache (a remote
// pinned entity, via the verified lazy library walk); the producers stay
// decoupled from that resolution machinery.
type LibrarySource interface {
	// ClosureComponent and ClosureFeature read the Component or Feature a closure
	// entity reference resolves to. They take the reference whole rather than its
	// URI, because where the walk collected it says which kind it can be, and an
	// implementation that would ask a registry asks only for that kind. They take
	// the caller's context because such a read can leave the process, and a
	// discovery whose caller has withdrawn must not wait on it.
	ClosureComponent(ctx context.Context, ref ResolvedRef) (types.Component, bool)
	ClosureFeature(ctx context.Context, ref ResolvedRef) (types.Feature, bool)
	SchemaLibrary(uri string) (types.SchemaLibrary, bool)
	RecipeByURI(uri string) (types.Recipe, bool)
	FeatureRecipeByURI(uri string) (types.FeatureRecipe, bool)
	MappingLibrary(uri string) (types.MappingLibrary, bool)
	WorkloadLibrary(uri string) (types.WorkloadLibrary, bool)
	ArtifactLibrary(uri string) (types.ArtifactLibrary, bool)
	// HoldResolvedLibrary makes a library the discovery has just resolved on axis
	// readable through the per-kind read above, at the version and content digest
	// its resolution recorded, where the source does not already hold it. A
	// library read from a catalog directory is held already; one the resolution
	// served from the content cache or a registry may be held by nothing else yet,
	// and its body is what the discovery descends next.
	HoldResolvedLibrary(axis LibraryAxis, lib ResolvedLibrary)
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
func AugmentClosure(ctx context.Context, cl *Closure, src LibrarySource, lookup Lookup) error {
	for _, d := range libraryDiscoverers() {
		if err := d.discover(ctx, cl, src, lookup); err != nil {
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
	discover func(context.Context, *Closure, LibrarySource, Lookup) error
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
		{LibraryAxisNamed("ArtifactLibraries"), discoverClosureArtifactLibraries},
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

// importConsumer carries a field's `$import:` target to discovery exactly as the
// document parser produced it. An inline body carries no import and yields none.
//
// The target is passed as the consumer's reference rather than rendered back into
// `$import: <target>` directive text, because the render side reads this same
// field verbatim: one value, one reading, and no room for the two halves of the
// compiler to disagree about which library the entity depends on.
func importConsumer(uri, importTarget string) []TemplateConsumer {
	if importTarget == "" {
		return nil
	}
	return []TemplateConsumer{{URI: uri, Reference: importTarget}}
}

// discoverClosureLibraries is the single producer of the closure's SchemaLibrary
// axis, shared by pull's pin flow and build's drift-verify flow. Both resolve the
// closure through the same lazy lookup, so they compute the identical library
// closure: the lockfile pull writes and the closure build verifies against it
// agree by construction, because pull and build read the same resolved closure
// rather than two independently-built catalog views. It scans every in-closure
// Component's and Feature's schema, and every per-edge contract their dependency
// sockets declare, for cross-document $ref:s, resolves the discovered libraries
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
func discoverClosureLibraries(ctx context.Context, cl *Closure, src LibrarySource, lookup Lookup) error {
	// Closure-scoped consumer scan over the lazy lookup: resolve each closure
	// entity and collect its schema — components first, then features, each sorted
	// by name. This preserves the deterministic consumer order the eager-map scan
	// produced, now reading the resolved closure rather than the bridged catalog.
	// Build's drift-verify flow and pull's pin flow each pass their own lazy
	// lookup, so there is one producer and no fork.
	var compConsumers, featConsumers []SchemaConsumer
	compSeen, featSeen := map[string]struct{}{}, map[string]struct{}{}
	for _, e := range cl.Entities {
		if comp, ok := src.ClosureComponent(ctx, e); ok {
			if _, dup := compSeen[comp.Metadata.Name]; dup {
				continue
			}
			compSeen[comp.Metadata.Name] = struct{}{}
			c := comp
			compConsumers = append(compConsumers, entitySchemaConsumers(c.Metadata.Name, &c.Spec.Schema, c.Spec.UsesSchema)...)
			continue
		}
		if feat, ok := src.ClosureFeature(ctx, e); ok {
			if _, dup := featSeen[feat.Metadata.Name]; dup {
				continue
			}
			featSeen[feat.Metadata.Name] = struct{}{}
			f := feat
			featConsumers = append(featConsumers, entitySchemaConsumers(f.Metadata.Name, &f.Spec.Schema, f.Spec.UsesSchema)...)
		}
	}
	// Stable, so an entity's own contracts keep their authored order behind its pillars.
	sort.SliceStable(compConsumers, func(i, j int) bool { return compConsumers[i].URI < compConsumers[j].URI })
	sort.SliceStable(featConsumers, func(i, j int) bool { return featConsumers[i].URI < featConsumers[j].URI })
	consumers := append(compConsumers, featConsumers...)

	// Transitive body accessor: a discovered library's own entry bodies, for
	// descending its cross-document $ref:s, read through the source at the
	// resolution the discovery just made (heldLibraryBodies).
	axis := LibraryAxisNamed("SchemaLibraries")
	body := heldLibraryBodies(src, axis, func(uri string) ([]*yaml.Node, bool) {
		sl, ok := src.SchemaLibrary(uri)
		if !ok {
			return nil, false
		}
		keys := sl.Spec.Schemas.Keys()
		bodies := make([]*yaml.Node, 0, len(keys))
		for _, k := range keys {
			entry, _ := sl.Spec.Schemas.Get(k)
			node := entry.Content
			bodies = append(bodies, &node)
		}
		return bodies, true
	})

	return cl.DiscoverSchemaLibraries(ctx, lookup, consumers, body)
}

// entitySchemaConsumers lists every schema body one Component or Feature
// declares, as the consumers whose cross-document $ref:s the SchemaLibrary axis
// discovers: its own spec.schema first, then each per-edge contract its
// dependency sockets declare, socket by socket in declared order. A socket's
// contract references a library exactly as a pillar does and is compiled against
// the same library set, so a library only a socket names must be discovered,
// pinned and reported missing here like any other — left out, it would be pinned
// by no lockfile and reported by nothing until the contract failed to compile.
func entitySchemaConsumers(uri string, schema *yaml.Node, sockets types.OrderedMap[types.UsesSchemaEntry]) []SchemaConsumer {
	out := []SchemaConsumer{{URI: uri, Schema: schema}}
	for _, entry := range sockets.All() {
		for _, contract := range entry.ContractNodes() {
			if contract.Kind != 0 {
				out = append(out, SchemaConsumer{URI: uri, Schema: contract})
			}
		}
	}
	return out
}

// heldLibraryBodies is the body accessor every library axis hands the discovery
// fixpoint: it has the source hold the library at the resolution the fixpoint just
// made, then reads that library's entry bodies with read. A library resolved from
// a catalog directory and one served from the content cache or a registry are
// therefore read alike, and a library reached only through another is discovered
// whichever source served that other one: a chain of any depth resolves from a
// registry as it resolves from a catalog directory.
//
// A library the source cannot read even then stops the descent through it.
func heldLibraryBodies[B any](src LibrarySource, axis LibraryAxis, read func(uri string) ([]B, bool)) func(ResolvedLibrary) ([]B, bool, error) {
	return func(lib ResolvedLibrary) ([]B, bool, error) {
		src.HoldResolvedLibrary(axis, lib)
		bodies, ok := read(lib.URI)
		return bodies, ok, nil
	}
}

// discoverClosureMappingLibraries is the single producer of the closure's
// MappingLibrary axis. It scans every in-closure Recipe and FeatureRecipe
// valuesMapping for a field-root `$import:` reference and descends the discovered
// libraries transitively.
//
// A valuesMapping's `$import:` target reaches the walker as the reference itself;
// an inline values template and a local-file import carry no library reference and
// are skipped by the walker's identity gate.
func discoverClosureMappingLibraries(ctx context.Context, cl *Closure, src LibrarySource, lookup Lookup) error {
	consumers := collectTemplateConsumers(cl, src,
		func(uri string, r types.Recipe) []TemplateConsumer {
			return importConsumer(uri, r.Spec.ValuesMapping.Import)
		},
		func(uri string, fr types.FeatureRecipe) []TemplateConsumer {
			return importConsumer(uri, fr.Spec.ValuesMapping.Import)
		})

	axis := LibraryAxisNamed("MappingLibraries")
	body := heldLibraryBodies(src, axis, func(uri string) ([]string, bool) {
		ml, ok := src.MappingLibrary(uri)
		if !ok {
			return nil, false
		}
		bodies := make([]string, 0, ml.Spec.Mappings.Len())
		for _, k := range ml.Spec.Mappings.Keys() {
			entry, _ := ml.Spec.Mappings.Get(k)
			bodies = append(bodies, entry.Body)
		}
		return bodies, true
	})

	return cl.DiscoverTemplateLibraries(ctx, axis, lookup, consumers, body)
}

// discoverClosureWorkloadLibraries is the single producer of the closure's
// WorkloadLibrary axis. It scans every in-closure Recipe and FeatureRecipe workload
// for a field-root `$import:` reference and descends the discovered libraries
// transitively.
//
// A FeatureRecipe workload library reference is refused at envelope validation, so
// an attachment consumer contributes no edge here; scanning it is harmless and
// keeps the producer symmetric with the mapping axis.
func discoverClosureWorkloadLibraries(ctx context.Context, cl *Closure, src LibrarySource, lookup Lookup) error {
	consumers := collectTemplateConsumers(cl, src,
		func(uri string, r types.Recipe) []TemplateConsumer {
			return importConsumer(uri, r.Spec.Workload.Import)
		},
		func(uri string, fr types.FeatureRecipe) []TemplateConsumer {
			return importConsumer(uri, fr.Spec.Workload.Import)
		})

	axis := LibraryAxisNamed("WorkloadLibraries")
	body := heldLibraryBodies(src, axis, func(uri string) ([]string, bool) {
		wl, ok := src.WorkloadLibrary(uri)
		if !ok {
			return nil, false
		}
		bodies := make([]string, 0, wl.Spec.Workloads.Len())
		for _, k := range wl.Spec.Workloads.Keys() {
			entry, _ := wl.Spec.Workloads.Get(k)
			bodies = append(bodies, entry.Body)
		}
		return bodies, true
	})

	return cl.DiscoverTemplateLibraries(ctx, axis, lookup, consumers, body)
}

// discoverClosureArtifactLibraries is the single producer of the closure's
// ArtifactLibrary axis. Unlike the workload and mapping axes — where an entity
// carries a single field — artifacts is a MAP, so one entity may contribute several
// edges, one per entry that imports a bundle. Entry author order is preserved.
func discoverClosureArtifactLibraries(ctx context.Context, cl *Closure, src LibrarySource, lookup Lookup) error {
	consumers := collectTemplateConsumers(cl, src,
		func(uri string, r types.Recipe) []TemplateConsumer {
			// A Recipe's boundary block imports a bundle the way its own artifacts
			// do, so its entries are consumers too, after the Recipe's own.
			return append(artifactsImportConsumers(uri, r.Spec.Artifacts),
				artifactsImportConsumers(uri, r.Spec.Boundary.Artifacts)...)
		},
		func(uri string, fr types.FeatureRecipe) []TemplateConsumer {
			return artifactsImportConsumers(uri, fr.Spec.Artifacts)
		})

	// A discovered bundle's own file bodies, flattened across every bundle entry.
	axis := LibraryAxisNamed("ArtifactLibraries")
	body := heldLibraryBodies(src, axis, func(uri string) ([]string, bool) {
		al, ok := src.ArtifactLibrary(uri)
		if !ok {
			return nil, false
		}
		var bodies []string
		for _, k := range al.Spec.Artifacts.Keys() {
			entry, _ := al.Spec.Artifacts.Get(k)
			for _, fk := range entry.Files.Keys() {
				fb, _ := entry.Files.Get(fk)
				bodies = append(bodies, fb)
			}
		}
		return bodies, true
	})

	return cl.DiscoverTemplateLibraries(ctx, axis, lookup, consumers, body)
}

// artifactsImportConsumers carries every artifacts-map entry's `$import:` target
// to discovery as the reference itself. An inline body yields no consumer. Author
// order of the entries is preserved.
func artifactsImportConsumers(uri string, artifacts types.OrderedMap[types.ArtifactEntry]) []TemplateConsumer {
	var out []TemplateConsumer
	for _, k := range artifacts.Keys() {
		entry, _ := artifacts.Get(k)
		out = append(out, importConsumer(uri, entry.Import)...)
	}
	return out
}
