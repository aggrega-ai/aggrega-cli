// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"context"
	stderrors "errors"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// WarningClass names which advisory surface a preview warning came from, so a
// caller can render each class in its own voice without parsing the text.
type WarningClass int

const (
	// WarningCatalog is an advisory the caller's own catalog load produced and
	// handed to Preview for emission in sequence (PreviewInputs.CatalogWarnings).
	WarningCatalog WarningClass = iota
	// WarningDeprecation is a recorded deprecation marker on a pinned reference
	// or on a plugin trusted for a verdict attachment.
	WarningDeprecation
	// WarningCatalogLint is a schema-pillar-overlap advisory over the closure's
	// entities.
	WarningCatalogLint
	// WarningExtends is a closure-scoped extends-resolution failure detail. It
	// accompanies a halt: the entries name each broken parent, and Preview
	// returns the summary failure alongside them.
	WarningExtends
)

// Warning is one advisory raised during a preview, carrying the class that
// produced it so a caller renders it in its own voice.
type Warning struct {
	Class WarningClass
	Text  string
}

// PreviewInputs carries the explicit state Preview needs. Every field is
// supplied by the caller: the layer holds no session state and reads no
// front-end fields.
//
// View is the request-scoped catalog lookup every resolution runs through, so
// the whole preview resolves against one pin snapshot. Lockfile is the
// Release's loaded lockfile (nil when none is present, which disables the
// gates that read it).
type PreviewInputs struct {
	View            *catalog.LazyLookup
	ReleasePath     string
	WorkspaceRoot   string
	WorkspaceConfig workspaceconfig.Config
	Lockfile        *lockfile.Lockfile
	CompilerVersion string

	// LockfilePreexisted reports whether the lockfile was on disk before this
	// run rather than filled during it. Only a pre-existing lockfile is drift-
	// verified: one just filled matches the closure by construction, so
	// verifying it would compare a set against itself.
	LockfilePreexisted bool

	// ExternalInputs is the render-time fetch seam for guest external-input
	// requests. When nil NO service is attached, and a guest asking for an
	// external input receives a not-available envelope instead of the content —
	// which renders different bytes than the same Release renders with one
	// attached. A preview that must match a build's bytes supplies a service
	// with acquisition off: pinned references are served from the content-
	// addressed store, unpinned ones refuse loudly, and nothing is written.
	ExternalInputs wasm.FetchService

	// CatalogWarnings are advisories the caller's own catalog load produced.
	// They are handed in rather than re-derived because only a caller that
	// eagerly loaded a catalog has them, and emitting them here keeps the whole
	// advisory sequence in one order with one definition.
	CatalogWarnings []string

	// Phase, when non-nil, is called as the preview enters each stage, so a
	// caller driving a progress surface can report where the run is. It is a
	// report OF the sequence, never a hook into it: the callback cannot alter
	// what runs or in what order. nil for a caller with no progress surface.
	Phase func(PreviewPhase)
}

// PreviewPhase names a stage of the preview sequence, for a caller reporting
// progress. The values are the stages a caller can meaningfully display; they
// do not enumerate every gate, because a progress surface that named each gate
// would flicker through most of them faster than a reader could see.
type PreviewPhase int

const (
	// PhaseClosure covers integrity verification and closure resolution.
	PhaseClosure PreviewPhase = iota
	// PhaseValidate covers closure-scoped validation, axis reconstruction and
	// the drift verify.
	PhaseValidate
	// PhaseInheritance covers extends resolution and the advisory gates.
	PhaseInheritance
	// PhaseRender covers the pipeline run through the provider render step.
	PhaseRender
)

// PreviewPolicy names, one field per axis, every gate difference that survives
// between the surfaces. It is exhaustive by construction: a gate absent from
// this struct runs identically everywhere, and a difference that cannot be
// justified is removed rather than given a field.
//
// The two shipped postures are ArtifactPolicy and InspectionPolicy below; a
// caller states which it is rather than assembling fields by hand.
type PreviewPolicy struct {
	// VerifyPinnedIntegrity halts when a pinned cache slot has drifted from its
	// lockfile pin, before the closure walk — so a tampered or drifted cache
	// surfaces as "re-pull" rather than a misleading downstream "not
	// resolvable". A surface that produces an artifact sets it; a resilient
	// inspection keeps its skip, because an editor that refuses to draw a graph
	// over a drifted slot is less useful than one that draws it and reports the
	// drift.
	VerifyPinnedIntegrity bool

	// HaltOnManifestInconsistency decides this surface's verdict on the
	// mapping-table findings every closure resolution produces. A surface that
	// emits a deployable artifact halts: rendering a component through a recipe
	// that does not implement its type would produce an artifact nobody asked
	// for. A reporting surface enumerates them beside its other findings, which
	// is why the findings ride back on the result either way.
	HaltOnManifestInconsistency bool

	// ValidateClosurePillars validates ONLY the entities the Release reaches
	// against the closure's libraries. An out-of-closure break is never
	// validated under either setting.
	ValidateClosurePillars bool

	// ReconstructClosureAxes rebuilds the sub-aggregate and pipeline-plugin axes
	// ComputeClosure leaves empty, hermetically, from the lockfile. It is
	// required before a drift verify — without it the comparison reports
	// spurious "no longer used" entries — and harmless without one.
	ReconstructClosureAxes bool

	// VerifyLockfileDrift halts when the computed closure has drifted from the
	// lockfile. An artifact-producing surface halts; an inspection surface
	// leaves it off and reports drift through its own non-blocking channel,
	// because inspection is not a deploy gate.
	VerifyLockfileDrift bool

	// ResolveExtends resolves extends inheritance for the closure's Components
	// and Features so the pipeline sees fully-merged entities. Closure-scoped:
	// an out-of-closure broken extends never fails under either setting.
	ResolveExtends bool

	// EnforceDeprecation reports every recorded deprecation marker — on pinned
	// references and on plugins trusted for a verdict attachment — and halts
	// where the workspace configured a halt at that severity. Reporting and
	// halting are independent: reporting is unconditional once this is set, and
	// only halting reads the dial.
	EnforceDeprecation bool

	// ReportCatalogLint surfaces schema-pillar-overlap advisories over the
	// closure's entities. Advisory only under every setting.
	ReportCatalogLint bool

	// VerifyRecipeResolution halts at render when an instance resolved to no
	// recipe. An artifact-producing surface sets it; an inspection surface
	// leaves it off so a graph still returns over an incomplete resolution.
	VerifyRecipeResolution bool

	// TolerateCatalogViewInconsistency drops the guard-layer halt for a type
	// that resolved as a catalog reference yet is momentarily absent from the
	// view. A resilient inspection sets it so the graph builds with the entity
	// skipped; an artifact-producing surface leaves it false so the divergence
	// halts loudly.
	TolerateCatalogViewInconsistency bool
}

// ArtifactPolicy is the posture of a surface that produces a deployable
// artifact — the command-line build, the acquisition pass, and the in-memory
// build tool. Every gate is on and the divergences halt, because the output is
// something a person will apply.
func ArtifactPolicy() PreviewPolicy {
	return PreviewPolicy{
		VerifyPinnedIntegrity:            true,
		HaltOnManifestInconsistency:      true,
		ValidateClosurePillars:           true,
		ReconstructClosureAxes:           true,
		VerifyLockfileDrift:              true,
		ResolveExtends:                   true,
		EnforceDeprecation:               true,
		ReportCatalogLint:                true,
		VerifyRecipeResolution:           true,
		TolerateCatalogViewInconsistency: false,
	}
}

// InspectionPolicy is the posture of a surface that draws a picture rather than
// emitting an artifact — the editor's graph inspection. It resolves and renders
// through the same computation, and it refuses nothing the artifact posture
// refuses, because an inspection that halts leaves the reader with no graph at
// all where a build would have left them a diagnostic.
//
// The caller owns the reporting: the findings and warnings ride back on the
// result, and an inspection surface publishes them through its own non-blocking
// channel.
func InspectionPolicy() PreviewPolicy {
	return PreviewPolicy{
		VerifyPinnedIntegrity:            false,
		HaltOnManifestInconsistency:      false,
		ValidateClosurePillars:           false,
		ReconstructClosureAxes:           true,
		VerifyLockfileDrift:              false,
		ResolveExtends:                   false,
		EnforceDeprecation:               false,
		ReportCatalogLint:                false,
		VerifyRecipeResolution:           false,
		TolerateCatalogViewInconsistency: true,
	}
}

// PreviewGate names one gate in the preview sequence. A caller reads it off the
// result to shape a gate-specific diagnostic — which document to attribute the
// failure to, and what to tell the reader to do — without matching on message
// text, which is presentation and free to change.
type PreviewGate int

const (
	// GateNone is the zero value: no gate refused.
	GateNone PreviewGate = iota
	// GatePinnedIntegrity is the pinned-cache integrity verification.
	GatePinnedIntegrity
	// GateClosureResolution is the Release-to-closure resolution.
	GateClosureResolution
	// GateManifestConsistency is the mapping-table verdict.
	GateManifestConsistency
	// GateClosurePillars is the closure-scoped schema validation.
	GateClosurePillars
	// GateAxisReconstruction is the hermetic closure-axis reconstruction.
	GateAxisReconstruction
	// GateLockfileDrift is the lockfile-drift verification.
	GateLockfileDrift
	// GateExtends is closure-scoped extends resolution.
	GateExtends
	// GateDeprecation is the deprecation verdict over the two record stores.
	GateDeprecation
	// GateRender is the pipeline run itself.
	GateRender
)

// PreviewResult carries everything a preview produces. Pipeline is nil when a
// gate halted before the render; Resolved and Warnings are populated as far as
// the run reached, so a caller can still report what was found before the halt.
type PreviewResult struct {
	Pipeline *pipeline.Result
	Resolved *ResolvedClosure
	Warnings []Warning

	// FailedGate names the gate that refused, or GateNone on success. It is the
	// caller's routing key for a gate-specific diagnostic.
	FailedGate PreviewGate

	// ManifestConsistencyFindings is the mapping-table finding set, carried here
	// as well as on Resolved so a caller reaching a non-halting verdict reads it
	// from the result it already holds.
	ManifestConsistencyFindings []error
}

// resolutionFailure marks a preview failure that a catalog-skip record can
// explain — one raised while resolving or validating what the Release reaches.
// A caller holding such a record decorates only these, so a deprecation halt
// never acquires a note about skipped catalog files that had nothing to do
// with it.
type resolutionFailure struct{ err error }

func (e *resolutionFailure) Error() string { return e.err.Error() }
func (e *resolutionFailure) Unwrap() error { return e.err }

// IsResolutionFailure reports whether err came from a gate whose failure a
// catalog-skip record can explain — closure resolution, closure-scoped
// validation, axis reconstruction or extends resolution. A caller annotates
// only these with its skip record.
func IsResolutionFailure(err error) bool {
	var rf *resolutionFailure
	return stderrors.As(err, &rf)
}

// Preview runs the one preview computation every rendering surface is a
// transport over: the gate sequence, then the full pipeline including render,
// returning the artifacts, the IR graph, the render diagnostics and the
// warnings. It performs NO filesystem writes — a caller that persists artifacts
// writes the returned buffers itself, and a caller that acquires external
// inputs merges them itself.
//
// The sequence is fixed here rather than at each surface, which is the point: a
// gate order carries meaning, and it is not something a caller should be free to
// re-derive. The orderings that are load-bearing:
//
//   - The pinned-integrity gate runs BEFORE the closure walk, so a drifted cache
//     surfaces as "re-pull" rather than as a downstream "not resolvable".
//   - The mapping-table verdict runs before the closure-scoped validation, so a
//     Manifest that contradicts the catalog is reported as such rather than as
//     whatever the walk failed on next.
//   - Axis reconstruction runs before the drift verify, because the verify
//     compares against the reconstructed set.
//
// pol names every gate difference that survives between surfaces; opts carries
// the pipeline knobs the caller owns (trace, render-skip, progress callbacks).
// ctx is the caller's, already carrying any of its own render knobs; Preview
// adds only the external-input seam.
//
// On a gate halt the returned result is non-nil and carries the warnings raised
// up to that point, so a caller reports what was found before it reports the
// failure.
func Preview(ctx context.Context, in PreviewInputs, pol PreviewPolicy, opts pipeline.Options) (*PreviewResult, error) {
	out := &PreviewResult{}
	warn := func(class WarningClass, text string) {
		out.Warnings = append(out.Warnings, Warning{Class: class, Text: text})
	}
	phase := func(p PreviewPhase) {
		if in.Phase != nil {
			in.Phase(p)
		}
	}

	phase(PhaseClosure)

	// Loud integrity gate, ahead of the closure walk. A pinned cache slot that
	// drifted from its lockfile pin halts here with the integrity diagnostic, so
	// a tampered or drifted cache surfaces as "re-pull" rather than a misleading
	// downstream "not resolvable".
	if pol.VerifyPinnedIntegrity {
		if err := in.View.VerifyPinnedIntegrity(); err != nil {
			out.FailedGate = GatePinnedIntegrity
			return out, fmt.Errorf("lockfile integrity: %w", err)
		}
	}

	// Shared Release to closure orchestration. Augment folds the cross-document
	// library axes so the closure matches the one drift is verified against; it
	// is on whenever this layer owns the reconstruction, and a caller that folded
	// the axes itself no longer exists — the fold has one home now.
	resolved, cerr := ResolveClosure(ClosureInputs{
		View:            in.View,
		ReleasePath:     in.ReleasePath,
		WorkspaceRoot:   in.WorkspaceRoot,
		WorkspaceConfig: in.WorkspaceConfig,
		Lockfile:        in.Lockfile,
		Augment:         true,
	})
	if cerr != nil {
		out.FailedGate = GateClosureResolution
		return out, &resolutionFailure{err: cerr}
	}
	out.Resolved = resolved
	out.ManifestConsistencyFindings = resolved.ManifestConsistencyFindings

	// The mapping-table verdict. Joined so every inconsistent mapping is
	// reported at once rather than one per re-run.
	if pol.HaltOnManifestInconsistency {
		if err := stderrors.Join(resolved.ManifestConsistencyFindings...); err != nil {
			out.FailedGate = GateManifestConsistency
			return out, err
		}
	}

	phase(PhaseValidate)

	// Closure-scoped validation: ONLY the entities the Release reaches, against
	// the closure's libraries.
	if pol.ValidateClosurePillars {
		if err := in.View.ValidateClosureScopedPillars(resolved.Closure); err != nil {
			out.FailedGate = GateClosurePillars
			return out, &resolutionFailure{err: err}
		}
	}

	// Reconstruct the axes ComputeClosure leaves empty, hermetically and from the
	// lockfile, so the drift comparison below is like-for-like.
	if pol.ReconstructClosureAxes && in.Lockfile != nil {
		if err := reconstructPreviewClosureAxes(in, resolved); err != nil {
			out.FailedGate = GateAxisReconstruction
			return out, &resolutionFailure{err: err}
		}
	}

	// Drift verification against the freshly-computed closure. Only a
	// pre-existing lockfile is verified; one filled during this run matches by
	// construction.
	if pol.VerifyLockfileDrift && in.Lockfile != nil && in.LockfilePreexisted {
		if err := lockfile.Verify(in.Lockfile, resolved.Closure); err != nil {
			out.FailedGate = GateLockfileDrift
			return out, fmt.Errorf("%w; run `aggrega pull --upgrade --release %s` to refresh the lockfile against current catalog state",
				err, in.ReleasePath)
		}
	}

	phase(PhaseInheritance)

	// Extends inheritance for the closure's Components and Features, so the
	// pipeline sees fully-merged entities.
	if pol.ResolveExtends {
		if extErrs := in.View.ResolveClosureExtends(resolved.Closure); len(extErrs) > 0 {
			for _, e := range extErrs {
				warn(WarningExtends, e.Error())
			}
			out.FailedGate = GateExtends
			return out, &resolutionFailure{err: fmt.Errorf("catalog extends: %d resolution error(s)", len(extErrs))}
		}
	}

	// Advisories the caller's own catalog load produced, emitted here so the
	// whole advisory sequence has one order.
	for _, w := range in.CatalogWarnings {
		warn(WarningCatalog, w)
	}

	// The deprecation gates, over the two stores. Both read only their record —
	// no registry access, preserving the ahead-of-time boundary. Reporting is
	// unconditional; only halting reads the dial, so a stricter threshold never
	// reduces what the caller is told.
	if pol.EnforceDeprecation {
		halt, err := enforceDeprecation(in, warn)
		if err != nil {
			out.FailedGate = GateDeprecation
			return out, err
		}
		if halt != nil {
			out.FailedGate = GateDeprecation
			return out, halt
		}
	}

	// Schema-pillar-overlap advisories: the warning-level sibling of the
	// closure-scoped validation above, over the same entity set.
	if pol.ReportCatalogLint {
		for _, w := range in.View.ClosureLintWarnings(resolved.Closure) {
			warn(WarningCatalogLint, w)
		}
	}

	phase(PhaseRender)

	// The render. The external-input seam is installed here and nowhere else, so
	// a surface cannot acquire the pipeline without it and render different bytes
	// than the surface beside it.
	if in.ExternalInputs != nil {
		ctx = wasm.ContextWithExternalInputs(ctx, in.ExternalInputs)
	}
	if pol.VerifyRecipeResolution {
		opts.RecipeResolutionVerifier = in.View.VerifyRecipeResolution
	}
	result, err := BuildToBuffer(ctx, BuildInputs{
		View:                             in.View,
		Resolved:                         resolved,
		ReleasePath:                      in.ReleasePath,
		WorkspaceConfig:                  in.WorkspaceConfig,
		WorkspaceRoot:                    in.WorkspaceRoot,
		Lockfile:                         in.Lockfile,
		CompilerVersion:                  in.CompilerVersion,
		TolerateCatalogViewInconsistency: pol.TolerateCatalogViewInconsistency,
	}, opts)
	if err != nil {
		out.FailedGate = GateRender
		return out, err
	}
	out.Pipeline = result
	return out, nil
}

// enforceDeprecation reports every recorded marker on the two stores and
// returns the halt the workspace configured, if any. The walk always completes
// before a halt is returned, so a configured halt never costs the reader the
// notices for references sorted after the halting one.
//
// The second return is a failure to READ the trust record, which is distinct
// from a halt the record's contents earned: a record that cannot be read leaves
// the surface unable to say whether anything was withdrawn, which is not the
// same as saying nothing was.
func enforceDeprecation(in PreviewInputs, warn func(WarningClass, string)) (halt error, readErr error) {
	report := func(notice string) { warn(WarningDeprecation, notice) }

	halt = LockfileDeprecationVerdict(in.Lockfile, in.WorkspaceConfig.Build, report)

	// The same gate over the other store. A plugin attached at a verdict role
	// shapes no rendered byte, so it is recorded beside the lockfile rather than
	// pinned in it and has no row the gate above could read. Without this, a
	// withdrawn gate plugin is fetched, cached and run with nothing said.
	trustRecord, _, trustErr := pluginstrust.LoadOrStart(pluginstrust.PathFor(in.ReleasePath))
	if trustErr != nil {
		return nil, trustErr
	}
	// Both walks always complete: the second runs even when the first earned a
	// halt, so a reader is told about every withdrawn plugin rather than only
	// about whichever store happened to be walked first.
	if trustHalt := TrustDeprecationVerdict(trustRecord, in.WorkspaceConfig.Build, report); halt == nil {
		halt = trustHalt
	}
	return halt, nil
}

// reconstructPreviewClosureAxes rebuilds the sub-aggregate and pipeline-plugin
// axes ComputeClosure leaves empty, plus the output-shaping workspace-socket
// entities, from the LOCKFILE rather than the workspace config — so a
// plugins.providers change made after the pull does not move the reconstructed
// closure and the drift comparison stays honest.
//
// Reconstruction is hermetic: the closure Lookup carries a nil puller, so no
// registry fetch fires. The re-fold shifts the view's pin set to the augmented
// closure to make the just-pinned plugin entities visible, and the pins are
// restored to the lockfile view afterward so the render resolves exactly as it
// would have without the reconstruction.
func reconstructPreviewClosureAxes(in PreviewInputs, resolved *ResolvedClosure) error {
	view := in.View
	lookup := resolved.Lookup

	aggLoader, err := closure.RequireAggregateLoader(lookup)
	if err != nil {
		return err
	}
	recipeLoader, err := closure.RequireRecipeLoader(lookup)
	if err != nil {
		return err
	}
	bindings := lockfile.ProviderBindings(in.Lockfile)
	resolvePlugin := func(ref string) (types.Plugin, bool, error) {
		return view.ProviderPluginForTarget(ref, bindings)
	}
	refold := func() (closure.RecipeLoader, error) {
		view.SetPins(catalog.PinsFromClosure(resolved.Closure))
		return view.ClosureRecipeLoader(), nil
	}
	if err := closure.ReconstructClosureAxes(&resolved.Closure, aggLoader, recipeLoader, lookup, resolvePlugin, refold); err != nil {
		view.SetPins(catalog.PinsFromLockfile(in.Lockfile))
		return err
	}
	view.SetPins(catalog.PinsFromLockfile(in.Lockfile))

	// The output-shaping workspace-socket plugin entities and their .wasm are
	// pull-time choices recorded only in the lockfile, so ComputeClosure cannot
	// re-derive them hermetically. The socket lookup honors the lockfile pins so
	// a pinned socket .wasm resolves from the content-addressed cache offline;
	// the entity and artifact are re-read so a drifted pin still surfaces as
	// drift.
	socketEntityURIs := lockfile.WorkspaceSocketEntityURIs(in.Lockfile)
	if len(socketEntityURIs) == 0 {
		return nil
	}
	socketLookup := view.ClosureLookupWithPins(nil, in.WorkspaceRoot, in.WorkspaceConfig.ResolvedArtifactCacheDir(), nil, catalog.PinsFromLockfile(in.Lockfile))
	socketRecipeLoader, err := closure.RequireRecipeLoader(socketLookup)
	if err != nil {
		return err
	}
	return closure.ReconstructWorkspaceSocketEntities(&resolved.Closure, socketEntityURIs, socketRecipeLoader, socketLookup)
}
