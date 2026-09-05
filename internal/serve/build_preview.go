// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"encoding/json"
)

// buildPreviewParams is the request shape for `aggrega/buildPreview`. The
// release field is a workspace-relative path to the Release YAML, under the
// same contract every Release-naming parameter here carries.
type buildPreviewParams struct {
	Release string `json:"release"`
}

// buildPreviewResult is the response shape for `aggrega/buildPreview`.
//
// Files are the output-relative names of the artifacts a build of this Release
// would emit, sorted, so a caller reporting them reports the same sequence on
// every run of the same input rather than a set reshuffled by map iteration.
// They are names rather than paths: this request writes nothing, so it has no
// output root to resolve them against and reporting one would name a location
// nothing put a file at.
//
// Notices carry the advisories that reach no document position. The findings
// that DO carry one travel on aggrega/publishDiagnostics instead, under the
// build layer's source, so a client puts them on its problems surface beside the
// findings the same layer raises while a graph is inspected.
type buildPreviewResult struct {
	Files   []string      `json:"files"`
	Notices []buildNotice `json:"notices,omitempty"`
}

// dispatchBuildPreview unmarshals params and answers the request.
func (s *Server) dispatchBuildPreview(ctx context.Context, req *jsonRPCRequest) error {
	var params buildPreviewParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	result, errEnvelope := s.handleBuildPreview(ctx, params)
	if ctx.Err() != nil {
		// Cancelled mid-pipeline (shutdown). Drop the response; the dispatcher
		// exits via the drain timer.
		return nil
	}
	if errEnvelope != nil {
		return s.writeErrorEnvelope(ctx, req.ID, errEnvelope)
	}
	return s.writeResult(ctx, req.ID, result)
}

// handleBuildPreview reports what a build of params.Release would write, without
// writing any of it.
//
// The render is the shared artifact-posture one — see
// [Server.renderUnderArtifactPosture] for the gates it runs, the seam it renders
// through and where its positioned findings go. What this method adds is the
// deliberate omission: the artifact set it was handed is reported by name and
// then dropped. That is the whole difference between this request and the build
// beside it, which is what makes a reader's choice between them meaningful.
func (s *Server) handleBuildPreview(ctx context.Context, params buildPreviewParams) (buildPreviewResult, *jsonRPCError) {
	preview, errEnvelope := s.renderUnderArtifactPosture(ctx, params.Release)
	if errEnvelope != nil {
		return buildPreviewResult{}, errEnvelope
	}
	return buildPreviewResult{
		Files:   renderedArtifactNames(preview.Pipeline),
		Notices: buildNotices(preview),
	}, nil
}
