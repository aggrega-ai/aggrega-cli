// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Negative-authoring corpus — build surface. Drives violating triad
// documents (Aggregate / Manifest / Release) and the four Invariant-5
// bans through the REAL build pipeline (pipelinetest.BuildE), asserting
// the envelope sentinel via errors.Is. Plus one pillar-level canary
// proving the CONTENT gate (ErrSchemaConformance) also stays loud — one
// row, not a parallel corpus (the pillar corpus already exists).
//
// writeMinimalWorkspace lays down a base workspace that builds CLEAN;
// each row overrides exactly ONE document with a single-fault variant.
// TestNegativeCorpusBuild_BaseValid is the positive control: the
// unmodified base must build green, so each violation row fails on its
// OWN fault and not on an unintended second defect in the base.
package compiler_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// minimal workspace documents — a single api Component on the compose
// provider, wired through a one-line Aggregate / Manifest / Release. The
// base builds green (proven by the positive control below).
const (
	baseAggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: corpus-app
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server
      values:
        port: 8080
`
	baseManifest = `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: corpus-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server: server-recipe:1.0.0
`
	baseRelease = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: corpus-rel
  environment: dev
  version: "1.0.0"
spec:
  aggregate: corpus-app
  manifest: corpus-manifest
  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          infra:
            replicas: 1
`
	baseComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port: { type: integer }
`
	baseProvider = `apiVersion: aggrega.ai/v1alpha1
kind: Provider
metadata:
  name: compose
  version: "1.0.0"
spec:
  plugin: "builtin:compose"
`
	baseRecipe = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  target: compose
  implements: core/server
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: x:1
        ports:
          p: 8080
`
	// baseComposeRendererPlugin binds the compose provider to the embedded compose
	// rendering guest through the plugin-resolution cascade's embed tier (the
	// digest-free spec.plugin identity resolves, keyed on the repository
	// reference, to the exact bytes compiled into the binary), so the minimal base
	// workspace renders through the compose guest.
	baseComposeRendererPlugin = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: compose-renderer
  version: "1.0.0"
spec:
  plugin: oci://hub.aggrega.ai/plugins/core/compose-provider
  pluginType: provider
  abiVersion: v1alpha1
  targets:
    - compose
  exports:
    - name: render_node
    - name: assemble_boundary
  runtime:
    maxMemoryPages: 4096
    timeoutMs: 300000
`
)

// workspaceFiles names the four overridable documents.
type workspaceFiles struct {
	aggregate, manifest, release, component string
}

func baseWorkspaceFiles() workspaceFiles {
	return workspaceFiles{
		aggregate: baseAggregate,
		manifest:  baseManifest,
		release:   baseRelease,
		component: baseComponent,
	}
}

// writeMinimalWorkspace lays the files down in a temp dir and returns the
// pipelinetest.Input that builds it. Catalog (component + provider +
// recipe) sits under catalog/; the triad sits at the root so the
// resolver's same-dir scan finds aggregate + manifest by Release pointer.
func writeMinimalWorkspace(t *testing.T, wf workspaceFiles) pipelinetest.Input {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("aggregate.yaml", wf.aggregate)
	write("manifest.yaml", wf.manifest)
	write("release.yaml", wf.release)
	write("catalog/components/server.yaml", wf.component)
	write("catalog/providers/compose.yaml", baseProvider)
	write("catalog/recipes/server.yaml", baseRecipe)
	write("catalog/plugins/compose-renderer.yaml", baseComposeRendererPlugin)
	return pipelinetest.Input{
		ReleasePath:  filepath.Join(dir, "release.yaml"),
		CatalogPaths: []string{filepath.Join(dir, "catalog")},
	}
}

// TestNegativeCorpusBuild_BaseValid is the positive control: the
// unmodified base workspace builds GREEN. Each violation row below
// overrides one document; this proves the base is clean, so a row's
// failure is attributable to its single injected fault.
func TestNegativeCorpusBuild_BaseValid(t *testing.T) {
	t.Parallel()
	in := writeMinimalWorkspace(t, baseWorkspaceFiles())
	if _, err := pipelinetest.BuildE(t, in); err != nil {
		t.Fatalf("base workspace must build clean (positive control); got: %v", err)
	}
}

type buildRow struct {
	name    string
	mutate  func(*workspaceFiles)
	want    error
	wantMsg string // optional substring, only where no sentinel fits
}

func buildCorpusRows() []buildRow {
	return []buildRow{
		// --- cross-surface pair 1 (build leg): the SAME catalog-Component
		// unknown-key violation the lint table asserts via lintCatalogWide.
		// Build (catalog load) + lint together evidence the one gate across
		// the two surfaces that take a catalog-entity subject. ---
		{name: "xsurface-component-unknown-key", want: envelope.ErrEntityEnvelope, mutate: func(wf *workspaceFiles) {
			wf.component = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  bogusKey: nope
  schema:
    values:
      port: { type: integer }
`
		}},
		// --- cross-surface pair 2 (build leg): the SAME Aggregate
		// component-infra envelope violation the serve row asserts as a
		// daemon diagnostic. Build + serve together evidence the one gate
		// across the two surfaces that take an Aggregate subject. ---
		{name: "xsurface-aggregate-component-infra", want: envelope.ErrEntityEnvelope, mutate: func(wf *workspaceFiles) {
			wf.aggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: corpus-app
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server
      infra:
        port: 8080
`
		}},
		// --- triad envelope classes ---
		// Default-scopes mutual exclusivity (Aggregate side, symmetric to the
		// Release XOR): top-level components AND named subsystems both non-empty
		// is rejected at the envelope gate's not/allOf. No new sentinel.
		{name: "aggregate-components-and-subsystems-both", want: envelope.ErrEntityEnvelope, mutate: func(wf *workspaceFiles) {
			wf.aggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: corpus-app
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server
      values:
        port: 8080
  subsystems:
    extra:
      components:
        worker:
          type: core/server
`
		}},
		{name: "aggregate-unknown-spec-key", want: envelope.ErrEntityEnvelope, mutate: func(wf *workspaceFiles) {
			wf.aggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: corpus-app
  version: "1.0.0"
spec:
  bogusKey: nope
  components:
    api:
      type: core/server
      values:
        port: 8080
`
		}},
		{name: "manifest-missing-required-mappings", want: envelope.ErrEntityEnvelope, mutate: func(wf *workspaceFiles) {
			wf.manifest = `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: corpus-manifest
  version: "1.0.0"
spec: {}
`
		}},
		{name: "release-unknown-spec-key", want: envelope.ErrEntityEnvelope, mutate: func(wf *workspaceFiles) {
			wf.release = injectReleaseSpecKey()
		}},
		// Default-scopes mutual exclusivity: a Release declaring BOTH a
		// top-level components surface AND named boundaries is rejected at the
		// envelope gate (the schema's not/allOf XOR). No new sentinel — the
		// envelope reject carries it.
		{name: "release-components-and-boundaries-both", want: envelope.ErrEntityEnvelope, mutate: func(wf *workspaceFiles) {
			wf.release = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: corpus-rel
  environment: dev
  version: "1.0.0"
spec:
  aggregate: corpus-app
  manifest: corpus-manifest
  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          infra:
            replicas: 1
  components:
    api:
      infra:
        replicas: 1
`
		}},
		// --- Invariant-5 ban set (permanent rows, each loud) ---
		{name: "inv5-release-values-on-boundary-component", want: envelope.ErrEntityEnvelope, mutate: func(wf *workspaceFiles) {
			wf.release = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: corpus-rel
  environment: dev
  version: "1.0.0"
spec:
  aggregate: corpus-app
  manifest: corpus-manifest
  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          values:
            port: 9999
`
		}},
		{name: "inv5-release-aggregate-with-extends", want: envelope.ErrEntityEnvelope, mutate: func(wf *workspaceFiles) {
			wf.release = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: corpus-rel
  environment: dev
  version: "1.0.0"
spec:
  extends: parent-rel
  aggregate: corpus-app
  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          infra:
            replicas: 1
`
		}},
		{name: "inv5-release-values-on-edge", want: envelope.ErrEntityEnvelope, mutate: func(wf *workspaceFiles) {
			wf.release = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: corpus-rel
  environment: dev
  version: "1.0.0"
spec:
  aggregate: corpus-app
  manifest: corpus-manifest
  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          infra:
            replicas: 1
          uses:
            databases:
              main:
                target: api
                values:
                  protocol: grpc
`
		}},
		{name: "inv5-from-family-on-boundary-component", want: envelope.ErrEntityEnvelope, mutate: func(wf *workspaceFiles) {
			wf.release = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: corpus-rel
  environment: dev
  version: "1.0.0"
spec:
  aggregate: corpus-app
  manifest: corpus-manifest
  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          valueFrom: somewhere
          infra:
            replicas: 1
`
		}},
		// --- pillar (content) gate canary: one row, ErrSchemaConformance ---
		{name: "pillar-canary-value-type-mismatch", want: resolver.ErrSchemaConformance, mutate: func(wf *workspaceFiles) {
			// port is declared integer; author a string. Envelope passes
			// (values is an open map); the Step-1 content gate rejects.
			wf.aggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: corpus-app
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server
      values:
        port: "not-an-integer"
`
		}},
	}
}

func injectReleaseSpecKey() string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: corpus-rel
  environment: dev
  version: "1.0.0"
spec:
  bogusKey: nope
  aggregate: corpus-app
  manifest: corpus-manifest
  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          infra:
            replicas: 1
`
}

// TestNegativeCorpusBuild_Violations drives each single-fault workspace
// through the real build pipeline (BuildE) and asserts the named
// sentinel via errors.Is — never a ValidateBytes shortcut, never a
// message-only assertion where a sentinel exists.
func TestNegativeCorpusBuild_Violations(t *testing.T) {
	t.Parallel()
	for _, r := range buildCorpusRows() {
		r := r
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			wf := baseWorkspaceFiles()
			r.mutate(&wf)
			in := writeMinimalWorkspace(t, wf)
			_, err := pipelinetest.BuildE(t, in)
			if err == nil {
				t.Fatalf("%s: expected the build to halt; got nil", r.name)
			}
			if !errors.Is(err, r.want) {
				t.Fatalf("%s: want errors.Is(err, %v); got %v", r.name, r.want, err)
			}
		})
	}
}
