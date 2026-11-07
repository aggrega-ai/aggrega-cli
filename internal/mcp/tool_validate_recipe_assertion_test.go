// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// portFloorFailure opens the sentence the port-floor Recipe's assertion fails with
// when the Release leaves port at 8080. It names the port as the Recipe's own
// provides resolved it, so a diagnostic carrying it shows the assertion read a
// resolved provides value.
const portFloorFailure = "port 8080 is below 9000"

// portFloorRecipe implements the shared catalog's api-server contract and asserts
// that the port the Release sets is at least 9000, reading its own resolved
// provides in the failure.
const portFloorRecipe = `apiVersion: aggrega.ai/v1alpha1
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
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: api-server:latest
`

const portFloorManifest = `apiVersion: aggrega.ai/v1alpha1
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

// portFloorHoldingRelease sets a port the assertion accepts; it is the fixture's
// default Release otherwise.
const portFloorHoldingRelease = `apiVersion: aggrega.ai/v1alpha1
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

// TestValidateToolReleaseScopeReportsAFailingRecipeAssertion holds the assistant
// tool's release scope to the verdict a build reaches on a Recipe's assertion. A
// Release whose port the Recipe refuses is answered invalid, with the shared
// core's run error — the assertion's condition, matched by identity — among the
// diagnostics as one line headed by its condition and code and carrying the
// author's sentence. The control, which only raises the port, is answered valid.
//
// 🔴 PLANTED DEFECT: skip the Recipe assertion stage in RunTier4Pipeline
// (internal/compiler/pipeline/pipeline.go, the evaluateRecipeAssertions call): the
// failing row is answered valid.
func TestValidateToolReleaseScopeReportsAFailingRecipeAssertion(t *testing.T) {
	for _, row := range []struct {
		name    string
		release string
		failing bool
	}{
		{name: "a port the Recipe refuses", release: "", failing: true},
		{name: "a port the Recipe accepts", release: portFloorHoldingRelease, failing: false},
	} {
		t.Run(row.name, func(t *testing.T) {
			s := newTestServer(t)
			root, dirs := writeReleaseParityWorkspace(t, releaseParityCase{
				aggregate: releaseParityBackendAggregate,
				manifest:  portFloorManifest,
				release:   row.release,
				overlay:   map[string]string{"recipes/api-server-port-floor.yaml": portFloorRecipe},
			})
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

			if !row.failing {
				if !payload.Valid || len(payload.Diagnostics) != 0 {
					t.Fatalf("a Release the assertion accepts was answered invalid: %#v", payload.Diagnostics)
				}
				return
			}

			if payload.Valid {
				t.Fatal("a Release whose Recipe assertion fails was answered valid")
			}

			// The oracle: the same core over the same resolved inputs
			// validateReleaseScope builds, so the condition is matched by identity.
			tw := resolvedToolWorkspace(t, s, root, dirs)
			_, lockPins, lockRefused := tw.loadReleaseLockfile(releasePath)
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
			if !errors.Is(vr.RunErr, renderer.ErrAssertionFailed()) || !renderer.AssertionCalledFail(vr.RunErr) {
				t.Fatalf("the core's run error is not the author's failing assertion: %v", vr.RunErr)
			}
			line := cerrors.HeadedCauseChain(vr.RunErr)
			if !slices.Contains(payload.Diagnostics, line) {
				t.Fatalf("the answer does not carry the core's failure as one headed line:\n want among: %q\n diagnostics: %#v", line, payload.Diagnostics)
			}
			for _, want := range []string{"[CATALOG_ERROR E0033]", "Recipe/FeatureRecipe assertions", portFloorFailure} {
				if !strings.Contains(line, want) {
					t.Errorf("the failure line does not carry %q: %q", want, line)
				}
			}
		})
	}
}
