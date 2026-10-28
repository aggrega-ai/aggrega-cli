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
	"errors"
	"reflect"
	"regexp"
	"strconv"
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
//
// A struct the decoder spells out in full rather than by a name — a header read
// into an anonymous struct — is a map to the author: the decoder fills it from a
// map's keys, and its spelling is the program's field list, which no author wrote.
func OfGoType(goType string) string {
	t := strings.TrimLeft(goType, "*")
	switch {
	case strings.HasPrefix(t, "[]"):
		return "a list of " + plural(t[2:])
	case strings.HasPrefix(t, "map["), strings.Contains(t, "OrderedMap["), isStructLiteral(t):
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
	case strings.HasPrefix(t, "map["), strings.Contains(t, "OrderedMap["), isStructLiteral(t):
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

// isStructLiteral reports whether a type is spelled as a struct written out in
// full, the way a decoder names an anonymous struct.
func isStructLiteral(t string) bool {
	return strings.HasPrefix(t, "struct {") || strings.HasPrefix(t, "struct{")
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
	return describeYAMLDecodeError(err, nil)
}

// DescribeYAMLDecodeErrorIn is DescribeYAMLDecodeError for a decode of a whole
// document whose bytes the caller holds. Each entry also names the field the
// value was written under, as a path of keys from the top of the document, so
// the author reads which field took the wrong shape as well as the line it is
// on: `line 4 (metadata): a list is written where a map is expected`.
//
// The decoder reports a line and the tag of what it found there, not a field, so
// the field is found in the document: the one value on that line written in that
// shape. Where the document holds no such value, or more than one — two values of
// one shape written on one line — the entry names the line alone rather than
// guess between them.
//
// It also rewords the decoder's refusal of a key written as a list or a map where
// the decode reads keys as text, which the decoder spells as the Go value it built
// from the key and with no line: the key is found in the document and named by its
// line, the field holding it and the shape it was written in —
// `line 6 (services.web.depends_on): reading a key: a list is written where text is
// expected`. Where the document shows no such key, that refusal is returned as it is.
func DescribeYAMLDecodeErrorIn(err error, document []byte) error {
	_, isTypeError := err.(*yaml.TypeError)
	nonTextKey := err != nil && strings.HasPrefix(err.Error(), invalidMapKeyPrefix)
	if !isTypeError && !nonTextKey {
		return err
	}
	var root yaml.Node
	if yaml.Unmarshal(document, &root) != nil {
		return describeYAMLDecodeError(err, nil)
	}
	if nonTextKey {
		if message := describeNonTextKey(&root, ""); message != "" {
			return &describedDecodeError{message: message, cause: err}
		}
		return err
	}
	fields := map[fieldSite][]string{}
	collectFieldSites(&root, "", fields)
	return describeYAMLDecodeError(err, func(line, tag string) string {
		paths := fields[fieldSite{line: line, tag: tag}]
		if len(paths) != 1 {
			return ""
		}
		return paths[0]
	})
}

// invalidMapKeyPrefix opens the decoder's refusal of a key it cannot read as text
// where it builds a map keyed by text.
const invalidMapKeyPrefix = "yaml: invalid map key: "

// describeNonTextKey names the first key in the document, in document order,
// written as a list or a map: its line, the field holding it, and its shape. It
// returns "" when the document holds none.
func describeNonTextKey(n *yaml.Node, path string) string {
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			if m := describeNonTextKey(c, path); m != "" {
				return m
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i]
			if key.Kind == yaml.SequenceNode || key.Kind == yaml.MappingNode {
				where := "line " + strconv.Itoa(key.Line)
				if path != "" {
					where += " (" + path + ")"
				}
				return where + ": reading a key: " + WrittenWhereExpected(OfYAMLNode(key), Text.Phrase())
			}
			child := key.Value
			if path != "" {
				child = path + "." + child
			}
			if m := describeNonTextKey(n.Content[i+1], child); m != "" {
				return m
			}
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			if m := describeNonTextKey(c, path+"["+strconv.Itoa(i)+"]"); m != "" {
				return m
			}
		}
	}
	return ""
}

// fieldSite is where a value sits as the decoder reports it: its line and the
// tag its shape resolved to.
type fieldSite struct {
	line string
	tag  string
}

// collectFieldSites records, for every value under a key or in a list, the path
// it was written under, keyed by its line and tag. A path is the keys from the
// top of the document joined with dots, with a list item's position in brackets.
func collectFieldSites(n *yaml.Node, path string, out map[fieldSite][]string) {
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			collectFieldSites(c, path, out)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			child := n.Content[i].Value
			if path != "" {
				child = path + "." + child
			}
			recordFieldSite(n.Content[i+1], child, out)
			collectFieldSites(n.Content[i+1], child, out)
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			child := path + "[" + strconv.Itoa(i) + "]"
			recordFieldSite(c, child, out)
			collectFieldSites(c, child, out)
		}
	}
}

// recordFieldSite records one value's path under its line and tag. An alias is
// passed over: the decoder reports the line of the content it points at, which
// is recorded where that content is written.
func recordFieldSite(n *yaml.Node, path string, out map[fieldSite][]string) {
	if n.Kind == yaml.AliasNode {
		return
	}
	site := fieldSite{line: strconv.Itoa(n.Line), tag: n.ShortTag()}
	out[site] = append(out[site], path)
}

// describeYAMLDecodeError is the one rewording both describers share. fieldAt,
// when it is set, names the field a value on a line was written under, or ""
// where it cannot say.
func describeYAMLDecodeError(err error, fieldAt func(line, tag string) string) error {
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
		where := "line " + m[1]
		if fieldAt != nil {
			if field := fieldAt(m[1], m[2]); field != "" {
				where += " (" + field + ")"
			}
		}
		entries[i] = where + ": " + WrittenWhereExpected(ofYAMLTag(m[2])+m[3], OfGoType(m[4]))
	}
	return &describedDecodeError{message: strings.Join(entries, "; "), cause: te}
}

// DescribeJSONDecodeError rewords a JSON decoder's report of a value it could not
// place — `json: cannot unmarshal number into Go struct field params.kind of type
// string` — as the field the value was sent under, the shape it was written in
// and the shape the field takes: `"kind": a number is written where text is
// expected`. Every other error is returned as it is.
//
// The decoder names the Go struct and the Go type it was filling, which say
// nothing to whoever wrote the JSON. The field it names is kept, as the path of
// keys the value was sent under.
//
// The original error is what the result unwraps to.
func DescribeJSONDecodeError(err error) error {
	var te *json.UnmarshalTypeError
	if !errors.As(err, &te) || te == nil {
		return err
	}
	message := WrittenWhereExpected(ofJSONValue(te.Value), ofReflectType(te.Type))
	if te.Field != "" {
		message = strconv.Quote(te.Field) + ": " + message
	}
	return &describedDecodeError{message: message, cause: err}
}

// ofJSONValue names the shape a JSON decoder reports having found, from the word
// it reports it by.
func ofJSONValue(value string) string {
	switch {
	case value == "string":
		return Text.Phrase()
	case value == "number", strings.HasPrefix(value, "number "):
		return Number.Phrase()
	case value == "bool":
		return Bool.Phrase()
	case value == "array":
		return List.Phrase()
	case value == "object":
		return Map.Phrase()
	}
	return Other.Phrase()
}

// ofReflectType names the shape a field of type t takes. A type of its own over a
// predeclared one takes what its underlying type takes, and a struct is filled
// from a JSON object, so it takes a map.
func ofReflectType(t reflect.Type) string {
	if t == nil {
		return "a value"
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		return "a list of " + plural(t.Elem().String())
	case reflect.Map, reflect.Struct:
		return Map.Phrase()
	case reflect.Interface:
		return "a value"
	}
	if k := KindOf(reflect.Zero(t)); k != Other {
		return k.Phrase()
	}
	return "a value"
}

// describedDecodeError carries the reworded report and the decoder's own error
// beneath it.
type describedDecodeError struct {
	message string
	cause   error
}

func (e *describedDecodeError) Error() string { return e.message }
func (e *describedDecodeError) Unwrap() error { return e.cause }
