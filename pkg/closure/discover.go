// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import (
	"context"

	"go.yaml.in/yaml/v3"
)

// SchemaConsumer pairs a resolved Component/Feature URI with its schema body
// (a yaml.Node) so DiscoverSchemaLibraries can scan it for cross-document
// SchemaLibrary $ref: references. The caller (pull's pin flow or build's
// drift-verify flow) supplies these from the catalog after ComputeClosure has
// resolved the entity closure.
//
// Kind is the closure kind the consuming entity was collected as (KindComponent,
// KindFeature): two consumers of one URI and different kinds are two consumers,
// each contributing its own version floor to the library's selection.
type SchemaConsumer struct {
	URI    string
	Kind   string
	Schema *yaml.Node
}

// LibraryBodyFunc returns the entry bodies of a resolved SchemaLibrary so its own
// cross-document $ref: references can be descended transitively. The caller
// resolves the body from the catalog (a locally-authored library) or the content
// cache (a remote pinned library, via the lazy library walk's verified Tier-2 read).
// A false found return stops transitive descent into that library (its body is
// not available to scan); err is for a hard read/parse failure.
type LibraryBodyFunc func(lib ResolvedLibrary) (entryBodies []*yaml.Node, found bool, err error)

// TemplateConsumer pairs a consuming entity URI with the one thing a template-axis
// discovery reads from it. Exactly one of the two content fields is populated, and
// which one says how the reference reached the walk rather than what it means.
//
// Every template axis shares this shape, so the per-axis names below are aliases
// rather than separate types: the axis says which surface is being walked, and the
// consumer shape is the same one on all of them.
type TemplateConsumer struct {
	URI string

	// Kind is the closure kind the consuming entity was collected as (KindRecipe,
	// KindFeatureRecipe): two consumers of one URI and different kinds are two
	// consumers, each contributing its own version floor to the library's selection.
	Kind string

	// Template is a template BODY to scan for a field-root `$import:` directive —
	// a library entry's own content during transitive descent, which is a document
	// and has lines.
	Template string

	// Reference is an `$import:` target already read off an authored field, passed
	// exactly as the document parser produced it.
	//
	// It is a field of its own rather than a directive string assembled around the
	// target, because rendering the target back into `$import: <target>` for the
	// body scanner to re-read is a SECOND reading of a value that was already read
	// — and the two readings do not agree on every input. The body scanner stops at
	// the first line break, which a document reader has no reason to do, so a
	// reference the render side resolves is one the scanner can decline to see, and
	// the dependency it carries reaches no lockfile.
	Reference string
}

// templateBody is the shared fixpoint's body type on a template axis: which of the
// two readings applies to this consumer, carried through the frontier so a library
// discovered from an authored reference still has its own body scanned.
type templateBody struct {
	body      string
	reference string
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
// references a library and the content to read those references out of. The
// content type varies by axis — a parsed node on the schema axis, a template body
// or an already-read reference on the others — which is the one thing the shared
// fixpoint is parameterised on.
type discoverConsumer[B any] struct {
	Consumer consumerIdentity
	Body     B
}

// discoverLibraries is the discovery fixpoint, shared by every library axis.
//
// It walks the current frontier into a fresh walk state seeded from the libraries
// already resolved, resolves the whole set so minimum-version selection sees every
// consumer of every library, merges the result back into the axis's section, and
// then builds the next frontier from libraries not yet descended. Re-seeding each
// round is safe: a seeded library the selection still selects keeps the resolution
// it arrived with (resolveLibraries), so a later round neither asks the lookup for
// it again nor changes what the first answer recorded.
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
	ctx context.Context,
	c *Closure,
	axis LibraryAxis,
	lookup Lookup,
	consumers []discoverConsumer[B],
	walkOne func(s *walkState, axis LibraryAxis, body B, consumer consumerIdentity) error,
	body func(lib ResolvedLibrary) ([]B, bool, error),
) error {
	descended := map[string]struct{}{}
	frontier := append([]discoverConsumer[B]{}, consumers...)

	for len(frontier) > 0 {
		state := newWalkState()
		state.seedLibs(axis, axis.Libraries(c))
		for _, dc := range frontier {
			if err := walkOne(state, axis, dc.Body, dc.Consumer); err != nil {
				return err
			}
		}
		if err := state.resolveLibraries(ctx, axis, lookup); err != nil {
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
				// A library consuming another on its own axis is read as the axis's kind.
				frontier = append(frontier, discoverConsumer[B]{Consumer: consumerIdentity{Kind: axis.Kind, URI: lib.URI}, Body: b})
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
func (c *Closure) DiscoverTemplateLibraries(ctx context.Context, axis LibraryAxis, lookup Lookup, consumers []TemplateConsumer, body TemplateBodyFunc) error {
	dcs := make([]discoverConsumer[templateBody], 0, len(consumers))
	for _, tc := range consumers {
		dcs = append(dcs, discoverConsumer[templateBody]{
			Consumer: consumerIdentity{Kind: tc.Kind, URI: tc.URI},
			Body:     templateBody{body: tc.Template, reference: tc.Reference},
		})
	}
	// A discovered library's own entries are bodies whichever way its consumer's
	// reference arrived, so the frontier rebuilt from them carries bodies only.
	bodies := func(lib ResolvedLibrary) ([]templateBody, bool, error) {
		entries, found, err := body(lib)
		if err != nil || !found {
			return nil, found, err
		}
		out := make([]templateBody, 0, len(entries))
		for _, e := range entries {
			out = append(out, templateBody{body: e})
		}
		return out, true, nil
	}
	return discoverLibraries(ctx, c, axis, lookup, dcs,
		func(s *walkState, a LibraryAxis, tb templateBody, consumer consumerIdentity) error {
			if tb.reference != "" {
				return s.walkImportRef(a, tb.reference, consumer)
			}
			return s.walkImportInto(a, tb.body, consumer)
		}, bodies)
}

// DiscoverMappingLibraries discovers the MappingLibrary closure reachable from a
// set of Recipe / FeatureRecipe valuesMapping bodies. See DiscoverTemplateLibraries.
func (c *Closure) DiscoverMappingLibraries(ctx context.Context, lookup Lookup, consumers []MappingConsumer, body MappingBodyFunc) error {
	return c.DiscoverTemplateLibraries(ctx, LibraryAxisNamed("MappingLibraries"), lookup, consumers, body)
}

// DiscoverWorkloadLibraries discovers the WorkloadLibrary closure reachable from a
// set of Recipe / FeatureRecipe workload bodies. See DiscoverTemplateLibraries.
func (c *Closure) DiscoverWorkloadLibraries(ctx context.Context, lookup Lookup, consumers []WorkloadConsumer, body WorkloadBodyFunc) error {
	return c.DiscoverTemplateLibraries(ctx, LibraryAxisNamed("WorkloadLibraries"), lookup, consumers, body)
}

// DiscoverArtifactLibraries discovers the ArtifactLibrary closure reachable from
// a set of Recipe / FeatureRecipe artifacts-map bodies. An artifacts map may
// contribute several consumers from one entity. See DiscoverTemplateLibraries.
func (c *Closure) DiscoverArtifactLibraries(ctx context.Context, lookup Lookup, consumers []ArtifactsConsumer, body ArtifactsBodyFunc) error {
	return c.DiscoverTemplateLibraries(ctx, LibraryAxisNamed("ArtifactLibraries"), lookup, consumers, body)
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
func (c *Closure) DiscoverSchemaLibraries(ctx context.Context, lookup Lookup, consumers []SchemaConsumer, body LibraryBodyFunc) error {
	dcs := make([]discoverConsumer[*yaml.Node], 0, len(consumers))
	for _, sc := range consumers {
		if sc.Schema != nil {
			dcs = append(dcs, discoverConsumer[*yaml.Node]{Consumer: consumerIdentity{Kind: sc.Kind, URI: sc.URI}, Body: sc.Schema})
		}
	}
	return discoverLibraries(ctx, c, LibraryAxisNamed("SchemaLibraries"), lookup, dcs,
		func(s *walkState, _ LibraryAxis, node *yaml.Node, consumer consumerIdentity) error {
			return s.walkSchemaRefsInto(node, consumer)
		}, body)
}
