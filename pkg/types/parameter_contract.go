// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// ParameterContract carries the typed-contract shape for one Aggregate
// parameter declaration. Parameter-contract validation (pkg/resolver) verifies
// Release-supplied parameter values against this shape before parameter
// interpolation.
//
// Aggregate.Spec.Parameters carries raw decoded entries in an
// OrderedMap[any] — each entry must be a YAML map authoring the
// compact-form typed-contract vocabulary. The validator reshapes each
// entry into a ParameterContract via DecodeParameterContract; bare-scalar
// entries are rejected with a directed error.
type ParameterContract struct {
	Type            string          `yaml:"type,omitempty"`
	Description     string          `yaml:"description,omitempty"`
	Default         any             `yaml:"default,omitempty"`
	Enum            []any           `yaml:"enum,omitempty"`
	Min             any             `yaml:"min,omitempty"`
	Max             any             `yaml:"max,omitempty"`
	MinLength       *int            `yaml:"minLength,omitempty"`
	MaxLength       *int            `yaml:"maxLength,omitempty"`
	Pattern         string          `yaml:"pattern,omitempty"`
	Format          string          `yaml:"format,omitempty"`
	Validate        string          `yaml:"validate,omitempty"`
	ValidateMessage string          `yaml:"validateMessage,omitempty"`
	Required        bool            `yaml:"required,omitempty"`
	Items           any             `yaml:"items,omitempty"`
	ValuesType      any             `yaml:"valuesType,omitempty"`
	Properties      OrderedMap[any] `yaml:"properties,omitempty"`
}

// DecodeParameterContract reshapes a raw decoded Aggregate parameter entry
// (the per-key value extracted from Aggregate.Spec.Parameters) into a
// ParameterContract. The entry must be a typed-contract map matching the
// compact-form vocabulary; bare-scalar entries are rejected with a
// directed error pointing the author at the typed-contract syntax.
//
// Returns an error when the entry is a scalar (rejected — author must
// use typed-contract form) or when the entry is a map that fails
// typed-contract decode.
func DecodeParameterContract(entry any) (ParameterContract, error) {
	if m, ok := isParameterMap(entry); ok {
		return decodeTypedContract(m)
	}
	return ParameterContract{}, fmt.Errorf(
		"Aggregate parameter declarations require the typed-contract form. " +
			"Use 'key: { type: <type>, default: <value> }' instead of the bare-scalar shorthand.")
}

// isParameterMap classifies a raw OrderedMap[any] entry as a typed-form
// map vs a scalar. Returns the map representation for typed-form entries
// (a map[string]any or an *OrderedMap[any]).
func isParameterMap(entry any) (any, bool) {
	switch v := entry.(type) {
	case map[string]any:
		return v, true
	case *OrderedMap[any]:
		return v, true
	case OrderedMap[any]:
		return v, true
	}
	return nil, false
}

func decodeTypedContract(m any) (ParameterContract, error) {
	data, err := yaml.Marshal(m)
	if err != nil {
		return ParameterContract{}, fmt.Errorf("marshal typed-contract intermediate: %w", err)
	}
	var pc ParameterContract
	if err := yaml.Unmarshal(data, &pc); err != nil {
		return ParameterContract{}, fmt.Errorf("decode typed-contract: %w", err)
	}
	return pc, nil
}
