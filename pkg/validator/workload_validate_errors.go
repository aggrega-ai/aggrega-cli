// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"errors"
	"fmt"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// Workload-validation sentinels. Two are surfaced today: the schema-
// validation failure (rule violations on the embedded Workload JSON
// Schema) and the cross-wave secretsSchema check (Workload secret
// references vs the Component's secretsSchema slot declarations).
var (
	// ErrWorkloadValidation: schema-validation failure on the parsed
	// Workload against the embedded Workload JSON Schema.
	ErrWorkloadValidation = errors.New("workload validation failed")

	// ErrWorkloadSecretsSchema: cross-wave check failure between Workload
	// secret references and the Component's secretsSchema slot allowlist.
	// Surfaces when a Workload's secrets entry names an undeclared slot,
	// uses a delivery mechanism (`as:`) not in the slot's `inject:`
	// allowlist, or references a blackbox slot (Inject == nil/empty,
	// reserved for Recipe-internal plumbing).
	ErrWorkloadSecretsSchema = errors.New("workload secretsSchema cross-check failed")
)

// WorkloadValidationError carries the structured detail for a Workload
// validation failure (schema validation or secretsSchema cross-check). The
// Error() output uses the same CATALOG_ERROR-prefixed format as
// RecipeValidationError so operator-facing messages are uniform across
// load-time validation surfaces.
type WorkloadValidationError struct {
	// Path is the Recipe file path the Workload was loaded from. Empty when
	// the Workload was constructed programmatically (tests, synthesizers).
	Path string
	// RecipeName is the owning Recipe's metadata.name; empty when not
	// available.
	RecipeName string
	// RecipeVersion is the owning Recipe's metadata.version; empty when not
	// available.
	RecipeVersion string
	// Stage is "schema" for schema-validation failures; "secretsSchema"
	// for cross-wave check failures.
	Stage string
	// Findings lists per-field failures. Each entry is a path + reason +
	// optional fix suggestion. Schema validation produces one Finding
	// per validator error; secretsSchema produces one Finding per
	// offending entry.
	Findings []WorkloadValidationFinding
	// Err is the sentinel for errors.Is matching.
	Err error
}

// WorkloadValidationFinding is one per-field failure inside a
// WorkloadValidationError.
type WorkloadValidationFinding struct {
	FieldPath  string // e.g. ".containers.main.health.ready"
	Constraint string // human-readable rule violated
	Suggestion string // optional fix hint
}

// Error formats the validation failure as a CATALOG_ERROR-prefixed message.
// Each finding renders as an indented detail line so a multi-failure error
// reads as a single grouped report rather than N separate errors.
func (e *WorkloadValidationError) Error() string {
	header := "CATALOG_ERROR: Workload"
	if e.RecipeName != "" || e.RecipeVersion != "" {
		header += fmt.Sprintf(" (in Recipe %s:%s)", e.RecipeName, e.RecipeVersion)
	}
	if e.Path != "" {
		header += " " + e.Path
	}
	header += " — " + e.Stage + " validation failed:"

	if len(e.Findings) == 0 {
		return header
	}

	var b strings.Builder
	b.WriteString(header)
	for _, f := range e.Findings {
		b.WriteString("\n  ")
		if f.FieldPath != "" {
			b.WriteString(f.FieldPath + ": ")
		}
		b.WriteString(f.Constraint)
		if f.Suggestion != "" {
			b.WriteString(" — " + f.Suggestion)
		}
	}
	return b.String()
}

// Unwrap returns the sentinel for errors.Is matching.
func (e *WorkloadValidationError) Unwrap() error {
	return e.Err
}

// ToCompilerError converts the structured WorkloadValidationError into a
// *cerrors.CompilerError carrying CategoryCatalogError. Workload spec
// validation failures are catalog-domain — the Recipe author wrote the
// workload block; the Aggregate / Release didn't author it. The Source
// location maps Path (the Recipe file) into SourceLocation.File. The
// Message preserves the multi-finding shape from Error() so operator-
// facing presentation is consistent regardless of conversion path.
//
// Inner error chain preserved via cerrors.Wrap so errors.Is against
// ErrWorkloadValidation / ErrWorkloadSecretsSchema continues to match.
func (e *WorkloadValidationError) ToCompilerError() *cerrors.CompilerError {
	return cerrors.Wrap(e, cerrors.CatalogError, cerrors.SourceLocation{File: e.Path}, e.Error())
}
