// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestYamlNodeKindName_DiscriminatesEveryKind pins the discriminator
// for every yaml.Kind plus the zero-value fall-through. The output is
// the diagnostic-formatter input for schema-shape error messages; any
// kind not enumerated falls into the "unknown YAML node kind" bucket.
func TestYamlNodeKindName_DiscriminatesEveryKind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind yaml.Kind
		want string
	}{
		{yaml.ScalarNode, "scalar / block-scalar string"},
		{yaml.SequenceNode, "sequence"},
		{yaml.MappingNode, "mapping"},
		{yaml.AliasNode, "alias"},
		{yaml.Kind(0), "unknown YAML node kind"},
		{yaml.DocumentNode, "unknown YAML node kind"},
	}
	for _, c := range cases {
		if got := yamlNodeKindName(c.kind); got != c.want {
			t.Errorf("yamlNodeKindName(%v) = %q; want %q", c.kind, got, c.want)
		}
	}
}

// TestPropertiesMap_AbsentReturnsFalse pins the no-`properties`-key
// branch: schema leaves and reference nodes without a properties map
// return (nil, false) so the walker terminates the recursion.
func TestPropertiesMap_AbsentReturnsFalse(t *testing.T) {
	t.Parallel()
	node := map[string]any{"type": "string", "format": "email"}
	if got, ok := propertiesMap(node); ok || got != nil {
		t.Errorf("absent properties: got (%v, %v); want (nil, false)", got, ok)
	}
}

// TestPropertiesMap_NonMapValueReturnsFalse pins the type-assert-miss
// branch: a `properties` key whose value is not a map[string]any
// (e.g., a malformed schema where properties is a list) returns
// (nil, false) rather than panic on the failed assertion.
func TestPropertiesMap_NonMapValueReturnsFalse(t *testing.T) {
	t.Parallel()
	node := map[string]any{"properties": []any{"not", "a", "map"}}
	if got, ok := propertiesMap(node); ok || got != nil {
		t.Errorf("non-map properties: got (%v, %v); want (nil, false)", got, ok)
	}
}

// TestPropertiesMap_HappyPathReturnsProperties pins the well-formed
// branch: a node with a proper properties map returns it.
func TestPropertiesMap_HappyPathReturnsProperties(t *testing.T) {
	t.Parallel()
	node := map[string]any{
		"properties": map[string]any{
			"x": map[string]any{"type": "string"},
		},
	}
	props, ok := propertiesMap(node)
	if !ok {
		t.Fatal("expected ok=true on well-formed properties map")
	}
	if _, has := props["x"]; !has {
		t.Errorf("returned map missing 'x' property: %v", props)
	}
}

// TestHasOverlapEscapeHatch_DetectsEveryEscapeClause pins the
// composition-clause discriminator. Any of $ref, patternProperties,
// oneOf, anyOf, allOf, or schema-bodied additionalProperties returns
// true; nodes without any clause return false.
func TestHasOverlapEscapeHatch_DetectsEveryEscapeClause(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		node map[string]any
		want bool
	}{
		{"ref", map[string]any{"$ref": "#/defs/X"}, true},
		{"patternProperties", map[string]any{"patternProperties": map[string]any{}}, true},
		{"oneOf", map[string]any{"oneOf": []any{}}, true},
		{"anyOf", map[string]any{"anyOf": []any{}}, true},
		{"allOf", map[string]any{"allOf": []any{}}, true},
		{"plain", map[string]any{"type": "string"}, false},
		{"propertiesOnly", map[string]any{"properties": map[string]any{}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := hasOverlapEscapeHatch(c.node); got != c.want {
				t.Errorf("hasOverlapEscapeHatch(%s) = %v; want %v", c.name, got, c.want)
			}
		})
	}
}

// TestValidationError_Error_FormatsAllFields pins the Error() output
// when every field is populated: Entity + FieldPath in the header,
// Constraint after an em dash, Actual in parens, Suggestion after a
// period.
func TestValidationError_Error_FormatsAllFields(t *testing.T) {
	t.Parallel()
	ve := &ValidationError{
		Entity:     "Component core/api:1.0.0",
		FieldPath:  ".values.port",
		Constraint: "must be >= 1024",
		Actual:     "80",
		Suggestion: "Use a port number >= 1024",
	}
	msg := ve.Error()
	for _, want := range []string{
		"Component core/api:1.0.0",
		".values.port",
		"must be >= 1024",
		"got 80",
		"Use a port number >= 1024",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("Error() output missing %q; got: %s", want, msg)
		}
	}
}

// TestValidationError_Error_MinimalShape pins the Error() output when
// only the required fields (Entity + FieldPath) are populated. The
// optional clauses (Constraint / Actual / Suggestion) are omitted.
func TestValidationError_Error_MinimalShape(t *testing.T) {
	t.Parallel()
	ve := &ValidationError{
		Entity:    "Feature core/x:1.0.0",
		FieldPath: ".values.foo",
	}
	msg := ve.Error()
	if !strings.Contains(msg, "Feature core/x:1.0.0") || !strings.Contains(msg, ".values.foo") {
		t.Errorf("minimal shape: required fields missing; got: %s", msg)
	}
	// Optional clauses absent from the rendered message.
	for _, absent := range []string{" — ", "(got "} {
		if strings.Contains(msg, absent) {
			t.Errorf("minimal shape should not include %q; got: %s", absent, msg)
		}
	}
}
