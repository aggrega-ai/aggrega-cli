// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parsersocket"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// importerDeclaring is an importer entity advertising formats and recording the
// recognition signatures detect, standing in for one a catalog holds.
func importerDeclaring(name string, formats []string, detect ...types.PluginDetectSignature) types.Plugin {
	capabilities := make([]string, 0, len(formats))
	for _, f := range formats {
		capabilities = append(capabilities, "import:"+f)
	}
	return types.Plugin{
		Metadata: types.PluginMetadata{Name: name, Version: "1.0.0", PluginType: "importer"},
		Spec:     types.PluginSpec{Importer: &types.PluginImporter{Capabilities: capabilities, Detect: detect}},
	}
}

// countingParsers wraps the binary's parsers and counts each syntax a recognition
// asks to have parsed, so a case can hold the recognition to parsing a source at
// most once per syntax.
type countingParsers struct {
	res    parsersocket.ParserResolver
	parsed map[string]int
}

func (c *countingParsers) ParserPluginForFormat(format string) (types.Plugin, error) {
	c.parsed[format]++
	return c.res.ParserPluginForFormat(format)
}

// detectionSeamOver is a seam whose catalog holds importers and parses through
// the binary's own parsers, counting each parse.
func detectionSeamOver(t *testing.T, importers ...types.Plugin) (Seam, *countingParsers) {
	t.Helper()
	res, load := importopstest.EmbeddedParsers(t)
	counting := &countingParsers{res: res, parsed: map[string]int{}}
	return Seam{ParserRes: counting, ParserLoad: load, Importers: importers}, counting
}

// TestAPredicateHoldsOverTheParsedTree drives the generic predicate over every
// kind it names, at the top of the tree, nested, through an array position, and
// where the path reaches nothing — and the string comparisons, which only a
// string value satisfies. Each row names the source, the predicate and whether a
// signature carrying it alone recognises the source.
func TestAPredicateHoldsOverTheParsedTree(t *testing.T) {
	ctx := context.Background()
	const source = `{"kind":"Flow","version":2,"enabled":true,"spec":{"nodes":[{"type":"n8n-nodes-base.start"}],` +
		`"edges":[],"empty":null,"meta":{}}}`

	for _, tc := range []struct {
		name  string
		match types.PluginPathMatch
		want  bool
	}{
		{"a top-level string is a string", types.PluginPathMatch{Path: "kind", Is: types.IsString}, true},
		{"a top-level number is a number", types.PluginPathMatch{Path: "version", Is: types.IsNumber}, true},
		{"a top-level boolean is a boolean", types.PluginPathMatch{Path: "enabled", Is: types.IsBoolean}, true},
		{"a nested object is an object", types.PluginPathMatch{Path: "spec.meta", Is: types.IsObject}, true},
		{"a nested array is an array", types.PluginPathMatch{Path: "spec.edges", Is: types.IsArray}, true},
		{"a string is not an array", types.PluginPathMatch{Path: "kind", Is: types.IsArray}, false},
		{"an array is not an object", types.PluginPathMatch{Path: "spec.nodes", Is: types.IsObject}, false},
		{"a number is not a string", types.PluginPathMatch{Path: "version", Is: types.IsString}, false},
		{"null is present", types.PluginPathMatch{Path: "spec.empty", Is: types.IsPresent}, true},
		{"null is of no kind", types.PluginPathMatch{Path: "spec.empty", Is: types.IsObject}, false},
		{"a missing member is absent", types.PluginPathMatch{Path: "spec.missing", Is: types.IsAbsent}, true},
		{"a present member is not absent", types.PluginPathMatch{Path: "spec.edges", Is: types.IsAbsent}, false},
		{"a missing member is not present", types.PluginPathMatch{Path: "spec.missing", Is: types.IsPresent}, false},
		{"a path through a scalar reaches nothing", types.PluginPathMatch{Path: "kind.inner", Is: types.IsAbsent}, true},
		{"a whole-number segment selects an array position", types.PluginPathMatch{
			Path: "spec.nodes.0.type", Is: types.IsString, Prefix: "n8n-nodes-base."}, true},
		{"a position past the end reaches nothing", types.PluginPathMatch{Path: "spec.nodes.1", Is: types.IsAbsent}, true},
		{"a name segment does not index an array", types.PluginPathMatch{Path: "spec.nodes.first", Is: types.IsAbsent}, true},
		{"equals holds for the exact string", types.PluginPathMatch{Path: "kind", Is: types.IsString, Equals: "Flow"}, true},
		{"equals does not hold for another string", types.PluginPathMatch{Path: "kind", Is: types.IsString, Equals: "flow"}, false},
		{"equals never holds for a non-string", types.PluginPathMatch{Path: "version", Is: types.IsPresent, Equals: "2"}, false},
		{"prefix holds for a string beginning with it", types.PluginPathMatch{Path: "kind", Is: types.IsString, Prefix: "Fl"}, true},
		{"prefix does not hold for a string not beginning with it", types.PluginPathMatch{Path: "kind", Is: types.IsString, Prefix: "ow"}, false},
		{"an is outside the declared kinds holds for nothing", types.PluginPathMatch{Path: "kind", Is: "text"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seam, _ := detectionSeamOver(t, importerDeclaring("probe", []string{"probe"},
				types.PluginDetectSignature{Syntax: "json", Match: []types.PluginPathMatch{tc.match}}))
			got, err := detectFormat(ctx, "source.json", []byte(source), seam)
			recognised := err == nil && got == "probe"
			if recognised != tc.want {
				t.Fatalf("the predicate %+v recognised the source = %v (format %q, err %v); want %v",
					tc.match, recognised, got, err, tc.want)
			}
			if !tc.want {
				var undetected *UndetectedFormatError
				if !errors.As(err, &undetected) {
					t.Fatalf("an unmatched predicate is refused as no recognition, got %T: %v", err, err)
				}
			}
		})
	}
}

// TestASignatureAppliesOnlyToTheFileNamesItDeclares drives the file patterns
// against the end of the source's name: one segment against the file's own name,
// several against as many trailing segments of its path, and no pattern against
// any name.
func TestASignatureAppliesOnlyToTheFileNamesItDeclares(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		files  []string
		source string
		want   bool
	}{
		{"no pattern applies to any name", nil, "anything/at/all.json", true},
		{"a one-segment pattern matches the file's own name", []string{"mcp.json"}, "/home/a/.cursor/mcp.json", true},
		{"a glob matches within the file's own name", []string{"*.flow.json"}, "exports/a.flow.json", true},
		{"a two-segment pattern matches the trailing directory and name", []string{".vscode/mcp.json"}, "proj/.vscode/mcp.json", true},
		{"a two-segment pattern refuses another directory", []string{".vscode/mcp.json"}, "proj/.cursor/mcp.json", false},
		{"a pattern longer than the name matches nothing", []string{"a/b/c.json"}, "c.json", false},
		{"any of several patterns suffices", []string{"x.json", "claude_desktop_config.json"}, "claude_desktop_config.json", true},
		{"a pattern that is not well formed matches nothing", []string{"[.json"}, "[.json", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seam, _ := detectionSeamOver(t, importerDeclaring("probe", []string{"probe"}, types.PluginDetectSignature{
				Syntax: "json", Files: tc.files,
				Match: []types.PluginPathMatch{{Path: "servers", Is: types.IsPresent}},
			}))
			got, err := detectFormat(ctx, tc.source, []byte(`{"servers":{}}`), seam)
			recognised := err == nil && got == "probe"
			if recognised != tc.want {
				t.Fatalf("files %v against %q recognised = %v (err %v); want %v", tc.files, tc.source, recognised, err, tc.want)
			}
		})
	}
}

// TestARecognitionParsesTheSourceOncePerSyntax holds the recognition to parsing a
// source at most once per syntax however many signatures name it, and only for a
// syntax some signature whose file patterns admit the source names.
func TestARecognitionParsesTheSourceOncePerSyntax(t *testing.T) {
	json := func(path string) types.PluginDetectSignature {
		return types.PluginDetectSignature{Syntax: "json", Match: []types.PluginPathMatch{{Path: path, Is: types.IsPresent}}}
	}
	seam, counting := detectionSeamOver(t,
		importerDeclaring("first", []string{"first"}, json("a"), json("b")),
		importerDeclaring("second", []string{"second"}, json("c")),
		importerDeclaring("third", []string{"third"}, types.PluginDetectSignature{
			Syntax: "yaml", Files: []string{"*.yaml"}, Match: []types.PluginPathMatch{{Path: "c", Is: types.IsPresent}},
		}),
	)
	got, err := detectFormat(context.Background(), "source.json", []byte(`{"c":1}`), seam)
	if err != nil || got != "second" {
		t.Fatalf("the source was recognised as %q (err %v); want second", got, err)
	}
	if counting.parsed["json"] != 1 {
		t.Errorf("the source was parsed as json %d times; want exactly once", counting.parsed["json"])
	}
	if counting.parsed["yaml"] != 0 {
		t.Errorf("the source was parsed as yaml %d times though no signature naming yaml applies to its name",
			counting.parsed["yaml"])
	}
}

// TestASourceNotInASyntaxMatchesNoSignatureNamingIt is the parse that halts: a
// source that does not parse in a signature's syntax is not that syntax, so the
// signature does not match and the recognition carries on with the others.
func TestASourceNotInASyntaxMatchesNoSignatureNamingIt(t *testing.T) {
	seam, _ := detectionSeamOver(t,
		importerDeclaring("json-shaped", []string{"json-shaped"}, types.PluginDetectSignature{
			Syntax: "json", Match: []types.PluginPathMatch{{Path: "kind", Is: types.IsPresent}},
		}),
		importerDeclaring("yaml-shaped", []string{"yaml-shaped"}, types.PluginDetectSignature{
			Syntax: "yaml", Match: []types.PluginPathMatch{{Path: "kind", Is: types.IsString, Equals: "Thing"}},
		}),
	)
	got, err := detectFormat(context.Background(), "thing.yaml", []byte("kind: Thing\nspec: {}\n"), seam)
	if err != nil || got != "yaml-shaped" {
		t.Fatalf("a YAML source was recognised as %q (err %v); want yaml-shaped", got, err)
	}
}

// TestASyntaxNoParserServesStopsTheRecognition is the parse that cannot be run: a
// signature naming a syntax no parser serves went unevaluated, so the
// recognition stops naming the parser's absence rather than choosing among the
// signatures it could evaluate.
func TestASyntaxNoParserServesStopsTheRecognition(t *testing.T) {
	seam, _ := detectionSeamOver(t,
		importerDeclaring("json-shaped", []string{"json-shaped"}, types.PluginDetectSignature{
			Syntax: "json", Match: []types.PluginPathMatch{{Path: "kind", Is: types.IsPresent}},
		}),
		importerDeclaring("cobol-shaped", []string{"cobol-shaped"}, types.PluginDetectSignature{
			Syntax: "cobol", Match: []types.PluginPathMatch{{Path: "division", Is: types.IsPresent}},
		}),
	)
	_, err := detectFormat(context.Background(), "thing.json", []byte(`{"kind":"x"}`), seam)
	if !errors.Is(err, catalog.ErrNoParserForFormat()) {
		t.Fatalf("a signature no parser can read was passed over rather than reported: %v", err)
	}
	if !strings.Contains(err.Error(), "importer/cobol-shaped") {
		t.Errorf("the refusal does not name the importer whose signature went unread: %v", err)
	}
}

// TestRecognitionIsDeterministicAndCollapsesOneFormat holds the recognition's
// ordering: two importers serving one format recognise one format, an importer
// advertising two formats recognises the source as both, and the formats a
// refusal names come in the order the catalog lists the importers.
func TestRecognitionIsDeterministicAndCollapsesOneFormat(t *testing.T) {
	ctx := context.Background()
	present := types.PluginDetectSignature{Syntax: "json", Match: []types.PluginPathMatch{{Path: "a", Is: types.IsPresent}}}

	seam, _ := detectionSeamOver(t,
		importerDeclaring("one", []string{"shared"}, present),
		importerDeclaring("two", []string{"shared"}, present))
	if got, err := detectFormat(ctx, "s.json", []byte(`{"a":1}`), seam); err != nil || got != "shared" {
		t.Errorf("two importers of one format recognised %q (err %v); want the one format", got, err)
	}

	seam, _ = detectionSeamOver(t,
		importerDeclaring("zeta", []string{"z"}, present),
		importerDeclaring("both", []string{"x", "y"}, present))
	for range 3 {
		_, err := detectFormat(ctx, "s.json", []byte(`{"a":1}`), seam)
		var ambiguous *AmbiguousFormatError
		if !errors.As(err, &ambiguous) {
			t.Fatalf("three formats recognised were not refused as ambiguous: %v", err)
		}
		if want := []string{"z", "x", "y"}; !slices.Equal(ambiguous.Formats(), want) {
			t.Fatalf("the refusal names %v; want %v, in the order the catalog lists the importers", ambiguous.Formats(), want)
		}
	}
}

// TestAnImporterRecordingNoSignatureIsNeverRecognised holds an importer entity
// recording no signature — one generated before the record existed among them —
// to being reachable only by naming its format: it is advertised, and nothing is
// recognised as it.
func TestAnImporterRecordingNoSignatureIsNeverRecognised(t *testing.T) {
	seam, _ := detectionSeamOver(t, importerDeclaring("silent", []string{"silent"}))
	_, err := detectFormat(context.Background(), "s.json", []byte(`{}`), seam)
	var undetected *UndetectedFormatError
	if !errors.As(err, &undetected) {
		t.Fatalf("a source was recognised by an importer declaring no signature: %v", err)
	}
	if !slices.Equal(undetected.Formats(), []string{"silent"}) {
		t.Errorf("the refusal offers %v; the silent importer's format is still one to name", undetected.Formats())
	}
}
