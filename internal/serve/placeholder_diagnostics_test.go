// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// What the editor is told about a drafted document's placeholders, driven over
// the daemon's own request path: the drafted document comes from the drafting
// request an editor sends, and every verdict is read off a validation request's
// answer rather than off the function that builds it.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// suggestLead is what the daemon's remedy for a placeholder opens with, ahead
// of what the value takes. It is written out here rather than read from the
// daemon, so an assertion built on it can fail when the wording moves.
const suggestLead = "fill it with "

// owedEntry is one placeholder as a surface names it: the key it sits at and
// what it takes.
type owedEntry struct {
	keyPath string
	takes   string
}

// draftedOverTheWire asks the daemon for a drafted document through the
// drafting request an editor sends, and returns the document.
func draftedOverTheWire(t *testing.T, srv *Server, kind, name string) string {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/scaffoldEntity", scaffoldEntityParams{Kind: kind, Name: name}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("the daemon refused to draft a %s: %+v", kind, resp.Error)
	}
	var drafted scaffoldEntityResult
	if err := json.Unmarshal(resp.Result, &drafted); err != nil {
		t.Fatalf("decode the drafted %s: %v", kind, err)
	}
	return drafted.Skeleton
}

// validateContentOverTheWire drives one aggrega/validate request for content at
// file, naming release when it is not empty, and returns the daemon's answer.
func validateContentOverTheWire(t *testing.T, srv *Server, file, content, release string) validateResult {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/validate", validateParams{File: file, Content: content, Release: release}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("validate %s: unexpected error envelope %+v", file, resp.Error)
	}
	var result validateResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode the validate answer for %s: %v", file, err)
	}
	return result
}

// initializeWithEncoding negotiates the one position encoding a session is to
// count columns in, as a client's handshake does.
func initializeWithEncoding(t *testing.T, srv *Server, encoding string) {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/initialize", initializeParams{
		Capabilities: clientCapabilities{General: &generalCapabilities{PositionEncodings: []string{encoding}}},
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("initialize: %+v", resp.Error)
	}
	var result initializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode the initialize answer: %v", err)
	}
	if result.Capabilities.PositionEncoding != encoding {
		t.Fatalf("the session negotiated %q, not %q", result.Capabilities.PositionEncoding, encoding)
	}
}

// replaceOnce replaces text a document carries, failing when it carries none,
// so a fill that changed nothing cannot pass for one that did.
func replaceOnce(t *testing.T, doc, old, repl string) string {
	t.Helper()
	if !strings.Contains(doc, old) {
		t.Fatalf("the document carries no %q:\n%s", old, doc)
	}
	return strings.Replace(doc, old, repl, 1)
}

// placeholderEntries reads the placeholders a daemon answer names. A
// placeholder entry is the one kind of document-level entry that carries a key
// path, so that is what selects it; its message must say the key is owed and
// its remedy must carry what the key takes.
func placeholderEntries(t *testing.T, diags []Diagnostic) []owedEntry {
	t.Helper()
	var out []owedEntry
	for _, d := range diags {
		if d.KeyPath == "" {
			continue
		}
		if !strings.Contains(d.Message, d.KeyPath+" is a placeholder still owed") {
			t.Errorf("the entry at %s does not say the key is a placeholder still owed: %q", d.KeyPath, d.Message)
		}
		takes, ok := strings.CutPrefix(d.Suggest, suggestLead)
		if !ok {
			t.Errorf("the entry at %s does not say what the key takes: suggest %q", d.KeyPath, d.Suggest)
		}
		out = append(out, owedEntry{keyPath: d.KeyPath, takes: takes})
	}
	return out
}

// commandPlaceholderLine is one line of the single-entity scope's refusal that
// names a placeholder: where it sits, its key path and what it takes.
var commandPlaceholderLine = regexp.MustCompile(`(?m)^\s+(\d+):\d+: (\S+) takes (.+)$`)

// commandPlaceholders validates path with the command's single-entity scope and
// reads the placeholders its refusal names, with the line each sits on. It
// returns the refusal as well, so a caller can read the identifiers it carries.
func commandPlaceholders(t *testing.T, root, path string) ([]owedEntry, []int, error) {
	t.Helper()
	_, err := validateops.ValidateEntity(context.Background(), root, t.TempDir(), nil, nil, path, false)
	if err == nil {
		return nil, nil, nil
	}
	var owed []owedEntry
	var lines []int
	for _, m := range commandPlaceholderLine.FindAllStringSubmatch(err.Error(), -1) {
		var line int
		if _, serr := fmt.Sscanf(m[1], "%d", &line); serr != nil {
			t.Fatalf("read the line of %q: %v", m[0], serr)
		}
		owed = append(owed, owedEntry{keyPath: m[2], takes: m[3]})
		lines = append(lines, line)
	}
	return owed, lines, err
}

// filledFor returns a value for a placeholder, read from what the command says
// it takes, so a document with one placeholder filled is filled with something
// the placeholder asked for wherever the answer names one.
func filledFor(takes string) string {
	switch {
	case strings.HasPrefix(takes, "a semantic version"):
		return "1.0.0"
	case strings.HasPrefix(takes, "one of: "):
		first, _, _ := strings.Cut(strings.TrimPrefix(takes, "one of: "), ", ")
		return first
	case strings.HasPrefix(takes, "exactly "):
		return strings.TrimPrefix(takes, "exactly ")
	default:
		return "filled"
	}
}

// fillPlaceholderLine fills the empty value on one line of a drafted document,
// failing when the line carries none.
func fillPlaceholderLine(t *testing.T, doc string, line int, value string) string {
	t.Helper()
	lines := strings.Split(doc, "\n")
	if line < 1 || line > len(lines) || !strings.Contains(lines[line-1], `""`) {
		t.Fatalf("line %d of the drafted document carries no empty value:\n%s", line, doc)
	}
	lines[line-1] = strings.Replace(lines[line-1], `""`, `"`+value+`"`, 1)
	return strings.Join(lines, "\n")
}

// TestTheDaemonNamesEveryPlaceholderADraftCarries drives the editor's loop:
// draft a document through the daemon, then validate the buffer.
//
// A drafted Component is told its version and its type label are owed, each
// with what it takes, where it used to be shown only the schema's pattern and
// enumeration findings for the two empty values. A drafted FeatureRecipe with
// only its version filled is told its capability and the Feature it implements
// are owed, although the schema admits both empty and so reports nothing — and
// it is told so whether or not the request names a Release, because this is a
// question the document answers by itself. Each entry carries the identifiers
// the command's refusal of the same bytes carries. Filled, neither is told of a
// placeholder.
//
// The type label is drawn at the label key itself, on the line the command
// names it at, although its key contains a literal dot that the field-path walk
// an envelope finding is positioned by does not address: a placeholder is drawn
// at the key the placeholder reading found.
func TestTheDaemonNamesEveryPlaceholderADraftCarries(t *testing.T) {
	t.Parallel()
	root := tier4Workspace(t, "parameter-type-mismatch", []string{"release.yaml", "manifest.yaml", "aggregate.yaml"})
	srv := newTestServer(t, root, nil, nil)

	component := draftedOverTheWire(t, srv, "component", "my-service")
	featureRecipe := draftedOverTheWire(t, srv, "featurerecipe", "my-feature-recipe")
	versioned := replaceOnce(t, featureRecipe, `  version: ""`, `  version: "1.0.0"`)

	type owedAt struct {
		keyPath, takesPrefix string
		line, column         int
	}
	schemaDescribes := "what its schema describes: "
	refs := []owedAt{
		{keyPath: "spec.capability", takesPrefix: schemaDescribes + "The explicit provider capability this recipe binds to", line: 7, column: 3},
		{keyPath: "spec.implements", takesPrefix: schemaDescribes + "Declares which catalog Feature type this recipe is able to implement", line: 8, column: 3},
	}
	cases := []struct {
		name, file, doc, release string
		want                     []owedAt
	}{
		{
			name: "a drafted Component", file: "component.yaml", doc: component,
			want: []owedAt{
				{keyPath: "metadata.version", takesPrefix: "a semantic version, such as 1.0.0", line: 5, column: 3},
				{keyPath: "metadata.labels.component.aggrega.ai/type", takesPrefix: "one of: server, worker, task, function, stateful-server, resource", line: 7, column: 5},
			},
		},
		{name: "a FeatureRecipe with only its version filled", file: "featurerecipe.yaml", doc: versioned, want: refs},
		{name: "the same FeatureRecipe with a Release named", file: "featurerecipe.yaml", doc: versioned, release: "release.yaml", want: refs},
	}
	for _, tc := range cases {
		path := filepath.Join(root, tc.file)
		if err := os.WriteFile(path, []byte(tc.doc), 0o644); err != nil {
			t.Fatalf("write %s: %v", tc.file, err)
		}
		_, _, refusal := commandPlaceholders(t, root, path)
		if refusal == nil {
			t.Fatalf("%s: the command accepted the document, so there is no refusal to compare with", tc.name)
		}
		wantCode, _ := cerrors.CodeOf(refusal)
		wantShort, _ := cerrors.ShortCodeOf(refusal)

		diags := validateContentOverTheWire(t, srv, path, tc.doc, tc.release).Diagnostics
		var owed []Diagnostic
		for _, d := range diags {
			if strings.Contains(d.Message, "does not match regular expression") || strings.Contains(d.Message, "does not equal any of") {
				t.Errorf("%s: the schema's rendering of an empty value still stands in for the placeholder: %q", tc.name, d.Message)
			}
			if d.KeyPath != "" {
				owed = append(owed, d)
			}
		}
		if len(owed) != len(tc.want) {
			t.Fatalf("%s: want %d placeholder entries, got %d:\n%+v", tc.name, len(tc.want), len(owed), diags)
		}
		for i, w := range tc.want {
			d := owed[i]
			if d.KeyPath != w.keyPath {
				t.Errorf("%s: entry %d names %q, want %q", tc.name, i, d.KeyPath, w.keyPath)
			}
			if !strings.Contains(d.Message, w.keyPath+" is a placeholder still owed") {
				t.Errorf("%s: entry %d does not say %s is a placeholder still owed: %q", tc.name, i, w.keyPath, d.Message)
			}
			if !strings.HasPrefix(d.Suggest, suggestLead+w.takesPrefix) {
				t.Errorf("%s: entry %d does not say what %s takes: suggest %q", tc.name, i, w.keyPath, d.Suggest)
			}
			if d.Line != w.line || d.Column != w.column {
				t.Errorf("%s: entry %d is drawn at %d:%d, want %d:%d", tc.name, i, d.Line, d.Column, w.line, w.column)
			}
			if d.Code != wantCode || d.ShortCode != wantShort {
				t.Errorf("%s: entry %d carries code %q / %q, and the command's refusal of the same bytes carries %q / %q",
					tc.name, i, d.Code, d.ShortCode, wantCode, wantShort)
			}
			// The message names the condition exactly when there is one to name.
			if opens := strings.HasPrefix(d.Message, envelope.ErrEntityEnvelope().Error()); opens != (wantCode != "") {
				t.Errorf("%s: entry %d opens with the envelope sentence = %v, and the refusal carries a condition = %v: %q",
					tc.name, i, opens, wantCode != "", d.Message)
			}
		}
	}

	filled := []struct{ name, file, doc string }{
		{name: "a Component with its placeholders filled", file: "component-filled.yaml", doc: replaceOnce(t,
			replaceOnce(t, component, `  version: ""`, `  version: "0.1.0"`),
			`    component.aggrega.ai/type: ""`, `    component.aggrega.ai/type: "server"`)},
		{name: "a FeatureRecipe with its placeholders filled", file: "featurerecipe-filled.yaml", doc: replaceOnce(t,
			replaceOnce(t, versioned, `  capability: ""`, `  capability: "helm"`),
			`  implements: ""`, `  implements: "example/observability"`)},
	}
	for _, tc := range filled {
		diags := validateContentOverTheWire(t, srv, filepath.Join(root, tc.file), tc.doc, "").Diagnostics
		for _, d := range diags {
			if d.KeyPath != "" || strings.Contains(d.Message, "placeholder") {
				t.Errorf("%s: still told of a placeholder: %+v", tc.name, d)
			}
		}
	}
}

// TestAPlaceholdersColumnIsCountedInTheSessionsEncoding puts a character ahead
// of a placeholder's key on the same line and reads the key's column in a
// session counting UTF-16 units, one counting UTF-8 bytes and one counting code
// points. The key is the 55th character of the line when the character before
// it is one code point; the rows pin where each session counts it:
//
//   - é is one UTF-16 unit and two UTF-8 bytes, so only the UTF-8 column moves;
//   - 🚀 lies outside the Basic Multilingual Plane, so it is two UTF-16 units
//     and four UTF-8 bytes, and the three sessions count three columns;
//   - an ASCII line is counted alike by every session.
//
// Each span is also read back as text in its session's units, so a column one
// unit early reads as a span starting on the space before the key.
func TestAPlaceholdersColumnIsCountedInTheSessionsEncoding(t *testing.T) {
	t.Parallel()
	document := func(description string) string {
		return "apiVersion: aggrega.ai/v1alpha1\n" +
			"kind: Component\n" +
			"metadata: {name: my-service, description: \"" + description + "\", version: \"\", labels: {component.aggrega.ai/type: server}}\n" +
			"spec: {}\n"
	}
	for _, tc := range []struct {
		name, description, encoding string
		column, endColumn           int
	}{
		{name: "a two-byte character", description: "Café API", encoding: "utf-16", column: 55, endColumn: 62},
		{name: "a two-byte character", description: "Café API", encoding: "utf-8", column: 56, endColumn: 63},
		{name: "a two-byte character", description: "Café API", encoding: "utf-32", column: 55, endColumn: 62},
		{name: "a four-byte character", description: "🚀 API", encoding: "utf-16", column: 53, endColumn: 60},
		{name: "a four-byte character", description: "🚀 API", encoding: "utf-8", column: 55, endColumn: 62},
		{name: "a four-byte character", description: "🚀 API", encoding: "utf-32", column: 52, endColumn: 59},
		{name: "an ASCII line", description: "Cafe API", encoding: "utf-16", column: 55, endColumn: 62},
		{name: "an ASCII line", description: "Cafe API", encoding: "utf-8", column: 55, endColumn: 62},
		{name: "an ASCII line", description: "Cafe API", encoding: "utf-32", column: 55, endColumn: 62},
	} {
		doc := document(tc.description)
		root := t.TempDir()
		srv := newTestServer(t, root, nil, nil)
		initializeWithEncoding(t, srv, tc.encoding)
		diags := validateContentOverTheWire(t, srv, filepath.Join(root, "component.yaml"), doc, "").Diagnostics
		var found bool
		for _, d := range diags {
			if d.KeyPath != "metadata.version" {
				continue
			}
			found = true
			if d.Line != 3 || d.Column != tc.column || d.EndLine != 3 || d.EndColumn != tc.endColumn {
				t.Errorf("%s, %s: the placeholder is drawn at %d:%d-%d:%d, want 3:%d-3:%d",
					tc.name, tc.encoding, d.Line, d.Column, d.EndLine, d.EndColumn, tc.column, tc.endColumn)
				continue
			}
			if got := spanText(t, doc, d, tc.encoding); got != "version" {
				t.Errorf("%s, %s: the placeholder's span covers %q, not the key", tc.name, tc.encoding, got)
			}
		}
		if !found {
			t.Errorf("%s, %s: no placeholder entry for metadata.version: %+v", tc.name, tc.encoding, diags)
		}
	}
}

// TestADottedPlaceholderKeyIsDrawnOnTheKeyItself draws a drafted Component's
// type label — a key whose own name contains a dot — in a session counting
// UTF-16 units and in one counting UTF-8 bytes, and reads the text each span
// covers in that session's units. In both, the placeholder covers the label key
// itself, on the line the command names it at. An envelope finding on the same
// dotted key that is not a placeholder is still drawn where the field-path walk
// draws it, at the enclosing key: only a placeholder is drawn at the key the
// placeholder reading found.
//
// The drafted label line carries no multi-byte character, so the two sessions
// count the same columns on it. A second document puts café before the label
// key on its line: é is one UTF-16 unit and two UTF-8 bytes, so the UTF-8
// session counts the key one column further along.
func TestADottedPlaceholderKeyIsDrawnOnTheKeyItself(t *testing.T) {
	t.Parallel()
	const typeLabel = "metadata.labels.component.aggrega.ai/type"
	const afterCafe = "apiVersion: aggrega.ai/v1alpha1\n" +
		"kind: Component\n" +
		"metadata:\n" +
		"  name: my-service\n" +
		"  version: \"1.0.0\"\n" +
		"  labels: {team: \"café\", component.aggrega.ai/type: \"\"}\n" +
		"spec: {}\n"
	afterCafeColumns := map[string][2]int{"utf-16": {26, 51}, "utf-8": {27, 52}}
	const notAPlaceholder = "apiVersion: aggrega.ai/v1alpha1\n" +
		"kind: Component\n" +
		"metadata:\n" +
		"  name: my-service\n" +
		"  version: \"1.0.0\"\n" +
		"  labels:\n" +
		"    component.aggrega.ai/type: bogus\n" +
		"spec: {}\n"
	for _, encoding := range []string{"utf-16", "utf-8"} {
		root := t.TempDir()
		srv := newTestServer(t, root, nil, nil)
		initializeWithEncoding(t, srv, encoding)
		drafted := draftedOverTheWire(t, srv, "component", "my-service")

		var label Diagnostic
		var found bool
		for _, d := range validateContentOverTheWire(t, srv, filepath.Join(root, "component.yaml"), drafted, "").Diagnostics {
			if d.KeyPath == typeLabel {
				label, found = d, true
			}
		}
		if !found {
			t.Fatalf("%s: no placeholder entry for %s", encoding, typeLabel)
		}
		if label.Line != 7 || label.Column != 5 || label.EndLine != 7 || label.EndColumn != 30 {
			t.Errorf("%s: the type label is drawn at %d:%d-%d:%d, want 7:5-7:30",
				encoding, label.Line, label.Column, label.EndLine, label.EndColumn)
		}
		if got := spanText(t, drafted, label, encoding); got != "component.aggrega.ai/type" {
			t.Errorf("%s: the type label's span covers %q, not the label key", encoding, got)
		}

		found = false
		want := afterCafeColumns[encoding]
		for _, d := range validateContentOverTheWire(t, srv, filepath.Join(root, "after-cafe.yaml"), afterCafe, "").Diagnostics {
			if d.KeyPath != typeLabel {
				continue
			}
			found = true
			if d.Line != 6 || d.Column != want[0] || d.EndLine != 6 || d.EndColumn != want[1] {
				t.Errorf("%s: after café, the type label is drawn at %d:%d-%d:%d, want 6:%d-6:%d",
					encoding, d.Line, d.Column, d.EndLine, d.EndColumn, want[0], want[1])
				continue
			}
			if got := spanText(t, afterCafe, d, encoding); got != "component.aggrega.ai/type" {
				t.Errorf("%s: after café, the type label's span covers %q, not the label key", encoding, got)
			}
		}
		if !found {
			t.Errorf("%s: no placeholder entry for %s after café", encoding, typeLabel)
		}

		diags := validateContentOverTheWire(t, srv, filepath.Join(root, "bogus.yaml"), notAPlaceholder, "").Diagnostics
		if len(diags) != 1 {
			t.Fatalf("%s: want the one enumeration finding on the type label, got %+v", encoding, diags)
		}
		finding := diags[0]
		if finding.KeyPath != "" || !strings.Contains(finding.Message, "does not equal any of") {
			t.Errorf("%s: want the schema's enumeration finding, got %+v", encoding, finding)
		}
		if finding.Line != 6 || finding.Column != 3 || finding.EndLine != 6 || finding.EndColumn != 9 {
			t.Errorf("%s: the enumeration finding is drawn at %d:%d-%d:%d, want 6:3-6:9",
				encoding, finding.Line, finding.Column, finding.EndLine, finding.EndColumn)
		}
		if got := spanText(t, notAPlaceholder, finding, encoding); got != "labels" {
			t.Errorf("%s: the enumeration finding's span covers %q, not the enclosing key", encoding, got)
		}
	}
}

// spanText returns the text of a document line a diagnostic's span covers,
// counting its 1-based columns in the session's encoding, so an assertion reads
// which key a span lands on rather than only a number. It counts with the
// standard library alone and shares nothing with the index the daemon draws
// through.
func spanText(t *testing.T, doc string, d Diagnostic, encoding string) string {
	t.Helper()
	lines := strings.Split(doc, "\n")
	if d.Line < 1 || d.Line > len(lines) || d.EndLine != d.Line {
		t.Fatalf("the span %d:%d-%d:%d does not sit on one line of the document", d.Line, d.Column, d.EndLine, d.EndColumn)
	}
	line := lines[d.Line-1]
	start := byteOffsetOfUnit(t, line, d.Column-1, encoding)
	end := byteOffsetOfUnit(t, line, d.EndColumn-1, encoding)
	if end < start {
		t.Fatalf("the span %d:%d-%d:%d ends before it starts", d.Line, d.Column, d.EndLine, d.EndColumn)
	}
	return line[start:end]
}

// byteOffsetOfUnit converts a 0-based column, counted in encoding's units, to a
// byte offset into line, failing when the column splits a character or lies
// past the line's end.
func byteOffsetOfUnit(t *testing.T, line string, units int, encoding string) int {
	t.Helper()
	counted := 0
	for i, r := range line {
		if counted == units {
			return i
		}
		switch encoding {
		case "utf-8":
			counted += utf8.RuneLen(r)
		case "utf-16":
			counted += utf16.RuneLen(r)
		case "utf-32":
			counted++
		default:
			t.Fatalf("no unit count for the encoding %q", encoding)
		}
		if counted > units {
			t.Fatalf("column %d splits a character of %q", units+1, line)
		}
	}
	if counted != units {
		t.Fatalf("column %d lies past the end of %q", units+1, line)
	}
	return len(line)
}

// TestTheDaemonAndTheCommandNameTheSamePlaceholders holds the two surfaces to
// one reading. The drafted document of every kind the single-entity validation
// accepts goes through the command's single-entity scope and through the
// daemon, and both must name the same keys, in the same order, with the same
// "what it takes" text — as drafted, and again with each placeholder filled in
// turn.
//
// The kinds are read from the list that validation reads, so a kind added there
// is compared here without an edit. The command's side is read from the words
// its refusal prints, which is what an author reads, rather than from the value
// the daemon is handed, so the comparison does not share an implementation with
// either side.
func TestTheDaemonAndTheCommandNameTheSamePlaceholders(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	srv := newTestServer(t, root, nil, nil)
	compared := 0
	for _, kind := range validateops.SingleEntityKinds() {
		segment := entitykind.Segment(entitykind.Kind(kind))
		drafted := draftedOverTheWire(t, srv, segment, "my-"+segment)

		draftedPath := filepath.Join(root, segment+"-drafted.yaml")
		if err := os.WriteFile(draftedPath, []byte(drafted), 0o644); err != nil {
			t.Fatalf("write the drafted %s: %v", kind, err)
		}
		owedAsDrafted, lines, _ := commandPlaceholders(t, root, draftedPath)
		documents := []struct{ name, doc string }{{name: "as drafted", doc: drafted}}
		for i, p := range owedAsDrafted {
			documents = append(documents, struct{ name, doc string }{
				name: "with " + p.keyPath + " filled",
				doc:  fillPlaceholderLine(t, drafted, lines[i], filledFor(p.takes)),
			})
		}
		for j, d := range documents {
			path := filepath.Join(root, fmt.Sprintf("%s-%d.yaml", segment, j))
			if err := os.WriteFile(path, []byte(d.doc), 0o644); err != nil {
				t.Fatalf("write %s: %v", path, err)
			}
			want, _, _ := commandPlaceholders(t, root, path)
			got := placeholderEntries(t, validateContentOverTheWire(t, srv, path, d.doc, "").Diagnostics)
			if !slices.Equal(got, want) {
				t.Errorf("%s %s: the daemon names %v and the command names %v", kind, d.name, got, want)
			}
			compared += len(want)
		}
	}
	if compared == 0 {
		t.Fatal("no drafted document named a placeholder on the command's side, so the comparison proved nothing")
	}
}
