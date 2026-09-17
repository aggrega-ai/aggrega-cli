// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package featurerecipe_pipeline

import (
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ErrPipelineTargetAbsent fires when a patch addresses a document not
// present in the keyed set — a scope/key pair the set does not hold. It is
// wrapped with the offending scope, key, and step id at the emission site.
//
// This is the walk's local surface for the absent-target condition. The
// renderer integration that wires the walk into the render loop promotes
// this to its own build-halting Loud Guardrail; the walk reports the
// typed cause so callers can match it with errors.Is.
func ErrPipelineTargetAbsent() error { return errPipelineTargetAbsent }

var errPipelineTargetAbsent = cerrors.Register("pipeline-target-absent", cerrors.Meta{
	ShortCode: "E0011",
	Message:   "pipeline patch targets a document not present in the set",
	RulePlace: cerrors.NotARule("A patch addresses a scope and key the document set does not hold. Every operation " +
		"reaching this check is one a guest returned at render time — a pipeline step's patches over the " +
		"documents a node emitted, or a provider's cross-boundary link wiring over the files a dispatch " +
		"group assembled — so the address it names is a product of the render rather than anything an " +
		"author wrote. No predicate over authored content decides it."),
})

// ErrPipelineStepFailed is the walk's umbrella sentinel for a step that
// failed for any reason other than an absent patch target — a guest trap,
// a malformed step output, a halt-severity diagnostic, a malformed
// document in the set, or a patch the apply engine rejected. The
// underlying cause is wrapped with %w so a caller can still match the
// specific engine error (for example jsonpatch.ErrTestFailed) through it.
func ErrPipelineStepFailed() error { return errPipelineStepFailed }

var errPipelineStepFailed = cerrors.Register("pipeline-step-failed", cerrors.Meta{
	ShortCode: "E0010",
	Message:   "pipeline step failed",
	RulePlace: cerrors.NotARule("A pipeline step did not run to completion — a guest trap, a malformed step output, a " +
		"halt-severity diagnostic the step returned, or a patch the apply engine rejected. It names " +
		"an operation that failed and carries the cause it wraps; no predicate over authored content " +
		"decides it."),
})

// ErrPipelineContractViolation fires when a step's typed input or output does
// not satisfy the schema its export declares: the rendered `with` input does
// not match the export's declared input schema, or the guest's returned outputs
// do not match its declared output schema. A declared typed-I/O contract is an
// authoring commitment, so a violation halts the walk as a Loud Guardrail — even
// for an optional step, the same way a broken `when`/`with` predicate halts
// regardless of Optional. The wrapping caller names the offending step and
// keeps this sentinel in the unwrap chain so callers match it with errors.Is.
func ErrPipelineContractViolation() error { return errPipelineContractViolation }

var errPipelineContractViolation = cerrors.Register("pipeline-contract-violation", cerrors.Meta{
	ShortCode: "E0009",
	Message:   "pipeline step typed-I/O contract violation",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrPipelineTargetOpaque fires when a patch addresses a unit the codec
// declined to give a tree-view — an opaque unit (a binary blob, free text,
// or a region the provider cannot safely tree). An opaque unit is handed to
// the guest tagged with its media type and bytes, so it is a SELECT and EMIT
// target, but it carries no RFC-6902-addressable tree, so a PATCH against it
// halts rather than silently skipping. What counts as structured is the
// codec's call, not a fixed parser-accepted test.
//
// This is the walk's local surface for the not-structured condition. The
// renderer integration promotes it to its own build-halting Loud Guardrail;
// the walk reports the typed cause so callers can match it with errors.Is.
func ErrPipelineTargetOpaque() error { return errPipelineTargetOpaque }

var errPipelineTargetOpaque = cerrors.Register("pipeline-target-opaque", cerrors.Meta{
	ShortCode: "E0012",
	Message:   "pipeline patch targets an opaque (non-structured) unit",
	RulePlace: cerrors.NotARule("A patch addresses a unit the codec gave no tree-view, so there is no addressable tree " +
		"to apply it against. Both sides of that are render-time products: the operations are ones a guest " +
		"returned — a pipeline step's patches over the documents a node emitted, or a provider's " +
		"cross-boundary link wiring over the files a dispatch group assembled — and whether a unit is " +
		"structured is the codec's own call over those same documents. No predicate over authored content " +
		"decides it."),
})
