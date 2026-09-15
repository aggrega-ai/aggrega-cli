// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/requestcancel"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// These tests cover where a Feature attachment for a node on a compiled graph
// belongs, across every authoring shape a component can be written in.
//
// The shapes are covered together because they are MUTUALLY EXCLUSIVE within one
// Aggregate: a document declares top-level components or named subsystems, never
// both. A suite exercising one of them exercises about half the authoring styles
// in use and passes while the capability does nothing for the other, so both are
// driven here, and the subsystem side is driven at two nesting depths because a
// derivation that joined the scope path as one segment would name a key no file
// contains only once the scope nests.
//
// Every answer is then APPLIED. A container path is a string until something
// writes at it, and a wrong one is still a string — so each case hands its answer
// to the insertion-point entry point, splices the composed text in, and re-reads
// the document through the compiler's own decode and instance walk. What is
// asserted there is not merely that an attachment appeared but that EXACTLY ONE
// component instance carries it and that it is the addressed one, which is what
// separates a right answer from a plausible one that landed on a sibling.

// attachmentContainerWorkspace lays out a workspace carrying two Releases, one
// per authoring shape, over one catalog.
//
// The nested Release's Aggregate declares named subsystems, one of them nested
// inside another, so a component sits two levels down. The flat Release's
// Aggregate declares top-level components and imports a module, so it covers the
// synthesized-scope shape and the qualified-identifier shape at once.
//
// The property that makes this fixture worth its length: a node id carries
// NOTHING about the subsystem enclosing it. The graph keys the two-level
// component as "ledger", exactly as it keys a top-level one, so a caller holding
// the id alone cannot compose the container at any nesting depth — which is the
// whole reason the compiler is the one answering.
func attachmentContainerWorkspace(t *testing.T) (ws string, nestedRel, flatRel string) {
	t.Helper()
	ws = t.TempDir()
	catalogDir := filepath.Join(ws, "catalog")

	writeFixtureFile(t, filepath.Join(catalogDir, "components", "edge.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: demo/edge
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    provides:
      host: { type: string, required: true }
`)
	writeFixtureFile(t, filepath.Join(catalogDir, "components", "store.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: demo/store
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "stateful-server"
spec:
  schema:
    provides:
      host: { type: string, required: true }
`)
	// The module. Its component key is what the qualified node id is built from:
	// the module is imported under "store", so its "primary" instance becomes
	// "store.primary" — and the document an attachment for it belongs in is this
	// file rather than the Aggregate the Release names.
	writeFixtureFile(t, filepath.Join(catalogDir, "aggregates", "store-module.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo/store-module
  version: "1.0.0"
spec:
  components:
    primary:
      type: demo/store
`)
	writeFixtureFile(t, filepath.Join(catalogDir, "recipes", "edge.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: demo-edge-synthesized
  version: "1.0.0"
spec:
  capability: helm
  implements: demo/edge
  provides:
    host: "{{ .context.instance_id }}"
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: demo-edge:latest
`)
	writeFixtureFile(t, filepath.Join(catalogDir, "recipes", "store.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: demo-store-synthesized
  version: "1.0.0"
spec:
  capability: helm
  implements: demo/store
  provides:
    host: "{{ .context.instance_id }}"
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: demo-store:latest
`)
	writeFixtureFile(t, filepath.Join(catalogDir, "plugins", "helm-renderer.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: helm
  pluginType: provider
  version: "1.0.0"
spec:
  abiVersion: v1alpha1
  provider:
    capabilities:
      - helm
  exports:
    - name: render_node
  runtime:
    maxMemoryPages: 4096
    timeoutMs: 300000
`)

	writeFixtureFile(t, filepath.Join(ws, "app-manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: app-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        demo/edge: demo-edge-synthesized
        demo/store: demo-store-synthesized
`)

	// The subsystem shape. "ledger" sits two levels down and "gateway" one, so
	// one document covers both a nested scope and a shallow one.
	writeFixtureFile(t, filepath.Join(ws, "nested-agg.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: nested-agg
  version: "1.0.0"
spec:
  subsystems:
    billing:
      subsystems:
        payments:
          components:
            ledger:
              type: "demo/edge:1.0.0"
    edge:
      components:
        gateway:
          type: "demo/edge:1.0.0"
`)
	nestedPath := filepath.Join(ws, "nested-release.yaml")
	writeFixtureFile(t, nestedPath, `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: nested-release
  environment: production
  version: "1.0.0"
spec:
  aggregate: nested-agg
  manifest: app-manifest
  boundaries:
    app:
      directory: ./dist/nested
      components:
        ledger: {}
        gateway: {}
`)

	// The top-level shape, plus the imported module.
	writeFixtureFile(t, filepath.Join(ws, "flat-agg.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: flat-agg
  version: "1.0.0"
spec:
  aggregates:
    store:
      type: demo/store-module
  components:
    checkout:
      type: "demo/edge:1.0.0"
`)
	flatPath := filepath.Join(ws, "flat-release.yaml")
	writeFixtureFile(t, flatPath, `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: flat-release
  environment: production
  version: "1.0.0"
spec:
  aggregate: flat-agg
  manifest: app-manifest
  boundaries:
    app:
      directory: ./dist/flat
      components:
        checkout: {}
        store.primary: {}
`)

	for _, rel := range []string{nestedPath, flatPath} {
		lf := &lockfile.Lockfile{
			Version:     1,
			Release:     "./" + filepath.Base(rel),
			GeneratedAt: "2026-06-03T10:00:00Z",
			Entities: []lockfile.Entity{
				{URI: "demo/edge:1.0.0", ResolvedVersion: "1.0.0"},
				{URI: "demo/store:1.0.0", ResolvedVersion: "1.0.0"},
			},
		}
		if err := lockfile.WriteLockfile(lockfile.PathFor(rel), lf); err != nil {
			t.Fatalf("WriteLockfile: %v", err)
		}
	}
	return ws, filepath.Base(nestedPath), filepath.Base(flatPath)
}

// attachmentContainerServer builds a daemon over the fixture workspace, with the
// workspace's own catalog directory on its search stack.
func attachmentContainerServer(t *testing.T, ws string) *Server {
	t.Helper()
	srv := newPoolTestServer(t, &bytes.Buffer{}, &bytes.Buffer{}, 1, nil)
	srv.workspaceRoot = ws
	cacheDir := ".aggrega/cache"
	srv.wsConfig.ArtifactCacheDir = cacheDir
	catalogDirs := []string{filepath.Join(ws, "catalog")}
	srv.wsConfig.Workspace.CatalogDirs = catalogDirs
	srv.lazyCatalog = catalog.NewLazyLookup(ws, cacheDir, catalogDirs, nil)
	reanchorTestWorkspace(t, srv)
	srv.serverCtx, srv.serverCancel = context.WithCancel(context.Background())
	srv.cancelTracker = requestcancel.New()
	t.Cleanup(srv.serverCancel)
	return srv
}

// attachmentContainerRequest drives one real aggrega/attachmentContainer request
// through the server's own message loop and method table, which is what makes
// this a test of the registered wire surface rather than of a Go function that
// happens to be reachable.
func attachmentContainerRequest(t *testing.T, srv *Server, params attachmentContainerParams) jsonRPCResponse {
	t.Helper()
	in := bytes.NewBuffer(framedRequest(1, "aggrega/attachmentContainer", params))
	return runOneRequest(t, srv, in, &bytes.Buffer{})
}

func decodeAttachmentContainer(t *testing.T, resp jsonRPCResponse) AttachmentContainerResult {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("attachmentContainer errored: %+v", resp.Error)
	}
	var out AttachmentContainerResult
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatalf("unmarshal result: %v (body: %s)", err, resp.Result)
	}
	return out
}

// attachmentContainerCase is one node, the document it was authored in, and the
// container an attachment for it belongs in.
type attachmentContainerCase struct {
	name      string
	release   func(nested, flat string) string
	node      string
	wantFile  string
	wantPath  []string
	subsystem string
}

// attachmentContainerCases enumerates the authoring shapes. Each expected
// container is written out in full rather than derived, because a derived
// expectation would be a second implementation of the thing under test and would
// agree with it however wrong both were.
var attachmentContainerCases = []attachmentContainerCase{
	{
		name:      "a component two subsystems deep",
		release:   func(nested, _ string) string { return nested },
		node:      "ledger",
		wantFile:  "nested-agg.yaml",
		wantPath:  []string{"spec", "subsystems", "billing", "subsystems", "payments", "components", "ledger", "features"},
		subsystem: "billing.payments",
	},
	{
		name:      "a component one subsystem deep",
		release:   func(nested, _ string) string { return nested },
		node:      "gateway",
		wantFile:  "nested-agg.yaml",
		wantPath:  []string{"spec", "subsystems", "edge", "components", "gateway", "features"},
		subsystem: "edge",
	},
	{
		name:     "a component at the top level",
		release:  func(_, flat string) string { return flat },
		node:     "checkout",
		wantFile: "flat-agg.yaml",
		wantPath: []string{"spec", "components", "checkout", "features"},
	},
	{
		name:     "a component an imported module contributed",
		release:  func(_, flat string) string { return flat },
		node:     "store.primary",
		wantFile: filepath.Join("catalog", "aggregates", "store-module.yaml"),
		wantPath: []string{"spec", "components", "primary", "features"},
	},
}

// TestAttachmentContainerNamesTheSiteTheAuthorWroteForEveryAuthoringShape drives
// the registered wire method for each shape and pins the document and the
// container it names.
//
// The last case is the one that catches an answer built from the Release's own
// Aggregate: a node an imported module contributed was authored in the module's
// document, under the name the module gave it, so both halves of the answer
// differ from anything the caller named in the request.
func TestAttachmentContainerNamesTheSiteTheAuthorWroteForEveryAuthoringShape(t *testing.T) {
	ws, nested, flat := attachmentContainerWorkspace(t)

	for _, tc := range attachmentContainerCases {
		t.Run(tc.name, func(t *testing.T) {
			srv := attachmentContainerServer(t, ws)
			got := decodeAttachmentContainer(t, attachmentContainerRequest(t, srv, attachmentContainerParams{
				GraphNode: tc.node,
				Release:   tc.release(nested, flat),
			}))

			if got.Node != tc.node {
				t.Errorf("node = %q, want %q", got.Node, tc.node)
			}
			if got.File != tc.wantFile {
				t.Errorf("file = %q, want %q — an attachment belongs in the document the node was "+
					"authored in, which is not always the one the Release names", got.File, tc.wantFile)
			}
			if strings.Join(got.Container, "\x00") != strings.Join(tc.wantPath, "\x00") {
				t.Errorf("container = %v, want %v", got.Container, tc.wantPath)
			}
		})
	}
}

// TestAttachmentContainerSaysNothingAboutTheEnclosingScopeInTheNodeId is the
// measurement behind this capability existing at all: the identifier a caller
// holds carries no trace of the scope the component sits in, so a caller cannot
// compose the container from it however carefully it splits.
//
// It is asserted rather than assumed because the justification rests on it. Were
// a node id ever to carry its scope, composing the path in the caller would
// become possible and this whole surface would be a convenience rather than a
// necessity — and nothing else here would notice the change.
func TestAttachmentContainerSaysNothingAboutTheEnclosingScopeInTheNodeId(t *testing.T) {
	ws, nested, _ := attachmentContainerWorkspace(t)
	srv := attachmentContainerServer(t, ws)

	got := decodeAttachmentContainer(t, attachmentContainerRequest(t, srv, attachmentContainerParams{
		GraphNode: "ledger",
		Release:   nested,
	}))
	if strings.Contains(got.Node, "billing") || strings.Contains(got.Node, "payments") {
		t.Fatalf("the node id %q names its enclosing scope; the premise this capability rests on "+
			"has changed and the justification has to be re-argued", got.Node)
	}
	for _, scope := range []string{"billing", "payments"} {
		var carried bool
		for _, seg := range got.Container {
			if seg == scope {
				carried = true
			}
		}
		if !carried {
			t.Errorf("the container %v omits the %q scope the component is authored inside",
				got.Container, scope)
		}
	}
}

// TestAttachmentContainerAnswerAppliesAndTheDocumentReloads is the applied-edit
// proof. A returned container path proves nothing on its own — a wrong one is
// still a path — so each answer is handed to the insertion-point entry point,
// the composed text is spliced in, and the result is read back through the
// compiler's own Aggregate decode and normalized instance walk.
//
// Three things are asserted together, and each covers what the others miss.
// Exactly one instance carries the attachment, so an answer that also reached a
// sibling fails. It is the addressed one, so an answer that reached only a
// sibling fails. And the component census is UNCHANGED across the edit, which is
// the assertion the first two cannot make: a container naming a scope the
// document does not carry is CREATED by the insertion rather than refused, so
// the attachment then lands on an instance that reads as the right one inside a
// scope nobody authored. That case parses, carries exactly one attachment, and
// reports the expected scope path — every other assertion here passes on it.
func TestAttachmentContainerAnswerAppliesAndTheDocumentReloads(t *testing.T) {
	for _, tc := range attachmentContainerCases {
		t.Run(tc.name, func(t *testing.T) {
			ws, nested, flat := attachmentContainerWorkspace(t)
			srv := attachmentContainerServer(t, ws)

			answer := decodeAttachmentContainer(t, attachmentContainerRequest(t, srv, attachmentContainerParams{
				GraphNode: tc.node,
				Release:   tc.release(nested, flat),
			}))

			// The second call, made with nothing but the first answer's own
			// fields. Composing anything here would be the caller doing the
			// structural work this capability exists to spare it.
			insertion, err := InsertionPoint(ws, answer.File, answer.Container, yamlpos.PositionEncodingUTF16)
			if err != nil {
				t.Fatalf("the insertion point refused the container this method reported: %v", err)
			}

			abs := filepath.Join(ws, answer.File)
			before, err := os.ReadFile(abs)
			if err != nil {
				t.Fatalf("read the document: %v", err)
			}
			edited := applyAttachmentEdit(t, string(before), insertion,
				[]string{"audit:", "  type: demo/audit"})
			if err := os.WriteFile(abs, []byte(edited), 0o644); err != nil {
				t.Fatalf("write the edited document: %v", err)
			}

			if was, now := instanceCensus(t, string(before)), instanceCensus(t, edited); was != now {
				t.Fatalf("the edit changed which components the document declares, so the container named a "+
					"place the document did not carry and the insertion created it:\n  before: %s\n  after:  %s\n---\n%s---",
					was, now, edited)
			}

			carriers := attachmentCarriers(t, edited, "audit")
			if len(carriers) != 1 {
				t.Fatalf("%d component instance(s) carry the attachment after the edit, want exactly 1: %v\n---\n%s---",
					len(carriers), carriers, edited)
			}
			authoredName := tc.node
			if i := strings.LastIndex(authoredName, "."); i >= 0 {
				authoredName = authoredName[i+1:]
			}
			want := tc.subsystem + "/" + authoredName
			if carriers[0] != want {
				t.Fatalf("the attachment landed on %q, want %q\n---\n%s---", carriers[0], want, edited)
			}
		})
	}
}

// applyAttachmentEdit composes the text the insertion answer calls for and
// splices it in at the reported position, the way a client holding the buffer
// would. It follows the answer's own contract and reads nothing out of the
// document itself.
func applyAttachmentEdit(t *testing.T, src string, p InsertionPointResult, entry []string) string {
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

// instanceCensus renders every component instance a document declares, through
// the compiler's own walk, as one comparable string.
//
// It exists to catch the one wrong answer every other assertion here admits: a
// container naming a scope the document does not carry is CREATED by the
// insertion rather than refused, and the attachment then sits on an instance
// that reads correctly inside a scope nobody authored. The census moves when
// that happens and moves for nothing an authoring edit legitimately does, since
// adding an attachment declares no component.
func instanceCensus(t *testing.T, content string) string {
	t.Helper()
	var agg types.Aggregate
	if err := yaml.Unmarshal([]byte(content), &agg); err != nil {
		t.Fatalf("the document does not decode as an Aggregate: %v\n---\n%s---", err, content)
	}
	var out []string
	for _, ref := range types.Normalize(&agg).ComponentInstances() {
		path := ref.SubsystemPath
		if ref.Synthesized {
			path = ""
		}
		out = append(out, path+"/"+ref.Name)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// attachmentCarriers re-reads a document through the compiler's own Aggregate
// decode and normalized instance walk, and returns every instance carrying an
// attachment under the given name, as "<subsystem path>/<instance name>".
//
// Reading it back through the compiler rather than through a text match is what
// makes this a proof about the document rather than about the bytes: a decode
// that fails, an attachment that landed under the wrong component, and one that
// landed at a scope level the walk does not reach are each visible here and none
// of them is visible in a substring search.
func attachmentCarriers(t *testing.T, content, instance string) []string {
	t.Helper()
	var agg types.Aggregate
	if err := yaml.Unmarshal([]byte(content), &agg); err != nil {
		t.Fatalf("the edited document no longer decodes as an Aggregate: %v\n---\n%s---", err, content)
	}
	var out []string
	for _, ref := range types.Normalize(&agg).ComponentInstances() {
		if _, ok := ref.Instance.Features.Get(instance); !ok {
			continue
		}
		path := ref.SubsystemPath
		if ref.Synthesized {
			path = ""
		}
		out = append(out, path+"/"+ref.Name)
	}
	return out
}

// TestTheDaemonsAttachmentContainerIsTheSharedLayers is the daemon's half of the
// cross-surface proof: what the wire method returns is exactly what the shared
// entry point produces for the same node of the same graph.
//
// The graph the shared layer is asked over is round-tripped through the wire
// encoding first, so the comparison runs over the graph a client would receive
// rather than over an in-process value the encoding never touched. Comparing
// against the shared layer rather than against a literal is what makes this a
// parity proof: a literal would be a third copy, free to agree with neither
// surface.
func TestTheDaemonsAttachmentContainerIsTheSharedLayers(t *testing.T) {
	ws, nested, _ := attachmentContainerWorkspace(t)
	srv := attachmentContainerServer(t, ws)

	fromMethod := decodeAttachmentContainer(t, attachmentContainerRequest(t, srv, attachmentContainerParams{
		GraphNode: "ledger",
		Release:   nested,
	}))

	preview, errEnvelope := srv.previewRelease(context.Background(), nested,
		pipeline.Options{IncludeTrace: false, SkipRender: true})
	if errEnvelope != nil {
		t.Fatalf("preview the Release: %+v", errEnvelope)
	}
	encoded, err := json.Marshal(preview.pipeline.Completed().IRGraph)
	if err != nil {
		t.Fatalf("marshal the graph: %v", err)
	}
	var graph types.IRGraph
	if err := json.Unmarshal(encoded, &graph); err != nil {
		t.Fatalf("unmarshal the graph: %v", err)
	}

	fromShared, refusal := AttachmentContainer(&graph, "ledger")
	if refusal != AttachmentContainerReported {
		t.Fatalf("the shared entry point refused a node the method answered: %s", refusal.Reason())
	}
	methodJSON, err := json.Marshal(fromMethod)
	if err != nil {
		t.Fatalf("marshal the method answer: %v", err)
	}
	sharedJSON, err := json.Marshal(fromShared)
	if err != nil {
		t.Fatalf("marshal the shared answer: %v", err)
	}
	if string(methodJSON) != string(sharedJSON) {
		t.Fatalf("the wire method's answer and the shared layer's differ:\n  method: %s\n  shared: %s",
			methodJSON, sharedJSON)
	}
}

// TestAttachmentContainerRefusesWhatItCannotName drives each refusal, with an
// answerable request first as the positive control — so a row that stopped
// exercising its own fault is visible as the control failing rather than as
// three rows quietly agreeing.
func TestAttachmentContainerRefusesWhatItCannotName(t *testing.T) {
	ws, nested, _ := attachmentContainerWorkspace(t)

	for _, tc := range []struct {
		name    string
		params  attachmentContainerParams
		refuses string
	}{
		{
			name:    "a node the graph carries",
			params:  attachmentContainerParams{GraphNode: "ledger", Release: nested},
			refuses: "",
		},
		{
			name:    "no node at all",
			params:  attachmentContainerParams{Release: nested},
			refuses: "address a node with params.graphNode",
		},
		{
			name:    "a node the graph does not carry",
			params:  attachmentContainerParams{GraphNode: "nonesuch", Release: nested},
			refuses: "carries no node with that id",
		},
		{
			name:    "a Release that is not there",
			params:  attachmentContainerParams{GraphNode: "ledger", Release: "no-such-release.yaml"},
			refuses: "no-such-release.yaml",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := attachmentContainerServer(t, ws)
			resp := attachmentContainerRequest(t, srv, tc.params)
			switch {
			case tc.refuses == "" && resp.Error != nil:
				t.Fatalf("an answerable request was refused: %+v", resp.Error)
			case tc.refuses == "":
				return
			case resp.Error == nil:
				t.Fatalf("a request that should have been refused was answered: %s", resp.Result)
			case !strings.Contains(resp.Error.Message, tc.refuses):
				t.Fatalf("the refusal does not say what was wrong: want %q in %q",
					tc.refuses, resp.Error.Message)
			}
		})
	}
}

// TestAttachmentContainerRefusesANodeWithNoDocument covers the refusal the
// fixture Releases cannot produce, because every node they carry was authored in
// a file. A node with no recorded document has no answer rather than an empty
// one, and reporting an empty file would hand a caller a path resolving to the
// workspace root.
func TestAttachmentContainerRefusesANodeWithNoDocument(t *testing.T) {
	var graph types.IRGraph
	graph.Spec.Nodes.Set("orphan", types.IRNode{Component: "demo/edge", SubsystemPath: "billing"})

	if _, refusal := AttachmentContainer(&graph, "orphan"); refusal != AttachmentContainerRefusedNoSourceDocument {
		t.Fatalf("refusal = %q, want %q", refusal, AttachmentContainerRefusedNoSourceDocument)
	}
	if _, refusal := AttachmentContainer(&graph, "absent"); refusal != AttachmentContainerRefusedUnknownNode {
		t.Fatalf("refusal = %q, want %q", refusal, AttachmentContainerRefusedUnknownNode)
	}
	if _, refusal := AttachmentContainer(nil, "orphan"); refusal != AttachmentContainerRefusedUnknownNode {
		t.Fatalf("refusal over a nil graph = %q, want %q", refusal, AttachmentContainerRefusedUnknownNode)
	}

	// Every refusal carries wording, so a surface embedding one never reports an
	// empty clause where a reason belongs.
	for _, r := range []AttachmentContainerRefusal{
		AttachmentContainerRefusedUnknownNode,
		AttachmentContainerRefusedNoSourceDocument,
		AttachmentContainerRefusedUnlocatableComponent,
	} {
		if strings.TrimSpace(r.Reason()) == "" {
			t.Errorf("the %q refusal renders no reason", r)
		}
	}
	if AttachmentContainerReported.Reason() != "" {
		t.Error("a reported container renders a reason")
	}
}
