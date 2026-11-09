// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// The per-position lockstep for the connection surfaces.
//
// The kind-wide alignment suite compares FLAT name sets: every property the schema
// declares anywhere against every yaml tag the kind's Go types carry anywhere. A
// name declared at one position and carried at another satisfies it, so it cannot
// tell a connection a component draws from a connection a Feature draws — the two
// share every key but one — and it cannot see a Go field one position's schema
// refuses while another position admits it. These tests hold each connection
// shape's Go type to the ONE schema position it decodes, in both directions: a
// schema property with no field is a silent drop, and a field the position does not
// admit is a key the type would carry where the document may not write one.

// entryPattern is the key pattern every socket, alias, slot and attachment name is
// declared under on these surfaces.
const entryPattern = "^[a-zA-Z0-9_-]+$"

func decodeSchema(t *testing.T, name, src string) map[string]any {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal([]byte(src), &root); err != nil {
		t.Fatalf("decode %s schema: %v", name, err)
	}
	return root
}

// schemaAt walks a schema by keys, failing with the path walked so far when a step is
// absent — a moved def reads as a broken walk, never as an empty property set.
func schemaAt(t *testing.T, node any, path ...any) map[string]any {
	t.Helper()
	cur := node
	for i, step := range path {
		switch s := step.(type) {
		case string:
			m, ok := cur.(map[string]any)
			if !ok {
				t.Fatalf("schema walk %v: step %d expects an object", path, i)
			}
			if cur, ok = m[s]; !ok {
				t.Fatalf("schema walk %v: no key %q at step %d", path, s, i)
			}
		case int:
			a, ok := cur.([]any)
			if !ok || s >= len(a) {
				t.Fatalf("schema walk %v: step %d expects an array with index %d", path, i, s)
			}
			cur = a[s]
		}
	}
	m, ok := cur.(map[string]any)
	if !ok {
		t.Fatalf("schema walk %v ends at a non-object", path)
	}
	return m
}

func propertyNames(t *testing.T, node map[string]any) []string {
	t.Helper()
	props, ok := node["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema node declares no properties: %v", node)
	}
	var out []string
	for k := range props {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// fieldTags returns the yaml tag names a struct's own fields carry, not
// descending into any of them.
func fieldTags(v any) []string {
	rt := reflect.TypeOf(v)
	var out []string
	for i := 0; i < rt.NumField(); i++ {
		tag := rt.Field(i).Tag.Get("yaml")
		if name := strings.Split(tag, ",")[0]; name != "" && name != "-" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func namesMinus(a, b []string) []string {
	in := map[string]bool{}
	for _, x := range b {
		in[x] = true
	}
	var out []string
	for _, x := range a {
		if !in[x] {
			out = append(out, x)
		}
	}
	return out
}

// TestConnectionShapesMirrorTheirSchemaPositions holds each connection-surface Go
// type to the schema position it decodes, both ways, naming what is missing on
// either side rather than comparing counts.
//
// One row carries a recorded difference, and the difference is a field the GO type
// has that its position refuses — never the other way, which would be a silent
// drop. A Feature attached on a Release draws its connections through the same Go
// type as one attached in the Aggregate, and the Release's position admits no
// per-connection values. The difference is asserted exactly, so a change that
// closes it — or widens it — fails here and is read.
func TestConnectionShapesMirrorTheirSchemaPositions(t *testing.T) {
	t.Parallel()

	agg := decodeSchema(t, "aggregate", schemas.AggregateV1beta1)
	rel := decodeSchema(t, "release", schemas.ReleaseV1beta1)
	comp := decodeSchema(t, "component", schemas.ComponentV1beta1)

	connectionEntry := func(root map[string]any, def string) map[string]any {
		return schemaAt(t, root, "$defs", def, "patternProperties", entryPattern, "anyOf", 1, "patternProperties", entryPattern)
	}

	cases := []struct {
		name string
		node map[string]any
		typ  any
		// goOnly is the exact set of yaml names the Go type carries that this
		// position refuses. Empty for every position but the one the doc names.
		goOnly []string
	}{
		{name: "a connection a component draws (Aggregate)", node: connectionEntry(agg, "component_uses_block"), typ: types.VerboseUsesEntry{}},
		{name: "a connection a Feature draws (Aggregate)", node: connectionEntry(agg, "feature_uses_block"), typ: types.FeatureUsesEntry{}},
		{name: "an attachment a connection carries (Aggregate)", node: schemaAt(t, agg, "$defs", "connection_attachment"), typ: types.ConnectionAttachment{}},
		{
			name: "a connection entry the Release writes",
			node: schemaAt(t, rel, "$defs", "release_uses_block", "patternProperties", entryPattern, "patternProperties", entryPattern),
			typ:  types.ReleaseUsesEntry{},
		},
		{name: "an attachment a Release connection entry carries", node: schemaAt(t, rel, "$defs", "release_connection_attachment"), typ: types.ReleaseConnectionAttachment{}},
		{
			name:   "a connection a Feature draws (Release)",
			node:   connectionEntry(rel, "uses_block"),
			typ:    types.FeatureUsesEntry{},
			goOnly: []string{"values"},
		},
		{
			name: "a Component socket",
			node: schemaAt(t, comp, "properties", "spec", "properties", "usesSchema", "patternProperties", entryPattern),
			typ:  types.UsesSchemaEntry{},
		},
		{
			name: "a slot a Component socket declares",
			node: schemaAt(t, comp, "properties", "spec", "properties", "usesSchema", "patternProperties", entryPattern,
				"properties", "featuresSchema", "additionalProperties"),
			typ: types.AttachmentSlot{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			schemaNames := propertyNames(t, tc.node)
			goNames := fieldTags(tc.typ)
			if dropped := namesMinus(schemaNames, goNames); len(dropped) > 0 {
				t.Errorf("the position declares %v and the Go type %T carries no field for them, so a document "+
					"writing one is accepted and the key is silently dropped", dropped, tc.typ)
			}
			extra := namesMinus(goNames, schemaNames)
			if !reflect.DeepEqual(nilIfEmpty(extra), nilIfEmpty(tc.goOnly)) {
				t.Errorf("the Go type %T carries %v that this position refuses; the recorded difference is %v",
					tc.typ, extra, tc.goOnly)
			}
		})
	}
}

func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

// TestFeatureSocketRefusesComponentOnlySocketFields holds the other half of the
// one socket type both kinds share: the fields only a Component's socket may
// declare are refused on a Feature's socket when the document's envelope is
// validated — each one, one fault per document — so a Feature decoded from a
// document never carries one. The two fields both kinds declare are accepted, the
// positive control that keeps each refusal attributable to its own field.
//
// The field set is read off the Component schema's socket and the Feature
// schema's socket rather than listed, so a field added to one kind and not the
// other is asked about here without an edit.
func TestFeatureSocketRefusesComponentOnlySocketFields(t *testing.T) {
	t.Parallel()

	comp := decodeSchema(t, "component", schemas.ComponentV1beta1)
	feat := decodeSchema(t, "feature", schemas.FeatureV1beta1)
	componentFields := propertyNames(t, schemaAt(t, comp, "properties", "spec", "properties", "usesSchema", "patternProperties", entryPattern))
	featureFields := propertyNames(t, schemaAt(t, feat, "properties", "spec", "properties", "usesSchema", "patternProperties", entryPattern))
	componentOnly := namesMinus(componentFields, featureFields)
	if !reflect.DeepEqual(componentOnly, []string{"featuresSchema", "infraSchema", "required", "valueSchema"}) {
		t.Fatalf("the fields only a Component socket declares are %v; this test's documents cover "+
			"featuresSchema, infraSchema, required and valueSchema", componentOnly)
	}

	bodies := map[string]string{
		"selector":       "",
		"inject":         "      inject: [env]\n",
		"required":       "      required: true\n",
		"valueSchema":    "      valueSchema:\n        pool: { type: integer }\n",
		"infraSchema":    "      infraSchema:\n        size: { type: string }\n",
		"featuresSchema": "      featuresSchema:\n        credential:\n          selector: \"feature.aggrega.ai/kind=secret\"\n",
	}
	doc := func(extra string) []byte {
		return []byte("apiVersion: aggrega.ai/v1beta1\nkind: Feature\nmetadata:\n  name: core/socket-probe\n  version: 1.0.0\nspec:\n" +
			"  usesSchema:\n    peers:\n      selector: \"component.aggrega.ai/type=db\"\n" + extra)
	}
	for _, field := range append(append([]string{}, featureFields...), componentOnly...) {
		body, ok := bodies[field]
		if !ok {
			t.Fatalf("no document written for socket field %q", field)
		}
		_, err := envelope.ValidateBytes(envelope.KindFeature, doc(body), "feature.yaml")
		shared := !containsName(componentOnly, field)
		switch {
		case shared && err != nil:
			t.Errorf("a Feature socket declaring %s must pass its envelope; got %v", field, err)
		case !shared && err == nil:
			t.Errorf("a Feature socket declaring %s must be refused: only a Component's socket declares it", field)
		case !shared && !errors.Is(err, envelope.ErrEntityEnvelope()):
			t.Errorf("a Feature socket declaring %s must be refused by the envelope; got %v", field, err)
		case !shared && !strings.Contains(err.Error(), field):
			t.Errorf("the refusal of %s must name the key; got %v", field, err)
		}
	}
}

func containsName(set []string, s string) bool {
	for _, x := range set {
		if x == s {
			return true
		}
	}
	return false
}
