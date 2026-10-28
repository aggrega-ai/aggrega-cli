// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package authoredshape names the shape of a value in the words an author writes
// values in — a number, text, a true/false value, a list, a map, no value — so a
// message about something an author wrote describes it the way they wrote it,
// rather than by the Go type the program happened to decode it into.
//
// It is the one home of those words. A message that named a value's shape in a
// spelling of its own would drift from the others the first time one of them was
// reworded, and a reader would then meet two names for one shape.
//
// Three inputs reach it, because a value's shape is known in three forms: a
// decoded value, a YAML node the decoder had not yet placed, and the spelling of
// the Go type a decoder was asked to place a node into. Each is answered in the
// same words.
package authoredshape

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
)

// Kind is the shape of a value as an author writes it.
type Kind int

// The shapes, in the words a message names them by.
const (
	// Absent is a value that is not there: null, or nothing at all.
	Absent Kind = iota
	// Number is an integer or a decimal.
	Number
	// Text is a string.
	Text
	// Bool is true or false.
	Bool
	// List is a sequence.
	List
	// Map is a mapping from keys to values.
	Map
	// Other is a value of none of the shapes above.
	Other
)

// Phrase is the shape as a message names it, with its article.
func (k Kind) Phrase() string {
	switch k {
	case Absent:
		return "no value"
	case Number:
		return "a number"
	case Text:
		return "text"
	case Bool:
		return "a true/false value"
	case List:
		return "a list"
	case Map:
		return "a map"
	}
	return "a value that is not a number, text, a true/false value, a list or a map"
}

var jsonNumberType = reflect.TypeFor[json.Number]()

// KindOf classifies a value that has already been dereferenced: no pointer and no
// interface stands around it. An invalid value is Absent.
func KindOf(v reflect.Value) Kind {
	if !v.IsValid() {
		return Absent
	}
	if v.Type() == jsonNumberType {
		return Number
	}
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return Number
	case reflect.String:
		return Text
	case reflect.Bool:
		return Bool
	case reflect.Slice, reflect.Array:
		return List
	case reflect.Map:
		return Map
	}
	return Other
}

// Of names the shape of a decoded value, following any pointer or interface
// around it; a nil anywhere on the way is no value.
func Of(v any) string {
	rv := reflect.ValueOf(v)
	for rv.IsValid() && (rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface) {
		if rv.IsNil() {
			return Absent.Phrase()
		}
		rv = rv.Elem()
	}
	return KindOf(rv).Phrase()
}

// OfYAMLNode names the shape a YAML node was written in: a mapping, a sequence,
// or a scalar by the tag it resolves to. An alias is named by what it points at.
func OfYAMLNode(n *yaml.Node) string {
	if n == nil {
		return Absent.Phrase()
	}
	switch n.Kind {
	case yaml.MappingNode:
		return Map.Phrase()
	case yaml.SequenceNode:
		return List.Phrase()
	case yaml.AliasNode:
		return OfYAMLNode(n.Alias)
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return Absent.Phrase()
		}
		return OfYAMLNode(n.Content[0])
	}
	return ofYAMLTag(n.ShortTag())
}

// ofYAMLTag names the shape a YAML tag denotes. A tag outside the core schema is
// named as written, since nothing about it says what shape it carries.
func ofYAMLTag(tag string) string {
	switch tag {
	case "!!map":
		return Map.Phrase()
	case "!!seq":
		return List.Phrase()
	case "!!str", "!!binary", "!!timestamp":
		return Text.Phrase()
	case "!!int", "!!float":
		return Number.Phrase()
	case "!!bool":
		return Bool.Phrase()
	case "!!null":
		return Absent.Phrase()
	}
	return "a value tagged " + tag
}

// OfGoType names the shape a decoder was asked to place a value into, from the
// spelling of that Go type as the decoder reports it. A list is named with what
// it holds and a named type by its own name in words, so a field expecting
// []VolumeMount reads as "a list of volume mounts" — the name the author wrote
// the field under, in the singular or the plural.
func OfGoType(goType string) string {
	t := strings.TrimLeft(goType, "*")
	switch {
	case strings.HasPrefix(t, "[]"):
		return "a list of " + plural(t[2:])
	case strings.HasPrefix(t, "map["), strings.Contains(t, "OrderedMap["):
		return Map.Phrase()
	}
	if k, ok := builtinKind(t); ok {
		if k == Other {
			return "a value"
		}
		return k.Phrase()
	}
	words := typeWords(t)
	return article(words) + " " + words
}

// plural names what a list holds, in the plural.
func plural(element string) string {
	t := strings.TrimLeft(element, "*")
	switch {
	case strings.HasPrefix(t, "[]"):
		return "lists"
	case strings.HasPrefix(t, "map["), strings.Contains(t, "OrderedMap["):
		return "maps"
	}
	if k, ok := builtinKind(t); ok {
		switch k {
		case Number:
			return "numbers"
		case Text:
			return "text values"
		case Bool:
			return "true/false values"
		}
		return "values"
	}
	words := typeWords(t)
	switch {
	case strings.HasSuffix(words, "s"), strings.HasSuffix(words, "x"),
		strings.HasSuffix(words, "ch"), strings.HasSuffix(words, "sh"):
		return words + "es"
	case strings.HasSuffix(words, "y") && len(words) > 1 && !strings.ContainsRune("aeiou", rune(words[len(words)-2])):
		return words[:len(words)-1] + "ies"
	}
	return words + "s"
}

// builtinKind answers for Go's predeclared types and the empty interface.
func builtinKind(t string) (Kind, bool) {
	switch t {
	case "string":
		return Text, true
	case "bool":
		return Bool, true
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
		"float32", "float64", "json.Number":
		return Number, true
	case "interface {}", "any":
		return Other, true
	}
	return Other, false
}

// typeWords spells a named type's own name as lower-case words: the package
// qualifier and any type arguments dropped, the name split where its case changes.
func typeWords(t string) string {
	if i := strings.IndexByte(t, '['); i >= 0 {
		t = t[:i]
	}
	if i := strings.LastIndexByte(t, '.'); i >= 0 {
		t = t[i+1:]
	}
	var b strings.Builder
	runes := []rune(t)
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) {
			prevLower := unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1])
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if prevLower || (unicode.IsUpper(runes[i-1]) && nextLower) {
				b.WriteByte(' ')
			}
		}
		b.WriteRune(unicode.ToLower(r))
	}
	if b.Len() == 0 {
		return "value"
	}
	return b.String()
}

// article is the indefinite article a phrase opens with.
func article(words string) string {
	if words != "" && strings.ContainsRune("aeiou", rune(words[0])) {
		return "an"
	}
	return "a"
}

// WrittenWhereExpected is the one sentence a shape mismatch is reported in: what
// the author wrote, then what the position takes.
func WrittenWhereExpected(found, expected string) string {
	return found + " is written where " + expected + " is expected"
}

// decodeMismatch matches one entry a YAML decoder records for a value it could
// not place: the line, the tag it resolved, the value quoted between backticks
// where the decoder quotes one, and the Go type it was asked to place it into.
var decodeMismatch = regexp.MustCompile("^line ([0-9]+): cannot unmarshal (!!\\S+)( `[^`]*`)? into (.+)$")

// DescribeYAMLDecodeError rewords a YAML decoder's report of values it could not
// place, naming each by the shape it was written in and the shape its position
// takes, and returns every other error as it is.
//
// The decoder names the Go type it was asked to fill — a list of a struct, a map
// of an ordered map — which says nothing to the author of the document and
// everything about how this program stores it. What is kept is what the author can
// act on: the line, and the value itself where the decoder quotes one, between the
// same backticks, so a reader comparing two such reports by what lies outside the
// quotes compares them as before. An entry the decoder words some other way is
// kept as it was written.
//
// The original error is what the result unwraps to, so a caller asking whether a
// failure is a decode failure still finds it.
func DescribeYAMLDecodeError(err error) error {
	te, ok := err.(*yaml.TypeError)
	if !ok || te == nil {
		return err
	}
	entries := make([]string, len(te.Errors))
	for i, entry := range te.Errors {
		m := decodeMismatch.FindStringSubmatch(entry)
		if m == nil {
			entries[i] = entry
			continue
		}
		entries[i] = "line " + m[1] + ": " + WrittenWhereExpected(ofYAMLTag(m[2])+m[3], OfGoType(m[4]))
	}
	return &describedDecodeError{message: strings.Join(entries, "; "), cause: te}
}

// describedDecodeError carries the reworded report and the decoder's own error
// beneath it.
type describedDecodeError struct {
	message string
	cause   error
}

func (e *describedDecodeError) Error() string { return e.message }
func (e *describedDecodeError) Unwrap() error { return e.cause }
