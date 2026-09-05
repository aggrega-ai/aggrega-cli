// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

// The resolution-completeness gate's ATTRIBUTION is a property of this layer, so
// it is pinned here rather than at a surface. The gate is supplied to the
// pipeline as an option, which means a run that does not supply it still fails on
// an unmapped instance — deeper down, naming an internal construction step. What
// the gate buys is therefore not acceptance but attribution: the difference
// between a failure an author can act on and one they cannot.
//
// Showing that requires running the same fixture with the gate off, which is a
// posture no operation offers and no caller outside this package can assemble.
// That is the enforcement working as intended, and it is why the pair lives here:
// the package that owns a gate is the one that can test it in isolation.
//
// The fixture is written here rather than shared with the surface tests that
// drive the same fault. A test helper cannot cross a package boundary without a
// non-test export, and an exported seam for a posture is reachable by production
// code — which would give back exactly the caller-side choice the entry points
// removed.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// unmappedInstanceAggregate declares one component whose type the manifest below
// maps to nothing.
const unmappedInstanceAggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: attribution-app
  version: "1.0.0"
spec:
  components:
    backend:
      type: core/compute/api-server
`

// unmappedInstanceManifest carries an empty component mapping table, so the
// instance the Aggregate declares resolves to no recipe at all.
const unmappedInstanceManifest = `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: attribution-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type: {}
`

const unmappedInstanceRelease = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: attribution-release
  environment: test
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    app:
      directory: ./dist/app
      components:
        backend:
          infra:
            port: 8080
`

// unmappedInstanceWorkspace materialises the fault into a fresh tempdir, resolved
// against the same shared catalog fixtureWorkspace reads. Only the Release, the
// Aggregate and the Manifest are local; every entity they reach comes from the
// shared catalog, so the fixture states the fault and nothing else.
func unmappedInstanceWorkspace(t *testing.T) (view *catalog.LazyLookup, releasePath, root string, wsCfg workspaceconfig.Config) {
	t.Helper()

	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs cwd: %v", err)
	}
	// This test runs from internal/compilerops/; the module root is two dirs up.
	moduleRoot := filepath.Dir(filepath.Dir(cwd))
	catalogDir := filepath.Join(moduleRoot, "internal", "compiler", "testdata", "fullstack-app", "catalog")

	root = t.TempDir()
	for name, body := range map[string]string{
		"aggregate.yaml": unmappedInstanceAggregate,
		"manifest.yaml":  unmappedInstanceManifest,
		"release.yaml":   unmappedInstanceRelease,
	} {
		if werr := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); werr != nil {
			t.Fatalf("write %s: %v", name, werr)
		}
	}

	wsCfg = workspaceconfig.Default()
	lazy := catalog.NewLazyLookup(root, wsCfg.ResolvedArtifactCacheDir(), []string{catalogDir}, nil)
	return lazy.ForRequest(nil), filepath.Join(root, "release.yaml"), root, wsCfg
}

// runUnmappedInstance drives the fixture through the shared computation under a
// caller-supplied posture. Only this package can call it, which is the point.
func runUnmappedInstance(t *testing.T, pol previewPolicy) error {
	t.Helper()
	view, releasePath, root, wsCfg := unmappedInstanceWorkspace(t)
	_, err := runPreview(
		context.Background(),
		NewPreviewInputs(NoExternalInputs(), view, releasePath, root, wsCfg, "test"),
		pol,
		pipeline.Options{},
	)
	return err
}

// TestUnmappedInstanceIsAttributedToTheResolutionGate pins what the
// resolution-completeness gate produces: a refusal naming recipe resolution, which
// is the fault the author can act on.
//
// Pinned to the gate by the condition it raises rather than by a summary
// sentence: the sentence is presentation and free to change, while the condition
// is what the gate IS.
func TestUnmappedInstanceIsAttributedToTheResolutionGate(t *testing.T) {
	err := runUnmappedInstance(t, artifactPolicy())
	if err == nil {
		t.Fatal("an unmapped instance must refuse under the artifact-producing posture; " +
			"a success here means the fixture stopped carrying its fault")
	}
	if !strings.Contains(err.Error(), "recipe resolution") {
		t.Fatalf("the refusal must come from the resolution-completeness gate:\n%v", err)
	}
}

// TestUnmappedInstanceMisattributesWithTheResolutionGateOff is the negative
// control, and it is what makes the case above falsifiable. It drives the SAME
// fixture with the resolution-completeness verifier switched off and nothing else
// changed, so whatever differs between the two runs is attributable to that gate.
//
// What differs is the ATTRIBUTION, not the acceptance, and that is worth being
// exact about because the tempting claim is the stronger one. With no verifier
// the run still fails — it reaches the render and dies in the engine factory for a
// node that resolved to no recipe. So the gate does not convert a silent success
// into a refusal here; it converts a downstream failure naming an internal
// construction step into one naming the fault the author can act on.
//
// The assertion is two-sided for that reason: the unverified run must fail, and it
// must fail as something OTHER than recipe resolution. A one-sided assertion would
// pass if the verifier had somehow run anyway.
func TestUnmappedInstanceMisattributesWithTheResolutionGateOff(t *testing.T) {
	pol := artifactPolicy()
	pol.verifyRecipeResolution = false

	err := runUnmappedInstance(t, pol)
	if err == nil {
		t.Fatal("the unmapped instance fails with or without the verifier; a success here means " +
			"the fixture stopped carrying its fault")
	}
	if strings.Contains(err.Error(), "recipe resolution") {
		t.Fatalf("with the verifier off, the failure must NOT be the resolution-completeness gate's — "+
			"this run exists to show what a surface supplying no verifier reports:\n%v", err)
	}
}
