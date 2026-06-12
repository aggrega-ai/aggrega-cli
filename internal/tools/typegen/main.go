// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Command typegen is the schema→Go field-declaration generator prototyped
// in the typegen design spike. It reads an entity kind's published JSON
// schema (from the embedded aggrega-schemas module) and emits the kind's
// struct field declarations — names, Go types, json/yaml tags, and godoc
// drawn from the schema descriptions — into a `<kind>_gen.go` file in
// pkg/types. Hand-written content (methods, custom marshalers, shared
// types like Maintainer) lives in adjacent hand-written files; the
// generator never touches them.
//
// Scope: this is a SPIKE. It handles exactly the shapes the Provider
// kind exercises — a top-level entity (apiVersion/kind/metadata/spec)
// whose metadata and spec are named nested objects of scalar, array, and
// x-aggrega-go-type-referenced fields. The non-mechanical idioms
// (OrderedMap, polymorphic, content-shape dispatch) are out of the
// prototype's reach by design; the decision memo rates them.
//
// Determinism: fields emit in the schema's property declaration order
// (encoding/json preserves object key order via json.RawMessage +
// manual decode here), so regeneration is byte-stable.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"sort"
	"strings"

	schemas "github.com/aggrega-ai/aggrega-schemas"
)

// orderedObject decodes a JSON Schema object node preserving the
// declared property order (json.Decoder over the raw "properties" map's
// token stream).
type schemaNode struct {
	Type        string
	Description string
	GoType      string // x-aggrega-go-type override (names a hand-written type)
	Required    []string
	props       []namedNode // in declared order
	Items       *schemaNode
}

type namedNode struct {
	name string
	node schemaNode
}

func decodeNode(raw json.RawMessage) (schemaNode, error) {
	var n schemaNode
	// Decode the flat fields first.
	var flat struct {
		Type        string          `json:"type"`
		Description string          `json:"description"`
		GoType      string          `json:"x-aggrega-go-type"`
		Required    []string        `json:"required"`
		Items       json.RawMessage `json:"items"`
		Properties  json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &flat); err != nil {
		return n, err
	}
	n.Type, n.Description, n.GoType, n.Required = flat.Type, flat.Description, flat.GoType, flat.Required
	if len(flat.Items) > 0 {
		it, err := decodeNode(flat.Items)
		if err != nil {
			return n, err
		}
		n.Items = &it
	}
	if len(flat.Properties) > 0 {
		ordered, err := decodeOrderedProps(flat.Properties)
		if err != nil {
			return n, err
		}
		n.props = ordered
	}
	return n, nil
}

// decodeOrderedProps walks the properties object's tokens in order,
// preserving declaration order (the determinism contract).
func decodeOrderedProps(raw json.RawMessage) ([]namedNode, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if _, err := dec.Token(); err != nil { // opening '{'
		return nil, err
	}
	var out []namedNode
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key := keyTok.(string)
		var sub json.RawMessage
		if err := dec.Decode(&sub); err != nil {
			return nil, err
		}
		node, err := decodeNode(sub)
		if err != nil {
			return nil, err
		}
		out = append(out, namedNode{name: key, node: node})
	}
	return out, nil
}

var initialisms = map[string]string{"api": "API", "url": "URL", "id": "ID", "ir": "IR", "uri": "URI", "json": "JSON"}

// pascal converts a schema property key (camelCase) to a Go exported
// field name, applying the Go initialism convention.
func pascal(s string) string {
	var segs []string
	cur := strings.Builder{}
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			segs = append(segs, cur.String())
			cur.Reset()
		}
		cur.WriteRune(r)
	}
	segs = append(segs, cur.String())
	out := strings.Builder{}
	for _, seg := range segs {
		low := strings.ToLower(seg)
		if init, ok := initialisms[low]; ok {
			out.WriteString(init)
		} else {
			out.WriteString(strings.ToUpper(seg[:1]) + seg[1:])
		}
	}
	return out.String()
}

// goType maps a schema node to its Go type. x-aggrega-go-type names the
// Go type of the node it annotates, uniformly — on an object node it is
// that type; on an `items` node the mechanical array recursion lifts it
// to []T. (Array-level annotation stays free as a future whole-field
// override.) Otherwise the mechanical scalar/array mapping applies, and a
// nested object becomes the named struct typeName.
func goType(n schemaNode, typeName string) string {
	if n.GoType != "" {
		return n.GoType
	}
	switch n.Type {
	case "string":
		return "string"
	case "boolean":
		return "bool"
	case "integer":
		return "int"
	case "array":
		if n.Items != nil {
			return "[]" + goType(*n.Items, "")
		}
		return "[]any"
	case "object":
		return typeName // a named nested struct
	default:
		return "any"
	}
}

// emitStruct writes one struct declaration. parentName seeds nested
// named types (<parent><Field>). Returns the nested object structs it
// discovered, to emit after.
func emitStruct(b *strings.Builder, typeName, doc string, n schemaNode) []pending {
	required := map[string]bool{}
	for _, r := range n.Required {
		required[r] = true
	}
	if doc != "" {
		b.WriteString(wrapDoc(typeName, doc))
	}
	fmt.Fprintf(b, "type %s struct {\n", typeName)
	var nested []pending
	for _, p := range n.props {
		field := pascal(p.name)
		nestedName := typeName + field
		gt := goType(p.node, nestedName)
		if p.node.Type == "object" && p.node.GoType == "" && len(p.node.props) > 0 {
			nested = append(nested, pending{name: nestedName, doc: p.node.Description, node: p.node})
		}
		tag := p.name
		if !required[p.name] {
			tag += ",omitempty"
		}
		if p.node.Description != "" {
			fmt.Fprintf(b, "\t// %s %s\n", field, oneLine(p.node.Description))
		}
		fmt.Fprintf(b, "\t%s %s `json:%q yaml:%q`\n", field, gt, tag, tag)
	}
	b.WriteString("}\n\n")
	return nested
}

type pending struct {
	name string
	doc  string
	node schemaNode
}

func wrapDoc(name, doc string) string {
	return "// " + name + " " + oneLine(doc) + "\n"
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: typegen <output-file>")
		os.Exit(2)
	}
	outPath := os.Args[1]

	root, err := decodeNode(json.RawMessage(schemas.ProviderV1alpha1))
	if err != nil {
		fmt.Fprintf(os.Stderr, "decode schema: %v\n", err)
		os.Exit(1)
	}

	var b strings.Builder
	b.WriteString("// SPDX-License-Identifier: Apache-2.0\n// Copyright 2026 Cognitio S.r.l.\n\n")
	b.WriteString("// Code generated by internal/tools/typegen from\n")
	b.WriteString("// aggrega-schemas/json/provider-v1alpha1.schema.json. DO NOT EDIT.\n")
	b.WriteString("// Hand-written content (shared types, methods, marshalers) lives in\n")
	b.WriteString("// provider.go and common.go; regenerate with `go generate ./pkg/types/...`.\n\n")
	b.WriteString("package types\n\n")

	// Top-level entity struct, then its nested named structs breadth-first
	// in declared order.
	queue := []pending{{name: "Provider", doc: root.Description, node: root}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		nested := emitStruct(&b, cur.name, cur.doc, cur.node)
		queue = append(queue, nested...)
	}

	src, ferr := format.Source([]byte(b.String()))
	if ferr != nil {
		// Emit unformatted for debugging, then fail.
		_ = os.WriteFile(outPath, []byte(b.String()), 0o644)
		fmt.Fprintf(os.Stderr, "gofmt: %v\n", ferr)
		os.Exit(1)
	}
	if err := os.WriteFile(outPath, src, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", outPath, err)
		os.Exit(1)
	}
	_ = sort.Strings // reserved; ordering is schema-driven, not sorted
}
