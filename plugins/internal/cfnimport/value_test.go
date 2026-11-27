// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"encoding/json"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cfncore/cfntemplate"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// sameAsDecodedPerLevel fails unless v holds, at every level, what decoding
// raw one level at a time gives: an object's keys and member texts as
// importer.ObjectEntries reads them, an array's item texts as encoding/json
// reads them, and the text of any other value as written. It is an
// independent cross-check of the decoder against the plugin kit's reading of
// one object and the standard library's reading of one array.
func sameAsDecodedPerLevel(t *testing.T, at string, v *value, raw json.RawMessage) {
	t.Helper()
	if string(v.json()) != string(raw) {
		t.Fatalf("%s: the value holds %s, want %s", at, v.json(), raw)
	}
	switch kind(v) {
	case '{':
		entries := importer.ObjectEntries(raw)
		if len(v.members) != len(entries) {
			t.Fatalf("%s: the value holds %d members, want %d", at, len(v.members), len(entries))
		}
		for i, e := range entries {
			if v.members[i].Key != e.Key {
				t.Fatalf("%s: member %d is keyed %q, want %q", at, i, v.members[i].Key, e.Key)
			}
			sameAsDecodedPerLevel(t, at+jsonPointer(e.Key), v.members[i].Value, e.Value)
		}
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			t.Fatalf("%s: %v", at, err)
		}
		if len(v.items) != len(items) {
			t.Fatalf("%s: the value holds %d items, want %d", at, len(v.items), len(items))
		}
		for i, item := range items {
			sameAsDecodedPerLevel(t, at+jsonPointer(strconv.Itoa(i)), v.items[i], item)
		}
	default:
		if v.members != nil || v.items != nil {
			t.Fatalf("%s: a scalar holds members or items", at)
		}
	}
}

// TestDecodedHoldsWhatEachLevelDecodes decodes, once, the long form the
// template reader writes for every template of the testdata, and documents
// written with space, escaped keys and text, numbers in every form and
// nesting, and finds at every level what decoding that level alone gives.
func TestDecodedHoldsWhatEachLevelDecodes(t *testing.T) {
	t.Parallel()
	docs := map[string]json.RawMessage{
		"space":   json.RawMessage(" {\n \"b\" : [ 1 , -2.5e+3 , true , false , null ] ,\t\"a\" : { } , \"c\" : [ ] }\n"),
		"escapes": json.RawMessage(`{"k\"ey":"v\\al\"ue","\u00e9t\u00e9":"\u2028","\\":{"/":["\"",""]},"é":"ü"}`),
		"nesting": json.RawMessage(`[[[{"a":[{"b":[[]]}]}]],{},"x",0]`),
		"scalar":  json.RawMessage(`  "text"  `),
		"number":  json.RawMessage(`12345678901234567890`),
	}
	err := filepath.WalkDir("testdata", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !slices.Contains([]string{".yaml", ".yml", ".json", ".template"}, filepath.Ext(p)) {
			return err
		}
		rel, err := filepath.Rel("testdata", p)
		if err != nil {
			return err
		}
		if tree, err := cfntemplate.Normalize(fixture(t, filepath.ToSlash(rel))); err == nil {
			docs[p] = tree
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range docs {
		v := decoded(raw)
		if v == nil {
			t.Fatalf("%s: nothing is decoded", name)
		}
		var trimmed json.RawMessage
		if err := json.Unmarshal(raw, &trimmed); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		sameAsDecodedPerLevel(t, name, v, trimmed)
	}
}

// TestDecodedHoldsTextThatIsNoJSONAlone reads text that is no JSON value as a
// value holding that text, with no member or item, without panicking, and
// nothing as no value.
func TestDecodedHoldsTextThatIsNoJSONAlone(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`{"a":`, `[1,`, `{"a" 1}`, `"open`, `{}{}`, `tru`} {
		v := decoded(json.RawMessage(raw))
		if v == nil || string(v.raw) != raw || v.members != nil || v.items != nil {
			t.Errorf("%s is decoded as %+v, want a value holding its text alone", raw, v)
		}
	}
	for _, raw := range []string{"", " \n\t"} {
		if v := decoded(json.RawMessage(raw)); v != nil {
			t.Errorf("%q is decoded as %+v, want no value", raw, v)
		}
	}
}

// TestJSONPointerIsTheKitsPointer writes the pointer the plugin kit writes for
// a plain token, a token holding "~", one holding "/", and several tokens
// together.
func TestJSONPointerIsTheKitsPointer(t *testing.T) {
	t.Parallel()
	for _, tokens := range [][]string{
		{"Resources"},
		{"a~b"},
		{"a/b"},
		{"~1/~0"},
		{"Resources", "My~Bucket", "Properties", "a/b", "0", ""},
	} {
		if got, want := jsonPointer(tokens...), importer.Pointer(tokens...); got != want {
			t.Errorf("the pointer of %q is %q, want the plugin kit's %q", tokens, got, want)
		}
	}
}
