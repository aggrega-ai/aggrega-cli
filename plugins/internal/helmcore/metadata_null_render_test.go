// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore_test

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/helmcore"
)

// conditionalMetadataChart writes ConfigMaps whose labels, annotations or whole
// metadata hold only conditional entries the values do not take, so each key
// renders with nothing under it, beside one writing a name and nothing else.
var conditionalMetadataChart = map[string]string{
	"Chart.yaml":  "apiVersion: v2\nname: web\nversion: 0.1.0\n",
	"values.yaml": "extra:\n  labels: {}\n  annotations: {}\n  metadata: {}\n",
	"templates/null-labels.yaml": `apiVersion: v1
kind: ConfigMap
metadata:
  name: null-labels
  labels:
    {{- with .Values.extra.labels }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
data:
  k: v
`,
	"templates/null-labels-and-annotations.yaml": `apiVersion: v1
kind: ConfigMap
metadata:
  name: null-labels-and-annotations
  labels:
    {{- with .Values.extra.labels }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
  annotations:
    {{- with .Values.extra.annotations }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
data:
  k: v
`,
	"templates/null-metadata.yaml": `apiVersion: v1
kind: ConfigMap
metadata:
  {{- with .Values.extra.metadata }}
  {{- toYaml . | nindent 2 }}
  {{- end }}
data:
  k: v
`,
	"templates/name-only.yaml": `apiVersion: v1
kind: ConfigMap
metadata:
  name: name-only
data:
  k: v
`,
}

// emittedMetadataMaps reads each emitted file's document and returns, keyed by
// "<kind>/<name>", the node it writes at metadata.labels and at
// metadata.annotations (nil where it writes none).
func emittedMetadataMaps(t *testing.T, files map[string][]byte) map[string][2]*yaml.Node {
	t.Helper()
	out := map[string][2]*yaml.Node{}
	for path, content := range files {
		var doc yaml.Node
		if err := yaml.NewDecoder(bytes.NewReader(content)).Decode(&doc); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		var kind, name string
		var found [2]*yaml.Node
		root := doc.Content[0]
		for i := 0; i+1 < len(root.Content); i += 2 {
			switch root.Content[i].Value {
			case "kind":
				kind = root.Content[i+1].Value
			case "metadata":
				md := root.Content[i+1]
				for j := 0; j+1 < len(md.Content); j += 2 {
					switch md.Content[j].Value {
					case "name":
						name = md.Content[j+1].Value
					case "labels":
						found[0] = md.Content[j+1]
					case "annotations":
						found[1] = md.Content[j+1]
					}
				}
			}
		}
		out[kind+"/"+name] = found
	}
	return out
}

// asStringMap reads a mapping node as a map of its keys to their values.
func asStringMap(n *yaml.Node) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		m[n.Content[i].Value] = n.Content[i+1].Value
	}
	return m
}

// TestRenderChart_ANullLabelsOrMetadataKeyCarriesTheProvenance renders a chart
// whose labels, annotations or whole metadata key renders with nothing under it,
// through the render the provider guest calls, and reads the labels and the
// annotations each object is emitted with. Every object carries exactly the label
// set and the annotation set the shared provenance builders compose for the node,
// as the object writing only a name does — the one whose metadata rendered null
// included, whose file is named after its template since it has no name.
func TestRenderChart_ANullLabelsOrMetadataKeyCarriesTheProvenance(t *testing.T) {
	t.Parallel()

	nc := nodeCtx("api", "in-memory")
	nc.BoundaryPath = "app"
	nc.RecipeName = "web-chart"
	nc.RecipeVersion = "1.0.0"
	wantLabels := rendercore.BuildAggregaProvenanceLabels(nc)
	wantAnnotations := rendercore.BuildAggregaProvenanceAnnotations(nc, rendercore.ProvenanceRecipeURIFromNodeCtx(nc))
	for _, k := range []string{"aggrega.ai/component-id", "aggrega.ai/aggregate-name"} {
		if wantLabels[k] == "" {
			t.Fatalf("the node composes no %s label, so this test proves nothing: %v", k, wantLabels)
		}
	}
	for _, k := range []string{"aggrega.ai/recipe-uri", "aggrega.ai/boundary-path"} {
		if wantAnnotations[k] == "" {
			t.Fatalf("the node composes no %s annotation, so this test proves nothing: %v", k, wantAnnotations)
		}
	}

	ch, err := chartLoaderFor(conditionalMetadataChart)()
	if err != nil {
		t.Fatalf("load chart: %v", err)
	}
	rendered, err := helmcore.RenderChart(context.Background(), ch, map[string]any{}, nc,
		helmcore.NewHermeticValuesValidator(forbidFetch(t)))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	got := emittedMetadataMaps(t, rendered.Files)

	for _, resource := range []string{
		"ConfigMap/null-labels",
		"ConfigMap/null-labels-and-annotations",
		"ConfigMap/", // the object whose metadata rendered null carries no name
		"ConfigMap/name-only",
	} {
		maps, ok := got[resource]
		if !ok {
			t.Errorf("%s was not emitted; emitted %d files", resource, len(rendered.Files))
			continue
		}
		for i, part := range []struct {
			key  string
			want map[string]string
		}{{"labels", wantLabels}, {"annotations", wantAnnotations}} {
			n := maps[i]
			if n == nil || n.Kind != yaml.MappingNode {
				tag := "nothing"
				if n != nil {
					tag = n.ShortTag()
				}
				t.Errorf("%s is emitted with %s at metadata.%s, want a map holding %v", resource, tag, part.key, part.want)
				continue
			}
			if have := asStringMap(n); !reflect.DeepEqual(have, part.want) {
				t.Errorf("%s is emitted with %s %v, want %v", resource, part.key, have, part.want)
			}
		}
	}
	if _, ok := rendered.Files["api-web-templates-null-metadata.yaml"]; !ok {
		names := make([]string, 0, len(rendered.Files))
		for name := range rendered.Files {
			names = append(names, name)
		}
		t.Errorf("the object whose metadata rendered null is not emitted under its template's name; emitted %v", names)
	}
}
