// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import (
	"fmt"
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
func ComputeClosure(
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
	if err := state.resolveAll(in.lookup); err != nil {
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
	entities      map[string]*pendingRef
	schemaLibs    map[string]*pendingLibrary
	mappingLibs   map[string]*pendingLibrary
	workloadLibs  map[string]*pendingLibrary
	artifactsLibs map[string]*pendingLibrary
}

// pendingRef collects the per-URI accumulating data the walker
// builds during the descent: a deduplicated set of authoring-site
// paths plus, once the resolveAll pass has run, the looked-up
// Resolution metadata.
type pendingRef struct {
	referencedFrom map[string]struct{}
	resolution     *Resolution
	// kindHint records a catalog kind ("recipe" / "feature_recipe") known from
	// the URI's collection site (e.g. a Manifest mapping value's position).
	// First non-empty hint wins; later references to the same URI keep it.
	kindHint string
}

// pendingLibrary collects per-library-URI consumer→fragment
// mappings the walker builds; the version constraints contributed
// by each consumer feed the minimum version selection pass.
// Once the resolveAll pass has run, resolution carries the
// looked-up LibraryResolution metadata.
type pendingLibrary struct {
	versionByConsumer map[string]string              // consumerURI → requested version (empty when unpinned)
	fragmentsByCons   map[string]map[string]struct{} // consumerURI → set of fragments
	resolution        *LibraryResolution
}

func newWalkState() *walkState {
	return &walkState{
		entities:      map[string]*pendingRef{},
		schemaLibs:    map[string]*pendingLibrary{},
		mappingLibs:   map[string]*pendingLibrary{},
		workloadLibs:  map[string]*pendingLibrary{},
		artifactsLibs: map[string]*pendingLibrary{},
	}
}

// walk runs every per-axis sub-walker over the inputs, populating
// the pending maps. The walk is purely structural; lookup is
// invoked only at the resolveAll stage.
func (s *walkState) walk(in inputs) error {
	if in.release == nil {
		return nil
	}

	if in.release.Spec.Aggregate != "" {
		s.addEntity(in.release.Spec.Aggregate, "release.spec.aggregate")
	}
	if in.release.Spec.Manifest != "" {
		s.addEntity(in.release.Spec.Manifest, "release.spec.manifest")
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
			s.addEntityKind(sub.Type, subPath, aggregateKindHint)
		}
	}
	// Every subsystem's components and its own scope-wide attachments are recorded;
	// the authoring-site path accumulated per level is what a reference records as
	// its origin. No visitor callback fails, so the walk cannot.
	_ = types.WalkInstanceTree(agg.Spec.Subsystems, types.SubsystemTree(), path+".spec", struct{}{},
		types.InstanceTreeVisitor[types.Subsystem, types.ComponentInstance, struct{}]{
			Path: func(parentPath, name string, _ bool) string {
				return parentPath + ".subsystems." + name
			},
			Visit: func(ci types.ComponentInstance, name, subPath string, _ bool, _ struct{}) error {
				if ci.Type != "" {
					s.addEntityKind(ci.Type, fmt.Sprintf("%s.components.%s.type", subPath, name), "component")
				}
				s.walkComponentInstanceFeatures(ci, fmt.Sprintf("%s.components.%s", subPath, name))
				return nil
			},
			Leave: func(sys types.Subsystem, _, subPath string, _ struct{}) {
				for name, fi := range sys.Features.All() {
					if fi.Type != "" {
						s.addEntityKind(fi.Type, fmt.Sprintf("%s.features.%s.type", subPath, name), "feature")
					}
				}
			},
		})
}

// walkComponentInstanceFeatures collects every Feature URI declared via the
// component-instance's Features map. Every attachment family is declared there,
// so one walk reaches them all — a credential attachment's type resolves to a
// Feature entity like any other, and its registry location is feature/<name>.
func (s *walkState) walkComponentInstanceFeatures(ci types.ComponentInstance, path string) {
	for name, fi := range ci.Features.All() {
		if fi.Type != "" {
			s.addEntityKind(fi.Type, fmt.Sprintf("%s.features.%s.type", path, name), "feature")
		}
	}
}

// walkManifest descends Manifest.Spec.Mappings.{Components,
// Features} flat-shape mappings. The Components mapping carries
// component-type → Recipe URIs; the Features mapping carries
// feature-type → FeatureRecipe URIs.
func (s *walkState) walkManifest(m *types.Manifest) {
	if m == nil {
		return
	}
	if m.Spec.Extends != "" {
		s.addEntity(m.Spec.Extends, "manifest.spec.extends")
	}
	if m.Spec.Mappings.Components != nil {
		for componentType, recipeURI := range m.Spec.Mappings.Components.Type.All() {
			// The mapping key is the developer-declared Component URI.
			s.addEntityKind(componentType, fmt.Sprintf("manifest.spec.mappings.components.type.%s", componentType), "component")
			// The mapping value is the Ops-side Recipe URI. Its position (a
			// components-mapping value) is authoritative for source-descent:
			// record the recipe kind so a short-form reference is recognised
			// even without a "/recipe/" path segment.
			if recipeURI != "" {
				s.addEntityKind(recipeURI, fmt.Sprintf("manifest.spec.mappings.components.type.%s", componentType), "recipe")
			}
		}
	}
	if m.Spec.Mappings.Features != nil {
		for featureType, frURI := range m.Spec.Mappings.Features.Type.All() {
			// The mapping key is the developer-declared Feature URI.
			s.addEntityKind(featureType, fmt.Sprintf("manifest.spec.mappings.features.type.%s", featureType), "feature")
			// The mapping value is the Ops-side FeatureRecipe URI. Its position
			// (a features-mapping value) is authoritative: record the
			// feature_recipe kind so source-descent recognises it regardless of
			// the URI's lexical form.
			if frURI != "" {
				s.addEntityKind(frURI, fmt.Sprintf("manifest.spec.mappings.features.type.%s", featureType), "feature_recipe")
			}
		}
	}
}

// walkReleaseAttachments descends Release.Spec.Boundaries recursively
// and collects the type URIs of per-component Feature attachments. A
// credential is a Feature carrying the feature.aggrega.ai/kind: secret
// label, so it is collected through the same walk as any other attachment.
func (s *walkState) walkReleaseAttachments(r *types.Release) {
	if r == nil {
		return
	}
	for name, b := range r.Spec.Boundaries.All() {
		s.walkBoundary(b, fmt.Sprintf("release.spec.boundaries.%s", name))
	}
}

func (s *walkState) walkBoundary(b types.Boundary, path string) {
	for compName, bc := range b.Components.All() {
		compPath := fmt.Sprintf("%s.components.%s", path, compName)
		for fname, bf := range bc.Features.All() {
			if bf.Type != "" {
				s.addEntityKind(bf.Type, fmt.Sprintf("%s.features.%s.type", compPath, fname), "feature")
			}
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
func (s *walkState) walkWorkspacePlugins(cfg workspaceconfig.Config) {
	record := func(uri, refPath string) {
		if uri == "" {
			return
		}
		s.addEntityKind(uri, refPath, pluginKindHint)
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
	state := newWalkState()
	// Seed the state with any libraries already present in the
	// closure so the post-call materialise step preserves them.
	for _, lib := range c.SchemaLibraries {
		state.schemaLibs[lib.URI] = libraryFromResolved(lib)
	}
	if err := state.walkSchemaRefsInto(node, consumerURI); err != nil {
		return err
	}
	c.SchemaLibraries = nil
	for _, lib := range state.materialiseSchemaLibs() {
		c.SchemaLibraries = append(c.SchemaLibraries, lib)
	}
	canonicaliseClosure(c)
	return nil
}

// walkSchemaRefsInto is the internal yaml.Node walker invoked
// during the closure build. It descends the tree, harvests every
// $ref: child whose value matches the library-reference shape,
// and records the (library, version, fragment, consumer) edge.
func (s *walkState) walkSchemaRefsInto(node *yaml.Node, consumerURI string) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			if err := s.walkSchemaRefsInto(child, consumerURI); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valNode := node.Content[i+1]
			if keyNode.Value == "$ref" && valNode.Kind == yaml.ScalarNode {
				if isIdentityLibraryRef(valNode.Value) {
					if err := s.recordSchemaRef(valNode.Value, consumerURI); err != nil {
						return err
					}
				}
				continue
			}
			if err := s.walkSchemaRefsInto(valNode, consumerURI); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			if err := s.walkSchemaRefsInto(child, consumerURI); err != nil {
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
func (s *walkState) recordSchemaRef(refValue, consumerURI string) error {
	libName, version, fragment, err := parseIdentityRef(refValue)
	if err != nil {
		return err
	}
	s.addSchemaLibrary(libName, version, consumerURI, formatFragmentRef(fragment))
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
	state := newWalkState()
	for _, lib := range c.MappingLibraries {
		state.mappingLibs[lib.URI] = libraryFromResolved(lib)
	}
	if err := state.walkMappingImportInto(template, consumerURI); err != nil {
		return err
	}
	c.MappingLibraries = nil
	for _, lib := range state.materialiseMappingLibs() {
		c.MappingLibraries = append(c.MappingLibraries, lib)
	}
	canonicaliseClosure(c)
	return nil
}

func (s *walkState) walkMappingImportInto(template, consumerURI string) error {
	trimmed := strings.TrimSpace(template)
	if !strings.HasPrefix(trimmed, "$import:") {
		return nil
	}
	// Take the rest of the first line as the URL value, trimming
	// surrounding whitespace.
	rest := strings.TrimPrefix(trimmed, "$import:")
	if newline := strings.IndexByte(rest, '\n'); newline >= 0 {
		rest = rest[:newline]
	}
	url := strings.TrimSpace(rest)
	if url == "" {
		return nil
	}
	// Gate as the $ref discovery does: record only host-less identity
	// MappingLibrary references; a local-file import or a host-bearing
	// form is not a cross-library edge and is left for its consumption site.
	if !isIdentityImportRef(url) {
		return nil
	}
	libName, version, fragment, err := parseIdentityImportRef(url)
	if err != nil {
		return err
	}
	s.addMappingLibrary(libName, version, consumerURI, formatFragmentRef(fragment))
	return nil
}

func (s *walkState) walkWorkloadImportInto(template, consumerURI string) error {
	trimmed := strings.TrimSpace(template)
	if !strings.HasPrefix(trimmed, "$import:") {
		return nil
	}
	rest := strings.TrimPrefix(trimmed, "$import:")
	if newline := strings.IndexByte(rest, '\n'); newline >= 0 {
		rest = rest[:newline]
	}
	url := strings.TrimSpace(rest)
	if url == "" {
		return nil
	}
	// Record only host-less identity WorkloadLibrary references; a local-file
	// import or a host-bearing form is not a cross-library edge and is left for
	// its consumption site.
	if !isIdentityWorkloadImportRef(url) {
		return nil
	}
	libName, version, fragment, err := parseIdentityWorkloadImportRef(url)
	if err != nil {
		return err
	}
	s.addWorkloadLibrary(libName, version, consumerURI, formatFragmentRef(fragment))
	return nil
}

func (s *walkState) walkArtifactImportInto(template, consumerURI string) error {
	trimmed := strings.TrimSpace(template)
	if !strings.HasPrefix(trimmed, "$import:") {
		return nil
	}
	rest := strings.TrimPrefix(trimmed, "$import:")
	if newline := strings.IndexByte(rest, '\n'); newline >= 0 {
		rest = rest[:newline]
	}
	url := strings.TrimSpace(rest)
	if url == "" {
		return nil
	}
	// Record only host-less identity ArtifactsLibrary references; a local-file
	// import or a host-bearing form is not a cross-library edge and is left for
	// its consumption site.
	if !isIdentityArtifactImportRef(url) {
		return nil
	}
	libName, version, fragment, err := parseIdentityArtifactImportRef(url)
	if err != nil {
		return err
	}
	s.addArtifactsLibrary(libName, version, consumerURI, formatFragmentRef(fragment))
	return nil
}

// resolveAll iterates the pending entity / library maps and invokes
// the Lookup interface per URI. NotFound on a catalog entity is a
// fatal closure gap and returns ErrUnresolvableURI. Library
// resolution feeds the minimum version selection pass before invoking
// the library lookup.
func (s *walkState) resolveAll(lookup Lookup) error {
	if err := s.resolveEntities(lookup); err != nil {
		return err
	}
	if err := s.resolveLibraries(s.schemaLibs, lookup.ResolveSchemaLibrary); err != nil {
		return err
	}
	if err := s.resolveLibraries(s.mappingLibs, lookup.ResolveMappingLibrary); err != nil {
		return err
	}
	if err := s.resolveLibraries(s.workloadLibs, lookup.ResolveWorkloadLibrary); err != nil {
		return err
	}
	return s.resolveLibraries(s.artifactsLibs, lookup.ResolveArtifactsLibrary)
}

func (s *walkState) resolveEntities(lookup Lookup) error {
	for uri, pending := range s.entities {
		// A sub-aggregate reference resolves through the dedicated aggregate
		// registry tier (bare-name capable), not the generic catalog-entity
		// tier-3 (which needs a tag); the walk marked it KindHint=="aggregate".
		// Every other entity passes its KindHint to the catalog-entity resolver so
		// a registry-backed pull can bind the bare identity to its OCI location.
		var res Resolution
		var err error
		if pending.kindHint == aggregateKindHint {
			res, err = lookup.ResolveAggregate(uri)
		} else {
			res, err = lookup.ResolveCatalogEntity(uri, pending.kindHint)
		}
		if err != nil {
			return fmt.Errorf("resolve: resolve entity %q: %w", uri, err)
		}
		if res.NotFound {
			return fmt.Errorf("%w: entity %q (referenced from %s)",
				ErrUnresolvableURI, uri, referencedFromPreview(pending.referencedFrom))
		}
		pending.resolution = &res
	}
	return nil
}

// resolveLibraries applies minimum version selection (MVS) across
// the per-library consumer→version mapping, then invokes the given
// Lookup resolver (one of ResolveSchemaLibrary / ResolveMappingLibrary /
// ResolveWorkloadLibrary) to obtain digest + resolution-path metadata.
// The pending map and resolver are supplied together so every library
// axis runs through one resolution loop rather than a per-axis copy.
func (s *walkState) resolveLibraries(pending map[string]*pendingLibrary, resolve func(uri string) (LibraryResolution, error)) error {
	for uri, lib := range pending {
		selected, err := selectMVSVersion(uri, lib.versionByConsumer)
		if err != nil {
			return err
		}
		// Thread the MVS-selected version (the optional `@version` floor) to the
		// lookup as a `name:version` URI, so a Tier-3 pull resolves that tag. A
		// pure bare-name reference (no floor) leaves the version empty for the
		// lookup to resolve at first pull (the list-tags→highest path). The stored
		// library key stays the host-less bare name regardless.
		resolveURI := uri
		if selected != "" {
			resolveURI = uri + ":" + selected
		}
		res, err := resolve(resolveURI)
		if err != nil {
			return fmt.Errorf("resolve: resolve library %q: %w", uri, err)
		}
		if res.NotFound {
			return fmt.Errorf("%w: library %q", ErrUnresolvableURI, uri)
		}
		// Trust the lookup-reported ResolvedVersion when callers
		// supplied no explicit version pin; otherwise prefer the
		// MVS-selected version when consumers had opinions.
		if selected != "" {
			res.ResolvedVersion = selected
		}
		lib.resolution = &res
	}
	return nil
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
func selectMVSVersion(libraryURI string, versionByConsumer map[string]string) (string, error) {
	var winner string
	var winnerMajor string
	conflicts := map[string]string{}
	for consumer, version := range versionByConsumer {
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
				ErrMVSConflict, libraryURI, formatConsumerConflicts(conflicts))
		}
		conflicts[consumer] = version
		if compareSemver(version, winner) > 0 {
			winner = version
		}
	}
	return winner, nil
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
	paths := make([]string, 0, len(m))
	for k := range m {
		paths = append(paths, k)
	}
	sort.Strings(paths)
	if len(paths) > 3 {
		paths = append(paths[:3], "...")
	}
	return strings.Join(paths, ", ")
}

// materialise converts the post-resolution pending maps into the
// public Closure shape. Slices are intentionally unsorted at this
// stage; canonicaliseClosure performs the final sort pass.
func (s *walkState) materialise() Closure {
	c := Closure{}
	for uri, p := range s.entities {
		if p.resolution == nil {
			continue
		}
		c.Entities = append(c.Entities, p.toResolvedRef(uri))
	}
	// c.Plugins is populated at pull time by the pipeline-plugin descent, which
	// descends every plugin-kinded entity (workspace-socket plugins recorded
	// here on the entity axis, plus Recipe / FeatureRecipe pipeline-step plugins) to
	// its .wasm artifact. ComputeClosure pins only the entities.
	c.SchemaLibraries = s.materialiseSchemaLibs()
	c.MappingLibraries = s.materialiseMappingLibs()
	c.WorkloadLibraries = s.materialiseWorkloadLibs()
	c.ArtifactsLibraries = s.materialiseArtifactsLibs()
	return c
}

func (s *walkState) materialiseSchemaLibs() []ResolvedLibrary {
	out := make([]ResolvedLibrary, 0, len(s.schemaLibs))
	for uri, lib := range s.schemaLibs {
		out = append(out, lib.toResolvedLibrary(uri))
	}
	return out
}

func (s *walkState) materialiseMappingLibs() []ResolvedLibrary {
	out := make([]ResolvedLibrary, 0, len(s.mappingLibs))
	for uri, lib := range s.mappingLibs {
		out = append(out, lib.toResolvedLibrary(uri))
	}
	return out
}

func (s *walkState) materialiseWorkloadLibs() []ResolvedLibrary {
	out := make([]ResolvedLibrary, 0, len(s.workloadLibs))
	for uri, lib := range s.workloadLibs {
		out = append(out, lib.toResolvedLibrary(uri))
	}
	return out
}

func (s *walkState) materialiseArtifactsLibs() []ResolvedLibrary {
	out := make([]ResolvedLibrary, 0, len(s.artifactsLibs))
	for uri, lib := range s.artifactsLibs {
		out = append(out, lib.toResolvedLibrary(uri))
	}
	return out
}

// canonicaliseClosure sorts every slice field by its canonical
// key (URI for top-level slices; ConsumerURI for Consumers;
// lexicographic for Fragments and ReferencedFrom). Two
// independent walks of the same input produce identical Closure
// bytes after this pass.
func canonicaliseClosure(c *Closure) {
	sort.Slice(c.Entities, func(i, j int) bool { return c.Entities[i].URI < c.Entities[j].URI })
	sort.Slice(c.Plugins, func(i, j int) bool { return c.Plugins[i].URI < c.Plugins[j].URI })
	sort.Slice(c.SchemaLibraries, func(i, j int) bool { return c.SchemaLibraries[i].URI < c.SchemaLibraries[j].URI })
	sort.Slice(c.MappingLibraries, func(i, j int) bool { return c.MappingLibraries[i].URI < c.MappingLibraries[j].URI })
	sort.Slice(c.WorkloadLibraries, func(i, j int) bool { return c.WorkloadLibraries[i].URI < c.WorkloadLibraries[j].URI })
	sort.Slice(c.ArtifactsLibraries, func(i, j int) bool { return c.ArtifactsLibraries[i].URI < c.ArtifactsLibraries[j].URI })

	for i := range c.Entities {
		sort.Strings(c.Entities[i].ReferencedFrom)
	}
	for i := range c.Plugins {
		sort.Strings(c.Plugins[i].ReferencedFrom)
	}
	for i := range c.SchemaLibraries {
		sortLibrary(&c.SchemaLibraries[i])
	}
	for i := range c.MappingLibraries {
		sortLibrary(&c.MappingLibraries[i])
	}
	for i := range c.WorkloadLibraries {
		sortLibrary(&c.WorkloadLibraries[i])
	}
	for i := range c.ArtifactsLibraries {
		sortLibrary(&c.ArtifactsLibraries[i])
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

// addEntity / addSchemaLibrary / addMappingLibrary are the
// structural accumulator entry points used by every per-axis
// sub-walker.

func (s *walkState) addEntity(uri, refPath string) {
	if s.entities[uri] == nil {
		s.entities[uri] = &pendingRef{referencedFrom: map[string]struct{}{}}
	}
	s.entities[uri].referencedFrom[refPath] = struct{}{}
}

// addEntityKind records an entity reference together with a catalog kind known
// from the collection site ("recipe" or "feature_recipe"). The kind is
// authoritative for source-descent, which would otherwise re-derive it
// lexically from the URI and miss short-form references. First non-empty kind
// for a URI wins, so a reference whose position implies a kind is not
// overwritten by a later position-less reference to the same URI.
func (s *walkState) addEntityKind(uri, refPath, kind string) {
	s.addEntity(uri, refPath)
	if kind == "" {
		return
	}
	// Recipe / FeatureRecipe / Aggregate hints are authoritative — source-descent
	// and sub-aggregate descent rely on them. A Component / Feature hint is
	// weaker: it fills an empty hint but never clobbers an authoritative one, and
	// an authoritative hint upgrades a previously-recorded weak one. This matters
	// when one bare URI is shared across kinds (a Component and a Recipe of the
	// same name): the authoritative kind must win regardless of walk order.
	cur := s.entities[uri].kindHint
	if cur == "" || (isWeakKindHint(cur) && !isWeakKindHint(kind)) {
		s.entities[uri].kindHint = kind
	}
}

// isWeakKindHint reports whether a KindHint is a weak (overridable) entity kind.
// Weak hints (component, feature) supply the registry-location kind for a
// bare-identity pull but yield to an authoritative recipe/feature_recipe/aggregate
// hint on a URI shared across kinds.
func isWeakKindHint(kind string) bool {
	return kind == "component" || kind == "feature"
}

func (s *walkState) addSchemaLibrary(libURI, version, consumerURI, fragment string) {
	if s.schemaLibs[libURI] == nil {
		s.schemaLibs[libURI] = &pendingLibrary{
			versionByConsumer: map[string]string{},
			fragmentsByCons:   map[string]map[string]struct{}{},
		}
	}
	l := s.schemaLibs[libURI]
	if version != "" {
		l.versionByConsumer[consumerURI] = version
	} else if _, present := l.versionByConsumer[consumerURI]; !present {
		l.versionByConsumer[consumerURI] = ""
	}
	if l.fragmentsByCons[consumerURI] == nil {
		l.fragmentsByCons[consumerURI] = map[string]struct{}{}
	}
	if fragment != "" {
		l.fragmentsByCons[consumerURI][fragment] = struct{}{}
	}
}

func (s *walkState) addMappingLibrary(libURI, version, consumerURI, fragment string) {
	if s.mappingLibs[libURI] == nil {
		s.mappingLibs[libURI] = &pendingLibrary{
			versionByConsumer: map[string]string{},
			fragmentsByCons:   map[string]map[string]struct{}{},
		}
	}
	l := s.mappingLibs[libURI]
	if version != "" {
		l.versionByConsumer[consumerURI] = version
	} else if _, present := l.versionByConsumer[consumerURI]; !present {
		l.versionByConsumer[consumerURI] = ""
	}
	if l.fragmentsByCons[consumerURI] == nil {
		l.fragmentsByCons[consumerURI] = map[string]struct{}{}
	}
	if fragment != "" {
		l.fragmentsByCons[consumerURI][fragment] = struct{}{}
	}
}

func (s *walkState) addWorkloadLibrary(libURI, version, consumerURI, fragment string) {
	if s.workloadLibs[libURI] == nil {
		s.workloadLibs[libURI] = &pendingLibrary{
			versionByConsumer: map[string]string{},
			fragmentsByCons:   map[string]map[string]struct{}{},
		}
	}
	l := s.workloadLibs[libURI]
	if version != "" {
		l.versionByConsumer[consumerURI] = version
	} else if _, present := l.versionByConsumer[consumerURI]; !present {
		l.versionByConsumer[consumerURI] = ""
	}
	if l.fragmentsByCons[consumerURI] == nil {
		l.fragmentsByCons[consumerURI] = map[string]struct{}{}
	}
	if fragment != "" {
		l.fragmentsByCons[consumerURI][fragment] = struct{}{}
	}
}

func (s *walkState) addArtifactsLibrary(libURI, version, consumerURI, fragment string) {
	if s.artifactsLibs[libURI] == nil {
		s.artifactsLibs[libURI] = &pendingLibrary{
			versionByConsumer: map[string]string{},
			fragmentsByCons:   map[string]map[string]struct{}{},
		}
	}
	l := s.artifactsLibs[libURI]
	if version != "" {
		l.versionByConsumer[consumerURI] = version
	} else if _, present := l.versionByConsumer[consumerURI]; !present {
		l.versionByConsumer[consumerURI] = ""
	}
	if l.fragmentsByCons[consumerURI] == nil {
		l.fragmentsByCons[consumerURI] = map[string]struct{}{}
	}
	if fragment != "" {
		l.fragmentsByCons[consumerURI][fragment] = struct{}{}
	}
}

// toResolvedRef materialises a ResolvedRef once the resolveAll pass
// has populated the resolution pointer. The URI is supplied by the
// caller since pendingRef itself is map-keyed by URI.
func (p *pendingRef) toResolvedRef(uri string) ResolvedRef {
	refs := make([]string, 0, len(p.referencedFrom))
	for r := range p.referencedFrom {
		refs = append(refs, r)
	}
	return ResolvedRef{
		URI:             uri,
		ResolvedVersion: p.resolution.ResolvedVersion,
		Digest:          p.resolution.Digest,
		ManifestDigest:  p.resolution.ManifestDigest,
		ResolutionPath:  p.resolution.ResolutionPath,
		ReferencedFrom:  refs,
		KindHint:        p.kindHint,
	}
}

func (p *pendingLibrary) toResolvedLibrary(uri string) ResolvedLibrary {
	consumers := make([]LibraryConsumer, 0, len(p.fragmentsByCons))
	for cons, fragSet := range p.fragmentsByCons {
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
		out.ResolutionPath = p.resolution.ResolutionPath
	}
	return out
}

// libraryFromResolved seeds pendingLibrary state from a ResolvedLibrary
// (used by WalkSchemaRefs / WalkMappingImport to merge new
// $ref:/$import: discoveries into an existing closure).
func libraryFromResolved(lib ResolvedLibrary) *pendingLibrary {
	p := &pendingLibrary{
		versionByConsumer: map[string]string{},
		fragmentsByCons:   map[string]map[string]struct{}{},
	}
	for _, c := range lib.Consumers {
		set := map[string]struct{}{}
		for _, f := range c.Fragments {
			set[f] = struct{}{}
		}
		p.fragmentsByCons[c.ConsumerURI] = set
		p.versionByConsumer[c.ConsumerURI] = lib.ResolvedVersion
	}
	if lib.ResolvedVersion != "" || lib.Digest != "" {
		p.resolution = &LibraryResolution{
			Resolution: Resolution{
				ResolvedVersion: lib.ResolvedVersion,
				Digest:          lib.Digest,
				ResolutionPath:  lib.ResolutionPath,
			},
		}
	}
	return p
}
