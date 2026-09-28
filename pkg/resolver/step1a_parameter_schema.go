// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"encoding/json"
	"fmt"
	"sync"

	libschema "github.com/google/jsonschema-go/jsonschema"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
)

// parameterContractSchemaJSON is the hardcoded map-level sub-schema for
// the Aggregate `spec.parameters:` block. It mirrors the canonical
// `properties.spec.properties.parameters` subtree of
// `aggrega-schemas/json/aggregate-v1alpha1.schema.json` —
// specifically the whole `{type: object, additionalProperties:
// {oneOf: [...]}}` shape, NOT the inner per-parameter `oneOf` alone.
//
// The mirror includes the `x-aggrega-go-type` marker the canonical carries on
// the parameters node — it names the Go field type for struct generation and
// is inert to JSON Schema validation; it is present here only to keep this
// literal byte-equivalent to the canonical subtree.
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
  "x-aggrega-go-type": "OrderedMap[any]",
  "description": "Global environment contract. Each entry is a typed-contract object authoring the compact-form vocabulary subset (type, description, default, enum, min/max, minLength/maxLength, pattern, format, validate, validateMessage, required, items, valuesType, properties). Nested structure is expressed via the typed-form properties: sub-field. Release fulfillment may be inline (parameters: {...}) or single-source file import (parameters: {$import: <path>}); the parameter-contract layer validates the merged map against this contract before parameter interpolation. See aggrega-docs/language-reference/entities/aggregate.md for the full vocabulary.",
  "additionalProperties": {
    "type": "object",
    "description": "Typed contract authoring the compact-form vocabulary subset.",
    "properties": {
      "type":            { "type": "string", "enum": ["string", "integer", "number", "boolean", "object", "array"] },
      "description":     { "type": "string" },
      "default":         {},
      "enum":            { "type": "array" },
      "min":             {},
      "max":             {},
      "minLength":       { "type": "integer", "minimum": 0 },
      "maxLength":       { "type": "integer", "minimum": 0 },
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
}`

var (
	parameterContractValidatorOnce sync.Once
	parameterContractValidator     jsonvalidator.Validator
	parameterContractValidatorErr  error
)

// loadParameterContractValidator lazily compiles parameterContractSchemaJSON
// into a jsonvalidator.Validator via the pkg/validator/jsonschema wrapper.
// The wrapper's deterministic walker covers the schema shapes the
// parameter-contract sub-schema uses (root-level `additionalProperties:
// <schema>` with `additionalProperties: false` on the inner typed-form
// object), emitting findings deterministically across runs.
//
// Compilation happens once per process; subsequent calls return the
// cached validator. Returns the compilation error if the literal
// fails to compile (programmer error — caught by tests).
func loadParameterContractValidator() (jsonvalidator.Validator, error) {
	parameterContractValidatorOnce.Do(func() {
		schema := &libschema.Schema{}
		if err := json.Unmarshal([]byte(parameterContractSchemaJSON), schema); err != nil {
			parameterContractValidatorErr = fmt.Errorf("unmarshal parameter-contract sub-schema: %w", err)
			return
		}
		v, err := jsonvalidator.New(schema)
		if err != nil {
			parameterContractValidatorErr = fmt.Errorf("compile parameter-contract sub-schema: %w", err)
			return
		}
		parameterContractValidator = v
	})
	return parameterContractValidator, parameterContractValidatorErr
}

// validateParameterContractEnvelope runs the second-line-of-defense
// envelope validation on the shallow-loaded `spec.parameters` block.
// Primary enforcement is JSON Schema validation at workspace-load layer;
// this is the additional check applied at parameter-contract validation's
// shallow-load path for paths that may not have gone through full envelope
// validation.
//
// The contract argument is the OrderedMap returned from
// LoadAggregateParameterContract's shallow unmarshal. Conversion to a
// generic any shape happens via JSON round-trip (OrderedMap →
// MarshalJSON preserves key order and produces standard JSON; the
// re-unmarshal lands in map[string]any).
//
// Per-finding ResolverErrors aggregate via aggregateParameterViolations
// for consistency with the contract-validation multi-violation path.
// Envelope-level failures carry ErrYAMLLoad as the sentinel
// (workspace-load issues, not contract violations).
//
// Returns nil if the contract passes envelope validation.
func validateParameterContractEnvelope(contract types.OrderedMap[any], aggregatePath string) error {
	v, err := loadParameterContractValidator()
	if err != nil {
		return &ResolverError{
			Entity:  "Aggregate",
			Field:   aggregatePath,
			Message: fmt.Sprintf("internal: failed to load parameter-contract sub-schema validator: %v", err),
			Err:     ErrYAMLLoad(),
		}
	}

	// OrderedMap → generic any via JSON round-trip.
	raw, err := json.Marshal(contract)
	if err != nil {
		return &ResolverError{
			Entity:  "Aggregate",
			Field:   aggregatePath,
			Message: fmt.Sprintf("internal: failed to marshal contract for envelope validation: %v", err),
			Err:     ErrYAMLLoad(),
		}
	}
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		return &ResolverError{
			Entity:  "Aggregate",
			Field:   aggregatePath,
			Message: fmt.Sprintf("internal: failed to unmarshal contract for envelope validation: %v", err),
			Err:     ErrYAMLLoad(),
		}
	}

	rpt, vErr := v.Validate(instance, nil)
	if vErr != nil {
		return &ResolverError{
			Entity:  "Aggregate",
			Field:   ".spec.parameters",
			Message: fmt.Sprintf("envelope-validation execution failed: %v", vErr),
			Err:     ErrYAMLLoad(),
		}
	}
	if len(rpt.Findings) == 0 {
		return nil
	}

	verrs := make([]*ResolverError, 0, len(rpt.Findings))
	for _, f := range rpt.Findings {
		field := ".spec.parameters"
		if f.FieldPath != "" {
			field = ".spec.parameters." + f.FieldPath
		}
		verrs = append(verrs, &ResolverError{
			Entity:  "Aggregate",
			Field:   field,
			Message: fmt.Sprintf("envelope: %s: %s", f.Keyword, f.Message),
			Err:     ErrYAMLLoad(),
		})
	}
	return aggregateParameterViolations(verrs)
}
