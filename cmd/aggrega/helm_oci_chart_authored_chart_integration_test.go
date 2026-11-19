// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build integration

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcekind"
)

const (
	// helmChartConfigMediaType and helmChartLayerMediaType are the media types a
	// Helm push gives a chart's config and its one content layer.
	helmChartConfigMediaType = "application/vnd.cncf.helm.config.v1+json"
	helmChartLayerMediaType  = "application/vnd.cncf.helm.chart.content.v1.tar+gzip"
)

// chartArchive packs a minimal chart named name as the .tgz a Helm push stores.
func chartArchive(t *testing.T, name string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range [][2]string{
		{name + "/Chart.yaml", "apiVersion: v2\nname: " + name + "\nversion: 1.0.0\n"},
		{name + "/templates/cm.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}-chart\ndata:\n  origin: oci-chart\n"},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: f[0], Mode: 0o644, Size: int64(len(f[1]))}); err != nil {
			t.Fatalf("tar header %s: %v", f[0], err)
		}
		if _, err := tw.Write([]byte(f[1])); err != nil {
			t.Fatalf("tar body %s: %v", f[0], err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

// TestHelmOCIChartSourceWithAuthoredChartPullsAndBuilds drives a helm Recipe whose
// helm-oci-chart source writes chart: through pull and build against a loopback
// registry. The build finds the pulled chart by the chart the author wrote; before
// the lockfile row recorded that chart, the build halted with "no lockfile entry".
func TestHelmOCIChartSourceWithAuthoredChartPullsAndBuilds(t *testing.T) {
	host := startRegistry(t, nil)
	pushOCIModule(t, host, "charts/svc", "1.0.0", "",
		&ociLayer{MediaType: helmChartConfigMediaType, Content: []byte("{}")},
		ociLayer{MediaType: helmChartLayerMediaType, Content: chartArchive(t, "svc")})

	root := t.TempDir()
	t.Chdir(root)
	catalogDir := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\nauto_pull: false\n")
	writeFixture(t, filepath.Join(catalogDir, "components", "svc.yaml"), "apiVersion: aggrega.ai/v1beta1\n"+
		"kind: Component\nmetadata:\n  name: svc\n  version: \"1.0.0\"\n  labels:\n"+
		"    component.aggrega.ai/type: \"server\"\nspec:\n  schema:\n    values: {}\n")
	recipePath := filepath.Join(catalogDir, "recipes", "svc", "recipe.yaml")
	writeFixture(t, recipePath, "apiVersion: aggrega.ai/v1beta1\n"+
		"kind: Recipe\nmetadata:\n  name: svc\n  version: \"1.0.0\"\nspec:\n  capability: helm\n  implements: svc\n"+
		"  source:\n    kind: "+sourcekind.HelmOCIChart+"\n    url: oci://"+host+"/charts/svc\n    version: 1.0.0\n    chart: svc\n"+
		"  valuesMapping: |\n    {}\n")
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\n"+
		"metadata:\n  name: aggregate\n  version: \"1.0.0\"\nspec:\n  components:\n    svc:\n      type: svc\n")
	writeFixture(t, filepath.Join(root, "manifest.yaml"), "apiVersion: aggrega.ai/v1beta1\nkind: Manifest\n"+
		"metadata:\n  name: manifest\n  version: \"1.0.0\"\nspec:\n  mappings:\n    components:\n      type:\n        svc: svc\n")
	releasePath := filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, boundaryOutputRelease("oci-chart",
		"    main:\n      directory: ./dist/main\n      components:\n        svc: {}\n"))
	seedEmbeddedPlugins(t, filepath.Join(catalogDir, "providers"), "provider/helm")

	if err := pinAndPull(t, root, releasePath, catalogDir, []string{recipePath}); err != nil {
		t.Fatalf("pull: %s", cerrors.CauseChain(err))
	}
	rows := sourceRows(t, releasePath)
	if len(rows) != 1 || rows[0].Chart != "svc" {
		t.Fatalf("want one source row recording the authored chart %q; got %+v", "svc", rows)
	}

	err := buildOmitsWorkspace(t, root, releasePath, catalogDir, false)
	if err != nil {
		t.Fatalf("build: %s", cerrors.CauseChain(err))
	}
	var rendered bool
	for _, body := range readTree(t, filepath.Join(root, "dist")) {
		if strings.Contains(body, "origin: oci-chart") {
			rendered = true
		}
	}
	if !rendered {
		t.Error("the build's output does not carry the pulled chart's ConfigMap")
	}
}
