// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

// Package main's hermetic-reconstructability proof: a lockfile a resolution has
// just written is complete enough to build from with no network at all.
//
// The property is about the lockfile's COMPLETENESS ACROSS THE KINDS OF
// DEPENDENCY IT RECORDS, so the fixture is chosen on that axis rather than on how
// much of the compiler it touches. Its closure reaches catalog entities, all four
// library kinds, a remote source artifact, a remote external input and a
// transform-role governance attachment in one Release; the census below reads the
// lockfile the resolution actually wrote and fails when a section it claims to
// cover came back empty, so the breadth is measured rather than asserted in prose.
//
// The build step is transport-free the way this suite already makes a step
// transport-free: the endpoints are closed, so a fetch that is attempted fails,
// and the workspace is declared offline, so the egress gate refuses before any
// dial. The two refuse at different places, which is why both are applied — a step
// that merely does not need the network on a warm machine is not the claim.
//
// Build-tagged `integration`, which is the whole of its coverage bound: the tag
// only ADDS files, so a run that does not pass it compiles this file out and
// cannot execute anything in it. The tagged run is the authoritative one.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// hermeticValuesSchema is the document the schema endpoint serves. It is
// permissive, so the chart's values validate against it and the render reaches its
// output — a schema that rejected the values would stop the render for a reason
// that has nothing to do with pinning.
const hermeticValuesSchema = `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`

// countingServer is a loopback endpoint serving body, carrying a reader of how
// many times it has been asked for it and a closer. The count is read across run
// boundaries so each assertion attributes a fetch to the run that made it: a
// lockfile carrying a pin says nothing about which run wrote it, and an offline
// build serving a pinned input renders identically to one that never needed it.
func countingServer(t *testing.T, contentType string, body []byte) (url string, fetches func() int, closeSrv func()) {
	t.Helper()
	served := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served++
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		_, _ = w.Write(body)
	}))
	return srv.URL, func() int { return served }, srv.Close
}

// hermeticFixtureCatalogDirs is the catalog search stack the fixture builds
// against, returned so every command in a case is handed the identical stack.
func hermeticFixtureCatalogDirs(root string) []string {
	return []string{filepath.Join(root, "catalog")}
}

// writeHermeticClosureFixture authors a workspace whose Release closure reaches as
// many kinds of recorded dependency as one Release can, and returns the Release
// path.
//
// Three components carry the breadth rather than one, because the shapes are
// mutually exclusive per Recipe: a chart-sourced Recipe carries the values mapping,
// a locally-sourced one carries the chart whose values schema names a remote
// document, and a sourceless one synthesizes its workload and its extra artifacts
// from libraries.
func writeHermeticClosureFixture(t *testing.T, root, chartURL, schemaURL string) string {
	t.Helper()
	catalogDir := filepath.Join(root, "catalog")

	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: hermetic-aggregate
  version: "1.0.0"
spec:
  components:
    api:
      type: core/api
    cache:
      type: core/cache
      values:
        tag: "v3"
    queue:
      type: core/queue
      values:
        port: 8080
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: hermetic-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          infra:
            replicas: 1
        cache:
          infra:
            replicas: 1
        queue:
          infra:
            replicas: 1
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: hermetic-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/api: api-recipe
        core/cache: cache-recipe
        core/queue: queue-recipe
`)

	writeFixture(t, filepath.Join(catalogDir, "components", "api.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/api
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values: {}
    infra:
      replicas: { type: integer, default: 1 }
`)
	writeFixture(t, filepath.Join(catalogDir, "components", "cache.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/cache
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      tag: { type: string, default: "v3" }
    infra:
      replicas: { type: integer, default: 1 }
`)
	// The queue Component's infra.storage is sourced by a bare-name cross-document
	// $ref into a SchemaLibrary, which is what puts that library in the closure —
	// and the workload below echoes the resolved default, so the library's
	// contribution is observable in the rendered artifact rather than only in a pin.
	writeFixture(t, filepath.Join(catalogDir, "components", "queue.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/queue
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
    infra:
      replicas: { type: integer, default: 1 }
      storage:
        $ref: "DbSizing#/schemas/Storage"
`)

	writeFixture(t, filepath.Join(catalogDir, "schemalibraries", "dbsizing.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: DbSizing
  version: "1.0.0"
spec:
  schemas:
    Storage:
      size: { type: string, default: "10Gi" }
`)
	writeFixture(t, filepath.Join(catalogDir, "mappinglibraries", "helmhelpers.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: MappingLibrary
metadata:
  name: HelmHelpers
  version: "1.0.0"
spec:
  mappings:
    CacheValues: |
      image: "hermetic-cache:{{ .values.tag }}"
`)
	writeFixture(t, filepath.Join(catalogDir, "workloadlibraries", "baseworkloads.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: WorkloadLibrary
metadata:
  name: BaseWorkloads
  version: "1.0.0"
spec:
  workloads:
    StatelessService: |
      apiVersion: aggrega.ai/v1alpha1
      kind: Workload
      replicas: {{ .infra.replicas | default 1 }}
      containers:
        main:
          image: hermetic-queue:v1
          variables:
            STORAGE: "{{ .infra.storage.size }}"
            NODE_LABELS: "{{ range $k, $v := .context.labels }}{{ $k }}={{ $v }};{{ end }}"
          ports:
            web: {{ .values.port }}
`)
	writeFixture(t, filepath.Join(catalogDir, "artifactslibraries", "sharedartifacts.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: ArtifactsLibrary
metadata:
  name: SharedArtifacts
  version: "1.0.0"
spec:
  artifacts:
    QueueBundle:
      queue-config.yaml: |
        apiVersion: v1
        kind: ConfigMap
        metadata:
          name: queue-{{ .values.port }}-config
        data:
          port: "{{ .values.port }}"
`)

	writeHelmRendererPlugin(t, filepath.Join(catalogDir, "providers"))
	writeFixture(t, filepath.Join(catalogDir, "plugins", "label-injector.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: label-injector
  version: "1.0.0"
  pluginType: governance
spec:
  abiVersion: v1alpha1
  exports:
    - name: mutate
`)

	// The api Recipe sources a remote chart, which is the source-artifact pin. Its
	// rendered document exists only if the cached archive served the offline build.
	// Each Recipe sits in a directory of its own, because the pull below pins, and
	// a pin writes the one lock its entity's directory holds.
	writeFixture(t, filepath.Join(catalogDir, "recipes", "api", "api.yaml"), fmt.Sprintf(`apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: api-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/api
  source:
    kind: helm-https-chart
    url: %s
    version: "1.0.0"
`, chartURL))

	// The cache Recipe's local chart carries a values schema naming a remote
	// document, which is what makes a render REQUEST an external input: the guest
	// compiles that schema through the host's fetch seam. Its values are overlaid
	// from a mapping-library entry, and the chart echoes the overlaid image, so that
	// library's contribution is observable in the rendered artifact too.
	cacheChart := filepath.Join(root, "charts", "cache")
	writeFixture(t, filepath.Join(cacheChart, "Chart.yaml"), "apiVersion: v2\nname: cache\nversion: 0.1.0\n")
	writeFixture(t, filepath.Join(cacheChart, "values.yaml"), "image: placeholder:latest\n")
	writeFixture(t, filepath.Join(cacheChart, "templates", "cm.yaml"), `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}-cache
data:
  image: {{ .Values.image | quote }}
`)
	writeFixture(t, filepath.Join(cacheChart, "values.schema.json"),
		fmt.Sprintf(`{"$schema":"https://json-schema.org/draft/2020-12/schema","$ref":%q}`, schemaURL))
	writeFixture(t, filepath.Join(catalogDir, "recipes", "cache", "cache.yaml"), fmt.Sprintf(`apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: cache-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/cache
  source: %q
  valuesMapping:
    $import: HelmHelpers#/mappings/CacheValues
`, cacheChart))

	// The queue Recipe is sourceless: its workload and its extra artifacts each
	// come from a library, which is what puts those two library kinds in the
	// closure.
	writeFixture(t, filepath.Join(catalogDir, "recipes", "queue", "queue.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: queue-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/queue
  workload:
    $import: BaseWorkloads#/workloads/StatelessService
  artifacts:
    bundle:
      $import: SharedArtifacts#/artifacts/QueueBundle
`)

	return filepath.Join(root, "release.yaml")
}

// hermeticWorkspaceConfig is the workspace declaration both runs read, with the
// egress posture the only thing that moves between them. The governance block
// attaches a transform-role plugin, which is what puts an attachment in the
// lockfile: a transform shapes output bytes, so its attachment set is pinned.
func hermeticWorkspaceConfig(offline bool) string {
	return fmt.Sprintf(`workspace: {}
offline: %v
plugins:
  governance:
    - uri: governance/label-injector:1.0.0
      role: label-transform
      with:
        settings:
          rules:
            - match:
                component.aggrega.ai/type: server
              set:
                inject.aggrega.ai/mark: hit
`, offline)
}

// lockfileAxisCensus counts the entries the lockfile records on each axis a
// dependency can be recorded on. The criterion is one entry per section of the
// written lockfile, read from the loaded document rather than from its text.
//
// Every section carrying a dependency is listed, INCLUDING the ones this fixture
// does not reach, because a census that enumerated only what it found could not
// distinguish a section that came back empty from one nobody thought to look at.
func lockfileAxisCensus(lf *lockfile.Lockfile) map[string]int {
	return map[string]int{
		"entities":            len(lf.Entities),
		"schema_libraries":    len(lf.SchemaLibraries),
		"mapping_libraries":   len(lf.MappingLibraries),
		"workload_libraries":  len(lf.WorkloadLibraries),
		"artifacts_libraries": len(lf.ArtifactsLibraries),
		"source_artifacts":    len(lf.SourceArtifacts),
		"external_inputs":     len(lf.ExternalInputs),
		"plugins":             len(lf.Plugins),
		"providers":           len(lf.Providers),
		"mutators":            len(lf.Mutators),
		"verdict_inputs":      len(lf.VerdictInputs),
	}
}

// hermeticCoveredAxes names the sections this fixture claims to populate. It is
// asserted rather than described, so a fixture edit that silently stopped reaching
// one fails here instead of leaving the proof narrower than its own documentation.
// The one section deliberately absent from this list is verdict_inputs, which no
// build reads: a verdict attachment shapes no output bytes, so its recorded rule
// digests are provenance rather than a build input and their absence cannot make a
// lockfile incomplete for the purpose asserted here.
var hermeticCoveredAxes = []string{
	"entities",
	"schema_libraries",
	"mapping_libraries",
	"workload_libraries",
	"artifacts_libraries",
	"source_artifacts",
	"external_inputs",
	"plugins",
	"providers",
	"mutators",
}

// pullThenCensus resolves the Release while connected and returns the lockfile it
// wrote, failing when a section this fixture claims to cover came back empty.
func pullThenCensus(t *testing.T, root, releasePath string) *lockfile.Lockfile {
	t.Helper()
	var diag, out bytes.Buffer
	// The resolution runs the pin step, as --pin-sources does: the fixture's
	// Recipe carries an authored-identity chart source, and by default a pull
	// halts on an entity whose lock does not record its pin.
	pinSources := true
	err := pullRun(context.Background(), pullConfig{
		ws:          resolveWorkspaceWithConfig(t, root, hermeticWorkspaceConfig(false)),
		ReleasePath: releasePath,
		CatalogDirs: hermeticFixtureCatalogDirs(root),
		PinSources:  &pinSources,
		DiagOut:     &diag,
		Out:         &out,
	})
	if err != nil {
		t.Fatalf("resolve while connected: %v\ndiagnostics:\n%s", err, diag.String())
	}

	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	census := lockfileAxisCensus(lf)
	t.Logf("lockfile axis census: %v", census)
	for _, axis := range hermeticCoveredAxes {
		if census[axis] == 0 {
			t.Fatalf("the fixture claims to reach %s and the written lockfile records none; census: %v", axis, census)
		}
	}
	return lf
}

// buildOffline runs a plain build with the endpoints already closed and the
// workspace declared offline, and returns whatever the build reported.
func buildOffline(t *testing.T, root, releasePath string, diag, out *bytes.Buffer) error {
	t.Helper()
	ws := resolveWorkspaceWithConfig(t, root, hermeticWorkspaceConfig(true))
	if !ws.Config().Offline {
		t.Fatal("the workspace did not come back offline, so the denial this case rests on is not in effect")
	}
	return dispatchBuildWithDiag(context.Background(), ws, releasePath, hermeticFixtureCatalogDirs(root), false,
		buildDiagOptions{}, buildops.RunOptions{}, diag, out)
}

// TestFreshlyWrittenLockfileBuildsWithNoNetwork is the property this file exists
// for: a resolution writes a lockfile, and a plain build from that lockfile alone —
// no fill, no reachable endpoint, no permitted egress — renders the Release.
//
// The fetch counters are the load-bearing instrument. Without them the case passes
// on a fixture whose render never wanted anything remote, which is the shape a
// hermeticity proof is worst at noticing about itself.
func TestFreshlyWrittenLockfileBuildsWithNoNetwork(t *testing.T) {
	nonInteractive(t)
	root := t.TempDir()
	t.Chdir(root)

	chartBlob := pinChartTgz(t)
	chartBase, chartFetches, closeChart := countingServer(t, "application/gzip", chartBlob)
	schemaBase, schemaFetches, closeSchema := countingServer(t, "", []byte(hermeticValuesSchema))

	releasePath := writeHermeticClosureFixture(t, root, chartBase+"/pinchart.tgz", schemaBase+"/values-schema.json")
	pullThenCensus(t, root, releasePath)

	if chartFetches() == 0 || schemaFetches() == 0 {
		closeChart()
		closeSchema()
		t.Fatalf("the resolution reached neither endpoint (chart=%d schema=%d), so this fixture proves nothing about acquisition",
			chartFetches(), schemaFetches())
	}
	closeChart()
	closeSchema()
	chartAfterPull, schemaAfterPull := chartFetches(), schemaFetches()

	var diag, out bytes.Buffer
	if err := buildOffline(t, root, releasePath, &diag, &out); err != nil {
		t.Fatalf("a build from a freshly written lockfile must render with no network: %v\ndiagnostics:\n%s", err, diag.String())
	}
	if chartFetches() != chartAfterPull || schemaFetches() != schemaAfterPull {
		t.Errorf("the offline build reached an endpoint: chart %d->%d, schema %d->%d",
			chartAfterPull, chartFetches(), schemaAfterPull, schemaFetches())
	}
	if !strings.Contains(out.String(), "Build completed") {
		t.Errorf("the offline build did not report a completed render; got:\n%s", out.String())
	}
	assertHermeticArtifacts(t, filepath.Join(root, "dist"))
}

// hermeticExpectedArtifacts is the rendered tree the fixture owes, with each entry
// carrying a marker that only ONE recorded dependency can have produced. A bare
// "something was written" check passes on a build that rendered a single node, and
// a path-set check passes on documents whose content came from somewhere else — so
// the paths and the markers are asserted together.
var hermeticExpectedArtifacts = []struct {
	path string
	// marker is a substring the document must carry, and from names the recorded
	// dependency it is evidence for.
	marker string
	from   string
}{
	// Only the archive the resolution fetched and cached carries this line, so it
	// is evidence the offline build read the pinned source artifact.
	{"dist/main/helm/api-configmap-api-pin.yaml", "origin: cache-extracted", "the pinned source artifact"},
	// The overlaid image is the mapping-library entry evaluated against the node's
	// values; the chart's own default is a different string.
	{"dist/main/helm/cache-configmap-cache-cache.yaml", `image: "hermetic-cache:v3"`, "the mapping library"},
	// The workload the queue node renders exists only in the workload library.
	{"dist/main/helm/queue-deployment.yaml", "image: hermetic-queue:v1", "the workload library"},
	// The storage default is declared in the schema library and nowhere else.
	{"dist/main/helm/queue-deployment.yaml", "value: 10Gi", "the schema library"},
	// The label is injected by the transform-role attachment the lockfile pins.
	{"dist/main/helm/queue-deployment.yaml", "inject.aggrega.ai/mark=hit", "the pinned governance attachment"},
	// The bundle's document and its path both come from the artifacts library.
	{"dist/main/helm/queue/bundle/queue-config.yaml", `port: "8080"`, "the artifacts library"},
}

// assertHermeticArtifacts requires the rendered tree to be exactly the expected
// document set and each document to carry its marker.
func assertHermeticArtifacts(t *testing.T, distRoot string) {
	t.Helper()
	rendered := snapshotDir(t, distRoot)
	wanted := map[string]bool{}
	for _, want := range hermeticExpectedArtifacts {
		wanted[want.path] = true
		body, ok := rendered[want.path]
		if !ok {
			t.Errorf("the offline build wrote no %s, so %s did not reach the render; tree: %v",
				want.path, want.from, sortedKeys(rendered))
			continue
		}
		if !strings.Contains(body, want.marker) {
			t.Errorf("%s does not carry %q, which is the evidence for %s; got:\n%s",
				want.path, want.marker, want.from, body)
		}
	}
	for path := range rendered {
		if !wanted[path] {
			t.Errorf("the offline build wrote an unexpected document %q; the expected set is %v", path, sortedKeys(wanted))
		}
	}
}

// TestIncompleteLockfileFailsTheOfflineBuild is the demonstration that the
// assertion above can fail. Each row removes ONE recorded entry from the lockfile
// the resolution wrote and requires the same offline build to halt naming what is
// missing — an assertion whose failure nobody has driven is decoration, and this
// one stands in for a check that used to fail loudly on its own.
func TestIncompleteLockfileFailsTheOfflineBuild(t *testing.T) {
	for _, tc := range []struct {
		name string
		// remove drops one entry and returns what the halt must name.
		remove func(t *testing.T, lf *lockfile.Lockfile) string
		// halted reports whether err carries the halting condition this row's
		// single fault pins. It is asserted rather than "some error", so a later
		// gate preempting this one cannot leave the row passing while covering
		// nothing.
		halted func(err error) bool
	}{
		{
			name: "external input",
			remove: func(t *testing.T, lf *lockfile.Lockfile) string {
				t.Helper()
				dropped := lf.ExternalInputs[0].URL
				lf.ExternalInputs = lf.ExternalInputs[1:]
				return dropped
			},
			// The unpinned-input refusal is raised where the render asks for the
			// document, which is inside the guest, and a guest's failure reaches
			// the host as text through the runtime's error channel — so the host's
			// error wraps a sealed report rather than the condition value itself
			// and an identity comparison cannot match. The condition's own message,
			// read from its exported accessor rather than transcribed, is therefore
			// the strongest form available at this boundary; it moves with the
			// declaration instead of drifting from it.
			halted: func(err error) bool {
				return chainContains(err, externalinputs.ErrAcquisitionDisabled().Error())
			},
		},
		{
			name: "source artifact",
			remove: func(t *testing.T, lf *lockfile.Lockfile) string {
				t.Helper()
				dropped := lf.SourceArtifacts[0].URL
				lf.SourceArtifacts = lf.SourceArtifacts[1:]
				return dropped
			},
			// The source read happens host-side, so the condition value survives
			// the chain and the identity comparison is available here.
			halted: func(err error) bool { return errors.Is(err, oci.ErrFetchNotFound()) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nonInteractive(t)
			root := t.TempDir()
			t.Chdir(root)

			chartBlob := pinChartTgz(t)
			chartBase, _, closeChart := countingServer(t, "application/gzip", chartBlob)
			schemaBase, _, closeSchema := countingServer(t, "", []byte(hermeticValuesSchema))

			releasePath := writeHermeticClosureFixture(t, root, chartBase+"/pinchart.tgz", schemaBase+"/values-schema.json")
			lf := pullThenCensus(t, root, releasePath)
			closeChart()
			closeSchema()

			missing := tc.remove(t, lf)
			if err := lockfile.WriteLockfilePreservingTimestamp(lockfile.PathFor(releasePath), lf); err != nil {
				t.Fatalf("write the incomplete lockfile: %v", err)
			}

			var diag, out bytes.Buffer
			err := buildOffline(t, root, releasePath, &diag, &out)
			if err == nil {
				t.Fatalf("the offline build accepted a lockfile missing %s %q, so the completeness assertion cannot fail:\noutput:\n%s",
					tc.name, missing, out.String())
			}
			if !tc.halted(err) {
				t.Errorf("the halt does not carry the condition this row's fault pins; got: %v\ndiagnostics:\n%s", err, diag.String())
			}
			if !chainContains(err, missing) {
				t.Errorf("the halt does not name the missing %s %q; got: %v\ndiagnostics:\n%s", tc.name, missing, err, diag.String())
			}
			t.Logf("halt on a lockfile missing %s %q: %v", tc.name, missing, err)
		})
	}
}
