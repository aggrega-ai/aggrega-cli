// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"

// ErrPipelinePatchTargetNotStructured marks an emitted file that a pipeline
// PATCH step addresses but which is not parseable structured YAML/JSON, so it
// has no addressable document tree. The segmenter surfaces this typed error to
// the pipeline caller, which halts with a Loud Guardrail rather than silently
// skipping the operation. An emitted file that is only ever an EMIT side
// effect — never a PATCH target — is not segmented, so a non-structured file
// is an error only when a step tries to address a document inside it.
func ErrPipelinePatchTargetNotStructured() error { return errPipelinePatchTargetNotStructured }

var errPipelinePatchTargetNotStructured = cerrors.Register("pipeline-patch-target-not-structured", cerrors.Meta{
	ShortCode: "E0022",
	Message:   "pipeline PATCH target is not parseable structured YAML/JSON",
})

// ErrPipelinePatchUnitNotCarved fires when reassembly is handed a patched unit
// whose token the carve of the same file never produced, so there is no byte
// range for the patched content to be spliced back into.
//
// Its reader is a plugin author rather than an entity author: no authored
// document can reach it, because the carve and the reassembly of one file are
// two halves of one codec's contract and an author supplies neither. A codec
// that advertises the carve and reassemble exports owns both halves, and a
// codec that composes the stock YAML codec for its structural work delegates
// the second half to it while keeping the first — which is the shape in which
// the two halves can disagree about a file's tokens at all. The refusal is the
// conformance requirement every codec is held to, so a codec that admitted such
// a token instead of refusing it would fail the same contract from the other
// side.
//
// It halts rather than dropping the unit: a silently discarded patch leaves
// output that is well-formed and missing an edit a step was asked to make,
// which is the failure a reader is least able to attribute.
// Category: CATALOG_ERROR.
func ErrPipelinePatchUnitNotCarved() error { return errPipelinePatchUnitNotCarved }

var errPipelinePatchUnitNotCarved = cerrors.Register("pipeline-patch-unit-not-carved", cerrors.Meta{
	ShortCode: "E0268",
	Message: "reassemble names a patched unit token the carve did not produce; " +
		"a conforming codec carves and reassembles a file under the same tokens",
})
