// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// A key of a compose service, or of a block written directly under one, written
// as null and carried as written.
//
// A fragment — a compose source with the node's values laid over it, or an
// artifacts: body — may write a key as null, and the Compose Specification gives
// that null its own reading, which is not the reading of a key left out. The
// typed decode cannot tell the two apart by itself: a field tagged omitempty
// decodes a null to its zero value and the encode then leaves the key out. So
// the decode of Service, and of the healthcheck and deploy blocks written under
// a service, records each modelled key written as null, and its encode writes
// `key: null` back, at the key's place among the keys the value writes, for each
// such key the value still has no value of its own for. A value set after the
// decode — the network the merge attaches to a service, say — is a value of its
// own, and is emitted as that value. A label written as null needs no record:
// the labels mapping holds it as an entry with no value.
//
// The record is read from the mapping the decoder reads, merge keys included,
// with the precedence the decoder gives them, so a key is recorded as null
// exactly when the decode gave its field nothing. An extension member written as
// null needs no record: its value is carried as the node written.
//
// This file carries no build tag: it compiles for the host and for the wasip1
// guest alike, so it imports no host-only capability.

package composecore

import (
	"reflect"

	"go.yaml.in/yaml/v3"
)

// healthcheckFields and deployFields are Healthcheck and Deploy without their
// methods, for the reason serviceFields gives.
type (
	healthcheckFields Healthcheck
	deployFields      Deploy
)

var (
	healthcheckFieldsType = reflect.TypeOf(healthcheckFields{})
	deployFieldsType      = reflect.TypeOf(deployFields{})
)

// walkFragmentKeys walks a healthcheck block against the keys Healthcheck
// models, which are the keys its decode carries.
func (Healthcheck) walkFragmentKeys(w *fragmentKeyWalk, node *yaml.Node, path string) {
	w.walk(node, healthcheckFieldsType, path)
}

// walkFragmentKeys walks a deploy block against the keys Deploy models, which
// are the keys its decode carries.
func (Deploy) walkFragmentKeys(w *fragmentKeyWalk, node *yaml.Node, path string) {
	w.walk(node, deployFieldsType, path)
}

// UnmarshalYAML decodes every modelled key and records the keys written as null.
func (h *Healthcheck) UnmarshalYAML(node *yaml.Node) error {
	var fields healthcheckFields
	nulls, err := decodeRecordingNulls(node, &fields)
	if err != nil {
		return err
	}
	fields.writtenNull = nulls
	*h = Healthcheck(fields)
	return nil
}

// MarshalYAML writes every modelled key, and each key written as null that still
// has no value of its own as `key: null` at its place.
//
// Value receiver so a Healthcheck encodes the same way whether it is reached by
// value or through a pointer.
func (h Healthcheck) MarshalYAML() (any, error) {
	return encodeRestoringNulls(healthcheckFields(h), h.writtenNull)
}

// UnmarshalYAML decodes every modelled key and records the keys written as null.
func (d *Deploy) UnmarshalYAML(node *yaml.Node) error {
	var fields deployFields
	nulls, err := decodeRecordingNulls(node, &fields)
	if err != nil {
		return err
	}
	fields.writtenNull = nulls
	*d = Deploy(fields)
	return nil
}

// MarshalYAML writes every modelled key, and each key written as null that still
// has no value of its own as `key: null` at its place.
//
// Value receiver, as Healthcheck's.
func (d Deploy) MarshalYAML() (any, error) {
	return encodeRestoringNulls(deployFields(d), d.writtenNull)
}

// decodeRecordingNulls decodes node into fields — a pointer to a struct without
// methods, so the decode reads its yaml tags — and returns the keys that struct
// models whose value node writes as null.
func decodeRecordingNulls(node *yaml.Node, fields any) ([]string, error) {
	if err := node.Decode(fields); err != nil {
		return nil, err
	}
	return writtenNullKeys(node, reflect.TypeOf(fields).Elem()), nil
}

// encodeRestoringNulls encodes fields — a struct without methods — and writes
// `key: null` for each key in nulls the encoded mapping does not carry (see
// restoreWrittenNulls). With no key recorded it returns fields itself, so a value
// written with no null encodes exactly as the struct does.
func encodeRestoringNulls(fields any, nulls []string) (any, error) {
	if len(nulls) == 0 {
		return fields, nil
	}
	var node yaml.Node
	if err := node.Encode(fields); err != nil {
		return nil, err
	}
	restoreWrittenNulls(&node, nulls, reflect.TypeOf(fields))
	return &node, nil
}

// writtenNullKeys returns the keys of mapping that t models and whose value is
// written as null, in the order the keys first appear.
func writtenNullKeys(mapping *yaml.Node, t reflect.Type) []string {
	fields, _, _ := fragmentFields(t)
	var out []string
	for _, kv := range effectiveFragmentPairs(mapping) {
		if _, modelled := fields[kv.key]; !modelled {
			continue
		}
		if isWrittenNull(kv.value) {
			out = append(out, kv.key)
		}
	}
	return out
}

// isWrittenNull reports whether a value is written as null: `null`, `~`, or no
// value at all, reached directly or through an alias.
func isWrittenNull(n *yaml.Node) bool {
	n = resolveFragmentAlias(n)
	return n != nil && n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null"
}

// restoreWrittenNulls adds `key: null` to node — the mapping t encodes to — for
// each key in nulls that node does not already carry, at the key's place in t's
// field order. The keys node carries keep their order and their values: the
// encoder writes them in field order, so the two orders are walked together.
func restoreWrittenNulls(node *yaml.Node, nulls []string, t reflect.Type) {
	if node.Kind != yaml.MappingNode || len(nulls) == 0 {
		return
	}
	isNull := make(map[string]bool, len(nulls))
	for _, k := range nulls {
		isNull[k] = true
	}
	fields, _, _ := fragmentFieldList(t)
	content := make([]*yaml.Node, 0, len(node.Content)+2*len(nulls))
	next := 0
	for _, f := range fields {
		if next+1 < len(node.Content) && node.Content[next].Value == f.key {
			content = append(content, node.Content[next], node.Content[next+1])
			next += 2
			continue
		}
		if isNull[f.key] {
			content = append(content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: f.key},
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"},
			)
		}
	}
	// Anything the encoder wrote that the field walk did not reach is kept, after
	// the rest, rather than dropped.
	content = append(content, node.Content[next:]...)
	node.Content = content
	// The encoder writes a mapping holding no key in flow style, `{}`; one holding
	// only restored keys is written in block style like every other mapping.
	node.Style &^= yaml.FlowStyle
}
