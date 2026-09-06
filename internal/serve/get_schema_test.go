// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// The daemon's schema-reporting leg. The oracle throughout is the schema set
// compiled into the binary, read through the same two entry points every other
// surface reads it through, so these prove the method surfaces that set faithfully
// rather than agreeing with a second copy of it written here. The command line
// writes those same bytes verbatim and the tool protocol returns the same
// document, so holding this leg to the set holds all three to each other without
// any test reaching into another package.

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/internal/scaffoldops"
)

// callGetSchema drives one aggrega/getSchema request through the production
// dispatch and serializer path and returns the decoded response.
func callGetSchema(t *testing.T, srv *Server, params map[string]any) jsonRPCResponse {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/getSchema", params))
	return runOneRequest(t, srv, &in, &out)
}

// decodeGetSchemaResult decodes a successful response's result payload.
func decodeGetSchemaResult(t *testing.T, resp jsonRPCResponse) getSchemaResult {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("aggrega/getSchema returned an error envelope: %+v", resp.Error)
	}
	var got getSchemaResult
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("decode result: %v (body: %s)", err, resp.Result)
	}
	return got
}

// asDocument decodes JSON bytes into a comparable value.
//
// The comparison this feeds is between two JSON documents rather than two byte
// runs, and deliberately so: a value travelling through the response encoder is
// compacted and has its HTML-significant characters escaped, so identical
// documents differ as bytes at the wire boundary while carrying the same content.
// Comparing the decoded values is the claim that holds — and it is the claim the
// method makes, since a client reads the document rather than the framing.
func asDocument(t *testing.T, what string, raw []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s is not valid JSON: %v (bytes: %s)", what, err, raw)
	}
	return v
}

// TestGetSchemaAnswersWithTheEmbeddedSchema is the invocation proof: the method
// runs, and for every kind the binary carries it answers with that kind's schema
// as the binary holds it.
//
// It walks the whole embedded set rather than a sample, because the mapping is
// per-kind: a kind wired to the wrong schema, or reachable on the command line and
// not here, is invisible to any subset that happens to miss it.
func TestGetSchemaAnswersWithTheEmbeddedSchema(t *testing.T) {
	srv := newTestServer(t, t.TempDir(), nil, nil)

	kinds := schemas.Kinds()
	// Calibration: an empty set would make the loop below run zero times and the
	// test would pass while asserting nothing.
	if len(kinds) == 0 {
		t.Fatal("the binary embeds no schema kinds, so the walk below asserts nothing")
	}

	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			want, ok := schemas.SchemaByKind(kind)
			if !ok {
				t.Fatalf("the oracle holds no schema for the enumerated kind %q", kind)
			}

			got := decodeGetSchemaResult(t, callGetSchema(t, srv, map[string]any{"kind": kind}))
			if got.Kind != kind {
				t.Errorf("the answer reports kind %q for a request about %q", got.Kind, kind)
			}
			if len(got.Schema) == 0 {
				t.Fatalf("the answer for %q carries no schema", kind)
			}
			if !reflect.DeepEqual(
				asDocument(t, "the answer", got.Schema),
				asDocument(t, "the embedded schema", []byte(want)),
			) {
				t.Errorf("the daemon's schema for %q is not the embedded one.\n got:\n%s\nwant:\n%s",
					kind, got.Schema, want)
			}
			// A schema request asks about one kind, so the enumeration is the other
			// answer and must not ride along: a client reading both halves could not
			// tell which question it asked.
			if len(got.Kinds) != 0 {
				t.Errorf("the answer for a named kind also carried the enumeration: %v", got.Kinds)
			}
		})
	}
}

// TestGetSchemaEnumeratesEveryKindWhenNoneIsNamed pins the other half of the
// request. Omitting the kind is a request in its own right — the one a client
// makes to learn what it may name — and not a malformed one.
//
// The expectation is the enumeration itself rather than a list written here, for
// the reason the command line's own listing test gives: a restated set would be a
// third copy, and it would pass while the method and the binary disagreed.
func TestGetSchemaEnumeratesEveryKindWhenNoneIsNamed(t *testing.T) {
	srv := newTestServer(t, t.TempDir(), nil, nil)

	want := schemas.Kinds()
	if len(want) == 0 {
		t.Fatal("the binary embeds no schema kinds, so the comparison below asserts nothing")
	}

	for _, tc := range []struct {
		name   string
		params map[string]any
	}{
		{"no kind key at all", map[string]any{}},
		{"an empty kind", map[string]any{"kind": ""}},
		{"a kind that is only space", map[string]any{"kind": "   "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := decodeGetSchemaResult(t, callGetSchema(t, srv, tc.params))
			if !reflect.DeepEqual(got.Kinds, want) {
				t.Errorf("the enumeration is not the embedded one.\n got: %v\nwant: %v", got.Kinds, want)
			}
			// Every enumerated token must be one this same method answers to, so a
			// client can walk the listing and fetch each entry without meeting a
			// refusal.
			for _, kind := range got.Kinds {
				if _, ok := schemas.SchemaByKind(kind); !ok {
					t.Errorf("the enumeration offers %q, which resolves to no schema", kind)
				}
			}
			if len(got.Schema) != 0 || got.Kind != "" {
				t.Errorf("an enumeration also carried a single kind's answer: kind=%q schema=%s",
					got.Kind, got.Schema)
			}
		})
	}
}

// TestGetSchemaRefusesAKindNoSchemaAnswersTo pins that the refusal carries the
// corrective text naming the whole valid set, and that it is the SHARED text
// rather than a wording of this method's own.
//
// The shared-text assertion is the load-bearing one. The same rejection is
// reachable on three surfaces, and a caller meeting two wordings of it would read
// them as two different faults.
//
// It also pins the band: an unrecognised kind is a malformed request, which the
// client corrects, and not a state of the workspace it would ask a person to
// repair.
func TestGetSchemaRefusesAKindNoSchemaAnswersTo(t *testing.T) {
	srv := newTestServer(t, t.TempDir(), nil, nil)

	resp := callGetSchema(t, srv, map[string]any{"kind": "compoent"})
	if resp.Error == nil {
		t.Fatalf("a kind no schema answers to was answered rather than refused: %s", resp.Result)
	}
	if resp.Error.Code != jsonRPCInvalidParams {
		t.Errorf("the refusal carries code %d, not the invalid-params code %d",
			resp.Error.Code, jsonRPCInvalidParams)
	}
	if resp.Error.Message != scaffoldops.UnknownKindGuidance("compoent") {
		t.Errorf("the refusal is not the shared guidance:\n got: %s\nwant: %s",
			resp.Error.Message, scaffoldops.UnknownKindGuidance("compoent"))
	}
	for _, kind := range schemas.Kinds() {
		if !strings.Contains(resp.Error.Message, kind) {
			t.Errorf("the refusal omits the valid kind %q: %s", kind, resp.Error.Message)
		}
	}
	if len(resp.Result) != 0 {
		t.Errorf("a refused request still carried a result: %s", resp.Result)
	}
}

// TestGetSchemaAnswersOnAWorkspaceHoldingNothing pins the reach the method
// deliberately does not have. Its subject is compiled into the binary, so an empty
// directory is enough — which is the workspace a client wiring an editor's schema
// association is often pointed at.
func TestGetSchemaAnswersOnAWorkspaceHoldingNothing(t *testing.T) {
	srv := newTestServer(t, t.TempDir(), nil, nil)

	got := decodeGetSchemaResult(t, callGetSchema(t, srv, map[string]any{"kind": "component"}))
	if len(got.Schema) == 0 {
		t.Fatal("the answer carries no schema in a workspace holding nothing")
	}
}

// TestGetSchemaWritesNothing pins the property the reference page states: the
// answer is read out of the binary, so nothing about the workspace moves.
//
// It asks for every kind the binary carries rather than one, because a write path
// added for a single kind would slip past a sampled check. The workspace holds a
// file before the requests so the snapshot is discriminating over content as well
// as over presence.
func TestGetSchemaWritesNothing(t *testing.T) {
	ws := t.TempDir()
	if err := writeScaffoldProbeFile(ws); err != nil {
		t.Fatalf("seed the workspace: %v", err)
	}
	srv := newTestServer(t, ws, nil, nil)

	before := treeSnapshot(t, ws)
	if len(before) == 0 {
		t.Fatal("the snapshot read nothing, so comparing it against itself would assert nothing")
	}
	beforeDirs := scaffoldWorkspaceDirs(t, ws)

	for _, kind := range schemas.Kinds() {
		if resp := callGetSchema(t, srv, map[string]any{"kind": kind}); resp.Error != nil {
			t.Fatalf("aggrega/getSchema refused %q: %+v", kind, resp.Error)
		}
	}
	if resp := callGetSchema(t, srv, map[string]any{}); resp.Error != nil {
		t.Fatalf("aggrega/getSchema refused the enumeration: %+v", resp.Error)
	}

	if after := treeSnapshot(t, ws); !reflect.DeepEqual(before, after) {
		t.Fatalf("reporting a schema changed the workspace's files.\nbefore: %v\nafter:  %v", before, after)
	}
	if after := scaffoldWorkspaceDirs(t, ws); !reflect.DeepEqual(beforeDirs, after) {
		t.Fatalf("reporting a schema changed the workspace's directories.\nbefore: %v\nafter:  %v",
			beforeDirs, after)
	}
}
