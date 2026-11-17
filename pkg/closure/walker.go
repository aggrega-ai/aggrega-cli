// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package closure

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// ComputeClosure walks a Release and its associated Manifest,
// Aggregate, and workspace config, producing the full Closure the
// lockfile writer consumes.
//
// The walker never makes network calls. Every URI resolution is a
// method invocation on lookup. The walker is purely structural: it
// enumerates reference sites, normalises URIs, applies minimum
// version selection over library dependency graphs, and aggregates
// the per-axis closure into the typed Closure return.
//
// Returns ErrMVSConflict on library-version disagreement;
// ErrUnresolvableURI on missing-from-Lookup catalog entities;
// ErrInvalidLibraryRef on malformed $ref: / $import: URIs.
//
// The result slices are sorted by URI (lexicographic ascending);
// LibraryConsumer lists sort by ConsumerURI; Fragments and
// ReferencedFrom slices sort lexicographically. Two independent
// walks of the same input produce structurally-identical Closure
// values.
//
// The aggregate arrives as a [types.NormalizedAggregate] so the walk's
// completeness is a compile-time fact rather than a convention inherited from
// callers: components live under subsystems, the descent is subsystems-only, and
// no caller can hand this package a shape whose components the walk would miss.
// The zero value stands for "no aggregate" and is walked as a no-op.
// ctx is the caller's own. The walk itself touches nothing outside the process, but
// the resolution pass it ends with does: a reference that misses the local catalog
// and the content cache is fetched from a registry, so a caller that gives up stops
// those fetches rather than waiting them out.
func ComputeClosure(
	ctx context.Context,
	release *types.Release,
	manifest *types.Manifest,
	aggregate types.NormalizedAggregate,
	workspaceCfg workspaceconfig.Config,
	lookup Lookup,
) (Closure, error) {
	in := inputs{
		release:      release,
		manifest:     manifest,
		aggregate:    aggregate,
		workspaceCfg: workspaceCfg,
		lookup:       lookup,
	}

	state := newWalkState()
	if err := state.walk(in); err != nil {
		return Closure{}, err
	}
	if err := state.resolveAll(ctx, in.lookup); err != nil {
		return Closure{}, err
	}

	closure := state.materialise()
	canonicaliseClosure(&closure)
	return closure, nil
}

// walkState aggregates the per-axis collection state as the
// recursive walkers descend through the release / manifest /
// aggregate trees.
type walkState struct {
	// entities holds one accumulator per entity, keyed by the kind it was
	// collected as and its reference, so two references of one name collected as
	// different kinds — the Release's own Aggregate and Manifest, a Component and a
	// Recipe — are two entities rather than one.
	entities map[EntityKey]*pendingRef
	// libs holds one accumulator per library axis, keyed by the axis's Closure
	// section name. Keying it rather than declaring a field per axis is what keeps
	// the walk axis-agnostic: an axis added to the declared set accumulates here
	// without this type changing.
	libs map[string]map[string]*pendingLibrary
}

// pendingRef collects the per-entity accumulating data the walker
// builds during the descent: a deduplicated set of authoring-site
// paths plus, once the resolveAll pass has run, the looked-up
// Resolution metadata.
type pendingRef struct {
	referencedFrom map[string]struct{}
	resolution     *Resolution
	// kindHint records the hint the entity's collection site carries (e.g. a
	// Manifest mapping value's position records "recipe"). Every hint for one
	// entity names its kind, so the hints for one entity differ only between the
	// two Aggregate hints; see addEntityKind.
	kindHint string
	// pinnedSocket marks the Plugin entity of a workspace-socket attachment the
	// lockfile pins, which resolves through ResolvePinnedSocketEntity rather than
	// as a reference read from catalog content.
	pinnedSocket bool
}

// pendingLibrary collects per-library-URI consumer→fragment
// mappings the walker builds; the version constraints contributed
// by each consumer feed the minimum version selection pass.
// Once the resolveAll pass has run, resolution carries the
// looked-up LibraryResolution metadata.
//
// Both maps are keyed by the consumer's identity, its kind as well as its URI, so a
// Recipe and a FeatureRecipe of one name that consume one library each contribute
// their own version floor: keyed by the URI alone, the later of the two would
// overwrite the earlier's floor and the selection could settle below a floor one of
// them requires. The lockfile records a consumer by its URI alone, so the
// identities are merged back to one row per URI when the library is materialised.
type pendingLibrary struct {
	versionByConsumer map[consumerIdentity]string              // consumer → requested version (empty when unpinned)
	fragmentsByCons   map[consumerIdentity]map[string]struct{} // consumer → set of fragments
	resolution        *LibraryResolution
}

// consumerIdentity identifies a library's consumer: the kind it was read as and its
// URI. An entity consumer's kind is the closure kind it was collected as
// (KindComponent, KindFeature, KindRecipe, KindFeatureRecipe); a library consuming
// another on its own axis carries the axis's kind (LibraryAxis.Kind); a consumer
// whose kind its caller did not say — one seeded back from a materialised library,
// whose row records none — carries none.
type consumerIdentity struct {
	Kind string
	URI  string
}

// consumerVersionLabels returns versionByConsumer keyed by a label per consumer for
// the selection to order and name consumers by: the URI, and — only where two
// consumers share a URI — the URI with the kind beside it, so the two stay two
// requirements and a conflict between them names both.
func consumerVersionLabels(versionByConsumer map[consumerIdentity]string) map[string]string {
	perURI := map[string]int{}
	for c := range versionByConsumer {
		perURI[c.URI]++
	}
	out := make(map[string]string, len(versionByConsumer))
	for c, version := range versionByConsumer {
		label := c.URI
		if perURI[c.URI] > 1 && c.Kind != "" {
			label = c.URI + " (" + c.Kind + ")"
		}
		out[label] = version
	}
	return out
}

func newWalkState() *walkState {
	s := &walkState{
		entities: map[EntityKey]*pendingRef{},
		libs:     make(map[string]map[string]*pendingLibrary, len(libraryAxes)),
	}
	for _, a := range libraryAxes {
		s.libs[a.Axis] = map[string]*pendingLibrary{}
	}
	return s
}

// walk runs every per-axis sub-walker over the inputs, populating
// the pending maps. The walk is purely structural; lookup is
// invoked only at the resolveAll stage.
func (s *walkState) walk(in inputs) error {
	if in.release == nil {
		return nil
	}

	if in.release.Spec.Aggregate != "" {
		s.addEntityKind(in.release.Spec.Aggregate, "release.spec.aggregate", ReleaseAggregateKindHint)
	}
	if in.release.Spec.Manifest != "" {
		s.addEntityKind(in.release.Spec.Manifest, "release.spec.manifest", KindManifest)
	}

	s.walkAggregate(in.aggregate, "aggregate")

	if in.manifest != nil {
		s.walkManifest(in.manifest)
	}

	s.walkReleaseAttachments(in.release)
	s.walkWorkspacePlugins(in.workspaceCfg)

	return nil
}

// walkAggregate descends Aggregate.Spec.{Subsystems, Aggregates} recursively,
// recording every type URI it touches.
//
// There is no root-components pass. A component authored at the top-level
// `.spec.components` surface reaches this walk inside the synthesized default
// subsystem, because the parameter is a [types.NormalizedAggregate] — a value
// obtainable only from [types.Normalize], which folds that surface into the
// default and empties the root map — and the authoring shape that declares both
// a root components map and subsystems is rejected at the document-envelope gate
// before any Aggregate is parsed. The type and that gate together are what make
// a subsystems-only descent complete; do not reintroduce a root pass, which
// would imply to a reader that the shape is reachable.
func (s *walkState) walkAggregate(na types.NormalizedAggregate, path string) {
	agg := na.Aggregate()
	if agg == nil {
		return
	}
	for name, sub := range agg.Spec.Aggregates.All() {
		subPath := fmt.Sprintf("%s.spec.aggregates.%s.type", path, name)
		if sub.Type != "" {
			// Mark the sub-aggregate so DescendSubAggregates can find the
			// aggregate entities to load and re-walk for their transitive
			// closure. The hint is not serialised to the lockfile.
			s.addEntityKind(sub.Type, subPath, KindAggregate)
		}
	}
	// Every subsystem's components and its own scope-wide attachments are recorded;
	// the authoring-site path accumulated per level is what a reference records as
	// its origin. No visitor callback fails, so the walk cannot.
	//
	// The subsystem normalization synthesized to hold the top-level components
	// contributes no level: it is not a key the document holds, so a component
	// written at the top level records the path its author wrote,
	// <path>.spec.components.<name>. It keys on the provenance flag rather than the
	// subsystem's name, so a subsystem an author declares under the same name keeps
	// its own level.
	_ = types.WalkInstanceTree(agg.Spec.Subsystems, types.SubsystemTree(), path+".spec", struct{}{},
		types.InstanceTreeVisitor[types.Subsystem, types.ComponentInstance, struct{}]{
			Path: func(parentPath, name string, synthesized bool) string {
				if synthesized {
					return parentPath
				}
				return parentPath + ".subsystems." + name
			},
			Visit: func(ci types.ComponentInstance, name, subPath string, _ bool, _ struct{}) error {
				if ci.Type != "" {
					s.addEntityKind(ci.Type, fmt.Sprintf("%s.components.%s.type", subPath, name), KindComponent)
				}
				s.walkComponentInstanceFeatures(ci, fmt.Sprintf("%s.components.%s", subPath, name))
				return nil
			},
			Leave: func(sys types.Subsystem, _, subPath string, _ struct{}) {
				for name, fi := range sys.Features.All() {
					if fi.Type != "" {
						s.addEntityKind(fi.Type, fmt.Sprintf("%s.features.%s.type", subPath, name), KindFeature)
					}
				}
			},
		})
}

// walkComponentInstanceFeatures collects every Feature URI a component instance
// attaches: the ones its own Features map declares, and the ones its connections
// carry. Every attachment family is declared in one of the two, so one walk
// reaches them all — a credential attachment's type resolves to a Feature entity
// like any other, and its registry location is feature/<name>.
//
// An attachment a connection carries is lowered onto this component and realized
// through the Manifest like one of its own, so the Feature it names is pinned like
// one: acquired at pull, recorded with its version and content digest, and held
// to that row by every build. The set is read from
// [types.ComponentInstance.ConnectionAttachments], the enumeration the lowering
// reads, so the build cannot lower an attachment whose Feature this walk missed.
func (s *walkState) walkComponentInstanceFeatures(ci types.ComponentInstance, path string) {
	for name, fi := range ci.Features.All() {
		if fi.Type != "" {
			s.addEntityKind(fi.Type, fmt.Sprintf("%s.features.%s.type", path, name), KindFeature)
		}
	}
	for conn, att := range ci.ConnectionAttachments() {
		s.addConnectionAttachment(conn, att.Type, path)
	}
}

// addConnectionAttachment records the Feature type one connection attachment
// names, under the path its author wrote it at: the component's own path, then the
// connection — its socket and alias — and the slot the attachment fills. An entry
// naming no type adds nothing, as an attachment written without one does
// elsewhere: on the Release it is the sizing for an attachment the Aggregate
// declares on the same connection, whose type is recorded where the Aggregate
// writes it.
func (s *walkState) addConnectionAttachment(conn types.AttachmentConnection, featureType, componentPath string) {
	if featureType == "" {
		return
	}
	s.addEntityKind(featureType,
		fmt.Sprintf("%s.uses.%s.%s.features.%s.type", componentPath, conn.Socket, conn.Alias, conn.Attachment), KindFeature)
}

// walkManifest descends Manifest.Spec.Mappings.{Components,
// Features}. The Components mapping carries
// component-type → Recipe URIs; the Features mapping carries
// feature-type → FeatureRecipe URIs.
func (s *walkState) walkManifest(m *types.Manifest) {
	if m == nil {
		return
	}
	if m.Spec.Extends != "" {
		s.addEntityKind(m.Spec.Extends, "manifest.spec.extends", KindManifest)
	}
	s.walkMapping(m.Spec.Mappings.Components, "components", KindComponent, KindRecipe)
	s.walkMapping(m.Spec.Mappings.Features, "features", KindFeature, KindFeatureRecipe)
}

// walkMapping records every realization one mapping binds, in every tier it
// carries, under the authoring path of the entry that binds it: the type tier's
// component-type → Recipe (or feature-type → FeatureRecipe) entries and the name
// tier's instance-name → Recipe (or FeatureRecipe) overrides alike.
//
// The tiers are read through [types.MappingTier.Tiers], the one statement of
// which tiers a mapping carries, which is also what the lookup that picks one
// instance's realization reads. A realization bound by instance name is
// therefore pinned exactly as one bound by type, and a build verifies it against
// its pin the same way: the build re-runs this walk and holds the lockfile to it,
// so a lockfile without the row is refused as drift. Reading a tier here from its
// field instead would let the lookup return a realization no lockfile row covers.
//
// A type-tier key is the developer-declared Component or Feature reference, so it
// is recorded as one; a name-tier key is an instance name, which is no entity. A
// value is the Ops-side realization, and its position — a components-mapping
// value is a Recipe, a features-mapping value a FeatureRecipe — is authoritative
// for source-descent, so the kind is recorded and a short-form reference is
// recognised without a "/recipe/" path segment.
func (s *walkState) walkMapping(mapping *types.MappingTier, section, keyKind, valueKind string) {
	for _, tier := range mapping.Tiers() {
		for key, uri := range tier.Entries.All() {
			site := fmt.Sprintf("manifest.spec.mappings.%s.%s.%s", section, tier.Key, key)
			if !tier.ByInstanceName {
				s.addEntityKind(key, site, keyKind)
			}
			if uri != "" {
				s.addEntityKind(uri, site, valueKind)
			}
		}
	}
}

// walkReleaseAttachments descends Release.Spec.Boundaries and every boundary
// nested inside them, collecting the type URIs of the Feature attachments declared
// on the boundary itself and on each component the boundary routes — the
// component entry's own attachments and the ones it puts on the component's
// connections, read from [types.BoundaryComponent.ConnectionAttachments], the
// enumeration the build's collection of them reads. An attachment only the
// Release makes on a connection therefore pins its Feature here. A Feature
// attached at any nesting depth is collected like one attached at the top. A
// credential is a Feature carrying the feature.aggrega.ai/kind: secret label, so it
// is collected through the same walk as any other attachment.
//
// The descent is the shared boundary-tree walk, so the boundaries reached here are
// the same ones every other consumer of that tree reaches. The authoring path
// recorded against each reference spells out the full chain of boundaries leading to
// it, so a pinned entity stays traceable to the position in the Release that asked
// for it.
//
// A boundary also holds the components it routes in from a sub-module through
// [types.Boundary.Aggregates], and those carry attachments of their own. They are
// components of the boundary like any other, so the shared walk reaches them and a
// Feature attached through a routing entry is pinned here. Each is recorded under
// the key path the author actually wrote — through the routing entry, under the
// component's local name inside its module — so the reference stays traceable to
// the line that asked for it rather than to a spelling absent from the document.
func (s *walkState) walkReleaseAttachments(r *types.Release) {
	if r == nil {
		return
	}
	// The accumulator carries the containing boundary so a component's authoring
	// path can be read from the axis that declares it. No visitor callback fails,
	// so the walk cannot.
	_ = types.WalkInstanceTree(r.Spec.Boundaries, types.BoundaryTree(), "release.spec", types.Boundary{},
		types.InstanceTreeVisitor[types.Boundary, types.BoundaryComponent, types.Boundary]{
			Path: func(parentPath, name string, _ bool) string {
				return parentPath + ".boundaries." + name
			},
			Enter: func(b types.Boundary, _, _ string, _ types.Boundary) types.Boundary {
				return b
			},
			Visit: func(bc types.BoundaryComponent, name, path string, _ bool, scope types.Boundary) error {
				componentPath := fmt.Sprintf("%s.%s", path, strings.Join(types.BoundaryComponentLocator(scope, name), "."))
				s.addBoundaryFeatures(bc.Features, componentPath)
				for conn, att := range bc.ConnectionAttachments() {
					s.addConnectionAttachment(conn, att.Type, componentPath)
				}
				return nil
			},
			Leave: func(b types.Boundary, _, path string, _ types.Boundary) {
				s.addBoundaryFeatures(b.Features, path)
			},
		})
}

// addBoundaryFeatures records every non-empty Feature type URI in one attachment
// map, under the authoring path of the site that declares it. A boundary and a
// component the boundary routes carry the same attachment shape, so one accumulator
// serves both and neither site can drift from the other.
func (s *walkState) addBoundaryFeatures(features types.OrderedMap[types.BoundaryFeature], path string) {
	for name, bf := range features.All() {
		if bf.Type != "" {
			s.addEntityKind(bf.Type, fmt.Sprintf("%s.features.%s.type", path, name), KindFeature)
		}
	}
}

// walkWorkspacePlugins collects the URI from the single output-shaping workspace
// socket — the mutator, whose plugin rewrites the IR and therefore influences the
// rendered output bytes. That socket URI is a Plugin ENTITY URI; the entity's .wasm
// artifact reference is derived from its bare identity. The walk records the entity
// on the catalog-entity axis (so ComputeClosure resolves and pins the entity,
// halting loudly on a missing one); the entity's .wasm artifact is pinned at pull
// time by the pipeline-plugin descent, which descends every plugin-kinded entity to
// its .wasm artifact.
//
// The other workspace sockets are resolved at their point of use rather than pinned
// in the closure: the policy socket renders an accept/reject verdict without
// altering the output, and the linter and importer sockets act on authoring input.
// None of them shape the rendered bytes the closure exists to make reproducible, so
// none belongs on the pinned axis.
//
// Each entity recorded here resolves through ResolvePinnedSocketEntity, so a version
// only the compiler binary carries — its own release's — resolves from the binary,
// as the same reference attached at a verdict role does where the build loads it.
func (s *walkState) walkWorkspacePlugins(cfg workspaceconfig.Config) {
	record := func(uri, refPath string) {
		if uri == "" {
			return
		}
		s.addEntityKind(uri, refPath, KindPlugin).pinnedSocket = true
	}
	for _, p := range cfg.Plugins.TransformGovernanceRefs() {
		record(p.URI, ".aggrega-config.yaml.plugins.governance")
	}
}

// WalkSchemaRefs scans a yaml.Node tree for $ref: keys whose
// values match the canonical library-reference shape and records
// each consumer→fragment edge under the supplied consumer URI.
// Public so that integration code can invoke it per resolved
// Component / Feature schema body without re-entering the
// package's private state machinery.
//
// Schemas that contain no $ref: keys are silent no-ops.
// In-document anchors ("#/$defs/X"), host-bearing "oci://…"
// references (rejected at the cross-document expander), and other
// non-library shapes are silently ignored as out-of-scope: the
// walker tracks only host-less cross-library references in the
// identity form `<Library>[@<version>]#/schemas/<Entry>` (no host).
// A $ref: matching that identity shape but otherwise malformed
// returns a wrapped ErrInvalidLibraryRef naming the offending string.
//
// Usage discipline: WalkSchemaRefs is designed for incremental
// population during a single closure walk — typically called
// after ComputeClosure's first pass has resolved Component /
// Feature entities and integration code descends into each
// resolved entity's schema body. Calling repeatedly AFTER a
// library's MVS-resolved version has already been recorded (via
// a prior ComputeClosure + Lookup pass) re-blends the per-consumer
// version constraints in a non-obvious way: each existing
// consumer's stored version becomes the previously-resolved
// version rather than its original pin, so MVS cannot detect a
// fresh major-version disagreement against a new consumer's
// different pin. A use case that requires post-resolution
// re-walking should re-run ComputeClosure (or a dedicated
// re-resolve helper) rather than chain WalkSchemaRefs calls
// against an already-resolved closure.
func (c *Closure) WalkSchemaRefs(node *yaml.Node, consumerURI string) error {
	axis := LibraryAxisNamed("SchemaLibraries")
	state := newWalkState()
	// Seed the state with any libraries already present in the
	// closure so the post-call materialise step preserves them.
	state.seedLibs(axis, axis.Libraries(c))
	if err := state.walkSchemaRefsInto(node, consumerIdentity{URI: consumerURI}); err != nil {
		return err
	}
	axis.SetLibraries(c, state.materialiseLibs(axis))
	canonicaliseClosure(c)
	return nil
}

// walkSchemaRefsInto is the internal yaml.Node walker invoked
// during the closure build. It descends the tree, harvests every
// $ref: child whose value matches the library-reference shape,
// and records the (library, version, fragment, consumer) edge.
func (s *walkState) walkSchemaRefsInto(node *yaml.Node, consumer consumerIdentity) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			if err := s.walkSchemaRefsInto(child, consumer); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valNode := node.Content[i+1]
			if keyNode.Value == "$ref" && valNode.Kind == yaml.ScalarNode {
				if LibraryAxisNamed("SchemaLibraries").IsRef(valNode.Value) {
					if err := s.recordSchemaRef(valNode.Value, consumer); err != nil {
						return err
					}
				}
				continue
			}
			if err := s.walkSchemaRefsInto(valNode, consumer); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			if err := s.walkSchemaRefsInto(child, consumer); err != nil {
				return err
			}
		}
	}
	return nil
}

// recordSchemaRef parses a host-less identity $ref: value
// (`<Library>[@<version>]#/schemas/<Entry>`) and records the resulting library
// name + consumer + fragment edge under the schema-library pending map. The
// optional `@<version>` flows in as a minimum-version floor. Callers
// gate on isIdentityLibraryRef, so a non-identity value here is malformed and
// surfaces as ErrInvalidLibraryRef.
func (s *walkState) recordSchemaRef(refValue string, consumer consumerIdentity) error {
	axis := LibraryAxisNamed("SchemaLibraries")
	libName, version, fragment, err := axis.ParseRef(refValue)
	if err != nil {
		return err
	}
	s.addLibrary(axis, libName, version, consumer, formatFragmentRef(fragment))
	return nil
}

// WalkMappingImport inspects a valuesMapping template body for an
// $import: declaration at the field-root position (per the
// file-import vocabulary spec: $import: is whole-content
// replacement at the field root only; never embedded). If the
// first non-whitespace token of the body is a host-less identity
// MappingLibrary reference `$import: <Library>[@<version>]#/mappings/<Entry>`,
// the reference is parsed and recorded as a mapping-library edge
// under the consumer URI.
//
// A local-file import (mode 2, no `#/mappings/` fragment) and the
// host-bearing `oci://…` form are not cross-library references and are
// silently ignored here — the file import is inlined at its consumption
// site and the host form is rejected there. Commented-out or
// deeper-embedded $import: patterns are likewise ignored — the
// schema-validation layer rejects them at parse time; the walker can
// safely scan only the first token.
func (c *Closure) WalkMappingImport(template, consumerURI string) error {
	axis := LibraryAxisNamed("MappingLibraries")
	state := newWalkState()
	state.seedLibs(axis, axis.Libraries(c))
	if err := state.walkImportInto(axis, template, consumerIdentity{URI: consumerURI}); err != nil {
		return err
	}
	axis.SetLibraries(c, state.materialiseLibs(axis))
	canonicaliseClosure(c)
	return nil
}

// walkImportInto lifts a field-root `$import:` reference out of a template BODY
// on the given axis and records the library edge it carries. Per the file-import
// vocabulary, `$import:` is whole-content replacement at the field root only and is
// never embedded, so scanning the first token is sufficient.
//
// It is for a body — a library entry's own content during transitive descent, which
// is a document and has lines. A reference a consuming entity's own field carries
// was read off that field when the document was parsed, so it is recorded through
// walkImportRef directly and never rendered back into directive text to be read a
// second time under these rules.
func (s *walkState) walkImportInto(axis LibraryAxis, template string, consumer consumerIdentity) error {
	trimmed := strings.TrimSpace(template)
	if !strings.HasPrefix(trimmed, "$import:") {
		return nil
	}
	// Take the rest of the first line as the reference value.
	rest := strings.TrimPrefix(trimmed, "$import:")
	if newline := strings.IndexByte(rest, '\n'); newline >= 0 {
		rest = rest[:newline]
	}
	return s.walkImportRef(axis, strings.TrimSpace(rest), consumer)
}

// walkImportRef records the library edge an `$import:` reference carries on the
// given axis. It is the single reading of a reference into an edge: the authored
// path reaches it with the value the document parser produced, and the transitive
// path reaches it with the value walkImportInto lifted out of a library body.
//
// A local-file import (no fragment) and the host-bearing `oci://…` form are not
// cross-library references and are ignored here: the file import is inlined at its
// consumption site and the host form is rejected there.
func (s *walkState) walkImportRef(axis LibraryAxis, ref string, consumer consumerIdentity) error {
	if ref == "" || !axis.IsRef(ref) {
		return nil
	}
	libName, version, fragment, err := axis.ParseRef(ref)
	if err != nil {
		return err
	}
	s.addLibrary(axis, libName, version, consumer, formatFragmentRef(fragment))
	return nil
}

// resolveAll iterates the pending entity / library maps and invokes
// the Lookup interface per URI. NotFound on a catalog entity is a
// fatal closure gap and returns ErrUnresolvableURI. Library
// resolution feeds the minimum version selection pass before invoking
// the library lookup.
func (s *walkState) resolveAll(ctx context.Context, lookup Lookup) error {
	if err := s.resolveEntities(ctx, lookup); err != nil {
		return err
	}
	for _, a := range libraryAxes {
		if err := s.resolveLibraries(ctx, a, lookup); err != nil {
			return err
		}
	}
	return nil
}

// resolveEntities resolves the recorded references in URI order, and two entities
// sharing a reference in kind order, never in map order. What it resolves first is
// visible: the first reference that does not resolve is the one the refusal names,
// and a catalog lookup reports what it finds while it reads, so an order the
// runtime randomises would change both from one run to the next over identical
// input.
//
// A lookup that can read ahead (RegistryPrefetcher) is first probed with every
// reference in that same order, so what the references need from a registry is
// fetched several at once; the resolution that follows is unchanged by it.
func (s *walkState) resolveEntities(ctx context.Context, lookup Lookup) error {
	keys := make([]EntityKey, 0, len(s.entities))
	for key := range s.entities {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Less(keys[j]) })
	prefetchRegistryReads(ctx, lookup, func(probe Lookup) {
		for _, key := range keys {
			_, _ = s.entities[key].resolve(ctx, probe, key.URI)
		}
	})
	for _, key := range keys {
		uri := key.URI
		pending := s.entities[key]
		res, err := pending.resolve(ctx, lookup, uri)
		if err != nil {
			return err
		}
		// A reference no lookup answered carries its reference and kind hint the
		// way a lookup failure does, for the same reason.
		if res.NotFound {
			return NewUnresolvedEntityError(uri, pending.kindHint,
				entityNotFound(uri, referencedFromPreview(pending.referencedFrom), res.UnsatisfiedPin))
		}
		pending.resolution = &res
	}
	return nil
}

// resolve asks lookup for the entity p records under uri, by the resolution its
// collection site calls for, and returns what the lookup answered — a NotFound
// resolution included, which is the caller's to judge. It is the one resolution
// of a recorded reference: the walk refuses a reference it answers NotFound for,
// and the extends descent leaves such a base to the chain walk that reports it.
func (p *pendingRef) resolve(ctx context.Context, lookup Lookup, uri string) (Resolution, error) {
	// A sub-aggregate reference resolves through the dedicated aggregate
	// registry tier (bare-name capable), not the generic catalog-entity
	// tier-3 (which needs a tag); the walk marked it KindHint=="aggregate".
	// A pinned workspace-socket entity resolves through the socket resolution
	// (see ResolvePinnedSocketEntity). Every other entity passes its KindHint to
	// the catalog-entity resolver so a registry-backed pull can bind the bare
	// identity to its OCI location.
	var res Resolution
	var err error
	switch {
	case p.kindHint == KindAggregate:
		res, err = lookup.ResolveAggregate(ctx, uri)
	case p.pinnedSocket:
		res, err = ResolvePinnedSocketEntity(ctx, lookup, uri)
	default:
		res, err = lookup.ResolveCatalogEntity(ctx, uri, p.kindHint)
	}
	// A failure carries the reference and its kind hint alongside the rendered
	// message, so a reporter can say WHICH entity went unresolved without parsing
	// its own output back. The message is unchanged.
	if err != nil {
		return Resolution{}, NewUnresolvedEntityError(uri, p.kindHint,
			fmt.Errorf("resolve entity %q: %w", uri, err))
	}
	return res, nil
}

// entityNotFound is the failure the walk reports for a catalog-entity reference
// no lookup answered, where referencedFrom names the authored sites that reach it.
//
// A reference whose version pin is malformed is reported as that malformation
// rather than as a URI no entity matches: the pin names no version, so no
// catalog could hold what it asks for, and "no entity matches" would send the
// author looking for an entity when the fault is the pin's text. It is asked
// only after the lookup missed, so it changes no reference that resolves.
//
// unsatisfiedPin is the condition the lookup reported for a reference whose
// entity it holds at other versions only (see [Resolution.UnsatisfiedPin]), and
// it is reported in place of the not-found condition, in the same words around
// it, for the same reason: the name is right and only the version is not.
func entityNotFound(uri, referencedFrom string, unsatisfiedPin error) error {
	if pe, malformed := types.ParseVersionPin(uri).Malformed(); malformed {
		return pe.At("referenced from " + referencedFrom)
	}
	if unsatisfiedPin != nil {
		return fmt.Errorf("%w: entity %q (referenced from %s)", unsatisfiedPin, uri, referencedFrom)
	}
	return fmt.Errorf("%w: entity %q (referenced from %s)", ErrUnresolvableURI(), uri, referencedFrom)
}

// resolveLibraries applies minimum version selection (MVS) across the
// per-library consumer→version mapping for one axis, then invokes the injected
// Lookup to obtain digest + resolution-path metadata. The axis selects both the
// accumulator to drain and the kind the resolver dispatches on, so every library
// axis runs through this one resolution loop rather than a per-axis copy.
//
// Libraries are resolved in URI order, never in map order. What is resolved first
// is visible: the first library that does not resolve is the one the refusal
// names, and a lookup may announce what it finds while it resolves, so an order
// the runtime randomises would change both from one run to the next over identical
// input.
//
// A library already resolved at the version selection still selects — one the
// discovery's earlier round resolved, carried into this round by seedLibs — keeps
// that resolution and is not asked of the lookup again. Asking again would answer
// from the cache entry the first answer wrote, so a library this run fetched from a
// registry would be reported as read from the cache, and the pull-time signature
// verification, which reads that report, would pass it by. The seed records each
// consumer of a resolved library at the version the lookup reported, which may be
// later than the floor it was asked at, so a later round selects that version
// again unless a new requirement moves the selection above it; that requirement
// re-resolves the library at the new floor.
//
// A lookup that can read ahead (RegistryPrefetcher) is first probed with the
// libraries in that same order, as resolveEntities probes its references.
func (s *walkState) resolveLibraries(ctx context.Context, axis LibraryAxis, lookup Lookup) error {
	pending := axis.pendingLibs(s)
	uris := slices.Sorted(maps.Keys(pending))
	// The read ahead asks for what the loop below asks for, in its order, and stops
	// where the loop's own selection would refuse.
	prefetchRegistryReads(ctx, lookup, func(probe Lookup) {
		for _, uri := range uris {
			_, resolveURI, ask, err := libraryRequest(uri, pending[uri])
			if err != nil {
				return
			}
			if ask {
				_, _ = probe.ResolveLibrary(ctx, axis, resolveURI)
			}
		}
	})
	for _, uri := range uris {
		lib := pending[uri]
		selected, resolveURI, ask, err := libraryRequest(uri, lib)
		if err != nil {
			return err
		}
		if !ask {
			continue
		}
		res, err := lookup.ResolveLibrary(ctx, axis, resolveURI)
		if err != nil {
			return fmt.Errorf("resolve: resolve library %q: %w", uri, err)
		}
		if res.NotFound {
			return libraryNotFound(uri, resolveURI, res.UnsatisfiedPin)
		}
		// The version recorded is the one the lookup reports: the copy it read,
		// which a floor may have selected above the floor itself. The selected
		// floor stands in only for a lookup that reports no version.
		if res.ResolvedVersion == "" {
			res.ResolvedVersion = selected
		}
		lib.resolution = &res
	}
	return nil
}

// libraryNotFound is the failure the walk reports for the library recorded under
// uri that no lookup tier answered when asked for resolveURI.
//
// unsatisfiedPin is the condition the lookup reported for a library it holds under
// that name at versions the request's floor does not admit only (see
// [Resolution.UnsatisfiedPin]), and it is reported in place of the not-found
// condition, naming the request with its floor, for the reason an entity's is:
// the name is right and only the version is not.
func libraryNotFound(uri, resolveURI string, unsatisfiedPin error) error {
	if unsatisfiedPin != nil {
		return fmt.Errorf("%w: library %q", unsatisfiedPin, resolveURI)
	}
	return fmt.Errorf("%w: library %q", ErrUnresolvableURI(), uri)
}

// libraryRequest is what resolveLibraries asks the lookup for the library recorded
// under uri: the version minimum version selection settles on across its consumers,
// the reference that asks for that version, and whether to ask at all — a library
// already resolved at the selected version is not asked again (see resolveLibraries).
// It is the one statement of the request, so the read ahead asks for what the
// resolution goes on to ask for.
//
// The selected version (the optional `@version` floor) reaches the lookup as a
// `name:version` reference, so a registry pull resolves that tag. A pure bare-name
// reference (no floor) leaves the version empty for the lookup to resolve at first
// pull (the list-tags→highest path). The stored library key stays the host-less bare
// name regardless.
func libraryRequest(uri string, lib *pendingLibrary) (selected, resolveURI string, ask bool, err error) {
	selected, err = selectMVSVersion(uri, consumerVersionLabels(lib.versionByConsumer))
	if err != nil {
		return "", "", false, err
	}
	if lib.resolution != nil && selected != "" && lib.resolution.ResolvedVersion == selected {
		return selected, "", false, nil
	}
	resolveURI = uri
	if selected != "" {
		resolveURI = uri + ":" + selected
	}
	return selected, resolveURI, true, nil
}

// selectMVSVersion applies the standard minimum version selection
// algorithm: per library URI, the resolved version is the maximum
// across all consumers' pinned versions (i.e., the lowest version
// that satisfies every consumer's minimum requirement). Major-
// version disagreement surfaces as ErrMVSConflict.
//
// Unpinned consumers (empty version string) don't contribute to
// the max; the result is determined by the pinned consumers
// alone. If every consumer is unpinned the function returns
// empty string (the lookup-reported ResolvedVersion wins).
//
// The result is a function of the pins alone, never of the order a map is
// ranged in. Consumers are visited in consumer-URI order: the major every pin is
// held to is the first pinned consumer's in that order, and a conflict names
// every pinned consumer visited up to and including the first that disagrees.
//
// Two pins compareSemver ranks equal but spelled differently — "1.0" and
// "1.0.0" — are decided by their spelling: the one naming more version
// components wins, and between two naming as many, the one that sorts first as a
// string. The selected spelling is the version recorded for the library and the
// tag a registry is asked for, and a library's own version is written in the full
// three-component form its schema declares, so the fuller spelling is preferred.
func selectMVSVersion(libraryURI string, versionByConsumer map[string]string) (string, error) {
	var winner string
	var winnerMajor string
	conflicts := map[string]string{}
	for _, consumer := range slices.Sorted(maps.Keys(versionByConsumer)) {
		version := versionByConsumer[consumer]
		if version == "" {
			continue
		}
		major := majorComponent(version)
		if winner == "" {
			winner = version
			winnerMajor = major
			conflicts[consumer] = version
			continue
		}
		if major != winnerMajor {
			conflicts[consumer] = version
			return "", fmt.Errorf("%w: library %q has consumers requiring incompatible majors: %s",
				ErrMVSConflict(), libraryURI, formatConsumerConflicts(conflicts))
		}
		conflicts[consumer] = version
		if order := compareSemver(version, winner); order > 0 || (order == 0 && preferSpelling(version, winner)) {
			winner = version
		}
	}
	return winner, nil
}

// preferSpelling reports whether version a is chosen over version b when
// compareSemver ranks the two equal: the spelling naming more version components
// first, then the one that sorts first as a string. It counts components as
// compareSemver splits them, so the two agree on what a component is.
func preferSpelling(a, b string) bool {
	if na, nb := len(strings.Split(a, ".")), len(strings.Split(b, ".")); na != nb {
		return na > nb
	}
	return a < b
}

// majorComponent returns the leading dotted component of a SemVer
// string. Returns the input unchanged if no dot is present.
func majorComponent(v string) string {
	if idx := strings.IndexByte(v, '.'); idx >= 0 {
		return v[:idx]
	}
	return v
}

// compareSemver returns -1, 0, or +1 if a < b, a == b, or a > b
// per a lexicographic-by-component comparison on the dotted
// segments. Sufficient for the exact-version-pinning model the
// walker accepts today; pre-release suffixes and richer SemVer
// comparison are not handled (a future evolution can extend this
// helper when those forms become reachable).
func compareSemver(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	n := len(aParts)
	if len(bParts) > n {
		n = len(bParts)
	}
	for i := 0; i < n; i++ {
		var ai, bi string
		if i < len(aParts) {
			ai = aParts[i]
		}
		if i < len(bParts) {
			bi = bParts[i]
		}
		ax := parseLeadingInt(ai)
		bx := parseLeadingInt(bi)
		if ax < bx {
			return -1
		}
		if ax > bx {
			return 1
		}
	}
	return 0
}

// parseLeadingInt extracts the leading sequence of decimal digits
// from a SemVer component, returning 0 when no digits are present.
// Non-digit suffixes (pre-release labels, build metadata) are
// ignored under the exact-version-pinning model the walker accepts
// today; richer SemVer support arrives with future extension.
func parseLeadingInt(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// formatConsumerConflicts emits a deterministic-ordered consumer →
// version string for the ErrMVSConflict error message.
func formatConsumerConflicts(m map[string]string) string {
	consumers := make([]string, 0, len(m))
	for k := range m {
		consumers = append(consumers, k)
	}
	sort.Strings(consumers)
	parts := make([]string, 0, len(consumers))
	for _, c := range consumers {
		parts = append(parts, fmt.Sprintf("%s wants %s", c, m[c]))
	}
	return strings.Join(parts, "; ")
}

// referencedFromPreview emits a deterministic-ordered preview of
// the referencedFrom set for the ErrUnresolvableURI error message.
func referencedFromPreview(m map[string]struct{}) string {
	return ReferencedFromPreview(slices.Collect(maps.Keys(m)))
}

// ReferencedFromPreview renders the authoring sites that reach one reference
// for a message: sorted, at most three of them, and "..." where more were
// recorded. It is the one wording of where a reference is written, shared by the
// walk's refusal of a reference nothing resolves and by the lockfile's report of
// a reference it does not pin, so the two name a site the same way. The input is
// not modified.
func ReferencedFromPreview(paths []string) string {
	sorted := slices.Sorted(slices.Values(paths))
	if len(sorted) > 3 {
		sorted = append(sorted[:3], "...")
	}
	return strings.Join(sorted, ", ")
}

// materialise converts the post-resolution pending maps into the
// public Closure shape. Slices are intentionally unsorted at this
// stage; canonicaliseClosure performs the final sort pass.
func (s *walkState) materialise() Closure {
	c := Closure{}
	for key, p := range s.entities {
		if p.resolution == nil {
			continue
		}
		c.Entities = append(c.Entities, p.toResolvedRef(key.URI))
	}
	// c.Plugins is populated at pull time by the pipeline-plugin descent, which
	// descends every plugin-kinded entity (workspace-socket plugins recorded
	// here on the entity axis, plus Recipe / FeatureRecipe pipeline-step plugins) to
	// its .wasm artifact. ComputeClosure pins only the entities.
	for _, a := range libraryAxes {
		a.SetLibraries(&c, s.materialiseLibs(a))
	}
	return c
}

// materialiseLibs converts one axis's post-resolution accumulator into the public
// slice shape. Slices are intentionally unsorted at this stage; canonicaliseClosure
// performs the final sort pass.
func (s *walkState) materialiseLibs(axis LibraryAxis) []ResolvedLibrary {
	pending := axis.pendingLibs(s)
	out := make([]ResolvedLibrary, 0, len(pending))
	for uri, lib := range pending {
		out = append(out, lib.toResolvedLibrary(uri))
	}
	return out
}

// canonicaliseClosure sorts every slice field by its canonical
// key (URI for top-level slices, then kind for entities sharing one;
// ConsumerURI for Consumers; lexicographic for Fragments and
// ReferencedFrom). Two independent walks of the same input produce
// identical Closure bytes after this pass.
func canonicaliseClosure(c *Closure) {
	sort.Slice(c.Entities, func(i, j int) bool { return c.Entities[i].Key().Less(c.Entities[j].Key()) })
	sort.Slice(c.Plugins, func(i, j int) bool { return c.Plugins[i].URI < c.Plugins[j].URI })
	for _, a := range libraryAxes {
		libs := a.Libraries(c)
		sort.Slice(libs, func(i, j int) bool { return libs[i].URI < libs[j].URI })
	}

	for i := range c.Entities {
		sort.Strings(c.Entities[i].ReferencedFrom)
	}
	for i := range c.Plugins {
		sort.Strings(c.Plugins[i].ReferencedFrom)
	}
	for _, a := range libraryAxes {
		libs := a.Libraries(c)
		for i := range libs {
			sortLibrary(&libs[i])
		}
	}
}

func sortLibrary(lib *ResolvedLibrary) {
	sort.Slice(lib.Consumers, func(i, j int) bool {
		return lib.Consumers[i].ConsumerURI < lib.Consumers[j].ConsumerURI
	})
	for i := range lib.Consumers {
		sort.Strings(lib.Consumers[i].Fragments)
	}
}

// ---- helper accessors on the pending types ----

// addEntityKind / addLibrary are the structural accumulator entry points used by
// every per-axis sub-walker.

// addEntityKind records an entity reference together with the hint its collection
// site carries, which names the entity's kind (see EntityKindOf). Every site the
// walk collects an entity at knows the kind from its position — a Manifest
// mapping value is a Recipe or a FeatureRecipe, a Release's spec.manifest is a
// Manifest — so the hint is never empty here.
//
// The entity is keyed by that kind and the reference, so one reference collected
// as two kinds — an Aggregate and a Manifest of one name, a Component and a Recipe
// of one name — is two entities, each resolved as its own kind and pinned by a
// row of its own. The hint stays authoritative for source-descent, which would
// otherwise re-derive the kind lexically from the URI and miss short-form
// references.
//
// One identity met under two hints keeps the one keptKindHint keeps, which is the
// rule a pass merging entities into a closure applies too (mergeEntity).
//
// It returns the entity's accumulator, so a site that records more about the
// entity it collected reaches the very entry the reference was recorded on.
func (s *walkState) addEntityKind(uri, refPath, hint string) *pendingRef {
	key := EntityKey{Kind: EntityKindOf(hint), URI: uri}
	p := s.entities[key]
	if p == nil {
		p = &pendingRef{referencedFrom: map[string]struct{}{}}
		s.entities[key] = p
	}
	p.referencedFrom[refPath] = struct{}{}
	p.kindHint = keptKindHint(p.kindHint, hint)
	return p
}

// seedLibs re-seeds an axis's accumulator from libraries already resolved into a
// closure, so an incremental walk preserves them through materialisation.
func (s *walkState) seedLibs(axis LibraryAxis, libs []ResolvedLibrary) {
	pending := axis.pendingLibs(s)
	for _, lib := range libs {
		pending[lib.URI] = libraryFromResolved(lib)
	}
}

// addLibrary records one consumer→library edge on the given axis: the consumer's
// optional version floor and the fragment it references. It is written once and
// parameterised by the axis, so every axis accumulates by exactly the same rule
// and a per-axis copy cannot drift from its siblings.
func (s *walkState) addLibrary(axis LibraryAxis, libURI, version string, consumer consumerIdentity, fragment string) {
	pending := axis.pendingLibs(s)
	if pending[libURI] == nil {
		pending[libURI] = &pendingLibrary{
			versionByConsumer: map[consumerIdentity]string{},
			fragmentsByCons:   map[consumerIdentity]map[string]struct{}{},
		}
	}
	l := pending[libURI]
	if version != "" {
		l.versionByConsumer[consumer] = version
	} else if _, present := l.versionByConsumer[consumer]; !present {
		l.versionByConsumer[consumer] = ""
	}
	if l.fragmentsByCons[consumer] == nil {
		l.fragmentsByCons[consumer] = map[string]struct{}{}
	}
	if fragment != "" {
		l.fragmentsByCons[consumer][fragment] = struct{}{}
	}
}

// toResolvedRef materialises a ResolvedRef once the resolveAll pass
// has populated the resolution pointer. The URI is supplied by the
// caller since pendingRef itself is map-keyed by kind and URI; the
// kind travels in the hint.
func (p *pendingRef) toResolvedRef(uri string) ResolvedRef {
	return ResolvedRef{
		URI:             uri,
		ResolvedVersion: p.resolution.ResolvedVersion,
		Digest:          p.resolution.Digest,
		ManifestDigest:  p.resolution.ManifestDigest,
		ResolutionPath:  p.resolution.ResolutionPath,
		ReferencedFrom:  p.sites(),
		KindHint:        p.kindHint,
	}
}

// sites returns the authoring sites p was recorded at, in no particular order;
// canonicaliseClosure sorts every entity's sites before a closure is handed out.
func (p *pendingRef) sites() []string {
	refs := make([]string, 0, len(p.referencedFrom))
	for r := range p.referencedFrom {
		refs = append(refs, r)
	}
	return refs
}

// toResolvedLibrary materialises the library resolved for uri. Its consumers are
// recorded one per URI, as a lockfile row records them: the fragments every
// identity sharing a URI references are merged into that URI's one row.
func (p *pendingLibrary) toResolvedLibrary(uri string) ResolvedLibrary {
	byURI := make(map[string]map[string]struct{}, len(p.fragmentsByCons))
	for cons, fragSet := range p.fragmentsByCons {
		merged := byURI[cons.URI]
		if merged == nil {
			merged = map[string]struct{}{}
			byURI[cons.URI] = merged
		}
		for f := range fragSet {
			merged[f] = struct{}{}
		}
	}
	consumers := make([]LibraryConsumer, 0, len(byURI))
	for cons, fragSet := range byURI {
		frags := make([]string, 0, len(fragSet))
		for f := range fragSet {
			frags = append(frags, f)
		}
		consumers = append(consumers, LibraryConsumer{
			ConsumerURI: cons,
			Fragments:   frags,
		})
	}
	out := ResolvedLibrary{
		URI:       uri,
		Consumers: consumers,
	}
	if p.resolution != nil {
		out.ResolvedVersion = p.resolution.ResolvedVersion
		out.Digest = p.resolution.Digest
		out.ManifestDigest = p.resolution.ManifestDigest
		out.ResolutionPath = p.resolution.ResolutionPath
	}
	return out
}

// libraryFromResolved seeds pendingLibrary state from a ResolvedLibrary
// (used by WalkSchemaRefs / WalkMappingImport to merge new
// $ref:/$import: discoveries into an existing closure).
func libraryFromResolved(lib ResolvedLibrary) *pendingLibrary {
	p := &pendingLibrary{
		versionByConsumer: map[consumerIdentity]string{},
		fragmentsByCons:   map[consumerIdentity]map[string]struct{}{},
	}
	for _, c := range lib.Consumers {
		set := map[string]struct{}{}
		for _, f := range c.Fragments {
			set[f] = struct{}{}
		}
		// A materialised consumer row records no kind, so it is seeded under none.
		consumer := consumerIdentity{URI: c.ConsumerURI}
		p.fragmentsByCons[consumer] = set
		p.versionByConsumer[consumer] = lib.ResolvedVersion
	}
	if lib.ResolvedVersion != "" || lib.Digest != "" {
		p.resolution = &LibraryResolution{
			Resolution: Resolution{
				ResolvedVersion: lib.ResolvedVersion,
				Digest:          lib.Digest,
				ManifestDigest:  lib.ManifestDigest,
				ResolutionPath:  lib.ResolutionPath,
			},
		}
	}
	return p
}
