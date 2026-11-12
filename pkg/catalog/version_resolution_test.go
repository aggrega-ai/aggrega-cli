// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import "testing"

const widgetV1 = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: demo/widget
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
    demo.aggrega.ai/release: "one-zero"
spec:
  schema:
    values:
      size:
        type: string
        default: "small"
`

const widgetV11 = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: demo/widget
  version: "1.1.0"
  labels:
    component.aggrega.ai/type: "server"
    demo.aggrega.ai/release: "one-one"
spec:
  schema:
    values:
      size:
        type: string
        default: "large"
`

// TestEagerCatalogVersionResolution is the end-to-end proof that the eager
// --catalog load path is version-aware: two versions of one component coexist in
// the index (the prior bare-name index would have silently collapsed them to one
// by walk-order last-wins), and a reference resolves three-form to the
// highest-satisfying version. The distinguishing release label proves WHICH
// version's content resolved — the same content a render consumes.
func TestEagerCatalogVersionResolution(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, dir+"/widget-1.0.0.yaml", widgetV1)
	writeFile(t, dir+"/widget-1.1.0.yaml", widgetV11)

	c, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// Both versions coexist — the index is keyed by name:version, not bare-name
	// last-wins.
	if len(c.Components) != 2 {
		t.Fatalf("expected 2 coexisting widget versions, got %d (a bare-name index would collapse them)", len(c.Components))
	}

	lookup := c.CatalogLookup()
	release := func(ref string) string {
		comp, ok := lookup.Component(ref)
		if !ok {
			return ""
		}
		rel, _ := comp.Metadata.Labels.Get("demo.aggrega.ai/release")
		return rel
	}

	cases := []struct {
		name, ref, wantVersion, wantRelease string
	}{
		{"unversioned resolves to highest", "demo/widget", "1.1.0", "one-one"},
		{"exact pins 1.0.0", "demo/widget:1.0.0", "1.0.0", "one-zero"},
		{"exact pins 1.1.0", "demo/widget:1.1.0", "1.1.0", "one-one"},
		{"caret picks highest in range", "demo/widget:^1.0.0", "1.1.0", "one-one"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			comp, ok := lookup.Component(tc.ref)
			if !ok {
				t.Fatalf("%q did not resolve", tc.ref)
			}
			if comp.Metadata.Version != tc.wantVersion {
				t.Errorf("%q resolved version %q, want %q", tc.ref, comp.Metadata.Version, tc.wantVersion)
			}
			if rel := release(tc.ref); rel != tc.wantRelease {
				t.Errorf("%q resolved release label %q, want %q", tc.ref, rel, tc.wantRelease)
			}
		})
	}

	// A reference to a version that does not exist is not silently coerced to a
	// present one — it is an honest miss at the lookup boundary.
	if _, ok := lookup.Component("demo/widget:9.9.9"); ok {
		t.Error("demo/widget:9.9.9 resolved; want a miss for an absent version")
	}
}
