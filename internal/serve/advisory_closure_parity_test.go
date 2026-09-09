// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// The closure-scoped advisory lines, and whether the editor is told about them.
//
// The shared validation core computes two advisory classes over the closure — a
// Component or Feature declaring one property name in two pillars, and a child
// replacing a constraint it inherited. Both are exit-code-neutral, and both are
// findings the author is the only person able to act on. The surface that runs
// validation on every keystroke is where an advisory is cheapest to act on, so it
// is the surface that must not be the one to drop them.
//
// The expectation here is DERIVED FROM THE SHARED RESULT rather than written out,
// and that is the whole design of this file. A test spelling the advisory text it
// expects proves only that one sentence survived; it says nothing about the class,
// and it keeps passing when a second class is added and dropped. Asking the shared
// core what it computed and requiring the wire to carry all of it is a claim about
// the channel instead of about a string — so a class added to the core later is
// covered by this assertion on the day it lands, without anyone editing this file.
//
// The two sides come from separate invocations on purpose. Deriving both from one
// call would compare a value with itself, which is the blindness that let this gap
// exist: every surface agreed about the fields it read, and nothing asked about the
// field none of them read.

// pillarOverlapAndNarrowingWorkspace writes a workspace whose closure raises both
// advisory classes at once, and nothing else that would explain them away.
//
// The Component declares `endpoint` in both the values and infra pillars, which is
// the overlap advisory. The attached Feature extends a parent that constrains
// `retries` and redeclares it with a lower bound, which is the narrowing advisory
// — the parent's constraint is replaced by a looser one. It has to be a Feature:
// an extending Component authors no pillar content, so it has nothing to redeclare
// and cannot raise this class at all.
//
// Both advisories are authored into CATALOG entities rather than into the
// Aggregate, because that is where they can be raised — and it is also why they
// arrive without a position in the edited document, each naming its entity by URI.
func pillarOverlapAndNarrowingWorkspace(t *testing.T) (ws, releaseRel string) {
	t.Helper()
	ws = t.TempDir()
	catalogDir := filepath.Join(ws, "catalog")

	// One property name in two pillars, plus the slot the Feature attaches to.
	writeFixtureFile(t, filepath.Join(catalogDir, "components", "svc.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: demo/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  featuresSchema:
    limits:
      inject: []
      description: Retry limits for the service.
  schema:
    values:
      endpoint: { type: string }
    infra:
      endpoint: { type: string }
`)

	writeFixtureFile(t, filepath.Join(catalogDir, "features", "limits-base.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: demo/limits-base
  version: "1.0.0"
  labels:
    feature.aggrega.ai/type: policy
spec:
  schema:
    values:
      retries: { type: integer, minimum: 5 }
`)

	// Redeclares the inherited bound at a lower value: the child replaces what it
	// inherited, which is the advisory the composition raises.
	writeFixtureFile(t, filepath.Join(catalogDir, "features", "limits.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: demo/limits
  version: "1.0.0"
  labels:
    feature.aggrega.ai/type: policy
spec:
  extends: demo/limits-base
  schema:
    values:
      retries: { type: integer, minimum: 1 }
`)

	writeFixtureFile(t, filepath.Join(ws, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: advisory-parity-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type: {}
`)

	writeFixtureFile(t, filepath.Join(ws, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: advisory-parity
  version: "1.0.0"
spec:
  components:
    app:
      type: demo/svc
      features:
        limits:
          type: demo/limits
          values:
            retries: 3
`)

	writeFixtureFile(t, filepath.Join(ws, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: advisory-parity-release
  environment: test
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        app: {}
`)

	writeFixtureFile(t, filepath.Join(ws, ".aggrega-config.yaml"), `workspace:
  catalog_dirs:
    - ./catalog
`)
	return ws, "release.yaml"
}

// sharedClosureAdvisories asks the shared validation core what advisories this
// Release's closure raises, and returns them per class.
//
// This is the expectation side, and it deliberately reads the SHARED RESULT rather
// than anything the daemon produced from it. The inputs are assembled the way the
// request path assembles them, so the two sides are answering about one closure;
// what is not shared is the projection into diagnostics, which is precisely the
// step under test.
func sharedClosureAdvisories(t *testing.T, srv *Server, releasePath string) (lint, narrowing []string) {
	t.Helper()
	view := srv.lazyCatalog.ForRequest(nil)
	resolved, envelope := srv.resolveReleaseClosure(view, releasePath)
	if envelope != nil {
		t.Fatalf("the closure did not resolve, so the shared core computed no advisories and this test "+
			"would assert nothing: %s", envelope.Message)
	}
	vr := compilerops.Validate(t.Context(), compilerops.ValidateInputs{
		View:            view,
		Closure:         resolved.Closure,
		Release:         resolved.Release,
		ReleasePath:     releasePath,
		Manifest:        resolved.Manifest,
		AggBytes:        resolved.AggBytes,
		AggregatePath:   catalog.RelativeToWorkspace(srv.workspaceRoot, resolved.AggregatePath),
		WorkspaceConfig: srv.wsConfig,
		CompilerVersion: srv.compilerVersion,
	})
	return vr.LintWarnings, vr.NarrowingWarnings
}

// TestValidateCarriesEveryClosureAdvisoryTheSharedCoreComputed drives
// aggrega/validate over the wire and requires the published set to account for
// every advisory the shared validation core computed for the same closure.
//
// The failure this pins is a silent one. A dropped advisory changes no verdict, no
// exit code and no other diagnostic, so the editor keeps reporting a document as
// it always did and there is nothing on screen to say a finding was withheld. The
// only way to see it is to ask the computation what it found and compare.
func TestValidateCarriesEveryClosureAdvisoryTheSharedCoreComputed(t *testing.T) {
	t.Parallel()
	ws, releaseRel := pillarOverlapAndNarrowingWorkspace(t)
	aggPath := filepath.Join(ws, "aggregate.yaml")
	releasePath := filepath.Join(ws, releaseRel)

	srv := newTestServer(t, ws, nil, nil)
	srv.wsConfig.Workspace.CatalogDirs = []string{filepath.Join(ws, "catalog")}
	srv.lazyCatalog = catalog.NewLazyLookup(ws, "", []string{filepath.Join(ws, "catalog")}, nil)

	wantLint, wantNarrowing := sharedClosureAdvisories(t, srv, releasePath)

	// Calibration, per class. Without it a fixture that stopped raising one class
	// would leave that half of the comparison iterating over nothing, and the test
	// would report success for a channel it never exercised.
	if len(wantLint) == 0 {
		t.Fatal("the shared core computed no pillar-overlap advisory for this closure, so the fixture no " +
			"longer raises that class and this test cannot see whether the daemon carries it")
	}
	if len(wantNarrowing) == 0 {
		t.Fatal("the shared core computed no narrowing advisory for this closure, so the fixture no longer " +
			"raises that class and this test cannot see whether the daemon carries it")
	}

	aggBytes, err := os.ReadFile(aggPath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/validate", validateParams{
		File:    aggPath,
		Content: string(aggBytes),
		Release: releaseRel,
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("unexpected envelope error: %+v", resp.Error)
	}
	var result validateResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	// Recorded so what the editor is actually sent is readable from a verbose run
	// rather than only inferable from the assertions that passed.
	t.Logf("the shared core computed %d pillar-overlap and %d narrowing advisory(ies); the request "+
		"published %d diagnostic(s): %v",
		len(wantLint), len(wantNarrowing), len(result.Diagnostics), diagMessages(result.Diagnostics))

	// Matched on containment rather than on equality: the daemon is entitled to
	// prefix an entry for the surface it publishes on, and that framing is
	// presentation. What it is not entitled to do is drop the finding.
	for _, class := range []struct {
		name string
		want []string
	}{
		{"pillar-overlap", wantLint},
		{"narrowing-on-extend", wantNarrowing},
	} {
		for _, w := range class.want {
			var found *Diagnostic
			for i := range result.Diagnostics {
				if strings.Contains(result.Diagnostics[i].Message, w) {
					found = &result.Diagnostics[i]
					break
				}
			}
			if found == nil {
				// Names the missing entry rather than reporting a count. Both sides
				// short by one still agree on a count, and the entry that went
				// missing is the only actionable part of the failure.
				t.Errorf("the %s advisory the shared core computed reaches no published diagnostic:\n"+
					"  missing: %s\n  published: %v", class.name, w, diagMessages(result.Diagnostics))
				continue
			}
			if found.Severity != editorSeverityWarning {
				t.Errorf("the %s advisory published at severity %q; want %q — an exit-code-neutral finding "+
					"published as an error has the editor assert the buffer is broken over authoring the "+
					"compiler permits", class.name, found.Severity, editorSeverityWarning)
			}
		}
	}
}
