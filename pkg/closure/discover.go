// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
// then builds the next frontier from the bodies of libraries not yet descended at
// the version they now resolve to. Re-seeding each round is safe: a seeded library
// the selection still selects keeps the resolution it arrived with
// (resolveLibraries), so a later round neither asks the lookup for it again nor
// changes what the first answer recorded.
//
// A library is descended once per version it resolves to: a later round whose
// requirements raise a library's selected version descends the body of the new
// version too, so the references that version makes are discovered. Termination
// is by that visited set on the library's identity and version, of which a catalog
// and a registry hold finitely many, so a genuine library-to-library cycle
// terminates discovery without error. The cycle itself surfaces at reference
// consumption, where the recursive expander detects it; discovery does not
// duplicate that diagnostic.
//
// Once no body is left to descend, the section is settled against the versions
// finally selected (settleLibraryConsumers): a library's references are those its
// selected version's body makes, and a library no longer reached that way, or from
// a consumer outside the axis, leaves the section. A discovery that read no
// library body leaves the section as it found it.
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
	// descended holds, by ResolvedLibrary.Reference, the references each library
	// body the discovery read makes; a body the source could not read makes none.
	descended := map[string][]discoverConsumer[B]{}
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
			if _, seen := descended[lib.Reference()]; seen {
				continue
			}
			bodies, found, err := body(lib)
			if err != nil {
				return err
			}
			var made []discoverConsumer[B]
			if found {
				for _, b := range bodies {
					// A library consuming another on its own axis is read as the axis's kind.
					made = append(made, discoverConsumer[B]{Consumer: consumerIdentity{Kind: axis.Kind, URI: lib.URI}, Body: b})
				}
			}
			descended[lib.Reference()] = made
			frontier = append(frontier, made...)
		}
	}
	if len(descended) == 0 {
		return nil
	}
	return settleLibraryConsumers(c, axis, descended, walkOne)
}

// settleLibraryConsumers settles an axis's section against the versions its
// libraries finally resolve to, once the discovery has descended every body.
//
// A round that raised a library's selected version kept the references the
// earlier version's body made: they were seeded into every later round as that
// library's consumer rows. Here every consumer row a library body recorded is
// replaced by the rows the body of the library's selected version records
// (descended), and every library then left unreached leaves the section — one
// reached neither from a consumer outside the axis nor from a library that is
// itself reached (pruneUnreachedLibraries). Resolutions are kept as they are: no
// lookup is asked here.
//
// A library's body records its rows under the library's name as consumer, as the
// lockfile records them, so a row is told apart from an entity's by that name
// alone.
func settleLibraryConsumers[B any](
	c *Closure,
	axis LibraryAxis,
	descended map[string][]discoverConsumer[B],
	walkOne func(s *walkState, axis LibraryAxis, body B, consumer consumerIdentity) error,
) error {
	read := map[string]struct{}{}
	for _, lib := range axis.Libraries(c) {
		if _, ok := descended[lib.Reference()]; ok {
			read[lib.URI] = struct{}{}
		}
	}
	state := newWalkState()
	state.seedLibs(axis, axis.Libraries(c))
	pending := axis.pendingLibs(state)
	// A library seeded with no consumer at all carries nothing to settle it by, so
	// it stays as it arrived.
	unreferenced := map[string]struct{}{}
	for uri, lib := range pending {
		if len(lib.fragmentsByCons) == 0 {
			unreferenced[uri] = struct{}{}
		}
		for consumer := range lib.fragmentsByCons {
			if _, fromBody := read[consumer.URI]; fromBody {
				delete(lib.fragmentsByCons, consumer)
				delete(lib.versionByConsumer, consumer)
			}
		}
	}
	for _, lib := range axis.Libraries(c) {
		for _, dc := range descended[lib.Reference()] {
			if err := walkOne(state, axis, dc.Body, dc.Consumer); err != nil {
				return err
			}
		}
	}
	state.pruneUnreachedLibraries(axis, unreferenced)
	axis.SetLibraries(c, state.materialiseLibs(axis))
	canonicaliseClosure(c)
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
