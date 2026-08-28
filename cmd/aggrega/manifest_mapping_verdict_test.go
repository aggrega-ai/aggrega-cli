// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package main — end-to-end proof of the per-surface verdict on an inconsistent
// Manifest mapping: the two artifact-producing commands halt, and each does so
// from its own call site.

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/lintops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// mappingVerdictWorkspace writes a workspace whose Manifest maps core/server onto
// the recipe named by recipeURI. Two recipes are always present — one
// implementing core/server, one implementing an unrelated type — so a caller
// chooses a consistent or an inconsistent mapping without changing anything else
// about the fixture.
func mappingVerdictWorkspace(t *testing.T, recipeURI string) (root, releasePath string, catalogDirs []string) {
	t.Helper()
	root = t.TempDir()

	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: mapping-verdict
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server
      values:
        port: 8080
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: mapping-verdict-rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          infra:
            replicas: 1
`)
	writeMappingVerdictManifest(t, root, recipeURI)

	catalogRoot := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogRoot, "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`)
	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "providers"))
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/server
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: {{ .infra.replicas | default 1 }}
    containers:
      server:
        image: test-server:latest
        ports:
          web: {{ .values.port }}
`)
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "elsewhere.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: elsewhere-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/somewhere-else
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      server:
        image: test-server:latest
        ports:
          web: {{ .values.port }}
`)

	return root, filepath.Join(root, "release.yaml"), []string{catalogRoot}
}

// writeMappingVerdictManifest rewrites just the Manifest, so a test can prime a
// lockfile against a consistent mapping and then make the mapping inconsistent.
func writeMappingVerdictManifest(t *testing.T, root, recipeURI string) {
	t.Helper()
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: mapping-verdict-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server: `+recipeURI+`
`)
}

// TestPull_InconsistentMappingHalts pins that pull keeps its OWN consistency call
// site. pull does not route through the shared closure resolver, so the
// unconditional gate the resolver runs cannot cover it: if pull's own call were
// removed as redundant, this test is what refuses.
func TestPull_InconsistentMappingHalts(t *testing.T) {
	root, releasePath, catalogDirs := mappingVerdictWorkspace(t, "elsewhere-recipe:1.0.0")
	t.Chdir(root)

	err := pullRun(pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: catalogDirs,
		DiagOut:     &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("pull must halt on a Manifest mapping the catalog contradicts")
	}
	if !errors.Is(err, catalog.ErrManifestImplementsMismatch()) {
		t.Fatalf("expected ErrManifestImplementsMismatch from pull; got %v", err)
	}
}

// TestBuild_InconsistentMappingHalts pins the build's own verdict on the findings
// the shared resolver now hands back. An artifact-producing command halts:
// rendering a component through a recipe that does not implement its type would
// emit an artifact nobody asked for.
//
// The lockfile is primed against a CONSISTENT mapping and the Manifest is made
// inconsistent afterwards. That isolation is the point: pull carries its own gate,
// so a fixture that was inconsistent from the start would halt inside the priming
// pull and never reach the build — and the test would pass while proving nothing
// about the build. Priming first is what makes the build's own site the only thing
// that can refuse here.
func TestBuild_InconsistentMappingHalts(t *testing.T) {
	root, releasePath, catalogDirs := mappingVerdictWorkspace(t, "server-recipe:1.0.0")
	t.Chdir(root)

	if err := pullRun(pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: catalogDirs,
		DiagOut:     &bytes.Buffer{},
	}); err != nil {
		t.Fatalf("priming pull over a consistent mapping must succeed: %v", err)
	}

	// Now make the mapping inconsistent, leaving everything else in place.
	writeMappingVerdictManifest(t, root, "elsewhere-recipe:1.0.0")

	err := dispatchBuild(resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		releasePath, catalogDirs, false, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("build must halt on a Manifest mapping the catalog contradicts")
	}
	if !errors.Is(err, catalog.ErrManifestImplementsMismatch()) {
		t.Fatalf("expected ErrManifestImplementsMismatch from build; got %v", err)
	}
	// The halt precedes any write: a refused build leaves no artifacts behind.
	if _, statErr := os.Stat(filepath.Join(root, "dist")); statErr == nil {
		t.Error("a refused build must write no artifacts, but dist/ exists")
	}
}

// TestBuild_ConsistentMappingUnaffected is the matched control for the two halts
// above: the identical fixture with the consistent mapping builds. Without it, a
// change that made the gate refuse everything would leave both halt tests green.
func TestBuild_ConsistentMappingUnaffected(t *testing.T) {
	root, releasePath, catalogDirs := mappingVerdictWorkspace(t, "server-recipe:1.0.0")
	t.Chdir(root)

	if err := pullRun(pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: catalogDirs,
		DiagOut:     &bytes.Buffer{},
	}); err != nil {
		t.Fatalf("pull over a consistent mapping must succeed: %v", err)
	}
	err := dispatchBuild(resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		releasePath, catalogDirs, false, &bytes.Buffer{}, &bytes.Buffer{})
	if errors.Is(err, catalog.ErrManifestImplementsMismatch()) {
		t.Fatalf("a consistent mapping must not trip the gate; got %v", err)
	}
}

// TestRenderReleaseValidation_MappingFindingAdvisesWithNonZeroVerdict pins
// validate's ruled posture, and the distinction that makes it worth stating: the
// finding is advisory in POSITION — it is enumerated beside every other
// diagnostic rather than replacing the report — and never in VERDICT.
//
// Both halves are asserted because each covers a different regression: reporting
// it and exiting zero would make the finding cosmetic, and halting on it would
// truncate the report a reporting verb exists to produce.
func TestRenderReleaseValidation_MappingFindingAdvisesWithNonZeroVerdict(t *testing.T) {
	findings := []error{errors.New("manifest mapping is inconsistent")}
	// A second, unrelated diagnostic, so "did not truncate" is observable rather
	// than assumed.
	vr := &compilerops.ValidateResult{
		ResolutionFindings: []error{errors.New("an unrelated pipeline diagnostic")},
	}

	var out bytes.Buffer
	err := renderReleaseValidation(vr, findings, "release.yaml", compilerops.VerbosityDetailed, &out, &out)
	if err == nil {
		t.Fatal("advise is not exit zero: a mapping finding must still yield a non-zero verdict")
	}
	got := out.String()
	if !contains(got, "manifest mapping is inconsistent") {
		t.Errorf("the mapping finding must be reported; got:\n%s", got)
	}
	if !contains(got, "an unrelated pipeline diagnostic") {
		t.Errorf("the report must not be truncated by the mapping finding; got:\n%s", got)
	}

	// The clean control: no findings, no mapping verdict.
	var cleanOut bytes.Buffer
	if cerr := renderReleaseValidation(&compilerops.ValidateResult{}, nil, "release.yaml",
		compilerops.VerbosityDetailed, &cleanOut, &cleanOut); cerr != nil {
		t.Fatalf("no findings must yield a nil verdict; got %v", cerr)
	}
}

// TestLintRelease_MappingFindingAdvisesWithNonZeroVerdict pins lint's ruled
// posture on the same axis validate's test pins: the conventions pass the author
// came for still runs and still reports, and the verdict is still non-zero.
//
// The distinction lint makes and validate does not: a convention finding is
// advisory in verdict too — conventions never move the exit code — so this
// finding cannot simply inherit the surrounding surface's posture. It is not a
// convention finding, and the test is what holds it apart from one.
func TestLintRelease_MappingFindingAdvisesWithNonZeroVerdict(t *testing.T) {
	root, releasePath, catalogDirs := mappingVerdictWorkspace(t, "elsewhere-recipe:1.0.0")
	t.Chdir(root)
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})

	var out, diagOut bytes.Buffer
	err := lintops.RunRelease(rw, releasePath, catalogDirs, &out, &diagOut, nil)
	if err == nil {
		t.Fatal("advise is not exit zero: lint must still return a non-zero verdict for the mapping")
	}
	if !contains(diagOut.String(), "spec.implements") {
		t.Errorf("lint must report the mapping finding on its diagnostic stream; got:\n%s", diagOut.String())
	}
	// The pass itself ran to completion rather than being pre-empted: its summary
	// reached the report writer. This is the non-truncation half — a halt ahead of
	// the pass would leave this empty.
	if out.Len() == 0 {
		t.Errorf("lint must complete its conventions pass and report it, not halt ahead of it; out was empty, diag:\n%s",
			diagOut.String())
	}

	// The matched control: the same fixture with a consistent mapping lints clean,
	// so the non-zero verdict above is attributable to the mapping and not to the
	// fixture merely failing to lint.
	cleanRoot, cleanRelease, cleanDirs := mappingVerdictWorkspace(t, "server-recipe:1.0.0")
	t.Chdir(cleanRoot)
	cleanRW := resolveTempWorkspace(t, tempWorkspaceOptions{Root: cleanRoot})
	var cleanOut, cleanDiag bytes.Buffer
	if cerr := lintops.RunRelease(cleanRW, cleanRelease, cleanDirs, &cleanOut, &cleanDiag, nil); cerr != nil {
		t.Fatalf("a consistent mapping must lint without a mapping verdict; got %v\ndiag:\n%s", cerr, cleanDiag.String())
	}
}

// contains avoids a strings import in this file alone.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
