// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validator

import (
	"fmt"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// Workload-validation sentinels. Each names one check over a Workload a Recipe
// or FeatureRecipe renders. The checks this package performs report through the
// grouped WorkloadValidationError below; a condition the renderer raises from a
// declared rule reports as that rule's own finding instead, and its declaration
// says so. The declarations are the list: no count of them is kept here, because
// a count written beside them goes stale the day a check is added.

// ErrWorkloadValidation: schema-validation failure on the parsed
// Workload against the embedded Workload JSON Schema.
func ErrWorkloadValidation() error { return errWorkloadValidation }

var errWorkloadValidation = cerrors.Register("workload-not-valid", cerrors.Meta{
	ShortCode: "E0214",
	Message:   "workload validation failed",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrWorkloadFeaturesSchema: cross-entity check failure between Workload
// attachment references and the Component's featuresSchema slot allowlist.
// Surfaces when a Workload's attachments entry names an undeclared slot,
// uses a delivery mechanism (`as:`) not in the slot's `inject:`
// allowlist, or references a blackbox slot (Inject == nil/empty,
// reserved for Recipe-internal plumbing).
func ErrWorkloadFeaturesSchema() error { return errWorkloadFeaturesSchema }

var errWorkloadFeaturesSchema = cerrors.Register("workload-features-schema", cerrors.Meta{
	ShortCode: "E0213",
	Message:   "workload featuresSchema cross-check failed",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrSlotNotReferenceable: a Workload attachment reference names a slot
// whose resolved provides does not expose the contract keys a workload
// reference reads (name, key). The slot's fill publishes no locator to
// point at, so it cannot be referenced by name from a Workload.
// Structural key-presence only; no value inspection.
func ErrSlotNotReferenceable() error { return errSlotNotReferenceable }

var errSlotNotReferenceable = cerrors.Register("slot-not-referenceable", cerrors.Meta{
	ShortCode: "E0212",
	Message:   "workload attachment slot is not referenceable",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrDuplicateContainerEnvironmentVariable: one container of a Workload gives
// one environment variable name two values — as a `variables` entry and as an
// `attachments` entry of the same name delivered `as: env`. A provider writes
// the two kinds of entry in a fixed order of its own, so which value the process
// saw would follow from where each kind lands rather than from anything the
// author wrote.
//
// It is raised by the renderer, on the host and once whichever provider renders
// the Workload, from a declared rule over the parsed Workload of a Recipe, of a
// FeatureRecipe an attachment binds on a component, and of a FeatureRecipe a
// boundary-scope attachment binds. The rule reads names only and never a value,
// and it reports through its own finding rather than through
// WorkloadValidationError: one finding per pair, naming the variable and the
// container, positioned at the Workload key of the document that declares it.
func ErrDuplicateContainerEnvironmentVariable() error {
	return errDuplicateContainerEnvironmentVariable
}

var errDuplicateContainerEnvironmentVariable = cerrors.Register("duplicate-container-environment-variable", cerrors.Meta{
	ShortCode: "E0345",
	Message:   "a container declares one environment variable twice",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// WorkloadValidationError carries the structured detail for a Workload
// validation failure, whichever of this package's Workload checks raised it.
//
// It is a GROUPED report rather than a singular failure: one detected
// condition yields one error carrying every offending field, so an author
// reading it sees the whole Workload's faults at once instead of fixing them
// one build at a time. That cardinality is why it keeps a shape of its own
// rather than reporting through ValidationError, which carries exactly one
// detail. Its Error output opens with the entity reference and leaves the
// category to the envelope, so a rendered diagnostic names the category once.
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
	// Stage is "schema" for schema-validation failures, "featuresSchema" for
	// cross-entity slot-declaration check failures, and "slotReferenceability"
	// for slot-provides key-presence failures.
	Stage string
	// Findings lists per-field failures. Each entry is a path + reason +
	// optional fix suggestion. Schema validation produces one Finding
	// per validator error; featuresSchema produces one Finding per
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

// Error formats the validation failure. The header opens with the condition's
// registered sentence and names the occurrence after it — the Workload, its
// owning Recipe, the file, and the stage that checked it — and each finding
// then renders as an indented detail line, so a multi-failure error reads as a
// single grouped report rather than N separate errors.
//
// The sentence is read off the condition rather than written here, so the words
// a reader sees have one home. The header states no framing of its own: that
// this stage's validation failed is exactly what the registered sentence says,
// and a header repeating it would state the condition twice in one line. The
// stage itself is an occurrence value, since which check ran is a property of
// the occurrence; the wording comes from the condition.
//
// It does not name its own category. The category is a rendering concern the
// diagnostic envelope owns and states once, as its leading tag; a message that
// also opened with the category word printed it twice in one diagnostic. The
// deeper reason the two must stay identical is that the chain walk suppresses a
// terminal frame only when the text it would add is already present — so an
// Error output that is not exactly what the envelope's message was built from
// renders a second time as a "caused by:" continuation, repeating the whole
// report. One rendering feeding both surfaces is what keeps that from
// happening, rather than agreement that has to be maintained, and it is why
// ToCompilerError passes this output rather than composing a second one.
func (e *WorkloadValidationError) Error() string {
	values := make([]string, 0, 3)
	if e.RecipeName != "" || e.RecipeVersion != "" {
		values = append(values, fmt.Sprintf("Workload in Recipe %s:%s", e.RecipeName, e.RecipeVersion))
	} else {
		values = append(values, "Workload")
	}
	if e.Path != "" {
		values = append(values, "at "+e.Path)
	}
	if e.Stage != "" {
		values = append(values, e.Stage+" stage")
	}
	header := cerrors.ComposeMessage(e.Err, values, "")

	if len(e.Findings) == 0 {
		return header
	}

	var b strings.Builder
	b.WriteString(header)
	b.WriteString(":")
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
// Message is exactly the Error output, so operator-facing presentation is
// consistent regardless of conversion path and the diagnostic is said once.
//
// Inner error chain preserved via cerrors.Wrap so errors.Is against
// ErrWorkloadValidation / ErrWorkloadFeaturesSchema continues to match.
func (e *WorkloadValidationError) ToCompilerError() *cerrors.CompilerError {
	return cerrors.Wrap(e, cerrors.CatalogError, cerrors.SourceLocation{File: e.Path}, e.Error())
}
