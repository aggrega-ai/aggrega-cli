// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// What this file proves, and why the obvious instrument does not prove it.
//
// The acquisition request runs the Release's render so every external input
// that render asks for is pinned. Where the workspace fills its lockfile
// automatically, a resolution runs first — and that resolution discovers
// external inputs by rendering too. So the two can render the same Release
// twice, and the reader cannot see it: both orders write the same lockfile and
// the same store, so the output is correct either way.
//
// A FETCH COUNTER CANNOT SEPARATE THEM, which is why the assertion below is not
// one. A pinned reference is served from the store rather than refetched, so a
// run whose resolution discovers fetches once and then renders again off the
// store, and a run whose resolution stands down fetches once during its own
// render. One fetch either way. The count is still asserted, because it bounds
// the cost claim from the other side — two fetches would mean neither render saw
// the other's pin — but it is not what discriminates.
//
// What discriminates is WHICH render did the pinning, and the answer is on the
// wire: the answer reports the inputs THIS run newly pinned. Where the
// resolution pinned them first, the acquiring render finds everything already
// pinned, acquires nothing, and the request answers "nothing pinned" while a pin
// was in fact written — so the double render is a reporting fault as well as a
// cost, and the reported set is the observable that fails when the declaration
// is missing.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// acquireSchemaBody is the schema document the loopback endpoint serves. It is
// permissive, so the chart's values validate and the render reaches its output —
// a schema that rejected the values would fail the render for a reason that has
// nothing to do with pinning.
const acquireSchemaBody = `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`

// acquireSchemaServer starts the endpoint the chart's values schema points at
// and returns its URL with a reader of how many times it has been asked for the
// document.
//
// The count is a function rather than a pointer so a caller cannot reset it, and
// it is read at run boundaries rather than as a running total.
func acquireSchemaServer(t *testing.T) (schemaURL string, fetches func() int) {
	t.Helper()
	served := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served++
		_, _ = w.Write([]byte(acquireSchemaBody))
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/values-schema.json", func() int { return served }
}

// acquireFetchWorkspace turns the rendering fixture into one whose render
// REQUESTS an external input, and declares the workspace to fill its lockfile
// automatically.
//
// The fetch comes from the chart rather than from any entity: the guest compiles
// the chart's values schema through the host's fetch seam, so a remote reference
// there is a render-time request rather than a resolution-time one. That is what
// makes the automatic fill and the acquiring render reach the same reference,
// which is the whole subject here.
//
// The recipe keeps the name, the implemented component and the provided key it
// already had, and changes only how it renders — from an inline workload to a
// chart source — so the manifest mapping and the component's declared key still
// resolve and the fixture's only new property is that its render fetches.
func acquireFetchWorkspace(t *testing.T, schemaURL string) (ws, releaseRel string) {
	t.Helper()
	ws, releaseRel = renderDialWorkspace(t)

	chartDir := filepath.Join(ws, "charts", "svc")
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

	writeFixtureFile(t, filepath.Join(ws, "catalog", "recipes", "server.yaml"),
		fmt.Sprintf(`apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: demo-server-synthesized
  version: "1.0.0"
spec:
  capability: helm
  implements: demo/svc
  provides:
    host: "{{ .context.instance_id }}"
  source: %q
`, chartDir))

	// The automatic fill is the workspace's, not the request's: this request
	// carries no fill switch, so the setting is the only way the resolution runs
	// before the acquiring render — which is the arrangement under test.
	if err := os.WriteFile(filepath.Join(ws, ".aggrega-config.yaml"),
		[]byte("auto_pull: true\nworkspace:\n  catalog_dirs: [catalog]\n"), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
	return ws, releaseRel
}

// TestAcquireExternalInputsRendersOnceUnderAutomaticFill is the assertion that
// the acquiring render is the one that pins, in a workspace that fills its
// lockfile first.
//
// The reported set is the discriminator. Where the fill's own discovery render
// runs, it pins the reference before the acquiring render sees it, so the
// acquiring render acquires nothing and this request answers with an empty set
// while the lockfile carries a pin — a client told nothing was pinned. Where the
// fill stands down, the acquiring render pins the reference and the request
// reports it.
//
// The lockfile is asserted separately, because it is correct under both
// arrangements: it is what makes the reported set the thing that moved rather
// than the outcome.
func TestAcquireExternalInputsRendersOnceUnderAutomaticFill(t *testing.T) {
	schemaURL, fetches := acquireSchemaServer(t)
	ws, releaseRel := acquireFetchWorkspace(t, schemaURL)
	// The harness declares the artifact cache as a RELATIVE path, so the store this
	// run writes lands beside the working directory rather than inside the fixture.
	// Without this the run pins its bytes into the source tree, which is both a
	// stray write and a cache shared with every other run of this package.
	t.Chdir(ws)
	srv := newFillCapableServer(t, ws)

	before := fetches()
	result, errEnvelope := srv.handleAcquireExternalInputs(
		context.Background(),
		acquireExternalInputsParams{Release: releaseRel},
		srv.progressFor(context.Background()))
	if errEnvelope != nil {
		t.Fatalf("the acquisition failed: %s", errEnvelope.Message)
	}

	if len(result.Pinned) != 1 {
		t.Fatalf("the request reported %d pinned input(s), want exactly 1: an empty set with a pin on disk "+
			"means the dependency fill's own discovery render pinned it first, so this Release was rendered "+
			"twice and the client was told nothing was pinned; reported: %+v", len(result.Pinned), result.Pinned)
	}
	if result.Pinned[0].URL != schemaURL {
		t.Errorf("the reported pin names %q, want the reference the render asked for %q",
			result.Pinned[0].URL, schemaURL)
	}

	// The other side of the cost claim: two fetches would mean neither render was
	// served the other's pin. One fetch is consistent with either arrangement,
	// which is why this is a bound and not the discriminator above.
	if got := fetches() - before; got != 1 {
		t.Errorf("the remote document was fetched %d time(s) during the request, want exactly 1: "+
			"more than one means a render refetched a reference the store already held, and none means "+
			"nothing reached the endpoint at all", got)
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
