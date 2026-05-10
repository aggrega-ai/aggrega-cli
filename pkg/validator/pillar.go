// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"encoding/json"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// ValidatePillar validates a values dictionary against one named pillar inside a
// pillar-structured cueSchema (e.g. the top-level `values:` or `infra:` sub-schema
// in a catalog Component cueSchema).
//
// If the pillar is absent from the schema, returns nil (no-op) — a component
// whose cueSchema defines only values and omits infra means "no infra constraints."
//
// Raw CUE error messages are never surfaced; all errors are translated via the
// shared validateCUEValue pipeline.
func ValidatePillar(cueSource, pillarName string, values map[string]any, entity string) []*ValidationError {
	if cueSource == "" || pillarName == "" {
		return nil
	}

	ctx := cuecontext.New()

	schemaVal := ctx.CompileString(cueSource)
	if schemaVal.Err() != nil {
		return []*ValidationError{{
			Entity:     entity,
			FieldPath:  ".",
			Constraint: "cueSchema compilation failed",
			Suggestion: "Check the cueSchema syntax in the catalog entity",
			Err:        ErrSchemaValidation,
		}}
	}

	pillarVal := schemaVal.LookupPath(cue.ParsePath(pillarName))
	if !pillarVal.Exists() {
		// Schema doesn't declare this pillar — treat as unconstrained (no-op).
		return nil
	}

	valuesJSON, err := json.Marshal(values)
	if err != nil {
		return []*ValidationError{{
			Entity:     entity,
			FieldPath:  "." + pillarName,
			Constraint: "failed to marshal values for pillar validation",
			Err:        ErrSchemaValidation,
		}}
	}

	dataVal := ctx.CompileBytes(valuesJSON)
	if dataVal.Err() != nil {
		return []*ValidationError{{
			Entity:     entity,
			FieldPath:  "." + pillarName,
			Constraint: "failed to compile values as CUE",
			Err:        ErrSchemaValidation,
		}}
	}

	unified := pillarVal.Unify(dataVal)
	errs := validateCUEValue(unified, entity, ErrSchemaValidation)
	// Prefix field paths with the pillar so errors locate to e.g. ".values.port"
	// rather than just ".port" — gives callers the full address.
	for _, e := range errs {
		e.FieldPath = prefixPillar(pillarName, e.FieldPath)
	}
	return errs
}

func prefixPillar(pillar, fieldPath string) string {
	if fieldPath == "" || fieldPath == "." {
		return "." + pillar
	}
	// fieldPath starts with "." — e.g. ".port"; produce ".<pillar>.port".
	return "." + pillar + fieldPath
}
