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
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
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
		return fmt.Errorf("OPS_ERROR: whole-catalog validation needs catalog directories to scan; pass --catalog or set `workspace.catalog_dirs` in .aggrega-config.yaml (or validate a specific Release with --release)")
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
			fmt.Fprintf(diagOut, "  extends: %s\n", e)
		}
		return fmt.Errorf("catalog validation failed: %d extends resolution error(s)", len(res.ExtendsErrs))
	}
	if res.OCICollision != nil {
		return fmt.Errorf("catalog validation failed: %w", res.OCICollision)
	}
	if len(res.InputKindErrs) > 0 {
		for _, e := range res.InputKindErrs {
			fmt.Fprintf(diagOut, "  input-kind: %s\n", e)
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
func validateRelease(rw *workspaceconfig.ResolvedWorkspace, releasePath string, catalogFlagDirs []string, verbosity string, diagOut, out io.Writer) error {
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
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
	// implementation. ResolveClosure loads the Manifest and Aggregate siblings and
	// walks the closure over the pinned view; Validate runs the closure-scoped
	// extends and schema-pillar gates, the Steps 1-4 pipeline, the
	// resolution-completeness findings, the input-kind capability check, and the
	// advisory lines.
	// Augment stays off: the release-scope validation core runs over the
	// un-augmented closure (the same footing serve's Tier-4 validation uses).
	// Folding the library axes here would turn a schema-pillar validation
	// diagnostic — the case a $ref to an absent library exercises — into an earlier
	// closure-resolution error, changing the verb's diagnostic surface.
	resolved, cerr := compilerops.ResolveClosure(compilerops.ClosureInputs{
		View:            lazyView,
		ReleasePath:     releasePath,
		WorkspaceRoot:   wsRoot,
		WorkspaceConfig: wsCfg,
		Lockfile:        lf,
		Augment:         false,
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

	vr := compilerops.Validate(context.Background(), compilerops.ValidateInputs{
		View:            lazyView,
		Closure:         resolved.Closure,
		Release:         resolved.Release,
		Manifest:        resolved.Manifest,
		AggBytes:        resolved.AggBytes,
		AggregatePath:   catalog.RelativeToWorkspace(wsRoot, resolved.AggregatePath),
		WorkspaceConfig: wsCfg,
		CompilerVersion: provenanceCompilerVersion(),
	})
	return renderReleaseValidation(vr, releasePath, verbosity, diagOut, out)
}

// renderReleaseValidation writes a release-scoped validation result's diagnostics
// and advisories in gate order and returns the verdict error (nil on a clean
// pass). The extends and schema-pillar gates halt at the first failure; when both
// pass, the pipeline diagnostics (Step-1 resolve and value conformance, Step-4
// analyze, a non-resolver phase error, and resolution-completeness findings), the
// advisory `notice:` lines, and the input-kind `input-kind:` lines are considered
// together, and any pipeline diagnostic or input-kind error yields a non-zero
// verdict. Advisories never change the verdict.
//
// verbosity governs output VOLUME only — never the verdict or the exit code.
// The detailed form (the default) writes every diagnostic in gate order plus the
// advisory lines; the concise form writes a single summary line carrying the
// diagnostic count and the first diagnostic, and no advisory lines. Both forms
// run the identical checks, reach the identical verdict, and return the identical
// error (or nil). Diagnostics and advisories go to diagOut; the success summary
// goes to out.
func renderReleaseValidation(vr *compilerops.ValidateResult, releasePath, verbosity string, diagOut, out io.Writer) error {
	detailed := verbosity != compilerops.VerbosityConcise

	if len(vr.ClosureExtendsErrors) > 0 {
		if detailed {
			for _, e := range vr.ClosureExtendsErrors {
				fmt.Fprintf(diagOut, "  extends: %s\n", e)
			}
		} else {
			fmt.Fprintf(diagOut, "release dependency validation: %d extends diagnostic(s); first: %s\n",
				len(vr.ClosureExtendsErrors), vr.ClosureExtendsErrors[0])
		}
		return fmt.Errorf("release dependency validation failed: %d extends resolution error(s)", len(vr.ClosureExtendsErrors))
	}
	if vr.PillarError != nil {
		return fmt.Errorf("release dependency validation failed: %w", vr.PillarError)
	}

	// A shutdown-drain cancellation is not a validation verdict; surface it as-is
	// with no diagnostics.
	if isCancellation(vr.RunErr) {
		return vr.RunErr
	}

	var pipelineDiags []string
	if vr.Tier4 != nil {
		for _, ve := range vr.Tier4.ResolveDiagnostics {
			pipelineDiags = append(pipelineDiags, ve.Error())
		}
		if vr.Tier4.Analysis != nil {
			for _, ae := range vr.Tier4.Analysis.Errors {
				pipelineDiags = append(pipelineDiags, ae.Error())
			}
		}
	}
	// A non-resolver pre-analyzer phase error carries no per-violation structure;
	// surface it only when the resolver produced no structured diagnostic, so a
	// resolver failure is not double-reported.
	if vr.RunErr != nil && (vr.Tier4 == nil || len(vr.Tier4.ResolveDiagnostics) == 0) {
		pipelineDiags = append(pipelineDiags, vr.RunErr.Error())
	}
	for _, f := range vr.ResolutionFindings {
		pipelineDiags = append(pipelineDiags, f.Error())
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
			fmt.Fprintf(diagOut, "  input-kind: %s\n", e)
		}
	} else {
		// The concise form collapses the verdict-affecting diagnostics — the
		// pipeline diagnostics followed by the input-kind errors, in gate order —
		// into a count and the first, and emits no advisory lines.
		if total := len(pipelineDiags) + len(vr.InputKindErrors); total > 0 {
			first := ""
			if len(pipelineDiags) > 0 {
				first = pipelineDiags[0]
			} else {
				first = "input-kind: " + vr.InputKindErrors[0].Error()
			}
			fmt.Fprintf(diagOut, "release dependency validation: %d diagnostic(s); first: %s\n", total, first)
		}
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

// isCancellation reports whether err is a shutdown-drain cancellation, for which
// validation yields no diagnostics.
func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
