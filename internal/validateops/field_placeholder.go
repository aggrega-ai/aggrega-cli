// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validateops

import (
	"math"
	"strings"
	"unicode/utf8"
)

// placeholderString is the value a build of one fills a required string with
// where the field's schema asks nothing more of it than a string.
const placeholderString = "example"

// maxPlaceholderSize bounds how many elements a placeholder array is built with
// and how many characters a placeholder string is lengthened to. A minItems or
// minLength above it is honoured only up to it, so a schema demanding an absurd
// size costs a finding naming the constraint rather than the process's memory.
const maxPlaceholderSize = 1024

// formatExamples holds a value of each supported string format, for a required
// field that declares one. Each is held to that format's own check by a test
// walking every supported format name, so a format added to the supported set
// without an example here fails that test rather than being filled with a value
// its check refuses.
var formatExamples = map[string]string{
	"email":     "user@example.com",
	"uri":       "https://example.com",
	"hostname":  "example.com",
	"ipv4":      "192.0.2.1",
	"ipv6":      "2001:db8::1",
	"uuid":      "00000000-0000-0000-0000-000000000000",
	"date-time": "2000-01-01T00:00:00Z",
	"date":      "2000-01-01",
}

// placeholderForField returns a value one compiled schema field accepts, for the
// build of one to fill a required field nobody else supplies there.
//
// The value honours what the field itself declares, because a fill the field's
// own schema refuses fails a Recipe that a build setting the field accepts:
//
//   - const, then the first member of enum, whatever the type;
//   - an integer or a number at its lower bound — minimum, or the next value past
//     exclusiveMinimum — rounded up to its multipleOf; with no lower bound, zero,
//     unless maximum or exclusiveMaximum excludes zero, and then that bound or
//     the next value below it, rounded down to its multipleOf;
//   - a string in the field's format where it declares one, the semantic version
//     1.0.0 where its pattern behaves as that grammar, and otherwise "example",
//     lengthened to minLength or cut to maxLength;
//   - an array of minItems elements, each the placeholder of its items schema;
//   - an object holding each property it requires, each the property's default
//     where it declares one and its own placeholder otherwise.
//
// A type written as a list is read as its first member. What the placeholder
// does not satisfy is left as the schema states it rather than guessed at: a
// pattern other than the semantic-version grammar, uniqueItems over more than one
// element, contains and prefixItems, a validate: predicate, a when: condition,
// a composition (allOf, anyOf, oneOf), and an in-document $ref, which is not
// followed. A field relying on one of them may still refuse its placeholder, and
// the check then reports that refusal in the schema's own words.
//
// The value is a function of the schema alone, so the same schema always yields
// the same placeholder.
func placeholderForField(field map[string]any) any {
	return placeholderAt(field, 0)
}

// placeholderAt is placeholderForField at a nesting depth, bounded so a schema
// nested past any authored depth ends the fill rather than the process.
func placeholderAt(field map[string]any, depth int) any {
	if field == nil || depth > maxSchemaDepth {
		return placeholderString
	}
	if value, ok := field["const"]; ok {
		return value
	}
	if members := asSlice(field["enum"]); len(members) > 0 {
		return members[0]
	}
	switch declaredType(field) {
	case "integer":
		return numberPlaceholder(field, true)
	case "number":
		return numberPlaceholder(field, false)
	case "boolean":
		return false
	case "null":
		return nil
	case "object":
		return objectPlaceholder(field, depth)
	case "array":
		return arrayPlaceholder(field, depth)
	default:
		return stringPlaceholder(field)
	}
}

// declaredType reads a field's declared type: the name it declares, or the first
// name of a list of them; failing that, an object where it declares properties
// and an array where it declares items; and the empty string otherwise.
func declaredType(field map[string]any) string {
	switch t := field["type"].(type) {
	case string:
		return t
	case []any:
		for _, member := range t {
			if name, ok := member.(string); ok {
				return name
			}
		}
	case []string:
		if len(t) > 0 {
			return t[0]
		}
	}
	if _, ok := field["properties"]; ok {
		return "object"
	}
	if _, ok := field["items"]; ok {
		return "array"
	}
	return ""
}

// numberPlaceholder is the placeholder of an integer or number field: its lower
// bound where it declares one, zero where it declares none and no upper bound
// excludes zero, and that upper bound or the next value below it otherwise —
// each rounded towards the bound that chose it, to a whole number for an integer
// and to a multiple of multipleOf where the field declares one.
func numberPlaceholder(field map[string]any, integer bool) any {
	value, lower := 0.0, false
	if m, ok := schemaNumber(field["minimum"]); ok {
		value, lower = m, true
	}
	if x, ok := schemaNumber(field["exclusiveMinimum"]); ok {
		next := x + 1
		if integer {
			next = math.Floor(x) + 1
		}
		if !lower || next > value {
			value, lower = next, true
		}
	}
	round := math.Ceil
	if !lower {
		if m, ok := schemaNumber(field["maximum"]); ok && value > m {
			value, round = m, math.Floor
		}
		if x, ok := schemaNumber(field["exclusiveMaximum"]); ok && value >= x {
			value, round = x-1, math.Floor
			if integer {
				value = math.Ceil(x) - 1
			}
		}
	}
	if integer {
		value = round(value)
	}
	if step, ok := schemaNumber(field["multipleOf"]); ok && step > 0 {
		value = round(value/step) * step
	}
	return plainNumber(value)
}

// plainNumber returns a whole value as an int and any other as a float64, so a
// placeholder for an integer field is written as an integer.
func plainNumber(value float64) any {
	if value == math.Trunc(value) && math.Abs(value) <= 1<<53 {
		return int(value)
	}
	return value
}

// schemaNumber reads a numeric keyword's value, tolerating the integer and
// floating-point shapes a compiled schema carries.
func schemaNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float64:
		return n, true
	case float32:
		return float64(n), true
	default:
		return 0, false
	}
}

// sizeBound reads a length or count keyword — minItems, minLength, maxLength —
// as a whole number no larger than maxPlaceholderSize, reporting whether the
// field declares it at all.
func sizeBound(v any) (int, bool) {
	n, ok := schemaNumber(v)
	if !ok || n < 0 {
		return 0, false
	}
	return int(math.Min(n, maxPlaceholderSize)), true
}

// stringPlaceholder is the placeholder of a string field: a value in its format,
// the semantic version where its pattern behaves as that grammar, or
// placeholderString brought within its length bounds.
func stringPlaceholder(field map[string]any) string {
	if format, _ := field["format"].(string); format != "" {
		if example, ok := formatExamples[format]; ok {
			return example
		}
	}
	if pattern, _ := field["pattern"].(string); pattern != "" && isSemanticVersionPattern(pattern) {
		return semanticVersionExample
	}
	value := placeholderString
	if n, ok := sizeBound(field["minLength"]); ok && n > utf8.RuneCountInString(value) {
		value += strings.Repeat("x", n-utf8.RuneCountInString(value))
	}
	// placeholderString and its padding are ASCII, so a byte offset is a
	// character offset here.
	if n, ok := sizeBound(field["maxLength"]); ok && n < utf8.RuneCountInString(value) {
		value = value[:n]
	}
	return value
}

// arrayPlaceholder is the placeholder of an array field: minItems elements, each
// the placeholder of its items schema, and none where it declares no minItems.
func arrayPlaceholder(field map[string]any, depth int) []any {
	n, _ := sizeBound(field["minItems"])
	items, _ := field["items"].(map[string]any)
	out := make([]any, 0, n)
	for range n {
		out = append(out, placeholderAt(items, depth+1))
	}
	return out
}

// objectPlaceholder is the placeholder of an object field: each property it
// requires, set to the property's default where it declares one and to its own
// placeholder otherwise.
//
// A required property keeps its default written out here, where a top-level
// field with a default is left for the resolver to materialize, because the
// resolver supplies neither of the two cases an object reaches: it materializes
// no default inside an array's element, and it keeps an authored object that is
// empty as empty, so an object whose required properties all carry defaults would
// otherwise be filled with nothing.
func objectPlaceholder(field map[string]any, depth int) map[string]any {
	out := map[string]any{}
	props, _ := field["properties"].(map[string]any)
	for _, name := range toStringSlice(field["required"]) {
		if name == "" {
			continue
		}
		child, _ := props[name].(map[string]any)
		if value, ok := child["default"]; ok {
			out[name] = value
			continue
		}
		out[name] = placeholderAt(child, depth+1)
	}
	return out
}
