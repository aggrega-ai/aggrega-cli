// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

// The closure-level fall-back for a pre-render phase error that carries no
// per-violation structure of its own. Two properties decide whether it is
// published, and each row below drives one of them, because they pull in
// opposite directions and a rule satisfying only one is wrong in the other
// direction:
//
//   - a phase error must still reach the editor when per-violation findings were
//     published alongside it but do not describe it;
//   - a run error that WAS published finding by finding must not also be
//     published as a closure-level line, which would report one failure twice.
//
// The first row is the one no other case in this package covers, because it
// needs a workspace carrying two independent faults at once: an instance the
// Manifest maps no recipe for, whose finding travels out of the pipeline on the
// per-violation channel without failing the run, and a Component contract whose
// build-time assertion fails, which is a phase error with nothing per-violation
// about it.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// serveFailingAssertionOverlayComponent declares a Component whose build-time
// assertion never holds, so every instance of it fails the assertion phase. It
// is otherwise the fixture catalog's own contract for this type, so the fault
// this fixture carries is the assertion and nothing else.
const serveFailingAssertionOverlayComponent = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/compute/api-server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  inject: ["env"]
  assertions:
    replicas-are-explicit: '{{ fail "this contract requires replicas to be declared explicitly" }}'
  schema:
    infra:
      port: { type: integer, default: 8080 }
      replicas: { type: integer, default: 1 }
    provides:
      host: { type: string, required: true }
      service_port: { type: string, required: true }
`

// TestRunTier4ValidationPublishesPhaseErrorBesidePerViolationFindings pins both
// directions of the closure-level fall-back against a workspace built for each.
func TestRunTier4ValidationPublishesPhaseErrorBesidePerViolationFindings(t *testing.T) {
	cases := []struct {
		name string
		// fixture is the workspace to validate.
		fixture serveReleaseParityCase
		// wantFallback is whether the run error is expected to reach the editor as
		// a closure-level line of its own.
		wantFallback bool
		// wantInstanceNamed is the instance a per-violation finding must name, or
		// empty where the fixture declares no component at all.
		wantInstanceNamed string
	}{
		{
			// A Manifest mapping no recipe for the one instance AND a Component
			// contract whose assertion fails. The first reports without refusing and
			// fills the per-violation channel; the second is the run error, and it
			// has no per-violation representation anywhere.
			name: "a phase error beside per-violation findings that do not describe it",
			fixture: serveReleaseParityCase{
				aggregate: serveBackendAggregate,
				manifest:  serveEmptyManifest,
				overlay: map[string]string{
					"components/api-server.yaml": serveFailingAssertionOverlayComponent,
				},
			},
			wantFallback:      true,
			wantInstanceNamed: `"backend"`,
		},
		{
			// A resolver-class failure on its own: the run error IS the per-violation
			// findings, so a fall-back beside them would report it twice.
			name: "a resolver-class failure already published finding by finding",
			fixture: serveReleaseParityCase{
				aggregate: serveResolveFailingAggregate,
				manifest:  serveEmptyManifest,
				release:   serveEmptyBoundaryRelease,
			},
			wantFallback: false,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			root, dirs := writeServeReleaseParityWorkspace(t, c.fixture)
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

			// The oracle: the same core over the same resolved inputs
			// runTier4Validation builds internally, so the expected fall-back text is
			// derived from the run error rather than transcribed from it.
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

			// Fixture sanity, stated per axis so a fixture that stopped carrying one
			// of its two faults fails here rather than passing the assertions below
			// for the wrong reason.
			if vr.RunErr == nil {
				t.Fatalf("the fixture produced no run error, so it no longer exercises this case; ValidateResult=%+v", vr)
			}
			if vr.Tier4 == nil || len(vr.Tier4.ResolveDiagnostics) == 0 {
				t.Fatalf("the fixture produced no per-violation finding, so it no longer exercises this case; ValidateResult=%+v", vr)
			}

			wantFallback := tier4PhaseErrorMessage(vr.RunErr)
			seen := 0
			for _, d := range diags {
				if d.Message == wantFallback {
					seen++
				}
			}
			switch {
			case c.wantFallback && seen == 0:
				t.Fatalf("the phase error reached no diagnostic; want %q\n diags: %v", wantFallback, diagMessages(diags))
			case c.wantFallback && seen > 1:
				t.Fatalf("the phase error was published %d times; want once\n diags: %v", seen, diagMessages(diags))
			case !c.wantFallback && seen > 0:
				t.Fatalf("the run error was published %d times as a closure-level line as well as finding by finding\n diags: %v",
					seen, diagMessages(diags))
			}

			if c.wantInstanceNamed != "" {
				named := false
				for _, d := range diags {
					if strings.Contains(d.Message, c.wantInstanceNamed) {
						named = true
						break
					}
				}
				if !named {
					t.Fatalf("no diagnostic names %s, so the per-violation half of this fixture is not being published\n diags: %v",
						c.wantInstanceNamed, diagMessages(diags))
				}
			}
		})
	}
}
