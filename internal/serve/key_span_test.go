// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// TestAKeyNodeIsDrawnAsTheFieldPathWalkDrawsIt holds the two ways the daemon
// draws a key to one span. A placeholder is drawn from the key node the
// placeholder reading found, and every other finding from the field path the
// envelope names; wherever a field path reaches a key, the two must draw that
// key identically, in every position encoding the daemon negotiates.
//
// Every key in the document whose own name holds no dot is compared: plain
// keys, a quoted key, keys in a flow mapping after characters that are two and
// four bytes long, and a key on the line after a multi-line value.
func TestAKeyNodeIsDrawnAsTheFieldPathWalkDrawsIt(t *testing.T) {
	t.Parallel()
	const doc = "apiVersion: aggrega.ai/v1beta1\n" +
		"kind: Component\n" +
		"metadata:\n" +
		"  name: my-service\n" +
		"  description: \"Café API\"\n" +
		"  \"quoted\": \"x\"\n" +
		"  labels: {team: \"café\", tier: \"\U0001D4B3\", owner: x}\n" +
		"spec:\n" +
		"  note: |\n" +
		"    two\n" +
		"    lines\n" +
		"  after: y\n"
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &root); err != nil {
		t.Fatalf("parse the document: %v", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		t.Fatalf("the document parsed to no content")
	}
	compared := 0
	for _, encoding := range []yamlpos.PositionEncoding{
		yamlpos.PositionEncodingUTF8, yamlpos.PositionEncodingUTF16, yamlpos.PositionEncodingUTF32,
	} {
		idx := yamlpos.BuildLineIndexWithEncoding([]byte(doc), encoding)
		eachAddressableKey(root.Content[0], "", func(path string, key *yaml.Node) {
			if yamlpos.FieldNode(&root, path) != key {
				t.Fatalf("%s does not resolve to its own key, so it cannot hold the comparison", path)
			}
			want, ok := yamlpos.FieldRange(&root, idx, path)
			if !ok {
				t.Fatalf("%s (%s): the field path resolved to nothing", path, encoding)
			}
			line, col, endLine, endCol := keySpan(idx, key)
			got := yamlpos.FieldSpan{Line: line, Column: col, EndLine: endLine, EndColumn: endCol}
			if got != want {
				t.Errorf("%s (%s): the key node is drawn at %+v and the field path at %+v", path, encoding, got, want)
			}
			compared++
		})
	}
	if compared == 0 {
		t.Fatal("no key was compared, so the comparison proved nothing")
	}
}

// eachAddressableKey calls visit for every mapping key under n whose own name
// holds no dot, with the dotted path a field-path walk resolves it by.
func eachAddressableKey(n *yaml.Node, prefix string, visit func(path string, key *yaml.Node)) {
	if n == nil || n.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, value := n.Content[i], n.Content[i+1]
		if strings.Contains(key.Value, ".") {
			continue
		}
		path := key.Value
		if prefix != "" {
			path = prefix + "." + key.Value
		}
		visit(path, key)
		eachAddressableKey(value, path, visit)
	}
}
