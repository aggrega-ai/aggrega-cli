// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compiler_test

import (
	"path"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// TestHelmChartRender_PlacesNamespacedResourcesInTheDeclaredNamespace builds one
// chart for two components through the real pipeline and the embedded helm
// provider: store in a boundary declaring the namespace "shop", edge in a boundary
// declaring none. The chart's templates write no namespace except on one
// ConfigMap, which pins kube-system.
//
// Under "shop", every namespaced resource writing none is emitted in it and named
// with the namespace segment — the Deployment, and the custom Widget whose kind the
// chart's own crds/ directory declares namespaced — while the pinned ConfigMap keeps
// kube-system, the cluster-scoped ClusterRole and CustomResourceDefinition stay
// namespace-less, and the custom Probe, whose kind nothing in the chart declares, is
// left without one and named in the build's one advisory about it. Under no
// declared namespace nothing is placed and nothing is advised.
func TestHelmChartRender_PlacesNamespacedResourcesInTheDeclaredNamespace(t *testing.T) {
	t.Parallel()

	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/helm-chart-namespace-placement/release.yaml",
		CatalogPaths: []string{"testdata/helm-chart-namespace-placement/catalog"},
	})

	byFile := map[string]emittedDocument{}
	for p, content := range result.Artifacts {
		docs := emittedDocuments(t, map[string][]byte{p: content})
		if len(docs) == 1 {
			byFile[path.Base(p)] = docs[0]
		}
	}
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)

	for _, want := range []struct {
		file, kind, namespace string
	}{
		{"store-deployment-shop-store-server.yaml", "Deployment", "shop"},
		{"store-widget-shop-store-widget.yaml", "Widget", "shop"},
		{"store-configmap-kube-system-store-lease.yaml", "ConfigMap", "kube-system"},
		{"store-clusterrole-store-reader.yaml", "ClusterRole", ""},
		{"store-customresourcedefinition-widgets-example-com.yaml", "CustomResourceDefinition", ""},
		{"store-probe-store-probe.yaml", "Probe", ""},
		{"edge-deployment-edge-server.yaml", "Deployment", ""},
		{"edge-widget-edge-widget.yaml", "Widget", ""},
		{"edge-configmap-kube-system-edge-lease.yaml", "ConfigMap", "kube-system"},
		{"edge-probe-edge-probe.yaml", "Probe", ""},
	} {
		d, ok := byFile[want.file]
		if !ok {
			t.Errorf("the build emitted no %s; it emitted %v", want.file, files)
			continue
		}
		if d.Kind != want.kind {
			t.Errorf("%s holds a %s, want a %s", want.file, d.Kind, want.kind)
		}
		switch got := d.Metadata.Namespace; {
		case want.namespace == "" && got != nil:
			t.Errorf("%s writes namespace %q, want none", want.file, *got)
		case want.namespace != "" && (got == nil || *got != want.namespace):
			t.Errorf("%s writes namespace %v, want %q", want.file, got, want.namespace)
		}
	}

	var placementAdvisories []string
	for _, d := range result.Advisories {
		if !strings.Contains(d.Message, "write no namespace") {
			continue
		}
		placementAdvisories = append(placementAdvisories, d.Message)
		if d.Location == nil || d.Location.Instance != "store" || d.Location.Kind != "helm" {
			t.Errorf("the advisory %q is located at %+v, want node store and provider helm", d.Message, d.Location)
		}
		for _, named := range []string{`kind "Probe" in API group "monitoring.example.org"`, `namespace "shop"`} {
			if !strings.Contains(d.Message, named) {
				t.Errorf("the advisory %q does not name %s", d.Message, named)
			}
		}
	}
	if len(placementAdvisories) != 1 {
		t.Errorf("the build raised %d advisories about resources left without a namespace, want one, for store's Probe: %v",
			len(placementAdvisories), placementAdvisories)
	}
}
