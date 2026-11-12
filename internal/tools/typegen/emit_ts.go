// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"fmt"
	"strconv"
	"strings"
)

// externalDefModuleTS maps an external def type (a type emitted by a sibling
// codec target and referenced across modules) to the TypeScript module basename
// that emits it. The Go and Rust backends resolve a sibling type through the
// package/crate root, but a TS import names the source module explicitly, so a
// cross-module external def type is registered here.
var externalDefModuleTS = map[string]string{
	"IRGraph":    "ir_gen",
	"Diagnostic": "diagnostic_gen",
}

// tsDefValue describes a `$defs` entry the module publishes as a VALUE rather
// than as an object.
type tsDefValue struct {
	// members are the TypeScript member types the alias is declared as, in the
	// order the schema declares them: one literal for a fixed value, one per
	// member for a closed vocabulary, one per type name for a value the
	// transport admits in more than one shape.
	members []string
	// goShape is the Go-vocabulary spelling of the value's runtime shape, and it
	// is what routes the encoders through this module's existing value arms
	// instead of a second encoding vocabulary written beside them. A value with
	// more than one runtime shape, or one the schema also admits as null,
	// resolves to the opaque spelling — the arms that spelling selects carry
	// either and read back what they wrote.
	goShape string
}

func (v tsDefValue) isValue() bool { return len(v.members) > 0 }

// tsScalarSpellings maps a JSON Schema scalar type name to its TypeScript
// spelling and to the Go-vocabulary spelling of its runtime shape. A name
// absent here is not a scalar, and a def declaring one keeps the interface
// emission.
var tsScalarSpellings = map[string]struct{ ts, goShape string }{
	"string":  {"string", "string"},
	"number":  {"number", "float64"},
	"integer": {"number", "int"},
	"boolean": {"boolean", "bool"},
	jsonNull:  {"null", "any"},
}

// tsDefValueOf reports how a `$defs` entry is published when it declares a
// value rather than an object, and the zero value for a def the interface
// emission already handles.
//
// The module builds one interface per def out of that def's `properties`, so a
// def declaring none emits an interface with no members — which for a scalar is
// not merely imprecise but false in a way that reaches the wire: the encoder
// built from an empty field list encodes every value as an empty object, so a
// message envelope whose version, identifier and method name are each such a
// def puts none of the three on the wire.
//
// Three readings are taken, in this order, because each is narrower than the
// next and the narrowest true statement is the one worth publishing: a fixed
// value becomes the literal it fixes, a closed vocabulary becomes the union of
// its members, and a value the schema types only by scalar names becomes those
// names. An object is left alone deliberately — a def declaring an object with
// no properties really is an empty object, and the interface says so correctly.
func tsDefValueOf(n schemaNode) tsDefValue {
	if n.GoType != "" || n.Ref != "" || len(n.props) > 0 || len(n.Types) == 0 {
		return tsDefValue{}
	}
	for _, t := range n.Types {
		if _, ok := tsScalarSpellings[t]; !ok {
			return tsDefValue{}
		}
	}
	// A fixed value and a closed vocabulary are both string vocabularies the
	// schema states outright, and both are narrower than the type name beside
	// them, so each wins over the mechanical scalar reading below.
	if n.Const != "" {
		return tsDefValue{members: []string{strconv.Quote(n.Const)}, goShape: "string"}
	}
	if len(n.Enum) > 0 {
		members := make([]string, 0, len(n.Enum))
		for _, e := range n.Enum {
			members = append(members, strconv.Quote(e))
		}
		return tsDefValue{members: members, goShape: "string"}
	}
	// The declared names are walked in order and no map is iterated: a map's
	// iteration order is unspecified, and this result reaches emitted bytes, so
	// the one shape is accumulated rather than collected and read back. The
	// membership set below is only ever asked a question, never walked.
	var members []string
	seen := map[string]bool{}
	shape, mixed := "", false
	for _, t := range n.Types {
		sp := tsScalarSpellings[t]
		if !seen[sp.ts] {
			seen[sp.ts] = true
			members = append(members, sp.ts)
		}
		switch {
		case shape == "":
			shape = sp.goShape
		case shape != sp.goShape:
			mixed = true
		}
	}
	if mixed {
		shape = "any"
	}
	return tsDefValue{members: members, goShape: shape}
}

// tsAliasBody returns everything a value alias declares after its `=`, leading
// separator included: the members joined on one line, or one member per line
// where the joined declaration would run past a hundred columns. The separator
// belongs to the body rather than to the format string so the wrapped form
// breaks immediately after the `=` and leaves no trailing space on that line.
// The choice is a pure function of the members, so identical input still emits
// identical bytes.
func tsAliasBody(typeName string, members []string) string {
	joined := strings.Join(members, " | ")
	if len(members) < 2 || len("export type "+typeName+" = "+joined+";") <= 100 {
		return " " + joined
	}
	var b strings.Builder
	for _, m := range members {
		fmt.Fprintf(&b, "\n  | %s", m)
	}
	return b.String()
}

// generateDefsTS produces the TypeScript source for a multi-type codec module:
// one interface per `$defs` entry plus a thin per-type encoder and decoder. A
// bare JSON.stringify over an interface would emit object keys in insertion
// order, breaking byte-parity with the Go and Rust codecs, so the encoder is
// generated, not delegated to JSON.stringify:
//
//   - struct fields encode in schema property-declaration order;
//   - plain map fields encode with keys sorted (Object.keys().sort()), matching
//     the Go codec's sort.Strings(keys) and Rust's BTreeMap;
//   - a map the schema marks order-preserving is the language's own Map, which
//     keeps insertion order for every key, and encodes in that order;
//   - opaque (`any` / json.RawMessage) values carry VERBATIM via JSON.stringify
//     of the parsed value (parse order preserved), matching Go's RawMessage
//     byte-carry and Rust's RawValue — opaque values are never sorted;
//   - non-required fields are omitted when empty, matching Go omitempty;
//   - a type holding no order-preserving map, at any depth, decodes by
//     JSON.parse into the interface (structural). A type that holds one decodes
//     through a generated reader that visits the members in the order they are
//     written, because JSON.parse has already moved a key that is an array
//     index, such as "8080", ahead of the rest before any generated code sees
//     the object.
//
// The top-level `oneOf` is ignored — the walk reads `$defs` only. The target's
// vocabulary rewrites x-aggrega-go-type values before the Go→TS translation.
func generateDefsTS(t codecTarget) ([]byte, error) {
	root, err := decodeNode([]byte(t.schema))
	if err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}
	if len(root.defs) == 0 {
		return nil, fmt.Errorf("schema %s has no $defs to emit", t.schemaFile)
	}

	// The module declares each `$defs` entry AND each object those entries state
	// inline. An inline object is named after the type declaring it and a field
	// typed as one carries that name, so a module emitting only the entries
	// publishes an interface that names a type the module never declares — which
	// the TypeScript compiler refuses, and which no gate over the generator's own
	// sources can see, because the source is correct and the emitted file is not.
	defs := expandInlineObjects(root.defs)

	defTypes := map[string]bool{}
	for _, d := range defs {
		defTypes[d.name] = true
	}
	// Which defs publish as a value rather than as an object is decided once,
	// here, because three emissions read it — the declaration, the JSON codec
	// and the CBOR codec — and a def they disagreed about would declare one
	// shape and encode another.
	defValues := map[string]tsDefValue{}
	for _, d := range defs {
		if v := tsDefValueOf(d.node); v.isValue() {
			defValues[d.name] = v
		}
	}
	// externalDefTypes are emitted by a sibling codec module; treat them as def
	// types so a field annotated with one encodes through its own generated
	// encode<T> (dispatch-consistent with the Go/Rust codecs, and sorting map
	// keys) rather than the opaque JSON.stringify fallback.
	for _, ext := range t.externalDefTypes {
		defTypes[ext] = true
	}
	// Which types hold an order-preserving map, directly or through a field,
	// decides which decoders read their JSON in member order. It is decided once
	// here, across the module and the sibling modules it imports from, because a
	// type's reader calls the reader of every such type it holds.
	holders, err := tsOrderedHolders(t, defs, map[string]bool{})
	if err != nil {
		return nil, err
	}

	// The interfaces and the JSON codecs are built first, so the imports placed
	// above them can name exactly what they reference.
	var codecSrc strings.Builder
	for _, d := range defs {
		if v, ok := defValues[d.name]; ok {
			emitTSValueAlias(&codecSrc, d.name, d.node, v)
			continue
		}
		emitTSInterface(&codecSrc, d.name, d.node, t.vocabulary, defTypes)
	}
	for _, d := range defs {
		if v, ok := defValues[d.name]; ok {
			emitTSValueCodec(&codecSrc, d.name, v, defTypes)
			continue
		}
		emitTSCodec(&codecSrc, d.name, d.node, t.vocabulary, defTypes, holders)
	}

	var b strings.Builder
	b.WriteString("// SPDX-License-Identifier: Apache-2.0\n// Copyright 2026 Cognitio s.r.l.\n\n")
	fmt.Fprintf(&b, "// Code generated by internal/tools/typegen from\n// aggrega-schemas/json/%s. DO NOT EDIT.\n", t.schemaFile)
	b.WriteString("// Regenerate with `go generate ./internal/tools/typegen` from the\n// aggrega-cli checkout.\n\n")

	// A field typed as a sibling-codec type imports its interface and its encoder
	// from the module that emits it. Its decoder is imported only where this
	// module reads it: a type holding no order-preserving map decodes by a
	// structural JSON.parse, so the nested type is parsed in place, while one that
	// holds such a map is read through its own module's member-order reader.
	for _, ext := range t.externalDefTypes {
		mod, ok := externalDefModuleTS[ext]
		if !ok {
			return nil, fmt.Errorf("no TS source module registered for external def type %q", ext)
		}
		decoder := ""
		if strings.Contains(codecSrc.String(), "decode"+ext+"(") {
			decoder = ", decode" + ext
		}
		fmt.Fprintf(&b, "import { type %s, encode%s%s } from \"./%s.ts\";\n", ext, ext, decoder, mod)
	}
	orderedMapImports := tsOrderedMapImports(codecSrc.String())
	b.WriteString(orderedMapImports)

	// The canonical-CBOR codec fork, parallel to the JSON codec, for a guest
	// target. Built into a temp buffer first so the CBOR-helper and external-CBOR
	// imports can be placed at the top, above the interfaces they annotate.
	var cborSrc strings.Builder
	if t.emitsCBOR() {
		for _, d := range defs {
			if v, ok := defValues[d.name]; ok {
				emitTSCBORValue(&cborSrc, d.name, v, defTypes)
				continue
			}
			emitTSCBOR(&cborSrc, d.name, d.node, t.vocabulary, defTypes)
		}
		b.WriteString("import {\n")
		b.WriteString("  writeHead, writeInt, writeText, writeBytes, writeBool, writeFloat,\n")
		// The repeated-key refusal is named only by a module that reads an
		// order-preserving map, so only such a module imports it.
		if strings.Contains(cborSrc.String(), "duplicateKeyError(") {
			b.WriteString("  sortKeys, b64decode, b64encode, utf8Encode, utf8Decode, CborDec, duplicateKeyError,\n")
		} else {
			b.WriteString("  sortKeys, b64decode, b64encode, utf8Encode, utf8Decode, CborDec,\n")
		}
		b.WriteString("} from \"./cbor_gen.ts\";\n")
		b.WriteString(tsCBORExternalImports(t, cborSrc.String()))
	}
	if len(t.externalDefTypes) > 0 || t.emitsCBOR() || orderedMapImports != "" {
		b.WriteString("\n")
	}

	// Shared encode helpers, emitted once per module.
	emitTSHelpers(&b)

	b.WriteString(codecSrc.String())
	b.WriteString(cborSrc.String())

	return []byte(b.String()), nil
}

// tsOrderedMapReaders are the functions of the order-keeping container's JSON
// leg a generated module may call, in the order an import names them.
var tsOrderedMapReaders = []string{"readJSONMembers", "readOrderedObject", "readJSONArray", "readPlainObject", "writeOrderedObject"}

// tsOrderedMapImports returns the import of exactly the JSON-leg functions the
// module body calls, or nothing where it calls none.
func tsOrderedMapImports(body string) string {
	var names []string
	for _, fn := range tsOrderedMapReaders {
		if strings.Contains(body, fn+"(") {
			names = append(names, fn)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "import { " + strings.Join(names, ", ") + " } from \"./orderedmap_gen.ts\";\n"
}

// tsOrderedHolders returns the types a module declares or imports whose value
// holds an order-preserving map at any depth — directly, inside a list or a
// plain map, or through a field of another such type. Their decoders read JSON
// in member order; every other type keeps a structural JSON.parse.
//
// An imported type is classified by the module that declares it, from that
// module's own schema, so the two modules cannot disagree about whether the
// type's decoder is a reader. visiting guards a module that imports, directly or
// not, a module importing it back.
func tsOrderedHolders(t codecTarget, defs []namedNode, visiting map[string]bool) (map[string]bool, error) {
	holders := map[string]bool{}
	for _, ext := range t.externalDefTypes {
		mod := externalDefModuleTS[ext]
		if visiting[mod] {
			continue
		}
		sibling, ok := codecTargets[mod+".ts"]
		if !ok {
			return nil, fmt.Errorf("no TypeScript codec target is registered for the module %q that declares %s", mod, ext)
		}
		root, err := decodeNode([]byte(sibling.schema))
		if err != nil {
			return nil, fmt.Errorf("decode schema: %w", err)
		}
		visiting[mod] = true
		theirs, err := tsOrderedHolders(sibling, expandInlineObjects(root.defs), visiting)
		if err != nil {
			return nil, err
		}
		if theirs[ext] {
			holders[ext] = true
		}
	}
	fields := map[string][]codecField{}
	for _, d := range defs {
		fields[d.name] = resolveCodecFields(d.name, d.node, t.vocabulary)
	}
	// A fixpoint over the defs in declaration order: a type that holds another
	// type is classified once that type is, wherever the two sit in the file.
	for changed := true; changed; {
		changed = false
		for _, d := range defs {
			if holders[d.name] {
				continue
			}
			for _, f := range fields[d.name] {
				if tsHoldsOrdered(f.goType, holders) {
					holders[d.name] = true
					changed = true
					break
				}
			}
		}
	}
	return holders, nil
}

// tsHoldsOrdered reports whether a value of the resolved Go type holds an
// order-preserving map: it is one, or it is a list, a plain map or a pointer
// whose element holds one, or it is a type the holders set names.
func tsHoldsOrdered(goType string, holders map[string]bool) bool {
	goType = strings.TrimLeft(goType, "*")
	if isOrderedMapSpelling(goType) {
		return true
	}
	switch {
	case strings.HasPrefix(goType, "map["):
		return tsHoldsOrdered(goType[strings.IndexByte(goType, ']')+1:], holders)
	case strings.HasPrefix(goType, "[]") && goType != "[]byte":
		return tsHoldsOrdered(goType[2:], holders)
	}
	return holders[goType]
}

// tsReadExpr returns a TS expression decoding the raw JSON text held by the
// variable raw into a value of the given resolved Go type. A value holding no
// order-preserving map is parsed whole; one that holds one is read member by
// member down to it, so no JSON.parse ever sees an object whose order matters.
// depth disambiguates the parameter names of nested readers.
func tsReadExpr(raw, goType string, holders map[string]bool, depth int) string {
	if !tsHoldsOrdered(goType, holders) {
		return "JSON.parse(" + raw + ")"
	}
	r := fmt.Sprintf("r%d", depth)
	switch {
	case strings.HasPrefix(goType, "*"):
		return fmt.Sprintf("(%s === \"null\" ? null : %s)", raw, tsReadExpr(raw, goType[1:], holders, depth))
	case isOrderedMapSpelling(goType):
		valType, _ := orderedMapValueType(goType)
		return fmt.Sprintf("readOrderedObject(%s, (%s) => %s)", raw, r, tsReadExpr(r, valType, holders, depth+1))
	case strings.HasPrefix(goType, "map["):
		valType := goType[strings.IndexByte(goType, ']')+1:]
		return fmt.Sprintf("readPlainObject(%s, (%s) => %s)", raw, r, tsReadExpr(r, valType, holders, depth+1))
	case strings.HasPrefix(goType, "[]"):
		return fmt.Sprintf("readJSONArray(%s, (%s) => %s)", raw, r, tsReadExpr(r, goType[2:], holders, depth+1))
	default:
		return fmt.Sprintf("decode%s(%s)", goType, raw)
	}
}

// emitTSHelpers writes the shared string-escape and verbatim-opaque helpers the
// generated encoders call. jstr matches encoding/json default string escaping
// for the cases the socket payloads exercise; encodeOpaque carries an opaque
// value verbatim (JSON.stringify preserves the key order from JSON.parse).
func emitTSHelpers(b *strings.Builder) {
	b.WriteString("function jstr(s: string): string {\n")
	b.WriteString("  return JSON.stringify(s);\n")
	b.WriteString("}\n\n")
	b.WriteString("// encodeOpaque carries an opaque (`any` / RawMessage) value VERBATIM: the\n")
	b.WriteString("// key order from JSON.parse is preserved, matching the Go and Rust codecs'\n")
	b.WriteString("// byte-carry of opaque values. Opaque values are never sorted.\n")
	b.WriteString("function encodeOpaque(v: unknown): string {\n")
	b.WriteString("  return JSON.stringify(v);\n")
	b.WriteString("}\n\n")
}

// emitTSValueAlias writes one `export type` declaration for a def that declares
// a value rather than an object.
func emitTSValueAlias(b *strings.Builder, typeName string, n schemaNode, v tsDefValue) {
	if n.Description != "" {
		writeTSDoc(b, "", oneLine(n.Description))
	}
	fmt.Fprintf(b, "export type %s =%s;\n\n", typeName, tsAliasBody(typeName, v.members))
}

// emitTSValueCodec writes the encode/decode pair for a value alias. The encode
// routes through the same expression arm a field of that shape takes, so a
// value has one encoding in this module whether it arrives as a def of its own
// or as a field of one.
func emitTSValueCodec(b *strings.Builder, typeName string, v tsDefValue, defTypes map[string]bool) {
	fmt.Fprintf(b, "export function encode%s(v: %s): string {\n", typeName, typeName)
	fmt.Fprintf(b, "  return %s;\n", tsEncodeExpr("v", v.goShape, defTypes, 0))
	b.WriteString("}\n\n")

	fmt.Fprintf(b, "export function decode%s(data: string): %s {\n", typeName, typeName)
	fmt.Fprintf(b, "  return JSON.parse(data) as %s;\n", typeName)
	b.WriteString("}\n\n")
}

// emitTSInterface writes one interface for a def node.
func emitTSInterface(b *strings.Builder, typeName string, n schemaNode, vocab map[string]string, defTypes map[string]bool) {
	fields := resolveCodecFields(typeName, n, vocab)
	if n.Description != "" {
		writeTSDoc(b, "", oneLine(n.Description))
	}
	fmt.Fprintf(b, "export interface %s {\n", typeName)
	for _, f := range fields {
		if f.desc != "" {
			writeTSDoc(b, "  ", oneLine(f.desc))
		}
		opt := ""
		if !f.required {
			opt = "?"
		}
		fmt.Fprintf(b, "  %s%s: %s;\n", tsFieldName(f.key), opt, tsFieldType(f, defTypes))
	}
	b.WriteString("}\n\n")
}

// tsFieldType returns the TypeScript type of one resolved field, stating the
// absence the schema declares rather than only the shape.
//
// A collection the wire sends as null is published as such. Flattening it to
// the bare array would type-check at every call site and throw at the first one
// that iterates the value the session really sent — the failure arrives at run
// time in a consumer's product, with nothing at compile time to have warned
// them. The Go and Rust emissions need no counterpart: a nil slice marshals to
// null and back, so their zero value already carries the same statement.
func tsFieldType(f codecField, defTypes map[string]bool) string {
	t := goTypeToTS(f.goType, defTypes)
	if f.nullable {
		t += " | null"
	}
	return t
}

// emitTSCodec writes the encode<T> and decode<T> functions for a def type.
// holders names the types whose decoder reads JSON in member order; see
// tsOrderedHolders.
func emitTSCodec(b *strings.Builder, typeName string, n schemaNode, vocab map[string]string, defTypes, holders map[string]bool) {
	fields := resolveCodecFields(typeName, n, vocab)

	fmt.Fprintf(b, "export function encode%s(v: %s): string {\n", typeName, typeName)
	b.WriteString("  const parts: string[] = [];\n")
	for _, f := range fields {
		acc := "v." + tsFieldAccess(f.key)
		enc := tsEncodeExpr(acc, f.goType, defTypes, 0)
		if f.required && f.nullable {
			// A required field the schema also declares as null has to put the
			// null on the wire: the shape arm has no null to write and would
			// throw reaching into it, and omitting the key would contradict the
			// schema's own required list.
			enc = fmt.Sprintf("%s === null ? \"null\" : %s", acc, enc)
		}
		line := fmt.Sprintf("parts.push(`${jstr(%q)}:${%s}`);", f.key, enc)
		if f.required {
			fmt.Fprintf(b, "  %s\n", line)
		} else {
			guard := tsOmitGuard(acc, f.goType, f.nullable)
			fmt.Fprintf(b, "  if (%s) %s\n", guard, line)
		}
	}
	b.WriteString("  return `{${parts.join(\",\")}}`;\n")
	b.WriteString("}\n\n")

	fmt.Fprintf(b, "export function decode%s(data: string): %s {\n", typeName, typeName)
	if !holders[typeName] {
		fmt.Fprintf(b, "  return JSON.parse(data) as %s;\n", typeName)
		b.WriteString("}\n\n")
		return
	}
	// A type holding an order-preserving map reads its members in the order they
	// are written. A member the type does not declare is skipped, as the CBOR
	// decoder beside it skips one.
	b.WriteString("  const o: any = {};\n")
	b.WriteString("  readJSONMembers(data, (k, raw) => {\n")
	b.WriteString("    switch (k) {\n")
	for _, f := range fields {
		read := tsReadExpr("raw", f.goType, holders, 0)
		if f.nullable && tsHoldsOrdered(f.goType, holders) {
			read = "raw === \"null\" ? null : " + read
		}
		fmt.Fprintf(b, "      case %q: o[%q] = %s; break;\n", f.key, f.key, read)
	}
	b.WriteString("    }\n")
	b.WriteString("  });\n")
	fmt.Fprintf(b, "  return o as %s;\n", typeName)
	b.WriteString("}\n\n")
}

// tsEncodeExpr returns a TS expression that encodes the value at `acc` of the
// given resolved Go type to a JSON string fragment. depth disambiguates the
// arrow-function parameter names of nested maps and slices (a map of maps, a
// slice of maps) so an inner closure does not shadow an outer one.
func tsEncodeExpr(acc, goType string, defTypes map[string]bool, depth int) string {
	switch {
	case goType == "string":
		return fmt.Sprintf("jstr(%s)", acc)
	case goType == "bool":
		return fmt.Sprintf("String(%s)", acc)
	case goType == "int" || goType == "int64" || goType == "float64":
		return fmt.Sprintf("String(%s)", acc)
	case goType == "[]byte":
		// base64 string on the wire; the value is already the base64 string.
		return fmt.Sprintf("jstr(%s)", acc)
	case goType == "json.RawMessage" || goType == "any" || goType == "interface{}":
		return fmt.Sprintf("encodeOpaque(%s)", acc)
	case isOrderedMapSpelling(goType):
		// The container's own order, written member by member; the value's
		// parameter name is depth-suffixed so a nested container does not shadow
		// the outer one. The source carries a non-null assertion for the reason
		// tsEncodeMap gives.
		valType, _ := orderedMapValueType(goType)
		v := fmt.Sprintf("v%d", depth)
		return fmt.Sprintf("writeOrderedObject(%s!, (%s) => %s)", acc, v, tsEncodeExpr(v, valType, defTypes, depth+1))
	case strings.HasPrefix(goType, "map["):
		valType := goType[strings.IndexByte(goType, ']')+1:]
		return tsEncodeMap(acc, valType, defTypes, depth)
	case strings.HasPrefix(goType, "[]"):
		return tsEncodeSlice(acc, goType[2:], defTypes, depth)
	case strings.HasPrefix(goType, "*"):
		elem := goType[1:]
		switch elem {
		case "string", "bool", "int", "int64", "float64", "[]byte":
			// A pointer to a scalar encodes as the scalar; the caller's omit
			// guard decides whether an absent pointer is emitted at all.
			return tsEncodeExpr(acc, elem, defTypes, depth)
		}
		if defTypes[elem] {
			return fmt.Sprintf("encode%s(%s)", elem, acc)
		}
		return fmt.Sprintf("encodeOpaque(%s)", acc)
	case defTypes[goType]:
		return fmt.Sprintf("encode%s(%s)", goType, acc)
	default:
		return fmt.Sprintf("encodeOpaque(%s)", acc)
	}
}

// tsEncodeMap returns an inline arrow expression that sorts the map keys and
// encodes each value, producing a JSON object string. Sorted keys match the Go
// and Rust codecs. The source is bound through a non-null assertion (the caller
// guards optional fields before emitting) so the value access does not re-widen
// to the optional type inside the closure; the key variable is depth-suffixed so
// nested maps do not shadow each other.
func tsEncodeMap(acc, valType string, defTypes map[string]bool, depth int) string {
	k := fmt.Sprintf("k%d", depth)
	src := acc + "!"
	valEnc := tsEncodeExpr(fmt.Sprintf("%s[%s]", src, k), valType, defTypes, depth+1)
	return fmt.Sprintf("`{${Object.keys(%s).sort().map((%s) => `${jstr(%s)}:${%s}`).join(\",\")}}`", src, k, k, valEnc)
}

// tsEncodeSlice returns an inline expression that encodes each element in order,
// producing a JSON array string. The element variable is depth-suffixed so
// nested slices/maps do not shadow each other.
func tsEncodeSlice(acc, elemType string, defTypes map[string]bool, depth int) string {
	e := fmt.Sprintf("e%d", depth)
	elemEnc := tsEncodeExpr(e, elemType, defTypes, depth+1)
	return fmt.Sprintf("`[${%s!.map((%s) => %s).join(\",\")}]`", acc, e, elemEnc)
}

// tsOmitGuard returns a boolean expression true when a non-required field must
// be emitted (non-empty), matching Go omitempty.
//
// nullable adds the null test a schema-declared null needs, and it is placed
// ahead of the emptiness test rather than beside it: the emptiness test reaches
// into the value, so running it on a null throws instead of omitting. Omitting
// on null is what Go does too — a nil slice under omitempty puts no key on the
// wire at all — so the two emissions still agree about what a non-required
// field carries.
func tsOmitGuard(acc, goType string, nullable bool) string {
	tests := []string{fmt.Sprintf("%s !== undefined", acc)}
	if nullable {
		tests = append(tests, fmt.Sprintf("%s !== null", acc))
	}
	switch {
	case goType == "string" || goType == "[]byte":
		tests = append(tests, fmt.Sprintf("%s !== \"\"", acc))
	case goType == "bool":
		tests = append(tests, fmt.Sprintf("%s !== false", acc))
	case goType == "int" || goType == "int64" || goType == "float64":
		tests = append(tests, fmt.Sprintf("%s !== 0", acc))
	case isOrderedMapSpelling(goType):
		tests = append(tests, fmt.Sprintf("%s.size > 0", acc))
	case strings.HasPrefix(goType, "map["):
		tests = append(tests, fmt.Sprintf("Object.keys(%s).length > 0", acc))
	case strings.HasPrefix(goType, "[]"):
		tests = append(tests, fmt.Sprintf("%s.length > 0", acc))
	case strings.HasPrefix(goType, "*"),
		goType == "json.RawMessage", goType == "any", goType == "interface{}":
		if !nullable {
			tests = append(tests, fmt.Sprintf("%s !== null", acc))
		}
	}
	return strings.Join(tests, " && ")
}

// goTypeToTS translates a resolved Go-vocabulary type to a TypeScript type. An
// order-preserving container is the language's own Map, which keeps insertion
// order for every key, where a plain object lists a key that is an array index
// ahead of the rest.
func goTypeToTS(goType string, defTypes map[string]bool) string {
	if valType, ok := orderedMapValueType(goType); ok {
		return "Map<string, " + goTypeToTS(valType, defTypes) + ">"
	}
	switch {
	case goType == "string" || goType == "[]byte":
		return "string"
	case goType == "bool":
		return "boolean"
	case goType == "int" || goType == "int64" || goType == "float64":
		return "number"
	case goType == "json.RawMessage" || goType == "any" || goType == "interface{}":
		return "unknown"
	case strings.HasPrefix(goType, "map["):
		valType := goType[strings.IndexByte(goType, ']')+1:]
		return "Record<string, " + goTypeToTS(valType, defTypes) + ">"
	case strings.HasPrefix(goType, "[]"):
		return goTypeToTS(goType[2:], defTypes) + "[]"
	case strings.HasPrefix(goType, "*"):
		elem := goType[1:]
		switch elem {
		case "string", "bool", "int", "int64", "float64", "[]byte":
			// A pointer to a scalar carries the scalar itself; the interface
			// field's optional marker (?) expresses the present-or-absent axis.
			return goTypeToTS(elem, defTypes)
		}
		if defTypes[elem] {
			return elem
		}
		return "unknown"
	default:
		return goType
	}
}

// tsFieldName returns the interface property name for a wire key. The wire keys
// are snake_case; TS allows snake_case identifiers, so the key is used verbatim.
func tsFieldName(key string) string {
	return key
}

// tsFieldAccess returns the member-access form for a wire key.
func tsFieldAccess(key string) string {
	return key
}

// writeTSDoc writes a // doc line at the given indent.
func writeTSDoc(b *strings.Builder, indent, doc string) {
	fmt.Fprintf(b, "%s// %s\n", indent, doc)
}
