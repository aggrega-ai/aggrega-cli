// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
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

// pinChartTgz builds a minimal, deterministic Helm chart archive (nested under a
// single <chartname>/ directory, the real OCI layout) whose single template
// renders from .Release.Name alone, so it renders with no supplied values.
func pinChartTgz(t *testing.T) []byte {
	t.Helper()
	entries := map[string]string{
		"pinchart/Chart.yaml":        "apiVersion: v2\nname: pinchart\nversion: 1.0.0\n",
		"pinchart/templates/cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}-pin\ndata:\n  origin: cache-extracted\n",
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	// Deterministic entry order.
	for _, name := range []string{"pinchart/Chart.yaml", "pinchart/templates/cm.yaml"} {
		body := entries[name]
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0).UTC(), Format: tar.FormatPAX}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("tar body: %v", err)
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func sha256OCI(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("sha256:%x", sum[:])
}

// sourcedPinFixture authors a buildable workspace whose `server` Recipe sources a
// remote helm-https-chart at chartURL. Returns the release path.
func sourcedPinFixture(t *testing.T, root, chartURL string) string {
	t.Helper()
	catalogRoot := filepath.Join(root, "catalog")
	// auto_pull off so `build` is hermetic — it reads the pull-frozen lockfile and
	// cached source rather than re-fetching from the network, which is the state a
	// committed release builds in and the state the recipe-pin verify runs against.
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\nauto_pull: false\n")
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
	writeFixture(t, filepath.Join(catalogRoot, "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
	writeFixture(t, filepath.Join(catalogRoot, "providers", "helm.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Provider
metadata:
  name: helm
  version: "1.0.0"
spec:
  plugin: "builtin:helm"
`)
	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "providers"))
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "server.yaml"), fmt.Sprintf(`apiVersion: aggrega.ai/v1alpha1
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
    version: "1.0.0"
`, chartURL))
	return filepath.Join(root, "release.yaml")
}

func serveChart(t *testing.T, blob []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(blob)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestE1FreezeAtResolution proves an unpinned local Recipe with a remote source,
// once pulled, carries a sourceDigests pin whose digest equals the fetched
// archive's hash AND the lockfile records the matching source_artifacts entry.
func TestE1FreezeAtResolution(t *testing.T) {
	blob := pinChartTgz(t)
	srv := serveChart(t, blob)
	root := t.TempDir()
	t.Chdir(root)
	chartURL := srv.URL + "/pinchart.tgz"
	releasePath := sourcedPinFixture(t, root, chartURL)
	recipePath := filepath.Join(root, "catalog", "recipes", "server.yaml")

	// Before pull: no pins.
	if strings.Contains(string(mustReadFile(t, recipePath)), "sourceDigests") {
		t.Fatalf("recipe carried sourceDigests before pull")
	}

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{filepath.Join(root, "catalog")}}); err != nil {
		t.Fatalf("pullRun: %v", err)
	}

	// After pull: the recipe file carries sourceDigests == the fetched hash.
	recipeBody := string(mustReadFile(t, recipePath))
	if !strings.Contains(recipeBody, "sourceDigests") {
		t.Fatalf("freeze did not write sourceDigests into the recipe:\n%s", recipeBody)
	}
	wantDigest := sha256OCI(blob)
	if !strings.Contains(recipeBody, wantDigest) {
		t.Errorf("recipe sourceDigests missing fetched hash %s:\n%s", wantDigest, recipeBody)
	}
	if !strings.Contains(recipeBody, chartURL) {
		t.Errorf("recipe sourceDigests missing url %s:\n%s", chartURL, recipeBody)
	}

	// The lockfile records the matching source_artifacts entry.
	lf := string(mustReadFile(t, filepath.Join(root, "release.lock")))
	if !strings.Contains(lf, "source_artifacts:") {
		t.Errorf("lockfile missing source_artifacts:\n%s", lf)
	}
	if !strings.Contains(lf, wantDigest) {
		t.Errorf("lockfile source_artifacts missing digest %s:\n%s", wantDigest, lf)
	}
}

// TestE1PullIsIdempotentAfterFreeze proves a second pull over the now-frozen
// recipe rewrites a byte-identical lockfile (modulo generated_at) and leaves the
// recipe file byte-identical — the freeze reaches a fixpoint after one pull.
func TestE1PullIsIdempotentAfterFreeze(t *testing.T) {
	blob := pinChartTgz(t)
	srv := serveChart(t, blob)
	root := t.TempDir()
	t.Chdir(root)
	releasePath := sourcedPinFixture(t, root, srv.URL+"/pinchart.tgz")
	recipePath := filepath.Join(root, "catalog", "recipes", "server.yaml")
	catalogDir := filepath.Join(root, "catalog")

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("first pull: %v", err)
	}
	recipe1 := mustReadFile(t, recipePath)
	lock1 := stripGeneratedAt(mustReadFile(t, filepath.Join(root, "release.lock")))

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("second pull: %v", err)
	}
	recipe2 := mustReadFile(t, recipePath)
	lock2 := stripGeneratedAt(mustReadFile(t, filepath.Join(root, "release.lock")))

	if !bytes.Equal(recipe1, recipe2) {
		t.Errorf("second pull churned the frozen recipe:\n--- first ---\n%s\n--- second ---\n%s", recipe1, recipe2)
	}
	if !bytes.Equal(lock1, lock2) {
		t.Errorf("second pull churned the lockfile (excluding generated_at):\n--- first ---\n%s\n--- second ---\n%s", lock1, lock2)
	}
}

// TestE9DeterministicBuildAfterFreeze proves the frozen release builds twice to
// byte-identical dist output.
func TestE9DeterministicBuildAfterFreeze(t *testing.T) {
	blob := pinChartTgz(t)
	srv := serveChart(t, blob)
	root := t.TempDir()
	t.Chdir(root)
	releasePath := sourcedPinFixture(t, root, srv.URL+"/pinchart.tgz")
	catalogDir := filepath.Join(root, "catalog")

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if err := dispatchBuild(nil, releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("first build: %v", err)
	}
	first := snapshotDist(t, filepath.Join(root, "dist"))
	if len(first) == 0 {
		t.Fatalf("first build produced no dist files")
	}
	if err := os.RemoveAll(filepath.Join(root, "dist")); err != nil {
		t.Fatalf("clean dist: %v", err)
	}
	if err := dispatchBuild(nil, releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("second build: %v", err)
	}
	second := snapshotDist(t, filepath.Join(root, "dist"))
	if len(first) != len(second) {
		t.Fatalf("build produced different file counts: %d vs %d", len(first), len(second))
	}
	for name, body := range first {
		if second[name] != body {
			t.Errorf("dist file %q differs between builds:\n--- first ---\n%s\n--- second ---\n%s", name, body, second[name])
		}
	}
}

// TestE2SourceTamperHaltsBuild proves that corrupting the cached source blob
// after freeze makes the build halt with a loud ErrFetchIntegrity.
func TestE2SourceTamperHaltsBuild(t *testing.T) {
	blob := pinChartTgz(t)
	srv := serveChart(t, blob)
	root := t.TempDir()
	t.Chdir(root)
	releasePath := sourcedPinFixture(t, root, srv.URL+"/pinchart.tgz")
	catalogDir := filepath.Join(root, "catalog")

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	// Close the source server so the build cannot silently re-fetch and heal the
	// tampered cache — the build must trust (and verify) the cached bytes alone.
	srv.Close()
	// Tamper every cached blob.tgz under the workspace cache.
	tampered := 0
	_ = filepath.Walk(filepath.Join(root, ".aggrega", "cache"), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Base(p) != "blob.tgz" {
			return nil
		}
		if werr := os.WriteFile(p, []byte("corrupt-not-a-real-tgz"), 0o644); werr != nil {
			t.Fatalf("tamper blob: %v", werr)
		}
		tampered++
		return nil
	})
	if tampered == 0 {
		t.Fatalf("no cached blob.tgz found to tamper")
	}

	err := dispatchBuild(nil, releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout)
	if err == nil {
		t.Fatalf("build succeeded over a tampered source; expected a loud halt")
	}
	if !errors.Is(err, oci.ErrFetchIntegrity) {
		t.Errorf("tampered-source build error = %v; want ErrFetchIntegrity", err)
	}
}

func mustReadFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return b
}

func snapshotDist(t *testing.T, dist string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.Walk(dist, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dist, p)
		out[rel] = string(mustReadFile(t, p))
		return nil
	})
	return out
}
