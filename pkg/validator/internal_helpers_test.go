// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	stderrors "errors"
	"fmt"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
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

// TestValidationError_Error_FormatsAllFields pins the Error() output when
// every field is populated: the condition's registered sentence opens it, and
// the values this occurrence supplied follow in the parenthesised list.
func TestValidationError_Error_FormatsAllFields(t *testing.T) {
	t.Parallel()
	ve := NewValidationError(envelope.KindComponent, "core/api:1.0.0", ValidationDetail{
		FieldPath:  ".values.port",
		Constraint: "must be >= 1024",
		Actual:     "80",
		Suggestion: "Use a port number >= 1024",
		Err:        ErrInheritanceMerge(),
	})
	msg := ve.Error()
	if sentence := ErrInheritanceMerge().Error(); !strings.HasPrefix(msg, sentence+" (") {
		t.Errorf("Error() must open with the registered sentence %q; got: %s", sentence, msg)
	}
	for _, want := range []string{
		"Component core/api:1.0.0",
		"at .values.port",
		"must be >= 1024",
		"got 80",
		"Use a port number >= 1024",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("Error() output missing %q; got: %s", want, msg)
		}
	}
}

// TestValidationError_Error_MinimalShape pins the Error() output when only the
// condition and the entity reference are supplied. The optional values are
// omitted rather than rendered as empty list entries, so a sparse failure reads
// as a sentence with one value after it.
func TestValidationError_Error_MinimalShape(t *testing.T) {
	t.Parallel()
	ve := NewValidationError(envelope.KindFeature, "core/x:1.0.0", ValidationDetail{
		Err: ErrCyclicExtension(),
	})
	msg := ve.Error()
	if want := ErrCyclicExtension().Error() + " (Feature core/x:1.0.0)"; msg != want {
		t.Errorf("minimal shape = %q, want %q", msg, want)
	}
	// No separator is left dangling for a value the failure did not supply.
	for _, absent := range []string{", ", "at ", "rule ", "got ", ". "} {
		if strings.Contains(msg, absent) {
			t.Errorf("minimal shape should not include %q; got: %s", absent, msg)
		}
	}
}

// TestNewValidationError_RefusesADetailWithNoCondition pins the refusal that
// makes "every rendered failure opens with its condition's sentence" a property
// of the type rather than a convention its call sites keep. A failure with no
// registered sentence to open with is refused where the detail was supplied,
// rather than at the surface that later tries to render it.
//
// Both refused shapes are covered, because they fail for one reason and would
// be easy to guard against separately: an absent condition, and a plain error
// that names no registered condition and so carries no sentence either.
func TestNewValidationError_RefusesADetailWithNoCondition(t *testing.T) {
	t.Parallel()
	for label, detail := range map[string]ValidationDetail{
		"no condition at all":   {FieldPath: ".values.port"},
		"an unregistered error": {FieldPath: ".values.port", Err: stderrors.New("a plain error")},
		"a wrapped plain error": {FieldPath: ".values.port", Err: fmt.Errorf("wrapping: %w", stderrors.New("a plain error"))},
	} {
		t.Run(label, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("NewValidationError accepted %s; want a refusal", label)
				}
				msg, ok := r.(string)
				if !ok || !strings.Contains(msg, "carries no condition") {
					t.Errorf("the refusal should say the failure carries no condition; got %v", r)
				}
			}()
			NewValidationError(envelope.KindComponent, "core/api:1.0.0", detail)
		})
	}

	// The positive half, so the refusal above cannot be passing because the
	// constructor refuses everything: an identity wrapped on its way in still
	// carries its sentence, and the message still opens with it.
	ve := NewValidationError(envelope.KindComponent, "core/api:1.0.0", ValidationDetail{
		FieldPath: ".values.port",
		Err:       fmt.Errorf("resolving the chain: %w", ErrCyclicExtension()),
	})
	if sentence := ErrCyclicExtension().Error(); !strings.HasPrefix(ve.Error(), sentence+" (") {
		t.Errorf("a wrapped identity should still open the message with %q; got %s", sentence, ve.Error())
	}
}
