// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/editops"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
)

// TestWireValueSetsMatchTheirDeclarations holds each closed value set the
// serve-wire schema publishes to the Go declaration that decides it.
//
// The schema states each set as described constants so that a client reading the
// published contract learns which values a field takes and what each one means.
// A JSON document cannot read a Go enumeration, so every such set is a second
// statement of a bound declared in this module — and a second statement agrees
// only until a value is added to one side. This is what turns that disagreement
// into a failure naming the position, rather than a published contract that
// quietly stops describing the daemon.
//
// The comparison is ordered: each set is published in its declaration's own
// order, so a reader meets the values in the order the code states them.
func TestWireValueSetsMatchTheirDeclarations(t *testing.T) {
	t.Parallel()

	doc := wireSchemaDocument(t)

	edits := make([]string, 0, len(editops.Kinds()))
	for _, k := range editops.Kinds() {
		edits = append(edits, string(k))
	}
	deprecations := []string{
		string(deprecation.SeverityWarning),
		string(deprecation.SeverityError),
		string(deprecation.SeverityRetracted),
	}

	for _, tc := range []struct {
		pointer string
		want    []string
	}{
		{"#/$defs/Diagnostic/properties/severity", []string{editorSeverityError, editorSeverityWarning, editorSeverityInfo}},
		{"#/$defs/DeprecateArtifactParams/properties/severity", deprecations},
		{"#/$defs/DeprecationNote/properties/severity", deprecations},
		{"#/$defs/DeprecationMarked/properties/severity", deprecations},
		{"#/$defs/EditDocumentParams/properties/edit", edits},
	} {
		t.Run(tc.pointer, func(t *testing.T) {
			t.Parallel()
			position, err := resolveSchemaPointer(doc, tc.pointer)
			if err != nil {
				t.Fatalf("%v — the row names a position the shipped schema does not declare, so it would check nothing", err)
			}
			got := publishedConstants(t, tc.pointer, position)
			if !slices.Equal(got, tc.want) {
				t.Errorf("%s publishes %q; the declaration it states decides %q", tc.pointer, got, tc.want)
			}
		})
	}
}

// publishedConstants reads the values a position publishes as described
// constants: a oneOf whose every branch is a const carrying its own description.
// A branch without a description is refused, because the description is the
// half of the set a reader cannot get from the value alone.
func publishedConstants(t *testing.T, pointer string, position map[string]any) []string {
	t.Helper()

	branches, ok := position["oneOf"].([]any)
	if !ok || len(branches) == 0 {
		t.Fatalf("%s publishes no oneOf of described constants", pointer)
	}
	values := make([]string, 0, len(branches))
	for i, branch := range branches {
		object, ok := branch.(map[string]any)
		if !ok {
			t.Fatalf("%s: branch %d is not an object", pointer, i)
		}
		value, ok := object["const"].(string)
		if !ok {
			t.Fatalf("%s: branch %d carries no string const", pointer, i)
		}
		if description, _ := object["description"].(string); description == "" {
			t.Errorf("%s: the branch for %q carries no description", pointer, value)
		}
		values = append(values, value)
	}
	return values
}
