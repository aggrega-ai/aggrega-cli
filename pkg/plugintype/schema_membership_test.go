// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package plugintype_test

import (
	"encoding/json"
	"sort"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
)

// TestSchemaPluginTypeEnumsMatchRegistry proves the two published plugin-type
// enums — the entity schema's metadata.pluginType and the wire descriptor
// schema's plugin_type — are both exactly the plugin types the registry
// declares. It reads the schema bytes from the embedded aggrega-schemas module
// (the single published source, never a file path) so a drift between a
// hand-edited schema enum and the registry fails loudly here.
//
// The expected set is the canonical string of every registry entry: a type the
// registry carries is one an author may write as a pluginType and a guest may
// declare on the wire, so the registry and the two published enums name one set
// across all three surfaces.
//
// This guard covers the schema enums only. The describe-consts and the
// workspace-config socket set are checked by their own superset guard, which
// composes with this one rather than duplicating it.
func TestSchemaPluginTypeEnumsMatchRegistry(t *testing.T) {
	want := registryTypeStrings()

	entityEnum := extractEnum(t, "entity (metadata.pluginType)", []byte(schemas.PluginV1alpha1),
		"properties", "metadata", "properties", "pluginType", "enum")
	assertEnumEqualsRegistry(t, "entity (metadata.pluginType)", entityEnum, want)

	wireEnum := extractEnum(t, "wire (plugin_type)", []byte(schemas.WasmDescribeV1alpha1),
		"$defs", "PluginDescriptor", "properties", "plugin_type", "enum")
	assertEnumEqualsRegistry(t, "wire (plugin_type)", wireEnum, want)
}

// registryTypeStrings returns the canonical strings of every registry entry,
// sorted for order-independent comparison.
func registryTypeStrings() []string {
	var out []string
	for _, e := range plugintype.Entries() {
		out = append(out, string(e.Type))
	}
	sort.Strings(out)
	return out
}

// extractEnum parses schema bytes and walks the given key path to an enum array
// of strings, returning the values sorted. It fails the test if the path is
// absent or any element is not a string, which localizes a schema restructure
// to this guard rather than a silent empty comparison.
func extractEnum(t *testing.T, side string, raw []byte, path ...string) []string {
	t.Helper()
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s: parse schema JSON: %v", side, err)
	}
	cur := doc
	for i, key := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("%s: schema path %v: element %q is not an object", side, path, path[i])
		}
		next, ok := obj[key]
		if !ok {
			t.Fatalf("%s: schema path %v: key %q not found", side, path, key)
		}
		cur = next
	}
	arr, ok := cur.([]any)
	if !ok {
		t.Fatalf("%s: schema path %v does not resolve to an array", side, path)
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("%s: enum element %v is not a string", side, v)
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// assertEnumEqualsRegistry fails when the schema enum diverges from the
// registry's type set, naming the diverged side and directing the author to
// reconcile against the pkg/plugintype registry so a future added type or a
// dropped enum member is caught here rather than at author time.
func assertEnumEqualsRegistry(t *testing.T, side string, got, want []string) {
	t.Helper()
	if !equalStringSlices(got, want) {
		t.Errorf("%s schema enum = %v; want %v (the plugin types the registry declares)\n"+
			"the schema enum and the pkg/plugintype registry disagree; reconcile the schema "+
			"against the registry — every registry type appears in the enum, and the enum names "+
			"no type the registry does not carry", side, got, want)
	}
}

// equalStringSlices reports whether two already-sorted string slices are equal.
func equalStringSlices(a, b []string) bool {
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
