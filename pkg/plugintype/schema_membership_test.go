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
// schema's plugin_type — are both exactly the non-reserved plugin types the
// registry declares. It reads the schema bytes from the embedded aggrega-schemas
// module (the single published source, never a file path) so a drift between a
// hand-edited schema enum and the registry fails loudly here.
//
// The expected set is the canonical strings of every registry entry whose
// Reserved field is false. Reserved types (the import socket) are intentionally
// excluded from both schema enums: a reserved entry names a workspace socket
// with no realized plugin type, so it is not an authorable pluginType and must
// not appear in the entity or wire enum. The subset the schemas publish is
// therefore precisely "the non-reserved types" — the same set on both surfaces.
//
// This guard covers the schema enums only. The describe-consts and the
// workspace-config socket set are checked by their own superset guard, which
// composes with this one rather than duplicating it.
func TestSchemaPluginTypeEnumsMatchRegistry(t *testing.T) {
	want := nonReservedRegistryTypes()

	entityEnum := extractEnum(t, "entity (metadata.pluginType)", []byte(schemas.PluginV1alpha1),
		"properties", "metadata", "properties", "pluginType", "enum")
	assertEnumEqualsRegistry(t, "entity (metadata.pluginType)", entityEnum, want)

	wireEnum := extractEnum(t, "wire (plugin_type)", []byte(schemas.WasmDescribeV1alpha1),
		"$defs", "PluginDescriptor", "properties", "plugin_type", "enum")
	assertEnumEqualsRegistry(t, "wire (plugin_type)", wireEnum, want)
}

// nonReservedRegistryTypes returns the canonical strings of every registry entry
// that is not reserved, sorted for order-independent comparison.
func nonReservedRegistryTypes() []string {
	var out []string
	for _, e := range plugintype.Entries() {
		if e.Reserved {
			continue
		}
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
// registry's non-reserved set, naming the diverged side and directing the author
// to reconcile against the pkg/plugintype registry so a future added type or a
// dropped enum member is caught here rather than at author time.
func assertEnumEqualsRegistry(t *testing.T, side string, got, want []string) {
	t.Helper()
	if !equalStringSlices(got, want) {
		t.Errorf("%s schema enum = %v; want %v (the registry's non-reserved plugin types)\n"+
			"the schema enum and the pkg/plugintype registry disagree; reconcile the schema "+
			"against the registry — every non-reserved registry type appears in the enum, and no "+
			"reserved type does", side, got, want)
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
