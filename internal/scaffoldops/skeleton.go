// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package scaffoldops

import (
	"encoding/json"
	"fmt"
	"strings"

	schemas "github.com/aggrega-ai/aggrega-schemas"
	"go.yaml.in/yaml/v3"
)

// UnknownKindError reports a kind token no schema answers to. Its message is the
// corrective text a caller shows: the token as it was sent, and the full valid
// set, so whoever sent it corrects a typo without a second call.
//
// It is a distinct type rather than a plain error because a caller renders an
// unresolvable kind and a failed walk on different channels — the first is an
// argument to correct, the second a fault in the schema set — and a caller that
// told the two apart by matching message text would stop telling them apart the
// moment either was reworded.
type UnknownKindError struct {
	// Kind is the token as it was received, with surrounding space removed.
	Kind string
}

// Error renders the corrective text for the unresolvable kind token.
func (e *UnknownKindError) Error() string { return UnknownKindGuidance(e.Kind) }

// UnknownKindGuidance renders the corrective text for a kind token no schema
// answers to: the token as it was sent, and the full valid set so the caller
// corrects a typo without a second call.
//
// It is exported because more than one capability resolves a kind token against
// the same set — drafting a skeleton for a kind, and reporting a kind's schema —
// and every one of them reaches this single rendering, so two of them cannot
// phrase the same rejection differently.
func UnknownKindGuidance(kind string) string {
	return fmt.Sprintf("unknown schema kind %q. Valid kinds: %s", kind, strings.Join(schemas.Kinds(), ", "))
}

// Skeleton derives a minimal YAML starter document for an entity kind from that
// kind's JSON Schema. The skeleton carries exactly the schema's required
// properties, recursively: a property with a fixed value (const, or a
// single-valued enum) is filled with that value; every other required leaf gets
// a type-appropriate placeholder. When name is non-empty and the skeleton
// carries a metadata.name field, it is seeded.
//
// Iteration follows the schema's ordered `required` lists, never a map range, so
// one kind yields byte-identical text on every run and to every caller.
//
// A kind token no schema answers to yields an *UnknownKindError carrying the
// corrective text; a schema the walk cannot read yields the walk's own failure,
// so a caller can route the two differently.
//
// It reads only the schema set compiled into the binary, so it resolves nothing
// from a catalog, a registry or the network — and it writes nothing.
//
// Both arguments have their surrounding space removed here rather than at each
// caller. That is what stops two callers handed the same padded token from
// resolving different kinds, which is the one way callers sharing this
// computation could still disagree about its answer.
func Skeleton(kind, name string) (string, error) {
	kind = strings.TrimSpace(kind)
	schema, ok := schemas.SchemaByKind(kind)
	if !ok {
		return "", &UnknownKindError{Kind: kind}
	}
	node, err := skeletonNode([]byte(schema))
	if err != nil {
		return "", err
	}
	if seed := strings.TrimSpace(name); seed != "" {
		seedMetadataName(node, seed)
	}
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(node); err != nil {
		_ = enc.Close()
		return "", fmt.Errorf("encode skeleton: %w", err)
	}
	if err := enc.Close(); err != nil {
		return "", fmt.Errorf("encode skeleton: %w", err)
	}
	return b.String(), nil
}

// jsonSchemaNode is the minimal view of a JSON Schema object the skeleton
// generator walks: the fields that decide what a required entry's placeholder
// looks like. Type is decoded raw because JSON Schema permits it to be either a
// single string or an array of strings.
type jsonSchemaNode struct {
	Type       json.RawMessage            `json:"type"`
	Const      json.RawMessage            `json:"const"`
	Enum       []json.RawMessage          `json:"enum"`
	Required   []string                   `json:"required"`
	Properties map[string]json.RawMessage `json:"properties"`
	Items      json.RawMessage            `json:"items"`
}

// skeletonNode builds the YAML node for one schema node. A fixed value (const
// or single-valued enum) wins; otherwise the node is shaped by its declared
// type. An object emits a mapping of its required children in declared order; an
// array emits an empty sequence; a scalar type emits a placeholder of that type.
func skeletonNode(raw []byte) (*yaml.Node, error) {
	var sn jsonSchemaNode
	if err := json.Unmarshal(raw, &sn); err != nil {
		return nil, fmt.Errorf("parse schema node: %w", err)
	}
	if len(sn.Const) > 0 {
		return scalarFromJSON(sn.Const)
	}
	if len(sn.Enum) == 1 {
		return scalarFromJSON(sn.Enum[0])
	}
	switch schemaPrimaryType(sn.Type) {
	case "object":
		m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, key := range sn.Required {
			child := sn.Properties[key]
			var childNode *yaml.Node
			if len(child) > 0 {
				cn, err := skeletonNode(child)
				if err != nil {
					return nil, err
				}
				childNode = cn
			} else {
				childNode = placeholderScalar("")
			}
			m.Content = append(m.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
				childNode)
		}
		return m, nil
	case "array":
		return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}, nil
	case "integer", "number":
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "0"}, nil
	case "boolean":
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "false"}, nil
	default:
		return placeholderScalar(""), nil
	}
}

// schemaPrimaryType reduces a JSON Schema `type` — a string or an array of
// strings — to a single primary type, preferring the first non-null entry when
// it is a union. An absent or unparseable type yields the empty string, which
// the caller treats as a free scalar placeholder.
func schemaPrimaryType(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return single
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		for _, t := range many {
			if t != "null" {
				return t
			}
		}
	}
	return ""
}

// scalarFromJSON renders a JSON scalar (const or enum value) as a YAML scalar
// node preserving the value's type.
func scalarFromJSON(raw json.RawMessage) (*yaml.Node, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("parse fixed value: %w", err)
	}
	n := &yaml.Node{}
	if err := n.Encode(v); err != nil {
		return nil, fmt.Errorf("encode fixed value: %w", err)
	}
	return n, nil
}

// placeholderScalar returns a quoted empty-string placeholder for a required
// free-form field the author fills in.
func placeholderScalar(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v, Style: yaml.DoubleQuotedStyle}
}

// seedMetadataName sets metadata.name in a skeleton mapping when both the
// metadata object and its name field are present, leaving every other
// placeholder untouched.
func seedMetadataName(root *yaml.Node, name string) {
	metadata := mappingChild(root, "metadata")
	if metadata == nil {
		return
	}
	nameNode := mappingChild(metadata, "name")
	if nameNode == nil {
		return
	}
	nameNode.Kind = yaml.ScalarNode
	nameNode.Tag = "!!str"
	nameNode.Value = name
	nameNode.Style = 0
}

// mappingChild returns the value node for key in a mapping node, or nil when
// the node is not a mapping or the key is absent.
func mappingChild(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}
