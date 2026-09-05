// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// buildPreviewParams is the request shape for `aggrega/buildPreview`. The
// release field is a workspace-relative path to the Release YAML, under the
// same contract every Release-naming parameter here carries.
type buildPreviewParams struct {
	Release string `json:"release"`
}

// buildPreviewNotice is one advisory the preview raised that is not attributable
// to a position in a document — a withdrawn pin, an overlapping schema pillar, a
// parent that would not resolve. Class names which advisory surface produced it,
// so a client renders each in its own voice instead of parsing the text.
//
// The class travels rather than being folded into Text here, because a client's
// presentation is the client's: text pre-formatted for one caller's terminal
// reads as noise in another's panel. It is empty for an advisory whose surface
// this daemon does not name, which a client renders as the bare text.
type buildPreviewNotice struct {
	Class string `json:"class"`
	Text  string `json:"text"`
}

// buildPreviewResult is the response shape for `aggrega/buildPreview`.
//
// Files are the output-relative names of the artifacts a build of this Release
// would emit, sorted, so a caller reporting them reports the same sequence on
// every run of the same input rather than a set reshuffled by map iteration.
// They are names rather than paths: this daemon writes nothing, so it has no
// output root to resolve them against and reporting one would name a location
// nothing put a file at.
//
// Notices carry the advisories that reach no document position. The findings
// that DO carry one travel on aggrega/publishDiagnostics instead, under the
// build layer's source, so a client puts them on its problems surface beside the
// findings the same layer raises while a graph is inspected.
type buildPreviewResult struct {
	Files   []string             `json:"files"`
	Notices []buildPreviewNotice `json:"notices,omitempty"`
}

// The advisory-surface names a notice carries on the wire. They are the
// descriptive register — what the surface is, not which code raised it — because
// a client groups by them and shows them to a person.
const (
	noticeClassDeprecation = "deprecation"
	noticeClassCatalog     = "catalog"
	noticeClassCatalogLint = "catalog-lint"
	noticeClassExtends     = "extends"
)

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

// handleBuildPreview renders the Release at params.Release through the one
// preview computation every rendering surface is a transport over, in the
// posture of a surface that emits an artifact, and reports what a build would
// write without writing any of it.
//
// # Why the artifact posture rather than the inspection one
//
// The two postures answer different questions. An inspection draws a picture and
// refuses nothing, because a reader left with no graph is worse off than one left
// with a graph and a diagnostic. This request is asked in order to learn what a
// build would do, so a gate that would refuse the build has to refuse here too:
// a preview that renders happily past a drifted lockfile answers a question
// nobody asked. Every gate is therefore on, and a divergence halts, which is why
// this handler can return an envelope where the graph inspection returns a graph
// over the same workspace.
//
// # What it does not do
//
// It writes nothing — no artifact, no output directory, no lockfile — and it
// reaches no network. External references are served from the workspace's
// content-addressed store where the lockfile pins them and declined where it does
// not, and each decline is reported rather than silently shortening the output.
// So the rendered bytes match a build's for a fully-pinned Release, and where they
// cannot the reader is told which reference is missing and what to run.
//
// # Where its findings go
//
// The advisories that carry a document position are published on the build
// diagnostic layer, which the graph inspection publishes on as well: one layer,
// replaced whole, so the newest render's findings are the ones on screen and a
// finding that stopped firing disappears. The advisories that carry no position
// ride back on the result instead. Sending those to the same layer would put a
// second copy of a withdrawn-pin notice beside the one a validation already
// publishes on the semantic layer, and two copies of one fact read as two facts.
func (s *Server) handleBuildPreview(ctx context.Context, params buildPreviewParams) (buildPreviewResult, *jsonRPCError) {
	releasePath, paramErr := resolveReleaseParam(s.workspaceRoot, params.Release)
	if paramErr != nil {
		return buildPreviewResult{}, paramErr
	}

	// The lockfile-absent gate, before anything is resolved. A build renders
	// against pinned dependencies, and this daemon fills no lockfile, so a
	// workspace without one is told to generate it rather than shown a render
	// resolved from whatever happened to be on disk.
	lockfilePath := lockfile.PathFor(releasePath)
	if _, err := os.Stat(lockfilePath); err != nil {
		if os.IsNotExist(err) {
			return buildPreviewResult{}, &jsonRPCError{
				Code:    errLockfileAbsent,
				Message: fmt.Sprintf("lockfile %s not found; run `aggrega pull --release %s` to generate", lockfilePath, params.Release),
				Data:    newLockfileErrorData(lockfilePath, params.Release),
			}
		}
		return buildPreviewResult{}, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: fmt.Sprintf("lockfile stat %s: %v", lockfilePath, err),
			Data:    newLockfileErrorData(lockfilePath, params.Release),
		}
	}

	// A lockfile that is present and unreadable is a refusal here, and this is the
	// one place the preview departs from the graph inspection's handling of the
	// same file. An inspection that cannot read the lockfile carries on with no
	// pins, because a graph resolved without them is still worth drawing. A
	// preview cannot: a build stops at the load failure, so a preview that
	// degraded to no pins would render a Release the build never reaches and
	// report artifacts nobody can obtain. The message is the loader's own, which
	// already names the file and the fault.
	lf, _, lfErr := lockfile.LoadLockfile(lockfilePath)
	if lfErr != nil {
		return buildPreviewResult{}, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: fmt.Sprintf("load lockfile: %v", lfErr),
			Data:    newLockfileErrorData(lockfilePath, params.Release),
			cause:   lfErr,
		}
	}

	// The request-scoped catalog view, seeded with THIS request's pins before any
	// cached read fires, so every resolution verifies-on-read against this
	// Release's lockfile and a concurrent request's pins can never perturb it.
	var pins map[string]catalog.Pin
	if lf != nil {
		pins = catalog.PinsFromLockfile(lf)
	}
	view := s.lazyCatalog.ForRequest(pins)

	fetchSeam := s.irGraphExternalInputs(lf)

	preview, err := compilerops.Preview(
		ctx,
		s.irGraphPreviewInputs(view, releasePath, lf, fetchSeam),
		// A build emits something a person applies, so every gate is on and every
		// divergence halts. That is the whole of what this request adds over the
		// graph inspection, which runs the same computation with the refusing
		// gates off.
		compilerops.ArtifactPolicy(),
		pipeline.Options{},
	)

	// The positioned advisories are published from whatever the run reached, and
	// before the failure is returned rather than after: a declined reference halts
	// the render, so the failure path is the one a reader most needs the advisory
	// on. One call carries both classes because the publication replaces the whole
	// layer — a second call would erase the first one's entries.
	if preview != nil && preview.Resolved != nil {
		var renderDiags []renderer.Diagnostic
		if preview.Pipeline != nil {
			renderDiags = preview.Pipeline.RenderDiagnostics
		}
		s.publishRenderDiagnostics(ctx, preview.Resolved.AggregatePath, renderDiags,
			externalInputDivergenceDiagnostics(fetchSeam.Declined(), releasePath))
	}

	if err != nil {
		return buildPreviewResult{}, s.previewFailureEnvelope(view, releasePath, preview, err)
	}

	return buildPreviewResult{
		Files:   renderedArtifactNames(preview.Pipeline),
		Notices: previewNotices(preview),
	}, nil
}

// renderedArtifactNames returns the output-relative names of the artifacts a
// result carries, sorted. Go randomises map iteration, so an unsorted answer
// would report the same render differently on every call.
func renderedArtifactNames(result *pipeline.Result) []string {
	if result == nil {
		return nil
	}
	names := make([]string, 0, len(result.Artifacts))
	for name := range result.Artifacts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// previewNotices converts a preview's advisories into the wire shape, in the
// order the preview raised them — which is the order its sequence defines, so a
// reader meets a finding where the run found it.
func previewNotices(preview *compilerops.PreviewResult) []buildPreviewNotice {
	if preview == nil || len(preview.Warnings) == 0 {
		return nil
	}
	out := make([]buildPreviewNotice, 0, len(preview.Warnings))
	for _, w := range preview.Warnings {
		out = append(out, buildPreviewNotice{Class: noticeClass(w.Class), Text: w.Text})
	}
	return out
}

// noticeClass names the advisory surface a warning came from.
//
// The empty string is the honest answer for a surface this daemon does not name,
// rather than a nearest match: a client shown the wrong surface has been told
// something false, where one shown none renders the text and is told nothing.
func noticeClass(class compilerops.WarningClass) string {
	switch class {
	case compilerops.WarningDeprecation:
		return noticeClassDeprecation
	case compilerops.WarningCatalog:
		return noticeClassCatalog
	case compilerops.WarningCatalogLint:
		return noticeClassCatalogLint
	case compilerops.WarningExtends:
		return noticeClassExtends
	default:
		return ""
	}
}
