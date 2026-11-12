// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

// What this file proves, and why the obvious instrument is not enough on its own.
//
// The resolution request is the session's acquisition moment: it renders the
// Release to discover which external references that render consumes, fetches
// them, and records them in the same lockfile write that carries the dependency
// pins. Nothing else in a session acquires — a build request renders the closure
// the lockfile records — so if this render does not pin a reference, no later
// request will, and a build over the same workspace halts naming it.
//
// A LOCKFILE PIN ALONE DOES NOT PROVE THIS RUN FETCHED IT. A pin can survive from
// an earlier run, and a pinned reference is then served from the store rather
// than refetched, so a recorded pin is consistent with this run having done
// nothing at all. The endpoint's own count is what separates the two: it is read
// across the run boundary, so a fetch is attributed to the run that made it.
//
// Both are asserted, and each covers what the other cannot. The count says this
// run reached the endpoint exactly once — more would mean a reference was fetched
// twice, none would mean the pin came from somewhere else. The lockfile record
// says the bytes were pinned rather than merely fetched, which is the half a
// later offline build depends on.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/entitypin"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// externalInputSchemaBody is the schema document the loopback endpoint serves. It
// is permissive, so the chart's values validate and the render reaches its output
// — a schema that rejected the values would fail the render for a reason that has
// nothing to do with pinning.
const externalInputSchemaBody = `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`

// externalInputSchemaServer starts the endpoint the chart's values schema points
// at and returns its URL with a reader of how many times it has been asked for
// the document.
//
// The count is a function rather than a pointer so a caller cannot reset it, and
// it is read at run boundaries rather than as a running total.
func externalInputSchemaServer(t *testing.T) (schemaURL string, fetches func() int) {
	t.Helper()
	served := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served++
		_, _ = w.Write([]byte(externalInputSchemaBody))
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/values-schema.json", func() int { return served }
}

// externalInputFetchWorkspace turns the rendering fixture into one whose render
// REQUESTS an external input.
//
// The fetch comes from the chart rather than from any entity: the guest compiles
// the chart's values schema through the host's fetch seam, so a remote reference
// there is a render-time request rather than a resolution-time one. That is what
// makes this fixture exercise the discovery render specifically — a reference no
// read of the Release could have enumerated.
//
// The recipe keeps the name, the implemented component and the provided key it
// already had, and changes only how it renders — from an inline workload to a
// chart source — so the manifest mapping and the component's declared key still
// resolve and the fixture's only new property is that its render fetches.
func externalInputFetchWorkspace(t *testing.T, schemaURL string) (ws, releaseRel string) {
	t.Helper()
	ws, releaseRel = renderDialWorkspace(t)

	// The chart sits in the Recipe's own directory and is named relative to it:
	// content an entity reads lives beside the entity.
	chartDir := filepath.Join(ws, "catalog", "recipes", "charts", "svc")
	writeFixtureFile(t, filepath.Join(chartDir, "Chart.yaml"), "apiVersion: v2\nname: svc\nversion: 0.1.0\n")
	writeFixtureFile(t, filepath.Join(chartDir, "templates", "cm.yaml"), `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}
data:
  releaseName: {{ .Release.Name | quote }}
`)
	writeFixtureFile(t, filepath.Join(chartDir, "values.schema.json"),
		fmt.Sprintf(`{"$schema":"https://json-schema.org/draft/2020-12/schema","$ref":%q}`, schemaURL))

	recipePath := filepath.Join(ws, "catalog", "recipes", "server.yaml")
	writeFixtureFile(t, recipePath, `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: demo-server-synthesized
  version: "1.0.0"
spec:
  capability: helm
  implements: demo/svc
  provides:
    host: "{{ .context.instance_id }}"
  source: ./charts/svc
`)

	if err := os.WriteFile(filepath.Join(ws, ".aggrega-config.yaml"),
		[]byte("workspace:\n  catalog_dirs: [catalog]\n"+testOwnedRegistryConfig(t)), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}

	// The Recipe reads a local chart, so its author pins it — through the step the
	// daemon's own pin method runs — before any resolution reads it.
	rw, err := workspaceconfig.Resolve(ws, "")
	if err != nil {
		t.Fatalf("resolve the workspace to pin in: %v", err)
	}
	if _, err := entitypin.Pin(context.Background(), entitypin.Options{Workspace: rw, EntityPath: recipePath}); err != nil {
		t.Fatalf("pin the fixture's Recipe: %v", err)
	}
	return ws, releaseRel
}

// TestResolveDependenciesPinsTheRendersExternalInputs is the assertion that the
// session's resolution is the acquisition moment: the reference the render asks
// for is fetched once by this run and recorded in the lockfile, so a later build
// over the same workspace is served from the store rather than halting.
func TestResolveDependenciesPinsTheRendersExternalInputs(t *testing.T) {
	schemaURL, fetches := externalInputSchemaServer(t)
	ws, releaseRel := externalInputFetchWorkspace(t, schemaURL)
	// The harness declares the artifact cache as a RELATIVE path, so the store this
	// run writes lands beside the working directory rather than inside the fixture.
	// Without this the run pins its bytes into the source tree, which is both a
	// stray write and a cache shared with every other run of this package.
	t.Chdir(ws)
	srv := newFillCapableServer(t, ws)

	before := fetches()
	if _, errEnvelope := srv.handleResolveDependencies(
		context.Background(),
		resolveDependenciesParams{Release: releaseRel},
		srv.progressFor(context.Background())); errEnvelope != nil {
		t.Fatalf("the resolution failed: %s", errEnvelope.Message)
	}

	if got := fetches() - before; got != 1 {
		t.Errorf("the remote document was fetched %d time(s) during the resolution, want exactly 1: "+
			"more than one means a render refetched a reference the store already held, and none means "+
			"the resolution never reached the endpoint, so nothing acquired", got)
	}

	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(filepath.Join(ws, releaseRel)))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	var pinned bool
	for _, rec := range lf.ExternalInputs {
		if rec.URL == schemaURL {
			pinned = true
		}
	}
	if !pinned {
		t.Errorf("the lockfile records no pin for %s, so a later build cannot serve it offline; recorded: %+v",
			schemaURL, lf.ExternalInputs)
	}
}
