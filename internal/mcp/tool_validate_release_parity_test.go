// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

// Release-scope single-source parity. The validate tool's release scope, the
// CLI validate verb, and the serve daemon's Tier-4 validation all dispatch to
// ONE importable core — compilerops.ResolveClosure + compilerops.Validate,
// producing a *compilerops.ValidateResult. These tests prove the MCP half of the
// transitive argument: the release-scope payload carries that core's verdict and
// diagnostics VERBATIM, so a re-introduced second orchestration on the MCP side
// would break the DeepEqual assertions here.
//
// The oracle is the core. Each case drives the validate tool over a fixture, then
// calls the SAME core DIRECTLY over the same resolved inputs the tool passes (the
// tool's own resolveToolWorkspace, lockfile load, and view construction), and
// asserts the payload's verdict, diagnostics, and notices equal the core's. The
// fixture cases each isolate one diagnostic class: a clean closure, the
// closure-scoped extends gate, the closure-scoped schema-pillar gate, the
// resolve/analyze pipeline, the resolution-completeness findings, the input-kind
// capability class (the load-bearing proof that the shared result is a superset
// every front end renders), and the Manifest mapping table — which is the one class
// decided by the closure resolution rather than by a ValidateResult field, so it is
// what holds the tool to reading the resolution's findings too.
//
// The fixture YAML is duplicated across the CLI and serve legs deliberately: each
// leg constructs the fixture independently and independently calls the core, so a
// drift in any one leg's construction is caught rather than masked by a shared
// helper. This mirrors the independent-oracle discipline the catalog-scope parity
// test uses when it re-derives diagnostic prefixes rather than routing through the
// production constant.

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
)

// fullstackCatalogDir returns the shared clean catalog the release-parity fixtures
// resolve their standard entities against; per-case overlays shadow it first-win.
func fullstackCatalogDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(moduleRoot(t), "internal", "compiler", "testdata", "fullstack-app", "catalog")
}

// releaseParityCase is one fixture: the three input files plus an optional catalog
// overlay whose entries shadow the shared catalog first-win. An empty release
// defaults to releaseParityDefaultRelease.
type releaseParityCase struct {
	name      string
	aggregate string
	manifest  string
	release   string
	overlay   map[string]string
}

const releaseParityBackendAggregate = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: parity-app
  version: "1.0.0"
spec:
  components:
    backend:
      type: core/compute/api-server
`

const releaseParityCleanManifest = `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: parity-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/compute/api-server: helm-api-server-synthesized-with-artifacts:1.0.0
`

const releaseParityEmptyManifest = `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: parity-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type: {}
`

// releaseParityMappingInconsistentManifest maps the api-server type onto the
// web-ui recipe, whose spec.implements names a different type. Both entities live
// in the shared catalog, so the mapping resolves and is judged rather than skipped
// — which is what makes the resulting finding real.
const releaseParityMappingInconsistentManifest = `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: parity-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/compute/api-server: helm-web-ui-synthesized-with-artifacts:1.0.0
`

const releaseParityDefaultRelease = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: parity-release
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

const releaseParityEmptyBoundaryRelease = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: parity-release
  environment: test
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    app:
      directory: ./dist/app
      components: {}
`

// releaseParityExtendsOverlayComponent shadows the standard api-server with one
// whose spec.extends names a parent absent from the catalog — a dangling extends
// the closure-scoped extends gate reports.
const releaseParityExtendsOverlayComponent = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/compute/api-server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  extends: local/non-existent-base:1.0.0
  inject: ["env"]
  schema: inherited
`

// releaseParityPillarOverlayComponent shadows the standard api-server with one
// whose infra pillar $refs a SchemaLibrary absent from the catalog — a schema
// compile failure the closure-scoped schema-pillar gate reports.
const releaseParityPillarOverlayComponent = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/compute/api-server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  inject: ["env"]
  schema:
    infra:
      storage:
        $ref: "Nonexistent#/schemas/Storage"
    provides:
      host: { type: string, required: true }
      service_port: { type: string, required: true }
`

const releaseParityResolveFailingAggregate = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: parity-app
  version: "1.0.0"
spec:
  parameters:
    database_url:
      type: string
      required: true
  components: {}
`

const releaseParityInputKindManifest = `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: parity-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/compute/api-server: api-sourceonly:1.0.0
`

const releaseParityArmInputKindManifest = `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: parity-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/compute/api-server: api-arm:1.0.0
`

// releaseParityArmInputKindRecipe declares a synthesized workload but binds the
// arm capability, whose embedded provider takes sourced and artifact input only.
const releaseParityArmInputKindRecipe = `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: api-arm
  version: "1.0.0"
spec:
  capability: arm
  implements: core/compute/api-server
  provides:
    host: "{{ .context.instance_id }}.svc"
    service_port: "8080"
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: x:latest
`

// releaseParityInputKindRecipe declares a synthesized workload but binds a
// capability whose serving plugin supports only the sourced input kind, so the
// input-kind capability check reports it.
const releaseParityInputKindRecipe = `apiVersion: aggrega.ai/v1beta1
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
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: x:latest
`

const releaseParityInputKindPlugin = `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: sourced-only-renderer
  pluginType: provider
  version: "1.0.0"
spec:
  abiVersion: v1beta1
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

// releaseParityCases enumerates the fixture cases every leg shares. Each
// isolates one ValidateResult field so the parity assertions read cleanly.
func releaseParityCases() []releaseParityCase {
	return []releaseParityCase{
		{
			name:      "clean",
			aggregate: releaseParityBackendAggregate,
			manifest:  releaseParityCleanManifest,
		},
		{
			name:      "extends-broken",
			aggregate: releaseParityBackendAggregate,
			manifest:  releaseParityCleanManifest,
			overlay:   map[string]string{"components/api-server.yaml": releaseParityExtendsOverlayComponent},
		},
		{
			name:      "pillar-invalid",
			aggregate: releaseParityBackendAggregate,
			manifest:  releaseParityCleanManifest,
			overlay:   map[string]string{"components/api-server.yaml": releaseParityPillarOverlayComponent},
		},
		{
			name:      "resolve-failing",
			aggregate: releaseParityResolveFailingAggregate,
			manifest:  releaseParityEmptyManifest,
			release:   releaseParityEmptyBoundaryRelease,
		},
		{
			name:      "resolution-incomplete",
			aggregate: releaseParityBackendAggregate,
			manifest:  releaseParityEmptyManifest,
		},
		{
			name:      "input-kind-capability-failing",
			aggregate: releaseParityBackendAggregate,
			manifest:  releaseParityInputKindManifest,
			overlay: map[string]string{
				"recipes/api-sourceonly.yaml":        releaseParityInputKindRecipe,
				"plugins/sourced-only-renderer.yaml": releaseParityInputKindPlugin,
			},
		},
		// The same class against the arm provider the binary embeds: the Recipe
		// declares a synthesized workload, and the embedded arm contract accepts
		// no synthesized input.
		{
			name:      "arm-input-kind-capability-failing",
			aggregate: releaseParityBackendAggregate,
			manifest:  releaseParityArmInputKindManifest,
			overlay:   map[string]string{"recipes/api-arm.yaml": releaseParityArmInputKindRecipe},
		},
		// The mapping-table class. It is the one case here whose finding is decided
		// by the closure RESOLUTION rather than by a ValidateResult gate, so it is
		// also the case that holds the tool to reading the finding off the resolved
		// closure: a tool that mapped only ValidateResult would report this Release
		// as valid.
		{
			name:      "mapping-inconsistent",
			aggregate: releaseParityBackendAggregate,
			manifest:  releaseParityMappingInconsistentManifest,
		},
	}
}

// writeReleaseParityWorkspace materialises a case into a fresh tempdir and returns
// the workspace root and the first-win catalog search stack (overlay, then the
// shared catalog).
func writeReleaseParityWorkspace(t *testing.T, c releaseParityCase) (root string, dirs []string) {
	t.Helper()
	root = t.TempDir()
	writeCatalogFile(t, filepath.Join(root, "aggregate.yaml"), c.aggregate)
	writeCatalogFile(t, filepath.Join(root, "manifest.yaml"), c.manifest)
	rel := c.release
	if rel == "" {
		rel = releaseParityDefaultRelease
	}
	writeCatalogFile(t, filepath.Join(root, "release.yaml"), rel)
	dirs = []string{fullstackCatalogDir(t)}
	if len(c.overlay) > 0 {
		overlay := filepath.Join(root, "overlay")
		for rel, body := range c.overlay {
			writeCatalogFile(t, filepath.Join(overlay, rel), body)
		}
		dirs = append([]string{overlay}, dirs...)
	}
	return root, dirs
}

// TestValidateToolReleaseScopeCoreParity proves the release-scope MCP payload
// carries the shared core's verdict, ordered diagnostics, and advisory notices
// verbatim across every fixture case. The core is called directly over the same
// resolved inputs the tool passes, so the payload cannot pass by drift.
//
// The case count is deliberately not written here. releaseParityCases is the
// statement of how many there are; a number repeated in prose beside it only
// resets the clock until the next case is added.
func TestValidateToolReleaseScopeCoreParity(t *testing.T) {
	for _, c := range releaseParityCases() {
		c := c
		t.Run(c.name, func(t *testing.T) {
			s := newTestServer(t)
			root, dirs := writeReleaseParityWorkspace(t, c)
			releasePath := filepath.Join(root, "release.yaml")

			res := callTool(t, s, "validate", validateArgs{
				Release:       releasePath,
				WorkspaceRoot: root,
				CatalogDirs:   dirs,
			})
			if res.IsError {
				t.Fatalf("release scope reported protocol error: %s", res.Content[0].Text)
			}
			payload := unmarshalValidatePayload(t, res)

			// Oracle: the same core over the same resolved inputs validateReleaseScope
			// builds internally (resolveToolWorkspace, lockfile load, view, closure).
			tw := resolvedToolWorkspace(t, s, root, dirs)
			lf, lockPins, lockRefused := tw.loadReleaseLockfile(releasePath)
			if lockRefused != "" {
				t.Fatalf("read the oracle's lockfile: %s", lockRefused)
			}
			view := tw.newView(releasePath, lockPins)
			resolved, cerr := compilerops.ResolveClosure(context.Background(), compilerops.ClosureInputs{
				View:            view,
				ReleasePath:     releasePath,
				WorkspaceRoot:   tw.root,
				WorkspaceConfig: tw.cfg,
			})
			if cerr != nil {
				t.Fatalf("oracle ResolveClosure failed: %v", cerr)
			}
			vr := compilerops.Validate(context.Background(), compilerops.ValidateInputs{
				View:            view,
				Closure:         resolved.Closure,
				Release:         resolved.Release,
				ReleasePath:     releasePath,
				Manifest:        resolved.Manifest,
				AggBytes:        resolved.AggBytes,
				AggregatePath:   resolved.AggregatePath,
				WorkspaceRoot:   tw.root,
				WorkspaceConfig: tw.cfg,
				CompilerVersion: s.serverVersion,
			})

			// The mapping findings come off the resolved closure, exactly as
			// validateReleaseScope reads them, so the oracle mirrors production
			// rather than a subset of it.
			wantDiags := validateDiagnostics(vr, resolved.ManifestConsistencyFindings)
			deprecations, trustRefused := tw.releaseDeprecations(lf, releasePath)
			if trustRefused != "" {
				t.Fatalf("read the oracle's withdrawal report: %s", trustRefused)
			}
			wantNotices := releaseAdvisories(vr, deprecations)

			if c.name == "clean" {
				if len(wantDiags) != 0 {
					t.Fatalf("clean fixture is not clean: core diagnostics %#v", wantDiags)
				}
			} else if len(wantDiags) == 0 {
				t.Fatalf("case %q produced no core diagnostics; the fixture no longer exercises its class", c.name)
			}

			if payload.Valid != (len(wantDiags) == 0) {
				t.Fatalf("verdict diverges from core: payload.Valid=%v, core diagnostics=%#v", payload.Valid, wantDiags)
			}
			if !reflect.DeepEqual(payload.Diagnostics, wantDiags) {
				t.Fatalf("diagnostics diverge from core:\n payload: %#v\n core:    %#v", payload.Diagnostics, wantDiags)
			}
			if !reflect.DeepEqual(payload.Notices, wantNotices) {
				t.Fatalf("notices diverge from core:\n payload: %#v\n core:    %#v", payload.Notices, wantNotices)
			}

			assertReleaseRenderVerdictInvariant(t, payload)
		})
	}
}

// assertReleaseRenderVerdictInvariant proves verbosity governs output VOLUME only:
// the detailed and concise release-scope renders reach the SAME verdict (both
// derive from payload.Valid), the detailed form lists every diagnostic while the
// concise form carries only the first, and the concise form never emits an
// advisory notice line.
func assertReleaseRenderVerdictInvariant(t *testing.T, p validateResultPayload) {
	t.Helper()
	detailed := renderValidateText(p, responseFormatDetailed)
	concise := renderValidateText(p, responseFormatConcise)

	if p.Valid {
		for _, form := range []string{detailed, concise} {
			if !strings.Contains(form, "validated clean") {
				t.Errorf("valid payload rendered a non-clean verdict:\n%s", form)
			}
		}
		if strings.Contains(concise, "notice:") {
			t.Errorf("concise render carried an advisory line:\n%s", concise)
		}
		return
	}

	verdict := fmt.Sprintf("has %d validation diagnostic(s)", len(p.Diagnostics))
	for _, form := range []string{detailed, concise} {
		if !strings.Contains(form, verdict) {
			t.Errorf("invalid payload rendered a divergent verdict (want %q):\n%s", verdict, form)
		}
	}
	for _, d := range p.Diagnostics {
		if !strings.Contains(detailed, d) {
			t.Errorf("detailed render dropped diagnostic %q:\n%s", d, detailed)
		}
	}
	if !strings.Contains(concise, p.Diagnostics[0]) {
		t.Errorf("concise render dropped the first diagnostic %q:\n%s", p.Diagnostics[0], concise)
	}
	if len(p.Diagnostics) >= 2 && strings.Contains(concise, p.Diagnostics[1]) {
		t.Errorf("concise render leaked a non-first diagnostic (volume not reduced):\n%s", concise)
	}
	if strings.Contains(concise, "notice:") {
		t.Errorf("concise render carried an advisory line:\n%s", concise)
	}
}
