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

var errPipelinePatchTargetNotStructured = cerrors.Register("compiler-rendercore/pipeline-patch-target-not-structured", cerrors.Meta{
	Message: "pipeline PATCH target is not parseable structured YAML/JSON",
})
