// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"encoding/json"
	"fmt"
	"strings"

	libschema "github.com/google/jsonschema-go/jsonschema"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
)

// guardSchemaConformance validates every developer-authored component instance's
// values pillar against the catalog Component's declared schema.values. A pillar
// instance that does not conform (wrong type, out of range, missing
// required-non-defaulted field, or a property the schema forbids) produces a
// ResolverError wrapping ErrSchemaConformance, naming the component, the pillar,
// the offending field path, and the constraint that failed.
//
// The gate is default-aware: schema-declared defaults are merged into the
// instance before validation (the same defaults the IR materialises later), so
// a required-with-default field is satisfied rather than flagged missing. It
// runs after parameter resolution, so x-aggrega-when / x-aggrega-validate
// predicates evaluate with the resolved parameter context available on the
// validator Context.
//
// The values pillar is validated closed: it is the developer's complete
// authoring surface, so an unknown property is a real authoring error. The infra
// pillar is NOT validated here — its resolved instance is operator boundary infra
// layered with provider-level keys (declared in the Provider baseSchema, not the
// Component schema), so closed conformance needs provider resolution the Step-1
// gate does not have; that is a dedicated follow-on.
//
// Single-document composition in the values schema (a local #/$defs $ref or
// allOf) is validated by carrying the schema's own definitional context into the
// validator, so such a pillar is NOT skipped. Cross-entity extends composition
// is a separate follow-on.
//
// Catalog-less paths (nil catalog.Component) skip entirely. A component whose
// type is absent from the catalog is skipped (unknown-type reporting belongs to
// other guards). A Component with no values schema has nothing to validate.
func guardSchemaConformance(
	agg *types.Aggregate,
	catalog CatalogLookup,
	parameters map[string]any,
) []*ResolverError {
	if agg == nil || catalog.Component == nil {
		return nil
	}
	entity := fmt.Sprintf("Aggregate %s", agg.Metadata.Name)

	// Memoise the compiled schema per Component type — many instances share one.
	type compiledEntry struct {
		schema compactform.Schema
		ok     bool
	}
	compiledCache := map[string]compiledEntry{}
	compiledFor := func(compType string) (compactform.Schema, bool) {
		if e, seen := compiledCache[compType]; seen {
			return e.schema, e.ok
		}
		catComp, found := catalog.Component(compType)
		if !found || catComp.Spec.Schema.Kind == 0 {
			compiledCache[compType] = compiledEntry{}
			return nil, false
		}
		var opts []compactform.CompileOption
		if len(catalog.SchemaLibraries) > 0 {
			opts = append(opts, compactform.WithSchemaLibraries(catalog.SchemaLibraries))
		}
		compiled, err := compactform.Compile(&catComp.Spec.Schema, opts...)
		if err != nil {
			compiledCache[compType] = compiledEntry{}
			return nil, false
		}
		compiledCache[compType] = compiledEntry{schema: compiled, ok: true}
		return compiled, true
	}

	var errs []*ResolverError
	for _, p := range collectComponentInstances(agg) {
		compiled, ok := compiledFor(p.inst.Type)
		if !ok {
			continue
		}
		errs = append(errs, validateValuesConformance(
			compiled, p.inst.Values, parameters, entity,
			fmt.Sprintf(".spec.components.%s", p.name))...)
	}
	return errs
}

// validateValuesConformance merges the values pillar's schema defaults into the
// authored instance (default-awareness) and validates the result against the
// Component's declared schema.values. Findings map to ResolverErrors wrapping
// ErrSchemaConformance. Returns nil when the Component declares no values
// schema, or when the schema genuinely does not compile into a validator.
//
// The validator is fed a definitional-context-preserving wrapper —
// {type: object, $defs: <root $defs>, properties: {values: <root values schema>}}
// validated over {values: <instance>} — rather than a bare extracted sub-schema.
// This keeps a single-document #/$defs $ref or allOf in the values pillar
// resolvable, so a composed values schema is validated rather than silently
// skipped.
func validateValuesConformance(
	compiled compactform.Schema,
	authored types.OrderedMap[any],
	parameters map[string]any,
	entity, componentField string,
) []*ResolverError {
	valuesSchema, ok := pillarSubSchema(compiled, "values")
	if !ok {
		return nil // no values schema declared → nothing to validate
	}

	effective := compactform.MergeSchemaDefaults(authored, compactform.PillarDefaults(compiled, "values"))
	instance, err := normalisePillarInstance(effective)
	if err != nil {
		return nil // a non-marshalable instance is not a conformance violation
	}

	wrapper := map[string]any{
		"type":       "object",
		"properties": map[string]any{"values": valuesSchema},
	}
	// Carry the root definitional context so a single-document $ref/allOf in the
	// values schema resolves within the wrapper document (2020-12 $defs and the
	// draft-07 definitions alias, whichever the compiled schema carries).
	if defs, present := compiled["$defs"]; present {
		wrapper["$defs"] = defs
	}
	if defs, present := compiled["definitions"]; present {
		wrapper["definitions"] = defs
	}
	wrapped := map[string]any{"values": instance}

	v, err := newPillarValidator(wrapper)
	if err != nil {
		return nil // a genuinely uncompilable schema is surfaced at catalog load, not here
	}

	rpt, err := v.Validate(wrapped, &jsonvalidator.Context{Parameters: parameters, Surround: wrapped})
	if err != nil || rpt == nil || len(rpt.Findings) == 0 {
		return nil
	}

	// The validator emits a finding at every level along a failure path (the
	// failing leaf plus each ancestor object). Keep only the leaf-most findings
	// — drop any whose FieldPath is a strict segment-wise prefix of another's —
	// so one violation maps to one error while independent violations stay
	// distinct.
	leaves := leafmostFindings(rpt.Findings)

	errs := make([]*ResolverError, 0, len(leaves))
	for _, f := range leaves {
		field := componentField
		if f.FieldPath != "" {
			field = componentField + "." + f.FieldPath
		}
		msg := f.Message
		if f.Keyword != "" {
			msg = fmt.Sprintf("%s (%s)", f.Message, f.Keyword)
		}
		errs = append(errs, &ResolverError{
			Entity:  entity,
			Field:   field,
			Message: fmt.Sprintf("values pillar does not conform to the Component schema: %s", msg),
			Err:     ErrSchemaConformance,
		})
	}
	return errs
}

// leafmostFindings returns the findings whose FieldPath is NOT a strict
// segment-wise prefix of another finding's FieldPath. The validator reports a
// failure at every level along the path (the leaf plus each containing object),
// so keeping only the most-specific entries yields one error per independent
// violation while preserving multiple distinct violations. Comparison is
// segment-wise (split on ".") so "values.d" is not treated as a prefix of
// "values.db".
func leafmostFindings(findings []jsonvalidator.Finding) []jsonvalidator.Finding {
	out := make([]jsonvalidator.Finding, 0, len(findings))
	for i, f := range findings {
		prefixOfAnother := false
		for j, g := range findings {
			if i == j {
				continue
			}
			if isStrictSegmentPrefix(f.FieldPath, g.FieldPath) {
				prefixOfAnother = true
				break
			}
		}
		if !prefixOfAnother {
			out = append(out, f)
		}
	}
	return out
}

// isStrictSegmentPrefix reports whether a is a strict segment-wise prefix of b
// (a != b, and a's "."-split segments are an initial run of b's). The empty path
// (the instance root) is a strict prefix of any non-empty path.
func isStrictSegmentPrefix(a, b string) bool {
	if a == b || b == "" {
		return false
	}
	if a == "" {
		return true
	}
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	if len(as) >= len(bs) {
		return false
	}
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

// pillarSubSchema returns compiled.properties.<pillar> as a JSON Schema map.
func pillarSubSchema(compiled compactform.Schema, pillar string) (map[string]any, bool) {
	props, ok := compiled["properties"].(map[string]any)
	if !ok {
		return nil, false
	}
	sub, ok := props[pillar].(map[string]any)
	return sub, ok
}

// newPillarValidator round-trips a compiled schema into the library schema type
// and constructs the validator (mirrors the baseSchema validator).
func newPillarValidator(schema map[string]any) (jsonvalidator.Validator, error) {
	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	libSchema := &libschema.Schema{}
	if err := json.Unmarshal(schemaJSON, libSchema); err != nil {
		return nil, err
	}
	return jsonvalidator.New(libSchema)
}

// normalisePillarInstance converts an authored OrderedMap pillar into the
// canonical generic value the validator consumes (JSON round-trip).
func normalisePillarInstance(pillar types.OrderedMap[any]) (any, error) {
	raw, err := json.Marshal(pillar)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
