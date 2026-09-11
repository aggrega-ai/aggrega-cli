// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"encoding/json"
	"testing"
)

// decodedResult marshals a response value and decodes it generically, so the
// assertions below read the object a client actually receives.
//
// The key set is read from the decoded object rather than from the response
// type for the reason the sibling wire-shape test states: unmarshalling back
// into the struct reports an absent key and a key carrying a zero value
// identically, which is exactly the distinction a wire contract turns on. It
// also cannot see a tag at all — a field whose tag was renamed unmarshals into
// the same Go field and reports the same value, so a struct-based assertion is
// blind to the rename this file exists to catch.
func decodedResult(t *testing.T, v any) (map[string]any, []byte) {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal the result: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("decode the marshalled result: %v (body: %s)", err, body)
	}
	return obj, body
}

// TestPluginGenCatalogResultWireShape pins the published key set of the
// aggrega/pluginGenCatalog response, across the three shapes its two optional
// halves produce.
//
// 🔴 It exists because nothing else reaches this shape. Every other test of
// this method drives a REFUSAL — a missing selection, a path outside the
// workspace, a fixture module that is not a real module — so the successful
// answer is marshalled nowhere in the suite, and the request-side guard covers
// only the spelling a caller SENDS. Without this the response keys could be
// renamed with nothing failing.
//
// The whole set is asserted rather than the presence of one name, so a key
// arriving under any spelling fails here too, and the delivery key is asserted
// in all three shapes because that is the property the response type promises:
// it states the delivery outright instead of leaving a client to infer it from
// which half is filled.
//
// The helper it calls is named for the neighbouring test's subject and is
// reused deliberately: the key-set comparison is one mechanic, and a second
// copy of it here would agree today and drift later.
func TestPluginGenCatalogResultWireShape(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		result pluginGenCatalogResult
		want   []string
	}{
		{
			// The dry-run delivery: the generated entities came back and
			// nothing was written, so the notices half is absent.
			name: "a dry run reports documents and no notices",
			result: pluginGenCatalogResult{
				Documents: []pluginGeneratedDocument{{Path: "generated/greet.yaml", Content: "metadata: {}\n"}},
				DryRun:    true,
			},
			want: []string{"documents", "dryRun"},
		},
		{
			// The writing delivery: the files reached disk and the generation's
			// account of them is what comes back, so the documents half is absent.
			name: "a write reports notices and no documents",
			result: pluginGenCatalogResult{
				Notices: "wrote generated/greet.yaml\n",
			},
			want: []string{"dryRun", "notices"},
		},
		{
			// 🔴 The shape that pins the delivery key hardest: a write with
			// nothing to say. Both optional halves are omitted, so if the
			// delivery key were ever made omittable it would vanish here while
			// the two rows above still passed — and a client branching on it
			// would read a write as indistinguishable from a field it never got.
			name:   "a write with neither half filled still states the delivery",
			result: pluginGenCatalogResult{},
			want:   []string{"dryRun"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			obj, body := decodedResult(t, tc.result)
			assertEntityRefKeys(t, "the gen-catalog result's", obj, tc.want, body)
			if obj["dryRun"] != tc.result.DryRun {
				t.Errorf("dryRun = %v, want %v: %s", obj["dryRun"], tc.result.DryRun, body)
			}
		})
	}
}

// TestPluginGenCatalogDocumentWireShape pins the key set of one reported
// document.
//
// It is asserted separately because the document type exists for its wire
// spelling and nothing else: the shared document type it projects from carries
// no tags for this protocol, so emitting that type directly would serialize its
// Go field names capitalised, against every other shape on this surface. A
// projection whose reason is the spelling needs the spelling asserted.
func TestPluginGenCatalogDocumentWireShape(t *testing.T) {
	t.Parallel()

	obj, body := decodedResult(t, pluginGeneratedDocument{
		Path:    "generated/greet.yaml",
		Content: "metadata: {}\n",
	})
	assertEntityRefKeys(t, "a reported document's", obj, []string{"content", "path"}, body)
}
