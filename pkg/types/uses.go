// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types

import (
	"bytes"
	"encoding/json"
	"fmt"

	"go.yaml.in/yaml/v3"
)

// UsesSocketValue is the typed value of one entry in a component instance's own
// `uses:` block — the connections a component draws. The schema admits two
// authoring forms; exactly one of the two fields is populated after YAML or JSON
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
//
// The connections an attached Feature draws decode into [FeatureUsesSocketValue]
// instead, because a connection a Feature draws carries no attachments of its
// own and its type has no field to hold one.
type UsesSocketValue struct {
	// Compact carries the string-array authoring form. Populated when the
	// source YAML or JSON for the socket is a sequence; empty otherwise.
	Compact []string

	// Verbose carries the alias-keyed authoring form. Populated when the
	// source YAML or JSON for the socket is a mapping; empty otherwise.
	Verbose OrderedMap[VerboseUsesEntry]
}

// VerboseUsesEntry is one entry of a verbose-form `uses:` socket on a
// component's own block. An alias maps to an entry object — the map form
// `{ "target": "name", "inject": {...} }`. Target may be omitted in the source;
// downstream callers default Target to the alias key when the field is empty
// after unmarshal. A bare scalar value for an alias (`"alias": "target-name"`)
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
	// its values; the Release sizes it.
	Values OrderedMap[any] `yaml:"values,omitempty" json:"values,omitempty,omitzero"`

	// Features carries the attachments this connection holds, keyed by the
	// slot each fills, in author order. A name has to be a slot the socket
	// declares on the consuming Component; the build refuses any other name
	// when it loads. Each attachment becomes an attachment of the consuming
	// component named by [JoinConnectionAttachmentName], carrying the
	// connection it came from as data. Empty when the entry declares none.
	Features OrderedMap[ConnectionAttachment] `yaml:"features,omitempty" json:"features,omitempty,omitzero"`
}

// ConnectionAttachment is one attachment a connection on a component's own
// `uses:` block carries: the Feature filling a slot the connection's socket
// declares, with the developer's configuration for it.
//
// It is narrower than [FeatureInstance] on purpose. An attachment on a
// connection does not nest, so it has no `uses` block, and it declares no
// `inject` block of its own; the Aggregate schema refuses both keys here.
type ConnectionAttachment struct {
	Type        string          `json:"type" yaml:"type"`
	Description string          `json:"description,omitempty" yaml:"description,omitempty"`
	Values      OrderedMap[any] `json:"values,omitempty,omitzero" yaml:"values,omitempty"`
}

// AttachmentConnection names the connection an attachment of a component was
// written on: the socket and the alias the connection sits under, and the slot
// name the attachment was authored under there.
//
// It is carried as data beside an attachment lowered from a connection, so every
// stage that needs the connection reads it from here. The attachment's own name
// is composed from the alias and the slot by [JoinConnectionAttachmentName], and
// nothing reads the connection back out of that name.
//
// The graph's wire form carries the socket and the alias, which are what join the
// attachment to its edge. Attachment stays off the wire on purpose: the slot an
// attachment fills is reported once, by [IRFeatureEntry.Binding], and a second
// field stating it would be a second answer to the same question that a reader
// could find disagreeing with the first. So a graph decoded from its wire form
// carries Socket and Alias here and an empty Attachment.
type AttachmentConnection struct {
	Socket     string `json:"socket"`
	Alias      string `json:"alias"`
	Attachment string `json:"-"`
}

// JoinConnectionAttachmentName composes the name an attachment a connection
// carries takes on the consuming component: the alias the connection is written
// under, a dot, and the slot the attachment fills — "gpt.credential".
//
// The dot is the separator the compiler already reserves for the names it
// composes, which no authored instance name, alias or slot name may contain, so
// a composed name can never equal one an author wrote on the component's own
// attachment block.
//
// It is the single spelling of that composition. [JoinQualifiedID] applies the
// same formula to a different coordinate — a component's place in the flattened
// graph — and is deliberately kept separate: the two would have to change
// independently if either coordinate's separator ever did.
func JoinConnectionAttachmentName(alias, attachment string) string {
	return alias + "." + attachment
}

// FeatureUsesSocketValue is the typed value of one entry in an attached
// Feature's `uses:` block — the connections a Feature draws — on an Aggregate
// attachment and on a Release attachment alike. It admits the same two
// authoring forms as [UsesSocketValue], under the same decoding rules.
//
// It is a type of its own because the entry shape differs: a connection a
// Feature draws carries no attachments, so [FeatureUsesEntry] has no field to
// hold one and an attachment written there cannot be decoded into anything.
type FeatureUsesSocketValue struct {
	// Compact carries the string-array authoring form.
	Compact []string

	// Verbose carries the alias-keyed authoring form.
	Verbose OrderedMap[FeatureUsesEntry]
}

// FeatureUsesEntry is one entry of a verbose-form `uses:` socket on an
// attached Feature: the target, the inject directives the entry binds to the
// resulting edge, and the developer's per-connection values. It decodes under
// the same rules as [VerboseUsesEntry] and carries no attachments.
type FeatureUsesEntry struct {
	Target string          `yaml:"target,omitempty" json:"target,omitempty"`
	Inject *InjectBlock    `yaml:"inject,omitempty" json:"inject,omitempty"`
	Values OrderedMap[any] `yaml:"values,omitempty" json:"values,omitempty,omitzero"`
}

// FeatureUsesConnections projects a Feature's `uses:` block onto the shape a
// component's own block takes, so a consumer that reads every surface drawing a
// connection — normalisation into edges, the socket-name rule, the inject
// validation — reads both through one implementation rather than a copy per
// shape.
//
// The projection carries every field a Feature's connection has and leaves the
// one it lacks empty, so it cannot invent an attachment. Order is preserved at
// both levels. An empty block projects to an empty one.
func FeatureUsesConnections(uses OrderedMap[FeatureUsesSocketValue]) OrderedMap[UsesSocketValue] {
	out := OrderedMap[UsesSocketValue]{}
	for socket, sv := range uses.All() {
		out.Set(socket, sv.Connections())
	}
	return out
}

// Connections projects one socket of a Feature's `uses:` block onto the shape
// a component's own socket takes. See [FeatureUsesConnections].
func (v FeatureUsesSocketValue) Connections() UsesSocketValue {
	out := UsesSocketValue{Compact: v.Compact}
	for alias, e := range v.Verbose.All() {
		out.Verbose.Set(alias, VerboseUsesEntry{Target: e.Target, Inject: e.Inject, Values: e.Values})
	}
	return out
}

// decodeSocketYAML dispatches one socket's YAML value on its node kind, for
// both socket-value types: a sequence is the compact form, a mapping the
// verbose form, and a null or empty scalar a zero value. typeName prefixes the
// error so a failure names the type that refused it.
func decodeSocketYAML[E any](typeName string, node *yaml.Node) ([]string, OrderedMap[E], error) {
	switch node.Kind {
	case yaml.SequenceNode:
		var compact []string
		if err := node.Decode(&compact); err != nil {
			return nil, OrderedMap[E]{}, fmt.Errorf("%s: decoding compact form at line %d: %w", typeName, node.Line, err)
		}
		return compact, OrderedMap[E]{}, nil
	case yaml.MappingNode:
		var verbose OrderedMap[E]
		if err := node.Decode(&verbose); err != nil {
			return nil, OrderedMap[E]{}, fmt.Errorf("%s: decoding verbose form at line %d: %w", typeName, node.Line, err)
		}
		return nil, verbose, nil
	case yaml.ScalarNode:
		// null / empty scalar produces a zero value
		var s string
		if err := node.Decode(&s); err == nil && s == "" {
			return nil, OrderedMap[E]{}, nil
		}
		return nil, OrderedMap[E]{}, fmt.Errorf("%s: expected sequence or mapping at line %d, got scalar %q", typeName, node.Line, node.Value)
	default:
		return nil, OrderedMap[E]{}, fmt.Errorf("%s: expected sequence or mapping at line %d, got kind %d", typeName, node.Line, node.Kind)
	}
}

// decodeSocketJSON dispatches one socket's JSON value on its leading
// non-whitespace byte, for both socket-value types: `[` is the compact form,
// `{` the verbose form, and `null` a zero value.
func decodeSocketJSON[E any](typeName string, data []byte) ([]string, OrderedMap[E], error) {
	trimmed := bytes.TrimLeft(data, " \t\n\r")
	if len(trimmed) == 0 {
		return nil, OrderedMap[E]{}, fmt.Errorf("%s: empty JSON value", typeName)
	}
	switch trimmed[0] {
	case 'n':
		if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			return nil, OrderedMap[E]{}, nil
		}
		return nil, OrderedMap[E]{}, fmt.Errorf("%s: expected array, object, or null, got %q", typeName, string(trimmed))
	case '[':
		var compact []string
		if err := json.Unmarshal(data, &compact); err != nil {
			return nil, OrderedMap[E]{}, fmt.Errorf("%s: decoding compact form: %w", typeName, err)
		}
		return compact, OrderedMap[E]{}, nil
	case '{':
		var verbose OrderedMap[E]
		if err := json.Unmarshal(data, &verbose); err != nil {
			return nil, OrderedMap[E]{}, fmt.Errorf("%s: decoding verbose form: %w", typeName, err)
		}
		return nil, verbose, nil
	default:
		preview := trimmed
		if len(preview) > 16 {
			preview = preview[:16]
		}
		return nil, OrderedMap[E]{}, fmt.Errorf("%s: expected array, object, or null, got %q", typeName, string(preview))
	}
}

// UnmarshalYAML on UsesSocketValue dispatches on the YAML node kind.
// SequenceNode populates Compact; MappingNode populates Verbose. A null
// or empty scalar produces a zero-value receiver. Other node kinds
// return an error naming the offending kind and line.
func (v *UsesSocketValue) UnmarshalYAML(node *yaml.Node) error {
	compact, verbose, err := decodeSocketYAML[VerboseUsesEntry]("UsesSocketValue", node)
	if err != nil {
		return err
	}
	v.Compact, v.Verbose = compact, verbose
	return nil
}

// UnmarshalJSON on UsesSocketValue dispatches on the leading
// non-whitespace byte. `[` populates Compact; `{` populates Verbose;
// `null` produces a zero-value receiver.
func (v *UsesSocketValue) UnmarshalJSON(data []byte) error {
	compact, verbose, err := decodeSocketJSON[VerboseUsesEntry]("UsesSocketValue", data)
	if err != nil {
		return err
	}
	v.Compact, v.Verbose = compact, verbose
	return nil
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

// UnmarshalYAML on FeatureUsesSocketValue dispatches exactly as
// [UsesSocketValue.UnmarshalYAML] does.
func (v *FeatureUsesSocketValue) UnmarshalYAML(node *yaml.Node) error {
	compact, verbose, err := decodeSocketYAML[FeatureUsesEntry]("FeatureUsesSocketValue", node)
	if err != nil {
		return err
	}
	v.Compact, v.Verbose = compact, verbose
	return nil
}

// UnmarshalJSON on FeatureUsesSocketValue dispatches exactly as
// [UsesSocketValue.UnmarshalJSON] does.
func (v *FeatureUsesSocketValue) UnmarshalJSON(data []byte) error {
	compact, verbose, err := decodeSocketJSON[FeatureUsesEntry]("FeatureUsesSocketValue", data)
	if err != nil {
		return err
	}
	v.Compact, v.Verbose = compact, verbose
	return nil
}

// MarshalYAML emits the form whose field is populated, as
// [UsesSocketValue.MarshalYAML] does.
func (v FeatureUsesSocketValue) MarshalYAML() (any, error) {
	if len(v.Compact) > 0 {
		return v.Compact, nil
	}
	if v.Verbose.Len() > 0 {
		return v.Verbose, nil
	}
	return nil, nil
}

// MarshalJSON emits the form whose field is populated, as
// [UsesSocketValue.MarshalJSON] does.
func (v FeatureUsesSocketValue) MarshalJSON() ([]byte, error) {
	if len(v.Compact) > 0 {
		return json.Marshal(v.Compact)
	}
	if v.Verbose.Len() > 0 {
		return json.Marshal(v.Verbose)
	}
	return []byte("null"), nil
}

// IsZero reports whether the value is empty.
func (v FeatureUsesSocketValue) IsZero() bool {
	return len(v.Compact) == 0 && v.Verbose.Len() == 0
}

// UnmarshalYAML on VerboseUsesEntry dispatches on the YAML node kind.
// MappingNode decodes the full {target, inject, values, features} struct. A
// ScalarNode (a bare-string alias value) is rejected, naming the alias
// and the required entry-object form.
func (e *VerboseUsesEntry) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		return fmt.Errorf("VerboseUsesEntry: bare-string target at line %d; an alias maps to an entry object — use {target: %q}", node.Line, node.Value)
	case yaml.MappingNode:
		// Use a separate alias type to avoid recursion into our own UnmarshalYAML.
		type plain struct {
			Target   string                           `yaml:"target,omitempty"`
			Inject   *InjectBlock                     `yaml:"inject,omitempty"`
			Values   OrderedMap[any]                  `yaml:"values,omitempty"`
			Features OrderedMap[ConnectionAttachment] `yaml:"features,omitempty"`
		}
		var p plain
		if err := node.Decode(&p); err != nil {
			return fmt.Errorf("VerboseUsesEntry: decoding map form at line %d: %w", node.Line, err)
		}
		e.Target = p.Target
		e.Inject = p.Inject
		e.Values = p.Values
		e.Features = p.Features
		return nil
	default:
		return fmt.Errorf("VerboseUsesEntry: expected scalar or mapping at line %d, got kind %d", node.Line, node.Kind)
	}
}

// UnmarshalJSON on VerboseUsesEntry dispatches on the leading
// non-whitespace byte. `{` decodes the full {target, inject, values,
// features} struct; a `"` string scalar (a bare-string alias value) is
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
			Target   string                           `json:"target,omitempty"`
			Inject   *InjectBlock                     `json:"inject,omitempty"`
			Values   OrderedMap[any]                  `json:"values,omitempty,omitzero"`
			Features OrderedMap[ConnectionAttachment] `json:"features,omitempty,omitzero"`
		}
		var p plain
		if err := json.Unmarshal(data, &p); err != nil {
			return fmt.Errorf("VerboseUsesEntry: decoding map form: %w", err)
		}
		e.Target = p.Target
		e.Inject = p.Inject
		e.Values = p.Values
		e.Features = p.Features
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
// values?, features? }`.
func (e VerboseUsesEntry) MarshalYAML() (any, error) {
	type plain struct {
		Target   string                           `yaml:"target,omitempty"`
		Inject   *InjectBlock                     `yaml:"inject,omitempty"`
		Values   OrderedMap[any]                  `yaml:"values,omitempty"`
		Features OrderedMap[ConnectionAttachment] `yaml:"features,omitempty"`
	}
	return plain{Target: e.Target, Inject: e.Inject, Values: e.Values, Features: e.Features}, nil
}

// MarshalJSON emits the entry's mapping form `{ target?, inject?,
// values?, features? }`.
func (e VerboseUsesEntry) MarshalJSON() ([]byte, error) {
	type plain struct {
		Target   string                           `json:"target,omitempty"`
		Inject   *InjectBlock                     `json:"inject,omitempty"`
		Values   OrderedMap[any]                  `json:"values,omitempty,omitzero"`
		Features OrderedMap[ConnectionAttachment] `json:"features,omitempty,omitzero"`
	}
	return json.Marshal(plain{Target: e.Target, Inject: e.Inject, Values: e.Values, Features: e.Features})
}

// IsZero reports whether the entry is empty.
func (e VerboseUsesEntry) IsZero() bool {
	return e.Target == "" && e.Inject == nil && e.Values.Len() == 0 && e.Features.Len() == 0
}

// UnmarshalYAML on FeatureUsesEntry decodes the {target, inject, values}
// struct from a mapping and refuses a bare-string alias value, exactly as
// [VerboseUsesEntry.UnmarshalYAML] does.
func (e *FeatureUsesEntry) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		return fmt.Errorf("FeatureUsesEntry: bare-string target at line %d; an alias maps to an entry object — use {target: %q}", node.Line, node.Value)
	case yaml.MappingNode:
		type plain FeatureUsesEntry
		var p plain
		if err := node.Decode(&p); err != nil {
			return fmt.Errorf("FeatureUsesEntry: decoding map form at line %d: %w", node.Line, err)
		}
		*e = FeatureUsesEntry(p)
		return nil
	default:
		return fmt.Errorf("FeatureUsesEntry: expected scalar or mapping at line %d, got kind %d", node.Line, node.Kind)
	}
}

// UnmarshalJSON on FeatureUsesEntry decodes the {target, inject, values}
// struct from an object and refuses a bare-string alias value, exactly as
// [VerboseUsesEntry.UnmarshalJSON] does.
func (e *FeatureUsesEntry) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimLeft(data, " \t\n\r")
	if len(trimmed) == 0 {
		return fmt.Errorf("FeatureUsesEntry: empty JSON value")
	}
	switch trimmed[0] {
	case '"':
		return fmt.Errorf("FeatureUsesEntry: bare-string target %s; an alias maps to an entry object — use {\"target\": ...}", string(trimmed))
	case '{':
		type plain FeatureUsesEntry
		var p plain
		if err := json.Unmarshal(data, &p); err != nil {
			return fmt.Errorf("FeatureUsesEntry: decoding map form: %w", err)
		}
		*e = FeatureUsesEntry(p)
		return nil
	default:
		preview := trimmed
		if len(preview) > 16 {
			preview = preview[:16]
		}
		return fmt.Errorf("FeatureUsesEntry: expected string or object, got %q", string(preview))
	}
}

// IsZero reports whether the entry is empty.
func (e FeatureUsesEntry) IsZero() bool {
	return e.Target == "" && e.Inject == nil && e.Values.Len() == 0
}
