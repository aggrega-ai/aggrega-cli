// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"encoding/json"
	"fmt"
	"sync"

	libschema "github.com/google/jsonschema-go/jsonschema"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// parameterContractSchemaJSON is the hardcoded map-level sub-schema for
// the Aggregate `spec.parameters:` block. It mirrors the canonical
// `properties.spec.properties.parameters` subtree of
// `aggrega-schemas/json-schemas/aggregate-v1alpha1.schema.json` —
// specifically the whole `{type: object, additionalProperties:
// {oneOf: [...]}}` shape, NOT the inner per-parameter `oneOf` alone.
//
// The literal exists because aggregate-v1alpha1.schema.json carries the
// parameter contract inline at `properties.spec.properties.parameters`
// rather than under a named `$defs/parameter_contract` slot —
// no standalone sub-schema is referencable. Hardcoding the literal here
// avoids runtime JSON Schema document traversal for the
// shallow-load envelope-validation second-line-of-defense check.
//
// MAINTENANCE CONTRACT — if `aggregate-v1alpha1.schema.json` changes its
// `properties.spec.properties.parameters` subtree, update THIS literal
// in lockstep. The drift detector at
// `step1a_parameter_schema_test.go::TestParameterContractSchema_NoDrift`
// reads the canonical schema from disk and asserts byte-equivalent
// content after canonical formatting. Drift fails the test with a
// directed message naming the canonical path.
const parameterContractSchemaJSON = `{
  "type": "object",
  "description": "Global environment contract. Each entry is either a simple <key>: <default-value> shorthand (a bare scalar default; the type is inferred from the scalar's YAML kind, no constraints) or a typed contract object using the compact-form vocabulary subset (type, description, default, enum, min/max, minLength/maxLength, pattern, format, validate, validateMessage, required, items, valuesType, properties). Nested structure is expressed via the typed-form properties: sub-field or via recursive YAML nesting at this map. Release fulfillment may be inline (parameters: {...}) or single-source file import (parameters: {$import: <path>}); Step 1a validates the merged map against this contract before parameter interpolation.",
  "additionalProperties": {
    "oneOf": [
      {
        "type": ["string", "number", "integer", "boolean"],
        "description": "Simple shorthand: bare scalar default value. Inferred as type matching the scalar's YAML kind; no further constraints."
      },
      {
        "type": "object",
        "description": "Typed contract using the compact-form vocabulary subset.",
        "properties": {
          "type":            { "type": "string", "enum": ["string", "integer", "number", "boolean", "object", "array"] },
          "description":     { "type": "string" },
          "default":         {},
          "enum":            { "type": "array" },
          "min":             {},
          "max":             {},
          "minLength":       { "type": "integer" },
          "maxLength":       { "type": "integer" },
          "pattern":         { "type": "string" },
          "format":          { "type": "string" },
          "validate":        { "type": "string" },
          "validateMessage": { "type": "string" },
          "required":        { "type": "boolean" },
          "items":           {},
          "valuesType":      {},
          "properties":      { "type": "object" }
        },
        "additionalProperties": false
      }
    ]
  }
}`

var (
	parameterContractResolvedOnce sync.Once
	parameterContractResolved     *libschema.Resolved
	parameterContractResolvedErr  error
)

// loadParameterContractResolved lazily compiles parameterContractSchemaJSON
// into a *libschema.Resolved via the google/jsonschema-go library directly.
// The pkg/validator/jsonschema wrapper is intentionally bypassed because
// its deterministic walker does not validate root-level oneOf or
// additionalProperties — the parameter-contract schema's structure is
// exactly `{additionalProperties: {oneOf: [...]}}`, which the wrapper
// would silently skip. The library's Resolved.Validate covers the full
// JSON Schema 2020-12 surface including root-level oneOf semantics.
//
// Compilation happens once per process; subsequent calls return the
// cached resolved schema. Returns the compilation error if the literal
// fails to compile (programmer error — caught by tests).
func loadParameterContractResolved() (*libschema.Resolved, error) {
	parameterContractResolvedOnce.Do(func() {
		schema := &libschema.Schema{}
		if err := json.Unmarshal([]byte(parameterContractSchemaJSON), schema); err != nil {
			parameterContractResolvedErr = fmt.Errorf("unmarshal parameter-contract sub-schema: %w", err)
			return
		}
		resolved, err := schema.Resolve(nil)
		if err != nil {
			parameterContractResolvedErr = fmt.Errorf("resolve parameter-contract sub-schema: %w", err)
			return
		}
		parameterContractResolved = resolved
	})
	return parameterContractResolved, parameterContractResolvedErr
}

// validateParameterContractEnvelope runs the second-line-of-defense
// envelope validation on the shallow-loaded `spec.parameters` block.
// Primary enforcement is JSON Schema validation at workspace-load layer;
// this is the additional check applied at Step 1a's shallow-load path
// for paths that may not have gone through full envelope validation.
//
// The contract argument is the OrderedMap returned from
// LoadAggregateParameterContract's shallow unmarshal. Conversion to a
// generic any shape happens via JSON round-trip (OrderedMap →
// MarshalJSON preserves key order and produces standard JSON; the
// re-unmarshal lands in map[string]any). Validation findings convert to
// *ResolverError entries with ErrYAMLLoad as the sentinel
// (envelope-level failures are workspace-load issues, not contract
// violations).
//
// Returns an aggregated *ResolverError per the
// aggregateParameterViolations contract (single-violation case returns
// the lone error directly; multi-violation case aggregates into one
// multi-line-Message *ResolverError). Returns nil if the contract
// passes envelope validation.
func validateParameterContractEnvelope(contract types.OrderedMap[any], aggregatePath string) *ResolverError {
	resolved, err := loadParameterContractResolved()
	if err != nil {
		return &ResolverError{
			Entity:  "Aggregate",
			Field:   aggregatePath,
			Message: fmt.Sprintf("internal: failed to load parameter-contract sub-schema: %v", err),
			Err:     ErrYAMLLoad,
		}
	}

	// OrderedMap → generic any via JSON round-trip.
	raw, err := json.Marshal(contract)
	if err != nil {
		return &ResolverError{
			Entity:  "Aggregate",
			Field:   aggregatePath,
			Message: fmt.Sprintf("internal: failed to marshal contract for envelope validation: %v", err),
			Err:     ErrYAMLLoad,
		}
	}
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		return &ResolverError{
			Entity:  "Aggregate",
			Field:   aggregatePath,
			Message: fmt.Sprintf("internal: failed to unmarshal contract for envelope validation: %v", err),
			Err:     ErrYAMLLoad,
		}
	}

	// Library's Resolved.Validate returns a single non-nil error when the
	// instance fails — covers full JSON Schema 2020-12 surface including
	// root-level oneOf and additionalProperties (the catalog validator's
	// deterministic walker bypasses both root-level constructs).
	if vErr := resolved.Validate(instance); vErr != nil {
		return &ResolverError{
			Entity:  "Aggregate",
			Field:   ".spec.parameters",
			Message: fmt.Sprintf("envelope: %v", vErr),
			Err:     ErrYAMLLoad,
		}
	}
	return nil
}
