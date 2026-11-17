// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package exportgen writes synthetic providers schema exports, in the format
// terraform providers schema -json writes, of any size, for tests that read
// exports at the scale real providers reach. Every name and description in an
// export is composed here; none is a vendor's text.
package exportgen

import (
	"bytes"
	"fmt"
	"strings"
)

// Spec is the shape of an export: the providers it holds, by registry address,
// the resource types each carries, the attributes of each type (the first named
// name, required) and the depth of the blocks nested in each type, one list
// block within the last.
type Spec struct {
	Providers                                       []string
	TypesPerProvider, AttributesPerType, BlockDepth int
}

// Generate returns a providers schema -json export of the shape s, byte-identical
// for one Spec. A provider's types are named after its type segment without a
// -beta suffix (google and google-beta both write google_resource_0000 ...), so
// a provider and its beta sibling carry the same names, as the real ones do.
func Generate(s Spec) []byte {
	var b bytes.Buffer
	b.WriteString(`{"format_version":"1.0","provider_schemas":{`)
	for i, address := range s.Providers {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `%q:{"provider":{"block":{}},"resource_schemas":{`, address)
		prefix := typePrefix(address)
		for n := 0; n < s.TypesPerProvider; n++ {
			if n > 0 {
				b.WriteByte(',')
			}
			writeType(&b, s, fmt.Sprintf("%s_resource_%04d", prefix, n), n)
		}
		b.WriteString(`}}`)
	}
	b.WriteString(`}}`)
	return b.Bytes()
}

// typePrefix is the provider type of address without its -beta suffix, as the
// prefix of the names of its types.
func typePrefix(address string) string {
	segment := address[strings.LastIndex(address, "/")+1:]
	return strings.ReplaceAll(strings.TrimSuffix(segment, "-beta"), "-", "_")
}

// writeType writes the resource type name, the n-th of its provider.
func writeType(b *bytes.Buffer, s Spec, name string, n int) {
	fmt.Fprintf(b, `%q:{"version":%d,"block":`, name, n%3)
	writeBlock(b, s.AttributesPerType, s.BlockDepth, 1, fmt.Sprintf("the resource type %s", name), n)
	b.WriteByte('}')
}

// writeBlock writes a block of attributes attributes holding depth levels of
// nested blocks, level being its own.
func writeBlock(b *bytes.Buffer, attributes, depth, level int, what string, n int) {
	b.WriteString(`{"attributes":{`)
	first := true
	for a := 1; a < attributes; a++ {
		if !first {
			b.WriteByte(',')
		}
		first = false
		writeAttribute(b, fmt.Sprintf("attr_%04d", a), a, what, n)
	}
	if attributes > 0 {
		if !first {
			b.WriteByte(',')
		}
		fmt.Fprintf(b, `"name":{"description":"The name of %s.","description_kind":"plain","required":true,"type":"string"}`, what)
	}
	b.WriteByte('}')
	if depth > 0 {
		fmt.Fprintf(b, `,"block_types":{"nested_%d":{"block":`, level)
		writeBlock(b, min(attributes, 2), depth-1, level+1, fmt.Sprintf("level %d of %s", level, what), n)
		b.WriteString(`,"max_items":4,"min_items":1,"nesting_mode":"list"}}`)
	}
	fmt.Fprintf(b, `,"description":"Synthetic %s, written to size an export.","description_kind":"plain"}`, what)
}

// attributeShapes are the members an attribute takes, by its number modulo the
// count, written in key order.
var attributeShapes = []string{
	`"optional":true,"type":"string"`,
	`"computed":true,"type":"string"`,
	`"computed":true,"optional":true,"type":"number"`,
	`"optional":true,"sensitive":true,"type":"string"`,
	`"optional":true,"type":["list","string"]`,
	`"optional":true,"type":["map","string"]`,
	`"optional":true,"type":"bool"`,
	`"deprecated":true,"optional":true,"type":"string"`,
}

// writeAttribute writes the attribute name, the a-th of its block, whose shape
// and description follow from a and n alone.
func writeAttribute(b *bytes.Buffer, name string, a int, what string, n int) {
	shape := attributeShapes[(a+n)%len(attributeShapes)]
	if a%11 == 0 {
		shape = `"required":true,"type":"string"`
	}
	fmt.Fprintf(b, `%q:{`, name)
	// The description is the first member of the key order after any
	// "computed" and "deprecated", so shapes that start with those are split.
	head, tail := splitShape(shape)
	if head != "" {
		b.WriteString(head + ",")
	}
	fmt.Fprintf(b, `"description":"Attribute %d of %s: %s","description_kind":"plain",%s}`, a, what, attributeText, tail)
}

// attributeText is the sentence closing the description of every attribute.
const attributeText = "a synthetic setting that controls an aspect of the object it belongs to and is written " +
	"only to give the export the size of a real provider's schema, which describes each setting at length."

// splitShape splits shape at the members that sort before "description".
func splitShape(shape string) (head, tail string) {
	members := strings.Split(shape, ",")
	i := 0
	for i < len(members) && (strings.HasPrefix(members[i], `"computed"`) || strings.HasPrefix(members[i], `"deprecated"`)) {
		i++
	}
	return strings.Join(members[:i], ","), strings.Join(members[i:], ",")
}
