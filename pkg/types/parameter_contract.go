// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// ParameterContract carries the typed-contract shape for one Aggregate
// parameter declaration. Step 1a (pkg/resolver) verifies Release-supplied
// parameter values against this shape before parameter interpolation.
//
// Aggregate.Spec.Parameters continues to carry raw decoded entries in an
// OrderedMap[any] — each entry is either a YAML scalar (simple shorthand;
// type inferred from the scalar kind, no constraints) or a YAML map
// (typed contract with the compact-form vocabulary subset). The validator
// reshapes each entry into a ParameterContract via DecodeParameterContract
// or SimpleParameterContract.
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

// SimpleParameterContract returns a synthetic contract for an Aggregate
// parameter entry authored as a bare scalar. Type is inferred from the
// scalar kind (string / integer / number / boolean); Default carries the
// scalar value; no constraints attach. A nil scalar produces an untyped
// contract (Type == ""); validation against an untyped contract accepts
// any Release value.
func SimpleParameterContract(scalar any) ParameterContract {
	pc := ParameterContract{Default: scalar}
	switch scalar.(type) {
	case string:
		pc.Type = "string"
	case bool:
		pc.Type = "boolean"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		pc.Type = "integer"
	case float32, float64:
		pc.Type = "number"
	}
	return pc
}

// DecodeParameterContract reshapes a raw decoded Aggregate parameter entry
// (the per-key value extracted from Aggregate.Spec.Parameters) into a
// ParameterContract. Scalar values produce a SimpleParameterContract; map
// values decode as typed-form contracts via yaml.Marshal + yaml.Unmarshal
// (the round-trip is correct because the upstream OrderedMap[any] preserves
// YAML node fidelity for typed-form sub-maps).
//
// Returns an error only when the entry is a map that fails typed-contract
// decode; non-map values dispatch to SimpleParameterContract unconditionally.
func DecodeParameterContract(entry any) (ParameterContract, error) {
	if m, ok := isParameterMap(entry); ok {
		return decodeTypedContract(m)
	}
	return SimpleParameterContract(entry), nil
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
