// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// The closure-level fall-back for a pre-render phase error that carries no
// per-violation structure of its own. Two properties decide whether the tool
// publishes it, and each row below drives one of them, because they pull in
// opposite directions and a rule satisfying only one is wrong in the other
// direction:
//
//   - a phase error must still reach the caller when per-violation findings were
//     published alongside it but do not describe it;
//   - a run error that WAS published finding by finding must not also be published
//     as a closure-level line, which would report one failure twice.
//
// The first row is the one no other case in this package covers, because it needs
// a workspace carrying two independent faults at once: an instance the Manifest
// maps no recipe for, whose finding travels out of the pipeline on the
// per-violation channel without failing the run, and a Component contract whose
// build-time assertion fails, which is a phase error with nothing per-violation
// about it.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// releaseParityFailingAssertionOverlayComponent declares a Component whose
// build-time assertion never holds, so every instance of it fails the assertion
// phase. It is otherwise the shared catalog's own contract for this type, so the
// fault this fixture carries is the assertion and nothing else.
const releaseParityFailingAssertionOverlayComponent = `apiVersion: aggrega.ai/v1alpha1
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

// TestValidateDiagnosticsPublishesPhaseErrorBesidePerViolationFindings pins both
// directions of the closure-level fall-back against a workspace built for each.
// It drives validateDiagnostics directly — the single mapping every release-scope
// response is composed from — so a row fails on the mapping rather than on
// anything the transport adds around it.
func TestValidateDiagnosticsPublishesPhaseErrorBesidePerViolationFindings(t *testing.T) {
	cases := []struct {
		name string
		// fixture is the workspace to validate.
		fixture releaseParityCase
		// wantFallback is whether the run error is expected to be published as a
		// diagnostic of its own.
		wantFallback bool
		// wantInstanceNamed is the instance a per-violation finding must name, or
		// empty where the fixture declares no component at all.
		wantInstanceNamed string
	}{
		{
			name: "a phase error beside per-violation findings that do not describe it",
			fixture: releaseParityCase{
				aggregate: releaseParityBackendAggregate,
				manifest:  releaseParityEmptyManifest,
				overlay: map[string]string{
					"components/api-server.yaml": releaseParityFailingAssertionOverlayComponent,
				},
			},
			wantFallback:      true,
			wantInstanceNamed: `"backend"`,
		},
		{
			name: "a resolver-class failure already published finding by finding",
			fixture: releaseParityCase{
				aggregate: releaseParityResolveFailingAggregate,
				manifest:  releaseParityEmptyManifest,
				release:   releaseParityEmptyBoundaryRelease,
			},
			wantFallback: false,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			s := newTestServer(t)
			root, dirs := writeReleaseParityWorkspace(t, c.fixture)
			releasePath := filepath.Join(root, "release.yaml")

			// The same core over the same resolved inputs validateReleaseScope builds
			// internally, so the expected fall-back text is derived from the run error
			// rather than transcribed from it.
			tw := resolvedToolWorkspace(t, s, root, dirs)
			lf, lockRefused := tw.loadReleaseLockfile(releasePath)
			if lockRefused != "" {
				t.Fatalf("read the oracle's lockfile: %s", lockRefused)
			}
			view := tw.newView(releasePath, lf)
			resolved, cerr := compilerops.ResolveClosure(context.Background(), compilerops.ClosureInputs{
				View:            view,
				ReleasePath:     releasePath,
				WorkspaceRoot:   tw.root,
				WorkspaceConfig: tw.cfg,
			})
			if cerr != nil {
				t.Fatalf("ResolveClosure failed: %v", cerr)
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

			// Fixture sanity, stated per axis so a fixture that stopped carrying one of
			// its two faults fails here rather than passing the assertions below for the
			// wrong reason.
			if vr.RunErr == nil {
				t.Fatalf("the fixture produced no run error, so it no longer exercises this case; ValidateResult=%+v", vr)
			}
			if vr.Tier4 == nil || len(vr.Tier4.ResolveDiagnostics) == 0 {
				t.Fatalf("the fixture produced no per-violation finding, so it no longer exercises this case; ValidateResult=%+v", vr)
			}

			diags := validateDiagnostics(vr, resolved.ManifestConsistencyFindings)

			fallback := cerrors.HeadedCauseChain(vr.RunErr)

			// Occurrences the other streams already contribute, so the count below
			// measures the fall-back alone even where a stream happens to render the
			// same text — which is what a single-violation run does.
			base := 0
			for _, ve := range vr.Tier4.ResolveDiagnostics {
				if ve.Error() == fallback {
					base++
				}
			}
			if vr.Tier4.Analysis != nil {
				for _, ae := range vr.Tier4.Analysis.Errors {
					if ae.Error() == fallback {
						base++
					}
				}
			}
			for _, f := range vr.ResolutionFindings {
				if f.Error() == fallback {
					base++
				}
			}

			seen := 0
			for _, d := range diags {
				if d == fallback {
					seen++
				}
			}
			want := base
			if c.wantFallback {
				want++
			}
			if seen != want {
				t.Fatalf("the run error was published %d time(s); want %d (%d of them from the other streams)\n text: %q\n diagnostics: %#v",
					seen, want, base, fallback, diags)
			}

			if c.wantInstanceNamed != "" {
				named := false
				for _, d := range diags {
					if strings.Contains(d, c.wantInstanceNamed) {
						named = true
						break
					}
				}
				if !named {
					t.Fatalf("no diagnostic names %s, so the per-violation half of this fixture is not being published\n diagnostics: %#v",
						c.wantInstanceNamed, diags)
				}
			}
		})
	}
}
