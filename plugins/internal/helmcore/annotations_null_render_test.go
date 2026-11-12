// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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

// conditionalAnnotationsChart writes a ConfigMap whose annotations key holds only
// a conditional entry the values do not take, so the key renders with nothing
// under it, beside a ConfigMap writing no annotations key at all. Charts commonly
// write their Services this way.
var conditionalAnnotationsChart = map[string]string{
	"Chart.yaml":  "apiVersion: v2\nname: web\nversion: 0.1.0\n",
	"values.yaml": "service:\n  annotations: {}\n",
	"templates/conditional.yaml": `apiVersion: v1
kind: ConfigMap
metadata:
  name: conditional
  annotations:
    {{- with .Values.service.annotations }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
data:
  k: v
`,
	"templates/plain.yaml": `apiVersion: v1
kind: ConfigMap
metadata:
  name: plain
data:
  k: v
`,
}

// renderedAnnotations reads each emitted file's documents and keys the node each
// writes at metadata.annotations by "<kind>/<name>" (nil where it writes none).
func renderedAnnotations(t *testing.T, files map[string][]byte) map[string]*yaml.Node {
	t.Helper()
	out := map[string]*yaml.Node{}
	for path, content := range files {
		var doc yaml.Node
		if err := yaml.NewDecoder(bytes.NewReader(content)).Decode(&doc); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		var kind, name string
		var ann *yaml.Node
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
					case "annotations":
						ann = md.Content[j+1]
					}
				}
			}
		}
		out[kind+"/"+name] = ann
	}
	return out
}

// TestRenderChart_AnAnnotationsKeyRenderedEmptyCarriesTheProvenanceAnnotations
// renders a chart whose annotations key renders with nothing under it, through the
// render the provider guest calls, and reads the annotations each object is
// emitted with. The object receives the provider's provenance annotations exactly
// as its sibling writing no annotations key does: the set the shared provenance
// builder composes for the node, no more and no less.
func TestRenderChart_AnAnnotationsKeyRenderedEmptyCarriesTheProvenanceAnnotations(t *testing.T) {
	t.Parallel()

	nc := nodeCtx("api", "in-memory")
	nc.BoundaryPath = "app"
	nc.RecipeName = "web-chart"
	nc.RecipeVersion = "1.0.0"
	want := rendercore.BuildAggregaProvenanceAnnotations(nc, rendercore.ProvenanceRecipeURIFromNodeCtx(nc))
	for _, k := range []string{"aggrega.ai/recipe-uri", "aggrega.ai/boundary-path"} {
		if want[k] == "" {
			t.Fatalf("the node composes no %s annotation, so this test proves nothing: %v", k, want)
		}
	}

	ch, err := chartLoaderFor(conditionalAnnotationsChart)()
	if err != nil {
		t.Fatalf("load chart: %v", err)
	}
	rendered, err := helmcore.RenderChart(context.Background(), ch, map[string]any{}, nc,
		helmcore.NewHermeticValuesValidator(forbidFetch(t)))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	got := renderedAnnotations(t, rendered.Files)

	for _, resource := range []string{"ConfigMap/conditional", "ConfigMap/plain"} {
		ann, ok := got[resource]
		if !ok {
			t.Errorf("%s was not emitted; emitted %d files", resource, len(rendered.Files))
			continue
		}
		if ann == nil || ann.Kind != yaml.MappingNode {
			tag := "nothing"
			if ann != nil {
				tag = ann.ShortTag()
			}
			t.Errorf("%s is emitted with %s at metadata.annotations, want a map holding %v", resource, tag, want)
			continue
		}
		have := map[string]string{}
		for i := 0; i+1 < len(ann.Content); i += 2 {
			have[ann.Content[i].Value] = ann.Content[i+1].Value
		}
		if !reflect.DeepEqual(have, want) {
			t.Errorf("%s is emitted with annotations %v, want %v", resource, have, want)
		}
	}
}
