// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
)

// buildPreviewParams is the request shape for `aggrega/buildPreview`. The
// release field is a workspace-relative path to the Release YAML, under the
// same contract every Release-naming parameter here carries.
type buildPreviewParams struct {
	Release string `json:"release"`

	// AutoPull is accepted here in order to be REFUSED, and the difference
	// between refusing it and ignoring it is the whole reason the field exists.
	// The field the build beside this one carries resolves dependencies and
	// writes the lockfile; this request writes nothing, so the two cannot both
	// be honoured. A request sending it is answered with what to send instead.
	//
	// Declining to decode it would leave the refusal to the caller, and a caller
	// is not where a contract is kept: a client that sent the field would be
	// told nothing and get a preview whose dependencies were never filled,
	// which reads exactly like a preview whose dependencies were.
	AutoPull bool `json:"autoPull,omitempty"`
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
	if err := decodeParams(req, &params); err != nil {
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
//
// # Why the dependency fill is refused rather than ignored
//
// The omission is what makes it incompatible: a fill writes the lockfile, and a
// request that reports without writing cannot perform one and still be what it
// says it is. The command line refuses the same pairing between the flag that
// previews and the flag that fills, so the refusal is the shared behaviour
// rather than a rule this surface invented.
//
// It is enforced here, on the answering side, because a caller is not a trust
// boundary: a client that sent the field and was quietly given an unfilled
// preview could not tell that from a filled one, and the contract would hold
// only for clients that already knew.
//
// A workspace whose auto_pull setting is on does not fill here either, and that
// is the same behaviour rather than a second rule: the setting asks every build
// to fill, and this request does not write, so it stands the setting down for
// the one call instead of refusing to run at all. The command line resolves the
// identical pairing the identical way — it refuses an explicit flag and
// suppresses the workspace setting — because a surface that writes nothing
// would otherwise have no way to report a render in a workspace configured to
// fill. So this method never consults the composition the writing build reads;
// there is no outcome it could return.
func (s *Server) handleBuildPreview(ctx context.Context, params buildPreviewParams) (buildPreviewResult, *jsonRPCError) {
	if params.AutoPull {
		return buildPreviewResult{}, &jsonRPCError{
			Code: jsonRPCInvalidParams,
			Message: "invalid params: autoPull is not accepted on aggrega/buildPreview, which reports what a " +
				"build would write and writes none of it, while resolving dependencies writes the lockfile. " +
				"Send aggrega/resolveDependencies for this Release, then aggrega/buildPreview",
		}
	}

	// The fill is refused above, so nothing on this path can have filled a
	// lockfile: one the render finds was on disk before the request.
	preview, errEnvelope := s.renderUnderArtifactPosture(ctx, params.Release, false)
	if errEnvelope != nil {
		return buildPreviewResult{}, errEnvelope
	}
	// The envelope above answered every refusal, so the finished payload is what
	// names the artifact set this method reports and then drops.
	return buildPreviewResult{
		Files:   renderedArtifactNames(preview.PipelineOutcome().Completed()),
		Notices: buildNotices(preview),
	}, nil
}
