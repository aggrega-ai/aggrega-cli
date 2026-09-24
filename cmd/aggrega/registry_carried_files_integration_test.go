// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
)

// carriedFilesPublisher is a publisher tree whose Recipe and FeatureRecipe each
// carry a local chart beside them, so a consumer can only render them if it
// reads the files the published archive carries.
var carriedFilesPublisher = map[string]string{
	"component/acme/compute/bannered/1.0.0/component.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: acme/compute/bannered
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
    component.aggrega.ai/category: compute
spec:
  featuresSchema:
    banner:
      inject: []
  schema:
    values:
      greeting: { type: string, default: hello }
`,
	"recipe/helm/bannered-local/1.0.0/recipe.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: bannered-local
  version: 1.0.0
spec:
  capability: helm
  implements: acme/compute/bannered:1.0.0
  source: ./chart
  valuesMapping: |
    {"greeting": "{{ .values.greeting }}"}
`,
	"recipe/helm/bannered-local/1.0.0/chart/Chart.yaml": "apiVersion: v2\nname: bannered\nversion: 0.1.0\n",
	"recipe/helm/bannered-local/1.0.0/chart/templates/greeting.yaml": `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}-greeting
data:
  greeting: {{ .Values.greeting | quote }}
  carried-by: the-chart-beside-the-recipe
`,
	"feature/acme/banner/1.0.0/feature.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: acme/banner
  version: 1.0.0
spec:
  schema:
    values:
      text: { type: string, default: welcome }
`,
	"feature-recipe/helm/banner-local/1.0.0/recipe.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: banner-local
  version: 1.0.0
spec:
  capability: helm
  implements: acme/banner:1.0.0
  source: ./chart
  valuesMapping: |
    name: "{{ .context.instance_id }}-banner"
    text: "{{ .values.text }}"
`,
	"component/acme/compute/templated/1.0.0/component.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: acme/compute/templated
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
    component.aggrega.ai/category: compute
spec:
  schema:
    values:
      greeting: { type: string, default: hello }
`,
	"recipe/helm/templated-local/1.0.0/recipe.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: templated-local
  version: 1.0.0
spec:
  capability: helm
  implements: acme/compute/templated:1.0.0
  locals:
    chart_dir: ./chart
  source: "{{ .locals.chart_dir }}"
  valuesMapping: |
    {"greeting": "{{ .values.greeting }}"}
`,
	"recipe/helm/templated-local/1.0.0/chart/Chart.yaml": "apiVersion: v2\nname: templated\nversion: 0.1.0\n",
	"recipe/helm/templated-local/1.0.0/chart/templates/cm.yaml": `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}-greeting
data:
  carried-by: the-chart-a-templated-source-names
`,
	"feature-recipe/helm/banner-local/1.0.0/chart/Chart.yaml": "apiVersion: v2\nname: banner\nversion: 0.1.0\n",
	"feature-recipe/helm/banner-local/1.0.0/chart/templates/banner.yaml": `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Values.name }}
data:
  text: {{ .Values.text | quote }}
  carried-by: the-chart-beside-the-feature-recipe
`,
}

// carriedFilesConsumer is a Release that reaches the publisher's entities by
// identity only; its workspace switches the local catalog off, so every entity
// resolves from the registry.
var carriedFilesConsumer = map[string]string{
	"aggregate.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    front:
      type: acme/compute/bannered:1.0.0
      values:
        greeting: ciao
      features:
        banner:
          type: acme/banner:1.0.0
          values:
            text: benvenuti
    templated:
      type: acme/compute/templated:1.0.0
`,
	"manifest.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        acme/compute/bannered:1.0.0: helm/bannered-local:1.0.0
        acme/compute/templated:1.0.0: helm/templated-local:1.0.0
    features:
      type:
        acme/banner:1.0.0: helm/banner-local:1.0.0
`,
	"release.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: dev
  environment: development
  version: "1.0.0"
spec:
  aggregate: aggregate
  manifest: manifest
  components:
    front: {}
    templated: {}
`,
	// A decoy at the path the templated source produces, relative to the
	// consumer's working directory. It must never be rendered: a pulled Recipe's
	// relative source resolves inside what was published.
	"chart/Chart.yaml":        "apiVersion: v2\nname: decoy\nversion: 0.1.0\n",
	"chart/templates/cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: decoy\ndata:\n  carried-by: the-consumer-working-directory-decoy\n",
}

// TestIntegration_RegistryEntityReadsTheFilesItsArchiveCarries publishes a
// Recipe and a FeatureRecipe that each carry a local chart, then builds a
// Release that pulls both from the registry and from nowhere else. Each renders
// from the chart its archive carried — the `carried-by` lines exist only in those
// charts — and after the pull each one's cache slot holds the laid-out tree. A
// third Recipe writes its source as a template, which is anchored at render
// rather than at load; it too renders its published chart, and the decoy planted
// at the same relative path in the consumer's working directory is never read.
func TestIntegration_RegistryEntityReadsTheFilesItsArchiveCarries(t *testing.T) {
	host := startRegistry(t, nil)

	pub := t.TempDir()
	for rel, body := range carriedFilesPublisher {
		writeFixture(t, filepath.Join(pub, filepath.FromSlash(rel)), body)
	}
	publisher := resolveTempWorkspace(t, tempWorkspaceOptions{Root: pub})
	for _, entity := range []string{
		"component/acme/compute/bannered/1.0.0/component.yaml",
		"recipe/helm/bannered-local/1.0.0/recipe.yaml",
		"feature/acme/banner/1.0.0/feature.yaml",
		"feature-recipe/helm/banner-local/1.0.0/recipe.yaml",
		"component/acme/compute/templated/1.0.0/component.yaml",
		"recipe/helm/templated-local/1.0.0/recipe.yaml",
	} {
		if err := pushRun(t.Context(), pushConfig{Workspace: publisher, EntityPath: filepath.Join(pub, entity), Registry: host}); err != nil {
			t.Fatalf("push %s: %v", entity, err)
		}
	}

	ws := t.TempDir()
	for rel, body := range carriedFilesConsumer {
		writeFixture(t, filepath.Join(ws, rel), body)
	}
	writeWorkspaceConfig(t, ws, "workspace:\n  catalog_dirs: []\ndefault_registry: \""+host+"\"\noffline: false")
	t.Chdir(ws)
	consumer := resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws})

	if err := pullRun(context.Background(), pullConfig{ws: consumer, ReleasePath: "release.yaml"}); err != nil {
		t.Fatalf("registry pull: %v", err)
	}
	for _, slot := range []struct{ uri, file string }{
		{"helm/bannered-local:1.0.0", "chart/templates/greeting.yaml"},
		{"helm/banner-local:1.0.0", "chart/templates/banner.yaml"},
	} {
		dir := oci.CachePath(ws, ".aggrega/cache", slot.uri, "1.0.0")
		pulled, err := os.ReadFile(filepath.Join(dir, "blob"))
		if err != nil {
			t.Fatalf("after the pull, %s's cache slot holds no artifact: %v", slot.uri, err)
		}
		laid := filepath.Join(sourcecache.ExtractedTree(dir, digest.ContentDigest(pulled)), filepath.FromSlash(slot.file))
		if _, err := os.Stat(laid); err != nil {
			t.Errorf("after the pull, %s's cache slot does not hold its laid-out chart: %v", slot.uri, err)
		}
	}

	if err := dispatchBuild(context.Background(), consumer, "release.yaml", nil, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("registry build: %v", err)
	}
	var rendered strings.Builder
	if err := filepath.WalkDir(filepath.Join(ws, "dist"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, rerr := os.ReadFile(path)
		rendered.Write(data)
		return rerr
	}); err != nil {
		t.Fatalf("read rendered output: %v", err)
	}
	for _, want := range []string{
		"carried-by: the-chart-beside-the-recipe",
		`greeting: "ciao"`,
		"carried-by: the-chart-beside-the-feature-recipe",
		`text: "benvenuti"`,
		"carried-by: the-chart-a-templated-source-names",
	} {
		if !strings.Contains(rendered.String(), want) {
			t.Errorf("the rendered output does not carry %q; the chart the archive carried was not rendered:\n%s", want, rendered.String())
		}
	}
	if strings.Contains(rendered.String(), "the-consumer-working-directory-decoy") {
		t.Errorf("a pulled Recipe's templated source resolved against the consumer's working directory and rendered its decoy chart:\n%s", rendered.String())
	}
}
