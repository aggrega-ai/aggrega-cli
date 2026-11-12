// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"sort"
	"testing"
)

// decodedObjectKeys reads the key set of one reported object off the
// generically-decoded response, sorted so the comparison is
// order-independent.
//
// The keys are read from the decoded JSON object rather than from the
// response type: unmarshalling into a struct reports an absent key and a key
// carrying the empty string identically, so a struct-based assertion cannot
// tell "carries no origin" from `origin: ""` — which is exactly the
// distinction these tests exist to make. The object's own key set is the
// only thing that can.
func decodedObjectKeys(obj map[string]any) []string {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// wireSchemaPosition returns the property names one position in the shipped
// schema declares, and which of them it requires.
//
// It is what makes the expectations below external. A key set written out in Go
// is a second copy of the contract that agrees on the day it is typed and is
// checked against nothing afterwards; the shipped schema is the copy clients
// read, so it is the one an assertion about the wire has to be made against.
//
// A pointer that does not resolve is fatal rather than an empty result. An empty
// property set would make every comparison below vacuous — the expected set
// would be read from nothing and a decoded object would have nothing to
// disagree with — and a vacuous key-set assertion reads exactly like a passing
// one.
func wireSchemaPosition(t *testing.T, def string) (declared, required map[string]bool) {
	t.Helper()

	position, err := resolveSchemaPointer(wireSchemaDocument(t), def)
	if err != nil {
		t.Fatalf("%v — the assertion names a position the shipped schema does not declare, so the "+
			"expected key set would be read from nothing and any object at all would satisfy it", err)
	}
	properties, ok := position["properties"].(map[string]any)
	if !ok || len(properties) == 0 {
		t.Fatalf("%s declares no properties, so the expected key set would be empty and any object "+
			"at all would satisfy it", def)
	}

	declared = map[string]bool{}
	for name := range properties {
		declared[name] = true
	}
	required = map[string]bool{}
	entries, _ := position["required"].([]any)
	for _, entry := range entries {
		name, ok := entry.(string)
		if !ok {
			t.Fatalf("%s: a required entry is %#v rather than a property name", def, entry)
		}
		if !declared[name] {
			t.Fatalf("%s requires %q and declares no property of that name, so the floor below would "+
				"demand a key the ceiling forbids", def, name)
		}
		required[name] = true
	}
	return declared, required
}

// assertWireKeys fails unless the decoded object carries exactly the keys the
// shipped schema declares for the named position, given which of that position's
// optional properties this value fills.
//
// The expected set is COMPUTED from the schema rather than written out, and the
// two edges catch different drift. The position's required properties are the
// FLOOR: a key an omitempty tag dropped is missing from a set the schema says
// must be there. The comparison is an equality rather than a containment, so
// the computed set is also the CEILING: a field whose tag was renamed arrives
// under a spelling nothing put in the expected set, and fails twice over, since
// the name it used to carry is now absent too.
//
// The exactness is this comparison's, not a property of the schema. Most
// positions on this wire do close their property set, but not all of them do,
// and a ceiling resting on that would be a ceiling with holes in it wherever
// one does not.
//
// carries names only the optional properties this value fills, which is the one
// thing the schema cannot know — an optional property is present or absent
// according to what the value holds, and that is the arm's own statement. It is
// still held against the schema in both directions: a name the position does not
// declare fails, and so does a name the position REQUIRES, because naming a
// required property adds nothing and would go on passing on the day it stops
// being required.
//
// The observed side is the generically-decoded object, for the reason
// decodedResult states: a decode back into the struct reports an absent key and
// a key carrying a zero value identically, and cannot see a tag at all.
func assertWireKeys(t *testing.T, what, def string, obj map[string]any, carries []string, body []byte) {
	t.Helper()

	declared, required := wireSchemaPosition(t, def)

	want := map[string]bool{}
	for name := range required {
		want[name] = true
	}
	for _, name := range carries {
		if !declared[name] {
			t.Fatalf("%s: this arm says it carries %q and %s declares no property of that name, so "+
				"the expectation names a key the published contract does not have", what, name, def)
		}
		if required[name] {
			t.Fatalf("%s: this arm names %q among the optional properties it fills and %s requires "+
				"it; the floor already demands it, and naming it here would go on passing on the day "+
				"it stops being required", what, name, def)
		}
		want[name] = true
	}

	got := decodedObjectKeys(obj)
	expected := sortedKeys(want)
	if len(got) != len(expected) {
		t.Fatalf("%s keys = %v, want exactly %v — %s requires %v and this arm fills %v: %s",
			what, got, expected, def, sortedKeys(required), carries, body)
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Fatalf("%s keys = %v, want exactly %v — %s requires %v and this arm fills %v: %s",
				what, got, expected, def, sortedKeys(required), carries, body)
		}
	}
}

// wireOptionalProperty returns a property name after confirming the shipped
// schema declares it at the named position and does not require it.
//
// A probe that reads one key off the payload BYTES is the only thing that can
// tell "not reported" from "reported empty", so those probes stay — but the key
// they look for is a string that would otherwise be checked against nothing.
// Resolving it here is what makes a respelling in the schema fail the probe
// rather than leave it hunting a key the contract no longer has.
//
// The optionality half matters as much: a probe asserting a key is ABSENT is
// only meaningful where the schema permits it to be, and one written against a
// required property is asserting the wire breaks its own contract.
func wireOptionalProperty(t *testing.T, def, name string) string {
	t.Helper()

	declared, required := wireSchemaPosition(t, def)
	if !declared[name] {
		t.Fatalf("%s declares no %q property, so a probe for that key is looking for something the "+
			"published contract does not have", def, name)
	}
	if required[name] {
		t.Fatalf("%s requires %q, so it is never legitimately absent and a probe for its presence "+
			"asserts nothing", def, name)
	}
	return name
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
//
// The two halves name two different schema positions and take their expected
// key sets from them, so the contrast is the published contract's rather than
// this file's: one position declares no origin property at all and the other
// requires one. A build that put origin on both would be emitting a key the
// first position does not declare, and one that put it on neither would be
// dropping a key the second position requires.
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
			assertWireKeys(t, "a connection target's", "#/$defs/WorkspaceEntityRef",
				target, nil, connBody)
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
		assertWireKeys(t, "an attach candidate's", "#/$defs/SourcedEntityRef",
			candidate, nil, attachBody)
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
