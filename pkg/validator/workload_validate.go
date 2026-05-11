// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"encoding/json"

	libschema "github.com/google/jsonschema-go/jsonschema"

	jsonschemas "github.com/cognitio/aggrega-cli/pkg/schemas/jsonschema"
	"github.com/cognitio/aggrega-cli/pkg/types"
	jsonvalidator "github.com/cognitio/aggrega-cli/pkg/validator/jsonschema"
)

// ValidateWorkload validates a parsed Workload against the embedded
// aggrega.ai/v1alpha1 Workload JSON Schema 2020-12. Returns nil when
// the value satisfies every constraint; otherwise returns a
// *WorkloadValidationError with one finding per violation.
//
// pkg/types is a validator-free leaf package; this validator is the
// explicit load-time entry point invoked by Phase B.2 (and any other
// consumer that constructs a Workload programmatically and wants to
// verify its shape before handing it to a synthesizer).
//
// recipePath / recipeName / recipeVersion are threaded into the
// error for operator-facing message context; pass "" when not
// available (e.g., tests that build a Workload directly).
func ValidateWorkload(
	w *types.Workload,
	recipePath, recipeName, recipeVersion string,
) error {
	if w == nil {
		return nil
	}

	// Unmarshal the embedded JSON Schema into a library Schema.
	// The embedded schema is verified to parse by
	// pkg/schemas/jsonschema/workload_test.go:TestWorkloadJSONSchemaParses;
	// a failure here would mean an embed regression, surfaced as a
	// single finding rather than a panic.
	schema := &libschema.Schema{}
	if err := json.Unmarshal([]byte(jsonschemas.WorkloadJSONSchema), schema); err != nil {
		return &WorkloadValidationError{
			Path:          recipePath,
			RecipeName:    recipeName,
			RecipeVersion: recipeVersion,
			Stage:         "schema",
			Findings: []WorkloadValidationFinding{{
				Constraint: "embedded Workload JSON Schema failed to parse: " + err.Error(),
			}},
			Err: ErrWorkloadValidation,
		}
	}

	v, err := jsonvalidator.New(schema)
	if err != nil {
		return &WorkloadValidationError{
			Path:          recipePath,
			RecipeName:    recipeName,
			RecipeVersion: recipeVersion,
			Stage:         "schema",
			Findings: []WorkloadValidationFinding{{
				Constraint: "embedded Workload JSON Schema failed to resolve: " + err.Error(),
			}},
			Err: ErrWorkloadValidation,
		}
	}

	// Marshal the typed Workload to JSON, then unmarshal as a generic
	// any-typed value. The validator's instance walk handles
	// map[string]any; this round-trip normalises typed fields into
	// the shape the walker expects.
	dataJSON, err := json.Marshal(w)
	if err != nil {
		return &WorkloadValidationError{
			Path:          recipePath,
			RecipeName:    recipeName,
			RecipeVersion: recipeVersion,
			Stage:         "schema",
			Findings: []WorkloadValidationFinding{{
				Constraint: "failed to marshal Workload to JSON: " + err.Error(),
			}},
			Err: ErrWorkloadValidation,
		}
	}
	var instance any
	if err := json.Unmarshal(dataJSON, &instance); err != nil {
		return &WorkloadValidationError{
			Path:          recipePath,
			RecipeName:    recipeName,
			RecipeVersion: recipeVersion,
			Stage:         "schema",
			Findings: []WorkloadValidationFinding{{
				Constraint: "failed to unmarshal Workload JSON for validation: " + err.Error(),
			}},
			Err: ErrWorkloadValidation,
		}
	}

	rpt, err := v.Validate(instance, nil)
	if err != nil {
		return &WorkloadValidationError{
			Path:          recipePath,
			RecipeName:    recipeName,
			RecipeVersion: recipeVersion,
			Stage:         "schema",
			Findings: []WorkloadValidationFinding{{
				Constraint: "Workload validation pipeline error: " + err.Error(),
			}},
			Err: ErrWorkloadValidation,
		}
	}

	if len(rpt.Findings) == 0 {
		return nil
	}

	return &WorkloadValidationError{
		Path:          recipePath,
		RecipeName:    recipeName,
		RecipeVersion: recipeVersion,
		Stage:         "schema",
		Findings:      findingsToWorkloadFindings(rpt.Findings),
		Err:           ErrWorkloadValidation,
	}
}

// findingsToWorkloadFindings adapts validator Findings into the
// WorkloadValidationFinding shape. FieldPath converts from the
// validator's instance-relative dot-delimited form ("containers.main.health")
// into the legacy leading-dot form (".containers.main.health") expected
// by downstream consumers of WorkloadValidationError. Each finding's
// Message becomes the Constraint; Suggestion stays empty by default
// (callers may layer suggestions in a higher-level error formatter).
func findingsToWorkloadFindings(findings []jsonvalidator.Finding) []WorkloadValidationFinding {
	out := make([]WorkloadValidationFinding, 0, len(findings))
	for _, f := range findings {
		path := f.FieldPath
		if path != "" {
			path = "." + path
		} else {
			path = "."
		}
		out = append(out, WorkloadValidationFinding{
			FieldPath:  path,
			Constraint: f.Message,
		})
	}
	return out
}
