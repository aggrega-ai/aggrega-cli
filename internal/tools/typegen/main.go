// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Command typegen is the schema→Go field-declaration generator. It reads a
// published JSON schema (from the embedded aggrega-schemas module) and emits
// Go type declarations — names, Go types, json/yaml tags, and godoc drawn from
// the schema descriptions — into a generated `*_gen.go` file. Hand-written
// content (methods, custom marshalers, shared types like Maintainer,
// polymorphic types like SelectorTerm) lives in adjacent hand-written files;
// the generator never touches them.
//
// It serves two emission shapes from one shared machinery:
//
//   - The single-type shape (the entity kinds in pkg/types): one top-level
//     entity struct whose nested objects become named structs breadth-first.
//   - The multi-type shape (a schema whose types live under `$defs`): one Go
//     type per `$defs` entry, in declared order, in a single file. The
//     top-level `oneOf` wire-discriminator is not a Go type and is ignored
//     (absence of handling — the defs walk reads `$defs` and never the root's
//     `oneOf`/`type`).
//
// The annotation vocabulary is uniform: `x-aggrega-go-type` names the Go type
// of the node it annotates. On an object node it is that type (e.g.
// OrderedMap[SecretSlot], yaml.Node, *Interoperability); on an `items` node it
// is the element type and the mechanical array recursion lifts it to []T (e.g.
// Maintainer → []Maintainer, SelectorTerm → []SelectorTerm). There is no
// context-dependent meaning and no per-form special case: the generator emits
// the named type verbatim and never descends into an annotated node. A
// `$ref` of the form `#/$defs/<Name>` resolves to the Go type `<Name>`
// (the def names are already PascalCase), and an `x-aggrega-go-type`
// annotation on the same node still wins over a `$ref`. Otherwise the
// mechanical scalar/bool/array mapping applies and a nested object becomes a
// named struct.
//
// A target may carry a type vocabulary — a map that rewrites an
// `x-aggrega-go-type` value before emission. A schema records host type
// spellings; a target whose package cannot reference those spellings (a
// cross-package opaque type, a host-only container) maps them to a neutral
// spelling (e.g. an opaque host type to json.RawMessage, an order-preserving
// host container to a plain map). Absence of an entry passes the annotation
// value through verbatim.
//
// Import resolution is mechanical and orthogonal to type emission: an emitted
// type carrying a package selector (e.g. yaml.Node) is matched against the
// knownImports table and the corresponding import is emitted. Extending that
// table for a new package is a one-line mechanical change, not a vocabulary
// decision.
//
// Determinism: fields emit in the schema's property declaration order, and
// multi-type files emit their types in `$defs` declaration order
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
	"wasmplugin_gen.go":     {"WasmPlugin", schemas.WasmPluginV1alpha1},
}

// codecTarget binds an output filename to a multi-type codec emission: the
// embedded `$defs`-shaped schema, the schema file basename for the generated
// header, the destination package name, and the per-target type vocabulary
// that rewrites x-aggrega-go-type values. Each `$defs` entry becomes one Go
// type plus a reflect-free streaming JSON codec (Marshal/UnmarshalJSON).
type codecTarget struct {
	schema     string
	schemaFile string
	pkg        string
	vocabulary map[string]string
}

// frpHostNeutralVocabulary maps the FRP schema's host type spellings to a
// neutral, cycle-free vocabulary: an opaque cross-package host type becomes
// json.RawMessage (verbatim passthrough preserves its bytes), and the
// order-preserving host container becomes a plain map (key order is the
// emitting host's concern; a consumer that selects by content reads a map).
// With this vocabulary the generated codec references only builtins,
// encoding/json, and its own def-local types — no cross-package import, so it
// is free of the import cycle a renderctx-spelled codec would carry.
var frpHostNeutralVocabulary = map[string]string{
	"renderctx.RenderContext": "json.RawMessage",
	"renderctx.Diagnostic":    "json.RawMessage",
	"OrderedMap[Unit]":        "map[string]Unit",
	"OrderedMap[any]":         "map[string]json.RawMessage",
	"DocumentScope":           "string",
}

var codecTargets = map[string]codecTarget{
	"frp_gen.go": {
		schema:     schemas.WasmFrpStepV1alpha1,
		schemaFile: "wasm-frp-step-v1alpha1.schema.json",
		pkg:        "frpcodec",
		vocabulary: frpHostNeutralVocabulary,
	},
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
	Ref         string // $ref target (e.g. #/$defs/Unit); resolves to a Go type name
	Required    []string
	props       []namedNode // in declared order
	Items       *schemaNode
	defs        []namedNode // $defs entries, in declared order (multi-type schemas only)
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
		Ref         string          `json:"$ref"`
		Required    []string        `json:"required"`
		Items       json.RawMessage `json:"items"`
		Properties  json.RawMessage `json:"properties"`
		Defs        json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &flat); err != nil {
		return n, err
	}
	n.Type, n.Description, n.GoType, n.Ref, n.Required = flat.Type, flat.Description, flat.GoType, flat.Ref, flat.Required
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
	if len(flat.Defs) > 0 {
		defs, err := decodeOrderedProps(flat.Defs)
		if err != nil {
			return n, err
		}
		n.defs = defs
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

// pascal converts a schema property key (camelCase or snake_case) to a Go
// exported field name, applying the Go initialism convention. Segment
// boundaries are an interior uppercase letter (camelCase) or an underscore
// (snake_case); the underscore is consumed, not emitted.
func pascal(s string) string {
	var segs []string
	cur := strings.Builder{}
	for i, r := range s {
		if r == '_' {
			segs = append(segs, cur.String())
			cur.Reset()
			continue
		}
		if i > 0 && r >= 'A' && r <= 'Z' {
			segs = append(segs, cur.String())
			cur.Reset()
		}
		cur.WriteRune(r)
	}
	segs = append(segs, cur.String())
	out := strings.Builder{}
	for _, seg := range segs {
		if seg == "" {
			continue
		}
		low := strings.ToLower(seg)
		if init, ok := initialisms[low]; ok {
			out.WriteString(init)
		} else {
			out.WriteString(strings.ToUpper(seg[:1]) + seg[1:])
		}
	}
	return out.String()
}

// goType maps a schema node to its Go type. Resolution order:
//
//  1. x-aggrega-go-type names the Go type of the node it annotates, uniformly
//     — on an object node it is that type; on an `items` node the mechanical
//     array recursion lifts it to []T. A non-nil vocabulary rewrites the
//     annotation value (host spelling → target spelling); absence passes it
//     through verbatim.
//  2. A `$ref` of the form #/$defs/<Name> resolves to the Go type <Name>
//     (the def names are already PascalCase; the resolution is a verbatim
//     suffix strip). An x-aggrega-go-type annotation on the same node wins
//     over a $ref.
//  3. Otherwise the mechanical scalar/array mapping applies, and a nested
//     object becomes the named struct typeName.
//
// A nil vocabulary (the single-type entity path) leaves every annotation
// value untouched.
func goType(n schemaNode, typeName string, vocab map[string]string) string {
	if n.GoType != "" {
		return rewriteType(n.GoType, vocab)
	}
	if n.Ref != "" {
		return refTypeName(n.Ref)
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
			return "[]" + goType(*n.Items, "", vocab)
		}
		return "[]any"
	case "object":
		return typeName // a named nested struct
	default:
		return "any"
	}
}

// rewriteType applies a target's type vocabulary to an x-aggrega-go-type
// value. A nil or entry-less vocabulary returns the value verbatim.
func rewriteType(goType string, vocab map[string]string) string {
	if vocab != nil {
		if mapped, ok := vocab[goType]; ok {
			return mapped
		}
	}
	return goType
}

// refTypeName resolves a #/$defs/<Name> $ref to the Go type name <Name>. The
// def names are authored in PascalCase, so the resolution is a verbatim
// suffix strip with no name munging. A $ref the generator does not recognize
// (not a #/$defs/ form) passes through verbatim so the resulting source fails
// to compile loudly rather than silently mistyping a field.
func refTypeName(ref string) string {
	const prefix = "#/$defs/"
	if strings.HasPrefix(ref, prefix) {
		return ref[len(prefix):]
	}
	return ref
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
// after; records package selectors used by field types into `used`. A non-nil
// vocab rewrites x-aggrega-go-type field values per the target's vocabulary.
func emitStruct(b *strings.Builder, typeName, doc string, n schemaNode, used map[string]bool, vocab map[string]string) []pending {
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
		gt := goType(p.node, nestedName, vocab)
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

// codecField is one struct field resolved for codec emission: the Go field
// name, the JSON wire key, the resolved Go type, and whether it is required
// (required fields are never omitted on marshal).
type codecField struct {
	field    string
	key      string
	goType   string
	required bool
}

// resolveCodecFields resolves a def node's properties into codec field
// descriptors, in declared order, applying the target vocabulary to
// x-aggrega-go-type values and resolving $refs to def type names. It mirrors
// emitStruct's field-type resolution so the codec types match the emitted
// struct fields exactly.
func resolveCodecFields(n schemaNode, vocab map[string]string) []codecField {
	required := map[string]bool{}
	for _, r := range n.Required {
		required[r] = true
	}
	var fields []codecField
	for _, p := range n.props {
		field := pascal(p.name)
		fields = append(fields, codecField{
			field:    field,
			key:      p.name,
			goType:   goType(p.node, field, vocab),
			required: required[p.name],
		})
	}
	return fields
}

// emitCodec writes a reflect-free streaming JSON codec — MarshalJSON and
// UnmarshalJSON — for one def struct type. Composite fields (named structs,
// slices of named structs, maps) are walked by generated code so no stdlib
// reflective struct/map-walk remains; scalar leaf fields (string, int,
// []byte, json.RawMessage) use a per-field decode/marshal that is
// non-reflective for those leaf types. The generated code references no
// `reflect` symbol — the reflect-free guarantee is by construction.
func emitCodec(b *strings.Builder, typeName string, n schemaNode, vocab map[string]string) {
	fields := resolveCodecFields(n, vocab)
	emitMarshal(b, typeName, fields)
	emitUnmarshal(b, typeName, fields)
}

// emitMarshal writes T.MarshalJSON: it builds the JSON object manually in
// field-declaration order (the determinism contract), honouring omitempty for
// non-required fields by Go type so the output matches encoding/json on the
// equivalent struct tags byte-for-byte.
func emitMarshal(b *strings.Builder, typeName string, fields []codecField) {
	fmt.Fprintf(b, "// MarshalJSON encodes %s in field-declaration order, omitting\n", typeName)
	fmt.Fprintf(b, "// empty non-required fields, reflect-free.\n")
	fmt.Fprintf(b, "func (t %s) MarshalJSON() ([]byte, error) {\n", typeName)
	b.WriteString("\tvar buf bytes.Buffer\n")
	b.WriteString("\tbuf.WriteByte('{')\n")
	b.WriteString("\tfirst := true\n")
	for _, f := range fields {
		guard := omitGuard("t."+f.field, f.goType)
		if !f.required && guard != "" {
			fmt.Fprintf(b, "\tif %s {\n", guard)
			emitMarshalField(b, f, "\t\t")
			b.WriteString("\t}\n")
		} else {
			emitMarshalField(b, f, "\t")
		}
	}
	b.WriteString("\tbuf.WriteByte('}')\n")
	b.WriteString("\treturn buf.Bytes(), nil\n")
	b.WriteString("}\n\n")
}

// emitMarshalField writes the separator + key + json.Marshal of one field
// value into buf. The value is a concrete typed field, so json.Marshal is the
// non-reflective-by-construction leaf encode (and for a composite field the
// field type's own generated MarshalJSON runs).
func emitMarshalField(b *strings.Builder, f codecField, indent string) {
	fmt.Fprintf(b, "%sif !first {\n%s\tbuf.WriteByte(',')\n%s}\n", indent, indent, indent)
	b.WriteString(indent + "first = false\n")
	fmt.Fprintf(b, "%sbuf.WriteString(%q)\n", indent, `"`+f.key+`":`)
	fmt.Fprintf(b, "%s%s, err := json.Marshal(t.%s)\n", indent, f.field+"Bytes", f.field)
	fmt.Fprintf(b, "%sif err != nil {\n%s\treturn nil, err\n%s}\n", indent, indent, indent)
	fmt.Fprintf(b, "%sbuf.Write(%s)\n", indent, f.field+"Bytes")
}

// omitGuard returns a boolean expression that is true when a non-required
// field of the given Go type must be emitted (i.e. it is non-empty), matching
// encoding/json's omitempty semantics for that type. An empty string means the
// type carries no omitempty notion (the field is always emitted).
func omitGuard(expr, goType string) string {
	switch {
	case goType == "string":
		return expr + ` != ""`
	case goType == "int" || goType == "int64" || goType == "float64":
		return expr + " != 0"
	case goType == "bool":
		return expr + " != false"
	case goType == "[]byte" || goType == "json.RawMessage":
		return "len(" + expr + ") != 0"
	case goType == "any" || goType == "interface{}":
		// encoding/json's omitempty on an interface field omits when the
		// interface is nil (the underlying scalar emptiness does not matter).
		return expr + " != nil"
	case strings.HasPrefix(goType, "[]"):
		return "len(" + expr + ") != 0"
	case strings.HasPrefix(goType, "map["):
		return "len(" + expr + ") != 0"
	case strings.HasPrefix(goType, "*"):
		return expr + " != nil"
	default:
		return ""
	}
}

// emitUnmarshal writes T.UnmarshalJSON: a streaming json.Decoder object walk
// that dispatches each known key to a per-field decode and descends fully into
// composite fields. Unknown keys decode into a discarded json.RawMessage
// (forward-compatible). No reflect symbol appears.
func emitUnmarshal(b *strings.Builder, typeName string, fields []codecField) {
	fmt.Fprintf(b, "// UnmarshalJSON decodes %s with a streaming token walk, descending\n", typeName)
	fmt.Fprintf(b, "// into composite fields, reflect-free. Unknown keys are skipped.\n")
	fmt.Fprintf(b, "func (t *%s) UnmarshalJSON(data []byte) error {\n", typeName)
	b.WriteString("\tdec := json.NewDecoder(bytes.NewReader(data))\n")
	b.WriteString("\tif err := expectDelim(dec, '{'); err != nil {\n\t\treturn err\n\t}\n")
	b.WriteString("\tfor dec.More() {\n")
	b.WriteString("\t\tkey, err := readKey(dec)\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
	b.WriteString("\t\tswitch key {\n")
	for _, f := range fields {
		fmt.Fprintf(b, "\t\tcase %q:\n", f.key)
		emitUnmarshalField(b, f, "\t\t\t")
	}
	b.WriteString("\t\tdefault:\n")
	b.WriteString("\t\t\tvar skip json.RawMessage\n")
	b.WriteString("\t\t\tif err := dec.Decode(&skip); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t}\n")
	b.WriteString("\tif err := expectDelim(dec, '}'); err != nil {\n\t\treturn err\n\t}\n")
	b.WriteString("\treturn nil\n")
	b.WriteString("}\n\n")
}

// emitUnmarshalField writes the per-field decode for one key. Maps and slices
// are token-walked element-wise so each composite value routes through its own
// generated decode; scalar and named-struct fields decode via dec.Decode into
// the concrete field (the named struct's own generated UnmarshalJSON runs).
func emitUnmarshalField(b *strings.Builder, f codecField, indent string) {
	switch {
	case strings.HasPrefix(f.goType, "map["):
		valType := f.goType[strings.IndexByte(f.goType, ']')+1:]
		fmt.Fprintf(b, "%st.%s = make(%s)\n", indent, f.field, f.goType)
		fmt.Fprintf(b, "%sif err := expectDelim(dec, '{'); err != nil {\n%s\treturn err\n%s}\n", indent, indent, indent)
		fmt.Fprintf(b, "%sfor dec.More() {\n", indent)
		fmt.Fprintf(b, "%s\tmk, err := readKey(dec)\n%s\tif err != nil {\n%s\t\treturn err\n%s\t}\n", indent, indent, indent, indent)
		fmt.Fprintf(b, "%s\tvar mv %s\n", indent, valType)
		fmt.Fprintf(b, "%s\tif err := dec.Decode(&mv); err != nil {\n%s\t\treturn err\n%s\t}\n", indent, indent, indent)
		fmt.Fprintf(b, "%s\tt.%s[mk] = mv\n", indent, f.field)
		fmt.Fprintf(b, "%s}\n", indent)
		fmt.Fprintf(b, "%sif err := expectDelim(dec, '}'); err != nil {\n%s\treturn err\n%s}\n", indent, indent, indent)
	case strings.HasPrefix(f.goType, "[]") && f.goType != "[]byte":
		elemType := f.goType[2:]
		fmt.Fprintf(b, "%st.%s = nil\n", indent, f.field)
		fmt.Fprintf(b, "%sif err := expectDelim(dec, '['); err != nil {\n%s\treturn err\n%s}\n", indent, indent, indent)
		fmt.Fprintf(b, "%sfor dec.More() {\n", indent)
		fmt.Fprintf(b, "%s\tvar elem %s\n", indent, elemType)
		fmt.Fprintf(b, "%s\tif err := dec.Decode(&elem); err != nil {\n%s\t\treturn err\n%s\t}\n", indent, indent, indent)
		fmt.Fprintf(b, "%s\tt.%s = append(t.%s, elem)\n", indent, f.field, f.field)
		fmt.Fprintf(b, "%s}\n", indent)
		fmt.Fprintf(b, "%sif err := expectDelim(dec, ']'); err != nil {\n%s\treturn err\n%s}\n", indent, indent, indent)
	default:
		fmt.Fprintf(b, "%sif err := dec.Decode(&t.%s); err != nil {\n%s\treturn err\n%s}\n", indent, f.field, indent, indent)
	}
}

// emitCodecHelpers writes the shared streaming-decode helpers the generated
// UnmarshalJSON methods call: expectDelim consumes an expected JSON delimiter,
// readKey reads an object key token. Both use json.Decoder.Token — no reflect.
func emitCodecHelpers(b *strings.Builder) {
	b.WriteString("// expectDelim reads the next token and asserts it is the given delimiter.\n")
	b.WriteString("func expectDelim(dec *json.Decoder, want json.Delim) error {\n")
	b.WriteString("\ttok, err := dec.Token()\n")
	b.WriteString("\tif err != nil {\n\t\treturn err\n\t}\n")
	b.WriteString("\td, ok := tok.(json.Delim)\n")
	b.WriteString("\tif !ok || d != want {\n")
	b.WriteString("\t\treturn fmt.Errorf(\"frpcodec: expected %q, got %v\", want, tok)\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn nil\n")
	b.WriteString("}\n\n")

	b.WriteString("// readKey reads the next token and asserts it is a string object key.\n")
	b.WriteString("func readKey(dec *json.Decoder) (string, error) {\n")
	b.WriteString("\ttok, err := dec.Token()\n")
	b.WriteString("\tif err != nil {\n\t\treturn \"\", err\n\t}\n")
	b.WriteString("\tkey, ok := tok.(string)\n")
	b.WriteString("\tif !ok {\n")
	b.WriteString("\t\treturn \"\", fmt.Errorf(\"frpcodec: expected string key, got %v\", tok)\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn key, nil\n")
	b.WriteString("}\n\n")
}

// generate produces the formatted Go source for one entity kind: its struct
// field declarations plus the import block its field types require. It is the
// single-type emission path (one top-level struct + nested named structs),
// shared by main and the regen-drift test. The single-type path uses no
// vocabulary — entity x-aggrega-go-type values emit verbatim.
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
	emitQueue(&body, queue, used, nil)

	head := genHeader("package types",
		fmt.Sprintf("// aggrega-schemas/json/%s-v1alpha1.schema.json. DO NOT EDIT.\n", strings.ToLower(typeName)),
		fmt.Sprintf("// Hand-written content (shared types, methods, marshalers) lives in\n// %s.go and common.go; regenerate with `go generate ./pkg/types/...`.\n", strings.ToLower(typeName)),
		used)

	return formatSource(head, body.String())
}

// generateDefs produces the formatted Go source for a multi-type codec
// package: one Go type per `$defs` entry (in declared order) plus, per type, a
// reflect-free streaming JSON codec. The top-level `oneOf` wire-discriminator
// is not a Go type and is ignored — the walk reads `$defs` only and never the
// root's oneOf/type. The target's vocabulary rewrites x-aggrega-go-type values.
func generateDefs(t codecTarget) ([]byte, error) {
	root, err := decodeNode(json.RawMessage(t.schema))
	if err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}
	if len(root.defs) == 0 {
		return nil, fmt.Errorf("schema %s has no $defs to emit", t.schemaFile)
	}

	var body strings.Builder
	used := map[string]bool{}
	// One named type per $def, in schema declaration order. Each def is an
	// object; emitStruct queues any anonymous nested objects breadth-first,
	// though the $defs-shaped schema names every composite via a $ref so the
	// nested-object queue stays empty in practice.
	var queue []pending
	for _, d := range root.defs {
		queue = append(queue, pending{name: d.name, doc: d.node.Description, node: d.node})
	}
	emitQueue(&body, queue, used, t.vocabulary)

	// The reflect-free streaming codec for each emitted def type, in the same
	// declared order. Codec emission descends into each field's composite
	// shape (map/struct/slice) so no stdlib reflective struct-walk remains.
	for _, d := range root.defs {
		emitCodec(&body, d.name, d.node, t.vocabulary)
	}
	emitCodecHelpers(&body)

	// The codec spine always imports bytes (buffer/reader), encoding/json
	// (Decoder/Marshal/RawMessage), and fmt (decode error wrapping).
	head := genHeaderImports("package "+t.pkg,
		fmt.Sprintf("// aggrega-schemas/json/%s. DO NOT EDIT.\n", t.schemaFile),
		"// Regenerate with `go generate ./...` from the package directory.\n",
		[]string{"bytes", "encoding/json", "fmt"})

	return formatSource(head, body.String())
}

// emitQueue drains a breadth-first struct-emission queue into body, emitting
// each queued struct and the nested anonymous structs it discovers, in order.
func emitQueue(body *strings.Builder, queue []pending, used map[string]bool, vocab map[string]string) {
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		nested := emitStruct(body, cur.name, cur.doc, cur.node, used, vocab)
		queue = append(queue, nested...)
	}
}

// genHeader builds the SPDX + code-generated header, the package clause, and
// the import block the used selectors require (mapped via knownImports).
func genHeader(packageClause, schemaLine, regenLines string, used map[string]bool) string {
	var imports []string
	for sel := range used {
		if path, ok := knownImports[sel]; ok {
			imports = append(imports, path)
		}
	}
	return genHeaderImports(packageClause, schemaLine, regenLines, imports)
}

// genHeaderImports builds the SPDX + code-generated header, the package
// clause, and an import block from an explicit import-path list (sorted,
// deduplicated by sort). Used by the codec path, whose imports are the fixed
// codec-spine packages rather than selector-derived.
func genHeaderImports(packageClause, schemaLine, regenLines string, imports []string) string {
	var head strings.Builder
	head.WriteString("// SPDX-License-Identifier: Apache-2.0\n// Copyright 2026 Cognitio S.r.l.\n\n")
	head.WriteString("// Code generated by internal/tools/typegen from\n")
	head.WriteString(schemaLine)
	head.WriteString(regenLines)
	head.WriteString("\n")
	head.WriteString(packageClause + "\n\n")

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
	return head.String()
}

// formatSource gofmts the concatenated header + body. On a gofmt failure it
// returns the unformatted source for debugging alongside the error.
func formatSource(head, body string) ([]byte, error) {
	src, ferr := format.Source([]byte(head + body))
	if ferr != nil {
		return []byte(head + body), fmt.Errorf("gofmt: %w", ferr)
	}
	return src, nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: typegen <output-file>")
		os.Exit(2)
	}
	outPath := os.Args[1]
	base := filepath.Base(outPath)

	out, err := generateFor(base)
	if err != nil {
		// On a gofmt failure `out` holds the unformatted source for debugging.
		if len(out) > 0 {
			_ = os.WriteFile(outPath, out, 0o644)
		}
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outPath, out, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", outPath, err)
		os.Exit(1)
	}
}

// generateFor dispatches an output basename to its emission path: a registered
// entity kind (single-type) or a registered codec target (multi-type). It is
// the shared dispatch main and the regen-drift test both use.
func generateFor(base string) ([]byte, error) {
	if src, ok := kindSources[base]; ok {
		return generate(src.typeName, src.schema)
	}
	if t, ok := codecTargets[base]; ok {
		return generateDefs(t)
	}
	return nil, fmt.Errorf("typegen: no kind or codec target registered for %q", base)
}
