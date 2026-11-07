// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/serve"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// These tests hold the assistant-facing tools to the daemon's terms for one
// connection a graph node draws: the same coordinate, the same slots and
// reasons, the same containers and refusals. The workspace is the daemon's own
// connection fixture, copied, so both surfaces are measured over one set of
// documents.

// connectionSiteFixtureDir is the daemon's connection fixture, read from its own
// package so the two surfaces cannot come to be tested over different documents.
var connectionSiteFixtureDir = filepath.Join("..", "serve", "testdata", "connection-site")

// connectionSiteWorkspace copies the fixture into a fresh workspace and records
// the lockfile a pull would. The cases write into the copy, never the fixture.
func connectionSiteWorkspace(t *testing.T) (root, release string) {
	t.Helper()
	root = t.TempDir()
	copyFixtureTree(t, connectionSiteFixtureDir, root)

	release = filepath.Join(root, "release.yaml")
	var entities []lockfile.Entity
	for _, name := range []string{"gateway", "model", "sink"} {
		b, err := os.ReadFile(filepath.Join(root, "catalog", "components", name+".yaml"))
		if err != nil {
			t.Fatalf("read the component fixture %s: %v", name, err)
		}
		entities = append(entities, lockfile.Entity{Kind: "component", URI: "conn/" + name, ResolvedVersion: "1.0.0", Digest: digest.ContentDigest(b)})
	}
	lf := &lockfile.Lockfile{Version: 1, Release: "./release.yaml", GeneratedAt: "2026-06-03T10:00:00Z", Entities: entities}
	if err := lockfile.WriteLockfile(lockfile.PathFor(release), lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}
	return root, release
}

// copyFixtureTree copies a fixture tree into dst file by file, replacing any file
// dst already holds at the same path, so a tree laid over a copied workspace
// changes exactly the files it carries.
func copyFixtureTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatalf("copy the fixture %s: %v", src, err)
	}
}

// connectionArgs is a graph-node call on the fixture's gateway naming one
// connection.
func connectionArgs(root, release string, connection map[string]any) map[string]any {
	return map[string]any{
		"workspace_root": root,
		"graph_node":     "gateway",
		"release":        release,
		"connection":     connection,
	}
}

// candidateNameList projects candidates to their names; names are asserted
// rather than counted, since a count agrees while naming the wrong Features.
func candidateNameList(cands []serve.AttachCandidate) []string {
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.Name)
	}
	return out
}

// TestTheAttachCandidatesToolAnswersForAConnection drives the registered tool
// once per kind of connection and pins its slots, or its one reason, exactly as
// the daemon answers them — and that a reason is never sent where a slot exists.
func TestTheAttachCandidatesToolAnswersForAConnection(t *testing.T) {
	root, release := connectionSiteWorkspace(t)
	s := &server{out: io.Discard, logw: io.Discard, serverVersion: "test", launchRoot: root}
	all := []string{"feature.conn/audit", "feature.conn/peering", "feature.conn/token"}

	for _, tc := range []struct {
		name       string
		connection map[string]any
		wantSlots  map[string][]string
		wantReason serve.NoSlotReason
	}{
		{
			name:       "a socket declaring two slots",
			connection: map[string]any{"socket": "models", "alias": "gpt"},
			wantSlots:  map[string][]string{"credential": {"feature.conn/token"}, "trace": all},
		},
		{
			name:       "a connection a Feature drew",
			connection: map[string]any{"socket": "peer", "alias": "p1"},
			wantReason: serve.NoSlotReasonFeatureCreatedConnection,
		},
		{
			name:       "a socket the Component does not declare",
			connection: map[string]any{"socket": "extra", "alias": "side"},
			wantReason: serve.NoSlotReasonAdHocSocket,
		},
		{
			name:       "a declared socket declaring no slot",
			connection: map[string]any{"socket": "logs", "alias": "out"},
			wantReason: serve.NoSlotReasonNoSlotDeclared,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := callDiscoveryTool(t, s, "attach-candidates", connectionArgs(root, release, tc.connection))
			var got attachCandidatesPayload
			decodeStructured(t, res, &got)

			if names := candidateNameList(got.Candidates); strings.Join(names, ",") != strings.Join(all, ",") {
				t.Errorf("candidates = %v, want the node's own %v", names, all)
			}
			if got.Slots == nil {
				t.Fatalf("the answer for a connection carries no slot list: %s", res.StructuredContent)
			}
			if tc.wantReason == "" && bytes.Contains(res.StructuredContent, []byte(`"noSlotReason"`)) {
				t.Errorf("a reason is sent where the socket declares slots: %s", res.StructuredContent)
			}
			if got.NoSlotReason != tc.wantReason {
				t.Errorf("noSlotReason = %q, want %q", got.NoSlotReason, tc.wantReason)
			}
			if len(got.Slots) != len(tc.wantSlots) {
				t.Fatalf("%d slot(s), want %d: %s", len(got.Slots), len(tc.wantSlots), res.StructuredContent)
			}
			for _, slot := range got.Slots {
				want, ok := tc.wantSlots[slot.Slot]
				if !ok {
					t.Errorf("an undeclared slot %q was answered", slot.Slot)
					continue
				}
				if names := candidateNameList(slot.Candidates); strings.Join(names, ",") != strings.Join(want, ",") {
					t.Errorf("slot %s candidates = %v, want %v", slot.Slot, names, want)
				}
			}
			// The reason reaches the readable rendering too, which is what an
			// assistant reads when it does not parse the structured answer.
			if tc.wantReason != "" && !strings.Contains(res.Content[0].Text, tc.wantReason.Sentence()) {
				t.Errorf("the readable answer does not say why no slot may be filled:\n%s", res.Content[0].Text)
			}
		})
	}
}

// TestTheConnectionToolsRefuseWhatTheyCannotAnswer drives each refusal through
// the registered tools, with an answerable call of each tool first.
func TestTheConnectionToolsRefuseWhatTheyCannotAnswer(t *testing.T) {
	root, release := connectionSiteWorkspace(t)
	s := &server{out: io.Discard, logw: io.Discard, serverVersion: "test", launchRoot: root}
	gpt := map[string]any{"socket": "models", "alias": "gpt"}

	for _, tc := range []struct {
		name    string
		tool    string
		args    map[string]any
		refuses string
	}{
		{name: "candidates for a connection the graph carries", tool: "attach-candidates",
			args: connectionArgs(root, release, gpt)},
		{name: "a container in the Aggregate", tool: "attachment-container",
			args: connectionArgs(root, release, map[string]any{"socket": "models", "alias": "gpt", "document": "aggregate"})},
		{name: "a connection beside a workspace entity", tool: "attach-candidates",
			args:    map[string]any{"workspace_root": root, "node": "conn/gateway", "connection": gpt},
			refuses: "'connection' names a connection a graph node draws"},
		{name: "a connection the node does not draw", tool: "attach-candidates",
			args:    connectionArgs(root, release, map[string]any{"socket": "models", "alias": "nonesuch"}),
			refuses: serve.ConnectionRefusedUnknownConnection.Reason()},
		{name: "a container naming no document", tool: "attachment-container",
			args:    connectionArgs(root, release, gpt),
			refuses: "'connection.document' is required here"},
		{name: "a Release-drawn connection in the Aggregate", tool: "attachment-container",
			args:    connectionArgs(root, release, map[string]any{"socket": "logs", "alias": "out", "document": "aggregate"}),
			refuses: serve.ConnectionRefusedReleaseDrawn.Reason()},
		{name: "a Feature-drawn connection", tool: "attachment-container",
			args:    connectionArgs(root, release, map[string]any{"socket": "peer", "alias": "p1", "document": "release"}),
			refuses: serve.ConnectionRefusedFeatureCreated.Reason()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := callDiscoveryTool(t, s, tc.tool, tc.args)
			switch {
			case tc.refuses == "" && res.IsError:
				t.Fatalf("an answerable call was refused: %s", res.Content[0].Text)
			case tc.refuses == "":
				return
			case !res.IsError:
				t.Fatalf("a call that should have been refused was answered: %s", res.Content[0].Text)
			case !strings.Contains(res.Content[0].Text, tc.refuses):
				t.Fatalf("the refusal does not say what was wrong: want %q in %q", tc.refuses, res.Content[0].Text)
			}
		})
	}
}

// TestTheAttachmentContainerToolNamesAConnectionsEntryInEachDocument pins the
// container in each document and applies an attachment at the insertion point
// the second tool reports for it, reading the document back through the
// compiler's own decode. The Release case writes keys the document did not carry.
func TestTheAttachmentContainerToolNamesAConnectionsEntryInEachDocument(t *testing.T) {
	for _, tc := range []struct {
		name     string
		document string
		wantFile string
		wantPath []string
	}{
		{name: "in the Aggregate", document: "aggregate", wantFile: "aggregate.yaml",
			wantPath: []string{"spec", "components", "gateway", "uses", "models", "gpt", "features"}},
		{name: "in the Release", document: "release", wantFile: "release.yaml",
			wantPath: []string{"spec", "boundaries", "cluster", "components", "gateway", "uses", "models", "gpt", "features"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, release := connectionSiteWorkspace(t)
			s := &server{out: io.Discard, logw: io.Discard, serverVersion: "test", launchRoot: root}

			var where attachmentContainerPayload
			decodeStructured(t, callDiscoveryTool(t, s, "attachment-container", connectionArgs(root, release,
				map[string]any{"socket": "models", "alias": "gpt", "document": tc.document})), &where)
			if where.File != tc.wantFile {
				t.Errorf("file = %q, want %q", where.File, tc.wantFile)
			}
			if strings.Join(where.Container, "\x00") != strings.Join(tc.wantPath, "\x00") {
				t.Errorf("container = %v, want %v", where.Container, tc.wantPath)
			}

			position := insertionPayloadFrom(t, s, insertionPointArgs{
				File: where.File, Container: where.Container, WorkspaceRoot: root,
			})
			abs := filepath.Join(root, where.File)
			before, err := os.ReadFile(abs)
			if err != nil {
				t.Fatalf("read the document: %v", err)
			}
			edited := applyInsertionPayload(t, string(before), position, []string{"trace:", "  type: feature.conn/audit"})

			if tc.document == "aggregate" {
				var agg types.Aggregate
				if err := yaml.Unmarshal([]byte(edited), &agg); err != nil {
					t.Fatalf("the edited Aggregate no longer decodes: %v\n---\n%s---", err, edited)
				}
				gateway, _ := agg.Spec.Components.Get("gateway")
				socket, _ := gateway.Uses.Get("models")
				entry, _ := socket.Verbose.Get("gpt")
				if _, ok := entry.Features.Get("trace"); !ok {
					t.Fatalf("the attachment is not on the connection's entry:\n---\n%s---", edited)
				}
				return
			}
			if len(position.MissingContainerKeys) == 0 {
				t.Error("the Release already carried the per-edge entry, so this case exercises no creation")
			}
			var rel types.Release
			if err := yaml.Unmarshal([]byte(edited), &rel); err != nil {
				t.Fatalf("the edited Release no longer decodes: %v\n---\n%s---", err, edited)
			}
			for _, e := range resolver.ReleaseEdgeEntries(&rel) {
				if e.NodeID == "gateway" && e.Socket == "models" && e.Alias == "gpt" {
					if _, ok := e.Entry.Features.Get("trace"); ok {
						return
					}
				}
			}
			t.Fatalf("no per-edge entry for the connection carries the attachment:\n---\n%s---", edited)
		})
	}
}

// TestTheDescribeToolShowsEachSchemaAsItsAuthorWroteIt pins the definition a
// Component is described with: a schema the author wrote arrives as that schema,
// keys in the order written, and a schema the Component does not declare is
// absent — never the parser's node structure, whose field names no author wrote.
// Each socket's slot declarations arrive beside it.
func TestTheDescribeToolShowsEachSchemaAsItsAuthorWroteIt(t *testing.T) {
	root, _ := connectionSiteWorkspace(t)
	s := &server{out: io.Discard, logw: io.Discard, serverVersion: "test", launchRoot: root}

	var got describeEntityPayload
	decodeStructured(t, callDiscoveryTool(t, s, "describe", map[string]any{
		"workspace_root": root, "name": "conn/gateway",
	}), &got)

	for _, leak := range []string{`"Kind"`, `"Line"`, `"Column"`, `"HeadComment"`, `"valueSchema"`, `"infraSchema"`} {
		if bytes.Contains(got.Definition, []byte(leak)) {
			t.Errorf("the definition carries %s, which is the parser's structure or a schema the Component does "+
				"not declare: %s", leak, got.Definition)
		}
	}

	var definition struct {
		Spec struct {
			Schema     json.RawMessage `json:"schema"`
			UsesSchema map[string]struct {
				FeaturesSchema map[string]json.RawMessage `json:"featuresSchema"`
			} `json:"usesSchema"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(got.Definition, &definition); err != nil {
		t.Fatalf("the definition is not an object: %v (%s)", err, got.Definition)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, definition.Spec.Schema); err != nil {
		t.Fatalf("compact the schema: %v", err)
	}
	if want := `{"provides":{"host":{"type":"string","required":true}}}`; compact.String() != want {
		t.Errorf("spec.schema = %s, want the schema as authored, keys in authored order: %s", compact.String(), want)
	}
	if _, ok := definition.Spec.UsesSchema["models"].FeaturesSchema["credential"]; !ok {
		t.Errorf("the models socket's slot declarations are not in the definition: %s", got.Definition)
	}
}
