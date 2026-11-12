// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package yamlpos

import (
	"testing"

	"go.yaml.in/yaml/v3"
)

func parseFieldDoc(t *testing.T, content string) *yaml.Node {
	t.Helper()
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(content), &node); err != nil {
		t.Fatalf("parse yaml: %v", err)
	}
	return &node
}

func TestFieldNode_TopLevelKey(t *testing.T) {
	t.Parallel()
	doc := parseFieldDoc(t,
		`apiVersion: aggrega.ai/v1beta1
kind: Aggregate
spec:
  components:
    web:
      type: hub.aggrega.ai/component/core/web-service
`)
	pos := FieldNode(doc, ".spec")
	if pos == nil {
		t.Fatalf("expected a node for .spec; got nil")
	}
	if pos.Line != 3 {
		t.Errorf("line = %d; want 3", pos.Line)
	}
	if pos.Column != 1 {
		t.Errorf("col = %d; want 1", pos.Column)
	}
}

func TestFieldNode_NestedParameterKey(t *testing.T) {
	t.Parallel()
	doc := parseFieldDoc(t,
		`apiVersion: aggrega.ai/v1beta1
kind: Aggregate
spec:
  parameters:
    port:
      type: integer
    host:
      type: string
`)
	pos := FieldNode(doc, ".spec.parameters.host")
	if pos == nil {
		t.Fatalf("expected a node for .spec.parameters.host; got nil")
	}
	if pos.Line != 7 {
		t.Errorf("line = %d; want 7 (host key line)", pos.Line)
	}
}

func TestFieldNode_ComponentExtendsValue(t *testing.T) {
	t.Parallel()
	doc := parseFieldDoc(t,
		`apiVersion: aggrega.ai/v1beta1
kind: Aggregate
spec:
  components:
    web:
      type: hub.aggrega.ai/component/core/web-service
      extends: hub.aggrega.ai/component/missing
`)
	pos := FieldNode(doc, ".spec.components.web.extends")
	if pos == nil {
		t.Fatalf("expected a node for extends; got nil")
	}
	if pos.Line != 7 {
		t.Errorf("line = %d; want 7 (extends key line)", pos.Line)
	}
}

// TestFieldNode_LeadingDotIsOptional pins that the two finding shapes
// feeding this walk resolve identically. Resolver findings spell an
// absolute path with a leading dot; schema findings spell the same path
// without one. A walk that accepted only the dotted form would silently
// position every schema finding at the document start.
func TestFieldNode_LeadingDotIsOptional(t *testing.T) {
	t.Parallel()
	doc := parseFieldDoc(t,
		`apiVersion: aggrega.ai/v1beta1
kind: Component
spec:
  schema:
    infra: {}
  retiredKey: gone
`)
	dotted := FieldNode(doc, ".spec.retiredKey")
	bare := FieldNode(doc, "spec.retiredKey")
	if dotted == nil || bare == nil {
		t.Fatalf("both spellings must resolve; dotted=%v bare=%v", dotted, bare)
	}
	if dotted.Line != bare.Line || dotted.Column != bare.Column {
		t.Errorf("the two spellings must resolve to one position; dotted=%d:%d bare=%d:%d",
			dotted.Line, dotted.Column, bare.Line, bare.Column)
	}
	if bare.Line != 6 {
		t.Errorf("line = %d; want 6 (retiredKey line)", bare.Line)
	}
}

func TestFieldNode_FirstSegmentMismatchReturnsNil(t *testing.T) {
	t.Parallel()
	doc := parseFieldDoc(t,
		`apiVersion: aggrega.ai/v1beta1
kind: Aggregate
spec:
  components: {}
`)
	// The first segment ("nonexistent") matches no key at the document
	// root, so there is no neighborhood to fall back to.
	pos := FieldNode(doc, ".nonexistent.foo")
	if pos != nil {
		t.Errorf("expected nil for first-segment mismatch; got %+v", pos)
	}
}

func TestFieldNode_PartialMatchReturnsLastFoundKey(t *testing.T) {
	t.Parallel()
	// When the walk reaches .spec.parameters but cannot find the inner
	// "foo" key, it falls back to the last found segment. This is the
	// shape a missing-required-property finding takes: the key it names
	// does not exist, so the enclosing key that does is the closest true
	// position.
	doc := parseFieldDoc(t,
		`spec:
  parameters:
    bar:
      type: string
`)
	pos := FieldNode(doc, ".spec.parameters.foo")
	if pos == nil {
		t.Fatalf("expected fallback to .spec.parameters; got nil")
	}
	if pos.Line != 2 {
		t.Errorf("line = %d; want 2 (parameters key line)", pos.Line)
	}
}

// fieldPathArrayDoc carries a value inside an array in each shape a validation
// finding names one: an element of a list of text, a key inside a mapping
// element (the shape a shared definition reached through a list takes, since
// the finding's path is the value's own path whatever definition judged it), a
// key inside a later element, a list inside a list, and a key spelled exactly
// like an indexed segment beside the list it would otherwise index.
const fieldPathArrayDoc = `workspace: {}
mcp:
  allowed_origins:
    - https://ok.example.com
    - https://app.example.com/
registries:
  - host: registry.example.com
    signing:
      rekor_url: rekor.example.com
  - host: second.example.com
    public_key: not-a-pem-block
plugins:
  cli:
    - uri: oci://hub.aggrega.ai/plugin/cli/moo:1.0.0
      digest: abc
matrix:
  - [a, b]
  - [c, d]
shadow:
  - from-the-sequence
"shadow[0]": from-the-key
`

// TestFieldNodeReachesAValueInsideAnArray pins that a path naming an array
// element by a bracketed index resolves to that element, or to a key inside it,
// rather than stopping at the key that holds the array — which would put a
// finding about one entry on the line of the whole list, or at the head of the
// file where the list sits at the top level.
//
// Each row writes out the coordinates and the node's text rather than comparing
// against a second walk, so the row says something the implementation agreeing
// with itself cannot satisfy. The fallback rows pin what a path the document
// does not fully carry resolves to: the deepest node reached, as for a key that
// is absent.
func TestFieldNodeReachesAValueInsideAnArray(t *testing.T) {
	t.Parallel()
	doc := parseFieldDoc(t, fieldPathArrayDoc)

	for _, tc := range []struct {
		name string
		path string
		// wantLine and wantColumn are the 1-based coordinates of the node the walk
		// settles on; wantText is its text (a key's name, an element's value), and
		// wantKind its node kind, so a coincidental line match cannot read as a pass.
		wantLine, wantColumn int
		wantText             string
		wantKind             yaml.Kind
	}{
		{name: "an element of a list of text", path: "mcp.allowed_origins[1]",
			wantLine: 5, wantColumn: 7, wantText: "https://app.example.com/", wantKind: yaml.ScalarNode},
		{name: "a key inside a mapping element, as a shared definition under a list is reached", path: "registries[0].signing.rekor_url",
			wantLine: 9, wantColumn: 7, wantText: "rekor_url", wantKind: yaml.ScalarNode},
		{name: "a key inside a later element", path: "registries[1].public_key",
			wantLine: 11, wantColumn: 5, wantText: "public_key", wantKind: yaml.ScalarNode},
		{name: "a key inside an element of a nested list", path: "plugins.cli[0].digest",
			wantLine: 15, wantColumn: 7, wantText: "digest", wantKind: yaml.ScalarNode},
		{name: "an element of a list inside a list", path: ".matrix[1][0]",
			wantLine: 18, wantColumn: 6, wantText: "c", wantKind: yaml.ScalarNode},
		{name: "a key spelled like the whole segment is matched before the list", path: "shadow[0]",
			wantLine: 21, wantColumn: 1, wantText: "shadow[0]", wantKind: yaml.ScalarNode},
		{name: "an index past the end stops at the key holding the list", path: "mcp.allowed_origins[7]",
			wantLine: 3, wantColumn: 3, wantText: "allowed_origins", wantKind: yaml.ScalarNode},
		{name: "an absent key inside an element stops at that element", path: "registries[1].signing.fulcio_url",
			wantLine: 10, wantColumn: 5, wantText: "", wantKind: yaml.MappingNode},
		{name: "an index on a value that is not a list stops at its key", path: "mcp[0].allowed_origins",
			wantLine: 2, wantColumn: 1, wantText: "mcp", wantKind: yaml.ScalarNode},
		{name: "a bracketed quoted key is not an index", path: `plugins.cli["0"].digest`,
			wantLine: 12, wantColumn: 1, wantText: "plugins", wantKind: yaml.ScalarNode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			node := FieldNode(doc, tc.path)
			if node == nil {
				t.Fatalf("FieldNode(%q) resolved to nothing; want %q at %d:%d", tc.path, tc.wantText, tc.wantLine, tc.wantColumn)
			}
			if node.Kind != tc.wantKind || node.Value != tc.wantText {
				t.Errorf("FieldNode(%q) reached a node of kind %v with text %q; want kind %v with text %q",
					tc.path, node.Kind, node.Value, tc.wantKind, tc.wantText)
			}
			if node.Line != tc.wantLine || node.Column != tc.wantColumn {
				t.Errorf("FieldNode(%q) = %d:%d; want %d:%d", tc.path, node.Line, node.Column, tc.wantLine, tc.wantColumn)
			}
		})
	}

	// A path whose first segment indexes a key the document does not carry has
	// nothing to fall back to, exactly as an absent first key does.
	if node := FieldNode(doc, "absent[0].key"); node != nil {
		t.Errorf("FieldNode(%q) = %d:%d %q; want nil", "absent[0].key", node.Line, node.Column, node.Value)
	}
}

// TestFieldRange_SpansAnArrayElement pins how an element is drawn: an element of
// text spans its value, as a key spans its name, and an element that is itself a
// mapping has no single line of text to span and is drawn at its start.
func TestFieldRange_SpansAnArrayElement(t *testing.T) {
	t.Parallel()
	doc := parseFieldDoc(t, fieldPathArrayDoc)
	idx := BuildLineIndexWithEncoding([]byte(fieldPathArrayDoc), PositionEncodingUTF8)

	for _, tc := range []struct {
		name string
		path string
		want FieldSpan
	}{
		// "https://app.example.com/" is 24 characters starting at column 7, so the
		// end sits one past its last character.
		{name: "an element of text spans its value", path: "mcp.allowed_origins[1]",
			want: FieldSpan{Line: 5, Column: 7, EndLine: 5, EndColumn: 31}},
		{name: "a key inside an element spans its name", path: "registries[1].public_key",
			want: FieldSpan{Line: 11, Column: 5, EndLine: 11, EndColumn: 15}},
		{name: "a mapping element is drawn at its start", path: "registries[1]",
			want: FieldSpan{Line: 10, Column: 5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			span, ok := FieldRange(doc, idx, tc.path)
			if !ok {
				t.Fatalf("FieldRange(%q) resolved to nothing; want %+v", tc.path, tc.want)
			}
			if span != tc.want {
				t.Errorf("FieldRange(%q) = %+v; want %+v", tc.path, span, tc.want)
			}
		})
	}
}

func TestFieldNode_EmptyFieldReturnsNil(t *testing.T) {
	t.Parallel()
	doc := parseFieldDoc(t, "spec:\n  foo: bar\n")
	if pos := FieldNode(doc, ""); pos != nil {
		t.Errorf("empty field should yield nil; got %+v", pos)
	}
	if pos := FieldNode(doc, "."); pos != nil {
		t.Errorf("dot-only field should yield nil; got %+v", pos)
	}
}

// TestFieldRange_SpansTheKeyToken pins that a resolved path carries both a
// start and an end, so a consumer can underline the key rather than mark a
// point.
func TestFieldRange_SpansTheKeyToken(t *testing.T) {
	t.Parallel()
	content := "spec:\n  retiredKey: gone\n"
	doc := parseFieldDoc(t, content)
	idx := BuildLineIndexWithEncoding([]byte(content), PositionEncodingUTF8)
	span, ok := FieldRange(doc, idx, "spec.retiredKey")
	if !ok {
		t.Fatal("spec.retiredKey must resolve")
	}
	if span.Line != 2 || span.Column != 3 {
		t.Errorf("start = %d:%d; want 2:3", span.Line, span.Column)
	}
	// "retiredKey" is 10 characters starting at column 3, so the end sits
	// one past its last character.
	if span.EndLine != 2 || span.EndColumn != 13 {
		t.Errorf("end = %d:%d; want 2:13", span.EndLine, span.EndColumn)
	}
}

// TestFieldRange_UnresolvedReportsNotOK pins that an unresolvable path is
// reported as such rather than as a position at the document start. The
// distinction is what lets a textual diagnostic omit the position entirely
// instead of naming coordinates a reader would have to distrust.
func TestFieldRange_UnresolvedReportsNotOK(t *testing.T) {
	t.Parallel()
	content := "spec:\n  foo: bar\n"
	doc := parseFieldDoc(t, content)
	idx := BuildLineIndexWithEncoding([]byte(content), PositionEncodingUTF8)
	span, ok := FieldRange(doc, idx, "nonexistent.key")
	if ok {
		t.Errorf("an unresolvable path must report ok=false; got span %+v", span)
	}
	if span != (FieldSpan{}) {
		t.Errorf("an unresolved span must be the zero value; got %+v", span)
	}
}

// TestFieldRange_ColumnFollowsTheIndexEncoding pins that the span is
// expressed in the coordinate space of the index it is given. A document
// with a multi-byte character before the key on the same line resolves to a
// different column under UTF-8 than under UTF-16, and each consumer needs
// its own. This is why the span is recomputed per surface rather than
// carried as one absolute number.
func TestFieldRange_ColumnFollowsTheIndexEncoding(t *testing.T) {
	t.Parallel()
	// The flow mapping puts a supplementary-plane rune (4 UTF-8 bytes, 2
	// UTF-16 code units) ahead of the "b" key on the same line.
	content := "spec: {\"\U0001F600\": 1, b: 2}\n"
	doc := parseFieldDoc(t, content)

	utf8Span, ok := FieldRange(doc, BuildLineIndexWithEncoding([]byte(content), PositionEncodingUTF8), "spec.b")
	if !ok {
		t.Fatal("spec.b must resolve under UTF-8")
	}
	utf16Span, ok := FieldRange(doc, BuildLineIndexWithEncoding([]byte(content), PositionEncodingUTF16), "spec.b")
	if !ok {
		t.Fatal("spec.b must resolve under UTF-16")
	}
	if utf8Span.Line != utf16Span.Line {
		t.Errorf("the line must not depend on the encoding; utf8=%d utf16=%d", utf8Span.Line, utf16Span.Line)
	}
	if utf8Span.Column == utf16Span.Column {
		t.Errorf("a supplementary-plane rune ahead of the key must move the column between encodings; both = %d", utf8Span.Column)
	}
}
