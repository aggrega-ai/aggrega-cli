// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"encoding/json"
	"reflect"
	"runtime"
	"strings"
	"testing"

	libschema "github.com/google/jsonschema-go/jsonschema"
)

// This file holds the agreement between the schema library's failure text and
// the rewrites [findingMessage] performs on it, and it is the only place that
// agreement is checked against text the library actually produced.
//
// # Why the inputs have to come from the library
//
// The rewrites are text transformations over a format the library owns and does
// not publish as a contract. A test that feeds hand-written messages into them
// checks that the transformations still do what they did — which stays true
// forever, because the literals move only when somebody edits them. It would
// pass unchanged on the day a library release renamed the prefix and every
// diagnostic the product emits started carrying schema-document coordinates the
// reader never wrote.
//
// So every message asserted here is produced by resolving a schema and
// validating a value against it, with the same library the adapter links. When
// the format moves, these inputs move with it, and the assertions below stop
// holding.
//
// # The two directions, and why one alone is not enough
//
// [TestEveryLibraryFailureShapeIsFullyRewritten] asserts both:
//
//   - The library still opens its failure text with the location-chain word the
//     rewrite recognises. This is the direction that catches a rename, and it is
//     the only one that can. [stripLocationChain] removes a chain by matching a
//     literal opening word; a library that renamed that word would leave the
//     chain in place on every message at once, and a check that only asked
//     whether the chain had been removed would see a message with no recognised
//     chain in it and report success.
//   - The rewrites completed: no location segment the strip recognises survives
//     anywhere in the result, no absent-value artifact survives, and no value
//     the failing call was handed is still rendered in Go's own syntax.
//
// The first direction fails loudly and immediately on a rename. The second
// fails when a rewrite stops reaching a shape it used to reach.

// schemaLibraryModulePath is the module whose failure text the rewrites read.
//
// It is stated rather than derived because the module is what
// [linkedSchemaLibrary] looks for in a compiled-in source path, and because a
// second schema library is present in this module's dependency graph for
// unrelated consumers. Pinning the wrong one would produce a check that passes
// on every release of the library the adapter actually uses.
const schemaLibraryModulePath = "github.com/google/jsonschema-go"

// verifiedSchemaLibraryVersion is the release whose emitted failure text the
// corpus in this file was read against.
//
// The corpus is a record of what one library release emits. A different release
// may phrase a failure differently, order a chain differently, or stop emitting
// a shape entirely — none of which the assertions can distinguish from a
// regression in the rewrites themselves. So the version is asserted, and a bump
// fails here first: the remedy is to re-read the raw text each case produces
// against the new release and move this constant, which is a deliberate act
// rather than a silent one.
const verifiedSchemaLibraryVersion = "v0.4.3"

// linkedSchemaLibrary reports the schema-library release compiled into this
// test binary, together with the source path it was read from.
//
// The version is read off the recorded source path of a library function rather
// than from the module manifest, because the manifest states what the build was
// asked for and this states what the build produced. The module cache lays a
// dependency out under a directory carrying its version, the compiler records
// that path against every function it compiles, and the version survives a
// trimmed-path build because trimming removes the cache prefix and keeps the
// module-and-version segment.
//
// An empty version means the path carried no version segment, which is what a
// local replacement directive produces. That is reported rather than ignored:
// the corpus's provenance is unknown in that state, so the pin below fails and
// names the path it read.
func linkedSchemaLibrary() (version, sourcePath string) {
	fn := runtime.FuncForPC(reflect.ValueOf((*libschema.Schema).Resolve).Pointer())
	if fn == nil {
		return "", ""
	}
	sourcePath, _ = fn.FileLine(fn.Entry())

	marker := schemaLibraryModulePath + "@"
	start := strings.Index(sourcePath, marker)
	if start < 0 {
		return "", sourcePath
	}
	version = sourcePath[start+len(marker):]
	if end := strings.IndexByte(version, '/'); end >= 0 {
		version = version[:end]
	}
	return version, sourcePath
}

// leftover is one thing a rewrite was supposed to remove and did not: the class
// of rewrite that missed it, and the text it left behind.
type leftover struct {
	rewrite string
	text    string
}

// rewriteLeftovers reports what [findingMessage]'s rewrites left in message,
// given the value the failing validation call was handed.
//
// One entry per rewrite that did not complete, so a failure names which rewrite
// was missed rather than reporting that the message looks wrong. An empty
// result means every rewrite reached everything it exists to reach.
//
// The location-chain class asks [stripLocationChain] itself whether anything
// remains strippable, rather than restating what a segment looks like. A second
// description of the segment shape would agree with the strip's own until the
// day one of them was changed, and the disagreement would surface as this
// reporting a leftover the strip had deliberately declined — or, worse, missing
// one it had simply failed to remove.
//
// It is asked line by line because the strip removes only the run of segments
// that OPENS the text, while a library is free to fold a nested failure into a
// later line of the same message. Such a line carries the same schema-document
// coordinates in the same vocabulary, and it reaches a reader just as the
// leading run would.
//
// A segment the strip declines by design is not a leftover here and must not
// be, because the decline is the rewrite working correctly:
// [TestAnUnstrippableLocationSegmentIsLeftWholeAndItsReasonHolds] is where that
// case is held to its reason.
func rewriteLeftovers(message string, instance any) []leftover {
	var found []leftover

	for _, line := range strings.Split(message, "\n") {
		if stripped := stripLocationChain(line); stripped != line {
			found = append(found, leftover{
				rewrite: "the schema-location chain",
				text:    line,
			})
		}
	}

	if strings.Contains(message, nullValueArtifact) {
		found = append(found, leftover{
			rewrite: "the absent value's name",
			text:    nullValueArtifact,
		})
	}

	for _, container := range containerRenderings(instance) {
		if strings.Contains(message, container.text) {
			found = append(found, leftover{
				rewrite: "the container's JSON type name, which should have replaced this rendering with " + container.jsonType,
				text:    container.text,
			})
		}
	}

	return found
}

// libraryFailure resolves a schema written as JSON, validates a value against
// it with the schema library the adapter links, and returns the library's own
// failure.
//
// It mirrors the call the adapter makes and returns the error itself rather
// than its text, so what the rewrites are handed here is the value they are
// handed in production rather than a copy of its rendering.
func libraryFailure(t *testing.T, schemaDoc string, instance any) error {
	t.Helper()

	schema := &libschema.Schema{}
	if err := json.Unmarshal([]byte(schemaDoc), schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve schema: %v", err)
	}
	err = resolved.Validate(instance)
	if err == nil {
		t.Fatalf("this value satisfies its schema, so there is no failure text to read; "+
			"a case that stops failing asserts nothing. schema: %s", schemaDoc)
	}
	return err
}

// libraryFailureShapes is the corpus: one violation per shape of failure text
// the library produces for a call the adapter makes.
//
// Each case names a value and a schema it violates, never an expected message.
// What the library says is read at run time; what is asserted is that the
// rewrites reach all of it.
//
// The keyword spread is deliberate. A chain is one segment deep at a call root
// and several deep inside a nested position; a root the schema named after
// itself is labelled differently from an anonymous one; and the value-naming
// rewrite only has work to do where the library folded the offending value into
// its text, which some keywords do and others do not.
//
// Compound keywords the adapter evaluates itself — the branch and conditional
// families — are deliberately absent, because the adapter never hands the
// library a whole compound keyword and so never receives its text. Their raw
// text carries nested location segments on lines of its own, which is the
// reason the leftover check reads every line rather than only the first: if the
// adapter ever stopped modelling one of these itself, that text would start
// arriving here.
var libraryFailureShapes = []struct {
	name     string
	schema   string
	instance any
}{
	{
		name:     "a call root, one chain segment deep",
		schema:   `{"type": "array"}`,
		instance: map[string]any{"replicas": 1},
	},
	{
		name:     "a nested position, several chain segments deep",
		schema:   `{"type": "object", "properties": {"inject": {"type": "object", "properties": {"env": {"type": "string"}}}}}`,
		instance: map[string]any{"inject": map[string]any{"env": 1}},
	},
	{
		name:     "a root the schema named after itself",
		schema:   `{"$id": "https://example.test/delivery", "type": "array"}`,
		instance: map[string]any{"replicas": 1},
	},
	{
		name:     "an absent value the library folded into its text",
		schema:   `{"type": "object", "properties": {"image": {"type": "string"}}}`,
		instance: map[string]any{"image": nil},
	},
	{
		name:     "an object the library folded into its text",
		schema:   `{"type": "object", "properties": {"env": {"type": "array"}}}`,
		instance: map[string]any{"env": map[string]any{"ZETA": "z", "ALPHA": "a"}},
	},
	{
		name:     "an array the library folded into its text",
		schema:   `{"type": "object", "properties": {"name": {"type": "string"}}}`,
		instance: map[string]any{"name": []any{"first", "second"}},
	},
	{
		name:     "an element the library reached inside an array itself",
		schema:   `{"type": "array", "items": {"type": "string"}}`,
		instance: []any{map[string]any{"containerPort": "8080"}},
	},
	{
		name:     "a key the schema requires and the value omits",
		schema:   `{"type": "object", "required": ["image"]}`,
		instance: map[string]any{"replicas": 1},
	},
	{
		name:     "a value outside the set the schema enumerates",
		schema:   `{"enum": ["tcp", "udp"]}`,
		instance: "sctp",
	},
	{
		name:     "a string the schema's pattern rejects",
		schema:   `{"type": "string", "pattern": "^[a-z]+$"}`,
		instance: "Not-Lower",
	},
	{
		name:     "a number below the schema's floor",
		schema:   `{"type": "number", "minimum": 10}`,
		instance: float64(3),
	},
	{
		name:     "a key the schema does not declare and does not allow",
		schema:   `{"type": "object", "additionalProperties": false, "properties": {"image": {}}}`,
		instance: map[string]any{"image": "nginx", "imagePullPolicy": "Always"},
	},
	{
		name:     "an array shorter than the schema's floor",
		schema:   `{"type": "array", "minItems": 3}`,
		instance: []any{"first"},
	},
	{
		name:     "an array holding the same element twice",
		schema:   `{"type": "array", "uniqueItems": true}`,
		instance: []any{"tcp", "tcp"},
	},
	{
		name:     "a key whose name the schema constrains",
		schema:   `{"type": "object", "propertyNames": {"pattern": "^[A-Z]+$"}}`,
		instance: map[string]any{"lower": 1},
	},
	{
		name:     "an array holding no element of the required shape",
		schema:   `{"type": "array", "contains": {"type": "number"}}`,
		instance: []any{"first", "second"},
	},
	{
		name:     "a position the schema reached through a reference",
		schema:   `{"$defs": {"delivery": {"type": "array"}}, "$ref": "#/$defs/delivery"}`,
		instance: map[string]any{"ZETA": "z"},
	},
	{
		name:     "a position the schema reached through a chain of references",
		schema:   `{"$defs": {"outer": {"type": "object", "properties": {"env": {"$ref": "#/$defs/inner"}}}, "inner": {"type": "array"}}, "$ref": "#/$defs/outer"}`,
		instance: map[string]any{"env": map[string]any{"ZETA": "z"}},
	},
	{
		name:     "a key the schema requires only because another is present",
		schema:   `{"type": "object", "dependentRequired": {"tls": ["certificate"]}}`,
		instance: map[string]any{"tls": true},
	},
	{
		name:     "a leading element the schema types individually",
		schema:   `{"type": "array", "prefixItems": [{"type": "string"}]}`,
		instance: []any{5},
	},
	{
		name:     "a string longer than the schema's ceiling",
		schema:   `{"type": "string", "maxLength": 2}`,
		instance: "abcdef",
	},
	{
		name:     "an object with fewer keys than the schema's floor",
		schema:   `{"type": "object", "minProperties": 3}`,
		instance: map[string]any{"image": "nginx"},
	},
	{
		name:     "a number that is not a whole multiple of the schema's step",
		schema:   `{"type": "number", "multipleOf": 3}`,
		instance: float64(4),
	},
	{
		name:     "a value the schema pins to one constant",
		schema:   `{"const": "ClusterIP"}`,
		instance: "LoadBalancer",
	},
}

// TestEveryLibraryFailureShapeIsFullyRewritten holds the schema library's
// failure text and the adapter's rewrites of it to each other, over text the
// library produced rather than text this file wrote.
//
// It fails in two situations, and the failure says which. A library that no
// longer opens its text with the word the strip recognises fails the first
// assertion on every case at once, which is the shape a release that renamed
// the prefix takes — and it is worth failing loudly, because the same rename
// would otherwise put a chain of schema-document coordinates in front of every
// reader while every other test in this package continued to pass. A rewrite
// that stopped reaching a shape it used to reach fails the second, naming the
// rewrite and quoting what it left.
//
// The first assertion overlaps [TestLibraryNamesACallRootAsRoot] without
// replacing it, and the overlap is deliberate. That test pins the whole label a
// call root is given, because the label is REPLACED by the name of the
// definition under check; this pins only the word every segment opens with,
// because that word is what the chain is RECOGNISED by. A release could keep
// one and move the other, so each failure points at the rewrite it belongs to.
func TestEveryLibraryFailureShapeIsFullyRewritten(t *testing.T) {
	t.Parallel()

	version, sourcePath := linkedSchemaLibrary()

	for _, shape := range libraryFailureShapes {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()

			failure := libraryFailure(t, shape.schema, shape.instance)
			raw := failure.Error()

			if !strings.HasPrefix(raw, validatingPrefix) {
				t.Fatalf("the schema library no longer opens its failure text with %q, so the "+
					"location chain is no longer recognised and none of it will be removed from "+
					"any diagnostic.\n"+
					"  library:   %s %s\n"+
					"  read from: %s\n"+
					"  emitted:   %q\n"+
					"Re-read what the chain now looks like and teach stripLocationChain to "+
					"recognise it, then move verifiedSchemaLibraryVersion to the release "+
					"read against.",
					validatingPrefix, schemaLibraryModulePath, version, sourcePath, raw)
			}

			rewritten := findingMessage(failure, shape.instance, "")

			for _, left := range rewriteLeftovers(rewritten, shape.instance) {
				t.Errorf("%s did not reach this failure, so a reader is shown %q.\n"+
					"  library:   %s %s\n"+
					"  emitted:   %q\n"+
					"  rewritten: %q",
					left.rewrite, left.text, schemaLibraryModulePath, version, raw, rewritten)
			}
		})
	}
}

// TestTheRewriteLeftoverCheckFiresOnEachShapeItGuards proves the check above
// can fail, by handing it one message per class of leftover it recognises and
// requiring it to report that class.
//
// Without this, a check that recognised nothing would report a clean result
// over every case in the corpus and read exactly like a check that had found
// nothing to report. The messages here are written rather than produced,
// deliberately and only here: the subject is the recogniser, not the library.
func TestTheRewriteLeftoverCheckFiresOnEachShapeItGuards(t *testing.T) {
	t.Parallel()

	instance := map[string]any{"env": map[string]any{"ZETA": "z"}}

	cases := []struct {
		name        string
		message     string
		wantRewrite string
	}{
		{
			name:        "a location chain the strip did not remove",
			message:     libraryRootLabel + `type: object has type "object", want "array"`,
			wantRewrite: "the schema-location chain",
		},
		{
			name:        "a location segment folded into a later line",
			message:     "anyOf: no branch matched:\n" + `validating /anyOf/0: type: object has type "object", want "string"`,
			wantRewrite: "the schema-location chain",
		},
		{
			name:        "an absent value left as the formatter rendered it",
			message:     `type: ` + nullValueArtifact + ` has type "null", want "string"`,
			wantRewrite: "the absent value's name",
		},
		{
			name:        "a container left in Go's own syntax",
			message:     `type: map[ZETA:z] has type "object", want "array"`,
			wantRewrite: "the container's JSON type name",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			found := rewriteLeftovers(tc.message, instance)
			if len(found) == 0 {
				t.Fatalf("this message carries %s and the check reported nothing, so a clean "+
					"result over the corpus certifies nothing: %q", tc.wantRewrite, tc.message)
			}
			named := false
			for _, left := range found {
				if strings.HasPrefix(left.rewrite, tc.wantRewrite) {
					named = true
				}
			}
			if !named {
				t.Errorf("the report must name %s so a reader learns which rewrite was missed; got %#v",
					tc.wantRewrite, found)
			}
		})
	}
}

// TestACleanlyRewrittenMessageReportsNoLeftover is the other half of the
// recogniser's calibration: a message with nothing left in it must report
// nothing, or every case in the corpus would pass through a check that objects
// to anything at all.
func TestACleanlyRewrittenMessageReportsNoLeftover(t *testing.T) {
	t.Parallel()

	instance := map[string]any{"env": map[string]any{"ZETA": "z"}}
	const clean = `type: object has type "object", want "array"`

	if found := rewriteLeftovers(clean, instance); len(found) != 0 {
		t.Errorf("a fully rewritten message must report no leftover; got %#v for %q", found, clean)
	}
}

// TestAnUnstrippableLocationSegmentIsLeftWholeAndItsReasonHolds covers the one
// shape the strip declines to remove, and holds the decline to the reason for
// it.
//
// A segment is recognised only while its location carries no space, because the
// clause the chain runs into always contains one — so the space is what stops
// the removal eating the failure text itself. A schema that names itself with
// an identifier containing a space produces a segment the strip therefore
// refuses, and the message reaches a reader whole. That is a message that reads
// poorly; it is never a message that names the wrong location, and shortening
// it would mean deleting text the reader needs.
//
// Two things are asserted, because the decline is only correct while its reason
// holds: the library still folds the schema's own identifier into the segment,
// and the identifier still arrives carrying the space. A release that quoted or
// escaped it would make this shape strippable, and the strip would then be
// declining a segment it could safely remove.
func TestAnUnstrippableLocationSegmentIsLeftWholeAndItsReasonHolds(t *testing.T) {
	t.Parallel()

	const spacedIdentifier = "https://example.test/a delivery"
	raw := libraryFailure(t,
		`{"$id": "`+spacedIdentifier+`", "type": "array"}`,
		map[string]any{"ZETA": "z"},
	).Error()

	if !strings.Contains(raw, spacedIdentifier) {
		t.Fatalf("the schema library no longer folds a schema's own identifier into its location "+
			"segment, so this case no longer produces the shape it exists to cover; got %q", raw)
	}

	segment := strings.TrimPrefix(raw, validatingPrefix)
	if separator := strings.Index(segment, ": "); separator < 0 {
		t.Fatalf("the location segment no longer ends where the strip looks for its end; got %q", raw)
	} else if !strings.ContainsRune(segment[:separator], ' ') {
		t.Fatalf("the identifier no longer reaches the segment carrying a space, so the strip is "+
			"now declining a segment it could remove; got %q", raw)
	}

	if got := stripLocationChain(raw); got != raw {
		t.Errorf("a segment carrying a space must be left whole rather than partly eaten;\n"+
			"  emitted: %q\n  became:  %q", raw, got)
	}
}

// TestTheLinkedSchemaLibraryIsTheReleaseTheShapesWereReadAgainst fails when the
// dependency moves, because every shape in this file is a reading of one
// release's output and a different release may phrase, order or omit any of
// them.
//
// It fails on a bump that changed nothing as readily as on one that changed
// everything, and that is the intent: the corpus cannot tell the two apart, so
// the reading is redone by a person and the constant moved deliberately.
func TestTheLinkedSchemaLibraryIsTheReleaseTheShapesWereReadAgainst(t *testing.T) {
	t.Parallel()

	version, sourcePath := linkedSchemaLibrary()

	if version == "" {
		t.Fatalf("the release of %s compiled into this binary could not be read, so the failure "+
			"shapes below have no known provenance. Source path read: %q",
			schemaLibraryModulePath, sourcePath)
	}
	if version != verifiedSchemaLibraryVersion {
		t.Errorf("the failure shapes in this file were read against %s %s and this binary links %s.\n"+
			"  read from: %s\n"+
			"Re-read the text each shape produces against the linked release, then move "+
			"verifiedSchemaLibraryVersion to it.",
			schemaLibraryModulePath, verifiedSchemaLibraryVersion, version, sourcePath)
	}
}
