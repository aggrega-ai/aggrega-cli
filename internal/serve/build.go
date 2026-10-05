// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/internal/artifactout"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/pullops"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// buildParams is the request shape for `aggrega/build`. The release field is a
// workspace-relative path to the Release YAML, under the same contract every
// Release-naming parameter here carries.
//
// Where a build writes is anchored, and the two surfaces anchor it differently
// because what they have to anchor against differs. A build at a terminal
// resolves the output directory against the directory it was run in, which is a
// location the person running it stated by standing there. A request carries no
// such location, so this wire resolves it against the workspace root instead —
// and OutputParent is how a caller states the location a terminal states by
// standing somewhere.
type buildParams struct {
	Release string `json:"release"`

	// OutputParent is the directory the output directory is created inside,
	// relative to the workspace root: the artifacts land in the output directory
	// beneath it, exactly as they land beneath the invocation directory at a
	// terminal. So a caller sending the folder its reader is looking at gets the
	// artifacts a build run in that folder would have produced, in the place that
	// build would have put them.
	//
	// 🔴 It names the output directory's PARENT and never the output directory
	// itself, which is what separates it from the destination the other writing
	// methods on this wire take. Those name the directory written INTO, so a
	// caller sending one folder name to each gets its files directly inside that
	// folder there and one level down here. The two are different things, so they
	// carry different names rather than one name a reader would have to try to
	// tell apart.
	//
	// Absent leaves the anchor at the workspace root, which is where every
	// request that does not send it already writes. Empty is the same state and
	// not a separate one: it is what a caller computing the root's own path
	// relative to itself produces, and the root is exactly what that names.
	//
	// 🔴 It is a destination, so it is confined to the workspace this session
	// opened by the one rule every destination on this surface is judged by — an
	// absolute value and one climbing out of the root are both refused. What the
	// field makes the caller's is where INSIDE the workspace a build writes; it
	// does not make the daemon a writer pointed anywhere by whoever is on the
	// other end of the pipe.
	OutputParent string `json:"outputParent,omitempty"`

	// AutoPull resolves the Release's dependencies before the render, as this
	// build's first step: the same resolution aggrega/resolveDependencies
	// performs, so a workspace filled by a build and one filled by that request
	// are filled identically. It reaches a registry and writes the lockfile.
	//
	// It ranks exactly as the command line's --auto-pull flag ranks, because the
	// two are read through one composition: the field and the workspace's own
	// auto_pull setting are ADDITIVE, and either one turns the fill on. So absent
	// — the zero value — the build renders against the lockfile as it stands and
	// refuses a Release that has none, UNLESS the workspace is configured to
	// fill, in which case it fills as it would for any other build there. What
	// the field decides is whether this one build fills a Release whose
	// dependencies are not yet resolved; what it cannot do is turn off a fill the
	// workspace asked for, which is the same thing the flag cannot do.
	//
	// Resolving is where an external input is acquired. The resolution renders
	// this Release to discover which remote references its render consumes,
	// fetches them, and records them in the same lockfile write that carries the
	// dependency pins — so a reference this Release needs is pinned after a fill
	// and, without one, is declined by the render and named in the answer. That is
	// the whole difference the field makes to what the render may reach: the
	// build's own render acquires nothing under either setting, and a fill is what
	// makes what it needs already pinned.
	//
	// A build that fills renders this Release exactly once. The fill's discovery
	// render is a render of this Release under the posture a build renders in, so
	// the build is answered from it rather than performing a second one.
	AutoPull bool `json:"autoPull,omitempty"`
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
//
// DependencyResolution is what the build's own fill step resolved, in the shape
// aggrega/resolveDependencies answers with, and it is absent on a build that did
// not fill. A caller that asked for the fill asked for a resolution as part of
// this request, so the facts that resolution computed are owed here rather than
// only on the request that does nothing else — which the caller would have to
// send a second time to find out what its own build just pinned. On this answer
// those facts are carried here and nowhere else: the lockfile written, and each
// reference reached with the version it settled on and where this run took it
// from, with the counts the report keeps beside them.
//
// The notices the fill adds beside it are not a second account of the same
// facts. They are the lines the resolution wrote about its own work as it went,
// such as a pin reused, a signature checked, a version skipped as withdrawn or
// retracted, or an advisory, and no line of them lists the references pinned,
// their versions or where each came from. A fill with nothing to remark on adds
// no notice at all, so a caller reporting what the fill did reads it off the
// report and never has to parse a line of prose to recover a fact.
type buildResult struct {
	OutputDirectory      string                      `json:"outputDirectory"`
	Files                []string                    `json:"files"`
	Notices              []buildNotice               `json:"notices,omitempty"`
	DependencyResolution *dependencyResolutionReport `json:"dependencyResolution,omitempty"`
}

// dispatchBuild unmarshals params and answers the request.
func (s *Server) dispatchBuild(ctx context.Context, req *jsonRPCRequest) error {
	var params buildParams
	if err := decodeParams(req, &params); err != nil {
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
// [Server.artifactPostureRender] for the gates it runs, the seam it renders
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
// # Where it writes, and what anchors it
//
// Into the default output directory, created inside the workspace root the
// request named — or, where the request sent outputParent, inside that location
// inside the workspace instead.
//
// 🔴 The anchor is not shared with the command line. What the two surfaces
// share is the directory NAME, which has one home so neither can move without
// the other; what they do not share is what that name resolves against. A
// terminal resolves it against the directory the command was run in, which is a
// location the person running it stated by standing there. A request states no
// such location, so this wire's anchor is the workspace root, and outputParent
// is how a caller states what standing somewhere states at a terminal. Absent,
// the anchor stays at the root, which is what every request that sends nothing
// already gets.
//
// A destination the request names is confined to the workspace this session
// opened, through the one rule every destination on this surface is judged by.
// So a request may say where inside the workspace a build writes, and may not
// point the write out of it.
//
// The write then goes through the shared build-output entry point, which clears
// the boundaries subtree a previous build left and refuses a rendered name that
// resolves outside the output root. Both belong to what a build's write is, so
// both happen here exactly as they happen on the command line.
//
// # Whether the lockfile is filled is the request's to say, or the workspace's
//
// Asked for, the resolution runs here, before the render — so the gates the
// render opens with read the lockfile this build just wrote rather than the
// absence they would otherwise refuse. A lockfile filled this run is not
// drift-verified again, because the fill held it to the same verify, over the
// closure a build reconstructs, before writing it, and refuses to write one the
// verify disagrees with. Left
// off, the render is held to the lockfile as it stands and a Release without one
// is refused.
//
// Left off is not the request's alone to decide, though, and that is the point
// of composing the two rather than branching on the field: a workspace whose
// auto_pull setting is on fills for every build it carries, and a build driven
// through this request is one of those. The request field adds to that setting
// and cannot subtract from it, which is exactly how the command line's flag
// behaves, so the same workspace answers the same way whichever surface a build
// arrives on.
//
// # The build's own render acquires nothing; the fill is what acquires
//
// No render a build performs reaches a network: a reference the lockfile pins is
// served from the content-addressed store and one it does not pin is declined.
// The fill is the exception and it is the whole of it — the resolution renders
// this Release with acquisition on precisely to discover which remote references
// the render consumes, fetches them, and pins them in the lockfile it writes. So
// a fill changes which references are pinned by the time a build renders, and
// changes nothing about what a build's own render may reach.
//
// Which is why a build asked to fill is answered from the fill's render rather
// than performing one: that render is a render of this Release, under this same
// posture, against the closure the fill pinned, so one build renders once.
//
// A declined reference is published as a positioned finding naming the reference
// and what pins it, and that publication happens whatever the render then makes
// of the decline. What it makes of it is the requesting guest's decision, because
// the host answers the fetch with an error envelope rather than aborting: the
// providers this binary carries hand that refusal back, so the render fails and
// the write below is never reached. A guest that carried on instead would
// produce bytes a fully-pinned render would not have, which is why the finding is
// published rather than left to be inferred from an output directory.
//
// # A catalog directory nothing can walk is refused before anything is written
//
// `aggrega build` loads the Release's whole catalog stack before it fills a
// lockfile or renders, so a configured directory that does not exist stops it
// with the load's own refusal and nothing written. The lookup this daemon
// resolves through finds what it can, and would build as though the directory
// were empty. So the stack is asked first (catalogDirsRefusal), after the
// destination and before the fill — the fill writes the lockfile, which is why
// the question cannot wait for the shared render — and a build refused there has
// written no lockfile and no artifact.
func (s *Server) handleBuild(ctx context.Context, params buildParams) (buildResult, *jsonRPCError) {
	// The destination is resolved before anything else runs, so a request naming
	// one this session may not write to is refused having rendered nothing and,
	// where it asked for a fill, having written no lockfile for a build that was
	// never going to land.
	outRoot, destErr := s.buildOutputRoot(params.OutputParent)
	if destErr != nil {
		return buildResult{}, destErr
	}

	if errEnvelope := s.catalogDirsRefusal(params.Release); errEnvelope != nil {
		return buildResult{}, errEnvelope
	}

	// The fill, before the render, because what it writes is what the render's
	// opening gates read. What it resolved is kept for the answer's report and the
	// lines it wrote about its own work for the answer's notices: a caller that
	// asked for the fill is owed both, and nothing here is watching a stream the
	// lines could have been written to instead.
	var fillNotices []string
	var fillReport *dependencyResolutionReport
	// The render the fill conserved, where it conserved one. Nil is not an error
	// signal and not a request: it is the absence of a render, and the step below
	// performs one for itself when it arrives that way.
	var filled *compilerops.PreviewResult
	lockfileFilled := false
	// Whether the fill runs is composed in the workspace config, which owns one of
	// the two inputs, rather than read off the request field alone. The field turns
	// the fill on for this one build and the workspace's auto_pull setting turns it
	// on for every build in the workspace; either is enough. Reading it from the one
	// home is what makes a build driven here and a build driven at a terminal rank
	// the two identically, so a workspace configured to fill is not a workspace
	// where only one of its surfaces does.
	//
	// A daemon holding no resolved workspace has no setting to read, so the request
	// field is the whole of the decision; that case is refused by the fill itself,
	// which names what is missing.
	fill := params.AutoPull
	if s.resolvedWorkspace != nil {
		fill = s.resolvedWorkspace.Config().FillsLockfile(params.AutoPull)
	}
	if fill {
		notices, report, render, errEnvelope := s.fillDependencies(ctx, params.Release)
		if errEnvelope != nil {
			return buildResult{}, errEnvelope
		}
		fillNotices = notices
		fillReport = report
		filled = render
		// Recorded as a fact about what just happened rather than read back off
		// the request, because what the render needs to know is that a lockfile
		// WAS written and not that one was asked for. The two part on the
		// refusal above, and a render told the wrong one would stand down a
		// drift verify that had something to check.
		//
		// It is recorded separately from the render above and not derived from it,
		// because a fill whose discovery render stopped part way wrote its lockfile
		// and conserved no render: this is true there and the render is nil.
		lockfileFilled = true
	}

	preview, errEnvelope := s.artifactPostureRender(ctx, params.Release, filled, lockfileFilled)
	if errEnvelope != nil {
		return buildResult{}, errEnvelope
	}
	// The envelope above has already answered every refusal, so what is left is a
	// run that reported no failure. Its finished payload is therefore what a write
	// draws on, and the guard below is the one case that leaves: no failure and no
	// finished render. The accessor is nil-receiver safe, so an absent preview
	// arrives here as an absent payload and needs no separate clause.
	completed := preview.PipelineOutcome().Completed()
	if completed == nil {
		// A run that reported no failure and finished no render. Nothing here can
		// be written, and clearing the output directory to write an empty set
		// would destroy the previous build's output in order to report that this
		// one emitted nothing. The refusal is loud because the reporting method
		// beside this one answers with an empty file list in the same situation,
		// and a build that quietly emptied a directory would be the worse of the
		// two silences.
		//
		// It is also where a presented render is refused, and that half is the one
		// worth stating: a render performed here has already returned on the
		// failure above, so a payload missing at this point means the fill handed
		// back a render that did not finish. Writing that out would emit an
		// artifact set stopping short of the nodes the render never reached — a
		// tree indistinguishable from a whole one. The fill declines to hand one
		// over, so this is the guard behind that promise rather than the only
		// thing keeping it.
		return buildResult{}, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: "the build finished no render, so there was nothing to write",
		}
	}

	// The output directory and every file written beneath it are held to the root
	// the directory it is composed in picks — the one answer every composed write
	// asks, as the command line's build asks it — rather than to a root this method
	// decides. The directory a request names as outputParent has already been held
	// inside the session's workspace, so the root picked is that workspace's.
	writeRoot := writescope.RootFor(s.workspaceRoot, filepath.Dir(outRoot))
	if err := artifactout.WriteBuildOutput(writeRoot, outRoot, completed.Artifacts); err != nil {
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
		OutputDirectory:      outRoot,
		Files:                renderedArtifactNames(completed),
		Notices:              append(dependencyResolutionNotices(fillNotices), buildNotices(preview)...),
		DependencyResolution: fillReport,
	}, nil
}

// fillDependencies resolves the Release's dependencies and returns what the
// resolution reported, together with its own account of itself line by line.
//
// It is the operation aggrega/resolveDependencies answers with rather than a
// second resolution written for a build, which is what makes a workspace filled
// by a build and one filled by that request the same workspace. A build that
// filled its own lockfile some other way would be free to resolve differently
// from every other surface, and the two would agree until they did not. The
// report is projected by that operation's own projection for the same reason:
// the fill a build performs is reported in the shape a caller already reads.
//
// Both are returned rather than written anywhere because this request reports no
// progress: nothing on this path opens a progress run, so a stream the caller is
// watching does not exist and the answer is where the account can still reach
// whoever asked for it. The lines and the report are not alternatives, and
// neither restates the other: the lines are the resolution's remarks on its own
// work, for a person, and the report is what it resolved, and a caller that asked
// for a fill is owed both.
//
// ctx is the requesting build's own, threaded into the resolution rather than
// replaced by one this method invented, so a client that withdraws the build
// stops the fill instead of only the answer to it. A withdrawn fill writes no
// lockfile and leaves whatever was already pinned exactly as it was.
//
// # The render it conserves, and why only a finished one
//
// The resolution renders this Release to discover which external inputs it
// consumes, so a fill that ran holds a render of exactly what the build was
// about to render. That render is returned, and the build is answered from it
// instead of rendering again.
//
// Only a render that FINISHED is handed over. A discovery that stopped part way
// holds findings worth reporting and no artifacts, and a build handed one would
// write out a tree missing whatever the render never reached — with nothing in
// the result saying so, because a short artifact set reads exactly like a
// complete one. The question is put to the render's own verdict rather than to
// the resolution's separate report of what stopped it, so there is one answer
// rather than two that agree today; the accessor answers the same way for a
// resolution that never reached a discovery at all, which needs no clause of its
// own for the same reason.
//
// Handing back nothing leaves the build to render for itself. The pins the fill
// did land are on disk and a reference it never reached is still unpinned, so
// that render is held to what the fill actually wrote and halts naming what is
// missing.
func (s *Server) fillDependencies(ctx context.Context, release string) ([]string, *dependencyResolutionReport, *compilerops.PreviewResult, *jsonRPCError) {
	releasePath, paramErr := resolveReleaseParam(s.workspaceRoot, s.wsConfig, release)
	if paramErr != nil {
		return nil, nil, nil, paramErr
	}
	if s.resolvedWorkspace == nil {
		return nil, nil, nil, &jsonRPCError{
			Code:    jsonRPCInternalError,
			Message: "the daemon holds no resolved workspace to resolve dependencies against",
		}
	}

	var notices bytes.Buffer
	res, err := pullops.Run(ctx, pullops.Config{
		Workspace:       s.resolvedWorkspace,
		ReleasePath:     releasePath,
		CompilerVersion: s.compilerVersion,
		Toolchain:       s.toolchain,
		DiagOut:         &notices,
		// No prompt is supplied, on the same terms every operation here runs
		// under: nobody is watching this stream, so a fork that would have been
		// put to a person halts naming what to supply.
	})
	if err != nil {
		return nil, nil, nil, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: err.Error(),
			cause:   err,
		}
	}
	report := dependencyResolutionReportOf(res, s.workspaceRoot)
	// The conserved render, asked of its own verdict — see this method's notes for
	// why only a finished one travels.
	conserved := res.DiscoveryRender
	if conserved.PipelineOutcome().Completed() == nil {
		conserved = nil
	}
	return noticeLines(notices.String()), &report, conserved, nil
}

// dependencyResolutionNotices carries the fill's own lines onto the answer under
// the surface that raised them, so a client renders them beside the render's
// advisories without having to parse either.
func dependencyResolutionNotices(lines []string) []buildNotice {
	if len(lines) == 0 {
		return nil
	}
	out := make([]buildNotice, 0, len(lines))
	for _, line := range lines {
		out = append(out, buildNotice{Class: noticeClassDependencyResolution, Text: line})
	}
	return out
}

// buildOutputRoot is the directory a build performed by this daemon writes to:
// the default output directory, created inside the location within the workspace
// the request named, or inside the workspace root this session anchored to when
// the request named none.
//
// It is a method rather than a literal at the call site so that what the daemon
// can write to is readable from a test, and so that the anchoring is stated once.
//
// 🔴 The joined default output directory is why the parameter names a PARENT.
// What the caller sends is the directory the output directory is created inside,
// so the directory that ends up holding the artifacts is one level below the
// value sent — which is the whole of the difference between this parameter and
// the destination the other writing methods on this wire take, and the reason
// the two carry different names.
//
// 🔴 outputParent is judged by workspacePath rather than by a comparison written
// here, because that is the one rule every destination on this surface takes —
// so what this method may return is bounded by the workspace the session opened,
// and a second containment written beside it could not drift away from the
// first. The refusal carries that rule's own sentence, which names the workspace
// and the fault, so a caller is told what to send rather than only that it sent
// the wrong thing.
//
// An empty outputParent is the request that named none rather than a value to
// judge. It is what a caller computing the root's own path relative to itself
// produces, and the root is precisely where a request naming nothing already
// writes, so the two states are one state.
//
// A daemon holding no workspace root resolves to the relative default, which is
// what the command line does with the same value — there is no session anchor to
// improve on it, and inventing an absolute path from the process's working
// directory would write somewhere neither surface named. A request that names a
// destination against such a session is refused by the rule above, which has no
// root to confine anything against.
func (s *Server) buildOutputRoot(outputParent string) (string, *jsonRPCError) {
	if outputParent != "" {
		anchor, err := s.workspacePath("build: outputParent", outputParent)
		if err != nil {
			return "", &jsonRPCError{
				Code:    jsonRPCInvalidParams,
				Message: "invalid params: " + err.Error(),
				cause:   err,
			}
		}
		return filepath.Join(anchor, artifactout.DefaultOutputDir), nil
	}
	if s.workspaceRoot == "" {
		return artifactout.DefaultOutputDir, nil
	}
	return filepath.Join(s.workspaceRoot, artifactout.DefaultOutputDir), nil
}
