// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Extension members — the keys beginning x- that the Compose Specification
// reserves for data any tool may carry and none has to read — at the top level of
// a compose file and on a service.
//
// ComposeFile and Service decode and encode themselves so that their extension
// members travel with them: the decode keeps each member in the order the mapping
// writes it, and the encode writes them after every key the type models, in that
// order. Nothing reads what a member holds, with one exception: the merge takes a
// service's x-aggrega-networks-of member off the service and resolves it (see
// resolveNetworksOf in networks_of.go). Inside a model's body an extension
// member is carried by the body's own inline map, which is why only these two
// types hold one.
//
// This file carries no build tag: it compiles for the host and for the wasip1
// guest alike, so it imports no host-only capability.

package composecore

import (
	"fmt"
	"reflect"
	"strings"

	"go.yaml.in/yaml/v3"
)

// extensionKeyPrefix opens every extension member's key, as the Compose
// Specification defines it.
const extensionKeyPrefix = "x-"

// isExtensionKey reports whether key names an extension member.
func isExtensionKey(key string) bool {
	return strings.HasPrefix(key, extensionKeyPrefix)
}

// Extensions holds the extension members one compose mapping writes, in the order
// it writes them, each with the value written for it. A field of this type
// declares that its struct carries extension members: the fragment key walk reads
// that declaration, so a struct holding one accepts every x- key and a struct
// holding none refuses them like any other key it does not model.
//
// The entry list is unexported so a member reaches the emitted bytes only through
// add, which keeps one entry per key, and through the emitted order the decode
// recorded. No map is iterated on the path to the output.
type Extensions struct {
	entries []extensionEntry
}

// extensionEntry is one extension member: its key and the value written for it,
// held as a YAML node so the value's own key order survives.
type extensionEntry struct {
	key   string
	value *yaml.Node
}

var extensionsType = reflect.TypeOf(Extensions{})

// Len returns the number of extension members held.
func (e Extensions) Len() int { return len(e.entries) }

// Keys returns the members' keys in the order they are held.
func (e Extensions) Keys() []string {
	keys := make([]string, 0, len(e.entries))
	for _, entry := range e.entries {
		keys = append(keys, entry.key)
	}
	return keys
}

// has reports whether a member is held under key.
func (e Extensions) has(key string) bool {
	for _, entry := range e.entries {
		if entry.key == key {
			return true
		}
	}
	return false
}

// add holds value under key, after every member already held. A key already held
// keeps its place and its value: the first writer of a key decides both.
func (e *Extensions) add(key string, value *yaml.Node) {
	if e.has(key) {
		return
	}
	e.entries = append(e.entries, extensionEntry{key: key, value: value})
}

// take removes the member held under key and returns its value, reporting
// whether one was held. The members after it keep their order.
func (e *Extensions) take(key string) (*yaml.Node, bool) {
	for i, entry := range e.entries {
		if entry.key == key {
			e.entries = append(e.entries[:i:i], e.entries[i+1:]...)
			return entry.value, true
		}
	}
	return nil, false
}

// decodeExtensions reads the extension members of mapping in document order. A
// member a YAML merge key (`<<`) brings in is read where the merge sits, as the
// fragment key walk reads it, with the value the decoder gives it (see
// effectiveFragmentPairs).
func decodeExtensions(mapping *yaml.Node) Extensions {
	var out Extensions
	for _, kv := range effectiveFragmentPairs(mapping) {
		if !isExtensionKey(kv.key) {
			continue
		}
		out.add(kv.key, carriedExtensionValue(kv.value))
	}
	return out
}

// carriedExtensionValue copies the value written for an extension member into the
// form it is held and emitted in: every alias replaced by a copy of the node it
// names, and no anchor, comment or presentation style kept. The content — each
// scalar, its tag, and every key in the order written — is what is carried.
//
// An alias is resolved because the node it names may sit in a part of the
// fragment the assembled file does not reproduce as written, where its anchor
// would not be emitted, and an alias whose anchor is missing is not YAML. Style is
// dropped so a member's emitted bytes are a function of its content alone, which
// is what two contributors writing one top-level member are compared on: a flow
// mapping and a block mapping holding the same keys are one declaration.
func carriedExtensionValue(n *yaml.Node) *yaml.Node {
	n = resolveFragmentAlias(n)
	if n == nil {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	}
	out := &yaml.Node{Kind: n.Kind, Tag: n.Tag, Value: n.Value}
	if len(n.Content) > 0 {
		out.Content = make([]*yaml.Node, 0, len(n.Content))
		for _, c := range n.Content {
			out.Content = append(out.Content, carriedExtensionValue(c))
		}
	}
	return out
}

// encodeWithExtensions encodes fields — a struct holding every key the type
// models — and writes ext's members after them, in the order held. With no member
// held it returns fields itself, so a value carrying none encodes exactly as the
// struct does.
func encodeWithExtensions(fields any, ext Extensions) (any, error) {
	if ext.Len() == 0 {
		return fields, nil
	}
	var node yaml.Node
	if err := node.Encode(fields); err != nil {
		return nil, err
	}
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("extension members: encoded %T is not a mapping", fields)
	}
	for _, entry := range ext.entries {
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: entry.key},
			entry.value,
		)
	}
	return &node, nil
}

// composeFileFields is ComposeFile without its methods: the struct ComposeFile's
// own decode and encode read and write every modelled key through, and the shape
// the fragment key walk reads for the top level of a fragment.
type composeFileFields ComposeFile

// serviceFields is Service without its methods, for the reason composeFileFields
// gives.
type serviceFields Service

var (
	composeFileFieldsType = reflect.TypeOf(composeFileFields{})
	serviceFieldsType     = reflect.TypeOf(serviceFields{})
)

// UnmarshalYAML decodes every modelled key as the struct declares it and keeps
// the extension members the top level writes, in document order.
func (c *ComposeFile) UnmarshalYAML(node *yaml.Node) error {
	var fields composeFileFields
	if err := node.Decode(&fields); err != nil {
		return err
	}
	fields.Extensions = decodeExtensions(node)
	*c = ComposeFile(fields)
	return nil
}

// MarshalYAML writes every modelled key, then the extension members in the order
// held.
//
// Value receiver so a ComposeFile encodes the same way whether it is reached by
// value or through a pointer.
func (c ComposeFile) MarshalYAML() (any, error) {
	return encodeWithExtensions(composeFileFields(c), c.Extensions)
}

// walkFragmentKeys walks the top level of a fragment against the keys
// ComposeFile models, accepting every extension member.
func (ComposeFile) walkFragmentKeys(w *fragmentKeyWalk, node *yaml.Node, path string) {
	w.walk(node, composeFileFieldsType, path)
}

// UnmarshalYAML decodes every modelled key as the struct declares it, keeps the
// extension members the service writes, in document order, and records the
// modelled keys it writes as null.
func (s *Service) UnmarshalYAML(node *yaml.Node) error {
	var fields serviceFields
	nulls, err := decodeRecordingNulls(node, &fields)
	if err != nil {
		return err
	}
	fields.Extensions = decodeExtensions(node)
	fields.writtenNull = nulls
	*s = Service(fields)
	return nil
}

// MarshalYAML writes every modelled key, each key written as null that still has
// no value of its own as `key: null` at its place, then the extension members in
// the order held.
//
// Value receiver so dispatch works when Service is the value of a map, which is
// not addressable.
func (s Service) MarshalYAML() (any, error) {
	fields, err := encodeRestoringNulls(serviceFields(s), s.writtenNull)
	if err != nil {
		return nil, err
	}
	return encodeWithExtensions(fields, s.Extensions)
}

// walkFragmentKeys walks a service against the keys Service models, accepting
// every extension member.
func (Service) walkFragmentKeys(w *fragmentKeyWalk, node *yaml.Node, path string) {
	w.walk(node, serviceFieldsType, path)
}
