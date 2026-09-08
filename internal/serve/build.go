// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/internal/artifactout"
)

// buildParams is the request shape for `aggrega/build`. The release field is a
// workspace-relative path to the Release YAML, under the same contract every
// Release-naming parameter here carries.
//
// There is no output-directory parameter, and its absence is the contract rather
// than an omission. The daemon writes into the workspace it opened at startup
// and nowhere else, so a caller cannot name a destination; a request carrying
// one would make the daemon a general-purpose writer pointed by whoever is on
// the other end of the pipe.
type buildParams struct {
	Release string `json:"release"`
}

// buildResult is the response shape for `aggrega/build`.
//
// OutputDirectory is the absolute directory the artifacts were written to, so a
// caller reporting the build can say where to look rather than leaving a reader
// to reconstruct it. Files are the output-relative names of the artifacts that
// were written, sorted, so a caller reports the same sequence on every run of
// the same input rather than a set reshuffled by map iteration. Each name is
// relative to OutputDirectory, which is what makes the two fields together an
// answer a reader can act on.
//
// Notices carry the advisories that reach no document position. The findings
// that DO carry one travel on aggrega/publishDiagnostics instead, under the
// build layer's source, so a client puts them on its problems surface beside the
// findings the same layer raises while a graph is inspected.
type buildResult struct {
	OutputDirectory string        `json:"outputDirectory"`
	Files           []string      `json:"files"`
	Notices         []buildNotice `json:"notices,omitempty"`
}

// dispatchBuild unmarshals params and answers the request.
func (s *Server) dispatchBuild(ctx context.Context, req *jsonRPCRequest) error {
	var params buildParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	result, errEnvelope := s.handleBuild(ctx, params)
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

// handleBuild builds params.Release and writes the artifacts it rendered.
//
// The render is the shared artifact-posture one — see
// [Server.renderUnderArtifactPosture] for the gates it runs, the seam it renders
// through and where its positioned findings go. What this method adds is the
// write.
//
// # Why this method writes when the rest of the daemon does not
//
// The daemon's no-write posture is about inspection. Every other request here
// answers a question about a workspace, and answering a question by changing the
// workspace would surprise a caller who only asked. A build is not a question:
// a caller sending this has asked for the artifacts, and answering with a report
// of the artifacts it declined to write would be a different capability wearing
// the same name. So this method writes, and it is the only one that does — the
// request that reports without writing is aggrega/buildPreview, which exists so
// the choice between the two is the caller's to make explicitly.
//
// # Where it writes, and why the caller does not choose
//
// Into the default output directory under the workspace root the daemon
// resolved at startup. That is the same location the command line writes to when
// it is run in that workspace, which is what makes the two surfaces produce one
// tree rather than two; the directory name has one home so neither surface can
// move without the other.
//
// The destination is therefore derived rather than requested, and the write goes
// through the shared build-output entry point, which clears the boundaries
// subtree a previous build left and refuses a rendered name that resolves
// outside the output root. Both belong to what a build's write is, so both
// happen here exactly as they happen on the command line.
//
// # The lockfile is not filled, and an unpinned reference is declined
//
// The shared render fills no lockfile and reaches no network, so this performs
// what the command line performs with its external-input set frozen: a pinned
// reference is served from the content-addressed store, and an unpinned one is
// declined. It is a real difference from an unfrozen command-line build, which
// may acquire such a reference and pin it.
//
// What a decline does to the render is the guest's decision — the host answers
// the fetch with an error envelope rather than aborting — so a render that
// carries on produces bytes an unfrozen build would not have produced, and those
// bytes are what gets written. That is why the decline is published as a
// positioned finding naming the reference and the command that pins it: the
// divergence is reported to the reader rather than left to be inferred from the
// output.
func (s *Server) handleBuild(ctx context.Context, params buildParams) (buildResult, *jsonRPCError) {
	preview, errEnvelope := s.renderUnderArtifactPosture(ctx, params.Release)
	if errEnvelope != nil {
		return buildResult{}, errEnvelope
	}
	// The envelope above has already answered every refusal, so what is left is a
	// run that reported no failure. Its finished payload is therefore what a write
	// draws on, and the guard below is the one case that leaves: no failure and no
	// render. The accessor is nil-receiver safe, so an absent preview arrives here
	// as an absent payload and needs no separate clause.
	completed := preview.PipelineOutcome().Completed()
	if completed == nil {
		// A run that reported no failure and produced no render. Nothing here can
		// be written, and clearing the output directory to write an empty set
		// would destroy the previous build's output in order to report that this
		// one emitted nothing. The refusal is loud because the reporting method
		// beside this one answers with an empty file list in the same situation,
		// and a build that quietly emptied a directory would be the worse of the
		// two silences.
		return buildResult{}, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: "the build produced no render, so there was nothing to write",
		}
	}

	outRoot := s.buildOutputRoot()
	if err := artifactout.WriteBuildOutput(outRoot, completed.Artifacts); err != nil {
		// A write failure is reported as the workspace-not-resolvable class the
		// daemon already carries for a workspace it cannot read as expected, and
		// the wrapped error is kept as the cause so a client matching on the
		// condition sees what the write layer named. The rendered set is dropped:
		// a partial write is on disk, and reporting the names as written would
		// claim more than happened.
		return buildResult{}, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: fmt.Sprintf("write build output to %s: %v", outRoot, err),
			cause:   err,
		}
	}

	return buildResult{
		OutputDirectory: outRoot,
		Files:           renderedArtifactNames(completed),
		Notices:         buildNotices(preview),
	}, nil
}

// buildOutputRoot is the directory a build performed by this daemon writes to:
// the default output directory, resolved against the workspace root this session
// anchored to.
//
// It is a method rather than a literal at the call site so that what the daemon
// can write to is readable from a test, and so that the anchoring is stated once.
// A daemon holding no workspace root resolves to the relative default, which is
// what the command line does with the same value — there is no session anchor to
// improve on it, and inventing an absolute path from the process's working
// directory would write somewhere neither surface named.
func (s *Server) buildOutputRoot() string {
	if s.workspaceRoot == "" {
		return artifactout.DefaultOutputDir
	}
	return filepath.Join(s.workspaceRoot, artifactout.DefaultOutputDir)
}
