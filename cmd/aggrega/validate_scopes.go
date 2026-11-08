// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/serve"
	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// validateCatalogWideFromWorkspace resolves the workspace's catalog directories
// and cache, then runs the whole-catalog correctness pass over each pass the one
// decision of which passes a whole-catalog check runs returns. Each pass's search
// stack is the shared one — the repeatable --catalog flag prepended to the
// configured catalog_dirs, with the conventional catalog directory at the
// workspace root last — and a run with nothing in any pass is refused rather
// than reported as a clean catalog, because a pass over no entity at all passes
// vacuously and reads exactly like a pass over a healthy one.
//
// A workspace that gives no Release directories of its own has one pass, over
// its own stack, and is validated exactly as a single catalog is. A workspace
// that does is validated once for each Release over the directories that Release
// reads, so one catalog variant is never loaded beside another; see
// validateCatalogPasses.
//
// The refusal names what a reader can do about it. Release-scoped correctness
// resolves a specific closure rather than a whole-tree enumeration, so it has
// its own way to reach entities and does not refuse here.
//
// ctx is the caller's, carried into every pass, so a reader who stops the run
// stops the reads the passes make.
func validateCatalogWideFromWorkspace(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, catalogFlagDirs []string) error {
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return fmt.Errorf("load workspace config: %w", err)
	}
	wsRoot := rw.Root()
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	// The refusal keys on the RESOLVED stacks rather than on the flag and the
	// configured lists, so a workspace whose entities sit in the conventional
	// directory is scanned rather than refused, and a workspace that switched the
	// local catalog off with an empty `catalog_dirs` is refused rather than
	// silently scanning nothing.
	passes, err := serve.WholeCatalogPasses(rw, catalogFlagDirs)
	if err != nil {
		return err
	}
	perRelease := len(passes) > 0 && passes[0].NamesReleases()
	if !workspaceconfig.AnyCatalogPassReads(passes) {
		// A workspace that gives its Releases directories of their own is also told
		// where a Release's own list is set, since the workspace's list does not
		// reach a Release whose entry replaces it.
		return cerrors.Unidentified(cerrors.OpsError, cerrors.SourceLocation{},
			fmt.Sprintf("whole-catalog validation needs catalog directories to scan; put the entities under `%s/` at the workspace root, pass --catalog, or set %s (or validate a specific Release with --release)",
				workspaceconfig.ConventionalCatalogDir, workspaceconfig.CatalogDirsSettingWords(perRelease)),
			nil, nil)
	}
	if !perRelease {
		pins, err := serve.CatalogPassPins(rw, passes[0])
		if err != nil {
			return err
		}
		return validateCatalogWide(ctx, wsRoot, cacheDir, passes[0].Dirs, passes[0].Named, pins, os.Stderr, os.Stdout)
	}
	return validateCatalogPasses(ctx, rw, passes, os.Stderr, os.Stdout)
}

// validateCatalogPasses runs the whole-catalog correctness pass once for each
// pass, each under a line naming the Releases it is run for and the directories
// it reads, and reports every pass whatever an earlier one found: a broken
// entity in one Release's catalog variant says nothing about another's, so a
// reader is told about each. A pass whose Releases read no catalog directory says
// so and checks nothing.
//
// The verdict is a failure when any pass failed, and it names the Releases whose
// directories failed; each failing pass's own reason is written beneath its line
// on diagOut, where the single-catalog run's diagnostics go.
//
// Each pass reads a base its catalog does not hold through the pins its own
// Releases' lockfiles record (serve.CatalogPassPins). A pass whose lockfiles cannot
// be read is reported as that pass's failure, as one whose catalog cannot be loaded
// is.
//
// The assistant-facing tool has a function of the same name, kept apart on
// purpose: each renders the passes for its own surface — lines on a terminal here,
// a structured answer there. What a pass reads is decided once for both, by
// serve.WholeCatalogPasses and serve.CatalogPassPins.
func validateCatalogPasses(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, passes []workspaceconfig.CatalogPass, diagOut, out io.Writer) error {
	wsRoot := rw.Root()
	cacheDir := rw.Config().ResolvedArtifactCacheDir()
	var failed []string
	for i, pass := range passes {
		if i > 0 {
			fmt.Fprintln(out)
		}
		reported := make([]string, 0, len(pass.Dirs))
		for _, d := range pass.Dirs {
			reported = append(reported, pathscope.ReportedName(wsRoot, d))
		}
		fmt.Fprintln(out, workspaceconfig.CatalogPassHeading(pass.Releases, reported))
		if len(pass.Dirs) == 0 {
			continue
		}
		pins, err := serve.CatalogPassPins(rw, pass)
		if err == nil {
			err = validateCatalogWide(ctx, wsRoot, cacheDir, pass.Dirs, pass.Named, pins, diagOut, out)
		}
		if err != nil {
			fmt.Fprintf(diagOut, "  %s\n", cerrors.CauseChain(err))
			failed = append(failed, pass.Releases...)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("catalog validation failed in the catalog directories of %s", strings.Join(failed, ", "))
	}
	return nil
}

// validateCatalogWide runs the whole-catalog correctness pass core and renders
// its structured result: it halts loudly on any schema-pillar violation,
// unresolvable or invalid library, extends-resolution failure, socket or slot
// selector the selector grammar refuses, location collision, or input-kind
// capability error, checked in that halt-at-first order.
// Advisory and error diagnostics are written to diagOut (os.Stderr in
// production); the final success summary is written to out (os.Stdout in
// production). The count-summary phrasing is composed here at the render seam;
// the core reports each failing category's full ordered detail. named is the
// part of dirs somebody named, as the resolver returned it. pins are the pins the
// pass reads a base its catalog does not hold through (validateops.ValidateCatalog).
func validateCatalogWide(ctx context.Context, wsRoot, cacheDir string, dirs, named []string, pins catalog.Pins, diagOut, out io.Writer) error {
	res, err := validateops.ValidateCatalog(ctx, wsRoot, cacheDir, dirs, named, pins)
	if err != nil {
		return fmt.Errorf("catalog validation failed: %w", err)
	}
	if len(res.ExtendsErrs) > 0 {
		for _, e := range res.ExtendsErrs {
			fmt.Fprintf(diagOut, "  extends: %s\n", cerrors.CauseChain(e))
		}
		return fmt.Errorf("catalog validation failed: %d extends resolution error(s)", len(res.ExtendsErrs))
	}
	if len(res.SelectorErrs) > 0 {
		for _, e := range res.SelectorErrs {
			fmt.Fprintf(diagOut, "  selector: %s\n", cerrors.HeadedCauseChain(e))
		}
		return fmt.Errorf("catalog validation failed: %d selector error(s)", len(res.SelectorErrs))
	}
	if res.OCICollision != nil {
		return fmt.Errorf("catalog validation failed: %w", res.OCICollision)
	}
	if len(res.InputKindErrs) > 0 {
		for _, e := range res.InputKindErrs {
			fmt.Fprintf(diagOut, "  input-kind: %s\n", cerrors.HeadedCauseChain(e))
		}
		return fmt.Errorf("catalog validation failed: %d input-kind capability error(s)", len(res.InputKindErrs))
	}
	for _, w := range res.Notices {
		fmt.Fprintf(diagOut, "  notice: %s\n", w)
	}
	fmt.Fprintln(out, "catalog validation passed (schema-pillar + library + extends + oci-location)")
	return nil
}

// validateRelease runs the release-scoped correctness pass: it computes a
// Release's resolved closure through the SAME build machinery the compiler uses
// (the shared closure resolver over the lazy lookup — never a local fork, which would re-open
// the build/validate view divergence) and validates over exactly the entities
// that Release reaches: closure-scoped extends resolution, closure-scoped
// schema-pillar validation, pre-render authored-value conformance via the
// build's shared Step-1 guard suite, and the Recipe and FeatureRecipe assertions
// evaluated against the Release's resolved values through the stages a build runs
// ahead of its render.
//
// Closure-scoped pillar VALIDATION errors fail the check (non-zero exit);
// schema-pillar OVERLAP advisories are informational `notice:` lines that never
// change the exit code, matching the build's own advisory treatment. No render
// runs — this is validation, not emission.
//
// The lockfile pins the closure when present (the build's pinned view); absent,
// the closure resolves through Tier-1 (local catalog). It never triggers a
// network pull — a registry-pinned closure that needs a lockfile surfaces as an
// honest unresolvable-URI error rather than a silent fetch. A lockfile that is
// present and no longer matches what the Release resolves to refuses the
// validation with the drift halt a build prints, before any check here runs, so a
// passing validation never stands over a lockfile the build's drift gate refuses.
//
// Advisory (`notice:`) and error diagnostics are written to diagOut (os.Stderr in
// production); the final success summary is written to out (os.Stdout in
// production).
//
// irGraph asks for the graph this validation resolved, written to out ahead of
// everything else this verb produces. The graph is the one the run already
// holds — the validation core carries it on its result — so asking for it costs
// no second resolution and cannot disagree with what was validated. It changes
// neither the checks, the verdict nor the exit code. A run whose gates refused
// before the graph was built has none to hand back, and says so on diagOut
// rather than emitting an empty document.
//
// unredacted is the reader's --unredacted: the graph withholds every value an
// author declared sensitive unless it is set. It reaches the graph and nothing
// else this verb writes.
//
// ctx is the caller's and reaches both halves of the pass — the closure
// resolution and the validation that dispatches the operator-attached guests —
// so a reader who stops the run stops the work rather than only the reply.
func validateRelease(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, releasePath string, catalogFlagDirs []string, verbosity string, irGraph, unredacted bool, diagOut, out io.Writer) error {
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return err
	}
	wsRoot := rw.Root()
	wsCfg := rw.Config()

	// Route onto the shared full-Tier-4 semantic-validation core — the same core
	// the other front doors validate a Release with — so every front door runs one
	// implementation. The dependency resolution loads the Manifest and Aggregate
	// siblings and walks the closure over the pinned view; Validate runs the closure-scoped
	// extends and schema-pillar gates, the Steps 1-4 pipeline and the Recipe and
	// FeatureRecipe assertion stages after it, the resolution-completeness findings, the input-kind capability check, and the
	// advisory lines.
	deps, err := resolveReleaseDependencies(ctx, rw, releasePath, catalogFlagDirs)
	if err != nil {
		return err
	}
	lazyView, lf, resolved := deps.view, deps.lockfile, deps.resolved

	// The record loaded above is threaded into the validation, not just into the
	// closure resolution. The pipeline this verb runs dispatches the same
	// operator-attached guests a build dispatches, so without the record those
	// guests would run with nothing to check their identity against and — for the
	// two that change the graph — with none of the pinned input the record carries
	// for them. Both follow from the same omission, so both are repaired by passing
	// it. A workspace with no record loads none here and validates as before, with
	// the resolution saying which plugins it could not check.
	vr := compilerops.Validate(ctx, compilerops.ValidateInputs{
		View:            lazyView,
		Closure:         resolved.Closure,
		Release:         resolved.Release,
		ReleasePath:     releasePath,
		Manifest:        resolved.Manifest,
		AggBytes:        resolved.AggBytes,
		Lockfile:        lf,
		AggregatePath:   catalog.RelativeToWorkspace(wsRoot, resolved.AggregatePath),
		WorkspaceRoot:   wsRoot,
		WorkspaceConfig: wsCfg,
		CompilerVersion: provenanceCompilerVersion(),
		Display:         diagOut,
	})
	// The deprecation report is the shared one every surface reads, over both the
	// lockfile loaded above and the plugin trust record beside the Release. This
	// verb produces a REPORT, so it enumerates each finding beside everything else
	// it has to say; the halt on a recorded marker belongs to the verbs that produce
	// an artifact. An unreadable record is reported rather than skipped, for the
	// same reason the record's loader refuses one: a damaged record must not be able
	// to pass as an empty one.
	// The graph the run already resolved, handed back before any of this verb's
	// own output. It is read off the result rather than rebuilt: the validation
	// core holds the graph it validated, and a second resolution here could
	// answer a different question than the verdict below reports on.
	if irGraph {
		if vr.Tier4 == nil || vr.Tier4.IRGraph == nil {
			fmt.Fprintf(diagOut, "  no graph: validation stopped before the graph was resolved, so there is none to hand back\n")
		} else if err := writeIRGraph(out, "validate", vr.Tier4.IRGraph, unredacted); err != nil {
			return err
		}
	}

	findings, trustErr := compilerops.ReleaseDeprecationReport(lf, releasePath)
	if trustErr != nil {
		return trustErr
	}
	var notices []string
	for _, f := range findings {
		notices = append(notices, f.Notice)
	}
	return renderReleaseValidation(vr, resolved.ManifestConsistencyFindings, notices, releasePath, verbosity, diagOut, out)
}

// releaseDependencies is what resolving a Release's dependencies the way a
// validation of it does leaves behind: the request-scoped view the resolution read
// through, the record of pinned versions that view was seeded with (nil where the
// workspace holds none), and the resolution itself.
type releaseDependencies struct {
	view     *catalog.LazyLookup
	lockfile *lockfile.Lockfile
	resolved *compilerops.ResolvedClosure
}

// resolveReleaseDependencies resolves the dependencies of the Release at
// releasePath exactly as a validation of that Release resolves them, and refuses
// the Release where one does not resolve.
//
// Two commands ask it, and both ask it here rather than each building the view
// for itself: the release-scoped validation, which reads everything this returns,
// and the inspection of a Release, which reads whether it refused and the lockfile
// and pinned view it leaves behind. So a reference no catalog holds — named by the
// Release's own Aggregate or by a module it imports, as a catalog identity or as a
// ./ or ../ path — is reported by both in the same words, rather than reaching the
// graph build, whose checks are entitled to assume every reference resolved.
//
// The view is built with no registry client, so the resolution is a local read: a
// reference the catalog directories and the cache do not hold is refused as
// unresolvable, never fetched. The record of pinned versions seeds the view where
// the workspace has one, so what resolves is what this Release is pinned to.
//
// Where that record exists it is then held to what the Release resolves to, by
// the build's drift comparison (compilerops.HoldReleaseToLockfile), and a record
// that no longer matches refuses the Release with the drift halt the build prints
// — so neither command passes over a lockfile the build's drift gate refuses. The
// build's other gates are not run, its pinned-integrity gate among them; that
// function says what a reading surface meets instead. A workspace with no record
// is resolved as before and held to nothing.
//
// catalogFlagDirs is the repeatable --catalog flag, prepended to the directories
// the workspace configuration gives this Release.
//
// The resolution runs under the caller's context and carries it into every step
// it takes, so a reader who stops the run stops the resolution — the behaviour
// the daemon gets from the same validation resolution under its per-request
// context.
func resolveReleaseDependencies(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, releasePath string, catalogFlagDirs []string) (releaseDependencies, error) {
	wsRoot := rw.Root()
	wsCfg := rw.Config()
	dirs, err := rw.ResolveCatalogDirs(releasePath, catalogFlagDirs)
	if err != nil {
		return releaseDependencies{}, err
	}
	named, err := rw.NamedCatalogDirs(releasePath, catalogFlagDirs)
	if err != nil {
		return releaseDependencies{}, err
	}

	view := catalog.NewLazyLookup(wsRoot, wsCfg.ResolvedArtifactCacheDir(), dirs, nil, named...)
	view.WriteNoticesTo(os.Stderr)
	lf, err := lockfile.LoadIfReadable(wsRoot, lockfile.PathFor(releasePath))
	if err != nil {
		return releaseDependencies{}, err
	}
	if lf != nil {
		pins, err := catalog.PinsFromLockfile(lf)
		if err != nil {
			return releaseDependencies{}, err
		}
		view.SetPins(pins)
	}
	resolved, cerr := compilerops.ResolveValidationClosure(ctx, compilerops.ValidationClosureInputs{
		View:            view,
		ReleasePath:     releasePath,
		WorkspaceRoot:   wsRoot,
		WorkspaceConfig: wsCfg,
		Lockfile:        lf,
	})
	if cerr != nil {
		// A file the catalog read refused makes its entity unresolvable, and an
		// unresolvable entity is all this failure can say on its own — it names
		// the reference, never the file behind it. Replaying the refusals the
		// view recorded turns that into the constraint the author has to fix.
		// This is the same annotation the build applies, over the same record,
		// through the same renderer. Where no refusal explains it, the reference
		// may name an entity sitting in a directory this workspace does not read,
		// so the workspace's own account of where it reads travels too.
		return releaseDependencies{}, compilerops.AnnotateSkipContext(cerr, view.SkippedEntities(), rw.LocalCatalogAdviceNote(releasePath))
	}
	// The lockfile loaded above is held to what the Release resolves to, by the
	// build's drift comparison, and a lockfile that no longer matches refuses the
	// Release with the drift halt the build prints. A validation that passed
	// here would predict a build that then refuses, and an inspection would draw a
	// picture no build of the same Release renders.
	if err := compilerops.HoldReleaseToLockfile(ctx, view, releasePath, wsRoot, wsCfg, lf); err != nil {
		return releaseDependencies{}, err
	}
	return releaseDependencies{view: view, lockfile: lf, resolved: resolved}, nil
}

// renderReleaseValidation writes a release-scoped validation result's diagnostics
// and advisories in gate order and returns the verdict error (nil on a clean
// pass). The extends, schema-pillar and verdict-plugin-trust gates halt at the
// first failure, each returning its own text as the verdict error and writing
// nothing to diagOut; when all three pass, the pipeline diagnostics (Step-1 resolve and value conformance, Step-4
// analyze, a non-resolver phase error, and resolution-completeness findings), the
// advisory `notice:` lines, and the input-kind `input-kind:` lines are considered
// together, and any pipeline diagnostic or input-kind error yields a non-zero
// verdict. Advisories never change the verdict.
//
// consistencyFindings carries the Manifest mapping-table findings from the
// closure resolution, which happens before any gate here runs. This verb reports
// on a document rather than producing an artifact, so a finding is enumerated
// alongside everything else the document has wrong rather than replacing it — and
// it still yields a non-zero verdict, because the mapping is wrong whoever asked.
// It is a parameter rather than a field on the result so a caller cannot leave it
// unpopulated and report a sound Manifest for an unsound one; there is no value
// that means "not checked".
//
// deprecationFindings carries the shared deprecation report over the lockfile's
// recorded markers — the same report the build reads, so the two surfaces name the
// same references — and is a parameter for the same reason. Its lines are
// exit-code-neutral, matching the recorded stance of the build's own default: a
// marker is registry state applied after the version was pinned, so it advises
// unless a workspace configured a halt, and the halt dial governs the build alone.
// Reporting them here removes the silence; it does not move this verb's verdict.
//
// verbosity governs output VOLUME only — never the verdict or the exit code.
// The detailed form (the default) writes every diagnostic in gate order plus the
// advisory lines; the concise form writes a single summary line carrying the
// diagnostic count and the first diagnostic, and no advisory lines. Both forms
// run the identical checks, reach the identical verdict, and return the identical
// error (or nil). Diagnostics and advisories go to diagOut; the success summary
// goes to out.
// deprecationNotices arrives already rendered, one sentence per finding, because
// the two stores a marker can sit in render their own sentence: the lockfile-backed
// one names the moment the record was observed, and the trust-record-backed one
// cannot, since that record carries no wall-clock stamp at all. Re-rendering either
// through the other's wording would assert a provenance it does not have.
func renderReleaseValidation(vr *compilerops.ValidateResult, consistencyFindings []error, deprecationNotices []string, releasePath, verbosity string, diagOut, out io.Writer) error {
	detailed := verbosity != compilerops.VerbosityConcise

	// The deprecation report leads, and outside the reach of every early return
	// below, for the same reason the mapping findings do: it was decided before any
	// gate here read anything — the marker was recorded at the pull that wrote the
	// lockfile. A gate refusing first must not hide it. It is written in the
	// detailed form only, like the other advisory lines, since the concise form
	// collapses the verdict-affecting classes and these affect no verdict.
	if detailed {
		for _, notice := range deprecationNotices {
			fmt.Fprintf(diagOut, "  deprecation: %s\n", notice)
		}
	}

	// The mapping-table findings were decided ahead of every gate below — the
	// closure resolution judged the composed Manifest before any of these gates
	// read anything — so they are reported first, and deliberately outside the
	// reach of the three early returns that follow: a Manifest fault is never
	// hidden by a later gate refusing first. In the concise form they fold into the
	// summary count with the other verdict-affecting classes.
	if detailed {
		for _, f := range consistencyFindings {
			fmt.Fprint(diagOut, releaseFinding("mapping", f))
		}
	}

	if len(vr.ClosureExtendsErrors) > 0 {
		if detailed {
			for _, e := range vr.ClosureExtendsErrors {
				fmt.Fprint(diagOut, releaseFinding("extends", e))
			}
		} else {
			fmt.Fprintf(diagOut, "release dependency validation: %d extends diagnostic(s); first: %s\n",
				len(vr.ClosureExtendsErrors), releaseFindingOneLine(vr.ClosureExtendsErrors[0]))
		}
		return fmt.Errorf("release dependency validation failed: %d extends resolution error(s)", len(vr.ClosureExtendsErrors))
	}
	if vr.PillarError != nil {
		return fmt.Errorf("release dependency validation failed: %w", vr.PillarError)
	}

	// The verdict-plugin trust gate. Like the pillar gate, its text rides the
	// verdict error rather than the diagnostic stream: it names a fault in the
	// workspace configuration or in the record beside the Release rather than in
	// any authored entity, so there is no source position for it to be reported at
	// and nothing further was validated after it.
	if vr.VerdictTrustError != nil {
		return fmt.Errorf("release dependency validation failed: %w", vr.VerdictTrustError)
	}

	// A shutdown-drain cancellation is not a validation verdict; surface it as-is
	// with no diagnostics.
	if isCancellation(vr.RunErr) {
		return vr.RunErr
	}

	// Every diagnostic here is rendered by releaseFinding, which prints it as a
	// build or a pull prints a failure: its bracketed head and short code, then the
	// frames beneath it — the offending entity, the assertion, and an assertion's
	// own author-written message, which is the only actionable part it has.
	var pipelineDiags []error
	// The resolver-class findings are taken once into a local, so the phase-error
	// guard below asks its question of the same slice this loop published.
	var perViolation []*resolver.ResolverError
	if vr.Tier4 != nil {
		perViolation = vr.Tier4.ResolveDiagnostics
	}
	for _, ve := range perViolation {
		pipelineDiags = append(pipelineDiags, ve)
	}
	// An analysis violation is converted as every reporting boundary converts it,
	// which is what puts its category, its authoring position and the condition it
	// names on the diagnostic; the bare value carries the sentence alone.
	if vr.Tier4 != nil && vr.Tier4.Analysis != nil {
		for _, ae := range vr.Tier4.Analysis.Errors {
			pipelineDiags = append(pipelineDiags, ae.ToCompilerError())
		}
	}
	// A non-resolver pre-analyzer phase error carries no per-violation structure;
	// surface it only when the resolver did not already publish it finding by
	// finding, so a resolver failure is not double-reported.
	//
	// Whether it was already published is asked of the run error itself and never of
	// whether the slice above was empty. That slice also carries the findings a rule
	// raises for an emitting run alone, which reach a surface like this one on a run
	// whose failure is elsewhere and on a run that did not fail at all — so reading
	// its length would take such a finding as evidence about the phase error and drop
	// the only diagnostic describing it — the one that carries the offending
	// entity, the assertion, and the assertion's own author-written message.
	if vr.RunErr != nil && !runErrPublishedPerViolation(vr.RunErr, perViolation) {
		pipelineDiags = append(pipelineDiags, vr.RunErr)
	}
	pipelineDiags = append(pipelineDiags, vr.ResolutionFindings...)

	if detailed {
		for _, d := range pipelineDiags {
			fmt.Fprint(diagOut, releaseFinding("", d))
		}

		// Schema-pillar overlap + narrowing-on-extend advisories — informational,
		// never change the exit code (the compiler does not police narrow-vs-relax).
		for _, w := range vr.LintWarnings {
			fmt.Fprintf(diagOut, "  notice: %s\n", w)
		}
		for _, w := range vr.NarrowingWarnings {
			fmt.Fprintf(diagOut, "  notice: %s\n", w)
		}

		for _, e := range vr.InputKindErrors {
			fmt.Fprint(diagOut, releaseFinding("input-kind", e))
		}
	} else {
		// The concise form collapses the verdict-affecting diagnostics — the mapping
		// findings, then the pipeline diagnostics, then the input-kind errors, in the
		// order they were decided — into a count and the first, and emits no advisory
		// lines.
		if total := len(consistencyFindings) + len(pipelineDiags) + len(vr.InputKindErrors); total > 0 {
			first := ""
			switch {
			case len(consistencyFindings) > 0:
				first = "mapping: " + releaseFindingOneLine(consistencyFindings[0])
			case len(pipelineDiags) > 0:
				first = releaseFindingOneLine(pipelineDiags[0])
			default:
				first = "input-kind: " + releaseFindingOneLine(vr.InputKindErrors[0])
			}
			fmt.Fprintf(diagOut, "release dependency validation: %d diagnostic(s); first: %s\n", total, first)
		}
	}

	// The verdict follows the reporting order above, so the message names the
	// earliest-decided class rather than whichever check happens to be tested
	// first. A mapping finding is advisory in POSITION only — it never truncates
	// the report — and never in verdict: the mapping is wrong whoever asked.
	if len(consistencyFindings) > 0 {
		return fmt.Errorf("release dependency validation failed: %d manifest mapping diagnostic(s)", len(consistencyFindings))
	}
	if len(pipelineDiags) > 0 {
		return fmt.Errorf("release dependency validation failed: %d diagnostic(s)", len(pipelineDiags))
	}
	if len(vr.InputKindErrors) > 0 {
		return fmt.Errorf("release dependency validation failed: %d input-kind capability error(s)", len(vr.InputKindErrors))
	}

	if detailed {
		fmt.Fprintf(out, "release dependency validation passed: %s (schema-pillar + dependency pillars)\n", releasePath)
	} else {
		fmt.Fprintf(out, "release dependency validation passed: %s\n", releasePath)
	}
	return nil
}

// releaseFinding renders one finding of a Release validation in the detailed
// form: the diagnostic formatCompilerError composes for it — the formatter build
// and pull print a failure through, so the finding opens with its bracketed head
// and the short code `aggrega explain` resolves — laid out as an indented block
// ending in a newline. label, where it is not empty, names the class of finding
// ahead of the head, as in "mapping: [CATALOG_ERROR E0123] …".
//
// It renders through that one formatter rather than composing a line of its own,
// so a finding reads the same here as it does where a build or a pull halts on it.
func releaseFinding(label string, err error) string {
	var b strings.Builder
	for i, line := range strings.Split(formatCompilerError(err), "\n") {
		b.WriteString("  ")
		if i == 0 && label != "" {
			b.WriteString(label)
			b.WriteString(": ")
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// releaseFindingOneLine is the same diagnostic releaseFinding renders, laid on
// one line for the concise form's summary: each of its lines, trimmed of the
// indentation that lays the block out, joined by a single space. The head, the
// short code and every frame stay; only the layout goes.
func releaseFindingOneLine(err error) string {
	var parts []string
	for _, line := range strings.Split(formatCompilerError(err), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, " ")
}

// runErrPublishedPerViolation reports whether the pre-render run error has
// already been published finding by finding among the resolver-class
// diagnostics, which is what decides whether the closure-level fall-back has
// anything left to say about it.
//
// It reads the ERROR's own shape rather than the published slice's length,
// because the two stopped answering the same question once a rule could raise a
// finding that refuses only a run producing something to apply: such a finding
// travels out of the pipeline on the same slice, on a run whose failure is
// somewhere else entirely and on a run that did not fail at all. The published
// slice still bounds the answer — an error whose per-violation extraction yielded
// nothing was not published however it is shaped — so it is a parameter rather
// than something re-derived here.
//
// The two shapes are the two the pipeline extracts into that slice: a group,
// which carries its violations, and a single finding, which is the one-element
// case.
//
// Every front end reading a validation result asks this same question of it, and
// the reading is written out per front end deliberately rather than shared. Its
// one structural home is the entry that fills the slice, which would mark each
// entry with whether the run error produced it and leave a consumer no reading to
// reproduce at all; short of that, a shared consumer-side copy would single-source
// the reproduction rather than the fact and hide that it is one. Each copy reads
// the same two error shapes that entry extracts, so the copies are comparable by
// inspection.
func runErrPublishedPerViolation(runErr error, published []*resolver.ResolverError) bool {
	if len(published) == 0 {
		return false
	}
	var multi *resolver.MultiResolverError
	var single *resolver.ResolverError
	return errors.As(runErr, &multi) || errors.As(runErr, &single)
}

// isCancellation reports whether err is a shutdown-drain cancellation, for which
// validation yields no diagnostics.
func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
