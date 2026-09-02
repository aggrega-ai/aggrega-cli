// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// These tests PIN TODAY'S BEHAVIOUR of aggrega/attachCandidates on two
// questions it cannot currently answer, so the gap is executable rather
// than described. They are characterisation tests: each asserts what the
// method does now, and each is expected to FAIL — deliberately, with a
// message saying so — when the behaviour is corrected.
//
// Read that as the design: whoever changes either behaviour updates the
// assertion in the same change, which is what keeps the two from drifting
// apart silently. An assertion here that starts failing is a signal, not a
// regression.
//
// The two questions:
//
//   - A request naming a rendered-graph node id is refused. The graph keys
//     its nodes by an id built from the component-instance key, prefixed by
//     the sub-aggregate chain; the method matches the request against a
//     document's metadata.name instead, so no graph node id resolves.
//
//   - Candidates are collected only from documents inside the workspace
//     root. A document outside it is dropped before the code asks whether
//     it is a Feature, so a Feature reached through catalog resolution is
//     never offered — and the request answers with an empty list rather
//     than an error, which reads as "nothing may attach here".

// graphNodeGapWorkspace lays out a workspace whose Features come ONLY from
// a catalog directory OUTSIDE the workspace root, alongside an Aggregate
// declaring one node at the top level and one contributed by a sub-module.
//
// Two properties are load-bearing. No Feature exists anywhere under the
// workspace root, so an empty candidate list cannot be explained by a
// workspace that simply has no Features in it. And every document is
// schema-valid, so a resolution miss cannot be explained by a refused
// document — a document missing metadata.version, or a Component missing
// the required component.aggrega.ai/type label, is skipped by catalog
// resolution and would make a miss read as absence.
func graphNodeGapWorkspace(t *testing.T) (root, catDir string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "ws")
	catDir = filepath.Join(base, "cat")

	ws := map[string]string{
		// A Component ENTITY, addressable today by its metadata.name.
		"component.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: api
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
    tier: edge
    env: prod
spec: {}
`,
		// The Aggregate. Its component-instance keys are what the graph's
		// node ids are built from: "checkout" at the top level, and
		// "ledger.api" for the instance the sub-module keyed "ledger"
		// contributes.
		"aggregate.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: shop
  version: 1.0.0
spec:
  aggregates:
    ledger:
      type: ./ledger/aggregate.yaml
  components:
    checkout:
      type: api
`,
		"ledger/aggregate.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: ledger-module
  version: 1.0.0
spec:
  components:
    api:
      type: api
`,
	}
	// Two of these three are satisfied by the node's labels (tier=edge);
	// db-backup is not. So a widened collection has a specific expected
	// answer rather than merely a non-empty one.
	cat := map[string]string{
		"features/observability.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: example/observability
  version: 1.0.0
spec:
  appliesTo: tier=edge
`,
		"features/edge-tls.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: example/edge-tls
  version: 1.0.0
spec:
  appliesToAnyOf:
    - tier=edge
`,
		"features/db-backup.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: example/db-backup
  version: 1.0.0
spec:
  appliesTo: tier=data
`,
	}
	for dir, files := range map[string]map[string]string{root: ws, catDir: cat} {
		for rel, content := range files {
			abs := filepath.Join(dir, rel)
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", rel, err)
			}
			if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
				t.Fatalf("write %s: %v", rel, err)
			}
		}
	}
	return root, catDir
}

// graphNodeGapServer builds a daemon whose catalog search stack is a
// directory outside the workspace root — the shape an ordinary workspace
// consuming Features from a catalog has, and the shape newTestServer
// deliberately does not have (it passes no catalog directories at all).
func graphNodeGapServer(t *testing.T, root string, catalogDirs ...string) *Server {
	t.Helper()
	wsCfg := workspaceconfig.Default()
	wsCfg.Workspace.CatalogDirs = catalogDirs
	srv := &Server{
		workspaceRoot: root,
		wsConfig:      wsCfg,
		lazyCatalog:   catalog.NewLazyLookup(root, "", catalogDirs, nil),
		parseCache:    newCache(0, 0),
		diagnostics:   map[string][]Diagnostic{},
		renderDiags:   map[string][]Diagnostic{},
		in:            bufio.NewReader(&bytes.Buffer{}),
		out:           &bytes.Buffer{},
		stderr:        io.Discard,
	}
	srv.serverCtx, srv.serverCancel = context.WithCancel(context.Background())
	srv.cancelTracker = newCancelRegistry()
	srv.markCascadeDone()
	t.Cleanup(srv.serverCancel)
	return srv
}

// graphNodeGapRequest drives one real aggrega/attachCandidates request
// through the server's own message loop and method table.
func graphNodeGapRequest(t *testing.T, srv *Server, node string) jsonRPCResponse {
	t.Helper()
	in := bytes.NewBuffer(framedRequest(1, "aggrega/attachCandidates", map[string]any{"node": node}))
	out := &bytes.Buffer{}
	return runOneRequest(t, srv, in, out)
}

// TestAttachCandidatesRefusesAGraphNodeID pins that a request naming a
// rendered-graph node id is refused as an unknown entity — for a top-level
// node and for one a sub-module contributes, since a qualified id is where
// a naive resolution breaks.
//
// The refusal is loud, which is the one merit of the present behaviour: a
// client is told the coordinate did not resolve rather than being handed an
// answer computed for something else.
func TestAttachCandidatesRefusesAGraphNodeID(t *testing.T) {
	root, catDir := graphNodeGapWorkspace(t)
	srv := graphNodeGapServer(t, root, catDir)

	for _, node := range []string{"checkout", "ledger.api"} {
		resp := graphNodeGapRequest(t, srv, node)
		if resp.Error == nil {
			t.Fatalf("node %q now resolves (result: %s) — graph-node addressing has been "+
				"implemented; update this assertion to the new contract", node, resp.Result)
		}
		if resp.Error.Code != errEntityNotResolvable {
			t.Errorf("node %q: error code = %d, want %d (%+v)",
				node, resp.Error.Code, errEntityNotResolvable, resp.Error)
		}
	}
}

// TestAttachCandidatesOmitsCatalogResolvedFeatures pins that a workspace
// declaring no local Feature answers with an empty candidate list, while
// the very Features that should have been offered resolve through the
// session catalog in the same server.
//
// The resolution half of the assertion is what makes the empty list
// attributable. Without it an empty list is equally consistent with a
// catalog that carries nothing, and the test would be pinning the fixture
// rather than the behaviour.
func TestAttachCandidatesOmitsCatalogResolvedFeatures(t *testing.T) {
	root, catDir := graphNodeGapWorkspace(t)
	srv := graphNodeGapServer(t, root, catDir)

	resp := graphNodeGapRequest(t, srv, "api")
	if resp.Error != nil {
		t.Fatalf("attachCandidates for a declared entity errored: %+v", resp.Error)
	}
	var result struct {
		Node       string            `json:"node"`
		NodeLabels map[string]string `json:"nodeLabels"`
		Candidates []json.RawMessage `json:"candidates"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v (body: %s)", err, resp.Result)
	}
	if len(result.Candidates) != 0 {
		t.Fatalf("candidates are no longer empty (%s) — catalog-resolved collection has been "+
			"implemented; update this assertion to the new contract", resp.Result)
	}

	// The control: these are resolvable in this very server, and two of
	// them declare an applicability the reported nodeLabels satisfy. So the
	// empty list above is a collection gap and not an empty catalog.
	for _, uri := range []string{"example/observability", "example/edge-tls"} {
		feat, ok := srv.lazyCatalog.Feature(uri)
		if !ok {
			t.Fatalf("control failed: Feature(%q) does not resolve, so the empty candidate "+
				"list above proves nothing about collection", uri)
		}
		if feat.Metadata.Name != uri {
			t.Errorf("Feature(%q) resolved to name %q", uri, feat.Metadata.Name)
		}
	}
	if result.NodeLabels["tier"] != "edge" {
		t.Errorf("nodeLabels[tier] = %q, want %q — the control's applicability is matched "+
			"against this label", result.NodeLabels["tier"], "edge")
	}
}

// TestSessionCatalogResolvesAComponentOnlyFromItsSearchStack records which
// configurations put a node's labels within reach of a read-only query.
//
// It matters for addressing a graph node: a node's labels are the resolved
// Component entity's, so a surface answering for a node id needs the
// Component to resolve. The daemon's search stack is the workspace config's
// catalog directories, and a workspace that configures none resolves no
// Component at all — in which case a label set is not merely incomplete but
// absent, and an absent label set would satisfy every Feature that declares
// no applicability while excluding every Feature that declares one.
func TestSessionCatalogResolvesAComponentOnlyFromItsSearchStack(t *testing.T) {
	root, catDir := graphNodeGapWorkspace(t)

	if _, ok := graphNodeGapServer(t, root).lazyCatalog.Component("api"); ok {
		t.Errorf("Component(api) resolved with no catalog directory configured")
	}
	if _, ok := graphNodeGapServer(t, root, catDir).lazyCatalog.Component("api"); ok {
		t.Errorf("Component(api) resolved from a catalog directory that does not carry it")
	}
	comp, ok := graphNodeGapServer(t, root, root).lazyCatalog.Component("api")
	if !ok {
		t.Fatalf("Component(api) did not resolve with the workspace root on the search stack")
	}
	if v, has := comp.Metadata.Labels.Get("tier"); !has || v != "edge" {
		t.Errorf("resolved labels lack tier=edge: %+v", comp.Metadata.Labels)
	}
}
