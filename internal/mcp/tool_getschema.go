// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	schemas "github.com/aggrega-ai/aggrega-schemas"
	"go.yaml.in/yaml/v3"
)

// getSchemaArgs is the get-schema tool's argument shape.
type getSchemaArgs struct {
	Kind string `json:"kind"`
	List bool   `json:"list"`
}

// getSchemaListResult is the payload when the caller asks for the kind index.
type getSchemaListResult struct {
	Kinds []string `json:"kinds"`
}

// getSchemaResult is the payload for a single kind's schema.
type getSchemaResult struct {
	Kind   string          `json:"kind"`
	Schema json.RawMessage `json:"schema"`
}

var getSchemaToolSpec = toolSpec{
	description: "Return the canonical JSON Schema for an Aggrega entity or contract kind, byte-identical to the " +
		"distributed schema file. Use it to learn the exact structure, required fields, and value constraints of a " +
		"kind before authoring or validating one. Pass the lowercase-hyphenated kind token as `kind` — for example " +
		"component, aggregate, release, manifest, recipe, ir, or wasm-provider. Pass `list: true` (with no kind) to " +
		"enumerate every addressable kind first. An unknown kind returns the full valid set so you can correct a typo " +
		"without a second call. This is the authoritative shape source; scaffold derives its starter skeletons from it.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "kind": {
      "type": "string",
      "minLength": 1,
      "description": "The lowercase-hyphenated schema kind token, e.g. component, aggregate, release, recipe, ir, wasm-provider. Required unless list is true."
    },
    "list": {
      "type": "boolean",
      "description": "When true, enumerate every addressable schema kind instead of returning one schema. Ignores kind."
    }
  },
  "anyOf": [
    {"required":["kind"]},
    {"required":["list"],"properties":{"list":{"const":true}}}
  ]
}`,
	invoke: func(_ *server, _ context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
		var a getSchemaArgs
		if len(args) > 0 {
			if err := json.Unmarshal(args, &a); err != nil {
				return invalidToolArguments("get-schema", err)
			}
		}
		if a.List {
			kinds := schemas.Kinds()
			return textResult("Available schema kinds:\n"+strings.Join(kinds, "\n"), getSchemaListResult{Kinds: kinds})
		}
		kind := strings.TrimSpace(a.Kind)
		if kind == "" {
			return errorResult("get-schema requires a 'kind' argument, or 'list': true to enumerate kinds")
		}
		schema, ok := schemas.SchemaByKind(kind)
		if !ok {
			return errorResult(unknownSchemaKindGuidance(kind))
		}
		return textResult(schema, getSchemaResult{Kind: kind, Schema: json.RawMessage(schema)})
	},
}

// unknownSchemaKindGuidance renders the corrective text for a kind token no
// schema answers to: the token as it was sent, and the full valid set so the
// caller corrects a typo without a second call. Both schema-driven tools reach
// the same set through the same route, so the two failures cannot phrase the
// same rejection differently.
func unknownSchemaKindGuidance(kind string) string {
	return fmt.Sprintf("unknown schema kind %q. Valid kinds: %s", kind, strings.Join(schemas.Kinds(), ", "))
}

// scaffoldArgs is the scaffold tool's argument shape.
type scaffoldArgs struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// scaffoldResult is the scaffold tool's structured payload: the kind it built a
// skeleton for and the rendered YAML skeleton.
type scaffoldResult struct {
	Kind     string `json:"kind"`
	Skeleton string `json:"skeleton"`
}

var scaffoldToolSpec = toolSpec{
	description: "Generate a minimal starter skeleton for an Aggrega entity kind, derived directly from that kind's " +
		"JSON Schema so it always tracks the real contract — the skeleton carries exactly the required fields, with the " +
		"fixed apiVersion and kind filled in and a placeholder for every other required value. Pass the kind token as " +
		"`kind` (component, aggregate, release, recipe, manifest, feature, and so on) and, optionally, a `name` to seed " +
		"metadata.name. Use it to start a new entity from a correct shape rather than from memory, then flesh out the " +
		"placeholders and validate. The output is YAML ready to write to a file.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "kind": {
      "type": "string",
      "minLength": 1,
      "description": "The entity kind to scaffold, as a lowercase-hyphenated schema token, e.g. component, aggregate, release, recipe, feature, manifest."
    },
    "name": {
      "type": "string",
      "description": "Optional value to seed metadata.name in the generated skeleton."
    }
  },
  "required": ["kind"]
}`,
	invoke: func(_ *server, _ context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
		var a scaffoldArgs
		if len(args) > 0 {
			if err := json.Unmarshal(args, &a); err != nil {
				return invalidToolArguments("scaffold", err)
			}
		}
		kind := strings.TrimSpace(a.Kind)
		if kind == "" {
			return errorResult("scaffold requires a 'kind' argument")
		}
		schema, ok := schemas.SchemaByKind(kind)
		if !ok {
			return errorResult(unknownSchemaKindGuidance(kind))
		}
		skeleton, err := schemaSkeleton([]byte(schema), strings.TrimSpace(a.Name))
		if err != nil {
			return errorResult(explainToolError(
				fmt.Sprintf("Could not derive a skeleton for kind %q from its schema.", kind), err, kind))
		}
		return textResult(skeleton, scaffoldResult{Kind: kind, Skeleton: skeleton})
	},
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

// schemaSkeleton derives a minimal YAML starter skeleton from a JSON Schema.
// The skeleton carries exactly the schema's required properties, recursively:
// a property with a fixed value (const, or a single-valued enum) is filled with
// that value; every other required leaf gets a type-appropriate placeholder.
// Iteration follows the schema's ordered `required` lists, never a map range,
// so the same schema yields byte-identical output on every run. When name is
// non-empty and the skeleton carries a metadata.name field, it is seeded.
func schemaSkeleton(raw []byte, name string) (string, error) {
	node, err := skeletonNode(raw)
	if err != nil {
		return "", err
	}
	if name != "" {
		seedMetadataName(node, name)
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
