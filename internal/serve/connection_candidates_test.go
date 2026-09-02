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

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// writeConnectionFixture lays out a workspace holding one consuming
// Component whose usesSchema exercises each way a socket's declared
// constraint can hold and each way it can fail, plus the Components that
// do and do not satisfy them.
//
// Four properties of the layout are load-bearing rather than incidental.
//
// The sockets are chosen so that no shallow projection produces the
// expected answer. `unconstrained` declares no selector, so a projection
// that treated an absent constraint as admitting nothing would report it
// empty when the correct answer is every Component. `impossible` declares
// a constraint no Component satisfies, so a projection that ignored the
// selector would report it full. `two-term` holds on its first
// requirement and fails on its second, so a check asking only whether the
// selector's keys are present would report a match. `malformed` cannot be
// parsed at all, so a projection treating an unparseable constraint as
// absent would offer every Component through it — the wrong direction.
//
// The consuming Component is itself a Component and therefore its own
// candidate target, which pins that self-targeting is not silently
// filtered out.
//
// The Feature carries a usesSchema too, so the surface whose sockets are
// closed can be addressed by the same request shape.
//
// The filenames are chosen so that sorting the target Components by path
// yields the exact REVERSE of sorting them by name. Without that, path
// order and reported order coincide and an ordering assertion passes
// whether the projection orders anything or not.
func writeConnectionFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		// The consuming entity. Socket order here is the author order the
		// response is asserted to preserve, and it is deliberately not
		// alphabetical.
		"consumer.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: api
  labels:
    tier: edge
spec:
  usesSchema:
    store:
      selector: component.aggrega.ai/type=database
      required: true
    unconstrained: {}
    impossible:
      selector: tier=no-such-tier
    two-term:
      selector: component.aggrega.ai/type=database,env=staging
    malformed:
      selector: "tier in (edge"
`,
		// A target satisfying `store` and `two-term`'s first requirement
		// only. 90- so path order reverses name order.
		"90-postgres.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: postgres
  labels:
    component.aggrega.ai/type: database
    env: prod
`,
		// A second target satisfying `store`, so the socket's answer is a
		// set rather than a single entity.
		"80-mysql.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: mysql
  labels:
    component.aggrega.ai/type: database
    env: prod
`,
		// Satisfies no declared constraint but is still a candidate for the
		// unconstrained socket.
		"70-cache.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: keyvalue
  labels:
    component.aggrega.ai/type: cache
`,
		// A Feature. Its sockets are closed, so it is the entity that
		// reports adHocSocketsAccepted false. It is not a candidate target
		// either, however its labels read.
		"60-mesh.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: mesh
  labels:
    component.aggrega.ai/type: database
spec:
  usesSchema:
    telemetry:
      selector: component.aggrega.ai/type=cache
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

// connectionFixtureAllFiles are every document the fixture writes, used to
// preload the parse cache so both traversal passes are live.
var connectionFixtureAllFiles = []string{
	"consumer.yaml",
	"90-postgres.yaml",
	"80-mysql.yaml",
	"70-cache.yaml",
	"60-mesh.yaml",
}

// connectionCandidatesOverWire drives one real
// aggrega/connectionCandidates request through the server's message loop
// and returns the raw Result bytes plus the generically-decoded response.
//
// The result is decoded into map[string]any rather than into
// connectionCandidatesResult on purpose: decoding into the response type
// reads every field back through the same struct tags that wrote it, so a
// mistagged field round-trips cleanly and the assertion passes while no
// client ever receives the value. Indexing by the literal wire key is what
// makes the field names themselves the thing under test.
func connectionCandidatesOverWire(t *testing.T, srv *Server, params map[string]any) ([]byte, map[string]any, []map[string]any) {
	t.Helper()
	in := bytes.NewBuffer(framedRequest(1, "aggrega/connectionCandidates", params))
	out := &bytes.Buffer{}
	resp := runOneRequest(t, srv, in, out)
	if resp.Error != nil {
		t.Fatalf("connectionCandidates returned an error: %+v", resp.Error)
	}
	var result map[string]any
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v (body: %s)", err, resp.Result)
	}
	raw, ok := result["sockets"]
	if !ok {
		t.Fatalf(`result carries no "sockets" key: %s`, resp.Result)
	}
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf(`"sockets" is not an array: %s`, resp.Result)
	}
	sockets := make([]map[string]any, 0, len(list))
	for i, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("sockets[%d] is not an object: %s", i, resp.Result)
		}
		sockets = append(sockets, entry)
	}
	return resp.Result, result, sockets
}

// socketNamed returns the reported candidacy for one socket name.
func socketNamed(t *testing.T, sockets []map[string]any, name string) map[string]any {
	t.Helper()
	for _, s := range sockets {
		if s["socket"] == name {
			return s
		}
	}
	t.Fatalf("no socket named %q in the response", name)
	return nil
}

// targetNames projects a reported socket's target list down to the names,
// in the order the response reported them.
func targetNames(t *testing.T, socket map[string]any) []string {
	t.Helper()
	raw, ok := socket["targets"]
	if !ok {
		t.Fatalf(`socket %v carries no "targets" key`, socket["socket"])
	}
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf(`socket %v "targets" is not an array: %v`, socket["socket"], raw)
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("a target is not an object: %v", item)
		}
		name, _ := entry["name"].(string)
		out = append(out, name)
	}
	return out
}

// TestConnectionCandidatesReportsDeclaredSocketsAndTheirTargetsThroughTheRealDispatchPath
// is the positive half of the acceptance: an actual
// aggrega/connectionCandidates request, routed through the daemon's own
// method table, comes back naming the sockets the entity declares and, per
// socket, exactly the Components whose declared labels satisfy what that
// socket declares — read off the marshalled JSON by the literal keys an
// integration author types.
func TestConnectionCandidatesReportsDeclaredSocketsAndTheirTargetsThroughTheRealDispatchPath(t *testing.T) {
	srv := newTestServer(t, writeConnectionFixture(t), &bytes.Buffer{}, &bytes.Buffer{})
	body, result, sockets := connectionCandidatesOverWire(t, srv, map[string]any{"node": "api"})

	if result["node"] != "api" {
		t.Errorf("result[node] = %v, want %q", result["node"], "api")
	}

	// A Component's own dependency surface accepts a socket its catalog
	// entity never declared, so the socket list is open and the response
	// says so.
	if result["adHocSocketsAccepted"] != true {
		t.Errorf("result[adHocSocketsAccepted] = %v, want true for a Component: %s",
			result["adHocSocketsAccepted"], body)
	}

	// Sockets arrive in the order the entity declares them, which the
	// fixture authors non-alphabetically on purpose.
	var got []string
	for _, s := range sockets {
		name, _ := s["socket"].(string)
		got = append(got, name)
	}
	want := []string{"store", "unconstrained", "impossible", "two-term", "malformed"}
	if len(got) != len(want) {
		t.Fatalf("sockets = %v, want %v: %s", got, want, body)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sockets = %v, want author order %v: %s", got, want, body)
		}
	}

	// Every declared socket reports itself as declared, and carries the
	// constraint its targets were computed from, so the answer is
	// auditable from the response alone.
	store := socketNamed(t, sockets, "store")
	if store["declaration"] != types.SocketDeclarationDeclared {
		t.Errorf("store[declaration] = %v, want %q", store["declaration"], types.SocketDeclarationDeclared)
	}
	if store["selector"] != "component.aggrega.ai/type=database" {
		t.Errorf("store[selector] = %v, want the declared constraint: %s", store["selector"], body)
	}
	if store["required"] != true {
		t.Errorf("store[required] = %v, want true: %s", store["required"], body)
	}
	// Both databases satisfy it, ordered by the shared entity order rather
	// than by the reversed path order the fixture writes.
	if names := targetNames(t, store); len(names) != 2 || names[0] != "mysql" || names[1] != "postgres" {
		t.Fatalf("store targets = %v, want [mysql postgres]: %s", names, body)
	}

	// An absent constraint admits every Component — including the
	// consuming entity itself, which is a Component and is not filtered.
	unconstrained := socketNamed(t, sockets, "unconstrained")
	if unconstrained["selector"] != "" {
		t.Errorf("unconstrained[selector] = %v, want empty", unconstrained["selector"])
	}
	names := targetNames(t, unconstrained)
	if len(names) != 4 {
		t.Fatalf("unconstrained targets = %v, want every Component: %s", names, body)
	}
	for _, want := range []string{"api", "keyvalue", "mysql", "postgres"} {
		var found bool
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("unconstrained targets = %v, missing %q: %s", names, want, body)
		}
	}

	// The real selector composition, not a shallow key-presence check:
	// two-term's first requirement holds against both databases and its
	// second does not, so it admits neither.
	if names := targetNames(t, socketNamed(t, sockets, "two-term")); len(names) != 0 {
		t.Errorf("two-term targets = %v, want none — the second requirement fails: %s", names, body)
	}

	// An unparseable constraint admits nothing rather than everything: a
	// mis-authored selector must not widen what is offered.
	if names := targetNames(t, socketNamed(t, sockets, "malformed")); len(names) != 0 {
		t.Errorf("malformed targets = %v, want none: %s", names, body)
	}

	// A Feature is not a candidate target, however its own labels read —
	// mesh carries component.aggrega.ai/type=database and satisfies
	// store's constraint, so its absence is the kind filter working.
	if bytes.Contains(body, []byte(`"mesh"`)) {
		t.Errorf("a Feature reached a target list: %s", body)
	}
}

// TestConnectionCandidatesReportsADeclaredSocketWithNoValidTargets pins
// the second of the two answers a client has to be able to tell apart: a
// socket the entity declares whose constraint no Component satisfies comes
// back present, declared, and carrying an empty array rather than null or
// an absent key. A socket dropped from the response because nothing
// satisfied it would read as a socket the entity does not declare.
func TestConnectionCandidatesReportsADeclaredSocketWithNoValidTargets(t *testing.T) {
	srv := newTestServer(t, writeConnectionFixture(t), &bytes.Buffer{}, &bytes.Buffer{})
	body, _, sockets := connectionCandidatesOverWire(t, srv,
		map[string]any{"node": "api", "socket": "impossible"})

	if len(sockets) != 1 {
		t.Fatalf("a named socket returned %d entries, want 1: %s", len(sockets), body)
	}
	impossible := socketNamed(t, sockets, "impossible")
	if impossible["declaration"] != types.SocketDeclarationDeclared {
		t.Errorf("impossible[declaration] = %v, want %q: %s",
			impossible["declaration"], types.SocketDeclarationDeclared, body)
	}
	if impossible["selector"] != "tier=no-such-tier" {
		t.Errorf("impossible[selector] = %v, want the declared constraint: %s", impossible["selector"], body)
	}
	if names := targetNames(t, impossible); len(names) != 0 {
		t.Errorf("impossible targets = %v, want none: %s", names, body)
	}
	// An empty array, not null: a client iterates without a nil check.
	if !bytes.Contains(body, []byte(`"targets":[]`)) {
		t.Errorf("an empty target list is not an empty array on the wire: %s", body)
	}
}

// TestConnectionCandidatesReportsAnAdHocSocketAsAdHocRatherThanRefusingIt
// is the case that proves the honesty requirement. A socket the entity's
// own declaration does not list is one an author may legitimately write,
// so the response answers for it — and marks it, so a client can tell it
// from a declared one rather than presenting both as one filtered list.
//
// It asserts the distinction in both directions from a single fixture: the
// ad-hoc socket and a declared socket on the same entity report different
// values under the same key. A response that marked everything the same
// way would pass a one-sided assertion.
func TestConnectionCandidatesReportsAnAdHocSocketAsAdHocRatherThanRefusingIt(t *testing.T) {
	srv := newTestServer(t, writeConnectionFixture(t), &bytes.Buffer{}, &bytes.Buffer{})

	body, result, sockets := connectionCandidatesOverWire(t, srv,
		map[string]any{"node": "api", "socket": "invented-by-the-author"})

	if len(sockets) != 1 {
		t.Fatalf("a named socket returned %d entries, want 1: %s", len(sockets), body)
	}
	adHoc := sockets[0]
	if adHoc["socket"] != "invented-by-the-author" {
		t.Errorf("the response renamed the requested socket: %s", body)
	}
	if adHoc["declaration"] != types.SocketDeclarationAdHoc {
		t.Fatalf("an undeclared socket reports declaration %v, want %q — a client cannot tell it "+
			"from a declared one: %s", adHoc["declaration"], types.SocketDeclarationAdHoc, body)
	}
	// The entity declares no constraint for a socket it never declared, so
	// every Component is a valid target — which is the answer a build
	// reaches for the same socket, an unconstrained one.
	if adHoc["selector"] != "" {
		t.Errorf("an undeclared socket reports a constraint %v, want none: %s", adHoc["selector"], body)
	}
	if names := targetNames(t, adHoc); len(names) != 4 {
		t.Errorf("ad-hoc socket targets = %v, want every Component: %s", names, body)
	}
	if result["adHocSocketsAccepted"] != true {
		t.Errorf("result[adHocSocketsAccepted] = %v, want true: %s", result["adHocSocketsAccepted"], body)
	}

	// The other direction, so the marker is discriminating rather than
	// constant: a declared socket on the same entity reports the other
	// value.
	_, _, declaredOnly := connectionCandidatesOverWire(t, srv,
		map[string]any{"node": "api", "socket": "store"})
	if got := declaredOnly[0]["declaration"]; got != types.SocketDeclarationDeclared {
		t.Fatalf("a declared socket reports declaration %v, want %q — the marker does not discriminate",
			got, types.SocketDeclarationDeclared)
	}
}

// TestConnectionCandidatesReportsAClosedSocketSurfaceAsClosed pins that
// adHocSocketsAccepted is a measured property of the addressed entity
// rather than a constant true. A Feature's attachment surface accepts only
// the sockets that Feature declares, so an undeclared socket there is one
// the build refuses outright — and the response offers no target through
// it, because a target list is the part a client acts on.
func TestConnectionCandidatesReportsAClosedSocketSurfaceAsClosed(t *testing.T) {
	srv := newTestServer(t, writeConnectionFixture(t), &bytes.Buffer{}, &bytes.Buffer{})

	// The declared socket on the closed surface still answers normally.
	body, result, sockets := connectionCandidatesOverWire(t, srv, map[string]any{"node": "mesh"})
	if result["adHocSocketsAccepted"] != false {
		t.Fatalf("result[adHocSocketsAccepted] = %v, want false for a Feature: %s",
			result["adHocSocketsAccepted"], body)
	}
	telemetry := socketNamed(t, sockets, "telemetry")
	if telemetry["declaration"] != types.SocketDeclarationDeclared {
		t.Errorf("telemetry[declaration] = %v, want %q", telemetry["declaration"], types.SocketDeclarationDeclared)
	}
	if names := targetNames(t, telemetry); len(names) != 1 || names[0] != "keyvalue" {
		t.Errorf("telemetry targets = %v, want [keyvalue]: %s", names, body)
	}

	// An undeclared socket on that surface is marked ad-hoc and carries no
	// target, because no connection through it is legal.
	body, _, sockets = connectionCandidatesOverWire(t, srv,
		map[string]any{"node": "mesh", "socket": "invented"})
	if got := sockets[0]["declaration"]; got != types.SocketDeclarationAdHoc {
		t.Errorf("declaration = %v, want %q", got, types.SocketDeclarationAdHoc)
	}
	if names := targetNames(t, sockets[0]); len(names) != 0 {
		t.Errorf("an undeclared socket on a closed surface offers targets %v, want none: %s", names, body)
	}
}

// TestConnectionCandidatesReportsAnEntityDeclaringNoSocketsAsAnEmptyList
// pins that an entity with no dependency surface is a successful answer
// carrying an empty array, not an error and not null. Its sockets are
// genuinely none; that is different from the entity not existing, which is
// the error case below.
func TestConnectionCandidatesReportsAnEntityDeclaringNoSocketsAsAnEmptyList(t *testing.T) {
	srv := newTestServer(t, writeConnectionFixture(t), &bytes.Buffer{}, &bytes.Buffer{})
	body, _, sockets := connectionCandidatesOverWire(t, srv, map[string]any{"node": "postgres"})
	if len(sockets) != 0 {
		t.Fatalf("an entity declaring no usesSchema reported %d socket(s): %s", len(sockets), body)
	}
	if !bytes.Contains(body, []byte(`"sockets":[]`)) {
		t.Errorf("an empty socket list is not an empty array on the wire: %s", body)
	}

	// Naming a socket on an entity that declares no dependency surface at
	// all reads the declaration set at its zero value, which is a distinct
	// path from reading a populated one. It answers ad-hoc rather than
	// failing: the entity is a Component, so an author may wire a socket on
	// it, and the entity simply declares none yet.
	body, result, sockets := connectionCandidatesOverWire(t, srv,
		map[string]any{"node": "postgres", "socket": "store"})
	if len(sockets) != 1 {
		t.Fatalf("a named socket on an entity with no usesSchema returned %d entries, want 1: %s",
			len(sockets), body)
	}
	if got := sockets[0]["declaration"]; got != types.SocketDeclarationAdHoc {
		t.Errorf("declaration = %v, want %q: %s", got, types.SocketDeclarationAdHoc, body)
	}
	if result["adHocSocketsAccepted"] != true {
		t.Errorf("result[adHocSocketsAccepted] = %v, want true: %s", result["adHocSocketsAccepted"], body)
	}
	if names := targetNames(t, sockets[0]); len(names) != 4 {
		t.Errorf("targets = %v, want every Component: %s", names, body)
	}
}

// TestConnectionCandidatesIsRoutedByTheMethodTable pins that the method is
// reachable at all. A handler present in the package but absent from the
// routing table compiles, and every direct-call test of it passes, while
// no client can ever invoke it — so this asserts the negative case too: an
// unrouted sibling name is rejected as unknown.
func TestConnectionCandidatesIsRoutedByTheMethodTable(t *testing.T) {
	srv := newTestServer(t, writeConnectionFixture(t), &bytes.Buffer{}, &bytes.Buffer{})

	in := bytes.NewBuffer(framedRequest(1, "aggrega/connectionCandidates", map[string]any{"node": "api"}))
	out := &bytes.Buffer{}
	if resp := runOneRequest(t, srv, in, out); resp.Error != nil {
		t.Fatalf("aggrega/connectionCandidates is not routed: %+v", resp.Error)
	}

	// Positive control for the assertion above: the same harness DOES
	// report an error for a method the table does not carry, so the green
	// above reflects routing rather than an unchecked field.
	in = bytes.NewBuffer(framedRequest(2, "aggrega/connectionCandidate", map[string]any{"node": "api"}))
	out = &bytes.Buffer{}
	resp := runOneRequest(t, srv, in, out)
	if resp.Error == nil {
		t.Fatal("an unrouted method name was accepted, so the routing assertion above proves nothing")
	}
	if resp.Error.Code != jsonRPCMethodNotFound {
		t.Errorf("unrouted method error code = %d, want %d", resp.Error.Code, jsonRPCMethodNotFound)
	}
}

// TestConnectionCandidatesDistinguishesAnUnknownNodeFromANodeWithNoSockets
// pins that the not-found answer is the one its sibling gives, rather than
// a second convention. A name the workspace does not declare is an error;
// a declared entity with nothing to offer is a successful empty answer.
func TestConnectionCandidatesDistinguishesAnUnknownNodeFromANodeWithNoSockets(t *testing.T) {
	srv := newTestServer(t, writeConnectionFixture(t), &bytes.Buffer{}, &bytes.Buffer{})

	in := bytes.NewBuffer(framedRequest(1, "aggrega/connectionCandidates", map[string]any{"node": "nope"}))
	out := &bytes.Buffer{}
	resp := runOneRequest(t, srv, in, out)
	if resp.Error == nil {
		t.Fatalf("an undeclared node name was accepted: %s", resp.Result)
	}
	if resp.Error.Code != errEntityNotResolvable {
		t.Errorf("undeclared node error code = %d, want %d", resp.Error.Code, errEntityNotResolvable)
	}

	// The contrast: a declared entity with no sockets answers successfully,
	// so the error above reports a name the workspace does not hold rather
	// than an empty result.
	_, _, sockets := connectionCandidatesOverWire(t, srv, map[string]any{"node": "postgres"})
	if len(sockets) != 0 {
		t.Errorf("the contrast case reported sockets: %v", sockets)
	}
}

// TestConnectionCandidatesRejectsARequestNamingNoNode pins that the node
// parameter is required: without one there is no declaration to read, and
// answering with every socket in the workspace would be an offer this
// method exists to scope to one entity.
func TestConnectionCandidatesRejectsARequestNamingNoNode(t *testing.T) {
	srv := newTestServer(t, writeConnectionFixture(t), &bytes.Buffer{}, &bytes.Buffer{})
	for _, params := range []map[string]any{{}, {"node": ""}, {"socket": "store"}} {
		in := bytes.NewBuffer(framedRequest(1, "aggrega/connectionCandidates", params))
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

// TestConnectionCandidatesOnAnEmptyWorkspaceReportsTheNodeAsUndeclared
// pins that a workspace with nothing in it reports the requested node as
// undeclared rather than as a node with no sockets.
func TestConnectionCandidatesOnAnEmptyWorkspaceReportsTheNodeAsUndeclared(t *testing.T) {
	srv := newTestServer(t, t.TempDir(), &bytes.Buffer{}, &bytes.Buffer{})
	in := bytes.NewBuffer(framedRequest(1, "aggrega/connectionCandidates", map[string]any{"node": "api"}))
	out := &bytes.Buffer{}
	resp := runOneRequest(t, srv, in, out)
	if resp.Error == nil {
		t.Fatalf("an empty workspace accepted a node name: %s", resp.Result)
	}
	if resp.Error.Code != errEntityNotResolvable {
		t.Errorf("error code = %d, want %d", resp.Error.Code, errEntityNotResolvable)
	}
}

// TestConnectionCandidatesOnACancelledRequestDoesNotReportTheNodeAsUndeclared
// pins that a cancelled request reports the cancellation rather than a
// conclusion no completed read reached. Without the entry check, the
// traversal would yield nothing under a cancelled context and the node
// would come back reported as one the workspace does not declare — a
// definite answer produced by a read that never ran.
func TestConnectionCandidatesOnACancelledRequestDoesNotReportTheNodeAsUndeclared(t *testing.T) {
	srv := newTestServer(t, writeConnectionFixture(t), &bytes.Buffer{}, &bytes.Buffer{})
	srv.serverCancel()

	in := bytes.NewBuffer(framedRequest(1, "aggrega/connectionCandidates", map[string]any{"node": "api"}))
	out := &bytes.Buffer{}
	resp := runOneRequest(t, srv, in, out)
	if resp.Error == nil {
		t.Fatalf("a cancelled request returned a result: %s", resp.Result)
	}
	if resp.Error.Code == errEntityNotResolvable {
		t.Errorf("a cancelled request reported the node as undeclared, which the interrupted read "+
			"never established: %+v", resp.Error)
	}
	if resp.Error.Code != jsonRPCInternalError {
		t.Errorf("cancelled request error code = %d, want %d", resp.Error.Code, jsonRPCInternalError)
	}
}

// TestConnectionCandidatesExcludesDocumentsOutsideTheWorkspaceRoot pins
// that a parse-cache entry for a file the editor sent from outside the
// workspace is not workspace-local and reaches neither the entity lookup
// nor any target list — which also keeps absolute host paths off the wire.
func TestConnectionCandidatesExcludesDocumentsOutsideTheWorkspaceRoot(t *testing.T) {
	root := writeConnectionFixture(t)
	outside := filepath.Join(t.TempDir(), "elsewhere.yaml")
	srv := newTestServer(t, root, &bytes.Buffer{}, &bytes.Buffer{})

	content := `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: elsewhere
  labels:
    component.aggrega.ai/type: database
`
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	srv.parseCache.Put(outside, &doc, []byte(content))

	body, _, sockets := connectionCandidatesOverWire(t, srv, map[string]any{"node": "api"})
	for _, name := range targetNames(t, socketNamed(t, sockets, "store")) {
		if name == "elsewhere" {
			t.Errorf("a Component outside the workspace root reached the targets: %s", body)
		}
	}
	if bytes.Contains(body, []byte(outside)) {
		t.Errorf("an absolute path outside the workspace root reached the wire: %s", body)
	}
}

// TestConnectionCandidatesIsByteStableAcrossRepeatedRequests is the
// backstop the ordering assertions name: whatever else moves, two
// identical requests against an unchanged workspace produce identical
// bytes. The parse cache is loaded first so both traversal passes are live
// and the cache's own map iteration has several entries to permute.
func TestConnectionCandidatesIsByteStableAcrossRepeatedRequests(t *testing.T) {
	root := writeConnectionFixture(t)
	srv := newTestServer(t, root, &bytes.Buffer{}, &bytes.Buffer{})

	for _, rel := range connectionFixtureAllFiles {
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

	first, _, _ := connectionCandidatesOverWire(t, srv, map[string]any{"node": "api"})
	for i := 2; i <= 12; i++ {
		again, _, _ := connectionCandidatesOverWire(t, srv, map[string]any{"node": "api"})
		if !bytes.Equal(first, again) {
			t.Fatalf("request %d returned different bytes\nfirst: %s\nagain: %s", i, first, again)
		}
	}
}
