// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"text/template"
	"text/template/parse"

	sprig "github.com/Masterminds/sprig/v3"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// EvalTemplate evaluates a Go template string with the default `{{ }}`
// delimiters, Sprig functions, and missingkey=zero. Thin wrapper over
// EvalTemplateWithDelims hard-coding the default delimiter pair so callers
// outside the artifacts-engine-dispatch path don't carry the delimiter
// noise.
//
// The input struct is walked reflectively and converted to a tree of plain
// Go values (maps / slices / scalars) with lowercase JSON-tag keys, so
// template authors write `.context.values.foo` matching the
// template-context-v1alpha1.schema.json contract.
//
// OrderedMap values are unwrapped during the walk into plain map[string]any
// containers. Text/template then iterates them in alphabetical key order
// (Go stdlib default). Author-order preservation across the JSON-
// serialization boundary (IR, --trace, Wasm payloads) lives in
// OrderedMap.MarshalJSON; the template-iteration layer is intentionally
// alphabetical to match Helm-community authoring expectations.
func EvalTemplate(name, tmplText string, data any) (string, error) {
	return EvalTemplateWithDelims(name, tmplText, data, "{{", "}}")
}

// EvalTemplateWithDelims evaluates a Go template with caller-controlled
// delimiters. Used by Phase B.3 when the engine satisfies
// ArtifactsTemplateDelims and returns alternates (e.g., helm-chart's
// "[[", "]]") so Aggrega's pre-templating pass leaves a downstream
// templating pass's `{{ }}` syntax untouched.
//
// Caller passes the explicit delimiter pair every time — there is no
// empty-string sentinel. The default-delimiter path is the EvalTemplate
// thin wrapper above.
func EvalTemplateWithDelims(name, tmplText string, data any, leftDelim, rightDelim string) (string, error) {
	templateData := toLowercaseMap(data)

	t, err := template.New(name).
		Option("missingkey=zero").
		Delims(leftDelim, rightDelim).
		Funcs(aggregaFuncMap()).
		Parse(tmplText)
	if err != nil {
		return "", cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: name}, fmt.Sprintf("template %q parse", name))
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, templateData); err != nil {
		return "", cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: name}, fmt.Sprintf("template %q execute", name))
	}
	return buf.String(), nil
}

// pathTemplateAllowlistDirectory is the set of top-level field names a
// `directory:` path-template may reference. Per-context allowlists differ
// between `directory:` and `subDirectory:` — `.component_id` is permitted
// only in subDirectory: templates because component identity is meaningless
// at the boundary level.
var pathTemplateAllowlistDirectory = map[string]bool{
	"provider":        true,
	"boundary_name":   true,
	"parameters":      true,
	"parent_boundary": true,
	"manifest":        true,
}

// pathTemplateAllowlistSubDirectory is the set of top-level field names a
// `subDirectory:` path-template may reference. Identical to the directory:
// allowlist plus `.component_id`.
var pathTemplateAllowlistSubDirectory = map[string]bool{
	"provider":        true,
	"boundary_name":   true,
	"parameters":      true,
	"component_id":    true,
	"parent_boundary": true,
	"manifest":        true,
}

// EvalPathTemplate evaluates a Release.spec.boundaries[].directory or
// subDirectory template against the path-template variable allowlist.
// Differs from EvalTemplate in two load-bearing ways:
//
//   - missingkey=error policy. An in-allowlist field with no value at eval
//     time produces an explicit error (rather than rendering empty as
//     EvalTemplate's missingkey=zero would). Path templates use stricter
//     missing-key handling than value templates because silent path
//     failures produce broken filesystem layout that operators only
//     discover post-build.
//   - Parse-time allowlist validation. Every top-level field reference in
//     the template AST is verified against the per-context allowlist
//     (directory: vs subDirectory:); unknown field references produce
//     ErrPathTemplateInvalidField wrapping the offending field name and
//     boundary name.
//
// contextKind selects between the two allowlists: "directory" or
// "subDirectory". An unknown contextKind value defaults to the directory:
// allowlist (more restrictive).
//
// boundaryName is included in the error wrapping when an invalid field is
// detected, so the operator can locate the offending template without
// re-grepping the Release file.
func EvalPathTemplate(name, contextKind, boundaryName, tmplText string, data any) (string, error) {
	allowlist := pathTemplateAllowlistDirectory
	if contextKind == "subDirectory" {
		allowlist = pathTemplateAllowlistSubDirectory
	}

	if err := validatePathTemplateFields(boundaryName, tmplText, allowlist); err != nil {
		return "", err
	}

	templateData := toLowercaseMap(data)

	t, err := template.New(name).
		Option("missingkey=error").
		Funcs(sprig.FuncMap()).
		Parse(tmplText)
	if err != nil {
		return "", cerrors.Wrap(err, cerrors.OpsError, cerrors.SourceLocation{File: name}, fmt.Sprintf("path template %q parse", name))
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, templateData); err != nil {
		return "", cerrors.Wrap(err, cerrors.OpsError, cerrors.SourceLocation{File: name}, fmt.Sprintf("path template %q execute", name))
	}
	return buf.String(), nil
}

// validatePathTemplateFields walks the template AST, extracts every
// top-level field reference (e.g., `.parameters.region` → top-level
// `parameters`), and rejects unknown fields against the allowlist. The
// walk uses Go's text/template parser to obtain a parse tree without
// triggering execution.
func validatePathTemplateFields(boundaryName, tmplText string, allowlist map[string]bool) error {
	// Parse with no missingkey constraint to get the AST; ignore execute-
	// time issues — this pass is purely structural.
	t, err := template.New("path-template-validate").
		Funcs(sprig.FuncMap()).
		Parse(tmplText)
	if err != nil {
		return cerrors.Wrap(err, cerrors.OpsError, cerrors.SourceLocation{File: boundaryName}, fmt.Sprintf("path template at boundary %q parse", boundaryName))
	}

	for _, refField := range collectTopLevelFields(t) {
		if !allowlist[refField] {
			inner := fmt.Errorf("%w: boundary %q references unknown field .%s; allowlist: see aggrega-docs/language-reference/path-template-variables.md",
				ErrPathTemplateInvalidField, boundaryName, refField)
			return cerrors.Wrap(inner, cerrors.OpsError, cerrors.SourceLocation{}, inner.Error())
		}
	}
	return nil
}

// collectTopLevelFields walks every action in the parsed template tree and
// collects the FIRST identifier of every field-chain reference (e.g.,
// `.parameters.region` → "parameters", `.parent_boundary.directory` →
// "parent_boundary"). Returns the deduplicated list.
func collectTopLevelFields(t *template.Template) []string {
	seen := map[string]bool{}
	var collected []string
	for _, ttree := range t.Templates() {
		if ttree.Tree == nil || ttree.Tree.Root == nil {
			continue
		}
		walkNodeForFields(ttree.Tree.Root, seen, &collected)
	}
	return collected
}

// walkNodeForFields recurses into template AST nodes, recording the first
// identifier of every FieldNode and ChainNode encountered.
func walkNodeForFields(node parse.Node, seen map[string]bool, collected *[]string) {
	switch n := node.(type) {
	case *parse.ListNode:
		if n == nil {
			return
		}
		for _, child := range n.Nodes {
			walkNodeForFields(child, seen, collected)
		}
	case *parse.ActionNode:
		if n.Pipe != nil {
			for _, cmd := range n.Pipe.Cmds {
				for _, arg := range cmd.Args {
					walkNodeForFields(arg, seen, collected)
				}
			}
		}
	case *parse.PipeNode:
		for _, cmd := range n.Cmds {
			for _, arg := range cmd.Args {
				walkNodeForFields(arg, seen, collected)
			}
		}
	case *parse.IfNode:
		if n.Pipe != nil {
			walkNodeForFields(n.Pipe, seen, collected)
		}
		if n.List != nil {
			walkNodeForFields(n.List, seen, collected)
		}
		if n.ElseList != nil {
			walkNodeForFields(n.ElseList, seen, collected)
		}
	case *parse.RangeNode:
		if n.Pipe != nil {
			walkNodeForFields(n.Pipe, seen, collected)
		}
		if n.List != nil {
			walkNodeForFields(n.List, seen, collected)
		}
		if n.ElseList != nil {
			walkNodeForFields(n.ElseList, seen, collected)
		}
	case *parse.WithNode:
		if n.Pipe != nil {
			walkNodeForFields(n.Pipe, seen, collected)
		}
		if n.List != nil {
			walkNodeForFields(n.List, seen, collected)
		}
		if n.ElseList != nil {
			walkNodeForFields(n.ElseList, seen, collected)
		}
	case *parse.FieldNode:
		// `.parameters.region` parses as a single FieldNode with Ident = ["parameters", "region"].
		if len(n.Ident) > 0 {
			top := n.Ident[0]
			if !seen[top] {
				seen[top] = true
				*collected = append(*collected, top)
			}
		}
	case *parse.ChainNode:
		// `(...).foo.bar` — recurse into the receiver, then record top of the chain.
		walkNodeForFields(n.Node, seen, collected)
		if len(n.Field) > 0 {
			top := n.Field[0]
			if !seen[top] {
				seen[top] = true
				*collected = append(*collected, top)
			}
		}
	}
}

// orderedMapForTemplate is satisfied by types.OrderedMap[V] for every V via
// its value-receiver TemplateAlphabeticalMap method. The walker uses this
// interface to detect OrderedMap values without depending on the generic
// type parameter.
type orderedMapForTemplate interface {
	TemplateAlphabeticalMap() map[string]any
}

// toLowercaseMap walks an arbitrary Go value and produces a representation
// shaped for Go text/template consumption. The walker replaces an earlier
// json.Marshal / json.Unmarshal round-trip that was destroying OrderedMap
// author-order at the JSON-decode step — author-order is load-bearing for
// JSON-serialization consumers (IR output, --trace debug, Wasm payloads)
// but irrelevant for text/template's alphabetical map-range default. The
// walker preserves OrderedMap shape all the way to the template boundary
// and then intentionally converts to map[string]any at that boundary —
// the controlled handoff from "author-order Go data" to
// "alphabetical-iteration template data."
//
// Conversion rules:
//   - Struct fields: reflected by JSON tag (first token before `,`).
//     `json:"-"` skips the field. No tag falls back to the lowercased field
//     name. `omitempty` and `omitzero` cause zero-valued fields to be
//     omitted (matching the prior JSON round-trip behavior).
//   - types.OrderedMap[V] (detected via orderedMapForTemplate interface):
//     unwrapped to map[string]any via TemplateAlphabeticalMap, then values
//     recursively walked.
//   - map[K]V where K is string-convertible: walked recursively, emitted
//     as map[string]any.
//   - Slices / arrays: walked element-wise, emitted as []any.
//   - Pointers / interfaces: dereferenced, then walked.
//   - Scalars (bool, numeric, string): passed through unchanged.
func toLowercaseMap(data any) any {
	if data == nil {
		return nil
	}
	return walkForTemplate(reflect.ValueOf(data))
}

func walkForTemplate(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}

	// Peel off pointers and interfaces uniformly.
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return nil
	}

	// OrderedMap detection — via value-receiver marker interface. Check both
	// the value type and its pointer type to catch both addressable values
	// and pointer-held copies.
	if v.CanInterface() {
		if om, ok := v.Interface().(orderedMapForTemplate); ok {
			raw := om.TemplateAlphabeticalMap()
			out := make(map[string]any, len(raw))
			for k, inner := range raw {
				out[k] = walkForTemplate(reflect.ValueOf(inner))
			}
			return out
		}
	}
	if v.CanAddr() && v.Addr().CanInterface() {
		if om, ok := v.Addr().Interface().(orderedMapForTemplate); ok {
			raw := om.TemplateAlphabeticalMap()
			out := make(map[string]any, len(raw))
			for k, inner := range raw {
				out[k] = walkForTemplate(reflect.ValueOf(inner))
			}
			return out
		}
	}

	switch v.Kind() {
	case reflect.Struct:
		return walkStruct(v)
	case reflect.Map:
		return walkMap(v)
	case reflect.Slice, reflect.Array:
		return walkSlice(v)
	default:
		// Scalars (bool, numeric, string, etc.) and anything else passes
		// through as-is. CanInterface guards against unexported-field
		// reflection panics in edge cases.
		if v.CanInterface() {
			return v.Interface()
		}
		return nil
	}
}

func walkStruct(v reflect.Value) map[string]any {
	t := v.Type()
	out := make(map[string]any, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		name, omitEmpty, skip := fieldJSONName(field)
		if skip {
			continue
		}
		fv := v.Field(i)
		if omitEmpty && isEmptyForTemplate(fv) {
			continue
		}
		out[name] = walkForTemplate(fv)
	}
	return out
}

func walkMap(v reflect.Value) map[string]any {
	out := make(map[string]any, v.Len())
	iter := v.MapRange()
	for iter.Next() {
		k := iter.Key()
		var keyStr string
		switch k.Kind() {
		case reflect.String:
			keyStr = k.String()
		default:
			// Non-string keys: format via fmt to preserve behavior. Rare in
			// practice — compiler TemplateContext maps are all string-keyed.
			keyStr = fmt.Sprint(k.Interface())
		}
		out[keyStr] = walkForTemplate(iter.Value())
	}
	return out
}

func walkSlice(v reflect.Value) []any {
	if v.Kind() == reflect.Slice && v.IsNil() {
		return nil
	}
	out := make([]any, v.Len())
	for i := 0; i < v.Len(); i++ {
		out[i] = walkForTemplate(v.Index(i))
	}
	return out
}

// fieldJSONName returns the emission-side name of a struct field per its
// json tag, plus whether `omitempty`/`omitzero` is declared, plus whether
// the field should be skipped entirely (`json:"-"`). Falls back to the
// lowercased field name when the struct has no json tag.
func fieldJSONName(f reflect.StructField) (name string, omitEmpty bool, skip bool) {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "", false, true
	}
	if tag == "" {
		return strings.ToLower(f.Name), false, false
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "" {
		name = strings.ToLower(f.Name)
	}
	for _, opt := range parts[1:] {
		if opt == "omitempty" || opt == "omitzero" {
			omitEmpty = true
		}
	}
	return name, omitEmpty, false
}

// isEmptyForTemplate matches encoding/json's omitempty semantics plus the
// Go 1.24 omitzero semantics. Keeps the walker's output shape equivalent
// to what the prior JSON round-trip produced.
func isEmptyForTemplate(v reflect.Value) bool {
	if !v.IsValid() {
		return true
	}
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Ptr:
		return v.IsNil()
	case reflect.Struct:
		// Honor IsZero() method if present (matches omitzero semantics for
		// types.OrderedMap and any other type implementing it).
		if v.CanInterface() {
			if z, ok := v.Interface().(interface{ IsZero() bool }); ok {
				return z.IsZero()
			}
		}
		if v.CanAddr() && v.Addr().CanInterface() {
			if z, ok := v.Addr().Interface().(interface{ IsZero() bool }); ok {
				return z.IsZero()
			}
		}
		return false
	}
	return false
}
