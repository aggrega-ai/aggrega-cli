// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types_test

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The render context is hand-written rather than generated from its schema, and
// nothing validates a context against that schema at run time, so the two are
// held together here: position by position, in both directions. A property the
// schema declares at a position the Go type has no field for is a pillar a
// template is promised and never handed; a field the Go type carries at a
// position the schema does not declare is a pillar a template can read that the
// published contract does not name.
//
// The walk pairs a schema position with a Go type: an object with declared
// properties against a struct, by json tag; an object whose entries are data
// against a keyed map, through its additionalProperties; an array against a
// slice, through its items. A position the schema leaves open — an object with
// no declared properties that admits any — is opaque on purpose and ends the
// walk there, as does an interface-typed field and a reference into another
// schema document, which that document's own alignment holds.

// contextSchemaFindings is what one walk reports.
type contextSchemaFindings struct {
	// unbound names schema properties the Go type has no field for.
	unbound []string
	// undeclared names Go fields the schema does not declare.
	undeclared []string
}

// walkContextSchema pairs one schema position with one Go type and records every
// property-versus-field disagreement beneath it.
func walkContextSchema(node any, goType reflect.Type, path string, defs map[string]any, out *contextSchemaFindings) {
	schema, ok := resolveContextSchema(node, defs)
	if !ok {
		return
	}
	for goType.Kind() == reflect.Pointer {
		goType = goType.Elem()
	}
	switch goType.Kind() {
	case reflect.Interface:
		return
	case reflect.Slice, reflect.Array:
		walkContextSchema(schema["items"], goType.Elem(), path+"[]", defs, out)
		return
	case reflect.Map:
		walkContextSchemaEntries(schema, goType.Elem(), path, defs, out)
		return
	case reflect.Struct:
	default:
		return
	}
	if strings.HasPrefix(goType.Name(), "OrderedMap[") {
		values, _ := goType.FieldByName("values")
		walkContextSchemaEntries(schema, values.Type.Elem(), path, defs, out)
		return
	}
	props, declares := schema["properties"].(map[string]any)
	if !declares {
		// An object that declares no property is open by design; a struct read
		// against it carries no contract to compare.
		return
	}
	fields := map[string]reflect.Type{}
	for i := 0; i < goType.NumField(); i++ {
		f := goType.Field(i)
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		fields[name] = f.Type
		if _, declared := props[name]; !declared {
			out.undeclared = append(out.undeclared, path+"."+name)
		}
	}
	for name, sub := range props {
		fieldType, bound := fields[name]
		if !bound {
			out.unbound = append(out.unbound, path+"."+name)
			continue
		}
		walkContextSchema(sub, fieldType, path+"."+name, defs, out)
	}
}

// walkContextSchemaEntries walks a keyed map: every entry the schema names and
// every entry it admits are read as the map's element type.
func walkContextSchemaEntries(schema map[string]any, elem reflect.Type, path string, defs map[string]any, out *contextSchemaFindings) {
	if props, ok := schema["properties"].(map[string]any); ok {
		for name, sub := range props {
			walkContextSchema(sub, elem, path+"."+name, defs, out)
		}
	}
	if entry, ok := schema["additionalProperties"].(map[string]any); ok {
		walkContextSchema(entry, elem, path+".*", defs, out)
	}
}

// resolveContextSchema follows a reference within the document and picks the one
// object-shaped arm of a nullable union. It reports false where the walk ends: a
// reference into another schema document, or a position with nothing to compare.
func resolveContextSchema(node any, defs map[string]any) (map[string]any, bool) {
	schema, ok := node.(map[string]any)
	if !ok {
		return nil, false
	}
	if ref, isRef := schema["$ref"].(string); isRef {
		name, local := strings.CutPrefix(ref, "#/$defs/")
		if !local {
			return nil, false
		}
		return resolveContextSchema(defs[name], defs)
	}
	if arms, union := schema["oneOf"].([]any); union {
		for _, arm := range arms {
			armSchema, _ := arm.(map[string]any)
			if _, isRef := armSchema["$ref"]; isRef {
				return resolveContextSchema(armSchema, defs)
			}
			if armSchema["type"] == "object" {
				return resolveContextSchema(armSchema, defs)
			}
		}
		return nil, false
	}
	return schema, true
}

// TestTemplateContextMirrorsItsSchema holds the render context, the connection view
// and the attachment shape to template-context-v1beta1.schema.json in both
// directions. The two control rows walk a type that drops a declared field and a
// type that adds an undeclared one against the same definitions, and must be
// reported exactly — so a clean result on the real rows comes from a walk that
// can fail.
func TestTemplateContextMirrorsItsSchema(t *testing.T) {
	t.Parallel()
	var doc map[string]any
	if err := json.Unmarshal([]byte(schemas.TemplateContextV1beta1), &doc); err != nil {
		t.Fatalf("decode the template-context schema: %v", err)
	}
	defs, _ := doc["$defs"].(map[string]any)
	if defs["enriched_edge"] == nil || defs["template_attachment"] == nil {
		t.Fatal("the schema no longer defines the connection view or the attachment shape under $defs")
	}

	// controlAttachmentShort carries one declared field of the attachment shape.
	type controlAttachmentShort struct {
		Type string `json:"type"`
	}
	// controlAttachmentLong carries every declared field and one more.
	type controlAttachmentLong struct {
		Type            string                   `json:"type"`
		ResolvedVersion string                   `json:"resolved_version"`
		Labels          types.OrderedMap[string] `json:"labels"`
		Values          types.OrderedMap[any]    `json:"values"`
		Infra           types.OrderedMap[any]    `json:"infra"`
		Provides        types.OrderedMap[string] `json:"provides"`
		Extra           string                   `json:"extra"`
	}

	for _, tc := range []struct {
		name           string
		schema         any
		root           reflect.Type
		wantUnbound    []string
		wantUndeclared []string
	}{
		{name: "the render context", schema: doc, root: reflect.TypeOf(types.TemplateContext{})},
		{name: "the connection view", schema: defs["enriched_edge"], root: reflect.TypeOf(types.EnrichedEdge{})},
		{name: "the attachment shape", schema: defs["template_attachment"], root: reflect.TypeOf(types.TemplateAttachment{})},
		{
			name:        "control: a type missing declared fields",
			schema:      defs["template_attachment"],
			root:        reflect.TypeOf(controlAttachmentShort{}),
			wantUnbound: []string{".infra", ".labels", ".provides", ".resolved_version", ".values"},
		},
		{
			name:           "control: a type carrying an undeclared field",
			schema:         defs["template_attachment"],
			root:           reflect.TypeOf(controlAttachmentLong{}),
			wantUndeclared: []string{".extra"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got contextSchemaFindings
			walkContextSchema(tc.schema, tc.root, "", defs, &got)
			sort.Strings(got.unbound)
			sort.Strings(got.undeclared)
			if !reflect.DeepEqual(got.unbound, tc.wantUnbound) {
				t.Errorf("schema properties with no Go field = %v; want %v", got.unbound, tc.wantUnbound)
			}
			if !reflect.DeepEqual(got.undeclared, tc.wantUndeclared) {
				t.Errorf("Go fields the schema does not declare = %v; want %v", got.undeclared, tc.wantUndeclared)
			}
		})
	}
}
