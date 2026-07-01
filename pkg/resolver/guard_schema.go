// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"encoding/json"
	"fmt"
	"strings"

	libschema "github.com/google/jsonschema-go/jsonschema"
	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
)

// guardSchemaConformance validates developer-authored and operator-resolved
// pillar instances against the catalog Component's declared schema. Two pillars
// are checked:
//
//   - values — the developer's authored values pillar against schema.values.
//   - infra — the operator's resolved boundary infra against schema.infra.
//
// A pillar instance that does not conform (wrong type, out of range, missing
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
// Both pillars are validated closed, symmetrically: values against schema.values
// and infra against schema.infra. The Component's own schema is the sole
// authoring contract for each pillar — an unknown property is a real authoring
// error. A key an operator sets in infra must be declared in the Component's
// schema.infra; the Component schema is the correct home for every infra key the
// operator may write.
//
// The infra pillar is validated only when the Component declares a schema.infra
// pillar. A component that declares no infra contract, or has no boundary infra
// set, is skipped for infra — the same opt-in shape as the values pillar.
//
// Single-document composition in a pillar schema (a local #/$defs $ref or
// allOf) is validated by carrying the schema's own definitional context into the
// validator, so such a pillar is NOT skipped. Cross-entity extends composition
// is a separate follow-on.
//
// Catalog-less paths (nil catalog.Component) skip entirely. A component whose
// type is absent from the catalog is skipped (unknown-type reporting belongs to
// other guards). A Component with no values schema has no values to validate;
// a component with no boundary infra has no infra to validate.
func guardSchemaConformance(
	na types.NormalizedAggregate,
	catalog CatalogLookup,
	boundaryInfra map[string]*BoundaryInfra,
	parameters map[string]any,
) []*ResolverError {
	agg := na.Aggregate()
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
		compiled, ok := CompileComponentSchema(catalog, compType)
		compiledCache[compType] = compiledEntry{schema: compiled, ok: ok}
		return compiled, ok
	}

	var errs []*ResolverError
	for _, p := range collectComponentInstances(na) {
		componentField := componentFieldPath(p.subsystemPath, p.name, p.synthesized)
		// Classify a missing catalog entity HERE, before the compile call —
		// compiledFor/CompileComponentSchema return only a bool, which would
		// swallow a resolved-but-absent halt into the legitimate Kind==0 /
		// compile-failure skip. A bare-token catalog ref absent from the view is
		// a loud inconsistency; a Tier-1 relative-path type is a legitimate skip.
		if _, found := catalog.Component(p.inst.Type); !found {
			if verr := guardEntityViewInconsistency(entity, componentField, p.inst.Type); verr != nil {
				errs = append(errs, verr)
			}
			continue
		}
		compiled, ok := compiledFor(p.inst.Type)
		if !ok {
			continue
		}
		errs = append(errs, validateValuesConformance(
			compiled, p.inst.Values, parameters, entity, componentField)...)

		bi := boundaryInfra[p.name]
		if bi == nil || bi.Infra.Len() == 0 {
			continue
		}
		errs = append(errs, validateInfraConformance(
			compiled, bi.Infra, parameters, entity, componentField)...)
	}
	// Canonicalise by (Entity, Field) so the gate's output is byte-identical
	// across runs of the same input, matching the other catalog-aware guards.
	sortResolverErrors(errs)
	return errs
}

// guardEdgeConformance validates per-edge connection-scoped config against the
// source socket's declared per-edge contract, closed. A developer-authored
// `values` block on a verbose `uses` entry (Aggregate) is validated against the
// socket's valueSchema; an operator-authored `infra` block on a Release `uses`
// entry is validated against the socket's infraSchema. Both route through the
// shared [ValidatePillarConformance] entry — the same one the component and
// attachment gates use — so an authored per-edge schema is interpreted
// identically wherever the value originates. Default-aware and parameter-aware
// via that shared entry. A socket that declares no contract for the pillar
// skips (opt-in conformance). Each violation wraps ErrSchemaConformance, its
// Field naming the edge (consumer, socket, alias), the pillar, and the leaf
// path.
//
// The Release infra walk resolves the consuming node's component type from the
// Aggregate's resolver-visible instances (top-level and subsystem components).
// A Release edge on a node the resolver cannot see a type for — a sub-aggregate
// component, which loads only at topology — skips here; that coverage boundary
// is the same one the undeclared-socket guard carries.
func guardEdgeConformance(na types.NormalizedAggregate, release *types.Release, catalog CatalogLookup, parameters map[string]any) []*ResolverError {
	agg := na.Aggregate()
	if agg == nil || catalog.Component == nil {
		return nil
	}
	var errs []*ResolverError

	// Developer per-edge values: walk every component instance's verbose uses
	// entries (top-level + subsystem) in author order.
	aggEntity := fmt.Sprintf("Aggregate %s", agg.Metadata.Name)
	for _, p := range collectComponentInstances(na) {
		if p.inst.Uses.Len() == 0 {
			continue
		}
		// Classify a missing catalog entity HERE, before CompileSocketPillarSchema
		// — its bool return would otherwise swallow a resolved-but-absent halt
		// into the legitimate socket-undeclared / no-pillar-schema skip.
		if _, found := catalog.Component(p.inst.Type); !found {
			if verr := guardEntityViewInconsistency(aggEntity,
				componentFieldPath(p.subsystemPath, p.name, p.synthesized), p.inst.Type); verr != nil {
				errs = append(errs, verr)
			}
			continue
		}
		for socket, sv := range p.inst.Uses.All() {
			for alias, e := range sv.Verbose.All() {
				if e.Values.Len() == 0 {
					continue
				}
				compiled, ok := CompileSocketPillarSchema(catalog, p.inst.Type, socket, "values")
				if !ok {
					continue
				}
				field := fmt.Sprintf("%s.uses[%q][%q]", componentFieldPath(p.subsystemPath, p.name, p.synthesized), socket, alias)
				errs = append(errs, ValidatePillarConformance(
					compiled, "values", e.Values, parameters,
					aggEntity, field, "the socket valueSchema")...)
			}
		}
	}

	// Operator per-edge infra: walk Release boundary uses entries.
	if release != nil {
		relEntity := fmt.Sprintf("Release %s", release.Metadata.Name)
		typeOf := map[string]string{}
		for _, p := range collectComponentInstances(na) {
			typeOf[p.name] = p.inst.Type
		}
		for _, ref := range collectReleaseEdgeEntries(release) {
			if ref.entry.Infra.Len() == 0 {
				continue
			}
			compType, ok := typeOf[ref.nodeID]
			if !ok {
				continue
			}
			field := fmt.Sprintf("%s.uses[%q][%q]",
				releaseComponentFieldPath(ref.boundaryPath, ref.nodeID, ref.synthesized), ref.socket, ref.alias)
			// Classify before CompileSocketPillarSchema (bool-swallow) — same as
			// the values path above.
			if _, found := catalog.Component(compType); !found {
				if verr := guardEntityViewInconsistency(relEntity, field, compType); verr != nil {
					errs = append(errs, verr)
				}
				continue
			}
			compiled, ok := CompileSocketPillarSchema(catalog, compType, ref.socket, "infra")
			if !ok {
				continue
			}
			errs = append(errs, ValidatePillarConformance(
				compiled, "infra", ref.entry.Infra, parameters,
				relEntity, field, "the socket infraSchema")...)
		}
	}

	sortResolverErrors(errs)
	return errs
}

// CompileSocketPillarSchema compiles a socket's per-edge contract for one
// pillar so it can be fed to the shared [ValidatePillarConformance] entry,
// which expects a pillar-keyed schema (properties.<pillar>). The socket's
// valueSchema / infraSchema is authored as bare pillar content (a field →
// constraint map), so it is wrapped under a synthetic <pillar> key before
// compilation. Returns false when the component type is unknown, the socket is
// undeclared, the socket declares no schema for the pillar (Kind == 0), or the
// content fails to compile.
func CompileSocketPillarSchema(catalog CatalogLookup, compType, socket, pillar string) (compactform.Schema, bool) {
	catComp, found := catalog.Component(compType)
	if !found {
		return nil, false
	}
	sock, ok := catComp.Spec.UsesSchema.Get(socket)
	if !ok {
		return nil, false
	}
	var node *yaml.Node
	switch pillar {
	case "values":
		node = &sock.ValueSchema
	case "infra":
		node = &sock.InfraSchema
	default:
		return nil, false
	}
	if node.Kind == 0 {
		return nil, false
	}
	wrapper := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: pillar},
			node,
		},
	}
	var opts []compactform.CompileOption
	if len(catalog.SchemaLibraries) > 0 {
		opts = append(opts, compactform.WithSchemaLibraries(catalog.SchemaLibraries))
	}
	compiled, err := compactform.Compile(wrapper, opts...)
	if err != nil {
		return nil, false
	}
	return compiled, true
}

// ValidatePillarConformance validates a resolved or authored pillar instance
// (values or infra) against the named pillar of an already-compiled
// Component-or-Feature schema, closed. It is the shared schema-conformance
// entry point: the Step-1 component values gate and the Step-3 Feature/Secret
// attachment gate both route through it, so an authored schema is interpreted
// identically wherever the value originates.
//
// The pillar sub-schema is extracted from compiled; schema-declared defaults for
// that pillar are merged into the instance (default-aware) before validation;
// the validator carries the compiled schema's own definitional context so a
// single-document #/$defs $ref or allOf inside the pillar resolves rather than
// being skipped; findings are deduped to the leaf-most. Each violation is a
// *ResolverError wrapping ErrSchemaConformance, its Field rooted at
// fieldPrefix + "." + <pillar>.<path>, its Message naming schemaLabel.
//
// Returns nil when compiled declares no such pillar (nothing to validate), when
// the instance does not marshal, or when the instance conforms. The returned
// slice is NOT sorted — callers that aggregate across multiple instances
// canonicalise the combined output.
func ValidatePillarConformance(
	compiled compactform.Schema,
	pillar string,
	instance types.OrderedMap[any],
	parameters map[string]any,
	entity, fieldPrefix, schemaLabel string,
) []*ResolverError {
	return validatePillarConformance(compiled, pillar, instance, parameters, entity, fieldPrefix, schemaLabel, false)
}

// ValidatePillarConformanceStructural is the predicate-inert variant of
// ValidatePillarConformance: it validates the unconditional structural
// constraints (type, range, enum, required, closed-ness) but does NOT evaluate
// the conditional extension predicates (x-aggrega-when / x-aggrega-validate).
// It is for callers that lack the correct parameter context for those
// predicates and must therefore validate structure only rather than evaluate
// them against a wrong or empty context. Same diagnostics and default-awareness
// as the full entry; only predicate evaluation is suppressed.
func ValidatePillarConformanceStructural(
	compiled compactform.Schema,
	pillar string,
	instance types.OrderedMap[any],
	parameters map[string]any,
	entity, fieldPrefix, schemaLabel string,
) []*ResolverError {
	return validatePillarConformance(compiled, pillar, instance, parameters, entity, fieldPrefix, schemaLabel, true)
}

// CompileComponentSchema compiles a Component type's declared schema for
// conformance validation, preferring the composed effective schema for an
// extends-ing Component (so an inherited constraint is enforced on the child)
// and falling back to compiling the authored schema for a non-extends
// Component. Returns (nil, false) when the catalog has no such component, the
// component declares no schema, or compilation fails. Uncached — callers that
// validate many instances of the same type should memoise.
func CompileComponentSchema(catalog CatalogLookup, compType string) (compactform.Schema, bool) {
	if catalog.EffectiveSchema != nil {
		if eff, ok := catalog.EffectiveSchema(compType); ok {
			return eff, true
		}
	}
	catComp, found := catalog.Component(compType)
	if !found || catComp.Spec.Schema.Kind == 0 {
		return nil, false
	}
	var opts []compactform.CompileOption
	if len(catalog.SchemaLibraries) > 0 {
		opts = append(opts, compactform.WithSchemaLibraries(catalog.SchemaLibraries))
	}
	compiled, err := compactform.Compile(&catComp.Spec.Schema, opts...)
	if err != nil {
		return nil, false
	}
	return compiled, true
}

func validatePillarConformance(
	compiled compactform.Schema,
	pillar string,
	instance types.OrderedMap[any],
	parameters map[string]any,
	entity, fieldPrefix, schemaLabel string,
	skipPredicates bool,
) []*ResolverError {
	pillarSchema, ok := pillarSubSchema(compiled, pillar)
	if !ok {
		return nil // no such pillar declared → nothing to validate
	}

	effective := compactform.MergeSchemaDefaults(instance, compactform.PillarDefaults(compiled, pillar))
	normalised, err := normalisePillarInstance(effective)
	if err != nil {
		return nil // a non-marshalable instance is not a conformance violation
	}

	return pillarConformanceErrors(
		pillar, pillarSchema, compiled, normalised, parameters,
		entity, fieldPrefix, schemaLabel, skipPredicates)
}

// validateValuesConformance validates a component instance's authored values
// pillar against the Component's declared schema.values, closed and
// default-aware, via the shared ValidatePillarConformance entry point.
func validateValuesConformance(
	compiled compactform.Schema,
	authored types.OrderedMap[any],
	parameters map[string]any,
	entity, componentField string,
) []*ResolverError {
	return ValidatePillarConformance(
		compiled, "values", authored, parameters,
		entity, componentField, "the Component schema")
}

// validateInfraConformance validates a component instance's resolved boundary
// infra pillar against the Component's declared schema.infra, closed and
// default-aware, via the shared ValidatePillarConformance entry point —
// symmetric with validateValuesConformance. Returns nil when the Component
// declares no schema.infra pillar (the component has not opted into an infra
// contract) or when the instance does not marshal.
func validateInfraConformance(
	compiled compactform.Schema,
	resolved types.OrderedMap[any],
	parameters map[string]any,
	entity, componentField string,
) []*ResolverError {
	return ValidatePillarConformance(
		compiled, "infra", resolved, parameters,
		entity, componentField, "the Component schema")
}

// pillarConformanceErrors validates a normalised pillar instance against the
// pillar's sub-schema and maps the resulting findings to ResolverErrors wrapping
// ErrSchemaConformance. The pillar instance is wrapped one level deep
// ({<pillar>: instance} against {properties: {<pillar>: schema}}) so the
// findings' field paths lead with the pillar name (values.x / infra.x), and the
// compiled schema's root definitional context ($defs / definitions) is carried
// onto the wrapper so a single-document $ref/allOf inside the pillar schema
// resolves. schemaLabel names the contract in the error message (e.g. "the
// Component schema"). Returns nil when the wrapper does not compile or the
// instance conforms.
func pillarConformanceErrors(
	pillar string,
	pillarSchema map[string]any,
	compiled compactform.Schema,
	instance any,
	parameters map[string]any,
	entity, componentField, schemaLabel string,
	skipPredicates bool,
) []*ResolverError {
	wrapper := map[string]any{
		"type":       "object",
		"properties": map[string]any{pillar: pillarSchema},
	}
	// Carry the root definitional context so a single-document $ref/allOf in the
	// pillar schema resolves within the wrapper document (2020-12 $defs and the
	// draft-07 definitions alias, whichever the compiled schema carries).
	if defs, present := compiled["$defs"]; present {
		wrapper["$defs"] = defs
	}
	if defs, present := compiled["definitions"]; present {
		wrapper["definitions"] = defs
	}
	wrapped := map[string]any{pillar: instance}

	v, err := newPillarValidator(wrapper)
	if err != nil {
		return nil // a genuinely uncompilable schema is surfaced at catalog load, not here
	}

	rpt, err := v.Validate(wrapped, &jsonvalidator.Context{Parameters: parameters, Surround: wrapped, SkipPredicates: skipPredicates})
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
			Message: fmt.Sprintf("%s pillar does not conform to %s: %s", pillar, schemaLabel, msg),
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
// and constructs the validator.
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
