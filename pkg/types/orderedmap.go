// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"iter"

	"gopkg.in/yaml.v3"
)

// OrderedMap is a generic string-keyed map that preserves author insertion
// order across YAML unmarshal/marshal round-trips. Iteration via Keys() or
// All() yields keys in the order they were inserted (or, for unmarshalled
// maps, the order they appeared in the source YAML).
//
// Author-order iteration is load-bearing for the compiler's deterministic
// output invariant: map[string]X iteration is hash-randomized, which breaks
// byte-identical rebuilds when an iterated field contributes to rendered
// output.
//
// OrderedMap is safe to use as a zero value. Set / Delete lazily initialize
// the backing map. The type is not safe for concurrent mutation; callers
// that share an OrderedMap across goroutines must serialize writes externally.
type OrderedMap[V any] struct {
	keys   []string
	values map[string]V
}

// NewOrderedMap returns an empty, initialized OrderedMap suitable for
// immediate Set calls. Prefer this over `OrderedMap[V]{}` literals when the
// map will receive writes — the zero value's Set works but uses a lazy
// allocation path that's fine for one-shot callers but adds one conditional
// per Set call.
func NewOrderedMap[V any]() *OrderedMap[V] {
	return &OrderedMap[V]{
		values: make(map[string]V),
	}
}

// Len returns the number of entries.
func (m *OrderedMap[V]) Len() int {
	if m == nil {
		return 0
	}
	return len(m.keys)
}

// IsZero reports whether the map is empty. Used by yaml.v3's omitempty (via
// the yaml.IsZeroer interface) and by encoding/json's omitzero struct tag
// (Go 1.24+) so that empty OrderedMap fields on struct types with the
// appropriate tag are omitted from marshaled output rather than emitting as
// `{}`.
func (m OrderedMap[V]) IsZero() bool {
	return len(m.keys) == 0
}

// Get returns the value stored at k and whether k is present.
func (m *OrderedMap[V]) Get(k string) (V, bool) {
	var zero V
	if m == nil || m.values == nil {
		return zero, false
	}
	v, ok := m.values[k]
	return v, ok
}

// Set inserts or updates the value at k. A new key is appended to the
// iteration order; an existing key keeps its position and has its value
// replaced.
func (m *OrderedMap[V]) Set(k string, v V) {
	if m.values == nil {
		m.values = make(map[string]V)
	}
	if _, exists := m.values[k]; !exists {
		m.keys = append(m.keys, k)
	}
	m.values[k] = v
}

// Delete removes k from the map and from the iteration order.
// Returns true if k was present.
func (m *OrderedMap[V]) Delete(k string) bool {
	if m == nil || m.values == nil {
		return false
	}
	if _, exists := m.values[k]; !exists {
		return false
	}
	delete(m.values, k)
	for i, kk := range m.keys {
		if kk == k {
			m.keys = append(m.keys[:i], m.keys[i+1:]...)
			break
		}
	}
	return true
}

// Keys returns a fresh slice of keys in author order. Mutating the returned
// slice does not affect the OrderedMap; callers may sort, filter, or modify
// freely.
func (m *OrderedMap[V]) Keys() []string {
	if m == nil {
		return []string{}
	}
	out := make([]string, len(m.keys))
	copy(out, m.keys)
	return out
}

// All returns a range-over-func iterator yielding entries in author order.
// Idiomatic usage:
//
//	for k, v := range m.All() {
//	    // ...
//	}
func (m *OrderedMap[V]) All() iter.Seq2[string, V] {
	return func(yield func(string, V) bool) {
		if m == nil {
			return
		}
		for _, k := range m.keys {
			if !yield(k, m.values[k]) {
				return
			}
		}
	}
}

// UnmarshalYAML decodes a YAML mapping node into the OrderedMap, preserving
// the order of keys as they appear in the source. Returns an error if the
// same key appears more than once.
func (m *OrderedMap[V]) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("OrderedMap: expected mapping node, got kind %d at line %d", node.Kind, node.Line)
	}
	if m.values == nil {
		m.values = make(map[string]V, len(node.Content)/2)
	}
	m.keys = m.keys[:0]

	for i := 0; i < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valueNode := node.Content[i+1]

		var k string
		if err := keyNode.Decode(&k); err != nil {
			return fmt.Errorf("OrderedMap: decoding key at line %d: %w", keyNode.Line, err)
		}

		if _, exists := m.values[k]; exists {
			return fmt.Errorf("OrderedMap: duplicate key %q at line %d", k, keyNode.Line)
		}

		var v V
		if err := valueNode.Decode(&v); err != nil {
			return fmt.Errorf("OrderedMap: decoding value for key %q at line %d: %w", k, valueNode.Line, err)
		}

		m.keys = append(m.keys, k)
		m.values[k] = v
	}
	return nil
}

// MarshalYAML emits a mapping node with keys in author order.
//
// Value receiver (not pointer) so dispatch works when OrderedMap is embedded
// as a struct field or as the value type of an enclosing map — yaml.v3's
// reflection sees the value-receiver method in the type's method set
// regardless of addressability.
func (m OrderedMap[V]) MarshalYAML() (any, error) {
	out := &yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
	}
	if len(m.keys) == 0 {
		return out, nil
	}
	out.Content = make([]*yaml.Node, 0, 2*len(m.keys))
	for _, k := range m.keys {
		keyNode := &yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!str",
			Value: k,
		}
		valueNode := &yaml.Node{}
		if err := valueNode.Encode(m.values[k]); err != nil {
			return nil, fmt.Errorf("OrderedMap: encoding value for key %q: %w", k, err)
		}
		out.Content = append(out.Content, keyNode, valueNode)
	}
	return out, nil
}

// MarshalJSON emits a JSON object with keys in author order. Value receiver
// matches MarshalYAML's dispatch rationale. The encoding/json package does
// not expose a way to influence its default map-key ordering, so we build
// the JSON object manually and delegate to json.Marshal for each value.
func (m OrderedMap[V]) MarshalJSON() ([]byte, error) {
	if len(m.keys) == 0 {
		return []byte("{}"), nil
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range m.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyBytes, err := json.Marshal(k)
		if err != nil {
			return nil, fmt.Errorf("OrderedMap: encoding JSON key %q: %w", k, err)
		}
		buf.Write(keyBytes)
		buf.WriteByte(':')
		valueBytes, err := json.Marshal(m.values[k])
		if err != nil {
			return nil, fmt.Errorf("OrderedMap: encoding JSON value for key %q: %w", k, err)
		}
		buf.Write(valueBytes)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// UnmarshalJSON decodes a JSON object preserving key order. Uses a streaming
// json.Decoder with Token() to observe keys in source order — encoding/json's
// default json.Unmarshal into a map collapses order, so we parse the object
// manually one key-value pair at a time.
//
// Rejects duplicate keys with the duplicate key name in the error.
func (m *OrderedMap[V]) UnmarshalJSON(data []byte) error {
	if m.values == nil {
		m.values = make(map[string]V)
	}
	m.keys = m.keys[:0]

	// Handle the literal null — JSON null decodes into a nil-equivalent state,
	// i.e. an empty OrderedMap, without error.
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("OrderedMap: reading JSON opening token: %w", err)
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return fmt.Errorf("OrderedMap: expected JSON object, got token %v", tok)
	}

	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("OrderedMap: reading JSON key: %w", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("OrderedMap: expected string key, got token %v", keyTok)
		}

		if _, exists := m.values[key]; exists {
			return fmt.Errorf("OrderedMap: duplicate key %q", key)
		}

		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return fmt.Errorf("OrderedMap: reading JSON value for key %q: %w", key, err)
		}

		var v V
		if err := json.Unmarshal(raw, &v); err != nil {
			return fmt.Errorf("OrderedMap: decoding JSON value for key %q: %w", key, err)
		}

		m.keys = append(m.keys, key)
		m.values[key] = v
	}

	// Consume the closing '}' token.
	if _, err := dec.Token(); err != nil && err != io.EOF {
		return fmt.Errorf("OrderedMap: reading JSON closing token: %w", err)
	}

	return nil
}
