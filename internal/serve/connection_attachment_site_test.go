// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// These tests cover the authoring helpers asked about one connection a graph
// node draws: which Features may fill each slot the connection's socket
// declares, and where an attachment on it is written in either document.
//
// The fixture is one workspace drawing every kind of connection those answers
// distinguish, from one component: a connection through a socket declaring two
// slots (one narrowed by a selector, one not), a connection a Feature drew, a
// connection through a socket the Component does not declare, and a connection
// the Release drew through a socket declaring no slot. Its Features are chosen
// so that each of the two rules a slot's filler is held to excludes a different
// one: `vault` satisfies the credential slot's selector and not the node's
// applicability, `audit` and `peering` the reverse.

// connectionSiteFixture is the committed workspace the cases below run over.
const connectionSiteFixture = "testdata/connection-site"

// connectionSiteWorkspace copies the fixture into a fresh workspace and records
// the lockfile a pull would, so a graph-node request runs past its lockfile
// precondition. The copy is what the cases write into: an applied edit must
// never touch the committed fixture.
func connectionSiteWorkspace(t *testing.T) (ws, release string) {
	t.Helper()
	ws = t.TempDir()
	copyConnectionSiteFixture(t, connectionSiteFixture, ws)

	release = "release.yaml"
	var entities []lockfile.Entity
	for _, name := range []string{"gateway", "model", "sink"} {
		b, err := os.ReadFile(filepath.Join(ws, "catalog", "components", name+".yaml"))
		if err != nil {
			t.Fatalf("read the component fixture %s: %v", name, err)
		}
		entities = append(entities, lockfile.Entity{
			Kind: "component", URI: "conn/" + name, ResolvedVersion: "1.0.0", Digest: contentDigestHex(b),
		})
	}
	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     release,
		GeneratedAt: "2026-06-03T10:00:00Z",
		Entities:    entities,
	}
	if err := lockfile.WriteLockfile(lockfile.PathFor(filepath.Join(ws, release)), lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}
	return ws, release
}

// copyConnectionSiteFixture copies a fixture tree file by file.
func copyConnectionSiteFixture(t *testing.T, src, dst string) {
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

// connectionSiteServer builds a daemon over the copied workspace, with its own
// catalog directory on the search stack.
func connectionSiteServer(t *testing.T, ws string) *Server {
	t.Helper()
	return graphNodeServer(t, ws, filepath.Join(ws, "catalog"))
}

// decodeConnectionCandidacy decodes a successful answer into the exported
// result type, and returns the raw result bytes beside it for the key-presence
// assertions a typed decode cannot make.
func decodeConnectionCandidacy(t *testing.T, resp jsonRPCResponse) (AttachCandidatesResult, []byte) {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("attachCandidates errored: %+v", resp.Error)
	}
	var out AttachCandidatesResult
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatalf("unmarshal result: %v (body: %s)", err, resp.Result)
	}
	return out, resp.Result
}

// topLevelKeys reads the keys of a JSON object in the order the bytes carry
// them, through the streaming decoder, so an assertion about which keys are
// present — and in what order — is not collapsed by a map decode.
func topLevelKeys(t *testing.T, body []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(body))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("the answer is not a JSON object: %v (%s)", err, body)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("read a key: %v (%s)", err, body)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("skip a value: %v (%s)", err, body)
		}
	}
	return keys
}

// wantSlot is a slot exactly as the answer must carry it: the declaration's
// fields as the fixture's Component writes them, and the names of the Features
// that may fill it.
type wantSlot struct {
	slot        string
	selector    string
	required    bool
	inject      []string
	sensitive   bool
	description string
	candidates  []string
}

// nodeCandidates is the node half of every answer over this fixture's gateway:
// the Features whose applicability its labels satisfy. vault is excluded by its
// applicability.
var nodeCandidates = []string{"feature.conn/audit", "feature.conn/peering", "feature.conn/token"}

// TestAttachCandidatesAnswersForAConnection drives the registered wire method
// once per kind of connection and pins the whole connection half of the answer:
// each slot as declared with the Features that may fill it, or the one reason no
// slot may be filled, and the node half beside it unchanged.
//
// Each row carries exactly one of the two outcomes, and the key-presence check is
// part of every row: a reason sent beside slots, or slots left off beside a
// reason, is the shape a client would misread, so both directions are asserted
// on the bytes as well as on the decoded value.
func TestAttachCandidatesAnswersForAConnection(t *testing.T) {
	ws, release := connectionSiteWorkspace(t)

	credential := wantSlot{
		slot:        "credential",
		selector:    "feature.aggrega.ai/kind=secret",
		required:    true,
		inject:      []string{"env"},
		sensitive:   true,
		description: "The credential the gateway presents to the model a connection reaches.",
		// audit and peering carry no label the selector asks for; vault carries
		// it and does not apply to the gateway.
		candidates: []string{"feature.conn/token"},
	}
	trace := wantSlot{
		slot:        "trace",
		inject:      []string{},
		description: "Where the gateway reports the calls a connection carries.",
		candidates:  nodeCandidates,
	}

	for _, tc := range []struct {
		name       string
		connection ConnectionCoordinate
		wantSlots  []wantSlot
		wantReason NoSlotReason
	}{
		{
			name:       "a socket declaring two slots",
			connection: ConnectionCoordinate{Socket: "models", Alias: "gpt"},
			wantSlots:  []wantSlot{credential, trace},
		},
		{
			name:       "the same connection named in the Release's terms, which does not change the answer",
			connection: ConnectionCoordinate{Socket: "models", Alias: "gpt", Document: "release"},
			wantSlots:  []wantSlot{credential, trace},
		},
		{
			name:       "a connection a Feature drew",
			connection: ConnectionCoordinate{Socket: "peer", Alias: "p1"},
			wantReason: NoSlotReasonFeatureCreatedConnection,
		},
		{
			name:       "a socket the Component does not declare",
			connection: ConnectionCoordinate{Socket: "extra", Alias: "side"},
			wantReason: NoSlotReasonAdHocSocket,
		},
		{
			name:       "a declared socket declaring no slot, on a connection the Release drew",
			connection: ConnectionCoordinate{Socket: "logs", Alias: "out"},
			wantReason: NoSlotReasonNoSlotDeclared,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := connectionSiteServer(t, ws)
			connection := tc.connection
			got, body := decodeConnectionCandidacy(t, attachRequest(t, srv, attachCandidatesParams{
				GraphNode: "gateway", Release: release, Connection: &connection,
			}))

			if got.Node != "gateway" || got.LabelSource != NodeLabelsResolved {
				t.Errorf("node half = %q over %q labels, want the graph node's own answer", got.Node, got.LabelSource)
			}
			if names := refNames(got.Candidates); !equalStrings(names, nodeCandidates) {
				t.Errorf("candidates = %v, want %v — the Features that may attach to the node itself", names,
					nodeCandidates)
			}

			keys := strings.Join(topLevelKeys(t, body), ",")
			if !strings.Contains(keys, "slots") {
				t.Fatalf("the answer for a connection carries no slots key at all: %s", body)
			}
			if got.Slots == nil {
				t.Fatalf("slots decoded as null rather than a list: %s", body)
			}
			if tc.wantReason == "" {
				if strings.Contains(keys, "noSlotReason") {
					t.Errorf("a reason is sent where the socket declares slots: %s", body)
				}
			} else if got.NoSlotReason != tc.wantReason {
				t.Errorf("noSlotReason = %q, want %q (%s)", got.NoSlotReason, tc.wantReason, body)
			}

			if len(got.Slots) != len(tc.wantSlots) {
				t.Fatalf("%d slot(s) answered, want %d: %s", len(got.Slots), len(tc.wantSlots), body)
			}
			for i, want := range tc.wantSlots {
				slot := got.Slots[i]
				if slot.Slot != want.slot || slot.Selector != want.selector || slot.Required != want.required ||
					slot.Sensitive != want.sensitive || slot.Description != want.description {
					t.Errorf("slot %d = %+v, want %+v", i, slot, want)
				}
				if slot.Inject == nil || !equalStrings(slot.Inject, want.inject) {
					t.Errorf("slot %s inject = %#v, want %#v", want.slot, slot.Inject, want.inject)
				}
				if names := refNames(slot.Candidates); !equalStrings(names, want.candidates) {
					t.Errorf("slot %s candidates = %v, want %v", want.slot, names, want.candidates)
				}
			}
		})
	}
}

// TestAttachCandidatesForAGraphNodeCarriesNoConnectionKeys pins the answer every
// request sent without a connection keeps: the four keys it always carried, in
// their order, and neither of the connection's. The connection fields are
// additive, and this is the assertion that says so on the bytes.
func TestAttachCandidatesForAGraphNodeCarriesNoConnectionKeys(t *testing.T) {
	ws, release := connectionSiteWorkspace(t)
	srv := connectionSiteServer(t, ws)

	resp := attachRequest(t, srv, attachCandidatesParams{GraphNode: "gateway", Release: release})
	if resp.Error != nil {
		t.Fatalf("a node-addressed request was refused: %+v", resp.Error)
	}
	keys := strings.Join(topLevelKeys(t, resp.Result), ",")
	if want := "node,nodeLabels,labelSource,candidates"; keys != want {
		t.Fatalf("a node-addressed answer carries the keys %s, want exactly %s: %s", keys, want, resp.Result)
	}
}

// TestTheDaemonsConnectionCandidacyIsTheSharedLayers is the daemon's half of the
// cross-surface proof: what the wire method answers for a connection is exactly
// what the shared entry point answers for the same graph, compared byte for byte.
func TestTheDaemonsConnectionCandidacyIsTheSharedLayers(t *testing.T) {
	ws, release := connectionSiteWorkspace(t)
	srv := connectionSiteServer(t, ws)
	connection := ConnectionCoordinate{Socket: "models", Alias: "gpt"}

	resp := attachRequest(t, srv, attachCandidatesParams{GraphNode: "gateway", Release: release, Connection: &connection})
	if resp.Error != nil {
		t.Fatalf("the wire method refused the connection: %+v", resp.Error)
	}

	preview, envelope := srv.previewRelease(context.Background(), release,
		pipeline.Options{IncludeTrace: false, SkipRender: true})
	if envelope != nil {
		t.Fatalf("preview the Release: %+v", envelope)
	}
	terms, envelope := srv.termsFor(release)
	if envelope != nil {
		t.Fatalf("the Release's terms: %+v", envelope)
	}
	shared, refusal := AttachCandidatesForConnection(context.Background(), srv.workspaceRoot, srv.wsConfig, terms.lookup,
		terms.reach, preview.pipeline.Completed().IRGraph, "gateway", connection)
	if refusal != ConnectionAnswered {
		t.Fatalf("the shared entry point refused what the method answered: %s", refusal.Reason())
	}
	sharedJSON, err := json.Marshal(shared)
	if err != nil {
		t.Fatalf("marshal the shared answer: %v", err)
	}
	if string(sharedJSON) != string(resp.Result) {
		t.Fatalf("the wire method's answer and the shared layer's differ:\n  method: %s\n  shared: %s",
			resp.Result, sharedJSON)
	}
}

// TestBothHelperRequestsDeclareTheConnectionTheDaemonAccepts is the schema → Go
// direction for the one field both requests gained: the published contract
// declares it optional on each, by reference to the one coordinate shape, and the
// shape the daemon decodes each request into declares it — so a client typed
// through the published binding is never refused the field the contract offers.
func TestBothHelperRequestsDeclareTheConnectionTheDaemonAccepts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		def    string
		params any
	}{
		{def: "#/$defs/AttachCandidatesParams", params: attachCandidatesParams{}},
		{def: "#/$defs/AttachmentContainerParams", params: attachmentContainerParams{}},
	} {
		name := wireOptionalProperty(t, tc.def, "connection")
		position, err := resolveSchemaPointer(wireSchemaDocument(t), tc.def+"/properties/"+name)
		if err != nil {
			t.Fatalf("%s: %v", tc.def, err)
		}
		if position["$ref"] != "#/$defs/ConnectionCoordinate" {
			t.Errorf("%s declares %q as %v, not by reference to the one coordinate shape", tc.def, name, position["$ref"])
		}
		if !declaredParamFields(tc.params)[name] {
			t.Errorf("the shape the daemon decodes %s into declares no %q, so the field the contract offers is refused",
				tc.def, name)
		}
	}
}

// TestAttachCandidatesRefusesAConnectionItCannotAnswer drives each refusal with an
// answerable request first as the positive control, so a row that stopped
// exercising its own fault reads as the control failing.
func TestAttachCandidatesRefusesAConnectionItCannotAnswer(t *testing.T) {
	ws, release := connectionSiteWorkspace(t)
	gpt := &ConnectionCoordinate{Socket: "models", Alias: "gpt"}

	for _, tc := range []struct {
		name     string
		params   attachCandidatesParams
		wantCode int
		refuses  string
	}{
		{name: "a connection the graph carries",
			params: attachCandidatesParams{GraphNode: "gateway", Release: release, Connection: gpt}},
		{name: "a connection beside a workspace document",
			params:   attachCandidatesParams{Node: "conn/gateway", Connection: gpt},
			wantCode: jsonRPCInvalidParams, refuses: "params.connection names a connection a graph node draws"},
		{name: "a connection beside a label set",
			params:   attachCandidatesParams{Labels: map[string]string{}, Connection: gpt},
			wantCode: jsonRPCInvalidParams, refuses: "params.connection names a connection a graph node draws"},
		{name: "a connection naming no socket",
			params: attachCandidatesParams{GraphNode: "gateway", Release: release,
				Connection: &ConnectionCoordinate{Alias: "gpt"}},
			wantCode: jsonRPCInvalidParams, refuses: "params.connection.socket is required"},
		{name: "a connection naming no alias",
			params: attachCandidatesParams{GraphNode: "gateway", Release: release,
				Connection: &ConnectionCoordinate{Socket: "models"}},
			wantCode: jsonRPCInvalidParams, refuses: "params.connection.alias is required"},
		{name: "a document outside the two",
			params: attachCandidatesParams{GraphNode: "gateway", Release: release,
				Connection: &ConnectionCoordinate{Socket: "models", Alias: "gpt", Document: "elsewhere"}},
			wantCode: jsonRPCInvalidParams, refuses: "params.connection.document names neither document"},
		{name: "a connection the node does not draw",
			params: attachCandidatesParams{GraphNode: "gateway", Release: release,
				Connection: &ConnectionCoordinate{Socket: "models", Alias: "nonesuch"}},
			wantCode: errEntityNotResolvable, refuses: ConnectionRefusedUnknownConnection.Reason()},
		{name: "a node the graph does not carry",
			params:   attachCandidatesParams{GraphNode: "nonesuch", Release: release, Connection: gpt},
			wantCode: errEntityNotResolvable, refuses: ConnectionRefusedUnknownNode.Reason()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := connectionSiteServer(t, ws)
			resp := attachRequest(t, srv, tc.params)
			switch {
			case tc.refuses == "" && resp.Error != nil:
				t.Fatalf("an answerable request was refused: %+v", resp.Error)
			case tc.refuses == "":
				return
			case resp.Error == nil:
				t.Fatalf("a request that should have been refused was answered: %s", resp.Result)
			case resp.Error.Code != tc.wantCode:
				t.Fatalf("refused with code %d, want %d: %s", resp.Error.Code, tc.wantCode, resp.Error.Message)
			case !strings.Contains(resp.Error.Message, tc.refuses):
				t.Fatalf("the refusal does not say what was wrong: want %q in %q", tc.refuses, resp.Error.Message)
			}
		})
	}
}

// TestConnectionAttachmentContainerNamesTheEntryInEachDocument drives the
// registered wire method for a connection in each document, and each refusal the
// two documents draw, with the answerable rows first.
//
// The expected containers are written out in full rather than derived, because a
// derived expectation would be a second implementation of the thing under test.
func TestConnectionAttachmentContainerNamesTheEntryInEachDocument(t *testing.T) {
	ws, release := connectionSiteWorkspace(t)

	for _, tc := range []struct {
		name       string
		connection ConnectionCoordinate
		wantFile   string
		wantPath   []string
		wantCode   int
		refuses    string
	}{
		{
			name:       "an Aggregate-drawn connection, in the Aggregate",
			connection: ConnectionCoordinate{Socket: "models", Alias: "gpt", Document: "aggregate"},
			wantFile:   "aggregate.yaml",
			wantPath:   []string{"spec", "components", "gateway", "uses", "models", "gpt", "features"},
		},
		{
			name:       "an Aggregate-drawn connection, in the Release, which writes no entry for it yet",
			connection: ConnectionCoordinate{Socket: "models", Alias: "gpt", Document: "release"},
			wantFile:   "release.yaml",
			wantPath: []string{"spec", "boundaries", "cluster", "components", "gateway", "uses", "models", "gpt",
				"features"},
		},
		{
			name:       "a Release-drawn connection, in the Release",
			connection: ConnectionCoordinate{Socket: "logs", Alias: "out", Document: "release"},
			wantFile:   "release.yaml",
			wantPath: []string{"spec", "boundaries", "cluster", "components", "gateway", "uses", "logs", "out",
				"features"},
		},
		{
			name:       "a Release-drawn connection, in the Aggregate",
			connection: ConnectionCoordinate{Socket: "logs", Alias: "out", Document: "aggregate"},
			wantCode:   errEntityNotResolvable,
			refuses:    ConnectionRefusedReleaseDrawn.Reason(),
		},
		{
			name:       "a Feature-drawn connection",
			connection: ConnectionCoordinate{Socket: "peer", Alias: "p1", Document: "release"},
			wantCode:   errEntityNotResolvable,
			refuses:    ConnectionRefusedFeatureCreated.Reason(),
		},
		{
			name:       "a connection naming no document",
			connection: ConnectionCoordinate{Socket: "models", Alias: "gpt"},
			wantCode:   jsonRPCInvalidParams,
			refuses:    "params.connection.document is required here",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := connectionSiteServer(t, ws)
			connection := tc.connection
			resp := attachmentContainerRequest(t, srv, attachmentContainerParams{
				GraphNode: "gateway", Release: release, Connection: &connection,
			})
			if tc.refuses != "" {
				switch {
				case resp.Error == nil:
					t.Fatalf("a request that should have been refused was answered: %s", resp.Result)
				case resp.Error.Code != tc.wantCode:
					t.Fatalf("refused with code %d, want %d: %s", resp.Error.Code, tc.wantCode, resp.Error.Message)
				case !strings.Contains(resp.Error.Message, tc.refuses):
					t.Fatalf("the refusal does not say what was wrong: want %q in %q", tc.refuses, resp.Error.Message)
				}
				return
			}
			got := decodeAttachmentContainer(t, resp)
			if got.Node != "gateway" || got.File != tc.wantFile {
				t.Errorf("node, file = %q, %q, want gateway, %q", got.Node, got.File, tc.wantFile)
			}
			if strings.Join(got.Container, "\x00") != strings.Join(tc.wantPath, "\x00") {
				t.Errorf("container = %v, want %v", got.Container, tc.wantPath)
			}
		})
	}
}

// TestConnectionAttachmentContainerRefusesWithoutAReleaseEntryOrADocument covers
// the two refusals the fixture's Release cannot produce through the wire: a
// Release writing no entry for the node, and a document the request leaves
// unnamed, which the wire refuses before the shared layer is reached.
func TestConnectionAttachmentContainerRefusesWithoutAReleaseEntryOrADocument(t *testing.T) {
	graph := &types.IRGraph{}
	graph.Spec.Nodes.Set("gateway", types.IRNode{Component: "conn/gateway", SourceFile: "aggregate.yaml"})
	graph.Spec.Edges = []types.IREdge{{
		Source: "gateway", Target: "model", Socket: "models", Alias: "gpt",
		SocketDeclaration: types.SocketDeclarationDeclared,
	}}
	release := &types.Release{}

	gpt := ConnectionCoordinate{Socket: "models", Alias: "gpt", Document: "release"}
	if _, refusal := ConnectionAttachmentContainer("/w", graph, release, "/w/release.yaml", "gateway",
		gpt); refusal != ConnectionRefusedNoReleaseEntry {
		t.Fatalf("a Release writing no entry for the node: refusal = %q, want %q", refusal,
			ConnectionRefusedNoReleaseEntry)
	}
	unnamed := ConnectionCoordinate{Socket: "models", Alias: "gpt"}
	if _, refusal := ConnectionAttachmentContainer("/w", graph, release, "/w/release.yaml", "gateway",
		unnamed); refusal != ConnectionRefusedDocumentUnnamed {
		t.Fatalf("an unnamed document: refusal = %q, want %q", refusal, ConnectionRefusedDocumentUnnamed)
	}
	aggregate := ConnectionCoordinate{Socket: "models", Alias: "gpt", Document: "aggregate"}
	if _, refusal := ConnectionAttachmentContainer("/w", graph, release, "/w/release.yaml", "gateway",
		aggregate); refusal != ConnectionAnswered {
		t.Fatalf("the positive control was refused: %s", refusal.Reason())
	}

	for _, r := range []ConnectionRefusal{
		ConnectionRefusedUnknownNode, ConnectionRefusedUnknownConnection, ConnectionRefusedSlotsUnknown,
		ConnectionRefusedFeatureCreated, ConnectionRefusedDocumentUnnamed, ConnectionRefusedReleaseDrawn,
		ConnectionRefusedNoReleaseEntry, ConnectionRefusedNoSourceDocument, ConnectionRefusedUnlocatableEntry,
	} {
		if strings.TrimSpace(r.Reason()) == "" {
			t.Errorf("the %q refusal renders no reason", r)
		}
	}
	if ConnectionAnswered.Reason() != "" {
		t.Error("an answered request renders a reason")
	}
	for _, r := range []NoSlotReason{
		NoSlotReasonFeatureCreatedConnection, NoSlotReasonAdHocSocket, NoSlotReasonNoSlotDeclared,
	} {
		if strings.TrimSpace(r.Sentence()) == "" {
			t.Errorf("the %q reason renders no sentence", r)
		}
	}
}

// TestConnectionAttachmentContainerAnswerAppliesInEachDocument is the
// applied-edit proof: the container and the insertion point are taken as
// answered, the attachment is written at the reported position, and the document
// is read back through the compiler's own decode — the Aggregate's connection
// entry, and the Release's per-edge entries through the walk the build reads them
// with. The Release case writes keys the document did not carry, which is the
// creation half of the answer.
func TestConnectionAttachmentContainerAnswerAppliesInEachDocument(t *testing.T) {
	for _, tc := range []struct {
		name     string
		document string
	}{
		{name: "in the Aggregate", document: "aggregate"},
		{name: "in the Release", document: "release"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws, release := connectionSiteWorkspace(t)
			srv := connectionSiteServer(t, ws)
			answer := decodeAttachmentContainer(t, attachmentContainerRequest(t, srv, attachmentContainerParams{
				GraphNode: "gateway", Release: release,
				Connection: &ConnectionCoordinate{Socket: "models", Alias: "gpt", Document: tc.document},
			}))

			insertion, err := InsertionPoint(ws, answer.File, answer.Container, yamlpos.PositionEncodingUTF16)
			if err != nil {
				t.Fatalf("the insertion point refused the container this method reported: %v", err)
			}
			abs := filepath.Join(ws, answer.File)
			before, err := os.ReadFile(abs)
			if err != nil {
				t.Fatalf("read the document: %v", err)
			}
			edited := applyAttachmentEdit(t, string(before), insertion, []string{"trace:", "  type: feature.conn/audit"})

			switch tc.document {
			case "aggregate":
				if was, now := instanceCensus(t, string(before)), instanceCensus(t, edited); was != now {
					t.Fatalf("the edit changed which components the document declares:\n  before: %s\n  after:  %s",
						was, now)
				}
				var agg types.Aggregate
				if err := yaml.Unmarshal([]byte(edited), &agg); err != nil {
					t.Fatalf("the edited Aggregate no longer decodes: %v\n---\n%s---", err, edited)
				}
				gateway, _ := agg.Spec.Components.Get("gateway")
				socket, _ := gateway.Uses.Get("models")
				entry, _ := socket.Verbose.Get("gpt")
				if _, ok := entry.Features.Get("trace"); !ok {
					t.Fatalf("the attachment is not on the connection's entry after the edit:\n---\n%s---", edited)
				}
				if _, ok := entry.Features.Get("credential"); !ok {
					t.Fatalf("the edit lost the attachment the entry already carried:\n---\n%s---", edited)
				}
			case "release":
				var rel types.Release
				if err := yaml.Unmarshal([]byte(edited), &rel); err != nil {
					t.Fatalf("the edited Release no longer decodes: %v\n---\n%s---", err, edited)
				}
				var found bool
				for _, e := range resolver.ReleaseEdgeEntries(&rel) {
					if e.NodeID != "gateway" || e.Socket != "models" || e.Alias != "gpt" {
						continue
					}
					if _, ok := e.Entry.Features.Get("trace"); ok {
						found = true
					}
				}
				if !found {
					t.Fatalf("no per-edge entry for the connection carries the attachment after the edit:\n---\n%s---",
						edited)
				}
			}
		})
	}
}
