// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validateops

import (
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// TestEveryDeclaredKeyLeftEmptyIsReadUnderACatalogSchema reads a drafted entry
// against a compiled catalog contract: every key the contract declares and the
// entry writes empty is read, required or not and at any depth, with the
// description its own schema gives it and whether it is sensitive, the path
// opening with the prefix given; a declared key carrying a value, a key no
// property declares, a value the schema names the empty string as, and a key
// the entry does not write are not.
//
// It is the same reading validate's required-only one takes, so the row beside it
// holds that one unchanged over the same document: only the required value.
func TestEveryDeclaredKeyLeftEmptyIsReadUnderACatalogSchema(t *testing.T) {
	t.Parallel()
	schema := map[string]any{
		"type": "object",
		"$defs": map[string]any{
			"origin": map[string]any{"type": "string", "x-aggrega-sensitive": true,
				"description": "Where the  secret\ncomes from."},
		},
		"required": []any{"infra"},
		"properties": map[string]any{
			"infra": map[string]any{
				"type":     "object",
				"required": []any{"port"},
				"properties": map[string]any{
					"image":  map[string]any{"type": "string", "description": "Container image. It runs the server."},
					"port":   map[string]any{"type": "string"},
					"tag":    map[string]any{"type": "string"},
					"mode":   map[string]any{"enum": []any{"", "fast"}},
					"origin": map[string]any{"$ref": "#/$defs/origin"},
					"nested": map[string]any{"type": "object", "properties": map[string]any{
						"inner": map[string]any{"type": "string"},
					}},
				},
			},
		},
	}
	data := []byte("infra:\n  image: \"\"\n  port: \"\"\n  tag: v1\n  mode: \"\"\n  undeclared: \"\"\n  origin: \"\"\n" +
		"  nested:\n    inner: \"\"\n")
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}

	got := PlaceholdersUnder(schema, doc.Content[0], data, []string{"spec", "components", "api"})
	var paths []string
	for _, p := range got {
		paths = append(paths, p.KeyPath)
		if p.KeyPath != strings.Join(p.Keys, ".") {
			t.Errorf("%s joins %v otherwise", p.KeyPath, p.Keys)
		}
	}
	want := []string{"spec.components.api.infra.image", "spec.components.api.infra.port",
		"spec.components.api.infra.origin", "spec.components.api.infra.nested.inner"}
	if !slices.Equal(paths, want) {
		t.Fatalf("read %v; want %v", paths, want)
	}
	if got[0].Description != "Container image. It runs the server." || got[0].Sensitive {
		t.Errorf("the image reads as %+v; want its whole description, not sensitive", got[0])
	}
	if got[2].Description != "Where the secret comes from." || !got[2].Sensitive {
		t.Errorf("the origin reads as %+v; want the referenced schema's folded description, sensitive", got[2])
	}

	found := readPlaceholdersUnder(schema, schema, doc.Content[0], data, nil, false)
	if len(found) != 1 || found[0].path != "infra.port" {
		t.Errorf("the required-only reading over the same document found %+v; want infra.port alone", found)
	}
}

// TestADeclarationWrittenBesideAReferenceIsRead reads, for a value left empty
// under a property whose schema is a reference, what the property declares
// beside the reference as well as what the schema it names declares: either one
// declaring the value sensitive makes it sensitive, and the description written
// nearest the property is the one given.
//
// 🔴 PLANTED DEFECT: read the description and the sensitivity off the resolved
// schema alone (r.resolve(written, 0) in place of written in declaredAlong's
// call): the beside and chain rows report the value not sensitive, and the
// beside-description row gives the referenced schema's description.
func TestADeclarationWrittenBesideAReferenceIsRead(t *testing.T) {
	t.Parallel()
	defs := map[string]any{
		"plain":  map[string]any{"type": "string", "description": "Named by the definition."},
		"secret": map[string]any{"type": "string", "x-aggrega-sensitive": true, "description": "A secret definition."},
		"middle": map[string]any{"$ref": "#/$defs/plain", "x-aggrega-sensitive": true},
	}
	for _, tc := range []struct {
		name            string
		property        map[string]any
		wantSensitive   bool
		wantDescription string
	}{
		{"the marker beside the reference", map[string]any{"$ref": "#/$defs/plain", "x-aggrega-sensitive": true},
			true, "Named by the definition."},
		{"the description beside the reference", map[string]any{"$ref": "#/$defs/plain",
			"description": "Named beside the reference."}, false, "Named beside the reference."},
		{"both beside the reference", map[string]any{"$ref": "#/$defs/plain", "x-aggrega-sensitive": true,
			"description": "A secret named beside."}, true, "A secret named beside."},
		{"the marker inside the referenced schema", map[string]any{"$ref": "#/$defs/secret"},
			true, "A secret definition."},
		{"the marker beside a reference the reference leads through", map[string]any{"$ref": "#/$defs/middle"},
			true, "Named by the definition."},
		{"a marker that is not the boolean true", map[string]any{"$ref": "#/$defs/plain",
			"x-aggrega-sensitive": "example/credential"}, false, "Named by the definition."},
		{"nothing declared beside the reference", map[string]any{"$ref": "#/$defs/plain"},
			false, "Named by the definition."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			schema := map[string]any{
				"type":       "object",
				"$defs":      defs,
				"properties": map[string]any{"origin": tc.property},
			}
			data := []byte("origin: \"\"\n")
			var doc yaml.Node
			if err := yaml.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			got := PlaceholdersUnder(schema, doc.Content[0], data, []string{"spec"})
			if len(got) != 1 || got[0].KeyPath != "spec.origin" {
				t.Fatalf("read %+v; want spec.origin alone", got)
			}
			if got[0].Sensitive != tc.wantSensitive || got[0].Description != tc.wantDescription {
				t.Errorf("spec.origin reads sensitive %v described %q; want sensitive %v described %q",
					got[0].Sensitive, got[0].Description, tc.wantSensitive, tc.wantDescription)
			}
		})
	}
}

// TestADraftedDocumentOfAnyKindTheBinaryCarriesIsReadAgainstItsKind reads a
// Release — a kind the single-entity scope does not validate — through the
// exported kind reading, and refuses a kind the binary carries no schema for.
func TestADraftedDocumentOfAnyKindTheBinaryCarriesIsReadAgainstItsKind(t *testing.T) {
	t.Parallel()
	release := "apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: r\n  environment: development\n" +
		"  version: 0.1.0\nspec:\n  aggregate: a\n  manifest: \"\"\n  components:\n    api: {}\n"
	got, err := PlaceholdersOf(envelope.Kind("Release"), []byte(release), "release.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].KeyPath != "spec.manifest" || !slices.Equal(got[0].Keys, []string{"spec", "manifest"}) {
		t.Errorf("the Release reads as %+v; want spec.manifest alone", got)
	}
	if _, err := PlaceholdersOf(envelope.Kind("Nonesuch"), []byte(release), "x.yaml"); err == nil {
		t.Error("a kind the binary carries no schema for was read")
	}
}
