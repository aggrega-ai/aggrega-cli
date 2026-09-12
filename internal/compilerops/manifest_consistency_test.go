// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// extendsMappingWorkspace builds a request-scoped view over the extends-mapping
// fixture and returns the inputs ResolveClosure needs for the named Release in
// it. Both Releases share one catalog, so the only thing that varies between the
// cases below is the Manifest's mapping value.
func extendsMappingWorkspace(t *testing.T, releaseName string) ClosureInputs {
	t.Helper()
	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs cwd: %v", err)
	}
	workspaceRoot := filepath.Join(cwd, "testdata", "extends-mapping")
	wsCfg := workspaceconfig.Default()
	lazy := catalog.NewLazyLookup(workspaceRoot, wsCfg.ResolvedArtifactCacheDir(),
		[]string{filepath.Join(workspaceRoot, "catalog")}, nil)
	return ClosureInputs{
		View:            lazy.ForRequest(nil),
		ReleasePath:     filepath.Join(workspaceRoot, releaseName),
		WorkspaceRoot:   workspaceRoot,
		WorkspaceConfig: wsCfg,
	}
}

// TestResolveClosure_MappingOntoBaseRecipeProducesNoFinding is the ancestry-wiring
// pin. The Manifest maps a wrapper Component's own type onto the Recipe
// implementing the base that wrapper extends, which is consistent — so the
// resolution must produce no finding AND must not fail.
//
// It discriminates on which ancestry resolver the resolution wires. The gate
// takes the resolver from its caller; the recipe shell carries recipes and
// libraries but no Component index, so a resolution wired to the shell's own
// resolver finds no ancestry and reports every mapping onto a base's Recipe as
// inconsistent. This case is therefore red under that mistake and green only when
// the view's own resolver is used. Its matched positive control below keeps the
// green from being vacuous.
func TestResolveClosure_MappingOntoBaseRecipeProducesNoFinding(t *testing.T) {
	t.Parallel()
	resolved, cerr := ResolveClosure(context.Background(), extendsMappingWorkspace(t, "release-base-recipe.yaml"))
	if cerr != nil {
		t.Fatalf("ResolveClosure must not fail on a consistent mapping: stage=%d %v", cerr.Stage, cerr)
	}
	if n := len(resolved.ManifestConsistencyFindings); n != 0 {
		t.Fatalf("a Recipe implementing the base must satisfy the extending wrapper; got %d finding(s): %v",
			n, resolved.ManifestConsistencyFindings)
	}
}

// TestResolveClosure_InconsistentMappingIsCarriedAsAFinding is the matched
// positive control for the case above, and the pin on the carrying mechanism
// clause the design rests on: an inconsistent mapping must arrive as a FINDING on
// the resolved closure rather than as a resolution failure.
//
// Both halves are asserted, because each covers a distinct regression. A
// resolution that failed here would put every reporting front end back to
// replacing its whole report with one line; a resolution that produced no finding
// would put them back to silence.
func TestResolveClosure_InconsistentMappingIsCarriedAsAFinding(t *testing.T) {
	t.Parallel()
	resolved, cerr := ResolveClosure(context.Background(), extendsMappingWorkspace(t, "release-unrelated-recipe.yaml"))
	if cerr != nil {
		t.Fatalf("an inconsistent mapping must be carried as a finding, not a resolution failure; got stage=%d %v",
			cerr.Stage, cerr)
	}
	if len(resolved.ManifestConsistencyFindings) != 1 {
		t.Fatalf("want exactly one mapping finding; got %d: %v",
			len(resolved.ManifestConsistencyFindings), resolved.ManifestConsistencyFindings)
	}
	// Asserted against the class the fault pins rather than against its text, so a
	// message rewording does not silently retire the coverage.
	if !errors.Is(resolved.ManifestConsistencyFindings[0], catalog.ErrManifestImplementsMismatch()) {
		t.Fatalf("finding must carry ErrManifestImplementsMismatch; got %v",
			resolved.ManifestConsistencyFindings[0])
	}
	// The resolution still delivers everything a caller came for. This is what
	// lets a reporting front end enumerate the finding beside its other output
	// instead of having nothing else to report.
	if resolved.Release == nil || resolved.Manifest == nil || resolved.Aggregate == nil {
		t.Fatalf("a carried finding must leave the resolution complete; got %+v", resolved)
	}
}
