// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
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
	// handed to the preview for emission in sequence (WithCatalogWarnings).
	WarningCatalog WarningClass = iota
	// WarningDeprecation is a recorded deprecation marker on a pinned reference
	// or on a plugin trusted for a verdict attachment.
	WarningDeprecation
	// WarningCatalogLint is a schema-pillar-overlap advisory over the closure's
	// entities.
	WarningCatalogLint
	// WarningExtends is a closure-scoped extends-resolution failure detail. It
	// accompanies a halt: the entries name each broken parent, and the run
	// returns the summary failure alongside them.
	WarningExtends
	// WarningReleaseComposition is a reading over what folding the Release's own
	// extends chain produced. Unlike WarningExtends it accompanies no halt: each
	// entry names a shape an author may legitimately mean, and the run succeeds.
	WarningReleaseComposition
)

// Warning is one advisory raised during a preview, carrying the class that
// produced it so a caller renders it in its own voice.
//
// Code is the identity of the registered condition the warning reports, read
// off that condition where the warning is raised, so a caller matching on it
// matches the condition rather than its wording. It is read there because that
// is the last point the finding is still the condition: past it the warning is
// text, and nothing downstream could recover the identity from the sentence. It
// is empty for a warning whose producer hands over text naming no condition.
type Warning struct {
	Class WarningClass
	Text  string
	Code  string
}

// PreviewInputs carries the explicit state a preview needs. Every value is
// supplied by the caller: the layer holds no session state and reads no
// front-end fields.
//
// EVERY FIELD IS UNEXPORTED AND THE ONLY WAY TO OBTAIN A USABLE VALUE IS
// NewPreviewInputs. That is a deliberate constraint rather than a style, and the
// reason is the render-time fetch seam: a keyed composite literal compiles
// perfectly well with a field left out, so an options struct — however
// exhaustively documented — cannot make omitting the seam an error. A caller
// that omitted it got no service attached, and the same Release then rendered
// different bytes on that surface than on the ones beside it, with no gate able
// to report the difference.
//
// Unexporting the seam alone would not have helped. Go forbids NAMING an
// unexported field from another package and forbids nothing about leaving one
// out, so a literal that simply omitted it would still compile and still leave
// the seam nil. Unexporting every field is what makes the whole literal
// unwritable from outside, which leaves the constructor as the only door.
//
// The guarantee is hybrid, and the seam of it is worth stating rather than
// glossing: the constructor's signature is compile-time — a caller that names no
// seam does not build — while the bare zero value PreviewInputs{} still
// compiles, because Go admits the zero value of any struct. A preview refuses a
// zero-valued input at run time to close that half. Inside this package,
// field-by-field construction remains possible; the run-time refusal catches
// that too unless the literal also sets constructed by hand.
type PreviewInputs struct {
	// view is the request-scoped catalog lookup every resolution runs through,
	// so the whole preview resolves against one pin snapshot.
	view            *catalog.LazyLookup
	releasePath     string
	workspaceRoot   string
	workspaceConfig workspaceconfig.Config
	compilerVersion string

	// externalInputs is the render-time fetch seam for guest external-input
	// requests. When nil NO service is attached, and a guest asking for an
	// external input receives a not-available envelope instead of the content —
	// which renders different bytes than the same Release renders with one
	// attached. A preview that must match a build's bytes supplies a service
	// with acquisition off: pinned references are served from the content-
	// addressed store and unpinned ones refuse loudly, so the closure cannot
	// widen. PinnedExternalInputs builds exactly that service.
	externalInputs wasm.FetchService

	// lockfile is the Release's loaded lockfile, nil when none is present, which
	// disables the gates that read it.
	lockfile *lockfile.Lockfile

	// lockfilePreexisted reports whether the lockfile was on disk before this
	// run rather than filled during it. A pre-existing lockfile is drift-verified.
	// One a fill wrote during this run is not verified again, because the fill
	// already held it to this same verify before writing it: the resolution that
	// fills a lockfile renders it through this computation with lockfileComposed
	// set, and refuses to write one this verify disagrees with.
	lockfilePreexisted bool

	// lockfileComposed reports that the lockfile is one the caller has just
	// composed and has not yet written, handed in so it can be held to the build's
	// drift verify before it reaches disk. A disagreement then is not a stale
	// lockfile — nothing has been written that could have gone stale — but the
	// composing resolution and the build disagreeing about the same Release, so it
	// is reported as that rather than as drift.
	lockfileComposed bool

	// composedTrust is the record of plugin identities beside the lockfile as the
	// caller has composed it and has not yet written it, or nil, which reads the
	// record from disk. Only the recorded signatures are read from it: by the check
	// before the render, and by the load of each plugin attached at a verdict role.
	composedTrust *pluginstrust.Trust

	// resolutionAdvisoriesShown reports that the caller's operation has already
	// shown the reader the advisories this Release's resolution raises, so the
	// render's own resolution records them without showing them again.
	resolutionAdvisoriesShown bool

	// display is where the preview's run displays what it raises as it raises
	// it — the lines a build shows a reader at a terminal. nil shows nothing; the
	// advisories arrive on the result either way. See WithDisplay.
	display io.Writer

	// catalogWarnings are advisories the caller's own catalog load produced.
	// They are handed in rather than re-derived because only a caller that
	// eagerly loaded a catalog has them, and emitting them here keeps the whole
	// advisory sequence in one order with one definition.
	catalogWarnings []string

	// phase, when non-nil, is called as the preview enters each stage, so a
	// caller driving a progress surface can report where the run is. It is a
	// report OF the sequence, never a hook into it: the callback cannot alter
	// what runs or in what order. nil for a caller with no progress surface.
	phase func(PreviewPhase)

	// constructed records that this value came from NewPreviewInputs. It is what
	// lets a preview tell a constructed input from the zero value the language
	// still admits, and it is set nowhere else.
	constructed bool
}

// PreviewOption supplies one of the inputs whose absence is a coherent decision
// rather than an oversight — a Release with no lockfile, a caller with no
// catalog advisories of its own, a caller with no progress surface. Everything
// whose absence would be a defect is a parameter of NewPreviewInputs instead, so
// it cannot be left out.
type PreviewOption func(*PreviewInputs)

// WithLockfile supplies the Release's loaded lockfile and whether it was on disk
// before this run. The two travel together because neither answers anything
// alone: "pre-existing" is a fact about a lockfile, so a caller cannot claim it
// without naming one.
func WithLockfile(lf *lockfile.Lockfile, preexisted bool) PreviewOption {
	return func(in *PreviewInputs) {
		in.lockfile = lf
		in.lockfilePreexisted = preexisted
	}
}

// WithComposedLockfile supplies a lockfile the caller has composed and is about
// to write, as the source of the render's pins and as the subject of the drift
// verify a build of the same Release will run. It is the one lockfile state the
// verify is not a staleness check for: nothing has been written yet, so a
// disagreement means the composing resolution and the build disagree about what
// the Release depends on, and the run refuses with errComposedLockfileRefused
// instead of the drift refusal. Its caller writes nothing on that refusal.
func WithComposedLockfile(lf *lockfile.Lockfile) PreviewOption {
	return func(in *PreviewInputs) {
		in.lockfile = lf
		in.lockfilePreexisted = false
		in.lockfileComposed = true
	}
}

// WithComposedTrustRecord supplies the record of plugin identities kept beside
// the lockfile as the caller has composed it and is about to write it, so the check
// of recorded signatures holds each plugin attached at a verdict role to the fact
// this run records rather than to the record on disk the run is about to replace.
// The resolution that composes the record writes it only after its render, so a
// render reading the file would read it as it stood before the run — and on a first
// resolution, there is none. The render's own load of each such plugin reads its
// recorded signature from it too, for the same reason. Nothing else in the preview
// reads it: the record's identity hold and its withdrawal report read the file, as
// every other surface does.
func WithComposedTrustRecord(trust *pluginstrust.Trust) PreviewOption {
	return func(in *PreviewInputs) { in.composedTrust = trust }
}

// WithResolutionAdvisoriesShown declares that the caller's operation has already
// shown the reader the advisories the Release's resolution raises, through a
// resolution of its own made before this preview, so the preview's resolution
// records them on the result as usual and shows none of them a second time. A
// pull is that caller: it resolves the Release to draw the graph it pins, then
// renders the same Release here to discover its external inputs.
func WithResolutionAdvisoriesShown() PreviewOption {
	return func(in *PreviewInputs) { in.resolutionAdvisoriesShown = true }
}

// WithDisplay supplies the writer the preview's run displays on as it raises
// each advisory: a surface whose reader looks at a terminal hands its error
// stream. Without it the run shows nothing as it goes, which is coherent for a
// surface that reads the advisories off the result.
func WithDisplay(w io.Writer) PreviewOption {
	return func(in *PreviewInputs) { in.display = w }
}

// WithCatalogWarnings supplies the advisories the caller's own catalog load
// produced, for emission in sequence with the ones the preview raises itself.
func WithCatalogWarnings(warnings []string) PreviewOption {
	return func(in *PreviewInputs) { in.catalogWarnings = warnings }
}

// WithProgress supplies the callback the preview calls as it enters each stage.
func WithProgress(phase func(PreviewPhase)) PreviewOption {
	return func(in *PreviewInputs) { in.phase = phase }
}

// NoExternalInputs is the value a caller names to render with NO external-input
// seam attached. It exists so that "no seam" is a decision written at the call
// site rather than a field nobody filled in: a guest asking for an external
// input under it receives a not-available envelope, and the surface renders
// different bytes than a surface with a seam attached.
//
// It reads as a decision; it is not a second enforcement. The constructor's
// signature is what makes a seam impossible to omit — passing nothing does not
// compile — and a caller may still write a bare nil in its place. What this buys
// is that a reader of the call site can tell a deliberate omission from an
// accidental one.
func NoExternalInputs() wasm.FetchService { return nil }

// NewPreviewInputs builds the inputs a preview runs over. The external-input seam
// is FIRST and positional precisely because it is the value whose omission was
// invisible: a caller must name either a service or NoExternalInputs, and one
// that names neither does not compile.
//
// view, releasePath, workspaceRoot, workspaceConfig and compilerVersion are
// parameters for the same reason — each has no coherent absent form, so leaving
// one out is a defect rather than a choice. The lockfile, the caller's catalog
// advisories and the progress callback are options, because a caller genuinely
// may have none of them.
func NewPreviewInputs(
	externalInputs wasm.FetchService,
	view *catalog.LazyLookup,
	releasePath string,
	workspaceRoot string,
	workspaceConfig workspaceconfig.Config,
	compilerVersion string,
	opts ...PreviewOption,
) PreviewInputs {
	in := PreviewInputs{
		view:            view,
		releasePath:     releasePath,
		workspaceRoot:   workspaceRoot,
		workspaceConfig: workspaceConfig,
		compilerVersion: compilerVersion,
		externalInputs:  externalInputs,
		constructed:     true,
	}
	for _, opt := range opts {
		opt(&in)
	}
	return in
}

// ExternalInputs returns the render-time fetch seam these inputs carry. It is
// read-only on purpose: a surface's own test asserts that what its construction
// hands the shared computation is the seam it built, which the constructor's
// signature cannot say — a caller that named NoExternalInputs compiles just as
// happily as one that named a service.
func (in PreviewInputs) ExternalInputs() wasm.FetchService { return in.externalInputs }

// errPreviewInputsZeroValue is the refusal for a PreviewInputs that never came
// from NewPreviewInputs. It closes the half of the guarantee the language does
// not: the constructor cannot be called without a seam, but the zero value of
// any struct is still writable, and a zero-valued input would render with no
// seam attached — the exact condition the constructor exists to prevent.
var errPreviewInputsZeroValue = stderrors.New(
	"preview inputs were not built by NewPreviewInputs, so no external-input seam was chosen; " +
		"build them with NewPreviewInputs, naming either a fetch service or NoExternalInputs")

// LockfileDriftRemedy is the advice a lockfile-drift finding carries: the
// command that re-resolves release and rewrites its lockfile. release is the
// Release's path, or a placeholder where the reporting surface holds none.
//
// It is the one home of that advice for every surface reporting the condition
// on a lockfile it read from disk — the command line's build and the editor's
// drift warning among them — so the two cannot come to name different remedies
// for one condition. It names an act that clears the condition whichever axis
// drifted — an entity, a library, a plugin or a Release file — which is why it
// speaks of the Release rather than of a catalog.
//
// That act is an ordinary pull, never an upgrade. A build resolves its closure
// from what is on the machine, under the pins the lockfile records, so every
// disagreement it reports is a change a pull reads back: the pull resolves the
// Release as it now stands, rewrites the pins that moved and holds every other
// one, so nothing floats that the reader did not change. Where the change is a
// file under a Recipe's local source, that pull first names the pin step for the
// entity whose recorded pins moved. The two cases an upgrade is for are raised by
// a pull rather than by a build, and each names the upgrade on its own entry: a
// source reference that moved at its remote, and a registry serving different
// bytes under a pinned version.
func LockfileDriftRemedy(release string) string {
	return fmt.Sprintf("run `aggrega pull --release %s` to re-resolve the Release and rewrite its lockfile", release)
}

// errComposedLockfileRefused is the refusal for a composed lockfile (see
// WithComposedLockfile) that the build's drift verify disagrees with. It names an
// internal inconsistency rather than a condition of the workspace: the lockfile
// was computed from this Release a moment ago and has not been written, so no
// edit made since can explain it, and resolving again would compose the same
// lockfile and meet the same refusal.
//
// It deliberately does not carry the drift condition's identity. That condition
// tells a reader their lockfile is out of date and that resolving again clears
// it; neither is true here, and a surface that routes on the condition would
// hand the reader that advice. The verify's own findings travel as the text of
// composedLockfileRefusal instead, so every disagreement is still named.
var errComposedLockfileRefused = stderrors.New(
	"internal: the lockfile composed for this Release does not match what a build of the same Release " +
		"resolves to, so it was not written. The resolution that composed it and the build disagree about " +
		"what the Release depends on, which is a defect in the compiler rather than in the workspace; " +
		"report it with the Release and the findings below")

// composedLockfileRefusal carries errComposedLockfileRefused together with the
// drift verify's own findings. It unwraps to the refusal and never to the drift
// condition, for the reason errComposedLockfileRefused gives.
type composedLockfileRefusal struct{ findings error }

func (e *composedLockfileRefusal) Error() string {
	return errComposedLockfileRefused.Error() + ":\n" + driftFindings(e.findings)
}

// driftFindings is the text of the entries a drift verify collected, one per
// line, without the drift condition's own opening sentence — that sentence says
// the lockfile is out of date, which is the one reading a composed lockfile
// cannot have. The entries are read off the verify's error tree rather than cut
// out of its text; a tree that does not have the shape looked for is reported
// whole, so no finding can be lost to the presentation.
func driftFindings(err error) string {
	multi, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return err.Error()
	}
	var entries []string
	for _, part := range multi.Unwrap() {
		if stderrors.Is(part, lockfile.ErrLockfileDrift()) {
			continue
		}
		entries = append(entries, part.Error())
	}
	if len(entries) == 0 {
		return err.Error()
	}
	return strings.Join(entries, "\n")
}

func (e *composedLockfileRefusal) Unwrap() error { return errComposedLockfileRefused }

// IsComposedLockfileRefusal reports whether err is the refusal a preview over a
// composed lockfile raises when the build's drift verify disagrees with it.
func IsComposedLockfileRefusal(err error) bool {
	return stderrors.Is(err, errComposedLockfileRefused)
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
	// PhaseInheritance covers extends resolution, the gates over the facts the
	// lockfile records, and the advisory gates.
	PhaseInheritance
	// PhaseRender covers the pipeline run through the provider render step.
	PhaseRender
)

// previewPolicy names, one field per axis, every gate difference that survives
// between the surfaces. It is exhaustive by construction: a gate absent from
// this struct runs identically everywhere, and a difference that cannot be
// justified is removed rather than given a field.
//
// THE TYPE AND ITS FIELDS ARE UNEXPORTED, AND SO ARE THE TWO POSTURES BELOW.
// That is a deliberate constraint rather than a style. A posture is not a
// preference a caller holds: it follows from what the operation IS, which is why
// all but one of these switches differ between the two postures — they are one
// idea expressed many times, not independent knobs. A caller handed a posture
// value could take a named one and change a switch on it, and the behaviour it
// then ran would have no name and no record. Nothing outside this package can
// name this type, so no such value can be built or altered there; each exported
// entry point supplies its own.
type previewPolicy struct {
	// verifyPinnedIntegrity halts, before the closure walk, on a pin whose cache
	// slot has drifted from it when no catalog directory holds the pinned bytes
	// either — so a tampered or drifted cache surfaces as the integrity finding
	// rather than a misleading downstream "not resolvable" — and passes a drifted
	// slot over, with a notice, where a catalog directory does hold them. A
	// surface that produces an artifact sets it; a resilient inspection keeps its
	// skip, because an editor that refuses to draw a graph over a drifted slot is
	// less useful than one that draws it and reports the drift.
	verifyPinnedIntegrity bool

	// haltOnManifestInconsistency decides this surface's verdict on the
	// mapping-table findings every closure resolution produces. A surface that
	// emits a deployable artifact halts: rendering a component through a recipe
	// that does not implement its type would produce an artifact nobody asked
	// for. A reporting surface enumerates them beside its other findings, which
	// is why the findings ride back on the result either way.
	haltOnManifestInconsistency bool

	// validateClosurePillars validates ONLY the entities the Release reaches
	// against the closure's libraries. An out-of-closure break is never
	// validated under either setting.
	validateClosurePillars bool

	// reconstructClosureAxes rebuilds the sub-aggregate and pipeline-plugin axes
	// ComputeClosure leaves empty, hermetically, from the lockfile. It is
	// required before a drift verify — without it the comparison reports
	// spurious "no longer used" entries — and harmless without one.
	reconstructClosureAxes bool

	// verifyLockfileDrift halts when the computed closure has drifted from the
	// lockfile. An artifact-producing surface halts; an inspection surface
	// leaves it off and reports drift through its own non-blocking channel,
	// because inspection is not a deploy gate.
	//
	// It carries a PRECONDITION the others do not: the question "has the
	// closure drifted from the lockfile?" presupposes a lockfile to ask it of.
	// The gate satisfies that itself rather than leaning on the operation — it
	// reads the lockfile from the inputs and stands down when there is none — and
	// that is what lets every artifact-producing surface verify drift on the same
	// terms, the one that neither requires a lockfile nor fills one included.
	//
	// So the third entry point over these two postures is not a third drift
	// posture. It survives for the tools that answer a question ABOUT a closure
	// rather than emitting an artifact from one, where refusing on a stale
	// lockfile would withhold the answer instead of qualifying it.
	verifyLockfileDrift bool

	// resolveExtends resolves extends inheritance for the closure's Components
	// and Features so the pipeline sees fully-merged entities. Closure-scoped:
	// an out-of-closure broken extends never fails under either setting.
	resolveExtends bool

	// enforceRecordedSignatures holds every reference the build reads to the
	// signature fact its lockfile row records: a recorded fact's cached bundle is
	// re-hashed, and a row the content cache served that records none halts where
	// the trust configuration requires a signature for it (see
	// enforceRecordedSignatures). A surface that produces an artifact sets it. An
	// inspection leaves it off, and so does the operation that renders from
	// whatever pins exist without holding the Release to a lockfile, because
	// neither emits something a person will apply.
	enforceRecordedSignatures bool

	// enforceDeprecation reports every recorded deprecation marker — on pinned
	// references and on plugins trusted for a verdict attachment — and halts
	// where the workspace configured a halt at that severity. Reporting and
	// halting are independent: reporting is unconditional once this is set, and
	// only halting reads the dial.
	enforceDeprecation bool

	// reportCatalogLint surfaces schema-pillar-overlap advisories over the
	// closure's entities. Advisory only under every setting.
	reportCatalogLint bool

	// verifyRecipeResolution halts at render when an instance resolved to no
	// recipe. An artifact-producing surface sets it; an inspection surface
	// leaves it off so a graph still returns over an incomplete resolution.
	verifyRecipeResolution bool

	// tolerateCatalogViewInconsistency drops the guard-layer halt for a type
	// that resolved as a catalog reference yet is momentarily absent from the
	// view. A resilient inspection sets it so the graph builds with the entity
	// skipped; an artifact-producing surface leaves it false so the divergence
	// halts loudly.
	tolerateCatalogViewInconsistency bool
}

// artifactPolicy is the posture of a surface that produces a deployable
// artifact — the command-line build, the pull's discovery render, and the
// in-memory build tool. Every gate is on and the divergences halt, because the
// output is something a person will apply.
func artifactPolicy() previewPolicy {
	return previewPolicy{
		verifyPinnedIntegrity:            true,
		haltOnManifestInconsistency:      true,
		validateClosurePillars:           true,
		reconstructClosureAxes:           true,
		verifyLockfileDrift:              true,
		resolveExtends:                   true,
		enforceRecordedSignatures:        true,
		enforceDeprecation:               true,
		reportCatalogLint:                true,
		verifyRecipeResolution:           true,
		tolerateCatalogViewInconsistency: false,
	}
}

// inspectionPolicy is the posture of a surface that draws a picture rather than
// emitting an artifact — the editor's graph inspection. It resolves and renders
// through the same computation, and it refuses nothing the artifact posture
// refuses, because an inspection that halts leaves the reader with no graph at
// all where a build would have left them a diagnostic.
//
// The caller owns the reporting: the findings and warnings ride back on the
// result, and an inspection surface publishes them through its own non-blocking
// channel.
func inspectionPolicy() previewPolicy {
	return previewPolicy{
		verifyPinnedIntegrity:            false,
		haltOnManifestInconsistency:      false,
		validateClosurePillars:           false,
		reconstructClosureAxes:           true,
		verifyLockfileDrift:              false,
		resolveExtends:                   false,
		enforceRecordedSignatures:        false,
		enforceDeprecation:               false,
		reportCatalogLint:                false,
		verifyRecipeResolution:           false,
		tolerateCatalogViewInconsistency: true,
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
	// GateRecordedSignatures is the check of the signature facts the lockfile
	// records for the references the build reads.
	GateRecordedSignatures
	// GateRender is the pipeline run itself.
	GateRender
)

// PreviewResult carries everything a preview produces. Resolved and Warnings are
// populated as far as the run reached, so a caller can still report what was
// found before a halt.
//
// The pipeline run's outcome is reached through [PreviewResult.PipelineOutcome]
// rather than read off a field. A gate that halted before the render leaves the
// zero outcome, which reports no completed payload; a gate that halted inside the
// run leaves the run's own outcome, which reports the advisories it raised. Both
// cases are the same accessor, so a caller cannot tell them apart by accident and
// cannot mistake either for a finished run.
type PreviewResult struct {
	// pipeline is unexported deliberately. Handed out as a field, a partial run's
	// payload is indistinguishable from a finished one at every consuming
	// surface, and a nil check standing in for the verdict compiles perfectly
	// well while discriminating nothing.
	pipeline pipeline.Outcome

	Resolved *ResolvedClosure
	Warnings []Warning

	// FailedGate names the gate that refused, or GateNone on success. It is the
	// caller's routing key for a gate-specific diagnostic.
	FailedGate PreviewGate

	// ManifestConsistencyFindings is the mapping-table finding set, carried here
	// as well as on Resolved so a caller reaching a non-halting verdict reads it
	// from the result it already holds.
	ManifestConsistencyFindings []error

	// renderFailure is the refusal the pipeline run raised, held where the run
	// raised it, and workspaceRoot the root the run resolved against. Both are
	// read through [PreviewResult.RenderRefusal] and nothing else, so a caller
	// cannot pair a refusal with a run other than the one that raised it.
	renderFailure error
	workspaceRoot string
}

// PipelineOutcome returns the pipeline run's outcome. It is nil-receiver safe and
// answers with the zero outcome for a preview that is absent or never reached the
// run, so a caller reads one shape whatever happened and needs no guard of its
// own before asking.
//
// The name is deliberately not the bare noun. A method named for the field it
// replaces would leave a comparison of that selector against nil compiling as a
// comparison of a method value — legal Go, always false — so a guard written that
// way would silently stop discriminating with nothing to reveal it. This name
// makes the same expression a compile error instead.
func (p *PreviewResult) PipelineOutcome() pipeline.Outcome {
	if p == nil {
		return pipeline.Outcome{}
	}
	return p.pipeline
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

// PreviewForArtifact renders a Release the way a surface that emits something a
// person will apply must: every gate on, every divergence a halt. It is the
// operation the command-line build, the pull's discovery render, the daemon's
// build methods and the in-memory build tool run.
//
// It holds the Release to its lockfile wherever one is present. Some of its
// callers establish that a lockfile exists before calling — the command line by
// filling a missing one, the daemon by refusing a Release that carries none — so
// for those a drifted lockfile contradicts something already guaranteed. A caller
// that does NEITHER still renders when none is present, and needs no relaxation
// to: the drift verify stands down on its own inputs, because the question it
// asks is about a lockfile and there is none to ask it of. What halts, on every
// caller alike, is a lockfile that exists and no longer matches.
func PreviewForArtifact(ctx context.Context, in PreviewInputs, opts pipeline.Options) (*PreviewResult, error) {
	return runPreview(ctx, in, artifactPolicy(), opts)
}

// PreviewForArtifactFromAvailablePins renders a Release under the same strict
// posture as PreviewForArtifact, for an operation that does not hold the Release
// to a lockfile: it neither requires one nor fills one, and resolves against
// whatever pins exist.
//
// It is a distinct operation rather than a third posture, and the distinction is
// the difference between what a run IS and how strict it is being. Every gate
// that judges the closure or the entities runs here exactly as it does for
// PreviewForArtifact. The single departure is the drift verify, whose question —
// has the closure drifted from the lockfile? — presupposes a lockfile the
// operation asked for. This one did not ask, so a lockfile it happens to find is
// a source of pins rather than a statement about the closure, and there is
// nothing for the drift verify to contradict.
//
// The gate reads the lockfile from the inputs and stands down when there is
// none, so a Release with no lockfile at all renders. Holding the verify off
// additionally accepts a STALE one, which is coherent only where the answer is
// not a claim about what a build would emit — an operation that emits one
// refuses it instead.
//
// The check of recorded signature facts stands down here for the same reason:
// it holds the references a build reads to what the lockfile records about them,
// and an operation that did not ask for a lockfile has made no claim about one.
func PreviewForArtifactFromAvailablePins(ctx context.Context, in PreviewInputs, opts pipeline.Options) (*PreviewResult, error) {
	pol := artifactPolicy()
	pol.verifyLockfileDrift = false
	pol.enforceRecordedSignatures = false
	return runPreview(ctx, in, pol, opts)
}

// PreviewForInspection renders a Release the way a surface that draws a picture
// must: the gates that would refuse are off, so a reader is left with a graph and
// a diagnostic rather than with nothing. It is the operation the editor's graph
// inspection runs.
//
// The findings and warnings ride back on the result, and the caller publishes
// them through its own non-blocking channel.
func PreviewForInspection(ctx context.Context, in PreviewInputs, opts pipeline.Options) (*PreviewResult, error) {
	return runPreview(ctx, in, inspectionPolicy(), opts)
}

// runPreview is the one preview computation every rendering surface is a
// transport over: the gate sequence, then the full pipeline including render,
// returning the artifacts, the IR graph, the render diagnostics and the
// warnings. It performs NO filesystem writes — a caller that persists artifacts
// writes the returned buffers itself, and a caller that acquires external
// inputs merges them itself.
//
// It is unexported, and the exported entry points above are the whole of the way
// in. Each supplies its own posture, so a caller selects an OPERATION and the
// strictness arrives with it; there is no posture-shaped argument to pass and
// therefore no way to answer, differently at each surface, a question the
// operation already answers.
//
// The sequence is fixed here rather than at each surface, which is the point: a
// gate order carries meaning, and it is not something a caller should be free to
// re-derive. The orderings that are load-bearing:
//
//   - The pinned-integrity gate runs BEFORE the closure walk, so a pin no source
//     holds surfaces as the integrity finding rather than as a downstream "not
//     resolvable".
//   - The mapping-table verdict runs before the closure-scoped validation, so a
//     Manifest that contradicts the catalog is reported as such rather than as
//     whatever the walk failed on next.
//   - Axis reconstruction runs before the drift verify, because the verify
//     compares against the reconstructed set.
//
// pol names every gate difference that survives between surfaces; opts carries
// the pipeline knobs the caller owns (trace, render-skip, progress callbacks).
// ctx is the caller's, already carrying any of its own render knobs; the run
// adds only the external-input seam.
//
// On a gate halt the returned result is non-nil and carries the warnings raised
// up to that point, so a caller reports what was found before it reports the
// failure.
func runPreview(ctx context.Context, in PreviewInputs, pol previewPolicy, opts pipeline.Options) (*PreviewResult, error) {
	out := &PreviewResult{}

	// The half of the constructor guarantee the language cannot carry. A caller
	// that named no seam does not compile; a caller holding the zero value does,
	// and would render with no seam attached — so it is refused here rather than
	// silently producing the divergent bytes the constructor exists to prevent.
	if !in.constructed {
		return out, errPreviewInputsZeroValue
	}

	warn := func(class WarningClass, text string) {
		out.Warnings = append(out.Warnings, Warning{Class: class, Text: text})
	}
	phase := func(p PreviewPhase) {
		if in.phase != nil {
			in.phase(p)
		}
	}

	phase(PhaseClosure)

	// Loud integrity gate, ahead of the closure walk. A pinned cache slot that
	// drifted from its lockfile pin halts here with the integrity diagnostic when
	// no catalog directory holds the pinned bytes either, so a tampered or drifted
	// cache surfaces as the integrity finding rather than a misleading downstream
	// "not resolvable"; where one does, the slot is passed over with a notice.
	if pol.verifyPinnedIntegrity {
		if err := in.view.VerifyPinnedIntegrity(); err != nil {
			out.FailedGate = GatePinnedIntegrity
			return out, fmt.Errorf("lockfile integrity: %w", err)
		}
	}

	// Shared Release to closure orchestration. Augment folds the cross-document
	// library axes so the closure matches the one drift is verified against; it
	// is on whenever this layer owns the reconstruction, and a caller that folded
	// the axes itself no longer exists — the fold has one home now.
	resolved, cerr := ResolveClosure(ctx, ClosureInputs{
		View:            in.view,
		ReleasePath:     in.releasePath,
		WorkspaceRoot:   in.workspaceRoot,
		WorkspaceConfig: in.workspaceConfig,
		Lockfile:        in.lockfile,
		Augment:         true,
	})
	if cerr != nil {
		out.FailedGate = GateClosureResolution
		return out, &resolutionFailure{err: cerr}
	}
	out.Resolved = resolved
	out.ManifestConsistencyFindings = resolved.ManifestConsistencyFindings

	// The readings the Release's own extends-chain fold advised about. They are
	// emitted here, before any gate below can halt, so the advice reaches a reader
	// whether or not the run goes on to fail for some other reason — and they halt
	// nothing themselves, which is the verdict this caller owes them.
	for _, advisory := range resolved.ReleaseCompositionAdvisories {
		code, _ := cerrors.CodeOf(advisory)
		out.Warnings = append(out.Warnings, Warning{Class: WarningReleaseComposition, Text: advisory.Error(), Code: code})
	}

	// The mapping-table verdict. Joined so every inconsistent mapping is
	// reported at once rather than one per re-run.
	if pol.haltOnManifestInconsistency {
		if err := stderrors.Join(resolved.ManifestConsistencyFindings...); err != nil {
			out.FailedGate = GateManifestConsistency
			return out, err
		}
	}

	phase(PhaseValidate)

	// Closure-scoped validation: ONLY the entities the Release reaches, against
	// the closure's libraries.
	if pol.validateClosurePillars {
		if err := in.view.ValidateClosureScopedPillars(ctx, resolved.Closure); err != nil {
			out.FailedGate = GateClosurePillars
			return out, &resolutionFailure{err: err}
		}
	}

	// Reconstruct the axes ComputeClosure leaves empty, hermetically and from the
	// lockfile, so the drift comparison below is like-for-like.
	//
	// The reconstruction is also what resolves the references the Release's modules
	// make, so a run that has no lockfile to reconstruct from resolves them through
	// the resolution a validation runs instead, at the same point. Without it a type
	// a module names and no catalog holds would reach the checks that compile the
	// Release unresolved, and be reported there as a disagreement inside the compiler
	// rather than as the author's reference to nothing; ResolveModuleReferences says
	// why.
	if pol.reconstructClosureAxes && in.lockfile != nil {
		if err := reconstructPreviewClosureAxes(ctx, in, resolved); err != nil {
			out.FailedGate = GateAxisReconstruction
			return out, &resolutionFailure{err: err}
		}
	} else if cerr := ResolveModuleReferences(ctx, resolved); cerr != nil {
		out.FailedGate = GateClosureResolution
		return out, &resolutionFailure{err: cerr}
	}

	// Drift verification against the freshly-computed closure, for a lockfile that
	// was on disk before this run and for one the caller composed and is about to
	// write. A lockfile a fill wrote during this run is not verified again: the fill
	// held it to this same verify, as a composed lockfile, before it wrote it.
	if pol.verifyLockfileDrift && in.lockfile != nil && (in.lockfilePreexisted || in.lockfileComposed) {
		if err := lockfile.Verify(in.lockfile, resolved.Closure); err != nil {
			out.FailedGate = GateLockfileDrift
			if in.lockfileComposed {
				return out, &composedLockfileRefusal{findings: err}
			}
			// The remedy is wrapped as an error of its own rather than formatted into
			// the frame, and that is what puts it in front of a reader at a terminal:
			// the command line renders a chain carrying several errors element by
			// element and shows no frame's own text, so advice written into the frame
			// reaches nobody there. The text of the whole is the same either way.
			return out, fmt.Errorf("%w; %w", err, stderrors.New(LockfileDriftRemedy(in.releasePath)))
		}
	}

	phase(PhaseInheritance)

	// Extends inheritance for the closure's Components and Features, so the
	// pipeline sees fully-merged entities.
	if pol.resolveExtends {
		if extErrs := in.view.ResolveClosureExtends(ctx, resolved.Closure); len(extErrs) > 0 {
			for _, e := range extErrs {
				warn(WarningExtends, e.Error())
			}
			out.FailedGate = GateExtends
			return out, &resolutionFailure{err: fmt.Errorf("catalog extends: %d resolution error(s)", len(extErrs))}
		}
	}

	// Advisories the caller's own catalog load produced, emitted here so the
	// whole advisory sequence has one order.
	for _, w := range in.catalogWarnings {
		warn(WarningCatalog, w)
	}

	// The deprecation gates, over the two stores. Both read only their record —
	// no registry access, preserving the ahead-of-time boundary. Reporting is
	// unconditional; only halting reads the dial, so a stricter threshold never
	// reduces what the caller is told.
	if pol.enforceDeprecation {
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

	// The recorded signature facts, for every reference the build reads — after
	// the axes are reconstructed, so the plugin and workspace-socket references are
	// among them, and after the deprecation gates, so their notices reach a reader
	// whatever this gate decides. It reads the lockfile, the closure, the
	// configuration and the cached bundles only: no registry access, the same
	// ahead-of-time boundary the deprecation gates keep.
	if pol.enforceRecordedSignatures {
		if err := enforceRecordedSignatures(in, resolved.Closure); err != nil {
			out.FailedGate = GateRecordedSignatures
			return out, err
		}
	}

	// Schema-pillar-overlap advisories: the warning-level sibling of the
	// closure-scoped validation above, over the same entity set.
	if pol.reportCatalogLint {
		for _, w := range in.view.ClosureLintWarnings(ctx, resolved.Closure) {
			warn(WarningCatalogLint, w)
		}
	}

	phase(PhaseRender)

	// The render. The external-input seam is installed here and nowhere else, so
	// a surface cannot acquire the pipeline without it and render different bytes
	// than the surface beside it.
	if in.externalInputs != nil {
		ctx = wasm.ContextWithExternalInputs(ctx, in.externalInputs)
	}
	if pol.verifyRecipeResolution {
		opts.RecipeResolutionVerifier = in.view.VerifyRecipeResolution
	}
	outcome, err := BuildToBuffer(ctx, BuildInputs{
		View:                             in.view,
		Resolved:                         resolved,
		ReleasePath:                      in.releasePath,
		WorkspaceConfig:                  in.workspaceConfig,
		WorkspaceRoot:                    in.workspaceRoot,
		Lockfile:                         in.lockfile,
		CompilerVersion:                  in.compilerVersion,
		TolerateCatalogViewInconsistency: pol.tolerateCatalogViewInconsistency,
		composedTrust:                    in.composedTrust,
		resolutionAdvisoriesShown:        in.resolutionAdvisoriesShown,
		display:                          in.display,
	}, opts)
	// Assigned BEFORE the verdict is read, and that ordering is the whole point
	// rather than a tidying. Assigned after the error check, the outcome would
	// reach a caller only on the path where it needs it least — a run that
	// finished cleanly — and the run that stopped, whose findings a reader has to
	// act on, would arrive with nothing to read. The outcome carries its own
	// verdict, so handing it over on both paths tells a caller no less than the
	// error does.
	out.pipeline = outcome
	if err != nil {
		out.FailedGate = GateRender
		out.renderFailure = err
		out.workspaceRoot = in.workspaceRoot
		return out, err
	}
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

	// The one report over both stores. A plugin attached at a verdict role shapes
	// no rendered byte, so it is recorded beside the lockfile rather than pinned in
	// it; without the record's half, a withdrawn gate plugin is fetched, cached and
	// run with nothing said. On a record that cannot be read the report still hands
	// back the lockfile's findings, and they are reported before the read failure is
	// returned, so a reader is told what could be read.
	findings, readErr := ReleaseDeprecationReport(in.lockfile, in.releasePath)
	halt = DeprecationVerdict(findings, in.workspaceConfig.Build, report)
	if readErr != nil {
		return nil, readErr
	}
	return halt, nil
}

// reconstructPreviewClosureAxes rebuilds the sub-aggregate and pipeline-plugin
// axes ComputeClosure leaves empty, plus the output-shaping workspace-socket
// entities, from the LOCKFILE rather than the workspace config — so a
// plugins.providers change made after the pull does not move the reconstructed
// closure and the drift comparison stays honest.
//
// The first two axes are descended by the same computation the pull ran to pin
// them (closure.DescendPinnedAxes), handed the build's side of each difference: the
// resolution's hermetic lookup, a plugin lookup carrying the lockfile's pins, the
// lockfile's recorded provider bindings, and a re-fold over this view.
//
// Reconstruction is hermetic: the closure Lookup carries a nil puller, so no
// registry fetch fires. The re-fold shifts the view's pin set to the augmented
// closure to make the just-pinned plugin entities visible, and the pins are
// restored to the lockfile view afterward so the render resolves exactly as it
// would have without the reconstruction.
func reconstructPreviewClosureAxes(ctx context.Context, in PreviewInputs, resolved *ResolvedClosure) error {
	view := in.view
	lookup := resolved.Lookup

	aggLoader, err := closure.RequireAggregateLoader(lookup)
	if err != nil {
		return err
	}
	recipeLoader, err := closure.RequireRecipeLoader(lookup)
	if err != nil {
		return err
	}
	bindings := lockfile.ProviderBindings(in.lockfile)
	resolvePlugin := func(ref string) (types.Plugin, bool, error) {
		return view.ProviderPluginForTarget(ref, bindings)
	}
	refold := func() (closure.RecipeLoader, error) {
		view.SetPins(catalog.PinsFromClosure(resolved.Closure))
		return view.ClosureRecipeLoader(), nil
	}
	// The plugin passes resolve through a lookup carrying the lockfile's pins. A
	// pinned plugin .wasm sits in a cache slot addressed by the content digest its
	// pin names, so a lookup without pins cannot reach it and the artifact would
	// resolve as absent — reported as drift against a lockfile that pins it. The
	// sub-aggregate pass keeps the resolved lookup, which carries the Release's
	// external documents that pass reads. One pinned lookup serves every plugin
	// pass, the workspace-socket one below included.
	pinned := view.ClosureLookup(nil, in.workspaceRoot, in.workspaceConfig.ResolvedArtifactCacheDir(), nil, nil,
		&catalog.ReuseVerification{PriorPins: catalog.PinsFromLockfile(in.lockfile)})
	// The artifact of a Plugin entity authored in a catalog directory is resolved from
	// this machine by the same question the pull asked when it wrote the lockfile, so
	// a transform-role attachment authored there reconstructs to the pin the pull
	// recorded from the same bytes. The artifact pass of the descent and the
	// workspace-socket pass below ask it. The entity pass asks nothing: a build loads
	// each plugin through the cascade that raises the same condition where it loads
	// it, so it refuses nothing at that step that its render would not.
	catalogArtifacts := pipeline.CatalogPluginArtifacts(in.workspaceRoot, in.workspaceConfig, view.PluginColocatedDirs())
	if err := closure.DescendPinnedAxes(ctx, &resolved.Closure, aggLoader, recipeLoader, lookup, pinned, resolvePlugin, refold, nil, catalogArtifacts); err != nil {
		view.SetPins(catalog.PinsFromLockfile(in.lockfile))
		return err
	}
	view.SetPins(catalog.PinsFromLockfile(in.lockfile))

	// The output-shaping workspace-socket plugin entities and their .wasm are
	// pull-time choices recorded only in the lockfile, so ComputeClosure cannot
	// re-derive them hermetically. They resolve through the same pinned lookup, so
	// a pinned socket .wasm resolves from the content-addressed cache offline; the
	// entity and artifact are re-read so a drifted pin still surfaces as drift.
	socketEntityURIs := lockfile.WorkspaceSocketEntityURIs(in.lockfile)
	if len(socketEntityURIs) == 0 {
		return nil
	}
	socketRecipeLoader, err := closure.RequireRecipeLoader(pinned)
	if err != nil {
		return err
	}
	return closure.ReconstructWorkspaceSocketEntities(ctx, &resolved.Closure, socketEntityURIs, socketRecipeLoader, pinned, catalogArtifacts)
}
