// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"encoding/json"
	"fmt"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cueerrors "cuelang.org/go/cue/errors"

	cueschemas "github.com/cognitio/aggrega-cli/pkg/schemas/cue"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// ValidateWorkload unifies a parsed Workload against the embedded
// `aggrega.ai/v1alpha1` Workload CUE schema. Returns nil when the value
// satisfies every constraint; otherwise returns a *WorkloadValidationError
// with one finding per CUE rule violation.
//
// Path B (per WI03 design): pkg/types is a CUE-free leaf package; this
// validator is the explicit load-time entry point invoked by Phase B.2 (and
// any other consumer that constructs a Workload programmatically and wants
// to verify its shape before handing it to a synthesizer).
//
// recipePath / recipeName / recipeVersion are threaded into the error for
// operator-facing message context; pass "" when not available (e.g., tests
// that build a Workload directly).
func ValidateWorkload(
	w *types.Workload,
	recipePath, recipeName, recipeVersion string,
) error {
	if w == nil {
		return nil
	}

	ctx := cuecontext.New()

	schemaVal := ctx.CompileString(cueschemas.WorkloadCUE)
	if err := schemaVal.Err(); err != nil {
		// The embedded CUE source is verified to compile by
		// pkg/schemas/cue/workload_test.go:TestWorkloadCUECompiles. A
		// failure here would mean an embed regression; surface it as a
		// single finding rather than panicking.
		return &WorkloadValidationError{
			Path:          recipePath,
			RecipeName:    recipeName,
			RecipeVersion: recipeVersion,
			Stage:         "schema",
			Findings: []WorkloadValidationFinding{{
				Constraint: "embedded Workload CUE source failed to compile: " + err.Error(),
			}},
			Err: ErrWorkloadValidation,
		}
	}

	// Marshal the typed Workload to JSON, then compile as CUE data. Mirrors
	// the existing evaluateSchema pattern in schema_engine.go.
	dataJSON, err := json.Marshal(w)
	if err != nil {
		return &WorkloadValidationError{
			Path:          recipePath,
			RecipeName:    recipeName,
			RecipeVersion: recipeVersion,
			Stage:         "schema",
			Findings: []WorkloadValidationFinding{{
				Constraint: fmt.Sprintf("failed to marshal Workload to JSON: %v", err),
			}},
			Err: ErrWorkloadValidation,
		}
	}

	dataVal := ctx.CompileBytes(dataJSON)
	if err := dataVal.Err(); err != nil {
		return &WorkloadValidationError{
			Path:          recipePath,
			RecipeName:    recipeName,
			RecipeVersion: recipeVersion,
			Stage:         "schema",
			Findings: []WorkloadValidationFinding{{
				Constraint: "failed to compile Workload as CUE: " + err.Error(),
			}},
			Err: ErrWorkloadValidation,
		}
	}

	// Look up the #Workload definition and unify the data against it.
	workloadDef := schemaVal.LookupPath(cue.ParsePath("#Workload"))
	if err := workloadDef.Err(); err != nil {
		return &WorkloadValidationError{
			Path:          recipePath,
			RecipeName:    recipeName,
			RecipeVersion: recipeVersion,
			Stage:         "schema",
			Findings: []WorkloadValidationFinding{{
				Constraint: "embedded Workload CUE missing #Workload definition: " + err.Error(),
			}},
			Err: ErrWorkloadValidation,
		}
	}

	unified := workloadDef.Unify(dataVal)
	if err := unified.Validate(cue.Concrete(true), cue.Final()); err != nil {
		findings := translateCUEErrorsToWorkloadFindings(err)
		return &WorkloadValidationError{
			Path:          recipePath,
			RecipeName:    recipeName,
			RecipeVersion: recipeVersion,
			Stage:         "schema",
			Findings:      findings,
			Err:           ErrWorkloadValidation,
		}
	}

	return nil
}

// translateCUEErrorsToWorkloadFindings adapts the existing CUE-error
// translation infrastructure (translate.go) into the WorkloadValidationFinding
// shape. Each CUE error becomes one finding with the offending field path and
// a sanitised constraint description.
func translateCUEErrorsToWorkloadFindings(err error) []WorkloadValidationFinding {
	cueErrs := cueerrors.Errors(err)
	if len(cueErrs) == 0 {
		return []WorkloadValidationFinding{{
			FieldPath:  ".",
			Constraint: "validation failed",
		}}
	}

	out := make([]WorkloadValidationFinding, 0, len(cueErrs))
	for _, ce := range cueErrs {
		out = append(out, oneCUEErrorToFinding(ce))
	}
	return out
}

func oneCUEErrorToFinding(ce error) WorkloadValidationFinding {
	path := cueErrorPath(ce)
	msg := cueerrors.Details(ce, nil)

	finding := WorkloadValidationFinding{FieldPath: path}

	switch {
	case containsAny(msg, "conflicting values"):
		finding.Constraint = extractConflict(msg)
		finding.Suggestion = "check that the value matches the expected type or enum"
	case containsAny(msg, "incomplete value"):
		finding.Constraint = "required field is missing"
		finding.Suggestion = "add the required field"
	case containsAny(msg, "not allowed"):
		finding.Constraint = "field not allowed by schema"
		finding.Suggestion = "remove the unexpected field"
	default:
		finding.Constraint = sanitizeCUEMessage(msg)
	}

	return finding
}

func containsAny(haystack string, needle string) bool {
	return len(haystack) >= len(needle) && (stringContains(haystack, needle))
}

// stringContains is a thin wrapper to avoid pulling strings into this file's
// imports just for one call (translate.go already imports strings; reusing
// extractConflict / sanitizeCUEMessage from there).
func stringContains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
