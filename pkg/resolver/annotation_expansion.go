// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"encoding/json"
	"fmt"
	"strings"

	libschema "github.com/google/jsonschema-go/jsonschema"

	"github.com/cognitio/aggrega-cli/pkg/types"
	jsonvalidator "github.com/cognitio/aggrega-cli/pkg/validator/jsonschema"
)

// ExpandAnnotations runs the annotation-driven Feature expansion pass.
// The pass reads the resolved Manifest's flat features.annotations
// bindings, walks the Release boundary tree with parent-to-child
// inheritance, computes the effective annotation set per component, and
// injects a synthetic BoundaryFeature attachment into
// rr.Release.Spec.Boundaries[].Components[].Features for every effective
// annotation that matches a registered binding.
//
// The pass mutates rr.Release in place by design — synthetic Feature
// attachments are indistinguishable from explicit attachments at the
// Release type level, so downstream phases (the guards pass, topology
// build, IR construction, and rendering) consume them without
// reconciling two sources. Authors who serialize the resolved Release
// for debug see author intent + synthetic injection mingled; the
// deterministic-output invariant holds because the pass iterates
// author-ordered OrderedMaps deterministically.
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
	if rr == nil || rr.Release == nil {
		return nil
	}
	registry := BuildAnnotationRegistry(rr.Manifest)
	if registry.Len() == 0 {
		return nil
	}
	if rr.AnnotationProvenance == nil {
		rr.AnnotationProvenance = map[string]AnnotationProvenance{}
	}

	for bName, boundary := range rr.Release.Spec.Boundaries.All() {
		if err := expandBoundary(bName, "", boundary, types.OrderedMap[*string]{}, map[string]string{}, registry, rr); err != nil {
			return err
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

// expandBinding parses a single annotation value against its binding's
// validating schema and produces the synthetic BoundaryFeature payload
// for injection. The value is interpreted in three shapes per the
// annotation-driven Feature expansion design: (a) empty string means
// "accept all schema defaults"; (b) scalar (non-empty string) maps to
// the first field of the Infra schema's properties; (c) any other
// shape (map-bearing YAML) is parsed as a multi-field payload. The
// current implementation supports shapes (a) and (b) at the YAML
// surface — Release.Boundary annotation values are typed
// OrderedMap[string], which collapses author-authored maps to their
// string serialization; full map-shape parsing depends on a parallel
// tristate-and-value-shape pass not yet active.
//
// Schema-defaults policy: this layer does NOT merge schema defaults
// into the synthetic Feature's Infra payload. The Infra produced here
// carries only the fields the author actually populated through the
// annotation value (one field for shape (b); zero fields for shape
// (a)). Default-value materialization is the FeatureRecipe rendering
// layer's responsibility, applied uniformly to explicit and synthetic
// attachments so that the two paths produce byte-identical IR and
// rendered output for the same effective configuration. Validation
// against the binding schema runs here as a pure conformance check —
// the validator reports findings but does not mutate the instance,
// so the synthetic Feature reflects author intent rather than
// default-resolved intent.
func expandBinding(key, value string, binding types.AnnotationBinding, boundaryPath, componentName string) (types.BoundaryFeature, error) {
	parsed, err := parseAnnotationValue(value, binding)
	if err != nil {
		return types.BoundaryFeature{}, wrapAnnotationOpsError(boundaryPath, componentName, key, "parse", err)
	}

	if err := validateAnnotationValue(parsed, binding); err != nil {
		return types.BoundaryFeature{}, wrapAnnotationOpsError(boundaryPath, componentName, key, "validate", err)
	}

	feature := types.BoundaryFeature{
		Type: binding.Feature,
	}
	if parsed != nil {
		for k, v := range parsed {
			feature.Infra.Set(k, v)
		}
	}
	return feature, nil
}

// parseAnnotationValue produces a field-map ready for schema validation
// from the annotation's authored string value. An empty value means
// "accept the schema defaults"; a non-empty scalar maps to the first
// field declared on the binding's Infra schema (in author order).
func parseAnnotationValue(value string, binding types.AnnotationBinding) (map[string]any, error) {
	if value == "" {
		return nil, nil
	}
	firstField, ok := firstSchemaField(binding.Infra)
	if !ok {
		return nil, fmt.Errorf("binding for feature %q declares no infra fields; scalar annotation value cannot map to a field", binding.Feature)
	}
	return map[string]any{firstField: value}, nil
}

// firstSchemaField returns the first property name declared on a binding's
// validating schema in author order. The binding schema is an inline
// JSON Schema 2020-12 source represented in Go as OrderedMap[any] — the
// schema's top-level "properties" entry is itself an OrderedMap-shaped
// object; the property names declared inside it are the binding's
// fields. Returns false when the schema does not declare a properties
// object or when the object is empty.
func firstSchemaField(infra types.OrderedMap[any]) (string, bool) {
	props, ok := infra.Get("properties")
	if !ok {
		return "", false
	}
	switch p := props.(type) {
	case *types.OrderedMap[any]:
		if p == nil {
			return "", false
		}
		keys := p.Keys()
		if len(keys) == 0 {
			return "", false
		}
		return keys[0], true
	case types.OrderedMap[any]:
		keys := p.Keys()
		if len(keys) == 0 {
			return "", false
		}
		return keys[0], true
	case map[string]any:
		for k := range p {
			return k, true
		}
		return "", false
	}
	return "", false
}

// validateAnnotationValue validates the parsed annotation field-map
// against the binding's Infra schema using the project's JSON Schema
// 2020-12 validator. Marshal the OrderedMap[any] author content to JSON,
// unmarshal into a libschema.Schema, construct a validator, and
// validate the parsed instance. The function tolerates a nil parsed
// instance — the schema-defaults case validates by construction when
// the binding schema's required fields all carry defaults.
func validateAnnotationValue(parsed map[string]any, binding types.AnnotationBinding) error {
	if binding.Infra.Len() == 0 {
		return nil
	}
	raw, err := json.Marshal(binding.Infra)
	if err != nil {
		return fmt.Errorf("marshal binding infra schema: %w", err)
	}
	schema := &libschema.Schema{}
	if err := json.Unmarshal(raw, schema); err != nil {
		return fmt.Errorf("parse binding infra schema: %w", err)
	}
	v, err := jsonvalidator.New(schema)
	if err != nil {
		return fmt.Errorf("resolve binding infra schema: %w", err)
	}
	instance := any(parsed)
	if parsed == nil {
		instance = map[string]any{}
	}
	report, err := v.Validate(instance, &jsonvalidator.Context{Surround: instance})
	if err != nil {
		return fmt.Errorf("validator error: %w", err)
	}
	if report != nil && len(report.Findings) > 0 {
		f := report.Findings[0]
		return fmt.Errorf("schema validation failed at %q (%s): %s", f.FieldPath, f.Keyword, f.Message)
	}
	return nil
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
