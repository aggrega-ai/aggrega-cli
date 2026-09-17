// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// validateCatalogWideFromWorkspace resolves the workspace's catalog directories
// and cache, then runs the whole-catalog correctness pass. Catalog directories
// come from the repeatable --catalog flag prepended to the workspace config's
// catalog_dirs; at least one source must be set — a whole-tree enumeration with
// no configured directory is refused rather than silently scanning the working
// directory, which would pull in unrelated trees (a subrepo's testdata fixtures)
// and surface as a misleading duplicate-identity dump. Release-scoped correctness
// resolves a specific closure rather than a whole-tree enumeration, so it keeps a
// working-directory default; the footgun is specific to whole-catalog scanning.
func validateCatalogWideFromWorkspace(rw *workspaceconfig.ResolvedWorkspace, catalogFlagDirs []string) error {
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return fmt.Errorf("load workspace config: %w", err)
	}
	wsRoot := rw.Root()
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	if len(catalogFlagDirs) == 0 && len(wsCfg.Workspace.CatalogDirs) == 0 {
		return cerrors.Unidentified(cerrors.OpsError, cerrors.SourceLocation{},
			"whole-catalog validation needs catalog directories to scan; pass --catalog or set `workspace.catalog_dirs` in .aggrega-config.yaml (or validate a specific Release with --release)",
			nil, nil)
	}
	dirs := workspaceconfig.ResolveCatalogDirs(catalogFlagDirs, wsCfg, wsRoot)
	return validateCatalogWide(wsRoot, cacheDir, dirs, os.Stderr, os.Stdout)
}

// validateCatalogWide runs the whole-catalog correctness pass core and renders
// its structured result: it halts loudly on any schema-pillar violation,
// unresolvable or invalid library, extends-resolution failure, location
// collision, or input-kind capability error, checked in that halt-at-first order.
// Advisory and error diagnostics are written to diagOut (os.Stderr in
// production); the final success summary is written to out (os.Stdout in
// production). The count-summary phrasing is composed here at the render seam;
// the core reports each failing category's full ordered detail.
func validateCatalogWide(wsRoot, cacheDir string, dirs []string, diagOut, out io.Writer) error {
	res, err := validateops.ValidateCatalog(wsRoot, cacheDir, dirs)
	if err != nil {
		return fmt.Errorf("catalog validation failed: %w", err)
	}
	if len(res.ExtendsErrs) > 0 {
		for _, e := range res.ExtendsErrs {
			fmt.Fprintf(diagOut, "  extends: %s\n", cerrors.CauseChain(e))
		}
		return fmt.Errorf("catalog validation failed: %d extends resolution error(s)", len(res.ExtendsErrs))
	}
	if res.OCICollision != nil {
		return fmt.Errorf("catalog validation failed: %w", res.OCICollision)
	}
	if len(res.InputKindErrs) > 0 {
		for _, e := range res.InputKindErrs {
			fmt.Fprintf(diagOut, "  input-kind: %s\n", cerrors.CauseChain(e))
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
// schema-pillar validation, and pre-render authored-value conformance via the
// build's shared Step-1 guard suite.
//
// Closure-scoped pillar VALIDATION errors fail the check (non-zero exit);
// schema-pillar OVERLAP advisories are informational `notice:` lines that never
// change the exit code, matching the build's own advisory treatment. No render
// runs — this is validation, not emission.
//
// The lockfile pins the closure when present (the build's pinned view); absent,
// the closure resolves through Tier-1 (local catalog). It never triggers a
// network pull — a registry-pinned closure that needs a lockfile surfaces as an
// honest unresolvable-URI error rather than a silent fetch.
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
// ctx is the caller's and reaches both halves of the pass — the closure
// resolution and the validation that dispatches the operator-attached guests —
// so a reader who stops the run stops the work rather than only the reply.
func validateRelease(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, releasePath string, catalogFlagDirs []string, verbosity string, irGraph bool, diagOut, out io.Writer) error {
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return err
	}
	wsRoot := rw.Root()
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	dirs := workspaceconfig.ResolveCatalogDirs(catalogFlagDirs, wsCfg, wsRoot)

	lazyView := catalog.NewLazyLookup(wsRoot, cacheDir, dirs, nil)
	lf, _, lfErr := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if lfErr == nil {
		lazyView.SetPins(catalog.PinsFromLockfile(lf))
	}

	// Route onto the shared full-Tier-4 semantic-validation core — the same core
	// the other front doors validate a Release with — so every front door runs one
	// implementation. ResolveValidationClosure loads the Manifest and Aggregate
	// siblings and walks the closure over the pinned view; Validate runs the closure-scoped
	// extends and schema-pillar gates, the Steps 1-4 pipeline, the
	// resolution-completeness findings, the input-kind capability check, and the
	// advisory lines.
	// The resolution below runs under the caller's context and carries it into
	// every registry step it takes, so a reader who stops the run stops the
	// resolution — and the daemon, which holds a per-request context, gets the
	// same behaviour over the same code.
	resolved, cerr := compilerops.ResolveValidationClosure(ctx, compilerops.ValidationClosureInputs{
		View:            lazyView,
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
		// through the same renderer.
		return withSkipContext(cerr, lazyView.SkippedEntities())
	}

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
	})
	// The deprecation report is the shared one every surface reads, from the
	// lockfile loaded above. This verb produces a REPORT, so it enumerates each
	// finding beside everything else it has to say; the halt on a recorded marker
	// belongs to the verbs that produce an artifact.
	//
	// Both stores are read, because which one holds a marker is decided by the
	// attachment's role rather than by anything the reader chose: a plugin attached
	// at a verdict role is recorded beside the lockfile and has no row here. A
	// surface reading one store and not the other reports a withdrawal for some
	// references and stays silent for others, and its silence reads as "nothing is
	// withdrawn". An unreadable record is reported rather than skipped, for the same
	// reason the record's loader refuses one: a damaged record must not be able to
	// pass as an empty one.
	// The graph the run already resolved, handed back before any of this verb's
	// own output. It is read off the result rather than rebuilt: the validation
	// core holds the graph it validated, and a second resolution here could
	// answer a different question than the verdict below reports on.
	if irGraph {
		if vr.Tier4 == nil || vr.Tier4.IRGraph == nil {
			fmt.Fprintf(diagOut, "  no graph: validation stopped before the graph was resolved, so there is none to hand back\n")
		} else if err := writeIRGraph(out, "validate", vr.Tier4.IRGraph, false); err != nil {
			return err
		}
	}

	trustRecord, _, trustErr := pluginstrust.LoadOrStart(pluginstrust.PathFor(releasePath))
	if trustErr != nil {
		return trustErr
	}
	var notices []string
	for _, f := range lf.DeprecationFindings() {
		notices = append(notices, f.Notice())
	}
	for _, f := range trustRecord.DeprecationFindings() {
		notices = append(notices, f.Notice())
	}
	return renderReleaseValidation(vr, resolved.ManifestConsistencyFindings, notices, releasePath, verbosity, diagOut, out)
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
	var mappingDiags []string
	for _, f := range consistencyFindings {
		mappingDiags = append(mappingDiags, cerrors.CauseChain(f))
	}
	if detailed {
		for _, d := range mappingDiags {
			fmt.Fprintf(diagOut, "  mapping: %s\n", d)
		}
	}

	if len(vr.ClosureExtendsErrors) > 0 {
		if detailed {
			for _, e := range vr.ClosureExtendsErrors {
				fmt.Fprintf(diagOut, "  extends: %s\n", cerrors.CauseChain(e))
			}
		} else {
			fmt.Fprintf(diagOut, "release dependency validation: %d extends diagnostic(s); first: %s\n",
				len(vr.ClosureExtendsErrors), cerrors.CauseChain(vr.ClosureExtendsErrors[0]))
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

	// Every diagnostic here is composed as ONE line, so each is flattened with
	// CauseChain rather than Error: a categorised envelope renders a single frame,
	// which names the phase that failed and drops the frame beneath it carrying the
	// actual fault — the offending entity, the assertion, and an assertion's own
	// author-written message, which is the only actionable part it has.
	var pipelineDiags []string
	// The resolver-class findings are taken once into a local, so the phase-error
	// guard below asks its question of the same slice this loop published.
	var perViolation []*resolver.ResolverError
	if vr.Tier4 != nil {
		perViolation = vr.Tier4.ResolveDiagnostics
	}
	for _, ve := range perViolation {
		pipelineDiags = append(pipelineDiags, cerrors.CauseChain(ve))
	}
	if vr.Tier4 != nil && vr.Tier4.Analysis != nil {
		for _, ae := range vr.Tier4.Analysis.Errors {
			pipelineDiags = append(pipelineDiags, cerrors.CauseChain(ae))
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
	// the only line describing it — the flattened cause chain that carries the
	// offending entity, the assertion, and the assertion's own author-written
	// message.
	if vr.RunErr != nil && !runErrPublishedPerViolation(vr.RunErr, perViolation) {
		pipelineDiags = append(pipelineDiags, cerrors.CauseChain(vr.RunErr))
	}
	for _, f := range vr.ResolutionFindings {
		pipelineDiags = append(pipelineDiags, cerrors.CauseChain(f))
	}

	if detailed {
		for _, d := range pipelineDiags {
			fmt.Fprintf(diagOut, "  %s\n", d)
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
			fmt.Fprintf(diagOut, "  input-kind: %s\n", cerrors.CauseChain(e))
		}
	} else {
		// The concise form collapses the verdict-affecting diagnostics — the mapping
		// findings, then the pipeline diagnostics, then the input-kind errors, in the
		// order they were decided — into a count and the first, and emits no advisory
		// lines.
		if total := len(mappingDiags) + len(pipelineDiags) + len(vr.InputKindErrors); total > 0 {
			first := ""
			switch {
			case len(mappingDiags) > 0:
				first = "mapping: " + mappingDiags[0]
			case len(pipelineDiags) > 0:
				first = pipelineDiags[0]
			default:
				first = "input-kind: " + cerrors.CauseChain(vr.InputKindErrors[0])
			}
			fmt.Fprintf(diagOut, "release dependency validation: %d diagnostic(s); first: %s\n", total, first)
		}
	}

	// The verdict follows the reporting order above, so the message names the
	// earliest-decided class rather than whichever check happens to be tested
	// first. A mapping finding is advisory in POSITION only — it never truncates
	// the report — and never in verdict: the mapping is wrong whoever asked.
	if len(mappingDiags) > 0 {
		return fmt.Errorf("release dependency validation failed: %d manifest mapping diagnostic(s)", len(mappingDiags))
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
