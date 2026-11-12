// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package yamlpos

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// applyInsertion composes the text an insertion calls for and splices it into
// the document at the reported position, returning the resulting bytes.
//
// It is the whole point of these tests: an insertion is only correct if a caller
// following it produces a document that still parses with the entry where the
// author would have written it, and a test asserting the numbers a position
// carries proves nothing about that. So every case below applies the answer and
// then reads the result back through the parser.
//
// entry carries the entry's own lines, with whatever relative indentation they
// have between themselves; the first is placed at EntryIndent and the rest keep
// their own leading spaces on top of it.
func applyInsertion(t *testing.T, src string, ins Insertion, idx LineIndex, entry []string) string {
	t.Helper()

	var b strings.Builder
	if ins.PrecededByLineBreak {
		b.WriteString("\n")
	}
	outermost := ins.EntryIndent - len(ins.MissingContainerKeys)*ins.IndentStep
	if outermost < 0 {
		t.Fatalf("the outermost missing key would be indented %d, which is not a column", outermost)
	}
	for i, key := range ins.MissingContainerKeys {
		b.WriteString(strings.Repeat(" ", outermost+i*ins.IndentStep))
		b.WriteString(key)
		b.WriteString(":\n")
	}
	for i, line := range entry {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(strings.Repeat(" ", ins.EntryIndent))
		b.WriteString(line)
	}

	byteInLine, err := idx.LSPCharToByte(ins.Position.Line, ins.Position.Character)
	if err != nil {
		t.Fatalf("convert the reported position to a byte offset: %v", err)
	}
	if ins.Position.Line >= len(idx.lineStarts) {
		t.Fatalf("the reported position names line %d in a document of %d lines",
			ins.Position.Line, len(idx.lineStarts))
	}
	at := idx.lineStarts[ins.Position.Line] + byteInLine
	if at > len(src) {
		t.Fatalf("the reported position resolves to byte %d in a document of %d bytes", at, len(src))
	}
	return src[:at] + b.String() + src[at:]
}

// scalarAt walks a parsed document down a structural path and returns the scalar
// it ends at. It is how each case checks that the entry landed where the author
// would have written it rather than merely somewhere the parser tolerates.
func scalarAt(doc *yaml.Node, path []string) (string, bool) {
	n := doc
	if n != nil && n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	for _, seg := range path {
		if n == nil {
			return "", false
		}
		switch n.Kind {
		case yaml.MappingNode:
			_, val := mappingEntry(n, seg)
			if val == nil {
				return "", false
			}
			n = val
		case yaml.SequenceNode:
			item := sequenceEntry(n, seg)
			if item == nil {
				return "", false
			}
			n = item
		default:
			return "", false
		}
	}
	if n == nil || n.Kind != yaml.ScalarNode {
		return "", false
	}
	return n.Value, true
}

// keysUnder returns the keys of the mapping at a path, in the order the document
// carries them, so a case can assert an appended entry landed after what was
// already there rather than merely inside it.
func keysUnder(doc *yaml.Node, path []string) []string {
	n := doc
	if n != nil && n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	for _, seg := range path {
		if n == nil || n.Kind != yaml.MappingNode {
			return nil
		}
		_, val := mappingEntry(n, seg)
		if val == nil {
			return nil
		}
		n = val
	}
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	var out []string
	for i := 0; i+1 < len(n.Content); i += 2 {
		out = append(out, n.Content[i].Value)
	}
	return out
}

// TestAnInsertionPointAppliedProducesTheDocumentTheAuthorWouldHaveWritten drives
// every case through the whole loop: locate, apply, re-parse, read the entry
// back at the path it was meant to land under.
//
// The two rows the naive answer gets wrong are the first two: a container that
// is absent and has to be created, and one that is there and has to be extended.
// A position computed for the second and used for the first produces a document
// that does not parse; a position computed for the first and used for the second
// duplicates the key.
func TestAnInsertionPointAppliedProducesTheDocumentTheAuthorWouldHaveWritten(t *testing.T) {
	for _, tc := range []struct {
		name      string
		src       string
		container []string
		entry     []string
		// readBack is the path the entry must be reachable at afterwards, and
		// want the scalar it must carry there.
		readBack []string
		want     string
		// wantMissing is the container keys the answer must report as absent,
		// and wantShape the shape it must report.
		wantMissing []string
		wantShape   EntryShape
		// wantKeyOrder, when set, is the key order the container must carry
		// after the edit.
		wantKeyOrder []string
		wantStep     int
		wantMeasured bool
	}{
		{
			name: "the container is absent and has to be created",
			src: "kind: Aggregate\n" +
				"metadata:\n" +
				"  name: web-app\n" +
				"spec:\n" +
				"  components:\n" +
				"    web:\n" +
				"      type: core/service\n",
			container:    []string{"spec", "components", "web", "features"},
			entry:        []string{"logging:", "  type: core/logging"},
			readBack:     []string{"spec", "components", "web", "features", "logging", "type"},
			want:         "core/logging",
			wantMissing:  []string{"features"},
			wantShape:    EntryShapeUnknown,
			wantStep:     2,
			wantMeasured: true,
		},
		{
			name: "the container is there and has to be extended",
			src: "kind: Aggregate\n" +
				"metadata:\n" +
				"  name: web-app\n" +
				"spec:\n" +
				"  components:\n" +
				"    web:\n" +
				"      type: core/service\n" +
				"      features:\n" +
				"        metrics:\n" +
				"          type: core/metrics\n",
			container:    []string{"spec", "components", "web", "features"},
			entry:        []string{"logging:", "  type: core/logging"},
			readBack:     []string{"spec", "components", "web", "features", "logging", "type"},
			want:         "core/logging",
			wantMissing:  nil,
			wantShape:    EntryShapeMapping,
			wantKeyOrder: []string{"metrics", "logging"},
			wantStep:     2,
			wantMeasured: true,
		},
		{
			name: "the container is declared and carries nothing",
			src: "kind: Aggregate\n" +
				"spec:\n" +
				"  components:\n" +
				"    web:\n" +
				"      type: core/service\n" +
				"      features:\n",
			container:    []string{"spec", "components", "web", "features"},
			entry:        []string{"logging:", "  type: core/logging"},
			readBack:     []string{"spec", "components", "web", "features", "logging", "type"},
			want:         "core/logging",
			wantMissing:  nil,
			wantShape:    EntryShapeUnknown,
			wantStep:     2,
			wantMeasured: true,
		},
		{
			name: "a block sequence whose dashes sit at its key's own column",
			src: "kind: Aggregate\n" +
				"spec:\n" +
				"  components:\n" +
				"    web:\n" +
				"      uses:\n" +
				"        db:\n" +
				"        - primary-db\n" +
				"        cache:\n" +
				"        - redis\n",
			container:    []string{"spec", "components", "web", "uses", "db"},
			entry:        []string{"- replica-db"},
			readBack:     []string{"spec", "components", "web", "uses", "db", "1"},
			want:         "replica-db",
			wantMissing:  nil,
			wantShape:    EntryShapeSequence,
			wantStep:     2,
			wantMeasured: true,
		},
		{
			name: "a blank line inside the block and a comment at the margin below it",
			src: "kind: Aggregate\n" +
				"spec:\n" +
				"  components:\n" +
				"    web:\n" +
				"      type: core/service\n" +
				"\n" +
				"# a note the author left at the margin\n",
			container:    []string{"spec", "components", "web", "features"},
			entry:        []string{"logging:", "  type: core/logging"},
			readBack:     []string{"spec", "components", "web", "features", "logging", "type"},
			want:         "core/logging",
			wantMissing:  []string{"features"},
			wantShape:    EntryShapeUnknown,
			wantStep:     2,
			wantMeasured: true,
		},
		{
			name: "a document indented four spaces at a time",
			src: "kind: Aggregate\n" +
				"spec:\n" +
				"    components:\n" +
				"        web:\n" +
				"            type: core/service\n",
			container:    []string{"spec", "components", "web", "features"},
			entry:        []string{"logging:", "    type: core/logging"},
			readBack:     []string{"spec", "components", "web", "features", "logging", "type"},
			want:         "core/logging",
			wantMissing:  []string{"features"},
			wantShape:    EntryShapeUnknown,
			wantStep:     4,
			wantMeasured: true,
		},
		{
			name: "every segment of the container path is absent",
			src: "kind: Aggregate\n" +
				"metadata:\n" +
				"  name: web-app\n",
			container:    []string{"spec", "components", "web", "features"},
			entry:        []string{"logging:", "  type: core/logging"},
			readBack:     []string{"spec", "components", "web", "features", "logging", "type"},
			want:         "core/logging",
			wantMissing:  []string{"spec", "components", "web", "features"},
			wantShape:    EntryShapeUnknown,
			wantStep:     2,
			wantMeasured: true,
		},
		{
			name:         "the document carries nothing at all",
			src:          "",
			container:    []string{"spec", "components"},
			entry:        []string{"web:", "  type: core/service"},
			readBack:     []string{"spec", "components", "web", "type"},
			want:         "core/service",
			wantMissing:  []string{"spec", "components"},
			wantShape:    EntryShapeUnknown,
			wantStep:     defaultIndentStep,
			wantMeasured: false,
		},
		{
			name:         "the document carries only a comment",
			src:          "# nothing authored here yet\n",
			container:    []string{"spec", "components"},
			entry:        []string{"web:", "  type: core/service"},
			readBack:     []string{"spec", "components", "web", "type"},
			want:         "core/service",
			wantMissing:  []string{"spec", "components"},
			wantShape:    EntryShapeUnknown,
			wantStep:     defaultIndentStep,
			wantMeasured: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var doc yaml.Node
			if err := yaml.Unmarshal([]byte(tc.src), &doc); err != nil {
				t.Fatalf("parse the fixture: %v", err)
			}
			idx := BuildLineIndex([]byte(tc.src))

			ins, refusal := InsertionPointIn(&doc, idx, tc.container)
			if refusal != InsertionAdmitted {
				t.Fatalf("refused with %q: %s", refusal, refusal.Reason())
			}
			if got := strings.Join(ins.MissingContainerKeys, "."); got != strings.Join(tc.wantMissing, ".") {
				t.Errorf("missing container keys = %v, want %v", ins.MissingContainerKeys, tc.wantMissing)
			}
			if ins.EntryShape != tc.wantShape {
				t.Errorf("entry shape = %q, want %q", ins.EntryShape, tc.wantShape)
			}
			if ins.IndentStep != tc.wantStep || ins.IndentStepMeasured != tc.wantMeasured {
				t.Errorf("indent step = %d (measured %v), want %d (measured %v)",
					ins.IndentStep, ins.IndentStepMeasured, tc.wantStep, tc.wantMeasured)
			}

			edited := applyInsertion(t, tc.src, ins, idx, tc.entry)

			var reloaded yaml.Node
			if err := yaml.Unmarshal([]byte(edited), &reloaded); err != nil {
				t.Fatalf("the edited document does not parse: %v\n---\n%s---", err, edited)
			}
			got, ok := scalarAt(&reloaded, tc.readBack)
			if !ok {
				t.Fatalf("the entry is not at %v after the edit\n---\n%s---",
					strings.Join(tc.readBack, "."), edited)
			}
			if got != tc.want {
				t.Fatalf("the entry at %v is %q, want %q\n---\n%s---",
					strings.Join(tc.readBack, "."), got, tc.want, edited)
			}
			if tc.wantKeyOrder != nil {
				order := keysUnder(&reloaded, tc.container)
				if strings.Join(order, ",") != strings.Join(tc.wantKeyOrder, ",") {
					t.Fatalf("the container carries %v after the edit, want %v\n---\n%s---",
						order, tc.wantKeyOrder, edited)
				}
			}
		})
	}
}

// TestAnInsertionIntoASequenceLeavesTheKeyThatFollowsItAlone is the sequence
// case's other half, and it is the one the block scan can get wrong without any
// row above failing: appending to the sequence must not disturb the sibling key
// sitting at the sequence's own column.
//
// It is asserted separately from the applied-edit table because what it checks
// is a property of the REST of the document rather than of the entry, so a row
// that only read the entry back would pass with the sibling swallowed.
func TestAnInsertionIntoASequenceLeavesTheKeyThatFollowsItAlone(t *testing.T) {
	src := "kind: Aggregate\n" +
		"spec:\n" +
		"  components:\n" +
		"    web:\n" +
		"      uses:\n" +
		"        db:\n" +
		"        - primary-db\n" +
		"        cache:\n" +
		"        - redis\n"

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("parse the fixture: %v", err)
	}
	idx := BuildLineIndex([]byte(src))

	ins, refusal := InsertionPointIn(&doc, idx, []string{"spec", "components", "web", "uses", "db"})
	if refusal != InsertionAdmitted {
		t.Fatalf("refused with %q: %s", refusal, refusal.Reason())
	}
	edited := applyInsertion(t, src, ins, idx, []string{"- replica-db"})

	var reloaded yaml.Node
	if err := yaml.Unmarshal([]byte(edited), &reloaded); err != nil {
		t.Fatalf("the edited document does not parse: %v\n---\n%s---", err, edited)
	}
	for _, probe := range []struct {
		path []string
		want string
	}{
		{[]string{"spec", "components", "web", "uses", "db", "0"}, "primary-db"},
		{[]string{"spec", "components", "web", "uses", "db", "1"}, "replica-db"},
		{[]string{"spec", "components", "web", "uses", "cache", "0"}, "redis"},
	} {
		got, ok := scalarAt(&reloaded, probe.path)
		if !ok || got != probe.want {
			t.Fatalf("%s = %q (found %v), want %q\n---\n%s---",
				strings.Join(probe.path, "."), got, ok, probe.want, edited)
		}
	}
}

// TestAnInsertionPointIsRefusedWhereNoEntryCanGo drives every refusal, with a
// well-formed case first as the positive control: without it, a refusal-only
// table cannot tell a working check apart from one that refuses everything.
func TestAnInsertionPointIsRefusedWhereNoEntryCanGo(t *testing.T) {
	const src = "kind: Aggregate\n" +
		"spec:\n" +
		"  components:\n" +
		"    web:\n" +
		"      type: core/service\n" +
		"      uses:\n" +
		"        db:\n" +
		"        - primary-db\n" +
		"    api: {}\n"

	for _, tc := range []struct {
		name      string
		container []string
		want      InsertionRefusal
	}{
		{
			name:      "a container path the document carries",
			container: []string{"spec", "components", "web"},
			want:      InsertionAdmitted,
		},
		{
			name:      "no container path at all",
			container: nil,
			want:      InsertionRefusedEmptyContainerPath,
		},
		{
			name:      "a container path carrying an empty segment",
			container: []string{"spec", "", "web"},
			want:      InsertionRefusedEmptyContainerPath,
		},
		{
			name:      "a container path running through a scalar",
			container: []string{"spec", "components", "web", "type", "features"},
			want:      InsertionRefusedNotAContainer,
		},
		{
			name:      "a container path ending at a scalar",
			container: []string{"spec", "components", "web", "type"},
			want:      InsertionRefusedNotAContainer,
		},
		{
			name:      "a container written in flow style",
			container: []string{"spec", "components", "api"},
			want:      InsertionRefusedFlowCollection,
		},
		{
			name:      "a container path running through a flow collection",
			container: []string{"spec", "components", "api", "features"},
			want:      InsertionRefusedFlowCollection,
		},
		{
			name:      "a sequence addressed past its last index",
			container: []string{"spec", "components", "web", "uses", "db", "5"},
			want:      InsertionRefusedSequenceIndex,
		},
		{
			name:      "a sequence addressed by a name",
			container: []string{"spec", "components", "web", "uses", "db", "primary-db"},
			want:      InsertionRefusedSequenceIndex,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var doc yaml.Node
			if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
				t.Fatalf("parse the fixture: %v", err)
			}
			_, refusal := InsertionPointIn(&doc, BuildLineIndex([]byte(src)), tc.container)
			if refusal != tc.want {
				t.Fatalf("refusal = %q, want %q", refusal, tc.want)
			}
			if tc.want == InsertionAdmitted {
				return
			}
			if reason := refusal.Reason(); reason == "" {
				t.Errorf("the refusal %q renders no reason a surface can report", refusal)
			}
		})
	}
}

// TestAnInsertionPointIsReportedInTheIndexsOwnEncoding pins that the character
// offset is measured in the units the caller negotiated, not in bytes with a
// label on it. The fixture's anchor line carries a character that is one UTF-16
// unit and two UTF-8 bytes, so a surface reporting one set of units under the
// other name is off by exactly that difference.
func TestAnInsertionPointIsReportedInTheIndexsOwnEncoding(t *testing.T) {
	src := "kind: Aggregate\n" +
		"spec:\n" +
		"  components:\n" +
		"    web:\n" +
		"      type: core/sérvice\n"
	container := []string{"spec", "components", "web", "features"}

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("parse the fixture: %v", err)
	}

	utf16Idx := BuildLineIndexWithEncoding([]byte(src), PositionEncodingUTF16)
	utf8Idx := BuildLineIndexWithEncoding([]byte(src), PositionEncodingUTF8)

	in16, refusal := InsertionPointIn(&doc, utf16Idx, container)
	if refusal != InsertionAdmitted {
		t.Fatalf("utf-16 refused with %q", refusal)
	}
	in8, refusal := InsertionPointIn(&doc, utf8Idx, container)
	if refusal != InsertionAdmitted {
		t.Fatalf("utf-8 refused with %q", refusal)
	}

	if in16.Position.Line != in8.Position.Line {
		t.Fatalf("the two encodings report different lines: %d and %d",
			in16.Position.Line, in8.Position.Line)
	}
	if in16.Position.Character == in8.Position.Character {
		t.Fatalf("the two encodings report the same character offset %d over a line carrying a "+
			"multi-byte character; one of them is not measuring in its own units",
			in16.Position.Character)
	}
	if in8.Position.Character-in16.Position.Character != 1 {
		t.Fatalf("utf-8 offset %d and utf-16 offset %d differ by %d, want 1 for one two-byte character",
			in8.Position.Character, in16.Position.Character, in8.Position.Character-in16.Position.Character)
	}

	// Each answer still applies correctly through the index it was computed
	// against, which is what makes the difference a unit and not a defect.
	for _, probe := range []struct {
		name string
		ins  Insertion
		idx  LineIndex
	}{
		{"utf-16", in16, utf16Idx},
		{"utf-8", in8, utf8Idx},
	} {
		t.Run(probe.name, func(t *testing.T) {
			edited := applyInsertion(t, src, probe.ins, probe.idx, []string{"logging:", "  type: core/logging"})
			var reloaded yaml.Node
			if err := yaml.Unmarshal([]byte(edited), &reloaded); err != nil {
				t.Fatalf("the edited document does not parse: %v\n---\n%s---", err, edited)
			}
			got, ok := scalarAt(&reloaded, append(append([]string{}, container...), "logging", "type"))
			if !ok || got != "core/logging" {
				t.Fatalf("the entry is not where it belongs after the edit\n---\n%s---", edited)
			}
		})
	}
}

// TestTheInsertionPointReadsStructureRatherThanMeaning is the invariant's own
// assertion, and it is written as an experiment rather than as a claim: two
// documents identical in structure and different in every name, type and label
// must yield the same answer.
//
// A helper that reached for what a key is called, what type a component carries,
// or what a label says would have to answer differently for one of the two.
//
// The two fixtures are written line for line at the same lengths, and that is
// load-bearing rather than tidy: a position is a fact about where text ends, so
// two documents whose lines are different lengths are ENTITLED to different
// answers, and a comparison over such a pair would fail on geometry while
// proving nothing about meaning.
func TestTheInsertionPointReadsStructureRatherThanMeaning(t *testing.T) {
	familiar := "kind: Aggregate\n" +
		"metadata:\n" +
		"  name: web-app\n" +
		"  labels:\n" +
		"    component.aggrega.ai/type: workload\n" +
		"spec:\n" +
		"  components:\n" +
		"    web:\n" +
		"      type: core/service\n"
	unfamiliar := "kind: Zzzzzzzzz\n" +
		"metadata:\n" +
		"  name: qqq-qqq\n" +
		"  labels:\n" +
		"    zzzzzzzzz.zzzzzzz.zz/zzzz: zzzzzzzz\n" +
		"spec:\n" +
		"  components:\n" +
		"    web:\n" +
		"      type: zzzz/zzzzzzz\n"

	// The pair is only a control while the lines line up, so the alignment is
	// asserted rather than assumed: a later edit to either fixture that broke it
	// would turn this test into one that fails for the wrong reason.
	famLines, unfamLines := strings.Split(familiar, "\n"), strings.Split(unfamiliar, "\n")
	if len(famLines) != len(unfamLines) {
		t.Fatalf("the fixtures have %d and %d lines; they must line up", len(famLines), len(unfamLines))
	}
	for i := range famLines {
		if len(famLines[i]) != len(unfamLines[i]) {
			t.Fatalf("fixture line %d is %d and %d characters long; they must line up to isolate "+
				"meaning from geometry", i, len(famLines[i]), len(unfamLines[i]))
		}
	}

	answer := func(src string) Insertion {
		t.Helper()
		var doc yaml.Node
		if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
			t.Fatalf("parse: %v", err)
		}
		ins, refusal := InsertionPointIn(&doc, BuildLineIndex([]byte(src)),
			[]string{"spec", "components", "web", "features"})
		if refusal != InsertionAdmitted {
			t.Fatalf("refused with %q", refusal)
		}
		return ins
	}

	a, b := answer(familiar), answer(unfamiliar)
	if a.Position != b.Position || a.EntryIndent != b.EntryIndent ||
		a.IndentStep != b.IndentStep || a.EntryShape != b.EntryShape ||
		a.PrecededByLineBreak != b.PrecededByLineBreak ||
		strings.Join(a.MissingContainerKeys, ".") != strings.Join(b.MissingContainerKeys, ".") {
		t.Fatalf("two structurally identical documents got different answers:\n  %+v\n  %+v", a, b)
	}
}

// TestTheIndentStepIsMeasuredFromTheDocument pins the measurement itself,
// including the case a naive tally gets wrong: a block sequence written with its
// dashes at its key's own column contributes a step of zero, and counting it
// would report that one nesting level costs nothing.
func TestTheIndentStepIsMeasuredFromTheDocument(t *testing.T) {
	for _, tc := range []struct {
		name         string
		src          string
		wantStep     int
		wantMeasured bool
	}{
		{
			name:         "two spaces throughout",
			src:          "spec:\n  components:\n    web:\n      type: x\n",
			wantStep:     2,
			wantMeasured: true,
		},
		{
			name:         "four spaces throughout",
			src:          "spec:\n    components:\n        web:\n            type: x\n",
			wantStep:     4,
			wantMeasured: true,
		},
		{
			name:         "a sequence at its key's own column contributes nothing",
			src:          "spec:\n  items:\n  - a\n  - b\n",
			wantStep:     2,
			wantMeasured: true,
		},
		{
			name:         "the most frequent step wins over a stray one",
			src:          "a:\n  b:\n    c: 1\n  d:\n    e: 2\nf:\n      g: 3\n",
			wantStep:     2,
			wantMeasured: true,
		},
		{
			name:         "a document with no nesting has no step to measure",
			src:          "kind: Aggregate\nname: web\n",
			wantStep:     defaultIndentStep,
			wantMeasured: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var doc yaml.Node
			if err := yaml.Unmarshal([]byte(tc.src), &doc); err != nil {
				t.Fatalf("parse the fixture: %v", err)
			}
			root := doc.Content[0]
			step, measured := measureIndentStep(root)
			if step != tc.wantStep || measured != tc.wantMeasured {
				t.Fatalf("step = %d (measured %v), want %d (measured %v)",
					step, measured, tc.wantStep, tc.wantMeasured)
			}
		})
	}
}

// TestABlockDoesNotReachIntoTheNextDocument pins the separator guard: a stream
// carrying a second document must not be where an entry for the first one lands.
//
// The parse only ever yields the first document, so nothing about the answer's
// shape would reveal the fault; what it would produce is an entry appended after
// the separator, inside a document nobody asked about.
func TestABlockDoesNotReachIntoTheNextDocument(t *testing.T) {
	src := "kind: Aggregate\n" +
		"metadata:\n" +
		"  name: first\n" +
		"---\n" +
		"kind: Aggregate\n" +
		"metadata:\n" +
		"  name: second\n"

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("parse the fixture: %v", err)
	}
	idx := BuildLineIndex([]byte(src))
	ins, refusal := InsertionPointIn(&doc, idx, []string{"spec"})
	if refusal != InsertionAdmitted {
		t.Fatalf("refused with %q", refusal)
	}
	// The first document's last line is `  name: first`, at index 2.
	if ins.Position.Line != 2 {
		t.Fatalf("the insertion point is on line %d; the first document ends on line 2, and the "+
			"separator is on line 3", ins.Position.Line)
	}
}
