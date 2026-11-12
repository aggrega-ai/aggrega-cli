// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package yamlpos

import (
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// allEncodings is every encoding a LineIndex can be built with.
var allEncodings = []PositionEncoding{PositionEncodingUTF8, PositionEncodingUTF16, PositionEncodingUTF32}

// TestANodeColumnIsCountedInCharacters converts the key of a flow mapping that
// sits after a character of each UTF-8 width on its line, and pins where each
// encoding counts it. yaml.v3 records the key's column in characters, so the
// key is the 23rd character of the ASCII line and of the two-byte line, and the
// 20th of the three- and four-byte lines, whatever its offset in bytes; each
// encoding then counts the characters before it in its own units.
//
// The ASCII row is the control: every encoding counts it alike, and it is the
// row a conversion that moves more than the multi-byte lines moves.
//
// Each span is also read back as text by a unit counter that shares nothing with
// the index, so a column one unit early reads as a span starting on the space
// before the key rather than only as a wrong number.
func TestANodeColumnIsCountedInCharacters(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		content string
		start   map[PositionEncoding]int
	}{
		{
			name:    "an ASCII line",
			content: "spec: {first: \"Cafe\", second: 2}\n",
			start:   map[PositionEncoding]int{PositionEncodingUTF8: 22, PositionEncodingUTF16: 22, PositionEncodingUTF32: 22},
		},
		{
			name:    "a two-byte character before the key",
			content: "spec: {first: \"Café\", second: 2}\n",
			start:   map[PositionEncoding]int{PositionEncodingUTF8: 23, PositionEncodingUTF16: 22, PositionEncodingUTF32: 22},
		},
		{
			name:    "a three-byte character before the key",
			content: "spec: {first: \"サ\", second: 2}\n",
			start:   map[PositionEncoding]int{PositionEncodingUTF8: 21, PositionEncodingUTF16: 19, PositionEncodingUTF32: 19},
		},
		{
			name:    "a four-byte character before the key",
			content: "spec: {first: \"🚀\", second: 2}\n",
			start:   map[PositionEncoding]int{PositionEncodingUTF8: 22, PositionEncodingUTF16: 20, PositionEncodingUTF32: 19},
		},
	} {
		var doc yaml.Node
		if err := yaml.Unmarshal([]byte(tc.content), &doc); err != nil {
			t.Fatalf("%s: parse: %v", tc.name, err)
		}
		key := FieldNode(&doc, "spec.second")
		if key == nil || key.Value != "second" {
			t.Fatalf("%s: the field path did not reach the key: %+v", tc.name, key)
		}
		for _, enc := range allEncodings {
			idx := BuildLineIndexWithEncoding([]byte(tc.content), enc)
			want := tc.start[enc]

			if got := idx.NodeToPosition(key); got != (Position{Line: 0, Character: want}) {
				t.Errorf("%s, %s: NodeToPosition = %+v, want 0:%d", tc.name, enc, got, want)
			}
			// "second" is six ASCII characters, so the key ends six units on.
			if got := idx.ScalarEnd(key); got != (Position{Line: 0, Character: want + 6}) {
				t.Errorf("%s, %s: ScalarEnd = %+v, want 0:%d", tc.name, enc, got, want+6)
			}
			span, ok := FieldRange(&doc, idx, "spec.second")
			if !ok {
				t.Fatalf("%s, %s: FieldRange did not resolve", tc.name, enc)
			}
			if span != (FieldSpan{Line: 1, Column: want + 1, EndLine: 1, EndColumn: want + 7}) {
				t.Errorf("%s, %s: FieldRange = %+v, want 1:%d-1:%d", tc.name, enc, span, want+1, want+7)
			}
			if got := textBetweenUnits(t, tc.content, span.Column-1, span.EndColumn-1, enc); got != "second" {
				t.Errorf("%s, %s: the span covers %q, not the key", tc.name, enc, got)
			}
		}
	}
}

// TestACursorResolvesToTheNodeUnderItCountedInCharacters is the reverse of the
// test above: a cursor on a value that sits after multi-byte characters on its
// line resolves to that value, at its first character and at its last, in every
// encoding, and a cursor on the space before the value resolves to nothing. The
// characters before the value are eleven bytes wider than they are long, which
// is more than the value itself, so a cursor read as a byte column against the
// value's character column lands past the value's end.
//
// The ASCII row is the control, resolved alike before and after any change to
// how a column is read. Its space-before cursor is what makes a cursor read one
// character late visible there: the cursor matching counts the position just
// past a value's last character as inside it, so the last-character cursor alone
// would absorb that shift.
func TestACursorResolvesToTheNodeUnderItCountedInCharacters(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                string
		content             string
		before, first, last map[PositionEncoding]int
	}{
		{
			name:    "an ASCII line",
			content: "spec: {first: \"Cafe\", implements: core/x}\n",
			before:  map[PositionEncoding]int{PositionEncodingUTF8: 33, PositionEncodingUTF16: 33, PositionEncodingUTF32: 33},
			first:   map[PositionEncoding]int{PositionEncodingUTF8: 34, PositionEncodingUTF16: 34, PositionEncodingUTF32: 34},
			last:    map[PositionEncoding]int{PositionEncodingUTF8: 39, PositionEncodingUTF16: 39, PositionEncodingUTF32: 39},
		},
		{
			name:    "three- and four-byte characters before the value",
			content: "spec: {first: \"サーバー🚀\", implements: core/x}\n",
			before:  map[PositionEncoding]int{PositionEncodingUTF8: 45, PositionEncodingUTF16: 35, PositionEncodingUTF32: 34},
			first:   map[PositionEncoding]int{PositionEncodingUTF8: 46, PositionEncodingUTF16: 36, PositionEncodingUTF32: 35},
			last:    map[PositionEncoding]int{PositionEncodingUTF8: 51, PositionEncodingUTF16: 41, PositionEncodingUTF32: 40},
		},
	} {
		var doc yaml.Node
		if err := yaml.Unmarshal([]byte(tc.content), &doc); err != nil {
			t.Fatalf("%s: parse: %v", tc.name, err)
		}
		for _, enc := range allEncodings {
			idx := BuildLineIndexWithEncoding([]byte(tc.content), enc)
			for _, at := range []struct {
				which, under string
				char         int
			}{{"before", " ", tc.before[enc]}, {"first", "c", tc.first[enc]}, {"last", "x", tc.last[enc]}} {
				if got := textBetweenUnits(t, tc.content, at.char, at.char+1, enc); got != at.under {
					t.Fatalf("%s, %s: the fixture's %s cursor sits on %q, not %q", tc.name, enc, at.which, got, at.under)
				}
				ctx, err := Classify(&doc, idx, Position{Line: 0, Character: at.char})
				if err != nil {
					t.Fatalf("%s, %s: Classify: %v", tc.name, enc, err)
				}
				onTheValue := ctx.Kind == ContextKindRecipeImplements && ctx.Value == "core/x"
				if at.which == "before" && onTheValue {
					t.Errorf("%s, %s: a cursor on the space before the value resolves to the value", tc.name, enc)
				}
				if at.which != "before" && !onTheValue {
					t.Errorf("%s, %s: a cursor on the value's %s character resolves to kind %v, value %q; want the implements reference core/x",
						tc.name, enc, at.which, ctx.Kind, ctx.Value)
				}
			}
		}
	}
}

// textBetweenUnits returns the text of the document's first line between two
// 0-based offsets counted in enc's units, failing when either offset splits a
// character or lies past the line's end. It counts with the standard library
// alone and shares nothing with the index under test.
func textBetweenUnits(t *testing.T, content string, start, end int, enc PositionEncoding) string {
	t.Helper()
	line := content
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			line = content[:i]
			break
		}
	}
	at := func(units int) int {
		counted := 0
		for i, r := range line {
			if counted == units {
				return i
			}
			switch enc {
			case PositionEncodingUTF8:
				counted += utf8.RuneLen(r)
			case PositionEncodingUTF16:
				counted += utf16.RuneLen(r)
			default:
				counted++
			}
			if counted > units {
				t.Fatalf("offset %d splits a character of %q", units, line)
			}
		}
		if counted != units {
			t.Fatalf("offset %d lies past the end of %q", units, line)
		}
		return len(line)
	}
	from, to := at(start), at(end)
	if to < from {
		t.Fatalf("the offsets %d-%d run backwards", start, end)
	}
	return line[from:to]
}
