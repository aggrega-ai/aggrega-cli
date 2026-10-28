// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

// TestWalkNonStringTemplate_NamesTheShapeWritten pins the wording of a predicate
// keyword whose value is not a template: the shape the value was written in, in the
// author's words, never the Go type it was decoded into.
func TestWalkNonStringTemplate_NamesTheShapeWritten(t *testing.T) {
	t.Parallel()
	for _, keyword := range []string{"x-aggrega-validate", "x-aggrega-when"} {
		t.Run(keyword, func(t *testing.T) {
			t.Parallel()
			schema := &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"field": {Type: "string", Extra: map[string]any{keyword: []any{"not", "a", "template"}}},
				},
			}
			_, err := New(schema)
			if err == nil {
				t.Fatal("a predicate written as a list must be refused")
			}
			if !strings.Contains(err.Error(), "expected a template written as text; got a list") {
				t.Errorf("the refusal must name the shape written: %v", err)
			}
			if strings.Contains(err.Error(), "[]interface") {
				t.Errorf("the refusal still names a Go type: %v", err)
			}
		})
	}
}
