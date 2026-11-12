// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// sourcedRepoRootFixture authors a buildable workspace whose `server` Recipe
// sources a helm-https-chart in the REPO-ROOT form: spec.source.url is a helm
// repository root (no .tgz suffix) and spec.source.chart names the chart. This
// is the shape a chart pulled from a helm repo index (contour, sealed-secrets,
// bitnami, ...) takes, as opposed to a direct .tgz URL. Returns the release path.
func sourcedRepoRootFixture(t *testing.T, root, repoRootURL, chart string) string {
	t.Helper()
	catalogRoot := filepath.Join(root, "catalog")
	// pin_sources_at_pull on: the pull under test records the recipe's source pin
	// beside it, which by default a pull does not do and halts for.
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\nauto_pull: false\npin_sources_at_pull: true\n")
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    api:
      type: server
      values:
        port: 8080
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: pin-rel
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
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        server: server
`)
	writeFixture(t, filepath.Join(catalogRoot, "components", "server.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`)
	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "providers"))
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "server.yaml"), fmt.Sprintf(`apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: server
  version: "1.0.0"
spec:
  capability: helm
  implements: server
  source:
    kind: helm-https-chart
    url: %s
    chart: %s
    version: "1.0.0"
`, repoRootURL, chart))
	return filepath.Join(root, "release.yaml")
}

// serveHelmRepo serves a minimal helm repository: an index.yaml at
// <root>/index.yaml that maps chart@version to the direct .tgz URL, and the
// chart archive itself at <root>/<chart>-<version>.tgz. The index's urls entry
// is an absolute URL back to this same test server, mirroring how a real helm
// repo index points at its artifacts.
func serveHelmRepo(t *testing.T, chart, version string, blob []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	tgzPath := "/" + chart + "-" + version + ".tgz"
	index := fmt.Sprintf(`apiVersion: v1
entries:
  %s:
  - name: %s
    version: %s
    urls:
    - %s
`, chart, chart, version, srv.URL+tgzPath)
	mux.HandleFunc("/index.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-yaml")
		_, _ = w.Write([]byte(index))
	})
	mux.HandleFunc(tgzPath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(blob)
	})
	return srv
}

// TestHTTPSRepoRootBuildReproduction drives the full pull -> build flow for a
// helm-https-chart authored in the REPO-ROOT form. Pull resolves the repo index
// to a direct .tgz and caches it; build must then find that cached content and
// render. The chart named "pinchart" reuses pinChartTgz so it renders with no
// supplied values.
func TestHTTPSRepoRootBuildReproduction(t *testing.T) {
	blob := pinChartTgz(t)
	srv := serveHelmRepo(t, "pinchart", "1.0.0", blob)
	root := t.TempDir()
	t.Chdir(root)
	releasePath := sourcedRepoRootFixture(t, root, srv.URL, "pinchart")
	catalogDir := filepath.Join(root, "catalog")

	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("pullRun: %v", err)
	}

	// Prove pull succeeded and recorded a source artifact.
	lf := string(mustReadFile(t, filepath.Join(root, "release.lock")))
	if !strings.Contains(lf, "source_artifacts:") {
		t.Fatalf("pull did not record a source artifact:\n%s", lf)
	}
	t.Logf("LOCKFILE source_artifacts region:\n%s", lockfileSourceRegion(lf))

	// Build must succeed over the pulled repo-root chart.
	err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout)
	if err != nil {
		t.Logf("FULL ERROR CHAIN: %v", err)
		t.Logf("errors.Is(err, oci.ErrFetchNotFound)  = %v", errors.Is(err, oci.ErrFetchNotFound()))
		t.Logf("errors.Is(err, oci.ErrFetchIntegrity) = %v", errors.Is(err, oci.ErrFetchIntegrity()))
		for e := err; e != nil; e = errors.Unwrap(e) {
			t.Logf("  wrap: %s", e.Error())
		}
		t.Fatalf("build FAILED for a repo-root https chart that pulled successfully")
	}
	first := snapshotDist(t, filepath.Join(root, "dist"))
	if len(first) == 0 {
		t.Fatalf("build produced no dist files")
	}
}

// namedChartTgz builds a minimal, deterministic helm chart archive that untars
// under a <name>/ directory and renders with no supplied values. Two charts with
// distinct names produce distinct archives that extract to distinct chart roots.
func namedChartTgz(t *testing.T, name string) []byte {
	t.Helper()
	entries := []struct{ path, body string }{
		{name + "/Chart.yaml", "apiVersion: v2\nname: " + name + "\nversion: 1.0.0\n"},
		{name + "/templates/cm.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}-" + name + "\ndata:\n  chart: " + name + "\n"},
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.path, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0).UTC(), Format: tar.FormatPAX}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatalf("tar body: %v", err)
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// serveHelmRepoMulti serves a helm repository whose index.yaml lists several
// charts, all at the same version, from one repo root — the exact shape that
// exercises the multi-chart-per-repo collision. Each chart archive is served at
// <root>/<chart>-<version>.tgz and the index points at it with an absolute URL.
func serveHelmRepoMulti(t *testing.T, version string, charts map[string][]byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	var b strings.Builder
	b.WriteString("apiVersion: v1\nentries:\n")
	for chart, blob := range charts {
		tgzPath := "/" + chart + "-" + version + ".tgz"
		fmt.Fprintf(&b, "  %s:\n  - name: %s\n    version: %s\n    urls:\n    - %s\n",
			chart, chart, version, srv.URL+tgzPath)
		body := blob
		mux.HandleFunc(tgzPath, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write(body)
		})
	}
	index := b.String()
	mux.HandleFunc("/index.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-yaml")
		_, _ = w.Write([]byte(index))
	})
	return srv
}

// collisionRepoRootFixture authors a workspace whose two components resolve two
// different charts served from the SAME repo root at the SAME version — via two
// recipes whose repo-root source URLs are identical and whose chart names differ.
// This is the multi-chart-per-repo case the authored-URL identity keeps distinct.
func collisionRepoRootFixture(t *testing.T, root, repoRootURL, chartA, chartB string) string {
	t.Helper()
	catalogRoot := filepath.Join(root, "catalog")
	// pin_sources_at_pull on: the pull under test records the recipe's source pin
	// beside it, which by default a pull does not do and halts for.
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\nauto_pull: false\npin_sources_at_pull: true\n")
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    api:
      type: server
      values:
        port: 8080
    bg:
      type: worker
      values: {}
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: pin-rel
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
        bg:
          infra:
            replicas: 1
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        server: server
        worker: worker
`)
	writeFixture(t, filepath.Join(catalogRoot, "components", "server.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`)
	writeFixture(t, filepath.Join(catalogRoot, "components", "worker.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: worker
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "worker"
spec:
  schema:
    values: {}
`)
	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "providers"))
	// Each Recipe in a directory of its own: the pull pins both, and a pin writes
	// the one lock its entity's directory holds.
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "server", "server.yaml"), fmt.Sprintf(`apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: server
  version: "1.0.0"
spec:
  capability: helm
  implements: server
  source:
    kind: helm-https-chart
    url: %s
    chart: %s
    version: "1.0.0"
`, repoRootURL, chartA))
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "worker", "worker.yaml"), fmt.Sprintf(`apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: worker
  version: "1.0.0"
spec:
  capability: helm
  implements: worker
  source:
    kind: helm-https-chart
    url: %s
    chart: %s
    version: "1.0.0"
`, repoRootURL, chartB))
	return filepath.Join(root, "release.yaml")
}

// TestHTTPSRepoRootChartCollision proves two charts served from the SAME repo
// root at the SAME version stay distinct: because the source-artifact identity is
// keyed on the authored URL PLUS the chart name (not on the resolved .tgz), the
// two resolve to distinct cache slots and distinct lockfile entries, and both
// render. Before chart entered the identity these two would have collided on the
// {kind, url, version} triple.
func TestHTTPSRepoRootChartCollision(t *testing.T) {
	blobA := namedChartTgz(t, "pinchart")
	blobB := namedChartTgz(t, "otherchart")
	srv := serveHelmRepoMulti(t, "1.0.0", map[string][]byte{"pinchart": blobA, "otherchart": blobB})
	root := t.TempDir()
	t.Chdir(root)
	releasePath := collisionRepoRootFixture(t, root, srv.URL, "pinchart", "otherchart")
	catalogDir := filepath.Join(root, "catalog")

	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("pullRun: %v", err)
	}

	// Two distinct lockfile source-artifact entries, one per chart, sharing the
	// same repo-root URL and version.
	lf := string(mustReadFile(t, filepath.Join(root, "release.lock")))
	if !strings.Contains(lf, "chart: pinchart") || !strings.Contains(lf, "chart: otherchart") {
		t.Fatalf("lockfile should carry a distinct entry per chart:\n%s", lockfileSourceRegion(lf))
	}
	if strings.Count(lf, "pinned_version: 1.0.0") < 2 {
		t.Fatalf("expected two source artifacts at the same version:\n%s", lockfileSourceRegion(lf))
	}

	// Distinct cache slots: the two charts occupy different directories under the
	// source-artifact cache even though their repo-root URL and version match.
	slotsRoot := filepath.Join(root, ".aggrega", "cache", "source-artifacts", "helm-https-chart")
	if !hasSubdir(t, slotsRoot, "pinchart") || !hasSubdir(t, slotsRoot, "otherchart") {
		t.Fatalf("expected distinct per-chart cache slots under %s", slotsRoot)
	}

	// Both render.
	if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Logf("errors.Is(err, oci.ErrFetchNotFound) = %v", errors.Is(err, oci.ErrFetchNotFound()))
		t.Fatalf("build FAILED for two charts colliding on one repo root: %v", err)
	}
	dist := snapshotDist(t, filepath.Join(root, "dist"))
	if len(dist) == 0 {
		t.Fatalf("build produced no dist files")
	}
}

// hasSubdir reports whether a directory named name exists anywhere beneath root.
func hasSubdir(t *testing.T, root, name string) bool {
	t.Helper()
	found := false
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() && info.Name() == name {
			found = true
		}
		return nil
	})
	return found
}

func lockfileSourceRegion(lf string) string {
	idx := strings.Index(lf, "source_artifacts:")
	if idx < 0 {
		return "(none)"
	}
	region := lf[idx:]
	if len(region) > 800 {
		region = region[:800]
	}
	return region
}
