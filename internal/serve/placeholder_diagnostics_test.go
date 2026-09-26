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
	_, err := validateops.ValidateEntity(context.Background(), root, t.TempDir(), nil, path, false)
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
// The type label is drawn at `labels`, the key that encloses it: its key
// contains a literal dot, which the field-path walk every envelope finding is
// positioned by does not address, so it is drawn where the envelope's own
// finding for the same empty value is drawn.
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
				{keyPath: "metadata.labels.component.aggrega.ai/type", takesPrefix: "one of: server, worker, task, function, stateful-server, resource", line: 6, column: 3},
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

// TestAPlaceholdersColumnIsCountedInTheSessionsEncoding puts a multi-byte
// character ahead of a placeholder's key on the same line and reads the key's
// column in a session counting UTF-16 units and in one counting UTF-8 bytes.
// The character is one unit in the first and two bytes in the second, so the
// columns differ by one: the placeholder is positioned in the client's units,
// never in the byte columns the command's reading prints for a terminal.
func TestAPlaceholdersColumnIsCountedInTheSessionsEncoding(t *testing.T) {
	t.Parallel()
	const doc = "apiVersion: aggrega.ai/v1alpha1\n" +
		"kind: Component\n" +
		"metadata: {name: my-service, description: \"Café API\", version: \"\", labels: {component.aggrega.ai/type: server}}\n" +
		"spec: {}\n"
	for _, tc := range []struct {
		encoding          string
		column, endColumn int
	}{
		{encoding: "utf-16", column: 54, endColumn: 61},
		{encoding: "utf-8", column: 55, endColumn: 62},
	} {
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
				t.Errorf("%s: the placeholder is drawn at %d:%d-%d:%d, want 3:%d-3:%d",
					tc.encoding, d.Line, d.Column, d.EndLine, d.EndColumn, tc.column, tc.endColumn)
			}
		}
		if !found {
			t.Errorf("%s: no placeholder entry for metadata.version: %+v", tc.encoding, diags)
		}
	}
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
