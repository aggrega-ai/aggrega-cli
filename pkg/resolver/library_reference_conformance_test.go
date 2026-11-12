// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// referenceLibrary holds one entry of each shape a reference can name: a
// string with a pattern, an object, and an object meant to be referenced as a
// whole pillar.
const referenceLibrary = `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: Names
  version: 1.0.0
spec:
  schemas:
    Slug:
      type: string
      pattern: "^[a-z][a-z0-9-]*$"
    Endpoint:
      type: object
      properties:
        host:
          type: string
        port:
          type: integer
    AppValues:
      type: object
      properties:
        name:
          type: string
          pattern: "^[a-z][a-z0-9-]*$"
`

// TestPillarConformance_LibraryReferenceValidatesAsTheEntry measures authored
// values against a Component schema that references SchemaLibrary entries, in
// each authoring form a reference is written in. A value is validated as the
// entry its reference names, whatever that entry's type: a string entry accepts
// a string matching its pattern and refuses one that does not, naming the
// pattern, while an object entry referenced from a property or as the whole
// pillar keeps validating its own properties.
func TestPillarConformance_LibraryReferenceValidatesAsTheEntry(t *testing.T) {
	t.Parallel()

	forms := []struct {
		name   string
		schema string
		object bool // the form references the object entry from a property
	}{
		{"compact form property references", `values:
  name:
    $ref: "Names#/schemas/Slug"
  endpoint:
    $ref: "Names#/schemas/Endpoint"
`, true},
		{"JSON Schema form property references", `values:
  type: object
  properties:
    name:
      $ref: "Names#/schemas/Slug"
    endpoint:
      $ref: "Names#/schemas/Endpoint"
`, true},
		{"whole pillar reference", `values:
  $ref: "Names#/schemas/AppValues"
`, false},
	}

	endpoint := func(port any) types.OrderedMap[any] {
		ep := types.OrderedMap[any]{}
		ep.Set("host", "db")
		ep.Set("port", port)
		return ep
	}
	values := func(name string, withEndpoint bool, port any) types.OrderedMap[any] {
		v := types.OrderedMap[any]{}
		v.Set("name", name)
		if withEndpoint {
			v.Set("endpoint", endpoint(port))
		}
		return v
	}

	const prefix = `.spec.components["app"].values`
	const pattern = "^[a-z][a-z0-9-]*$"

	for _, form := range forms {
		form := form
		t.Run(form.name, func(t *testing.T) {
			t.Parallel()
			compiled := compileEntitySchemaWithLibrary(t, form.schema, referenceLibrary)
			validate := func(instance types.OrderedMap[any]) []*ResolverError {
				return ValidatePillarConformance(compiled, "values", instance, nil,
					"Aggregate probe", `.spec.components["app"]`, "the Component schema")
			}

			// Accepted: a name matching the entry's pattern.
			if errs := validate(values("good-name", form.object, 5432)); len(errs) != 0 {
				t.Fatalf("a value matching the referenced entry was refused: %v", errs)
			}

			// Refused: a name the entry's pattern does not match, at the
			// property, naming the pattern — and never as a type mismatch.
			errs := validate(values("Bad_Name", form.object, 5432))
			if len(errs) != 1 {
				t.Fatalf("got %d findings for a non-matching name, want 1: %v", len(errs), errs)
			}
			got := errs[0]
			if got.Field != prefix+".name" {
				t.Errorf("finding at %q, want %q", got.Field, prefix+".name")
			}
			if !errors.Is(got, ErrSchemaConformance()) {
				t.Errorf("finding does not wrap the schema-conformance condition: %v", got.Err)
			}
			if !strings.Contains(got.Message, "does not match") || !strings.Contains(got.Message, pattern) {
				t.Errorf("finding does not name the entry's pattern: %q", got.Message)
			}
			if strings.Contains(got.Message, `want \"object\"`) || strings.Contains(got.Message, `want "object"`) {
				t.Errorf("a string entry was measured as an object: %q", got.Message)
			}

			if !form.object {
				return
			}
			// The object entry referenced from a property still measures its
			// own properties.
			errs = validate(values("good-name", true, "not-a-number"))
			if len(errs) != 1 {
				t.Fatalf("got %d findings for a non-integer port, want 1: %v", len(errs), errs)
			}
			if errs[0].Field != prefix+".endpoint.port" {
				t.Errorf("finding at %q, want %q", errs[0].Field, prefix+".endpoint.port")
			}
			if !strings.Contains(errs[0].Message, "integer") {
				t.Errorf("finding does not name the object entry's property type: %q", errs[0].Message)
			}
		})
	}
}
