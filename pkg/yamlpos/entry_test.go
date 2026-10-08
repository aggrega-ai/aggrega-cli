// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package yamlpos

import (
	"testing"

	"go.yaml.in/yaml/v3"
)

// entryDoc carries every shape an entry's range turns on: a key whose value is a
// block mapping, one whose value sits on its own line in flow style, a block
// sequence indented under its key and one whose dashes sit at its key's column, a
// blank line inside a block, a comment ending one, a flow collection, and a
// document separator.
const entryDoc = `spec:
  components:
    web:
      uses:
        db:
          primary:
            target: store

            features:
              credential:
                type: x
          replica: {target: store}
        cache:
        - redis
        - memcached
        queue:
          - a
          - b
      # a comment at the components' own depth
    store: {uses: {db: {main: {target: x}}}}
  tags:
  - one
  - two
---
other: document
`

// TestEntryRangeSpansTheKeyThroughItsBlock pins the range of every shape, each
// expected range written out in full rather than derived, because a derived
// expectation would be a second implementation of the thing under test.
func TestEntryRangeSpansTheKeyThroughItsBlock(t *testing.T) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(entryDoc), &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	idx := BuildLineIndex([]byte(entryDoc))
	at := func(line, char int) Position { return Position{Line: line, Character: char} }

	for _, tc := range []struct {
		name string
		path []string
		want Range
		ok   bool
	}{
		{name: "a key whose block holds a blank line and nested entries",
			path: []string{"spec", "components", "web", "uses", "db", "primary"},
			want: Range{Start: at(5, 10), End: at(10, 23)}, ok: true},
		{name: "a key whose flow value sits on its own line",
			path: []string{"spec", "components", "web", "uses", "db", "replica"},
			want: Range{Start: at(11, 10), End: at(11, 34)}, ok: true},
		{name: "a key whose sequence dashes sit at its own column",
			path: []string{"spec", "components", "web", "uses", "cache"},
			want: Range{Start: at(12, 8), End: at(14, 19)}, ok: true},
		{name: "a key whose sequence is indented under it",
			path: []string{"spec", "components", "web", "uses", "queue"},
			want: Range{Start: at(15, 8), End: at(17, 13)}, ok: true},
		{name: "a sequence element, from its dash",
			path: []string{"spec", "components", "web", "uses", "cache", "1"},
			want: Range{Start: at(14, 8), End: at(14, 19)}, ok: true},
		{name: "a block a comment at a shallower depth ends",
			path: []string{"spec", "components", "web", "uses"},
			want: Range{Start: at(3, 6), End: at(17, 13)}, ok: true},
		{name: "a top-level sequence a document separator ends",
			path: []string{"spec", "tags"},
			want: Range{Start: at(20, 2), End: at(22, 7)}, ok: true},
		{name: "a path the document carries only part of", path: []string{"spec", "components", "web", "uses", "db", "absent"}},
		{name: "a path through a flow collection",
			path: []string{"spec", "components", "store", "uses", "db", "main"}},
		{name: "a sequence position past the end", path: []string{"spec", "tags", "2"}},
		{name: "a sequence position that is not a number", path: []string{"spec", "tags", "one"}},
		{name: "a path through a scalar", path: []string{"spec", "components", "web", "uses", "db", "primary", "target", "x"}},
		{name: "no path at all", path: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := EntryRange(&doc, idx, tc.path)
			if ok != tc.ok {
				t.Fatalf("resolved = %v, want %v (range %+v)", ok, tc.ok, got)
			}
			if ok && got != tc.want {
				t.Fatalf("range = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestRangeContainsBothEndsAndNothingOutside pins containment at each boundary:
// the first character and the last line's end are inside, and the character
// before the first and the line after the last are not.
func TestRangeContainsBothEndsAndNothingOutside(t *testing.T) {
	r := Range{Start: Position{Line: 5, Character: 10}, End: Position{Line: 10, Character: 23}}
	for _, tc := range []struct {
		name string
		p    Position
		want bool
	}{
		{"the first character", Position{Line: 5, Character: 10}, true},
		{"the character before it", Position{Line: 5, Character: 9}, false},
		{"a later character on the first line", Position{Line: 5, Character: 40}, true},
		{"the start of an inner line", Position{Line: 7, Character: 0}, true},
		{"the last line's end", Position{Line: 10, Character: 23}, true},
		{"past the last line's end", Position{Line: 10, Character: 24}, false},
		{"the line after the last", Position{Line: 11, Character: 0}, false},
		{"the line before the first", Position{Line: 4, Character: 30}, false},
	} {
		if got := r.Contains(tc.p); got != tc.want {
			t.Errorf("%s: Contains(%+v) = %v, want %v", tc.name, tc.p, got, tc.want)
		}
	}
}

// TestEntryRangeCountsInTheIndexsEncoding pins that a range is measured in the
// units of the index it was resolved against: a key following a character two
// UTF-16 units and four UTF-8 bytes wide starts at a different character offset
// in each encoding.
func TestEntryRangeCountsInTheIndexsEncoding(t *testing.T) {
	const content = "\"😀\": {k: {v: 1}}\nroot:\n  key: 1\n"
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, tc := range []struct {
		enc     PositionEncoding
		wantEnd int
	}{
		{PositionEncodingUTF8, 19},
		{PositionEncodingUTF16, 17},
		{PositionEncodingUTF32, 16},
	} {
		got, ok := EntryRange(&doc, BuildLineIndexWithEncoding([]byte(content), tc.enc), []string{"😀"})
		if !ok {
			t.Fatalf("%s: the key is not resolved", tc.enc)
		}
		if got.Start != (Position{Line: 0, Character: 0}) || got.End != (Position{Line: 0, Character: tc.wantEnd}) {
			t.Errorf("%s: range = %+v, want 0:0 to 0:%d", tc.enc, got, tc.wantEnd)
		}
	}
}
