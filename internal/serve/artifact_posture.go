// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// buildNotice is one advisory a build raised that is not attributable to a
// position in a document — a withdrawn pin, an overlapping schema pillar, a
// parent that would not resolve. Class names which advisory surface produced it,
// so a client renders each in its own voice instead of parsing the text.
//
// The class travels rather than being folded into Text here, because a client's
// presentation is the client's: text pre-formatted for one caller's terminal
// reads as noise in another's panel. It is empty for an advisory whose surface
// this daemon does not name, which a client renders as the bare text.
//
// One shape serves both build methods. A build and a report of what that build
// would write raise the same advisories from the same run of the same
// computation, so two shapes would be two spellings of one fact and would drift
// the moment either method gained a class.
type buildNotice struct {
	Class string `json:"class"`
	Text  string `json:"text"`
}

// The advisory-surface names a notice carries on the wire. They are the
// descriptive register — what the surface is, not which code raised it — because
// a client groups by them and shows them to a person.
const (
	noticeClassDeprecation = "deprecation"
	noticeClassCatalog     = "catalog"
	noticeClassCatalogLint = "catalog-lint"
	noticeClassExtends     = "extends"

	// noticeClassReleaseComposition covers the readings over what folding the
	// Release's own extends chain produced. It is its own surface rather than a
	// second use of the extends one, which names a closure-scoped resolution
	// failure accompanying a halt: these accompany a build that succeeded.
	noticeClassReleaseComposition = "release-composition"

	// noticeClassDependencyResolution covers the lines a build's own dependency
	// fill raised. It names a surface this daemon does have a name for, which is
	// why the lines travel under it rather than under the empty class the
	// conversion below falls back to.
	noticeClassDependencyResolution = "dependency-resolution"
)

// artifactPostureRender is the render a build here is answered from, in the
// posture of a surface that emits an artifact: the render the dependency fill
// already performed, or one performed here when the fill performed none. Either
// way it comes from the one preview computation every rendering surface is a
// transport over, and the positioned findings it raised are published before it
// is returned.
//
// It is the whole of what the daemon's two build methods share: the Release
// parameter's contract, the lockfile gates, the frozen external-input seam, the
// gate sequence and the publication. What each method adds is only what it does
// with the render — one writes it, the other reports what writing it would have
// produced. Keeping the shared half here is what stops the two coming to
// disagree about which gates run or in what order, which is the disagreement a
// reader choosing between them would have no way to see.
//
// # One build renders the Release once
//
// Discovering which external inputs a Release consumes is itself a render, so a
// build whose fill ran already holds a render of the Release it was about to
// render — performed under this same posture, through this same computation,
// against the closure that fill pinned. filled is that render, and this presents
// it rather than computing a second one.
//
// The PRESENCE of that render is the decision. It is a fact about what this run
// did, not a setting: there is deliberately nothing here asking whether to reuse
// it, because a switch of that shape would be a second answer to a question the
// render itself already answers by existing, and two readings of one dial agree
// until they do not.
//
// filled and lockfileFilled are independent facts and neither derives from the
// other. A fill that ran and whose discovery render stopped part way wrote its
// lockfile and handed back no render: lockfileFilled is true and filled is nil,
// and the render performed here is held to what that fill did write. Collapsing
// the two would stand the drift verify down on a run that never filled, or run
// it against a lockfile written moments ago by this one.
//
// # Why the artifact posture rather than the inspection one
//
// The two postures answer different questions. An inspection draws a picture and
// refuses nothing, because a reader left with no graph is worse off than one left
// with a graph and a diagnostic. Both callers here are asked what a build does,
// so a gate that would refuse the build has to refuse here too. Every gate is
// therefore on, and a divergence halts, which is why this returns an envelope
// where the graph inspection returns a graph over the same workspace.
//
// # What it does not do
//
// No render it performs reaches a network. External references are served from
// the workspace's content-addressed store where the lockfile pins them and
// declined where it does not, and each decline is reported rather than silently
// shortening the output. So the rendered bytes match a build's for a
// fully-pinned Release, and where they cannot the reader is told which reference
// is missing and what to run.
//
// It fills no lockfile either, and lockfileFilled is how it is told that its
// caller already did. The parameter reports a fact rather than requesting an
// action: a caller that resolved the dependencies before calling holds a
// lockfile the fill already held to this drift verify, over the closure a build
// reconstructs, before writing it — the fill refuses to write one the verify
// disagrees with — so the verify below is stood down for it. Passing true
// without having filled would
// suppress a real drift verdict, which is why the only caller that passes it is
// the one that performs the fill.
//
// A presented render acquired what it needed while the fill was running — the
// fill is the acquisition, and it is where an external reference this Release
// consumes is fetched and pinned. So nothing was declined on that path and the
// divergence advisory has nothing to raise, which is the same reason the
// declined set is read off the seam this method attaches rather than carried
// on the render: a render this method did not perform refused nothing here.
//
// # Where its findings go
//
// The advisories that carry a document position are published on the build
// diagnostic layer, which the graph inspection publishes on as well: one layer,
// replaced whole, so the newest render's findings are the ones on screen and a
// finding that stopped firing disappears. The advisories that carry no position
// are returned on the result instead. Sending those to the same layer would put a
// second copy of a withdrawn-pin notice beside the one a validation already
// publishes on the semantic layer, and two copies of one fact read as two facts.
func (s *Server) artifactPostureRender(ctx context.Context, release string, filled *compilerops.PreviewResult, lockfileFilled bool) (*compilerops.PreviewResult, *jsonRPCError) {
	releasePath, paramErr := resolveReleaseParam(s.workspaceRoot, release)
	if paramErr != nil {
		return nil, paramErr
	}

	// The lockfile-absent gate, before anything is resolved. A build renders
	// against pinned dependencies, and this computation fills no lockfile, so a
	// workspace without one is told to generate it rather than shown a render
	// resolved from whatever happened to be on disk. A caller that filled one
	// first passes the gate on what the fill wrote. It is the gate the graph
	// inspection crosses as well, so a refusal for absence withdraws the drift
	// finding on that path here exactly as it does there.
	lockfilePath := lockfile.PathFor(releasePath)
	if envelope := s.requireLockfile(ctx, lockfilePath, release); envelope != nil {
		return nil, envelope
	}

	// A lockfile that is present and unreadable is a refusal here, and this is the
	// one place the artifact posture departs from the graph inspection's handling
	// of the same file. An inspection that cannot read the lockfile carries on with
	// no pins, because a graph resolved without them is still worth drawing. A
	// build cannot: it stops at the load failure, so a surface that degraded to no
	// pins would render a Release the build never reaches and report artifacts
	// nobody can obtain. The message is the loader's own, which already names the
	// file and the fault.
	lf, _, lfErr := lockfile.LoadLockfile(lockfilePath)
	if lfErr != nil {
		return nil, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: fmt.Sprintf("load lockfile: %v", lfErr),
			Data:    newLockfileErrorData(lockfilePath, release),
			cause:   lfErr,
		}
	}

	// The render, or the render that already happened. The presence of a conserved
	// render is the whole of the decision — see this method's own notes for why
	// that is a fact rather than a dial.
	//
	// declined is the references this method's own render was refused. It stays
	// empty on the presenting path for the same reason: a render this method did
	// not perform refused nothing here, and the fill that did perform it acquired
	// rather than declined. view is likewise only assembled where a render needs
	// it, and is read afterwards only on the failure path, which the presenting
	// path cannot reach because no render was performed to fail.
	preview := filled
	var view *catalog.LazyLookup
	var declined []string
	var err error
	if preview == nil {
		// The request-scoped catalog view over this Release's own catalog stack,
		// seeded with THIS request's pins before any cached read fires, so every
		// resolution verifies-on-read against this Release's lockfile and a
		// concurrent request's pins can never perturb it.
		var pins map[string]catalog.Pin
		if lf != nil {
			pins = catalog.PinsFromLockfile(lf)
		}
		view = s.catalogFor(releasePath).ForRequest(pins)

		fetchSeam := s.irGraphExternalInputs(lf)

		// A build emits something a person applies, so every gate is on and every
		// divergence halts. That is the whole of what these methods add over the graph
		// inspection, which runs the same computation with the refusing gates off. The
		// gate above has already established that the Release carries a lockfile, so
		// the drift verify has something to hold it to.
		preview, err = compilerops.PreviewForArtifact(
			ctx,
			s.irGraphPreviewInputs(view, releasePath, lf, fetchSeam, lf != nil && !lockfileFilled),
			pipeline.Options{},
		)
		declined = fetchSeam.Declined()
	}

	// The positioned advisories are published from whatever the run reached, and
	// before the failure is returned rather than after. A gate that halted still
	// found everything ahead of it, and a reader who loses those findings has to
	// re-run to see them — so the failure path is the one they are most needed on.
	// One call carries both classes because the publication replaces the whole
	// layer: a second call would erase the first one's entries.
	if preview != nil && preview.Resolved != nil {
		// Read through the accessor that answers on either verdict, so a run that
		// halted publishes the findings the phases ahead of it raised rather than an
		// empty layer. That is what makes the sentence above true of the failure path
		// and not only of the success one.
		advisories := preview.PipelineOutcome().Advisories()
		s.publishRenderDiagnostics(ctx, preview.Resolved.AggregatePath, advisories,
			externalInputDivergenceDiagnostics(declined, releasePath),
			preview.Resolved.AggBytes)
	}

	if err != nil {
		return preview, s.previewFailureEnvelope(view, releasePath, preview, err)
	}
	return preview, nil
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

// buildNotices converts a run's advisories into the wire shape, in the order the
// run raised them — which is the order its sequence defines, so a reader meets a
// finding where the run found it.
func buildNotices(preview *compilerops.PreviewResult) []buildNotice {
	if preview == nil || len(preview.Warnings) == 0 {
		return nil
	}
	out := make([]buildNotice, 0, len(preview.Warnings))
	for _, w := range preview.Warnings {
		out = append(out, buildNotice{Class: noticeClass(w.Class), Text: w.Text})
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
	case compilerops.WarningReleaseComposition:
		return noticeClassReleaseComposition
	default:
		return ""
	}
}
