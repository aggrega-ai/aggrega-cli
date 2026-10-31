// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package nodeorigin

import (
	"testing"

	"go.yaml.in/yaml/v3"
)

// parseMapping parses src and returns its top-level mapping node.
func parseMapping(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		t.Fatalf("fixture is not a mapping document: %q", src)
	}
	return doc.Content[0]
}

// TestDocument_AnswersForEveryNodeTheRecordCovers asserts each case the record has
// to answer, against the one fixture shape: a mapping read from an imported file,
// held the way a schema surface is held.
func TestDocument_AnswersForEveryNodeTheRecordCovers(t *testing.T) {
	const imported = "/catalog/component/x/contract.yaml"
	cases := []struct {
		name   string
		node   func(recorded *yaml.Node) *yaml.Node
		want   string
		wantOK bool
	}{
		{
			name:   "the recorded node itself",
			node:   func(r *yaml.Node) *yaml.Node { return r },
			want:   imported,
			wantOK: true,
		},
		{
			name:   "a body nested beneath it",
			node:   func(r *yaml.Node) *yaml.Node { return r.Content[1].Content[1] },
			want:   imported,
			wantOK: true,
		},
		{
			name: "a copy of it, as a struct holding the mapping by value carries it",
			node: func(r *yaml.Node) *yaml.Node {
				copied := *r
				return &copied
			},
			want:   imported,
			wantOK: true,
		},
		{
			name:   "an absent node",
			node:   func(*yaml.Node) *yaml.Node { return nil },
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorded := parseMapping(t, "values:\n  model: { type: string }\n")
			Record(recorded, imported)
			got, ok := Document(tc.node(recorded))
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("Document = (%q, %v); want (%q, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestDocument_NothingRecordedForContentNeverImported is the control for every
// case above: content parsed and never recorded answers nothing, and a position on
// it belongs to the document holding it.
func TestDocument_NothingRecordedForContentNeverImported(t *testing.T) {
	inline := parseMapping(t, "values:\n  model: { type: string }\n")
	if got, ok := Document(inline); ok {
		t.Errorf("inline content answered %q", got)
	}
	if got := DocumentOr(inline.Content[1], "/catalog/component/x/component.yaml"); got != "/catalog/component/x/component.yaml" {
		t.Errorf("DocumentOr = %q; want the holding document", got)
	}
}

// TestRecord_ReplacesAnEarlierRecord covers a chain of imports: content that was
// itself replaced by a further import is read from the last file the chain reached.
func TestRecord_ReplacesAnEarlierRecord(t *testing.T) {
	node := parseMapping(t, "$import: ./b.yaml\n")
	Record(node, "/catalog/a.yaml")
	*node = *parseMapping(t, "greeting: { type: string }\n")
	Record(node, "/catalog/b.yaml")
	if got := DocumentOr(node, "/catalog/entity.yaml"); got != "/catalog/b.yaml" {
		t.Errorf("DocumentOr = %q; want the last file the chain reached", got)
	}
}
