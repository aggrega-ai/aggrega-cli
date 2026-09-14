// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
)

// ValidateAnnotationBindingTargets asks the target-resolution rule of every
// annotation binding the resolve's Manifest declares, and refuses the build when
// any of them routes to a Feature the catalog does not hold or to a pillar path
// that Feature does not declare.
//
// It presents the documents and names the rule rather than walking the registry
// itself: the rule is declared in this package as a constraint, so where it runs
// follows from the facts it reads, and the provenance its findings carry is
// supplied by the framework rather than assembled here. The binding keys are read
// back off the presentation rather than off the Manifest beside it — they are one
// of the facts the presentation states, so reading them anywhere else would be
// answering from a second source that happens to agree.
//
// It is asked here, before annotation expansion, and that position is load-bearing
// rather than incidental. Expansion consumes the binding, so a binding naming a
// Feature the catalog lacks reaches recipe selection and is reported there as a
// missing recipe mapping, and a binding routing to an undeclared path is reported
// against the annotation the operator wrote rather than against the binding that
// is wrong. Both send a reader to the wrong document. Asking the rule first is
// what keeps the diagnostic on the binding.
//
// It reports EVERY offending binding, and every offending route within one,
// rather than the first — a contract of this rule rather than a property of how
// it happens to be written. The rule is invoked once per declared binding and the
// framework accumulates what each invocation raised, so an author fixing a
// Manifest whose bindings offend several times sees them all in one build instead
// of one per build.
//
// A Feature whose declared contract could not be resolved is reported ahead of
// the rule's own findings, once, naming the resolution failure verbatim. That
// order is the point: the rule reading such a Feature's contract sees the same
// absence a Feature declaring no contract presents, so it passes the binding over
// — and reporting the cause beneath findings the cause suppressed would put them
// the wrong way round.
//
// The findings are joined, so a caller matching the refusal by identity keeps
// matching however many bindings offended.
func ValidateAnnotationBindingTargets(result *ResolveResult, catalogLookup CatalogLookup) error {
	if result == nil || result.Manifest == nil {
		return nil
	}
	// Whether there is a catalog to consult at all is settled here rather than
	// reported once per binding. The rule reads whether the catalog holds a
	// Feature, and a build resolving against no catalog has no entry set to ask
	// about — so asking anyway would report every binding in the document as
	// routing to a Feature that is missing, when what is missing is the catalog.
	if catalogLookup.Feature == nil {
		return nil
	}
	presented := newManifestNormalizedView(
		manifestDocumentOf(result), result.Manifest,
		newNormalizedView(
			normalizedAggregateOf(result), result.Release, catalogLookup, result.Parameters),
		catalogLookup,
	)
	document := manifestDocumentOf(result)

	findings := unresolvableTargetFindings(presented)
	findings = append(findings, findingsAtTheirOwnDocument(
		constraint.RunOnDocumentAnnotationBindings(
			document,
			presented.ManifestAnnotationBindingKeys(document),
			constraint.NormalizedView(presented),
			annotationBindingResolvesAgainstItsTargetFeature,
		),
		"Manifest",
		"Manifest",
	)...)
	if len(findings) == 0 {
		return nil
	}
	joined := make([]error, 0, len(findings))
	for _, f := range findings {
		joined = append(joined, f)
	}
	return errors.Join(joined...)
}

// manifestDocumentOf is the document coordinate the Manifest's facts are keyed
// by. The resolve carries the Manifest itself but not the file it was read from,
// so the coordinate is empty and the findings name the entity kind alone — which
// is the attribution this refusal has always carried. A resolve that comes to
// carry the path gains a located finding here with no other change.
func manifestDocumentOf(*ResolveResult) string {
	return ""
}

// normalizedAggregateOf is the application document the embedded presentation is
// built from, normalized. A resolve holding none presents an empty one rather
// than nothing: the facts the rule asked here reads are the Manifest's and the
// catalog's, and an absent application declares no instance for the rest to be
// wrong about.
func normalizedAggregateOf(result *ResolveResult) types.NormalizedAggregate {
	if result.Aggregate == nil {
		return types.Normalize(&types.Aggregate{})
	}
	return types.Normalize(result.Aggregate)
}

// unresolvableTargetFindings renders every bound Feature whose declared contract
// could not be resolved as a halting finding, in the order the resolution names
// them.
//
// The message names the resolution failure verbatim. That text already carries
// the offending key, the source line and the keyword, and re-deriving any of it
// here would be a second answer to what went wrong. The finding is attributed to
// the binding's own condition rather than to a condition of its own, because what
// the author asked for is a binding that resolves and this is one of the ways it
// does not.
func unresolvableTargetFindings(v *manifestNormalizedView) []*ResolverError {
	failures := v.unresolvableTargets()
	if len(failures) == 0 {
		return nil
	}
	out := make([]*ResolverError, 0, len(failures))
	for _, f := range failures {
		out = append(out, &ResolverError{
			Entity: "Manifest",
			Message: fmt.Sprintf(
				"annotation binding routes to Feature %q, whose declared schema does not resolve: %v",
				f.FeatureType, f.Err),
			Err: ErrAnnotationBindingTargetUnresolved(),
		})
	}
	return out
}

// schemaHasPropertyAtPath walks a compiled JSON Schema 2020-12 schema
// along the dotted-path segments under the named pillar, checking that
// each segment exists in an explicit properties map at the current
// level. Returns (ok, skippable):
//
//   - ok=true, skippable=false: the path resolves cleanly through
//     explicit `properties` maps at every segment.
//   - ok=false, skippable=false: the walker reached a `properties`
//     node and a named segment was not listed. The validator halts
//     for this case.
//   - skippable=true: the walker encountered a schema shape it cannot
//     statically resolve ($ref, patternProperties, additionalProperties
//     with a schema body, oneOf/anyOf/allOf). The validator treats this
//     as a soft no-op and trusts downstream validation; the path may
//     or may not be valid under the escaping shape, and false-positive
//     halts are worse than false-negative passes.
//
// The discipline is default-to-skip: any uncertainty at any segment
// flips skippable true. Only the explicit "encountered properties map,
// segment not listed, no escape hatches at this level" path produces a
// halt.
func schemaHasPropertyAtPath(schema compactform.Schema, pillar, path string) (ok, skippable bool) {
	if schema == nil {
		return false, true
	}
	pillarNode, foundPillar := propertyOf(schema, pillar)
	if !foundPillar {
		return false, true
	}
	current, currentSkippable := pillarNode.(map[string]any)
	if !currentSkippable {
		return false, true
	}
	if path == "" {
		return true, false
	}
	for _, seg := range strings.Split(path, ".") {
		if hasEscapeHatch(current) {
			return false, true
		}
		next, found := propertyOf(current, seg)
		if !found {
			return false, false
		}
		nextMap, isMap := next.(map[string]any)
		if !isMap {
			return false, true
		}
		current = nextMap
	}
	return true, false
}

// propertyOf returns the `properties.<name>` entry from a JSON Schema
// 2020-12 node, plus a found flag. Returns false when the node does not
// carry a `properties` map at all or when the named property is absent
// from the map.
func propertyOf(node map[string]any, name string) (any, bool) {
	propsRaw, has := node["properties"]
	if !has {
		return nil, false
	}
	props, ok := propsRaw.(map[string]any)
	if !ok {
		return nil, false
	}
	val, found := props[name]
	return val, found
}

// hasEscapeHatch reports whether a JSON Schema node carries any
// composition or reference clause that escapes simple property-walk.
// The walker flips skippable when it encounters such a clause before
// the path has fully resolved.
func hasEscapeHatch(node map[string]any) bool {
	if _, has := node["$ref"]; has {
		return true
	}
	if _, has := node["patternProperties"]; has {
		return true
	}
	if _, has := node["oneOf"]; has {
		return true
	}
	if _, has := node["anyOf"]; has {
		return true
	}
	if _, has := node["allOf"]; has {
		return true
	}
	if ap, has := node["additionalProperties"]; has {
		// additionalProperties may be a boolean (true/false) or a
		// schema body. A schema body is the escape — any property
		// is potentially declared. A boolean false is a no-escape
		// (only listed properties are valid); boolean true is an
		// escape (any property is allowed).
		switch v := ap.(type) {
		case bool:
			if v {
				return true
			}
		default:
			return true
		}
	}
	return false
}

// validateSubkeyValueAgainstFeatureSchema validates a bound annotation subkey
// value against the target Feature's spec.schema sub-schema at the routed
// pillar+path — full JSON Schema 2020-12 validation (type, enum, pattern,
// numeric range, nested object/array shape), not a type-only check. The
// sub-schema is located by walking properties.<pillar>.properties.<segment>...
// to the terminal node; the value is then validated against that sub-schema
// through the shared pillar-conformance machinery, carrying the Feature
// schema's definitional context so a terminal $ref / allOf / oneOf resolves.
//
// Compile caching. The compiled form of feature.Spec.Schema arrives via the
// [featureSchemaCache] supplied by the caller — one compile per versioned
// Feature URI per [ExpandAnnotations] invocation. A nil/!ok cache result
// soft-skips (a compile failure is surfaced at catalog load, not here).
//
// Skip vs halt. The default-to-skip discipline is retained ONLY for the
// navigation step: an escape hatch ($ref / patternProperties / composition /
// schema-bodied additionalProperties) encountered before the terminal segment
// leaves the path statically unresolvable, so the validator soft-skips that
// binding-path pair rather than risk a false halt. Once the terminal sub-schema
// is located it is validated in full — no type-only relaxation, no soft-skip of
// a terminal composed shape. A terminal segment absent from an explicit
// properties map returns a finding naming the undeclared subkey (this case
// typically pre-empts via [ValidateAnnotationBindingTargets] at catalog load).
// A value conflicting with the located sub-schema returns a finding naming the
// subkey, the value, and the failing constraint.
//
// Neither finding carries an authoring domain of its own. Both authoring sides
// reach this check, and the domain a diagnostic prints is the one belonging to
// the document the annotation value was authored in, supplied by the scope-path
// wrap the finding is collected through.
func validateSubkeyValueAgainstFeatureSchema(cache *featureSchemaCache, feature *types.Feature, pillar, subkey string, value any) error {
	if feature == nil {
		return nil
	}
	compiled, ok := cache.schemaFor(feature)
	if !ok {
		// Compile errors surface upstream at catalog load; defensive
		// soft-skip here matches the path-target validator's
		// default-to-skip discipline.
		return nil
	}
	subSchema, found, skippable := schemaSubSchemaAtPath(compiled, pillar, subkey)
	if skippable {
		return nil
	}
	if !found {
		return fmt.Errorf(
			"%w: annotation subkey %q is not declared at Feature %q spec.schema.%s.%s",
			ErrAnnotationSubkeyNotConforming(), subkey, feature.Metadata.Name, pillar, subkey,
		)
	}
	terminal := subkey
	if i := strings.LastIndex(subkey, "."); i >= 0 {
		terminal = subkey[i+1:]
	}
	if msg, bad := subkeyConformanceFailure(compiled, subSchema, terminal, value); bad {
		return fmt.Errorf(
			"%w: annotation subkey %q value %v does not conform to Feature %q spec.schema.%s.%s: %s",
			ErrAnnotationSubkeyNotConforming(), subkey, value, feature.Metadata.Name, pillar, subkey, msg,
		)
	}
	return nil
}

// schemaSubSchemaAtPath walks the compiled JSON Schema 2020-12 form along the
// pillar + dotted-path and returns the terminal property's sub-schema node.
// The contract mirrors the path walker: (sub, true, false) when the path
// resolves cleanly through explicit `properties` maps; (nil, false, false) when
// a `properties` node lists no such segment (a real "undeclared" miss);
// (nil, false, true) when an escape hatch ($ref, patternProperties,
// oneOf/anyOf/allOf, schema-bodied additionalProperties) on the way leaves the
// path statically unresolvable. The terminal node is returned verbatim — a
// terminal $ref/allOf/oneOf is resolved by the validator with the root
// definitional context, not soft-skipped here.
func schemaSubSchemaAtPath(schema compactform.Schema, pillar, path string) (sub map[string]any, found bool, skippable bool) {
	if schema == nil {
		return nil, false, true
	}
	pillarNode, foundPillar := propertyOf(schema, pillar)
	if !foundPillar {
		return nil, false, true
	}
	current, ok := pillarNode.(map[string]any)
	if !ok {
		return nil, false, true
	}
	if path == "" {
		return nil, false, true
	}
	segments := strings.Split(path, ".")
	for i, seg := range segments {
		if hasEscapeHatch(current) {
			return nil, false, true
		}
		next, ok := propertyOf(current, seg)
		if !ok {
			return nil, false, false
		}
		nextMap, isMap := next.(map[string]any)
		if !isMap {
			return nil, false, true
		}
		if i == len(segments)-1 {
			return nextMap, true, false
		}
		current = nextMap
	}
	return nil, false, true
}

// subkeyConformanceFailure validates a single bound value against subSchema —
// the terminal sub-schema located at the binding path — through the shared
// one-property validator assembly: {seg: subSchema} validated over {seg: value},
// carrying the enclosing schema's definitional context so a $ref/allOf/oneOf in
// subSchema resolves. That assembly has one home, so this surface and the pillar
// conformance beside it cannot come to disagree about which schemas are usable.
// Unlike a type-only check this enforces the full JSON Schema 2020-12 constraint
// set (enum, pattern, numeric range, nested object/array shape). Returns the
// leaf-most finding's message and true on a violation; ("", false) when the value
// conforms or no validator could be built — a declared schema nothing can be
// built from is refused as the catalog loads, so it does not arrive here.
func subkeyConformanceFailure(compiled compactform.Schema, subSchema map[string]any, seg string, value any) (string, bool) {
	v, err := jsonvalidator.NewUnderProperty(seg, subSchema, compiled)
	if err != nil {
		return "", false
	}
	raw, err := json.Marshal(map[string]any{seg: value})
	if err != nil {
		return "", false
	}
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		return "", false
	}
	rpt, err := v.Validate(instance, &jsonvalidator.Context{Surround: instance})
	if err != nil || rpt == nil || len(rpt.Findings) == 0 {
		return "", false
	}
	f := leafmostFindings(rpt.Findings)[0]
	if f.Keyword != "" {
		return fmt.Sprintf("%s (%s)", f.Message, f.Keyword), true
	}
	return f.Message, true
}
