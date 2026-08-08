// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"encoding/json"
	"errors"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
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
// It validates the typed value re-marshalled to JSON, which is a
// different instance from the document the value was decoded from: a
// field whose zero value is elided on marshal is absent here even when
// the document carried it as an explicit empty value, so a schema
// constraint requiring that field fails here and holds in the document.
// That is why this entry point is not subsumed by ValidateWorkloadBytes,
// and why a caller holding both the bytes and the typed value runs both.
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

	// Marshal the typed Workload to JSON, then unmarshal as a generic
	// any-typed value. The validator's instance walk handles
	// map[string]any; this round-trip normalises typed fields into
	// the shape the walker expects.
	dataJSON, err := json.Marshal(w)
	if err != nil {
		return workloadPipelineError(recipePath, recipeName, recipeVersion,
			"failed to marshal Workload to JSON: "+err.Error())
	}
	var instance any
	if err := json.Unmarshal(dataJSON, &instance); err != nil {
		return workloadPipelineError(recipePath, recipeName, recipeVersion,
			"failed to unmarshal Workload JSON for validation: "+err.Error())
	}

	// The shared envelope component owns the compiled Workload schema
	// (one compile, cached across recipes); engine-level failures —
	// including an embed regression — surface as a single finding
	// rather than a panic.
	findings, err := envelope.ValidateInstance(envelope.KindWorkload, instance)
	if err != nil {
		return workloadPipelineError(recipePath, recipeName, recipeVersion,
			"Workload validation pipeline error: "+err.Error())
	}

	if len(findings) == 0 {
		return nil
	}

	return workloadSchemaError(recipePath, recipeName, recipeVersion, findings)
}

// ValidateWorkloadBytes validates a raw Workload document — the bytes as
// authored or as a template produced them — against the embedded
// aggrega.ai/v1alpha1 Workload JSON Schema 2020-12. Returns nil when the
// document satisfies every constraint; otherwise returns a
// *WorkloadValidationError in the same shape ValidateWorkload produces, so
// both entry points are indistinguishable to a caller formatting the result.
//
// This is the entry point for a document that exists as bytes. It observes
// constraints the typed-value entry point structurally cannot: the schema
// closes the document to its declared keys, while a decode into the typed
// model silently discards a key the model does not name. The two are
// complementary rather than ordered by strictness — see ValidateWorkload for
// what the typed entry point observes that this one does not.
//
// recipePath / recipeName / recipeVersion are threaded into the error for
// operator-facing message context; pass "" when not available.
func ValidateWorkloadBytes(data []byte, recipePath, recipeName, recipeVersion string) error {
	_, err := envelope.ValidateBytes(envelope.KindWorkload, data, recipePath)
	if err == nil {
		return nil
	}
	var eerr *envelope.Error
	if errors.As(err, &eerr) {
		return workloadSchemaError(recipePath, recipeName, recipeVersion, eerr.Findings)
	}
	// Parse and engine failures carry no per-field findings, so they take the
	// findings-free form rather than an empty Findings slice that would render
	// as a header with nothing under it.
	return workloadPipelineError(recipePath, recipeName, recipeVersion,
		"Workload validation pipeline error: "+err.Error())
}

// workloadSchemaError is the single construction site for a per-field
// Workload schema failure. Both Workload entry points route through it so the
// error contract — path, entity name, version, stage, leading-dot field
// paths, sentinel — cannot drift between them.
func workloadSchemaError(recipePath, recipeName, recipeVersion string, findings []jsonvalidator.Finding) *WorkloadValidationError {
	return &WorkloadValidationError{
		Path:          recipePath,
		RecipeName:    recipeName,
		RecipeVersion: recipeVersion,
		Stage:         "schema",
		Findings:      findingsToWorkloadFindings(findings),
		Err:           ErrWorkloadValidation(),
	}
}

// workloadPipelineError is the single construction site for a Workload
// validation failure that has no per-field findings — a marshal, parse, or
// engine-level failure. The whole reason is carried as one finding's
// constraint text so the rendered message stays a single grouped report.
func workloadPipelineError(recipePath, recipeName, recipeVersion, constraint string) *WorkloadValidationError {
	return &WorkloadValidationError{
		Path:          recipePath,
		RecipeName:    recipeName,
		RecipeVersion: recipeVersion,
		Stage:         "schema",
		Findings: []WorkloadValidationFinding{{
			Constraint: constraint,
		}},
		Err: ErrWorkloadValidation(),
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
