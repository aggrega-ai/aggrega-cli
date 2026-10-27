// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"bytes"
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"
)

// injectedMetadata decodes the single document the annotation merge wrote and
// returns the keys of its metadata map, in order, and the node written under
// metadata.annotations (nil when there is none).
func injectedMetadata(t *testing.T, out []byte) ([]string, *yaml.Node) {
	t.Helper()
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(out)).Decode(&doc); err != nil {
		t.Fatalf("decode the merged document: %v\n%s", err, out)
	}
	metadata := findMapChild(doc.Content[0], "metadata")
	if metadata == nil || metadata.Kind != yaml.MappingNode {
		t.Fatalf("the merged document has no metadata map:\n%s", out)
	}
	var keys []string
	for i := 0; i+1 < len(metadata.Content); i += 2 {
		keys = append(keys, metadata.Content[i].Value)
	}
	return keys, findMapChild(metadata, "annotations")
}

// mappingPairs flattens a mapping node into its keys and values, in order.
func mappingPairs(n *yaml.Node) []string {
	var out []string
	for _, c := range n.Content {
		out = append(out, c.Value)
	}
	return out
}

// TestInjectAggregaAnnotations_EveryShapeOfTheAnnotationsKey merges the
// provider's annotations into a Service whose chart wrote its annotations key in
// each shape a chart template produces. A key written with nothing under it — a
// chart whose every entry under it is conditional and none taken — and its two
// explicit null spellings receive the annotations in a block map, in the key's
// own place, exactly as an absent key does; an empty map, a map holding the
// chart's own entries, and a quoted "null" (which is text) keep the behaviour
// they had.
func TestInjectAggregaAnnotations_EveryShapeOfTheAnnotationsKey(t *testing.T) {
	t.Parallel()

	provider := map[string]string{
		"aggrega.ai/boundary-path": "app",
		"aggrega.ai/recipe-uri":    "probe:1.0.0",
	}
	stamped := []string{"aggrega.ai/boundary-path", "app", "aggrega.ai/recipe-uri", "probe:1.0.0"}

	for _, tc := range []struct {
		name        string
		annotations string // the metadata.annotations lines the chart wrote
		hookWeight  string
		wantKind    yaml.Kind
		wantStyle   yaml.Style
		wantPairs   []string // for a mapping: keys and values in order
		wantValue   string   // for a scalar: the text kept
	}{
		{name: "a key with nothing under it", annotations: "  annotations:\n",
			wantKind: yaml.MappingNode, wantPairs: stamped},
		{name: "a key with nothing under it, beside a sync wave", annotations: "  annotations:\n", hookWeight: "5",
			wantKind: yaml.MappingNode, wantPairs: stamped},
		{name: "the word null", annotations: "  annotations: null\n",
			wantKind: yaml.MappingNode, wantPairs: stamped},
		{name: "a tilde", annotations: "  annotations: ~\n",
			wantKind: yaml.MappingNode, wantPairs: stamped},
		{name: "an empty map", annotations: "  annotations: {}\n",
			wantKind: yaml.MappingNode, wantStyle: yaml.FlowStyle, wantPairs: stamped},
		{name: "the chart's own entries", annotations: "  annotations:\n    chart.example/key: keep\n",
			wantKind:  yaml.MappingNode,
			wantPairs: append([]string{"chart.example/key", "keep"}, stamped...)},
		{name: "a quoted null, which is text", annotations: "  annotations: \"null\"\n",
			wantKind: yaml.ScalarNode, wantValue: "null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := []byte("apiVersion: v1\nkind: Service\nmetadata:\n  name: svc\n" + tc.annotations +
				"spec:\n  type: ClusterIP\n")
			out, err := injectAggregaAnnotations(src, provider, tc.hookWeight, 2)
			if err != nil {
				t.Fatalf("merge: %v", err)
			}
			keys, ann := injectedMetadata(t, out)
			if want := []string{"name", "annotations"}; !reflect.DeepEqual(keys, want) {
				t.Errorf("metadata keys = %v, want %v (the annotations key keeps its place):\n%s", keys, want, out)
			}
			if ann == nil {
				t.Fatalf("no metadata.annotations in the merged document:\n%s", out)
			}
			if ann.Kind != tc.wantKind {
				t.Fatalf("metadata.annotations is a %s node (tag %s), want kind %d:\n%s", ann.ShortTag(), ann.Tag, tc.wantKind, out)
			}
			if tc.wantKind == yaml.ScalarNode {
				if ann.Value != tc.wantValue || ann.ShortTag() != "!!str" {
					t.Errorf("metadata.annotations = %s %q, want the text %q kept:\n%s", ann.ShortTag(), ann.Value, tc.wantValue, out)
				}
				return
			}
			if ann.Style != tc.wantStyle {
				t.Errorf("metadata.annotations style = %d, want %d:\n%s", ann.Style, tc.wantStyle, out)
			}
			if got := mappingPairs(ann); !reflect.DeepEqual(got, tc.wantPairs) {
				t.Errorf("metadata.annotations = %v, want %v:\n%s", got, tc.wantPairs, out)
			}
		})
	}
}

// TestIsNullScalar_TellsANullFromText pins which scalars the annotation merge
// treats as an absent map: the three null spellings, never text that reads
// "null", an empty string, or a node that is already a map.
func TestIsNullScalar_TellsANullFromText(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		src  string
		want bool
	}{
		{"k:\n", true},
		{"k: null\n", true},
		{"k: ~\n", true},
		{"k: Null\n", true},
		{"k: \"null\"\n", false},
		{"k: 'null'\n", false},
		{"k: \"\"\n", false},
		{"k: text\n", false},
		{"k: {}\n", false},
		{"k: []\n", false},
	} {
		var doc yaml.Node
		if err := yaml.Unmarshal([]byte(tc.src), &doc); err != nil {
			t.Fatalf("decode %q: %v", tc.src, err)
		}
		if got := isNullScalar(findMapChild(doc.Content[0], "k")); got != tc.want {
			t.Errorf("isNullScalar(%q) = %v, want %v", tc.src, got, tc.want)
		}
	}
	if isNullScalar(nil) {
		t.Errorf("isNullScalar(nil) = true, want false")
	}
}
