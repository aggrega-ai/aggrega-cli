// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"sort"
	"testing"
)

// entityRefKeys reads the key set of one reported entity identity off the
// generically-decoded response, sorted so the comparison is
// order-independent.
//
// The keys are read from the decoded JSON object rather than from the
// response type: unmarshalling into a struct reports an absent key and a key
// carrying the empty string identically, so a struct-based assertion cannot
// tell "carries no origin" from `origin: ""` — which is exactly the
// distinction these tests exist to make. The object's own key set is the
// only thing that can.
func entityRefKeys(obj map[string]any) []string {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// assertEntityRefKeys fails unless the reported identity carries exactly the
// expected keys. The whole set is asserted rather than the presence or
// absence of one name, so a fourth key arriving under any spelling fails
// here too.
func assertEntityRefKeys(t *testing.T, what string, obj map[string]any, want []string, body []byte) {
	t.Helper()
	got := entityRefKeys(obj)
	if len(got) != len(want) {
		t.Fatalf("%s keys = %v, want exactly %v: %s", what, got, want, body)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s keys = %v, want exactly %v: %s", what, got, want, body)
		}
	}
}

// socketTargets reads one reported socket's target list as decoded objects.
func socketTargets(t *testing.T, socket map[string]any, body []byte) []map[string]any {
	t.Helper()
	raw, ok := socket["targets"]
	if !ok {
		t.Fatalf(`socket %v carries no "targets" key: %s`, socket["socket"], body)
	}
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf(`socket %v "targets" is not an array: %s`, socket["socket"], body)
	}
	out := make([]map[string]any, 0, len(list))
	for i, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("socket %v targets[%d] is not an object: %s", socket["socket"], i, body)
		}
		out = append(out, entry)
	}
	return out
}

// TestEntityIdentityCarriesOriginOnlyWhereItDistinguishesSomething is the
// wire-shape contract for the two reference shapes, asserted from a single
// fixture so the two halves are a contrast rather than two independent
// claims.
//
// aggrega/attachCandidates draws its candidates from two places — the
// workspace documents and the configured catalog directories — so a client
// cannot read a candidate's identity without being told which of them it
// came from, and each candidate carries origin beside the identity triple.
// aggrega/connectionCandidates draws its targets from the workspace alone,
// so each target is the triple and nothing else.
//
// Asserting both directions from one fixture is what makes either half
// discriminating. A build that put origin on every reported identity would
// pass the attach half; one that put it on none would pass the connection
// half; only the contrast rejects both.
func TestEntityIdentityCarriesOriginOnlyWhereItDistinguishesSomething(t *testing.T) {
	root := writeConnectionFixture(t)
	srv := newTestServer(t, root, &bytes.Buffer{}, &bytes.Buffer{})

	// The connection half: every target is the bare identity triple.
	connBody, _, sockets := connectionCandidatesOverWire(t, srv, map[string]any{"node": "api"})
	t.Logf("aggrega/connectionCandidates wire capture: %s", connBody)

	var targetsSeen int
	for _, socket := range sockets {
		for _, target := range socketTargets(t, socket, connBody) {
			targetsSeen++
			assertEntityRefKeys(t, "a connection target's", target,
				[]string{"file", "kind", "name"}, connBody)
		}
	}
	// A fixture reporting no target at all would make the loop above
	// vacuous, and a vacuous key-set assertion reads exactly like a passing
	// one.
	if targetsSeen == 0 {
		t.Fatalf("no target was reported, so the key-set assertion above checked nothing: %s", connBody)
	}

	// The blunt backstop over the whole response, which reaches any nesting
	// the per-object walk does not. No other key in this response carries
	// the token.
	if bytes.Contains(connBody, []byte(`"origin"`)) {
		t.Errorf("the connection-candidate response carries an origin key: %s", connBody)
	}

	// The attach half, on the same workspace: the identity carries origin.
	// mesh is a Feature declaring no applicability, so it is a candidate for
	// every node, and it is a workspace document — which makes the value
	// assertable as well as the key.
	attachBody, _, candidates := attachCandidatesOverWire(t, srv, "api")
	t.Logf("aggrega/attachCandidates wire capture: %s", attachBody)
	if len(candidates) == 0 {
		t.Fatalf("no candidate was reported, so the assertions below check nothing: %s", attachBody)
	}
	for _, candidate := range candidates {
		assertEntityRefKeys(t, "an attach candidate's", candidate,
			[]string{"file", "kind", "name", "origin"}, attachBody)
		if candidate["origin"] != EntityOriginWorkspace {
			t.Errorf("candidate %v origin = %v, want %q: %s",
				candidate["name"], candidate["origin"], EntityOriginWorkspace, attachBody)
		}
	}
}

// TestEntityIdentityJoinsAcrossTheTwoReferenceShapes pins the property the
// single declaration exists for: an identity reported by one method joins
// against the same entity reported by another, because both spell the
// triple's keys the same way.
//
// It is asserted over the wire rather than over the Go types, since the Go
// types share a declaration by construction and would agree even if a tag on
// one of them did not. The join is performed on the marshalled objects,
// which is what a client actually holds.
func TestEntityIdentityJoinsAcrossTheTwoReferenceShapes(t *testing.T) {
	root := writeConnectionFixture(t)
	srv := newTestServer(t, root, &bytes.Buffer{}, &bytes.Buffer{})

	// A Component reported as a connection target, and the same Component
	// reported in the entity index — the surface a client joins a candidate
	// against for anything it wants to display.
	connBody, _, sockets := connectionCandidatesOverWire(t, srv, map[string]any{"node": "api"})
	targets := socketTargets(t, socketNamed(t, sockets, "store"), connBody)
	if len(targets) == 0 {
		t.Fatalf("the store socket reported no target to join on: %s", connBody)
	}
	got := targets[0]

	in := bytes.NewBuffer(framedRequest(2, "aggrega/catalogIndex", map[string]any{}))
	out := &bytes.Buffer{}
	resp := runOneRequest(t, srv, in, out)
	if resp.Error != nil {
		t.Fatalf("catalogIndex returned an error: %+v", resp.Error)
	}
	var index struct {
		Entities []map[string]any `json:"entities"`
	}
	if err := json.Unmarshal(resp.Result, &index); err != nil {
		t.Fatalf("unmarshal index: %v (body: %s)", err, resp.Result)
	}

	var joined map[string]any
	for _, e := range index.Entities {
		if e["kind"] == got["kind"] && e["name"] == got["name"] && e["file"] == got["file"] {
			joined = e
		}
	}
	if joined == nil {
		t.Fatalf("a reported target %v joins against no entity in the index: %v", got, index.Entities)
	}
	// The join landed, so the index's own describing fields are what a client
	// displays — which is why the target carries none of them.
	if joined["apiVersion"] == "" {
		t.Errorf("the joined index entry carries no apiVersion, so a client has nothing to display: %v", joined)
	}
	if joined["origin"] != EntityOriginWorkspace {
		t.Errorf("the joined index entry origin = %v, want %q: %v",
			joined["origin"], EntityOriginWorkspace, joined)
	}
}
