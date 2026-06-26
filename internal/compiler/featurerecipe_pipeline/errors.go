// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package featurerecipe_pipeline

import "errors"

// ErrPipelineTargetAbsent fires when a patch addresses a document not
// present in the keyed set — a scope/key pair the set does not hold. It is
// wrapped with the offending scope, key, and step id at the emission site.
//
// This is the walk's local surface for the absent-target condition. The
// renderer integration that wires the walk into the render loop promotes
// this to its own build-halting Loud Guardrail; the walk reports the
// typed cause so callers can match it with errors.Is.
var ErrPipelineTargetAbsent = errors.New("pipeline patch targets a document not present in the set")

// ErrPipelineStepFailed is the walk's umbrella sentinel for a step that
// failed for any reason other than an absent patch target — a guest trap,
// a malformed step output, an error-severity diagnostic, a malformed
// document in the set, or a patch the apply engine rejected. The
// underlying cause is wrapped with %w so a caller can still match the
// specific engine error (for example jsonpatch.ErrTestFailed) through it.
var ErrPipelineStepFailed = errors.New("pipeline step failed")

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
var ErrPipelineTargetOpaque = errors.New("pipeline patch targets an opaque (non-structured) unit")
