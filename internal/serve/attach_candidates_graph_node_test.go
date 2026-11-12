// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/requestcancel"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// These tests cover the two questions aggrega/attachCandidates answers about
// a node: which Features may attach to a workspace document, and which may
// attach to a node on a compiled graph. They also cover the population the
// candidates are drawn from, which spans the workspace and the configured
// catalog directories.
//
// The fixture is self-contained and reaches no network: every entity resolves
// from a directory on disk, and the lockfile is written locally.

// graphNodeWorkspace lays out a workspace whose Features live ONLY in a
// catalog directory outside the workspace root, alongside an Aggregate that
// declares one component at the top level and one contributed by a
// sub-aggregate.
//
// Three properties are load-bearing.
//
// No Feature exists anywhere under the workspace root, so a candidate list
// that names one can only have come through catalog resolution — an empty
// list cannot be explained away, and a populated one cannot be explained by a
// workspace that happened to carry its own copy.
//
// The workspace declares a Component ENTITY whose metadata.name is exactly
// the graph node id "checkout", and gives it a DIFFERENT label set from the
// component that node resolves to. That is the collision the two request
// parameters exist to separate: one string, two subjects, two correct and
// different answers.
//
// The sub-aggregate contributes a node whose id is qualified ("store.primary"),
// because a qualified id is where a resolution that rebuilt ids from the
// authored documents rather than reading the graph's own keys would break.
func graphNodeWorkspace(t *testing.T) (ws, featuresDir, releaseRel string) {
	t.Helper()
	base := t.TempDir()
	ws = filepath.Join(base, "ws")
	featuresDir = filepath.Join(base, "features")

	catalogDir := filepath.Join(ws, "catalog")

	writeFixtureFile(t, filepath.Join(catalogDir, "components", "edge.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: demo/edge
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
    tier: "edge"
spec:
  schema:
    provides:
      host: { type: string, required: true }
`)
	writeFixtureFile(t, filepath.Join(catalogDir, "components", "store.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: demo/store
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "stateful-server"
    tier: "data"
spec:
  schema:
    provides:
      host: { type: string, required: true }
`)
	// The sub-aggregate. Its component key is what the qualified node id is
	// built from: the aggregate is attached under "store", so its "primary"
	// instance becomes "store.primary".
	writeFixtureFile(t, filepath.Join(catalogDir, "aggregates", "store-module.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: demo/store-module
  version: "1.0.0"
spec:
  components:
    primary:
      type: demo/store
`)
	writeFixtureFile(t, filepath.Join(catalogDir, "recipes", "edge.yaml"), `apiVersion: aggrega.ai/v1beta1
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
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: demo-edge:latest
`)
	writeFixtureFile(t, filepath.Join(catalogDir, "recipes", "store.yaml"), `apiVersion: aggrega.ai/v1beta1
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
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: demo-store:latest
`)
	writeFixtureFile(t, filepath.Join(catalogDir, "plugins", "helm-renderer.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: helm
  pluginType: provider
  version: "1.0.0"
spec:
  abiVersion: v1beta1
  provider:
    capabilities:
      - helm
  exports:
    - name: render_node
  runtime:
    maxMemoryPages: 4096
    timeoutMs: 300000
`)

	// The collision. This is a workspace DOCUMENT named "checkout" — the same
	// string as the top-level graph node id — carrying labels that differ from
	// the component that node resolves to, so the two answers are
	// distinguishable rather than coincidentally equal.
	writeFixtureFile(t, filepath.Join(ws, "checkout-entity.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: checkout
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "stateful-server"
    tier: "data"
spec: {}
`)

	writeFixtureFile(t, filepath.Join(ws, "app-manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
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
	writeFixtureFile(t, filepath.Join(ws, "app-agg.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: app-agg
  version: "1.0.0"
spec:
  aggregates:
    store:
      type: demo/store-module
  components:
    checkout:
      type: "demo/edge:1.0.0"
`)
	relPath := filepath.Join(ws, "app-release.yaml")
	writeFixtureFile(t, relPath, `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: app-release
  environment: production
  version: "1.0.0"
spec:
  aggregate: app-agg
  manifest: app-manifest
  boundaries:
    app:
      directory: ./dist/app
      components:
        checkout: {}
        store.primary: {}
`)

	// Features live outside the workspace root, so they are reachable only
	// through catalog resolution. Two are satisfied by the edge node's labels
	// and one is not, so a widened collection has a specific expected answer
	// rather than merely a non-empty one.
	writeFixtureFile(t, filepath.Join(featuresDir, "observability.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: example/observability
  version: "1.0.0"
spec:
  appliesTo: tier=edge
`)
	writeFixtureFile(t, filepath.Join(featuresDir, "edge-tls.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: example/edge-tls
  version: "1.0.0"
spec:
  appliesToAnyOf:
    - tier=edge
`)
	writeFixtureFile(t, filepath.Join(featuresDir, "db-backup.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: example/db-backup
  version: "1.0.0"
spec:
  appliesTo: tier=data
`)

	// Each row records its component's own content digest, as a pull would: the
	// lockfile refuses a row without a well-formed one.
	componentDigest := func(name string) string {
		b, err := os.ReadFile(filepath.Join(catalogDir, "components", name))
		if err != nil {
			t.Fatalf("read the component fixture %s: %v", name, err)
		}
		return contentDigestHex(b)
	}
	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     "./app-release.yaml",
		GeneratedAt: "2026-06-03T10:00:00Z",
		Entities: []lockfile.Entity{
			{Kind: "component", URI: "demo/edge:1.0.0", ResolvedVersion: "1.0.0", Digest: componentDigest("edge.yaml")},
			{Kind: "component", URI: "demo/store:1.0.0", ResolvedVersion: "1.0.0", Digest: componentDigest("store.yaml")},
		},
	}
	if err := lockfile.WriteLockfile(lockfile.PathFor(relPath), lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}
	return ws, featuresDir, filepath.Base(relPath)
}

// graphNodeServer builds a daemon whose catalog search stack carries the
// workspace's own catalog directory and a Features directory outside the
// workspace root — the shape an ordinary workspace consuming Features from a
// catalog has.
func graphNodeServer(t *testing.T, ws string, catalogDirs ...string) *Server {
	t.Helper()
	srv := newPoolTestServer(t, &bytes.Buffer{}, &bytes.Buffer{}, 1, nil)
	srv.workspaceRoot = ws
	cacheDir := ".aggrega/cache"
	srv.wsConfig.ArtifactCacheDir = cacheDir
	srv.wsConfig.Workspace.CatalogDirs = catalogDirs
	srv.lazyCatalog = catalog.NewLazyLookup(ws, cacheDir, catalogDirs, nil)
	reanchorTestWorkspace(t, srv)
	srv.serverCtx, srv.serverCancel = context.WithCancel(context.Background())
	srv.cancelTracker = requestcancel.New()
	t.Cleanup(srv.serverCancel)
	return srv
}

// attachRequest drives one real aggrega/attachCandidates request through the
// server's own message loop and method table.
func attachRequest(t *testing.T, srv *Server, params attachCandidatesParams) jsonRPCResponse {
	t.Helper()
	in := bytes.NewBuffer(framedRequest(1, "aggrega/attachCandidates", params))
	out := &bytes.Buffer{}
	return runOneRequest(t, srv, in, out)
}

// attachRequestPastPushes is attachRequest for a graph-node request over a
// workspace whose lockfile is gone. That refusal is preceded by the withdrawal
// of any drift finding on the lockfile, which attachRequest would decode as the
// answer.
func attachRequestPastPushes(t *testing.T, srv *Server, params attachCandidatesParams) jsonRPCResponse {
	t.Helper()
	in := bytes.NewBuffer(framedRequest(1, "aggrega/attachCandidates", params))
	resp, _ := runOneRequestPastPushes(t, srv, in, &bytes.Buffer{})
	return resp
}

// attachResult is the decoded response shape these tests assert against.
type attachResult struct {
	Node        string            `json:"node"`
	NodeLabels  map[string]string `json:"nodeLabels"`
	LabelSource string            `json:"labelSource"`
	Candidates  []AttachCandidate `json:"candidates"`
}

func decodeAttachResult(t *testing.T, resp jsonRPCResponse) attachResult {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("attachCandidates errored: %+v", resp.Error)
	}
	var out attachResult
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatalf("unmarshal result: %v (body: %s)", err, resp.Result)
	}
	return out
}

// candidateNames projects a candidate list down to its names, for comparison
// against an expected set. Names are asserted rather than counted: a count
// agrees while naming the wrong Features.
func refNames(cands []AttachCandidate) []string {
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.Name)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestAttachCandidatesAnswersForAGraphNode covers the addressing half for a
// top-level node and for one a sub-aggregate contributes, since a qualified
// id is where a naive resolution breaks.
//
// It asserts the labels as well as the candidates. The labels are the node's
// finalised set, which is the whole reason this path is worth having: they
// are what a build evaluates applicability against, and the response reports
// which of the two label sets it used so a client cannot mistake the weaker
// answer for the stronger one.
func TestAttachCandidatesAnswersForAGraphNode(t *testing.T) {
	ws, features, rel := graphNodeWorkspace(t)

	cases := []struct {
		node       string
		wantTier   string
		wantCands  []string
		wantLabels string
	}{
		{
			node:      "checkout",
			wantTier:  "edge",
			wantCands: []string{"example/edge-tls", "example/observability"},
		},
		{
			node:      "store.primary",
			wantTier:  "data",
			wantCands: []string{"example/db-backup"},
		},
	}
	for _, c := range cases {
		t.Run(c.node, func(t *testing.T) {
			srv := graphNodeServer(t, ws, filepath.Join(ws, "catalog"), features)
			result := decodeAttachResult(t, attachRequest(t, srv, attachCandidatesParams{
				GraphNode: c.node,
				Release:   rel,
			}))

			if result.Node != c.node {
				t.Errorf("node = %q, want %q", result.Node, c.node)
			}
			if result.LabelSource != NodeLabelsResolved {
				t.Errorf("labelSource = %q, want %q — a graph node's labels are the "+
					"finalised set, and the response must say so", result.LabelSource, NodeLabelsResolved)
			}
			if got := result.NodeLabels["tier"]; got != c.wantTier {
				t.Errorf("nodeLabels[tier] = %q, want %q; full set %+v",
					got, c.wantTier, result.NodeLabels)
			}
			if got := refNames(result.Candidates); !equalStrings(got, c.wantCands) {
				t.Errorf("candidates = %v, want %v", got, c.wantCands)
			}
			for _, cand := range result.Candidates {
				if cand.Origin != EntityOriginCatalog {
					t.Errorf("candidate %q origin = %q, want %q",
						cand.Name, cand.Origin, EntityOriginCatalog)
				}
				if cand.File != "" {
					t.Errorf("candidate %q reports file %q; a catalog-resolved entity "+
						"must not put a host path on the wire", cand.Name, cand.File)
				}
			}
		})
	}
}

// TestAttachCandidatesSeparatesTheTwoIdentifierSpaces is the correctness proof
// for the separate parameter, and it is the reason the parameter exists rather
// than a convenience test.
//
// The workspace declares a Component named "checkout" and the compiled graph
// declares a node with the id "checkout". They are different subjects with
// different labels, so the same string must produce two different answers
// depending on which coordinate the client used. A single overloaded
// parameter could not: whichever space it searched first would silently
// answer for the wrong subject, and the answer would look perfectly ordinary.
func TestAttachCandidatesSeparatesTheTwoIdentifierSpaces(t *testing.T) {
	ws, features, rel := graphNodeWorkspace(t)
	dirs := []string{filepath.Join(ws, "catalog"), features}

	byName := decodeAttachResult(t, attachRequest(t, graphNodeServer(t, ws, dirs...),
		attachCandidatesParams{Node: "checkout"}))
	byGraph := decodeAttachResult(t, attachRequest(t, graphNodeServer(t, ws, dirs...),
		attachCandidatesParams{GraphNode: "checkout", Release: rel}))

	if byName.NodeLabels["tier"] != "data" {
		t.Errorf("the workspace document named checkout has tier=data; got %+v", byName.NodeLabels)
	}
	if byGraph.NodeLabels["tier"] != "edge" {
		t.Errorf("the graph node checkout has tier=edge; got %+v", byGraph.NodeLabels)
	}
	if byName.LabelSource != NodeLabelsDeclared || byGraph.LabelSource != NodeLabelsResolved {
		t.Errorf("labelSource: by-name = %q, by-graph = %q; want %q and %q",
			byName.LabelSource, byGraph.LabelSource, NodeLabelsDeclared, NodeLabelsResolved)
	}

	nameCands := refNames(byName.Candidates)
	graphCands := refNames(byGraph.Candidates)
	if !equalStrings(nameCands, []string{"example/db-backup"}) {
		t.Errorf("by-name candidates = %v, want [example/db-backup]", nameCands)
	}
	if !equalStrings(graphCands, []string{"example/edge-tls", "example/observability"}) {
		t.Errorf("by-graph candidates = %v, want [example/edge-tls example/observability]", graphCands)
	}
	if equalStrings(nameCands, graphCands) {
		t.Fatalf("both coordinates answered identically (%v), so this fixture no longer "+
			"demonstrates the collision the separate parameter exists to resolve", nameCands)
	}
}

// TestAttachCandidatesRefusesAnUnknownGraphNode pins that a graph-node request
// naming an id the graph does not carry is refused rather than answered with
// an empty list, and that the refusal names the graph rather than the
// workspace — a client told to look for a workspace document after asking
// about a graph node would search the wrong place.
func TestAttachCandidatesRefusesAnUnknownGraphNode(t *testing.T) {
	ws, features, rel := graphNodeWorkspace(t)
	srv := graphNodeServer(t, ws, filepath.Join(ws, "catalog"), features)

	resp := attachRequest(t, srv, attachCandidatesParams{GraphNode: "nonexistent", Release: rel})
	if resp.Error == nil {
		t.Fatalf("an unknown graph node was answered rather than refused: %s", resp.Result)
	}
	if resp.Error.Code != errEntityNotResolvable {
		t.Errorf("error code = %d, want %d (%+v)", resp.Error.Code, errEntityNotResolvable, resp.Error)
	}
	if !bytes.Contains([]byte(resp.Error.Message), []byte("compiled graph")) {
		t.Errorf("refusal %q does not name the graph as the space searched", resp.Error.Message)
	}
}

// TestAttachCandidatesGateOnTheLockfileOnlyForAGraphNode pins that the
// lockfile precondition rides on the computation rather than on the method.
//
// Building a graph means resolving what the Release reaches, so that path
// refuses when the lockfile is absent. Addressing a workspace document builds
// no graph and crosses no such precondition, so it keeps answering on a
// workspace nobody has prepared — which is the behaviour that must not move.
func TestAttachCandidatesGateOnTheLockfileOnlyForAGraphNode(t *testing.T) {
	ws, features, rel := graphNodeWorkspace(t)
	dirs := []string{filepath.Join(ws, "catalog"), features}

	// Remove the lockfile the fixture wrote.
	lockPath := lockfile.PathFor(filepath.Join(ws, rel))
	if err := os.Remove(lockPath); err != nil {
		t.Fatalf("remove lockfile: %v", err)
	}

	graphResp := attachRequestPastPushes(t, graphNodeServer(t, ws, dirs...),
		attachCandidatesParams{GraphNode: "checkout", Release: rel})
	if graphResp.Error == nil {
		t.Fatalf("a graph-node request answered with no lockfile present: %s", graphResp.Result)
	}
	if graphResp.Error.Code != errLockfileAbsent {
		t.Errorf("graph-node error code = %d, want %d (%+v)",
			graphResp.Error.Code, errLockfileAbsent, graphResp.Error)
	}

	// The same server, the same absent lockfile, the name-addressed coordinate.
	nameResp := attachRequest(t, graphNodeServer(t, ws, dirs...),
		attachCandidatesParams{Node: "checkout"})
	if nameResp.Error != nil {
		t.Fatalf("a name-addressed request acquired a lockfile precondition it "+
			"never had: %+v", nameResp.Error)
	}
}

// TestAttachCandidatesRefusesAmbiguousOrEmptyAddressing pins that the two
// coordinates are refused together and that neither one alone is optional.
// Ranking them silently would answer a question the client did not ask, on a
// surface where the wrong pick still produces a plausible-looking answer.
func TestAttachCandidatesRefusesAmbiguousOrEmptyAddressing(t *testing.T) {
	ws, features, rel := graphNodeWorkspace(t)
	dirs := []string{filepath.Join(ws, "catalog"), features}

	cases := []struct {
		name   string
		params attachCandidatesParams
	}{
		{"neither coordinate", attachCandidatesParams{}},
		{"both coordinates", attachCandidatesParams{Node: "checkout", GraphNode: "checkout", Release: rel}},
		{"release without a graph node", attachCandidatesParams{Node: "checkout", Release: rel}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := attachRequest(t, graphNodeServer(t, ws, dirs...), c.params)
			if resp.Error == nil {
				t.Fatalf("accepted: %s", resp.Result)
			}
			if resp.Error.Code != jsonRPCInvalidParams {
				t.Errorf("error code = %d, want %d (%+v)",
					resp.Error.Code, jsonRPCInvalidParams, resp.Error)
			}
		})
	}
}

// TestCatalogResolvedFeaturesAreCandidatesAndAreJoinable is the collection
// half, asserted together with the index it must be joinable against.
//
// The two are asserted in one test on purpose. A candidate list naming
// entities the index does not carry is not a smaller success than the pair —
// it is a client holding an identity it cannot resolve to anything
// displayable, which is why the two surfaces widen together.
func TestCatalogResolvedFeaturesAreCandidatesAndAreJoinable(t *testing.T) {
	ws, features, _ := graphNodeWorkspace(t)
	srv := graphNodeServer(t, ws, filepath.Join(ws, "catalog"), features)

	result := decodeAttachResult(t, attachRequest(t, srv,
		attachCandidatesParams{Node: "checkout"}))
	if len(result.Candidates) == 0 {
		t.Fatalf("no candidates; the workspace declares no Feature of its own, so an "+
			"empty list here means catalog-resolved Features are not being collected (%+v)", result)
	}

	index := srv.catalogIndex(context.Background())
	indexed := map[string]CatalogEntity{}
	for _, e := range index {
		indexed[e.Kind+"/"+e.Name+"/"+e.File] = e
	}
	for _, cand := range result.Candidates {
		key := cand.Kind + "/" + cand.Name + "/" + cand.File
		entry, ok := indexed[key]
		if !ok {
			t.Fatalf("candidate %+v has no entry in the entity index, so a client "+
				"cannot explain it without re-deriving anything", cand)
		}
		if entry.Origin != cand.Origin {
			t.Errorf("candidate %q origin %q disagrees with the index's %q",
				cand.Name, cand.Origin, entry.Origin)
		}
	}
}

// TestCatalogIndexReportsCatalogFeaturesWithoutAHostPath pins the disclosure
// bound on the widened index: a catalog-resolved entity is addressed by name
// and reports no filesystem path, while a workspace document still reports
// its workspace-relative one.
func TestCatalogIndexReportsCatalogFeaturesWithoutAHostPath(t *testing.T) {
	ws, features, _ := graphNodeWorkspace(t)
	srv := graphNodeServer(t, ws, filepath.Join(ws, "catalog"), features)

	var sawCatalogFeature, sawWorkspaceDoc bool
	for _, e := range srv.catalogIndex(context.Background()) {
		switch e.Origin {
		case EntityOriginCatalog:
			sawCatalogFeature = true
			if e.File != "" {
				t.Errorf("catalog entity %q reports file %q; an absolute host path "+
					"must never reach the wire", e.Name, e.File)
			}
			if filepath.IsAbs(e.File) {
				t.Errorf("catalog entity %q reports an absolute path %q", e.Name, e.File)
			}
		case EntityOriginWorkspace:
			sawWorkspaceDoc = true
			if e.File == "" {
				t.Errorf("workspace entity %q reports no file", e.Name)
			}
			if filepath.IsAbs(e.File) {
				t.Errorf("workspace entity %q reports an absolute path %q", e.Name, e.File)
			}
		default:
			t.Errorf("entity %q carries origin %q, which is neither of the two the "+
				"wire defines", e.Name, e.Origin)
		}
	}
	if !sawCatalogFeature {
		t.Error("the index carries no catalog-resolved entity, so this test proves nothing")
	}
	if !sawWorkspaceDoc {
		t.Error("the index carries no workspace entity, so this test proves nothing")
	}
}

// TestCatalogIndexPrefersTheWorkspaceCopyOfAFeature pins the deduplication
// rule for the common layout where the workspace root is itself one of the
// configured catalog directories. The Feature is reported once, as the
// workspace document, because that is the copy whose file a client can open.
func TestCatalogIndexPrefersTheWorkspaceCopyOfAFeature(t *testing.T) {
	ws, features, _ := graphNodeWorkspace(t)
	writeFixtureFile(t, filepath.Join(ws, "local-feature.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: example/local-only
  version: "1.0.0"
spec:
  appliesTo: tier=edge
`)
	// The workspace root joins the search stack, so the Feature above is
	// reachable both as a workspace document and through catalog resolution.
	srv := graphNodeServer(t, ws, ws, filepath.Join(ws, "catalog"), features)

	var seen []CatalogEntity
	for _, e := range srv.catalogIndex(context.Background()) {
		if e.Name == "example/local-only" {
			seen = append(seen, e)
		}
	}
	if len(seen) != 1 {
		t.Fatalf("example/local-only reported %d times, want 1: %+v", len(seen), seen)
	}
	if seen[0].Origin != EntityOriginWorkspace {
		t.Errorf("origin = %q, want %q — the workspace copy is the one with a file "+
			"a client can open", seen[0].Origin, EntityOriginWorkspace)
	}
	if seen[0].File != "local-feature.yaml" {
		t.Errorf("file = %q, want local-feature.yaml", seen[0].File)
	}
}
