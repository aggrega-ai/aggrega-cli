// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
// explicit load-time entry point invoked by workload synthesis (and any other
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

	return workloadSchemaError(recipePath, recipeName, recipeVersion, findings, instance)
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
	instance, err := envelope.ValidateBytes(envelope.KindWorkload, data, recipePath)
	if err == nil {
		return nil
	}
	var eerr *envelope.Error
	if errors.As(err, &eerr) {
		return workloadSchemaError(recipePath, recipeName, recipeVersion, eerr.Findings, instance)
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
//
// instance is the validated document as it decoded, and it is what lets a
// finding carry advice as well as a verdict; pass nil where the caller does not
// hold one, which yields findings with no advice rather than wrong advice.
func workloadSchemaError(
	recipePath, recipeName, recipeVersion string,
	findings []jsonvalidator.Finding,
	instance any,
) *WorkloadValidationError {
	return &WorkloadValidationError{
		Path:          recipePath,
		RecipeName:    recipeName,
		RecipeVersion: recipeVersion,
		Stage:         "schema",
		Findings:      findingsToWorkloadFindings(findings, instance),
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
// into the leading-dot form (".containers.main.health") every consumer of
// WorkloadValidationError reads. Each finding's Message becomes the
// Constraint, and its Suggestion is whatever the validated document itself
// supports saying about the value at that path.
//
// The advice is derived HERE rather than left to a caller further out, and
// that is the point: this is the last place holding both the verdict and the
// document it was reached over, and a Workload is a document nobody authored
// directly — a Recipe's template produced it — so a caller further out has the
// finding without the value it landed on.
func findingsToWorkloadFindings(findings []jsonvalidator.Finding, instance any) []WorkloadValidationFinding {
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
			Suggestion: renderedScalarAdvice(instance, f.FieldPath),
		})
	}
	return out
}

// adviceQuoteTheExpression states, for a value the rendered document carries
// as something other than text, what YAML made of it and what an author who
// meant text does about it. The type word is the only part that varies, and
// its article is fixed here because the two words that reach it both take
// "a" — a third that did not would need the article to vary with it.
//
// It says what happened before it says what to do, because the author did not
// write this document: a Recipe's template did, and an expression left
// unquoted is read as whatever its expansion looks like. A whole-core sizing
// quantity, a version that is all digits and a port are the values this
// reaches, and none of them looks wrong in the Recipe.
//
// The remedy is conditional — "where the key takes text" — and that
// conditional is load-bearing rather than hedging. What the key requires is
// stated by the constraint this advice follows, which is where the schema's
// own verdict already is; repeating a judgement about the key here would be a
// second, weaker copy of that verdict, and it would be wrong for a key that
// genuinely takes a number and failed some other constraint.
const adviceQuoteTheExpression = "YAML read this unquoted value as a %s; " +
	"where the key takes text, quote the expression in the Recipe"

// renderedScalarAdvice returns the advice for a finding whose value in the
// rendered document arrived as a number or a boolean, and the empty string for
// every other finding.
//
// A quoted expression expands into text and decodes as a string; an unquoted
// one decodes as whatever its expansion looks like. So the decoded value is
// itself the answer to "was this quoted", read from the document rather than
// from the failure text — which is what keeps this independent of how the
// schema engine words its verdict. A wording change there cannot silently stop
// this from firing, because this never reads that wording.
//
// An absent value is deliberately left without advice. It reads as a missing
// quotation and is not one: nothing was rendered at all, so quoting would
// produce empty text rather than the value the author expected, and the advice
// would send them to the wrong document.
func renderedScalarAdvice(instance any, fieldPath string) string {
	value, found := valueAtInstancePath(instance, fieldPath)
	if !found {
		return ""
	}
	switch jsonvalidator.JSONTypeName(value) {
	case "number", "boolean":
		return fmt.Sprintf(adviceQuoteTheExpression, jsonvalidator.JSONTypeName(value))
	default:
		return ""
	}
}

// valueAtInstancePath returns the value a dotted field path names in a decoded
// document, and whether the path resolved to one exactly.
//
// It is a descent over a DECODED value, and it is deliberately not the
// field-path-to-node descent pkg/yamlpos owns. That one walks a parse tree to
// find where a key sits in a file, and it falls back to the nearest enclosing
// key it did reach so that a finding about an absent key still gets a position.
// Both properties are wrong here: this reads what a value turned out to BE, so
// a coarser answer is not a coarser position but a different value, and the
// fallback would report a containing object's type as though it were the
// leaf's. Hence the exact-match report, and hence no reuse.
//
// A path crossing an array resolves to nothing rather than guessing which
// element was meant. The engine addresses array positions in the same dotted
// form as object keys, so an index is indistinguishable from a key here; a walk
// that tried both would answer for whichever the document happened to hold.
func valueAtInstancePath(instance any, fieldPath string) (any, bool) {
	path := strings.TrimPrefix(fieldPath, ".")
	if path == "" {
		return nil, false
	}
	current := instance
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}
