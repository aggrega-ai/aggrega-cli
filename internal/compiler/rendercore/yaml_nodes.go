// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import "go.yaml.in/yaml/v3"

// DocumentMapping returns the mapping node at the document root, or nil
// when the document is empty / not a mapping. Handles both the
// DocumentNode wrapper that yaml.Unmarshal produces and the bare mapping
// case that some constructors yield.
func DocumentMapping(root *yaml.Node) *yaml.Node {
	if root == nil {
		return nil
	}
	n := root
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		n = n.Content[0]
	}
	n = UnwrapAlias(n)
	if n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

// FindKeyValueNode returns the (key, value) yaml.Node pair for a given
// string key inside a mapping, or (nil, nil) when the key is absent.
// Returning the key node lets callers distinguish "absent" from "present
// but value is nil"; some callers discard the key, so the dual return surface stays useful
// for the path that needs to check presence before reaching for the value.
func FindKeyValueNode(mapping *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		k := mapping.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			return k, mapping.Content[i+1]
		}
	}
	return nil, nil
}

// UnwrapAlias resolves a yaml.AliasNode to its referent; returns the
// argument unchanged when it is not an alias. Defensive — Aggrega's
// catalog templates don't author YAML anchors today, but yaml.v3 surfaces
// alias nodes generically and the rest of the injection path expects to
// see the resolved Kind.
func UnwrapAlias(n *yaml.Node) *yaml.Node {
	if n != nil && n.Kind == yaml.AliasNode && n.Alias != nil {
		return n.Alias
	}
	return n
}
