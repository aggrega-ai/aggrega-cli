// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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
		`apiVersion: aggrega.ai/v1alpha1
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
		`apiVersion: aggrega.ai/v1alpha1
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
		`apiVersion: aggrega.ai/v1alpha1
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
		`apiVersion: aggrega.ai/v1alpha1
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
		`apiVersion: aggrega.ai/v1alpha1
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
