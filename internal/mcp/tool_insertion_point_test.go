// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/serve"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

const insertionAbsentBlockDoc = "kind: Aggregate\n" +
	"metadata:\n" +
	"  name: web-app\n" +
	"spec:\n" +
	"  components:\n" +
	"    web:\n" +
	"      type: core/service\n"

const insertionExistingBlockDoc = "kind: Aggregate\n" +
	"metadata:\n" +
	"  name: web-app\n" +
	"spec:\n" +
	"  components:\n" +
	"    web:\n" +
	"      type: core/service\n" +
	"      features:\n" +
	"        metrics:\n" +
	"          type: core/metrics\n"

// insertionWorkspace writes a document into a fresh workspace root and returns
// the root, so each case addresses a real file through the real resolution.
func insertionWorkspace(t *testing.T, rel, content string) string {
	t.Helper()
	root := t.TempDir()
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("create the fixture directory: %v", err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write the fixture: %v", err)
	}
	return root
}

// insertionPayloadFrom invokes the tool through the real tools/call path and
// decodes its structured payload, failing on a refusal.
func insertionPayloadFrom(t *testing.T, s *server, args insertionPointArgs) insertionPointPayload {
	t.Helper()
	res := callTool(t, s, "insertion-point", args)
	if res.IsError {
		t.Fatalf("insertion-point reported an error: %s", res.Content[0].Text)
	}
	var payload insertionPointPayload
	if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
		t.Fatalf("decode the payload: %v\n%s", err, res.StructuredContent)
	}
	return payload
}

// applyInsertionPayload composes the text the answer calls for and splices it in
// at the reported position, the way a client holding the buffer would.
func applyInsertionPayload(t *testing.T, src string, p insertionPointPayload, entry []string) string {
	t.Helper()

	var b strings.Builder
	if p.PrecededByLineBreak {
		b.WriteString("\n")
	}
	outermost := p.EntryIndent - len(p.MissingContainerKeys)*p.IndentStep
	for i, key := range p.MissingContainerKeys {
		b.WriteString(strings.Repeat(" ", outermost+i*p.IndentStep))
		b.WriteString(key)
		b.WriteString(":\n")
	}
	for i, line := range entry {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(strings.Repeat(" ", p.EntryIndent))
		b.WriteString(line)
	}

	idx := yamlpos.BuildLineIndexWithEncoding([]byte(src), yamlpos.PositionEncoding(p.PositionEncoding))
	byteInLine, err := idx.LSPCharToByte(p.Position.Line, p.Position.Character)
	if err != nil {
		t.Fatalf("convert the reported position: %v", err)
	}
	offset := 0
	for i := 0; i < p.Position.Line; i++ {
		next := strings.IndexByte(src[offset:], '\n')
		if next < 0 {
			offset = len(src)
			break
		}
		offset += next + 1
	}
	at := offset + byteInLine
	if at > len(src) {
		t.Fatalf("the reported position resolves to byte %d in a document of %d bytes", at, len(src))
	}
	return src[:at] + b.String() + src[at:]
}

// scalarAtInDocument walks a parsed document to the scalar at a structural path.
func scalarAtInDocument(t *testing.T, content string, path []string) (string, bool) {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		t.Fatalf("the document does not parse: %v\n---\n%s---", err, content)
	}
	n := &doc
	if len(n.Content) > 0 {
		n = n.Content[0]
	}
	for _, seg := range path {
		if n == nil || n.Kind != yaml.MappingNode {
			return "", false
		}
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == seg {
				next = n.Content[i+1]
				break
			}
		}
		if next == nil {
			return "", false
		}
		n = next
	}
	if n == nil || n.Kind != yaml.ScalarNode {
		return "", false
	}
	return n.Value, true
}

// TestTheInsertionPointToolLocatesWhereAnAttachmentBelongs drives the tool over
// both cases the naive answer gets wrong, applies each answer and re-loads the
// result.
func TestTheInsertionPointToolLocatesWhereAnAttachmentBelongs(t *testing.T) {
	for _, tc := range []struct {
		name        string
		doc         string
		wantMissing []string
		wantShape   yamlpos.EntryShape
	}{
		{
			name:        "the features block is absent and has to be created",
			doc:         insertionAbsentBlockDoc,
			wantMissing: []string{"features"},
			wantShape:   yamlpos.EntryShapeUnknown,
		},
		{
			name:        "the features block is there and has to be extended",
			doc:         insertionExistingBlockDoc,
			wantMissing: nil,
			wantShape:   yamlpos.EntryShapeMapping,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := insertionWorkspace(t, "aggregates/web.yaml", tc.doc)
			s := newTestServer(t)

			payload := insertionPayloadFrom(t, s, insertionPointArgs{
				File:          "aggregates/web.yaml",
				Container:     []string{"spec", "components", "web", "features"},
				WorkspaceRoot: root,
			})

			if payload.File != "aggregates/web.yaml" {
				t.Errorf("file = %q, want the workspace-relative path", payload.File)
			}
			if strings.Join(payload.MissingContainerKeys, ".") != strings.Join(tc.wantMissing, ".") {
				t.Errorf("missing container keys = %v, want %v", payload.MissingContainerKeys, tc.wantMissing)
			}
			if payload.EntryShape != tc.wantShape {
				t.Errorf("entry shape = %q, want %q", payload.EntryShape, tc.wantShape)
			}

			edited := applyInsertionPayload(t, tc.doc, payload, []string{"logging:", "  type: core/logging"})
			got, ok := scalarAtInDocument(t, edited,
				[]string{"spec", "components", "web", "features", "logging", "type"})
			if !ok || got != "core/logging" {
				t.Fatalf("the attachment is not where it belongs after the edit (%q, found %v)\n---\n%s---",
					got, ok, edited)
			}
			if kept, ok := scalarAtInDocument(t, edited,
				[]string{"spec", "components", "web", "type"}); !ok || kept != "core/service" {
				t.Fatalf("the edit disturbed what the author wrote\n---\n%s---", edited)
			}
		})
	}
}

// TestTheToolProtocolsAnswerIsTheSharedLayers is the tool protocol's half of the
// cross-surface proof: the payload the tool returns carries exactly what the
// shared entry point produced, which is the same entry point the daemon's own
// half of this proof compares against.
//
// Comparing against the shared layer rather than against a literal is what makes
// the two halves a parity proof: a literal would be a third copy, free to agree
// with neither surface.
func TestTheToolProtocolsAnswerIsTheSharedLayers(t *testing.T) {
	root := insertionWorkspace(t, "aggregates/web.yaml", insertionAbsentBlockDoc)
	s := newTestServer(t)
	container := []string{"spec", "components", "web", "features"}

	payload := insertionPayloadFrom(t, s, insertionPointArgs{
		File:          "aggregates/web.yaml",
		Container:     container,
		WorkspaceRoot: root,
	})
	shared, err := serve.InsertionPoint(root, "aggregates/web.yaml", container, yamlpos.PositionEncodingUTF16)
	if err != nil {
		t.Fatalf("the shared entry point refused: %v", err)
	}

	toolJSON, err := json.Marshal(payload.InsertionPointResult)
	if err != nil {
		t.Fatalf("marshal the tool answer: %v", err)
	}
	sharedJSON, err := json.Marshal(shared)
	if err != nil {
		t.Fatalf("marshal the shared answer: %v", err)
	}
	if string(toolJSON) != string(sharedJSON) {
		t.Fatalf("the tool's answer and the shared layer's differ:\n  tool:   %s\n  shared: %s",
			toolJSON, sharedJSON)
	}
	if payload.WorkspaceRoot != root {
		t.Errorf("workspace_root = %q, want %q", payload.WorkspaceRoot, root)
	}
}

// TestTheInsertionPointToolReportsPositionsInTheUnitsItWasAsked pins the
// encoding argument: a caller measuring its buffer in bytes gets byte offsets,
// and one that says nothing gets what a session takes when a client negotiates
// nothing.
func TestTheInsertionPointToolReportsPositionsInTheUnitsItWasAsked(t *testing.T) {
	const doc = "kind: Aggregate\n" +
		"spec:\n" +
		"  components:\n" +
		"    web:\n" +
		"      type: core/sérvice\n"
	root := insertionWorkspace(t, "web.yaml", doc)
	s := newTestServer(t)
	container := []string{"spec", "components", "web", "features"}

	byDefault := insertionPayloadFrom(t, s, insertionPointArgs{
		File: "web.yaml", Container: container, WorkspaceRoot: root,
	})
	if byDefault.PositionEncoding != string(yamlpos.PositionEncodingUTF16) {
		t.Fatalf("the default encoding is %q, want utf-16", byDefault.PositionEncoding)
	}

	inBytes := insertionPayloadFrom(t, s, insertionPointArgs{
		File: "web.yaml", Container: container, WorkspaceRoot: root, PositionEncoding: "utf-8",
	})
	if inBytes.PositionEncoding != string(yamlpos.PositionEncodingUTF8) {
		t.Fatalf("the named encoding is %q, want utf-8", inBytes.PositionEncoding)
	}
	if inBytes.Position.Character == byDefault.Position.Character {
		t.Fatalf("both encodings report character %d over a line carrying a multi-byte character",
			inBytes.Position.Character)
	}

	// Each applies correctly in its own units, which is what makes the
	// difference a unit rather than a defect.
	for _, probe := range []insertionPointPayload{byDefault, inBytes} {
		edited := applyInsertionPayload(t, doc, probe, []string{"logging:", "  type: core/logging"})
		if got, ok := scalarAtInDocument(t, edited,
			[]string{"spec", "components", "web", "features", "logging", "type"}); !ok || got != "core/logging" {
			t.Fatalf("the %s answer does not apply\n---\n%s---", probe.PositionEncoding, edited)
		}
	}
}

// TestTheInsertionPointToolRefusesWhatItCannotLocate drives every refusal the
// tool carries, with an answerable call first as the positive control.
func TestTheInsertionPointToolRefusesWhatItCannotLocate(t *testing.T) {
	root := insertionWorkspace(t, "aggregates/web.yaml", insertionAbsentBlockDoc)
	s := newTestServer(t)

	for _, tc := range []struct {
		name    string
		args    insertionPointArgs
		refuses string
	}{
		{
			name: "a call the workspace can answer",
			args: insertionPointArgs{
				File:          "aggregates/web.yaml",
				Container:     []string{"spec", "components", "web", "features"},
				WorkspaceRoot: root,
			},
			refuses: "",
		},
		{
			name:    "no file",
			args:    insertionPointArgs{Container: []string{"spec"}, WorkspaceRoot: root},
			refuses: "requires a 'file'",
		},
		{
			name:    "no container",
			args:    insertionPointArgs{File: "aggregates/web.yaml", WorkspaceRoot: root},
			refuses: "requires a 'container'",
		},
		{
			name: "an encoding this surface does not report in",
			args: insertionPointArgs{
				File:             "aggregates/web.yaml",
				Container:        []string{"spec"},
				WorkspaceRoot:    root,
				PositionEncoding: "latin-1",
			},
			refuses: "does not report positions in",
		},
		{
			name: "a document outside the workspace",
			args: insertionPointArgs{
				File: "../elsewhere.yaml", Container: []string{"spec"}, WorkspaceRoot: root,
			},
			refuses: "not inside the workspace",
		},
		{
			name: "a container path running through a scalar",
			args: insertionPointArgs{
				File:          "aggregates/web.yaml",
				Container:     []string{"spec", "components", "web", "type", "features"},
				WorkspaceRoot: root,
			},
			refuses: "carries as a scalar",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := callTool(t, s, "insertion-point", tc.args)
			switch {
			case tc.refuses == "" && res.IsError:
				t.Fatalf("an answerable call was refused: %s", res.Content[0].Text)
			case tc.refuses == "":
				return
			case !res.IsError:
				t.Fatalf("a call that should have been refused was answered: %s", res.Content[0].Text)
			case !strings.Contains(res.Content[0].Text, tc.refuses):
				t.Fatalf("the refusal does not say what was wrong: want %q in %q",
					tc.refuses, res.Content[0].Text)
			}
		})
	}
}

// TestTheInsertionPointToolIsReachableFromTheCapabilityListing pins that the
// tool is discoverable and invocable under the name the operation table gives,
// rather than merely present in a registry.
//
// A tool a client cannot learn of is a tool nothing calls, and this one sits in
// the tail rather than in the set a caller meets first — so the capability tool
// has to index it under the name the wire uses.
func TestTheInsertionPointToolIsReachableFromTheCapabilityListing(t *testing.T) {
	s := newTestServer(t)

	listing := indexOf(t, s)
	var found *capabilityProjection
	for i := range listing.Index {
		if listing.Index[i].Name == "insertion-point" {
			found = &listing.Index[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("the capability index does not surface insertion-point; it surfaced %v",
			capabilityNames(listing))
	}
	if !found.Deferred {
		t.Errorf("insertion-point is surfaced as though it sat in the set a caller meets first")
	}

	// Discovery is only useful if the definition is one call away, so the
	// argument schema is asked for by name and has to arrive.
	defined := definitionsOf(t, s, "insertion-point")
	if len(defined.Capabilities) != 1 || len(defined.Capabilities[0].InputSchema) == 0 {
		t.Errorf("naming insertion-point returned %+v, not the argument schema a caller composes a call from",
			defined.Capabilities)
	}

	// Kind-agnostic: an author working on any entity kind is offered it.
	for _, kind := range []string{"aggregate", "component", "release"} {
		scoped := callTool(t, s, describeCapabilitiesName, describeCapabilitiesArgs{Kind: kind})
		var narrowed describeCapabilitiesResult
		if err := json.Unmarshal(scoped.StructuredContent, &narrowed); err != nil {
			t.Fatalf("decode the %s listing: %v", kind, err)
		}
		var present bool
		for _, c := range narrowed.Index {
			if c.Name == "insertion-point" {
				present = true
				break
			}
		}
		if !present {
			t.Errorf("the %s-scoped capability listing does not surface insertion-point", kind)
		}
	}
}
