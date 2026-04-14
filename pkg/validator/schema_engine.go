// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"encoding/json"
	"fmt"

	"cuelang.org/go/cue/cuecontext"
)

// evaluateSchema validates values against a CUE schema string.
// Returns nil if validation passes, or a slice of translated errors.
func evaluateSchema(cueSource string, values map[string]interface{}, entity string) []*ValidationError {
	if cueSource == "" {
		return nil
	}

	ctx := cuecontext.New()

	schemaVal := ctx.CompileString(cueSource)
	if schemaVal.Err() != nil {
		return []*ValidationError{{
			Entity:     entity,
			FieldPath:  ".",
			Constraint: "CUE schema compilation failed",
			Suggestion: "Check the cueSchema syntax in the catalog entity",
			Err:        ErrSchemaValidation,
		}}
	}

	valuesJSON, err := json.Marshal(values)
	if err != nil {
		return []*ValidationError{{
			Entity:     entity,
			FieldPath:  ".",
			Constraint: fmt.Sprintf("failed to marshal values: %v", err),
			Err:        ErrSchemaValidation,
		}}
	}

	dataVal := ctx.CompileBytes(valuesJSON)
	if dataVal.Err() != nil {
		return []*ValidationError{{
			Entity:     entity,
			FieldPath:  ".",
			Constraint: "failed to compile values as CUE",
			Err:        ErrSchemaValidation,
		}}
	}

	unified := schemaVal.Unify(dataVal)
	return validateCUEValue(unified, entity, ErrSchemaValidation)
}
