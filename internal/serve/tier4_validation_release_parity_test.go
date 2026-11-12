// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

// Release-scope single-source parity, serve leg. The daemon's runTier4Validation
// maps the SAME shared core the CLI verb and the MCP release scope render —
// compilerops.ResolveClosure + compilerops.Validate. serve has no verbosity (it is
// the editor-diagnostic surface), so these tests prove the two parity properties
// that matter for the daemon: its verdict matches the core (it returns at least
// one error-severity diagnostic exactly when the core reports findings), and each
// core finding's own text reaches a serve diagnostic message. The input-kind case
// is load-bearing — the daemon's mapping must keep its verdict inclusive of the
// input-kind capability class so it reaches the same verdict as the other legs.
//
// The oracle is a direct compilerops call over the same resolved inputs
// runTier4Validation builds internally (the request view, the resolved closure).
// The fixture YAML mirrors the CLI and MCP legs deliberately — independent
// construction per leg catches a drift rather than masking it.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

func serveFullstackCatalogDir(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	moduleRoot := filepath.Dir(filepath.Dir(cwd))
	return filepath.Join(moduleRoot, "internal", "compiler", "testdata", "fullstack-app", "catalog")
}

type serveReleaseParityCase struct {
	name      string
	aggregate string
	manifest  string
	release   string
	overlay   map[string]string
}

const serveBackendAggregate = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: parity-app
  version: "1.0.0"
spec:
  components:
    backend:
      type: core/compute/api-server
`

const serveCleanManifest = `apiVersion: aggrega.ai/v1beta1
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

const serveEmptyManifest = `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: parity-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type: {}
`

const serveDefaultRelease = `apiVersion: aggrega.ai/v1beta1
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

const serveEmptyBoundaryRelease = `apiVersion: aggrega.ai/v1beta1
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

const serveExtendsOverlayComponent = `apiVersion: aggrega.ai/v1beta1
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

const servePillarOverlayComponent = `apiVersion: aggrega.ai/v1beta1
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

// serveUnusablePillarOverlayComponent declares a pillar whose body COMPILES and
// then builds no validator: `items` carries a bare scalar where a schema
// belongs.
//
// It is the companion of servePillarOverlayComponent above and not a duplicate
// of it. That one carries a body the compile itself rejects; this one carries a
// body the compile accepts, so the two exercise the two faults the
// declared-contract condition covers. A row for only the first would leave the
// editor's handling of the second unproven, and the second is the one that used
// to reach no gate at all.
const serveUnusablePillarOverlayComponent = `apiVersion: aggrega.ai/v1beta1
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
      storage: { type: array, items: nonsense }
    provides:
      host: { type: string, required: true }
      service_port: { type: string, required: true }
`

const serveResolveFailingAggregate = `apiVersion: aggrega.ai/v1beta1
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

const serveInputKindManifest = `apiVersion: aggrega.ai/v1beta1
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

const serveInputKindRecipe = `apiVersion: aggrega.ai/v1beta1
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

const serveInputKindPlugin = `apiVersion: aggrega.ai/v1beta1
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

func serveReleaseParityCases() []serveReleaseParityCase {
	return []serveReleaseParityCase{
		{name: "clean", aggregate: serveBackendAggregate, manifest: serveCleanManifest},
		{name: "extends-broken", aggregate: serveBackendAggregate, manifest: serveCleanManifest,
			overlay: map[string]string{"components/api-server.yaml": serveExtendsOverlayComponent}},
		{name: "pillar-invalid", aggregate: serveBackendAggregate, manifest: serveCleanManifest,
			overlay: map[string]string{"components/api-server.yaml": servePillarOverlayComponent}},
		{name: "pillar-unusable", aggregate: serveBackendAggregate, manifest: serveCleanManifest,
			overlay: map[string]string{"components/api-server.yaml": serveUnusablePillarOverlayComponent}},
		{name: "resolve-failing", aggregate: serveResolveFailingAggregate, manifest: serveEmptyManifest,
			release: serveEmptyBoundaryRelease},
		{name: "resolution-incomplete", aggregate: serveBackendAggregate, manifest: serveEmptyManifest},
		{name: "input-kind-capability-failing", aggregate: serveBackendAggregate, manifest: serveInputKindManifest,
			overlay: map[string]string{
				"recipes/api-sourceonly.yaml":        serveInputKindRecipe,
				"plugins/sourced-only-renderer.yaml": serveInputKindPlugin,
			}},
	}
}

func writeServeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeServeReleaseParityWorkspace(t *testing.T, c serveReleaseParityCase) (root string, dirs []string) {
	t.Helper()
	root = t.TempDir()
	writeServeFile(t, filepath.Join(root, "aggregate.yaml"), c.aggregate)
	writeServeFile(t, filepath.Join(root, "manifest.yaml"), c.manifest)
	rel := c.release
	if rel == "" {
		rel = serveDefaultRelease
	}
	writeServeFile(t, filepath.Join(root, "release.yaml"), rel)
	dirs = []string{serveFullstackCatalogDir(t)}
	if len(c.overlay) > 0 {
		overlay := filepath.Join(root, "overlay")
		for rel, body := range c.overlay {
			writeServeFile(t, filepath.Join(overlay, rel), body)
		}
		dirs = append([]string{overlay}, dirs...)
	}
	return root, dirs
}

// serveCoreHasErrors reports whether the shared core's result carries any blocking
// finding — the verdict the daemon must reproduce as at least one error-severity
// diagnostic. Cancellation is not a finding.
func serveCoreHasErrors(vr *compilerops.ValidateResult) bool {
	if len(vr.ClosureExtendsErrors) > 0 || vr.PillarError != nil ||
		vr.VerdictTrustError != nil ||
		len(vr.ResolutionFindings) > 0 || len(vr.InputKindErrors) > 0 {
		return true
	}
	if vr.Tier4 != nil {
		if len(vr.Tier4.ResolveDiagnostics) > 0 {
			return true
		}
		if vr.Tier4.Analysis != nil && len(vr.Tier4.Analysis.Errors) > 0 {
			return true
		}
	}
	if vr.RunErr != nil &&
		!errors.Is(vr.RunErr, context.Canceled) && !errors.Is(vr.RunErr, context.DeadlineExceeded) {
		return true
	}
	return false
}

// serveExpectedFinding returns the core-derived text the daemon is expected to
// carry in some diagnostic for a failing case. It projects each class the way the
// daemon's mapping does — the resolver classes surface the violation Message
// (resolverFindingToDiagnostic), while the closure classes surface the error's
// full text — so the assertion is an independent oracle of that mapping.
//
// The unresolved-component case reads the subject-free rendering rather than the
// bare Message field, and the difference is the assertion's whole falsifiability:
// a finding raised through the constraint framework supplies no Message of its
// own, and an empty expectation is one every diagnostic contains, so a test
// written against that field could not fail.
func serveExpectedFinding(t *testing.T, name string, vr *compilerops.ValidateResult) string {
	t.Helper()
	switch name {
	case "extends-broken":
		return vr.ClosureExtendsErrors[0].Error()
	case "pillar-invalid", "pillar-unusable":
		return vr.PillarError.Error()
	case "resolve-failing":
		return vr.Tier4.ResolveDiagnostics[0].Message
	case "resolution-incomplete":
		if vr.Tier4 == nil || len(vr.Tier4.ResolveDiagnostics) == 0 {
			t.Fatalf("the unmapped-instance fixture produced no per-violation finding; ValidateResult=%+v", vr)
		}
		want := vr.Tier4.ResolveDiagnostics[0].MessageWithoutSubject()
		if want == "" {
			t.Fatalf("the unmapped-instance finding rendered no text, so the assertion below could not fail")
		}
		return want
	case "input-kind-capability-failing":
		return vr.InputKindErrors[0].Error()
	default:
		t.Fatalf("no expected finding for case %q", name)
		return ""
	}
}

// TestRunTier4ValidationReleaseCoreParity proves the daemon's Tier-4 validation
// reproduces the shared core's verdict and surfaces each core finding's text
// across the six fixture cases. The input-kind case pins the daemon's superset
// inclusion of the input-kind capability class.
func TestRunTier4ValidationReleaseCoreParity(t *testing.T) {
	for _, c := range serveReleaseParityCases() {
		c := c
		t.Run(c.name, func(t *testing.T) {
			root, dirs := writeServeReleaseParityWorkspace(t, c)
			releasePath := filepath.Join(root, "release.yaml")
			aggPath := filepath.Join(root, "aggregate.yaml")

			srv := newTestServer(t, root, nil, nil)
			srv.wsConfig.Workspace.CatalogDirs = dirs
			srv.lazyCatalog = catalog.NewLazyLookup(root, "", dirs, nil)

			content, err := os.ReadFile(aggPath)
			if err != nil {
				t.Fatalf("read aggregate: %v", err)
			}
			doc := parseYAML(t, string(content))

			diags := srv.runTier4Validation(context.Background(), releasePath, aggPath, content, doc)

			// Oracle: the same core over the same resolved inputs runTier4Validation
			// builds internally (request view, resolved closure).
			view := srv.lazyCatalog.ForRequest(nil)
			resolved, env := srv.resolveReleaseClosure(context.Background(), view, releasePath)
			if env != nil {
				t.Fatalf("oracle resolveReleaseClosure envelope: %+v", env)
			}
			relAggPath := catalog.RelativeToWorkspace(srv.workspaceRoot, aggPath)
			vr := compilerops.Validate(context.Background(), compilerops.ValidateInputs{
				View:            view,
				Closure:         resolved.Closure,
				Release:         resolved.Release,
				ReleasePath:     releasePath,
				Manifest:        resolved.Manifest,
				AggBytes:        resolved.AggBytes,
				AggregatePath:   relAggPath,
				WorkspaceRoot:   srv.workspaceRoot,
				WorkspaceConfig: srv.wsConfig,
				CompilerVersion: srv.compilerVersion,
			})

			coreHasErrors := serveCoreHasErrors(vr)

			// Fixture sanity.
			if c.name == "clean" {
				if coreHasErrors {
					t.Fatalf("clean fixture is not clean per the core")
				}
			} else if !coreHasErrors {
				t.Fatalf("case %q produced no core findings; the fixture no longer exercises its class", c.name)
			}

			serveHasError := false
			for _, d := range diags {
				if d.Severity == "error" {
					serveHasError = true
					break
				}
			}

			// Verdict parity: the daemon reports an error diagnostic exactly when the
			// core reports a finding.
			if serveHasError != coreHasErrors {
				t.Fatalf("verdict diverges from core: serveHasError=%v, coreHasErrors=%v\n diags: %v",
					serveHasError, coreHasErrors, diagMessages(diags))
			}

			if !coreHasErrors {
				if len(diags) != 0 {
					t.Fatalf("clean fixture produced serve diagnostics: %v", diagMessages(diags))
				}
				return
			}

			// Content faithfulness: the core-derived finding text reaches a serve
			// diagnostic message.
			want := serveExpectedFinding(t, c.name, vr)
			found := false
			for _, d := range diags {
				if strings.Contains(d.Message, want) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("core finding %q did not reach any serve diagnostic\n diags: %v", want, diagMessages(diags))
			}

			// The unmapped-instance case additionally pins that the published
			// diagnostic names the instance at fault. The text comparison above is
			// satisfied by the condition's registered sentence alone, which every
			// instance of the condition shares, so without this the case would pass
			// over a finding that had lost the one value telling the author where to
			// look.
			if c.name == "resolution-incomplete" {
				named := false
				for _, d := range diags {
					if strings.Contains(d.Message, `"backend"`) {
						named = true
						break
					}
				}
				if !named {
					t.Fatalf("no diagnostic names the unmapped instance\n diags: %v", diagMessages(diags))
				}
			}

			// The input-kind case additionally pins the daemon's class prefix, the
			// load-bearing evidence of superset inclusion.
			if c.name == "input-kind-capability-failing" {
				sawPrefix := false
				for _, d := range diags {
					if strings.Contains(d.Message, "input-kind:") {
						sawPrefix = true
						break
					}
				}
				if !sawPrefix {
					t.Fatalf("input-kind class not surfaced with its prefix\n diags: %v", diagMessages(diags))
				}
			}
		})
	}
}
