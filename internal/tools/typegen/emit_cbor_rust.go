// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"sort"
	"strings"
)

// This file is the Rust CBOR emission fork, the cross-language twin of the Go
// fork in emit_cbor.go. It emits a reflect-free, dependency-free canonical-CBOR
// codec (RFC 8949 §4.2.1 Core Deterministic Encoding; floats per CBOR CDE §4.2)
// for each generated struct: an inherent impl carrying encode_cbor / decode_cbor
// plus the append_cbor / decode_cbor_from workers they drive. No runtime CBOR or
// float crate is used — the shared machinery lives in the generated cbor_gen.rs
// module (rustCBORHelpersSrc) and the canonical rules are inline.
//
// The one rule-set matches the Go fork exactly, so the two produce byte-identical
// wire output:
//
//   - a struct is a definite-length CBOR map; its fields emit in canonical key
//     order fixed at generation time (sort by encoded text-key bytes);
//   - a plain string-keyed map (BTreeMap) is a definite-length CBOR map whose
//     keys sort at runtime by their encoded text-key bytes — NOT by Rust's String
//     Ord (which is plain lexicographic and disagrees with the CBOR length-first
//     order), so the BTreeMap is re-sorted before emission;
//   - a slice (Vec) is a definite-length CBOR array in element order;
//   - integers use the shortest head; bools are fixed single bytes; text and byte
//     strings use the shortest length head;
//   - a float uses the shortest of float16/32/64 that round-trips, NaN → 0xf97e00,
//     ±Inf → 0xf97c00 / 0xf9fc00, ±0 preserved by sign, no numeric reduction;
//   - a []byte value rides in Rust as a base64 String (matching the JSON wire), so
//     it is base64-DECODED to its raw bytes before emission as a CBOR byte-string;
//   - an opaque value (json.RawMessage / any) rides as Box<RawValue> and carries as
//     a CBOR byte-string of its verbatim JSON bytes (R10).

// emitRustCBOR writes the inherent impl carrying the canonical-CBOR codec for one
// def struct type. Encode emits fields in canonical key order fixed here; decode
// switches on the wire key and is order-independent.
func emitRustCBOR(b *strings.Builder, typeName string, n schemaNode, vocab map[string]string, defTypes map[string]bool) {
	fields := resolveCodecFields(typeName, n, vocab)
	sorted := append([]codecField(nil), fields...)
	sort.SliceStable(sorted, func(i, j int) bool { return cborKeyLess(sorted[i].key, sorted[j].key) })

	fmt.Fprintf(b, "impl %s {\n", typeName)

	// encode
	b.WriteString("    /// Encodes this value as canonical CBOR (RFC 8949 §4.2.1; floats per\n")
	b.WriteString("    /// CBOR CDE §4.2), dispatch-free and without a runtime CBOR library.\n")
	b.WriteString("    pub fn encode_cbor(&self) -> Result<Vec<u8>, crate::cbor_gen::CborError> {\n")
	b.WriteString("        let mut out = Vec::new();\n")
	b.WriteString("        self.append_cbor(&mut out)?;\n")
	b.WriteString("        Ok(out)\n")
	b.WriteString("    }\n\n")

	b.WriteString("    pub(crate) fn append_cbor(&self, out: &mut Vec<u8>) -> Result<(), crate::cbor_gen::CborError> {\n")
	nRequired := 0
	nOptional := 0
	for _, f := range sorted {
		if f.required || rustOmitGuard("self."+rustFieldNameOnly(f.key), f, defTypes) == "" {
			nRequired++
		} else {
			nOptional++
		}
	}
	if nOptional > 0 {
		fmt.Fprintf(b, "        let mut n: u64 = %d;\n", nRequired)
	} else {
		fmt.Fprintf(b, "        let n: u64 = %d;\n", nRequired)
	}
	for _, f := range sorted {
		if f.required {
			continue
		}
		guard := rustOmitGuard("self."+rustFieldNameOnly(f.key), f, defTypes)
		if guard != "" {
			fmt.Fprintf(b, "        if %s { n += 1; }\n", guard)
		}
	}
	b.WriteString("        crate::cbor_gen::write_head(out, 5, n);\n")
	for _, f := range sorted {
		emitRustCBOREncodeField(b, f, defTypes)
	}
	b.WriteString("        Ok(())\n")
	b.WriteString("    }\n\n")

	// decode
	b.WriteString("    /// Decodes a value from canonical CBOR, dispatch-free. Unknown keys are\n")
	b.WriteString("    /// skipped.\n")
	b.WriteString("    pub fn decode_cbor(data: &[u8]) -> Result<Self, crate::cbor_gen::CborError> {\n")
	b.WriteString("        let mut dec = crate::cbor_gen::CborDec::new(data);\n")
	b.WriteString("        Self::decode_cbor_from(&mut dec)\n")
	b.WriteString("    }\n\n")

	b.WriteString("    pub(crate) fn decode_cbor_from(dec: &mut crate::cbor_gen::CborDec) -> Result<Self, crate::cbor_gen::CborError> {\n")
	for _, f := range fields {
		rustTy := rustFieldType(f, defTypes)
		local := rustLocalName(f.key)
		if f.required {
			fmt.Fprintf(b, "        let mut f_%s: Option<%s> = None;\n", local, rustTy)
		} else {
			fmt.Fprintf(b, "        let mut f_%s: %s = %s;\n", local, rustTy, rustEmptyInit(rustTy))
		}
	}
	b.WriteString("        let n = dec.read_map_header()?;\n")
	b.WriteString("        for _ in 0..n {\n")
	b.WriteString("            let k = dec.read_text()?;\n")
	b.WriteString("            match k.as_str() {\n")
	for _, f := range fields {
		emitRustCBORDecodeArm(b, f, defTypes)
	}
	b.WriteString("                _ => dec.skip()?,\n")
	b.WriteString("            }\n")
	b.WriteString("        }\n")
	b.WriteString("        Ok(Self {\n")
	for _, f := range fields {
		name := rustFieldNameOnly(f.key)
		local := rustLocalName(f.key)
		if f.required {
			fmt.Fprintf(b, "            %s: f_%s.ok_or_else(|| crate::cbor_gen::CborError(\"cbor: missing required field %s\".to_string()))?,\n", name, local, f.key)
		} else {
			fmt.Fprintf(b, "            %s: f_%s,\n", name, local)
		}
	}
	b.WriteString("        })\n")
	b.WriteString("    }\n")
	b.WriteString("}\n\n")
}

// rustFieldNameOnly returns just the Rust field identifier for a wire key (the
// snake_case or raw-identifier form), discarding the rename flag.
func rustFieldNameOnly(key string) string {
	name, _ := rustFieldName(key)
	return name
}

// rustLocalName returns a plain identifier for a decode-local temp (`f_<name>`).
// A raw-identifier field name (r#type) cannot form part of another identifier, so
// the r# prefix is stripped: the field r#type decodes through the local f_type.
func rustLocalName(key string) string {
	return strings.TrimPrefix(rustFieldNameOnly(key), "r#")
}

// rustOmitGuard returns a boolean Rust expression true when a non-required field
// must be emitted (non-empty), matching the Go codec's omitempty. An empty string
// means the field has no omit notion (a required-by-shape nested struct realized
// by value), so it always emits.
func rustOmitGuard(expr string, f codecField, defTypes map[string]bool) string {
	rustTy := rustFieldType(f, defTypes)
	switch {
	case rustTy == "String":
		return "!" + expr + ".is_empty()"
	case rustTy == "i64":
		return expr + " != 0"
	case rustTy == "f64":
		return expr + " != 0.0"
	case rustTy == "bool":
		return expr
	case strings.HasPrefix(rustTy, "Vec<"):
		return "!" + expr + ".is_empty()"
	case strings.HasPrefix(rustTy, "BTreeMap<"):
		return "!" + expr + ".is_empty()"
	case strings.HasPrefix(rustTy, "Option<"):
		return expr + ".is_some()"
	default:
		return ""
	}
}

// emitRustCBOREncodeField writes one field's canonical key text-string followed
// by its encoded value, guarding a non-required field by its omit predicate. The
// value is always encoded through a reference: an owned field passes &self.name,
// an Option field binds the inner reference via let-Some.
func emitRustCBOREncodeField(b *strings.Builder, f codecField, defTypes map[string]bool) {
	name := rustFieldNameOnly(f.key)
	rustTy := rustFieldType(f, defTypes)
	src := "self." + name

	if strings.HasPrefix(rustTy, "Option<") {
		// An Option-wrapped field (optional opaque, or a pointer). Emit inside a
		// let-Some guard; the bound reference x is the inner value. A pointer
		// field's element type is f.goType with the leading * stripped.
		inner := rustTy[len("Option<") : len(rustTy)-1]
		innerGo := strings.TrimPrefix(f.goType, "*")
		fmt.Fprintf(b, "        if let Some(ref x) = %s {\n", src)
		fmt.Fprintf(b, "            crate::cbor_gen::write_text(out, %q);\n", f.key)
		emitRustCBOREncodeRef(b, "x", innerGo, inner, "            ", defTypes)
		b.WriteString("        }\n")
		return
	}

	emit := func(indent string) {
		fmt.Fprintf(b, "%scrate::cbor_gen::write_text(out, %q);\n", indent, f.key)
		emitRustCBOREncodeRef(b, "&self."+name, f.goType, rustTy, indent, defTypes)
	}
	if f.required {
		emit("        ")
		return
	}
	guard := rustOmitGuard(src, f, defTypes)
	if guard == "" {
		emit("        ")
		return
	}
	fmt.Fprintf(b, "        if %s {\n", guard)
	emit("            ")
	b.WriteString("        }\n")
}

// rustRefValue returns the expression denoting the value that the reference
// expression src points at, spelled the way a Rust author would spell it.
//
// The two forms src can take are already values in disguise or genuine
// references, and they need opposite treatment. A borrow taken at the call site
// (&self.name) denotes the very same place as the field itself, so the borrow is
// dropped; a binding that is already a reference (the let-Some, map-value and
// slice-element bindings) is dereferenced. Composing the two blindly would emit
// *&self.name — a borrow the compiler immediately undoes, which reads as an
// oversight and which every Rust linter reports.
func rustRefValue(src string) string {
	if strings.HasPrefix(src, "&") {
		return src[1:]
	}
	return "*" + src
}

// rustRefReceiver returns the expression to call a method on, given a reference
// expression src. A method call auto-references its receiver, so a borrow taken
// at the call site is redundant and is dropped: &self.name becomes self.name,
// while a binding that is already a reference calls through the borrow unchanged.
//
// This is deliberately a separate rule from rustRefValue rather than a shared
// one. They agree on a borrowed place and disagree on a reference binding, where
// a value needs the dereference a receiver must not have.
func rustRefReceiver(src string) string {
	return strings.TrimPrefix(src, "&")
}

// emitRustCBOREncodeRef appends the canonical CBOR encoding of the value the
// reference expression src (a &T of the given resolved Go type) points at. Every
// value is handled through a reference so strings coerce to &str and composites
// iterate through the borrow — one shape regardless of whether src is a struct
// field, a map value, or a slice element. Scalars and method receivers are
// spelled through rustRefValue and rustRefReceiver, which resolve the borrow at
// emission time so the output carries no borrow the compiler would undo.
func emitRustCBOREncodeRef(b *strings.Builder, src, goType, rustTy, indent string, defTypes map[string]bool) {
	switch {
	case goType == "string":
		fmt.Fprintf(b, "%scrate::cbor_gen::write_text(out, %s);\n", indent, src)
	case goType == "int" || goType == "int64":
		fmt.Fprintf(b, "%scrate::cbor_gen::write_int(out, %s);\n", indent, rustRefValue(src))
	case goType == "float64":
		fmt.Fprintf(b, "%scrate::cbor_gen::write_float(out, %s);\n", indent, rustRefValue(src))
	case goType == "bool":
		fmt.Fprintf(b, "%scrate::cbor_gen::write_bool(out, %s);\n", indent, rustRefValue(src))
	case goType == "[]byte":
		fmt.Fprintf(b, "%scrate::cbor_gen::write_bytes(out, &crate::cbor_gen::b64_decode(%s)?);\n", indent, src)
	case goType == "json.RawMessage" || goType == "any" || goType == "interface{}":
		fmt.Fprintf(b, "%scrate::cbor_gen::write_bytes(out, %s.get().as_bytes());\n", indent, rustRefReceiver(src))
	case strings.HasPrefix(goType, "*"):
		emitRustCBOREncodeRef(b, src, goType[1:], strings.TrimSuffix(strings.TrimPrefix(rustTy, "Option<"), ">"), indent, defTypes)
	case defTypes[goType]:
		fmt.Fprintf(b, "%s%s.append_cbor(out)?;\n", indent, rustRefReceiver(src))
	case strings.HasPrefix(goType, "map["):
		valType := goType[strings.IndexByte(goType, ']')+1:]
		emitRustCBOREncodeMap(b, src, valType, goTypeToRust(valType, "", defTypes), indent, defTypes)
	case strings.HasPrefix(goType, "[]"):
		elemType := goType[2:]
		emitRustCBOREncodeSlice(b, src, elemType, goTypeToRust(elemType, "", defTypes), indent, defTypes)
	default:
		// A def type behind a Box (pointer-to-def, inner of Option<Box<T>>): the
		// method auto-derefs through the Box.
		fmt.Fprintf(b, "%s%s.append_cbor(out)?;\n", indent, rustRefReceiver(src))
	}
}

// emitRustCBOREncodeMap appends a BTreeMap (referenced by src) as a canonical
// CBOR map: the keys are re-sorted by encoded text-key bytes (BTreeMap's own
// String Ord is lexicographic and disagrees with the CBOR length-first order),
// then each value is encoded through its own reference. The key/value bindings
// are depth-neutral (they live in a fresh block), so nested maps do not clash.
func emitRustCBOREncodeMap(b *strings.Builder, src, valType, valRust, indent string, defTypes map[string]bool) {
	fmt.Fprintf(b, "%s{\n", indent)
	fmt.Fprintf(b, "%s    let c = %s;\n", indent, src)
	fmt.Fprintf(b, "%s    let mut ks: Vec<&String> = c.keys().collect();\n", indent)
	fmt.Fprintf(b, "%s    crate::cbor_gen::sort_keys(&mut ks);\n", indent)
	fmt.Fprintf(b, "%s    crate::cbor_gen::write_head(out, 5, ks.len() as u64);\n", indent)
	fmt.Fprintf(b, "%s    for k in ks {\n", indent)
	fmt.Fprintf(b, "%s        crate::cbor_gen::write_text(out, k);\n", indent)
	fmt.Fprintf(b, "%s        let mv = &c[k];\n", indent)
	emitRustCBOREncodeRef(b, "mv", valType, valRust, indent+"        ", defTypes)
	fmt.Fprintf(b, "%s    }\n", indent)
	fmt.Fprintf(b, "%s}\n", indent)
}

// emitRustCBOREncodeSlice appends a Vec (referenced by src) as a canonical CBOR
// array in element order.
func emitRustCBOREncodeSlice(b *strings.Builder, src, elemType, elemRust, indent string, defTypes map[string]bool) {
	fmt.Fprintf(b, "%s{\n", indent)
	fmt.Fprintf(b, "%s    let c = %s;\n", indent, src)
	fmt.Fprintf(b, "%s    crate::cbor_gen::write_head(out, 4, c.len() as u64);\n", indent)
	fmt.Fprintf(b, "%s    for ev in c {\n", indent)
	emitRustCBOREncodeRef(b, "ev", elemType, elemRust, indent+"        ", defTypes)
	fmt.Fprintf(b, "%s    }\n", indent)
	fmt.Fprintf(b, "%s}\n", indent)
}

// emitRustCBORDecodeArm writes the match arm decoding one field from the wire.
// Every shape decodes through rustDecodeExpr, which yields a value (a block
// expression for maps and slices); a required field wraps it in Some.
func emitRustCBORDecodeArm(b *strings.Builder, f codecField, defTypes map[string]bool) {
	name := rustLocalName(f.key)
	rustTy := rustFieldType(f, defTypes)
	expr := rustDecodeExpr(f.goType, rustTy, defTypes)
	if f.required {
		fmt.Fprintf(b, "                %q => f_%s = Some(%s),\n", f.key, name, expr)
	} else {
		fmt.Fprintf(b, "                %q => f_%s = %s,\n", f.key, name, expr)
	}
}

// rustDecodeExpr returns a Rust expression that decodes the next CBOR item into a
// value of the field's Rust type (rustTy) from the reader `dec`. Maps and slices
// yield a block expression that reads the header and loops; the trailing `?` in
// each read propagates from the enclosing decode_cbor_from (which returns Result).
func rustDecodeExpr(goType, rustTy string, defTypes map[string]bool) string {
	switch {
	case goType == "string":
		return "dec.read_text()?"
	case goType == "int" || goType == "int64":
		return "dec.read_int()?"
	case goType == "float64":
		return "dec.read_float()?"
	case goType == "bool":
		return "dec.read_bool()?"
	case goType == "[]byte":
		return "crate::cbor_gen::b64_encode(&dec.read_bytes()?)"
	case goType == "json.RawMessage" || goType == "any" || goType == "interface{}":
		expr := "crate::cbor_gen::raw_from_bytes(dec.read_bytes()?)?"
		if strings.HasPrefix(rustTy, "Option<") {
			return "Some(" + expr + ")"
		}
		return expr
	case strings.HasPrefix(goType, "*"):
		elem := goType[1:]
		if defTypes[elem] {
			return fmt.Sprintf("Some(Box::new(%s::decode_cbor_from(dec)?))", elem)
		}
		inner := strings.TrimSuffix(strings.TrimPrefix(rustTy, "Option<"), ">")
		return "Some(" + rustDecodeExpr(elem, inner, defTypes) + ")"
	case defTypes[goType]:
		return fmt.Sprintf("%s::decode_cbor_from(dec)?", goType)
	case strings.HasPrefix(goType, "map["):
		valType := goType[strings.IndexByte(goType, ']')+1:]
		valRust := goTypeToRust(valType, "", defTypes)
		return fmt.Sprintf("{ let mn = dec.read_map_header()?; let mut m: BTreeMap<String, %s> = BTreeMap::new(); for _ in 0..mn { let mk = dec.read_text()?; let mv = %s; m.insert(mk, mv); } m }",
			valRust, rustDecodeExpr(valType, valRust, defTypes))
	case strings.HasPrefix(goType, "[]"):
		elemType := goType[2:]
		elemRust := goTypeToRust(elemType, "", defTypes)
		return fmt.Sprintf("{ let sn = dec.read_array_header()?; let mut sv: Vec<%s> = Vec::with_capacity(sn); for _ in 0..sn { sv.push(%s); } sv }",
			elemRust, rustDecodeExpr(elemType, elemRust, defTypes))
	default:
		return fmt.Sprintf("%s::decode_cbor_from(dec)?", goType)
	}
}

// rustEmptyInit returns the empty-value initializer for a Rust field type, used
// as the decode-local seed for a non-required field (a missing field decodes to
// its empty value, matching the Go codec's omitempty decode).
func rustEmptyInit(rustTy string) string {
	switch {
	case rustTy == "String":
		return "String::new()"
	case rustTy == "i64":
		return "0"
	case rustTy == "f64":
		return "0.0"
	case rustTy == "bool":
		return "false"
	case strings.HasPrefix(rustTy, "Vec<"):
		return "Vec::new()"
	case strings.HasPrefix(rustTy, "BTreeMap<"):
		return "BTreeMap::new()"
	case strings.HasPrefix(rustTy, "Option<"):
		return "None"
	default:
		return rustTy + "::default()"
	}
}

// generateRustCBORHelpers produces the cbor_gen.rs helpers module: the shared
// CBOR read/write machinery, the runtime key sort, the float16 conversions, and a
// dependency-free base64 codec. Emitted once per crate; the per-type impls
// reference it as crate::cbor_gen::*.
func generateRustCBORHelpers(t codecTarget) ([]byte, error) {
	return []byte(schemaFreeGenHeader + rustCBORHelpersSrc), nil
}

// rustCBORHelpersSrc is the shared Rust CBOR machinery — the byte-for-byte twin of
// the Go cborHelpersSrc. No runtime CBOR/float/base64 crate is used.
const rustCBORHelpersSrc = `use serde_json::value::RawValue;

/// A canonical-CBOR encode/decode error.
#[derive(Debug)]
pub struct CborError(pub String);

impl std::fmt::Display for CborError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.0)
    }
}

impl std::error::Error for CborError {}

/// Appends a CBOR head: the major type in the top three bits and the shortest
/// length/argument encoding.
pub fn write_head(out: &mut Vec<u8>, major: u8, arg: u64) {
    let mb = major << 5;
    if arg < 24 {
        out.push(mb | arg as u8);
    } else if arg < 0x100 {
        out.push(mb | 24);
        out.push(arg as u8);
    } else if arg < 0x10000 {
        out.push(mb | 25);
        out.extend_from_slice(&(arg as u16).to_be_bytes());
    } else if arg < 0x1_0000_0000 {
        out.push(mb | 26);
        out.extend_from_slice(&(arg as u32).to_be_bytes());
    } else {
        out.push(mb | 27);
        out.extend_from_slice(&arg.to_be_bytes());
    }
}

/// Appends a signed integer as CBOR major type 0 (non-negative) or 1 (negative),
/// shortest head.
pub fn write_int(out: &mut Vec<u8>, i: i64) {
    if i < 0 {
        write_head(out, 1, (-1 - i) as u64);
    } else {
        write_head(out, 0, i as u64);
    }
}

/// Appends a text string (major 3): shortest length head then the UTF-8 bytes.
pub fn write_text(out: &mut Vec<u8>, s: &str) {
    write_head(out, 3, s.len() as u64);
    out.extend_from_slice(s.as_bytes());
}

/// Appends a byte string (major 2): shortest length head then the bytes.
pub fn write_bytes(out: &mut Vec<u8>, b: &[u8]) {
    write_head(out, 2, b.len() as u64);
    out.extend_from_slice(b);
}

/// Appends the fixed single-byte CBOR bool.
pub fn write_bool(out: &mut Vec<u8>, v: bool) {
    out.push(if v { 0xf5 } else { 0xf4 });
}

/// Appends f as a canonical CBOR float per CBOR CDE §4.2: the shortest of
/// float16/float32/float64 that round-trips to the exact value, with NaN
/// canonicalized to 0xf97e00, ±Inf to the float16 infinities, and ±0 preserved by
/// sign. No numeric reduction: an integer-valued float stays a float.
pub fn write_float(out: &mut Vec<u8>, f: f64) {
    if f.is_nan() {
        out.extend_from_slice(&[0xf9, 0x7e, 0x00]);
        return;
    }
    if f == f64::INFINITY {
        out.extend_from_slice(&[0xf9, 0x7c, 0x00]);
        return;
    }
    if f == f64::NEG_INFINITY {
        out.extend_from_slice(&[0xf9, 0xfc, 0x00]);
        return;
    }
    let f32v = f as f32;
    if f32v as f64 == f {
        if let Some(h) = f32_to_f16_exact(f32v.to_bits()) {
            out.push(0xf9);
            out.extend_from_slice(&h.to_be_bytes());
            return;
        }
        out.push(0xfa);
        out.extend_from_slice(&f32v.to_bits().to_be_bytes());
        return;
    }
    out.push(0xfb);
    out.extend_from_slice(&f.to_bits().to_be_bytes());
}

/// Converts float32 bits to a float16 if the value is EXACTLY representable (no
/// rounding), returning the 16-bit encoding. Used only to select the shortest
/// float that round-trips, so a lossy conversion returns None. ±0 preserves its
/// sign; Inf/NaN are handled by the caller.
fn f32_to_f16_exact(b: u32) -> Option<u16> {
    let sign = ((b >> 16) & 0x8000) as u16;
    let exp = ((b >> 23) & 0xff) as i32;
    let mant = b & 0x7fffff;
    if exp == 0 {
        if mant == 0 {
            return Some(sign); // ±0
        }
        return None; // float32 subnormal: not float16-exact
    }
    if exp == 0xff {
        return None; // Inf/NaN: handled by the caller
    }
    let e = exp - 127;
    if (-14..=15).contains(&e) {
        if mant & 0x1fff != 0 {
            return None; // low mantissa bits would be lost
        }
        return Some(sign | (((e + 15) as u16) << 10) | (mant >> 13) as u16);
    }
    if (-24..=-15).contains(&e) {
        let full = mant | 0x800000; // add the implicit leading 1
        let shift = (-(e + 1)) as u32;
        if full & ((1 << shift) - 1) != 0 {
            return None; // low bits would be lost
        }
        let hm = full >> shift;
        if hm == 0 || hm > 0x3ff {
            return None;
        }
        return Some(sign | hm as u16);
    }
    None
}

/// Expands a CBOR half-precision float to f64.
fn float16_to_f64(h: u16) -> f64 {
    let sign = ((h & 0x8000) as u32) << 16;
    let exp = (h >> 10) as u32 & 0x1f;
    let mut mant = (h & 0x3ff) as u32;
    match exp {
        0 => {
            if mant == 0 {
                return f32::from_bits(sign) as f64; // ±0
            }
            let mut e: u32 = 0;
            while mant & 0x400 == 0 {
                mant <<= 1;
                e += 1;
            }
            mant &= 0x3ff;
            let exp32 = 127 - 15 - e + 1;
            f32::from_bits(sign | (exp32 << 23) | (mant << 13)) as f64
        }
        0x1f => {
            if mant == 0 {
                f32::from_bits(sign | 0x7f80_0000) as f64 // ±Inf
            } else {
                f32::from_bits(sign | 0x7f80_0000 | (mant << 13)) as f64 // NaN
            }
        }
        _ => {
            let exp32 = exp + 127 - 15;
            f32::from_bits(sign | (exp32 << 23) | (mant << 13)) as f64
        }
    }
}

/// Sorts map keys into canonical CBOR order: by the bytewise order of their
/// encoded text-key bytes (length head first, then UTF-8 content).
pub fn sort_keys(keys: &mut Vec<&String>) {
    keys.sort_by(|a, b| less_key(a, b));
}

fn less_key(a: &str, b: &str) -> std::cmp::Ordering {
    if a.len() != b.len() {
        return str_head(a.len()).cmp(&str_head(b.len()));
    }
    a.cmp(b)
}

fn str_head(n: usize) -> Vec<u8> {
    if n < 24 {
        vec![0x60 | n as u8]
    } else if n < 0x100 {
        vec![0x78, n as u8]
    } else if n < 0x10000 {
        vec![0x79, (n >> 8) as u8, n as u8]
    } else {
        vec![0x7a, (n >> 24) as u8, (n >> 16) as u8, (n >> 8) as u8, n as u8]
    }
}

/// Builds a Box<RawValue> from verbatim opaque bytes (R10 decode): the bytes are
/// interpreted as UTF-8 JSON, matching the byte-string the encode side carried.
pub fn raw_from_bytes(b: Vec<u8>) -> Result<Box<RawValue>, CborError> {
    let s = String::from_utf8(b).map_err(|e| CborError(format!("cbor: opaque not utf8: {e}")))?;
    RawValue::from_string(s).map_err(|e| CborError(format!("cbor: opaque not valid json: {e}")))
}

const B64: &[u8; 64] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

/// Encodes bytes as standard (padded) base64, matching encoding/json's []byte
/// wire form so a decoded byte-string re-enters the base64-String field.
pub fn b64_encode(data: &[u8]) -> String {
    let mut out = String::with_capacity((data.len() + 2) / 3 * 4);
    for chunk in data.chunks(3) {
        let b0 = chunk[0] as u32;
        let b1 = if chunk.len() > 1 { chunk[1] as u32 } else { 0 };
        let b2 = if chunk.len() > 2 { chunk[2] as u32 } else { 0 };
        let n = (b0 << 16) | (b1 << 8) | b2;
        out.push(B64[((n >> 18) & 0x3f) as usize] as char);
        out.push(B64[((n >> 12) & 0x3f) as usize] as char);
        if chunk.len() > 1 {
            out.push(B64[((n >> 6) & 0x3f) as usize] as char);
        } else {
            out.push('=');
        }
        if chunk.len() > 2 {
            out.push(B64[(n & 0x3f) as usize] as char);
        } else {
            out.push('=');
        }
    }
    out
}

/// Decodes standard (padded) base64 into raw bytes.
pub fn b64_decode(s: &str) -> Result<Vec<u8>, CborError> {
    fn val(c: u8) -> Result<u32, CborError> {
        match c {
            b'A'..=b'Z' => Ok((c - b'A') as u32),
            b'a'..=b'z' => Ok((c - b'a' + 26) as u32),
            b'0'..=b'9' => Ok((c - b'0' + 52) as u32),
            b'+' => Ok(62),
            b'/' => Ok(63),
            _ => Err(CborError(format!("cbor: invalid base64 byte {c}"))),
        }
    }
    let bytes = s.as_bytes();
    let mut out = Vec::with_capacity(bytes.len() / 4 * 3);
    let mut i = 0;
    while i < bytes.len() {
        if bytes.len() - i < 4 {
            return Err(CborError("cbor: truncated base64".to_string()));
        }
        let c0 = val(bytes[i])?;
        let c1 = val(bytes[i + 1])?;
        let n2 = bytes[i + 2] == b'=';
        let n3 = bytes[i + 3] == b'=';
        let c2 = if n2 { 0 } else { val(bytes[i + 2])? };
        let c3 = if n3 { 0 } else { val(bytes[i + 3])? };
        let n = (c0 << 18) | (c1 << 12) | (c2 << 6) | c3;
        out.push((n >> 16) as u8);
        if !n2 {
            out.push((n >> 8) as u8);
        }
        if !n3 {
            out.push(n as u8);
        }
        i += 4;
    }
    Ok(out)
}

/// A minimal canonical-CBOR reader over a byte slice. It reads only the
/// definite-length items the codec produces; indefinite lengths and unsupported
/// majors are rejected loudly.
pub struct CborDec<'a> {
    data: &'a [u8],
    pos: usize,
}

impl<'a> CborDec<'a> {
    pub fn new(data: &'a [u8]) -> Self {
        CborDec { data, pos: 0 }
    }

    fn read_head(&mut self) -> Result<(u8, u8, u64), CborError> {
        if self.pos >= self.data.len() {
            return Err(CborError("cbor: unexpected end of input".to_string()));
        }
        let ib = self.data[self.pos];
        self.pos += 1;
        let major = ib >> 5;
        let ai = ib & 0x1f;
        let arg = match ai {
            0..=23 => ai as u64,
            24 => {
                self.need(1)?;
                let v = self.data[self.pos] as u64;
                self.pos += 1;
                v
            }
            25 => {
                self.need(2)?;
                let v = ((self.data[self.pos] as u64) << 8) | self.data[self.pos + 1] as u64;
                self.pos += 2;
                v
            }
            26 => {
                self.need(4)?;
                let mut v: u64 = 0;
                for k in 0..4 {
                    v = (v << 8) | self.data[self.pos + k] as u64;
                }
                self.pos += 4;
                v
            }
            27 => {
                self.need(8)?;
                let mut v: u64 = 0;
                for k in 0..8 {
                    v = (v << 8) | self.data[self.pos + k] as u64;
                }
                self.pos += 8;
                v
            }
            _ => return Err(CborError(format!("cbor: reserved additional info {ai}"))),
        };
        Ok((major, ai, arg))
    }

    fn need(&self, n: usize) -> Result<(), CborError> {
        if self.pos + n > self.data.len() {
            return Err(CborError("cbor: truncated argument".to_string()));
        }
        Ok(())
    }

    pub fn read_map_header(&mut self) -> Result<usize, CborError> {
        let (major, _, arg) = self.read_head()?;
        if major != 5 {
            return Err(CborError(format!("cbor: expected map, got major {major}")));
        }
        Ok(arg as usize)
    }

    pub fn read_array_header(&mut self) -> Result<usize, CborError> {
        let (major, _, arg) = self.read_head()?;
        if major != 4 {
            return Err(CborError(format!("cbor: expected array, got major {major}")));
        }
        Ok(arg as usize)
    }

    pub fn read_text(&mut self) -> Result<String, CborError> {
        let (major, _, arg) = self.read_head()?;
        if major != 3 {
            return Err(CborError(format!("cbor: expected text string, got major {major}")));
        }
        let n = arg as usize;
        self.need(n)?;
        let s = std::str::from_utf8(&self.data[self.pos..self.pos + n])
            .map_err(|e| CborError(format!("cbor: text not utf8: {e}")))?
            .to_string();
        self.pos += n;
        Ok(s)
    }

    pub fn read_bytes(&mut self) -> Result<Vec<u8>, CborError> {
        let (major, _, arg) = self.read_head()?;
        if major != 2 {
            return Err(CborError(format!("cbor: expected byte string, got major {major}")));
        }
        let n = arg as usize;
        self.need(n)?;
        let out = self.data[self.pos..self.pos + n].to_vec();
        self.pos += n;
        Ok(out)
    }

    pub fn read_int(&mut self) -> Result<i64, CborError> {
        let (major, _, arg) = self.read_head()?;
        match major {
            0 => Ok(arg as i64),
            1 => Ok(-1 - arg as i64),
            _ => Err(CborError(format!("cbor: expected integer, got major {major}"))),
        }
    }

    pub fn read_float(&mut self) -> Result<f64, CborError> {
        let (major, ai, arg) = self.read_head()?;
        if major != 7 {
            return Err(CborError(format!("cbor: expected float, got major {major}")));
        }
        match ai {
            25 => Ok(float16_to_f64(arg as u16)),
            26 => Ok(f32::from_bits(arg as u32) as f64),
            27 => Ok(f64::from_bits(arg)),
            _ => Err(CborError(format!("cbor: expected float, got additional info {ai}"))),
        }
    }

    pub fn read_bool(&mut self) -> Result<bool, CborError> {
        let (major, ai, _) = self.read_head()?;
        if major != 7 || (ai != 20 && ai != 21) {
            return Err(CborError("cbor: expected bool".to_string()));
        }
        Ok(ai == 21)
    }

    /// Consumes exactly one CBOR item (recursively for arrays and maps),
    /// discarding it — used for an unknown map key's value.
    pub fn skip(&mut self) -> Result<(), CborError> {
        let (major, _, arg) = self.read_head()?;
        match major {
            0 | 1 | 7 => Ok(()),
            2 | 3 => {
                let n = arg as usize;
                self.need(n)?;
                self.pos += n;
                Ok(())
            }
            4 => {
                for _ in 0..arg {
                    self.skip()?;
                }
                Ok(())
            }
            5 => {
                for _ in 0..arg * 2 {
                    self.skip()?;
                }
                Ok(())
            }
            _ => Err(CborError(format!("cbor: cannot skip major {major}"))),
        }
    }
}
`
