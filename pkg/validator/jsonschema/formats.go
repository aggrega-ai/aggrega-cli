// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator/stringformat"
)

// The format keyword is the one scalar constraint this adapter evaluates itself
// rather than leaving to the library, because the library records it and asserts
// nothing. The names and the checks behind them live in package stringformat and
// nowhere else; this file only decides where in a schema a name is judged and
// where a value is checked against it.

// librarySourceKey is the extension keyword a cross-document reference expansion
// sets on every position it contributes, naming the SchemaLibrary entry the
// position came from.
const librarySourceKey = "x-aggrega-source"

// refuseUnsupportedFormats reports the first format keyword, in document order,
// that names a format outside the supported set, anywhere in schema: every
// sub-schema position is visited, whether or not any instance position reaches it,
// because a name that is refused only when a value arrives is a name that loads
// and reads as a constraint until then.
//
// skipContributed passes over every position a cross-document reference expansion
// contributed, and everything beneath it. It is for a reader judging what one
// document authors itself: a contributed position is judged in the document that
// authors it, so judging it again here would report one fault twice.
func refuseUnsupportedFormats(schema *jsonschema.Schema, skipContributed bool) error {
	pointer, definition, name, found := firstUnsupportedFormat(schema, "", "", skipContributed)
	if !found {
		return nil
	}
	return &AdapterError{
		Path:    instancePathFromPointer(pointer),
		Keyword: "format",
		Message: nameViolatedDefinition(stringformat.CheckName(name).Error(), definition),
		Err:     ErrSchemaResolve(),
	}
}

// RefuseUnsupportedAuthoredFormats reports the first format keyword the compiled
// schema document authors itself that names a format outside the supported set,
// or nil when every name it authors is supported. A position a cross-document
// reference expansion contributed from another SchemaLibrary entry is passed over:
// that entry is judged where it is authored.
//
// It is the same judgement [New] makes of every schema it is handed, narrowed to
// what one document authors, for a caller that holds a library entry and wants
// its fault named at the entry rather than at every document that references it.
func RefuseUnsupportedAuthoredFormats(schema map[string]any) error {
	raw, err := json.Marshal(schema)
	if err != nil {
		return err
	}
	decoded := &jsonschema.Schema{}
	if err := json.Unmarshal(raw, decoded); err != nil {
		return unreadableSchemaError(err)
	}
	return refuseUnsupportedFormats(decoded, true)
}

// firstUnsupportedFormat walks s depth-first in a fixed order — the schema's
// fields in declaration order, a map's keys sorted, a list by index — and returns
// the first position naming an unsupported format, with the reusable definition
// it sits inside, if any.
func firstUnsupportedFormat(s *jsonschema.Schema, pointer, definition string, skipContributed bool) (string, string, string, bool) {
	if s == nil {
		return "", "", "", false
	}
	if skipContributed && s.Extra != nil {
		if _, contributed := s.Extra[librarySourceKey]; contributed {
			return "", "", "", false
		}
	}
	if s.Format != "" && !stringformat.Supported(s.Format) {
		return pointer, definition, s.Format, true
	}
	for _, child := range subSchemas(s) {
		inside := definition
		if child.definition != "" {
			inside = child.definition
		}
		if p, d, n, ok := firstUnsupportedFormat(child.schema, pointer+"/"+child.segment, inside, skipContributed); ok {
			return p, d, n, true
		}
	}
	return "", "", "", false
}

// subSchema is one sub-schema position of a schema node: the pointer segment that
// reaches it, the node itself, and the definition name when the position is a
// reusable definition.
type subSchema struct {
	segment    string
	schema     *jsonschema.Schema
	definition string
}

var (
	schemaPtrType   = reflect.TypeOf((*jsonschema.Schema)(nil))
	schemaSliceType = reflect.TypeOf([]*jsonschema.Schema(nil))
	schemaMapType   = reflect.TypeOf(map[string]*jsonschema.Schema(nil))
)

// subSchemas lists every sub-schema position of s, in a fixed order.
//
// It reads the schema type's fields by their types rather than naming them, so a
// sub-schema keyword the library adds is visited without an edit here — the
// direction that matters, since a keyword this missed would carry names nothing
// judged. A field's pointer segment is its keyword as the schema document spells
// it.
func subSchemas(s *jsonschema.Schema) []subSchema {
	var out []subSchema
	v := reflect.ValueOf(s).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		keyword := schemaKeyword(field)
		value := v.Field(i)
		switch field.Type {
		case schemaPtrType:
			if child, _ := value.Interface().(*jsonschema.Schema); child != nil {
				out = append(out, subSchema{segment: keyword, schema: child})
			}
		case schemaSliceType:
			for index, child := range value.Interface().([]*jsonschema.Schema) {
				if child != nil {
					out = append(out, subSchema{segment: keyword + "/" + strconv.Itoa(index), schema: child})
				}
			}
		case schemaMapType:
			children := value.Interface().(map[string]*jsonschema.Schema)
			names := make([]string, 0, len(children))
			for name := range children {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				if children[name] == nil {
					continue
				}
				sub := subSchema{segment: keyword + "/" + escapePointerSegment(name), schema: children[name]}
				if keyword == "$defs" || keyword == "definitions" {
					sub.definition = name
				}
				out = append(out, sub)
			}
		}
	}
	return out
}

// schemaKeyword is the keyword a schema field is written under in a document.
// Three sub-schema fields are decoded by hand and carry no name of their own in
// their tag; they are named here after the keyword each is read from.
func schemaKeyword(field reflect.StructField) string {
	if name, _, _ := strings.Cut(field.Tag.Get("json"), ","); name != "" && name != "-" {
		return name
	}
	switch field.Name {
	case "Items", "ItemsArray":
		return "items"
	case "DependencySchemas":
		return "dependencies"
	}
	return field.Name
}

// escapePointerSegment escapes the two characters that mean something inside a
// pointer segment, escape character first.
func escapePointerSegment(name string) string {
	return strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1")
}

// formatFinding checks value against a position's format and returns the finding
// a failure produces, or false when the position declares no format, the value is
// not a string — a format judges strings, and passes a value of any other type,
// whose type is its type keyword's to judge — or the value conforms.
//
// A name the checkers do not support never reaches here: [New] refused the schema
// carrying it.
func formatFinding(format string, value any, path, definition string) (Finding, bool) {
	if format == "" {
		return Finding{}, false
	}
	s, ok := value.(string)
	if !ok {
		return Finding{}, false
	}
	err := stringformat.Check(format, s)
	if err == nil {
		return Finding{}, false
	}
	return Finding{
		FieldPath: path,
		Keyword:   "format",
		Message:   nameViolatedDefinition("value "+err.Error(), definition),
	}, true
}

// libraryRegions lists the sub-schemas the library evaluates whole at a position,
// on the walk's behalf, because the walk does not descend into them there: the
// schema a reference names where the reference could not be substituted for the
// position — it carries a constraint of its own beside it — and the then: branch
// of a when-gated position, which the reduced copy keeps and the library applies.
//
// The library checks everything in them except format, so each is swept for
// format (see sweepFormats). The else: branch of a when-gated position is not
// listed: it applies only when the predicate is false, which the walk does not
// know, and the predicate pass sweeps it when it applies.
func (a *googleAdapter) libraryRegions(s *jsonschema.Schema) []*jsonschema.Schema {
	if s == nil {
		return nil
	}
	var out []*jsonschema.Schema
	if s.Ref != "" {
		if target := a.referenceTarget(s.Ref, s.Defs); target != nil {
			out = append(out, target)
		}
	}
	if carriesWhenPredicate(s) && s.Then != nil && isTrueSchema(s.If) {
		out = append(out, s.Then)
	}
	return out
}

// isTrueSchema reports whether s is the schema every value satisfies, which is
// how `true` decodes in a schema position.
func isTrueSchema(s *jsonschema.Schema) bool {
	return s != nil && reflect.ValueOf(*s).IsZero()
}

// referenceTarget resolves a reference into the document for the format sweep: a
// definition by name, against the definitions in scope at the position and then the
// document's own, or any other in-document pointer against the document root. A
// reference that names nothing this can reach resolves to nil, and the sweep does
// not follow it.
func (a *googleAdapter) referenceTarget(ref string, inScope map[string]*jsonschema.Schema) *jsonschema.Schema {
	for _, prefix := range []string{localDefsPrefix, "#/definitions/"} {
		if name, ok := strings.CutPrefix(ref, prefix); ok && name != "" && !strings.Contains(name, "/") {
			if target := inScope[name]; target != nil {
				return target
			}
		}
	}
	pointer, ok := strings.CutPrefix(ref, "#")
	if !ok || a.root == nil {
		return nil
	}
	return schemaAtPointer(a.root, pointer)
}

// schemaAtPointer follows a JSON Pointer through a schema document, reading each
// step as one of the document's sub-schema keywords.
func schemaAtPointer(root *jsonschema.Schema, pointer string) *jsonschema.Schema {
	if pointer == "" {
		return root
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil
	}
	current := root
	segments := strings.Split(pointer[1:], "/")
	for i := 0; i < len(segments); i++ {
		keyword := segments[i]
		var next *jsonschema.Schema
		for _, child := range subSchemas(current) {
			step, rest, _ := strings.Cut(child.segment, "/")
			if step != keyword {
				continue
			}
			if rest == "" {
				next = child.schema
				break
			}
			if i+1 < len(segments) && rest == segments[i+1] {
				next = child.schema
				i++
				break
			}
		}
		if next == nil {
			return nil
		}
		current = next
	}
	return current
}

// sweepKey identifies one visit of the format sweep: a schema node applied at an
// instance position, reached from beneath a declaration of sensitivity or not. A
// reference cycle that reaches the same node at the same position again would
// never end, so a second visit is skipped. The two readings of sensitivity are
// two visits, so a node reached first along a route that declares nothing is
// still reached along one that does.
type sweepKey struct {
	schema    *jsonschema.Schema
	path      string
	sensitive bool
}

// sweepFormats checks format across a region of the schema the library
// evaluates whole — see libraryRegions, the unresolved positions and the else:
// branch of a when-gated position — so a value there is held to its format as it
// is everywhere the walk reaches itself.
//
// It follows the applicators that apply unconditionally: declared, pattern and
// additional properties, array elements, allOf and in-document references. It does
// not descend into an applicator whose applying depends on the value — anyOf,
// oneOf, not, if/then/else, dependentSchemas, contains — nor into a node carrying a
// when: predicate: deciding which branch applies there takes the full evaluation
// the library performs, and applying a branch that does not apply would refuse a
// value nothing constrains. So within such a region the sweep can under-report,
// and it never invents a failure.
//
// sensitive reports that a node the sweep has passed through on the way to s
// declares its value sensitive. A region is checked whole, so no position inside
// it was built to carry that declaration, and the sweep carries it down itself:
// a format failure beneath such a node is composed from the schema rather than
// from the value wherever the validator withholds sensitive values.
func (a *googleAdapter) sweepFormats(s *jsonschema.Schema, instance any, path, definition string, rpt *Report, seen map[sweepKey]bool, sensitive bool) {
	if s == nil || carriesWhenPredicate(s) {
		return
	}
	sensitive = sensitive || DeclaresSensitive(s.Extra)
	key := sweepKey{schema: s, path: path, sensitive: sensitive}
	if seen[key] {
		return
	}
	seen[key] = true

	if f, failed := formatFinding(s.Format, instance, path, definition); failed {
		if sensitive && a.withholdSensitive {
			f = withheldFinding(f, s, definition)
		}
		appendUniqueFinding(rpt, f)
	}
	if s.Ref != "" {
		target := a.referenceTarget(s.Ref, s.Defs)
		inside := definition
		if name, ok := strings.CutPrefix(s.Ref, localDefsPrefix); ok && !strings.Contains(name, "/") {
			inside = name
		}
		a.sweepFormats(target, instance, path, inside, rpt, seen, sensitive)
	}
	for _, branch := range s.AllOf {
		a.sweepFormats(branch, instance, path, definition, rpt, seen, sensitive)
	}

	switch value := instance.(type) {
	case map[string]any:
		patterns := make([]string, 0, len(s.PatternProperties))
		for p := range s.PatternProperties {
			patterns = append(patterns, p)
		}
		sort.Strings(patterns)
		for _, k := range sortedKeys(value) {
			sub := childPath(path, k)
			declared, isDeclared := s.Properties[k]
			if isDeclared {
				a.sweepFormats(declared, value[k], sub, definition, rpt, seen, sensitive)
			}
			matched := false
			for _, p := range patterns {
				// The library compiled every pattern when the schema was resolved,
				// so a pattern that does not compile never reaches here; the error
				// arm only keeps an unreadable one from being read as a match.
				if re, err := regexp.Compile(p); err == nil && re.MatchString(k) {
					matched = true
					a.sweepFormats(s.PatternProperties[p], value[k], sub, definition, rpt, seen, sensitive)
				}
			}
			if !isDeclared && !matched && s.AdditionalProperties != nil && !isClosedForm(s.AdditionalProperties) {
				a.sweepFormats(s.AdditionalProperties, value[k], sub, definition, rpt, seen, sensitive)
			}
		}
	case []any:
		for index, element := range value {
			at := elementPath(path, index)
			switch {
			case index < len(s.PrefixItems):
				a.sweepFormats(s.PrefixItems[index], element, at, definition, rpt, seen, sensitive)
			case s.Items != nil:
				a.sweepFormats(s.Items, element, at, definition, rpt, seen, sensitive)
			case index < len(s.ItemsArray):
				a.sweepFormats(s.ItemsArray[index], element, at, definition, rpt, seen, sensitive)
			case len(s.ItemsArray) > 0 && s.AdditionalItems != nil:
				a.sweepFormats(s.AdditionalItems, element, at, definition, rpt, seen, sensitive)
			}
		}
	}
}

// sweepSchema sweeps one region the library evaluates whole.
//
// The region starts out not sensitive: whether the position it is swept at holds
// a sensitive value is that position's to decide, and it does so over everything
// the position reports, the sweep's findings included.
func (a *googleAdapter) sweepSchema(s *jsonschema.Schema, instance any, path, definition string, rpt *Report) {
	start := len(rpt.Findings)
	a.sweepFormats(s, instance, path, definition, rpt, map[sweepKey]bool{}, false)
	withholdBesideWithheld(rpt.Findings[start:])
}

// sweepRegions sweeps every region the library evaluates whole at one position.
func (a *googleAdapter) sweepRegions(regions []*jsonschema.Schema, instance any, path, definition string, rpt *Report) {
	if len(regions) == 0 {
		return
	}
	start := len(rpt.Findings)
	seen := map[sweepKey]bool{}
	for _, region := range regions {
		a.sweepFormats(region, instance, path, definition, rpt, seen, false)
	}
	withholdBesideWithheld(rpt.Findings[start:])
}

// appendUniqueFinding adds f unless the report already carries the same finding.
// Two schema positions applying one format to one value — a property declared both
// beside a reference and in the schema it names — fail the same way, and one
// report of that is the whole of what an author needs.
func appendUniqueFinding(rpt *Report, f Finding) {
	for _, existing := range rpt.Findings {
		if existing.FieldPath == f.FieldPath && existing.Keyword == f.Keyword && existing.Message == f.Message {
			return
		}
	}
	rpt.append(f)
}

// checkFormatAt appends the finding a position's own format produces for its
// value, and sweeps the regions the library evaluates whole there.
func (a *googleAdapter) checkFormatAt(path string, instance any, rpt *Report, definition string) {
	if f, failed := formatFinding(a.formats[path], instance, path, definition); failed {
		appendUniqueFinding(rpt, f)
	}
	a.sweepRegions(a.regions[path], instance, path, definition, rpt)
}

// checkBranchFormat is checkFormatAt for a combinator or element branch.
func (a *googleAdapter) checkBranchFormat(bi *branchIndex, instance any, path string, rpt *Report) {
	if f, failed := formatFinding(bi.format, instance, path, bi.definition); failed {
		appendUniqueFinding(rpt, f)
	}
	a.sweepRegions(bi.regions, instance, path, bi.definition, rpt)
}
