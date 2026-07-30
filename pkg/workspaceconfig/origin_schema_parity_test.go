// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"encoding/json"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"
)

// schemaPropertyPaths returns every dotted property path the config schema
// declares, walking nested `properties` objects. Only paths a plain object
// nesting reaches are collected: an array's items and a `$ref` target are
// deliberately not descended into, because the origin surface attributes those
// as a unit rather than per leaf.
func schemaPropertyPaths(t *testing.T) map[string]bool {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal([]byte(schemas.AggregaConfigV1alpha1), &root); err != nil {
		t.Fatalf("unmarshal the embedded config schema: %v", err)
	}
	out := map[string]bool{}
	var walk func(node map[string]any, prefix string)
	walk = func(node map[string]any, prefix string) {
		props, ok := node["properties"].(map[string]any)
		if !ok {
			return
		}
		for name, raw := range props {
			path := name
			if prefix != "" {
				path = prefix + "." + name
			}
			out[path] = true
			if child, ok := raw.(map[string]any); ok {
				walk(child, path)
			}
		}
	}
	walk(root, "")
	if len(out) == 0 {
		t.Fatal("the embedded config schema yielded no property paths, so this test could not observe a stale key")
	}
	return out
}

// TestOriginKeysNameRealSchemaProperties pins the direction of drift that costs
// a reader the most: an origin key naming a property the schema no longer
// declares. Each key here is a hand-written literal and each mirrors a schema
// property path, so a renamed property leaves the literal behind — and because
// the literal is only ever printed, never matched, nothing else in the suite
// notices. `config show --origin` would then keep printing a key no config file
// can legally carry.
//
// The check runs one direction on purpose. A schema property with no origin
// entry is a coverage question that needs a per-key judgement about whether the
// setting is attributable at all; a key naming a property that is gone is
// unambiguously stale, and that is what this refuses.
//
// A key whose leading segment addresses an array's items or a `$ref` target is
// skipped rather than failed: the walk does not descend those, so it cannot
// speak to them, and failing what it cannot see would make the test lie about
// its own reach.
func TestOriginKeysNameRealSchemaProperties(t *testing.T) {
	t.Parallel()
	declared := schemaPropertyPaths(t)

	// Roots the walk does not descend. Named explicitly so adding one is a
	// deliberate act with a visible reason, not a silent widening.
	undescended := map[string]bool{
		"registries": true, // array items
		"signing":    true, // $ref target
		"plugins":    true, // per-socket lists of $ref items
	}

	checked := 0
	for _, f := range originFields() {
		root := f.name
		if i := strings.IndexByte(root, '.'); i >= 0 {
			root = root[:i]
		}
		if undescended[root] {
			continue
		}
		checked++
		if !declared[f.name] {
			t.Errorf("origin key %q names no property the config schema declares; "+
				"a schema rename left the literal behind", f.name)
		}
	}
	if checked == 0 {
		t.Fatal("no origin key was checked, so a passing result would prove nothing")
	}
	t.Logf("checked %d origin keys against %d declared schema property paths", checked, len(declared))
}
