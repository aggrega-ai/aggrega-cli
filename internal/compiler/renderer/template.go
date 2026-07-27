// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"text/template"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// templateMissingKey is text/template's missing-key policy for one surface.
type templateMissingKey string

const (
	// missingKeyZero lets an absent key under an open root render empty, which is
	// what keeps a defensively-written optional key working on a value template.
	missingKeyZero templateMissingKey = "zero"
	// missingKeyError halts on an absent key. It is the policy for surfaces whose
	// output is a filesystem path or a predicate, where a silently empty value is
	// discovered long after the build rather than at it.
	missingKeyError templateMissingKey = "error"
)

// templateEvalStage names which half of one evaluation a failure came from, so a
// surface can say so in its own diagnostic.
type templateEvalStage string

const (
	stageParse   templateEvalStage = "parse"
	stageExecute templateEvalStage = "execute"
)

// templateEval is one template evaluation. Every template-bearing surface in the
// renderer runs through it, which is what makes the reference check and the
// execution unable to disagree: the tree that is walked is the tree that runs,
// and the context the references are checked against is the context the template
// is rendered against — both are scope.data, read once.
//
// The fields after the text are the only things that differ between surfaces: the
// delimiter pair, the missing-key policy, any per-surface function, and how a
// surface names a refused reference or a failed stage with its own published
// diagnostic. A surface that needs anything else needs a change here, where every
// surface sees it, rather than a second evaluator.
type templateEval struct {
	name string
	text string
	// scope carries the render context in .data and this surface's narrowing over
	// what that context type defines.
	scope templateScope
	// leftDelim and rightDelim are passed through to text/template, which reads an
	// empty delimiter as its own default.
	leftDelim  string
	rightDelim string
	// missingKey is the surface's missing-key policy; empty is missingKeyZero.
	missingKey templateMissingKey
	// category is the authoring domain a diagnostic raised here is charged to,
	// which is what decides whether the author is sent to the Recipe or to the
	// Aggregate. Empty is cerrors.CatalogError, the Recipe-authored default every
	// surface but a developer-authored one carries. A surface stating its own
	// diagnostic through refused or failed carries the category in that
	// diagnostic instead.
	category cerrors.Category
	// extraFuncs are functions this surface adds over aggregaFuncMap. The map is
	// template.FuncMap's own underlying type, so a surface contributing one does
	// not have to reach for text/template itself.
	extraFuncs map[string]any
	// refused names a reference the scope refuses with this surface's own
	// diagnostic; nil reports ErrTemplateUnknownRoot.
	refused func(*templateScopeViolation) error
	// failed names a parse or execute failure with this surface's own diagnostic;
	// nil returns the underlying error unwrapped.
	failed func(templateEvalStage, error) error
}

func (e templateEval) run() (string, error) {
	missingKey := e.missingKey
	if missingKey == "" {
		missingKey = missingKeyZero
	}
	category := e.category
	if category == "" {
		category = cerrors.CatalogError
	}
	funcs := aggregaFuncMap()
	// Iterating a map is safe here where it usually would not be: the loop only
	// assigns distinct keys into another map, so the merged function set is the
	// same whatever order the keys arrive in and no iteration order can reach the
	// rendered bytes.
	for name, fn := range e.extraFuncs {
		funcs[name] = fn
	}
	t, err := template.New(e.name).
		Option("missingkey="+string(missingKey)).
		Delims(e.leftDelim, e.rightDelim).
		Funcs(funcs).
		Parse(e.text)
	if err != nil {
		return "", e.wrap(stageParse, err)
	}
	if v := e.scope.check(t.Tree.Root); v != nil {
		if e.refused != nil {
			return "", e.refused(v)
		}
		return "", templateUnknownRootErr(category, e.name, v)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, toLowercaseMap(e.scope.data)); err != nil {
		return "", e.wrap(stageExecute, err)
	}
	return buf.String(), nil
}

func (e templateEval) wrap(stage templateEvalStage, err error) error {
	if e.failed == nil {
		return err
	}
	return e.failed(stage, err)
}

// EvalTemplate evaluates a Go template string with the default `{{ }}`
// delimiters, Sprig functions, and missingkey=zero. Thin wrapper over
// EvalTemplateWithDelims hard-coding the default delimiter pair so callers
// outside the artifacts-engine-dispatch path don't carry the delimiter
// noise.
//
// The input struct is walked reflectively and converted to a tree of plain
// Go values (maps / slices / scalars) with lowercase JSON-tag keys, so
// template authors write `.values.foo` matching the
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
//
// Every template evaluated here is checked against the root names its context
// type defines before it executes: a reference to a root the context does not
// define halts with ErrTemplateUnknownRoot instead of resolving to nothing. A
// missing KEY under a defined root is unaffected and still follows
// missingkey=zero, so an optional-key idiom keeps working; what cannot happen is
// a fallback wrapper turning an unknown ROOT into an empty value.
func EvalTemplateWithDelims(name, tmplText string, data any, leftDelim, rightDelim string) (string, error) {
	// The value-template surface declares no narrowing: every root the context
	// type defines is reachable from a template rendered against it.
	return evalValueTemplate(name, tmplText, templateScope{data: data}, leftDelim, rightDelim)
}

// evalValueTemplate is the value-template evaluation, taking the surface's scope
// rather than only the data it renders against. It exists for the value surfaces
// that admit less than their context type defines: a scope-bearing caller states
// its narrowing and gets the same missing-key policy, the same diagnostic wording
// and the same category as every other value template, so a narrowing does not
// cost the surface its own evaluator.
func evalValueTemplate(name, tmplText string, scope templateScope, leftDelim, rightDelim string) (string, error) {
	return templateEval{
		name:       name,
		text:       tmplText,
		scope:      scope,
		leftDelim:  leftDelim,
		rightDelim: rightDelim,
		missingKey: missingKeyZero,
		failed: func(stage templateEvalStage, err error) error {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: name},
				fmt.Sprintf("template %q %s", name, stage))
		},
	}.run()
}

// pathTemplateDirectoryScope is the reference set a `directory:` path template is
// checked against. The universe is PathTemplateContext — the type both path
// templates are evaluated against — so the variables the check admits are derived
// from that type rather than enumerated here, and a variable added to or retired
// from the context reaches both surfaces with no edit at either.
//
// The surface declares one narrowing, and it is a deliberately kept exclusion:
// .component_id names a single component, while a `directory:` template is
// evaluated once for the whole boundary. Admitting it would make the emitted path
// depend on which component happened to be evaluated first, so the reference is
// refused rather than resolved to an arbitrary one.
var pathTemplateDirectoryScope = templateScope{
	data:    PathTemplateContext{},
	exclude: []string{"component_id"},
}

// pathTemplateSubDirectoryScope is the reference set a `subDirectory:` path
// template is checked against: the same context type with no narrowing. A
// subDirectory segment is evaluated once per component, so .component_id names
// exactly the component being placed.
var pathTemplateSubDirectoryScope = templateScope{
	data: PathTemplateContext{},
}

// EvalPathTemplate evaluates a Release.spec.boundaries[].directory or
// subDirectory template. Differs from EvalTemplate in two load-bearing ways:
//
//   - missingkey=error policy. A defined variable with no value at eval time
//     produces an explicit error rather than rendering empty as EvalTemplate's
//     missingkey=zero would. Path templates use stricter missing-key handling
//     than value templates because a silently empty path segment produces broken
//     filesystem layout that operators only discover post-build.
//   - The reference check refuses a variable this surface does not admit before
//     the template executes, reporting ErrPathTemplateInvalidField with the
//     offending reference and the boundary name.
//
// contextKind selects the surface: "subDirectory" admits .component_id, and any
// other value — "directory" included — selects the narrower `directory:` scope.
//
// boundaryName is named in the diagnostic when a reference is refused, so the
// operator can locate the offending template without searching the Release file.
func EvalPathTemplate(name, contextKind, boundaryName, tmplText string, data PathTemplateContext) (string, error) {
	scope := pathTemplateDirectoryScope
	if contextKind == "subDirectory" {
		scope = pathTemplateSubDirectoryScope
	}
	return templateEval{
		name:       name,
		text:       tmplText,
		scope:      scope.with(data),
		missingKey: missingKeyError,
		refused: func(v *templateScopeViolation) error {
			return pathTemplateRefusedErr(boundaryName, v)
		},
		failed: func(stage templateEvalStage, err error) error {
			return cerrors.Wrap(err, cerrors.OpsError, cerrors.SourceLocation{File: name},
				fmt.Sprintf("path template %q %s", name, stage))
		},
	}.run()
}

// pathTemplateRefusedErr names a refused path-template reference with the
// diagnostic that describes it. A variable the surface does not admit — one the
// context type does not define at all, or .component_id in a `directory:`
// template — reports the path-template halt the language reference documents for
// this surface. A key refused UNDER a defined variable is the general
// unknown-reference halt instead, the same one every other template surface
// reports for a mistyped key, so neither diagnostic tells the author their
// reference is something it is not.
func pathTemplateRefusedErr(boundaryName string, v *templateScopeViolation) error {
	if v.Level == scopeLevelKey {
		return templateUnknownRootErr(cerrors.CatalogError, boundaryName, v)
	}
	inner := fmt.Errorf("%w: boundary %q references unknown field %s; allowlist: see aggrega-docs/language-reference/path-template-variables.md",
		ErrPathTemplateInvalidField, boundaryName, v.Ref.Path)
	return cerrors.Wrap(inner, cerrors.OpsError, cerrors.SourceLocation{}, inner.Error())
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
