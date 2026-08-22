// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The input-kind-capability fixture: an aggregate whose sole component maps
// (via the manifest) to a Recipe served by a provider plugin that declares only
// the sourced input kind, so the synthesized workload target trips the
// input-kind capability detector and Validate returns a non-empty
// InputKindErrors on a non-cancelled run.
const cancelBackendAggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: cancel-app
  version: "1.0.0"
spec:
  components:
    backend:
      type: core/compute/api-server
`

const cancelInputKindManifest = `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: cancel-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/compute/api-server: api-sourceonly:1.0.0
`

const cancelDefaultRelease = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: cancel-release
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

const cancelInputKindRecipe = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: api-sourceonly
  version: "1.0.0"
spec:
  capability: sourced-only-cap
  implements: core/compute/api-server
  provides:
    host: "{{ .context.instance_id }}.svc"
    service_port: "8080"
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: x:latest
`

const cancelInputKindPlugin = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: sourced-only-renderer
  pluginType: provider
  version: "1.0.0"
spec:
  abiVersion: v1alpha1
  exports:
    - name: render_node
    - name: assemble_boundary
  provider:
    capabilities:
      - sourced-only-cap
    contract:
      inputKinds:
        sourced: true
      boundaryAssembly: nest
`

func writeCancelFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// resolveCancelInputs materialises the input-kind fixture workspace, layers its
// overlay ahead of the shared fullstack catalog (first-win), and resolves the
// closure — the shared inputs both cancellation legs validate over. The Release
// path is returned because validation needs it to locate the record of the plugin
// identities trusted for the workspace's verdict attachments; this fixture writes
// no record, which is the ordinary first-run state and holds nothing.
func resolveCancelInputs(t *testing.T) (view *catalog.LazyLookup, resolved *ResolvedClosure, wsCfg workspaceconfig.Config, releasePath string) {
	t.Helper()

	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs cwd: %v", err)
	}
	// This test runs from internal/compilerops/; the module root is two dirs up.
	moduleRoot := filepath.Dir(filepath.Dir(cwd))
	catalogDir := filepath.Join(moduleRoot, "internal", "compiler", "testdata", "fullstack-app", "catalog")

	root := t.TempDir()
	releasePath = filepath.Join(root, "release.yaml")
	writeCancelFixture(t, filepath.Join(root, "aggregate.yaml"), cancelBackendAggregate)
	writeCancelFixture(t, filepath.Join(root, "manifest.yaml"), cancelInputKindManifest)
	writeCancelFixture(t, releasePath, cancelDefaultRelease)

	overlay := filepath.Join(root, "overlay")
	writeCancelFixture(t, filepath.Join(overlay, "recipes", "api-sourceonly.yaml"), cancelInputKindRecipe)
	writeCancelFixture(t, filepath.Join(overlay, "plugins", "sourced-only-renderer.yaml"), cancelInputKindPlugin)

	// Overlay first, shared catalog second: first-win resolution binds the
	// sourced-only Recipe and its provider plugin ahead of the catalog defaults.
	dirs := []string{overlay, catalogDir}

	wsCfg = workspaceconfig.Default()
	view = catalog.NewLazyLookup(root, "", dirs, nil).ForRequest(nil)

	res, cerr := ResolveClosure(ClosureInputs{
		View:            view,
		ReleasePath:     releasePath,
		WorkspaceRoot:   root,
		WorkspaceConfig: wsCfg,
	})
	if cerr != nil {
		t.Fatalf("ResolveClosure: stage=%d %v", cerr.Stage, cerr)
	}
	return view, res, wsCfg, releasePath
}

// TestValidateCancellationSuppressesFindings proves the cancellation guard
// actually guards: over the SAME resolved inputs, a non-cancelled run surfaces
// the input-kind capability errors the fixture genuinely triggers, while a
// pre-cancelled context suppresses the entire post-pipeline finding block.
func TestValidateCancellationSuppressesFindings(t *testing.T) {
	view, resolved, wsCfg, releasePath := resolveCancelInputs(t)

	inputs := func() ValidateInputs {
		return ValidateInputs{
			View:            view,
			Closure:         resolved.Closure,
			Release:         resolved.Release,
			ReleasePath:     releasePath,
			Manifest:        resolved.Manifest,
			AggBytes:        resolved.AggBytes,
			AggregatePath:   resolved.AggregatePath,
			WorkspaceConfig: wsCfg,
			CompilerVersion: "test",
		}
	}

	// Live branch: the fixture genuinely triggers the input-kind class.
	vr := Validate(context.Background(), inputs())
	if len(vr.InputKindErrors) == 0 {
		t.Fatalf("fixture no longer exercises the input-kind class: InputKindErrors empty; the test would be vacuous")
	}

	// Cancelled branch: the guard suppresses the whole post-pipeline finding block.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	vc := Validate(ctx, inputs())
	if len(vc.InputKindErrors) != 0 {
		t.Fatalf("cancelled run computed InputKindErrors: %v", vc.InputKindErrors)
	}
	if len(vc.LintWarnings) != 0 {
		t.Fatalf("cancelled run computed LintWarnings: %v", vc.LintWarnings)
	}
	if len(vc.NarrowingWarnings) != 0 {
		t.Fatalf("cancelled run computed NarrowingWarnings: %v", vc.NarrowingWarnings)
	}
	if len(vc.ResolutionFindings) != 0 {
		t.Fatalf("cancelled run computed ResolutionFindings: %v", vc.ResolutionFindings)
	}
}
