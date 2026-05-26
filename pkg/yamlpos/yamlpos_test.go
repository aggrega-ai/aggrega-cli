// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package yamlpos

import (
	"errors"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLineIndexLSPCharToByteASCII(t *testing.T) {
	t.Parallel()
	idx := BuildLineIndex([]byte("hello\nworld\n"))
	cases := []struct {
		line, char int
		wantByte   int
	}{
		{0, 0, 0},
		{0, 5, 5},
		{0, 1, 1},
		{1, 3, 3},
	}
	for _, c := range cases {
		got, err := idx.LSPCharToByte(c.line, c.char)
		if err != nil {
			t.Errorf("LSPCharToByte(%d, %d): unexpected err %v", c.line, c.char, err)
			continue
		}
		if got != c.wantByte {
			t.Errorf("LSPCharToByte(%d, %d) = %d; want %d", c.line, c.char, got, c.wantByte)
		}
	}
}

func TestLineIndexLSPCharToByteUTF8BMP(t *testing.T) {
	t.Parallel()
	// "name: サーバー" — each Japanese rune is 3 bytes UTF-8 / 1 UTF-16 unit.
	idx := BuildLineIndex([]byte("name: サーバー\n"))
	// Cursor at the start of サ: LSP char = 6, byte = 6.
	got, err := idx.LSPCharToByte(0, 6)
	if err != nil {
		t.Fatalf("LSPCharToByte: %v", err)
	}
	if got != 6 {
		t.Errorf("LSPCharToByte(0, 6) = %d; want 6 (after 'name: ')", got)
	}
	// Cursor after サ: LSP char = 7 (one UTF-16 unit past), byte = 9 (3 bytes per rune).
	got, err = idx.LSPCharToByte(0, 7)
	if err != nil {
		t.Fatalf("LSPCharToByte: %v", err)
	}
	if got != 9 {
		t.Errorf("LSPCharToByte(0, 7) = %d; want 9 (after first multibyte rune)", got)
	}
	// Cursor at the end of サーバー (4 runes, 12 bytes): LSP char = 10, byte = 18.
	got, err = idx.LSPCharToByte(0, 10)
	if err != nil {
		t.Fatalf("LSPCharToByte: %v", err)
	}
	if got != 18 {
		t.Errorf("LSPCharToByte(0, 10) = %d; want 18", got)
	}
}

func TestLineIndexLSPCharToByteSupplementary(t *testing.T) {
	t.Parallel()
	// "emoji: 🎉" — 🎉 is U+1F389 (supplementary plane), 4 bytes UTF-8,
	// 2 UTF-16 code units (surrogate pair).
	idx := BuildLineIndex([]byte("emoji: 🎉\n"))
	// Cursor immediately before 🎉: LSP char = 7, byte = 7.
	got, err := idx.LSPCharToByte(0, 7)
	if err != nil {
		t.Fatalf("LSPCharToByte: %v", err)
	}
	if got != 7 {
		t.Errorf("LSPCharToByte(0, 7) = %d; want 7", got)
	}
	// Cursor immediately after 🎉: LSP char = 9 (7 + 2 UTF-16 units),
	// byte = 11 (7 + 4 UTF-8 bytes).
	got, err = idx.LSPCharToByte(0, 9)
	if err != nil {
		t.Fatalf("LSPCharToByte: %v", err)
	}
	if got != 11 {
		t.Errorf("LSPCharToByte(0, 9) = %d; want 11 (4 bytes per supplementary rune)", got)
	}
}

func TestLineIndexLSPCharToByteCRLF(t *testing.T) {
	t.Parallel()
	idx := BuildLineIndex([]byte("ab\r\ncd\r\n"))
	if idx.LineCount() != 3 {
		t.Fatalf("LineCount = %d; want 3 (two lines + trailing empty)", idx.LineCount())
	}
	got, err := idx.LSPCharToByte(1, 1)
	if err != nil {
		t.Fatalf("LSPCharToByte: %v", err)
	}
	if got != 1 {
		t.Errorf("LSPCharToByte(1, 1) = %d; want 1 (CRLF terminator excluded from line bytes)", got)
	}
}

func TestLineIndexByteToLSPCharRoundtrip(t *testing.T) {
	t.Parallel()
	content := []byte("name: サーバー 🎉 end\n")
	idx := BuildLineIndex(content)
	// Roundtrip holds at rune boundaries. Char 12 splits the 🎉
	// surrogate pair (high surrogate); LSPCharToByte snaps past the
	// full rune, so the roundtrip is not identity for that position.
	// The boundary positions for this content: 0..11 then 13..16.
	for _, char := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 14, 15, 16} {
		byteOff, err := idx.LSPCharToByte(0, char)
		if err != nil {
			t.Fatalf("LSPCharToByte(0, %d): %v", char, err)
		}
		back := idx.ByteToLSPChar(0, byteOff)
		if back != char {
			t.Errorf("roundtrip LSP→byte→LSP at char %d: got %d (via byte %d)", char, back, byteOff)
		}
	}
}

func TestLineIndexLSPCharToByteSurrogateSplitSnapsForward(t *testing.T) {
	t.Parallel()
	content := []byte("emoji: 🎉 end\n")
	idx := BuildLineIndex(content)
	// char = 8 splits the surrogate pair (🎉 starts at char 7 and
	// occupies 2 UTF-16 units). Per the snap-forward convention,
	// LSPCharToByte returns the byte position after the full rune.
	got, err := idx.LSPCharToByte(0, 8)
	if err != nil {
		t.Fatalf("LSPCharToByte: %v", err)
	}
	want := 11
	if got != want {
		t.Errorf("LSPCharToByte(0, 8) (surrogate split) = %d; want %d (snap past full rune)", got, want)
	}
}

func TestLineIndexScalarEndASCII(t *testing.T) {
	t.Parallel()
	content := []byte("name: hello\n")
	idx := BuildLineIndex(content)
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	// Find the "hello" value node.
	mapping := doc.Content[0]
	valueNode := mapping.Content[1]
	end := idx.ScalarEnd(valueNode)
	want := Position{Line: 0, Character: 11}
	if end != want {
		t.Errorf("ScalarEnd ASCII = %+v; want %+v", end, want)
	}
}

func TestLineIndexScalarEndDoubleQuoted(t *testing.T) {
	t.Parallel()
	content := []byte("name: \"hello\"\n")
	idx := BuildLineIndex(content)
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	valueNode := doc.Content[0].Content[1]
	if valueNode.Style != yaml.DoubleQuotedStyle {
		t.Fatalf("fixture precondition: expected DoubleQuotedStyle; got %v", valueNode.Style)
	}
	end := idx.ScalarEnd(valueNode)
	// Value text "hello" is 5 UTF-16 units; +2 for the surrounding
	// quote pair. Start column 6 (after "name: "); end column 13.
	want := Position{Line: 0, Character: 13}
	if end != want {
		t.Errorf("ScalarEnd double-quoted = %+v; want %+v (value + 2 quote bytes)", end, want)
	}
}

func TestLineIndexScalarEndSingleQuoted(t *testing.T) {
	t.Parallel()
	content := []byte("name: 'hello'\n")
	idx := BuildLineIndex(content)
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	valueNode := doc.Content[0].Content[1]
	if valueNode.Style != yaml.SingleQuotedStyle {
		t.Fatalf("fixture precondition: expected SingleQuotedStyle; got %v", valueNode.Style)
	}
	end := idx.ScalarEnd(valueNode)
	want := Position{Line: 0, Character: 13}
	if end != want {
		t.Errorf("ScalarEnd single-quoted = %+v; want %+v (value + 2 quote bytes)", end, want)
	}
}

func TestLineIndexScalarEndMultibyte(t *testing.T) {
	t.Parallel()
	content := []byte("name: サーバー\n")
	idx := BuildLineIndex(content)
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	valueNode := doc.Content[0].Content[1]
	end := idx.ScalarEnd(valueNode)
	want := Position{Line: 0, Character: 10}
	if end != want {
		t.Errorf("ScalarEnd multibyte = %+v; want %+v (6-char ASCII prefix + 4 BMP runes)", end, want)
	}
}

func TestLookupReturnsScalarNodeAtCursor(t *testing.T) {
	t.Parallel()
	content := []byte("metadata:\n  name: my-component\n")
	idx := BuildLineIndex(content)
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	// Cursor at line 1, char 8 — middle of "my-component" (which starts
	// at LSP char 8 of line 1).
	node, err := Lookup(&doc, idx, Position{Line: 1, Character: 10})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if node == nil {
		t.Fatal("Lookup returned nil node")
	}
	if node.Value != "my-component" {
		t.Errorf("Lookup returned node with Value = %q; want %q", node.Value, "my-component")
	}
}

func TestLookupReturnsErrorOnOutOfRangePosition(t *testing.T) {
	t.Parallel()
	content := []byte("name: hello\n")
	idx := BuildLineIndex(content)
	var doc yaml.Node
	_ = yaml.Unmarshal(content, &doc)
	_, err := Lookup(&doc, idx, Position{Line: 999, Character: 0})
	if !errors.Is(err, ErrPositionOutOfRange) {
		t.Errorf("Lookup with out-of-range line: got err %v; want ErrPositionOutOfRange", err)
	}
}

func classifyAtCursor(t *testing.T, content string, line, char int) Context {
	t.Helper()
	idx := BuildLineIndex([]byte(content))
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	ctx, err := Classify(&doc, idx, Position{Line: line, Character: char})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	return ctx
}

func TestClassifyDetectsMetadataName(t *testing.T) {
	t.Parallel()
	ctx := classifyAtCursor(t, "metadata:\n  name: my-thing\n", 1, 10)
	if ctx.Kind != ContextKindMetadataName {
		t.Errorf("Kind = %d; want ContextKindMetadataName", ctx.Kind)
	}
	if !reflect.DeepEqual(ctx.Path, []string{"metadata", "name"}) {
		t.Errorf("Path = %v; want [metadata name]", ctx.Path)
	}
	if ctx.Value != "my-thing" {
		t.Errorf("Value = %q; want %q", ctx.Value, "my-thing")
	}
}

func TestClassifyDetectsExtendsValue(t *testing.T) {
	t.Parallel()
	ctx := classifyAtCursor(t, "spec:\n  extends: parent-name\n", 1, 13)
	if ctx.Kind != ContextKindExtendsValue {
		t.Errorf("Kind = %d; want ContextKindExtendsValue", ctx.Kind)
	}
	if ctx.Value != "parent-name" {
		t.Errorf("Value = %q; want %q", ctx.Value, "parent-name")
	}
}

func TestClassifyDetectsComponentsTypeTopLevel(t *testing.T) {
	t.Parallel()
	src := "spec:\n  components:\n    api:\n      type: core/api\n"
	ctx := classifyAtCursor(t, src, 3, 14)
	if ctx.Kind != ContextKindComponentsType {
		t.Errorf("Kind = %d; want ContextKindComponentsType", ctx.Kind)
	}
	if ctx.Value != "core/api" {
		t.Errorf("Value = %q; want %q", ctx.Value, "core/api")
	}
}

func TestClassifyDetectsComponentsTypeWithSubsystemNesting(t *testing.T) {
	t.Parallel()
	src := "spec:\n  subsystems:\n    data:\n      components:\n        db:\n          type: core/postgres\n"
	ctx := classifyAtCursor(t, src, 5, 18)
	if ctx.Kind != ContextKindComponentsType {
		t.Errorf("Kind = %d; want ContextKindComponentsType", ctx.Kind)
	}
	if ctx.Value != "core/postgres" {
		t.Errorf("Value = %q; want %q", ctx.Value, "core/postgres")
	}
}

func TestClassifyDetectsComponentsTypeWithRecursiveSubsystemNesting(t *testing.T) {
	t.Parallel()
	src := "spec:\n  subsystems:\n    data:\n      subsystems:\n        cache:\n          components:\n            redis:\n              type: core/redis\n"
	ctx := classifyAtCursor(t, src, 7, 22)
	if ctx.Kind != ContextKindComponentsType {
		t.Errorf("Kind = %d; want ContextKindComponentsType at recursive subsystem depth", ctx.Kind)
	}
	if ctx.Value != "core/redis" {
		t.Errorf("Value = %q; want %q", ctx.Value, "core/redis")
	}
}

func TestClassifyDetectsFeaturesTypeComponentNested(t *testing.T) {
	t.Parallel()
	src := "spec:\n  components:\n    api:\n      type: core/api\n      features:\n        scaling:\n          type: feature/autoscaler\n"
	ctx := classifyAtCursor(t, src, 6, 22)
	if ctx.Kind != ContextKindFeaturesType {
		t.Errorf("Kind = %d; want ContextKindFeaturesType", ctx.Kind)
	}
	if ctx.Value != "feature/autoscaler" {
		t.Errorf("Value = %q; want %q", ctx.Value, "feature/autoscaler")
	}
}

func TestClassifyDetectsFeaturesTypeSubsystemDirectAttachment(t *testing.T) {
	t.Parallel()
	src := "spec:\n  subsystems:\n    data:\n      features:\n        replication:\n          type: feature/replication\n"
	ctx := classifyAtCursor(t, src, 5, 18)
	if ctx.Kind != ContextKindFeaturesType {
		t.Errorf("Kind = %d; want ContextKindFeaturesType for subsystem-direct feature attachment", ctx.Kind)
	}
	if ctx.Value != "feature/replication" {
		t.Errorf("Value = %q; want %q", ctx.Value, "feature/replication")
	}
}

func TestClassifyDetectsFeaturesTypeWithSubsystemNestedComponent(t *testing.T) {
	t.Parallel()
	src := "spec:\n  subsystems:\n    data:\n      components:\n        db:\n          type: core/postgres\n          features:\n            scaling:\n              type: feature/autoscaler\n"
	ctx := classifyAtCursor(t, src, 8, 22)
	if ctx.Kind != ContextKindFeaturesType {
		t.Errorf("Kind = %d; want ContextKindFeaturesType for subsystem-nested component feature", ctx.Kind)
	}
}

func TestClassifyDetectsRecipeImplements(t *testing.T) {
	t.Parallel()
	src := "spec:\n  implements: core/api\n"
	ctx := classifyAtCursor(t, src, 1, 17)
	if ctx.Kind != ContextKindRecipeImplements {
		t.Errorf("Kind = %d; want ContextKindRecipeImplements", ctx.Kind)
	}
	if ctx.Value != "core/api" {
		t.Errorf("Value = %q; want %q", ctx.Value, "core/api")
	}
}

func TestClassifyDetectsSchemaProperty(t *testing.T) {
	t.Parallel()
	src := "spec:\n  schema:\n    properties:\n      size:\n        type: string\n"
	ctx := classifyAtCursor(t, src, 4, 15)
	if ctx.Kind != ContextKindSchemaProperty {
		t.Errorf("Kind = %d; want ContextKindSchemaProperty", ctx.Kind)
	}
}

func TestClassifyRejectsBareFeaturesTypeAtTopOfSpec(t *testing.T) {
	t.Parallel()
	// The guard: spec.features.<feat>.type at the top of spec (no
	// subsystem-pair stripped) returns ContextKindUnknown because the
	// path is structurally invalid against AggregateSpec.
	src := "spec:\n  features:\n    foo:\n      type: feature/bar\n"
	ctx := classifyAtCursor(t, src, 3, 14)
	if ctx.Kind != ContextKindUnknown {
		t.Errorf("Kind = %d; want ContextKindUnknown for top-of-spec features.<feat>.type (path does not exist on AggregateSpec)", ctx.Kind)
	}
}

func TestClassifyReturnsUnknownForUnrecognizedPosition(t *testing.T) {
	t.Parallel()
	src := "metadata:\n  version: 1.0.0\n"
	ctx := classifyAtCursor(t, src, 1, 13)
	if ctx.Kind != ContextKindUnknown {
		t.Errorf("Kind = %d; want ContextKindUnknown for metadata.version", ctx.Kind)
	}
}

func TestClassifyDetectsUsesTargetVerboseAliasMap(t *testing.T) {
	t.Parallel()
	src := "spec:\n  components:\n    api:\n      uses:\n        backend:\n          primary:\n            target: db\n"
	ctx := classifyAtCursor(t, src, 6, 21)
	if ctx.Kind != ContextKindUsesTarget {
		t.Errorf("Kind = %d; want ContextKindUsesTarget (verbose alias-map)", ctx.Kind)
	}
	if ctx.Value != "db" {
		t.Errorf("Value = %q; want %q", ctx.Value, "db")
	}
	if ctx.Side != ContextSideValue {
		t.Errorf("Side = %d; want ContextSideValue", ctx.Side)
	}
}

func TestClassifyDetectsUsesTargetSimpleList(t *testing.T) {
	t.Parallel()
	src := "spec:\n  components:\n    api:\n      uses:\n        backend:\n          - db\n          - cache\n"
	ctx := classifyAtCursor(t, src, 5, 13)
	if ctx.Kind != ContextKindUsesTarget {
		t.Errorf("Kind = %d; want ContextKindUsesTarget (simple list)", ctx.Kind)
	}
	if ctx.Value != "db" {
		t.Errorf("Value = %q; want %q", ctx.Value, "db")
	}
}

func TestClassifyDetectsUsesTargetBareStringShorthand(t *testing.T) {
	t.Parallel()
	src := "spec:\n  components:\n    api:\n      uses:\n        backend:\n          hr-db: corporate-hr\n"
	// Cursor on the VALUE "corporate-hr". Line 5, character 17 (after "          hr-db: ").
	ctx := classifyAtCursor(t, src, 5, 17)
	if ctx.Kind != ContextKindUsesTarget {
		t.Errorf("Kind = %d; want ContextKindUsesTarget (bare-string shorthand)", ctx.Kind)
	}
	if ctx.Side != ContextSideValue {
		t.Errorf("Side = %d; want ContextSideValue", ctx.Side)
	}
	if ctx.Value != "corporate-hr" {
		t.Errorf("Value = %q; want %q", ctx.Value, "corporate-hr")
	}
}

func TestClassifyRejectsBareStringShorthandOnKey(t *testing.T) {
	t.Parallel()
	// Same fixture as the bare-string shorthand test, but cursor on the
	// alias KEY "hr-db" (line 5, char 12 — start of "hr-db:"). The
	// side-aware classifier returns Unknown because keys at this shape
	// are aliases, not target component names.
	src := "spec:\n  components:\n    api:\n      uses:\n        backend:\n          hr-db: corporate-hr\n"
	ctx := classifyAtCursor(t, src, 5, 12)
	if ctx.Kind == ContextKindUsesTarget {
		t.Errorf("Kind = ContextKindUsesTarget for alias key; bare-string-shorthand requires Side == Value")
	}
	if ctx.Side != ContextSideKey {
		t.Errorf("Side = %d; want ContextSideKey", ctx.Side)
	}
}

func TestClassifyDetectsUsesTargetSubsystemNested(t *testing.T) {
	t.Parallel()
	src := "spec:\n  subsystems:\n    data:\n      components:\n        primary:\n          uses:\n            backend:\n              alias:\n                target: secondary\n"
	ctx := classifyAtCursor(t, src, 8, 25)
	if ctx.Kind != ContextKindUsesTarget {
		t.Errorf("Kind = %d; want ContextKindUsesTarget under subsystem nesting", ctx.Kind)
	}
}

func TestClassifyDetectsManifestRecipeReferenceFlat(t *testing.T) {
	t.Parallel()
	src := "spec:\n  mappings:\n    components:\n      type:\n        core/server: recipes/helm-server:1.0.0\n"
	ctx := classifyAtCursor(t, src, 4, 24)
	if ctx.Kind != ContextKindManifestRecipeReference {
		t.Errorf("Kind = %d; want ContextKindManifestRecipeReference (flat shape)", ctx.Kind)
	}
	if ctx.Side != ContextSideValue {
		t.Errorf("Side = %d; want ContextSideValue", ctx.Side)
	}
	if ctx.Value != "recipes/helm-server:1.0.0" {
		t.Errorf("Value = %q; want %q", ctx.Value, "recipes/helm-server:1.0.0")
	}
}

func TestClassifyDetectsManifestRecipeReferenceLegacy(t *testing.T) {
	t.Parallel()
	src := "spec:\n  mappings:\n    helm:\n      components:\n        type:\n          core/server: recipes/helm-server:1.0.0\n"
	ctx := classifyAtCursor(t, src, 5, 26)
	if ctx.Kind != ContextKindManifestRecipeReference {
		t.Errorf("Kind = %d; want ContextKindManifestRecipeReference (legacy provider-keyed)", ctx.Kind)
	}
}

func TestClassifyManifestKeySideReturnsUnknown(t *testing.T) {
	t.Parallel()
	src := "spec:\n  mappings:\n    components:\n      type:\n        core/server: recipes/helm-server:1.0.0\n"
	// Cursor on the KEY "core/server" (line 4, char 9 = start of value
	// portion of the indented entry). Key-side cursor on Manifest
	// mappings is intentionally NOT classified at this WI.
	ctx := classifyAtCursor(t, src, 4, 9)
	if ctx.Kind == ContextKindManifestRecipeReference {
		t.Errorf("Kind = ContextKindManifestRecipeReference on the KEY side; should be Unknown at this WI")
	}
}

func TestClassifyDetectsReleaseAggregatePath(t *testing.T) {
	t.Parallel()
	src := "spec:\n  aggregate: my-app\n"
	ctx := classifyAtCursor(t, src, 1, 14)
	if ctx.Kind != ContextKindReleaseAggregatePath {
		t.Errorf("Kind = %d; want ContextKindReleaseAggregatePath", ctx.Kind)
	}
	if ctx.Value != "my-app" {
		t.Errorf("Value = %q; want %q", ctx.Value, "my-app")
	}
}

func TestClassifyDetectsReleaseManifestPath(t *testing.T) {
	t.Parallel()
	src := "spec:\n  manifest: my-manifest\n"
	ctx := classifyAtCursor(t, src, 1, 13)
	if ctx.Kind != ContextKindReleaseManifestPath {
		t.Errorf("Kind = %d; want ContextKindReleaseManifestPath", ctx.Kind)
	}
}

func TestClassifySideKeyVsValueOnSimpleMappingPair(t *testing.T) {
	t.Parallel()
	src := "spec:\n  components:\n    api:\n      type: core/api\n"
	// Cursor on the KEY "type" — line 3, char 7.
	keyCtx := classifyAtCursor(t, src, 3, 7)
	if keyCtx.Side != ContextSideKey {
		t.Errorf("Side on key = %d; want ContextSideKey", keyCtx.Side)
	}
	// Cursor on the VALUE "core/api" — line 3, char 14.
	valCtx := classifyAtCursor(t, src, 3, 14)
	if valCtx.Side != ContextSideValue {
		t.Errorf("Side on value = %d; want ContextSideValue", valCtx.Side)
	}
}

func TestScalarEndLiteralBlockAdvancesLine(t *testing.T) {
	t.Parallel()
	content := []byte("key: |\n  line one\n  line two\n")
	idx := BuildLineIndex(content)
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	valueNode := doc.Content[0].Content[1]
	if valueNode.Style != yaml.LiteralStyle {
		t.Fatalf("fixture precondition: expected LiteralStyle; got %v", valueNode.Style)
	}
	// yaml.v3 reports Value as "line one\nline two\n" with the trailing
	// newline preserved (default clip chomping). The walk:
	//   - "line one"  → Character += 8 (in UTF-16; one unit per ASCII rune)
	//   - "\n"        → Line++, Character = 0
	//   - "line two"  → Character += 8
	//   - "\n"        → Line++, Character = 0
	// So end.Line = startLine + 2 and end.Character = 0.
	end := idx.ScalarEnd(valueNode)
	start := idx.NodeToPosition(valueNode)
	if end.Line != start.Line+2 {
		t.Errorf("ScalarEnd literal block end.Line = %d; want %d (two newlines walked)", end.Line, start.Line+2)
	}
	if end.Character != 0 {
		t.Errorf("ScalarEnd literal block end.Character = %d; want 0 (Character reset after final \\n)", end.Character)
	}
}

func TestScalarEndFoldedBlockStaysSingleLine(t *testing.T) {
	t.Parallel()
	content := []byte("key: >-\n  word one\n  word two\n")
	idx := BuildLineIndex(content)
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	valueNode := doc.Content[0].Content[1]
	if valueNode.Style != yaml.FoldedStyle {
		t.Fatalf("fixture precondition: expected FoldedStyle; got %v", valueNode.Style)
	}
	end := idx.ScalarEnd(valueNode)
	startLine := idx.NodeToPosition(valueNode).Line
	// Folded-block Value = "word one word two" (no embedded newlines).
	if end.Line != startLine {
		t.Errorf("ScalarEnd folded block end.Line = %d; want %d (folded content is single-line)", end.Line, startLine)
	}
}

func TestPositionEncodingUTF8RoundtripASCII(t *testing.T) {
	t.Parallel()
	content := []byte("hello world\n")
	idx := BuildLineIndexWithEncoding(content, PositionEncodingUTF8)
	// UTF-8: char offset == byte offset across ASCII.
	for char := 0; char <= 11; char++ {
		got, err := idx.LSPCharToByte(0, char)
		if err != nil {
			t.Fatalf("LSPCharToByte(%d): %v", char, err)
		}
		if got != char {
			t.Errorf("LSPCharToByte(0, %d) UTF-8 = %d; want %d (char==byte)", char, got, char)
		}
		back := idx.ByteToLSPChar(0, got)
		if back != char {
			t.Errorf("ByteToLSPChar roundtrip at %d failed: got %d", char, back)
		}
	}
}

func TestPositionEncodingUTF8MultibyteUsesByteOffsets(t *testing.T) {
	t.Parallel()
	// "サ" — 3 bytes UTF-8.
	content := []byte("サ end\n")
	idx := BuildLineIndexWithEncoding(content, PositionEncodingUTF8)
	// UTF-8 char offset 3 = byte offset 3 = position right after サ.
	got, err := idx.LSPCharToByte(0, 3)
	if err != nil {
		t.Fatalf("LSPCharToByte: %v", err)
	}
	if got != 3 {
		t.Errorf("LSPCharToByte(0, 3) UTF-8 = %d; want 3", got)
	}
}

func TestPositionEncodingUTF32RoundtripBMP(t *testing.T) {
	t.Parallel()
	// "サーバー" — 4 BMP runes, 12 bytes UTF-8, 4 UTF-16 units, 4 UTF-32 units.
	content := []byte("サーバー\n")
	idx := BuildLineIndexWithEncoding(content, PositionEncodingUTF32)
	// UTF-32 char 4 = byte 12 (4 runes × 3 bytes/rune).
	got, err := idx.LSPCharToByte(0, 4)
	if err != nil {
		t.Fatalf("LSPCharToByte: %v", err)
	}
	if got != 12 {
		t.Errorf("LSPCharToByte(0, 4) UTF-32 = %d; want 12", got)
	}
	back := idx.ByteToLSPChar(0, 12)
	if back != 4 {
		t.Errorf("ByteToLSPChar roundtrip UTF-32 = %d; want 4", back)
	}
}

func TestPositionEncodingUTF32SupplementaryCountsAsOne(t *testing.T) {
	t.Parallel()
	// "🎉" — supplementary plane: 1 rune, 4 bytes UTF-8, 2 UTF-16 units, 1 UTF-32 unit.
	content := []byte("🎉 end\n")
	idx := BuildLineIndexWithEncoding(content, PositionEncodingUTF32)
	// UTF-32 char 1 = byte 4 (right after the 4-byte emoji).
	got, err := idx.LSPCharToByte(0, 1)
	if err != nil {
		t.Fatalf("LSPCharToByte: %v", err)
	}
	if got != 4 {
		t.Errorf("LSPCharToByte(0, 1) UTF-32 supplementary = %d; want 4 (one UTF-32 unit per rune)", got)
	}
}

func TestBuildLineIndexWithEncodingPropagates(t *testing.T) {
	t.Parallel()
	idx := BuildLineIndexWithEncoding([]byte("x\n"), PositionEncodingUTF8)
	if idx.Encoding() != PositionEncodingUTF8 {
		t.Errorf("Encoding() = %q; want %q", idx.Encoding(), PositionEncodingUTF8)
	}
	defaultIdx := BuildLineIndex([]byte("x\n"))
	if defaultIdx.Encoding() != PositionEncodingUTF16 {
		t.Errorf("BuildLineIndex (default) Encoding() = %q; want %q", defaultIdx.Encoding(), PositionEncodingUTF16)
	}
}
