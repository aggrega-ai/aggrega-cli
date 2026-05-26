// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

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
func ComputeClosure(
	release *types.Release,
	manifest *types.Manifest,
	aggregate *types.Aggregate,
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
	entities    map[string]*pendingRef
	wasmPlugins map[string]*pendingRef
	schemaLibs  map[string]*pendingLibrary
	mappingLibs map[string]*pendingLibrary
}

// pendingRef collects the per-URI accumulating data the walker
// builds during the descent: a deduplicated set of authoring-site
// paths plus, once the resolveAll pass has run, the looked-up
// Resolution metadata.
type pendingRef struct {
	referencedFrom map[string]struct{}
	resolution     *Resolution
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
		entities:    map[string]*pendingRef{},
		wasmPlugins: map[string]*pendingRef{},
		schemaLibs:  map[string]*pendingLibrary{},
		mappingLibs: map[string]*pendingLibrary{},
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

	if in.aggregate != nil {
		s.walkAggregate(in.aggregate, "aggregate")
	}

	if in.manifest != nil {
		s.walkManifest(in.manifest)
	}

	s.walkReleaseAttachments(in.release)
	s.walkWorkspacePlugins(in.workspaceCfg)

	return nil
}

// walkAggregate descends Aggregate.Spec.{Components, Subsystems,
// Aggregates} recursively, recording every type URI it touches.
func (s *walkState) walkAggregate(agg *types.Aggregate, path string) {
	if agg == nil {
		return
	}
	for name, ci := range agg.Spec.Components.All() {
		instancePath := fmt.Sprintf("%s.spec.components.%s.type", path, name)
		if ci.Type != "" {
			s.addEntity(ci.Type, instancePath)
		}
		s.walkComponentInstanceFeatures(ci, fmt.Sprintf("%s.spec.components.%s", path, name))
	}
	for name, sub := range agg.Spec.Aggregates.All() {
		subPath := fmt.Sprintf("%s.spec.aggregates.%s.type", path, name)
		if sub.Type != "" {
			s.addEntity(sub.Type, subPath)
		}
	}
	for name, sys := range agg.Spec.Subsystems.All() {
		subPath := fmt.Sprintf("%s.spec.subsystems.%s", path, name)
		s.walkSubsystem(sys, subPath)
	}
}

// walkSubsystem recurses into Subsystem.Subsystems (recursive),
// Subsystem.Components, and Subsystem.Features.
func (s *walkState) walkSubsystem(sys types.Subsystem, path string) {
	for name, ci := range sys.Components.All() {
		instancePath := fmt.Sprintf("%s.components.%s.type", path, name)
		if ci.Type != "" {
			s.addEntity(ci.Type, instancePath)
		}
		s.walkComponentInstanceFeatures(ci, fmt.Sprintf("%s.components.%s", path, name))
	}
	for name, fi := range sys.Features.All() {
		instancePath := fmt.Sprintf("%s.features.%s.type", path, name)
		if fi.Type != "" {
			s.addEntity(fi.Type, instancePath)
		}
	}
	for name, sub := range sys.Subsystems.All() {
		s.walkSubsystem(sub, fmt.Sprintf("%s.subsystems.%s", path, name))
	}
}

// walkComponentInstanceFeatures collects every Feature URI declared
// via the component-instance's Features and Secrets maps.
func (s *walkState) walkComponentInstanceFeatures(ci types.ComponentInstance, path string) {
	for name, fi := range ci.Features.All() {
		if fi.Type != "" {
			s.addEntity(fi.Type, fmt.Sprintf("%s.features.%s.type", path, name))
		}
	}
	for name, si := range ci.Secrets.All() {
		if si.Type != "" {
			s.addEntity(si.Type, fmt.Sprintf("%s.secrets.%s.type", path, name))
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
			s.addEntity(componentType, fmt.Sprintf("manifest.spec.mappings.components.type.%s", componentType))
			// The mapping value is the Ops-side Recipe URI.
			if recipeURI != "" {
				s.addEntity(recipeURI, fmt.Sprintf("manifest.spec.mappings.components.type.%s", componentType))
			}
		}
	}
	if m.Spec.Mappings.Features != nil {
		for featureType, frURI := range m.Spec.Mappings.Features.Type.All() {
			// The mapping key is the developer-declared Feature URI.
			s.addEntity(featureType, fmt.Sprintf("manifest.spec.mappings.features.type.%s", featureType))
			// The mapping value is the Ops-side FeatureRecipe URI.
			if frURI != "" {
				s.addEntity(frURI, fmt.Sprintf("manifest.spec.mappings.features.type.%s", featureType))
			}
		}
	}
}

// walkReleaseAttachments descends Release.Spec.Boundaries recursively
// and collects boundary-attached Features / Secrets and per-component
// Features / Secrets type URIs.
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
				s.addEntity(bf.Type, fmt.Sprintf("%s.features.%s.type", compPath, fname))
			}
		}
		for sname, bs := range bc.Secrets.All() {
			if bs.Type != "" {
				s.addEntity(bs.Type, fmt.Sprintf("%s.secrets.%s.type", compPath, sname))
			}
		}
	}
}

// walkWorkspacePlugins collects URIs from the four workspace-level
// plugin sockets (mutator, policy, linter, importer).
func (s *walkState) walkWorkspacePlugins(cfg workspaceconfig.Config) {
	for _, p := range cfg.Plugins.Mutator {
		s.addWasmPlugin(p.URI, ".aggrega-config.yaml.plugins.mutator")
	}
	for _, p := range cfg.Plugins.Policy {
		s.addWasmPlugin(p.URI, ".aggrega-config.yaml.plugins.policy")
	}
	for _, p := range cfg.Plugins.Linter {
		s.addWasmPlugin(p.URI, ".aggrega-config.yaml.plugins.linter")
	}
	for _, p := range cfg.Plugins.Importer {
		s.addWasmPlugin(p.URI, ".aggrega-config.yaml.plugins.importer")
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
// Non-oci:// $ref: values (JSON-Schema local anchors like
// "#/$defs/X", file-system relative paths) are silently ignored
// as out-of-scope: the walker only tracks cross-library references
// via the file-import vocabulary's `oci://...` URI shape.
// Malformed `oci://` $ref: values that don't match the canonical
// library-reference shape return a wrapped ErrInvalidLibraryRef
// naming the offending string.
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
				if strings.HasPrefix(valNode.Value, "oci://") {
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

// recordSchemaRef parses a $ref: value and records the resulting
// library URI + consumer + fragment edge under the schema-library
// pending map.
func (s *walkState) recordSchemaRef(refValue, consumerURI string) error {
	libURI, version, fragment, err := parseLibraryRef(refValue)
	if err != nil {
		return err
	}
	s.addSchemaLibrary(libURI, version, consumerURI, formatFragmentRef(fragment))
	return nil
}

// WalkMappingImport inspects a valuesMapping template body for an
// $import: declaration at the field-root position (per the
// file-import vocabulary spec: $import: is whole-content
// replacement at the field root only; never embedded). If the
// first non-whitespace token of the body is `$import: <url>`,
// the URL is parsed and recorded as a mapping-library reference
// under the consumer URI.
//
// Commented-out or deeper-embedded $import: patterns are silently
// ignored — the schema-validation layer rejects them at parse
// time; the walker can safely scan only the first token.
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
	libURI, version, fragment, err := parseLibraryRef(url)
	if err != nil {
		return err
	}
	s.addMappingLibrary(libURI, version, consumerURI, formatFragmentRef(fragment))
	return nil
}

// resolveAll iterates the pending entity / wasm plugin / library
// maps and invokes the Lookup interface per URI. NotFound on a
// catalog entity is a fatal closure gap and returns
// ErrUnresolvableURI. Library resolution feeds the minimum
// version selection pass before invoking the library lookup.
func (s *walkState) resolveAll(lookup Lookup) error {
	if err := s.resolveEntities(lookup); err != nil {
		return err
	}
	if err := s.resolveWasmPlugins(lookup); err != nil {
		return err
	}
	if err := s.resolveLibraries(lookup, true /* schema */); err != nil {
		return err
	}
	return s.resolveLibraries(lookup, false /* mapping */)
}

func (s *walkState) resolveEntities(lookup Lookup) error {
	for uri, pending := range s.entities {
		res, err := lookup.ResolveCatalogEntity(uri)
		if err != nil {
			return fmt.Errorf("closure: resolve entity %q: %w", uri, err)
		}
		if res.NotFound {
			return fmt.Errorf("%w: entity %q (referenced from %s)",
				ErrUnresolvableURI, uri, referencedFromPreview(pending.referencedFrom))
		}
		pending.resolution = &res
	}
	return nil
}

func (s *walkState) resolveWasmPlugins(lookup Lookup) error {
	for uri, pending := range s.wasmPlugins {
		res, err := lookup.ResolveWasmPlugin(uri)
		if err != nil {
			return fmt.Errorf("closure: resolve wasm plugin %q: %w", uri, err)
		}
		if res.NotFound {
			return fmt.Errorf("%w: wasm plugin %q (referenced from %s)",
				ErrUnresolvableURI, uri, referencedFromPreview(pending.referencedFrom))
		}
		pending.resolution = &res
	}
	return nil
}

// resolveLibraries applies minimum version selection (MVS) across
// the per-library consumer→version mapping, then invokes the
// appropriate Lookup method to obtain digest + resolution-path
// metadata.
func (s *walkState) resolveLibraries(lookup Lookup, schemas bool) error {
	pending := s.schemaLibs
	if !schemas {
		pending = s.mappingLibs
	}
	for uri, lib := range pending {
		selected, err := selectMVSVersion(uri, lib.versionByConsumer)
		if err != nil {
			return err
		}
		var res LibraryResolution
		if schemas {
			res, err = lookup.ResolveSchemaLibrary(uri)
		} else {
			res, err = lookup.ResolveMappingLibrary(uri)
		}
		if err != nil {
			return fmt.Errorf("closure: resolve library %q: %w", uri, err)
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
	for uri, p := range s.wasmPlugins {
		if p.resolution == nil {
			continue
		}
		c.WasmPlugins = append(c.WasmPlugins, p.toResolvedRef(uri))
	}
	c.SchemaLibraries = s.materialiseSchemaLibs()
	c.MappingLibraries = s.materialiseMappingLibs()
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

// canonicaliseClosure sorts every slice field by its canonical
// key (URI for top-level slices; ConsumerURI for Consumers;
// lexicographic for Fragments and ReferencedFrom). Two
// independent walks of the same input produce identical Closure
// bytes after this pass.
func canonicaliseClosure(c *Closure) {
	sort.Slice(c.Entities, func(i, j int) bool { return c.Entities[i].URI < c.Entities[j].URI })
	sort.Slice(c.WasmPlugins, func(i, j int) bool { return c.WasmPlugins[i].URI < c.WasmPlugins[j].URI })
	sort.Slice(c.SchemaLibraries, func(i, j int) bool { return c.SchemaLibraries[i].URI < c.SchemaLibraries[j].URI })
	sort.Slice(c.MappingLibraries, func(i, j int) bool { return c.MappingLibraries[i].URI < c.MappingLibraries[j].URI })

	for i := range c.Entities {
		sort.Strings(c.Entities[i].ReferencedFrom)
	}
	for i := range c.WasmPlugins {
		sort.Strings(c.WasmPlugins[i].ReferencedFrom)
	}
	for i := range c.SchemaLibraries {
		sortLibrary(&c.SchemaLibraries[i])
	}
	for i := range c.MappingLibraries {
		sortLibrary(&c.MappingLibraries[i])
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

// addEntity / addWasmPlugin / addSchemaLibrary / addMappingLibrary
// are the structural accumulator entry points used by every per-
// axis sub-walker.

func (s *walkState) addEntity(uri, refPath string) {
	if s.entities[uri] == nil {
		s.entities[uri] = &pendingRef{referencedFrom: map[string]struct{}{}}
	}
	s.entities[uri].referencedFrom[refPath] = struct{}{}
}

func (s *walkState) addWasmPlugin(uri, refPath string) {
	if uri == "" {
		return
	}
	if s.wasmPlugins[uri] == nil {
		s.wasmPlugins[uri] = &pendingRef{referencedFrom: map[string]struct{}{}}
	}
	s.wasmPlugins[uri].referencedFrom[refPath] = struct{}{}
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
		ResolutionPath:  p.resolution.ResolutionPath,
		ReferencedFrom:  refs,
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
