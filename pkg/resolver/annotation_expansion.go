// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Side-string values naming the two
// authoring surfaces an annotation can originate from: Aggregate (the
// Developer-side authoring surface) and Release (the Operator-side
// authoring surface). The Aggregate-as-Authoring-Surface invariant
// partitions binding paths by pillar — Aggregate-side annotations may
// only route through values-pillar paths; Release-side annotations may
// only route through infra-pillar paths.
const (
	sideAggregate = "aggregate"
	sideRelease   = "release"
)

// featureSchemaCache memoizes compactform.Compile output for each
// distinct target Feature observed during one ExpandAnnotations
// invocation. The annotation expansion pass invokes
// validateSubkeyValueAgainstFeatureSchema once per (component,
// annotation, subkey) tuple; without caching, the same Feature
// schema body re-compiles N times where N is the per-Feature subkey
// fan-out. The cache collapses N compiles into one.
//
// Scope. Per-call lifetime — ExpandAnnotations constructs the cache
// at entry and discards it on return. No cross-build state, no
// process-wide singleton, no on-disk persistence; subsequent build
// invocations populate fresh caches. The discipline avoids the
// staleness window an inter-build cache would open if a Feature
// schema changes between builds.
//
// Key. The versioned Feature URI form
// (Metadata.Name + ":" + Metadata.Version, or bare Metadata.Name
// when Version is empty) via [versionedName]. Keying on the
// versioned URI matches the Manifest binding declaration shape
// (binding.Feature carries the versioned URI today) and stays
// correct if multi-version coexistence per Feature name lands in
// the catalog layer later — distinct versions would compile and
// cache separately rather than collide on a shared bare-name key.
//
// Negative caching. A compactform.Compile failure caches as
// (zero, false); subsequent requests for the same Feature return
// the cached negative without re-attempting the compile. The
// validator's soft-skip-on-compile-error path treats a cached
// negative identically to a fresh negative, so author behavior is
// unchanged.
//
// Test observability. hits and misses count cache lookups; tests
// inspect the counters directly to assert one-compile-per-feature
// without instrumenting compactform.Compile.
type featureSchemaCache struct {
	entries map[string]featureSchemaCacheEntry
	// effectiveSchema, when non-nil, returns the composed (parent+child) schema for
	// an extends-ing Feature. schemaFor prefers it over compiling the child-only
	// authored schema, so the annotation path validates an extends-ing Feature
	// against the same composed schema the main conformance consumers use. Nil →
	// structural-only (no lookup / a Feature with no extends composition).
	effectiveSchema func(typeURI string) (compactform.Schema, bool)
	hits            int
	misses          int
}

// featureSchemaCacheEntry is the per-Feature cached compile output.
// ok=true carries a successful compile in compiled; ok=false carries
// a cached compile failure (compiled is the zero compactform.Schema).
type featureSchemaCacheEntry struct {
	compiled compactform.Schema
	ok       bool
}

// newFeatureSchemaCache returns an empty cache ready for use across
// one ExpandAnnotations invocation.
func newFeatureSchemaCache(effectiveSchema func(typeURI string) (compactform.Schema, bool)) *featureSchemaCache {
	return &featureSchemaCache{entries: map[string]featureSchemaCacheEntry{}, effectiveSchema: effectiveSchema}
}

// schemaFor returns the compiled JSON Schema 2020-12 form of the
// Feature's spec.schema, compiling lazily on the first request and
// returning the cached result on subsequent requests for the same
// versioned Feature URI. Returns (compiled, true) when the schema
// compiled cleanly; (zero, false) when the compile failed.
//
// Nil-safe. A nil receiver falls through to direct
// compactform.Compile with no memoization, preserving the
// uncached behavior for any future caller that wires
// validateSubkeyValueAgainstFeatureSchema without constructing a
// cache. The expansion paths created in this package always pass
// a non-nil cache.
//
// A nil feature returns (zero, false) and increments no counter —
// the validator handles nil feature defensively elsewhere.
func (c *featureSchemaCache) schemaFor(feature *types.Feature) (compactform.Schema, bool) {
	if feature == nil {
		return nil, false
	}
	if c == nil {
		compiled, err := compactform.Compile(&feature.Spec.Schema)
		return compiled, err == nil
	}
	key := versionedName(feature)
	if entry, has := c.entries[key]; has {
		c.hits++
		return entry.compiled, entry.ok
	}
	c.misses++
	// Prefer the composed effective schema (extends parent+child) so an extends-ing
	// Feature is validated against the same schema the main consumers use; fall back
	// to the child-only authored schema when there is no composition.
	if c.effectiveSchema != nil {
		if eff, ok := c.effectiveSchema(key); ok {
			entry := featureSchemaCacheEntry{compiled: eff, ok: true}
			c.entries[key] = entry
			return entry.compiled, entry.ok
		}
	}
	compiled, err := compactform.Compile(&feature.Spec.Schema)
	entry := featureSchemaCacheEntry{compiled: compiled, ok: err == nil}
	c.entries[key] = entry
	return entry.compiled, entry.ok
}

// versionedName returns the canonical versioned Feature URI string
// used as the featureSchemaCache key. Format: "<name>:<version>"
// when Metadata.Version is non-empty, bare "<name>" otherwise.
// The form matches the Manifest binding.Feature URI convention and
// differentiates distinct versions of a Feature should the catalog
// later support multi-version coexistence per name.
func versionedName(feature *types.Feature) string {
	if feature.Metadata.Version == "" {
		return feature.Metadata.Name
	}
	return feature.Metadata.Name + ":" + feature.Metadata.Version
}

// ExpandAnnotations runs the annotation-driven Feature expansion pass.
// The pass reads the resolved Manifest's flat features.annotations
// bindings, walks both authoring trees (Release boundary tree and
// Aggregate subsystem-and-component tree) with parent-to-child
// inheritance, computes the effective annotation set per component, and
// injects a synthetic Feature attachment for every effective annotation
// that matches a registered binding. On the Release side, the synthetic
// attachment lands on rr.Release.Spec.Boundaries[].Components[].Features
// as a BoundaryFeature; on the Aggregate side, the synthetic attachment
// lands on rr.Aggregate.Spec.{Components, Subsystems[…].Components}[].Features
// as a FeatureInstance.
//
// The pass mutates rr.Release and rr.Aggregate in place by design —
// synthetic Feature attachments are indistinguishable from explicit
// attachments at the type level, so downstream phases (the guards pass,
// topology build, IR construction, and rendering) consume them without
// reconciling two sources. Authors who serialize a resolved Release or
// Aggregate for debug see author intent + synthetic injection mingled;
// the deterministic-output invariant holds because the pass iterates
// author-ordered OrderedMaps deterministically.
//
// Side-vs-pillar partition: Aggregate-side annotations may only route
// through binding paths in Maps.Values; Release-side annotations may
// only route through Maps.Infra. A mismatch halts the build, but not
// here — the rule is asked over the authored entry by a declared check
// one step ahead of this pass, and by the time expansion runs a crossed
// entry has already been refused. What expansion itself does with one is
// route it nowhere and return no error, so this pass is not the place to
// read for what refuses a crossed annotation. An annotation whose
// binding declares paths in both pillars and is authored from one side
// fires only the side-compatible subset; the cross-pillar partition is
// the normal cross-side-composition scenario, not a violation.
//
// Pipeline placement: the orchestrator invokes ExpandAnnotations
// between Resolve and RunGuards, so an expanded attachment is
// indistinguishable from an explicitly authored one everywhere
// downstream. A component whose presence mandate is satisfied only by
// an annotation-driven attachment therefore passes: the mandate tests
// the attachment set, not how each attachment got there.
//
// Null-opt-out: explicit YAML `null` on an annotation key removes the
// inherited binding from the overriding boundary or component. The
// cascade carries the pointer-tristate from Boundary.Annotations and
// BoundaryComponent.Annotations (both OrderedMap[*string]) through to
// the per-component effective set; nil-pointer entries are skipped at
// injection time and do not produce synthetic Feature attachments.
// Sibling components in the same boundary that did not opt out still
// receive the inherited binding.
func ExpandAnnotations(rr *ResolveResult, catalogLookup CatalogLookup) error {
	if rr == nil {
		return nil
	}
	registry := BuildAnnotationRegistry(rr.Manifest)
	if registry.Len() == 0 {
		return nil
	}
	if rr.AnnotationProvenance == nil {
		rr.AnnotationProvenance = map[string]AnnotationProvenance{}
	}
	cache := newFeatureSchemaCache(catalogLookup.EffectiveSchema)

	if rr.Release != nil {
		if err := expandBoundaries(registry, catalogLookup, cache, rr); err != nil {
			return err
		}
	}

	if rr.Aggregate != nil {
		// The subsystem descent is complete on its own: a component authored at the
		// top-level `.spec.components` surface reaches it inside the synthesized
		// default subsystem, because the value walked is a normalized Aggregate —
		// obtainable only from types.Normalize, which folds that surface into the
		// default and empties the root map — and the authoring shape declaring both
		// a root components map and subsystems is rejected at the document-envelope
		// gate before any Aggregate is parsed. So there is no root-components pass;
		// adding one back would tell a reader the shape is reachable.
		//
		// The normalized value is stored back onto rr before the descent, because
		// this pass MUTATES the Aggregate (it writes synthesized attachments into
		// each component's Features) and the writer reads rr.Aggregate afresh per
		// component. Normalizing without storing would let the writes land in a
		// value the caller never sees.
		na := types.Normalize(rr.Aggregate)
		rr.Aggregate = na.Aggregate()
		if err := expandAggregateSubsystems(na, registry, catalogLookup, cache, rr); err != nil {
			return err
		}
	}
	return nil
}

// annotationCascade is the per-scope state an annotation expansion carries down a
// container tree: the effective annotation values at that scope, and per key the
// authoring location the value was written at. The source is the authoring scope,
// NOT the scope where the value currently applies — an inherited annotation
// reports the scope it was written at, which is what makes provenance point at the
// text the author can edit.
type annotationCascade struct {
	values  types.OrderedMap[*types.AnnotationValue]
	sources map[string]string
}

// withScopeAnnotations layers one scope's own annotations onto the inherited
// cascade, recording that scope as the source of every key it declares. sourceOf
// formats the authoring location for a key at this scope.
func (c annotationCascade) withScopeAnnotations(own types.OrderedMap[*types.AnnotationValue], sourceOf func(key string) string) annotationCascade {
	next := annotationCascade{
		values:  mergeAnnotationValueMaps(c.values, own),
		sources: make(map[string]string, len(c.sources)+own.Len()),
	}
	for k, v := range c.sources {
		next.sources[k] = v
	}
	for k := range own.All() {
		next.sources[k] = sourceOf(k)
	}
	return next
}

// expandBoundaries walks the Release boundary tree, cascading annotation values
// and their source locations from outer to inner. Nil-pointer values in the
// effective map are opt-out signals; expansion skips them without injecting a
// synthetic Feature and without recording provenance.
//
// The mutated component is written back into the tree per component rather than
// once at the end, for the same reason the Aggregate side does it — see
// [expandAggregateSubsystems].
func expandBoundaries(
	registry *AnnotationRegistry,
	catalogLookup CatalogLookup,
	cache *featureSchemaCache,
	rr *ResolveResult,
) error {
	return types.WalkInstanceTree(rr.Release.Spec.Boundaries, types.BoundaryTree(), "",
		annotationCascade{sources: map[string]string{}},
		types.InstanceTreeVisitor[types.Boundary, types.BoundaryComponent, annotationCascade]{
			Path: joinBoundaryPath,
			Enter: func(b types.Boundary, _, path string, inherited annotationCascade) annotationCascade {
				return inherited.withScopeAnnotations(b.Annotations, func(key string) string {
					return annotationSourcePath(rr.ReleasePath, path, "", key, b.Synthesized)
				})
			},
			Visit: func(comp types.BoundaryComponent, name, path string, synthesized bool, cascade annotationCascade) error {
				effective := cascade.withScopeAnnotations(comp.Annotations, func(key string) string {
					return annotationSourcePath(rr.ReleasePath, path, name, key, synthesized)
				})

				for key, value := range effective.values.All() {
					if value.IsEmpty() {
						// Whole-value opt-out: no synthetic Feature for this key on this
						// component; no provenance record.
						continue
					}
					binding, ok := registry.Lookup(key)
					if !ok {
						continue
					}
					feature, errs := expandBinding(key, value, binding, sideRelease, lookupFeature(catalogLookup, binding.Feature), cache, path, name, effective.sources[key])
					if len(errs) > 0 {
						return wrapAnnotationOpsError(path, name, key, "expansion",
							rr.AnnotationProvenance[AnnotationProvenanceKey(path, name, key)].InjectedByPlugin,
							errors.Join(errs...))
					}
					if _, already := comp.Features.Get(key); already {
						continue
					}
					comp.Features.Set(key, feature)
					recordAnnotationProvenance(rr, AnnotationProvenanceKey(path, name, key), key, effective.sources[key])
				}

				writeComponent(rr.Release, path, name, comp)
				return nil
			},
		})
}

// annotationSourcePath formats the source location for an annotation
// declaration written in the Release, in the shape [annotationSource] composes,
// with no line number. componentName empty signals a boundary-level annotation;
// non-empty signals a component-level annotation under that boundary.
func annotationSourcePath(releasePath, boundaryPath, componentName, key string, synthesized bool) string {
	// A synthesized-default boundary holds components authored at the top-level
	// `.spec.components` surface; attribute their annotations to that root shape
	// rather than the generated `boundaries.default` scope. Keys on the
	// provenance flag, not the path string.
	if synthesized {
		if componentName != "" {
			return annotationSource(releasePath, "components."+componentName+".annotations."+key)
		}
		return annotationSource(releasePath, "annotations."+key)
	}
	dotted := "boundaries." + strings.ReplaceAll(boundaryPath, "/", ".boundaries.")
	if componentName != "" {
		return annotationSource(releasePath, dotted+".components."+componentName+".annotations."+key)
	}
	return annotationSource(releasePath, dotted+".annotations."+key)
}

// annotationSource composes the source an annotation is traced to: the file the
// annotation was written in, a colon, and the dotted key path inside that file.
//
// Where the run names no file for the document, the source is the key path alone.
// No file name is put in the empty place: a name the run was not given is a name
// the author may not have written, and a source naming it sends them to a file
// that is not the one holding the annotation. The key path is still exactly what
// they wrote, so it travels whole.
//
// It is the one composition both authoring documents' formatters use, so the two
// sides of an annotation's provenance cannot come to be spelled differently.
func annotationSource(file, keyPath string) string {
	if file == "" {
		return keyPath
	}
	return file + ":" + keyPath
}

// expandBinding consumes an annotation value plus its binding and
// produces the synthetic BoundaryFeature payload for injection on the
// Release side. The binding's Maps groups dotted-path subkey routes by
// target pillar (Maps.Infra, Maps.Values); only Maps.Infra paths fire
// on the Release side per the side-vs-pillar invariant.
//
// Scalar values route to the binding's single declared path (the
// scalar-and-multi-map ambiguity check fires for bindings declaring
// more than one path total). Structured values route per subkey: each
// subkey looks up its target pillar via Maps.Lookup, gates through
// side-vs-pillar enforcement (subkey routing to values pillar fails
// on the Release side), validates the subvalue's Go type against the
// target Feature's spec.schema at the routed path, and on success
// writes the leaf value into the synthetic Feature's Infra pillar at
// the dotted-path leaf.
//
// Per-subkey errors compose via errors.Join (collect-and-emit-all);
// the caller sees every authoring problem per build cycle rather
// than fix-rebuild-fix-rebuild cycling. The orchestrator's error
// printer walks Unwrap() []error to render each constituent on its
// own line.
//
// An empty AnnotationValue (whole-value opt-out) reaches here only as
// a defensive case — the cascade-flatten step filters opt-outs
// upstream. The function returns an empty BoundaryFeature with no
// pillar contributions in that case.
//
// Path-target validation against the target Feature's spec.schema
// runs upstream at catalog load (ValidateAnnotationBindingTargets);
// this function's per-subkey type validation is the defensive
// expansion-time backstop for bindings whose paths the load-time
// check soft-skipped (e.g., paths under a $ref subtree).
func expandBinding(
	key string,
	value *types.AnnotationValue,
	binding types.AnnotationBinding,
	side string,
	feature *types.Feature,
	cache *featureSchemaCache,
	boundaryPath, componentName, sourceLocation string,
) (types.BoundaryFeature, []error) {
	out := types.BoundaryFeature{Type: binding.Feature}
	if value.IsEmpty() {
		return out, nil
	}
	if value.IsScalar() {
		return expandBindingScalar(out, key, *value.Scalar, binding, side, feature, cache, sourceLocation)
	}
	return expandBindingStructured(out, key, *value, binding, side, feature, cache, sourceLocation)
}

// expandBindingScalar handles the single-path scalar branch. The
// binding must declare exactly one path across both pillar lists, or
// the value is refused as one that does not route. Whether the routed
// path lies in the authoring side's pillar, and whether the value
// conforms to the target Feature's schema there, are declared rules
// asked over the authored entry before expansion runs.
func expandBindingScalar(
	out types.BoundaryFeature,
	key string,
	value string,
	binding types.AnnotationBinding,
	side string,
	feature *types.Feature,
	cache *featureSchemaCache,
	sourceLocation string,
) (types.BoundaryFeature, []error) {
	path, pillar, err := resolveBindingTarget(binding, value)
	if err != nil {
		return out, []error{wrapAnnotationOpsErrorStage(key, "parse", err)}
	}
	if value == "" {
		return out, nil
	}
	if pillar == "infra" {
		types.SetLeafAtPath(&out.Infra, path, value)
	}
	// A Release-side binding whose single path lives in Maps.Values contributes
	// nothing here. Which side may route through which pillar is a declared rule
	// asked over the authored entry before expansion runs, so a document that
	// crossed the partition has already halted and the gate above is the routing
	// itself rather than a second reading of that rule.
	return out, nil
}

// expandBindingStructured handles the structured-value branch. Each
// subkey in the AnnotationValue routes per Maps.Lookup; per-subkey
// errors collect across the iteration so the caller sees every
// problem per build cycle. Subkeys that pass all checks fire their
// pillar contribution; subkeys whose checks fail accumulate to the
// returned error slice and the corresponding leaf does NOT fire.
//
// The successful subkeys still populate the contribution even when
// other subkeys failed — partial contribution is preserved as the
// best-effort diagnostic posture. The caller halts the build via
// errors.Join when the error slice is non-empty; the partial
// contribution never reaches the IR.
//
// Parallel structure: the body intentionally mirrors the structured
// branch of [synthesizeAggregateFeatureFromAnnotation] (Aggregate
// side) — same per-subkey iteration, same Maps.Lookup routing, same
// refusal of a subkey the binding does not route, same
// collect-and-emit-all discipline. The two helpers differ only in
// which pillar they write to (Infra vs Values) and which return-struct
// type wraps the contribution (BoundaryFeature vs FeatureInstance).
// Future maintainers touching one MUST touch the other in lockstep —
// divergence between sides silently breaks cross-side composition.
func expandBindingStructured(
	out types.BoundaryFeature,
	key string,
	value types.AnnotationValue,
	binding types.AnnotationBinding,
	side string,
	feature *types.Feature,
	cache *featureSchemaCache,
	sourceLocation string,
) (types.BoundaryFeature, []error) {
	var errs []error
	for subkey, subvalue := range value.Structured.All() {
		pillar, found := binding.Maps.Lookup(subkey)
		if !found {
			// A subkey the binding does not route is refused, under its own condition,
			// by the rule asked over the authored entry before expansion runs, so a
			// build never reaches this branch with one in place. The refusal stays
			// rather than a skip because expansion is reachable without that rule —
			// directly, by a caller that has not asked it — and there an unroutable
			// value would otherwise be dropped with nothing said.
			declared := append(append([]string{}, binding.Maps.Infra...), binding.Maps.Values...)
			errs = append(errs, cerrors.Occurrence(errAnnotationSubkeyNotBound, fmt.Sprintf(
				"annotation subkey %q on key %q at %s is not declared in binding map; declared paths: %v",
				subkey, key, sourceLocation, declared,
			)))
			continue
		}
		if pillar == "infra" {
			types.SetLeafAtPath(&out.Infra, subkey, subvalue)
		}
		// A subkey routing to the other side's pillar contributes nothing here, on
		// the reading the scalar branch states: the partition is a declared rule
		// asked over the authored entry before expansion runs.
	}
	return out, errs
}

// synthesizeAggregateFeatureFromAnnotation produces the synthetic
// FeatureInstance for the Aggregate-side expansion path. Mirrors
// expandBinding's responsibilities (per-subkey routing,
// collect-and-emit-all) but produces a FeatureInstance and writes to
// its Values pillar.
//
// Scalar values route to the binding's single declared path, and
// contribute only where that path lies in the values pillar: an
// Aggregate-side annotation routing through infra is refused by the
// side-vs-pillar rule asked over the authored entry before expansion
// runs. Structured values route per subkey; per-subkey errors collect
// via the same discipline as the Release side.
//
// Parallel structure: the structured branch below intentionally
// mirrors [expandBindingStructured] (Release side) — same per-subkey
// iteration, same Maps.Lookup routing, same refusal of a subkey the
// binding does not route, same collect-and-emit-all discipline. The
// two helpers differ only in which pillar they write to (Values vs
// Infra) and which return-struct type wraps the contribution
// (FeatureInstance vs BoundaryFeature). Future maintainers touching
// one MUST touch the other in lockstep — divergence between sides
// silently breaks cross-side composition.
func synthesizeAggregateFeatureFromAnnotation(
	key string,
	value types.AnnotationValue,
	binding types.AnnotationBinding,
	feature *types.Feature,
	cache *featureSchemaCache,
	sourceLocation string,
) (types.FeatureInstance, []error) {
	out := types.FeatureInstance{Type: binding.Feature}
	if value.IsScalar() {
		path, pillar, err := resolveBindingTarget(binding, *value.Scalar)
		if err != nil {
			return out, []error{wrapAnnotationOpsErrorStage(key, "parse", err)}
		}
		if *value.Scalar != "" && pillar == "values" {
			types.SetLeafAtPath(&out.Values, path, *value.Scalar)
		}
		return out, nil
	}
	if value.IsStructured() {
		var errs []error
		for subkey, subvalue := range value.Structured.All() {
			pillar, found := binding.Maps.Lookup(subkey)
			if !found {
				// Reached only without the rule over the authored entry, for the
				// reason the Release-side branch states.
				declared := append(append([]string{}, binding.Maps.Infra...), binding.Maps.Values...)
				errs = append(errs, cerrors.Occurrence(errAnnotationSubkeyNotBound, fmt.Sprintf(
					"annotation subkey %q on key %q at %s is not declared in binding map; declared paths: %v",
					subkey, key, sourceLocation, declared,
				)))
				continue
			}
			if pillar == "values" {
				types.SetLeafAtPath(&out.Values, subkey, subvalue)
			}
		}
		return out, errs
	}
	return out, nil
}

// wrapAnnotationOpsErrorStage formats a per-annotation-key inner-stage
// failure for collection through the errors.Join chain.
//
// No finding raised inside the expansion carries the category word. Every such
// finding is collected into the per-component error slice and reaches the reader
// through one of the two scope-path wraps — wrapAnnotationOpsError on the Release
// side, wrapAggregateAnnotationError on the Aggregate side — and that wrap is
// the single place the category is supplied, so it is named once per diagnostic
// line however many findings the line joins. An inner site that names it too says
// it twice at the rendered boundary, which is why the category belongs to the
// wrap alone.
//
// Each wrap supplies the domain of the document its annotation value was
// authored in, so a finding raised from both sides is filed under the developer's
// surface when it came from an Aggregate and the operator's when it came from a
// Release. A finding that wrote a category into its own text would assert one of
// those two at a site that cannot tell which side reached it.
//
// The composed shapes are, inner:
//
//	annotation <stage> failed at key "<key>": <reason>
//
// and outer, with the bracketed head composed by the formatter from the category
// the wrap supplies and the condition the wrapped chain names:
//
//	annotation <stage> failed at boundary "<path>" component "<name>" key "<key>"
func wrapAnnotationOpsErrorStage(key, stage string, inner error) error {
	return fmt.Errorf("annotation %s failed at key %q: %w", stage, key, inner)
}

// lookupFeature returns the resolved Feature for a binding's target
// type URI, or nil when the catalog lookup is unavailable (nil
// Feature function field) or the Feature is not present in the
// catalog. The per-subkey type validator and the
// synthesize/expandBinding paths handle nil Feature defensively —
// schema-type validation soft-skips on nil to preserve the
// default-to-skip discipline established for the path-target
// validator.
//
// The nil-tolerant lookup keeps the expansion phase exercisable in
// catalog-less test scenarios (constructing ResolveResult fixtures
// without spinning up a full catalog) and in the orchestrator's
// pre-RunGuards path where the catalog is loaded but a binding's
// target Feature may itself be missing — in which case the
// load-time ValidateAnnotationBindingTargets has already halted the
// build; reaching this fallback means tests are calling Expansion
// directly.
func lookupFeature(catalogLookup CatalogLookup, typeURI string) *types.Feature {
	if catalogLookup.Feature == nil {
		return nil
	}
	feat, found := catalogLookup.Feature(typeURI)
	if !found {
		return nil
	}
	return &feat
}

// resolveBindingTarget reads a binding's Maps and returns the dotted-path
// route plus its target pillar for a scalar annotation value. The binding
// must declare exactly one path across both pillar lists for scalar
// routing to be unambiguous; declaring multiple paths paired with a
// scalar annotation surfaces as the unambiguous-routing error.
//
// An empty annotation value still resolves the (path, pillar) target —
// the empty value populates no leaf, but the caller may need to know
// the routing for provenance recording. A binding with zero declared
// paths is structurally invalid and is rejected at Manifest load by the
// JSON Schema's minProperties / minItems constraints; this function
// returns a clear runtime error if the load-time check has been bypassed.
//
// Every refusal here is an occurrence of ErrAnnotationScalarNotRoutable, built
// through cerrors.Occurrence so its text stays exactly as composed here.
func resolveBindingTarget(binding types.AnnotationBinding, value string) (string, string, error) {
	total := binding.Maps.Len()
	if total == 0 {
		return "", "", cerrors.Occurrence(errAnnotationScalarNotRoutable, fmt.Sprintf(
			"binding for feature %q declares no paths in maps; scalar annotation value cannot route to any subkey",
			binding.Feature))
	}
	if total > 1 && value != "" {
		paths := append(append([]string{}, binding.Maps.Infra...), binding.Maps.Values...)
		return "", "", cerrors.Occurrence(errAnnotationScalarNotRoutable, fmt.Sprintf(
			"binding for feature %q maps multiple subkey paths (%v); scalar annotation value %q cannot route to multiple subkeys unambiguously. Provide separate annotation keys for each path",
			binding.Feature, paths, value))
	}
	if len(binding.Maps.Infra) == 1 {
		return binding.Maps.Infra[0], "infra", nil
	}
	if len(binding.Maps.Values) == 1 {
		return binding.Maps.Values[0], "values", nil
	}
	// total == 0 covered above; total > 1 covered above; remaining case
	// is exactly one path across both lists, handled by the two branches.
	// Defensive fallback for unreachable state.
	return "", "", cerrors.Occurrence(errAnnotationScalarNotRoutable, fmt.Sprintf(
		"binding for feature %q maps shape is structurally invalid", binding.Feature))
}

// writeComponent persists a mutated component back into its containing
// boundary in the Release tree. OrderedMap returns values by value, so
// the cascade walker holds a local copy of each component; this helper
// reads the boundary chain fresh from the Release (picking up any prior
// component writes within the same boundary), Sets the mutated component
// at the leaf, and writes the chain back up to the Release root.
//
// The leaf write goes through [types.SetBoundaryComponent], so a component the
// boundary routes in from a sub-module is written back into the routing entry
// that declares it rather than materialised as a second entry on the boundary's
// own component axis.
func writeComponent(release *types.Release, boundaryPath, compName string, comp types.BoundaryComponent) {
	segments := splitBoundaryPath(boundaryPath)
	if len(segments) == 0 {
		return
	}
	chain := make([]types.Boundary, len(segments))
	top, ok := release.Spec.Boundaries.Get(segments[0])
	if !ok {
		return
	}
	chain[0] = top
	for i := 1; i < len(segments); i++ {
		child, ok := chain[i-1].Boundaries.Get(segments[i])
		if !ok {
			return
		}
		chain[i] = child
	}
	types.SetBoundaryComponent(&chain[len(segments)-1], compName, comp)
	for i := len(segments) - 1; i > 0; i-- {
		chain[i-1].Boundaries.Set(segments[i], chain[i])
	}
	release.Spec.Boundaries.Set(segments[0], chain[0])
}

// joinBoundaryPath composes the slash-separated path of a boundary from its
// parent's path and its own name: "aws-prod" at the top level,
// "aws-prod/eks-main" one level in. It is the inverse of [splitBoundaryPath] and
// the spelling [BoundaryInfra.BoundaryPath] carries.
//
// The synthesized flag is accepted so the function is usable directly as an
// [types.InstanceTreeVisitor.Path]; the path does not vary with it, because a
// component routed into the synthesized default keys under that boundary's name
// like any other and consumers that render it differently key on the flag.
func joinBoundaryPath(parentPath, name string, _ bool) string {
	if parentPath == "" {
		return name
	}
	return parentPath + "/" + name
}

func splitBoundaryPath(path string) []string {
	if path == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			out = append(out, path[start:i])
			start = i + 1
		}
	}
	out = append(out, path[start:])
	return out
}

// releaseCategory is the authoring domain a fault on the Release is reported
// under. It is read from the one home of the kind-to-domain correspondence
// rather than written out here: a category named at a raise site is a second
// copy of that correspondence, and a copy agrees on the day it is written and is
// free to disagree the day either side moves.
//
// It panics on an unmapped kind, for the reason the other resolutions of this
// question panic: the kind is a constant chosen in the compiler rather than read
// from an authored document, so an unmappable one means the correspondence
// stopped covering a kind that raises categorised faults. That is a mistake in
// the compiler, not in anything a user wrote, and it would otherwise surface as
// a fault quietly attributed to the wrong authoring domain.
func releaseCategory() cerrors.Category {
	category, ok := cerrors.ClassifyEntityKind(string(entitykind.Release))
	if !ok {
		panic("resolver: the Release kind belongs to no authoring domain, so an annotation-expansion " +
			"fault on a Release cannot be attributed; extend the entity-kind category mapping to cover it")
	}
	return category
}

// aggregateCategory is the authoring domain a fault on the Aggregate is
// reported under, resolved the same way releaseCategory resolves the Release's
// and for the same reason: the correspondence between an entity kind and the
// domain its faults are filed under has one home, and a category named at a
// raise site is a second copy of it.
//
// It panics on an unmapped kind, as the other resolutions of this question do:
// the kind is a constant chosen in the compiler rather than read from an
// authored document, so an unmappable one means the correspondence stopped
// covering a kind that raises categorised faults. That is a mistake in the
// compiler, not in anything a user wrote, and it would otherwise surface as a
// fault quietly attributed to the wrong authoring domain.
func aggregateCategory() cerrors.Category {
	category, ok := cerrors.ClassifyEntityKind(string(entitykind.Aggregate))
	if !ok {
		panic("resolver: the Aggregate kind belongs to no authoring domain, so an annotation-expansion " +
			"fault on an Aggregate cannot be attributed; extend the entity-kind category mapping to cover it")
	}
	return category
}

// wrapAnnotationOpsError files an annotation-expansion failure under the
// Release's authoring domain and names the scope the inner findings cannot know.
// The bad annotation value lives in the Release, so the domain is the one the
// kind-to-domain correspondence gives the Release, asked for rather than
// restated — see releaseCategory.
//
// The category is passed to the constructor rather than written into the
// sentence, which is what lets the formatter compose the head once — and lets it
// carry the short code of whichever registered condition the wrapped chain
// names, which a hand-written prefix cannot supply.
//
// injectedBy is the plugin that injected the annotation on this component, read
// from the provenance the injecting role recorded, and empty for an annotation an
// author wrote. Where it is set the scope says so, naming the plugin and the
// governance role it is attached at: the coordinate reads as a key in the
// Release, and an operator sent there would look for a value nobody wrote in that
// file, when the value to correct is the one the plugin decided. An authored
// annotation's scope is unchanged.
func wrapAnnotationOpsError(boundaryPath, componentName, key, stage, injectedBy string, inner error) error {
	scope := fmt.Sprintf("annotation %s failed at boundary %q component %q key %q", stage, boundaryPath, componentName, key)
	if injectedBy != "" {
		scope += ", " + injectionAttribution(injectedBy)
	}
	return cerrors.Wrap(inner, releaseCategory(), cerrors.SourceLocation{}, scope)
}

// wrapAggregateAnnotationError is the same wrap on the Aggregate side. The bad
// annotation value lives in the Aggregate, so the domain is the one the
// kind-to-domain correspondence gives the Aggregate, asked for rather than
// restated — see aggregateCategory.
//
// Each wrapper therefore takes the classification of the document its annotation
// value was authored in, and the two differ because their entities do: the same
// condition reported against an Aggregate names the developer's surface and
// against a Release names the operator's. That is what a reader has to act on —
// which file to open — so it follows the document rather than the wrap.
func wrapAggregateAnnotationError(componentPath, key, stage string, inner error) error {
	return cerrors.Wrap(inner, aggregateCategory(), cerrors.SourceLocation{},
		fmt.Sprintf("annotation %s failed at aggregate component %q key %q", stage, componentPath, key))
}

// recordAnnotationProvenance writes the side-table entry for a synthetic Feature
// attachment, preserving a host pre-seeded InjectedByPlugin. The
// annotation-transform role runs before expansion and pre-seeds the side-table
// at the injection coordinate with the injecting plugin's URI; expansion then
// records the annotation key and source at that same coordinate without
// clobbering the provenance the host already stamped, so a host-injected
// annotation's synthetic attachment carries both its trigger and its author.
func recordAnnotationProvenance(rr *ResolveResult, provKey, annotationKey, source string) {
	prov := AnnotationProvenance{AnnotationKey: annotationKey, AnnotationSource: source}
	if existing, ok := rr.AnnotationProvenance[provKey]; ok {
		prov.InjectedByPlugin = existing.InjectedByPlugin
	}
	rr.AnnotationProvenance[provKey] = prov
}

// expandAggregateSubsystems walks the Aggregate's subsystem-and-component tree,
// cascading annotation values and their authoring source locations from outer to
// inner.
//
// For each contained component, every effective annotation that matches a
// registered binding synthesizes a FeatureInstance attachment on the component's
// Features map. Aggregate-side expansion routes only through paths in
// binding.Maps.Values; routing the annotation through an infra-pillar path halts
// with a Loud Guardrail. Empty AnnotationValue entries (whole-value opt-outs)
// produce no synthetic Feature and no provenance record. Structured values are not
// yet routed through expansion — the parse, cascade, and schema layers accept
// them, but per-subkey expansion routing is not implemented; encountering a
// structured value at expansion time surfaces a clear error rather than silently
// dropping the contribution.
//
// The mutated component is written back into the tree per component rather than
// once at the end: OrderedMap hands out its values by value, so the walk holds a
// copy of each component, and a later write must observe the earlier ones in the
// same scope.
func expandAggregateSubsystems(
	na types.NormalizedAggregate,
	registry *AnnotationRegistry,
	catalogLookup CatalogLookup,
	cache *featureSchemaCache,
	rr *ResolveResult,
) error {
	return types.WalkInstanceTree(na.Aggregate().Spec.Subsystems, types.SubsystemTree(), "",
		annotationCascade{sources: map[string]string{}},
		types.InstanceTreeVisitor[types.Subsystem, types.ComponentInstance, annotationCascade]{
			Path: aggregateAnnotationScopePath,
			Enter: func(sub types.Subsystem, _, path string, inherited annotationCascade) annotationCascade {
				return inherited.withScopeAnnotations(sub.Annotations, func(key string) string {
					return aggregateAnnotationSourcePath(rr.AggregatePath, path, "", key, sub.Synthesized)
				})
			},
			Visit: func(comp types.ComponentInstance, name, path string, synthesized bool, cascade annotationCascade) error {
				// componentPath keys the provenance table and the write-back; the
				// authored path is what a reader is shown.
				componentPath := aggregateAnnotationComponentPath(path, name)
				authoredPath := aggregateAuthoredComponentPath(path, name, synthesized)
				effective := cascade.withScopeAnnotations(comp.Annotations, func(key string) string {
					return aggregateAnnotationSourcePath(rr.AggregatePath, path, name, key, synthesized)
				})

				for key, value := range effective.values.All() {
					if value.IsEmpty() {
						continue
					}
					binding, ok := registry.Lookup(key)
					if !ok {
						continue
					}
					if _, already := comp.Features.Get(key); already {
						continue
					}
					feature, errs := synthesizeAggregateFeatureFromAnnotation(key, *value, binding, lookupFeature(catalogLookup, binding.Feature), cache, effective.sources[key])
					if len(errs) > 0 {
						return wrapAggregateAnnotationError(authoredPath, key, "expansion", errors.Join(errs...))
					}
					comp.Features.Set(key, feature)
					recordAnnotationProvenance(rr, AnnotationProvenanceKey(componentPath, "", key), key, effective.sources[key])
				}
				writeAggregateComponent(rr.Aggregate, componentPath, comp)
				return nil
			},
		})
}

// aggregateAnnotationSourcePath formats the source location for an annotation
// declaration written in the Aggregate, in the shape [annotationSource] composes,
// with no line number — the Aggregate-side sibling of [annotationSourcePath].
// componentName empty signals a subsystem-level annotation; non-empty signals a
// component-level annotation in that subsystem. scopePath is the subsystem's
// canonical dotted path, as [aggregateAnnotationScopePath] composes it.
func aggregateAnnotationSourcePath(aggregatePath, scopePath, componentName, key string, synthesized bool) string {
	if componentName == "" {
		if synthesized {
			return annotationSource(aggregatePath, "annotations."+key)
		}
		return annotationSource(aggregatePath, scopePath+".annotations."+key)
	}
	return annotationSource(aggregatePath,
		aggregateAuthoredComponentPath(scopePath, componentName, synthesized)+".annotations."+key)
}

// aggregateAuthoredComponentPath composes the dotted path of one component's
// block as the author wrote it in the Aggregate, from the canonical path of the
// subsystem holding it.
//
// It differs from the canonical path, which keys the effective-annotation and
// provenance tables, in exactly one case. A component authored at the top-level
// `.spec.components` surface is folded into the synthesized default subsystem
// before any walk reaches it, so its canonical path names a scope no file
// spells; this names the top-level shape the author did write. It keys on the
// provenance flag rather than the path string, so a subsystem an author declares
// and happens to name the same as the synthesized one keeps its own path.
//
// Every user-visible string naming an Aggregate component's site — an
// annotation's provenance source and an expansion failure's component — is
// composed here, so the two cannot come to name one component two ways.
func aggregateAuthoredComponentPath(scopePath, componentName string, synthesized bool) string {
	if synthesized {
		return "components." + componentName
	}
	return aggregateAnnotationComponentPath(scopePath, componentName)
}

// writeAggregateComponent persists a mutated ComponentInstance back
// into the Subsystem that contains it in the Aggregate tree. OrderedMap
// returns values by value, so the cascade walker holds a local copy of
// each component; this helper reads the subsystem chain fresh from the
// Aggregate (picking up any prior component writes within the same
// subsystem), Sets the mutated component at the leaf, and writes the
// chain back up to the Aggregate root.
//
// The canonical path is
// "subsystems.<s1>.[subsystems.<s2>.]*components.<name>". A component
// authored at the top-level `.spec.components` surface has been folded into
// the synthesized default subsystem before the walk, so it arrives here under
// "subsystems.default.components.<name>" like any other; there is no
// root-components write path.
//
// Paths that do not match the canonical shape are silently skipped; the
// walker never produces such a path, so a non-match indicates a caller
// error rather than a runtime condition to recover from.
func writeAggregateComponent(agg *types.Aggregate, path string, comp types.ComponentInstance) {
	if agg == nil {
		return
	}
	segments := splitAggregatePath(path)
	if len(segments) < 2 {
		return
	}
	if segments[0] != "subsystems" {
		return
	}
	// Subsystem chain: every odd-positioned segment is "subsystems" or
	// "components"; every even-positioned segment is a name. The leaf
	// pair is always "components.<name>". Collect the subsystem names
	// by reading every other segment until the trailing "components"
	// segment.
	if len(segments) < 4 || len(segments)%2 != 0 {
		return
	}
	if segments[len(segments)-2] != "components" {
		return
	}
	subsystemNames := make([]string, 0, (len(segments)-2)/2)
	for i := 1; i < len(segments)-2; i += 2 {
		subsystemNames = append(subsystemNames, segments[i])
	}
	chain := make([]types.Subsystem, len(subsystemNames))
	top, ok := agg.Spec.Subsystems.Get(subsystemNames[0])
	if !ok {
		return
	}
	chain[0] = top
	for i := 1; i < len(subsystemNames); i++ {
		child, ok := chain[i-1].Subsystems.Get(subsystemNames[i])
		if !ok {
			return
		}
		chain[i] = child
	}
	leafName := segments[len(segments)-1]
	chain[len(chain)-1].Components.Set(leafName, comp)
	for i := len(chain) - 1; i > 0; i-- {
		chain[i-1].Subsystems.Set(subsystemNames[i], chain[i])
	}
	agg.Spec.Subsystems.Set(subsystemNames[0], chain[0])
}

// splitAggregatePath parses a canonical Aggregate component path
// ("components.<name>", "subsystems.<s>.components.<name>", or with
// further "subsystems.<...>" bridges) into its dot-separated segments.
// Empty input returns nil; otherwise every segment is returned in
// order, including the structural "components" and "subsystems"
// tokens.
func splitAggregatePath(path string) []string {
	if path == "" {
		return nil
	}
	return strings.Split(path, ".")
}
