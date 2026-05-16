// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"strings"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// Side-string values for validateAnnotationSideCompatibility. The two
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
// Side-vs-pillar enforcement: Aggregate-side annotations may only route
// through binding paths in Maps.Values; Release-side annotations may
// only route through Maps.Infra. A mismatch halts with a clear error
// citing the annotation key, the routed path, the routed pillar, the
// authoring side, and the source location. An annotation whose binding
// declares paths in both pillars and is authored from one side fires
// only the side-compatible subset; the cross-pillar partition is the
// normal cross-side-composition scenario, not a violation.
//
// Pipeline placement: the orchestrator invokes ExpandAnnotations
// between Resolve and RunGuards. RunGuards' requiredFeatures
// completeness check therefore reads synthetic Features identically to
// explicit ones — a component that satisfies a requiredFeatures
// dependency via an annotation-driven attachment passes the guard.
// Operators debugging a failed requiredFeatures check on an
// annotation-driven attachment will see an author-shape error message
// until IR-level provenance markers (Origin: "annotation",
// AnnotationKey, AnnotationSource) ship in the IR-provenance pass and
// give the diagnostic layer the information to surface annotation-
// origin in error chains.
//
// Null-opt-out: explicit YAML `null` on an annotation key removes the
// inherited binding from the overriding boundary or component. The
// cascade carries the pointer-tristate from Boundary.Annotations and
// BoundaryComponent.Annotations (both OrderedMap[*string]) through to
// the per-component effective set; nil-pointer entries are skipped at
// injection time and do not produce synthetic Feature attachments.
// Sibling components in the same boundary that did not opt out still
// receive the inherited binding.
func ExpandAnnotations(rr *ResolveResult) error {
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

	if rr.Release != nil {
		for bName, boundary := range rr.Release.Spec.Boundaries.All() {
			if err := expandBoundary(bName, "", boundary, types.OrderedMap[*string]{}, map[string]string{}, registry, rr); err != nil {
				return err
			}
		}
	}

	if rr.Aggregate != nil {
		for compName, comp := range rr.Aggregate.Spec.Components.All() {
			if err := expandAggregateTopLevelComponent(compName, comp, registry, rr); err != nil {
				return err
			}
		}
		for subName, sub := range rr.Aggregate.Spec.Subsystems.All() {
			if err := expandAggregateSubsystem(subName, "subsystems", sub, types.OrderedMap[*string]{}, map[string]string{}, registry, rr); err != nil {
				return err
			}
		}
	}
	return nil
}

// expandBoundary walks one boundary (and its descendants) of the Release
// tree, cascading annotation values + their source locations from outer
// to inner. parentAnnotations carries the effective pointer-tristate
// value map at entry; parentSources carries the per-key author location
// (the boundary path or component path where the annotation was
// authored, NOT the boundary where it currently applies — the
// distinction matters for provenance, because an inherited annotation
// reports its authoring boundary as its source). Nil-pointer values in
// the effective map are opt-out signals; expansion skips them without
// injecting a synthetic Feature and without recording provenance.
func expandBoundary(
	name, parentPath string,
	b types.Boundary,
	parentAnnotations types.OrderedMap[*string],
	parentSources map[string]string,
	registry *AnnotationRegistry,
	rr *ResolveResult,
) error {
	currentPath := name
	if parentPath != "" {
		currentPath = parentPath + "/" + name
	}

	mergedAnnotations := mergePointerStringMaps(parentAnnotations, b.Annotations)
	mergedSources := make(map[string]string, len(parentSources)+b.Annotations.Len())
	for k, v := range parentSources {
		mergedSources[k] = v
	}
	for k := range b.Annotations.All() {
		mergedSources[k] = annotationSourcePath(rr.ReleasePath, currentPath, "", k)
	}

	for compName, comp := range b.Components.All() {
		effective := mergePointerStringMaps(mergedAnnotations, comp.Annotations)
		effectiveSources := make(map[string]string, len(mergedSources)+comp.Annotations.Len())
		for k, v := range mergedSources {
			effectiveSources[k] = v
		}
		for k := range comp.Annotations.All() {
			effectiveSources[k] = annotationSourcePath(rr.ReleasePath, currentPath, compName, k)
		}

		for key, ptr := range effective.All() {
			if ptr == nil {
				// Explicit null in the effective map is the opt-out
				// signal: no synthetic Feature for this key on this
				// component; no provenance record.
				continue
			}
			binding, ok := registry.Lookup(key)
			if !ok {
				continue
			}
			path, pillar, err := resolveBindingTarget(binding, *ptr)
			if err != nil {
				return wrapAnnotationOpsError(currentPath, compName, key, "parse", err)
			}
			if err := validateAnnotationSideCompatibility(sideRelease, key, path, pillar, effectiveSources[key]); err != nil {
				return err
			}
			feature, err := expandBinding(key, *ptr, binding, currentPath, compName)
			if err != nil {
				return err
			}
			if _, already := comp.Features.Get(key); already {
				continue
			}
			comp.Features.Set(key, feature)
			rr.AnnotationProvenance[AnnotationProvenanceKey(currentPath, compName, key)] = AnnotationProvenance{
				AnnotationKey:    key,
				AnnotationSource: effectiveSources[key],
			}
		}

		writeComponent(rr.Release, currentPath, compName, comp)
	}

	for childName, child := range b.Boundaries.All() {
		if err := expandBoundary(childName, currentPath, child, mergedAnnotations, mergedSources, registry, rr); err != nil {
			return err
		}
	}
	return nil
}

// annotationSourcePath formats the source location for an annotation
// declaration. The format is <release-file-path>:<dotted-yaml-key-path>;
// line numbers are deferred until yaml.Node access lands in the
// resolver pipeline. componentName empty signals a boundary-level
// annotation; non-empty signals a component-level annotation under
// that boundary.
func annotationSourcePath(releasePath, boundaryPath, componentName, key string) string {
	prefix := releasePath
	if prefix == "" {
		prefix = "release.yaml"
	}
	dotted := "boundaries." + strings.ReplaceAll(boundaryPath, "/", ".boundaries.")
	if componentName != "" {
		return prefix + ":" + dotted + ".components." + componentName + ".annotations." + key
	}
	return prefix + ":" + dotted + ".annotations." + key
}

// expandBinding consumes a scalar annotation value plus its binding and
// produces the synthetic BoundaryFeature payload for injection on the
// Release side. The binding's Maps groups dotted-path subkey routes by
// target pillar (Maps.Infra, Maps.Values). For the Release-side cascade,
// only paths in Maps.Infra fire; Maps.Values paths are inert here
// (Aggregate-side expansion handles them in a parallel pass).
//
// Annotation values are scalar in this layer. A scalar paired with a
// binding declaring more than one path total (Maps.Infra plus Maps.Values)
// is ambiguous routing and halts the build with an OPS_ERROR.
//
// Scalar paired with a single-path binding whose declared path sits in
// Maps.Infra routes the value into the synthetic Feature's Infra at the
// dotted-path leaf — setPillarLeaf navigates the path components,
// creating intermediate OrderedMap nodes for nested schema shapes.
//
// An empty scalar populates no field; the synthetic Feature carries an
// empty Infra pillar. FeatureRecipe rendering materializes any
// target-Feature schema defaults downstream — the expansion layer
// carries only the values the author actually populated through the
// annotation.
//
// Path-target validation against the target Feature's spec.schema (does
// the routed path correspond to an actually-declared property?) runs at
// the catalog cross-reference pass, not here. Expansion-time receives a
// binding whose paths have already been schema-target validated.
func expandBinding(key, value string, binding types.AnnotationBinding, boundaryPath, componentName string) (types.BoundaryFeature, error) {
	path, pillar, err := resolveBindingTarget(binding, value)
	if err != nil {
		return types.BoundaryFeature{}, wrapAnnotationOpsError(boundaryPath, componentName, key, "parse", err)
	}

	feature := types.BoundaryFeature{
		Type: binding.Feature,
	}
	// Release-side cascade processes only the infra-pillar routes from
	// the binding's Maps. Paths in Maps.Values are inert on this side;
	// the parallel Aggregate-side expansion handles values-pillar routes.
	//
	// Empty annotation value populates no field; the synthetic Feature
	// attachment is produced with an empty pillar. FeatureRecipe
	// rendering materializes any schema defaults uniformly across
	// explicit and synthetic attachments downstream.
	if pillar == "infra" && value != "" {
		setPillarLeaf(&feature.Infra, path, value)
	}
	return feature, nil
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
func resolveBindingTarget(binding types.AnnotationBinding, value string) (string, string, error) {
	total := binding.Maps.Len()
	if total == 0 {
		return "", "", fmt.Errorf("binding for feature %q declares no paths in maps; scalar annotation value cannot route to any subkey", binding.Feature)
	}
	if total > 1 && value != "" {
		paths := append(append([]string{}, binding.Maps.Infra...), binding.Maps.Values...)
		return "", "", fmt.Errorf("binding for feature %q maps multiple subkey paths (%v); scalar annotation value %q cannot route to multiple subkeys unambiguously. Provide separate annotation keys for each path", binding.Feature, paths, value)
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
	return "", "", fmt.Errorf("binding for feature %q maps shape is structurally invalid", binding.Feature)
}

// setPillarLeaf walks the dotted-path components through a pillar tree
// (an OrderedMap whose leaves are scalar values and whose intermediate
// nodes are nested OrderedMaps), creating intermediate nodes as needed,
// and sets the leaf value at the final component.
//
// A path of "ingress.host" against an empty pillar tree produces:
//
//	{ "ingress": OrderedMap[any]{ "host": <value> } }
//
// A path of "host" against an empty pillar tree produces a top-level
// leaf: { "host": <value> }. Subsequent calls to setPillarLeaf against
// the same tree may extend nested intermediates; if a path's intermediate
// already exists, the existing node is reused.
//
// The function does not protect against tree-shape collisions (e.g.
// writing a scalar leaf to a path whose intermediate node was previously
// a scalar). The schema-target validation at catalog cross-reference
// time rejects bindings whose paths cannot be navigated through the
// Feature's schema tree; this layer trusts the validated input.
func setPillarLeaf(pillarTree *types.OrderedMap[any], path string, value any) {
	components := strings.Split(path, ".")
	current := pillarTree
	for i, comp := range components {
		if i == len(components)-1 {
			current.Set(comp, value)
			return
		}
		next, exists := current.Get(comp)
		if !exists {
			child := types.OrderedMap[any]{}
			current.Set(comp, &child)
			current = &child
			continue
		}
		switch n := next.(type) {
		case *types.OrderedMap[any]:
			current = n
		case types.OrderedMap[any]:
			// The intermediate is stored value-typed (e.g., YAML unmarshalling
			// nested objects yields value-typed entries by default). Take a
			// local copy, write its pointer back into the parent so subsequent
			// mutations propagate, then descend into the upgraded entry.
			upgraded := n
			current.Set(comp, &upgraded)
			current = &upgraded
		default:
			// Intermediate exists but is not an OrderedMap — a tree-shape
			// collision the schema-target validation should have rejected.
			// Overwrite with a fresh intermediate; subsequent components
			// proceed against the new node.
			child := types.OrderedMap[any]{}
			current.Set(comp, &child)
			current = &child
		}
	}
}

// writeComponent persists a mutated component back into its containing
// boundary in the Release tree. OrderedMap returns values by value, so
// the cascade walker holds a local copy of each component; this helper
// reads the boundary chain fresh from the Release (picking up any prior
// component writes within the same boundary), Sets the mutated component
// at the leaf, and writes the chain back up to the Release root.
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
	chain[len(segments)-1].Components.Set(compName, comp)
	for i := len(segments) - 1; i > 0; i-- {
		chain[i-1].Boundaries.Set(segments[i], chain[i])
	}
	release.Spec.Boundaries.Set(segments[0], chain[0])
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

// wrapAnnotationOpsError formats an annotation-expansion failure with
// the OPS_ERROR prefix. The bad annotation value lives in the Release,
// so the error is Release-scoped (OPS_ERROR) rather than Catalog-scoped.
func wrapAnnotationOpsError(boundaryPath, componentName, key, stage string, inner error) error {
	return fmt.Errorf("OPS_ERROR: annotation expansion %s failed at boundary %q component %q key %q: %w", stage, boundaryPath, componentName, key, inner)
}

// wrapAggregateAnnotationOpsError formats an annotation-expansion
// failure on the Aggregate side. The bad annotation value lives in the
// Aggregate (Developer-authored), so the error is Aggregate-scoped
// rather than Release-scoped; the OPS_ERROR prefix is preserved for
// uniform diagnostic ergonomics with the Release-side wrapper.
func wrapAggregateAnnotationOpsError(componentPath, key, stage string, inner error) error {
	return fmt.Errorf("OPS_ERROR: annotation expansion %s failed at aggregate component %q key %q: %w", stage, componentPath, key, inner)
}

// validateAnnotationSideCompatibility enforces the
// Aggregate-as-Authoring-Surface invariant's pillar partition:
// Aggregate-side annotations may only route through binding paths
// targeting the values pillar; Release-side annotations may only route
// through paths targeting infra.
//
// Permitted: side=sideAggregate paired with pillar="values";
// side=sideRelease paired with pillar="infra". Any other pairing
// returns an OPS_ERROR naming the annotation key, the routed path, the
// routed pillar, the authoring side, and the source location so the
// operator can navigate from the diagnostic to the authoring site.
//
// The function is side-of-authoring focused; bindings declaring paths
// in both pillars route the side-compatible subset on each pass and
// trigger no mismatch.
func validateAnnotationSideCompatibility(side, annotationKey, path, pillar, sourceLocation string) error {
	switch {
	case side == sideAggregate && pillar == "values":
		return nil
	case side == sideRelease && pillar == "infra":
		return nil
	}
	return fmt.Errorf(
		"OPS_ERROR: annotation %q at %s routes through pillar %q at path %q, but %s-side annotations may only route through pillar %q",
		annotationKey, sourceLocation, pillar, path, side, allowedPillarFor(side),
	)
}

// allowedPillarFor returns the pillar each authoring side is permitted
// to route annotations through. The mapping is part of the
// Aggregate-as-Authoring-Surface invariant and is consulted only when
// building the side-vs-pillar mismatch error message.
func allowedPillarFor(side string) string {
	if side == sideAggregate {
		return "values"
	}
	return "infra"
}

// expandAggregateTopLevelComponent processes one top-level Aggregate
// component (no enclosing subsystem). Top-level components carry no
// inherited cascade scope; the only effective annotations are those
// declared directly on the component. The canonical path is
// "components.<name>". The function mirrors the per-component body of
// expandAggregateSubsystem.
func expandAggregateTopLevelComponent(name string, comp types.ComponentInstance, registry *AnnotationRegistry, rr *ResolveResult) error {
	componentPath := "components." + name
	for key, ptr := range comp.Annotations.All() {
		if ptr == nil {
			continue
		}
		binding, ok := registry.Lookup(key)
		if !ok {
			continue
		}
		sourceLoc := aggregateAnnotationSourcePath(rr.AggregatePath, componentPath, key)
		path, pillar, err := resolveBindingTarget(binding, *ptr)
		if err != nil {
			return wrapAggregateAnnotationOpsError(componentPath, key, "parse", err)
		}
		if err := validateAnnotationSideCompatibility(sideAggregate, key, path, pillar, sourceLoc); err != nil {
			return err
		}
		if _, already := comp.Features.Get(key); already {
			continue
		}
		feature := synthesizeAggregateFeature(binding, *ptr, path, pillar)
		comp.Features.Set(key, feature)
		rr.AnnotationProvenance[AnnotationProvenanceKey(componentPath, "", key)] = AnnotationProvenance{
			AnnotationKey:    key,
			AnnotationSource: sourceLoc,
		}
	}
	writeAggregateComponent(rr.Aggregate, componentPath, comp)
	return nil
}

// expandAggregateSubsystem walks one subsystem of the Aggregate's
// subsystem-and-component tree, cascading annotation values and their
// authoring source locations from outer to inner. parentAnnotations
// carries the effective pointer-tristate value map at entry;
// parentSources carries the per-key author location.
//
// For each contained component, every effective annotation that matches
// a registered binding synthesizes a FeatureInstance attachment on the
// component's Features map. Aggregate-side expansion routes only
// through paths in binding.Maps.Values; routing the annotation through
// an infra-pillar path halts with a Loud Guardrail. Nil-pointer entries
// in the effective map are opt-out signals and produce no synthetic
// Feature and no provenance record.
//
// The walker carries the merged OrderedMap[*string] through recursion
// in pointer-tristate form and flattens at write time per component;
// nested subsystems and contained components see the same merged shape
// regardless of whether an inherited binding originated at the
// immediate parent or an outer ancestor.
//
// parentPath is the dotted prefix accumulated by the caller — for the
// first-level entry from ExpandAnnotations, parentPath is "subsystems";
// for a recursive call into a nested subsystem, the caller appends the
// current subsystem name and the bridge ".subsystems".
func expandAggregateSubsystem(
	name, parentPath string,
	s types.Subsystem,
	parentAnnotations types.OrderedMap[*string],
	parentSources map[string]string,
	registry *AnnotationRegistry,
	rr *ResolveResult,
) error {
	currentPath := parentPath + "." + name

	mergedAnnotations := mergePointerStringMaps(parentAnnotations, s.Annotations)
	mergedSources := make(map[string]string, len(parentSources)+s.Annotations.Len())
	for k, v := range parentSources {
		mergedSources[k] = v
	}
	for k := range s.Annotations.All() {
		mergedSources[k] = aggregateAnnotationSourcePath(rr.AggregatePath, currentPath, k)
	}

	for compName, comp := range s.Components.All() {
		componentPath := currentPath + ".components." + compName
		effective := mergePointerStringMaps(mergedAnnotations, comp.Annotations)
		effectiveSources := make(map[string]string, len(mergedSources)+comp.Annotations.Len())
		for k, v := range mergedSources {
			effectiveSources[k] = v
		}
		for k := range comp.Annotations.All() {
			effectiveSources[k] = aggregateAnnotationSourcePath(rr.AggregatePath, componentPath, k)
		}

		for key, ptr := range effective.All() {
			if ptr == nil {
				continue
			}
			binding, ok := registry.Lookup(key)
			if !ok {
				continue
			}
			path, pillar, err := resolveBindingTarget(binding, *ptr)
			if err != nil {
				return wrapAggregateAnnotationOpsError(componentPath, key, "parse", err)
			}
			if err := validateAnnotationSideCompatibility(sideAggregate, key, path, pillar, effectiveSources[key]); err != nil {
				return err
			}
			if _, already := comp.Features.Get(key); already {
				continue
			}
			feature := synthesizeAggregateFeature(binding, *ptr, path, pillar)
			comp.Features.Set(key, feature)
			rr.AnnotationProvenance[AnnotationProvenanceKey(componentPath, "", key)] = AnnotationProvenance{
				AnnotationKey:    key,
				AnnotationSource: effectiveSources[key],
			}
		}
		writeAggregateComponent(rr.Aggregate, componentPath, comp)
	}

	for childName, child := range s.Subsystems.All() {
		if err := expandAggregateSubsystem(childName, currentPath+".subsystems", child, mergedAnnotations, mergedSources, registry, rr); err != nil {
			return err
		}
	}
	return nil
}

// synthesizeAggregateFeature builds the FeatureInstance payload for an
// Aggregate-side synthetic attachment. Aggregate-side expansion routes
// only through values-pillar paths; the synthesized FeatureInstance
// carries the value at the named values leaf via setPillarLeaf, which
// navigates the dotted path through nested OrderedMap intermediates and
// creates the intermediate nodes as needed.
//
// An empty scalar produces a FeatureInstance with an empty Values
// pillar; FeatureRecipe rendering materializes any schema defaults
// uniformly across explicit and synthetic attachments downstream.
//
// The function trusts the caller to have validated side-vs-pillar
// compatibility upstream (see validateAnnotationSideCompatibility);
// receiving a non-values pillar here is a programming error rather than
// a user-error and the function returns a FeatureInstance with the
// declared Type and empty pillars.
func synthesizeAggregateFeature(binding types.AnnotationBinding, value, path, pillar string) types.FeatureInstance {
	feature := types.FeatureInstance{Type: binding.Feature}
	if pillar == "values" && value != "" {
		setPillarLeaf(&feature.Values, path, value)
	}
	return feature
}

// aggregateAnnotationSourcePath formats the source location for an
// Aggregate-authored annotation. The format is
// <aggregate-file-path>:<canonical-dotted-path>.annotations.<key>;
// line numbers are deferred until yaml.Node access lands in the
// resolver pipeline. componentPath is the canonical dotted path of the
// authoring scope (the subsystem path for a subsystem-level annotation
// or the component path for a component-level annotation).
func aggregateAnnotationSourcePath(aggregatePath, componentPath, key string) string {
	prefix := aggregatePath
	if prefix == "" {
		prefix = "aggregate.yaml"
	}
	return prefix + ":" + componentPath + ".annotations." + key
}

// writeAggregateComponent persists a mutated ComponentInstance back
// into its containing parent (either Aggregate.Spec.Components for a
// top-level component, or a nested Subsystem's Components for a
// subsystem-contained component) in the Aggregate tree. OrderedMap
// returns values by value, so the cascade walker holds a local copy of
// each component; this helper reads the subsystem chain fresh from the
// Aggregate (picking up any prior component writes within the same
// subsystem), Sets the mutated component at the leaf, and writes the
// chain back up to the Aggregate root.
//
// The canonical path is one of:
//   - "components.<name>" — top-level component.
//   - "subsystems.<s1>.[subsystems.<s2>.]*components.<name>" — subsystem-
//     contained or nested-subsystem-contained component.
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
	if segments[0] == "components" && len(segments) == 2 {
		agg.Spec.Components.Set(segments[1], comp)
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
