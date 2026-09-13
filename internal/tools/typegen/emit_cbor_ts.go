// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"sort"
	"strings"
)

// This file is the TypeScript CBOR emission fork, the cross-language twin of the
// Go fork in emit_cbor.go and the Rust fork in emit_cbor_rust.go. It emits a
// dependency-free canonical-CBOR codec (RFC 8949 §4.2.1; floats per CBOR CDE §4.2)
// for each generated interface: encodeCBOR<T> / decodeCBOR<T> plus the append and
// decode workers they drive. No runtime CBOR/float library is used — the shared
// machinery lives in the generated cbor_gen.ts module (tsCBORHelpersSrc), and the
// canonical rules match the Go and Rust forks byte-for-byte.

// tsCBORFieldType returns the Go-vocabulary spelling the CBOR fork encodes a
// field through.
//
// A value the schema also declares as null takes the opaque arm. The structural
// arms have no null to write, and the reader has no way to tell a null from the
// item it was expecting — this fork's reader exposes no way to look at the next
// item's major type before consuming it — so a structural pair could encode a
// null it could never read back. The opaque arm carries either form and reads
// back exactly what it wrote, which is the same answer the fork already gives
// any value it has no single CBOR shape for.
func tsCBORFieldType(f codecField) string {
	if f.nullable {
		return "any"
	}
	return f.goType
}

// emitTSCBORValue writes the CBOR encode/decode pair for a def that declares a
// value rather than an object, through the same value arms a field of that
// shape takes.
func emitTSCBORValue(b *strings.Builder, typeName string, v tsDefValue, defTypes map[string]bool) {
	fmt.Fprintf(b, "export function encodeCBOR%s(v: %s): Uint8Array {\n", typeName, typeName)
	b.WriteString("  const out: number[] = [];\n")
	fmt.Fprintf(b, "  appendCBOR%s(out, v);\n", typeName)
	b.WriteString("  return new Uint8Array(out);\n")
	b.WriteString("}\n\n")

	fmt.Fprintf(b, "export function appendCBOR%s(out: number[], v: %s): void {\n", typeName, typeName)
	emitTSCBOREncodeValue(b, "v", v.goShape, "  ", defTypes, 0)
	b.WriteString("}\n\n")

	fmt.Fprintf(b, "export function decodeCBOR%s(data: Uint8Array): %s {\n", typeName, typeName)
	b.WriteString("  const dec = new CborDec(data);\n")
	fmt.Fprintf(b, "  return decodeCBOR%sFrom(dec);\n", typeName)
	b.WriteString("}\n\n")

	fmt.Fprintf(b, "export function decodeCBOR%sFrom(dec: CborDec): %s {\n", typeName, typeName)
	fmt.Fprintf(b, "  return %s as %s;\n", tsDecodeExpr(v.goShape, defTypes), typeName)
	b.WriteString("}\n\n")
}

// emitTSCBOR writes the CBOR encode/decode functions for one def interface.
// Encode emits fields in canonical key order fixed here; decode switches on the
// wire key and is order-independent.
func emitTSCBOR(b *strings.Builder, typeName string, n schemaNode, vocab map[string]string, defTypes map[string]bool) {
	fields := resolveCodecFields(typeName, n, vocab)
	sorted := append([]codecField(nil), fields...)
	sort.SliceStable(sorted, func(i, j int) bool { return cborKeyLess(sorted[i].key, sorted[j].key) })

	// encode
	fmt.Fprintf(b, "export function encodeCBOR%s(v: %s): Uint8Array {\n", typeName, typeName)
	b.WriteString("  const out: number[] = [];\n")
	fmt.Fprintf(b, "  appendCBOR%s(out, v);\n", typeName)
	b.WriteString("  return new Uint8Array(out);\n")
	b.WriteString("}\n\n")

	fmt.Fprintf(b, "export function appendCBOR%s(out: number[], v: %s): void {\n", typeName, typeName)
	nRequired := 0
	for _, f := range sorted {
		if f.required || tsOmitGuard("v."+tsFieldAccess(f.key), tsCBORFieldType(f), f.nullable) == "" {
			nRequired++
		}
	}
	fmt.Fprintf(b, "  let n = %d;\n", nRequired)
	for _, f := range sorted {
		if f.required {
			continue
		}
		guard := tsOmitGuard("v."+tsFieldAccess(f.key), tsCBORFieldType(f), f.nullable)
		if guard != "" {
			fmt.Fprintf(b, "  if (%s) n++;\n", guard)
		}
	}
	b.WriteString("  writeHead(out, 5, n);\n")
	for _, f := range sorted {
		emitTSCBOREncodeField(b, f, defTypes)
	}
	b.WriteString("}\n\n")

	// decode
	fmt.Fprintf(b, "export function decodeCBOR%s(data: Uint8Array): %s {\n", typeName, typeName)
	b.WriteString("  const dec = new CborDec(data);\n")
	fmt.Fprintf(b, "  return decodeCBOR%sFrom(dec);\n", typeName)
	b.WriteString("}\n\n")

	fmt.Fprintf(b, "export function decodeCBOR%sFrom(dec: CborDec): %s {\n", typeName, typeName)
	b.WriteString("  const o: any = {};\n")
	b.WriteString("  const n = dec.readMapHeader();\n")
	b.WriteString("  for (let i = 0; i < n; i++) {\n")
	b.WriteString("    const k = dec.readText();\n")
	b.WriteString("    switch (k) {\n")
	for _, f := range fields {
		fmt.Fprintf(b, "      case %q: o[%q] = %s; break;\n", f.key, f.key, tsDecodeExpr(tsCBORFieldType(f), defTypes))
	}
	b.WriteString("      default: dec.skip();\n")
	b.WriteString("    }\n")
	b.WriteString("  }\n")
	fmt.Fprintf(b, "  return o as %s;\n", typeName)
	b.WriteString("}\n\n")
}

// emitTSCBOREncodeField appends one field's canonical key text-string and value,
// guarding a non-required field by its omit predicate.
func emitTSCBOREncodeField(b *strings.Builder, f codecField, defTypes map[string]bool) {
	acc := "v." + tsFieldAccess(f.key)
	emit := func(indent string) {
		fmt.Fprintf(b, "%swriteText(out, %q);\n", indent, f.key)
		emitTSCBOREncodeValue(b, acc, tsCBORFieldType(f), indent, defTypes, 0)
	}
	if f.required {
		emit("  ")
		return
	}
	guard := tsOmitGuard(acc, tsCBORFieldType(f), f.nullable)
	if guard == "" {
		emit("  ")
		return
	}
	fmt.Fprintf(b, "  if (%s) {\n", guard)
	emit("    ")
	b.WriteString("  }\n")
}

// emitTSCBOREncodeValue appends statements encoding the value at acc of the given
// resolved Go type to out. depth disambiguates the block-local bindings of nested
// maps and slices so an inner binding does not shadow (and self-reference) an
// outer one.
func emitTSCBOREncodeValue(b *strings.Builder, acc, goType, indent string, defTypes map[string]bool, depth int) {
	switch {
	case goType == "string":
		fmt.Fprintf(b, "%swriteText(out, %s);\n", indent, acc)
	case goType == "int" || goType == "int64":
		fmt.Fprintf(b, "%swriteInt(out, %s);\n", indent, acc)
	case goType == "float64":
		fmt.Fprintf(b, "%swriteFloat(out, %s);\n", indent, acc)
	case goType == "bool":
		fmt.Fprintf(b, "%swriteBool(out, %s);\n", indent, acc)
	case goType == "[]byte":
		fmt.Fprintf(b, "%swriteBytes(out, b64decode(%s));\n", indent, acc)
	case goType == "json.RawMessage" || goType == "any" || goType == "interface{}":
		fmt.Fprintf(b, "%swriteBytes(out, utf8Encode(JSON.stringify(%s)));\n", indent, acc)
	case strings.HasPrefix(goType, "*"):
		emitTSCBOREncodeValue(b, acc, goType[1:], indent, defTypes, depth)
	case defTypes[goType]:
		fmt.Fprintf(b, "%sappendCBOR%s(out, %s);\n", indent, goType, acc)
	case strings.HasPrefix(goType, "map["):
		valType := goType[strings.IndexByte(goType, ']')+1:]
		emitTSCBOREncodeMap(b, acc, valType, indent, defTypes, depth)
	case strings.HasPrefix(goType, "[]"):
		emitTSCBOREncodeSlice(b, acc, goType[2:], indent, defTypes, depth)
	default:
		fmt.Fprintf(b, "%swriteBytes(out, utf8Encode(JSON.stringify(%s)));\n", indent, acc)
	}
}

// emitTSCBOREncodeMap appends a plain object as a canonical CBOR map: the keys
// sort at runtime by their encoded text-key bytes (not JS string order), then
// each value is encoded. The bindings are depth-suffixed so a nested map does not
// shadow (and self-reference in its initializer) the enclosing one.
func emitTSCBOREncodeMap(b *strings.Builder, acc, valType, indent string, defTypes map[string]bool, depth int) {
	m := fmt.Sprintf("m%d", depth)
	ks := fmt.Sprintf("ks%d", depth)
	kk := fmt.Sprintf("kk%d", depth)
	fmt.Fprintf(b, "%s{\n", indent)
	fmt.Fprintf(b, "%s  const %s = %s;\n", indent, m, acc)
	fmt.Fprintf(b, "%s  const %s = Object.keys(%s); sortKeys(%s);\n", indent, ks, m, ks)
	fmt.Fprintf(b, "%s  writeHead(out, 5, %s.length);\n", indent, ks)
	fmt.Fprintf(b, "%s  for (const %s of %s) {\n", indent, kk, ks)
	fmt.Fprintf(b, "%s    writeText(out, %s);\n", indent, kk)
	emitTSCBOREncodeValue(b, fmt.Sprintf("%s[%s]", m, kk), valType, indent+"    ", defTypes, depth+1)
	fmt.Fprintf(b, "%s  }\n", indent)
	fmt.Fprintf(b, "%s}\n", indent)
}

// emitTSCBOREncodeSlice appends an array as a canonical CBOR array in element
// order.
func emitTSCBOREncodeSlice(b *strings.Builder, acc, elemType, indent string, defTypes map[string]bool, depth int) {
	a := fmt.Sprintf("a%d", depth)
	ee := fmt.Sprintf("ee%d", depth)
	fmt.Fprintf(b, "%s{\n", indent)
	fmt.Fprintf(b, "%s  const %s = %s;\n", indent, a, acc)
	fmt.Fprintf(b, "%s  writeHead(out, 4, %s.length);\n", indent, a)
	fmt.Fprintf(b, "%s  for (const %s of %s) {\n", indent, ee, a)
	emitTSCBOREncodeValue(b, ee, elemType, indent+"    ", defTypes, depth+1)
	fmt.Fprintf(b, "%s  }\n", indent)
	fmt.Fprintf(b, "%s}\n", indent)
}

// tsDecodeExpr returns a TS expression decoding the next CBOR item into a value
// of the field's type. Maps and slices use an inline IIFE that reads the header
// and loops, so a nested composite decodes correctly.
func tsDecodeExpr(goType string, defTypes map[string]bool) string {
	switch {
	case goType == "string":
		return "dec.readText()"
	case goType == "int" || goType == "int64":
		return "dec.readInt()"
	case goType == "float64":
		return "dec.readFloat()"
	case goType == "bool":
		return "dec.readBool()"
	case goType == "[]byte":
		return "b64encode(dec.readBytes())"
	case goType == "json.RawMessage" || goType == "any" || goType == "interface{}":
		return "JSON.parse(utf8Decode(dec.readBytes()))"
	case strings.HasPrefix(goType, "*"):
		return tsDecodeExpr(goType[1:], defTypes)
	case defTypes[goType]:
		return fmt.Sprintf("decodeCBOR%sFrom(dec)", goType)
	case strings.HasPrefix(goType, "map["):
		valType := goType[strings.IndexByte(goType, ']')+1:]
		return fmt.Sprintf("(() => { const m: any = {}; const mn = dec.readMapHeader(); for (let j = 0; j < mn; j++) { const mk = dec.readText(); m[mk] = %s; } return m; })()", tsDecodeExpr(valType, defTypes))
	case strings.HasPrefix(goType, "[]"):
		return fmt.Sprintf("(() => { const a: any[] = []; const sn = dec.readArrayHeader(); for (let j = 0; j < sn; j++) { a.push(%s); } return a; })()", tsDecodeExpr(goType[2:], defTypes))
	default:
		return "JSON.parse(utf8Decode(dec.readBytes()))"
	}
}

// tsCBORExternalImports returns the import statements a module needs for the CBOR
// append/decode workers of external def types (types emitted by a sibling
// module). Mirrors the JSON externalDefType import wiring.
func tsCBORExternalImports(t codecTarget, structSrc string) string {
	var b strings.Builder
	for _, ext := range t.externalDefTypes {
		mod, ok := externalDefModuleTS[ext]
		if !ok {
			continue
		}
		if strings.Contains(structSrc, "appendCBOR"+ext) || strings.Contains(structSrc, "decodeCBOR"+ext+"From") {
			fmt.Fprintf(&b, "import { appendCBOR%s, decodeCBOR%sFrom } from \"./%s.ts\";\n", ext, ext, mod)
		}
	}
	return b.String()
}

// generateTSCBORHelpers produces the cbor_gen.ts helpers module: the shared CBOR
// read/write machinery, the runtime key sort, the float16 conversions, a
// dependency-free base64 codec, and UTF-8 helpers. Emitted once per package; the
// per-type codecs import it.
func generateTSCBORHelpers(t codecTarget) ([]byte, error) {
	var b strings.Builder
	b.WriteString("// SPDX-License-Identifier: Apache-2.0\n// Copyright 2026 Cognitio S.r.l.\n\n")
	b.WriteString("// Code generated by internal/tools/typegen. DO NOT EDIT.\n")
	b.WriteString("// Regenerate with `go generate ./internal/tools/typegen` from the\n// aggrega-cli checkout.\n\n")
	b.WriteString(tsCBORHelpersSrc)
	return []byte(b.String()), nil
}

// tsCBORHelpersSrc is the shared TypeScript CBOR machinery — the byte-for-byte
// twin of the Go cborHelpersSrc and the Rust rustCBORHelpersSrc. No runtime
// CBOR/float/base64 library is used.
const tsCBORHelpersSrc = `// TextEncoder / TextDecoder are runtime globals in Node, browsers, and Wasm-JS
// hosts; declared here so the codec type-checks without pulling in the DOM lib.
declare const TextEncoder: { new (): { encode(input: string): Uint8Array } };
declare const TextDecoder: { new (): { decode(input: Uint8Array): string } };

const B64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

// writeHead appends a CBOR head: the major type in the top three bits and the
// shortest length/argument encoding. arg is a non-negative integer within the
// safe-integer range (socket lengths and values never exceed it).
export function writeHead(out: number[], major: number, arg: number): void {
  const mb = major << 5;
  if (arg < 24) {
    out.push(mb | arg);
  } else if (arg < 0x100) {
    out.push(mb | 24, arg & 0xff);
  } else if (arg < 0x10000) {
    out.push(mb | 25, (arg >> 8) & 0xff, arg & 0xff);
  } else if (arg < 0x100000000) {
    out.push(mb | 26, (arg / 0x1000000) & 0xff, (arg >> 16) & 0xff, (arg >> 8) & 0xff, arg & 0xff);
  } else {
    const hi = Math.floor(arg / 0x100000000);
    const lo = arg >>> 0;
    out.push(
      mb | 27,
      (hi >> 24) & 0xff, (hi >> 16) & 0xff, (hi >> 8) & 0xff, hi & 0xff,
      (lo >>> 24) & 0xff, (lo >> 16) & 0xff, (lo >> 8) & 0xff, lo & 0xff,
    );
  }
}

// writeInt appends a signed integer as CBOR major type 0 (non-negative) or 1
// (negative), shortest head.
export function writeInt(out: number[], i: number): void {
  if (i < 0) {
    writeHead(out, 1, -1 - i);
  } else {
    writeHead(out, 0, i);
  }
}

const TE = new TextEncoder();
const TD = new TextDecoder();

export function utf8Encode(s: string): Uint8Array {
  return TE.encode(s);
}

export function utf8Decode(b: Uint8Array | number[]): string {
  return TD.decode(b instanceof Uint8Array ? b : new Uint8Array(b));
}

// writeText appends a text string (major 3): shortest length head then the UTF-8
// bytes. The length head is the byte length, not the JS string length.
export function writeText(out: number[], s: string): void {
  const b = utf8Encode(s);
  writeHead(out, 3, b.length);
  for (const x of b) out.push(x);
}

// writeBytes appends a byte string (major 2): shortest length head then bytes.
export function writeBytes(out: number[], b: Uint8Array | number[]): void {
  writeHead(out, 2, b.length);
  for (const x of b) out.push(x);
}

// writeBool appends the fixed single-byte CBOR bool.
export function writeBool(out: number[], v: boolean): void {
  out.push(v ? 0xf5 : 0xf4);
}

const f32buf = new ArrayBuffer(4);
const f32dv = new DataView(f32buf);
const f64buf = new ArrayBuffer(8);
const f64dv = new DataView(f64buf);

function f32Bits(f: number): number {
  f32dv.setFloat32(0, f);
  return f32dv.getUint32(0);
}

// writeFloat appends f as a canonical CBOR float per CBOR CDE §4.2: the shortest
// of float16/float32/float64 that round-trips to the exact value, with NaN
// canonicalized to 0xf97e00, ±Inf to the float16 infinities, and ±0 preserved by
// sign. No numeric reduction: an integer-valued float stays a float.
export function writeFloat(out: number[], f: number): void {
  if (Number.isNaN(f)) {
    out.push(0xf9, 0x7e, 0x00);
    return;
  }
  if (f === Infinity) {
    out.push(0xf9, 0x7c, 0x00);
    return;
  }
  if (f === -Infinity) {
    out.push(0xf9, 0xfc, 0x00);
    return;
  }
  const f32 = Math.fround(f);
  if (f32 === f && Object.is(Math.fround(f), f)) {
    const bits32 = f32Bits(f32);
    const h = f32ToF16Exact(bits32);
    if (h !== null) {
      out.push(0xf9, (h >> 8) & 0xff, h & 0xff);
      return;
    }
    out.push(0xfa, (bits32 >>> 24) & 0xff, (bits32 >> 16) & 0xff, (bits32 >> 8) & 0xff, bits32 & 0xff);
    return;
  }
  f64dv.setFloat64(0, f);
  out.push(
    0xfb,
    f64dv.getUint8(0), f64dv.getUint8(1), f64dv.getUint8(2), f64dv.getUint8(3),
    f64dv.getUint8(4), f64dv.getUint8(5), f64dv.getUint8(6), f64dv.getUint8(7),
  );
}

// f32ToF16Exact converts float32 bits to a float16 if the value is EXACTLY
// representable (no rounding), returning the 16-bit encoding or null. ±0
// preserves its sign; Inf/NaN are handled by the caller.
function f32ToF16Exact(b: number): number | null {
  const sign = (b >>> 16) & 0x8000;
  const exp = (b >>> 23) & 0xff;
  const mant = b & 0x7fffff;
  if (exp === 0) {
    if (mant === 0) return sign; // ±0
    return null; // float32 subnormal: not float16-exact
  }
  if (exp === 0xff) return null; // Inf/NaN: handled by the caller
  const e = exp - 127;
  if (e >= -14 && e <= 15) {
    if ((mant & 0x1fff) !== 0) return null; // low mantissa bits would be lost
    return sign | ((e + 15) << 10) | (mant >> 13);
  }
  if (e >= -24 && e <= -15) {
    const full = mant | 0x800000; // implicit leading 1
    const shift = -(e + 1);
    if ((full & ((1 << shift) - 1)) !== 0) return null; // low bits would be lost
    const hm = full >>> shift;
    if (hm === 0 || hm > 0x3ff) return null;
    return sign | hm;
  }
  return null;
}

// float16ToNumber expands a CBOR half-precision float to a JS number.
function float16ToNumber(h: number): number {
  const sign = (h & 0x8000) << 16;
  const exp = (h >> 10) & 0x1f;
  let mant = h & 0x3ff;
  if (exp === 0) {
    if (mant === 0) {
      f32dv.setUint32(0, sign >>> 0);
      return f32dv.getFloat32(0); // ±0
    }
    let e = 0;
    while ((mant & 0x400) === 0) {
      mant <<= 1;
      e++;
    }
    mant &= 0x3ff;
    const exp32 = 127 - 15 - e + 1;
    f32dv.setUint32(0, (sign | (exp32 << 23) | (mant << 13)) >>> 0);
    return f32dv.getFloat32(0);
  }
  if (exp === 0x1f) {
    if (mant === 0) {
      f32dv.setUint32(0, (sign | 0x7f800000) >>> 0);
      return f32dv.getFloat32(0); // ±Inf
    }
    f32dv.setUint32(0, (sign | 0x7f800000 | (mant << 13)) >>> 0);
    return f32dv.getFloat32(0); // NaN
  }
  const exp32 = exp + 127 - 15;
  f32dv.setUint32(0, (sign | (exp32 << 23) | (mant << 13)) >>> 0);
  return f32dv.getFloat32(0);
}

// sortKeys sorts map keys into canonical CBOR order: by their encoded text-key
// bytes (UTF-8 byte length first, then the UTF-8 bytes), matching the Go and Rust
// codecs — not JS string order, which compares UTF-16 code units.
export function sortKeys(keys: string[]): void {
  const enc = new Map<string, Uint8Array>();
  const b = (k: string): Uint8Array => {
    let e = enc.get(k);
    if (e === undefined) {
      e = utf8Encode(k);
      enc.set(k, e);
    }
    return e;
  };
  keys.sort((x, y) => {
    const bx = b(x);
    const by = b(y);
    if (bx.length !== by.length) return bx.length - by.length;
    for (let i = 0; i < bx.length; i++) {
      if (bx[i] !== by[i]) return bx[i] - by[i];
    }
    return 0;
  });
}

function b64Val(c: number): number {
  if (c >= 65 && c <= 90) return c - 65;
  if (c >= 97 && c <= 122) return c - 97 + 26;
  if (c >= 48 && c <= 57) return c - 48 + 52;
  if (c === 43) return 62;
  if (c === 47) return 63;
  throw new Error("cbor: invalid base64 byte " + c);
}

// b64decode decodes standard (padded) base64 into raw bytes, matching
// encoding/json's []byte wire form.
export function b64decode(s: string): number[] {
  const out: number[] = [];
  let i = 0;
  while (i < s.length) {
    if (s.length - i < 4) throw new Error("cbor: truncated base64");
    const c0 = b64Val(s.charCodeAt(i));
    const c1 = b64Val(s.charCodeAt(i + 1));
    const p2 = s.charCodeAt(i + 2) === 61;
    const p3 = s.charCodeAt(i + 3) === 61;
    const c2 = p2 ? 0 : b64Val(s.charCodeAt(i + 2));
    const c3 = p3 ? 0 : b64Val(s.charCodeAt(i + 3));
    const nn = (c0 << 18) | (c1 << 12) | (c2 << 6) | c3;
    out.push((nn >> 16) & 0xff);
    if (!p2) out.push((nn >> 8) & 0xff);
    if (!p3) out.push(nn & 0xff);
    i += 4;
  }
  return out;
}

// b64encode encodes raw bytes as standard (padded) base64.
export function b64encode(data: number[] | Uint8Array): string {
  let out = "";
  for (let i = 0; i < data.length; i += 3) {
    const b0 = data[i];
    const b1 = i + 1 < data.length ? data[i + 1] : 0;
    const b2 = i + 2 < data.length ? data[i + 2] : 0;
    const nn = (b0 << 16) | (b1 << 8) | b2;
    out += B64[(nn >> 18) & 0x3f];
    out += B64[(nn >> 12) & 0x3f];
    out += i + 1 < data.length ? B64[(nn >> 6) & 0x3f] : "=";
    out += i + 2 < data.length ? B64[nn & 0x3f] : "=";
  }
  return out;
}

// CborDec is a minimal canonical-CBOR reader over a byte array. It reads only the
// definite-length items the codec produces; indefinite lengths and unsupported
// majors are rejected loudly.
export class CborDec {
  private data: Uint8Array;
  private pos = 0;

  constructor(data: Uint8Array) {
    this.data = data;
  }

  private readHead(): [number, number, number] {
    if (this.pos >= this.data.length) throw new Error("cbor: unexpected end of input");
    const ib = this.data[this.pos];
    this.pos++;
    const major = ib >> 5;
    const ai = ib & 0x1f;
    if (ai < 24) return [major, ai, ai];
    if (ai === 24) {
      this.need(1);
      const v = this.data[this.pos];
      this.pos += 1;
      return [major, ai, v];
    }
    if (ai === 25) {
      this.need(2);
      const v = (this.data[this.pos] << 8) | this.data[this.pos + 1];
      this.pos += 2;
      return [major, ai, v];
    }
    if (ai === 26) {
      this.need(4);
      const v =
        this.data[this.pos] * 0x1000000 +
        (this.data[this.pos + 1] << 16) +
        (this.data[this.pos + 2] << 8) +
        this.data[this.pos + 3];
      this.pos += 4;
      return [major, ai, v];
    }
    if (ai === 27) {
      this.need(8);
      let v = 0;
      for (let k = 0; k < 8; k++) v = v * 256 + this.data[this.pos + k];
      this.pos += 8;
      return [major, ai, v];
    }
    throw new Error("cbor: reserved additional info " + ai);
  }

  private need(n: number): void {
    if (this.pos + n > this.data.length) throw new Error("cbor: truncated argument");
  }

  readMapHeader(): number {
    const [major, , arg] = this.readHead();
    if (major !== 5) throw new Error("cbor: expected map, got major " + major);
    return arg;
  }

  readArrayHeader(): number {
    const [major, , arg] = this.readHead();
    if (major !== 4) throw new Error("cbor: expected array, got major " + major);
    return arg;
  }

  readText(): string {
    const [major, , arg] = this.readHead();
    if (major !== 3) throw new Error("cbor: expected text string, got major " + major);
    this.need(arg);
    const s = utf8Decode(this.data.subarray(this.pos, this.pos + arg));
    this.pos += arg;
    return s;
  }

  readBytes(): number[] {
    const [major, , arg] = this.readHead();
    if (major !== 2) throw new Error("cbor: expected byte string, got major " + major);
    this.need(arg);
    const out = Array.from(this.data.subarray(this.pos, this.pos + arg));
    this.pos += arg;
    return out;
  }

  readInt(): number {
    const [major, , arg] = this.readHead();
    if (major === 0) return arg;
    if (major === 1) return -1 - arg;
    throw new Error("cbor: expected integer, got major " + major);
  }

  readFloat(): number {
    const [major, ai, arg] = this.readHead();
    if (major !== 7) throw new Error("cbor: expected float, got major " + major);
    if (ai === 25) return float16ToNumber(arg);
    if (ai === 26) {
      f32dv.setUint32(0, arg >>> 0);
      return f32dv.getFloat32(0);
    }
    if (ai === 27) {
      for (let k = 0; k < 8; k++) f64dv.setUint8(k, (this.data[this.pos - 8 + k]));
      return f64dv.getFloat64(0);
    }
    throw new Error("cbor: expected float, got additional info " + ai);
  }

  readBool(): boolean {
    const [major, ai] = this.readHead();
    if (major !== 7 || (ai !== 20 && ai !== 21)) throw new Error("cbor: expected bool");
    return ai === 21;
  }

  // skip consumes exactly one CBOR item (recursively for arrays and maps).
  skip(): void {
    const [major, , arg] = this.readHead();
    switch (major) {
      case 0:
      case 1:
      case 7:
        return;
      case 2:
      case 3:
        this.need(arg);
        this.pos += arg;
        return;
      case 4:
        for (let k = 0; k < arg; k++) this.skip();
        return;
      case 5:
        for (let k = 0; k < arg * 2; k++) this.skip();
        return;
      default:
        throw new Error("cbor: cannot skip major " + major);
    }
  }
}
`
