// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"
)

// writeAttachFixture lays out a workspace holding one node and a spread of
// Features whose applicability declarations exercise each way a match can
// hold and each way it can fail.
//
// Three properties of the layout are load-bearing rather than incidental.
//
// The Features are chosen so that no single one of the selector package's
// entry points produces the expected answer on its own. anyof-excludes
// declares only appliesToAnyOf and satisfies none of its items, so a
// projection evaluating only the appliesTo string reports it as a match —
// an empty selector string constrains nothing. both-surfaces declares an
// appliesTo the node satisfies and an appliesToAnyOf it does not, so the
// same projection reports it as a match too. Either one alone would be
// reported wrongly by an implementation that reached for the convenient
// signature instead of the composition a build evaluates.
//
// two-term-miss is the near-miss for term composition inside one selector
// string: its first requirement holds against the node and its second does
// not. A shallow check that asked only whether the selector's keys are
// present on the node would report it as a match.
//
// The filenames are chosen so that sorting the Features by path yields the
// exact REVERSE of sorting them by name. Without that, path order and the
// reported order coincide and an ordering assertion passes whether the
// projection orders anything or not.
func writeAttachFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		// The node. Its labels are authored in an order no assertion
		// relies on, since a selector surface reads a label set by key.
		"node.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: api
  labels:
    tier: edge
    component.aggrega.ai/type: service
    env: prod
`,
		// MATCHES — declares neither surface, so it constrains nothing.
		"10-metrics.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: metrics
spec: {}
`,
		// MATCHES — a single appliesTo requirement the node satisfies.
		"20-edge-tls.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: edge-tls
spec:
  appliesTo: tier=edge
`,
		// MATCHES — declares only appliesToAnyOf, and the second item's
		// flat-map arm holds. Exercises the map arm and the disjunction.
		"30-anyof-only.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: anyof-only
spec:
  appliesToAnyOf:
    - tier=data
    - tier: edge
`,
		// DOES NOT MATCH — a clean miss on a single requirement.
		"40-db-backup.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: db-backup
spec:
  appliesTo: tier=data
`,
		// DOES NOT MATCH — near-miss on term composition within one
		// selector string: tier holds, env does not.
		"50-two-term-miss.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: two-term-miss
spec:
  appliesTo: tier=edge,env=staging
`,
		// DOES NOT MATCH — near-miss across the two surfaces: appliesTo
		// holds, appliesToAnyOf does not. Reported as a match by anything
		// that evaluates only appliesTo.
		"60-both-surfaces.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: both-surfaces
spec:
  appliesTo: tier=edge
  appliesToAnyOf:
    - env=staging
    - tier: data
`,
		// DOES NOT MATCH — declares only appliesToAnyOf and satisfies no
		// item. Reported as a match by anything that evaluates only
		// appliesTo, because an absent appliesTo constrains nothing.
		"70-anyof-excludes.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: anyof-excludes
spec:
  appliesToAnyOf:
    - tier=data
    - env: staging
`,
		// DOES NOT MATCH — the selector cannot be parsed at all, so there
		// is no declaration to evaluate. An unparseable selector must not
		// widen what is offered.
		"80-malformed.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: malformed
spec:
  appliesTo: "tier in (edge"
`,
		// DOES NOT MATCH — appliesTo is authored as a mapping, which has no
		// string form to evaluate. Reading a non-scalar as absent would let
		// a mis-authored surface constrain nothing and so be offered
		// everywhere, which is the wrong direction for malformed input.
		"85-appliesto-not-scalar.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: appliesto-not-scalar
spec:
  appliesTo:
    tier: edge
`,
		// DOES NOT MATCH — appliesToAnyOf is authored as a scalar rather
		// than a list, which the selector term cannot read.
		"86-anyof-not-a-list.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: anyof-not-a-list
spec:
  appliesToAnyOf: tier=edge
`,
		// Not a candidate: a Component is not something that attaches to a
		// node, however its own labels read.
		"90-other-component.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: sidecar
  labels:
    tier: edge
`,
	}
	for rel, content := range files {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}

// attachFixtureAllFiles are every document the fixture writes, used to
// preload the parse cache so both traversal passes are live.
var attachFixtureAllFiles = []string{
	"node.yaml",
	"10-metrics.yaml",
	"20-edge-tls.yaml",
	"30-anyof-only.yaml",
	"40-db-backup.yaml",
	"50-two-term-miss.yaml",
	"60-both-surfaces.yaml",
	"70-anyof-excludes.yaml",
	"80-malformed.yaml",
	"85-appliesto-not-scalar.yaml",
	"86-anyof-not-a-list.yaml",
	"90-other-component.yaml",
}

// TestAttachCandidatesOnACancelledRequestDoesNotReportTheNodeAsUndeclared
// pins that a cancelled request reports the cancellation rather than a
// conclusion no completed read reached. Without the entry check, the
// traversal would yield nothing under a cancelled context and the node
// would come back reported as one the workspace does not declare — a
// definite answer produced by a read that never ran.
//
// What it covers is the entry check, which is where cancellation is
// answered. The narrower case of a cancellation arriving mid-traversal is
// not covered here and is not defended against in the handler either: the
// in-memory harness cannot cancel between the entry check and the
// traversal without either a production hook or a wall-clock race, and
// shipping a guard no test can reach would have been unexercised code
// rather than coverage.
func TestAttachCandidatesOnACancelledRequestDoesNotReportTheNodeAsUndeclared(t *testing.T) {
	srv := newTestServer(t, writeAttachFixture(t), &bytes.Buffer{}, &bytes.Buffer{})

	// Cancel the session context before the request runs. The per-request
	// context handle() derives is a child of it, so the handler observes a
	// cancelled context on entry.
	srv.serverCancel()

	in := bytes.NewBuffer(framedRequest(1, "aggrega/attachCandidates", map[string]any{"node": "api"}))
	out := &bytes.Buffer{}
	resp := runOneRequest(t, srv, in, out)
	if resp.Error == nil {
		t.Fatalf("a cancelled request returned a result: %s", resp.Result)
	}
	if resp.Error.Code == errEntityNotResolvable {
		t.Errorf("a cancelled request reported the node as undeclared, which the interrupted read never established: %+v", resp.Error)
	}
	if resp.Error.Code != jsonRPCInternalError {
		t.Errorf("cancelled request error code = %d, want %d", resp.Error.Code, jsonRPCInternalError)
	}
}

// attachCandidatesOverWire drives one real aggrega/attachCandidates
// request through the server's message loop and returns the raw Result
// bytes plus the generically-decoded response.
//
// The result is decoded into map[string]any rather than into
// attachCandidatesResult on purpose: decoding into the response type reads
// every field back through the same struct tags that wrote it, so a
// mistagged field round-trips cleanly and the assertion passes while no
// client ever receives the value. Indexing by the literal wire key is what
// makes the field names themselves the thing under test.
func attachCandidatesOverWire(t *testing.T, srv *Server, node string) ([]byte, map[string]any, []map[string]any) {
	t.Helper()
	in := bytes.NewBuffer(framedRequest(1, "aggrega/attachCandidates", map[string]any{"node": node}))
	out := &bytes.Buffer{}
	resp := runOneRequest(t, srv, in, out)
	if resp.Error != nil {
		t.Fatalf("attachCandidates returned an error: %+v", resp.Error)
	}
	var result map[string]any
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v (body: %s)", err, resp.Result)
	}
	raw, ok := result["candidates"]
	if !ok {
		t.Fatalf(`result carries no "candidates" key: %s`, resp.Result)
	}
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf(`"candidates" is not an array: %s`, resp.Result)
	}
	candidates := make([]map[string]any, 0, len(list))
	for i, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("candidates[%d] is not an object: %s", i, resp.Result)
		}
		candidates = append(candidates, entry)
	}
	return resp.Result, result, candidates
}

// candidateNames projects the decoded candidate list down to the names, in
// the order the response reported them.
func candidateNames(candidates []map[string]any) []string {
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		name, _ := c["name"].(string)
		out = append(out, name)
	}
	return out
}

// TestAttachCandidatesReportsSelectorMatchedFeaturesThroughTheRealDispatchPath
// is the positive half of the acceptance: an actual
// aggrega/attachCandidates request, routed through the daemon's own method
// table, comes back naming exactly the Features whose declared
// applicability the node's labels satisfy — read off the marshalled JSON by
// the literal keys an integration author types.
func TestAttachCandidatesReportsSelectorMatchedFeaturesThroughTheRealDispatchPath(t *testing.T) {
	srv := newTestServer(t, writeAttachFixture(t), &bytes.Buffer{}, &bytes.Buffer{})
	body, result, candidates := attachCandidatesOverWire(t, srv, "api")

	if result["node"] != "api" {
		t.Errorf("result[node] = %v, want %q", result["node"], "api")
	}

	// The labels the match was evaluated against are on the wire, so the
	// answer is auditable from the response alone.
	labels, ok := result["nodeLabels"].(map[string]any)
	if !ok {
		t.Fatalf("result[nodeLabels] is not an object: %v", result["nodeLabels"])
	}
	wantLabels := map[string]string{
		"tier":                      "edge",
		"component.aggrega.ai/type": "service",
		"env":                       "prod",
	}
	if len(labels) != len(wantLabels) {
		t.Errorf("result[nodeLabels] = %v, want %v", labels, wantLabels)
	}
	for k, v := range wantLabels {
		if labels[k] != v {
			t.Errorf("result[nodeLabels][%q] = %v, want %q", k, labels[k], v)
		}
	}

	// Ordering is by kind, then name, then file. Every candidate is a
	// Feature, so name decides — and this sequence is the reverse of the
	// fixture's path order for these three.
	want := []struct {
		kind string
		name string
		file string
	}{
		{"Feature", "anyof-only", "30-anyof-only.yaml"},
		{"Feature", "edge-tls", "20-edge-tls.yaml"},
		{"Feature", "metrics", "10-metrics.yaml"},
	}
	if len(candidates) != len(want) {
		t.Fatalf("got %d candidates %v, want %d: %s",
			len(candidates), candidateNames(candidates), len(want), body)
	}
	for i, w := range want {
		got := candidates[i]
		if got["kind"] != w.kind {
			t.Errorf("candidates[%d][kind] = %v, want %q", i, got["kind"], w.kind)
		}
		if got["name"] != w.name {
			t.Errorf("candidates[%d][name] = %v, want %q", i, got["name"], w.name)
		}
		if got["file"] != w.file {
			t.Errorf("candidates[%d][file] = %v, want %q", i, got["file"], w.file)
		}
	}
}

// TestAttachCandidatesExcludesFeaturesTheNodeDoesNotSatisfy is the other
// half of "returns exactly", and it is the half that actually proves the
// matching runs: a test showing only that matching Features are present
// passes on a projection that returns every Feature in the workspace.
//
// Each row names one way a match fails, and two of them are the rows that
// pin the composition rather than any single entry point. anyof-excludes
// and both-surfaces are each reported as a match by a projection that
// evaluates only the appliesTo string, so their absence here is what
// establishes that appliesToAnyOf is evaluated too.
func TestAttachCandidatesExcludesFeaturesTheNodeDoesNotSatisfy(t *testing.T) {
	srv := newTestServer(t, writeAttachFixture(t), &bytes.Buffer{}, &bytes.Buffer{})
	body, _, candidates := attachCandidatesOverWire(t, srv, "api")

	present := map[string]bool{}
	for _, name := range candidateNames(candidates) {
		present[name] = true
	}

	excluded := []struct {
		name string
		why  string
	}{
		{"db-backup", "its single appliesTo requirement does not hold"},
		{"two-term-miss", "the second requirement inside its appliesTo string does not hold"},
		{"both-surfaces", "its appliesTo holds but no appliesToAnyOf item does"},
		{"anyof-excludes", "it declares only appliesToAnyOf and satisfies no item"},
		{"malformed", "its selector cannot be parsed, so there is no declaration to evaluate"},
		{"appliesto-not-scalar", "its appliesTo is a mapping, which has no string form to evaluate"},
		{"anyof-not-a-list", "its appliesToAnyOf is a scalar, which the selector term cannot read"},
		{"sidecar", "a Component is not a candidate however its own labels read"},
	}
	for _, e := range excluded {
		if present[e.name] {
			t.Errorf("%q reached the candidate list, but %s: %s", e.name, e.why, body)
		}
	}

	// Positive control for the six assertions above: the fixture's
	// matching Features ARE present, so a green above reflects exclusion
	// rather than an empty list.
	for _, name := range []string{"metrics", "edge-tls", "anyof-only"} {
		if !present[name] {
			t.Errorf("%q is absent, so the exclusion assertions above prove nothing: %s", name, body)
		}
	}
}

// TestAttachCandidatesOrderIsTheSharedEntityOrderRatherThanPathOrder is
// the determinism assertion, and it is discriminating rather than
// decorative because the fixture's path order is the REVERSE of the
// reported order for the three matching Features. Removing the sort from
// the projection makes the response report them in path order, which this
// table does not admit.
//
// The traversal itself delivers deterministically — the cache pass sorts
// by path and the on-disk walk visits lexically — so repeated identical
// requests would return identical bytes with no ordering imposed at all.
// That is exactly why byte-stability across calls is a backstop here and
// this assertion is the discriminator: the property worth pinning is that
// the reported order is a function of the workspace's content and not of
// its file names.
func TestAttachCandidatesOrderIsTheSharedEntityOrderRatherThanPathOrder(t *testing.T) {
	root := writeAttachFixture(t)
	srv := newTestServer(t, root, &bytes.Buffer{}, &bytes.Buffer{})

	byName := []string{"anyof-only", "edge-tls", "metrics"}
	byPath := []string{"metrics", "edge-tls", "anyof-only"}

	// Guard the fixture's own load-bearing property: if the two orders
	// ever coincide, the assertion below stops discriminating and this
	// says so instead of passing quietly.
	same := true
	for i := range byName {
		if byName[i] != byPath[i] {
			same = false
			break
		}
	}
	if same {
		t.Fatal("the fixture's path order and name order coincide, so this test can no longer detect a missing sort")
	}

	_, _, candidates := attachCandidatesOverWire(t, srv, "api")
	got := candidateNames(candidates)
	if len(got) != len(byName) {
		t.Fatalf("got %v, want %v", got, byName)
	}
	for i := range byName {
		if got[i] != byName[i] {
			t.Fatalf("candidate order = %v, want %v (path order would be %v)", got, byName, byPath)
		}
	}
}

// TestAttachCandidatesIsByteStableAcrossRepeatedRequests is the backstop
// the ordering assertion above names: whatever else moves, two identical
// requests against an unchanged workspace produce identical bytes. The
// parse cache is loaded first so both traversal passes are live and the
// cache's own map iteration has several entries to permute.
func TestAttachCandidatesIsByteStableAcrossRepeatedRequests(t *testing.T) {
	root := writeAttachFixture(t)
	srv := newTestServer(t, root, &bytes.Buffer{}, &bytes.Buffer{})

	for _, rel := range attachFixtureAllFiles {
		abs := filepath.Join(root, rel)
		content, err := os.ReadFile(abs)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(content, &doc); err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		srv.parseCache.Put(abs, &doc, content)
	}

	first, _, _ := attachCandidatesOverWire(t, srv, "api")
	for i := 2; i <= 12; i++ {
		again, _, _ := attachCandidatesOverWire(t, srv, "api")
		if !bytes.Equal(first, again) {
			t.Fatalf("request %d returned different bytes\nfirst: %s\nagain: %s", i, first, again)
		}
	}
}

// TestAttachCandidatesIsRoutedByTheMethodTable pins that the method is
// reachable at all. A handler present in the package but absent from the
// routing table compiles, and every direct-call test of it passes, while no
// client can ever invoke it — so this asserts the negative case too: an
// unrouted sibling name is rejected as unknown.
func TestAttachCandidatesIsRoutedByTheMethodTable(t *testing.T) {
	srv := newTestServer(t, writeAttachFixture(t), &bytes.Buffer{}, &bytes.Buffer{})

	in := bytes.NewBuffer(framedRequest(1, "aggrega/attachCandidates", map[string]any{"node": "api"}))
	out := &bytes.Buffer{}
	if resp := runOneRequest(t, srv, in, out); resp.Error != nil {
		t.Fatalf("aggrega/attachCandidates is not routed: %+v", resp.Error)
	}

	// Positive control for the assertion above: the same harness DOES
	// report an error for a method the table does not carry, so the green
	// above reflects routing rather than an unchecked field.
	in = bytes.NewBuffer(framedRequest(2, "aggrega/attachCandidate", map[string]any{"node": "api"}))
	out = &bytes.Buffer{}
	resp := runOneRequest(t, srv, in, out)
	if resp.Error == nil {
		t.Fatal("an unrouted method name was accepted, so the routing assertion above proves nothing")
	}
	if resp.Error.Code != jsonRPCMethodNotFound {
		t.Errorf("unrouted method error code = %d, want %d", resp.Error.Code, jsonRPCMethodNotFound)
	}
}

// TestAttachCandidatesDistinguishesAnUnknownNodeFromANodeWithNoCandidates
// pins that the two answers stay apart on the wire. Collapsing them into an
// empty array would report a typo as a node with nothing to offer.
func TestAttachCandidatesDistinguishesAnUnknownNodeFromANodeWithNoCandidates(t *testing.T) {
	root := writeAttachFixture(t)

	// A name the workspace does not declare is an error.
	srv := newTestServer(t, root, &bytes.Buffer{}, &bytes.Buffer{})
	in := bytes.NewBuffer(framedRequest(1, "aggrega/attachCandidates", map[string]any{"node": "nope"}))
	out := &bytes.Buffer{}
	resp := runOneRequest(t, srv, in, out)
	if resp.Error == nil {
		t.Fatalf("an undeclared node name was accepted: %s", resp.Result)
	}
	if resp.Error.Code != errEntityNotResolvable {
		t.Errorf("undeclared node error code = %d, want %d", resp.Error.Code, errEntityNotResolvable)
	}

	// A declared node whose labels satisfy no Feature's applicability is a
	// successful response carrying an empty array — and it is an array
	// rather than null, so a client iterates without a nil check.
	//
	// sidecar carries only tier=edge, which satisfies edge-tls, metrics and
	// anyof-only — so the no-candidate case needs a node of its own.
	bare := filepath.Join(root, "95-bare.yaml")
	content := `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: bare
  labels:
    tier: nothing-matches-this
`
	if err := os.WriteFile(bare, []byte(content), 0o644); err != nil {
		t.Fatalf("write bare node: %v", err)
	}
	srv = newTestServer(t, root, &bytes.Buffer{}, &bytes.Buffer{})
	body, _, candidates := attachCandidatesOverWire(t, srv, "bare")
	// metrics declares no applicability at all, so it is a candidate for
	// every node. anyof-only and edge-tls both require tier=edge.
	if names := candidateNames(candidates); len(names) != 1 || names[0] != "metrics" {
		t.Fatalf("bare node candidates = %v, want just the unconstrained Feature: %s", names, body)
	}
}

// TestAttachCandidatesRejectsARequestNamingNoNode pins that the node
// parameter is required: without one there is no label set to match
// against, and answering with the whole Feature list would be the
// unfiltered offer this method exists to narrow.
func TestAttachCandidatesRejectsARequestNamingNoNode(t *testing.T) {
	srv := newTestServer(t, writeAttachFixture(t), &bytes.Buffer{}, &bytes.Buffer{})
	for _, params := range []map[string]any{{}, {"node": ""}} {
		in := bytes.NewBuffer(framedRequest(1, "aggrega/attachCandidates", params))
		out := &bytes.Buffer{}
		resp := runOneRequest(t, srv, in, out)
		if resp.Error == nil {
			t.Fatalf("params %v was accepted: %s", params, resp.Result)
		}
		if resp.Error.Code != jsonRPCInvalidParams {
			t.Errorf("params %v error code = %d, want %d", params, resp.Error.Code, jsonRPCInvalidParams)
		}
	}
}

// TestAttachCandidatesOnAnEmptyWorkspaceReportsTheNodeAsUndeclared pins
// that a workspace with nothing in it reports the requested node as
// undeclared rather than as a node with no candidates.
func TestAttachCandidatesOnAnEmptyWorkspaceReportsTheNodeAsUndeclared(t *testing.T) {
	srv := newTestServer(t, t.TempDir(), &bytes.Buffer{}, &bytes.Buffer{})
	in := bytes.NewBuffer(framedRequest(1, "aggrega/attachCandidates", map[string]any{"node": "api"}))
	out := &bytes.Buffer{}
	resp := runOneRequest(t, srv, in, out)
	if resp.Error == nil {
		t.Fatalf("an empty workspace accepted a node name: %s", resp.Result)
	}
	if resp.Error.Code != errEntityNotResolvable {
		t.Errorf("error code = %d, want %d", resp.Error.Code, errEntityNotResolvable)
	}
}

// TestAttachCandidatesExcludesDocumentsOutsideTheWorkspaceRoot pins that a
// parse-cache entry for a file the editor sent from outside the workspace
// is not workspace-local and reaches neither the node lookup nor the
// candidate list — which also keeps absolute host paths off the wire.
func TestAttachCandidatesExcludesDocumentsOutsideTheWorkspaceRoot(t *testing.T) {
	root := writeAttachFixture(t)
	outside := filepath.Join(t.TempDir(), "elsewhere.yaml")
	srv := newTestServer(t, root, &bytes.Buffer{}, &bytes.Buffer{})

	content := `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: elsewhere
spec:
  appliesTo: tier=edge
`
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	srv.parseCache.Put(outside, &doc, []byte(content))

	body, _, candidates := attachCandidatesOverWire(t, srv, "api")
	for _, name := range candidateNames(candidates) {
		if name == "elsewhere" {
			t.Errorf("a Feature outside the workspace root reached the candidates: %s", body)
		}
	}
	if bytes.Contains(body, []byte(outside)) {
		t.Errorf("an absolute path outside the workspace root reached the wire: %s", body)
	}
}
