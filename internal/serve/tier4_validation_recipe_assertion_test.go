// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// servePortFloorFailure opens the sentence the port-floor Recipe's assertion fails
// with when the Release leaves port at 8080. It names the port as the Recipe's own
// provides resolved it, so a diagnostic carrying it shows the assertion read a
// resolved provides value.
const servePortFloorFailure = "port 8080 is below 9000"

// servePortFloorRecipe implements the fixture catalog's api-server contract and
// asserts that the port the Release sets is at least 9000, reading its own
// resolved provides in the failure.
const servePortFloorRecipe = `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: api-server-port-floor
  version: "1.0.0"
spec:
  capability: helm
  implements: core/compute/api-server
  assertions:
    port_floor: '{{ if lt .infra.port 9000 }}{{ fail (printf "port %s is below 9000, the lowest port this Recipe serves on" .provides.service_port) }}{{ end }}'
  provides:
    host: "{{ .context.instance_id }}.svc.cluster.local"
    service_port: "{{ optional .infra ` + "`port`" + ` 8080 }}"
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: api-server:latest
`

const servePortFloorManifest = `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: parity-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/compute/api-server: api-server-port-floor:1.0.0
`

// servePortFloorHoldingRelease sets a port the assertion accepts; it is the
// fixture's default Release otherwise.
const servePortFloorHoldingRelease = `apiVersion: aggrega.ai/v1beta1
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
            port: 9090
`

// TestRunTier4ValidationReportsAFailingRecipeAssertion holds the daemon's
// release-scoped validation to the verdict a build reaches on a Recipe's
// assertion. A Release whose port the Recipe refuses is answered, on the Aggregate,
// with a diagnostic carrying the assertion's condition code and its sentence; the
// shared core's run error is that condition by identity. The control, which only
// raises the port, is answered with no error-severity diagnostic.
//
// 🔴 PLANTED DEFECT: skip the Recipe assertion stage in RunTier4Pipeline
// (internal/compiler/pipeline/pipeline.go, the evaluateRecipeAssertions call): the
// failing row publishes no such diagnostic.
func TestRunTier4ValidationReportsAFailingRecipeAssertion(t *testing.T) {
	for _, row := range []struct {
		name    string
		release string
		failing bool
	}{
		{name: "a port the Recipe refuses", release: "", failing: true},
		{name: "a port the Recipe accepts", release: servePortFloorHoldingRelease, failing: false},
	} {
		t.Run(row.name, func(t *testing.T) {
			root, dirs := writeServeReleaseParityWorkspace(t, serveReleaseParityCase{
				aggregate: serveBackendAggregate,
				manifest:  servePortFloorManifest,
				release:   row.release,
				overlay:   map[string]string{"recipes/api-server-port-floor.yaml": servePortFloorRecipe},
			})
			releasePath := filepath.Join(root, "release.yaml")
			aggPath := filepath.Join(root, "aggregate.yaml")

			srv := newTestServer(t, root, nil, nil)
			srv.wsConfig.Workspace.CatalogDirs = dirs
			srv.lazyCatalog = catalog.NewLazyLookup(root, "", dirs, nil)

			content, err := os.ReadFile(aggPath)
			if err != nil {
				t.Fatalf("read aggregate: %v", err)
			}
			diags := srv.runTier4Validation(context.Background(), releasePath, aggPath, content, parseYAML(t, string(content)))

			var assertionDiags, errorDiags []Diagnostic
			for _, d := range diags {
				if d.Severity == editorSeverityError {
					errorDiags = append(errorDiags, d)
				}
				if d.ConditionCodes.ShortCode == "E0033" {
					assertionDiags = append(assertionDiags, d)
				}
			}

			if !row.failing {
				if len(errorDiags) != 0 {
					t.Fatalf("a Release the assertion accepts drew error diagnostics: %v", diagMessages(errorDiags))
				}
				return
			}

			if len(assertionDiags) != 1 {
				t.Fatalf("want exactly one diagnostic carrying E0033; got %d\n diags: %v", len(assertionDiags), diagMessages(diags))
			}
			if d := assertionDiags[0]; d.Severity != editorSeverityError || !strings.Contains(d.Message, servePortFloorFailure) {
				t.Fatalf("the assertion diagnostic is not an error carrying %q: severity %q, message %q", servePortFloorFailure, d.Severity, d.Message)
			}

			// The oracle: the same core over the same resolved inputs, so the
			// condition is matched by identity rather than read off the text.
			view := srv.lazyCatalog.ForRequest(nil)
			resolved, env := srv.resolveReleaseClosure(context.Background(), view, releasePath)
			if env != nil {
				t.Fatalf("oracle resolveReleaseClosure envelope: %+v", env)
			}
			vr := compilerops.Validate(context.Background(), compilerops.ValidateInputs{
				View:            view,
				Closure:         resolved.Closure,
				Release:         resolved.Release,
				ReleasePath:     releasePath,
				Manifest:        resolved.Manifest,
				AggBytes:        resolved.AggBytes,
				AggregatePath:   catalog.RelativeToWorkspace(srv.workspaceRoot, aggPath),
				WorkspaceRoot:   srv.workspaceRoot,
				WorkspaceConfig: srv.wsConfig,
				CompilerVersion: srv.compilerVersion,
			})
			if !errors.Is(vr.RunErr, renderer.ErrAssertionFailed()) || !renderer.AssertionCalledFail(vr.RunErr) {
				t.Fatalf("the core's run error is not the author's failing assertion: %v", vr.RunErr)
			}
			if want := tier4PhaseErrorMessage(vr.RunErr); assertionDiags[0].Message != want {
				t.Errorf("the diagnostic does not carry the core's failure as the daemon renders it:\n got:  %q\n want: %q", assertionDiags[0].Message, want)
			}
		})
	}
}
