// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"bytes"
	"encoding/json"
	"fmt"

	"go.yaml.in/yaml/v3"
)

// UsesSocketValue is the typed value of one entry in a Component, Feature,
// or Secret instance's `uses:` block. The schema admits two authoring
// forms; exactly one of the two fields is populated after YAML or JSON
// unmarshal of a non-null value:
//
//   - Compact form — string array. Each entry is a target instance name;
//     the emitted edge's Alias equals Target. Authored as
//     `socket: ["target-1", "target-2"]`.
//
//   - Verbose form — alias-keyed map. Each entry produces an edge whose
//     Alias is the map key and whose Target is the entry's `target`
//     field (downstream callers default Target to the alias when absent).
//     Authored as `socket: { "alias": { "target": "name", "inject": {...} } }`.
//
// MarshalYAML and MarshalJSON emit the form whose corresponding field is
// populated; both empty marshals as the YAML/JSON null value.
type UsesSocketValue struct {
	// Compact carries the string-array authoring form. Populated when the
	// source YAML or JSON for the socket is a sequence; empty otherwise.
	Compact []string

	// Verbose carries the alias-keyed authoring form. Populated when the
	// source YAML or JSON for the socket is a mapping; empty otherwise.
	Verbose OrderedMap[VerboseUsesEntry]
}

// VerboseUsesEntry is one entry of a verbose-form `uses:` socket. An
// alias maps to an entry object — the map form `{ "target": "name",
// "inject": {...} }`. Target may be omitted in the source; downstream
// callers default Target to the alias key when the field is empty after
// unmarshal. A bare scalar value for an alias (`"alias": "target-name"`)
// is rejected at unmarshal, naming the alias and the required form.
type VerboseUsesEntry struct {
	// Target is the destination instance name. Empty after unmarshal of
	// a map-form entry that omits the `target:` field; downstream callers
	// default Target to the alias key in that case.
	Target string `yaml:"target,omitempty" json:"target,omitempty"`

	// Inject carries optional inject directives the entry binds to the
	// resulting edge. Nil when the source omits the `inject:` field.
	Inject *InjectBlock `yaml:"inject,omitempty" json:"inject,omitempty"`

	// Values carries per-connection developer config for this edge,
	// validated against the valueSchema the CONSUMING component — the one
	// whose uses block carries this entry — declares on the socket, by the
	// conformance gate at resolution, and projected into the consuming node's
	// render context at .uses.<alias>.values (the uses map is alias-keyed).
	// Empty when omitted: this is the authored bag verbatim, and the
	// socket's declared defaults are merged in when the graph is built.
	//
	// It has no Infra counterpart by construction: per-connection infra is
	// the operator's surface and is authored on a Release edge entry, which
	// carries its own Infra field. The developer declares the connection and
	// its values; the deployment sizes it.
	Values OrderedMap[any] `yaml:"values,omitempty" json:"values,omitempty,omitzero"`
}

// UnmarshalYAML on UsesSocketValue dispatches on the YAML node kind.
// SequenceNode populates Compact; MappingNode populates Verbose. A null
// or empty scalar produces a zero-value receiver. Other node kinds
// return an error naming the offending kind and line.
func (v *UsesSocketValue) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.SequenceNode:
		var compact []string
		if err := node.Decode(&compact); err != nil {
			return fmt.Errorf("UsesSocketValue: decoding compact form at line %d: %w", node.Line, err)
		}
		v.Compact = compact
		return nil
	case yaml.MappingNode:
		var verbose OrderedMap[VerboseUsesEntry]
		if err := node.Decode(&verbose); err != nil {
			return fmt.Errorf("UsesSocketValue: decoding verbose form at line %d: %w", node.Line, err)
		}
		v.Verbose = verbose
		return nil
	case yaml.ScalarNode:
		// null / empty scalar produces a zero-value UsesSocketValue
		var s string
		if err := node.Decode(&s); err == nil && s == "" {
			return nil
		}
		return fmt.Errorf("UsesSocketValue: expected sequence or mapping at line %d, got scalar %q", node.Line, node.Value)
	default:
		return fmt.Errorf("UsesSocketValue: expected sequence or mapping at line %d, got kind %d", node.Line, node.Kind)
	}
}

// UnmarshalJSON on UsesSocketValue dispatches on the leading
// non-whitespace byte. `[` populates Compact; `{` populates Verbose;
// `null` produces a zero-value receiver.
func (v *UsesSocketValue) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimLeft(data, " \t\n\r")
	if len(trimmed) == 0 {
		return fmt.Errorf("UsesSocketValue: empty JSON value")
	}
	switch trimmed[0] {
	case 'n':
		if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			return nil
		}
		return fmt.Errorf("UsesSocketValue: expected array, object, or null, got %q", string(trimmed))
	case '[':
		var compact []string
		if err := json.Unmarshal(data, &compact); err != nil {
			return fmt.Errorf("UsesSocketValue: decoding compact form: %w", err)
		}
		v.Compact = compact
		return nil
	case '{':
		var verbose OrderedMap[VerboseUsesEntry]
		if err := json.Unmarshal(data, &verbose); err != nil {
			return fmt.Errorf("UsesSocketValue: decoding verbose form: %w", err)
		}
		v.Verbose = verbose
		return nil
	default:
		preview := trimmed
		if len(preview) > 16 {
			preview = preview[:16]
		}
		return fmt.Errorf("UsesSocketValue: expected array, object, or null, got %q", string(preview))
	}
}

// MarshalYAML emits the form whose field is populated. The empty case
// emits null. Compact takes precedence if both fields are populated — a
// state the unmarshal hooks never produce; ad-hoc construction with both
// fields populated round-trips as compact-only.
func (v UsesSocketValue) MarshalYAML() (any, error) {
	if len(v.Compact) > 0 {
		return v.Compact, nil
	}
	if v.Verbose.Len() > 0 {
		return v.Verbose, nil
	}
	return nil, nil
}

// MarshalJSON emits the form whose field is populated. The empty case
// emits null. Compact takes precedence if both fields are populated — a
// state the unmarshal hooks never produce; ad-hoc construction with both
// fields populated round-trips as compact-only.
func (v UsesSocketValue) MarshalJSON() ([]byte, error) {
	if len(v.Compact) > 0 {
		return json.Marshal(v.Compact)
	}
	if v.Verbose.Len() > 0 {
		return json.Marshal(v.Verbose)
	}
	return []byte("null"), nil
}

// IsZero reports whether the value is empty. Used by yaml.v3's omitempty
// (via yaml.IsZeroer) and by encoding/json's omitzero struct tag (Go 1.24+).
func (v UsesSocketValue) IsZero() bool {
	return len(v.Compact) == 0 && v.Verbose.Len() == 0
}

// UnmarshalYAML on VerboseUsesEntry dispatches on the YAML node kind.
// MappingNode decodes the full {target, inject, values} struct. A
// ScalarNode (a bare-string alias value) is rejected, naming the alias
// and the required entry-object form.
func (e *VerboseUsesEntry) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		return fmt.Errorf("VerboseUsesEntry: bare-string target at line %d; an alias maps to an entry object — use {target: %q}", node.Line, node.Value)
	case yaml.MappingNode:
		// Use a separate alias type to avoid recursion into our own UnmarshalYAML.
		type plain struct {
			Target string          `yaml:"target,omitempty"`
			Inject *InjectBlock    `yaml:"inject,omitempty"`
			Values OrderedMap[any] `yaml:"values,omitempty"`
		}
		var p plain
		if err := node.Decode(&p); err != nil {
			return fmt.Errorf("VerboseUsesEntry: decoding map form at line %d: %w", node.Line, err)
		}
		e.Target = p.Target
		e.Inject = p.Inject
		e.Values = p.Values
		return nil
	default:
		return fmt.Errorf("VerboseUsesEntry: expected scalar or mapping at line %d, got kind %d", node.Line, node.Kind)
	}
}

// UnmarshalJSON on VerboseUsesEntry dispatches on the leading
// non-whitespace byte. `{` decodes the full {target, inject, values}
// struct; a `"` string scalar (a bare-string alias value) is
// rejected, naming the required entry-object form.
func (e *VerboseUsesEntry) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimLeft(data, " \t\n\r")
	if len(trimmed) == 0 {
		return fmt.Errorf("VerboseUsesEntry: empty JSON value")
	}
	switch trimmed[0] {
	case '"':
		return fmt.Errorf("VerboseUsesEntry: bare-string target %s; an alias maps to an entry object — use {\"target\": ...}", string(trimmed))
	case '{':
		// Use a separate alias type to avoid recursion into our own UnmarshalJSON.
		type plain struct {
			Target string          `json:"target,omitempty"`
			Inject *InjectBlock    `json:"inject,omitempty"`
			Values OrderedMap[any] `json:"values,omitempty,omitzero"`
		}
		var p plain
		if err := json.Unmarshal(data, &p); err != nil {
			return fmt.Errorf("VerboseUsesEntry: decoding map form: %w", err)
		}
		e.Target = p.Target
		e.Inject = p.Inject
		e.Values = p.Values
		return nil
	default:
		preview := trimmed
		if len(preview) > 16 {
			preview = preview[:16]
		}
		return fmt.Errorf("VerboseUsesEntry: expected string or object, got %q", string(preview))
	}
}

// MarshalYAML emits the entry's mapping form `{ target?, inject?,
// values? }`.
func (e VerboseUsesEntry) MarshalYAML() (any, error) {
	type plain struct {
		Target string          `yaml:"target,omitempty"`
		Inject *InjectBlock    `yaml:"inject,omitempty"`
		Values OrderedMap[any] `yaml:"values,omitempty"`
	}
	return plain{Target: e.Target, Inject: e.Inject, Values: e.Values}, nil
}

// MarshalJSON emits the entry's mapping form `{ target?, inject?,
// values? }`.
func (e VerboseUsesEntry) MarshalJSON() ([]byte, error) {
	type plain struct {
		Target string          `json:"target,omitempty"`
		Inject *InjectBlock    `json:"inject,omitempty"`
		Values OrderedMap[any] `json:"values,omitempty,omitzero"`
	}
	return json.Marshal(plain{Target: e.Target, Inject: e.Inject, Values: e.Values})
}

// IsZero reports whether the entry is empty.
func (e VerboseUsesEntry) IsZero() bool {
	return e.Target == "" && e.Inject == nil && e.Values.Len() == 0
}
