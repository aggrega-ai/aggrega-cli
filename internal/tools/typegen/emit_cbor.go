// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"sort"
	"strings"
)

// This file is the Go CBOR emission fork, parallel to the JSON codec fork in
// main.go. It emits a reflect-free, dispatch-free canonical-CBOR codec
// (RFC 8949 §4.2.1 Core Deterministic Encoding; floats per CBOR CDE §4.2) for
// each def struct type: EncodeCBOR / DecodeCBOR plus the value-level
// appendCBOR / decodeCBOR they drive. It reuses the shared front-end
// (resolveCodecFields); only the emission half is new.
//
// The canonical rules, one rule-set across Go/Rust/TS:
//
//   - a struct is a definite-length CBOR map; its fields emit in canonical key
//     order fixed HERE at generation time (sort by encoded text-key bytes), so
//     no runtime sort is needed and the order matches a reference encoder that
//     sorts at runtime;
//   - a plain string-keyed map is a definite-length CBOR map whose keys sort at
//     runtime by their encoded text-key bytes (length-first, then bytewise) —
//     not by Go string comparison;
//   - a slice is a definite-length CBOR array in element order;
//   - integers use the shortest head; bools are fixed single bytes; text and
//     byte strings use the shortest length head;
//   - a float64 uses the shortest of float16/32/64 that round-trips to the
//     exact value, with NaN canonicalized to 0xf97e00, ±Inf to 0xf97c00 /
//     0xf9fc00, and ±0 preserved by sign — no numeric reduction (an
//     integer-valued float stays a float);
//   - an opaque json.RawMessage / any value carries as a CBOR byte-string of
//     its verbatim bytes (the json.RawMessage analog): the compiler never
//     interprets opaque content, and a float inside it rides untouched.
//
// The generated methods are named EncodeCBOR/DecodeCBOR rather than the
// cbor.Marshaler/Unmarshaler interface names (MarshalCBOR/UnmarshalCBOR) on
// purpose: a reference CBOR encoder used as a test oracle must reflect-encode
// the same value INDEPENDENTLY, which it cannot do if it dispatches to a codec
// method the type carries. Non-interface names keep the oracle independent.

// emitCBORCodec writes the canonical-CBOR codec — EncodeCBOR/appendCBOR and
// DecodeCBOR/decodeCBOR — for one def struct type. The encode side emits fields
// in canonical key order fixed at generation time; the decode side switches on
// the wire key, so it is order-independent.
func emitCBORCodec(b *strings.Builder, typeName string, n schemaNode, vocab map[string]string, defTypes map[string]bool) {
	fields := resolveCodecFields(n, vocab)
	sorted := append([]codecField(nil), fields...)
	sort.SliceStable(sorted, func(i, j int) bool { return cborKeyLess(sorted[i].key, sorted[j].key) })
	emitCBOREncode(b, typeName, sorted, defTypes)
	emitCBORDecode(b, typeName, fields, defTypes)
}

// cborKeyLess orders two text-string map keys by the bytewise order of their
// CBOR encodings (RFC 8949 §4.2.1). The length head dominates: a shorter
// encoded length sorts first; equal lengths compare by UTF-8 content. This is
// the generation-time twin of the runtime cborLessKey helper, so a struct's
// gen-time field order matches a plain map's runtime key order and a reference
// encoder's runtime struct-field sort.
func cborKeyLess(a, b string) bool {
	if len(a) != len(b) {
		ha := cborStrHead(len(a))
		hb := cborStrHead(len(b))
		for i := 0; i < len(ha) && i < len(hb); i++ {
			if ha[i] != hb[i] {
				return ha[i] < hb[i]
			}
		}
		return len(ha) < len(hb)
	}
	return a < b
}

// cborStrHead returns the CBOR major-type-3 (text string) length head for a
// string of the given byte length, so the key comparator can order by encoded
// head bytes.
func cborStrHead(n int) []byte {
	switch {
	case n < 24:
		return []byte{0x60 | byte(n)}
	case n < 0x100:
		return []byte{0x78, byte(n)}
	case n < 0x10000:
		return []byte{0x79, byte(n >> 8), byte(n)}
	default:
		return []byte{0x7a, byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
	}
}

// emitCBOREncode writes EncodeCBOR (the []byte-returning entry point) and the
// appendCBOR worker that builds the definite-length CBOR map: it counts the
// present fields for the map head, then appends each present field's key and
// encoded value in canonical order.
func emitCBOREncode(b *strings.Builder, typeName string, sorted []codecField, defTypes map[string]bool) {
	fmt.Fprintf(b, "// EncodeCBOR encodes %s as canonical CBOR (RFC 8949 §4.2.1; floats per\n", typeName)
	fmt.Fprintf(b, "// CBOR CDE §4.2), dispatch-free.\n")
	fmt.Fprintf(b, "func (t %s) EncodeCBOR() ([]byte, error) {\n\treturn t.appendCBOR(nil)\n}\n\n", typeName)

	fmt.Fprintf(b, "// appendCBOR appends %s's canonical CBOR encoding to dst. Fields emit in\n", typeName)
	fmt.Fprintf(b, "// canonical key order (fixed at generation time), omitting empty\n")
	fmt.Fprintf(b, "// non-required fields so the map head's pair count matches.\n")
	fmt.Fprintf(b, "func (t %s) appendCBOR(dst []byte) ([]byte, error) {\n", typeName)

	nRequired := 0
	for _, f := range sorted {
		if f.required || omitGuard("t."+f.field, f.goType) == "" {
			nRequired++
		}
	}
	fmt.Fprintf(b, "\tn := %d\n", nRequired)
	for _, f := range sorted {
		guard := omitGuard("t."+f.field, f.goType)
		if !f.required && guard != "" {
			fmt.Fprintf(b, "\tif %s {\n\t\tn++\n\t}\n", guard)
		}
	}
	b.WriteString("\tdst = appendCBORHead(dst, 5, uint64(n))\n")

	needErr := false
	for _, f := range sorted {
		if cborFieldUsesErr(f.goType, defTypes) {
			needErr = true
			break
		}
	}
	if needErr {
		b.WriteString("\tvar err error\n")
	}
	for _, f := range sorted {
		guard := omitGuard("t."+f.field, f.goType)
		if !f.required && guard != "" {
			fmt.Fprintf(b, "\tif %s {\n", guard)
			emitCBOREncodeKV(b, f, "\t\t", defTypes)
			b.WriteString("\t}\n")
		} else {
			emitCBOREncodeKV(b, f, "\t", defTypes)
		}
	}
	b.WriteString("\treturn dst, nil\n}\n\n")
}

// emitCBOREncodeKV appends one field's canonical key text-string followed by
// its encoded value.
func emitCBOREncodeKV(b *strings.Builder, f codecField, indent string, defTypes map[string]bool) {
	fmt.Fprintf(b, "%sdst = appendCBORTextString(dst, %q)\n", indent, f.key)
	emitCBOREncodeValue(b, "t."+f.field, f.goType, indent, defTypes)
}

// emitCBOREncodeValue appends the canonical CBOR encoding of the lvalue src of
// the given resolved Go type to dst.
func emitCBOREncodeValue(b *strings.Builder, src, goType, indent string, defTypes map[string]bool) {
	switch {
	case goType == "string":
		fmt.Fprintf(b, "%sdst = appendCBORTextString(dst, %s)\n", indent, src)
	case goType == "int" || goType == "int64":
		fmt.Fprintf(b, "%sdst = appendCBORInt(dst, int64(%s))\n", indent, src)
	case goType == "float64":
		fmt.Fprintf(b, "%sdst = appendCBORFloat(dst, %s)\n", indent, src)
	case goType == "bool":
		fmt.Fprintf(b, "%sdst = appendCBORBool(dst, %s)\n", indent, src)
	case goType == "[]byte":
		fmt.Fprintf(b, "%sdst = appendCBORByteString(dst, %s)\n", indent, src)
	case goType == "json.RawMessage":
		// R10: opaque passthrough carries as a byte-string of the verbatim bytes.
		fmt.Fprintf(b, "%sdst = appendCBORByteString(dst, %s)\n", indent, src)
	case strings.HasPrefix(goType, "*"):
		elem := goType[1:]
		if defTypes[elem] {
			fmt.Fprintf(b, "%sif %s == nil {\n%s\tdst = append(dst, 0xf6)\n%s} else if dst, err = %s.appendCBOR(dst); err != nil {\n%s\treturn nil, err\n%s}\n", indent, src, indent, indent, src, indent, indent)
		} else {
			// Pointer to a scalar: a present pointer encodes as the scalar (the
			// caller's omit guard drops a nil pointer).
			switch elem {
			case "string", "bool", "int", "int64", "float64", "[]byte":
				emitCBOREncodeValue(b, "(*"+src+")", elem, indent, defTypes)
			default:
				emitCBOREncodeOpaqueAny(b, src, indent)
			}
		}
	case defTypes[goType]:
		fmt.Fprintf(b, "%sif dst, err = %s.appendCBOR(dst); err != nil {\n%s\treturn nil, err\n%s}\n", indent, src, indent, indent)
	case strings.HasPrefix(goType, "map["):
		valType := goType[strings.IndexByte(goType, ']')+1:]
		emitCBOREncodeMap(b, src, valType, indent, defTypes)
	case strings.HasPrefix(goType, "[]"):
		emitCBOREncodeSlice(b, src, goType[2:], indent, defTypes)
	default:
		// Bare any / interface{}: an opaque value (R10). Marshal it to its JSON
		// bytes and carry them as a byte-string, the json.RawMessage analog — the
		// one residual stdlib encode, reached only for a value the guest authors.
		emitCBOREncodeOpaqueAny(b, src, indent)
	}
}

// emitCBOREncodeOpaqueAny appends a bare opaque value as a CBOR byte-string of
// its JSON bytes (R10).
func emitCBOREncodeOpaqueAny(b *strings.Builder, src, indent string) {
	fmt.Fprintf(b, "%sif raw, mErr := json.Marshal(%s); mErr != nil {\n%s\treturn nil, mErr\n%s} else {\n%s\tdst = appendCBORByteString(dst, raw)\n%s}\n", indent, src, indent, indent, indent, indent)
}

// emitCBOREncodeMap appends a plain string-keyed map as a canonical CBOR map:
// keys sort at runtime by their encoded text-key bytes, then each value encodes
// through emitCBOREncodeValue.
func emitCBOREncodeMap(b *strings.Builder, src, valType, indent string, defTypes map[string]bool) {
	fmt.Fprintf(b, "%s{\n", indent)
	fmt.Fprintf(b, "%s\tkeys := make([]string, 0, len(%s))\n", indent, src)
	fmt.Fprintf(b, "%s\tfor k := range %s {\n%s\t\tkeys = append(keys, k)\n%s\t}\n", indent, src, indent, indent)
	fmt.Fprintf(b, "%s\tsortCBORKeys(keys)\n", indent)
	fmt.Fprintf(b, "%s\tdst = appendCBORHead(dst, 5, uint64(len(keys)))\n", indent)
	fmt.Fprintf(b, "%s\tfor _, k := range keys {\n", indent)
	fmt.Fprintf(b, "%s\t\tdst = appendCBORTextString(dst, k)\n", indent)
	fmt.Fprintf(b, "%s\t\tmv := %s[k]\n", indent, src)
	emitCBOREncodeValue(b, "mv", valType, indent+"\t\t", defTypes)
	fmt.Fprintf(b, "%s\t}\n", indent)
	fmt.Fprintf(b, "%s}\n", indent)
}

// emitCBOREncodeSlice appends a slice as a canonical CBOR array in element
// order.
func emitCBOREncodeSlice(b *strings.Builder, src, elemType, indent string, defTypes map[string]bool) {
	fmt.Fprintf(b, "%sdst = appendCBORHead(dst, 4, uint64(len(%s)))\n", indent, src)
	fmt.Fprintf(b, "%sfor i := range %s {\n", indent, src)
	fmt.Fprintf(b, "%s\tev := %s[i]\n", indent, src)
	emitCBOREncodeValue(b, "ev", elemType, indent+"\t", defTypes)
	fmt.Fprintf(b, "%s}\n", indent)
}

// cborFieldUsesErr reports whether the encode of the given type references the
// shared `err` variable in appendCBOR (so it is declared only when used). Only a
// nested def type or a pointer to one — encoded via `dst, err = v.appendCBOR(dst)`
// — uses it, directly or through a map/slice. A bare opaque `any` can also fail,
// but it uses its own locally-scoped error variable, so it does NOT require the
// shared `err`.
func cborFieldUsesErr(goType string, defTypes map[string]bool) bool {
	switch {
	case strings.HasPrefix(goType, "*"):
		return defTypes[goType[1:]]
	case defTypes[goType]:
		return true
	case strings.HasPrefix(goType, "map["):
		return cborFieldUsesErr(goType[strings.IndexByte(goType, ']')+1:], defTypes)
	case goType == "[]byte":
		return false
	case strings.HasPrefix(goType, "[]"):
		return cborFieldUsesErr(goType[2:], defTypes)
	default:
		return false
	}
}

// emitCBORDecode writes DecodeCBOR (the []byte entry point) and the decodeCBOR
// worker that reads a definite-length CBOR map, dispatching each key to its
// field decode and skipping unknown keys.
func emitCBORDecode(b *strings.Builder, typeName string, fields []codecField, defTypes map[string]bool) {
	fmt.Fprintf(b, "// DecodeCBOR decodes %s from canonical CBOR, dispatch-free. Unknown keys\n", typeName)
	fmt.Fprintf(b, "// are skipped.\n")
	fmt.Fprintf(b, "func (t *%s) DecodeCBOR(data []byte) error {\n\tdec := &cborDec{data: data}\n\treturn t.decodeCBOR(dec)\n}\n\n", typeName)

	fmt.Fprintf(b, "func (t *%s) decodeCBOR(dec *cborDec) error {\n", typeName)
	b.WriteString("\tn, err := dec.readMapHeader()\n\tif err != nil {\n\t\treturn err\n\t}\n")
	b.WriteString("\tfor i := 0; i < n; i++ {\n")
	b.WriteString("\t\tkey, err := dec.readTextString()\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
	b.WriteString("\t\tswitch key {\n")
	for _, f := range fields {
		fmt.Fprintf(b, "\t\tcase %q:\n", f.key)
		emitCBORDecodeField(b, f, "\t\t\t", defTypes)
	}
	b.WriteString("\t\tdefault:\n\t\t\tif err := dec.skip(); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn nil\n}\n\n")
}

// emitCBORDecodeField decodes one field into t.<field>. Every shape — including
// a nested map or slice in value position — descends through emitCBORDecodeValue.
func emitCBORDecodeField(b *strings.Builder, f codecField, indent string, defTypes map[string]bool) {
	emitCBORDecodeValue(b, "t."+f.field, f.goType, indent, defTypes)
}

// emitCBORDecodeValue decodes the next CBOR item into the lvalue dst of the given
// resolved Go type. Maps and slices generate a block-scoped loop that builds the
// collection and assigns it to dst, so a nested composite (a map of maps, a slice
// of maps) decodes correctly rather than falling through to the opaque path.
func emitCBORDecodeValue(b *strings.Builder, dst, goType, indent string, defTypes map[string]bool) {
	switch {
	case goType == "string":
		fmt.Fprintf(b, "%sif s, err := dec.readTextString(); err != nil {\n%s\treturn err\n%s} else {\n%s\t%s = s\n%s}\n", indent, indent, indent, indent, dst, indent)
	case goType == "int" || goType == "int64":
		fmt.Fprintf(b, "%sif v, err := dec.readInt(); err != nil {\n%s\treturn err\n%s} else {\n%s\t%s = %s(v)\n%s}\n", indent, indent, indent, indent, dst, goType, indent)
	case goType == "float64":
		fmt.Fprintf(b, "%sif v, err := dec.readFloat(); err != nil {\n%s\treturn err\n%s} else {\n%s\t%s = v\n%s}\n", indent, indent, indent, indent, dst, indent)
	case goType == "bool":
		fmt.Fprintf(b, "%sif v, err := dec.readBool(); err != nil {\n%s\treturn err\n%s} else {\n%s\t%s = v\n%s}\n", indent, indent, indent, indent, dst, indent)
	case goType == "[]byte":
		fmt.Fprintf(b, "%sif v, err := dec.readByteString(); err != nil {\n%s\treturn err\n%s} else {\n%s\t%s = v\n%s}\n", indent, indent, indent, indent, dst, indent)
	case goType == "json.RawMessage":
		fmt.Fprintf(b, "%sif v, err := dec.readByteString(); err != nil {\n%s\treturn err\n%s} else {\n%s\t%s = json.RawMessage(v)\n%s}\n", indent, indent, indent, indent, dst, indent)
	case strings.HasPrefix(goType, "*"):
		elem := goType[1:]
		if defTypes[elem] {
			fmt.Fprintf(b, "%s{\n%s\tv := new(%s)\n%s\tif err := v.decodeCBOR(dec); err != nil {\n%s\t\treturn err\n%s\t}\n%s\t%s = v\n%s}\n", indent, indent, elem, indent, indent, indent, indent, dst, indent)
		} else {
			switch elem {
			case "string", "bool", "int", "int64", "float64", "[]byte":
				fmt.Fprintf(b, "%s{\n%s\tvar sv %s\n", indent, indent, elem)
				emitCBORDecodeValue(b, "sv", elem, indent+"\t", defTypes)
				fmt.Fprintf(b, "%s\t%s = &sv\n%s}\n", indent, dst, indent)
			default:
				emitCBORDecodeOpaqueAny(b, dst, indent)
			}
		}
	case defTypes[goType]:
		fmt.Fprintf(b, "%sif err := (&%s).decodeCBOR(dec); err != nil {\n%s\treturn err\n%s}\n", indent, dst, indent, indent)
	case strings.HasPrefix(goType, "map["):
		valType := goType[strings.IndexByte(goType, ']')+1:]
		fmt.Fprintf(b, "%s{\n", indent)
		fmt.Fprintf(b, "%s\tmn, err := dec.readMapHeader()\n%s\tif err != nil {\n%s\t\treturn err\n%s\t}\n", indent, indent, indent, indent)
		fmt.Fprintf(b, "%s\tm := make(%s, mn)\n", indent, goType)
		fmt.Fprintf(b, "%s\tfor mi := 0; mi < mn; mi++ {\n", indent)
		fmt.Fprintf(b, "%s\t\tmk, err := dec.readTextString()\n%s\t\tif err != nil {\n%s\t\t\treturn err\n%s\t\t}\n", indent, indent, indent, indent)
		fmt.Fprintf(b, "%s\t\tvar mv %s\n", indent, valType)
		emitCBORDecodeValue(b, "mv", valType, indent+"\t\t", defTypes)
		fmt.Fprintf(b, "%s\t\tm[mk] = mv\n", indent)
		fmt.Fprintf(b, "%s\t}\n", indent)
		fmt.Fprintf(b, "%s\t%s = m\n", indent, dst)
		fmt.Fprintf(b, "%s}\n", indent)
	case strings.HasPrefix(goType, "[]"):
		elemType := goType[2:]
		fmt.Fprintf(b, "%s{\n", indent)
		fmt.Fprintf(b, "%s\tsn, err := dec.readArrayHeader()\n%s\tif err != nil {\n%s\t\treturn err\n%s\t}\n", indent, indent, indent, indent)
		fmt.Fprintf(b, "%s\ts := make(%s, 0, sn)\n", indent, goType)
		fmt.Fprintf(b, "%s\tfor si := 0; si < sn; si++ {\n", indent)
		fmt.Fprintf(b, "%s\t\tvar ev %s\n", indent, elemType)
		emitCBORDecodeValue(b, "ev", elemType, indent+"\t\t", defTypes)
		fmt.Fprintf(b, "%s\t\ts = append(s, ev)\n", indent)
		fmt.Fprintf(b, "%s\t}\n", indent)
		fmt.Fprintf(b, "%s\t%s = s\n", indent, dst)
		fmt.Fprintf(b, "%s}\n", indent)
	default:
		// bare any / interface{}: read the opaque byte-string and decode its JSON
		// bytes with a number-preserving decoder, mirroring the JSON codec's
		// residual opaque decode.
		emitCBORDecodeOpaqueAny(b, dst, indent)
	}
}

// emitCBORDecodeOpaqueAny decodes a bare opaque any: read the byte-string (R10)
// and json.Unmarshal its bytes with UseNumber so nested integers stay integers.
func emitCBORDecodeOpaqueAny(b *strings.Builder, dst, indent string) {
	fmt.Fprintf(b, "%sif raw, err := dec.readByteString(); err != nil {\n%s\treturn err\n%s} else {\n", indent, indent, indent)
	fmt.Fprintf(b, "%s\td2 := json.NewDecoder(bytes.NewReader(raw))\n", indent)
	fmt.Fprintf(b, "%s\td2.UseNumber()\n", indent)
	fmt.Fprintf(b, "%s\tif err := d2.Decode(&%s); err != nil {\n%s\t\treturn err\n%s\t}\n%s}\n", indent, dst, indent, indent, indent)
}

// emitCBORHelpers writes the shared CBOR read/write helpers and the float16
// conversions the generated methods call. Emitted once per package (the
// helpers-bearing file), like the JSON helpers.
func emitCBORHelpers(b *strings.Builder) {
	b.WriteString(cborHelpersSrc)
}

// cborHelpersSrc is the shared CBOR machinery: the append-side head/scalar/
// float writers, the runtime key sort, and the cborDec reader. No runtime CBOR
// library is used — the canonical rules are inline here.
const cborHelpersSrc = `// appendCBORHead appends a CBOR head: the major type in the top three bits and
// the shortest length/argument encoding.
func appendCBORHead(dst []byte, major byte, arg uint64) []byte {
	mb := major << 5
	switch {
	case arg < 24:
		return append(dst, mb|byte(arg))
	case arg < 0x100:
		return append(dst, mb|24, byte(arg))
	case arg < 0x10000:
		return append(dst, mb|25, byte(arg>>8), byte(arg))
	case arg < 0x100000000:
		return append(dst, mb|26, byte(arg>>24), byte(arg>>16), byte(arg>>8), byte(arg))
	default:
		return append(dst, mb|27,
			byte(arg>>56), byte(arg>>48), byte(arg>>40), byte(arg>>32),
			byte(arg>>24), byte(arg>>16), byte(arg>>8), byte(arg))
	}
}

// appendCBORInt appends a signed integer as CBOR major type 0 (non-negative) or
// 1 (negative), shortest head.
func appendCBORInt(dst []byte, i int64) []byte {
	if i < 0 {
		return appendCBORHead(dst, 1, uint64(-1-i))
	}
	return appendCBORHead(dst, 0, uint64(i))
}

// appendCBORTextString appends s as a CBOR text string (major 3): shortest
// length head then the UTF-8 bytes verbatim.
func appendCBORTextString(dst []byte, s string) []byte {
	dst = appendCBORHead(dst, 3, uint64(len(s)))
	return append(dst, s...)
}

// appendCBORByteString appends b as a CBOR byte string (major 2): shortest
// length head then the bytes verbatim.
func appendCBORByteString(dst, b []byte) []byte {
	dst = appendCBORHead(dst, 2, uint64(len(b)))
	return append(dst, b...)
}

// appendCBORBool appends the fixed single-byte CBOR bool.
func appendCBORBool(dst []byte, v bool) []byte {
	if v {
		return append(dst, 0xf5)
	}
	return append(dst, 0xf4)
}

// appendCBORFloat appends f as a canonical CBOR float per CBOR CDE §4.2: the
// shortest of float16/float32/float64 that round-trips to the exact value, with
// NaN canonicalized to 0xf97e00, ±Inf to the float16 infinities, and ±0
// preserved by sign. No numeric reduction: an integer-valued float stays a
// float.
func appendCBORFloat(dst []byte, f float64) []byte {
	switch {
	case math.IsNaN(f):
		return append(dst, 0xf9, 0x7e, 0x00)
	case math.IsInf(f, 1):
		return append(dst, 0xf9, 0x7c, 0x00)
	case math.IsInf(f, -1):
		return append(dst, 0xf9, 0xfc, 0x00)
	}
	f32 := float32(f)
	if float64(f32) == f {
		if h, ok := f32ToF16Exact(math.Float32bits(f32)); ok {
			return append(dst, 0xf9, byte(h>>8), byte(h))
		}
		bits := math.Float32bits(f32)
		return append(dst, 0xfa, byte(bits>>24), byte(bits>>16), byte(bits>>8), byte(bits))
	}
	bits := math.Float64bits(f)
	return append(dst, 0xfb,
		byte(bits>>56), byte(bits>>48), byte(bits>>40), byte(bits>>32),
		byte(bits>>24), byte(bits>>16), byte(bits>>8), byte(bits))
}

// f32ToF16Exact converts float32 bits to a float16 if the value is EXACTLY
// representable (no rounding), returning the 16-bit encoding and true. It is
// used only to select the shortest float that round-trips, so a lossy
// conversion returns false and the caller keeps the wider width. ±0 preserves
// its sign; Inf/NaN are handled by the caller.
func f32ToF16Exact(b uint32) (uint16, bool) {
	sign := uint16((b >> 16) & 0x8000)
	exp := int32((b >> 23) & 0xff)
	mant := b & 0x7fffff
	if exp == 0 {
		if mant == 0 {
			return sign, true // ±0
		}
		return 0, false // float32 subnormal: not float16-exact
	}
	if exp == 0xff {
		return 0, false // Inf/NaN: handled by the caller
	}
	e := exp - 127
	switch {
	case e >= -14 && e <= 15:
		if mant&0x1fff != 0 {
			return 0, false // low mantissa bits would be lost
		}
		return sign | uint16((e+15)<<10) | uint16(mant>>13), true
	case e >= -24 && e <= -15:
		full := mant | 0x800000 // add the implicit leading 1
		shift := uint32(-(e + 1))
		if full&((1<<shift)-1) != 0 {
			return 0, false // low bits would be lost
		}
		hm := full >> shift
		if hm == 0 || hm > 0x3ff {
			return 0, false
		}
		return sign | uint16(hm), true
	default:
		return 0, false
	}
}

// float16ToFloat64 expands a CBOR half-precision float to float64.
func float16ToFloat64(h uint16) float64 {
	sign := uint32(h&0x8000) << 16
	exp := uint32(h>>10) & 0x1f
	mant := uint32(h & 0x3ff)
	switch exp {
	case 0:
		if mant == 0 {
			return float64(math.Float32frombits(sign)) // ±0
		}
		e := uint32(0)
		for mant&0x400 == 0 {
			mant <<= 1
			e++
		}
		mant &= 0x3ff
		exp32 := uint32(127 - 15 - e + 1)
		return float64(math.Float32frombits(sign | (exp32 << 23) | (mant << 13)))
	case 0x1f:
		if mant == 0 {
			return float64(math.Float32frombits(sign | 0x7f800000)) // ±Inf
		}
		return float64(math.Float32frombits(sign | 0x7f800000 | (mant << 13))) // NaN
	default:
		exp32 := exp - 15 + 127
		return float64(math.Float32frombits(sign | (exp32 << 23) | (mant << 13)))
	}
}

// sortCBORKeys sorts map keys into canonical CBOR order: by the bytewise order
// of their encoded text-key bytes (length head first, then UTF-8 content).
func sortCBORKeys(keys []string) {
	sort.Slice(keys, func(i, j int) bool { return cborLessKey(keys[i], keys[j]) })
}

// cborLessKey is the runtime twin of the generator's key comparator.
func cborLessKey(a, b string) bool {
	if len(a) != len(b) {
		ha := cborStrHeadBytes(len(a))
		hb := cborStrHeadBytes(len(b))
		return bytes.Compare(ha, hb) < 0
	}
	return a < b
}

// cborStrHeadBytes returns the CBOR text-string length head for a string of the
// given byte length.
func cborStrHeadBytes(n int) []byte {
	switch {
	case n < 24:
		return []byte{0x60 | byte(n)}
	case n < 0x100:
		return []byte{0x78, byte(n)}
	case n < 0x10000:
		return []byte{0x79, byte(n >> 8), byte(n)}
	default:
		return []byte{0x7a, byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
	}
}

// cborDec is a minimal canonical-CBOR reader over a byte slice. It reads only
// the definite-length items the codec produces; indefinite lengths and
// unsupported majors are rejected loudly.
type cborDec struct {
	data []byte
	pos  int
}

// readHead reads one item head, returning the major type, the additional-info
// nibble, and the decoded argument (the immediate for ai < 24, else the
// big-endian follow bytes).
func (d *cborDec) readHead() (byte, byte, uint64, error) {
	if d.pos >= len(d.data) {
		return 0, 0, 0, fmt.Errorf("abi: cbor: unexpected end of input")
	}
	ib := d.data[d.pos]
	d.pos++
	major := ib >> 5
	ai := ib & 0x1f
	switch {
	case ai < 24:
		return major, ai, uint64(ai), nil
	case ai == 24:
		if d.pos+1 > len(d.data) {
			return 0, 0, 0, fmt.Errorf("abi: cbor: truncated 1-byte argument")
		}
		v := uint64(d.data[d.pos])
		d.pos++
		return major, ai, v, nil
	case ai == 25:
		if d.pos+2 > len(d.data) {
			return 0, 0, 0, fmt.Errorf("abi: cbor: truncated 2-byte argument")
		}
		v := uint64(d.data[d.pos])<<8 | uint64(d.data[d.pos+1])
		d.pos += 2
		return major, ai, v, nil
	case ai == 26:
		if d.pos+4 > len(d.data) {
			return 0, 0, 0, fmt.Errorf("abi: cbor: truncated 4-byte argument")
		}
		v := uint64(d.data[d.pos])<<24 | uint64(d.data[d.pos+1])<<16 | uint64(d.data[d.pos+2])<<8 | uint64(d.data[d.pos+3])
		d.pos += 4
		return major, ai, v, nil
	case ai == 27:
		if d.pos+8 > len(d.data) {
			return 0, 0, 0, fmt.Errorf("abi: cbor: truncated 8-byte argument")
		}
		var v uint64
		for k := 0; k < 8; k++ {
			v = v<<8 | uint64(d.data[d.pos+k])
		}
		d.pos += 8
		return major, ai, v, nil
	default:
		return 0, 0, 0, fmt.Errorf("abi: cbor: reserved additional info %d", ai)
	}
}

func (d *cborDec) readMapHeader() (int, error) {
	major, _, arg, err := d.readHead()
	if err != nil {
		return 0, err
	}
	if major != 5 {
		return 0, fmt.Errorf("abi: cbor: expected map, got major %d", major)
	}
	return int(arg), nil
}

func (d *cborDec) readArrayHeader() (int, error) {
	major, _, arg, err := d.readHead()
	if err != nil {
		return 0, err
	}
	if major != 4 {
		return 0, fmt.Errorf("abi: cbor: expected array, got major %d", major)
	}
	return int(arg), nil
}

func (d *cborDec) readTextString() (string, error) {
	major, _, arg, err := d.readHead()
	if err != nil {
		return "", err
	}
	if major != 3 {
		return "", fmt.Errorf("abi: cbor: expected text string, got major %d", major)
	}
	if d.pos+int(arg) > len(d.data) {
		return "", fmt.Errorf("abi: cbor: truncated text string")
	}
	s := string(d.data[d.pos : d.pos+int(arg)])
	d.pos += int(arg)
	return s, nil
}

func (d *cborDec) readByteString() ([]byte, error) {
	major, _, arg, err := d.readHead()
	if err != nil {
		return nil, err
	}
	if major != 2 {
		return nil, fmt.Errorf("abi: cbor: expected byte string, got major %d", major)
	}
	if d.pos+int(arg) > len(d.data) {
		return nil, fmt.Errorf("abi: cbor: truncated byte string")
	}
	out := make([]byte, arg)
	copy(out, d.data[d.pos:d.pos+int(arg)])
	d.pos += int(arg)
	return out, nil
}

func (d *cborDec) readInt() (int64, error) {
	major, _, arg, err := d.readHead()
	if err != nil {
		return 0, err
	}
	switch major {
	case 0:
		return int64(arg), nil
	case 1:
		return -1 - int64(arg), nil
	default:
		return 0, fmt.Errorf("abi: cbor: expected integer, got major %d", major)
	}
}

func (d *cborDec) readFloat() (float64, error) {
	major, ai, arg, err := d.readHead()
	if err != nil {
		return 0, err
	}
	if major != 7 {
		return 0, fmt.Errorf("abi: cbor: expected float, got major %d", major)
	}
	switch ai {
	case 25:
		return float16ToFloat64(uint16(arg)), nil
	case 26:
		return float64(math.Float32frombits(uint32(arg))), nil
	case 27:
		return math.Float64frombits(arg), nil
	default:
		return 0, fmt.Errorf("abi: cbor: expected float, got additional info %d", ai)
	}
}

func (d *cborDec) readBool() (bool, error) {
	major, ai, _, err := d.readHead()
	if err != nil {
		return false, err
	}
	if major != 7 || (ai != 20 && ai != 21) {
		return false, fmt.Errorf("abi: cbor: expected bool")
	}
	return ai == 21, nil
}

// skip consumes exactly one CBOR item (recursively for arrays and maps),
// discarding it — used for an unknown map key's value.
func (d *cborDec) skip() error {
	major, ai, arg, err := d.readHead()
	if err != nil {
		return err
	}
	switch major {
	case 0, 1, 7:
		return nil // argument already consumed by readHead
	case 2, 3:
		if d.pos+int(arg) > len(d.data) {
			return fmt.Errorf("abi: cbor: truncated string")
		}
		d.pos += int(arg)
		return nil
	case 4:
		for k := 0; k < int(arg); k++ {
			if err := d.skip(); err != nil {
				return err
			}
		}
		return nil
	case 5:
		for k := 0; k < int(arg)*2; k++ {
			if err := d.skip(); err != nil {
				return err
			}
		}
		return nil
	default:
		_ = ai
		return fmt.Errorf("abi: cbor: cannot skip major %d", major)
	}
}
`
