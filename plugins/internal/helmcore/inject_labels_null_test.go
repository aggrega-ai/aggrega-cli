// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"bytes"
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"
)

// mergedRoot decodes the single document a merge wrote and returns its root map.
func mergedRoot(t *testing.T, out []byte) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(out)).Decode(&doc); err != nil {
		t.Fatalf("decode the merged document: %v\n%s", err, out)
	}
	return doc.Content[0]
}

// mapKeys lists a mapping node's keys, in order.
func mapKeys(n *yaml.Node) []string {
	var keys []string
	for i := 0; i+1 < len(n.Content); i += 2 {
		keys = append(keys, n.Content[i].Value)
	}
	return keys
}

// TestInjectAggregaLabels_EveryShapeOfTheLabelsKey merges the provider's labels
// into a Service whose chart wrote its labels key in each shape a chart template
// produces. A key written with nothing under it and its two explicit null
// spellings receive the labels in a block map, in the key's own place, exactly as
// an absent key does; an empty map, a map holding the chart's own entries, a
// quoted "null" (which is text) and a list keep the behaviour they had — the list
// is written into as it stands, so it gains the keys and values as entries.
func TestInjectAggregaLabels_EveryShapeOfTheLabelsKey(t *testing.T) {
	t.Parallel()

	provider := map[string]string{
		"aggrega.ai/component-id": "api",
		"app.kubernetes.io/name":  "api",
	}
	stamped := []string{"aggrega.ai/component-id", "api", "app.kubernetes.io/name", "api"}

	for _, tc := range []struct {
		name      string
		labels    string // the metadata.labels lines the chart wrote
		wantKind  yaml.Kind
		wantStyle yaml.Style
		wantPairs []string // for a mapping or a list: keys and values (or entries) in order
		wantValue string   // for a scalar: the text kept
	}{
		{name: "a key with nothing under it", labels: "  labels:\n",
			wantKind: yaml.MappingNode, wantPairs: stamped},
		{name: "the word null", labels: "  labels: null\n",
			wantKind: yaml.MappingNode, wantPairs: stamped},
		{name: "a tilde", labels: "  labels: ~\n",
			wantKind: yaml.MappingNode, wantPairs: stamped},
		{name: "an empty map", labels: "  labels: {}\n",
			wantKind: yaml.MappingNode, wantStyle: yaml.FlowStyle, wantPairs: stamped},
		{name: "the chart's own entries", labels: "  labels:\n    app: web\n",
			wantKind:  yaml.MappingNode,
			wantPairs: append([]string{"app", "web"}, stamped...)},
		{name: "a quoted null, which is text", labels: "  labels: \"null\"\n",
			wantKind: yaml.ScalarNode, wantValue: "null"},
		{name: "a list", labels: "  labels:\n    - x\n    - y\n",
			wantKind:  yaml.SequenceNode,
			wantPairs: append([]string{"x", "y"}, stamped...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := []byte("apiVersion: v1\nkind: Service\nmetadata:\n  name: svc\n" + tc.labels +
				"spec:\n  type: ClusterIP\n")
			out, err := injectAggregaLabels(src, provider, 2)
			if err != nil {
				t.Fatalf("merge: %v", err)
			}
			metadata := findMapChild(mergedRoot(t, out), "metadata")
			if metadata == nil || metadata.Kind != yaml.MappingNode {
				t.Fatalf("the merged document has no metadata map:\n%s", out)
			}
			if got, want := mapKeys(metadata), []string{"name", "labels"}; !reflect.DeepEqual(got, want) {
				t.Errorf("metadata keys = %v, want %v (the labels key keeps its place):\n%s", got, want, out)
			}
			lbl := findMapChild(metadata, "labels")
			if lbl == nil {
				t.Fatalf("no metadata.labels in the merged document:\n%s", out)
			}
			if lbl.Kind != tc.wantKind {
				t.Fatalf("metadata.labels is a %s node (tag %s), want kind %d:\n%s", lbl.ShortTag(), lbl.Tag, tc.wantKind, out)
			}
			if tc.wantKind == yaml.ScalarNode {
				if lbl.Value != tc.wantValue || lbl.ShortTag() != "!!str" {
					t.Errorf("metadata.labels = %s %q, want the text %q kept:\n%s", lbl.ShortTag(), lbl.Value, tc.wantValue, out)
				}
				return
			}
			if lbl.Style != tc.wantStyle {
				t.Errorf("metadata.labels style = %d, want %d:\n%s", lbl.Style, tc.wantStyle, out)
			}
			if got := mappingPairs(lbl); !reflect.DeepEqual(got, tc.wantPairs) {
				t.Errorf("metadata.labels = %v, want %v:\n%s", got, tc.wantPairs, out)
			}
		})
	}
}

// TestInjectAggregaProvenance_EveryShapeOfTheMetadataKey runs the label merge and
// then the annotation merge, as the render does, over a ConfigMap whose chart
// wrote its metadata key in each shape. A metadata written with nothing under it,
// or as `null` or `~`, ends as a block map holding the provider's labels and then
// its annotations, in the key's own place, exactly as an absent metadata ends at
// the document's end; a metadata holding only a name, or only a null labels and a
// null annotations key, receives both sets beside what the chart wrote; a quoted
// "null" (which is text) is left as written and receives neither.
func TestInjectAggregaProvenance_EveryShapeOfTheMetadataKey(t *testing.T) {
	t.Parallel()

	labels := map[string]string{"aggrega.ai/component-id": "api"}
	annotations := map[string]string{"aggrega.ai/recipe-uri": "probe:1.0.0"}
	wantLabels := []string{"aggrega.ai/component-id", "api"}
	wantAnnotations := []string{"aggrega.ai/recipe-uri", "probe:1.0.0"}

	for _, tc := range []struct {
		name         string
		metadata     string   // the metadata lines the chart wrote, empty for none
		wantRootKeys []string // the root's keys in order
		wantMetaKeys []string // the metadata map's keys in order; nil where metadata stays text
		wantText     string   // where metadata stays text: the text kept
	}{
		{name: "no metadata key", metadata: "",
			wantRootKeys: []string{"apiVersion", "kind", "data", "metadata"},
			wantMetaKeys: []string{"labels", "annotations"}},
		{name: "a key with nothing under it", metadata: "metadata:\n",
			wantRootKeys: []string{"apiVersion", "kind", "metadata", "data"},
			wantMetaKeys: []string{"labels", "annotations"}},
		{name: "the word null", metadata: "metadata: null\n",
			wantRootKeys: []string{"apiVersion", "kind", "metadata", "data"},
			wantMetaKeys: []string{"labels", "annotations"}},
		{name: "a tilde", metadata: "metadata: ~\n",
			wantRootKeys: []string{"apiVersion", "kind", "metadata", "data"},
			wantMetaKeys: []string{"labels", "annotations"}},
		{name: "only a name", metadata: "metadata:\n  name: cm\n",
			wantRootKeys: []string{"apiVersion", "kind", "metadata", "data"},
			wantMetaKeys: []string{"name", "labels", "annotations"}},
		{name: "a name, a null labels and a null annotations", metadata: "metadata:\n  name: cm\n  labels:\n  annotations:\n",
			wantRootKeys: []string{"apiVersion", "kind", "metadata", "data"},
			wantMetaKeys: []string{"name", "labels", "annotations"}},
		{name: "a quoted null, which is text", metadata: "metadata: \"null\"\n",
			wantRootKeys: []string{"apiVersion", "kind", "metadata", "data"},
			wantText:     "null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := []byte("apiVersion: v1\nkind: ConfigMap\n" + tc.metadata + "data:\n  k: v\n")
			labeled, err := injectAggregaLabels(src, labels, 2)
			if err != nil {
				t.Fatalf("label merge: %v", err)
			}
			out, err := injectAggregaAnnotations(labeled, annotations, "", 2)
			if err != nil {
				t.Fatalf("annotation merge: %v", err)
			}
			root := mergedRoot(t, out)
			if got := mapKeys(root); !reflect.DeepEqual(got, tc.wantRootKeys) {
				t.Errorf("root keys = %v, want %v:\n%s", got, tc.wantRootKeys, out)
			}
			metadata := findMapChild(root, "metadata")
			if metadata == nil {
				t.Fatalf("no metadata in the merged document:\n%s", out)
			}
			if tc.wantMetaKeys == nil {
				if metadata.Kind != yaml.ScalarNode || metadata.ShortTag() != "!!str" || metadata.Value != tc.wantText {
					t.Errorf("metadata = %s %q, want the text %q kept:\n%s", metadata.ShortTag(), metadata.Value, tc.wantText, out)
				}
				return
			}
			if metadata.Kind != yaml.MappingNode {
				t.Fatalf("metadata is a %s node (tag %s), want a map:\n%s", metadata.ShortTag(), metadata.Tag, out)
			}
			if got := mapKeys(metadata); !reflect.DeepEqual(got, tc.wantMetaKeys) {
				t.Errorf("metadata keys = %v, want %v:\n%s", got, tc.wantMetaKeys, out)
			}
			for _, part := range []struct {
				key  string
				want []string
			}{{"labels", wantLabels}, {"annotations", wantAnnotations}} {
				n := findMapChild(metadata, part.key)
				if n == nil || n.Kind != yaml.MappingNode {
					t.Errorf("metadata.%s is not a map:\n%s", part.key, out)
					continue
				}
				if n.Style != 0 {
					t.Errorf("metadata.%s style = %d, want a block map:\n%s", part.key, n.Style, out)
				}
				if got := mappingPairs(n); !reflect.DeepEqual(got, part.want) {
					t.Errorf("metadata.%s = %v, want %v:\n%s", part.key, got, part.want, out)
				}
			}
		})
	}
}

// TestFindOrCreateMapChild_EveryShapeOfTheValue pins the one rule both merges
// find their maps through: an absent key gets an empty map appended after the
// keys already there, a null value becomes an empty block map in place keeping
// the comment written on it, and any other value is returned as it stands.
func TestFindOrCreateMapChild_EveryShapeOfTheValue(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		src      string
		wantKind yaml.Kind
		wantKeys []string // the parent's keys in order after the call
		wantSame bool     // the returned node is the one the parent already held
	}{
		{"absent", "a: 1\n", yaml.MappingNode, []string{"a", "k"}, false},
		{"nothing under it", "k:\na: 1\n", yaml.MappingNode, []string{"k", "a"}, true},
		{"null", "k: null\na: 1\n", yaml.MappingNode, []string{"k", "a"}, true},
		{"tilde", "k: ~\na: 1\n", yaml.MappingNode, []string{"k", "a"}, true},
		{"a map", "k: {x: y}\na: 1\n", yaml.MappingNode, []string{"k", "a"}, true},
		{"text", "k: text\na: 1\n", yaml.ScalarNode, []string{"k", "a"}, true},
		{"a quoted null", "k: \"null\"\na: 1\n", yaml.ScalarNode, []string{"k", "a"}, true},
		{"a list", "k: [x]\na: 1\n", yaml.SequenceNode, []string{"k", "a"}, true},
	} {
		var doc yaml.Node
		if err := yaml.Unmarshal([]byte(tc.src), &doc); err != nil {
			t.Fatalf("%s: decode: %v", tc.name, err)
		}
		parent := doc.Content[0]
		before := findMapChild(parent, "k")
		wasNull := isNullScalar(before)
		got := findOrCreateMapChild(parent, "k")
		if got == nil || got.Kind != tc.wantKind {
			t.Errorf("%s: findOrCreateMapChild returned %v, want kind %d", tc.name, got, tc.wantKind)
			continue
		}
		if keys := mapKeys(parent); !reflect.DeepEqual(keys, tc.wantKeys) {
			t.Errorf("%s: parent keys = %v, want %v", tc.name, keys, tc.wantKeys)
		}
		if (got == before) != tc.wantSame {
			t.Errorf("%s: returned the node already held = %v, want %v", tc.name, got == before, tc.wantSame)
		}
		if got != before || wasNull {
			if got.Style != 0 || len(got.Content) != 0 || got.Tag != "!!map" {
				t.Errorf("%s: the map is style %d, tag %s, %d children; want an empty block map", tc.name, got.Style, got.Tag, len(got.Content))
			}
		}
	}

	// A comment written on a null value survives its turning into a map.
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("k: # kept\na: 1\n"), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	findOrCreateMapChild(doc.Content[0], "k")
	out, err := yaml.Marshal(&doc)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !bytes.Contains(out, []byte("# kept")) {
		t.Errorf("the comment on the null value was lost:\n%s", out)
	}
}
