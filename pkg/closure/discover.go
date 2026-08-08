// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import "go.yaml.in/yaml/v3"

// SchemaConsumer pairs a resolved Component/Feature URI with its schema body
// (a yaml.Node) so DiscoverSchemaLibraries can scan it for cross-document
// SchemaLibrary $ref: references. The caller (pull's pin flow or build's
// drift-verify flow) supplies these from the catalog after ComputeClosure has
// resolved the entity closure.
type SchemaConsumer struct {
	URI    string
	Schema *yaml.Node
}

// LibraryBodyFunc returns the entry bodies of a resolved SchemaLibrary so its own
// cross-document $ref: references can be descended transitively. The caller
// resolves the body from the catalog (a locally-authored library) or the content
// cache (a remote pinned library, via the lazy library walk's verified Tier-2 read).
// A false found return stops transitive descent into that library (its body is
// not available to scan); err is for a hard read/parse failure.
type LibraryBodyFunc func(lib ResolvedLibrary) (entryBodies []*yaml.Node, found bool, err error)

// TemplateConsumer pairs a consuming entity URI with a template body so a
// template-axis discovery can scan it for a field-root `$import:` reference. For a
// Recipe or FeatureRecipe the Template is the axis's own field body in
// `$import: <ref>` directive form; during transitive descent it is a library
// entry's own body, which may itself carry a field-root `$import:`.
//
// Every template axis shares this shape, so the per-axis names below are aliases
// rather than separate types: the axis says which surface is being walked, and the
// consumer shape is the same one on all of them.
type TemplateConsumer struct {
	URI      string
	Template string
}

// TemplateBodyFunc returns the entry bodies of a resolved library on a template
// axis so its own cross-library `$import:` references can be descended
// transitively. A false found return stops transitive descent into that library;
// err is for a hard read/parse failure.
type TemplateBodyFunc func(lib ResolvedLibrary) (entryBodies []string, found bool, err error)

// The per-axis consumer and body-function names are aliases of the shared template
// shapes. They read at a call site as the axis being walked while remaining one
// type, so a body accessor written for one axis cannot drift in shape from another.
type (
	MappingConsumer   = TemplateConsumer
	WorkloadConsumer  = TemplateConsumer
	ArtifactsConsumer = TemplateConsumer

	MappingBodyFunc   = TemplateBodyFunc
	WorkloadBodyFunc  = TemplateBodyFunc
	ArtifactsBodyFunc = TemplateBodyFunc
)

// discoverConsumer is the driver's uniform view of a consumer: the URI that
// references a library and the body to scan for those references. The body type
// varies by axis — a parsed node on the schema axis, a template string on the
// others — which is the one thing the shared fixpoint is parameterised on.
type discoverConsumer[B any] struct {
	URI  string
	Body B
}

// discoverLibraries is the discovery fixpoint, shared by every library axis.
//
// It walks the current frontier into a fresh walk state seeded from the libraries
// already resolved, resolves the whole set so minimum-version selection sees every
// consumer of every library, merges the result back into the axis's section, and
// then builds the next frontier from libraries not yet descended. Re-resolution is
// idempotent, so re-seeding each round is safe.
//
// Termination is by a visited-set on the canonical library identity, so each
// library's body is descended exactly once and a genuine library-to-library cycle
// terminates discovery without error. The cycle itself surfaces at reference
// consumption, where the recursive expander detects it; discovery does not
// duplicate that diagnostic.
//
// This is the single producer of every library axis: a pin flow and a verify flow
// calling it with the same inputs compute the identical closure, so the lockfile
// one writes and the closure the other checks cannot diverge.
func discoverLibraries[B any](
	c *Closure,
	axis LibraryAxis,
	lookup Lookup,
	consumers []discoverConsumer[B],
	walkOne func(s *walkState, axis LibraryAxis, body B, consumerURI string) error,
	body func(lib ResolvedLibrary) ([]B, bool, error),
) error {
	descended := map[string]struct{}{}
	frontier := append([]discoverConsumer[B]{}, consumers...)

	for len(frontier) > 0 {
		state := newWalkState()
		state.seedLibs(axis, axis.Libraries(c))
		for _, dc := range frontier {
			if err := walkOne(state, axis, dc.Body, dc.URI); err != nil {
				return err
			}
		}
		if err := state.resolveLibraries(axis, lookup); err != nil {
			return err
		}
		axis.SetLibraries(c, state.materialiseLibs(axis))
		canonicaliseClosure(c)

		frontier = frontier[:0]
		for _, lib := range axis.Libraries(c) {
			if _, seen := descended[lib.URI]; seen {
				continue
			}
			descended[lib.URI] = struct{}{}
			bodies, found, err := body(lib)
			if err != nil {
				return err
			}
			if !found {
				continue
			}
			for _, b := range bodies {
				frontier = append(frontier, discoverConsumer[B]{URI: lib.URI, Body: b})
			}
		}
	}
	return nil
}

// DiscoverTemplateLibraries discovers the library closure reachable on any axis
// whose references are carried as a field-root `$import:` in a template body,
// resolves each discovered library, and descends it transitively to a fixpoint,
// merging the result into that axis's section.
//
// The axis is a parameter rather than baked into the function name, so an axis
// added to the declared set is discovered here without a new entry point.
func (c *Closure) DiscoverTemplateLibraries(axis LibraryAxis, lookup Lookup, consumers []TemplateConsumer, body TemplateBodyFunc) error {
	dcs := make([]discoverConsumer[string], 0, len(consumers))
	for _, tc := range consumers {
		dcs = append(dcs, discoverConsumer[string]{URI: tc.URI, Body: tc.Template})
	}
	return discoverLibraries(c, axis, lookup, dcs,
		func(s *walkState, a LibraryAxis, tmpl, consumerURI string) error {
			return s.walkImportInto(a, tmpl, consumerURI)
		}, body)
}

// DiscoverMappingLibraries discovers the MappingLibrary closure reachable from a
// set of Recipe / FeatureRecipe valuesMapping bodies. See DiscoverTemplateLibraries.
func (c *Closure) DiscoverMappingLibraries(lookup Lookup, consumers []MappingConsumer, body MappingBodyFunc) error {
	return c.DiscoverTemplateLibraries(LibraryAxisNamed("MappingLibraries"), lookup, consumers, body)
}

// DiscoverWorkloadLibraries discovers the WorkloadLibrary closure reachable from a
// set of Recipe / FeatureRecipe workload bodies. See DiscoverTemplateLibraries.
func (c *Closure) DiscoverWorkloadLibraries(lookup Lookup, consumers []WorkloadConsumer, body WorkloadBodyFunc) error {
	return c.DiscoverTemplateLibraries(LibraryAxisNamed("WorkloadLibraries"), lookup, consumers, body)
}

// DiscoverArtifactsLibraries discovers the ArtifactsLibrary closure reachable from
// a set of Recipe / FeatureRecipe artifacts-map bodies. An artifacts map may
// contribute several consumers from one entity. See DiscoverTemplateLibraries.
func (c *Closure) DiscoverArtifactsLibraries(lookup Lookup, consumers []ArtifactsConsumer, body ArtifactsBodyFunc) error {
	return c.DiscoverTemplateLibraries(LibraryAxisNamed("ArtifactsLibraries"), lookup, consumers, body)
}

// DiscoverSchemaLibraries discovers the SchemaLibrary closure reachable from a set
// of Component / Feature schema bodies via cross-document `$ref:` references.
//
// It runs the same fixpoint as the template axes and differs only in what a
// consumer body is and how it is scanned: a parsed document walked for `$ref:`
// values at any depth, rather than a template string whose first token is read.
// That difference is the reference grammar itself, so it is a parameter of the
// shared fixpoint rather than a second copy of it.
//
// A consumer carrying no schema contributes nothing and is dropped before the walk.
func (c *Closure) DiscoverSchemaLibraries(lookup Lookup, consumers []SchemaConsumer, body LibraryBodyFunc) error {
	dcs := make([]discoverConsumer[*yaml.Node], 0, len(consumers))
	for _, sc := range consumers {
		if sc.Schema != nil {
			dcs = append(dcs, discoverConsumer[*yaml.Node]{URI: sc.URI, Body: sc.Schema})
		}
	}
	return discoverLibraries(c, LibraryAxisNamed("SchemaLibraries"), lookup, dcs,
		func(s *walkState, _ LibraryAxis, node *yaml.Node, consumerURI string) error {
			return s.walkSchemaRefsInto(node, consumerURI)
		}, body)
}
