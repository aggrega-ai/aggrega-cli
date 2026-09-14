// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"reflect"
	"sync"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"go.yaml.in/yaml/v3"
)

// ErrOrderedMapDecodeDepthExceeded fires when nested object recursion in
// UnmarshalJSON or UnmarshalYAML exceeds orderedMapDecodeMaxDepth, guarding
// against pathological input that would otherwise blow the call stack.
// Wrapped via fmt.Errorf at the emission site with the offending depth.
func ErrOrderedMapDecodeDepthExceeded() error { return errOrderedMapDecodeDepthExceeded }

var errOrderedMapDecodeDepthExceeded = cerrors.Register("document-nesting-depth-exceeded", cerrors.Meta{
	ShortCode: "E0192",
	Message:   "OrderedMap: decode depth exceeded",
	RulePlace: cerrors.NotARule("A document's nested-object recursion exceeded the bound the decoder applies to keep " +
		"pathological input off the call stack. The bound belongs to the decoder and is applied while " +
		"the document is being read, so what it reports is a decode that was stopped."),
})

// orderedMapDecodeMaxDepth bounds nested-OrderedMap recursion in unmarshal
// paths. Catalog payloads observed in practice nest at most a handful of
// levels; the bound leaves substantial headroom while ruling out unbounded
// recursion on adversarial input.
const orderedMapDecodeMaxDepth = 100

// jsonUnmarshalerCache memoizes per-V whether V (or *V) satisfies
// json.Unmarshaler. The first call per V type pays a reflection cost;
// subsequent calls hit the sync.Map cache. sync.Map.LoadOrStore is the
// primitive — atomic and goroutine-safe per stdlib documentation, so
// concurrent first-call attempts converge to a single cache entry.
var jsonUnmarshalerCache sync.Map // map[reflect.Type]bool

// yamlUnmarshalerCache mirrors jsonUnmarshalerCache for yaml.Unmarshaler.
var yamlUnmarshalerCache sync.Map // map[reflect.Type]bool

// vIsInterfaceCache memoizes per-V whether V is an interface kind. Used
// by the decode helpers to dispatch between the recursive any
// path and the typed-V plain-decode path.
var vIsInterfaceCache sync.Map // map[reflect.Type]bool

// OrderedMap is a generic string-keyed map that preserves author insertion
// order across YAML and JSON marshal/unmarshal round-trips. Iteration via
// Keys() or All() yields keys in the order they were inserted (or, for
// unmarshalled maps, the order they appeared in the source).
//
// Author-order iteration is load-bearing for the compiler's deterministic
// output invariant: map[string]X iteration is hash-randomized, which breaks
// byte-identical rebuilds when an iterated field contributes to rendered
// output. The marshal/unmarshal pair preserves order across round-trips, so
// canonical YAML or JSON parsed into an OrderedMap and re-emitted produces
// the same key order as the source — the property GitOps diff-based
// reviewers depend on.
//
// Order is frequently semantic rather than merely cosmetic. A Workload's
// containers dictionary fixes which container leads the generated pod spec;
// a featuresSchema slot dictionary fixes the order diagnostics report slots
// in; an Aggregate's components dictionary fixes listing order. In each case
// the author's key order is part of what the author declared, and a
// hash-randomized map would discard it.
//
// # Choosing OrderedMap over a plain map
//
// Use OrderedMap for an author-declared collection: anything parsed from a
// YAML or JSON authoring surface whose key order the author chose. Use a
// plain map[string]X for a collection that is derived rather than authored —
// an internal cache or index that never serializes has no author order to
// preserve, and neither does a set computed from author input.
//
// The derived case is worth naming explicitly, because reaching for
// OrderedMap there is a mistake rather than a harmless over-precaution.
// Edges computed from declarations, an inventory assembled by a walk, or any
// other synthesized collection has no author order, so "preserving insertion
// order" preserves whatever traversal happened to build it — which is not a
// stable property. Sort such a collection explicitly by a derivation key
// instead. That is a different mechanism for a different problem, and
// conflating the two produces output that is ordered without being
// deterministic.
//
// OrderedMap is safe to use as a zero value. Set / Delete lazily initialize
// the backing map. The type is not safe for concurrent mutation; callers
// that share an OrderedMap across goroutines must serialize writes
// externally.
//
// Mutation semantics on a nil receiver mirror Go's builtin map: Delete is
// a no-op (returns false); Set panics with a typed error message.
//
// A struct field declared *OrderedMap[V] rather than OrderedMap[V] carries
// one distinction the value form cannot: a field absent from the source
// stays nil, while a field present but empty decodes as non-nil and empty.
// The value form collapses those two into the same zero value. Use the
// pointer form only where a consumer behaves differently for the two cases —
// inheriting a parent's entries versus explicitly clearing them is the usual
// reason. Where both mean the same thing, the value form is simpler and
// spares every reader a nil check.
//
// # Round-trip fidelity for OrderedMap[any]
//
// When V is any, marshal/unmarshal round-trip preserves both numeric
// precision and nested-object author order:
//
//   - Numeric precision: JSON numbers decode as json.Number on the
//     JSON path, preserving exact integer precision regardless of
//     magnitude. Callers reading numeric values via type-switch handle
//     case json.Number and convert via .Int64() / .Float64() / .String()
//     as the use case requires. The YAML path preserves precision via
//     yaml.v3's int64 default; numeric values surface as int / int64 /
//     float64 per yaml.v3's type detection.
//
//   - Nested-object author order: JSON objects and YAML mappings nested
//     inside any-typed values decode as nested *OrderedMap[any] rather
//     than map[string]any, preserving author order at every nesting
//     depth. Callers reading nested objects via type-switch handle case
//     *OrderedMap[any] and iterate via .All() for author-ordered
//     traversal.
//
// Decode depth is bounded; pathological input that exceeds the internal
// depth limit returns an error wrapping ErrOrderedMapDecodeDepthExceeded.
//
// # Dispatching on decoded OrderedMap[any] values
//
// Because a nested object decodes as *OrderedMap[any] rather than
// map[string]any, any consumer that inspects a decoded value by its type
// must account for that shape. Three arrangements do so, and they are not
// equally durable:
//
//   - An explicit type-switch arm on *OrderedMap[any] that iterates via
//     All(). The consumer handles the shape itself and stays correct however
//     it is called.
//   - A pre-unwrap: the consumer recognizes the ordered shape through a
//     small interface and flattens it before its own dispatch runs. Also
//     self-contained.
//   - A parameter typed map[string]any rather than any, which leaves each
//     caller responsible for unwrapping first — via
//     TemplateAlphabeticalMapDeep or an equivalent — before it calls.
//
// Only the first two are enforced by the type system. The third is a
// convention among callsites, and when it lapses it lapses silently: an
// ordered value reaching such a consumer fails its map[string]any type
// assertion and is treated as an opaque scalar rather than as a map, so a
// merge replaces where it should have recursed. Nothing panics and nothing
// reports an error. A change that routes OrderedMap-decoded content into a
// map[string]any-typed consumer therefore adds an explicit arm to that
// consumer in the same change.
//
// A consumer whose type switch ends in a default arm passing unknown shapes
// through untouched is forward-compatible but imprecise: it carries an
// ordered value through rather than mishandling it, which is correct only
// while that consumer owes the value no order-dependent behavior.
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

// TemplateAlphabeticalMap returns a plain map[string]any representation of
// the ordered map, intended exclusively for the renderer's template-
// preparation walker. The caller (text/template) iterates the returned map
// in alphabetical key order (Go stdlib default), not in author order. For
// author-order iteration, callers access Keys() and All() directly; this
// method is the intentional bridge to the alphabetical-iteration template
// boundary that matches Helm-community authoring expectations.
//
// The alphabetical boundary is deliberate and narrow. Every other consumer
// reads author order: MarshalJSON and MarshalYAML both emit keys in author
// order, so anything reading a serialized OrderedMap sees the order the
// author wrote. Go's text/template is the single exception, because its map
// range is alphabetical and offers no hook to change that.
//
// One consequence binds template authoring. A template must not recover an
// ordered sequence by ranging a map, because at this boundary that order is
// alphabetical rather than authored. Where sequence carries meaning — init
// containers, environment variables that reference one another, a dependency
// ordering, successive script lines — the authoring surface models it as a
// list, or a synthesizer emits it already ordered. The order is never
// reconstructed from map iteration inside a template.
//
// Single-level only: nested *OrderedMap[any] values pass through unchanged.
// Callers that need a fully unwrapped representation walkable by Go templates
// at every depth use TemplateAlphabeticalMapDeep instead.
//
// Value receiver so the method dispatches when OrderedMap is a field value
// (same rationale as MarshalYAML / MarshalJSON).
func (m OrderedMap[V]) TemplateAlphabeticalMap() map[string]any {
	if len(m.keys) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(m.keys))
	for _, k := range m.keys {
		out[k] = m.values[k]
	}
	return out
}

// TemplateAlphabeticalMapDeep returns a fully-unwrapped representation of
// the ordered map for Go-template consumption: nested *OrderedMap[any]
// values become map[string]any, []any elements walk recursively, and
// scalars pass through unchanged. Use this when the consumer is a Go
// template that needs to traverse nested objects via field-access syntax
// (.foo.bar.baz) and the value tree may contain gamma-decoded shapes
// (json.Number for numerics, *OrderedMap[any] for nested objects).
//
// The conversion is template-input-only — does not affect storage, marshal
// output, or author-order preservation at the OrderedMap level. Iteration
// inside the returned map is alphabetical (Go stdlib map default), matching
// TemplateAlphabeticalMap's contract.
//
// Value receiver matches TemplateAlphabeticalMap's dispatch rationale.
func (m OrderedMap[V]) TemplateAlphabeticalMapDeep() map[string]any {
	if len(m.keys) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(m.keys))
	for _, k := range m.keys {
		out[k] = unwrapForTemplate(m.values[k])
	}
	return out
}

// unwrapForTemplate recursively converts gamma-decoded OrderedMap structures
// into plain Go maps and slices. Nested *OrderedMap[any] values become
// map[string]any via TemplateAlphabeticalMapDeep; []any elements walk
// recursively; everything else passes through unchanged.
func unwrapForTemplate(v any) any {
	switch val := v.(type) {
	case *OrderedMap[any]:
		if val == nil {
			return nil
		}
		return val.TemplateAlphabeticalMapDeep()
	case OrderedMap[any]:
		return val.TemplateAlphabeticalMapDeep()
	case []any:
		out := make([]any, len(val))
		for i, elem := range val {
			out[i] = unwrapForTemplate(elem)
		}
		return out
	default:
		return v
	}
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

// Set inserts a new key/value pair or updates the value for an existing
// key, preserving the key's position in the iteration order. Like the
// builtin map's m[k] = v, calling Set on a nil *OrderedMap[V] panics; the
// receiver must be a non-nil pointer to a (possibly zero-value) struct,
// or constructed via NewOrderedMap[V].
func (m *OrderedMap[V]) Set(k string, v V) {
	if m == nil {
		panic("types.OrderedMap.Set: receiver is nil; " +
			"construct via NewOrderedMap or use a non-nil " +
			"struct value")
	}
	if m.values == nil {
		m.values = make(map[string]V)
	}
	if _, exists := m.values[k]; !exists {
		m.keys = append(m.keys, k)
	}
	m.values[k] = v
}

// Delete removes a key from the map and from the iteration order,
// returning true if the key was present. Like the builtin map's
// delete(m, k), Delete on a nil *OrderedMap[V] is a no-op that returns
// false.
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
// same key appears more than once within the source document.
//
// When V is any, value-side decoding produces shapes that round-trip
// without losing structure: nested YAML mappings decode into nested
// *OrderedMap[any] (preserving author key order at every depth),
// sequences walk element-wise with the same recursion for mapping-kind
// elements, and scalars surface via yaml.v3's default type detection. Numeric
// precision relies on yaml.v3's int64 default, so high-precision integers
// preserve through round-trip without parallel json.Number machinery.
//
// When V is concrete and implements yaml.Unmarshaler (value or pointer
// receiver), the recursive path defers to V's UnmarshalYAML method. When V
// is concrete without UnmarshalYAML, the typed-decode path runs unchanged.
//
// Re-unmarshal semantics: each call replaces the receiver's contents with
// the decoded document. Re-unmarshalling into a populated OrderedMap
// produces a map containing only the keys from the new document; prior
// content is discarded. The duplicate-key check applies within a single
// source document, not across the receiver's lifetime.
//
// Pathological deeply-nested input that exceeds the internal depth bound
// returns an error wrapping ErrOrderedMapDecodeDepthExceeded rather than
// panicking with a stack overflow.
func (m *OrderedMap[V]) UnmarshalYAML(node *yaml.Node) error {
	return m.unmarshalYAMLWithDepth(node, 0)
}

// unmarshalYAMLWithDepth is the depth-tracked implementation of UnmarshalYAML.
// Mirrors unmarshalJSONWithDepth's structure on the YAML path.
func (m *OrderedMap[V]) unmarshalYAMLWithDepth(node *yaml.Node, depth int) error {
	if depth > orderedMapDecodeMaxDepth {
		return fmt.Errorf("%w (depth=%d)", ErrOrderedMapDecodeDepthExceeded(), depth)
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("OrderedMap: expected mapping node, got kind %d at line %d", node.Kind, node.Line)
	}
	m.keys = make([]string, 0, len(node.Content)/2)
	m.values = make(map[string]V, len(node.Content)/2)

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

		v, err := decodeYAMLValue[V](valueNode, depth+1)
		if err != nil {
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
// When V is any, value-side decoding produces shapes that round-trip
// without losing structure: nested JSON objects decode into nested
// *OrderedMap[any] (preserving author key order at every depth),
// numeric values decode as json.Number (preserving exact integer precision
// regardless of magnitude), and arrays of objects recurse element-wise. When
// V is concrete and implements json.Unmarshaler (value or pointer receiver),
// the recursive path defers to V's UnmarshalJSON method. When V is concrete
// without UnmarshalJSON, the typed-decode path runs unchanged.
//
// Rejects duplicate keys with the duplicate key name in the error. The
// duplicate-key check applies within a single source document.
//
// Re-unmarshal semantics: each call replaces the receiver's contents with
// the decoded document. Re-unmarshalling into a populated OrderedMap
// produces a map containing only the keys from the new document; prior
// content is discarded.
//
// Pathological deeply-nested input that exceeds the internal depth bound
// returns an error wrapping ErrOrderedMapDecodeDepthExceeded rather than
// panicking with a stack overflow.
func (m *OrderedMap[V]) UnmarshalJSON(data []byte) error {
	return m.unmarshalJSONWithDepth(data, 0)
}

// unmarshalJSONWithDepth is the depth-tracked implementation of UnmarshalJSON.
// The public entry point calls it with depth=0; the recursive path through
// decodeJSONValue increments depth at each level of nested OrderedMap
// construction.
func (m *OrderedMap[V]) unmarshalJSONWithDepth(data []byte, depth int) error {
	if depth > orderedMapDecodeMaxDepth {
		return fmt.Errorf("%w (depth=%d)", ErrOrderedMapDecodeDepthExceeded(), depth)
	}

	m.keys = make([]string, 0)
	m.values = make(map[string]V)

	// Handle the literal null — JSON null decodes into a nil-equivalent state,
	// i.e. an empty OrderedMap, without error.
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}

	dec := json.NewDecoder(bytes.NewReader(data))

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

		v, err := decodeJSONValue[V](raw, depth+1)
		if err != nil {
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

// vImplementsJSONUnmarshaler reports whether V or *V satisfies
// json.Unmarshaler. The detection caches via jsonUnmarshalerCache so the
// reflection cost is paid at most once per V type.
func vImplementsJSONUnmarshaler[V any]() bool {
	var zero V
	t := reflect.TypeOf(&zero).Elem()
	if cached, ok := jsonUnmarshalerCache.Load(t); ok {
		return cached.(bool)
	}
	iface := reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
	impl := t.Implements(iface) || reflect.PointerTo(t).Implements(iface)
	actual, _ := jsonUnmarshalerCache.LoadOrStore(t, impl)
	return actual.(bool)
}

// vImplementsYAMLUnmarshaler reports whether V or *V satisfies
// yaml.Unmarshaler. Mirror of vImplementsJSONUnmarshaler for the YAML
// decode path.
func vImplementsYAMLUnmarshaler[V any]() bool {
	var zero V
	t := reflect.TypeOf(&zero).Elem()
	if cached, ok := yamlUnmarshalerCache.Load(t); ok {
		return cached.(bool)
	}
	iface := reflect.TypeOf((*yaml.Unmarshaler)(nil)).Elem()
	impl := t.Implements(iface) || reflect.PointerTo(t).Implements(iface)
	actual, _ := yamlUnmarshalerCache.LoadOrStore(t, impl)
	return actual.(bool)
}

// vIsInterface reports whether V is an interface kind. Used by the decode
// helpers to dispatch between the recursive any path (which
// produces nested *OrderedMap[any] for nested objects and
// json.Number for numerics) and the typed-V plain-decode path.
func vIsInterface[V any]() bool {
	var zero V
	t := reflect.TypeOf(&zero).Elem()
	if cached, ok := vIsInterfaceCache.Load(t); ok {
		return cached.(bool)
	}
	impl := t.Kind() == reflect.Interface
	actual, _ := vIsInterfaceCache.LoadOrStore(t, impl)
	return actual.(bool)
}

// decodeJSONValue is the per-value JSON decoder used by
// unmarshalJSONWithDepth. Dispatch order:
//
//  1. Depth-bound check.
//  2. If V or *V implements json.Unmarshaler, plain json.Unmarshal — V's
//     UnmarshalJSON method runs.
//  3. If V is concrete (kind != Interface), plain json.Unmarshal — typed
//     decode unchanged from pre-gamma behavior.
//  4. If V is any: peek the first non-whitespace byte and dispatch:
//     '{' recurses into a fresh *OrderedMap[any], '[' walks the
//     array element-wise with recursive decode for each element, numeric
//     values decode via a per-value json.Decoder with UseNumber producing
//     json.Number, scalars (string, bool, null) plain-decode.
//
// The per-value UseNumber decoder is the load-bearing change for high-
// precision integer preservation: encoding/json's outer-decoder UseNumber
// flag does not propagate into per-value json.Unmarshal calls, so closing
// the precision gap at the per-value layer is mandatory.
func decodeJSONValue[V any](raw json.RawMessage, depth int) (V, error) {
	var zero V

	if depth > orderedMapDecodeMaxDepth {
		return zero, fmt.Errorf("%w (depth=%d)", ErrOrderedMapDecodeDepthExceeded(), depth)
	}

	if vImplementsJSONUnmarshaler[V]() {
		var v V
		if err := json.Unmarshal(raw, &v); err != nil {
			return zero, err
		}
		return v, nil
	}

	if !vIsInterface[V]() {
		var v V
		if err := json.Unmarshal(raw, &v); err != nil {
			return zero, err
		}
		return v, nil
	}

	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return zero, fmt.Errorf("OrderedMap: empty JSON value")
	}

	switch trimmed[0] {
	case '{':
		nested := NewOrderedMap[any]()
		if err := nested.unmarshalJSONWithDepth(raw, depth); err != nil {
			return zero, err
		}
		return any(nested).(V), nil

	case '[':
		var rawArr []json.RawMessage
		if err := json.Unmarshal(raw, &rawArr); err != nil {
			return zero, err
		}
		result := make([]any, 0, len(rawArr))
		for _, elem := range rawArr {
			ev, err := decodeJSONValue[any](elem, depth+1)
			if err != nil {
				return zero, err
			}
			result = append(result, ev)
		}
		return any(result).(V), nil

	case '"', 't', 'f', 'n':
		var v V
		if err := json.Unmarshal(raw, &v); err != nil {
			return zero, err
		}
		return v, nil

	default:
		valDec := json.NewDecoder(bytes.NewReader(raw))
		valDec.UseNumber()
		var num json.Number
		if err := valDec.Decode(&num); err != nil {
			return zero, fmt.Errorf("OrderedMap: decoding JSON number: %w", err)
		}
		return any(num).(V), nil
	}
}

// decodeYAMLValue is the per-value YAML decoder used by
// unmarshalYAMLWithDepth. Dispatch order mirrors decodeJSONValue:
//
//  1. Depth-bound check.
//  2. If V or *V implements yaml.Unmarshaler, valueNode.Decode delegates to
//     V's UnmarshalYAML method.
//  3. If V is concrete (kind != Interface), valueNode.Decode runs unchanged.
//  4. If V is any: branch on valueNode.Kind. Mapping nodes recurse
//     into a fresh *OrderedMap[any]; sequence nodes walk element-
//     wise with recursive decode for each element; scalar / alias / document
//     nodes use yaml.v3's default Decode (which preserves int64 precision
//     for numerics, so no parallel UseNumber-style layer is needed).
func decodeYAMLValue[V any](valueNode *yaml.Node, depth int) (V, error) {
	var zero V

	if depth > orderedMapDecodeMaxDepth {
		return zero, fmt.Errorf("%w (depth=%d)", ErrOrderedMapDecodeDepthExceeded(), depth)
	}

	if vImplementsYAMLUnmarshaler[V]() {
		var v V
		if err := valueNode.Decode(&v); err != nil {
			return zero, err
		}
		return v, nil
	}

	if !vIsInterface[V]() {
		var v V
		if err := valueNode.Decode(&v); err != nil {
			return zero, err
		}
		return v, nil
	}

	switch valueNode.Kind {
	case yaml.MappingNode:
		nested := NewOrderedMap[any]()
		if err := nested.unmarshalYAMLWithDepth(valueNode, depth); err != nil {
			return zero, err
		}
		return any(nested).(V), nil

	case yaml.SequenceNode:
		result := make([]any, 0, len(valueNode.Content))
		for _, elem := range valueNode.Content {
			ev, err := decodeYAMLValue[any](elem, depth+1)
			if err != nil {
				return zero, err
			}
			result = append(result, ev)
		}
		return any(result).(V), nil

	default:
		var v V
		if err := valueNode.Decode(&v); err != nil {
			return zero, err
		}
		return v, nil
	}
}
