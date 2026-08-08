// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ErrSelectorViolation is the ABI-stable sentinel for the
// usesSchema-selector and Feature-appliesTo cross-node type-safety
// checks Step 4 performs. Each violation is recorded as an
// [AnalysisError] entry on [AnalysisResult].Errors with this sentinel
// in its Sentinel field; [AnalysisError.ToCompilerError] wraps it as
// the CompilerError's inner error, so the joined build error is
// errors.Is-recoverable at the pipeline boundary. Both the selector
// and the appliesTo checks carry this one sentinel.
func ErrSelectorViolation() error { return errSelectorViolation }

var errSelectorViolation = cerrors.Register("compiler-analyzer/selector-violation", cerrors.Meta{
	Message: "selector violation",
})

// ErrSlotSelectorUnsatisfied is the ABI-stable sentinel for an attachment
// slot whose declared `selector` is not satisfied by the labels of the
// Feature attached to fill it — the slot analog of a usesSchema socket
// selector violation, evaluated against the attached Feature's labels at
// Step 4.
func ErrSlotSelectorUnsatisfied() error { return errSlotSelectorUnsatisfied }

var errSlotSelectorUnsatisfied = cerrors.Register("compiler-analyzer/slot-selector-unsatisfied", cerrors.Meta{
	Message: "attachment slot selector unsatisfied",
})

// ErrRequiredSlotUnfilled is the ABI-stable sentinel for an attachment
// slot a Component declares `required: true` that the node's final
// attachment set leaves unbound — the slot analog of a missing required
// uses socket, evaluated at Step 4 over the resolved set rather than over
// an authored block.
//
// The stage is what makes the rule honest: an attachment contributed at a
// subsystem, boundary or Release scope binds the slot for every component
// under that scope, so a slot is satisfied whether the deployment fills it
// per-consumer or attaches once at a scope the consumers share. Only the
// post-cascade set can see both shapes, and the check is a name-presence
// test over it — it never inspects what the fill means.
//
// It is deliberately distinct from ErrMissingRequiredFeature, which reports
// an unsatisfied presence mandate. A mandate demands that SOME attachment
// of a named type be present, at any name, with at-least-one cardinality; a
// slot's `required` demands that ONE NAMED binding point be bound. Two
// questions with two remediations, so two sentinels.
func ErrRequiredSlotUnfilled() error { return errRequiredSlotUnfilled }

var errRequiredSlotUnfilled = cerrors.Register("compiler-analyzer/required-slot-unfilled", cerrors.Meta{
	Message: "required attachment slot unfilled",
})

// ErrPolicyViolation is the ABI-stable sentinel for a Step-4
// policy socket verdict that fails: a plugin result whose
// pass flag is false. Each failed rule is recorded as an
// [AnalysisError] entry (Kind [AnalysisErrorPolicy]) on
// [AnalysisResult].Errors with this sentinel in its Sentinel field;
// [AnalysisError.ToCompilerError] wraps it as the CompilerError's
// inner error, so the joined build error is errors.Is-recoverable at
// the pipeline boundary. Policy never mutates — a failed rule halts
// the build (the Invariant-4 "reject, never silently mutate" Loud
// Guardrail); the message the plugin supplied is surfaced verbatim.
func ErrPolicyViolation() error { return errPolicyViolation }

var errPolicyViolation = cerrors.Register("compiler-analyzer/policy-violation", cerrors.Meta{
	Message: "policy violation",
})

// ErrMalformedAttachmentRequirement is the ABI-stable sentinel for a
// presence mandate whose declaration cannot be evaluated: a
// features.aggrega.ai/requiredType.<label> annotation with no label, or
// whose value is not a Feature type URI. It is deliberately distinct from
// the missing-required-feature sentinel, which reports a well-formed
// requirement that no attachment satisfies. The two have different
// remediations — correct the Component catalog entity, versus attach the
// feature — and an annotation value is an opaque string that no structural
// validation upstream has checked, so a broken declaration must announce
// itself as broken rather than as unsatisfied.
func ErrMalformedAttachmentRequirement() error { return errMalformedAttachmentRequirement }

var errMalformedAttachmentRequirement = cerrors.Register("compiler-analyzer/malformed-attachment-requirement", cerrors.Meta{
	Message: "malformed attachment requirement",
})

// ErrOverriddenAnnotation is the ABI-stable sentinel for a leaf of an
// attachment's configuration where an explicitly authored value took
// precedence over one an annotation supplied. It is advisory: the emitted
// output is not wrong, so the finding is reported and the build finishes.
//
// It names a suppression rather than a conflict, and the distinction is what
// makes the rule report every displacement rather than only the ones that
// changed something. The per-leaf merge overwrites the annotation-supplied
// value with the explicit one, so the suppressed value is not recoverable
// downstream and no comparison between the two is available to make; the
// provenance record is the whole of the evidence.
func ErrOverriddenAnnotation() error { return errOverriddenAnnotation }

var errOverriddenAnnotation = cerrors.Register("compiler-analyzer/overridden-annotation", cerrors.Meta{
	Message: "annotation contribution overridden by an explicit value",
})

// ErrCatalogLookup is the ABI-stable sentinel reserved for catalog
// lookups that fail during analyzer-phase evaluation. Today catalog
// misses inside validateSelectors and validateAppliesTo trigger a
// silent skip rather than a wrapped halt: Step 1's resolver guards
// already validate that every component and feature reference an
// Aggregate or Release names, so an analyzer-phase miss reflects an
// internal invariant violation rather than an authoring error. The
// sentinel ships preserved for any future emission site that needs
// to surface that invariant violation as a fatal halt.
func ErrCatalogLookup() error { return errCatalogLookup }

var errCatalogLookup = cerrors.Register("compiler-analyzer/catalog-lookup", cerrors.Meta{
	Message: "catalog lookup failed",
})
