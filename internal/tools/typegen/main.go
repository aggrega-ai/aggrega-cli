// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Command typegen is the schema→Go field-declaration generator. It reads an
// entity kind's published JSON schema (from the embedded aggrega-schemas
// module) and emits the kind's struct field declarations — names, Go types,
// json/yaml tags, and godoc drawn from the schema descriptions — into a
// `<kind>_gen.go` file in pkg/types. Hand-written content (methods, custom
// marshalers, shared types like Maintainer, polymorphic types like
// SelectorTerm) lives in adjacent hand-written files; the generator never
// touches them.
//
// The annotation vocabulary is uniform: `x-aggrega-go-type` names the Go type
// of the node it annotates. On an object node it is that type (e.g.
// OrderedMap[SecretSlot], yaml.Node, *Interoperability); on an `items` node it
// is the element type and the mechanical array recursion lifts it to []T (e.g.
// Maintainer → []Maintainer, SelectorTerm → []SelectorTerm). There is no
// context-dependent meaning and no per-form special case: the generator emits
// the named type verbatim and never descends into an annotated node. Otherwise
// the mechanical scalar/bool/array mapping applies and a nested object becomes
// a named struct.
//
// Import resolution is mechanical and orthogonal to type emission: an emitted
// type carrying a package selector (e.g. yaml.Node) is matched against the
// knownImports table and the corresponding import is emitted. Extending that
// table for a new package is a one-line mechanical change, not a vocabulary
// decision.
//
// Determinism: fields emit in the schema's property declaration order
// (encoding/json preserves object key order via json.RawMessage + manual
// decode here), so regeneration is byte-stable.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"

	schemas "github.com/aggrega-ai/aggrega-schemas"
)

// kindSource binds an output filename to the Go type name and the embedded
// schema bytes the generator reads. Adding a kind is a one-line entry.
type kindSource struct {
	typeName string
	schema   string
}

var kindSources = map[string]kindSource{
	"provider_gen.go":       {"Provider", schemas.ProviderV1alpha1},
	"feature_gen.go":        {"Feature", schemas.FeatureV1alpha1},
	"manifest_gen.go":       {"Manifest", schemas.ManifestV1alpha1},
	"recipe_gen.go":         {"Recipe", schemas.RecipeV1alpha1},
	"featurerecipe_gen.go":  {"FeatureRecipe", schemas.FeatureRecipeV1alpha1},
	"schemalibrary_gen.go":  {"SchemaLibrary", schemas.SchemaLibraryV1alpha1},
	"mappinglibrary_gen.go": {"MappingLibrary", schemas.MappingLibraryV1alpha1},
	"aggregate_gen.go":      {"Aggregate", schemas.AggregateV1alpha1},
	"release_gen.go":        {"Release", schemas.ReleaseV1alpha1},
	"component_gen.go":      {"Component", schemas.ComponentV1alpha1},
}

// knownImports maps a package selector that may appear in an x-aggrega-go-type
// annotation to its import path. Mechanical and extensible — a new qualified
// type (e.g. a future time.Duration inside a generic) is a one-line add here,
// not a vocabulary-improvisation halt.
var knownImports = map[string]string{
	"yaml": "gopkg.in/yaml.v3",
}

// schemaNode is a decoded JSON Schema object node preserving the declared
// property order.
type schemaNode struct {
	Type        string
	Description string
	GoType      string // x-aggrega-go-type override (names a hand-written or generic type)
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

// goType maps a schema node to its Go type. x-aggrega-go-type names the Go
// type of the node it annotates, uniformly — on an object node it is that
// type; on an `items` node the mechanical array recursion lifts it to []T.
// Otherwise the mechanical scalar/array mapping applies, and a nested object
// becomes the named struct typeName.
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

// collectSelectors records the package selector of any qualified type
// (<lower>.<Upper>, e.g. yaml.Node) appearing in a Go type string, so the
// import block can be emitted. Same-package types (OrderedMap[...], []T, *T)
// carry no selector and are ignored.
func collectSelectors(goType string, used map[string]bool) {
	for i := 1; i < len(goType)-1; i++ {
		if goType[i] != '.' {
			continue
		}
		j := i - 1
		for j >= 0 && (isIdentByte(goType[j])) {
			j--
		}
		sel := goType[j+1 : i]
		next := goType[i+1]
		if sel != "" && sel[0] >= 'a' && sel[0] <= 'z' && next >= 'A' && next <= 'Z' {
			used[sel] = true
		}
	}
}

func isIdentByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

// emitStruct writes one struct declaration. typeName seeds nested named types
// (<parent><Field>). Returns the nested object structs it discovered, to emit
// after; records package selectors used by field types into `used`.
func emitStruct(b *strings.Builder, typeName, doc string, n schemaNode, used map[string]bool) []pending {
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
		collectSelectors(gt, used)
		if p.node.Type == "object" && p.node.GoType == "" && len(p.node.props) > 0 {
			nested = append(nested, pending{name: nestedName, doc: p.node.Description, node: p.node})
		}
		jsonTag, yamlTag := p.name, p.name
		if !required[p.name] {
			jsonTag += ",omitempty"
			yamlTag += ",omitempty"
			// OrderedMap is a struct value: encoding/json's omitempty is a
			// no-op on it, so omitzero (Go 1.24+, via IsZero) is the
			// load-bearing option that omits an empty map rather than emitting
			// `{}`. yaml.v3 honours its IsZeroer through plain omitempty.
			if strings.HasPrefix(gt, "OrderedMap[") {
				jsonTag += ",omitzero"
			}
		}
		if p.node.Description != "" {
			fmt.Fprintf(b, "\t// %s %s\n", field, oneLine(p.node.Description))
		}
		fmt.Fprintf(b, "\t%s %s `json:%q yaml:%q`\n", field, gt, jsonTag, yamlTag)
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

// generate produces the formatted Go source for one kind: its struct field
// declarations plus the import block its field types require. It is the
// callable core shared by main and the regen-drift test.
func generate(typeName, schemaJSON string) ([]byte, error) {
	root, err := decodeNode(json.RawMessage(schemaJSON))
	if err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}

	var body strings.Builder
	used := map[string]bool{}
	// Top-level entity struct, then its nested named structs breadth-first in
	// declared order.
	queue := []pending{{name: typeName, doc: root.Description, node: root}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		nested := emitStruct(&body, cur.name, cur.doc, cur.node, used)
		queue = append(queue, nested...)
	}

	var head strings.Builder
	head.WriteString("// SPDX-License-Identifier: Apache-2.0\n// Copyright 2026 Cognitio S.r.l.\n\n")
	head.WriteString("// Code generated by internal/tools/typegen from\n")
	fmt.Fprintf(&head, "// aggrega-schemas/json/%s-v1alpha1.schema.json. DO NOT EDIT.\n", strings.ToLower(typeName))
	head.WriteString("// Hand-written content (shared types, methods, marshalers) lives in\n")
	fmt.Fprintf(&head, "// %s.go and common.go; regenerate with `go generate ./pkg/types/...`.\n\n", strings.ToLower(typeName))
	head.WriteString("package types\n\n")

	var imports []string
	for sel := range used {
		if path, ok := knownImports[sel]; ok {
			imports = append(imports, path)
		}
	}
	sort.Strings(imports)
	switch {
	case len(imports) == 1:
		fmt.Fprintf(&head, "import %q\n\n", imports[0])
	case len(imports) > 1:
		head.WriteString("import (\n")
		for _, imp := range imports {
			fmt.Fprintf(&head, "\t%q\n", imp)
		}
		head.WriteString(")\n\n")
	}

	src, ferr := format.Source([]byte(head.String() + body.String()))
	if ferr != nil {
		return []byte(head.String() + body.String()), fmt.Errorf("gofmt: %w", ferr)
	}
	return src, nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: typegen <output-file>")
		os.Exit(2)
	}
	outPath := os.Args[1]
	src, ok := kindSources[filepath.Base(outPath)]
	if !ok {
		fmt.Fprintf(os.Stderr, "typegen: no kind registered for %q\n", filepath.Base(outPath))
		os.Exit(2)
	}

	out, err := generate(src.typeName, src.schema)
	if err != nil {
		// On a gofmt failure `out` holds the unformatted source for debugging.
		_ = os.WriteFile(outPath, out, 0o644)
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outPath, out, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", outPath, err)
		os.Exit(1)
	}
}
