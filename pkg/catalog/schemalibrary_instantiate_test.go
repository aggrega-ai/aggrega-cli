// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for `$instantiate:` resolution at SchemaLibrary load. A schema entry
// authored as a template reference is substituted where the library is turned
// into a value, so every consumer downstream reads a resolved entry and none
// of them sees the reference. These tests drive the real load path rather
// than a hand-built library value, because the substitution's whole point is
// that it happens on the way in.
//
// Each negative fixture carries exactly one fault and asserts the condition
// that fault pins, so a fixture cannot keep passing on a different rejection.

package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// loadLibraryFixture writes one SchemaLibrary document into a fresh directory
// and loads it through the catalog's own entry point, returning the loaded
// library. A load failure is returned rather than fatal, so the negative
// fixtures below can assert on it.
func loadLibraryFixture(t *testing.T, src string) (types.SchemaLibrary, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "library.yaml"), []byte(src), 0o644); err != nil {
		t.Fatalf("write library fixture: %v", err)
	}
	c, err := LoadAll("", dir)
	if err != nil {
		return types.SchemaLibrary{}, err
	}
	lib, ok := c.SchemaLibraries["DbSizing"]
	if !ok {
		t.Fatalf("DbSizing not registered; keys = %v", schemaLibraryKeys(c))
	}
	return lib, nil
}

// errorChainText joins the message of every error in a chain. A catalog load
// failure renders its cause chain at the command layer, so the outermost
// error's own message carries only the load context; the sentence naming what
// is wrong sits further down.
func errorChainText(err error) string {
	var parts []string
	var walk func(error)
	walk = func(e error) {
		if e == nil {
			return
		}
		parts = append(parts, e.Error())
		switch u := e.(type) {
		case interface{ Unwrap() error }:
			walk(u.Unwrap())
		case interface{ Unwrap() []error }:
			for _, inner := range u.Unwrap() {
				walk(inner)
			}
		}
	}
	walk(err)
	return strings.Join(parts, "\n")
}

// entryScalarAt walks a loaded entry's content along a key path and returns
// the leaf scalar's value, failing the test when the path is absent.
func entryScalarAt(t *testing.T, entry types.SchemaEntry, path ...string) *yaml.Node {
	t.Helper()
	current := &entry.Content
	for _, key := range path {
		if current.Kind != yaml.MappingNode {
			t.Fatalf("at %q: node is not a mapping", key)
		}
		var match *yaml.Node
		for i := 0; i+1 < len(current.Content); i += 2 {
			if current.Content[i].Value == key {
				match = current.Content[i+1]
				break
			}
		}
		if match == nil {
			t.Fatalf("key %q absent under path %v", key, path)
		}
		current = match
	}
	return current
}

// TestSchemaLibraryInstantiate_Resolves verifies that a library entry authored
// as a template reference is loaded already substituted: the bound argument
// replaces its placeholder, a parameter left unbound falls back to its
// declared default, and the entry no longer carries the reference keys.
func TestSchemaLibraryInstantiate_Resolves(t *testing.T) {
	t.Parallel()

	lib, err := loadLibraryFixture(t, `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: DbSizing
  version: 1.0.0
spec:
  schemas:
    Storage:
      $instantiate: StorageTemplate
      $args:
        defaultSize: 10Gi
  templates:
    StorageTemplate:
      $params:
        defaultSize:
          type: string
        tier:
          type: string
          default: standard
      type: object
      properties:
        size:
          type: string
          default: $defaultSize
        tier:
          type: string
          const: $tier
`)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	entry, ok := lib.Spec.Schemas.Get("Storage")
	if !ok {
		t.Fatalf("Storage entry absent; keys = %v", lib.Spec.Schemas.Keys())
	}

	if got := entryScalarAt(t, entry, "type").Value; got != "object" {
		t.Errorf("resolved type = %q, want object", got)
	}
	if got := entryScalarAt(t, entry, "properties", "size", "default").Value; got != "10Gi" {
		t.Errorf("bound parameter substituted to %q, want 10Gi", got)
	}
	if got := entryScalarAt(t, entry, "properties", "tier", "const").Value; got != "standard" {
		t.Errorf("defaulted parameter substituted to %q, want standard", got)
	}
	for i := 0; i+1 < len(entry.Content.Content); i += 2 {
		if key := entry.Content.Content[i].Value; key == "$instantiate" || key == "$args" {
			t.Errorf("resolved entry still carries %q", key)
		}
	}
}

// TestSchemaLibraryInstantiate_PreservesAuthorOrder pins the deterministic
// ordering contract across substitution: resolving an entry in place must not
// move it, or any other entry, in the library's author order.
func TestSchemaLibraryInstantiate_PreservesAuthorOrder(t *testing.T) {
	t.Parallel()

	lib, err := loadLibraryFixture(t, `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: DbSizing
  version: 1.0.0
spec:
  schemas:
    Alpha:
      type: string
    Storage:
      $instantiate: StorageTemplate
      $args:
        defaultSize: 10Gi
    Zulu:
      type: boolean
  templates:
    StorageTemplate:
      $params:
        defaultSize:
          type: string
      type: object
      properties:
        size:
          type: string
          default: $defaultSize
`)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	want := []string{"Alpha", "Storage", "Zulu"}
	got := lib.Spec.Schemas.Keys()
	if len(got) != len(want) {
		t.Fatalf("entry order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entry order = %v, want %v", got, want)
		}
	}
}

// TestSchemaLibraryInstantiate_Rejections walks one fixture per fault. Each
// asserts the condition its own fault pins; the shape faults, which no
// condition declares, assert on the sentence instead.
func TestSchemaLibraryInstantiate_Rejections(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		schemas  string
		wantIs   error
		wantText string
	}{
		{
			name: "template not declared",
			schemas: `    Storage:
      $instantiate: NoSuchTemplate
      $args:
        defaultSize: 10Gi
`,
			wantIs: types.ErrTemplateNotFound(),
		},
		{
			name: "required parameter left unbound",
			schemas: `    Storage:
      $instantiate: StorageTemplate
      $args: {}
`,
			wantIs: types.ErrTemplateParamMissing(),
		},
		{
			name: "bound argument has the wrong type",
			schemas: `    Storage:
      $instantiate: StorageTemplate
      $args:
        defaultSize: 12345
`,
			wantIs: types.ErrTemplateParamTypeMismatch(),
		},
		{
			name: "argument names no declared parameter",
			schemas: `    Storage:
      $instantiate: StorageTemplate
      $args:
        defaultSize: 10Gi
        unknownParam: nope
`,
			wantIs:   types.ErrTemplateParamTypeMismatch(),
			wantText: `"unknownParam" is not declared`,
		},
		{
			name: "template name is not a scalar",
			schemas: `    Storage:
      $instantiate:
        - StorageTemplate
`,
			wantText: "$instantiate must name a template as a scalar",
		},
		{
			name: "binding block is not a mapping",
			schemas: `    Storage:
      $instantiate: StorageTemplate
      $args:
        - 10Gi
`,
			wantText: "$args must be a mapping of parameter bindings",
		},
		{
			name: "a sibling key would be dropped",
			schemas: `    Storage:
      $instantiate: StorageTemplate
      $args:
        defaultSize: 10Gi
      description: a storage sizing shape
`,
			wantText: `"description" would be dropped`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: DbSizing
  version: 1.0.0
spec:
  schemas:
` + tc.schemas + `  templates:
    StorageTemplate:
      $params:
        defaultSize:
          type: string
      type: object
      properties:
        size:
          type: string
          default: $defaultSize
`
			_, err := loadLibraryFixture(t, src)
			if err == nil {
				t.Fatal("expected the library to be refused; it loaded")
			}
			chain := errorChainText(err)
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Errorf("error = %s, want it to match %v", chain, tc.wantIs)
			}
			if tc.wantText != "" && !strings.Contains(chain, tc.wantText) {
				t.Errorf("error = %s, want it to contain %q", chain, tc.wantText)
			}
			if !strings.Contains(chain, `schema "Storage"`) {
				t.Errorf("error = %s, want it to name the offending entry", chain)
			}
		})
	}
}

// TestSchemaLibraryInstantiate_ForbiddenKeywordViaArgs closes the parametric
// route: a forbidden keyword supplied as an argument value lands in the
// resolved body as a keyword, and the scan that runs after substitution
// refuses it. An entry authoring the keyword literally is refused by the scan
// that runs at load on authored content; this is the case that scan cannot
// see, because the keyword is not in the library's text.
func TestSchemaLibraryInstantiate_ForbiddenKeywordViaArgs(t *testing.T) {
	t.Parallel()

	_, err := loadLibraryFixture(t, `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: DbSizing
  version: 1.0.0
spec:
  schemas:
    Storage:
      $instantiate: StorageTemplate
      $args:
        keyword: $dynamicAnchor
  templates:
    StorageTemplate:
      $params:
        keyword:
          type: string
      type: object
      properties:
        size:
          $keyword: anchored
          type: string
`)
	if err == nil {
		t.Fatal("expected the library to be refused; it loaded")
	}
	chain := errorChainText(err)
	if !errors.Is(err, types.ErrForbiddenKeyword()) {
		t.Errorf("error = %s, want it to match ErrForbiddenKeyword", chain)
	}
	if !strings.Contains(chain, "$dynamicAnchor") {
		t.Errorf("error = %s, want it to name the keyword that was introduced", chain)
	}
}
