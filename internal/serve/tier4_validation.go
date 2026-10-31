// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// runTier4Validation runs Steps 1-4 of the compiler pipeline against the
// Release the request named and maps per-phase violations into
// source-positioned diagnostics for the requested file.
//
// releasePath is the request's own Release, resolved at the handler
// boundary, and it is a parameter rather than daemon state so two
// requests naming two Releases are answered about the Release each one
// named. That is what makes a validation and a graph query about the same
// Release describe the same thing, and it is why nothing here consults a
// stored selection.
//
// Position-mapping fidelity:
//
//	Tier A — clean sub-file: scalar position recovered from the
//	         parsed yaml.Node for resolver-class violations whose
//	         Field locator names a path the daemon can walk
//	         (parameter type mismatch, missing required parameter,
//	         dangling extends reference). Uses the parsed yaml.Node
//	         to find the matching key; returns its Line/Column.
//	Tier B — best-effort sub-file: selector and appliesTo violations
//	         carry a Field locator naming the uses-socket or feature-
//	         instance key; the diagnostic spans that key. Falls back to
//	         the document's kind: key (keyPathSpan's fall-back) when the
//	         locator names a path the walker cannot reach
//	         (subsystem-nested components, sequence-indexed keys).
//	Tier C — file-level only: renderer / file-context errors;
//	         fall back to {1, 1} with a "<filename>: " prefix.
//	         Distinguished from Tier D in a future iteration.
//	Tier D — closure-level: catalog load failures, lockfile drift,
//	         cross-file IR assembly errors; fall back to {1, 1}
//	         with the "Dependencies: " prefix.
//
// A catalog entity of a kind the single-entity check accepts never reaches
// this function: handleValidate answers it with that check in the Release's
// terms (runEntityValidation). When the request file is the named Release
// itself, the pipeline runs as it does for the Aggregate and the answer is the
// findings positioned in the Release — a finding about what the Release wrote,
// whether the composition or the graph raised it, lands on the Release, on the
// key that wrote it, and on the Aggregate not at all (see
// ownDocumentDiagnostics and filedIn). A request on another Aggregate document is
// answered the same way, with the findings filed under it — a module the named
// Release's Aggregate imports carries what the composition found in what it
// declares. When the request file is any other document (e.g., the Manifest or
// a library), the Tier-4 pipeline is not relevant to the requested file and the
// function returns an empty slice once the closure has resolved — the caller's
// existing Tier-1 diagnostics for the file already populated stay in place.
//
// A closure that does not resolve is reported at the head of the named Release's
// own document and of every document the daemon reads as one of Aggrega's own —
// the Aggregate, the Manifest, a library, another Release, the workspace
// configuration — and on no other: YAML the daemon reads as no kind of its own
// (classifyBuffer) is answered with an empty slice whether the closure resolves or
// not. On every document but the Release's own, the entry names the Release the
// document was checked against (dependencyFailureMessage).
func (s *Server) runTier4Validation(ctx context.Context, releasePath, file string, content []byte, doc *yaml.Node) []Diagnostic {
	// The validated file and the named Release's own document, in the
	// workspace-relative form every comparison below is made in. Both are read off
	// the request rather than off the resolution, so the failure returns below can
	// ask which of them they are answering before anything has resolved.
	// catalog.RelativeToWorkspace is the single source of truth for the conversion,
	// and the pipeline names the Release by this same rule — it renders this
	// request's Release path against this daemon's workspace root — so a finding
	// about what the Release wrote carries exactly relRelease as its file.
	//
	// onReleaseDocument is the one statement of whether this request is answering
	// the named Release's own document. Every branch below that treats that document
	// apart from the rest reads it, so no two of them can disagree about which
	// document it is.
	relFile := catalog.RelativeToWorkspace(s.workspaceRoot, file)
	relRelease := catalog.RelativeToWorkspace(s.workspaceRoot, releasePath)
	onReleaseDocument := relFile == relRelease

	// Build the per-request catalog view, seeded with the named Release's
	// lockfile pins, so every Tier-4 resolution read below verifies-on-read
	// against THIS Release's pins and a concurrent request resolving a different
	// Release's pins can never perturb this validation. The pins are loaded once
	// here and the same view threads through closure compute, closure-scoped
	// extends + Pass-2, the Tier-4 pipeline, and resolution findings, so the
	// whole validation sees one consistent pin snapshot. A failed/absent/invalid
	// lockfile yields nil pins, disabling cache trust for the request, never an
	// inherited cross-request pin set.
	//
	// This is a QUIET load — pins only, no drift verify and no diagnostic
	// publication. Lockfile drift is surfaced by the graph request, whose
	// notification persists in the client; re-publishing here (validate fires per
	// edit) would re-send an un-deduplicated drift notice at editing cadence
	// without adding a needed signal.
	//
	// ORDERING IS LOAD-BEARING: the view's pins are set (via ForRequest) before
	// any Tier-2 read fires. An unpinned view uses no cached slot at all, so
	// validation would resolve nothing from the cache and report the silence as
	// though the workspace said it — guarded by an end-to-end test
	// (TestServeValidateVerifiesCacheAgainstPinBeforeTier4).
	//
	// The loaded lockfile is retained past the pin extraction because the
	// deprecation report below reads its recorded markers. Retaining it does NOT
	// change what is passed to the closure resolution: that still receives nil, so
	// the quiet-load posture described above is untouched.
	//
	// A lockfile that is a link leading out of the root a write of it is held to is
	// not absent but refused, and the validation reports the refusal as it reports
	// a closure that does not resolve — the pins the Release records are not read
	// from elsewhere, and are not silently done without.
	var pins map[string]catalog.Pin
	lf, lfErr := lockfile.LoadIfReadable(s.workspaceRoot, lockfile.PathFor(releasePath))
	if lfErr != nil {
		if _, ours := classifyBuffer(file, releasePath, doc); !ours && !onReleaseDocument {
			return nil
		}
		return []Diagnostic{{
			Line:           1,
			Column:         1,
			Severity:       editorSeverityError,
			Message:        s.dependencyFailureMessage(releasePath, onReleaseDocument, lfErr.Error()),
			ConditionCodes: conditionCodesOf(lfErr),
		}}
	}
	if lf != nil {
		pins = catalog.PinsFromLockfile(lf)
	}
	// Forked from the lookup over the named Release's own catalog stack, so the
	// validation reads the directories a build of that Release reads.
	view := s.catalogFor(releasePath).ForRequest(pins)

	// The deprecation report over both stores — the lockfile's recorded markers and
	// the plugin trust record beside the Release — the same report the build reads,
	// so the editor names the same references and plugins the build would. Which
	// store holds a marker is decided by the attachment's role, so reading one store
	// alone would leave a withdrawn gate plugin unmentioned here.
	//
	// It is computed HERE, before closure resolution, so it is carried through the
	// closure-level failure return as well: a recorded marker is a fact about the
	// store that recorded it, so the author still learns something is withdrawn on
	// an edit where the closure does not resolve at all — where it may well be the
	// explanation. That is more than the mapping findings below can offer, which are
	// decided BY the resolution.
	//
	// Past that point it is bounded exactly as every other closure-level class is:
	// this function returns nothing once the requested file is not the named
	// Release's Aggregate, so the notice reaches the author while the Aggregate is
	// open rather than on whatever document they happen to be editing.
	//
	// The shape is closureAdvisoryDiagnostic's, shared with the other advisory
	// classes below. One reason is this class's alone: the marker's own severity is
	// named in the message text, so mapping a recorded severity onto the editor's
	// error tier would put a policy stance in the editor that the product
	// deliberately leaves to a build-time dial.
	//
	// A trust record that is there and cannot be read is reported in place of the
	// validation, in the shape the lockfile refusal above takes: it must not pass as
	// an empty record, which would report no withdrawn plugin where the daemon
	// cannot tell.
	deprecations, trustErr := compilerops.ReleaseDeprecationReport(lf, releasePath)
	if trustErr != nil {
		if _, ours := classifyBuffer(file, releasePath, doc); !ours && !onReleaseDocument {
			return nil
		}
		return []Diagnostic{{
			Line:           1,
			Column:         1,
			Severity:       editorSeverityError,
			Message:        s.dependencyFailureMessage(releasePath, onReleaseDocument, trustErr.Error()),
			ConditionCodes: conditionCodesOf(trustErr),
		}}
	}
	var deprecationDiags []Diagnostic
	for _, f := range deprecations {
		deprecationDiags = append(deprecationDiags, closureAdvisoryDiagnostic(f.Notice))
	}

	// Quiet load — pins only, no drift verify, and no lockfile reaches the
	// resolution. Drift is surfaced by the graph request, which reconstructs the
	// closure axes and verifies against them.
	resolved, envelope := s.resolveReleaseClosure(ctx, view, releasePath)
	if envelope != nil {
		// A document the daemon reads as no kind of its own (classifyBuffer) — a
		// continuous-integration workflow, a Compose file — is about nothing this
		// Release depends on, so the failure is not published on it, and neither are
		// the deprecation notices riding with it: placed there, either would
		// attribute the Release's fault to a file that has none. The Release's own
		// document is the exception and keeps both whatever its buffer holds, since
		// it is the document the request named, and one whose header no longer
		// classifies mid-edit is still the place its dependencies' failure belongs.
		if _, ours := classifyBuffer(file, releasePath, doc); !ours && !onReleaseDocument {
			return nil
		}
		// Closure-level failure (catalog load, Release/Manifest load,
		// Aggregate read) — Tier-D fall-back at file:1:1. The code comes off
		// the envelope's cause rather than off its Message: the message is
		// rendered text, and the condition the failure names is only reachable
		// through the failure itself. The deprecation notices ride along because
		// they were decided before this resolution ran and a withdrawn pin can be
		// why it failed.
		return append(deprecationDiags, Diagnostic{
			Line:           1,
			Column:         1,
			Severity:       editorSeverityError,
			Message:        s.dependencyFailureMessage(releasePath, onReleaseDocument, envelope.Message),
			ConditionCodes: conditionCodesOf(envelope.cause),
		})
	}

	// The root aggregate's path (passed via Inputs.AggregatePath) is rendered in
	// the same workspace-relative coordinate system as relFile and relRelease
	// above, the one the catalog accessors produce for sub-aggregate paths, so
	// per-node SourceFile and the per-file comparison sit in one form.
	aggPath := resolved.AggregatePath
	relAggPath := catalog.RelativeToWorkspace(s.workspaceRoot, aggPath)

	// The Tier-4 branch answers two documents: the named Release's Aggregate,
	// which carries everything this validation reports bar what is positioned in
	// the Release, and the named Release itself, which carries what is.
	// Validating any other file against that Release produces no semantic
	// diagnostics for THAT file from this pipeline.
	//
	// The paths are compared in that one coordinate system rather than as the
	// strings they arrived as. The resolved Aggregate's path, the Release's path
	// and the validated file reach this function from different places — the
	// closure resolution, the request's Release, the request's file — so comparing
	// the raw strings asks whether they agree on a spelling when the question is
	// whether they name the same document. Deciding it on the spelling withholds
	// every finding below from a request that named this very file the other way,
	// and withholds them as an empty result, which reads as a file with nothing
	// wrong rather than as a comparison that failed.
	onAggregate := relFile == relAggPath
	// A third document carries findings of its own: an Aggregate the named
	// Release's Aggregate imports as a module. A finding about what a module
	// declares is a path inside the module's document, so it is answered when
	// that document is the one validated, placed on the key that wrote it — the
	// answer the named Release's own document gets for what it wrote. Whether the
	// buffer IS such a module is known only once the composition has read its
	// modules, so the pipeline runs for any Aggregate buffer, and the answer is
	// the findings filed under it, which for an Aggregate the composition never
	// imported is none.
	onModuleDocument := !onAggregate && !onReleaseDocument && isAggregateBuffer(file, releasePath, doc)
	if !onAggregate && !onReleaseDocument && !onModuleDocument {
		return nil
	}

	// Manifest mapping-table findings, decided by the closure resolution above and
	// therefore ahead of every gate below. They are a Tier-D class: the fault is in
	// the Manifest, not in the edited Aggregate, so it carries no position in this
	// file and sits at 1:1 behind the "Dependencies: " prefix, the same footing the
	// resolution-completeness findings use.
	//
	// The daemon ADVISES rather than halts — it publishes them and keeps going —
	// and every early return below carries them forward. That is the whole reason
	// the resolver hands these back as findings instead of failing: a failure at
	// that position replaces this file's entire diagnostic set with one line, at
	// every keystroke, so an editor would lose every other thing it had to say
	// about the buffer in exchange for one Manifest complaint.
	// The deprecation notices lead every set below, matching the order the facts
	// were decided in: both stores recorded their markers at the pull, before the
	// Manifest was judged and before any gate here read anything.
	//
	// Both are the Aggregate's to carry. A request on the Release's own document is
	// answered with what is positioned in it alone, below, so neither class is
	// published twice.
	mappingDiags := deprecationDiags
	for _, f := range resolved.ManifestConsistencyFindings {
		mappingDiags = append(mappingDiags, Diagnostic{
			Line:           1,
			Column:         1,
			Severity:       editorSeverityError,
			Message:        "Dependencies: " + f.Error(),
			ConditionCodes: conditionCodesOf(f),
		})
	}

	// Run the semantic-validation core through the shared compiler-operations
	// layer (closure-scoped extends gate, closure-scoped schema-pillar gate,
	// Steps 1-4 pipeline, resolution-completeness findings). The gates read
	// through the pinned request view so every resolution verifies-on-read
	// against this Release's pins. The daemon owns the diagnostic MAPPING below;
	// the shared layer returns the raw outcome in gate order.
	// No lockfile is supplied, and the omission is deliberate rather than an
	// oversight — it is the same quiet-load posture the pin extraction above
	// describes, carried through to the pipeline. This runs on every edit, and the
	// two things a supplied record would add are both things this surface must not
	// do at that cadence: hold the dispatched guests to their recorded digests,
	// which republishes on every keystroke a condition the graph handler already
	// reports once; and reconstruct the transform attachments
	// from the record, which would swap the attachment set an author is editing for
	// the one recorded at the last resolution, so the editor would stop reflecting
	// the workspace configuration in front of them.
	//
	// Leave the field unset.
	vr := compilerops.Validate(ctx, compilerops.ValidateInputs{
		View:            view,
		Closure:         resolved.Closure,
		Release:         resolved.Release,
		ReleasePath:     releasePath,
		Manifest:        resolved.Manifest,
		AggBytes:        resolved.AggBytes,
		AggregatePath:   relAggPath,
		WorkspaceRoot:   s.workspaceRoot,
		WorkspaceConfig: s.wsConfig,
		CompilerVersion: s.compilerVersion,
	})

	// The request is on the Release's own document, or on a module's: what is
	// positioned there, and nothing else.
	if !onAggregate {
		return s.ownDocumentDiagnostics(vr.RunErr, vr.Tier4, relFile, content, doc)
	}

	// Closure-scoped extends — early-warning extends gate. A dangling parent (or
	// a cycle / malformed chain) on an in-closure Component/Feature surfaces as a
	// Tier-D closure-level diagnostic; an out-of-closure broken extends is never
	// resolved.
	if len(vr.ClosureExtendsErrors) > 0 {
		return append(mappingDiags, Diagnostic{
			Line:           1,
			Column:         1,
			Severity:       editorSeverityError,
			Message:        fmt.Sprintf("Dependencies: catalog extends: %v", vr.ClosureExtendsErrors[0]),
			ConditionCodes: conditionCodesOf(vr.ClosureExtendsErrors[0]),
		})
	}

	// Closure-scoped Pass-2 — early-warning schema-pillar gate. An in-closure
	// schema-invalid Component/Feature (or a $ref to a skipped/missing library)
	// surfaces as a Tier-D closure-level diagnostic, with the skip context
	// explaining a missing library. build remains the authoritative validator.
	if vr.PillarError != nil {
		return append(mappingDiags, Diagnostic{
			Line:           1,
			Column:         1,
			Severity:       editorSeverityError,
			Message:        "Dependencies: " + s.skipContext(view, vr.PillarError.Error()),
			ConditionCodes: conditionCodesOf(vr.PillarError),
		})
	}

	// The verdict-plugin trust gate — the workspace configuration disagreed with
	// the record of the plugin identities trusted for its verdict attachments, so
	// no gate ran and nothing past this point was validated. It is a fault in the
	// configuration or in the record beside the Release rather than in the edited
	// file, so it surfaces as a closure-level diagnostic at file:1:1, the same
	// footing the two gates above use.
	if vr.VerdictTrustError != nil {
		return append(mappingDiags, Diagnostic{
			Line:           1,
			Column:         1,
			Severity:       editorSeverityError,
			Message:        "Dependencies: " + vr.VerdictTrustError.Error(),
			ConditionCodes: conditionCodesOf(vr.VerdictTrustError),
		})
	}

	// Cancellation (shutdown drain) surfaces as a context error from the
	// pipeline. Return no diagnostics rather than a spurious closure-level
	// entry; the caller drops the response.
	if errors.Is(vr.RunErr, context.Canceled) || errors.Is(vr.RunErr, context.DeadlineExceeded) {
		return nil
	}

	// Source-position recovery walks the parsed document and computes
	// scalar end positions in the validated file's coordinate space.
	idx := yamlpos.BuildLineIndexWithEncoding(content, s.positionEncoding())

	// The mapping findings lead the published set, matching the order they were
	// decided in: the Manifest was judged before the pipeline ran.
	diags := mappingDiags
	tier4 := vr.Tier4

	// Resolver-class violations (Step 1) — Tier-A position recovery
	// from the structured ResolveDiagnostics slice. The slice is
	// populated uniformly for both single- and multi-violation cases.
	var perViolation []*resolver.ResolverError
	if tier4 != nil {
		perViolation = tier4.ResolveDiagnostics
	}
	for _, ve := range perViolation {
		// A finding about what the Release wrote is the Release document's to
		// carry, and it is published there; the full slice still answers the
		// phase-error question below, because it was published, only elsewhere.
		// A finding about what a module declares is the module document's, for
		// the same reason: its locator is a path inside that document, and placed
		// here it would land on whichever key of this one shares its spelling.
		if filedIn(ve.File, relRelease) || ve.InAnotherDeveloperDocument(relAggPath) {
			continue
		}
		diags = append(diags, resolverFindingToDiagnostic(ve, editorSeverityError, doc, idx))
	}

	// Non-resolver pre-analyzer phase error (topology / IR / guards
	// without per-violation structure). Tier-D fall-back at file:1:1.
	//
	// Whether it is owed is asked of the run error itself and never of whether
	// the slice above was empty. That slice also carries the findings a rule
	// raises for an emitting run alone, which reach a surface like this one on a
	// run whose failure is elsewhere and on a run that did not fail at all — so
	// reading its length would take such a finding as evidence about the phase
	// error and publish nothing where the author has no other way to see it.
	if vr.RunErr != nil && !runErrPublishedPerViolation(vr.RunErr, perViolation) {
		diags = append(diags, Diagnostic{
			Line:           1,
			Column:         1,
			Severity:       editorSeverityError,
			Message:        tier4PhaseErrorMessage(vr.RunErr),
			ConditionCodes: conditionCodesOf(vr.RunErr),
		})
	}

	// Advisory findings — exit-code-neutral, so they never contribute to the
	// verdict. They arrive on the compiler's one advisory channel, carrying their
	// own severity, and ride the same position recovery the halting violations
	// above do: an advisory that named an authoring key squiggles that key rather
	// than the key that names the whole document.
	//
	// The severity is the ADVISORY's, not this call site's. A halting violation is
	// published at the error tier by the surface that knows it halted; an advisory
	// carries the tier its producer chose, so a warn and an info stay
	// distinguishable in the editor instead of being flattened to one tier here.
	if tier4 != nil {
		for _, a := range tier4.Advisories {
			diags = append(diags, advisoryToDiagnostic(a, doc, idx))
		}
	}

	if tier4 != nil && tier4.Analysis != nil {
		diags = append(diags, analyzerErrorsToDiagnostics(tier4.Analysis.Errors, relFile, doc, idx)...)
	}

	// Resolution-completeness diagnostics — the de-fallback regression guard.
	// Resolution is mapping-only; an unmapped instance no longer silently
	// resolves by spec.implements. serve keeps its resilient no-halt posture
	// but surfaces each unmapped instance as a diagnostic carrying the
	// implementing-recipe pin candidates, rather than a silent miss. This runs
	// the SAME shared producer the CLI build joins into a halt, over the
	// complete enumerated recipe set, so the editor's candidate suggestions
	// match the build's exactly.
	for _, ferr := range vr.ResolutionFindings {
		diags = append(diags, Diagnostic{
			Line:           1,
			Column:         1,
			Severity:       editorSeverityError,
			Message:        "Dependencies: " + ferr.Error(),
			ConditionCodes: conditionCodesOf(ferr),
		})
	}

	// Input-kind capability errors — computed after the pipeline, this class
	// keeps serve's verdict inclusive of an input-kind failure so the daemon
	// reaches the same verdict as the other front ends.
	for _, e := range vr.InputKindErrors {
		diags = append(diags, Diagnostic{
			Line:           1,
			Column:         1,
			Severity:       editorSeverityError,
			Message:        "Dependencies: input-kind: " + e.Error(),
			ConditionCodes: conditionCodesOf(e),
		})
	}

	// The closure-scoped advisory lines: the schema-pillar overlap advisories for
	// the closure's Components and Features, then the narrowing-on-extend
	// advisories accumulated while composing its extends chains. Both are
	// exit-code-neutral, so neither contributes to a verdict.
	//
	// They are closure-level rather than positioned, and that is a fact about the
	// findings rather than a limitation here: each names a catalog entity by URI,
	// which is the only locator a registry-pinned entity has, so there is no path
	// into the edited document for a walk to recover. Every other class naming an
	// entity instead of a position sits on the same footing.
	//
	// The order is the shared validation core's own, where the two accessors are
	// read together after the input-kind check. Taking it from there rather than
	// choosing one here is what keeps the daemon from becoming a second opinion
	// about which advisory was decided first.
	for _, w := range vr.LintWarnings {
		diags = append(diags, closureAdvisoryDiagnostic(w))
	}
	for _, w := range vr.NarrowingWarnings {
		diags = append(diags, closureAdvisoryDiagnostic(w))
	}

	return diags
}

// closureAdvisoryDiagnostic shapes one exit-code-neutral, closure-level advisory
// into the entry the editor publishes.
//
// Every class reaching it names a catalog entity rather than a position in the
// edited document — a withdrawn pin, a Component declaring one property name in
// two pillars, a child replacing a constraint it inherited — so none has a
// position to lose, and all sit at the head of the file behind the same prefix the
// other entity-scoped classes use. The text is the producer's own, carried
// verbatim, so an advisory reads the same here as it does on a terminal.
//
// They publish at the warning tier and never as errors. The daemon reaches no
// verdict for them to contribute to, and an advisory published as an error would
// have the editor assert the buffer is broken over authoring the compiler
// deliberately permits.
//
// It is one function rather than the same literal written out per class so that a
// class added later cannot acquire a different position, prefix or tier simply by
// being written again.
func closureAdvisoryDiagnostic(text string) Diagnostic {
	return Diagnostic{
		Line:     1,
		Column:   1,
		Severity: editorSeverityWarning,
		Message:  "Dependencies: " + text,
	}
}

// dependencyFailureMessage is the text of the entry a failure to resolve the named
// Release's dependencies is published as, given account, the resolution's own
// words for what stopped it.
//
// On the Release's own document the entry is the account behind the
// "Dependencies: " prefix every closure-level entry carries. Every other document
// carrying it — an Aggregate or a Manifest several Releases may share, a library,
// another Release, the workspace configuration — carries it only because the
// request checked that document against this Release, and nothing in the document
// says which Release that was. So there the entry says so and names the Release,
// and a reader can tell a Release that does not resolve from a fault of the
// document in front of them. Which document is the Release's own is decided by
// the caller's one comparison, passed in as onReleaseDocument, rather than here.
//
// The Release is spelled as every answer spells a document it names
// (pathscope.ReportedName), so a Release inside the workspace is named relative to
// its root, the form a request names it in.
//
// Both closure-level failure returns of runTier4Validation compose their entry
// here, so the two cannot come to word one fact two ways.
func (s *Server) dependencyFailureMessage(releasePath string, onReleaseDocument bool, account string) string {
	if onReleaseDocument {
		return "Dependencies: " + account
	}
	return "Dependencies: this document is checked against the Release " +
		pathscope.ReportedName(s.workspaceRoot, releasePath) +
		", whose dependencies cannot be resolved: " + account
}

// analyzerErrorsToDiagnostics maps the analyzer's structured
// violation slice to Diagnostics for the requested file. Selector and
// appliesTo violations (Step 4) carry a Field locator naming the
// authoring site; this helper walks it to a sub-file position and
// spans the offending key (Tier-B fidelity). When the Field is empty
// or walks to a path the document does not contain, the diagnostic
// falls back to file:1:1 with the "Dependencies: " prefix.
//
// Cross-file filter: each AnalysisError carries an OriginFile
// field naming the source file where the violating authoring
// lives. The catalog accessors that supplied those paths render
// them in whatever form the catalog was constructed with — workspace-
// relative when Load/LoadAll received a non-empty workspace root (the
// production daemon); absolute when no workspace root was wired.
// Callers convert the validated file to the same form before
// invocation; this helper compares string-for-string and drops
// entries whose OriginFile points to a different file from the
// validated one. Per-file diagnostic collection means cross-file
// errors surface when the user opens the offending file. The Field
// walk therefore runs against the requested file's document, which is
// the origin file for every entry that survives the filter.
//
// Mutates the input slice in place to backfill OriginFile when
// empty. Callers that want to preserve the input shape should
// copy before invoking.
func analyzerErrorsToDiagnostics(errs []analyzer.AnalysisError, file string, doc *yaml.Node, idx yamlpos.LineIndex) []Diagnostic {
	var out []Diagnostic
	for i := range errs {
		if errs[i].OriginFile == "" {
			errs[i].OriginFile = file
		}
		if errs[i].OriginFile != file {
			continue
		}
		line, col, endLine, endCol, located := keyPathSpan(doc, idx, errs[i].Field)
		out = append(out, Diagnostic{
			Line:      line,
			Column:    col,
			EndLine:   endLine,
			EndColumn: endCol,
			unplaced:  !located,
			Severity:  editorSeverityError,
			// The code comes off Sentinel rather than off the entry: an
			// AnalysisError reports its own Message and does not unwrap to the
			// condition it names, so a chain walk over the entry itself finds
			// nothing to report.
			Message:        "Dependencies: " + errs[i].Error(),
			ConditionCodes: conditionCodesOf(errs[i].Sentinel),
		})
	}
	return out
}

// ownDocumentDiagnostics answers a validation of a document other than the named
// Release's Aggregate — the named Release's own, or a module the Aggregate
// imports: the findings the validation positioned in it, each placed on the
// authored key its locator names, and nothing else.
//
// A rule over what the Release wrote — an entry it writes on a connection, an
// attachment it fills there, a connection it draws — names the Release's file,
// so the Release document is the one an author opens to act on it, and it is
// published there. A rule over what a module declares — a component's
// attachments, its values, the values on its own connections — names the
// module's file, for the same reason, and is published on the module. That
// holds for a finding the composition raised before any graph existed as for
// one the graph's rules raised, and both are read through [filedIn], the one
// statement of which document a finding belongs to. Every other finding this
// validation produces stays on the Aggregate's answer, a finding naming no file
// among them: the filters below keep only what names this file and adopt
// nothing, so no finding is shown on two documents.
//
// The composition's findings lead, in the order they were raised, followed by
// the graph's: the order the two classes were decided in, which is the order the
// Aggregate's answer publishes them in too.
//
// A cancelled run publishes nothing, exactly as on the Aggregate. What stopped a
// run before any of these findings could be raised — a gate over the
// dependencies, say — names no file of this document's and is published on the
// Aggregate.
//
// It is handed the two parts of the validation outcome it reads — the pipeline's
// run error, which says whether the run was cancelled, and its result, which
// holds the findings — rather than the outcome whole, so every part of that
// outcome the daemon reads is named in the function that called the validation.
func (s *Server) ownDocumentDiagnostics(runErr error, tier4 *pipeline.Tier4Result, file string, content []byte, doc *yaml.Node) []Diagnostic {
	if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
		return nil
	}
	if tier4 == nil {
		return nil
	}
	var composition []*resolver.ResolverError
	for _, ve := range tier4.ResolveDiagnostics {
		if filedIn(ve.File, file) {
			composition = append(composition, ve)
		}
	}
	var placed []analyzer.AnalysisError
	if tier4.Analysis != nil {
		for _, e := range tier4.Analysis.Errors {
			if filedIn(e.OriginFile, file) {
				placed = append(placed, e)
			}
		}
	}
	if len(composition) == 0 && len(placed) == 0 {
		return nil
	}
	idx := yamlpos.BuildLineIndexWithEncoding(content, s.positionEncoding())
	var out []Diagnostic
	for _, ve := range composition {
		out = append(out, resolverFindingToDiagnostic(ve, editorSeverityError, doc, idx))
	}
	return append(out, analyzerErrorsToDiagnostics(placed, file, doc, idx)...)
}

// filedIn reports whether a finding is about what the given document wrote:
// whether the file its locator is a path inside — the one its own subject
// recorded — is that document, in the workspace-relative form the compilation
// names its documents by.
//
// It is the one rule deciding which document a validation answers a finding
// belongs to. A finding it answers yes for, for the named Release or for a module
// the Aggregate imports, is published on that document, on the key that wrote it,
// and on the Aggregate not at all; every other finding is the Aggregate's to
// carry. A finding recording no file is never another document's: that absence
// says nothing about which document holds the path, and publishing it there would
// assert a place no rule positioned it at.
func filedIn(file, document string) bool {
	return file != "" && file == document
}

// isAggregateBuffer reports whether the validated buffer is an Aggregate
// document, by the classification every validated document gets.
func isAggregateBuffer(file, releasePath string, doc *yaml.Node) bool {
	kind, ours := classifyBuffer(file, releasePath, doc)
	return ours && kind == envelope.KindAggregate
}

// resolverFindingToDiagnostic maps a resolver-class finding to a
// source-positioned diagnostic at the supplied severity. The Field locator
// (e.g., ".spec.parameters.foo" or ".spec.components.bar.extends") is walked
// against the parsed yaml.Node to find the matching key; when found,
// the diagnostic carries the key's start and end position so the
// editor renders a squiggle spanning the whole token. When the Field
// walks to a path the document does not contain (orthogonal to the
// authoring shape), or when the Field is empty, the diagnostic takes
// keyPathSpan's fall-back: the document's kind: key, else its
// metadata.name, else the head of the file.
//
// Severity is the caller's, not the finding's: halting violations and
// exit-code-neutral advisories share this one position-recovery path, so an
// advisory lands on its authoring site with the same fidelity a violation gets
// rather than acquiring a second walker that drifts from this one.
//
// The text is the finding's own subject-free rendering rather than the raw field
// it carries, so the diagnostic opens with the condition's registered sentence
// exactly as the terminal's does. Reading the field instead would hand the editor
// whatever that occurrence happened to supply — for a rule reporting through the
// constraint framework, its detail values with no sentence at all — while the
// same failure on a terminal named the condition.
func resolverFindingToDiagnostic(ve *resolver.ResolverError, severity string, doc *yaml.Node, idx yamlpos.LineIndex) Diagnostic {
	line, col, endLine, endCol, located := keyPathSpan(doc, idx, ve.Field)
	return Diagnostic{
		Line:           line,
		Column:         col,
		EndLine:        endLine,
		EndColumn:      endCol,
		Severity:       severity,
		Message:        ve.MessageWithoutSubject(),
		ConditionCodes: conditionCodesOf(ve),
		unplaced:       !located,
	}
}

// The field-path-to-position walk lives at yamlpos.FieldNode /
// yamlpos.FieldRange rather than here, because the envelope layer needs the
// same walk and must not depend on the daemon: with one implementation, a
// violation reported to an editor and the same violation reported on a
// terminal cannot point at different keys. Do not add a local walker — the
// guard test in yamlpos pins the caller set for exactly that reason.

// runErrPublishedPerViolation reports whether the pre-render run error has
// already been published finding by finding among the resolver-class
// diagnostics, which is what decides whether the closure-level fall-back has
// anything left to say about it.
//
// It reads the ERROR's own shape rather than the published slice's length,
// because the two stopped answering the same question once a rule could raise a
// finding that refuses only a run producing something to apply: such a finding
// travels out of this pipeline on the same slice, on a run whose failure is
// somewhere else entirely and on a run that did not fail at all. The published
// slice still bounds the answer — an error whose per-violation extraction
// yielded nothing was not published however it is shaped — so it is a parameter
// rather than something re-derived here.
//
// The two shapes are the two the pipeline extracts into that slice: a group,
// which carries its violations, and a single finding, which is the one-element
// case.
func runErrPublishedPerViolation(runErr error, published []*resolver.ResolverError) bool {
	if len(published) == 0 {
		return false
	}
	var multi *resolver.MultiResolverError
	var single *resolver.ResolverError
	return errors.As(runErr, &multi) || errors.As(runErr, &single)
}

// tier4PhaseErrorMessage formats a non-resolver pre-analyzer phase
// error into the Tier-D message shape. RunTier4Pipeline wraps phase
// errors with "Step N (phase): ..." framing; strip the framing for
// brevity and prefix with "Dependencies: " so the message reads naturally
// in the IDE.
func tier4PhaseErrorMessage(err error) string {
	msg := err.Error()
	if idx := strings.Index(msg, "): "); idx > 0 && strings.HasPrefix(msg, "Step ") {
		msg = msg[idx+3:]
	}
	return fmt.Sprintf("Dependencies: %s", msg)
}
